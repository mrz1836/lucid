package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/router"
)

// stormBackfillFlagDryRun prints exactly what the backfill would append without
// writing anything, so an operator can preview the one-time write first.
const stormBackfillFlagDryRun = "dry-run"

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
//
// `--day` dates the storm event itself, on declare, renew, and end — the days a
// storm is worst are the days declaring it at the time was not possible. An
// event dated before the last recorded one is rejected: history folds in order.
func newStormCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "storm <clause-label|unwritten|end>",
		Short: "Declare, renew, or end a storm",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			day, _ := cmd.Flags().GetString(flagDay)
			res, err := r.StormCommand(router.StormRequest{
				Arg:    strings.Join(args, " "),
				DayArg: day,
				Now:    clockNow(),
			})
			if err != nil {
				// A refused `--day` is a deterministic rejection like an unknown
				// clause label, so it takes the rejection path below — prose on
				// stderr, or the rejected view under --json — rather than
				// becoming a silent non-zero exit.
				var refused *router.DayRejectedError
				if !errors.As(err, &refused) {
					return err
				}
				res = router.StormResult{Rejected: true, Ack: refused.Reason}
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
	cmd.Flags().String(
		flagDay, "",
		"Date the storm event itself, e.g. @yesterday or YYYY-MM-DD "+
			"(an event before the last recorded one is rejected)",
	)
	cmd.AddCommand(newStormBackfillEpisodesCmd())
	return cmd
}

// stormBackfillView is the `storm backfill-episodes --json` surface: the episode
// records that would be (or were) appended, plus whether the write happened.
// Planned is a non-nil empty slice on a no-op, so the shape is stable
// ({"planned":[],...}).
type stormBackfillView struct {
	Planned []engine.StormEpisode `json:"planned"`
	Written bool                  `json:"written"`
}

// newStormBackfillEpisodesCmd builds the `backfill-episodes` child: give every
// storm run recorded before the episodes index existed a written id by appending
// to the sibling episodes[] index. It is append-only — no history event is
// touched — and explicit, never automatic. Human-first prose ack by default; the
// planned records plus whether they were written as JSON under --json for
// scripts (ADR-0007). --dry-run prints exactly what would be appended and writes
// nothing.
//
// Registering this subcommand does not change how the positional `lucid storm
// <clause-label|unwritten|end>` forms dispatch: the parent's explicit
// MinimumNArgs(1) keeps cobra from reading a clause label as an unknown
// subcommand. A clause literally named "backfill-episodes" is the one label this
// child shadows.
func newStormBackfillEpisodesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backfill-episodes",
		Short: "Give every existing storm run a written episode id",
		Long: `backfill-episodes gives every storm run recorded before the episodes index
existed a written id. It is append-only: identity is added to a sibling
episodes[] index and no history event is ever edited, reordered, or removed, so
the derived status.json is unchanged — a storm's standing is read from history,
not from this index.

This command is explicit and never automatic. A normal ` + "`lucid storm`" + ` command
already mints an episode id when a run opens, so this command's job is only to
complete the index for runs that predate it. It is safe to re-run — a history
whose runs are all already indexed appends nothing. Add --dry-run to print what
would be appended without writing anything.`,
		Args: cobra.NoArgs,
		Example: `  # Preview the one-time backfill without writing.
  lucid storm backfill-episodes --dry-run

  # Append the episode records.
  lucid storm backfill-episodes

  # Machine-readable output for a harness.
  lucid storm backfill-episodes --json`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			dryRun, _ := cmd.Flags().GetBool(stormBackfillFlagDryRun)
			res, err := r.BackfillStormEpisodes(router.BackfillStormEpisodesRequest{
				Now:    clockNow(),
				DryRun: dryRun,
			})
			if err != nil {
				return err
			}
			if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
				return writeJSON(cmd.OutOrStdout(), stormBackfillJSON(res))
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
			return nil
		},
	}
	cmd.Flags().Bool(stormBackfillFlagDryRun, false, "Print exactly what would be appended without writing anything")
	return cmd
}

// stormBackfillJSON projects a backfill result into its machine surface, with a
// non-nil Planned so the JSON shape is stable on a no-op.
func stormBackfillJSON(res router.BackfillStormEpisodesResult) stormBackfillView {
	planned := res.Planned
	if planned == nil {
		planned = []engine.StormEpisode{}
	}
	return stormBackfillView{Planned: planned, Written: res.Written}
}
