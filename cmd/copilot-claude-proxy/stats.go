package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"

	"github.com/urfave/cli/v3"

	"github.com/fabriziosestito/copilot-claude-proxy/internal/copilot"
)

func newStatsCommand() *cli.Command {
	return &cli.Command{
		Name:   "stats",
		Usage:  "Show runtime request, rate-limit, and account failover statistics",
		Flags:  []cli.Flag{portFlag(), hostFlag()},
		Action: runStats,
	}
}

func runStats(ctx context.Context, cmd *cli.Command) error {
	usage, err := fetchUsage(ctx, cmd)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "Current account: %s\n", usage.Current)
	fmt.Fprintf(os.Stdout, "Upstream requests: %d\n", usage.Requests)
	fmt.Fprintf(os.Stdout, "Account failovers: %d\n\n", usage.Failovers)
	fmt.Fprintln(os.Stdout, "ACCOUNT\tREQUESTS\tRATE LIMITED\tQUOTA EXCEEDED")
	for _, account := range usage.Accounts {
		fmt.Fprintf(os.Stdout, "%s\t%d\t%d\t%d\n",
			account.Name, account.Requests, account.RateLimited, account.QuotaExceeded)
	}
	return nil
}

func fetchUsage(ctx context.Context, cmd *cli.Command) (copilot.PoolUsage, error) {
	url := proxyURL(cmd, "/stats")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return copilot.PoolUsage{}, fmt.Errorf("build stats request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return copilot.PoolUsage{}, fmt.Errorf("read proxy stats (is the proxy running at %s?): %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return copilot.PoolUsage{}, fmt.Errorf("stats endpoint returned %s", resp.Status)
	}

	var usage copilot.PoolUsage
	if err := json.NewDecoder(resp.Body).Decode(&usage); err != nil {
		return copilot.PoolUsage{}, fmt.Errorf("decode proxy stats: %w", err)
	}
	return usage, nil
}

func switchRuntimeAccount(
	ctx context.Context,
	cmd *cli.Command,
	account string,
) (copilot.PoolUsage, error) {
	body, err := json.Marshal(map[string]string{"account": account})
	if err != nil {
		return copilot.PoolUsage{}, fmt.Errorf("encode account switch request: %w", err)
	}
	url := proxyURL(cmd, "/accounts/switch")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return copilot.PoolUsage{}, fmt.Errorf("build account switch request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return copilot.PoolUsage{}, fmt.Errorf("switch proxy account (is the proxy running at %s?): %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var payload struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if decodeErr := json.NewDecoder(resp.Body).Decode(&payload); decodeErr == nil && payload.Error.Message != "" {
			return copilot.PoolUsage{}, fmt.Errorf("switch account: %s", payload.Error.Message)
		}
		return copilot.PoolUsage{}, fmt.Errorf("account switch endpoint returned %s", resp.Status)
	}
	var usage copilot.PoolUsage
	if err := json.NewDecoder(resp.Body).Decode(&usage); err != nil {
		return copilot.PoolUsage{}, fmt.Errorf("decode account switch response: %w", err)
	}
	return usage, nil
}

func proxyURL(cmd *cli.Command, path string) string {
	return "http://" + net.JoinHostPort(cmd.String("host"), strconv.Itoa(cmd.Int("port"))) + path
}
