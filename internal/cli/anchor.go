package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/router"
)

// errAnchorNotRecorded maps a deterministically rejected anchor verb to a
// non-zero exit. The fixed reason is already printed to stderr; this sentinel
// just keeps a script from reading a rejected write as success (mirrors
// errModeNotAccepted). All three verbs share it: whether the rejection was
// about the input (`add`'s label or date) or about the state of the store
// (which anchor a bare label means), the caller's contract is the same —
// nothing was appended, and the exit says so.
var errAnchorNotRecorded = errors.New("lucid: anchor not recorded")

// anchorRejection maps the router's two deterministic rejection types to the
// shared exit shape: the fixed reason on stderr, a non-zero exit, and nothing
// written. [router.ErrAnchorRejected] gates a verb's input;
// [router.AnchorRejectedError] gates the state of the store. Anything else is a
// real failure and travels on unchanged.
func anchorRejection(cmd *cobra.Command, err error) error {
	var rejected *router.AnchorRejectedError
	if errors.Is(err, router.ErrAnchorRejected) || errors.As(err, &rejected) {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err.Error())
		return errAnchorNotRecorded
	}
	return err
}

// newAnchorCmd wires `lucid anchor` (engine-module.md §Commands): the parent
// group for the days-since milestone verbs — add, sunset, and rename.
func newAnchorCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "anchor",
		Short: "Record days-since milestones (anchors)",
		Long: `anchor groups the days-since milestone verbs. A milestone is a labeled
civil date the metrics surface counts elapsed days from — a cessation or a
gate the practice measures against. Each anchor carries a stable id and the
label is a display name, so a name can be corrected or renamed without forking
the milestone. Anchors are append-only: a correction, a reset, a retirement,
and a rename are all new dated records, and the latest record per identity
wins.`,
		Args: cobra.NoArgs,
	}
	parent.AddCommand(newAnchorAddCmd())
	parent.AddCommand(newAnchorSunsetCmd())
	parent.AddCommand(newAnchorRenameCmd())
	parent.AddCommand(newAnchorBackfillIDsCmd())
	return parent
}

// anchorBackfillFlagDryRun prints exactly what the backfill would append without
// writing anything, so an operator can preview the one-time write first.
const anchorBackfillFlagDryRun = "dry-run"

// newAnchorBackfillIDsCmd builds the `backfill-ids` child: give every anchor
// recorded before ids existed a written, durable id by appending an adoption
// record for each. It is append-only — no stored record is rewritten — and
// explicit, never automatic. Human-first prose ack by default; the planned
// records plus whether they were written as JSON under --json for scripts
// (ADR-0007). --dry-run prints exactly what would be appended and writes
// nothing.
func newAnchorBackfillIDsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backfill-ids",
		Short: "Give every pre-id anchor a written id by appending adoption records",
		Long: `backfill-ids gives every anchor recorded before ids existed a written,
durable id. It is append-only: no stored record is ever rewritten. For each
pre-id anchor it appends one adoption record carrying a freshly minted id and
the legacy:<label> identity it takes over; the fold then reads the pre-id
record and its adoption as one anchor, so labels, dates, notes, states, and the
retirement date all stay exactly as they were — only the published id changes,
from legacy:<label> to the minted id.

This command is explicit and never automatic: a silent write to primary
testimony on upgrade is exactly what lucid does not do. It is safe to re-run —
a log whose anchors all carry ids appends nothing. Add --dry-run to print what
would be appended without writing anything.

After a real run, ` + "`lucid metrics --json`" + ` publishes the minted id in place of
legacy:<label> for the adopted anchors. That synthetic address was read-time
only and never durable, so the change is the intended outcome, not a regression.`,
		Args: cobra.NoArgs,
		Example: `  # Preview the one-time backfill without writing.
  lucid anchor backfill-ids --dry-run

  # Append the adoption records.
  lucid anchor backfill-ids

  # Machine-readable output for a harness.
  lucid anchor backfill-ids --json`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			dryRun, _ := cmd.Flags().GetBool(anchorBackfillFlagDryRun)
			res, err := r.BackfillAnchorIDs(router.BackfillAnchorIDsRequest{
				Now:    clockNow(),
				DryRun: dryRun,
			})
			if err != nil {
				return err
			}
			if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
				return writeJSON(cmd.OutOrStdout(), anchorBackfillJSON(res))
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
			return nil
		},
	}
	cmd.Flags().Bool(anchorBackfillFlagDryRun, false, "Print exactly what would be appended without writing anything")
	return cmd
}

// anchorBackfillView is the `anchor backfill-ids --json` surface: the adoption
// records that would be (or were) appended, each projected through the same
// [engine.AnchorRecord] every anchor surface uses so the minted id is published
// rather than an empty one, plus whether the write happened. Planned is a
// non-nil empty slice on a no-op, so the shape is stable ({"planned":[],...}).
type anchorBackfillView struct {
	Planned []engine.AnchorRecord `json:"planned"`
	Written bool                  `json:"written"`
}

// anchorBackfillJSON projects a backfill result into its machine surface.
func anchorBackfillJSON(res router.BackfillAnchorIDsResult) anchorBackfillView {
	planned := make([]engine.AnchorRecord, len(res.Planned))
	for i, a := range res.Planned {
		planned[i] = engine.ProjectAnchor(a)
	}
	return anchorBackfillView{Planned: planned, Written: res.Written}
}

// newAnchorAddCmd builds the `add` child: record one labeled milestone with an
// optional trailing note. The date reads the shared grammar
// ([resolveAnchorDate]), so `@yesterday` works alongside a civil YYYY-MM-DD.
// Human-first prose ack by default; the recorded anchor as JSON under --json
// for scripts (ADR-0007). A rejected input (empty label, unreadable date, or a
// partial date) prints the fixed reason on stderr and exits non-zero without
// writing; the record path is model-free.
//
// Which milestone the record lands on follows the identity model: a label an
// active anchor already holds corrects *that* anchor, and a label whose only
// holder was retired starts a new one, with an ack that names the earlier
// retirement.
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
January 1st would report a confident wrong number.

Recording a label an active anchor already holds appends under that same
anchor and the latest record wins, so a mistyped date and a genuine reset are
the same operation. Recording a label whose only holder was sunset starts a new
milestone with its own id — a freed name, not a resumed count — and the ack
names the earlier retirement.`,
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
				return anchorRejection(cmd, err)
			}
			if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
				// Projected, never marshaled raw. An add that corrects an
				// anchor recorded before ids existed carries no stored id, so
				// marshaling the record would publish an empty one; the
				// projection synthesizes the legacy: address instead.
				return writeJSON(cmd.OutOrStdout(), engine.ProjectAnchor(res.Anchor))
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
			return nil
		},
	}
}

// newAnchorSunsetCmd builds the `sunset` child: retire the milestone a label
// names. The retirement is an append like every other anchor write, so nothing
// is deleted and the record stays auditable. Human-first prose ack by default;
// the appended record as JSON under --json for scripts (ADR-0007). A label no
// active anchor holds is rejected with the fixed reason on stderr and a
// non-zero exit, and nothing is appended.
func newAnchorSunsetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sunset <label> [reason...]",
		Short: "Retire an anchor from the counting surfaces (the record is kept)",
		Long: `sunset retires a days-since milestone: it stops counting, and nothing is
deleted. The store is append-only, so a retirement is a new record mirroring
the anchor plus the optional trailing reason. The milestone stays in the
history and appears in ` + "`metrics --json`" + ` under anchors_sunset[]; it drops out
of the metrics prose, out of anchors[], and out of the witness aging-anchor
check.

A bare label is enough, because at most one active anchor holds one. Recording
the label again later is supported and starts a new milestone under that freed
name, with its own id, so the two runs stay distinct in the ledger. A label no
active anchor holds — never recorded, or already retired — is rejected with the
recorded labels named, and nothing is appended.`,
		Args: cobra.MinimumNArgs(1),
		Example: `  # Retire a milestone that has stopped being useful.
  lucid anchor sunset gate-30

  # Say why (trailing words are joined into the record's note).
  lucid anchor sunset gate-30 mistyped label

  # Machine-readable output for a harness.
  lucid anchor sunset gate-30 --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.AnchorSunset(router.AnchorSunsetRequest{
				Label:  args[0],
				Reason: strings.Join(args[1:], " "),
				Now:    clockNow(),
			})
			if err != nil {
				return anchorRejection(cmd, err)
			}
			if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
				return writeJSON(cmd.OutOrStdout(), engine.ProjectAnchor(res.Anchor))
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
			return nil
		},
	}
}

// newAnchorRenameCmd builds the `rename` child: give an active milestone a
// different display name. It takes exactly two arguments and no trailing
// reason — nothing is being retired from the user's point of view, so there is
// nothing to explain. Human-first prose ack by default; the surviving renamed
// record as JSON under --json for scripts (ADR-0007). Every rejection prints
// the fixed reason on stderr, exits non-zero, and appends nothing.
func newAnchorRenameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename <label> <new-label>",
		Short: "Rename an anchor's display label (the day count carries forward)",
		Long: `rename gives an active milestone a different display name. The label is a
display name rather than the identity, so the anchor keeps its id, its date,
its note, and therefore its running day count — a rename costs the count
nothing.

Renaming an anchor recorded before anchors carried ids adopts it into the
identity model: the old name is retired as a stub in anchors_sunset[] under the
id legacy:<old-label>, and the milestone continues under the new name with a
minted id. That stub is visible on the retired surface by design. Both records
are written together or neither is.

A new name an active anchor already holds is rejected; a name held only by a
retired anchor is free, exactly as it is for ` + "`anchor add`" + `. A label no active
anchor holds — never recorded, or already retired — is rejected with the
recorded labels named.`,
		Args: cobra.ExactArgs(2),
		Example: `  # Correct a mistyped label without forking the milestone.
  lucid anchor rename gate-30 gate-thirty

  # Machine-readable output for a harness.
  lucid anchor rename gate-30 gate-thirty --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.AnchorRename(router.AnchorRenameRequest{
				Label:    args[0],
				NewLabel: args[1],
				Now:      clockNow(),
			})
			if err != nil {
				return anchorRejection(cmd, err)
			}
			if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
				// The surviving record, not the retirement stub: a caller asked
				// what the milestone is now, not what its old name became.
				return writeJSON(cmd.OutOrStdout(), engine.ProjectAnchor(res.Anchor))
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
