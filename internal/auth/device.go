// Package auth implements the GitHub OAuth device authorization flow
// (RFC 8628) used to obtain a token entitled to the Copilot API.
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/fabriziosestito/copilot-claude-proxy/internal/copilot"
)

const (
	// slowDownIncrement is added to the poll interval on slow_down responses.
	slowDownIncrement = 5 * time.Second
	// defaultPollInterval applies when GitHub omits the interval hint.
	defaultPollInterval = 5 * time.Second

	contentTypeJSON = "application/json"
)

// Terminal device flow outcomes.
var (
	ErrAccessDenied         = errors.New("github authorization was denied")
	ErrAuthorizationExpired = errors.New("github device code expired before authorization")
)

// FlowConfig carries the dependencies of a Flow.
type FlowConfig struct {
	HTTPClient *http.Client
	// BaseURL overrides the github.com origin; empty selects the default.
	BaseURL string
	// APIBaseURL overrides the api.github.com origin; empty selects the default.
	APIBaseURL string
}

// Flow drives the GitHub device authorization flow.
type Flow struct {
	client     *http.Client
	baseURL    string
	apiBaseURL string
}

// NewFlow builds a Flow against github.com unless overridden.
func NewFlow(cfg FlowConfig) *Flow {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = copilot.GitHubBaseURL
	}
	apiBaseURL := cfg.APIBaseURL
	if apiBaseURL == "" {
		apiBaseURL = copilot.GitHubAPIBaseURL
	}
	return &Flow{client: cfg.HTTPClient, baseURL: baseURL, apiBaseURL: apiBaseURL}
}

// DeviceAuthorization is a pending device flow authorization.
type DeviceAuthorization struct {
	// UserCode is entered by the user at VerificationURI.
	UserCode string
	// VerificationURI is the page where the user approves the authorization.
	VerificationURI string

	deviceCode string
	interval   time.Duration
	expiresAt  time.Time
}

// Start requests a device code from GitHub.
func (f *Flow) Start(ctx context.Context) (*DeviceAuthorization, error) {
	request := struct {
		ClientID string `json:"client_id"`
		Scope    string `json:"scope"`
	}{ClientID: copilot.GitHubClientID, Scope: copilot.GitHubAppScopes}

	var response struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int64  `json:"expires_in"`
		Interval        int64  `json:"interval"`
	}
	if err := f.postJSON(ctx, f.baseURL+"/login/device/code", request, &response); err != nil {
		return nil, fmt.Errorf("request device code: %w", err)
	}

	interval := time.Duration(response.Interval) * time.Second
	if interval <= 0 {
		interval = defaultPollInterval
	}
	return &DeviceAuthorization{
		UserCode:        response.UserCode,
		VerificationURI: response.VerificationURI,
		deviceCode:      response.DeviceCode,
		interval:        interval,
		expiresAt:       time.Now().Add(time.Duration(response.ExpiresIn) * time.Second),
	}, nil
}

// oauthError is a definitive OAuth error response from GitHub. Unlike a
// transient transport failure, it terminates the device flow.
type oauthError struct {
	code        string
	description string
}

func (e *oauthError) Error() string {
	return fmt.Sprintf("github authorization failed: %s (%s)", e.code, e.description)
}

// Wait polls GitHub until the user approves the authorization, then returns
// the OAuth access token. Transient poll failures (network blips, gateway
// errors) do not abort the flow: the device code stays valid for minutes
// while the user types it in, so polling continues until it expires.
func (f *Flow) Wait(ctx context.Context, authorization *DeviceAuthorization) (string, error) {
	interval := authorization.interval
	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("wait for authorization: %w", ctx.Err())
		case <-time.After(interval):
		}
		if time.Now().After(authorization.expiresAt) {
			return "", ErrAuthorizationExpired
		}

		token, status, err := f.poll(ctx, authorization.deviceCode)
		if err != nil {
			if isTerminalPollError(err) {
				return "", err
			}
			continue
		}
		switch status {
		case pollSucceeded:
			return token, nil
		case pollPending:
		case pollSlowDown:
			interval += slowDownIncrement
		}
	}
}

// isTerminalPollError reports whether polling must stop: a definitive answer
// from GitHub (denied, expired, or another explicit OAuth error) or a
// canceled context. Everything else is worth retrying.
func isTerminalPollError(err error) bool {
	var oauthErr *oauthError
	return errors.Is(err, ErrAccessDenied) ||
		errors.Is(err, ErrAuthorizationExpired) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.As(err, &oauthErr)
}

// UserLogin returns the login of the token's user, validating the token.
func (f *Flow) UserLogin(ctx context.Context, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.apiBaseURL+"/user", nil)
	if err != nil {
		return "", fmt.Errorf("build user request: %w", err)
	}
	req.Header.Set("Accept", contentTypeJSON)
	req.Header.Set("Authorization", "token "+token)

	resp, err := f.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request user: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github user endpoint returned %s", resp.Status)
	}
	var user struct {
		Login string `json:"login"`
	}
	if decodeErr := json.NewDecoder(resp.Body).Decode(&user); decodeErr != nil {
		return "", fmt.Errorf("decode user response: %w", decodeErr)
	}
	return user.Login, nil
}

type pollStatus int

const (
	pollPending pollStatus = iota
	pollSlowDown
	pollSucceeded
)

func (f *Flow) poll(ctx context.Context, deviceCode string) (string, pollStatus, error) {
	request := struct {
		ClientID   string `json:"client_id"`
		DeviceCode string `json:"device_code"`
		GrantType  string `json:"grant_type"`
	}{
		ClientID:   copilot.GitHubClientID,
		DeviceCode: deviceCode,
		GrantType:  "urn:ietf:params:oauth:grant-type:device_code",
	}

	var response struct {
		AccessToken      string `json:"access_token"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	// RFC 8628 servers deliver error payloads with HTTP 400; GitHub uses 200,
	// but both forms carry the same JSON body and must be parsed, not treated
	// as transport failures.
	err := f.postJSON(ctx, f.baseURL+"/login/oauth/access_token", request, &response,
		http.StatusBadRequest)
	if err != nil {
		return "", pollPending, fmt.Errorf("poll for access token: %w", err)
	}

	switch response.Error {
	case "":
		if response.AccessToken == "" {
			return "", pollPending, errors.New("github returned neither token nor error")
		}
		return response.AccessToken, pollSucceeded, nil
	case "authorization_pending":
		return "", pollPending, nil
	case "slow_down":
		return "", pollSlowDown, nil
	case "expired_token":
		return "", pollPending, ErrAuthorizationExpired
	case "access_denied":
		return "", pollPending, ErrAccessDenied
	default:
		return "", pollPending, &oauthError{
			code:        response.Error,
			description: response.ErrorDescription,
		}
	}
}

// postJSON sends a JSON request and decodes the JSON response. Responses with
// a status other than 200 or one of extraStatuses are errors.
func (f *Flow) postJSON(ctx context.Context, url string, body, out any, extraStatuses ...int) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", contentTypeJSON)
	req.Header.Set("Accept", contentTypeJSON)

	resp, err := f.client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && !slices.Contains(extraStatuses, resp.StatusCode) {
		return fmt.Errorf("unexpected status %s", resp.Status)
	}
	if decodeErr := json.NewDecoder(resp.Body).Decode(out); decodeErr != nil {
		return fmt.Errorf("decode response: %w", decodeErr)
	}
	return nil
}
