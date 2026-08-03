package server

import (
	"math"
	"net/http"
	"sync"
	"time"
)

// activity accumulates the request-level facts /status reports. It is written
// on every proxied message and read by a status line polling several times a
// minute, so the counters stay behind a plain mutex rather than growing into
// a metrics dependency.
type activity struct {
	mu        sync.Mutex
	requests  uint64
	failures  uint64
	requested string
	resolved  string
	lastError string
}

// record notes a request the proxy is about to forward upstream.
func (a *activity) record(requested, resolved string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requests++
	a.requested, a.resolved = requested, resolved
}

// fail notes an upstream request that did not succeed.
func (a *activity) fail(reason string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failures++
	a.lastError = reason
}

func (a *activity) snapshot() activitySnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	return activitySnapshot{
		requests:  a.requests,
		failures:  a.failures,
		requested: a.requested,
		resolved:  a.resolved,
		lastError: a.lastError,
	}
}

type activitySnapshot struct {
	requests  uint64
	failures  uint64
	requested string
	resolved  string
	lastError string
}

// statusResponse describes the proxy to a status line. Unlike /health it
// always answers 200: a status line renders whatever it gets, and a non-2xx
// answer would only make it harder to tell "degraded" from "not running".
type statusResponse struct {
	Status          string      `json:"status"`
	AccountType     string      `json:"account_type,omitempty"`
	TokenValid      bool        `json:"token_valid"`
	TokenExpiresIn  int         `json:"token_expires_in_seconds"`
	Models          int         `json:"models"`
	AnthropicModels int         `json:"anthropic_models"`
	UptimeSeconds   int         `json:"uptime_seconds"`
	Requests        uint64      `json:"requests"`
	Errors          uint64      `json:"errors"`
	Model           statusModel `json:"model"`
	LastError       string      `json:"last_error,omitempty"`
}

// statusModel reports the most recent model resolution, so a status line can
// show what the proxy actually sent upstream rather than what was asked for.
type statusModel struct {
	Requested string `json:"requested,omitempty"`
	Resolved  string `json:"resolved,omitempty"`
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	models := s.catalog.Models()
	anthropic := 0
	for _, model := range models {
		if model.SupportsAnthropicMessages() {
			anthropic++
		}
	}

	tokenValid, expiresIn := true, 0
	if s.tokens != nil {
		tokenValid = s.tokens.TokenValid()
		if expiry := s.tokens.TokenExpiry(); !expiry.IsZero() {
			expiresIn = secondsUntil(expiry)
		}
	}

	status := "ok"
	if !tokenValid || anthropic == 0 {
		status = "degraded"
	}

	current := s.activity.snapshot()
	writeJSON(w, http.StatusOK, statusResponse{
		Status:          status,
		AccountType:     s.accountType,
		TokenValid:      tokenValid,
		TokenExpiresIn:  expiresIn,
		Models:          len(models),
		AnthropicModels: anthropic,
		UptimeSeconds:   int(time.Since(s.startedAt).Seconds()),
		Requests:        current.requests,
		Errors:          current.failures,
		Model:           statusModel{Requested: current.requested, Resolved: current.resolved},
		LastError:       current.lastError,
	})
}

// secondsUntil clamps an expiry to a non-negative whole number of seconds; an
// already-expired token reports 0 rather than a negative countdown.
func secondsUntil(deadline time.Time) int {
	remaining := time.Until(deadline).Seconds()
	if remaining <= 0 {
		return 0
	}
	return int(math.Round(remaining))
}
