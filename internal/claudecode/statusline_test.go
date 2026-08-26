package claudecode_test

import (
	"strings"
	"testing"

	"github.com/fabriziosestito/copilot-claude-proxy/internal/claudecode"
)

func TestStatusLineCommandPinsTheExecutable(t *testing.T) {
	t.Parallel()
	command := claudecode.StatusLineCommand("http://127.0.0.1:4141")

	if !strings.HasSuffix(command, " statusline --url 'http://127.0.0.1:4141'") {
		t.Errorf("command = %q, want the statusline subcommand and quoted url", command)
	}
	// The command targets the running executable, which for tests lives in the
	// build cache; that path stays valid for the session, so it is used as-is
	// rather than falling back to a bare name.
	if !strings.HasPrefix(command, "'") {
		t.Errorf("command = %q, want a shell-quoted executable path", command)
	}
}

func TestStatusLineCommandQuotesShellMetacharacters(t *testing.T) {
	t.Parallel()
	command := claudecode.StatusLineCommand("http://127.0.0.1:4141/'; rm -rf /; '")

	if !strings.HasSuffix(command, ` 'http://127.0.0.1:4141/'\''; rm -rf /; '\'''`) {
		t.Errorf("command = %q, want the url single-quote escaped", command)
	}
}
