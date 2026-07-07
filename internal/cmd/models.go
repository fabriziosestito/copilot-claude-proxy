package cmd

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/urfave/cli/v3"
)

const (
	tabwriterMinWidth = 2
	tabwriterPadding  = 2
)

func newModelsCommand() *cli.Command {
	return &cli.Command{
		Name:  "models",
		Usage: "List the models available on this Copilot account",
		Flags: []cli.Flag{
			accountTypeFlag(),
			githubTokenFlag(),
			verboseFlag(),
		},
		Action: runModels,
	}
}

func runModels(ctx context.Context, cmd *cli.Command) error {
	application, err := bootstrap(ctx, cmd)
	if err != nil {
		return err
	}
	if refreshErr := application.catalog.Refresh(ctx); refreshErr != nil {
		return refreshErr
	}

	writer := tabwriter.NewWriter(os.Stdout, tabwriterMinWidth, 0, tabwriterPadding, ' ', 0)
	fmt.Fprintln(writer, "ID\tVENDOR\tCONTEXT\tMAX OUTPUT\tVISION\tCLAUDE CODE")
	for _, model := range application.catalog.Models() {
		fmt.Fprintf(writer, "%s\t%s\t%d\t%d\t%s\t%s\n",
			model.ID,
			model.Vendor,
			model.Capabilities.Limits.MaxContextWindowTokens,
			model.Capabilities.Limits.MaxOutputTokens,
			boolCell(model.Capabilities.Supports.Vision),
			boolCell(model.SupportsAnthropicMessages()))
	}
	if flushErr := writer.Flush(); flushErr != nil {
		return fmt.Errorf("write model table: %w", flushErr)
	}
	return nil
}

// boolCell renders a boolean as a compact table cell.
func boolCell(value bool) string {
	if value {
		return "yes"
	}
	return "-"
}
