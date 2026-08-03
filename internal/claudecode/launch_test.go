package claudecode_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fabriziosestito/copilot-claude-proxy/internal/claudecode"
)

func TestClientURL(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		host string
		port int
		want string
	}{
		"loopback":       {"127.0.0.1", 4141, "http://127.0.0.1:4141"},
		"hostname":       {"proxy.internal", 8080, "http://proxy.internal:8080"},
		"ipv4 wildcard":  {"0.0.0.0", 4141, "http://127.0.0.1:4141"},
		"ipv6 wildcard":  {"::", 4141, "http://[::1]:4141"},
		"ipv6 loopback":  {"::1", 4141, "http://[::1]:4141"},
		"empty defaults": {"", 4141, "http://127.0.0.1:4141"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := claudecode.ClientURL(test.host, test.port); got != test.want {
				t.Errorf("ClientURL(%q, %d) = %q, want %q", test.host, test.port, got, test.want)
			}
		})
	}
}

// script writes an executable shell script and returns its path.
func script(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stub")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return path
}

func TestLaunchForwardsArgsAndPinsProxyEnv(t *testing.T) {
	// Values left over from an earlier proxy, which the launch must replace.
	t.Setenv("ANTHROPIC_BASE_URL", "http://127.0.0.1:9999")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "stale")

	output := filepath.Join(t.TempDir(), "out")
	stub := script(t,
		`printf '%s|%s|%s\n' "$*" "$ANTHROPIC_BASE_URL" "$ANTHROPIC_AUTH_TOKEN" > "`+output+`"`)

	status, err := claudecode.Launch(t.Context(), claudecode.LaunchConfig{
		Path:    stub,
		Args:    []string{"--resume", "hello world"},
		BaseURL: "http://127.0.0.1:4242",
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if status != 0 {
		t.Errorf("status = %d, want 0", status)
	}

	got, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read stub output: %v", err)
	}
	want := "--resume hello world|http://127.0.0.1:4242|copilot-claude-proxy\n"
	if string(got) != want {
		t.Errorf("stub recorded %q, want %q", got, want)
	}
}

func TestLaunchReturnsExitStatus(t *testing.T) {
	t.Parallel()
	status, err := claudecode.Launch(t.Context(), claudecode.LaunchConfig{
		Path: script(t, "exit 7"),
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if status != 7 {
		t.Errorf("status = %d, want 7", status)
	}
}

func TestLaunchMissingBinaryErrors(t *testing.T) {
	t.Parallel()
	if _, err := claudecode.Launch(t.Context(), claudecode.LaunchConfig{
		Path: filepath.Join(t.TempDir(), "absent"),
	}); err == nil {
		t.Fatal("expected an error for a missing executable")
	}
}

func TestLaunchCancelTerminatesChild(t *testing.T) {
	t.Parallel()
	// The child traps SIGTERM so a plain kill would not produce this status.
	stub := script(t, `trap 'exit 42' TERM
i=0
while [ $i -lt 200 ]; do sleep 0.05; i=$((i+1)); done`)

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	status, err := claudecode.Launch(ctx, claudecode.LaunchConfig{Path: stub})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if status != 42 {
		t.Errorf("status = %d, want 42 (SIGTERM handled by the child)", status)
	}
}

func TestLookupReportsInstallHint(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := claudecode.Lookup()
	if err == nil {
		t.Fatal("expected an error when claude is absent from PATH")
	}
	if !strings.Contains(err.Error(), claudecode.BinaryName) {
		t.Errorf("error %q does not mention %q", err, claudecode.BinaryName)
	}
}
