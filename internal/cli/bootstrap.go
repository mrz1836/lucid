package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
)

// bootstrapView is the machine-readable projection of a
// [router.BootstrapResult] under --json (harness-integration.md §C). It is
// built CLI-side with a stable snake_case name so a shelling harness reads the
// resulting mode directly rather than parsing the prose ack; the router package
// stays untouched.
type bootstrapView struct {
	BootstrapMode bool `json:"bootstrap_mode"`
}

// newBootstrapCmd wires `lucid bootstrap` / `lucid bootstrap done` (scope.md §4;
// error-states.md §E-6): toggle historical-entry mode. The bare form turns it on
// (captures stamp bootstrap:true and pattern proposals pause); `done` turns it
// off. It is a thin dispatch over Router.Bootstrap, which persists lucid.json and
// updates the router's effective config; no model is involved. Human-first prose
// by default; the bootstrapView shape under --json.
//
// `done` is the only valid positional arg — OnlyValidArgs turns any other token
// (`lucid bootstrap foo`) into a usage error (exit 2), while both accepted forms
// exit 0.
func newBootstrapCmd() *cobra.Command {
	return &cobra.Command{
		Use:       "bootstrap [done]",
		Short:     "Toggle historical-entry (bootstrap) mode",
		ValidArgs: []string{"done"},
		Args:      cobra.MatchAll(cobra.MaximumNArgs(1), cobra.OnlyValidArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			done := len(args) == 1 && args[0] == "done"
			res, err := r.Bootstrap(router.BootstrapRequest{Done: done})
			if err != nil {
				return err
			}
			if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
				return writeJSON(cmd.OutOrStdout(), bootstrapView{BootstrapMode: res.BootstrapMode})
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
			return nil
		},
	}
}
