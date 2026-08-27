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
	Session *copilot.Session
	Host    string
	Port    int
	// Ready is optional; when set it is called once the listener is bound and
	// the catalog is loaded, with the address the listener bound, so callers
	// can start clients without polling. With Port 0 the address carries the
	// ephemeral port the OS assigned.
	Ready func(addr net.Addr)
}

// Run serves the proxy until the context is canceled, keeping the Copilot
// token and model catalog fresh in the background and shutting down
// gracefully.
func Run(ctx context.Context, cfg RunConfig) error {
	logger := cfg.Logger
	session := cfg.Session

	if refreshErr := session.Catalog.Refresh(ctx); refreshErr != nil {
		logger.WarnContext(ctx, "initial model catalog fetch failed, retrying in the background",
			"error", refreshErr)
	} else {
		logCatalogSummary(ctx, logger, session.Catalog)
	}

	go session.Tokens.Run(ctx)
	go session.Catalog.Run(ctx, copilot.ModelRefreshInterval)

	proxy := New(Config{
		Logger:      logger,
		Copilot:     session.Client,
		Catalog:     session.Catalog,
		Tokens:      session.Tokens,
		AccountType: string(session.AccountType),
	})

	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           proxy.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		// No WriteTimeout: SSE responses stream for minutes.
	}

	// Bind before serving so an address already in use is reported here
	// instead of asynchronously, and so Ready means "connections accepted".
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}

	logger.InfoContext(ctx, "proxy listening", "url", "http://"+listener.Addr().String())
	logger.InfoContext(ctx, "configure claude code with: copilot-claude-proxy setup")
	if cfg.Ready != nil {
		cfg.Ready(listener.Addr())
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- httpServer.Serve(listener) }()

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
