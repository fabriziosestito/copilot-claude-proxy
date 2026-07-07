package copilot

import (
	"fmt"
	"io"
	"text/tabwriter"
)

const (
	tabwriterMinWidth = 2
	tabwriterPadding  = 2
)

// WriteModelTable renders the models as an aligned text table.
func WriteModelTable(out io.Writer, models []Model) error {
	writer := tabwriter.NewWriter(out, tabwriterMinWidth, 0, tabwriterPadding, ' ', 0)
	fmt.Fprintln(writer, "ID\tVENDOR\tCONTEXT\tMAX OUTPUT\tVISION\tCLAUDE CODE")
	for _, model := range models {
		fmt.Fprintf(writer, "%s\t%s\t%d\t%d\t%s\t%s\n",
			model.ID,
			model.Vendor,
			model.Capabilities.Limits.MaxContextWindowTokens,
			model.Capabilities.Limits.MaxOutputTokens,
			boolCell(model.Capabilities.Supports.Vision),
			boolCell(model.SupportsAnthropicMessages()))
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("write model table: %w", err)
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
