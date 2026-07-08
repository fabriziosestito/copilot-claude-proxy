package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/fabriziosestito/copilot-claude-proxy/internal/auth"
	"github.com/fabriziosestito/copilot-claude-proxy/internal/storage"
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

	_, err = auth.Login(ctx, &http.Client{}, store, logger, os.Stdout)
	return err
}

func newLogoutCommand() *cli.Command {
	return &cli.Command{
		Name:   "logout",
		Usage:  "Remove the GitHub token from the system keyring",
		Action: runLogout,
	}
}

func runLogout(_ context.Context, _ *cli.Command) error {
	removed, err := storage.NewTokenStore().Clear()
	if err != nil {
		return err
	}
	if !removed {
		fmt.Fprintln(os.Stdout, "No stored GitHub token found.")
		return nil
	}
	fmt.Fprintln(os.Stdout, "Removed the GitHub token from the system keyring.")
	return nil
}
