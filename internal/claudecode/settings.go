package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// SettingsFlag is the Claude Code flag that loads an additional settings
// document, either a path to a JSON file or a JSON document inline.
const SettingsFlag = "--settings"

// statusLineKey is the settings key Claude Code reads its status line command
// from, and baseURLKey and authTokenKey are the environment entries that point
// it at a proxy.
const (
	statusLineKey = "statusLine"
	baseURLKey    = "ANTHROPIC_BASE_URL"
	authTokenKey  = "ANTHROPIC_AUTH_TOKEN" //nolint:gosec // a variable name, not a credential.
)

// statusLineBlock is the settings.json representation of a status line.
type statusLineBlock struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// SettingsConfig describes the settings document handed to Claude Code.
type SettingsConfig struct {
	// BaseURL is the proxy Claude Code should send requests to.
	BaseURL string
	// StatusLineCommand renders the status line. Empty installs none and
	// leaves any inherited status line in place.
	StatusLineCommand string
	// Inherited is a --settings value the user supplied themselves, either a
	// path to a JSON file or a JSON document. It forms the base of the merge.
	Inherited string
}

// BuildSettings renders the JSON document passed to `claude --settings`.
//
// Claude Code layers this document over ~/.claude/settings.json key by key, so
// only what has to point at this proxy is set here and the model selection
// `setup` wrote survives untouched. It also takes precedence over the
// inherited environment, which is what makes --host/--port effective.
//
// A second --settings on the command line replaces this one wholesale rather
// than merging with it, so a document the user supplied is merged in here
// instead of being forwarded as a flag of its own. Their keys are kept except
// where they would point the session away from the proxy.
func BuildSettings(cfg SettingsConfig) (string, error) {
	document, err := inheritedSettings(cfg.Inherited)
	if err != nil {
		return "", err
	}

	if envErr := overlayEnv(document, cfg.BaseURL); envErr != nil {
		return "", envErr
	}
	if cfg.StatusLineCommand != "" {
		block, marshalErr := json.Marshal(statusLineBlock{
			Type:    "command",
			Command: cfg.StatusLineCommand,
		})
		if marshalErr != nil {
			return "", fmt.Errorf("encode %s: %w", statusLineKey, marshalErr)
		}
		document[statusLineKey] = block
	}

	encoded, err := json.Marshal(document)
	if err != nil {
		return "", fmt.Errorf("encode settings: %w", err)
	}
	return string(encoded), nil
}

// inheritedSettings decodes the user's --settings value, which Claude Code
// accepts as either a file path or an inline JSON document.
func inheritedSettings(value string) (map[string]json.RawMessage, error) {
	document := map[string]json.RawMessage{}
	if value == "" {
		return document, nil
	}

	data := []byte(value)
	if !strings.HasPrefix(strings.TrimSpace(value), "{") {
		read, err := os.ReadFile(value)
		if err != nil {
			return nil, fmt.Errorf("read %s file: %w", SettingsFlag, err)
		}
		data = read
	}

	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("%s is not a JSON object: %w", SettingsFlag, err)
	}
	return document, nil
}

// overlayEnv sets the proxy connection in the document's env block, keeping
// every other entry the user put there. Values are written as raw JSON so an
// inherited block survives verbatim.
func overlayEnv(document map[string]json.RawMessage, baseURL string) error {
	env := map[string]json.RawMessage{}
	if raw, present := document[envKey]; present && string(raw) != jsonNull {
		if err := json.Unmarshal(raw, &env); err != nil {
			return fmt.Errorf("%s block is not an object: %w", envKey, err)
		}
	}

	for key, value := range map[string]string{
		baseURLKey:   baseURL,
		authTokenKey: defaultAuthToken,
	} {
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("encode %s: %w", key, err)
		}
		env[key] = encoded
	}

	encoded, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("encode %s block: %w", envKey, err)
	}
	document[envKey] = encoded
	return nil
}

// SplitSettingsArg removes a --settings flag from arguments meant for Claude
// Code and returns it separately, so it can be merged into the document this
// tool passes rather than replacing it. Both "--settings x" and
// "--settings=x" are recognized, and a repeated flag keeps the last value the
// way Claude Code itself would.
func SplitSettingsArg(args []string) ([]string, string, error) {
	remaining := make([]string, 0, len(args))
	var value string

	for index := 0; index < len(args); index++ {
		argument := args[index]
		switch {
		case argument == SettingsFlag:
			if index+1 >= len(args) {
				return nil, "", fmt.Errorf("%s needs a file path or a JSON document", SettingsFlag)
			}
			index++
			value = args[index]
		case strings.HasPrefix(argument, SettingsFlag+"="):
			value = strings.TrimPrefix(argument, SettingsFlag+"=")
		default:
			remaining = append(remaining, argument)
		}
	}
	return remaining, value, nil
}
