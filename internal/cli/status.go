package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newStatusCmd wires `lucid status` (engine-module.md §Commands): the
// read-only L0 ambient surface. Human-first prose by default; the derived
// status.json projection verbatim under --json for scripts (ADR-0007). It
// writes nothing except the silent rebuild recovery when status.json is
// corrupt or missing.
func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the derived Engine status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.Status(clockNow())
			if err != nil {
				return err
			}
			if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
				return writeJSON(cmd.OutOrStdout(), res.Status)
			}
			for _, line := range res.Lines {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), line)
			}
			return nil
		},
	}
}
