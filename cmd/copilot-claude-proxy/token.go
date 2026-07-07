package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/fabrizio/copilot-claude-proxy/internal/auth"
	"github.com/fabrizio/copilot-claude-proxy/internal/copilot"
	"github.com/fabrizio/copilot-claude-proxy/internal/storage"
)

// githubToken returns the first available token: flag/env, the system
// keyring, or a fresh interactive device flow (which persists the result).
func githubToken(
	ctx context.Context,
	cmd *cli.Command,
	httpClient *http.Client,
	logger *slog.Logger,
) (string, error) {
	if token := strings.TrimSpace(cmd.String("github-token")); token != "" {
		return token, nil
	}

	store := storage.NewTokenStore()
	stored, err := store.Load()
	if err != nil {
		return "", err
	}
	if stored != "" {
		logger.DebugContext(ctx, "github token loaded from the system keyring")
		return stored, nil
	}

	logger.InfoContext(ctx, "no github token found, starting device authorization")
	return auth.Login(ctx, httpClient, store, logger, os.Stdout)
}

// connect reads the shared connection flags and establishes a Copilot
// session for the networked commands.
func connect(ctx context.Context, cmd *cli.Command) (*copilot.Session, *slog.Logger, error) {
	logger := newLogger(cmd.Bool("verbose"))
	httpClient := &http.Client{}

	token, err := githubToken(ctx, cmd, httpClient, logger)
	if err != nil {
		return nil, nil, err
	}

	session, err := copilot.Connect(ctx, copilot.SessionConfig{
		HTTPClient:  httpClient,
		Logger:      logger,
		GitHubToken: token,
		AccountType: cmd.String("account-type"),
		ModelMap:    cmd.StringMap("model-map"),
	})
	if err != nil {
		return nil, nil, err
	}
	return session, logger, nil
}
