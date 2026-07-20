package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// jsonFlag is the persistent flag name that switches supported
// commands (status, day, export, validate, version, upgrade --check)
// into machine-readable output. Human-first prose is the default so
// automation never scrapes formatted text (ADR-0007).
const jsonFlag = "json"

// writeJSON marshals v as indented JSON to w with a trailing newline.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("lucid: encode json: %w", err)
	}
	return nil
}

// containsFold reports whether substr is contained in s,
// case-insensitively.
func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// emit renders a read-model command result: when --json is set it writes the
// machine payload verbatim, otherwise it prints each human-first line to
// stdout. It is the shared tail of the read commands (status, day, stats,
// metrics) so the --json branch and the line loop live in one place, keeping
// prose the default and JSON strictly opt-in (ADR-0007).
func emit(cmd *cobra.Command, jsonPayload any, lines []string) error {
	if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
		return writeJSON(cmd.OutOrStdout(), jsonPayload)
	}
	for _, line := range lines {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), line)
	}
	return nil
}
