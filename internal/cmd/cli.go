// Package cmd wires the command line interface of the proxy.
package cmd

import (
	"context"
	"log/slog"
	"os"

	"github.com/urfave/cli/v3"
)

// Run executes the CLI.
func Run(ctx context.Context, args []string) error {
	return newRootCommand().Run(ctx, args) //nolint:wrapcheck // CLI errors are user-facing as-is.
}

func newRootCommand() *cli.Command {
	return &cli.Command{
		Name:  "copilot-claude-proxy",
		Usage: "Expose GitHub Copilot as an Anthropic-compatible API for Claude Code",
		Commands: []*cli.Command{
			newAuthCommand(),
			newLogoutCommand(),
			newStartCommand(),
			newModelsCommand(),
			newSetupCommand(),
		},
	}
}

func newLogger(verbose bool) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

func verboseFlag() *cli.BoolFlag {
	return &cli.BoolFlag{
		Name:    "verbose",
		Aliases: []string{"v"},
		Usage:   "Enable debug logging",
	}
}

func githubTokenFlag() *cli.StringFlag {
	return &cli.StringFlag{
		Name:    "github-token",
		Aliases: []string{"g"},
		Usage:   "GitHub OAuth token to use instead of the stored one",
		Sources: cli.EnvVars("COPILOT_CLAUDE_PROXY_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"),
	}
}

func accountTypeFlag() *cli.StringFlag {
	return &cli.StringFlag{
		Name:    "account-type",
		Aliases: []string{"a"},
		Value:   "auto",
		Usage:   "Copilot account tier: auto, individual, business, or enterprise",
		Sources: cli.EnvVars("COPILOT_CLAUDE_PROXY_ACCOUNT_TYPE"),
	}
}

func portFlag() *cli.IntFlag {
	return &cli.IntFlag{
		Name:    "port",
		Aliases: []string{"p"},
		Value:   defaultPort,
		Usage:   "Port the proxy listens on",
		Sources: cli.EnvVars("COPILOT_CLAUDE_PROXY_PORT"),
	}
}

func hostFlag() *cli.StringFlag {
	return &cli.StringFlag{
		Name:    "host",
		Aliases: []string{"H"},
		Value:   defaultHost,
		Usage:   "Host the proxy binds to",
		Sources: cli.EnvVars("COPILOT_CLAUDE_PROXY_HOST"),
	}
}

func modelMapFlag() *cli.StringMapFlag {
	return &cli.StringMapFlag{
		Name:  "model-map",
		Usage: "Extra model aliases as alias=model-id (e.g. haiku=claude-haiku-4.5); repeatable",
	}
}
