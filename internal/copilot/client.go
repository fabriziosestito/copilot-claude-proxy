package copilot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
)

// maxErrorBodyBytes bounds how much of an upstream error body is quoted in errors.
const maxErrorBodyBytes = 2048

// Intent value describing the interaction type, mirrored from VS Code.
const intentPanel = "conversation-panel"

// ClientConfig carries the dependencies of a Client.
type ClientConfig struct {
	HTTPClient    *http.Client
	Logger        *slog.Logger
	Tokens        *TokenManager
	BaseURL       string
	VSCodeVersion string
}

// Client performs authenticated calls against the Copilot API.
type Client struct {
	httpClient    *http.Client
	logger        *slog.Logger
	tokens        *TokenManager
	baseURL       string
	vscodeVersion string
	interactionID string
}

// NewClient builds a Client; the interaction id identifies this proxy session.
func NewClient(cfg ClientConfig) *Client {
	return &Client{
		httpClient:    cfg.HTTPClient,
		logger:        cfg.Logger,
		tokens:        cfg.Tokens,
		baseURL:       cfg.BaseURL,
		vscodeVersion: cfg.VSCodeVersion,
		interactionID: newRequestID(),
	}
}

// CallOptions describes a single Copilot API request.
type CallOptions struct {
	Method string
	Path   string
	// Body is buffered so the request can be replayed after a token refresh.
	Body []byte
	// Header entries are applied on top of the standard Copilot headers.
	Header http.Header
}

// Do executes the request with the full Copilot header set. On a 401 it
// refreshes the Copilot token once and replays the request.
func (c *Client) Do(ctx context.Context, opts CallOptions) (*http.Response, error) {
	resp, err := c.doOnce(ctx, opts)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}
	_ = resp.Body.Close()
	c.logger.InfoContext(ctx, "copilot api returned 401, refreshing token and retrying")
	if _, refreshErr := c.tokens.Refresh(ctx); refreshErr != nil {
		return nil, refreshErr
	}
	return c.doOnce(ctx, opts)
}

// FetchModels retrieves the model catalog.
func (c *Client) FetchModels(ctx context.Context) ([]Model, error) {
	resp, err := c.Do(ctx, CallOptions{Method: http.MethodGet, Path: "/models"})
	if err != nil {
		return nil, fmt.Errorf("request models: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models endpoint returned %s: %s",
			resp.Status, readBodySnippet(resp.Body))
	}
	var payload modelsResponse
	if decodeErr := json.NewDecoder(resp.Body).Decode(&payload); decodeErr != nil {
		return nil, fmt.Errorf("decode models response: %w", decodeErr)
	}
	return payload.Data, nil
}

func (c *Client) doOnce(ctx context.Context, opts CallOptions) (*http.Response, error) {
	token, err := c.tokens.Token(ctx)
	if err != nil {
		return nil, err
	}

	var body io.Reader
	if opts.Body != nil {
		body = bytes.NewReader(opts.Body)
	}
	req, err := http.NewRequestWithContext(ctx, opts.Method, c.baseURL+opts.Path, body)
	if err != nil {
		return nil, fmt.Errorf("build copilot request: %w", err)
	}

	req.Header = c.baseHeaders(token)
	for key, values := range opts.Header {
		req.Header.Del(key)
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call copilot api: %w", err)
	}
	return resp, nil
}

// baseHeaders builds the standard Copilot header set. X-Initiator is
// deliberately not sent: the proxy cannot tell user-typed prompts from
// agent-loop turns, and VS Code's own Anthropic proxy omits the header rather
// than guess from conversation history.
func (c *Client) baseHeaders(token string) http.Header {
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
	header.Set("Content-Type", "application/json")
	header.Set("Copilot-Integration-Id", integrationID)
	header.Set("Editor-Version", "vscode/"+c.vscodeVersion)
	header.Set("Editor-Plugin-Version", editorPluginVersion)
	header.Set("User-Agent", userAgent)
	header.Set("Openai-Intent", intentPanel)
	header.Set("X-Github-Api-Version", copilotAPIVersion)
	header.Set("X-Request-Id", newRequestID())
	header.Set("X-Interaction-Id", c.interactionID)
	header.Set("X-Interaction-Type", intentPanel)
	header.Set("X-Vscode-User-Agent-Library-Version", userAgentLibrary)
	return header
}

// readBodySnippet returns a bounded prefix of a response body for error messages.
func readBodySnippet(body io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(body, maxErrorBodyBytes))
	if err != nil || len(data) == 0 {
		return "<no body>"
	}
	return string(data)
}
