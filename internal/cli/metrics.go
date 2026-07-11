package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newMetricsCmd wires `lucid metrics` (engine-module.md §Commands): the
// read-only practice-quality rollup. Human-first prose by default; the derived
// metrics projection verbatim under --json for scripts (ADR-0007) — current and
// longest streak, adherence over the 30-day window plus the 30/60/90 gate
// rollups, misses in that window, the error budget, and days-since for each
// recorded anchor. It writes nothing beyond the idempotent engine-tree scaffold.
func newMetricsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "metrics",
		Short: "Show the derived practice metrics (streak, adherence, days-since)",
		Long: `metrics reports the committed chain's quality: current and longest
streak, adherence over the trailing 30-day window (with the 30/60/90 gate
rollups under --json), misses in that window, the isolated-miss error budget,
and days-since for each recorded anchor. Every number comes from the engine, so
a harness reads one deterministic surface and never recomputes downstream.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.Metrics(clockNow())
			if err != nil {
				return err
			}
			if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
				return writeJSON(cmd.OutOrStdout(), res.Metrics)
			}
			for _, line := range res.Lines {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), line)
			}
			return nil
		},
	}
}
