package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/fabrizio/copilot-claude-proxy/internal/auth"
	"github.com/fabrizio/copilot-claude-proxy/internal/copilot"
	"github.com/fabrizio/copilot-claude-proxy/internal/storage"
)

const (
	defaultPort = 4141
	defaultHost = "127.0.0.1"
)

// app bundles the shared dependencies of the networked commands.
type app struct {
	logger  *slog.Logger
	http    *http.Client
	tokens  *copilot.TokenManager
	client  *copilot.Client
	catalog *copilot.Catalog
}

// bootstrap authenticates with GitHub, obtains a Copilot token, and builds
// the Copilot client and model catalog shared by the commands.
func bootstrap(ctx context.Context, cmd *cli.Command) (*app, error) {
	logger := newLogger(cmd.Bool("verbose"))
	httpClient := &http.Client{}

	githubToken, err := resolveGitHubToken(ctx, cmd, httpClient, logger)
	if err != nil {
		return nil, err
	}

	vscodeVersion := copilot.LatestVSCodeVersion(ctx, httpClient, logger)
	accountType, err := resolveAccountType(ctx, cmd, httpClient, githubToken, vscodeVersion, logger)
	if err != nil {
		return nil, err
	}
	logger.InfoContext(ctx, "copilot endpoint selected",
		"account_type", accountType, "base_url", accountType.BaseURL())

	tokens := copilot.NewTokenManager(copilot.TokenManagerConfig{
		HTTPClient:    httpClient,
		Logger:        logger,
		GitHubToken:   githubToken,
		VSCodeVersion: vscodeVersion,
	})
	if _, tokenErr := tokens.Token(ctx); tokenErr != nil {
		return nil, fmt.Errorf(
			"copilot token exchange failed (is Copilot enabled for this account?): %w", tokenErr)
	}

	client := copilot.NewClient(copilot.ClientConfig{
		HTTPClient:    httpClient,
		Logger:        logger,
		Tokens:        tokens,
		BaseURL:       accountType.BaseURL(),
		VSCodeVersion: vscodeVersion,
	})
	catalog := copilot.NewCatalog(client, logger, cmd.StringMap("model-map"))

	return &app{
		logger:  logger,
		http:    httpClient,
		tokens:  tokens,
		client:  client,
		catalog: catalog,
	}, nil
}

// resolveGitHubToken returns the first available token: flag/env, the system
// keyring, or a fresh interactive device flow (which persists the result).
func resolveGitHubToken(
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
	return performDeviceFlow(ctx, httpClient, store, logger)
}

// performDeviceFlow runs the interactive GitHub device authorization and
// stores the resulting token in the system keyring.
func performDeviceFlow(
	ctx context.Context,
	httpClient *http.Client,
	store *storage.TokenStore,
	logger *slog.Logger,
) (string, error) {
	flow := auth.NewFlow(auth.FlowConfig{HTTPClient: httpClient})
	authorization, err := flow.Start(ctx)
	if err != nil {
		return "", err
	}

	fmt.Fprintf(os.Stdout, "\nOpen %s and enter code: %s\n",
		authorization.VerificationURI, authorization.UserCode)
	fmt.Fprintln(os.Stdout, "Waiting for authorization...")
	openBrowser(ctx, authorization.VerificationURI, logger)

	token, err := flow.Wait(ctx, authorization)
	if err != nil {
		return "", err
	}

	login, err := flow.UserLogin(ctx, token)
	if err != nil {
		return "", fmt.Errorf("token validation failed: %w", err)
	}
	fmt.Fprintf(os.Stdout, "Authenticated as %s.\n", login)

	if saveErr := store.Save(token); saveErr != nil {
		return "", saveErr
	}
	fmt.Fprintln(os.Stdout, "Token saved to the system keyring.")
	return token, nil
}

func resolveAccountType(
	ctx context.Context,
	cmd *cli.Command,
	httpClient *http.Client,
	githubToken, vscodeVersion string,
	logger *slog.Logger,
) (copilot.AccountType, error) {
	switch value := strings.ToLower(strings.TrimSpace(cmd.String("account-type"))); value {
	case "", "auto":
		return copilot.DetectAccountType(ctx, httpClient, githubToken, vscodeVersion, logger), nil
	case string(copilot.AccountIndividual):
		return copilot.AccountIndividual, nil
	case string(copilot.AccountBusiness):
		return copilot.AccountBusiness, nil
	case string(copilot.AccountEnterprise):
		return copilot.AccountEnterprise, nil
	default:
		return "", fmt.Errorf(
			"invalid account type %q (expected auto, individual, business, or enterprise)", value)
	}
}
