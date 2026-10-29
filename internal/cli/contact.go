package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/contact"
	"github.com/mrz1836/lucid/internal/router"
)

// Contact verb flag names. --via carries the optional closed-set channel;
// --note the optional one-line note (--note-file its shell-safe form); --wrap
// switches `due` to the Sunday wrap. --day comes from the shared
// registerDayFlag (the bare capture form only), --json from the persistent
// root flag.
const (
	flagContactVia      = "via"
	flagContactNote     = "note"
	flagContactNoteFile = "note-file"
	flagContactWrap     = "wrap"
)

// errContactNotRecorded maps a deterministically refused contact write to a
// non-zero exit. The fixed reason is already printed to stderr; this sentinel
// keeps a script from reading a refused write as success (mirrors
// errPersonNotRecorded). Every contact write verb shares it.
var errContactNotRecorded = errors.New("lucid: contact not recorded")

// contactReceiptView is the receipt every contact write emits under --json
// (usage/commands.md §contact "Receipts"): the global {receipt_id,
// logical_date} receipt extended with the canonical person the subject
// resolved to and the stored event verbatim — the way the anchor event verbs
// extend it.
type contactReceiptView struct {
	ReceiptID   string        `json:"receipt_id"`
	LogicalDate string        `json:"logical_date"`
	PersonKey   string        `json:"person_key"`
	Event       contact.Event `json:"event"`
}

// emitContactReceipt renders a contact write's result: the shared receipt under
// --json, else the inventory-only ack.
func emitContactReceipt(cmd *cobra.Command, res router.ContactWriteResult) error {
	if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
		return writeJSON(cmd.OutOrStdout(), contactReceiptView{
			ReceiptID:   res.Event.ID,
			LogicalDate: res.Event.LogicalDate,
			PersonKey:   res.PersonKey,
			Event:       res.Event,
		})
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
	return nil
}

// contactUsage types a contact verb's input-shape refusal — a window, a --via
// or a note the grammar does not take (error-states.md Ct-3..Ct-5) — as a
// usage error (exit 2), carrying the fixed copy and the nothing-was-saved
// clause every contact refusal ends with.
func contactUsage(cmd *cobra.Command, reason error) error {
	return newUsageError(cmd, errors.New(reason.Error()+". Nothing was saved."))
}

// contactRejection maps the router's deterministic refusals to the shared exit
// shape. An input-shape refusal is a usage error the root renders once (exit
// 2); every other refusal — an unknown or ambiguous subject, a transition from
// the wrong state, a snooze or void that does not apply, a refused --day — has
// its fixed reason printed to stderr and exits non-zero. Nothing was written
// either way. Anything else is a real failure: printed, and returned
// unchanged.
func contactRejection(cmd *cobra.Command, err error) error {
	var (
		rejected *router.ContactRejectedError
		day      *router.DayRejectedError
	)
	switch {
	case errors.Is(err, contact.ErrInvalidWindow), errors.Is(err, contact.ErrInvalidVia),
		errors.Is(err, contact.ErrNoteNotOneLine):
		return newUsageError(cmd, err)
	case errors.As(err, &rejected), errors.As(err, &day):
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err.Error())
		return errContactNotRecorded
	}
	return emitErr(cmd, err)
}

// newContactCmd wires `lucid contact` (ADR-0014; usage/contact.md): keep in
// touch with the people who matter inside a time frame you choose. The parent's
// own RunE is the capture — `lucid contact <subject>` records that you were in
// touch — and the cadence, snooze, void and due verbs hang beneath, the shape
// `person` uses. Deterministic and agent-free in every path (architecture P9).
// A subcommand name shadows a person with the same literal name, who is reached
// by their person_key instead.
func newContactCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "contact <subject...>",
		Short: "Keep in touch — per-person check-in cadences, a who's-due view, and contact capture",
		Long: `contact keeps you in touch with the people who matter to you, inside a time
frame you choose. It is a reminder for you and never a message to anyone else:
lucid never contacts, messages or schedules anything with the people you track
here.

The bare form records that you and a person were in touch on a logical day and
prints its receipt. It is a capture, like obs: it runs at once, and
` + "`contact void <id>`" + ` corrects a mistake. Contact either way counts; nothing asks who
reached out, and no content is needed. --via (call, text, video or in-person)
and a one-line --note (or --note-file) are optional, and --day backdates on the
strict tier. Logging contact never creates a person: a name that matches nobody
points at ` + "`lucid person create <name>`" + `.

A cadence ("every 4w") gives a person a window, and the due view groups people
over the Monday–Sunday week: due this week, still open from before, and coming
up next week. A line shows the cadence and the last time you caught up, and
never a count of days. ` + "`contact due --wrap`" + ` is the Sunday wrap: the first two lists only.

Everything is append-only: a cadence change, a contact, a snooze and a void are
each a new event with its own receipt, and nothing is rewritten. The subcommand
names shadow a person with the same literal name — reach such a person by their
person_key.`,
		Args: cobra.MinimumNArgs(1),
		Example: `  # Log a call from yesterday.
  lucid contact Sam Rivera --via call --day @yesterday

  # Check in with someone every three weeks, then see who is due.
  lucid contact every Jordan Lee 3w
  lucid contact due

  # Skip a person this week, and read the Sunday wrap for a harness.
  lucid contact snooze Alex Kim
  lucid contact due --wrap --json`,
		RunE: runContactLog,
	}
	cmd.Flags().String(flagContactVia, "", "How you were in touch: "+strings.Join(contact.ViaValues(), ", ")+" (optional)")
	cmd.Flags().String(flagContactNote, "", "An optional one-line note")
	cmd.Flags().String(flagContactNoteFile, "", "Read the one-line note from this file (or - for stdin) instead of --note")
	registerDayFlag(cmd)
	cmd.AddCommand(
		newContactEveryCmd(),
		newContactCadenceCmd(contact.ActionPause, "Pause a check-in cadence (keeps its window)",
			`pause takes an active check-in cadence off the lists and keeps its window. A
cadence that is not active is refused, and nothing is written. resume puts it
back.`),
		newContactCadenceCmd(contact.ActionResume, "Resume a paused check-in cadence",
			`resume puts a paused check-in cadence back on the lists. The window restarts
from the later of the last contact and the resume day, so a pause never puts
anyone straight into "still open from before". A cadence that is not paused is
refused, and nothing is written.`),
		newContactCadenceCmd(contact.ActionRetire, "Retire a check-in cadence (its history is kept)",
			`retire ends a live check-in cadence, active or paused, and keeps its history.
With no live cadence it is refused, and nothing is written. A later every sets
a fresh one.`),
		newContactSnoozeCmd(),
		newContactVoidCmd(),
		newContactDueCmd(),
	)
	return cmd
}

// runContactLog is the bare capture form: `lucid contact <subject...> [--via]
// [--note | --note-file] [--day]`. A --via outside the closed set is a usage
// error checked before anything is read or booted, so a refusal never drains
// stdin or touches the Ledger; a note of more than one line is a usage error
// too. Under --json it emits the contact receipt.
func runContactLog(cmd *cobra.Command, args []string) error {
	via, _ := cmd.Flags().GetString(flagContactVia)
	via = strings.TrimSpace(via)
	if !contact.ValidVia(via) {
		return contactUsage(cmd, contact.ErrInvalidVia)
	}
	r, err := bootedRouter(cmd)
	if err != nil {
		return err
	}
	note, err := resolveOptionalText(cmd, "contact", "contact note", flagContactNote, flagContactNoteFile)
	if err != nil {
		return emitErr(cmd, err)
	}
	if strings.ContainsAny(strings.TrimSpace(note), "\r\n") {
		return contactUsage(cmd, contact.ErrNoteNotOneLine)
	}
	day, _ := cmd.Flags().GetString(flagDay)
	res, err := r.ContactLog(router.ContactLogRequest{
		Subject: strings.Join(args, " "),
		Via:     via,
		Note:    note,
		DayArg:  day,
		Now:     clockNow(),
	})
	if err != nil {
		return contactRejection(cmd, err)
	}
	return emitContactReceipt(cmd, res)
}

// newContactEveryCmd wires `lucid contact every <subject...> <window>`: set a
// check-in cadence, or change the window of an existing one. The window is the
// last argument and everything before it the subject; a window outside the Nd /
// Nw grammar is a usage error that carries the grammar, checked before
// anything is booted. Echo-and-confirm tier for a harness.
func newContactEveryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "every <subject...> <window>",
		Short: "Set or change a person's check-in window (Nd or Nw, e.g. 4w)",
		Long: `every gives a person a check-in window, or changes the window of an existing
cadence. The window is the last argument: Nd or Nw, a whole number of at least 1
days or weeks, so 28d and 4w are the same window (the form you type is kept for
display). Everything before it is the subject.

A new window is anchored on the most recent contact, or on today when nothing is
logged, so a new 4w first comes due four weeks later; log a backdated contact
to record when you really last caught up. Changing the window keeps the anchor,
and on a paused cadence it changes the window and stays paused.`,
		Args: cobra.MinimumNArgs(2),
		Example: `  lucid contact every Jordan Lee 3w
  lucid contact every Sam Rivera 28d --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			window := args[len(args)-1]
			if _, err := contact.ParseWindow(window); err != nil {
				return contactUsage(cmd, err)
			}
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.ContactCadence(router.ContactCadenceRequest{
				Subject: strings.Join(args[:len(args)-1], " "),
				Action:  contact.ActionSet,
				Window:  window,
				Now:     clockNow(),
			})
			if err != nil {
				return contactRejection(cmd, err)
			}
			return emitContactReceipt(cmd, res)
		},
	}
}

// newContactCadenceCmd wires one of the cadence state verbs — `pause`,
// `resume`, `retire` — each `<action> <subject...>`: one appended cadence event
// stamped with the current logical day. A transition from the wrong state is
// refused naming the person and the state, and nothing is written.
// Echo-and-confirm tier for a harness.
func newContactCadenceCmd(action, short, long string) *cobra.Command {
	return &cobra.Command{
		Use:   action + " <subject...>",
		Short: short,
		Long:  long,
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.ContactCadence(router.ContactCadenceRequest{
				Subject: strings.Join(args, " "),
				Action:  action,
				Now:     clockNow(),
			})
			if err != nil {
				return contactRejection(cmd, err)
			}
			return emitContactReceipt(cmd, res)
		},
	}
}

// newContactSnoozeCmd wires `lucid contact snooze <subject...>`: keep a person
// off the lists for the ISO week of the current logical day, leaving their
// window and anchor untouched. It takes no --day, needs an active cadence, and
// is refused a second time in the same week. Capture tier, like the bare form.
func newContactSnoozeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "snooze <subject...>",
		Short: "Skip a person this week (their window is untouched)",
		Long: `snooze keeps a person with an active cadence off the lists for this
Monday–Sunday week, leaving their window and anchor untouched: next Monday they
appear wherever their window puts them. It always covers the current week and
takes no --day. A person without an active cadence, or one already snoozed this
week, is refused, and nothing is written. contact void <id> undoes a snooze.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.ContactSnooze(router.ContactSnoozeRequest{Subject: strings.Join(args, " "), Now: clockNow()})
			if err != nil {
				return contactRejection(cmd, err)
			}
			return emitContactReceipt(cmd, res)
		},
	}
}

// newContactVoidCmd wires `lucid contact void <id>`: correct a mistaken
// contact or snooze by its receipt id by appending a void that names it. An
// unknown id, an already-voided event, a void and a cadence event are refused,
// and nothing is written. Echo-and-confirm tier for a harness.
func newContactVoidCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "void <id>",
		Short: "Correct a mistaken contact or snooze by its receipt id",
		Long: `void corrects a contact or a snooze by the receipt id it returned. A voided
contact stops counting toward the window, a voided snooze stops hiding the
person, and both records stay in the history. A cadence is never voided —
change it with every, pause, resume or retire.`,
		Args:    cobra.ExactArgs(1),
		Example: `  lucid contact void contact_2026_10_29_a`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.ContactVoid(router.ContactVoidRequest{ID: args[0], Now: clockNow()})
			if err != nil {
				return contactRejection(cmd, err)
			}
			return emitContactReceipt(cmd, res)
		},
	}
}

// newContactDueCmd wires `lucid contact due [--wrap] [--json]`: the due view
// for the current logical day — due this week, still open from before, coming
// up next week, and the week's birthdays — or under --wrap the Sunday wrap's
// two lists alone. Computed fresh on every call; it appends nothing.
// Human-first by default; the documented, byte-deterministic view under --json.
func newContactDueCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "due",
		Short: "Show who's due this week (or the Sunday wrap with --wrap)",
		Long: `due shows the people whose check-in window closes this Monday–Sunday week,
the ones still open from before, the ones coming up next week, and the week's
birthdays. Each line shows the cadence and the last time you caught up, never a
count of days. Paused, retired and snoozed people stay off the lists, and
off-limits people appear nowhere in the view.

--wrap is the Sunday wrap: only "Due this week" and "Still open from before",
computed fresh. Anyone left open rolls into next Monday's list on their own.
Under --json the view is byte-deterministic for a given Ledger and day.`,
		Args: cobra.NoArgs,
		Example: `  lucid contact due
  lucid contact due --json
  lucid contact due --wrap --json`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			wrap, _ := cmd.Flags().GetBool(flagContactWrap)
			view, err := r.ContactDue(clockNow(), wrap)
			if err != nil {
				return emitErr(cmd, err)
			}
			text := contact.RenderDueText(view)
			if wrap {
				text = contact.RenderWrapText(view)
			}
			return emit(cmd, view, []string{text})
		},
	}
	cmd.Flags().Bool(flagContactWrap, false, "The Sunday wrap: only the due-this-week and still-open lists")
	return cmd
}
