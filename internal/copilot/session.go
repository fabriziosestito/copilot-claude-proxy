package copilot

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
)

// SessionConfig carries everything needed to establish a Copilot session.
// All fields are plain values: resolving them (flags, keyring, device flow)
// is the caller's concern.
type SessionConfig struct {
	HTTPClient *http.Client
	Logger     *slog.Logger
	// GitHubToken is an OAuth token entitled to the Copilot API.
	GitHubToken string
	// AccountType is the Copilot tier: "auto" (or empty) probes the account,
	// otherwise one of individual, business, or enterprise.
	AccountType string
	// ModelMap adds extra model aliases (alias -> model ID) to the catalog.
	ModelMap map[string]string
}

// Session bundles the authenticated Copilot dependencies shared by the
// networked commands.
type Session struct {
	Tokens  *TokenManager
	Client  *Client
	Catalog *Catalog
	// AccountType is the tier the endpoint was selected from, after resolving
	// "auto" against the account.
	AccountType AccountType
}

// Connect exchanges the GitHub token for a Copilot token and builds the
// client and model catalog against the endpoint of the account tier.
func Connect(ctx context.Context, cfg SessionConfig) (*Session, error) {
	vscodeVersion := LatestVSCodeVersion(ctx, cfg.HTTPClient, cfg.Logger)

	accountType, err := resolveAccountType(ctx, cfg, vscodeVersion)
	if err != nil {
		return nil, err
	}
	cfg.Logger.InfoContext(ctx, "copilot endpoint selected",
		"account_type", accountType, "base_url", accountType.BaseURL())

	tokens := NewTokenManager(TokenManagerConfig{
		HTTPClient:    cfg.HTTPClient,
		Logger:        cfg.Logger,
		GitHubToken:   cfg.GitHubToken,
		VSCodeVersion: vscodeVersion,
	})
	if _, tokenErr := tokens.Token(ctx); tokenErr != nil {
		return nil, fmt.Errorf(
			"copilot token exchange failed (is Copilot enabled for this account?): %w", tokenErr)
	}

	client := NewClient(ClientConfig{
		HTTPClient:    cfg.HTTPClient,
		Logger:        cfg.Logger,
		Tokens:        tokens,
		BaseURL:       accountType.BaseURL(),
		VSCodeVersion: vscodeVersion,
	})

	return &Session{
		Tokens:      tokens,
		Client:      client,
		Catalog:     NewCatalog(client, cfg.Logger, cfg.ModelMap),
		AccountType: accountType,
	}, nil
}

func resolveAccountType(
	ctx context.Context,
	cfg SessionConfig,
	vscodeVersion string,
) (AccountType, error) {
	switch value := strings.ToLower(strings.TrimSpace(cfg.AccountType)); value {
	case "", "auto":
		return DetectAccountType(ctx, cfg.HTTPClient, cfg.GitHubToken, vscodeVersion, cfg.Logger), nil
	case string(AccountIndividual):
		return AccountIndividual, nil
	case string(AccountBusiness):
		return AccountBusiness, nil
	case string(AccountEnterprise):
		return AccountEnterprise, nil
	default:
		return "", fmt.Errorf(
			"invalid account type %q (expected auto, individual, business, or enterprise)", value)
	}
}
