package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newModeCmd wires `lucid mode <green|yellow|red>` (engine-module.md
// §Commands): declare today's Engine mode, fixed at the bell. It is
// human-first prose only — mode is not a script-facing surface, so it does
// not honor --json. A rejected (post-bell) or invalid declaration prints the
// fixed copy and exits non-zero so a caller never mistakes it for a success.
func newModeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mode <green|yellow|red>",
		Short: "Declare today's Engine mode (green|yellow|red)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.Mode(args[0], clockNow())
			if err != nil {
				return err
			}
			if res.Invalid || res.Rejected {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), res.Ack)
				return errModeNotAccepted
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
			return nil
		},
	}
}
