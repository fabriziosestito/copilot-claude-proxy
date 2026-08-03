package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// statusLineKey is the settings.json key Claude Code reads its status line
// command from.
const statusLineKey = "statusLine"

// proxyBinaryName is this tool's own executable name, used when its install
// location cannot be pinned.
const proxyBinaryName = "copilot-claude-proxy"

// statusLineSubcommand identifies a status line this tool installed, so an
// existing one can be refreshed in place while a hand-written one is left
// alone unless the user approves replacing it.
const statusLineSubcommand = proxyBinaryName + " statusline"

// statusLine is the part of the settings.json block this tool inspects; the
// remaining fields (padding, refreshInterval) are preserved as raw JSON.
type statusLine struct {
	Command string `json:"command"`
}

// StatusLinePlan describes the proposed status line change.
type StatusLinePlan struct {
	// Command is the proposed command line.
	Command string
	// Previous is the command currently configured, empty when there is none.
	Previous string
	// Foreign is true when the existing status line was not installed by this
	// tool, so applying the plan would discard someone else's configuration.
	Foreign bool
}

// Empty reports whether applying the plan would change nothing.
func (p *StatusLinePlan) Empty() bool { return p == nil || p.Command == p.Previous }

// Destructive reports whether the plan replaces a status line this tool did
// not write.
func (p *StatusLinePlan) Destructive() bool {
	return p != nil && !p.Empty() && p.Foreign && p.Previous != ""
}

// Format renders the plan the way ChangeSet renders env changes.
func (p *StatusLinePlan) Format() string {
	switch {
	case p.Empty():
		return ""
	case p.Previous == "":
		return fmt.Sprintf("  + %s = %s", statusLineKey, p.Command)
	default:
		return fmt.Sprintf("  ~ %s: %s -> %s", statusLineKey, p.Previous, p.Command)
	}
}

// StatusLineCommand builds the command Claude Code should run, pinned to this
// executable so a status line keeps working when the binary is not on PATH.
// Temporary build outputs (`go run`) are not durable locations, so those fall
// back to the plain name.
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

// planStatusLine compares the desired command with what settings.json holds.
// A null or absent block counts as no status line at all.
func planStatusLine(settings map[string]json.RawMessage, command string) (*StatusLinePlan, error) {
	plan := &StatusLinePlan{Command: command}

	raw, present := settings[statusLineKey]
	if !present || string(raw) == jsonNull {
		return plan, nil
	}

	var existing statusLine
	if err := json.Unmarshal(raw, &existing); err != nil {
		return nil, fmt.Errorf("%s block is not an object, fix or remove it: %w", statusLineKey, err)
	}
	plan.Previous = existing.Command
	plan.Foreign = !strings.Contains(existing.Command, statusLineSubcommand)
	return plan, nil
}

// applyStatusLine writes the block, preserving the sibling fields of an
// existing one (padding, refreshInterval, hideVimModeIndicator) so only the
// command changes.
func applyStatusLine(settings map[string]json.RawMessage, command string) error {
	block := map[string]json.RawMessage{}
	if raw, present := settings[statusLineKey]; present && string(raw) != jsonNull {
		if err := json.Unmarshal(raw, &block); err != nil {
			return fmt.Errorf("%s block is not an object, fix or remove it: %w", statusLineKey, err)
		}
	}

	encoded, err := json.Marshal(command)
	if err != nil {
		return fmt.Errorf("encode %s command: %w", statusLineKey, err)
	}
	block["type"] = json.RawMessage(`"command"`)
	block["command"] = encoded

	updated, err := json.Marshal(block)
	if err != nil {
		return fmt.Errorf("encode %s block: %w", statusLineKey, err)
	}
	settings[statusLineKey] = updated
	return nil
}
