package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

// errProfileRejected is returned when `lucid profile` declined the switch —
// an undefined profile name, which has no disk effect. The fixed user copy is
// already emitted (prose to stderr, or the rejected view under --json); this
// sentinel maps the result to a non-zero exit so a caller never reads a rejected
// switch as a success. It mirrors errModeNotAccepted.
var errProfileRejected = errors.New("lucid: profile switch not accepted")

// profileView is the machine-readable projection of a [router.ProfileResult]
// under --json (harness-integration.md §C). It is built CLI-side with stable
// snake_case names so a shelling harness branches on the outcome fields rather
// than parsing the prose ack; the router package stays untouched.
type profileView struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Effective string `json:"effective"`
	Rejected  bool   `json:"rejected"`
}

// newProfileCmd wires `lucid profile <name>` (engine-module.md §Commands):
// switch to a named clock profile defined in chain.json. It is a thin dispatch
// over Router.Profile; the deterministic guarantee (the switch is effective the
// next logical day, never the current one) lives in the router, and no model is
// involved. Human-first prose by default; the profileView shape under --json. An
// undefined name is rejected — the fixed copy to stderr (or the rejected view
// under --json) and a non-zero exit, with no disk effect.
func newProfileCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "profile <name>",
		Short: "Switch to a named clock profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.Profile(args[0], clockNow())
			if err != nil {
				return err
			}
			if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
				view := profileView{From: res.From, To: res.To, Effective: res.Effective, Rejected: res.Rejected}
				if werr := writeJSON(cmd.OutOrStdout(), view); werr != nil {
					return werr
				}
				if res.Rejected {
					return errProfileRejected
				}
				return nil
			}
			if res.Rejected {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), res.Ack)
				return errProfileRejected
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
			return nil
		},
	}
}
