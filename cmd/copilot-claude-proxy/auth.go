package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/fabriziosestito/copilot-claude-proxy/internal/auth"
	"github.com/fabriziosestito/copilot-claude-proxy/internal/storage"
)

func newAuthCommand() *cli.Command {
	return &cli.Command{
		Name:  "auth",
		Usage: "Authenticate with GitHub via the device flow and store the token",
		Flags: []cli.Flag{
			verboseFlag(),
		},
		Action: runAuth,
	}
}

func runAuth(ctx context.Context, cmd *cli.Command) error {
	logger := newLogger(cmd.Bool("verbose"))
	store := storage.NewTokenStore()

	_, err := auth.Login(ctx, &http.Client{}, store, logger, os.Stdout)
	return err
}

func newLogoutCommand() *cli.Command {
	return &cli.Command{
		Name:  "logout",
		Usage: "Remove GitHub tokens from the system keyring",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "account",
				Usage: "Remove only this GitHub login",
			},
		},
		Action: runLogout,
	}
}

func runLogout(_ context.Context, cmd *cli.Command) error {
	store := storage.NewTokenStore()
	account := strings.TrimSpace(cmd.String("account"))
	if account != "" {
		removed, err := store.ClearAccount(account)
		if err != nil {
			return err
		}
		if !removed {
			fmt.Fprintf(os.Stdout, "No stored account %s found.\n", account)
			return nil
		}
		fmt.Fprintf(os.Stdout, "Removed account %s from the system keyring.\n", account)
		return nil
	}

	accounts, err := store.Accounts()
	if err != nil {
		return err
	}
	if len(accounts) == 0 {
		fmt.Fprintln(os.Stdout, "No stored GitHub token found.")
		return nil
	}
	for _, stored := range accounts {
		if stored.Name == "default" {
			continue
		}
		if _, clearErr := store.ClearAccount(stored.Name); clearErr != nil {
			return clearErr
		}
	}
	if _, clearErr := store.Clear(); clearErr != nil {
		return clearErr
	}
	fmt.Fprintln(os.Stdout, "Removed all GitHub tokens from the system keyring.")
	return nil
}

func newAccountsCommand() *cli.Command {
	return &cli.Command{
		Name:  "accounts",
		Usage: "List authenticated GitHub accounts and show the active proxy account",
		Flags: []cli.Flag{portFlag(), hostFlag()},
		Commands: []*cli.Command{
			{
				Name:      "switch",
				Aliases:   []string{"swith"},
				Usage:     "Select the account used by the running proxy",
				ArgsUsage: "<github-login>",
				Flags:     []cli.Flag{portFlag(), hostFlag()},
				Action:    runAccountSwitch,
			},
		},
		Action: runAccounts,
	}
}

func runAccounts(ctx context.Context, cmd *cli.Command) error {
	accounts, err := storage.NewTokenStore().Accounts()
	if err != nil {
		return err
	}
	if len(accounts) == 0 {
		fmt.Fprintln(os.Stdout, "No stored GitHub accounts found.")
		return nil
	}
	usage, usageErr := fetchUsage(ctx, cmd)
	if usageErr != nil {
		fmt.Fprintln(os.Stdout, "ACCOUNT\tCURRENT")
		for _, account := range accounts {
			fmt.Fprintf(os.Stdout, "%s\t-\n", account.Name)
		}
		fmt.Fprintln(os.Stdout, "\nProxy is not running; current account is unavailable.")
		return nil
	}

	fmt.Fprintln(os.Stdout, "ACCOUNT\tCURRENT")
	for _, account := range accounts {
		marker := ""
		if strings.EqualFold(account.Name, usage.Current) {
			marker = "*"
		}
		fmt.Fprintf(os.Stdout, "%s\t%s\n", account.Name, marker)
	}
	return nil
}

func runAccountSwitch(ctx context.Context, cmd *cli.Command) error {
	account := strings.TrimSpace(cmd.Args().First())
	if account == "" {
		return fmt.Errorf("usage: accounts switch <github-login>")
	}
	usage, err := switchRuntimeAccount(ctx, cmd, account)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "Current account: %s\n", usage.Current)
	return nil
}
