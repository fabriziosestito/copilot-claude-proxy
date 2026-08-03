package main

import (
	"io"
	"log/slog"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/fabriziosestito/copilot-claude-proxy/internal/claudecode"
)

const (
	defaultPort = 4141
	defaultHost = "127.0.0.1"
)

func newLogger(verbose bool) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	return newLoggerAt(os.Stderr, level)
}

func newLoggerAt(out io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: level}))
}

// clientURL is the base URL Claude Code should call this proxy at.
func clientURL(cmd *cli.Command) string {
	return claudecode.ClientURL(cmd.String("host"), cmd.Int("port"))
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

func logFileFlag() *cli.StringFlag {
	return &cli.StringFlag{
		Name:    "log-file",
		Aliases: []string{"l"},
		Usage:   "Append proxy logs to this file instead of sharing the terminal with Claude Code",
		Sources: cli.EnvVars("COPILOT_CLAUDE_PROXY_LOG_FILE"),
	}
}
