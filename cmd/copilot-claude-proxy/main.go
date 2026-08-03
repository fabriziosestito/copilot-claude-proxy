// Command copilot-claude-proxy exposes GitHub Copilot as an
// Anthropic-compatible API so Claude Code can use Copilot as its backend.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"
)

func main() {
	os.Exit(run())
}

// exitCoder is implemented by errors that carry a process exit status.
type exitCoder interface {
	ExitCode() int
}

func run() int {
	ctx, signals, stop := notifySignals(context.Background())
	defer stop()

	//nolint:wrapcheck // CLI errors are user-facing as-is.
	if err := rootCommand(signals).Run(ctx, os.Args); err != nil {
		if errors.Is(err, context.Canceled) {
			return 0
		}
		// A child process that already reported its own failure propagates its
		// status silently.
		var coder exitCoder
		if errors.As(err, &coder) {
			return coder.ExitCode()
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

func rootCommand(signals *signalHandling) *cli.Command {
	return &cli.Command{
		Name:  "copilot-claude-proxy",
		Usage: "Expose GitHub Copilot as an Anthropic-compatible API for Claude Code",
		// The default handler calls os.Exit on ExitCoder errors, skipping the
		// deferred cleanup in run; every exit path goes through run instead.
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Commands: []*cli.Command{
			newAuthCommand(),
			newLogoutCommand(),
			newStartCommand(),
			newRunCommand(signals),
			newModelsCommand(),
			newSetupCommand(),
			newStatuslineCommand(),
		},
	}
}
