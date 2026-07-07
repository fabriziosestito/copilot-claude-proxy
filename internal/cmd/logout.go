package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/fabrizio/copilot-claude-proxy/internal/storage"
)

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
