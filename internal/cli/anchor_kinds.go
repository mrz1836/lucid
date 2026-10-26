package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/router"
)

// The anchor kind and event verbs' flag names (usage/commands.md §anchor).
const (
	anchorFlagKind       = "kind"
	anchorFlagAllowance  = "allowance"
	anchorFlagWindow     = "window"
	anchorFlagNoteFile   = "note-file"
	anchorFlagReasonFile = "reason-file"
	anchorFlagPlanned    = "planned"
	anchorFlagSlip       = "slip"
	anchorFlagThrough    = "through"
	anchorFlagFrom       = "from"
	anchorFlagGraduated  = "graduated"
)

// anchorConfirmView is `anchor confirm --json` (usage/commands.md §"anchor
// confirm"): the through-day used, one entry per habit confirmed and one per
// habit whose span was empty. Both arrays are always present, empty when there
// is nothing to list.
type anchorConfirmView struct {
	Through          string                `json:"through"`
	Confirmed        []anchorConfirmedView `json:"confirmed"`
	AlreadyConfirmed []anchorSkippedView   `json:"already_confirmed"`
}

// anchorConfirmedView is one written confirm: the habit, the span it attests,
// and its receipt.
type anchorConfirmedView struct {
	Label     string `json:"label"`
	AnchorID  string `json:"anchor_id"`
	From      string `json:"from"`
	Through   string `json:"through"`
	ReceiptID string `json:"receipt_id"`
}

// anchorSkippedView is one habit whose span was empty; last_confirmed is
// omitted for a habit never confirmed.
type anchorSkippedView struct {
	Label         string `json:"label"`
	AnchorID      string `json:"anchor_id"`
	LastConfirmed string `json:"last_confirmed,omitempty"`
}

// anchorConfirmJSON projects a confirm result into its machine surface.
func anchorConfirmJSON(res router.AnchorConfirmResult) anchorConfirmView {
	view := anchorConfirmView{
		Through:          res.Through,
		Confirmed:        make([]anchorConfirmedView, 0, len(res.Confirmed)),
		AlreadyConfirmed: make([]anchorSkippedView, 0, len(res.AlreadyConfirmed)),
	}
	for _, c := range res.Confirmed {
		view.Confirmed = append(view.Confirmed, anchorConfirmedView{
			Label: c.Label, AnchorID: c.Event.AnchorID, From: c.Event.From, Through: c.Event.Day, ReceiptID: c.Event.ID,
		})
	}
	for _, s := range res.AlreadyConfirmed {
		view.AlreadyConfirmed = append(view.AlreadyConfirmed, anchorSkippedView{
			Label: s.Label, AnchorID: s.AnchorID, LastConfirmed: s.LastConfirmed,
		})
	}
	return view
}

// anchorSunsetGraduatedView is `anchor sunset --graduated --json`: the appended
// sunset record, projected exactly as a plain sunset projects it, plus the
// receipt and the graduate event verbatim.
type anchorSunsetGraduatedView struct {
	engine.AnchorRecord

	ReceiptID string             `json:"receipt_id"`
	Event     engine.AnchorEvent `json:"event"`
}

// anchorAddKindView is `anchor add --kind --json`: the recorded anchor,
// projected exactly as a plain add projects it, plus the kind, the receipt and
// the kind event verbatim.
type anchorAddKindView struct {
	engine.AnchorRecord

	Kind      string             `json:"kind"`
	ReceiptID string             `json:"receipt_id"`
	Event     engine.AnchorEvent `json:"event"`
}

// anchorEventView is the shared receipt the event verbs emit under --json
// (usage/commands.md §anchor "Receipts"): the global {receipt_id, logical_date}
// receipt extended with the anchor's current label, the stored event verbatim,
// and — only when the verb also appended a definition record to anchors.json —
// that record, projected as `anchor add --json` projects one.
type anchorEventView struct {
	ReceiptID   string               `json:"receipt_id"`
	LogicalDate string               `json:"logical_date"`
	Label       string               `json:"label"`
	Event       engine.AnchorEvent   `json:"event"`
	Compat      *engine.AnchorRecord `json:"compat,omitempty"`
}

// emitAnchorEvent renders an event verb's result: the shared receipt under
// --json, else the prose ack.
func emitAnchorEvent(cmd *cobra.Command, res router.AnchorEventResult) error {
	if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
		view := anchorEventView{
			ReceiptID:   res.Event.ID,
			LogicalDate: res.Event.Day,
			Label:       res.Label,
			Event:       res.Event,
		}
		if res.Compat != nil {
			rec := engine.ProjectAnchor(*res.Compat)
			view.Compat = &rec
		}
		return writeJSON(cmd.OutOrStdout(), view)
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
	return nil
}

// addAnchorParamFlags registers a kind's two parameters.
func addAnchorParamFlags(cmd *cobra.Command) {
	cmd.Flags().Int(anchorFlagAllowance, 0, "A limit's allowance: N use-days per window (requires --window)")
	cmd.Flags().Int(anchorFlagWindow, 0, "A limit's or habit's rolling window in days (a habit defaults to 30)")
}

// anchorKindFlags reads and checks a kind and its parameters before anything
// is opened, so a problem visible on the command line alone is a usage error
// (exit 2) that writes nothing (error-states.md An-9). On `add` the kind is the
// --kind flag and may be absent; on `kind` it is the positional argument.
func anchorKindFlags(cmd *cobra.Command, positional string) (kind string, allowance, window int, err error) {
	kind, given := positional, positional != ""
	if !given {
		kind, _ = cmd.Flags().GetString(anchorFlagKind)
		given = cmd.Flags().Changed(anchorFlagKind)
	}
	allowance, _ = cmd.Flags().GetInt(anchorFlagAllowance)
	window, _ = cmd.Flags().GetInt(anchorFlagWindow)
	switch {
	case cmd.Flags().Changed(anchorFlagAllowance) && allowance <= 0:
		return "", 0, 0, anchorUsage(cmd, "--allowance is a positive whole number of use-days")
	case cmd.Flags().Changed(anchorFlagWindow) && window <= 0:
		return "", 0, 0, anchorUsage(cmd, "--window is a positive whole number of days")
	case !given && (allowance != 0 || window != 0):
		return "", 0, 0, anchorUsage(cmd, "--allowance and --window go with --kind, e.g. --kind limit --allowance 4 --window 90")
	case !given:
		return "", 0, 0, nil
	}
	if err := engine.ValidateKindParams(kind, allowance, window); err != nil {
		return "", 0, 0, anchorUsage(cmd, strings.TrimPrefix(err.Error(), "engine: "))
	}
	return kind, allowance, window, nil
}

// anchorUsage types an anchor verb's command-line rejection as a usage error,
// with the nothing-was-saved clause every anchor rejection ends with.
func anchorUsage(cmd *cobra.Command, reason string) error {
	return newUsageError(cmd, errors.New(reason+". Nothing was saved."))
}

// newAnchorKindCmd builds the `kind` child: reclassify an active anchor in
// place. Echo-and-confirm tier.
func newAnchorKindCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "kind <label> <kind>",
		Short: "Reclassify an anchor as a milestone, quit, limit or habit",
		Long: `kind reclassifies an active anchor in place as a milestone, quit, limit or
habit. Its id, label and date are unchanged; one dated kind event is appended,
and anchors.json is untouched — with one exception, below. Changing a limit's
allowance or window is the same verb: the dated kind events are the parameter
history. A limit needs both --allowance N and --window D; a habit may take
--window D (default 30); a milestone or a quit takes neither. --note-file
records why, on the event.

Events are facts that outlive a kind change: quit slips and limit uses are both
use-days, so a limit's uses count as slips under quit and a quit's slips as
slip-intent uses under limit. No conversion makes a quit run longer than the
truth: reclassifying to quit counts from the latest use-day when it is later
than the anchor's date, and then also appends a re-dated definition so an older
lucid reading the same Ledger agrees.

Reclassifying to habit starts its confirmations today, never on the anchor
date. The anchor needs a durable id: one recorded before ids existed needs
` + "`lucid anchor backfill-ids`" + ` first.`,
		Args: cobra.ExactArgs(2),
		Example: `  # Reclassify a milestone as a quit.
  lucid anchor kind sobriety quit

  # A limit of 4 use-days in any 90 days — and later, a new allowance.
  lucid anchor kind late-night-snacks limit --allowance 4 --window 90
  lucid anchor kind late-night-snacks limit --allowance 3 --window 90

  # A habit, with the reason on the event.
  lucid anchor kind morning-walk habit --note-file /tmp/why.txt

  # Machine-readable receipt for a harness.
  lucid anchor kind sobriety quit --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, allowance, window, err := anchorKindFlags(cmd, args[1])
			if err != nil {
				return err
			}
			note, err := resolvePrimaryText(cmd, "anchor kind", anchorFlagNoteFile, "")
			if err != nil {
				return emitErr(cmd, err)
			}
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.AnchorKind(router.AnchorKindRequest{
				Label: args[0], Kind: kind, Allowance: allowance, Window: window, Note: note, Now: clockNow(),
			})
			if err != nil {
				return anchorRejection(cmd, err)
			}
			return emitAnchorEvent(cmd, res)
		},
	}
	addAnchorParamFlags(cmd)
	cmd.Flags().String(anchorFlagNoteFile, "", "Read a note for the kind event from this file (or - for stdin)")
	return cmd
}

// newAnchorSlipCmd builds the `slip` child: record a relapse on a quit.
// Capture tier.
func newAnchorSlipCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "slip <label>",
		Short: "Record a slip on a quit (the count restarts; the longest run is kept)",
		Long: `slip records a relapse on a quit anchor, kept separate from a date
correction: the running count restarts from the slip day, while the longest
clean run and the slip count are kept. --day backdates it (the shared grammar;
default: the current logical day, so a slip logged after midnight and before
the rollover lands on the evening it belongs to); --note-file adds a note.

When the slip moves the quit's effective date, slip also appends a re-dated
definition to anchors.json, written before the event, so neither this binary
nor an older one ever shows a run longer than the truth. Re-dating with
` + "`anchor add`" + ` stays the correction path, for a start date that was simply wrong.
A slip on anything but a quit is rejected with the reclassify command named.`,
		Args: cobra.ExactArgs(1),
		Example: `  # A slip today.
  lucid anchor slip sobriety

  # Last night's slip, logged this morning, with a note.
  lucid anchor slip sobriety --day @yesterday --note-file /tmp/slip-note.txt

  # Machine-readable receipt for a harness.
  lucid anchor slip sobriety --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			note, err := resolvePrimaryText(cmd, "anchor slip", anchorFlagNoteFile, "")
			if err != nil {
				return emitErr(cmd, err)
			}
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			day, _ := cmd.Flags().GetString(flagDay)
			res, err := r.AnchorSlip(router.AnchorSlipRequest{Label: args[0], DayArg: day, Note: note, Now: clockNow()})
			if err != nil {
				return anchorRejection(cmd, err)
			}
			return emitAnchorEvent(cmd, res)
		},
	}
	cmd.Flags().String(flagDay, "", "The day the slip happened (shared grammar; default: the current logical day)")
	cmd.Flags().String(anchorFlagNoteFile, "", "Read a note for the slip from this file (or - for stdin)")
	return cmd
}

// newAnchorUseCmd builds the `use` child: log one use of a limit's allowance
// with its required intent. Capture tier.
func newAnchorUseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "use <label> (--planned | --slip)",
		Short: "Log a use of a limit's allowance, planned or a slip",
		Long: `use logs one use of a limit anchor's allowance with its intent: exactly one
of --planned (a deliberate, sanctioned use) or --slip (one you did not mean to
have). Neither or both is a usage error — the binary never guesses an intent.
--day backdates it (the shared grammar; default: the current logical day);
--note-file adds a note.

A use spends allowance; a planned use never resets or zeroes anything — it is
spend, not failure. Several uses on one day are one use-day: a limit counts
frequency, not quantity. The ack says where the allowance stands, and says so
plainly when it is over. A use on anything but a limit is rejected with the
reclassify command named.`,
		Args: cobra.ExactArgs(1),
		Example: `  # A planned use, with a note.
  lucid anchor use late-night-snacks --planned --note-file /tmp/use-note.txt

  # One you did not mean to have, last night.
  lucid anchor use late-night-snacks --slip --day @yesterday

  # Machine-readable receipt for a harness.
  lucid anchor use late-night-snacks --planned --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			planned, _ := cmd.Flags().GetBool(anchorFlagPlanned)
			slip, _ := cmd.Flags().GetBool(anchorFlagSlip)
			if planned == slip {
				return anchorUsage(cmd, "say whether this use was planned or a slip: --planned or --slip")
			}
			intent := engine.AnchorIntentPlanned
			if slip {
				intent = engine.AnchorIntentSlip
			}
			note, err := resolvePrimaryText(cmd, "anchor use", anchorFlagNoteFile, "")
			if err != nil {
				return emitErr(cmd, err)
			}
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			day, _ := cmd.Flags().GetString(flagDay)
			res, err := r.AnchorUse(router.AnchorUseRequest{
				Label: args[0], Intent: intent, DayArg: day, Note: note, Now: clockNow(),
			})
			if err != nil {
				return anchorRejection(cmd, err)
			}
			return emitAnchorEvent(cmd, res)
		},
	}
	cmd.Flags().Bool(anchorFlagPlanned, false, "The use was deliberate and sanctioned")
	cmd.Flags().Bool(anchorFlagSlip, false, "The use was one you did not mean to have")
	cmd.Flags().String(flagDay, "", "The day of the use (shared grammar; default: the current logical day)")
	cmd.Flags().String(anchorFlagNoteFile, "", "Read a note for the use from this file (or - for stdin)")
	return cmd
}

// newAnchorMissCmd builds the `miss` child: log a day a habit did not happen.
// Capture tier.
func newAnchorMissCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "miss <label>",
		Short: "Log a day a habit did not happen (the only thing a habit logs as it happens)",
		Long: `miss logs a day a habit anchor did not happen — the only thing a habit logs
as it happens; a normal day costs nothing. --day backdates it (the shared
grammar; default: the current logical day, so a miss logged after midnight and
before the rollover lands on the evening it belongs to); --note-file adds a note
("at a friend's place").

A miss always wins over a confirm on the same day, so a miss remembered after a
day was already confirmed is still recorded and still counts. One isolated miss
means nothing: the signal is never miss twice, so only a run of consecutive
misses reaching anchors.miss_alarm (default 2) is called out, in the ack and in
` + "`lucid metrics`" + `. A miss on anything but a habit is rejected with the reclassify
command named.`,
		Args: cobra.ExactArgs(1),
		Example: `  # No walk today.
  lucid anchor miss morning-walk

  # Yesterday's miss, with a note.
  lucid anchor miss morning-walk --day @yesterday --note-file /tmp/miss-note.txt

  # Machine-readable receipt for a harness.
  lucid anchor miss morning-walk --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			note, err := resolvePrimaryText(cmd, "anchor miss", anchorFlagNoteFile, "")
			if err != nil {
				return emitErr(cmd, err)
			}
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			day, _ := cmd.Flags().GetString(flagDay)
			res, err := r.AnchorMiss(router.AnchorMissRequest{Label: args[0], DayArg: day, Note: note, Now: clockNow()})
			if err != nil {
				return anchorRejection(cmd, err)
			}
			return emitAnchorEvent(cmd, res)
		},
	}
	cmd.Flags().String(flagDay, "", "The day the habit was missed (shared grammar; default: the current logical day)")
	cmd.Flags().String(anchorFlagNoteFile, "", "Read a note for the miss from this file (or - for stdin)")
	return cmd
}

// newAnchorConfirmCmd builds the `confirm` child: attest a span of days for
// each named habit, or every active habit. Echo-and-confirm tier — a confirm
// claims days as kept.
func newAnchorConfirmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "confirm [<label>...]",
		Short: "Confirm habits held through a day (quiet days become kept days)",
		Long: `confirm attests that a habit held on the days you have not logged a miss —
the weekly question that turns quiet days into kept days. Without it, an
unlogged day is unaccounted, never kept.

Each named <label> must be an active habit; with no labels it covers every
active habit. Every named habit is checked before anything is written, and
their confirms land in one write.

--through <grammar> is the last day confirmed (strict tier: a future or partial
date is rejected), defaulting to yesterday's logical day, because today is not
over. Each habit's span starts at the later of the day after its last confirm
and the day it became a habit — never the anchor date — so by default nothing
before the cutover is claimed, and a skipped week heals itself: the next confirm
covers the gap. --from <grammar> widens the start to an earlier stretch you
actually vouch for; it is rejected before the anchor date or after the
through-day. Days in the span with no logged miss count as kept; a miss inside
it stays a miss. An empty span writes nothing for that habit, and with no
active habits the command is an honest no-op.

The ack names the exact span confirmed per habit, so whoever asked can say it
back.`,
		Args: cobra.ArbitraryArgs,
		Example: `  # The Sunday question: confirm every habit through yesterday.
  lucid anchor confirm

  # One habit, through a given day.
  lucid anchor confirm morning-walk --through 2026-01-24

  # Vouch for an earlier stretch too.
  lucid anchor confirm morning-walk --from 2026-01-05

  # Machine-readable spans and receipts for a harness.
  lucid anchor confirm --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			through, _ := cmd.Flags().GetString(anchorFlagThrough)
			from, _ := cmd.Flags().GetString(anchorFlagFrom)
			res, err := r.AnchorConfirm(router.AnchorConfirmRequest{
				Labels: args, ThroughArg: through, FromArg: from, Now: clockNow(),
			})
			if err != nil {
				return anchorRejection(cmd, err)
			}
			if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
				return writeJSON(cmd.OutOrStdout(), anchorConfirmJSON(res))
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
			return nil
		},
	}
	cmd.Flags().String(anchorFlagThrough, "",
		"The last day confirmed (shared grammar; default: yesterday's logical day)")
	cmd.Flags().String(anchorFlagFrom, "",
		"Start the span earlier, at a day you vouch for (never before the anchor date)")
	return cmd
}

// newAnchorVoidCmd builds the `void` child: correct a mistaken event by
// appending a void naming it. Echo-and-confirm tier — voiding a lapse makes
// the record kinder, so it runs only on an explicit correction.
func newAnchorVoidCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "void <event-id>",
		Short: "Correct a mistaken slip, use, miss, confirm or graduation (both records are kept)",
		Long: `void corrects a misheard or mistaken event without rewriting anything: it
appends a void naming the event, the voided event drops out of all math, and
the history keeps both. --reason-file records why. A void corrects a slip, use,
miss, confirm or graduate event. A kind event is never voidable — the dated
kind events are the parameter history — so a wrong kind is corrected with
` + "`anchor kind`" + `.

Voiding a slip that had moved a quit's effective date also appends the
restoring definition record (the void first, then the record), so an older
lucid reads the corrected date too. Voiding a graduate event leaves the plain
sunset in place, so the anchor reads as an ordinary retirement.`,
		Args: cobra.ExactArgs(1),
		Example: `  # Void a misheard slip by its receipt, saying why.
  lucid anchor void anchorev_2026_04_02_a --reason-file /tmp/void-reason.txt

  # Machine-readable receipt for a harness.
  lucid anchor void anchorev_2026_04_02_a --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			reason, err := resolvePrimaryText(cmd, "anchor void", anchorFlagReasonFile, "")
			if err != nil {
				return emitErr(cmd, err)
			}
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.AnchorVoid(router.AnchorVoidRequest{EventID: args[0], Reason: reason, Now: clockNow()})
			if err != nil {
				return anchorRejection(cmd, err)
			}
			return emitAnchorEvent(cmd, res)
		},
	}
	cmd.Flags().String(anchorFlagReasonFile, "", "Read the reason for the void from this file (or - for stdin)")
	return cmd
}
