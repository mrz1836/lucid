package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/router"
)

// modeView is mode's minimal --json shape. Mode declares state and mints no
// receipt, so it has no receipt_id / logical_date to report; it emits just the
// accepted mode as a bare snake_case object (ADR-0007), keeping --json uniform
// across the write verbs without inventing a receipt that does not exist.
type modeView struct {
	Mode string `json:"mode"`
}

// newModeCmd wires `lucid mode <green|yellow|red> [--day <date>]`
// (engine-module.md §Commands): declare today's Engine mode, fixed at the bell.
// It is human-first prose by default; under --json it emits the minimal {mode}
// shape (mode mints no receipt, so no receipt_id / logical_date). A rejected
// (post-bell) or invalid declaration prints the fixed copy to stderr and exits
// non-zero so a caller never mistakes it for a success — the --json success
// shape never masks a rejection.
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
			if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
				// The idempotent path may leave res.Mode empty; the validated
				// positional is the accepted mode either way.
				mode := string(res.Mode)
				if mode == "" {
					mode = args[0]
				}
				return writeJSON(cmd.OutOrStdout(), modeView{Mode: mode})
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
