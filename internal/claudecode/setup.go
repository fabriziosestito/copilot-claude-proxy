package claudecode

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	settingsDirName  = ".claude"
	settingsFileName = "settings.json"
	claudeJSONName   = ".claude.json"

	configDirPermissions  = 0o750
	configFilePermissions = 0o600

	onboardingKey = "hasCompletedOnboarding"
	envKey        = "env"

	// jsonNull is the literal text of an explicitly unset JSON value.
	jsonNull = "null"
)

// SetupConfig describes a Claude Code setup run.
type SetupConfig struct {
	// Home is the user's home directory.
	Home string
	// Env describes the desired environment block.
	Env EnvConfig
	// StatusLine is the command to install as the Claude Code status line.
	// Empty leaves any existing status line untouched.
	StatusLine string
}

// Setup is a planned configuration change, computed by PlanSetup and written
// by Apply. Splitting plan from apply lets callers show a diff and ask for
// confirmation in between.
type Setup struct {
	// Env is the proposed complete env block.
	Env map[string]string
	// Changes describes how Env differs from the current settings.
	Changes ChangeSet
	// StatusLine is the proposed status line change, nil when none was asked
	// for.
	StatusLine *StatusLinePlan
	// OnboardingNeeded is true when ~/.claude.json lacks the onboarding flag.
	OnboardingNeeded bool

	settingsPath   string
	claudeJSONPath string
}

// PlanSetup reads the current Claude Code configuration and computes the
// proposed changes without writing anything.
func PlanSetup(cfg SetupConfig) (*Setup, error) {
	settingsPath := filepath.Join(cfg.Home, settingsDirName, settingsFileName)
	claudeJSONPath := filepath.Join(cfg.Home, claudeJSONName)

	settings, err := readJSONObject(settingsPath)
	if err != nil {
		return nil, err
	}
	claudeJSON, err := readJSONObject(claudeJSONPath)
	if err != nil {
		return nil, err
	}

	existingEnv, err := decodeEnv(settings[envKey])
	if err != nil {
		return nil, fmt.Errorf("%s: %w", settingsPath, err)
	}
	proposedEnv := BuildEnv(cfg.Env, existingEnv)

	var statusLinePlan *StatusLinePlan
	if cfg.StatusLine != "" {
		if statusLinePlan, err = planStatusLine(settings, cfg.StatusLine); err != nil {
			return nil, fmt.Errorf("%s: %w", settingsPath, err)
		}
	}

	var onboarded bool
	if raw, ok := claudeJSON[onboardingKey]; ok {
		_ = json.Unmarshal(raw, &onboarded)
	}

	return &Setup{
		Env:              proposedEnv,
		Changes:          DiffEnv(existingEnv, proposedEnv),
		StatusLine:       statusLinePlan,
		OnboardingNeeded: !onboarded,
		settingsPath:     settingsPath,
		claudeJSONPath:   claudeJSONPath,
	}, nil
}

// NeedsWrite reports whether Apply would change anything on disk.
func (s *Setup) NeedsWrite() bool {
	return !s.Changes.Empty() || !s.StatusLine.Empty() || s.OnboardingNeeded
}

// Destructive reports whether applying would overwrite or delete anything the
// user already configured.
func (s *Setup) Destructive() bool {
	return s.Changes.Destructive() || s.StatusLine.Destructive()
}

// SettingsPath returns the settings.json location.
func (s *Setup) SettingsPath() string { return s.settingsPath }

// ClaudeJSONPath returns the .claude.json location.
func (s *Setup) ClaudeJSONPath() string { return s.claudeJSONPath }

// Apply writes the planned configuration, preserving all unrelated keys of
// both files. Each file is re-read at write time so state written by a
// concurrent Claude Code session between plan and apply is kept, and only the
// files that actually need a change are touched.
func (s *Setup) Apply() error {
	if err := os.MkdirAll(filepath.Dir(s.settingsPath), configDirPermissions); err != nil {
		return fmt.Errorf("create claude config directory: %w", err)
	}

	if !s.Changes.Empty() || !s.StatusLine.Empty() {
		if err := s.writeSettings(); err != nil {
			return err
		}
	}

	if s.OnboardingNeeded {
		claudeJSON, err := readJSONObject(s.claudeJSONPath)
		if err != nil {
			return err
		}
		claudeJSON[onboardingKey] = json.RawMessage("true")
		return writeJSONObject(s.claudeJSONPath, claudeJSON)
	}
	return nil
}

// writeSettings merges the planned env and status line into settings.json.
func (s *Setup) writeSettings() error {
	settings, err := readJSONObject(s.settingsPath)
	if err != nil {
		return err
	}

	if !s.Changes.Empty() {
		envRaw, marshalErr := json.Marshal(s.Env)
		if marshalErr != nil {
			return fmt.Errorf("encode env block: %w", marshalErr)
		}
		settings[envKey] = envRaw
	}
	if !s.StatusLine.Empty() {
		if statusErr := applyStatusLine(settings, s.StatusLine.Command); statusErr != nil {
			return statusErr
		}
	}
	return writeJSONObject(s.settingsPath, settings)
}

// readJSONObject loads a JSON object file. A missing file yields an empty
// object; an unparseable file is an error rather than silently overwritten.
func readJSONObject(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	object := map[string]json.RawMessage{}
	if len(data) > 0 {
		if parseErr := json.Unmarshal(data, &object); parseErr != nil {
			return nil, fmt.Errorf("%s is not a valid JSON object, fix or remove it: %w", path, parseErr)
		}
	}
	return object, nil
}

// writeJSONObject writes the file atomically (temp file + rename) so a crash
// mid-write cannot leave truncated JSON behind.
func writeJSONObject(path string, object map[string]json.RawMessage) error {
	data, err := json.MarshalIndent(object, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	if err := tmp.Chmod(configFilePermissions); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// decodeEnv extracts the entries of a settings env block. Non-string scalars
// (numbers, booleans) are preserved by their literal JSON text — env values
// are strings anyway — while structured values are rejected instead of being
// silently blanked and rewritten to disk.
func decodeEnv(raw json.RawMessage) (map[string]string, error) {
	env := map[string]string{}
	if len(raw) == 0 {
		return env, nil
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("env block is not a JSON object: %w", err)
	}
	for key, value := range entries {
		var text string
		if json.Unmarshal(value, &text) == nil {
			env[key] = text
			continue
		}
		literal := strings.TrimSpace(string(value))
		if strings.HasPrefix(literal, "{") || strings.HasPrefix(literal, "[") || literal == jsonNull {
			return nil, fmt.Errorf("env value %q is not a string", key)
		}
		env[key] = literal
	}
	return env, nil
}
