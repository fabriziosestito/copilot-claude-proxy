// Command copilot-claude-proxy exposes GitHub Copilot as an
// Anthropic-compatible API so Claude Code can use Copilot as its backend.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/urfave/cli/v3"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	//nolint:wrapcheck // CLI errors are user-facing as-is.
	if err := rootCommand().Run(ctx, os.Args); err != nil {
		if errors.Is(err, context.Canceled) {
			return 0
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

func rootCommand() *cli.Command {
	return &cli.Command{
		Name:  "copilot-claude-proxy",
		Usage: "Expose GitHub Copilot as an Anthropic-compatible API for Claude Code",
		Commands: []*cli.Command{
			newAuthCommand(),
			newLogoutCommand(),
			newAccountsCommand(),
			newStartCommand(),
			newStatsCommand(),
			newModelsCommand(),
			newSetupCommand(),
		},
	}
}
