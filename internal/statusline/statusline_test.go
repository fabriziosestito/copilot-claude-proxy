package statusline_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fabriziosestito/copilot-claude-proxy/internal/statusline"
)

// sessionJSON is the shape Claude Code writes to stdin.
const sessionJSON = `{"model":{"id":"claude-fable-5[1m]","display_name":"Fable"},
	"workspace":{"current_dir":"/tmp"},"cost":{"total_cost_usd":0.1}}`

// statusServer serves one /status payload and reports whether it was called.
func statusServer(t *testing.T, status statusline.Status) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			t.Errorf("requested %q, want /status", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(status)
	}))
	t.Cleanup(server.Close)
	return server
}

func healthy() statusline.Status {
	status := statusline.Status{
		Status: "ok", AccountType: "business", TokenValid: true,
		TokenExpiresIn: 1500, AnthropicModels: 4, Requests: 42,
	}
	status.Model.Requested = "claude-fable-5[1m]"
	status.Model.Resolved = "claude-fable-5-1m"
	return status
}

func run(t *testing.T, baseURL, session string) string {
	t.Helper()
	var out bytes.Buffer
	err := statusline.Run(t.Context(), statusline.Config{
		BaseURL: baseURL,
		In:      strings.NewReader(session),
		Out:     &out,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return out.String()
}

func TestRunReportsProxyState(t *testing.T) {
	t.Parallel()
	got := run(t, statusServer(t, healthy()).URL, sessionJSON)

	want := "copilot business · claude-fable-5-1m · 42 req\n"
	if got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}

func TestRunIsSilentWhenProxyIsUnreachable(t *testing.T) {
	t.Parallel()
	// Closed immediately, so the port is almost certainly refusing connections.
	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL
	server.Close()

	if got := run(t, url, sessionJSON); got != "" {
		t.Errorf("line = %q, want no output for an unreachable proxy", got)
	}
}

func TestRunSurvivesUnparseableSession(t *testing.T) {
	t.Parallel()
	if got := run(t, statusServer(t, healthy()).URL, "not json at all"); got == "" {
		t.Error("expected the proxy segments to render without a usable session payload")
	}
}

func TestRenderFallsBackToTheSessionModel(t *testing.T) {
	t.Parallel()
	status := healthy()
	status.Model.Resolved = ""
	status.Requests = 0

	var session statusline.Session
	session.Model.ID = "claude-opus-5"

	want := "copilot business · claude-opus-5 · 0 req"
	if got := statusline.Render(session, &status, false); got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}

func TestRenderFlagsDegradedStates(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		mutate func(*statusline.Status)
		want   string
	}{
		"expired token": {
			mutate: func(s *statusline.Status) { s.TokenValid = false },
			want:   "token expired",
		},
		"token about to expire": {
			mutate: func(s *statusline.Status) { s.TokenExpiresIn = 30 },
			want:   "token 30s",
		},
		"empty catalog": {
			mutate: func(s *statusline.Status) { s.AnthropicModels = 0 },
			want:   "no anthropic models",
		},
		"upstream errors": {
			mutate: func(s *statusline.Status) { s.Errors = 2; s.LastError = "upstream 429" },
			want:   "2 err (upstream 429)",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			status := healthy()
			test.mutate(&status)
			if got := statusline.Render(statusline.Session{}, &status, false); !strings.Contains(got, test.want) {
				t.Errorf("line %q does not contain %q", got, test.want)
			}
		})
	}
}

func TestRenderColorsOnlyWhenAsked(t *testing.T) {
	t.Parallel()
	status := healthy()
	status.Errors = 1

	if plain := statusline.Render(statusline.Session{}, &status, false); strings.Contains(plain, "\x1b[") {
		t.Errorf("uncolored line %q contains escape codes", plain)
	}
	if colored := statusline.Render(statusline.Session{}, &status, true); !strings.Contains(colored, "\x1b[31m") {
		t.Errorf("colored line %q lacks the error color", colored)
	}
}

func TestRenderWithoutStatusIsEmpty(t *testing.T) {
	t.Parallel()
	if got := statusline.Render(statusline.Session{}, nil, true); got != "" {
		t.Errorf("line = %q, want empty", got)
	}
}
