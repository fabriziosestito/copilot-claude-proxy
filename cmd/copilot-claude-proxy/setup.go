package main

import (
	"context"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/fabriziosestito/copilot-claude-proxy/internal/setup"
)

func newSetupCommand() *cli.Command {
	return &cli.Command{
		Name:  "setup",
		Usage: "Write Claude Code configuration pointing it at this proxy",
		Flags: []cli.Flag{
			portFlag(),
			hostFlag(),
			&cli.StringFlag{
				Name:    "model",
				Aliases: []string{"m"},
				Usage:   "Main model (requires --small-model; omit both for interactive selection)",
			},
			&cli.StringFlag{
				Name:    "small-model",
				Aliases: []string{"s"},
				Usage:   "Small/fast model for background tasks (requires --model)",
			},
			&cli.BoolFlag{
				Name:    "with-extras",
				Aliases: []string{"e"},
				Usage:   "Also write opinionated tuning vars (telemetry off, auto-compact, caching fix)",
			},
			&cli.BoolFlag{
				Name:    "yes",
				Aliases: []string{"y"},
				Usage:   "Apply changes without asking for confirmation",
			},
			accountTypeFlag(),
			githubTokenFlag(),
			verboseFlag(),
		},
		Action: runSetup,
	}
}

func runSetup(ctx context.Context, cmd *cli.Command) error {
	session, _, err := connect(ctx, cmd)
	if err != nil {
		return err
	}
	if refreshErr := session.Catalog.Refresh(ctx); refreshErr != nil {
		return refreshErr
	}

	serverURL := clientURL(cmd)
	return setup.Run(setup.Config{
		Catalog:     session.Catalog,
		ServerURL:   serverURL,
		Model:       cmd.String("model"),
		SmallModel:  cmd.String("small-model"),
		WithExtras:  cmd.Bool("with-extras"),
		AutoApprove: cmd.Bool("yes"),
		In:          os.Stdin,
		Out:         os.Stdout,
	})
}
