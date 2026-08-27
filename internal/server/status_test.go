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

// replayCaller answers every call with a freshly built response, so a test
// can drive several requests through one handler.
type replayCaller struct {
	status int
	err    error
}

func (c *replayCaller) Do(_ context.Context, _ copilot.CallOptions) (*http.Response, error) {
	if c.err != nil {
		return nil, c.err
	}
	return &http.Response{
		StatusCode: c.status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"msg_1"}`)),
	}, nil
}

func newStatusHandler(t *testing.T, caller server.CopilotCaller) http.Handler {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	catalog := copilot.NewCatalog(staticFetcher{models: testModels()}, logger, nil)
	if err := catalog.Refresh(t.Context()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	return server.New(server.Config{
		Logger: logger, Copilot: caller, Catalog: catalog,
		Tokens: staticTokenHealth(true), AccountType: "business",
	}).Handler()
}

func getStatus(t *testing.T, handler http.Handler) map[string]any {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/status", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200 (the endpoint always answers 200)", recorder.Code)
	}
	payload := map[string]any{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	return payload
}

func TestStatusReportsSessionFacts(t *testing.T) {
	t.Parallel()
	handler := newStatusHandler(t, &replayCaller{status: http.StatusOK})

	initial := getStatus(t, handler)
	if initial["status"] != "ok" || initial["account_type"] != "business" {
		t.Errorf("status = %v, account_type = %v", initial["status"], initial["account_type"])
	}
	if initial["requests"] != float64(0) || initial["errors"] != float64(0) {
		t.Errorf("counters start at %v/%v, want 0/0", initial["requests"], initial["errors"])
	}
	// staticTokenHealth is valid for an hour; the countdown must be positive
	// and bounded by it.
	expiresIn, _ := initial["token_expires_in_seconds"].(float64)
	if expiresIn <= 0 || expiresIn > time.Hour.Seconds() {
		t.Errorf("token_expires_in_seconds = %v, want a countdown within the hour", expiresIn)
	}

	body := `{"model":"claude-sonnet-4-5-20250929","messages":[{"role":"user","content":"hi"}]}`
	if recorder := postJSON(handler, "/v1/messages", body); recorder.Code != http.StatusOK {
		t.Fatalf("messages status = %d, want 200", recorder.Code)
	}

	after := getStatus(t, handler)
	if after["requests"] != float64(1) {
		t.Errorf("requests = %v, want 1", after["requests"])
	}
	model, ok := after["model"].(map[string]any)
	if !ok {
		t.Fatalf("model block missing: %v", after)
	}
	// The alias is recorded as asked for and as resolved against the catalog.
	if model["requested"] != "claude-sonnet-4-5-20250929" || model["resolved"] != "claude-sonnet-4.5" {
		t.Errorf("model = %v, want the requested alias and its resolution", model)
	}
}

func TestStatusCountsUpstreamFailures(t *testing.T) {
	t.Parallel()
	body := `{"model":"claude-sonnet-4.5","messages":[{"role":"user","content":"hi"}]}`

	t.Run("error status", func(t *testing.T) {
		t.Parallel()
		handler := newStatusHandler(t, &replayCaller{status: http.StatusTooManyRequests})
		postJSON(handler, "/v1/messages", body)

		status := getStatus(t, handler)
		if status["errors"] != float64(1) {
			t.Errorf("errors = %v, want 1", status["errors"])
		}
		if status["last_error"] != "upstream 429" {
			t.Errorf("last_error = %v, want upstream 429", status["last_error"])
		}
	})

	t.Run("transport failure", func(t *testing.T) {
		t.Parallel()
		handler := newStatusHandler(t, &replayCaller{err: errors.New("dial tcp: refused")})
		postJSON(handler, "/v1/messages", body)

		status := getStatus(t, handler)
		if status["errors"] != float64(1) {
			t.Errorf("errors = %v, want 1", status["errors"])
		}
		if status["last_error"] != "upstream unreachable" {
			t.Errorf("last_error = %v, want upstream unreachable", status["last_error"])
		}
	})

	t.Run("mid-stream abort", func(t *testing.T) {
		t.Parallel()
		// The upstream answers 200 and starts streaming, then the body dies
		// with a non-EOF error; the failure must still reach the counters.
		handler := newStatusHandler(t, &streamCaller{
			body: io.MultiReader(
				strings.NewReader("event: message_start\ndata: {}\n\n"),
				iotest.ErrReader(errors.New("connection reset")),
			),
		})
		postJSON(handler, "/v1/messages", body)

		status := getStatus(t, handler)
		if status["errors"] != float64(1) {
			t.Errorf("errors = %v, want 1", status["errors"])
		}
		if status["last_error"] != "stream aborted" {
			t.Errorf("last_error = %v, want stream aborted", status["last_error"])
		}
	})
}

// streamCaller answers with a 200 SSE response wrapping the given body, so a
// test can fail the stream partway through the relay.
type streamCaller struct {
	body io.Reader
}

func (c *streamCaller) Do(_ context.Context, _ copilot.CallOptions) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(c.body),
	}, nil
}

func TestStatusStaysAvailableWhenDegraded(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	catalog := copilot.NewCatalog(staticFetcher{models: testModels()}, logger, nil)
	if err := catalog.Refresh(t.Context()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	handler := server.New(server.Config{
		Logger: logger, Copilot: &replayCaller{status: http.StatusOK}, Catalog: catalog,
		Tokens: staticTokenHealth(false),
	}).Handler()

	// /health answers 503 here; /status must still answer 200 so a status line
	// can tell "degraded" apart from "not running".
	status := getStatus(t, handler)
	if status["status"] != "degraded" || status["token_valid"] != false {
		t.Errorf("status = %v, token_valid = %v", status["status"], status["token_valid"])
	}
	if status["token_expires_in_seconds"] != float64(0) {
		t.Errorf("token_expires_in_seconds = %v, want 0 without a token",
			status["token_expires_in_seconds"])
	}
}
