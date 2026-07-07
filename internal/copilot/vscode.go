package copilot

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

const vscodeFetchTimeout = 5 * time.Second

// LatestVSCodeVersion returns the current VS Code release tag, falling back to
// a known-good version when the lookup fails. The version is only used to
// populate the Editor-Version header.
func LatestVSCodeVersion(ctx context.Context, client *http.Client, logger *slog.Logger) string {
	ctx, cancel := context.WithTimeout(ctx, vscodeFetchTimeout)
	defer cancel()

	url := GitHubAPIBaseURL + "/repos/microsoft/vscode/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fallbackVSCodeVersion
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		logger.DebugContext(ctx, "vscode version lookup failed, using fallback",
			"fallback", fallbackVSCodeVersion, "error", err)
		return fallbackVSCodeVersion
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		logger.DebugContext(ctx, "vscode version lookup returned unexpected status, using fallback",
			"status", resp.StatusCode, "fallback", fallbackVSCodeVersion)
		return fallbackVSCodeVersion
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if decodeErr := json.NewDecoder(resp.Body).Decode(&release); decodeErr != nil ||
		release.TagName == "" {
		return fallbackVSCodeVersion
	}
	return release.TagName
}
