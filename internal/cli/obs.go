package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
)

// newObsCmd wires `lucid obs` (observations-module.md §Commands): the
// deterministic micro-log capture entry point. The first argument is the kind
// or a shorthand alias, the rest is the value text:
//
//	lucid obs pain 6 knee aching after the run
//	lucid obs bm 4
//	lucid obs ate eggs, toast, coffee @yesterday 19:30
//	lucid obs where Lisbon
//	lucid obs symptom headache 4
//
// The named shorthands (`/pain`, `/ate`, `/drank`, `/bm`, `/mood`, `/slept`)
// are aliases into this one intent; on the chat surface they are separate
// slashes, here they are the first token. Capture never blocks: an unparseable
// head is kept verbatim on the partial path, and the ack is inventory only.
func newObsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "obs [kind] [value...]",
		Short: "Log a health/context observation (micro-log)",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.Capture(router.CaptureRequest{
				Tokens: args,
				Now:    time.Now(),
				Source: sourceCLI,
			})
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
			return nil
		},
	}
}
