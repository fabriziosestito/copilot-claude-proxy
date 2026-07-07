package main

import (
	"context"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/fabrizio/copilot-claude-proxy/internal/copilot"
)

func newModelsCommand() *cli.Command {
	return &cli.Command{
		Name:  "models",
		Usage: "List the models available on this Copilot account",
		Flags: []cli.Flag{
			accountTypeFlag(),
			githubTokenFlag(),
			verboseFlag(),
		},
		Action: runModels,
	}
}

func runModels(ctx context.Context, cmd *cli.Command) error {
	session, _, err := connect(ctx, cmd)
	if err != nil {
		return err
	}
	if refreshErr := session.Catalog.Refresh(ctx); refreshErr != nil {
		return refreshErr
	}
	return copilot.WriteModelTable(os.Stdout, session.Catalog.Models())
}
