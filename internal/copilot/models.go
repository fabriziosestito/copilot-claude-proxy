package copilot

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// ModelRefreshInterval is the default cadence for catalog refreshes.
const ModelRefreshInterval = 10 * time.Minute

// catalogRetryDelay schedules the next attempt after a failed refresh; an
// empty or stale catalog degrades every model resolution, so waiting a full
// interval would be far too long.
const catalogRetryDelay = 30 * time.Second

const (
	anthropicMessagesEndpoint = "/v1/messages"
	anthropicVendor           = "anthropic"
)

// Model describes a Copilot model catalog entry.
type Model struct {
	ID                 string            `json:"id"`
	Name               string            `json:"name"`
	Vendor             string            `json:"vendor"`
	Version            string            `json:"version"`
	Preview            bool              `json:"preview"`
	ModelPickerEnabled bool              `json:"model_picker_enabled"`
	Capabilities       ModelCapabilities `json:"capabilities"`
	SupportedEndpoints []string          `json:"supported_endpoints"`
}

// SupportsAnthropicMessages reports whether Copilot natively serves this model
// through the Anthropic Messages API.
func (m Model) SupportsAnthropicMessages() bool {
	if len(m.SupportedEndpoints) > 0 {
		return slices.Contains(m.SupportedEndpoints, anthropicMessagesEndpoint)
	}
	return strings.EqualFold(m.Vendor, anthropicVendor)
}

// ModelCapabilities carries limits and feature support of a model.
type ModelCapabilities struct {
	Family    string        `json:"family"`
	Type      string        `json:"type"`
	Tokenizer string        `json:"tokenizer"`
	Limits    ModelLimits   `json:"limits"`
	Supports  ModelSupports `json:"supports"`
}

// ModelLimits carries the advertised token limits of a model.
type ModelLimits struct {
	MaxContextWindowTokens int `json:"max_context_window_tokens"`
	MaxOutputTokens        int `json:"max_output_tokens"`
	MaxPromptTokens        int `json:"max_prompt_tokens"`
}

// ModelSupports carries feature flags of a model.
type ModelSupports struct {
	Streaming bool `json:"streaming"`
	ToolCalls bool `json:"tool_calls"`
	Vision    bool `json:"vision"`
}

type modelsResponse struct {
	Data []Model `json:"data"`
}

// ModelFetcher retrieves the model catalog from upstream.
type ModelFetcher interface {
	FetchModels(ctx context.Context) ([]Model, error)
}

// Catalog caches the Copilot model list and resolves client-supplied model
// names (Anthropic style, aliases) to Copilot model IDs.
type Catalog struct {
	fetcher   ModelFetcher
	logger    *slog.Logger
	overrides map[string]string

	dateSuffix  *regexp.Regexp
	versionDash *regexp.Regexp
	bracketTag  *regexp.Regexp

	mu     sync.RWMutex
	models map[string]Model
	order  []string
}

// NewCatalog builds an empty catalog; call Refresh to populate it. The
// overrides map routes requested names (e.g. "haiku") to Copilot model IDs.
func NewCatalog(fetcher ModelFetcher, logger *slog.Logger, overrides map[string]string) *Catalog {
	normalized := make(map[string]string, len(overrides))
	for key, value := range overrides {
		normalized[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
	}
	return &Catalog{
		fetcher:   fetcher,
		logger:    logger,
		overrides: normalized,
		// Anthropic-style date suffix, e.g. claude-sonnet-4-5-20250929.
		dateSuffix: regexp.MustCompile(`-\d{8}$`),
		// Dashed version numbers, e.g. claude-sonnet-4-5 vs claude-sonnet-4.5.
		versionDash: regexp.MustCompile(`(\d)-(\d)`),
		// Claude Code capability tags, e.g. claude-sonnet-4.5[1m].
		bracketTag: regexp.MustCompile(`\[([\w.-]+)\]$`),
		models:     map[string]Model{},
	}
}

// Refresh replaces the cached catalog with a fresh fetch.
func (c *Catalog) Refresh(ctx context.Context) error {
	models, err := c.fetcher.FetchModels(ctx)
	if err != nil {
		return fmt.Errorf("refresh model catalog: %w", err)
	}

	byID := make(map[string]Model, len(models))
	order := make([]string, 0, len(models))
	for _, model := range models {
		if _, exists := byID[model.ID]; exists {
			continue
		}
		byID[model.ID] = model
		order = append(order, model.ID)
	}

	c.mu.Lock()
	c.models = byID
	c.order = order
	c.mu.Unlock()

	c.logger.DebugContext(ctx, "model catalog refreshed", "models", len(order))
	return nil
}

// Run refreshes the catalog periodically until ctx is canceled. While the
// catalog is empty (e.g. the initial fetch failed) or the last refresh
// errored, it retries on a much shorter delay than the regular interval.
func (c *Catalog) Run(ctx context.Context, interval time.Duration) {
	retrying := c.empty()
	for {
		delay := interval
		if retrying {
			delay = min(catalogRetryDelay, interval)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		err := c.Refresh(ctx)
		retrying = err != nil
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			c.logger.WarnContext(ctx, "model catalog refresh failed", "error", err)
		}
	}
}

func (c *Catalog) empty() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.order) == 0
}

// Models returns the cached catalog in upstream order.
func (c *Catalog) Models() []Model {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Model, 0, len(c.order))
	for _, id := range c.order {
		out = append(out, c.models[id])
	}
	return out
}

// Lookup returns the model with the exact given ID.
func (c *Catalog) Lookup(id string) (Model, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	model, ok := c.models[id]
	return model, ok
}

// Resolution is the outcome of resolving a requested model name.
type Resolution struct {
	// ID is the model identifier to send upstream.
	ID string
	// Model is populated when the ID matched the catalog.
	Model Model
	// Known reports whether the catalog recognized the model.
	Known bool
}

// Resolve maps a client-supplied model name to a Copilot model ID. Claude
// Code sends Anthropic-style names while Copilot uses its own IDs, so the
// following rewrites are tried in order, preferring the most literal
// interpretation that exists in the catalog:
//
//   - user-supplied overrides (--model-map aliases) are applied first
//   - Claude Code bracket tags: claude-sonnet-4.5[1m] -> claude-sonnet-4.5-1m
//   - exact ID: claude-sonnet-4.5
//   - Anthropic date suffix stripped: claude-sonnet-4-5-20250929 -> claude-sonnet-4-5
//   - dashed version dotted: claude-sonnet-4-5 -> claude-sonnet-4.5
//
// The bracket tag is extracted before the other rewrites so that tagged dated
// names (claude-sonnet-4-5-20250929[1m]) still get their date stripped; the
// tag-less variants serve as fallbacks when no tagged catalog entry exists.
// Names that match nothing in the catalog pass through untouched, so
// upstream gets the final say.
func (c *Catalog) Resolve(requested string) Resolution {
	name := strings.ToLower(strings.TrimSpace(requested))
	if mapped, ok := c.overrides[name]; ok {
		name = mapped
	}
	var tag string
	if match := c.bracketTag.FindStringSubmatch(name); match != nil {
		tag = match[1]
		name = strings.TrimSuffix(name, match[0])
	}
	for _, candidate := range c.candidates(name, tag) {
		if model, ok := c.Lookup(candidate); ok {
			return Resolution{ID: candidate, Model: model, Known: true}
		}
	}
	if tag != "" {
		name += "-" + tag
	}
	return Resolution{ID: name}
}

func (c *Catalog) candidates(name, tag string) []string {
	base := []string{name}
	if stripped := c.dateSuffix.ReplaceAllString(name, ""); stripped != name {
		base = append(base, stripped)
	}

	// Every base candidate yields itself plus its dotted-version variant.
	const variantsPerCandidate = 2
	variants := make([]string, 0, variantsPerCandidate*len(base))
	seen := make(map[string]struct{}, variantsPerCandidate*len(base))
	for _, candidate := range base {
		for _, variant := range []string{candidate, c.versionDash.ReplaceAllString(candidate, "$1.$2")} {
			if _, duplicate := seen[variant]; duplicate {
				continue
			}
			seen[variant] = struct{}{}
			variants = append(variants, variant)
		}
	}
	if tag == "" {
		return variants
	}

	// Tagged forms are the literal reading of the request and come first; the
	// tag-less forms remain as fallbacks.
	out := make([]string, 0, variantsPerCandidate*len(variants))
	for _, variant := range variants {
		out = append(out, variant+"-"+tag)
	}
	return append(out, variants...)
}
