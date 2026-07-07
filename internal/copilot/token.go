package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

const (
	// tokenExpiryLeeway refreshes tokens slightly before their expiry.
	tokenExpiryLeeway = 60 * time.Second
	// tokenRefreshMinimum is the shortest pause between background refreshes.
	tokenRefreshMinimum = 60 * time.Second
	// defaultRefreshInterval applies when the API omits refresh_in.
	defaultRefreshInterval = 30 * time.Minute
	// tokenFailureRetryDelay schedules the next background attempt after the
	// retry budget of a refresh is exhausted.
	tokenFailureRetryDelay = 5 * time.Minute

	tokenRetryAttempts  = 3
	tokenRetryBaseDelay = time.Second
	tokenRetryMaxDelay  = 30 * time.Second

	// tokenFetchTimeout bounds a single token fetch attempt: the shared HTTP
	// client has no timeout (it also serves unbounded SSE streams), so without
	// it a hung connection would stall a refresh indefinitely.
	tokenFetchTimeout = 30 * time.Second
)

// errEmptyCopilotToken signals a well-formed response without a token.
var errEmptyCopilotToken = errors.New("copilot token response contained no token")

// TokenManagerConfig carries the dependencies of a TokenManager.
type TokenManagerConfig struct {
	HTTPClient    *http.Client
	Logger        *slog.Logger
	GitHubToken   string
	VSCodeVersion string
	// APIBaseURL overrides the GitHub API origin; empty selects the default.
	APIBaseURL string
}

// TokenManager exchanges a GitHub OAuth token for short-lived Copilot bearer
// tokens and keeps them fresh.
type TokenManager struct {
	httpClient    *http.Client
	logger        *slog.Logger
	githubToken   string
	vscodeVersion string
	apiBaseURL    string

	// refreshMu serializes network refreshes. It is never held together with
	// mu, so a slow or hung fetch cannot block readers of the cached state.
	refreshMu sync.Mutex

	mu        sync.Mutex
	token     string
	expiresAt time.Time
	refreshIn time.Duration
}

// NewTokenManager returns a manager that fetches tokens lazily; call Run to
// enable proactive background refresh.
func NewTokenManager(cfg TokenManagerConfig) *TokenManager {
	baseURL := cfg.APIBaseURL
	if baseURL == "" {
		baseURL = GitHubAPIBaseURL
	}
	return &TokenManager{
		httpClient:    cfg.HTTPClient,
		logger:        cfg.Logger,
		githubToken:   cfg.GitHubToken,
		vscodeVersion: cfg.VSCodeVersion,
		apiBaseURL:    baseURL,
	}
}

// Token returns a valid Copilot token, refreshing it first when the cached
// one is missing or about to expire.
func (m *TokenManager) Token(ctx context.Context) (string, error) {
	if token, ok := m.cached(); ok {
		return token, nil
	}
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	// Another caller may have refreshed while this one waited for the lock.
	if token, ok := m.cached(); ok {
		return token, nil
	}
	return m.doRefresh(ctx)
}

// Refresh discards the cached token and fetches a new one. It is used both by
// the background loop and reactively after upstream 401 responses.
func (m *TokenManager) Refresh(ctx context.Context) (string, error) {
	m.mu.Lock()
	rejected := m.token
	m.mu.Unlock()

	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	// Concurrent 401s coalesce: if another caller already replaced the token
	// this Refresh was called about, reuse it instead of fetching again.
	if token, ok := m.cached(); ok && token != rejected {
		return token, nil
	}
	return m.doRefresh(ctx)
}

// TokenValid reports whether a non-expired token is cached, without blocking
// on any in-flight refresh.
func (m *TokenManager) TokenValid() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.token != "" && time.Now().Before(m.expiresAt)
}

// cached returns the token when it is present and not about to expire.
func (m *TokenManager) cached() (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.token != "" && time.Now().Add(tokenExpiryLeeway).Before(m.expiresAt) {
		return m.token, true
	}
	return "", false
}

// Run refreshes the token in the background until ctx is canceled.
func (m *TokenManager) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(m.nextRefreshDelay()):
		}
		if _, err := m.Refresh(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			m.logger.WarnContext(ctx, "background copilot token refresh failed", "error", err)
		}
	}
}

// doRefresh fetches a new token and stores it. The caller must hold
// refreshMu (and not mu).
func (m *TokenManager) doRefresh(ctx context.Context) (string, error) {
	var lastErr error
	for attempt := range tokenRetryAttempts {
		if attempt > 0 {
			if err := sleepContext(ctx, retryDelay(attempt)); err != nil {
				return "", err
			}
		}
		fetched, err := m.fetch(ctx)
		if err == nil {
			m.mu.Lock()
			m.token = fetched.Token
			m.expiresAt = time.Unix(fetched.ExpiresAt, 0)
			m.refreshIn = time.Duration(fetched.RefreshIn) * time.Second
			if m.refreshIn <= 0 {
				m.refreshIn = defaultRefreshInterval
			}
			expiresAt := m.expiresAt
			m.mu.Unlock()
			m.logger.DebugContext(ctx, "copilot token refreshed", "expires_at", expiresAt)
			return fetched.Token, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
		m.logger.DebugContext(ctx, "copilot token fetch attempt failed",
			"attempt", attempt+1, "error", err)
	}
	return "", fmt.Errorf("fetch copilot token: %w", lastErr)
}

func (m *TokenManager) nextRefreshDelay() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.token == "" {
		return tokenFailureRetryDelay
	}
	return max(m.refreshIn-tokenExpiryLeeway, tokenRefreshMinimum)
}

type copilotTokenResponse struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
	RefreshIn int64  `json:"refresh_in"`
}

func (m *TokenManager) fetch(ctx context.Context) (copilotTokenResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, tokenFetchTimeout)
	defer cancel()

	url := m.apiBaseURL + "/copilot_internal/v2/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return copilotTokenResponse{}, fmt.Errorf("build token request: %w", err)
	}
	req.Header = githubHeaders(m.githubToken, m.vscodeVersion, internalAPIVersion)

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return copilotTokenResponse{}, fmt.Errorf("request copilot token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return copilotTokenResponse{}, fmt.Errorf("copilot token endpoint returned %s: %s",
			resp.Status, readBodySnippet(resp.Body))
	}

	var token copilotTokenResponse
	if decodeErr := json.NewDecoder(resp.Body).Decode(&token); decodeErr != nil {
		return copilotTokenResponse{}, fmt.Errorf("decode copilot token response: %w", decodeErr)
	}
	if token.Token == "" {
		return copilotTokenResponse{}, errEmptyCopilotToken
	}
	return token, nil
}

func retryDelay(attempt int) time.Duration {
	return min(tokenRetryBaseDelay<<(attempt-1), tokenRetryMaxDelay)
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf("wait for retry: %w", ctx.Err())
	case <-time.After(delay):
		return nil
	}
}
