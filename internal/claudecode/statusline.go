package claudecode

import (
	"os"
	"path/filepath"
	"strings"
)

// proxyBinaryName is this tool's own executable name, used when its install
// location cannot be resolved.
const proxyBinaryName = "copilot-claude-proxy"

// StatusLineCommand builds the command Claude Code should run to render the
// status line, pinned to this executable so it keeps working when the binary
// is not on PATH. Claude Code runs the command through a shell, so both the
// executable path and the URL are shell-quoted.
func StatusLineCommand(serverURL string) string {
	binary := statusLineBinary(os.Executable)
	return shellQuote(binary) + " statusline --url " + shellQuote(serverURL)
}

// statusLineBinary resolves the executable path, taking the lookup as an
// argument so the fallback is testable. The resolved path is used verbatim,
// including for `go run` build outputs: that binary stays in place for the
// lifetime of the session, whereas a bare name generally is not on PATH.
func statusLineBinary(executable func() (string, error)) string {
	path, err := executable()
	if err != nil {
		return proxyBinaryName
	}
	if resolved, linkErr := filepath.EvalSymlinks(path); linkErr == nil {
		path = resolved
	}
	return path
}

// shellQuote wraps a value in single quotes so a POSIX shell treats it as one
// literal argument, escaping any embedded single quotes.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
