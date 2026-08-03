package claudecode_test

import (
	"strings"
	"testing"

	"github.com/fabriziosestito/copilot-claude-proxy/internal/claudecode"
)

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
