// Package copilot implements a minimal GitHub Copilot API client: token
// acquisition and refresh, model catalog access, and authenticated request
// forwarding with the editor headers Copilot expects.
package copilot

import (
	"crypto/rand"
	"fmt"
	"net/http"
)

// GitHub OAuth and API endpoints.
const (
	// GitHubBaseURL hosts the OAuth device flow endpoints.
	GitHubBaseURL = "https://github.com"
	// GitHubAPIBaseURL hosts the REST API, including Copilot internal endpoints.
	GitHubAPIBaseURL = "https://api.github.com"

	// GitHubClientID is the OAuth app of VS Code, which is entitled to Copilot.
	GitHubClientID = "Iv1.b507a08c87ecfe98"
	// GitHubAppScopes is the OAuth scope requested during the device flow.
	GitHubAppScopes = "read:user"
)

// Editor identity presented to the Copilot API. Mirrors VS Code Copilot Chat.
const (
	chatVersion         = "0.38.0"
	editorPluginVersion = "copilot-chat/" + chatVersion
	userAgent           = "GitHubCopilotChat/" + chatVersion
	integrationID       = "vscode-chat"

	githubAPIVersion   = "2022-11-28"
	internalAPIVersion = "2025-04-01"
	copilotAPIVersion  = "2025-05-01"

	// AnthropicVersion is sent as the anthropic-version header on /v1/messages.
	AnthropicVersion = "2023-06-01"

	fallbackVSCodeVersion = "1.104.3"

	userAgentLibrary = "electron-fetch"
)

// AccountType selects the Copilot API endpoint tier.
type AccountType string

// Supported Copilot account tiers.
const (
	AccountIndividual AccountType = "individual"
	AccountBusiness   AccountType = "business"
	AccountEnterprise AccountType = "enterprise"
)

// BaseURL returns the Copilot API origin for the account tier.
func (t AccountType) BaseURL() string {
	switch t {
	case AccountBusiness:
		return "https://api.business.githubcopilot.com"
	case AccountEnterprise:
		return "https://api.enterprise.githubcopilot.com"
	case AccountIndividual:
		return "https://api.githubcopilot.com"
	}
	return AccountIndividual.BaseURL()
}

// githubHeaders builds the header set for api.github.com calls.
func githubHeaders(githubToken, vscodeVersion, apiVersion string) http.Header {
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("Accept", "application/json")
	header.Set("Authorization", "token "+githubToken)
	header.Set("Editor-Version", "vscode/"+vscodeVersion)
	header.Set("Editor-Plugin-Version", editorPluginVersion)
	header.Set("User-Agent", userAgent)
	header.Set("X-Github-Api-Version", apiVersion)
	header.Set("X-Vscode-User-Agent-Library-Version", userAgentLibrary)
	return header
}

// UUID v4 layout constants (RFC 4122, section 4.4).
const (
	uuidVersionIndex = 6
	uuidVariantIndex = 8
	uuidVersionMask  = 0x0f
	uuidVersion4Bits = 0x40
	uuidVariantMask  = 0x3f
	uuidVariantBits  = 0x80
)

// newRequestID returns a random UUID v4 string.
func newRequestID() string {
	var raw [16]byte
	// crypto/rand.Read is documented to never return an error.
	_, _ = rand.Read(raw[:])
	raw[uuidVersionIndex] = raw[uuidVersionIndex]&uuidVersionMask | uuidVersion4Bits
	raw[uuidVariantIndex] = raw[uuidVariantIndex]&uuidVariantMask | uuidVariantBits
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
}
