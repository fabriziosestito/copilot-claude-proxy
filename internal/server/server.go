// Package server exposes the Anthropic- and OpenAI-compatible HTTP surface
// backed by the GitHub Copilot API.
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

// TokenHealth reports the state of the cached Copilot token.
type TokenHealth interface {
	// TokenValid reports whether a usable token is currently cached.
	TokenValid() bool
	// TokenExpiry returns when that token expires, or the zero time when none
	// has been fetched.
	TokenExpiry() time.Time
}

// Config carries the dependencies of a Server.
type Config struct {
	Logger  *slog.Logger
	Copilot CopilotCaller
	Catalog *copilot.Catalog
	// Tokens is optional; when set, /health and /status reflect token state.
	Tokens TokenHealth
	// AccountType is the resolved Copilot tier, reported by /status.
	AccountType string
}

// Server handles the Anthropic- and OpenAI-compatible routes.
type Server struct {
	logger      *slog.Logger
	copilot     CopilotCaller
	catalog     *copilot.Catalog
	tokens      TokenHealth
	accountType string
	startedAt   time.Time
	activity    activity
}

// New builds a Server.
func New(cfg Config) *Server {
	return &Server{
		logger:      cfg.Logger,
		copilot:     cfg.Copilot,
		catalog:     cfg.Catalog,
		tokens:      cfg.Tokens,
		accountType: cfg.AccountType,
		startedAt:   time.Now(),
	}
}

// Handler returns the routed HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/messages", s.handleMessages)
	mux.HandleFunc("POST /anthropic/v1/messages", s.handleMessages)
	mux.HandleFunc("POST /v1/messages/count_tokens", s.handleCountTokens)
	mux.HandleFunc("POST /anthropic/v1/messages/count_tokens", s.handleCountTokens)
	mux.HandleFunc("GET /v1/models", s.handleModels)
	mux.HandleFunc("GET /anthropic/v1/models", s.handleAnthropicModels)
	mux.HandleFunc("POST /v1/responses", s.handleResponses)
	mux.HandleFunc("POST /openai/v1/responses", s.handleResponses)
	mux.HandleFunc("POST /v1/chat/completions", s.handleChatCompletions)
	mux.HandleFunc("POST /openai/v1/chat/completions", s.handleChatCompletions)
	mux.HandleFunc("GET /openai/v1/models", s.handleOpenAIModels)
	mux.HandleFunc("POST /api/event_logging", s.handleEventLogging)
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /status", s.handleStatus)
	mux.HandleFunc("GET /{$}", s.handleRoot)
	return s.logRequests(trimTrailingSlash(mux))
}

func (s *Server) handleRoot(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("copilot-claude-proxy is running (Anthropic and OpenAI routes)\n"))
}

type healthResponse struct {
	Status          string `json:"status"`
	Models          int    `json:"models"`
	AnthropicModels int    `json:"anthropic_models"`
	OpenAIModels    int    `json:"openai_models"`
	TokenValid      bool   `json:"token_valid"`
}

// handleHealth reports whether the proxy can actually serve traffic: a valid
// Copilot token must be cached and the catalog must contain at least one
// model usable through some route. Anything less is degraded and answers 503
// so monitors notice. The per-dialect counts let a monitor that needs one
// specific API apply a stricter check.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	models := s.catalog.Models()
	counts := countByDialect(models)
	tokenValid := s.tokens == nil || s.tokens.TokenValid()

	status, code := "ok", http.StatusOK
	if !tokenValid || counts.servable() == 0 {
		status, code = "degraded", http.StatusServiceUnavailable
	}
	writeJSON(w, code, healthResponse{
		Status:          status,
		Models:          len(models),
		AnthropicModels: counts.anthropic,
		OpenAIModels:    counts.openAI,
		TokenValid:      tokenValid,
	})
}

// dialectCounts tallies how many catalog models each wire protocol can serve.
type dialectCounts struct {
	anthropic int
	openAI    int
}

// servable is the number of models reachable through at least one route.
func (c dialectCounts) servable() int {
	return c.anthropic + c.openAI
}

func countByDialect(models []copilot.Model) dialectCounts {
	var counts dialectCounts
	for _, model := range models {
		if model.SupportsAnthropicMessages() {
			counts.anthropic++
		}
		if model.SupportsOpenAI() {
			counts.openAI++
		}
	}
	return counts
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
