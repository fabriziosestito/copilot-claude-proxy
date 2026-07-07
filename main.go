// Command copilot-claude-proxy exposes GitHub Copilot as an
// Anthropic-compatible API so Claude Code can use Copilot as its backend.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/fabrizio/copilot-claude-proxy/internal/cmd"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := cmd.Run(ctx, os.Args); err != nil {
		if errors.Is(err, context.Canceled) {
			return 0
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}
