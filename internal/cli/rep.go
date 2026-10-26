package cli

import (
	"errors"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
)

// Rep verb flag names. --solo carries the optional no-people variant (--solo-file
// its shell-safe form); --level the optional positive level; --all (with its
// --include-retired alias) switches the list into the audit view. --day comes
// from the shared registerDayFlag, --body-file from registerBodyFileFlag, --json
// from the persistent root flag.
const (
	flagRepSolo           = "solo"
	flagRepSoloFile       = "solo-file"
	flagRepLevel          = "level"
	flagRepAll            = "all"
	flagRepIncludeRetired = "include-retired"
)

// newRepCmd wires `lucid rep` (reps.md §3–§6): keep and rotate the small
// practice reps a person surfaces one-per-day, each with an optional solo
// variant and an optional level. It is a thin dispatch group over four
// subcommands — deterministic, agent-free, no LLM in any path (architecture
// P9), modeled on `lucid focus`:
//
//	lucid rep add "Ask one question in a meeting" --solo "Send it afterward" --level 2
//	lucid rep add --body-file /tmp/rep.txt --solo-file /tmp/solo.txt
//	lucid rep add "Try one new route home" --day @yesterday
//	lucid rep list --json
//	lucid rep list --all
//	lucid rep surface
//	lucid rep retire rep_2026_08_23_001
//
// add appends one immutable rep and prints its receipt id; list reads the pool
// (active by default, retired under --all); surface returns exactly one active
// rep for the logical day, with its solo variant, and records that it was shown
// (rotating least-recently-surfaced, idempotent within a day); retire appends a
// retirement event, so the rep stops surfacing without being deleted.
func newRepCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rep",
		Short: "Keep and rotate your daily practice reps",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newRepAddCmd(), newRepListCmd(), newRepSurfaceCmd(), newRepRetireCmd())
	return cmd
}

// newRepAddCmd wires `lucid rep add <text> [--body-file <path>] [--solo <text> |
// --solo-file <path>] [--level N] [--day <date>]`. The text is required and
// stored verbatim (all positionals are joined so an unquoted rep works alongside
// a quoted one); --solo carries the optional solo variant, stored verbatim and
// never synthesized; --level, when given, must be a positive whole number (omit
// it for an unlevelled rep). --day is the strict backdating tier — a bad token or
// a future day is a clean refusal that writes nothing, printed to stderr. Under
// --json it emits the shared {receipt_id, logical_date} receipt through
// emitReceipt.
func newRepAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <text>",
		Short: "Append a practice rep (with an optional solo variant and level)",
		Args:  requireTextArgs(0, 1, "body-file"),
		RunE: func(cmd *cobra.Command, args []string) error {
			// A given --level is checked before anything is read or booted, so
			// a refusal never drains stdin or touches the Ledger.
			level, _ := cmd.Flags().GetInt(flagRepLevel)
			if cmd.Flags().Changed(flagRepLevel) && level < 1 {
				return newUsageError(cmd, errors.New("--level is a positive whole number (omit it for an unlevelled rep). Nothing was saved"))
			}
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			// Reject two fields both reading stdin before any read drains it.
			if err = ensureSingleStdinFlags(cmd, "body-file", flagRepSoloFile); err != nil {
				return emitErr(cmd, err)
			}
			// --body-file supplies the text and --solo-file the solo variant off
			// the command line; the positional text and --solo are the inline
			// forms, each mutually exclusive with its file form.
			text, err := resolvePrimaryText(cmd, "rep add", "body-file", strings.Join(args, " "))
			if err != nil {
				return emitErr(cmd, err)
			}
			solo, err := resolveOptionalText(cmd, "rep add", "solo variant", flagRepSolo, flagRepSoloFile)
			if err != nil {
				return emitErr(cmd, err)
			}
			day, _ := cmd.Flags().GetString(flagDay)
			res, err := r.AddRep(router.AddRepRequest{
				Text:   text,
				Solo:   solo,
				Level:  level,
				DayArg: day,
				Now:    time.Now(),
			})
			if err != nil {
				return emitRefusedDay(cmd, err)
			}
			return emitReceipt(cmd, res.Rep.ID, res.Rep.LogicalDate, res.Ack)
		},
	}
	cmd.Flags().String(flagRepSolo, "", "Optional solo variant — a version of the rep that works with nobody around")
	cmd.Flags().String(flagRepSoloFile, "", "Read the solo variant from this file (or - for stdin) instead of --solo")
	cmd.Flags().Int(flagRepLevel, 0, "Optional level, a positive whole number (omit for an unlevelled rep)")
	registerDayFlag(cmd)
	registerBodyFileFlag(cmd, "rep text")
	return cmd
}

// newRepListCmd wires `lucid rep list [--all] [--json]`: the rep pool (state
// folded, retirement markers removed), active-only by default with the audit
// view (active + retired) under --all (alias --include-retired). Human-first by
// default; the structured {count, reps} list under --json (ADR-0007). It writes
// nothing.
func newRepListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List practice reps (active by default; --all includes retired)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			all, _ := cmd.Flags().GetBool(flagRepAll)
			includeRetired, _ := cmd.Flags().GetBool(flagRepIncludeRetired)
			res, err := r.ListReps(router.ListRepsRequest{IncludeRetired: all || includeRetired})
			if err != nil {
				return err
			}
			return emit(cmd, res.View, res.Lines)
		},
	}
	cmd.Flags().Bool(flagRepAll, false, "Include retired reps (the audit view)")
	cmd.Flags().Bool(flagRepIncludeRetired, false, "Include retired reps (the audit view); alias of --all")
	return cmd
}

// newRepSurfaceCmd wires `lucid rep surface [--json]`: the one active rep for the
// logical day, together with its solo variant, recording that it was shown. It
// is the daily one-per-day source a morning surface reads. It takes no solo-day
// or capacity input. Human-first by default (`<text> (level N) — Solo: <solo>`);
// the single pick as {day, rep} under --json.
func newRepSurfaceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "surface",
		Short: "Surface one practice rep for the day (with its solo variant)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.SurfaceRep(time.Now())
			if err != nil {
				return err
			}
			return emit(cmd, res.View, res.Lines)
		},
	}
}

// newRepRetireCmd wires `lucid rep retire <id>`: retire an active rep by
// appending a retirement event that references it. Nothing is deleted — the rep
// drops out of the surface and the default list but stays in `list --all`. An
// unknown id, or one already retired, is a clean error printed to stderr with a
// non-zero exit and nothing appended.
func newRepRetireCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "retire <id>",
		Short: "Retire a practice rep (kept for the audit view)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.RetireRep(router.RetireRepRequest{ID: args[0], Now: time.Now()})
			if err != nil {
				return emitErr(cmd, err)
			}
			return emitReceipt(cmd, res.Rep.ID, res.Rep.LogicalDate, res.Ack)
		},
	}
}
