package server_test

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fabriziosestito/copilot-claude-proxy/internal/copilot"
	"github.com/fabriziosestito/copilot-claude-proxy/internal/server"
)

// newUpstream mocks the two endpoints a starting proxy talks to: the GitHub
// token exchange and the Copilot model catalog.
func newUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/copilot_internal/v2/token":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"token":"copilot-token","expires_at":%d,"refresh_in":1800}`,
				time.Now().Add(time.Hour).Unix())
		case "/models":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	return upstream
}

// TestRunEphemeralPort binds port 0 and checks that Ready reports the port
// the OS assigned and that the proxy answers on it.
func TestRunEphemeralPort(t *testing.T) {
	t.Parallel()
	upstream := newUpstream(t)
	logger := slog.New(slog.DiscardHandler)

	tokens := copilot.NewTokenManager(copilot.TokenManagerConfig{
		HTTPClient:  upstream.Client(),
		Logger:      logger,
		GitHubToken: "gh-token",
		APIBaseURL:  upstream.URL,
	})
	client := copilot.NewClient(copilot.ClientConfig{
		HTTPClient: upstream.Client(),
		Logger:     logger,
		Tokens:     tokens,
		BaseURL:    upstream.URL,
	})
	session := &copilot.Session{
		Tokens:      tokens,
		Client:      client,
		Catalog:     copilot.NewCatalog(client, logger, nil),
		AccountType: copilot.AccountIndividual,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ready := make(chan net.Addr, 1)
	runDone := make(chan error, 1)
	go func() {
		runDone <- server.Run(ctx, server.RunConfig{
			Logger:  logger,
			Session: session,
			Host:    "127.0.0.1",
			Port:    0,
			Ready:   func(addr net.Addr) { ready <- addr },
		})
	}()

	var addr net.Addr
	select {
	case addr = <-ready:
	case err := <-runDone:
		t.Fatalf("Run returned before ready: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the proxy to become ready")
	}

	tcpAddr, ok := addr.(*net.TCPAddr)
	if !ok {
		t.Fatalf("Ready reported %T, want *net.TCPAddr", addr)
	}
	if tcpAddr.Port == 0 {
		t.Fatal("Ready reported port 0, want the assigned ephemeral port")
	}

	resp, err := http.Get(fmt.Sprintf("http://%s/status", addr))
	if err != nil {
		t.Fatalf("GET /status on the reported address: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Errorf("Run returned %v after cancel, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for Run to shut down")
	}
}
