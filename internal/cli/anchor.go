package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/router"
)

// errAnchorNotRecorded maps a rejected `anchor add` to a non-zero exit. The
// fixed reason is already printed to stderr; this sentinel just keeps a script
// from reading a rejected record as success (mirrors errModeNotAccepted).
var errAnchorNotRecorded = errors.New("lucid: anchor not recorded")

// newAnchorCmd wires `lucid anchor` (engine-module.md §Commands): the parent
// group for the days-since milestone verbs. It currently exposes only `add`;
// it exists as a group so later anchor subcommands attach without reshaping the
// tree.
func newAnchorCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "anchor",
		Short: "Record days-since milestones (anchors)",
		Long: `anchor groups the days-since milestone verbs. A milestone is a labeled
civil date the metrics surface counts elapsed days from — a cessation or a
gate the practice measures against. Anchors are append-only: a correction or a
reset is a new dated record, and the latest record per label wins.`,
		Args: cobra.NoArgs,
	}
	parent.AddCommand(newAnchorAddCmd())
	return parent
}

// newAnchorAddCmd builds the `add` child: record one labeled milestone with an
// optional trailing note. The date reads the shared grammar
// ([resolveAnchorDate]), so `@yesterday` works alongside a civil YYYY-MM-DD.
// Human-first prose ack by default; the recorded anchor as JSON under --json
// for scripts (ADR-0007). A rejected input (empty label, unreadable date, or a
// partial date) prints the fixed reason on stderr and exits non-zero without
// writing; the record path is model-free.
func newAnchorAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <label> <date> [note...]",
		Short: "Record a days-since anchor (a labeled civil-date milestone)",
		Long: `add records one days-since milestone: a label, a date, and an optional
trailing note. The date reads the shared date grammar (usage/commands.md
"Backdating with --day"), so ` + "`@yesterday`" + ` works alongside a civil YYYY-MM-DD,
with two deliberate carve-outs. A future date is accepted — an anchor may be a
forward commitment, a date you count toward rather than only since, and this is
the one date surface with no future ceiling. A partial date (2014, 2014-09) is
rejected: the whole output is a precise day count, and counting from a guessed
January 1st would report a confident wrong number. Recording the same label
again appends a new record and the latest one wins, so a mistyped date and a
genuine reset are the same operation.`,
		Args: cobra.MinimumNArgs(2),
		Example: `  # Record a cessation milestone.
  lucid anchor add sobriety 2026-01-01

  # The shared grammar: a relative day works too.
  lucid anchor add streak-restart @yesterday

  # A forward commitment — a date you count toward (no future ceiling here).
  lucid anchor add gate 2027-01-01

  # Add a note (trailing words are joined).
  lucid anchor add gate 2026-03-15 first ninety-day gate

  # Machine-readable output for a harness.
  lucid anchor add sobriety 2026-01-01 --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			now := clockNow()
			date, err := resolveAnchorDate(args[1], now)
			if err != nil {
				// Same shape as the router's deterministic rejection: the fixed
				// reason on stderr, a non-zero exit, and nothing written.
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err.Error())
				return errAnchorNotRecorded
			}
			res, err := r.AnchorAdd(router.AnchorAddRequest{
				Label: args[0],
				Date:  date,
				Note:  strings.Join(args[2:], " "),
				Now:   now,
			})
			if err != nil {
				if errors.Is(err, router.ErrAnchorRejected) {
					// Deterministic rejection: surface the fixed reason and exit
					// non-zero so a caller never mistakes it for a recorded anchor.
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err.Error())
					return errAnchorNotRecorded
				}
				return err
			}
			if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
				return writeJSON(cmd.OutOrStdout(), res.Anchor)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
			return nil
		},
	}
}

// resolveAnchorDate reads `anchor add`'s positional date through the shared
// grammar ([observations.ResolveDay], usage/commands.md §"Backdating with
// --day") and returns the civil YYYY-MM-DD the anchors store records. It is the
// CLI's two documented exceptions to the strict tier, and both live here rather
// than in the engine — [engine.ValidateAnchor] is a pure shape check that
// accepts any civil date, so neither carve-out can be enforced downstream:
//
//   - AllowFuture keeps a forward-dated anchor. An anchor may be a date you are
//     counting toward, not only since, so this is the one date surface in the
//     CLI with no future ceiling.
//   - AllowPartial is off, and the rejection is explicit rather than a
//     fall-through. A "days since" count is a precise number; deriving it from
//     the January 1st a partial date snaps to would report a confident wrong
//     one. The explicitness matters twice over: ResolveDay always reads a bare
//     four-digit token as a year, so letting `2014` fall through the grammar
//     instead would resolve it to the clock time 20:14 and silently record an
//     anchor dated *today*.
//
// Every other failure is one message naming the value and the day forms this
// command takes — nothing is written either way (error-states.md §St-1).
func resolveAnchorDate(arg string, now time.Time) (string, error) {
	res, err := observations.ResolveDay(arg, now, observations.DayOptions{
		AllowFuture:  true,
		AllowPartial: false,
	})
	switch {
	case errors.Is(err, observations.ErrDayPartial):
		return "", fmt.Errorf(
			"cannot anchor to %q — a days-since count needs a real day, not a whole year or month; "+
				"give a YYYY-MM-DD date (or @yesterday); nothing was saved", arg,
		)
	case err != nil:
		// Deliberately not [observations.AcceptedDayForms]: the shared list
		// names the partial forms, and offering a caller a form this command
		// rejects two lines above would be the same broken promise the strict
		// tier exists to remove.
		return "", fmt.Errorf(
			"could not read the anchor date %q (want @yesterday or YYYY-MM-DD; a whole year or month is not a day count); "+
				"nothing was saved", arg,
		)
	}
	return observations.DateString(res.OccurredAt), nil
}
