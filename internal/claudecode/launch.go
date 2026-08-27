package claudecode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// BinaryName is the Claude Code executable looked up on PATH.
const BinaryName = "claude"

// terminationGrace bounds how long a child may take to exit after being told
// to ([terminate]) before the runtime kills it, so a hung or unresponsive
// Claude Code cannot block shutdown forever.
const terminationGrace = 5 * time.Second

// signalExitBase is the shell convention for reporting a signal-terminated
// child: its exit code is 128 plus the terminating signal number.
const signalExitBase = 128

// LaunchConfig describes a Claude Code invocation.
type LaunchConfig struct {
	// Path is the resolved Claude Code executable, from [Lookup].
	Path string
	// Args are the arguments forwarded to Claude Code.
	Args []string
	// Settings, when non-empty, is passed as --settings and pins this session
	// to the proxy that launched it. Claude Code accepts either a path to a
	// JSON file or an inline document; a file keeps inherited values out of
	// the child's argv.
	Settings string
}

// Lookup resolves the Claude Code executable on PATH. Callers should do this
// before any expensive setup so a missing CLI fails immediately.
func Lookup() (string, error) {
	path, err := exec.LookPath(BinaryName)
	if err != nil {
		return "", fmt.Errorf(
			"%s not found on PATH; install Claude Code: https://docs.anthropic.com/en/docs/claude-code",
			BinaryName)
	}
	return path, nil
}

// Launch runs Claude Code attached to the current terminal and returns its
// exit status once it finishes.
//
// The child shares this process's terminal and process group, so the terminal
// delivers Ctrl-C to it directly; callers must stop treating SIGINT as their
// own shutdown signal for the duration. Canceling ctx ends the child via
// [terminate] — SIGTERM where the platform has it — giving Claude Code a
// chance to save its session.
func Launch(ctx context.Context, cfg LaunchConfig) (int, error) {
	args := cfg.Args
	if cfg.Settings != "" {
		args = append([]string{SettingsFlag, cfg.Settings}, cfg.Args...)
	}

	cmd := exec.CommandContext(ctx, cfg.Path, args...) //nolint:gosec // the CLI is explicitly asked to run this.
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = childEnv()
	cmd.Cancel = func() error { return terminate(cmd.Process) }
	// After Cancel's termination request, kill the child if it has not exited
	// in time so a hung Claude Code cannot block cancellation indefinitely.
	cmd.WaitDelay = terminationGrace

	err := cmd.Run()
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return exitStatus(exitErr), nil
	}
	if err != nil {
		return 0, fmt.Errorf("run %s: %w", BinaryName, err)
	}
	return 0, nil
}

// exitStatus turns a child's exit into a shell-conventional code. A process
// killed by a signal has an ExitCode of -1; report it as 128+signal (e.g. 130
// for SIGINT) the way a shell would, instead of letting -1 become 255.
func exitStatus(exitErr *exec.ExitError) int {
	if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return signalExitBase + int(status.Signal())
	}
	return exitErr.ExitCode()
}

// blockedEnvKeys are the environment entries that would override or bypass what
// --settings pins. They are scrubbed from both the child process environment
// ([childEnv]) and the inherited --settings env block ([overlayEnv]).
//
// ANTHROPIC_API_KEY is the load-bearing one: Claude Code sends it as an
// x-api-key header even when the auth token comes from --settings, putting a
// real Anthropic credential on every request to this proxy. The rest either
// name a different endpoint or select a different provider altogether, which
// would route the session past the proxy entirely.
func blockedEnvKeys() map[string]struct{} {
	return map[string]struct{}{
		"ANTHROPIC_API_KEY":                      {},
		"ANTHROPIC_AUTH_TOKEN":                   {},
		"ANTHROPIC_BASE_URL":                     {},
		"ANTHROPIC_CUSTOM_HEADERS":               {},
		"CLAUDE_CODE_API_BASE_URL":               {},
		"CLAUDE_CODE_OAUTH_TOKEN":                {},
		"CLAUDE_CODE_USE_ANTHROPIC_AWS":          {},
		"CLAUDE_CODE_USE_ANTHROPIC_GOOGLE_CLOUD": {},
		"CLAUDE_CODE_USE_BEDROCK":                {},
		"CLAUDE_CODE_USE_FOUNDRY":                {},
		"CLAUDE_CODE_USE_GATEWAY":                {},
		"CLAUDE_CODE_USE_MANTLE":                 {},
		"CLAUDE_CODE_USE_VERTEX":                 {},
	}
}

// childEnv returns the current environment without the entries that would
// override or bypass what --settings pins. Everything else the user exported is
// preserved. Names are compared case-insensitively: Windows treats
// anthropic_api_key and ANTHROPIC_API_KEY as the same variable, and on Unix a
// lowercase spelling is inert anyway.
func childEnv() []string {
	blocked := blockedEnvKeys()
	environ := os.Environ()
	kept := make([]string, 0, len(environ))
	for _, entry := range environ {
		name, _, _ := strings.Cut(entry, "=")
		if _, found := blocked[strings.ToUpper(name)]; found {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}
