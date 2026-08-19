package copilot

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"
)

type poolRoundTripper struct {
	statuses map[string][]int
}

func (r *poolRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	token := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
	statuses := r.statuses[token]
	status := statuses[0]
	r.statuses[token] = statuses[1:]
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{}`)),
	}, nil
}

func TestAccountPoolFailsOverOn429(t *testing.T) {
	t.Parallel()
	roundTripper := &poolRoundTripper{statuses: map[string][]int{
		"first":  {http.StatusTooManyRequests},
		"second": {http.StatusOK},
	}}
	pool, err := NewAccountPool([]AccountSession{
		{Name: "alice", Session: poolTestSession(roundTripper, "first")},
		{Name: "bob", Session: poolTestSession(roundTripper, "second")},
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := pool.Do(t.Context(), CallOptions{Method: http.MethodPost, Path: "/v1/messages"})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	usage := pool.Usage()
	if usage.Current != "bob" || usage.Requests != 2 || usage.Failovers != 1 ||
		usage.Accounts[0].RateLimited != 1 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestAccountPoolSwitchAccount(t *testing.T) {
	t.Parallel()
	roundTripper := &poolRoundTripper{statuses: map[string][]int{
		"second": {http.StatusOK},
	}}
	pool, err := NewAccountPool([]AccountSession{
		{Name: "alice", Session: poolTestSession(roundTripper, "first")},
		{Name: "bob", Session: poolTestSession(roundTripper, "second")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.SwitchAccount("BOB"); err != nil {
		t.Fatal(err)
	}
	resp, err := pool.Do(t.Context(), CallOptions{Method: http.MethodPost, Path: "/v1/messages"})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if usage := pool.Usage(); usage.Current != "bob" || usage.Accounts[1].Requests != 1 {
		t.Fatalf("usage = %+v", usage)
	}
	if err := pool.SwitchAccount("missing"); err == nil {
		t.Fatal("SwitchAccount missing account should fail")
	}
}

func poolTestSession(roundTripper http.RoundTripper, token string) *Session {
	logger := slog.New(slog.DiscardHandler)
	tokens := &TokenManager{token: token, expiresAt: time.Now().Add(time.Hour)}
	client := NewClient(ClientConfig{
		HTTPClient:    &http.Client{Transport: roundTripper},
		Logger:        logger,
		Tokens:        tokens,
		BaseURL:       "https://example.test",
		VSCodeVersion: "1.0.0",
	})
	return &Session{Tokens: tokens, Client: client}
}
