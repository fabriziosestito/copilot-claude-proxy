package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/fabriziosestito/copilot-claude-proxy/internal/auth"
	"github.com/fabriziosestito/copilot-claude-proxy/internal/copilot"
	"github.com/fabriziosestito/copilot-claude-proxy/internal/storage"
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
	accounts, err := store.Accounts()
	if err != nil {
		return "", err
	}
	if len(accounts) > 0 {
		logger.DebugContext(ctx, "github token loaded from the system keyring",
			"account", accounts[0].Name)
		return accounts[0].Token, nil
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

// connectAccountPool connects every stored GitHub account. An explicit token
// keeps the existing single-account override behavior.
func connectAccountPool(
	ctx context.Context,
	cmd *cli.Command,
) (*copilot.AccountPool, *copilot.Session, *slog.Logger, error) {
	if strings.TrimSpace(cmd.String("github-token")) != "" {
		session, logger, err := connect(ctx, cmd)
		if err != nil {
			return nil, nil, nil, err
		}
		pool, err := copilot.NewAccountPool([]copilot.AccountSession{{
			Name: "token-override", Session: session,
		}})
		return pool, session, logger, err
	}

	logger := newLogger(cmd.Bool("verbose"))
	httpClient := &http.Client{}
	store := storage.NewTokenStore()
	accounts, err := store.Accounts()
	if err != nil {
		return nil, nil, nil, err
	}
	if len(accounts) == 0 {
		logger.InfoContext(ctx, "no github token found, starting device authorization")
		if _, loginErr := auth.Login(ctx, httpClient, store, logger, os.Stdout); loginErr != nil {
			return nil, nil, nil, loginErr
		}
		accounts, err = store.Accounts()
		if err != nil {
			return nil, nil, nil, err
		}
	}

	connected := make([]copilot.AccountSession, 0, len(accounts))
	var connectionErrors []error
	for _, account := range accounts {
		session, connectErr := copilot.Connect(ctx, copilot.SessionConfig{
			HTTPClient:  httpClient,
			Logger:      logger,
			GitHubToken: account.Token,
			AccountType: cmd.String("account-type"),
			ModelMap:    cmd.StringMap("model-map"),
		})
		if connectErr != nil {
			logger.WarnContext(ctx, "skipping unavailable Copilot account",
				"account", account.Name, "error", connectErr)
			connectionErrors = append(connectionErrors,
				fmt.Errorf("account %s: %w", account.Name, connectErr))
			continue
		}
		connected = append(connected, copilot.AccountSession{Name: account.Name, Session: session})
	}
	if len(connected) == 0 {
		return nil, nil, nil, fmt.Errorf("no stored Copilot account could connect: %w",
			errors.Join(connectionErrors...))
	}
	pool, err := copilot.NewAccountPool(connected)
	if err != nil {
		return nil, nil, nil, err
	}
	return pool, connected[0].Session, logger, nil
}
