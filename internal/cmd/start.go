package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/fabrizio/copilot-claude-proxy/internal/copilot"
	"github.com/fabrizio/copilot-claude-proxy/internal/server"
)

const (
	readHeaderTimeout = 10 * time.Second
	// idleTimeout must comfortably exceed typical LLM stream pauses.
	idleTimeout     = 5 * time.Minute
	shutdownTimeout = 5 * time.Second
)

func newStartCommand() *cli.Command {
	return &cli.Command{
		Name:  "start",
		Usage: "Run the Anthropic-compatible proxy server",
		Flags: []cli.Flag{
			portFlag(),
			hostFlag(),
			accountTypeFlag(),
			githubTokenFlag(),
			modelMapFlag(),
			verboseFlag(),
		},
		Action: runStart,
	}
}

func runStart(ctx context.Context, cmd *cli.Command) error {
	application, err := bootstrap(ctx, cmd)
	if err != nil {
		return err
	}
	logger := application.logger

	if refreshErr := application.catalog.Refresh(ctx); refreshErr != nil {
		logger.WarnContext(ctx, "initial model catalog fetch failed, retrying in the background",
			"error", refreshErr)
	} else {
		logCatalogSummary(ctx, application)
	}

	go application.tokens.Run(ctx)
	go application.catalog.Run(ctx, copilot.ModelRefreshInterval)

	proxy := server.New(server.Config{
		Logger:  logger,
		Copilot: application.client,
		Catalog: application.catalog,
		Tokens:  application.tokens,
	})

	addr := net.JoinHostPort(cmd.String("host"), strconv.Itoa(cmd.Int("port")))
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

func logCatalogSummary(ctx context.Context, application *app) {
	models := application.catalog.Models()
	anthropic := 0
	for _, model := range models {
		if model.SupportsAnthropicMessages() {
			anthropic++
		}
	}
	application.logger.InfoContext(ctx, "model catalog loaded",
		"models", len(models), "anthropic_models", anthropic)
}
