// Package server exposes the Anthropic-compatible HTTP surface backed by the
// GitHub Copilot API.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/fabriziosestito/copilot-claude-proxy/internal/copilot"
)

// CopilotCaller executes authenticated requests against the Copilot API.
type CopilotCaller interface {
	Do(ctx context.Context, opts copilot.CallOptions) (*http.Response, error)
}

// TokenHealth reports whether a usable Copilot token is currently cached.
type TokenHealth interface {
	TokenValid() bool
}

// Config carries the dependencies of a Server.
type Config struct {
	Logger  *slog.Logger
	Copilot CopilotCaller
	Catalog *copilot.Catalog
	// Tokens is optional; when set, /health reflects token validity.
	Tokens TokenHealth
}

// Server handles the Anthropic-compatible routes.
type Server struct {
	logger  *slog.Logger
	copilot CopilotCaller
	catalog *copilot.Catalog
	tokens  TokenHealth
}

// New builds a Server.
func New(cfg Config) *Server {
	return &Server{logger: cfg.Logger, copilot: cfg.Copilot, catalog: cfg.Catalog, tokens: cfg.Tokens}
}

// Handler returns the routed HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/messages", s.handleMessages)
	mux.HandleFunc("POST /anthropic/v1/messages", s.handleMessages)
	mux.HandleFunc("POST /v1/messages/count_tokens", s.handleCountTokens)
	mux.HandleFunc("POST /anthropic/v1/messages/count_tokens", s.handleCountTokens)
	mux.HandleFunc("GET /v1/models", s.handleModels)
	mux.HandleFunc("GET /anthropic/v1/models", s.handleModels)
	mux.HandleFunc("POST /api/event_logging", s.handleEventLogging)
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /{$}", s.handleRoot)
	return s.logRequests(trimTrailingSlash(mux))
}

func (s *Server) handleRoot(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("copilot-claude-proxy is running\n"))
}

type healthResponse struct {
	Status          string `json:"status"`
	Models          int    `json:"models"`
	AnthropicModels int    `json:"anthropic_models"`
	TokenValid      bool   `json:"token_valid"`
}

// handleHealth reports whether the proxy can actually serve traffic: a valid
// Copilot token must be cached and the catalog must contain at least one
// model usable through the Anthropic Messages API. Anything less is degraded
// and answers 503 so monitors notice.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	models := s.catalog.Models()
	anthropic := 0
	for _, model := range models {
		if model.SupportsAnthropicMessages() {
			anthropic++
		}
	}
	tokenValid := s.tokens == nil || s.tokens.TokenValid()

	status, code := "ok", http.StatusOK
	if !tokenValid || anthropic == 0 {
		status, code = "degraded", http.StatusServiceUnavailable
	}
	writeJSON(w, code, healthResponse{
		Status:          status,
		Models:          len(models),
		AnthropicModels: anthropic,
		TokenValid:      tokenValid,
	})
}

// handleEventLogging swallows Anthropic SDK telemetry so clients do not retry.
func (s *Server) handleEventLogging(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, struct{}{})
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		s.logger.InfoContext(r.Context(), "request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", recorder.status,
			"duration", time.Since(start).Round(time.Millisecond))
	})
}

// statusRecorder captures the response status while preserving streaming.
type statusRecorder struct {
	http.ResponseWriter

	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Flush forwards flushes so SSE streaming keeps working through the recorder.
func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func trimTrailingSlash(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trimmed := strings.TrimRight(r.URL.Path, "/")
		if trimmed == r.URL.Path || trimmed == "" {
			next.ServeHTTP(w, r)
			return
		}
		clone := r.Clone(r.Context())
		clone.URL.Path = trimmed
		next.ServeHTTP(w, clone)
	})
}
