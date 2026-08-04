package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/router"
)

// newModeCmd wires `lucid mode <green|yellow|red> [--day <date>]`
// (engine-module.md §Commands): declare today's Engine mode, fixed at the bell.
// It is human-first prose only — mode is not a script-facing surface, so it
// does not honor --json. A rejected (post-bell) or invalid declaration prints
// the fixed copy and exits non-zero so a caller never mistakes it for a
// success.
//
// `--day` is gap-fill only: it declares the mode for a past logical day whose
// record carries none, never overwriting one that was declared, and reaching
// back at most backfill_window_days. A rejected gap-fill — out of window, or a
// day whose mode already stands — takes the same non-zero exit.
func newModeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mode <green|yellow|red>",
		Short: "Declare today's Engine mode (green|yellow|red)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			day, _ := cmd.Flags().GetString(flagDay)
			res, err := r.DeclareMode(router.ModeRequest{
				Mode:   engine.Mode(args[0]),
				DayArg: day,
				Now:    clockNow(),
			})
			if err != nil {
				// A refused `--day` is a deterministic rejection like any other,
				// so it takes the rejection path below rather than becoming a
				// silent non-zero exit — Execute renders no returned error.
				var refused *router.DayRejectedError
				if !errors.As(err, &refused) {
					return err
				}
				res = router.ModeResult{Rejected: true, Ack: refused.Reason}
			}
			if res.Invalid || res.Rejected {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), res.Ack)
				return errModeNotAccepted
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
			return nil
		},
	}
	cmd.Flags().String(
		flagDay, "",
		"Gap-fill a past logical day that carries no mode, e.g. @yesterday or YYYY-MM-DD "+
			"(within backfill_window_days; never overwrites a declared mode)",
	)
	return cmd
}
