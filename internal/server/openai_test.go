package server_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
)

func TestResponsesRewritesModelAndForwards(t *testing.T) {
	t.Parallel()
	caller := &fakeCaller{response: jsonResponse(http.StatusOK, `{"id":"resp_1","object":"response"}`)}
	handler := newTestHandler(t, caller)

	recorder := postJSON(handler, "/v1/responses",
		`{"model":"gpt-6-1-sol","input":"hi","reasoning":{"effort":"high"}}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", recorder.Code, recorder.Body)
	}
	if !strings.Contains(recorder.Body.String(), `"id":"resp_1"`) {
		t.Errorf("body = %s, want upstream body relayed", recorder.Body)
	}
	if caller.opts.Path != "/responses" {
		t.Errorf("path = %q, want /responses", caller.opts.Path)
	}
	var forwarded map[string]any
	if err := json.Unmarshal(caller.opts.Body, &forwarded); err != nil {
		t.Fatalf("unmarshal forwarded body: %v", err)
	}
	if forwarded["model"] != "gpt-6.1-sol" {
		t.Errorf("forwarded model = %v, want gpt-6.1-sol", forwarded["model"])
	}
	if _, ok := forwarded["reasoning"]; !ok {
		t.Error("forwarded body lost the reasoning field")
	}
	if got := caller.opts.Header.Get("Anthropic-Version"); got != "" {
		t.Errorf("Anthropic-Version = %q, want unset on an OpenAI route", got)
	}
}

func TestChatCompletionsForwards(t *testing.T) {
	t.Parallel()
	caller := &fakeCaller{response: jsonResponse(http.StatusOK, `{"id":"chatcmpl_1"}`)}
	handler := newTestHandler(t, caller)

	recorder := postJSON(handler, "/v1/chat/completions",
		`{"model":"kimi-k3","messages":[{"role":"user","content":"hi"}]}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", recorder.Code, recorder.Body)
	}
	if caller.opts.Path != "/chat/completions" {
		t.Errorf("path = %q, want /chat/completions", caller.opts.Path)
	}
}

func TestOpenAIRoutesGateOnSupportedEndpoints(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		path  string
		model string
		want  int
	}{
		{name: "anthropic model on responses", path: "/v1/responses", model: "claude-sonnet-4.5", want: 400},
		{name: "chat-only model on responses", path: "/v1/responses", model: "gpt-5-mini", want: 400},
		{name: "chat-only model on chat", path: "/v1/chat/completions", model: "gpt-5-mini", want: 200},
		{name: "responses model on chat", path: "/v1/chat/completions", model: "gpt-6.1-sol", want: 400},
		{name: "empty endpoint list on responses", path: "/v1/responses", model: "gpt-4o", want: 200},
		{name: "empty endpoint list on chat", path: "/v1/chat/completions", model: "gpt-4o", want: 200},
		{name: "unknown model passes through", path: "/v1/responses", model: "gpt-9-nova", want: 200},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			caller := &fakeCaller{response: jsonResponse(http.StatusOK, `{}`)}
			handler := newTestHandler(t, caller)

			recorder := postJSON(handler, tt.path, `{"model":"`+tt.model+`","input":"hi"}`)

			if recorder.Code != tt.want {
				t.Fatalf("status = %d, want %d; body: %s", recorder.Code, tt.want, recorder.Body)
			}
			if tt.want != http.StatusBadRequest {
				return
			}
			var payload struct {
				Error struct {
					Type  string `json:"type"`
					Param string `json:"param"`
				} `json:"error"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
				t.Fatalf("unmarshal error body: %v", err)
			}
			if payload.Error.Type != "invalid_request_error" || payload.Error.Param != "model" {
				t.Errorf("error = %+v, want invalid_request_error on param model", payload.Error)
			}
			if caller.opts.Path != "" {
				t.Errorf("rejected request reached upstream at %q", caller.opts.Path)
			}
		})
	}
}

func TestOpenAIPrefixedAliases(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path     string
		upstream string
	}{
		{path: "/openai/v1/responses", upstream: "/responses"},
		{path: "/openai/v1/chat/completions", upstream: "/chat/completions"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()
			caller := &fakeCaller{response: jsonResponse(http.StatusOK, `{}`)}
			handler := newTestHandler(t, caller)

			recorder := postJSON(handler, tt.path, `{"model":"gpt-4o","input":"hi"}`)

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", recorder.Code, recorder.Body)
			}
			if caller.opts.Path != tt.upstream {
				t.Errorf("path = %q, want %s", caller.opts.Path, tt.upstream)
			}
		})
	}
}

func TestOpenAIRequiresModel(t *testing.T) {
	t.Parallel()
	handler := newTestHandler(t, &fakeCaller{})

	recorder := postJSON(handler, "/v1/responses", `{"input":"hi"}`)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), `"param":"model"`) {
		t.Errorf("body = %s, want OpenAI error naming the model param", recorder.Body)
	}
}

func TestOpenAIUpstreamFailure(t *testing.T) {
	t.Parallel()
	caller := &fakeCaller{err: errors.New("dial tcp: connection refused")}
	handler := newTestHandler(t, caller)

	recorder := postJSON(handler, "/v1/chat/completions", `{"model":"kimi-k3","messages":[]}`)

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", recorder.Code)
	}
	want := `"error":{"message":"failed to reach the Copilot API","type":"api_error"}`
	if !strings.Contains(recorder.Body.String(), want) {
		t.Errorf("body = %s, want OpenAI api_error", recorder.Body)
	}
}

func TestOpenAIRelaysSSEAndKeepsDone(t *testing.T) {
	t.Parallel()
	upstream := "event: response.created\n" +
		"data: {\"type\":\"response.created\"}\n" +
		"\n" +
		"event: response.completed\n" +
		"data: {\"type\":\"response.completed\"}\n" +
		"\n" +
		"data: [DONE]\n" +
		"\n"
	caller := &fakeCaller{response: sseResponse(strings.NewReader(upstream))}
	handler := newTestHandler(t, caller)

	recorder := postJSON(handler, "/v1/responses", `{"model":"gpt-6.1-sol","stream":true,"input":"hi"}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "event: response.created") ||
		!strings.Contains(body, "event: response.completed") {
		t.Errorf("stream lost events: %q", body)
	}
	if !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Errorf("stream must end with the [DONE] sentinel: %q", body)
	}
	if !recorder.Flushed {
		t.Error("stream was never flushed")
	}
}

func TestOpenAIStopsAtDoneBeforeLateReset(t *testing.T) {
	t.Parallel()
	upstream := "data: {\"id\":\"chatcmpl_1\"}\n\ndata: [DONE]\n\n"
	body := io.MultiReader(strings.NewReader(upstream), iotest.ErrReader(errors.New("connection reset")))
	caller := &fakeCaller{response: sseResponse(body)}
	handler := newTestHandler(t, caller)

	recorder := postJSON(handler, "/v1/chat/completions",
		`{"model":"kimi-k3","stream":true,"messages":[]}`)

	output := recorder.Body.String()
	if !strings.HasSuffix(output, "data: [DONE]\n\n") {
		t.Errorf("stream must end at the [DONE] sentinel: %q", output)
	}
	if strings.Contains(output, `"error"`) {
		t.Errorf("a reset after [DONE] must not surface as an error: %q", output)
	}
	statusRecorder := httptest.NewRecorder()
	handler.ServeHTTP(statusRecorder, httptest.NewRequest(http.MethodGet, "/status", nil))
	if !strings.Contains(statusRecorder.Body.String(), `"errors":0`) {
		t.Errorf("a reset after [DONE] must not count as a failure: %s", statusRecorder.Body)
	}
}

func TestOpenAIEmitsErrorDataOnStreamAbort(t *testing.T) {
	t.Parallel()
	partial := "data: {\"id\":\"chatcmpl_1\",\"choices\":[{\"delta\":{\"content\":\"He"
	body := io.MultiReader(strings.NewReader(partial), iotest.ErrReader(errors.New("upstream died")))
	caller := &fakeCaller{response: sseResponse(body)}
	handler := newTestHandler(t, caller)

	recorder := postJSON(handler, "/v1/chat/completions",
		`{"model":"kimi-k3","stream":true,"messages":[]}`)

	output := recorder.Body.String()
	want := "\n\ndata: {\"error\":{\"message\":\"upstream stream aborted unexpectedly\",\"type\":\"api_error\"}}\n\n"
	if !strings.HasSuffix(output, want) {
		t.Errorf("expected terminal OpenAI error data event, got: %q", output)
	}
	if strings.Contains(output, "event: error") {
		t.Errorf("OpenAI stream must not use the Anthropic error event: %q", output)
	}
}

func TestOpenAIDetectsVision(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		path string
		body string
		want string
	}{
		{
			name: "chat image_url part",
			path: "/v1/chat/completions",
			body: `{"model":"gpt-4o","messages":[{"role":"user","content":[` +
				`{"type":"text","text":"what is this"},{"type":"image_url","image_url":{"url":"data:x"}}]}]}`,
			want: "true",
		},
		{
			name: "responses input_image part",
			path: "/v1/responses",
			body: `{"model":"gpt-6.1-sol","input":[{"role":"user","content":[` +
				`{"type":"input_text","text":"what is this"},{"type":"input_image","image_url":"data:x"}]}]}`,
			want: "true",
		},
		{
			name: "responses function_call_output with screenshot",
			path: "/v1/responses",
			body: `{"model":"gpt-6.1-sol","input":[{"type":"function_call_output","call_id":"c1","output":[` +
				`{"type":"input_image","image_url":"data:x"}]}]}`,
			want: "true",
		},
		{
			name: "text only",
			path: "/v1/responses",
			body: `{"model":"gpt-6.1-sol","input":"hi"}`,
			want: "",
		},
		{
			name: "image for a model without vision",
			path: "/v1/chat/completions",
			body: `{"model":"kimi-k3","messages":[{"role":"user","content":[` +
				`{"type":"image_url","image_url":{"url":"data:x"}}]}]}`,
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			caller := &fakeCaller{response: jsonResponse(http.StatusOK, `{}`)}
			handler := newTestHandler(t, caller)

			recorder := postJSON(handler, tt.path, tt.body)

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", recorder.Code, recorder.Body)
			}
			if got := caller.opts.Header.Get("Copilot-Vision-Request"); got != tt.want {
				t.Errorf("Copilot-Vision-Request = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOpenAIModelsListsOpenAIDialects(t *testing.T) {
	t.Parallel()
	handler := newTestHandler(t, &fakeCaller{})

	req := httptest.NewRequest(http.MethodGet, "/openai/v1/models", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var list struct {
		Object string `json:"object"`
		Data   []struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if list.Object != "list" {
		t.Errorf("object = %q, want list", list.Object)
	}
	ids := make([]string, 0, len(list.Data))
	for _, model := range list.Data {
		ids = append(ids, model.ID)
		if model.Object != "model" {
			t.Errorf("model %s object = %q, want model", model.ID, model.Object)
		}
	}
	want := []string{"gpt-5-mini", "gpt-6.1-sol", "kimi-k3", "gpt-4o"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Errorf("ids = %v, want %v", ids, want)
	}
	if list.Data[2].OwnedBy != "Moonshot AI" {
		t.Errorf("kimi owned_by = %q, want Moonshot AI", list.Data[2].OwnedBy)
	}
}

func TestModelsNegotiatesByAnthropicVersion(t *testing.T) {
	t.Parallel()
	handler := newTestHandler(t, &fakeCaller{})

	withHeader := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	withHeader.Header.Set("Anthropic-Version", "2023-06-01")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, withHeader)
	if !strings.Contains(recorder.Body.String(), `"first_id":"claude-sonnet-4.5"`) {
		t.Errorf("Anthropic client got %s, want the Anthropic list", recorder.Body)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if !strings.Contains(recorder.Body.String(), `"object":"list"`) ||
		!strings.Contains(recorder.Body.String(), `"id":"kimi-k3"`) {
		t.Errorf("OpenAI client got %s, want the OpenAI list", recorder.Body)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/anthropic/v1/models", nil))
	if !strings.Contains(recorder.Body.String(), `"first_id":"claude-sonnet-4.5"`) {
		t.Errorf("/anthropic/v1/models got %s, want the Anthropic list", recorder.Body)
	}
}

func TestHealthAndStatusCountOpenAIModels(t *testing.T) {
	t.Parallel()
	handler := newTestHandler(t, &fakeCaller{})

	for _, path := range []string{"/health", "/status"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		payload := map[string]any{}
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Fatalf("unmarshal %s: %v", path, err)
		}
		if payload["openai_models"] != float64(4) {
			t.Errorf("%s openai_models = %v, want 4", path, payload["openai_models"])
		}
		if payload["anthropic_models"] != float64(1) {
			t.Errorf("%s anthropic_models = %v, want 1", path, payload["anthropic_models"])
		}
	}
}
