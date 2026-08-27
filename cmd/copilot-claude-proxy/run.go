package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"

	"github.com/urfave/cli/v3"

	"github.com/fabriziosestito/copilot-claude-proxy/internal/claudecode"
	"github.com/fabriziosestito/copilot-claude-proxy/internal/server"
)

// logFilePermissions keeps proxy logs private; they carry request metadata.
const logFilePermissions = 0o600

// errProxyStopped reports a proxy that shut down before accepting connections.
var errProxyStopped = errors.New("proxy stopped before it was ready")

func newRunCommand(signals *signalHandling) *cli.Command {
	return &cli.Command{
		Name:      "run",
		Usage:     "Start the proxy, run Claude Code against it, and stop the proxy on exit",
		ArgsUsage: "[-- claude arguments...]",
		Description: "Starts the proxy, waits until it accepts connections, then hands the\n" +
			"terminal to Claude Code, pinned to the proxy with --settings. The proxy\n" +
			"shuts down when Claude Code exits, and its exit status is propagated.\n\n" +
			"Without --port the proxy binds a free ephemeral port, so run never\n" +
			"collides with another proxy already listening on the default port.\n\n" +
			"Arguments after -- are forwarded to Claude Code:\n" +
			"  copilot-claude-proxy run -- --resume",
		Flags: []cli.Flag{
			portFlag(),
			hostFlag(),
			accountTypeFlag(),
			githubTokenFlag(),
			modelMapFlag(),
			logFileFlag(),
			noStatusLineFlag(),
			verboseFlag(),
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runRun(ctx, cmd, signals)
		},
	}
}

func runRun(ctx context.Context, cmd *cli.Command, signals *signalHandling) error {
	// The proxy URL handed to Claude Code embeds the port, so the settings
	// document is built only after the listener reports what it bound. That
	// makes an ephemeral port (0) workable, and it is the default when no
	// --port is given: run writes the URL into a per-session settings file, so
	// a stable port buys nothing and a busy 4141 would only fail the session.
	port := cmd.Int("port")
	if !cmd.IsSet("port") {
		port = 0
	}
	if port < 0 || port > 65535 {
		return fmt.Errorf("run requires --port in 0-65535 (0 picks a free port), got %d", port)
	}

	// Resolved first so a missing CLI fails before authenticating or binding.
	claudePath, err := claudecode.Lookup()
	if err != nil {
		return err
	}

	// Split before anything expensive so a malformed --settings among the
	// forwarded arguments is reported while there is still nothing to tear
	// down. The document itself is built later, once the bound port is known.
	forwarded, inherited, err := claudecode.SplitSettingsArg(cmd.Args().Slice())
	if err != nil {
		return err
	}

	logger, closeLogs, err := proxyLogger(cmd)
	if err != nil {
		return err
	}
	defer closeLogs()

	session, err := connectWith(ctx, cmd, logger)
	if err != nil {
		return err
	}

	proxyCtx, stopProxy := context.WithCancel(ctx)
	defer stopProxy()

	ready := make(chan net.Addr, 1)
	var readyOnce sync.Once
	proxyDone := make(chan error, 1)
	go func() {
		proxyDone <- server.Run(proxyCtx, server.RunConfig{
			Logger:  logger,
			Session: session,
			Host:    cmd.String("host"),
			Port:    port,
			Ready: func(addr net.Addr) {
				readyOnce.Do(func() { ready <- addr })
			},
		})
	}()

	var boundAddr net.Addr
	select {
	case boundAddr = <-ready:
	case <-ctx.Done():
		return ctx.Err()
	case proxyErr := <-proxyDone:
		if proxyErr != nil {
			return proxyErr
		}
		return errProxyStopped
	}

	settings, err := buildClaudeSettings(cmd, inherited, boundAddr)
	if err != nil {
		return err
	}

	// The merged document goes to Claude Code as a private file rather than
	// inline: an inherited settings file may carry credentials, and argv is
	// readable by every process on the machine.
	settingsPath, removeSettings, err := claudecode.WriteSettingsFile(settings)
	if err != nil {
		return err
	}
	defer removeSettings()

	// Claude Code owns the terminal from here, including Ctrl-C.
	restoreInterrupt := signals.DetachInterrupt()
	defer restoreInterrupt()

	return superviseSession(ctx, claudecode.LaunchConfig{
		Path:     claudePath,
		Args:     forwarded,
		Settings: settingsPath,
	}, stopProxy, proxyDone)
}

// launchResult carries Claude Code's exit outcome across the supervising select.
type launchResult struct {
	status int
	err    error
}

// superviseSession runs Claude Code concurrently with the proxy and returns
// once both have stopped. Watching the proxy while the session runs matters:
// a server that dies mid-session would otherwise leave Claude Code talking to
// a dead port until the user gives up and exits it by hand.
func superviseSession(
	ctx context.Context,
	cfg claudecode.LaunchConfig,
	stopProxy func(),
	proxyDone <-chan error,
) error {
	launchCtx, stopLaunch := context.WithCancel(ctx)
	defer stopLaunch()

	launchDone := make(chan launchResult, 1)
	go func() {
		status, err := claudecode.Launch(launchCtx, cfg)
		launchDone <- launchResult{status: status, err: err}
	}()

	var result launchResult
	var proxyErr error
	select {
	case result = <-launchDone:
		stopProxy()
		proxyErr = <-proxyDone
	case proxyErr = <-proxyDone:
		stopLaunch()
		result = <-launchDone
		// A proxy that stopped on its own took the live session down with it,
		// so its failure is the outcome to report, not the child's terminated
		// status. When ctx canceled both (Ctrl-C or SIGTERM on this process),
		// the child's own exit is the one that matters.
		if ctx.Err() == nil {
			if proxyErr == nil {
				proxyErr = errProxyStopped
			}
			return proxyErr
		}
	}

	switch {
	case result.err != nil:
		return result.err
	case proxyErr != nil:
		return proxyErr
	case result.status != 0:
		return exitCodeError{status: result.status}
	}
	return nil
}

// buildClaudeSettings renders the settings document pinning Claude Code to
// the proxy at boundAddr, folding in a --settings the user passed after --.
// Claude Code keeps only the last --settings on a command line, so forwarding
// theirs alongside ours would silently discard the proxy connection. The URL
// host comes from --host so a wildcard bind is rewritten to a dialable
// loopback, while the port comes from the listener so an ephemeral bind
// reports the port it was actually assigned.
func buildClaudeSettings(cmd *cli.Command, inherited string, boundAddr net.Addr) (string, error) {
	tcpAddr, ok := boundAddr.(*net.TCPAddr)
	if !ok {
		return "", fmt.Errorf("proxy bound a non-TCP address %q", boundAddr)
	}
	serverURL := claudecode.ClientURL(cmd.String("host"), tcpAddr.Port)

	var statusLine string
	if !cmd.Bool("no-statusline") {
		statusLine = claudecode.StatusLineCommand(serverURL)
	}

	return claudecode.BuildSettings(claudecode.SettingsConfig{
		BaseURL:           serverURL,
		StatusLineCommand: statusLine,
		Inherited:         inherited,
	})
}

// proxyLogger builds the logger for a proxy sharing a terminal with Claude
// Code. Log lines scribble over the TUI, so without --log-file the proxy is
// held to errors; with one, everything goes to the file instead.
func proxyLogger(cmd *cli.Command) (*slog.Logger, func(), error) {
	verbose := cmd.Bool("verbose")

	path := cmd.String("log-file")
	if path == "" {
		level := slog.LevelError
		if verbose {
			level = slog.LevelDebug
		}
		return newLoggerAt(os.Stderr, level), func() {}, nil
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, logFilePermissions)
	if err != nil {
		return nil, nil, fmt.Errorf("open log file: %w", err)
	}
	// OpenFile's mode only applies on creation; a pre-existing file keeps
	// whatever permissions it had, so tighten it explicitly.
	if err := file.Chmod(logFilePermissions); err != nil {
		_ = file.Close()
		return nil, nil, fmt.Errorf("restrict log file permissions: %w", err)
	}
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	return newLoggerAt(file, level), func() { _ = file.Close() }, nil
}

// exitCodeError propagates a child process status without printing anything:
// Claude Code has already reported whatever went wrong.
type exitCodeError struct{ status int }

func (e exitCodeError) Error() string { return fmt.Sprintf("claude exited with status %d", e.status) }

func (e exitCodeError) ExitCode() int { return e.status }
