// Package setup configures Claude Code to use the proxy: it selects the
// models (explicitly or interactively), plans the configuration changes, and
// applies them after confirmation.
package setup

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/fabrizio/copilot-claude-proxy/internal/claudecode"
	"github.com/fabrizio/copilot-claude-proxy/internal/copilot"
)

var errNoAnthropicModels = errors.New(
	"no models supporting the Anthropic Messages API are available on this Copilot account")

// Config describes a setup run. Model and SmallModel must be provided
// together; when both are empty the models are selected interactively.
type Config struct {
	// Catalog is the refreshed model catalog of the Copilot account.
	Catalog *copilot.Catalog
	// ServerURL is the base URL Claude Code should call the proxy at.
	ServerURL string
	// Model and SmallModel request specific models by name or alias.
	Model      string
	SmallModel string
	// WithExtras also writes opinionated tuning environment variables.
	WithExtras bool
	// AutoApprove applies destructive changes without asking.
	AutoApprove bool
	// In and Out carry the interactive prompts.
	In  io.Reader
	Out io.Writer
}

// Run selects the models and writes the Claude Code configuration.
func Run(cfg Config) error {
	eligible := eligibleModels(cfg.Catalog)
	if len(eligible) == 0 {
		return errNoAnthropicModels
	}

	// A single stdin reader is shared by every prompt: a second bufio.Reader
	// would lose whatever the first one already buffered (e.g. the trailing
	// confirmation line of piped input).
	stdin := bufio.NewReader(cfg.In)

	model, smallModel, err := chooseModels(cfg, eligible, stdin)
	if err != nil {
		return err
	}
	warnFableChineseBug(cfg.Out, model, smallModel)

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}

	setup, err := claudecode.PlanSetup(claudecode.SetupConfig{
		Home: home,
		Env: claudecode.EnvConfig{
			ServerURL:  cfg.ServerURL,
			Model:      model,
			SmallModel: smallModel,
			WithExtras: cfg.WithExtras,
		},
	})
	if err != nil {
		return err
	}

	return applySetup(setup, cfg.AutoApprove, stdin, cfg.Out)
}

// applySetup previews the planned changes, asks for confirmation when they
// overwrite existing values, and writes them.
func applySetup(setup *claudecode.Setup, autoApprove bool, stdin *bufio.Reader, out io.Writer) error {
	if !setup.NeedsWrite() {
		fmt.Fprintln(out, "Claude Code is already configured for this proxy - no changes needed.")
		return nil
	}

	if !setup.Changes.Empty() {
		fmt.Fprintf(out, "Changes to %s:\n%s\n", setup.SettingsPath(), setup.Changes.Format())
	}
	if setup.OnboardingNeeded {
		fmt.Fprintf(out, "%s: set hasCompletedOnboarding = true\n", setup.ClaudeJSONPath())
	}

	if setup.Changes.Destructive() && !autoApprove {
		approved, err := promptConfirm(stdin, out, "Apply these changes?")
		if err != nil {
			return err
		}
		if !approved {
			fmt.Fprintln(out, "Aborted - nothing written.")
			return nil
		}
	}

	if err := setup.Apply(); err != nil {
		return err
	}

	fmt.Fprintf(out, "\nClaude Code configured.\n")
	fmt.Fprintf(out, "  Model:       %s\n", setup.Env["ANTHROPIC_MODEL"])
	fmt.Fprintf(out, "  Small model: %s\n", setup.Env["ANTHROPIC_DEFAULT_HAIKU_MODEL"])
	fmt.Fprintf(out, "  API URL:     %s\n", setup.Env["ANTHROPIC_BASE_URL"])
	fmt.Fprintf(out, "\nStart the proxy (copilot-claude-proxy start), then run 'claude'.\n")
	return nil
}

// fableModelPrefix identifies the claude-fable-5 model family, which has a
// known bug where responses can switch to Chinese mid-session.
const fableModelPrefix = "claude-fable-5"

// warnFableChineseBug prints a warning when a selected model belongs to the
// fable-5 family, pointing at the ~/.claude/CLAUDE.md language-pinning fix.
func warnFableChineseBug(out io.Writer, models ...copilot.Model) {
	affected := false
	for _, model := range models {
		if strings.HasPrefix(strings.ToLower(model.ID), fableModelPrefix) {
			affected = true
			break
		}
	}
	if !affected {
		return
	}
	fmt.Fprintf(out, `
Warning: claude-fable-5 has a known bug where it can start responding (and
thinking) in Chinese mid-session. Pin the language in ~/.claude/CLAUDE.md:

  # User preferences

  - Always respond and display your thinking in English, regardless of the
    language used in project files, documentation, or code comments.

`)
}

func eligibleModels(catalog *copilot.Catalog) []copilot.Model {
	var eligible []copilot.Model
	for _, model := range catalog.Models() {
		if model.SupportsAnthropicMessages() {
			eligible = append(eligible, model)
		}
	}
	return eligible
}

// chooseModels returns the main and small models from the config or
// interactively.
func chooseModels(
	cfg Config,
	eligible []copilot.Model,
	stdin *bufio.Reader,
) (copilot.Model, copilot.Model, error) {
	mainRequested := strings.TrimSpace(cfg.Model)
	smallRequested := strings.TrimSpace(cfg.SmallModel)

	switch {
	case mainRequested != "" && smallRequested != "":
		model, err := findEligibleModel(cfg.Catalog, eligible, mainRequested, "model")
		if err != nil {
			return copilot.Model{}, copilot.Model{}, err
		}
		smallModel, err := findEligibleModel(cfg.Catalog, eligible, smallRequested, "small model")
		if err != nil {
			return copilot.Model{}, copilot.Model{}, err
		}
		return model, smallModel, nil
	case mainRequested != "" || smallRequested != "":
		return copilot.Model{}, copilot.Model{}, errors.New(
			"--model and --small-model must be provided together, or neither for interactive selection")
	default:
		model, err := promptSelect(stdin, cfg.Out, "main", eligible)
		if err != nil {
			return copilot.Model{}, copilot.Model{}, err
		}
		smallModel, err := promptSelect(stdin, cfg.Out, "small/fast", eligible)
		if err != nil {
			return copilot.Model{}, copilot.Model{}, err
		}
		return model, smallModel, nil
	}
}

// findEligibleModel resolves a requested name against the catalog and checks
// it is usable through the Anthropic Messages API.
func findEligibleModel(
	catalog *copilot.Catalog,
	eligible []copilot.Model,
	requested, label string,
) (copilot.Model, error) {
	resolution := catalog.Resolve(requested)
	if resolution.Known && resolution.Model.SupportsAnthropicMessages() {
		return resolution.Model, nil
	}
	ids := make([]string, 0, len(eligible))
	for _, model := range eligible {
		ids = append(ids, model.ID)
	}
	return copilot.Model{}, fmt.Errorf("invalid %s %q; available: %s",
		label, requested, strings.Join(ids, ", "))
}

func promptSelect(
	reader *bufio.Reader,
	out io.Writer,
	label string,
	models []copilot.Model,
) (copilot.Model, error) {
	fmt.Fprintf(out, "\nSelect the %s model:\n", label)
	for index, model := range models {
		fmt.Fprintf(out, "  %2d) %s\n", index+1, model.ID)
	}
	for {
		fmt.Fprintf(out, "Enter a number (1-%d): ", len(models))
		line, err := reader.ReadString('\n')
		if err != nil {
			return copilot.Model{}, fmt.Errorf("read selection: %w", err)
		}
		choice, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil || choice < 1 || choice > len(models) {
			fmt.Fprintln(out, "Invalid selection, try again.")
			continue
		}
		return models[choice-1], nil
	}
}

func promptConfirm(reader *bufio.Reader, out io.Writer, question string) (bool, error) {
	fmt.Fprintf(out, "%s [y/N]: ", question)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false, fmt.Errorf("read confirmation: %w", err)
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}
