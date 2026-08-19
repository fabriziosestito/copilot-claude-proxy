package auth

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/fabriziosestito/copilot-claude-proxy/internal/storage"
)

// Login runs the interactive GitHub device authorization and stores the
// resulting token in the system keyring. Progress and instructions for the
// user are written to out.
func Login(
	ctx context.Context,
	httpClient *http.Client,
	store *storage.TokenStore,
	logger *slog.Logger,
	out io.Writer,
) (string, error) {
	flow := NewFlow(FlowConfig{HTTPClient: httpClient})
	authorization, err := flow.Start(ctx)
	if err != nil {
		return "", err
	}

	fmt.Fprintf(out, "\nOpen %s and enter code: %s\n",
		authorization.VerificationURI, authorization.UserCode)
	fmt.Fprintln(out, "Waiting for authorization...")
	openBrowser(ctx, authorization.VerificationURI, logger)

	token, err := flow.Wait(ctx, authorization)
	if err != nil {
		return "", err
	}

	login, err := flow.UserLogin(ctx, token)
	if err != nil {
		return "", fmt.Errorf("token validation failed: %w", err)
	}
	fmt.Fprintf(out, "Authenticated as %s.\n", login)

	if saveErr := store.SaveAccount(login, token); saveErr != nil {
		return "", saveErr
	}
	fmt.Fprintf(out, "Token saved to the system keyring as %s.\n", login)
	return token, nil
}
