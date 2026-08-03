package claudecode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// BinaryName is the Claude Code executable looked up on PATH.
const BinaryName = "claude"

// LaunchConfig describes a Claude Code invocation.
type LaunchConfig struct {
	// Path is the resolved Claude Code executable, from [Lookup].
	Path string
	// Args are the arguments forwarded to Claude Code.
	Args []string
	// BaseURL, when non-empty, pins the proxy connection in the child
	// environment so Claude Code talks to the instance that launched it.
	BaseURL string
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
// own shutdown signal for the duration. Canceling ctx sends SIGTERM to the
// child rather than killing it outright, giving Claude Code a chance to save
// its session.
func Launch(ctx context.Context, cfg LaunchConfig) (int, error) {
	cmd := exec.CommandContext(ctx, cfg.Path, cfg.Args...) //nolint:gosec // the CLI is explicitly asked to run this.
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = childEnv(cfg.BaseURL)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }

	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	if err != nil {
		return 0, fmt.Errorf("run %s: %w", BinaryName, err)
	}
	return 0, nil
}

// childEnv returns the current environment with the proxy connection pinned:
// the address of the proxy that launched Claude Code, and the placeholder token
// the CLI refuses to start without. Both are set unconditionally and overwrite
// what was inherited, since a value left over from another proxy would point
// the session somewhere else. Everything else the user exported is preserved.
//
// Claude Code layers the env block in ~/.claude/settings.json over the
// inherited environment, so these apply only where settings.json leaves them
// unset. `setup` writes both, and when it has run its values are what Claude
// Code uses.
func childEnv(baseURL string) []string {
	if baseURL == "" {
		return nil
	}
	return append(os.Environ(),
		"ANTHROPIC_BASE_URL="+baseURL,
		"ANTHROPIC_AUTH_TOKEN="+defaultAuthToken,
	)
}
