package claudecode

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestStatusLineBinaryPrefersADurableInstallPath(t *testing.T) {
	t.Parallel()
	installed := "/usr/local/bin/copilot-claude-proxy"

	if got := statusLineBinary(func() (string, error) { return installed, nil }); got != installed {
		t.Errorf("statusLineBinary = %q, want the installed path %q", got, installed)
	}
}

func TestStatusLineBinaryFallsBackToPath(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		path      string
		err       error
		inTempDir bool
	}{
		"lookup failed": {err: errors.New("no executable")},
		"go run build cache": {
			path: "/Users/x/Library/Caches/go-build/ab/cd-d/exe/copilot-claude-proxy",
		},
		"temp directory": {inTempDir: true},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := test.path
			if test.inTempDir {
				path = filepath.Join(t.TempDir(), proxyBinaryName)
			}
			got := statusLineBinary(func() (string, error) { return path, test.err })
			if got != proxyBinaryName {
				t.Errorf("statusLineBinary = %q, want the %q fallback", got, proxyBinaryName)
			}
		})
	}
}
