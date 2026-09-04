package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
)

// Focus verb flag names. --success carries the optional criterion; --all (with
// its --include-retired alias) switches the list into the audit view. --day
// comes from the shared registerDayFlag, --json from the persistent root flag.
const (
	flagFocusSuccess        = "success"
	flagFocusAll            = "all"
	flagFocusIncludeRetired = "include-retired"
)

// newFocusCmd wires `lucid focus` (focus.md §3–§5): keep and rotate the slow
// inner "work-ons" a person surfaces one-per-day. It is a thin dispatch group
// over four subcommands — deterministic, agent-free, no LLM in any path
// (architecture P9), modeled on `lucid reframe`:
//
//	lucid focus add "Take a walk after lunch" --success "I stepped outside"
//	lucid focus add "Read ten pages tonight" --day @yesterday
//	lucid focus list --json
//	lucid focus list --all
//	lucid focus surface
//	lucid focus retire focus_2026_08_23_001
//
// add appends one immutable item and prints its receipt id; list reads the pool
// (active by default, retired under --all); surface returns exactly one active
// item for the logical day and records that it was shown (rotating
// least-recently-surfaced, idempotent within a day); retire appends a
// retirement event, so the item stops surfacing without being deleted.
func newFocusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "focus",
		Short: "Keep and rotate your focus items (slow inner work-ons)",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newFocusAddCmd(), newFocusListCmd(), newFocusSurfaceCmd(), newFocusRetireCmd())
	return cmd
}

// newFocusAddCmd wires `lucid focus add <text> [--success <criterion>] [--day
// <date>]`. The text is required and stored verbatim (all positionals are joined
// so an unquoted work-on works alongside a quoted one); --success carries an
// optional criterion, stored verbatim and never synthesized. --day is the strict
// backdating tier — a bad token or a future day is a clean refusal that writes
// nothing, printed to stderr like the observation micro-log's --day. It ignores
// --json (a write verb, same as `log`/`reframe`).
func newFocusAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <text>",
		Short: "Append a focus item (a slow inner work-on)",
		Args:  requireTextArgs(0, 1, "body-file"),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			// Reject two fields both reading stdin before any read drains it.
			if err = ensureSingleStdinFlags(cmd, "body-file", "success-file"); err != nil {
				return emitErr(cmd, err)
			}
			// --body-file supplies the text and --success-file the criterion off
			// the command line; the positional text and --success are the inline
			// forms, each mutually exclusive with its file form.
			text, err := resolvePrimaryText(cmd, "focus add", "body-file", strings.Join(args, " "))
			if err != nil {
				return emitErr(cmd, err)
			}
			success, err := resolveOptionalText(cmd, "focus add", "success criterion", flagFocusSuccess, "success-file")
			if err != nil {
				return emitErr(cmd, err)
			}
			day, _ := cmd.Flags().GetString(flagDay)
			res, err := r.AddFocus(router.AddFocusRequest{
				Text:             text,
				SuccessCriterion: success,
				DayArg:           day,
				Now:              time.Now(),
			})
			if err != nil {
				return emitRefusedDay(cmd, err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
			return nil
		},
	}
	cmd.Flags().String(flagFocusSuccess, "", "Optional success criterion for the focus item")
	cmd.Flags().String("success-file", "", "Read the success criterion from this file (or - for stdin) instead of --success")
	registerDayFlag(cmd)
	registerBodyFileFlag(cmd, "focus item text")
	return cmd
}

// newFocusListCmd wires `lucid focus list [--all] [--json]`: the focus pool
// (state folded, retirement markers removed), active-only by default with the
// audit view (active + retired) under --all (alias --include-retired).
// Human-first by default; the structured list under --json (ADR-0007). It writes
// nothing.
func newFocusListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List focus items (active by default; --all includes retired)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			all, _ := cmd.Flags().GetBool(flagFocusAll)
			includeRetired, _ := cmd.Flags().GetBool(flagFocusIncludeRetired)
			res, err := r.ListFocus(router.ListFocusRequest{IncludeRetired: all || includeRetired})
			if err != nil {
				return err
			}
			return emit(cmd, res.View, res.Lines)
		},
	}
	cmd.Flags().Bool(flagFocusAll, false, "Include retired items (the audit view)")
	cmd.Flags().Bool(flagFocusIncludeRetired, false, "Include retired items (the audit view); alias of --all")
	return cmd
}

// newFocusSurfaceCmd wires `lucid focus surface [--json]`: the one active focus
// item for the logical day, recording that it was shown. It is the daily
// one-per-day source the morning surface reads. Human-first by default; the
// single pick as a structured object under --json.
func newFocusSurfaceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "surface",
		Short: "Surface one focus item for the day",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.SurfaceFocus(time.Now())
			if err != nil {
				return err
			}
			return emit(cmd, res.View, res.Lines)
		},
	}
}

// newFocusRetireCmd wires `lucid focus retire <id>`: retire an active focus item
// by appending a retirement event that references it. Nothing is deleted — the
// item drops out of the surface and the default list but stays in `list --all`.
// An unknown id, or one already retired, is a clean error printed to stderr with
// a non-zero exit and nothing appended.
func newFocusRetireCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "retire <id>",
		Short: "Retire a focus item (kept for the audit view)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.RetireFocus(router.RetireFocusRequest{ID: args[0], Now: time.Now()})
			if err != nil {
				return emitErr(cmd, err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
			return nil
		},
	}
}
