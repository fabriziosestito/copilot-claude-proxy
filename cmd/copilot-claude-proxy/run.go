package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	// run pins Claude Code to the proxy URL before the listener binds, so a
	// port outside the dialable range would leave the client unable to reach
	// it. Ephemeral (0) is rejected rather than plumbing the bound address back
	// through readiness; anything past 65535 never binds at all.
	if port := cmd.Int("port"); port < 1 || port > 65535 {
		return fmt.Errorf("run requires a fixed --port in 1-65535, got %d", port)
	}

	// Resolved first so a missing CLI fails before authenticating or binding.
	claudePath, err := claudecode.Lookup()
	if err != nil {
		return err
	}

	// Built before anything expensive so a malformed --settings is reported
	// while there is still nothing to tear down.
	forwarded, settings, err := claudeSettings(cmd)
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

	ready := make(chan struct{})
	proxyDone := make(chan error, 1)
	go func() {
		proxyDone <- server.Run(proxyCtx, server.RunConfig{
			Logger:  logger,
			Session: session,
			Host:    cmd.String("host"),
			Port:    cmd.Int("port"),
			Ready:   sync.OnceFunc(func() { close(ready) }),
		})
	}()

	select {
	case <-ready:
	case <-ctx.Done():
		return ctx.Err()
	case proxyErr := <-proxyDone:
		if proxyErr != nil {
			return proxyErr
		}
		return errProxyStopped
	}

	// Claude Code owns the terminal from here, including Ctrl-C.
	restoreInterrupt := signals.DetachInterrupt()
	defer restoreInterrupt()

	status, launchErr := claudecode.Launch(ctx, claudecode.LaunchConfig{
		Path:     claudePath,
		Args:     forwarded,
		Settings: settings,
	})

	stopProxy()
	proxyErr := <-proxyDone

	switch {
	case launchErr != nil:
		return launchErr
	case proxyErr != nil:
		return proxyErr
	case status != 0:
		return exitCodeError{status: status}
	}
	return nil
}

// claudeSettings splits a --settings the user passed after -- out of the
// forwarded arguments and folds it into the document this command supplies.
// Claude Code keeps only the last --settings on a command line, so forwarding
// theirs alongside ours would silently discard the proxy connection.
func claudeSettings(cmd *cli.Command) ([]string, string, error) {
	forwarded, inherited, err := claudecode.SplitSettingsArg(cmd.Args().Slice())
	if err != nil {
		return nil, "", err
	}

	serverURL := clientURL(cmd)
	var statusLine string
	if !cmd.Bool("no-statusline") {
		statusLine = claudecode.StatusLineCommand(serverURL)
	}

	settings, err := claudecode.BuildSettings(claudecode.SettingsConfig{
		BaseURL:           serverURL,
		StatusLineCommand: statusLine,
		Inherited:         inherited,
	})
	if err != nil {
		return nil, "", err
	}
	return forwarded, settings, nil
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
