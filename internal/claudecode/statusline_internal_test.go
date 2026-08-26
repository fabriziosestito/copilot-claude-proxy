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
