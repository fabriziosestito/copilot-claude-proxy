//go:build !windows

package claudecode

import (
	"os"
	"syscall"
)

// terminate asks Claude Code to exit. SIGTERM rather than SIGKILL gives it a
// chance to save its session; [Launch]'s WaitDelay bounds how long that grace
// can take.
func terminate(process *os.Process) error {
	//nolint:wrapcheck // the caller (exec.Cmd.Cancel) inspects the raw error.
	return process.Signal(syscall.SIGTERM)
}
