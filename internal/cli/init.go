package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
	"github.com/mrz1836/lucid/internal/storage"
)

// initResult is the machine-readable shape emitted by `lucid init
// --json`. Field names are a stable script contract.
type initResult struct {
	Home        string   `json:"home"`
	CreatedDirs []string `json:"created_dirs"`
	WroteConfig bool     `json:"wrote_config"`
	Warnings    []string `json:"warnings"`
}

// newInitCmd wires `lucid init`: scaffold the ~/.lucid/ Mirror tree
// (idempotently) and boot the router so an out-of-range lucid.json is
// clipped and rewritten. It is safe to run repeatedly; a second run
// makes no changes (acceptance-criteria.md phase 1). The Ledger home is
// the LUCID_HOME override or ~/.lucid/.
func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Scaffold the ~/.lucid/ Ledger tree",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := storage.Open()
			if err != nil {
				return fmt.Errorf("lucid init: resolve home: %w", err)
			}

			res, err := store.Scaffold()
			if err != nil {
				return err
			}

			warnings, err := router.New(store).Boot()
			if err != nil {
				return err
			}

			asJSON, _ := cmd.Flags().GetBool(jsonFlag)
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), initResult{
					Home:        res.Home,
					CreatedDirs: res.CreatedDirs,
					WroteConfig: res.WroteConfig,
					Warnings:    warnings,
				})
			}

			printInitHuman(cmd, res, warnings)
			return nil
		},
	}
}

// printInitHuman renders the prose summary of an init run: what was
// created (or that the Ledger was already present), and any config clip
// warnings, surfaced once.
func printInitHuman(cmd *cobra.Command, res storage.ScaffoldResult, warnings []string) {
	out := cmd.OutOrStdout()
	if len(res.CreatedDirs) == 0 && !res.WroteConfig {
		_, _ = fmt.Fprintf(out, "Ledger already present at %s — nothing to do.\n", res.Home)
	} else {
		_, _ = fmt.Fprintf(out, "Scaffolded Ledger at %s\n", res.Home)
		for _, d := range res.CreatedDirs {
			_, _ = fmt.Fprintf(out, "  created %s/\n", d)
		}
		if res.WroteConfig {
			_, _ = fmt.Fprintf(out, "  wrote   lucid.json\n")
		}
	}
	for _, w := range warnings {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
	}
}
