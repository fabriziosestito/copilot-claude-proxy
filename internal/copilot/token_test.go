package copilot_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fabriziosestito/copilot-claude-proxy/internal/copilot"
)

// newTokenServer fakes the GitHub Copilot token endpoint, minting a new token
// per call.
func newTokenServer(t *testing.T, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/copilot_internal/v2/token" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "token gh-token" {
			t.Errorf("Authorization = %q, want %q", got, "token gh-token")
		}
		if got := r.Header.Get("X-Github-Api-Version"); got != "2025-04-01" {
			t.Errorf("X-Github-Api-Version = %q, want %q", got, "2025-04-01")
		}
		count := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"token":"copilot-%d","expires_at":%d,"refresh_in":1800}`,
			count, time.Now().Add(time.Hour).Unix())
	}))
	t.Cleanup(server.Close)
	return server
}

func newTokenManager(server *httptest.Server) *copilot.TokenManager {
	return copilot.NewTokenManager(copilot.TokenManagerConfig{
		HTTPClient:    server.Client(),
		Logger:        slog.New(slog.DiscardHandler),
		GitHubToken:   "gh-token",
		VSCodeVersion: "1.104.3",
		APIBaseURL:    server.URL,
	})
}

func TestTokenManagerCachesToken(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	manager := newTokenManager(newTokenServer(t, &calls))

	first, err := manager.Token(t.Context())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if first != "copilot-1" {
		t.Errorf("Token = %q, want copilot-1", first)
	}

	second, err := manager.Token(t.Context())
	if err != nil {
		t.Fatalf("Token (cached): %v", err)
	}
	if second != first {
		t.Errorf("cached Token = %q, want %q", second, first)
	}
	if calls.Load() != 1 {
		t.Errorf("token endpoint calls = %d, want 1", calls.Load())
	}
}

func TestTokenManagerRefreshReplacesToken(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	manager := newTokenManager(newTokenServer(t, &calls))

	if _, err := manager.Token(t.Context()); err != nil {
		t.Fatalf("Token: %v", err)
	}
	refreshed, err := manager.Refresh(t.Context())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if refreshed != "copilot-2" {
		t.Errorf("Refresh = %q, want copilot-2", refreshed)
	}
	if calls.Load() != 2 {
		t.Errorf("token endpoint calls = %d, want 2", calls.Load())
	}
}

// TestTokenManagerServesCachedDuringRefresh pins that a hung background
// refresh does not block callers holding a still-valid cached token.
func TestTokenManagerServesCachedDuringRefresh(t *testing.T) {
	t.Parallel()
	refreshArrived := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		count := calls.Add(1)
		if count > 1 {
			close(refreshArrived)
			<-release // simulate a hung connection
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"token":"copilot-%d","expires_at":%d,"refresh_in":1800}`,
			count, time.Now().Add(time.Hour).Unix())
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })
	manager := newTokenManager(server)

	first, err := manager.Token(t.Context())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}

	go func() { _, _ = manager.Refresh(context.Background()) }()
	<-refreshArrived

	done := make(chan string, 1)
	go func() {
		token, tokenErr := manager.Token(t.Context())
		if tokenErr != nil {
			t.Errorf("Token during refresh: %v", tokenErr)
		}
		done <- token
	}()
	select {
	case token := <-done:
		if token != first {
			t.Errorf("Token during refresh = %q, want cached %q", token, first)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Token blocked behind an in-flight refresh")
	}
}

func TestClientRetriesOnUnauthorized(t *testing.T) {
	t.Parallel()
	var tokenCalls atomic.Int32
	tokenServer := newTokenServer(t, &tokenCalls)
	manager := newTokenManager(tokenServer)

	var apiCalls atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if apiCalls.Add(1) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer copilot-2" {
			t.Errorf("Authorization after refresh = %q, want Bearer copilot-2", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(api.Close)

	client := copilot.NewClient(copilot.ClientConfig{
		HTTPClient:    api.Client(),
		Logger:        slog.New(slog.DiscardHandler),
		Tokens:        manager,
		BaseURL:       api.URL,
		VSCodeVersion: "1.104.3",
	})

	resp, err := client.Do(t.Context(), copilot.CallOptions{Method: http.MethodGet, Path: "/models"})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if apiCalls.Load() != 2 {
		t.Errorf("api calls = %d, want 2", apiCalls.Load())
	}
	if tokenCalls.Load() != 2 {
		t.Errorf("token calls = %d, want 2 (initial + refresh)", tokenCalls.Load())
	}
}
