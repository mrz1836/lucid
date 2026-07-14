package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
)

// newStatsCmd wires `lucid stats` (docs/usage/commands.md §stats): the
// read-only Ledger-volume rollup over a window of logical days — raw-entry
// count, observation count, observation counts by kind, total events, and a
// per-logical-day breakdown. Human-first prose by default; the assembled view
// as JSON under --json for a harness (ADR-0007). It is a pure projection and
// writes nothing beyond the idempotent observation- and engine-tree scaffolds
// the read verbs already perform, and never exposes journal content.
//
// It is the volume sibling of `lucid metrics` (practice quality): the two share
// the same rollover / logical-day basis but have zero overlapping output fields.
func newStatsCmd() *cobra.Command {
	var (
		last int
		from string
		to   string
	)
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show Ledger volume over a window of logical days",
		Long: `stats reports how much has been recorded: raw-entry count, observation
count, observation counts by kind, total events, and a per-logical-day
breakdown. Bare stats reports today's logical day; --last N reports the N days
ending at and including today; --from/--to reports an explicit inclusive range
(--last is mutually exclusive with --from/--to). Every number is a count read
from the Ledger — deterministic, read-only, and agent-free; no journal content
is ever exposed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			opts := router.StatsOptions{
				LastSet: cmd.Flags().Changed("last"),
				Last:    last,
				From:    from,
				To:      to,
			}
			res, err := r.Stats(opts, clockNow())
			if err != nil {
				return err
			}
			if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
				return writeJSON(cmd.OutOrStdout(), res.View)
			}
			for _, line := range res.Lines {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), line)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&last, "last", 0, "Report the last N logical days, ending at and including today")
	cmd.Flags().StringVar(&from, "from", "", "Start of an explicit inclusive range (YYYY-MM-DD)")
	cmd.Flags().StringVar(&to, "to", "", "End of an explicit inclusive range (YYYY-MM-DD; defaults to today)")
	return cmd
}
