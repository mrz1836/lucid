package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
	"github.com/mrz1836/lucid/internal/storage"
)

// sourceCLI identifies the local command-line surface in raw entries and
// session records.
const sourceCLI = "cli"

// newLogCmd wires `lucid log [text]`: capture the text as one immutable
// raw entry under ~/.lucid/raw/ with a sub-second ack. It scaffolds the
// Ledger on first use (idempotently) so capture never blocks on setup
// (product-principles.md P10). Structuring and insight work run in later
// stages; /log only captures — nothing is written under processed/ or
// insights/.
func newLogCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "log [text]",
		Short: "Capture an immutable raw entry",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := storage.Open()
			if err != nil {
				return fmt.Errorf("lucid log: resolve home: %w", err)
			}
			if _, err = store.Scaffold(); err != nil {
				return fmt.Errorf("lucid log: %w", err)
			}
			r := router.New(store)
			warnings, err := r.Boot()
			if err != nil {
				return fmt.Errorf("lucid log: %w", err)
			}
			for _, w := range warnings {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
			}

			res, err := r.Log(router.LogRequest{
				Text:      strings.Join(args, " "),
				Now:       time.Now(),
				Source:    sourceCLI,
				Harness:   sourceCLI,
				ChannelID: sourceCLI,
			})
			if err != nil {
				return err
			}

			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
			return nil
		},
	}
}
