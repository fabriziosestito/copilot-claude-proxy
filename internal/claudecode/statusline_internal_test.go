package claudecode

import (
	"errors"
	"testing"
)

func TestStatusLineBinaryPrefersADurableInstallPath(t *testing.T) {
	t.Parallel()
	installed := "/usr/local/bin/copilot-claude-proxy"

	if got := statusLineBinary(func() (string, error) { return installed, nil }); got != installed {
		t.Errorf("statusLineBinary = %q, want the installed path %q", got, installed)
	}
}

func TestStatusLineBinaryUsesResolvedPathForTemporaryBuilds(t *testing.T) {
	t.Parallel()
	// A `go run` build output stays in place for the session, so it is used
	// verbatim rather than replaced with the bare, not-on-PATH name.
	build := "/Users/x/Library/Caches/go-build/ab/cd-d/exe/copilot-claude-proxy"

	if got := statusLineBinary(func() (string, error) { return build, nil }); got != build {
		t.Errorf("statusLineBinary = %q, want the build path %q", got, build)
	}
}

func TestStatusLineBinaryFallsBackWhenLookupFails(t *testing.T) {
	t.Parallel()
	got := statusLineBinary(func() (string, error) { return "", errors.New("no executable") })
	if got != proxyBinaryName {
		t.Errorf("statusLineBinary = %q, want the %q fallback", got, proxyBinaryName)
	}
}

func TestStatusLineCommandOnWindows(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		binary string
		want   string
	}{
		// Bare tokens are the only spelling both Git Bash and PowerShell
		// invoke, and forward slashes keep Git Bash from eating separators.
		"plain path stays bare": {
			binary: `C:\Tools\copilot-claude-proxy.exe`,
			want:   `C:/Tools/copilot-claude-proxy.exe statusline --url http://127.0.0.1:4141`,
		},
		// A path with spaces cannot be bare; Git Bash quoting is the best
		// remaining option.
		"path with spaces is quoted": {
			binary: `C:\Program Files\copilot-claude-proxy.exe`,
			want:   `'C:/Program Files/copilot-claude-proxy.exe' statusline --url http://127.0.0.1:4141`,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := statusLineCommand("windows", test.binary, "http://127.0.0.1:4141")
			if got != test.want {
				t.Errorf("statusLineCommand = %q, want %q", got, test.want)
			}
		})
	}
}
