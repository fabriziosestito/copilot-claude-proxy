package copilot_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/fabriziosestito/copilot-claude-proxy/internal/copilot"
)

type staticFetcher struct {
	models []copilot.Model
}

func (f staticFetcher) FetchModels(_ context.Context) ([]copilot.Model, error) {
	return f.models, nil
}

func anthropicModel(id string) copilot.Model {
	return copilot.Model{
		ID:                 id,
		Vendor:             "Anthropic",
		SupportedEndpoints: []string{"/v1/messages", "/chat/completions"},
	}
}

func testCatalog(t *testing.T, overrides map[string]string) *copilot.Catalog {
	t.Helper()
	fetcher := staticFetcher{models: []copilot.Model{
		anthropicModel("claude-sonnet-4.5"),
		anthropicModel("claude-sonnet-4.5-1m"),
		anthropicModel("claude-3.5-haiku"),
		anthropicModel("claude-haiku-4.5"),
		anthropicModel("claude-opus-4.1"),
		{ID: "gpt-5-mini", Vendor: "OpenAI", SupportedEndpoints: []string{"/chat/completions"}},
	}}
	catalog := copilot.NewCatalog(fetcher, slog.New(slog.DiscardHandler), overrides)
	if err := catalog.Refresh(t.Context()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	return catalog
}

func TestCatalogResolve(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		requested string
		wantID    string
		wantKnown bool
	}{
		{name: "exact id", requested: "claude-sonnet-4.5", wantID: "claude-sonnet-4.5", wantKnown: true},
		{
			name:      "date suffix stripped",
			requested: "claude-opus-4-1-20250805",
			wantID:    "claude-opus-4.1",
			wantKnown: true,
		},
		{name: "dashed version dotted", requested: "claude-sonnet-4-5", wantID: "claude-sonnet-4.5", wantKnown: true},
		{
			name:      "old naming with date",
			requested: "claude-3-5-haiku-20241022",
			wantID:    "claude-3.5-haiku",
			wantKnown: true,
		},
		{name: "bracket tag", requested: "claude-sonnet-4.5[1m]", wantID: "claude-sonnet-4.5-1m", wantKnown: true},
		{
			name:      "bracket tag with dashed version",
			requested: "claude-sonnet-4-5[1m]",
			wantID:    "claude-sonnet-4.5-1m",
			wantKnown: true,
		},
		{
			name:      "bracket tag with date suffix",
			requested: "claude-sonnet-4-5-20250929[1m]",
			wantID:    "claude-sonnet-4.5-1m",
			wantKnown: true,
		},
		{
			name:      "bracket tag without matching 1m entry falls back to base model",
			requested: "claude-opus-4-1-20250805[1m]",
			wantID:    "claude-opus-4.1",
			wantKnown: true,
		},
		{
			name:      "catalog id already carrying -1m",
			requested: "claude-sonnet-4.5-1m",
			wantID:    "claude-sonnet-4.5-1m",
			wantKnown: true,
		},
		{name: "uppercase input", requested: "Claude-Sonnet-4.5", wantID: "claude-sonnet-4.5", wantKnown: true},
		{
			name:      "surrounding whitespace",
			requested: "  claude-sonnet-4.5  ",
			wantID:    "claude-sonnet-4.5",
			wantKnown: true,
		},
		{
			name:      "unknown model passes through",
			requested: "claude-legacy-1",
			wantID:    "claude-legacy-1",
			wantKnown: false,
		},
		{name: "non anthropic model resolves", requested: "gpt-5-mini", wantID: "gpt-5-mini", wantKnown: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			catalog := testCatalog(t, nil)

			resolution := catalog.Resolve(tt.requested)
			if resolution.ID != tt.wantID {
				t.Errorf("Resolve(%q).ID = %q, want %q", tt.requested, resolution.ID, tt.wantID)
			}
			if resolution.Known != tt.wantKnown {
				t.Errorf("Resolve(%q).Known = %v, want %v", tt.requested, resolution.Known, tt.wantKnown)
			}
		})
	}
}

func TestCatalogResolveOverrides(t *testing.T) {
	t.Parallel()
	catalog := testCatalog(t, map[string]string{
		"haiku": "claude-haiku-4.5",
		"Opus":  "claude-opus-4.1",
	})

	if got := catalog.Resolve("haiku"); got.ID != "claude-haiku-4.5" || !got.Known {
		t.Errorf("Resolve(haiku) = %+v, want claude-haiku-4.5 (known)", got)
	}
	if got := catalog.Resolve("OPUS"); got.ID != "claude-opus-4.1" || !got.Known {
		t.Errorf("Resolve(OPUS) = %+v, want claude-opus-4.1 (known)", got)
	}
}

func TestCatalogModelsKeepsUpstreamOrder(t *testing.T) {
	t.Parallel()
	catalog := testCatalog(t, nil)

	models := catalog.Models()
	if len(models) != 6 {
		t.Fatalf("len(Models()) = %d, want 6", len(models))
	}
	if models[0].ID != "claude-sonnet-4.5" || models[5].ID != "gpt-5-mini" {
		t.Errorf("unexpected order: first %q last %q", models[0].ID, models[5].ID)
	}
}

func TestSupportsAnthropicMessages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		model copilot.Model
		want  bool
	}{
		{
			name:  "endpoint advertised",
			model: copilot.Model{Vendor: "OpenAI", SupportedEndpoints: []string{"/v1/messages"}},
			want:  true,
		},
		{
			name:  "endpoint missing",
			model: copilot.Model{Vendor: "Anthropic", SupportedEndpoints: []string{"/chat/completions"}},
			want:  false,
		},
		{
			name:  "no endpoint list falls back to vendor",
			model: copilot.Model{Vendor: "Anthropic"},
			want:  true,
		},
		{
			name:  "no endpoint list non anthropic",
			model: copilot.Model{Vendor: "OpenAI"},
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.model.SupportsAnthropicMessages(); got != tt.want {
				t.Errorf("SupportsAnthropicMessages() = %v, want %v", got, tt.want)
			}
		})
	}
}
