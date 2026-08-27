//go:build windows

package claudecode

import "os"

// terminate ends Claude Code immediately. Windows has no SIGTERM delivery to
// ask a process to exit, and the console-control alternative needs the child
// in its own process group, which would stop the terminal from handing
// Ctrl-C to Claude Code. Killing outright at least makes cancellation prompt
// instead of silently waiting out [Launch]'s WaitDelay first.
func terminate(process *os.Process) error {
	//nolint:wrapcheck // the caller (exec.Cmd.Cancel) inspects the raw error.
	return process.Kill()
}
