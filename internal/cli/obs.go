package cli

import (
	"fmt"
	"strings"
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
//	lucid obs ate eggs, toast --day @yesterday
//	lucid obs where Lisbon
//	lucid obs symptom headache 4
//
// The named shorthands (`/pain`, `/ate`, `/drank`, `/bm`, `/mood`, `/slept`)
// are aliases into this one intent; on the chat surface they are separate
// slashes, here they are the first token. Capture never blocks: an unparseable
// head is kept verbatim on the partial path, and the ack is inventory only.
//
// A date reaches this verb two ways, and they are two different tiers. The
// in-text @-token is permissive — it is parsed out of a sentence, so an
// unrecognized one stays in the note and the capture still lands. `--day` is
// the strict tier: a value the grammar cannot read, or a day that has not
// happened yet, is a clean error and nothing is written. When both are given
// the flag wins.
//
// `obs void <obs-id>` is the one subcommand: it retracts a mistaken measurement
// reading, and every other first token still routes to capture.
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
			day, _ := cmd.Flags().GetString(flagDay)

			// --body-file supplies the whole observation expression (kind plus
			// value) off the command line. It is split on whitespace into the
			// same token list the shell would have produced, so it flows through
			// the identical kind/value parser — metacharacters in the value stay
			// data because no shell ever sees them.
			tokens := args
			if cmd.Flags().Changed("body-file") {
				if len(args) > 0 {
					return emitErr(cmd, fmt.Errorf(
						"lucid obs: give the observation via --body-file or as positional words, not both",
					))
				}
				path, _ := cmd.Flags().GetString("body-file")
				body, rerr := readBodyFile("body-file", path, cmd.InOrStdin())
				if rerr != nil {
					return emitErr(cmd, fmt.Errorf("lucid obs: %w", rerr))
				}
				tokens = strings.Fields(body)
			}

			res, err := r.Capture(router.CaptureRequest{
				Tokens:  tokens,
				Now:     time.Now(),
				DayArg:  day,
				Harness: obsHarness(cmd),
				Agent:   flagOrEnv(cmd, flagAgent, envAgent, ""),
				Model:   flagOrEnv(cmd, flagModel, envModel, ""),
				Channel: flagOrEnv(cmd, flagChannel, envChannel, ""),
			})
			if err != nil {
				return emitRefusedDay(cmd, err)
			}
			return emitReceipt(cmd, res.EventID, res.LogicalDate, res.Ack)
		},
	}
	registerProvenanceFlags(cmd)
	registerDayFlag(cmd)
	registerBodyFileFlag(cmd, "observation expression (kind and value)")
	// void is a subcommand of obs (the memory amend/show parent pattern): a bare
	// `obs <kind> <value>` still captures, while `obs void <obs-id>` retracts a
	// reading. No kind or alias is named void, so the subcommand shadows nothing.
	cmd.AddCommand(newObsVoidCmd())
	return cmd
}

// newObsVoidCmd wires `lucid obs void <obs-id>` (usage/commands.md §"obs
// void"): an append-only retraction of one measurement reading. It takes no
// --day (a void files under the current logical day) and no provenance flags;
// the parent's flags are local, so none is inherited. A refusal prints a
// `lucid obs void:` reason on stderr and writes nothing; a disabled measurement
// kind prints the enable hint and exits 0.
func newObsVoidCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "void <obs-id>",
		Short: "Retract a mistaken measurement reading by its receipt id",
		Long: `void retracts one measurement reading without rewriting anything: it appends a
void naming the reading, and both records are kept. The voided reading drops out
of 'lucid export measurements' and the clinician packet's "Measurements in range"
section; 'lucid day' shows both.

A void is only a retraction, never an overlay. To fix a wrong value, unit, metric
or day, void the reading, then re-log the right value with
'lucid obs measurement <metric> <value> <unit> --day @YYYY-MM-DD'.

The void files under the current logical day and takes no reason text. Only
measurement readings are voidable: workout and memory observations are corrected
with 'lucid workout amend' and 'lucid memory amend', and other kinds aren't
voidable yet. A void can't itself be voided — to restore a reading, log it again.`,
		Example: "  lucid obs void obs_2026_03_04_002",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.VoidObservation(router.ObsVoidRequest{ID: args[0], Now: clockNow()})
			if err != nil {
				return emitErr(cmd, fmt.Errorf("lucid obs void: %w", err))
			}
			if res.Rejected {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
				return nil
			}
			return emitReceipt(cmd, res.EventID, res.LogicalDate, res.Ack)
		},
	}
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
