// Package claudecode generates Claude Code configuration that points it at
// the proxy: the env block in ~/.claude/settings.json and the onboarding flag
// in ~/.claude.json. Existing configuration is merged with, never clobbered.
package claudecode

import (
	"strconv"
	"strings"

	"github.com/fabrizio/copilot-claude-proxy/internal/copilot"
)

// Claude Code enables its 1M-context client path only when ANTHROPIC_MODEL
// ends with "[1m]". The band edges bracket models advertising ~1M input
// tokens; models outside the band keep the default 200K strategy.
const (
	oneMillionBandLow  = 800_000
	oneMillionBandHigh = 1_500_000
	oneMillionSuffix   = "[1m]"
)

// defaultAuthToken is a placeholder: the proxy does not authenticate clients,
// but Claude Code refuses to start without a token value.
const defaultAuthToken = "copilot-claude-proxy"

// deprecatedEnvKey is no longer read by Claude Code and is removed on setup.
const deprecatedEnvKey = "ANTHROPIC_SMALL_FAST_MODEL"

// autocompactDefaultPercent leaves a margin before prompts exceed the model limit.
const autocompactDefaultPercent = "85"

// EnvConfig describes the desired Claude Code environment.
type EnvConfig struct {
	// ServerURL is the base URL of this proxy.
	ServerURL string
	// Model serves the main and sonnet roles.
	Model copilot.Model
	// SmallModel serves the haiku role.
	SmallModel copilot.Model
	// WithExtras adds opinionated tuning vars (telemetry off, auto-compact).
	WithExtras bool
}

// BuildEnv computes the proposed env block. Keys the user already set are
// carried over untouched (minus deprecated ones); a customized auth token is
// preserved.
func BuildEnv(cfg EnvConfig, existing map[string]string) map[string]string {
	env := make(map[string]string, len(existing))
	for key, value := range existing {
		if key == deprecatedEnvKey {
			continue
		}
		env[key] = value
	}

	mainModel := withOneMillionSuffix(cfg.Model.ID, cfg.Model.Capabilities.Limits.MaxPromptTokens)
	smallModel := withOneMillionSuffix(
		cfg.SmallModel.ID, cfg.SmallModel.Capabilities.Limits.MaxPromptTokens)

	authToken := existing["ANTHROPIC_AUTH_TOKEN"]
	if authToken == "" {
		authToken = defaultAuthToken
	}

	env["ANTHROPIC_BASE_URL"] = cfg.ServerURL
	env["ANTHROPIC_AUTH_TOKEN"] = authToken
	env["ANTHROPIC_MODEL"] = mainModel
	env["ANTHROPIC_DEFAULT_SONNET_MODEL"] = mainModel
	// The opus role maps to the main model too; without it Claude Code falls
	// back to its built-in Anthropic opus ID, escaping the proxy's mapping.
	env["ANTHROPIC_DEFAULT_OPUS_MODEL"] = mainModel
	env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = smallModel

	if cfg.WithExtras {
		applyExtras(env, existing, cfg.Model)
	}
	return env
}

// applyExtras layers the opt-in recommendations on top of the essential env.
func applyExtras(env, existing map[string]string, model copilot.Model) {
	env["DISABLE_NON_ESSENTIAL_MODEL_CALLS"] = "1"
	env["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
	env["CLAUDE_CODE_ENABLE_TELEMETRY"] = "0"
	// The attribution header breaks prompt caching on non-Anthropic gateways.
	env["CLAUDE_CODE_ATTRIBUTION_HEADER"] = "0"

	percent := existing["CLAUDE_AUTOCOMPACT_PCT_OVERRIDE"]
	if percent == "" {
		percent = autocompactDefaultPercent
	}
	env["CLAUDE_AUTOCOMPACT_PCT_OVERRIDE"] = percent

	if window := model.Capabilities.Limits.MaxPromptTokens; window > 0 {
		env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] = strconv.Itoa(window)
	}
}

// withOneMillionSuffix appends "[1m]" to model IDs whose advertised prompt
// limit falls in the 1M tier. The proxy resolves the suffix back to the
// upstream "-1m" form.
func withOneMillionSuffix(modelID string, maxPromptTokens int) string {
	if strings.HasSuffix(modelID, oneMillionSuffix) {
		return modelID
	}
	if maxPromptTokens <= oneMillionBandLow || maxPromptTokens >= oneMillionBandHigh {
		return modelID
	}
	// Catalog IDs already carrying the upstream "-1m" form are rewritten to
	// the bracket form; appending to them would resolve to "<id>-1m-1m".
	return strings.TrimSuffix(modelID, "-1m") + oneMillionSuffix
}
