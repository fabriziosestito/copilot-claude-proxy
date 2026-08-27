package claudecode_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/fabriziosestito/copilot-claude-proxy/internal/claudecode"
)

const proxyURL = "http://127.0.0.1:4141"

// decodeSettings parses a built document into nested maps for inspection.
func decodeSettings(t *testing.T, document string) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(document), &decoded); err != nil {
		t.Fatalf("built settings are not JSON: %v (%s)", err, document)
	}
	return decoded
}

// settingsEnv returns the env block of a built document.
func settingsEnv(t *testing.T, document string) map[string]any {
	t.Helper()
	env, ok := decodeSettings(t, document)["env"].(map[string]any)
	if !ok {
		t.Fatalf("settings have no env block: %s", document)
	}
	return env
}

func TestBuildSettingsPinsTheProxyConnection(t *testing.T) {
	t.Parallel()
	document, err := claudecode.BuildSettings(claudecode.SettingsConfig{BaseURL: proxyURL})
	if err != nil {
		t.Fatalf("BuildSettings: %v", err)
	}

	env := settingsEnv(t, document)
	if env["ANTHROPIC_BASE_URL"] != proxyURL {
		t.Errorf("base URL = %v, want %q", env["ANTHROPIC_BASE_URL"], proxyURL)
	}
	// Claude Code refuses to start without a token, and the proxy does not
	// authenticate its clients, so any non-empty value will do.
	if env["ANTHROPIC_AUTH_TOKEN"] == "" || env["ANTHROPIC_AUTH_TOKEN"] == nil {
		t.Error("auth token is missing")
	}
	// Only the connection is pinned; the model selection belongs to setup.
	if len(env) != 2 {
		t.Errorf("env = %v, want only the connection keys", env)
	}
	if _, present := decodeSettings(t, document)["statusLine"]; present {
		t.Error("statusLine was written without a command")
	}
}

func TestBuildSettingsAddsTheStatusLine(t *testing.T) {
	t.Parallel()
	document, err := claudecode.BuildSettings(claudecode.SettingsConfig{
		BaseURL:           proxyURL,
		StatusLineCommand: "proxy statusline --url " + proxyURL,
	})
	if err != nil {
		t.Fatalf("BuildSettings: %v", err)
	}

	block, ok := decodeSettings(t, document)["statusLine"].(map[string]any)
	if !ok {
		t.Fatalf("statusLine is missing: %s", document)
	}
	if block["type"] != "command" {
		t.Errorf("type = %v, want command", block["type"])
	}
	if block["command"] != "proxy statusline --url "+proxyURL {
		t.Errorf("command = %v", block["command"])
	}
}

func TestBuildSettingsMergesAnInheritedDocument(t *testing.T) {
	t.Parallel()
	inherited := `{
		"env": {"ANTHROPIC_MODEL": "theirs", "ANTHROPIC_BASE_URL": "http://elsewhere"},
		"statusLine": {"type": "command", "command": "theirs"},
		"cleanupPeriodDays": 45
	}`

	document, err := claudecode.BuildSettings(claudecode.SettingsConfig{
		BaseURL:           proxyURL,
		StatusLineCommand: "ours",
		Inherited:         inherited,
	})
	if err != nil {
		t.Fatalf("BuildSettings: %v", err)
	}

	decoded := decodeSettings(t, document)
	if decoded["cleanupPeriodDays"] != float64(45) {
		t.Errorf("unrelated key was lost: %v", decoded["cleanupPeriodDays"])
	}

	env := settingsEnv(t, document)
	if env["ANTHROPIC_MODEL"] != "theirs" {
		t.Errorf("their env entry was lost: %v", env["ANTHROPIC_MODEL"])
	}
	// The one key they cannot keep: it would route the session past the proxy.
	if env["ANTHROPIC_BASE_URL"] != proxyURL {
		t.Errorf("base URL = %v, want ours (%q) to win", env["ANTHROPIC_BASE_URL"], proxyURL)
	}

	block, _ := decoded["statusLine"].(map[string]any)
	if block["command"] != "ours" {
		t.Errorf("status line = %v, want ours to win", block["command"])
	}
}

func TestBuildSettingsStripsBlockedInheritedEnv(t *testing.T) {
	t.Parallel()
	// An inherited env block is a second path to smuggle a real credential or a
	// rival provider selector past the process-env scrub; both must be dropped.
	// Lowercase spellings count too: Windows environment names are
	// case-insensitive.
	inherited := `{"env": {
		"ANTHROPIC_API_KEY": "real-key",
		"CLAUDE_CODE_USE_BEDROCK": "1",
		"anthropic_auth_token": "lowercase",
		"ANTHROPIC_MODEL": "theirs"
	}}`

	document, err := claudecode.BuildSettings(claudecode.SettingsConfig{
		BaseURL:   proxyURL,
		Inherited: inherited,
	})
	if err != nil {
		t.Fatalf("BuildSettings: %v", err)
	}

	env := settingsEnv(t, document)
	if _, present := env["ANTHROPIC_API_KEY"]; present {
		t.Errorf("ANTHROPIC_API_KEY survived the overlay: %v", env["ANTHROPIC_API_KEY"])
	}
	if _, present := env["CLAUDE_CODE_USE_BEDROCK"]; present {
		t.Errorf("CLAUDE_CODE_USE_BEDROCK survived the overlay: %v", env["CLAUDE_CODE_USE_BEDROCK"])
	}
	if _, present := env["anthropic_auth_token"]; present {
		t.Errorf("anthropic_auth_token survived the overlay: %v", env["anthropic_auth_token"])
	}
	if env["ANTHROPIC_MODEL"] != "theirs" {
		t.Errorf("unrelated env entry was lost: %v", env["ANTHROPIC_MODEL"])
	}
}

func TestBuildSettingsKeepsAnInheritedStatusLineWhenDisabled(t *testing.T) {
	t.Parallel()
	document, err := claudecode.BuildSettings(claudecode.SettingsConfig{
		BaseURL:   proxyURL,
		Inherited: `{"statusLine": {"type": "command", "command": "theirs"}}`,
	})
	if err != nil {
		t.Fatalf("BuildSettings: %v", err)
	}

	block, ok := decodeSettings(t, document)["statusLine"].(map[string]any)
	if !ok {
		t.Fatalf("their status line was dropped: %s", document)
	}
	if block["command"] != "theirs" {
		t.Errorf("status line = %v, want theirs untouched", block["command"])
	}
}

func TestBuildSettingsReadsAnInheritedFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"env":{"ANTHROPIC_MODEL":"from-file"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	document, err := claudecode.BuildSettings(claudecode.SettingsConfig{
		BaseURL: proxyURL, Inherited: path,
	})
	if err != nil {
		t.Fatalf("BuildSettings: %v", err)
	}
	if env := settingsEnv(t, document); env["ANTHROPIC_MODEL"] != "from-file" {
		t.Errorf("file contents were not merged: %v", env)
	}
}

func TestBuildSettingsRejectsUnusableInput(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"missing file":      filepath.Join(t.TempDir(), "absent.json"),
		"malformed":         `{"env":`,
		"not an object":     `["a"]`,
		"env not an object": `{"env": "a string"}`,
	}
	for name, inherited := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := claudecode.BuildSettings(claudecode.SettingsConfig{
				BaseURL: proxyURL, Inherited: inherited,
			}); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestSplitSettingsArg(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		args      []string
		remaining []string
		value     string
	}{
		"absent": {
			args:      []string{"--resume"},
			remaining: []string{"--resume"},
		},
		"separate value": {
			args:      []string{"--resume", "--settings", "a.json", "-p"},
			remaining: []string{"--resume", "-p"},
			value:     "a.json",
		},
		"joined value": {
			args:      []string{"--settings=a.json", "--resume"},
			remaining: []string{"--resume"},
			value:     "a.json",
		},
		// Claude Code keeps the last of a repeated flag; so does this.
		"repeated": {
			args:      []string{"--settings", "a.json", "--settings={}"},
			remaining: []string{},
			value:     "{}",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			remaining, value, err := claudecode.SplitSettingsArg(test.args)
			if err != nil {
				t.Fatalf("SplitSettingsArg: %v", err)
			}
			if !reflect.DeepEqual(remaining, test.remaining) {
				t.Errorf("remaining = %v, want %v", remaining, test.remaining)
			}
			if value != test.value {
				t.Errorf("value = %q, want %q", value, test.value)
			}
		})
	}
}

func TestSplitSettingsArgRejectsAMissingValue(t *testing.T) {
	t.Parallel()
	_, _, err := claudecode.SplitSettingsArg([]string{"--resume", "--settings"})
	if err == nil {
		t.Fatal("expected an error for a trailing --settings")
	}
	if !strings.Contains(err.Error(), claudecode.SettingsFlag) {
		t.Errorf("error %q does not name the flag", err)
	}
}

func TestWriteSettingsFileKeepsTheDocumentPrivate(t *testing.T) {
	t.Parallel()
	document := `{"env":{"ANTHROPIC_BASE_URL":"` + proxyURL + `"}}`

	path, cleanup, err := claudecode.WriteSettingsFile(document)
	if err != nil {
		t.Fatalf("WriteSettingsFile: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings file: %v", err)
	}
	if string(content) != document {
		t.Errorf("content = %q, want %q", content, document)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat settings file: %v", err)
	}
	// The document may carry inherited credentials; nobody else may read it.
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("permissions = %v, want 0600", perm)
	}

	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("cleanup left the settings file behind: %v", err)
	}
}
