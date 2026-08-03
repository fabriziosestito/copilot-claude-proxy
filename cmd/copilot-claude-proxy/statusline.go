package main

import (
	"context"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/fabriziosestito/copilot-claude-proxy/internal/claudecode"
	"github.com/fabriziosestito/copilot-claude-proxy/internal/statusline"
)

// newStatuslineCommand builds the entry point Claude Code invokes for its
// status line. It is hidden because nobody runs it by hand: `setup` wires it
// into settings.json and Claude Code calls it with a JSON payload on stdin.
func newStatuslineCommand() *cli.Command {
	return &cli.Command{
		Name:   "statusline",
		Hidden: true,
		Usage:  "Render the Claude Code status line for a running proxy (used by settings.json)",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "url",
				Value: claudecode.ClientURL(defaultHost, defaultPort),
				Usage: "Base URL of the proxy to report on",
				// ANTHROPIC_BASE_URL is deliberately not a source: it can point
				// at a non-proxy endpoint, and this command runs several times a
				// minute.
				Sources: cli.EnvVars("COPILOT_CLAUDE_PROXY_URL"),
			},
		},
		Action: runStatusline,
	}
}

func runStatusline(ctx context.Context, cmd *cli.Command) error {
	//nolint:wrapcheck // the package already returns user-facing errors.
	return statusline.Run(ctx, statusline.Config{
		BaseURL: cmd.String("url"),
		In:      os.Stdin,
		Out:     os.Stdout,
		Color:   os.Getenv("NO_COLOR") == "",
	})
}
