package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/fabrizio/copilot-claude-proxy/internal/storage"
)

func newAuthCommand() *cli.Command {
	return &cli.Command{
		Name:  "auth",
		Usage: "Authenticate with GitHub via the device flow and store the token",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "force",
				Aliases: []string{"f"},
				Usage:   "Re-authenticate even when a token is already stored",
			},
			verboseFlag(),
		},
		Action: runAuth,
	}
}

func runAuth(ctx context.Context, cmd *cli.Command) error {
	logger := newLogger(cmd.Bool("verbose"))
	store := storage.NewTokenStore()

	existing, err := store.Load()
	if err != nil {
		return err
	}
	if existing != "" && !cmd.Bool("force") {
		fmt.Fprintln(os.Stdout,
			"Already authenticated (token in the system keyring). Use --force to re-authenticate.")
		return nil
	}

	_, err = performDeviceFlow(ctx, &http.Client{}, store, logger)
	return err
}
