package claudecode

import (
	"os"
	"path/filepath"
	"strings"
)

// proxyBinaryName is this tool's own executable name, used when its install
// location cannot be pinned.
const proxyBinaryName = "copilot-claude-proxy"

// StatusLineCommand builds the command Claude Code should run to render the
// status line, pinned to this executable so it keeps working when the binary
// is not on PATH. Temporary build outputs (`go run`) are not durable
// locations, so those fall back to the plain name.
func StatusLineCommand(serverURL string) string {
	return statusLineBinary(os.Executable) + " statusline --url " + serverURL
}

// statusLineBinary resolves the executable path, taking the lookup as an
// argument so the fallback is testable.
func statusLineBinary(executable func() (string, error)) string {
	path, err := executable()
	if err != nil {
		return proxyBinaryName
	}
	if resolved, linkErr := filepath.EvalSymlinks(path); linkErr == nil {
		path = resolved
	}
	if isTemporaryPath(path) {
		return proxyBinaryName
	}
	return path
}

// isTemporaryPath reports whether a path lives somewhere that will not exist
// on the next run, such as the `go run` build cache.
func isTemporaryPath(path string) bool {
	if strings.Contains(path, "/go-build") {
		return true
	}
	tempDir := os.TempDir()
	if withinDir(tempDir, path) {
		return true
	}
	// TMPDIR is routinely a symlink (/var -> /private/var on macOS), so a path
	// can name the same directory without sharing its prefix.
	resolved, err := filepath.EvalSymlinks(tempDir)
	return err == nil && resolved != tempDir && withinDir(resolved, path)
}

// withinDir reports whether path is dir or lives underneath it.
func withinDir(dir, path string) bool {
	relative, err := filepath.Rel(dir, path)
	return err == nil && !strings.HasPrefix(relative, "..")
}
