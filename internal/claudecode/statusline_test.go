package claudecode_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fabriziosestito/copilot-claude-proxy/internal/claudecode"
)

// statusLineCommand is a stand-in for what StatusLineCommand produces; the
// plan only cares that it names the statusline subcommand.
const statusLineCommand = "/usr/local/bin/copilot-claude-proxy statusline --url http://127.0.0.1:4141"

// homeWithSettings prepares a home directory holding the given settings.json.
func homeWithSettings(t *testing.T, settings string) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o750); err != nil {
		t.Fatal(err)
	}
	if settings != "" {
		writeFile(t, filepath.Join(home, ".claude", "settings.json"), settings)
	}
	return home
}

func planWithStatusLine(t *testing.T, home string) *claudecode.Setup {
	t.Helper()
	setup, err := claudecode.PlanSetup(claudecode.SetupConfig{
		Home: home, Env: envConfig(false), StatusLine: statusLineCommand,
	})
	if err != nil {
		t.Fatalf("PlanSetup: %v", err)
	}
	return setup
}

// statusLineBlock returns the written statusLine object.
func statusLineBlock(t *testing.T, home string) map[string]any {
	t.Helper()
	settings := readJSONFile(t, filepath.Join(home, ".claude", "settings.json"))
	block, ok := settings["statusLine"].(map[string]any)
	if !ok {
		t.Fatalf("statusLine block missing or not an object: %v", settings["statusLine"])
	}
	return block
}

func TestStatusLineIsOptional(t *testing.T) {
	t.Parallel()
	home := homeWithSettings(t, "")

	setup, err := claudecode.PlanSetup(claudecode.SetupConfig{Home: home, Env: envConfig(false)})
	if err != nil {
		t.Fatalf("PlanSetup: %v", err)
	}
	if !setup.StatusLine.Empty() {
		t.Error("StatusLine.Empty() = false without a requested command, want true")
	}
	if applyErr := setup.Apply(); applyErr != nil {
		t.Fatalf("Apply: %v", applyErr)
	}

	settings := readJSONFile(t, filepath.Join(home, ".claude", "settings.json"))
	if _, present := settings["statusLine"]; present {
		t.Error("statusLine was written without being requested")
	}
}

func TestStatusLineWritesAndIsIdempotent(t *testing.T) {
	t.Parallel()
	// A null block is Claude Code's "no status line" state, not an object to
	// preserve.
	home := homeWithSettings(t, `{"statusLine":null,"permissions":{"allow":["Bash"]}}`)

	setup := planWithStatusLine(t, home)
	if setup.StatusLine.Empty() {
		t.Fatal("StatusLine.Empty() = true, want a planned change")
	}
	if setup.Destructive() {
		t.Error("replacing a null status line must not be destructive")
	}
	if applyErr := setup.Apply(); applyErr != nil {
		t.Fatalf("Apply: %v", applyErr)
	}

	block := statusLineBlock(t, home)
	if block["type"] != "command" || block["command"] != statusLineCommand {
		t.Errorf("statusLine = %v, want the proxy command", block)
	}
	settings := readJSONFile(t, filepath.Join(home, ".claude", "settings.json"))
	if _, ok := settings["permissions"]; !ok {
		t.Error("permissions key was dropped")
	}

	if again := planWithStatusLine(t, home); !again.StatusLine.Empty() {
		t.Errorf("second plan still wants a change: %s", again.StatusLine.Format())
	}
}

func TestStatusLineUpgradeKeepsSiblingFieldsAndIsNotDestructive(t *testing.T) {
	t.Parallel()
	home := homeWithSettings(t, `{"statusLine":{"type":"command",
		"command":"/old/path/copilot-claude-proxy statusline --url http://127.0.0.1:9999",
		"refreshInterval":5,"padding":1}}`)

	setup := planWithStatusLine(t, home)
	if setup.Destructive() {
		t.Error("refreshing this tool's own status line must not be destructive")
	}
	if applyErr := setup.Apply(); applyErr != nil {
		t.Fatalf("Apply: %v", applyErr)
	}

	block := statusLineBlock(t, home)
	if block["command"] != statusLineCommand {
		t.Errorf("command = %v, want the refreshed command", block["command"])
	}
	if block["refreshInterval"] != float64(5) || block["padding"] != float64(1) {
		t.Errorf("sibling fields lost: %v", block)
	}
}

func TestStatusLineOverwritingAForeignOneIsDestructive(t *testing.T) {
	t.Parallel()
	home := homeWithSettings(t,
		`{"statusLine":{"type":"command","command":"~/.claude/my-statusline.sh"}}`)

	setup := planWithStatusLine(t, home)
	if !setup.Destructive() {
		t.Error("Destructive() = false when replacing a hand-written status line, want true")
	}
	if diff := setup.StatusLine.Format(); !strings.Contains(diff, "my-statusline.sh") {
		t.Errorf("diff %q does not show what would be replaced", diff)
	}
}

func TestStatusLineRejectsAMalformedBlock(t *testing.T) {
	t.Parallel()
	home := homeWithSettings(t, `{"statusLine":"echo hi"}`)

	_, err := claudecode.PlanSetup(claudecode.SetupConfig{
		Home: home, Env: envConfig(false), StatusLine: statusLineCommand,
	})
	if err == nil {
		t.Fatal("expected an error for a non-object statusLine block")
	}
	if !strings.Contains(err.Error(), "statusLine") {
		t.Errorf("error %q does not name the offending key", err)
	}
}

func TestStatusLineCommandPinsTheExecutable(t *testing.T) {
	t.Parallel()
	command := claudecode.StatusLineCommand("http://127.0.0.1:4141")

	if !strings.HasSuffix(command, " statusline --url http://127.0.0.1:4141") {
		t.Errorf("command = %q, want the statusline subcommand and url", command)
	}
	// Tests run from a binary in the build cache, which is not a durable
	// install location, so the command must fall back to the bare name.
	if !strings.HasPrefix(command, "copilot-claude-proxy ") {
		t.Errorf("command = %q, want the PATH fallback for a temporary binary", command)
	}
}
