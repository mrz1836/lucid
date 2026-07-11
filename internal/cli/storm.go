package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// errStormRejected is returned when `lucid storm` declined the command — an
// unknown clause label, a second renewal (a season, not a storm), or `end` with
// nothing standing. The fixed user copy is already emitted (prose to stderr, or
// the rejected view under --json); this sentinel maps the result to a non-zero
// exit so a caller never reads a rejected storm as a success. It mirrors
// errModeNotAccepted.
var errStormRejected = errors.New("lucid: storm command not accepted")

// stormView is the machine-readable projection of a [router.StormResult] under
// --json (harness-integration.md §C). It is built CLI-side with stable
// snake_case names so a shelling harness branches on the outcome fields rather
// than parsing the prose ack; the router package stays untouched.
type stormView struct {
	Event    string `json:"event"`
	Label    string `json:"label"`
	Through  string `json:"through"`
	Rejected bool   `json:"rejected"`
}

// newStormCmd wires `lucid storm <clause-label|unwritten|end>` (engine-module.md
// §Commands): declare, renew, or end a storm — the stake that stays during an
// incapacity. It is a thin dispatch over Router.Storm; the deterministic
// guarantees (a storm stays the stake, renews once, ends on witness math) live
// in the router, and no model is involved. Clause labels are opaque tokens that
// may contain spaces, so trailing args are joined (the obs/closeout precedent).
// Human-first prose by default; the stormView shape under --json. A rejection
// prints the fixed copy to stderr (or the rejected view under --json) and exits
// non-zero.
func newStormCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "storm <clause-label|unwritten|end>",
		Short: "Declare, renew, or end a storm",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.Storm(strings.Join(args, " "), clockNow())
			if err != nil {
				return err
			}
			if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
				view := stormView{Event: res.Event, Label: res.Label, Through: res.Through, Rejected: res.Rejected}
				if werr := writeJSON(cmd.OutOrStdout(), view); werr != nil {
					return werr
				}
				if res.Rejected {
					return errStormRejected
				}
				return nil
			}
			if res.Rejected {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), res.Ack)
				return errStormRejected
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
			return nil
		},
	}
}
