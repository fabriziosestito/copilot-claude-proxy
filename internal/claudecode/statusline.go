package claudecode

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// proxyBinaryName is this tool's own executable name, used when its install
// location cannot be resolved.
const proxyBinaryName = "copilot-claude-proxy"

// StatusLineCommand builds the command Claude Code should run to render the
// status line, pinned to this executable so it keeps working when the binary
// is not on PATH. Claude Code runs the command through a shell, so both the
// executable path and the URL are quoted for the shell of the platform.
func StatusLineCommand(serverURL string) string {
	return statusLineCommand(runtime.GOOS, statusLineBinary(os.Executable), serverURL)
}

// statusLineCommand renders the command line, taking the platform as an
// argument so both spellings are testable.
//
// On POSIX systems Claude Code runs the command under /bin/sh, where single
// quotes make any value one literal argument. On Windows it uses Git Bash
// when installed and PowerShell otherwise; both invoke a bare token, but
// PowerShell does not invoke a leading quoted string at all, so values are
// left bare when every character is shell-neutral — with backslashes
// rewritten to forward slashes, which Git Bash would otherwise consume as
// escapes — and single-quoted (the Git Bash spelling) only when quoting is
// unavoidable.
func statusLineCommand(goos, binary, serverURL string) string {
	if goos == "windows" {
		// Not filepath.ToSlash: that swaps the separator of the build
		// platform, while this branch is selected by goos. A backslash is
		// never an ordinary character in a Windows path, so replacing all of
		// them is safe.
		binary = strings.ReplaceAll(binary, `\`, "/")
		return windowsArg(binary) + " statusline --url " + windowsArg(serverURL)
	}
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

// windowsArg leaves a shell-neutral value bare so both Git Bash and
// PowerShell invoke it, quoting only when the value would otherwise be split
// or reinterpreted.
func windowsArg(value string) string {
	if isShellNeutral(value) {
		return value
	}
	return shellQuote(value)
}

// isShellNeutral reports whether every character passes through Git Bash and
// PowerShell verbatim, covering drive-letter paths and http URLs.
func isShellNeutral(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("/:._+-", r):
		default:
			return false
		}
	}
	return true
}
