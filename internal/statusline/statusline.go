// Package statusline renders the Claude Code status line that reports what
// the proxy is doing: the Copilot tier it is talking to, the model it last
// resolved upstream, and whether anything is failing.
//
// Claude Code draws this row above its own footer badges rather than in place
// of them, so the line deliberately carries only facts the footer cannot
// know. When the proxy is not reachable the line renders empty and the row
// disappears, which is the desired behavior for a session that is not going
// through the proxy at all.
package statusline

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// probeTimeout bounds the status request. The status line reruns on nearly
// every UI event, so a proxy that is slow to answer must not stall the render;
// the endpoint is loopback and lock-free, so this is generous.
const probeTimeout = 300 * time.Millisecond

// lowTokenWarning is how close a Copilot token gets to expiry before its
// remaining lifetime is worth a segment. Refresh happens well before this, so
// seeing it means refresh is failing.
const lowTokenWarning = 5 * time.Minute

// ANSI attributes; Claude Code renders escape codes in the status row.
const (
	ansiReset = "\x1b[0m"
	ansiDim   = "\x1b[2m"
	ansiRed   = "\x1b[31m"
)

// Session is the part of Claude Code's stdin payload this line uses. Every
// field is optional: they can be null before the first API response lands.
type Session struct {
	Model struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
	} `json:"model"`
}

// Status is the proxy's /status response.
type Status struct {
	Status          string `json:"status"`
	AccountType     string `json:"account_type"`
	TokenValid      bool   `json:"token_valid"`
	TokenExpiresIn  int    `json:"token_expires_in_seconds"`
	AnthropicModels int    `json:"anthropic_models"`
	Requests        uint64 `json:"requests"`
	Errors          uint64 `json:"errors"`
	Model           struct {
		Requested string `json:"requested"`
		Resolved  string `json:"resolved"`
	} `json:"model"`
	LastError string `json:"last_error"`
}

// Config describes one status line render.
type Config struct {
	// BaseURL is where the proxy serves /status.
	BaseURL string
	// In carries Claude Code's session JSON; Out receives the rendered line.
	In  io.Reader
	Out io.Writer
	// Color enables ANSI attributes.
	Color bool
	// Client is optional; the zero value uses a client bounded by probeTimeout.
	Client *http.Client
}

// Run reads the session payload, probes the proxy, and writes the line. An
// unreachable or unintelligible proxy is not an error: the line is simply
// omitted, because a status line that prints diagnostics would push its own
// failure into the UI on every keystroke.
func Run(ctx context.Context, cfg Config) error {
	session := decodeSession(cfg.In)

	line := Render(session, probeQuietly(ctx, cfg), cfg.Color)
	if line == "" {
		return nil
	}
	if _, err := fmt.Fprintln(cfg.Out, line); err != nil {
		return fmt.Errorf("write status line: %w", err)
	}
	return nil
}

// probeQuietly returns nil for any proxy that cannot answer, collapsing "not
// running", "not listening here", and "answered nonsense" into the one state
// the status line renders identically: absent.
func probeQuietly(ctx context.Context, cfg Config) *Status {
	status, err := Probe(ctx, cfg.BaseURL, cfg.Client)
	if err != nil {
		return nil
	}
	return status
}

// decodeSession parses Claude Code's payload, always draining stdin so the
// writer never blocks on a full pipe. A malformed payload yields the zero
// value rather than an error: the proxy segments do not depend on it.
func decodeSession(in io.Reader) Session {
	var session Session
	if in == nil {
		return session
	}
	data, err := io.ReadAll(in)
	if err != nil {
		return session
	}
	_ = json.Unmarshal(data, &session)
	return session
}

// Probe fetches /status from the proxy.
func Probe(ctx context.Context, baseURL string, client *http.Client) (*Status, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	url := strings.TrimSuffix(baseURL, "/") + "/status"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build status request: %w", err)
	}
	if client == nil {
		client = &http.Client{Timeout: probeTimeout}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reach proxy: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("proxy status endpoint returned %s", resp.Status)
	}
	var status Status
	if decodeErr := json.NewDecoder(resp.Body).Decode(&status); decodeErr != nil {
		return nil, fmt.Errorf("decode status: %w", decodeErr)
	}
	return &status, nil
}

// Render builds the line. Segments are joined with a dim separator, and
// anything wrong is colored so it stands out against the routine ones.
func Render(session Session, status *Status, color bool) string {
	if status == nil {
		return ""
	}
	paint := painter(color)

	segments := []string{paint(ansiDim, "copilot "+tierLabel(status))}
	if model := modelLabel(session, status); model != "" {
		segments = append(segments, model)
	}
	segments = append(segments, warnings(status, paint)...)
	segments = append(segments, paint(ansiDim, fmt.Sprintf("%d req", status.Requests)))
	if status.Errors > 0 {
		segments = append(segments, paint(ansiRed, errorLabel(status)))
	}

	return strings.Join(segments, paint(ansiDim, " · "))
}

// warnings lists the states worth interrupting the routine segments for.
func warnings(status *Status, paint func(string, string) string) []string {
	var found []string
	remaining := time.Duration(status.TokenExpiresIn) * time.Second
	switch {
	case !status.TokenValid:
		found = append(found, paint(ansiRed, "token expired"))
	case remaining > 0 && remaining < lowTokenWarning:
		found = append(found, paint(ansiRed, "token "+shortDuration(remaining)))
	}
	if status.AnthropicModels == 0 {
		found = append(found, paint(ansiRed, "no anthropic models"))
	}
	return found
}

// shortDuration renders a countdown in the largest unit that keeps it legible.
func shortDuration(remaining time.Duration) string {
	if remaining < time.Minute {
		return fmt.Sprintf("%ds", int(remaining.Seconds()))
	}
	return fmt.Sprintf("%dm", int(remaining.Minutes()))
}

// errorLabel summarizes the failures, naming the most recent cause when the
// proxy reported one.
func errorLabel(status *Status) string {
	label := fmt.Sprintf("%d err", status.Errors)
	if status.LastError != "" {
		label += " (" + status.LastError + ")"
	}
	return label
}

// tierLabel names the Copilot account tier, falling back to a neutral word
// when the proxy did not resolve one.
func tierLabel(status *Status) string {
	if status.AccountType == "" {
		return "proxy"
	}
	return status.AccountType
}

// modelLabel prefers the model the proxy last sent upstream, which is the
// only view that reflects alias and 1M-context rewriting. Before the first
// request it falls back to what Claude Code believes it is using.
func modelLabel(session Session, status *Status) string {
	if status.Model.Resolved != "" {
		return status.Model.Resolved
	}
	if session.Model.ID != "" {
		return session.Model.ID
	}
	return session.Model.DisplayName
}

// painter returns a colorizer, or a passthrough when color is disabled.
func painter(color bool) func(attribute, text string) string {
	if !color {
		return func(_, text string) string { return text }
	}
	return func(attribute, text string) string { return attribute + text + ansiReset }
}
