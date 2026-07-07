package copilot

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
)

type copilotUserResponse struct {
	CopilotPlan   string `json:"copilot_plan"`
	AccessTypeSKU string `json:"access_type_sku"`
}

// DetectAccountType probes the Copilot subscription of the authenticated user
// and infers the account tier. It falls back to the individual tier when the
// probe fails, which matches the most common setup.
func DetectAccountType(
	ctx context.Context,
	client *http.Client,
	githubToken, vscodeVersion string,
	logger *slog.Logger,
) AccountType {
	url := GitHubAPIBaseURL + "/copilot_internal/user"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return AccountIndividual
	}
	req.Header = githubHeaders(githubToken, vscodeVersion, internalAPIVersion)

	resp, err := client.Do(req)
	if err != nil {
		logger.WarnContext(ctx, "account type probe failed, assuming individual", "error", err)
		return AccountIndividual
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		logger.WarnContext(ctx, "account type probe returned unexpected status, assuming individual",
			"status", resp.StatusCode)
		return AccountIndividual
	}

	var user copilotUserResponse
	if decodeErr := json.NewDecoder(resp.Body).Decode(&user); decodeErr != nil {
		logger.WarnContext(ctx, "account type probe response unreadable, assuming individual",
			"error", decodeErr)
		return AccountIndividual
	}

	plan := strings.ToLower(user.CopilotPlan + " " + user.AccessTypeSKU)
	switch {
	case strings.Contains(plan, "enterprise"):
		return AccountEnterprise
	case strings.Contains(plan, "business"):
		return AccountBusiness
	default:
		return AccountIndividual
	}
}
