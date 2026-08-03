package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/fabriziosestito/copilot-claude-proxy/internal/copilot"
	"github.com/fabriziosestito/copilot-claude-proxy/internal/server"
)

type staticFetcher struct {
	models []copilot.Model
}

func (f staticFetcher) FetchModels(_ context.Context) ([]copilot.Model, error) {
	return f.models, nil
}

// fakeCaller records the forwarded request and plays back a canned response.
type fakeCaller struct {
	opts     copilot.CallOptions
	response *http.Response
	err      error
}

func (f *fakeCaller) Do(_ context.Context, opts copilot.CallOptions) (*http.Response, error) {
	f.opts = opts
	if f.err != nil {
		return nil, f.err
	}
	return f.response, nil
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func sseResponse(body io.Reader) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(body),
	}
}

func testModels() []copilot.Model {
	sonnet := copilot.Model{
		ID:                 "claude-sonnet-4.5",
		Name:               "Claude Sonnet 4.5",
		Vendor:             "Anthropic",
		SupportedEndpoints: []string{"/v1/messages"},
	}
	sonnet.Capabilities.Supports.Vision = true
	gpt := copilot.Model{
		ID:                 "gpt-5-mini",
		Name:               "GPT 5 mini",
		Vendor:             "OpenAI",
		SupportedEndpoints: []string{"/chat/completions"},
	}
	return []copilot.Model{sonnet, gpt}
}

func newTestHandler(t *testing.T, caller server.CopilotCaller) http.Handler {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	catalog := copilot.NewCatalog(staticFetcher{models: testModels()}, logger, nil)
	if err := catalog.Refresh(t.Context()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	return server.New(server.Config{Logger: logger, Copilot: caller, Catalog: catalog}).Handler()
}

func postJSON(handler http.Handler, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func TestMessagesRewritesModelAndSetsHeaders(t *testing.T) {
	t.Parallel()
	caller := &fakeCaller{response: jsonResponse(http.StatusOK, `{"id":"msg_1"}`)}
	handler := newTestHandler(t, caller)

	body := `{"model":"claude-sonnet-4-5-20250929","max_tokens":32,` +
		`"messages":[{"role":"user","content":"hi"}]}`
	recorder := postJSON(handler, "/v1/messages", body)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", recorder.Code, recorder.Body)
	}

	var forwarded map[string]any
	if err := json.Unmarshal(caller.opts.Body, &forwarded); err != nil {
		t.Fatalf("unmarshal forwarded body: %v", err)
	}
	if forwarded["model"] != "claude-sonnet-4.5" {
		t.Errorf("forwarded model = %v, want claude-sonnet-4.5", forwarded["model"])
	}
	if forwarded["max_tokens"] != float64(32) {
		t.Errorf("forwarded max_tokens = %v, want 32", forwarded["max_tokens"])
	}
	if caller.opts.Path != "/v1/messages" {
		t.Errorf("path = %q, want /v1/messages", caller.opts.Path)
	}
	if got := caller.opts.Header.Get("Anthropic-Version"); got != "2023-06-01" {
		t.Errorf("Anthropic-Version = %q, want 2023-06-01", got)
	}
	if recorder.Body.String() != `{"id":"msg_1"}` {
		t.Errorf("body = %q, want passthrough", recorder.Body.String())
	}
}

func TestMessagesDetectsVision(t *testing.T) {
	t.Parallel()
	caller := &fakeCaller{response: jsonResponse(http.StatusOK, `{}`)}
	handler := newTestHandler(t, caller)

	body := `{"model":"claude-sonnet-4.5","messages":[` +
		`{"role":"user","content":[{"type":"text","text":"look"},` +
		`{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGk="}}]},` +
		`{"role":"assistant","content":"I see."}]}`
	recorder := postJSON(handler, "/v1/messages", body)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if got := caller.opts.Header.Get("Copilot-Vision-Request"); got != "true" {
		t.Errorf("Copilot-Vision-Request = %q, want true", got)
	}
}

func TestMessagesDetectsImageInToolResult(t *testing.T) {
	t.Parallel()
	caller := &fakeCaller{response: jsonResponse(http.StatusOK, `{}`)}
	handler := newTestHandler(t, caller)

	body := `{"model":"claude-sonnet-4.5","messages":[` +
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1",` +
		`"content":[{"type":"image","source":{}}]}]}]}`
	postJSON(handler, "/v1/messages", body)

	if got := caller.opts.Header.Get("Copilot-Vision-Request"); got != "true" {
		t.Errorf("Copilot-Vision-Request = %q, want true (image nested in tool_result)", got)
	}
}

func TestMessagesForwardsAnthropicBeta(t *testing.T) {
	t.Parallel()
	caller := &fakeCaller{response: jsonResponse(http.StatusOK, `{}`)}
	handler := newTestHandler(t, caller)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"claude-sonnet-4.5","messages":[]}`))
	req.Header.Add("Anthropic-Beta", "interleaved-thinking-2025-05-14")
	req.Header.Add("Anthropic-Beta", "context-1m-2025-08-07")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	want := "interleaved-thinking-2025-05-14,context-1m-2025-08-07"
	if got := caller.opts.Header.Get("Anthropic-Beta"); got != want {
		t.Errorf("Anthropic-Beta = %q, want all field lines joined: %q", got, want)
	}
}

// TestMessagesRelaysUpstreamHeaders pins that rate-limit metadata and request
// ids survive the proxy on non-streaming responses.
func TestMessagesRelaysUpstreamHeaders(t *testing.T) {
	t.Parallel()
	response := jsonResponse(http.StatusTooManyRequests, `{"type":"error"}`)
	response.Header.Set("Retry-After", "60")
	response.Header.Set("Request-Id", "req_123")
	response.Header.Set("Connection", "keep-alive")
	caller := &fakeCaller{response: response}
	handler := newTestHandler(t, caller)

	recorder := postJSON(handler, "/v1/messages",
		`{"model":"claude-sonnet-4.5","messages":[]}`)

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 passthrough", recorder.Code)
	}
	if got := recorder.Header().Get("Retry-After"); got != "60" {
		t.Errorf("Retry-After = %q, want 60", got)
	}
	if got := recorder.Header().Get("Request-Id"); got != "req_123" {
		t.Errorf("Request-Id = %q, want req_123", got)
	}
	if got := recorder.Header().Get("Connection"); got != "" {
		t.Errorf("hop-by-hop Connection header relayed: %q", got)
	}
}

func TestMessagesRejectsNonAnthropicModel(t *testing.T) {
	t.Parallel()
	caller := &fakeCaller{response: jsonResponse(http.StatusOK, `{}`)}
	handler := newTestHandler(t, caller)

	recorder := postJSON(handler, "/v1/messages",
		`{"model":"gpt-5-mini","messages":[{"role":"user","content":"hi"}]}`)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
	var payload struct {
		Type  string `json:"type"`
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal error payload: %v", err)
	}
	if payload.Type != "error" || payload.Error.Type != "invalid_request_error" {
		t.Errorf("error payload = %+v, want anthropic invalid_request_error", payload)
	}
}

func TestMessagesPassesUnknownModelThrough(t *testing.T) {
	t.Parallel()
	caller := &fakeCaller{response: jsonResponse(http.StatusNotFound, `{"type":"error"}`)}
	handler := newTestHandler(t, caller)

	recorder := postJSON(handler, "/v1/messages",
		`{"model":"claude-legacy-1","messages":[]}`)

	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want upstream 404 passthrough", recorder.Code)
	}
	var forwarded map[string]any
	if err := json.Unmarshal(caller.opts.Body, &forwarded); err != nil {
		t.Fatalf("unmarshal forwarded body: %v", err)
	}
	if forwarded["model"] != "claude-legacy-1" {
		t.Errorf("forwarded model = %v, want claude-legacy-1", forwarded["model"])
	}
}

func TestMessagesUpstreamFailure(t *testing.T) {
	t.Parallel()
	caller := &fakeCaller{err: errors.New("connection refused")}
	handler := newTestHandler(t, caller)

	recorder := postJSON(handler, "/v1/messages",
		`{"model":"claude-sonnet-4.5","messages":[]}`)

	if recorder.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", recorder.Code)
	}
}

func TestMessagesRelaysSSEAndStripsDone(t *testing.T) {
	t.Parallel()
	upstream := "event: message_start\n" +
		"data: {\"type\":\"message_start\"}\n" +
		"\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n" +
		"\n" +
		"data: [DONE]\n" +
		"\n"
	caller := &fakeCaller{response: sseResponse(strings.NewReader(upstream))}
	handler := newTestHandler(t, caller)

	recorder := postJSON(handler, "/v1/messages",
		`{"model":"claude-sonnet-4.5","stream":true,"messages":[]}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "event: message_start") ||
		!strings.Contains(body, "event: message_stop") {
		t.Errorf("stream lost events: %q", body)
	}
	if strings.Contains(body, "[DONE]") {
		t.Errorf("stream should strip the [DONE] sentinel: %q", body)
	}
	if !recorder.Flushed {
		t.Error("stream was never flushed")
	}
}

func TestMessagesEmitsErrorEventOnStreamAbort(t *testing.T) {
	t.Parallel()
	partial := "event: message_start\ndata: {\"type\":\"message_start\"}\n\n"
	body := io.MultiReader(strings.NewReader(partial), iotest.ErrReader(errors.New("upstream died")))
	caller := &fakeCaller{response: sseResponse(body)}
	handler := newTestHandler(t, caller)

	recorder := postJSON(handler, "/v1/messages",
		`{"model":"claude-sonnet-4.5","stream":true,"messages":[]}`)

	output := recorder.Body.String()
	if !strings.Contains(output, "event: error") ||
		!strings.Contains(output, `"type":"api_error"`) {
		t.Errorf("expected terminal error event, got: %q", output)
	}
}

// TestMessagesTerminatesPartialEventBeforeStreamError pins that an upstream
// dying mid-line does not corrupt the terminal error event: the dangling
// fragment must be closed off so "event: error" starts a fresh SSE event.
func TestMessagesTerminatesPartialEventBeforeStreamError(t *testing.T) {
	t.Parallel()
	partial := "event: content_block_delta\ndata: {\"type\":\"content_blo"
	body := io.MultiReader(strings.NewReader(partial), iotest.ErrReader(errors.New("upstream died")))
	caller := &fakeCaller{response: sseResponse(body)}
	handler := newTestHandler(t, caller)

	recorder := postJSON(handler, "/v1/messages",
		`{"model":"claude-sonnet-4.5","stream":true,"messages":[]}`)

	output := recorder.Body.String()
	if !strings.Contains(output, "\n\nevent: error\ndata: ") {
		t.Errorf("error event not separated from the truncated fragment: %q", output)
	}
	if strings.Contains(output, "content_bloevent:") {
		t.Errorf("error event glued onto the partial line: %q", output)
	}
}

func TestCountTokensEstimates(t *testing.T) {
	t.Parallel()
	handler := newTestHandler(t, &fakeCaller{})

	// system 40 chars + text 40 chars = 80 chars -> 20 tokens, + 2 messages * 4.
	longText := strings.Repeat("a", 40)
	body := `{"model":"claude-sonnet-4.5","system":"` + longText + `",` +
		`"messages":[{"role":"user","content":"` + longText + `"},` +
		`{"role":"assistant","content":[{"type":"thinking","thinking":"` + longText + `"}]}]}`
	recorder := postJSON(handler, "/v1/messages/count_tokens", body)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", recorder.Code, recorder.Body)
	}
	var response struct {
		InputTokens int `json:"input_tokens"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if response.InputTokens != 28 {
		t.Errorf("input_tokens = %d, want 28 (thinking excluded)", response.InputTokens)
	}
}

func TestCountTokensWeighsCJKRunes(t *testing.T) {
	t.Parallel()
	handler := newTestHandler(t, &fakeCaller{})

	// 100 Japanese characters (300 UTF-8 bytes) should count as ~100 tokens,
	// not 300/4 = 75: CJK tokenizes at roughly one token per rune.
	cjk := strings.Repeat("あ", 100)
	body := `{"model":"claude-sonnet-4.5",` +
		`"messages":[{"role":"user","content":"` + cjk + `"}]}`
	recorder := postJSON(handler, "/v1/messages/count_tokens", body)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", recorder.Code, recorder.Body)
	}
	var response struct {
		InputTokens int `json:"input_tokens"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if response.InputTokens != 104 { // 100 rune-tokens + 1 message * 4 overhead
		t.Errorf("input_tokens = %d, want 104 (one token per CJK rune)", response.InputTokens)
	}
}

func TestCountTokensMinimum(t *testing.T) {
	t.Parallel()
	handler := newTestHandler(t, &fakeCaller{})

	recorder := postJSON(handler, "/v1/messages/count_tokens",
		`{"model":"claude-sonnet-4.5","messages":[]}`)

	var response struct {
		InputTokens int `json:"input_tokens"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if response.InputTokens != 1 {
		t.Errorf("input_tokens = %d, want minimum 1", response.InputTokens)
	}
}

func TestModelsListsAnthropicOnly(t *testing.T) {
	t.Parallel()
	handler := newTestHandler(t, &fakeCaller{})

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var list struct {
		Data []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"data"`
		FirstID string `json:"first_id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(list.Data) != 1 || list.Data[0].ID != "claude-sonnet-4.5" {
		t.Errorf("data = %+v, want only claude-sonnet-4.5", list.Data)
	}
	if list.FirstID != "claude-sonnet-4.5" {
		t.Errorf("first_id = %q, want claude-sonnet-4.5", list.FirstID)
	}
}

// staticTokenHealth is a token that is either valid for an hour or absent.
type staticTokenHealth bool

func (h staticTokenHealth) TokenValid() bool { return bool(h) }

func (h staticTokenHealth) TokenExpiry() time.Time {
	if !h {
		return time.Time{}
	}
	return time.Now().Add(time.Hour)
}

func TestHealthReflectsTokenAndCatalog(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)

	newHandler := func(models []copilot.Model, tokens server.TokenHealth) http.Handler {
		catalog := copilot.NewCatalog(staticFetcher{models: models}, logger, nil)
		if err := catalog.Refresh(t.Context()); err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		return server.New(server.Config{
			Logger: logger, Copilot: &fakeCaller{}, Catalog: catalog, Tokens: tokens,
		}).Handler()
	}
	getHealth := func(handler http.Handler) (*httptest.ResponseRecorder, map[string]any) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))
		payload := map[string]any{}
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Fatalf("unmarshal health: %v", err)
		}
		return recorder, payload
	}

	recorder, payload := getHealth(newHandler(testModels(), staticTokenHealth(true)))
	if recorder.Code != http.StatusOK || payload["status"] != "ok" {
		t.Errorf("healthy proxy: status = %d %v, want 200 ok", recorder.Code, payload["status"])
	}
	if payload["anthropic_models"] != float64(1) {
		t.Errorf("anthropic_models = %v, want 1", payload["anthropic_models"])
	}

	recorder, payload = getHealth(newHandler(testModels(), staticTokenHealth(false)))
	if recorder.Code != http.StatusServiceUnavailable || payload["status"] != "degraded" {
		t.Errorf("invalid token: status = %d %v, want 503 degraded", recorder.Code, payload["status"])
	}

	// A catalog with only non-Anthropic vendors cannot serve any request.
	gptOnly := []copilot.Model{{
		ID: "gpt-5-mini", Vendor: "OpenAI", SupportedEndpoints: []string{"/chat/completions"},
	}}
	recorder, payload = getHealth(newHandler(gptOnly, staticTokenHealth(true)))
	if recorder.Code != http.StatusServiceUnavailable || payload["status"] != "degraded" {
		t.Errorf("no anthropic models: status = %d %v, want 503 degraded",
			recorder.Code, payload["status"])
	}
}

func TestTrailingSlashAndEventLogging(t *testing.T) {
	t.Parallel()
	caller := &fakeCaller{response: jsonResponse(http.StatusOK, `{}`)}
	handler := newTestHandler(t, caller)

	recorder := postJSON(handler, "/v1/messages/",
		`{"model":"claude-sonnet-4.5","messages":[]}`)
	if recorder.Code != http.StatusOK {
		t.Errorf("trailing slash status = %d, want 200", recorder.Code)
	}

	logging := postJSON(handler, "/api/event_logging", `{"events":[]}`)
	if logging.Code != http.StatusOK {
		t.Errorf("event_logging status = %d, want 200", logging.Code)
	}
}
