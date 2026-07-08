package claudecode_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fabriziosestito/copilot-claude-proxy/internal/claudecode"
	"github.com/fabriziosestito/copilot-claude-proxy/internal/copilot"
)

func model(id string, maxPromptTokens int) copilot.Model {
	m := copilot.Model{ID: id, Vendor: "Anthropic"}
	m.Capabilities.Limits.MaxPromptTokens = maxPromptTokens
	return m
}

func envConfig(withExtras bool) claudecode.EnvConfig {
	return claudecode.EnvConfig{
		ServerURL:  "http://127.0.0.1:4141",
		Model:      model("claude-sonnet-4.5", 200_000),
		SmallModel: model("claude-haiku-4.5", 200_000),
		WithExtras: withExtras,
	}
}

func TestBuildEnvEssentials(t *testing.T) {
	t.Parallel()
	env := claudecode.BuildEnv(envConfig(false), nil)

	want := map[string]string{
		"ANTHROPIC_BASE_URL":             "http://127.0.0.1:4141",
		"ANTHROPIC_AUTH_TOKEN":           "copilot-claude-proxy",
		"ANTHROPIC_MODEL":                "claude-sonnet-4.5",
		"ANTHROPIC_DEFAULT_SONNET_MODEL": "claude-sonnet-4.5",
		"ANTHROPIC_DEFAULT_OPUS_MODEL":   "claude-sonnet-4.5",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":  "claude-haiku-4.5",
	}
	for key, value := range want {
		if env[key] != value {
			t.Errorf("env[%s] = %q, want %q", key, env[key], value)
		}
	}
	if len(env) != len(want) {
		t.Errorf("len(env) = %d, want %d: %v", len(env), len(want), env)
	}
}

func TestBuildEnvPreservesUserValues(t *testing.T) {
	t.Parallel()
	existing := map[string]string{
		"ANTHROPIC_AUTH_TOKEN":       "my-secret",
		"MCP_TIMEOUT":                "30000",
		"ANTHROPIC_SMALL_FAST_MODEL": "obsolete",
	}
	env := claudecode.BuildEnv(envConfig(false), existing)

	if env["ANTHROPIC_AUTH_TOKEN"] != "my-secret" {
		t.Errorf("auth token = %q, want preserved my-secret", env["ANTHROPIC_AUTH_TOKEN"])
	}
	if env["MCP_TIMEOUT"] != "30000" {
		t.Errorf("MCP_TIMEOUT = %q, want carried over", env["MCP_TIMEOUT"])
	}
	if _, exists := env["ANTHROPIC_SMALL_FAST_MODEL"]; exists {
		t.Error("deprecated ANTHROPIC_SMALL_FAST_MODEL should be removed")
	}
}

func TestBuildEnvOneMillionSuffix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		id              string
		maxPromptTokens int
		want            string
	}{
		{name: "inside band", id: "claude-sonnet-4.5", maxPromptTokens: 1_000_000, want: "claude-sonnet-4.5[1m]"},
		{name: "below band", id: "claude-sonnet-4.5", maxPromptTokens: 800_000, want: "claude-sonnet-4.5"},
		{name: "above band", id: "claude-sonnet-4.5", maxPromptTokens: 1_500_000, want: "claude-sonnet-4.5"},
		{name: "unknown limit", id: "claude-sonnet-4.5", maxPromptTokens: 0, want: "claude-sonnet-4.5"},
		{
			name:            "catalog id already carries -1m",
			id:              "claude-sonnet-4.5-1m",
			maxPromptTokens: 1_000_000,
			want:            "claude-sonnet-4.5[1m]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := envConfig(false)
			cfg.Model = model(tt.id, tt.maxPromptTokens)
			env := claudecode.BuildEnv(cfg, nil)

			if env["ANTHROPIC_MODEL"] != tt.want {
				t.Errorf("ANTHROPIC_MODEL = %q, want %q", env["ANTHROPIC_MODEL"], tt.want)
			}
			if env["ANTHROPIC_DEFAULT_SONNET_MODEL"] != tt.want {
				t.Errorf("sonnet model = %q, want %q", env["ANTHROPIC_DEFAULT_SONNET_MODEL"], tt.want)
			}
		})
	}
}

func TestBuildEnvExtras(t *testing.T) {
	t.Parallel()
	existing := map[string]string{"CLAUDE_AUTOCOMPACT_PCT_OVERRIDE": "70"}
	env := claudecode.BuildEnv(envConfig(true), existing)

	want := map[string]string{
		"DISABLE_NON_ESSENTIAL_MODEL_CALLS":        "1",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
		"CLAUDE_CODE_ENABLE_TELEMETRY":             "0",
		"CLAUDE_CODE_ATTRIBUTION_HEADER":           "0",
		"CLAUDE_AUTOCOMPACT_PCT_OVERRIDE":          "70",
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW":          "200000",
	}
	for key, value := range want {
		if env[key] != value {
			t.Errorf("env[%s] = %q, want %q", key, env[key], value)
		}
	}
}

func TestDiffEnv(t *testing.T) {
	t.Parallel()
	before := map[string]string{"KEEP": "same", "CHANGE": "old", "DROP": "gone"}
	after := map[string]string{"KEEP": "same", "CHANGE": "new", "ADD": "fresh"}

	changes := claudecode.DiffEnv(before, after)

	if len(changes.Added) != 1 || changes.Added[0].Key != "ADD" {
		t.Errorf("Added = %+v, want [ADD]", changes.Added)
	}
	if len(changes.Updated) != 1 || changes.Updated[0].Key != "CHANGE" {
		t.Errorf("Updated = %+v, want [CHANGE]", changes.Updated)
	}
	if len(changes.Removed) != 1 || changes.Removed[0].Key != "DROP" {
		t.Errorf("Removed = %+v, want [DROP]", changes.Removed)
	}
	if changes.Empty() {
		t.Error("Empty() = true, want false")
	}
	if !changes.Destructive() {
		t.Error("Destructive() = false, want true (update + removal)")
	}

	additionsOnly := claudecode.DiffEnv(map[string]string{}, map[string]string{"NEW": "x"})
	if additionsOnly.Destructive() {
		t.Error("pure additions must not be destructive")
	}
	noChanges := claudecode.DiffEnv(before, before)
	if !noChanges.Empty() {
		t.Errorf("identical envs should yield no changes: %+v", noChanges)
	}
}

func TestPlanSetupFreshHomeAndIdempotence(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cfg := claudecode.SetupConfig{Home: home, Env: envConfig(false)}

	setup, err := claudecode.PlanSetup(cfg)
	if err != nil {
		t.Fatalf("PlanSetup: %v", err)
	}
	if !setup.NeedsWrite() {
		t.Fatal("NeedsWrite() = false on a fresh home, want true")
	}
	if setup.Changes.Destructive() {
		t.Error("fresh setup must not be destructive")
	}
	if applyErr := setup.Apply(); applyErr != nil {
		t.Fatalf("Apply: %v", applyErr)
	}

	settings := readJSONFile(t, filepath.Join(home, ".claude", "settings.json"))
	env, ok := settings["env"].(map[string]any)
	if !ok {
		t.Fatalf("settings.env missing: %v", settings)
	}
	if env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:4141" {
		t.Errorf("written base url = %v", env["ANTHROPIC_BASE_URL"])
	}

	claudeJSON := readJSONFile(t, filepath.Join(home, ".claude.json"))
	if claudeJSON["hasCompletedOnboarding"] != true {
		t.Errorf("hasCompletedOnboarding = %v, want true", claudeJSON["hasCompletedOnboarding"])
	}

	again, err := claudecode.PlanSetup(cfg)
	if err != nil {
		t.Fatalf("PlanSetup (second): %v", err)
	}
	if again.NeedsWrite() {
		t.Errorf("NeedsWrite() = true after apply, want idempotence; changes: %+v", again.Changes)
	}
}

func TestPlanSetupPreservesUnknownKeys(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(claudeDir, "settings.json"),
		`{"permissions":{"allow":["Bash"]},"env":{"FOO":"bar"}}`)
	writeFile(t, filepath.Join(home, ".claude.json"),
		`{"projects":{"/work":{"history":[]}},"hasCompletedOnboarding":false}`)

	setup, err := claudecode.PlanSetup(claudecode.SetupConfig{Home: home, Env: envConfig(false)})
	if err != nil {
		t.Fatalf("PlanSetup: %v", err)
	}
	if !setup.OnboardingNeeded {
		t.Error("OnboardingNeeded = false, want true")
	}
	if applyErr := setup.Apply(); applyErr != nil {
		t.Fatalf("Apply: %v", applyErr)
	}

	settings := readJSONFile(t, filepath.Join(claudeDir, "settings.json"))
	if _, ok := settings["permissions"]; !ok {
		t.Error("permissions key was dropped from settings.json")
	}
	env, ok := settings["env"].(map[string]any)
	if !ok {
		t.Fatal("env block missing after apply")
	}
	if env["FOO"] != "bar" {
		t.Errorf("env.FOO = %v, want preserved bar", env["FOO"])
	}
	if env["ANTHROPIC_MODEL"] != "claude-sonnet-4.5" {
		t.Errorf("env.ANTHROPIC_MODEL = %v, want claude-sonnet-4.5", env["ANTHROPIC_MODEL"])
	}

	claudeJSON := readJSONFile(t, filepath.Join(home, ".claude.json"))
	if _, hasProjects := claudeJSON["projects"]; !hasProjects {
		t.Error("projects key was dropped from .claude.json")
	}
	if claudeJSON["hasCompletedOnboarding"] != true {
		t.Errorf("hasCompletedOnboarding = %v, want true", claudeJSON["hasCompletedOnboarding"])
	}
}

func TestPlanSetupRejectsCorruptSettings(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(claudeDir, "settings.json"), `{not json`)

	if _, err := claudecode.PlanSetup(claudecode.SetupConfig{
		Home: home,
		Env:  envConfig(false),
	}); err == nil {
		t.Error("PlanSetup accepted a corrupt settings.json, want error to avoid clobbering")
	}
}

func TestPlanSetupPreservesNonStringEnvValues(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(claudeDir, "settings.json"),
		`{"env":{"CLAUDE_CODE_MAX_OUTPUT_TOKENS":64000}}`)

	setup, err := claudecode.PlanSetup(claudecode.SetupConfig{Home: home, Env: envConfig(false)})
	if err != nil {
		t.Fatalf("PlanSetup: %v", err)
	}
	if setup.Env["CLAUDE_CODE_MAX_OUTPUT_TOKENS"] != "64000" {
		t.Errorf("numeric env value = %q, want preserved as 64000",
			setup.Env["CLAUDE_CODE_MAX_OUTPUT_TOKENS"])
	}
	if applyErr := setup.Apply(); applyErr != nil {
		t.Fatalf("Apply: %v", applyErr)
	}
	settings := readJSONFile(t, filepath.Join(claudeDir, "settings.json"))
	env, ok := settings["env"].(map[string]any)
	if !ok {
		t.Fatal("env block missing after apply")
	}
	if env["CLAUDE_CODE_MAX_OUTPUT_TOKENS"] != "64000" {
		t.Errorf("written value = %v, want 64000", env["CLAUDE_CODE_MAX_OUTPUT_TOKENS"])
	}
}

func TestPlanSetupRejectsStructuredEnvValues(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(claudeDir, "settings.json"), `{"env":{"BROKEN":{"nested":true}}}`)

	if _, err := claudecode.PlanSetup(claudecode.SetupConfig{
		Home: home,
		Env:  envConfig(false),
	}); err == nil {
		t.Error("PlanSetup accepted a structured env value, want error to avoid destroying it")
	}
}

func TestApplyKeepsConcurrentChanges(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".claude.json"), `{}`)

	setup, err := claudecode.PlanSetup(claudecode.SetupConfig{Home: home, Env: envConfig(false)})
	if err != nil {
		t.Fatalf("PlanSetup: %v", err)
	}

	// A concurrent Claude Code session writes state between plan and apply.
	writeFile(t, filepath.Join(home, ".claude.json"),
		`{"projects":{"/work":{"history":["h1"]}}}`)

	if applyErr := setup.Apply(); applyErr != nil {
		t.Fatalf("Apply: %v", applyErr)
	}
	claudeJSON := readJSONFile(t, filepath.Join(home, ".claude.json"))
	if _, kept := claudeJSON["projects"]; !kept {
		t.Error("Apply clobbered concurrently written projects state")
	}
	if claudeJSON["hasCompletedOnboarding"] != true {
		t.Errorf("hasCompletedOnboarding = %v, want true", claudeJSON["hasCompletedOnboarding"])
	}
}

func readJSONFile(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	object := map[string]any{}
	if parseErr := json.Unmarshal(data, &object); parseErr != nil {
		t.Fatalf("parse %s: %v", path, parseErr)
	}
	return object
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
