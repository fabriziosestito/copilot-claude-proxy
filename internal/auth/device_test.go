package auth_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fabriziosestito/copilot-claude-proxy/internal/auth"
)

func testAuthorization(server *httptest.Server) (*auth.Flow, *auth.DeviceAuthorization) {
	flow := auth.NewFlow(auth.FlowConfig{
		HTTPClient: server.Client(),
		BaseURL:    server.URL,
		APIBaseURL: server.URL,
	})
	authorization := auth.NewDeviceAuthorizationForTest(
		"device-123", time.Millisecond, time.Now().Add(time.Minute))
	return flow, authorization
}

// TestWaitSurvivesTransientPollErrors pins that a gateway blip mid-flow does
// not abort the authorization while the user is still typing the code, and
// that RFC 8628-style error payloads delivered with HTTP 400 are parsed.
func TestWaitSurvivesTransientPollErrors(t *testing.T) {
	t.Parallel()
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch polls.Add(1) {
		case 1:
			w.WriteHeader(http.StatusBadGateway)
		case 2:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
		default:
			_, _ = w.Write([]byte(`{"access_token":"gh-token"}`))
		}
	}))
	t.Cleanup(server.Close)
	flow, authorization := testAuthorization(server)

	token, err := flow.Wait(t.Context(), authorization)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if token != "gh-token" {
		t.Errorf("token = %q, want gh-token", token)
	}
	if polls.Load() != 3 {
		t.Errorf("polls = %d, want 3 (transient error + pending + success)", polls.Load())
	}
}

func TestWaitStopsOnAccessDenied(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":"access_denied"}`))
	}))
	t.Cleanup(server.Close)
	flow, authorization := testAuthorization(server)

	if _, err := flow.Wait(t.Context(), authorization); !errors.Is(err, auth.ErrAccessDenied) {
		t.Errorf("Wait error = %v, want ErrAccessDenied", err)
	}
}

func TestWaitStopsOnDefinitiveOAuthError(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"unsupported_grant_type","error_description":"nope"}`))
	}))
	t.Cleanup(server.Close)
	flow, authorization := testAuthorization(server)

	_, err := flow.Wait(t.Context(), authorization)
	code, isOAuthError := auth.OAuthErrorCode(err)
	if !isOAuthError {
		t.Fatalf("Wait error = %v, want a definitive OAuth error", err)
	}
	if code != "unsupported_grant_type" {
		t.Errorf("oauth error code = %q, want unsupported_grant_type", code)
	}
}
