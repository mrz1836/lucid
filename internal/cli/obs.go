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
	cmd := &cobra.Command{
		Use:   "obs [kind] [value...]",
		Short: "Log a health/context observation (micro-log)",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.Capture(router.CaptureRequest{
				Tokens:  args,
				Now:     time.Now(),
				Harness: obsHarness(cmd),
				Agent:   flagOrEnv(cmd, flagAgent, envAgent, ""),
				Model:   flagOrEnv(cmd, flagModel, envModel, ""),
				Channel: flagOrEnv(cmd, flagChannel, envChannel, ""),
			})
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
			return nil
		},
	}
	registerProvenanceFlags(cmd)
	return cmd
}

// obsHarness resolves the harness provenance token for `lucid obs`. The frozen
// observation envelope has no separate source field (its source stays
// microlog), so provenance.harness is the single relayer slot:
// --harness/LUCID_HARNESS is the exact match, and --source/LUCID_SOURCE is
// accepted as the cross-surface synonym — the same token becomes a raw entry's
// source on `lucid log`, so a relay that sets one env attributes both commands
// rather than silently dropping it. The empty default keeps a bare terminal
// capture free of provenance (byte-stable). --thread has no home in the
// observation envelope and is intentionally not consumed here.
func obsHarness(cmd *cobra.Command) string {
	return flagOrEnv(cmd, flagHarness, envHarness, flagOrEnv(cmd, flagSource, envSource, ""))
}
