package cmd

import (
	"context"
	"log/slog"
	"os/exec"
	"runtime"
)

// openBrowser makes a best-effort attempt to open the URL in the default
// browser. The device flow prints the URL as well, so failures are only
// logged.
func openBrowser(ctx context.Context, url string, logger *slog.Logger) {
	name, args := browserCommand(url)
	if _, err := exec.LookPath(name); err != nil {
		logger.DebugContext(ctx, "browser opener not available", "command", name)
		return
	}

	command := exec.CommandContext(ctx, name, args...)
	if err := command.Start(); err != nil {
		logger.DebugContext(ctx, "opening browser failed", "command", name, "error", err)
		return
	}
	// Reap the opener in the background; xdg-open and friends exit quickly.
	go func() { _ = command.Wait() }()
}

func browserCommand(url string) (string, []string) {
	switch runtime.GOOS {
	case "darwin":
		return "open", []string{url}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		return "xdg-open", []string{url}
	}
}
