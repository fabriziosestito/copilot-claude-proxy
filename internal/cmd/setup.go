package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/fabrizio/copilot-claude-proxy/internal/claudecode"
	"github.com/fabrizio/copilot-claude-proxy/internal/copilot"
)

var errNoAnthropicModels = errors.New(
	"no models supporting the Anthropic Messages API are available on this Copilot account")

// flagYes names the confirmation-skipping flag.
const flagYes = "yes"

func newSetupCommand() *cli.Command {
	return &cli.Command{
		Name:  "setup",
		Usage: "Write Claude Code configuration pointing it at this proxy",
		Flags: []cli.Flag{
			portFlag(),
			hostFlag(),
			&cli.StringFlag{
				Name:    "model",
				Aliases: []string{"m"},
				Usage:   "Main model (requires --small-model; omit both for interactive selection)",
			},
			&cli.StringFlag{
				Name:    "small-model",
				Aliases: []string{"s"},
				Usage:   "Small/fast model for background tasks (requires --model)",
			},
			&cli.BoolFlag{
				Name:    "with-extras",
				Aliases: []string{"e"},
				Usage:   "Also write opinionated tuning vars (telemetry off, auto-compact, caching fix)",
			},
			&cli.BoolFlag{
				Name:    flagYes,
				Aliases: []string{"y"},
				Usage:   "Apply changes without asking for confirmation",
			},
			accountTypeFlag(),
			githubTokenFlag(),
			verboseFlag(),
		},
		Action: runSetup,
	}
}

func runSetup(ctx context.Context, cmd *cli.Command) error {
	application, err := bootstrap(ctx, cmd)
	if err != nil {
		return err
	}
	if refreshErr := application.catalog.Refresh(ctx); refreshErr != nil {
		return refreshErr
	}

	eligible := eligibleModels(application.catalog)
	if len(eligible) == 0 {
		return errNoAnthropicModels
	}

	// A single stdin reader is shared by every prompt: a second bufio.Reader
	// would lose whatever the first one already buffered (e.g. the trailing
	// confirmation line of piped input).
	stdin := bufio.NewReader(os.Stdin)

	model, smallModel, err := chooseModels(cmd, application.catalog, eligible, stdin)
	if err != nil {
		return err
	}
	warnFableChineseBug(os.Stdout, model, smallModel)

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	serverURL := "http://" + net.JoinHostPort(cmd.String("host"), strconv.Itoa(cmd.Int("port")))

	setup, err := claudecode.PlanSetup(claudecode.SetupConfig{
		Home: home,
		Env: claudecode.EnvConfig{
			ServerURL:  serverURL,
			Model:      model,
			SmallModel: smallModel,
			WithExtras: cmd.Bool("with-extras"),
		},
	})
	if err != nil {
		return err
	}

	return applySetup(setup, cmd.Bool(flagYes), stdin)
}

// applySetup previews the planned changes, asks for confirmation when they
// overwrite existing values, and writes them.
func applySetup(setup *claudecode.Setup, autoApprove bool, stdin *bufio.Reader) error {
	out := os.Stdout
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

// chooseModels returns the main and small models from flags or interactively.
func chooseModels(
	cmd *cli.Command,
	catalog *copilot.Catalog,
	eligible []copilot.Model,
	stdin *bufio.Reader,
) (copilot.Model, copilot.Model, error) {
	mainFlag := strings.TrimSpace(cmd.String("model"))
	smallFlag := strings.TrimSpace(cmd.String("small-model"))

	switch {
	case mainFlag != "" && smallFlag != "":
		model, err := findEligibleModel(catalog, eligible, mainFlag, "model")
		if err != nil {
			return copilot.Model{}, copilot.Model{}, err
		}
		smallModel, err := findEligibleModel(catalog, eligible, smallFlag, "small model")
		if err != nil {
			return copilot.Model{}, copilot.Model{}, err
		}
		return model, smallModel, nil
	case mainFlag != "" || smallFlag != "":
		return copilot.Model{}, copilot.Model{}, errors.New(
			"--model and --small-model must be provided together, or neither for interactive selection")
	default:
		model, err := promptSelect(stdin, os.Stdout, "main", eligible)
		if err != nil {
			return copilot.Model{}, copilot.Model{}, err
		}
		smallModel, err := promptSelect(stdin, os.Stdout, "small/fast", eligible)
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
