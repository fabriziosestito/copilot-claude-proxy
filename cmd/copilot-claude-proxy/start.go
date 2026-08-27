package main

import (
	"context"

	"github.com/urfave/cli/v3"

	"github.com/fabriziosestito/copilot-claude-proxy/internal/server"
)

func newStartCommand() *cli.Command {
	return &cli.Command{
		Name:  "start",
		Usage: "Run the Anthropic-compatible proxy server",
		Flags: []cli.Flag{
			portFlag(),
			hostFlag(),
			accountTypeFlag(),
			githubTokenFlag(),
			modelMapFlag(),
			verboseFlag(),
		},
		Action: runStart,
	}
}

func runStart(ctx context.Context, cmd *cli.Command) error {
	pool, primary, logger, err := connectAccountPool(ctx, cmd)
	if err != nil {
		return err
	}
	return server.Run(ctx, server.RunConfig{
		Logger:  logger,
		Pool:    pool,
		Catalog: primary.Catalog,
		Host:    cmd.String("host"),
		Port:    cmd.Int("port"),
	})
}
