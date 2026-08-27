package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/fabriziosestito/copilot-claude-proxy/internal/copilot"
)

const (
	readHeaderTimeout = 10 * time.Second
	// idleTimeout must comfortably exceed typical LLM stream pauses.
	idleTimeout     = 5 * time.Minute
	shutdownTimeout = 5 * time.Second
)

// RunConfig carries the dependencies of Run.
type RunConfig struct {
	Logger  *slog.Logger
	Pool    *copilot.AccountPool
	Catalog *copilot.Catalog
	Host    string
	Port    int
}

// Run serves the proxy until the context is canceled, keeping the Copilot
// token and model catalog fresh in the background and shutting down
// gracefully.
func Run(ctx context.Context, cfg RunConfig) error {
	logger := cfg.Logger
	pool := cfg.Pool
	catalog := cfg.Catalog

	if refreshErr := catalog.Refresh(ctx); refreshErr != nil {
		logger.WarnContext(ctx, "initial model catalog fetch failed, retrying in the background",
			"error", refreshErr)
	} else {
		logCatalogSummary(ctx, logger, catalog)
	}

	for _, account := range pool.Sessions() {
		go account.Session.Tokens.Run(ctx)
	}
	go catalog.Run(ctx, copilot.ModelRefreshInterval)

	proxy := New(Config{
		Logger:   logger,
		Copilot:  pool,
		Catalog:  catalog,
		Tokens:   pool,
		Stats:    pool,
		Accounts: pool,
	})

	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           proxy.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		// No WriteTimeout: SSE responses stream for minutes.
	}

	logger.InfoContext(ctx, "proxy listening", "url", "http://"+addr)
	logger.InfoContext(ctx, "configure claude code with: copilot-claude-proxy setup")

	serveErr := make(chan error, 1)
	go func() { serveErr <- httpServer.ListenAndServe() }()

	select {
	case serveFailure := <-serveErr:
		return fmt.Errorf("server failed: %w", serveFailure)
	case <-ctx.Done():
	}

	logger.InfoContext(ctx, "shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if shutdownErr := httpServer.Shutdown(shutdownCtx); shutdownErr != nil {
		// Streams still running after the grace period get cut.
		if closeErr := httpServer.Close(); closeErr != nil {
			return errors.Join(shutdownErr, closeErr)
		}
	}
	return nil
}

func logCatalogSummary(ctx context.Context, logger *slog.Logger, catalog *copilot.Catalog) {
	models := catalog.Models()
	anthropic := 0
	for _, model := range models {
		if model.SupportsAnthropicMessages() {
			anthropic++
		}
	}
	logger.InfoContext(ctx, "model catalog loaded",
		"models", len(models), "anthropic_models", anthropic)
}
