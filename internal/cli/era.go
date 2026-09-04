package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
)

// Era write-verb flag names (mvp/life-archive.md §4): a named life chapter with
// an optional, backdate-aware start/end range. flagNote is shared with
// `lucid injury` (declared in injury.go).
const (
	flagStart = "start"
	flagEnd   = "end"
)

// eraWriteView is the machine-readable projection for `lucid era --json`. An era
// is a chapter, not a graded state (life-archive.md §4), so — unlike the shared
// registryWriteView that injury/thread/pet use — it surfaces no status word.
// Instead it renders the chapter span plus its raw start/end bounds, so no era
// output surface presents "active". This is output-only: the stored status
// placeholder is untouched, merely never surfaced. Built CLI-side with stable
// snake_case names so a harness branches on fields rather than parsing prose.
type eraWriteView struct {
	Kind        string         `json:"kind"`
	Key         string         `json:"key"`
	DisplayName string         `json:"display_name"`
	Created     bool           `json:"created"`
	Start       string         `json:"start,omitempty"`
	End         string         `json:"end,omitempty"`
	Span        string         `json:"span,omitempty"`
	Fields      map[string]any `json:"fields"`
}

// eraWriteViewOf projects an era write result into the stable --json shape: the
// chapter span (through the shared router.EraSpan helper, so the write ack,
// recall, and this machine surface cannot drift) plus its raw start/end bounds,
// and Fields as a (possibly empty) object rather than null so a harness can index
// it unconditionally.
func eraWriteViewOf(res router.RegistryWriteResult) eraWriteView {
	fields := res.Fields
	if fields == nil {
		fields = map[string]any{}
	}
	start := fieldString(fields["start"])
	end := fieldString(fields["end"])
	return eraWriteView{
		Kind:        res.Kind,
		Key:         res.Key,
		DisplayName: res.DisplayName,
		Created:     res.Created,
		Start:       start,
		End:         end,
		Span:        router.EraSpan(start, end),
		Fields:      fields,
	}
}

// fieldString reads a string-valued registry Fields value (an era's start/end
// bound is stored as a plain string), trimmed, or "" when absent or not a string.
func fieldString(v any) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

// eraListItemView is one row of `lucid era list --json`, built CLI-side with
// stable snake_case names so a harness branches on fields rather than parsing
// prose. The raw start/end bounds are surfaced verbatim (omitted when absent)
// alongside the rendered chapter span.
type eraListItemView struct {
	Key         string `json:"key"`
	DisplayName string `json:"display_name"`
	Start       string `json:"start,omitempty"`
	End         string `json:"end,omitempty"`
	Span        string `json:"span,omitempty"`
}

// eraListView is the `lucid era list --json` envelope: the eras array is always
// present (never null) so a harness can index it unconditionally on an empty
// store.
type eraListView struct {
	Eras []eraListItemView `json:"eras"`
}

// eraListViewOf projects the router's read-only era summaries into the stable
// --json shape.
func eraListViewOf(eras []router.EraSummary) eraListView {
	view := eraListView{Eras: make([]eraListItemView, 0, len(eras))}
	for _, e := range eras {
		view.Eras = append(view.Eras, eraListItemView{
			Key:         e.Key,
			DisplayName: e.DisplayName,
			Start:       e.Start,
			End:         e.End,
			Span:        e.Span,
		})
	}
	return view
}

// reservedEraName rejects a name that would shadow an `era` subcommand or read
// as a flag (life-archive.md §4, strict creation): the reserved words `list`,
// `ls`, `show`, and `help` (compared case-insensitively), and any name
// beginning with '-'. It is applied on the create, amend, and bare-alias paths
// before any write, so a fat-fingered `era list` / `era ls` / `era show` can
// never be minted or amended into a chapter (including `era create list`).
func reservedEraName(name string) error {
	trimmed := strings.TrimSpace(name)
	if strings.HasPrefix(trimmed, "-") {
		return fmt.Errorf("%q is not a valid era name — a name cannot begin with '-'; nothing was saved", name)
	}
	switch strings.ToLower(trimmed) {
	case "list", "ls", "show", "help":
		return fmt.Errorf("%q is a reserved word and cannot be used as an era name; nothing was saved", trimmed)
	}
	return nil
}

// newEraCmd wires the `lucid era` parent and its explicit subcommands
// (mvp/life-archive.md §4): `era list` (read-only enumeration), `era create
// <name>` (the sole mint path), and `era amend <name>` (append-only amend that
// hard-errors on a non-matching name). Stories attach to an era via refs.era so
// the past becomes browsable by chapter rather than by a date no one remembers.
//
// Bare `era <name>` is retained as an amend-only alias — it amends an existing
// chapter and never mints (a non-matching name hard-errors), preserving muscle
// memory while keeping creation an intentional, explicit `era create` act. A
// bare `era` with no args is a usage error; `era list/create/amend` dispatch to
// the subcommands (cobra prefers a subcommand match). A name may contain spaces
// (joined from the trailing args).
func newEraCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "era [name]",
		Short: "List, create, or amend life chapters (eras)",
		Long: "Life chapters (eras) browsable by name, not by a date no one remembers.\n\n" +
			"  era list            Read-only: list existing eras (never writes)\n" +
			"  era create <name>   Start a new chapter (the only way to mint an era)\n" +
			"  era amend <name>    Amend an existing chapter (errors if it doesn't exist)\n\n" +
			"Bare `era <name>` is an amend-only alias: it amends an existing chapter and\n" +
			"errors on a name that matches nothing — only `era create` mints a new era.",
		Args: cobra.MinimumNArgs(1),
		// Bare `era <name>` amends only (never mints); a non-matching name
		// hard-errors via WriteEra's MustExist gate.
		RunE: runEraWrite(true),
	}
	registerEraWriteFlags(cmd)
	cmd.AddCommand(newEraListCmd())
	cmd.AddCommand(newEraCreateCmd())
	cmd.AddCommand(newEraAmendCmd())
	return cmd
}

// newEraListCmd wires `lucid era list`: the read-only enumeration of existing
// eras (mvp/life-archive.md §4). It writes nothing — a discovery command must
// have no side effects — so it dispatches over the projection-only
// [router.Router.ListEras]. Human-first bullets by default (`• <name> (<span>)
// — <key>`); the eraListView shape under --json. Distinct from `recall --era`,
// which browses the stories filed under one known era.
func newEraListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Read-only: list your life chapters (eras) — never writes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			eras, err := r.ListEras()
			if err != nil {
				return err
			}
			return renderEraList(cmd, eras)
		},
	}
}

// newEraCreateCmd wires `lucid era create <name> [--start …] [--end …]
// [--note …]`: the sole path that mints a new chapter (mvp/life-archive.md §4).
// It dispatches over [router.WriteEra] with MustExist false, so it creates or
// amends by name; the reserved-name guard runs first so `era create list` (and
// friends) can never mint a shadow chapter.
func newEraCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Start a new life chapter (the only way to mint an era)",
		Args:  cobra.MinimumNArgs(1),
		RunE:  runEraWrite(false),
	}
	registerEraWriteFlags(cmd)
	return cmd
}

// newEraAmendCmd wires `lucid era amend <name> [--start …] [--end …]
// [--note …]`: the strict, append-only amend (mvp/life-archive.md §4). It
// dispatches over [router.WriteEra] with MustExist true, so a name that matches
// no existing chapter hard-errors and writes nothing — only `era create` mints.
func newEraAmendCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "amend <name>",
		Short: "Amend an existing life chapter (errors if it doesn't exist)",
		Args:  cobra.MinimumNArgs(1),
		RunE:  runEraWrite(true),
	}
	registerEraWriteFlags(cmd)
	return cmd
}

// registerEraWriteFlags declares the shared write flags on an era create/amend
// (and the bare alias) command, so the three write paths self-document the same
// range/note grammar.
func registerEraWriteFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.String(flagStart, "", "When the chapter began: @yesterday, YYYY-MM-DD, or a partial date like 2014 or 2014-09")
	f.String(flagEnd, "", "When it ended, same forms as --start (omit for a still-running chapter)")
	f.String(flagNote, "", "A free-text note kept verbatim")
	f.String("note-file", "", "Read the note from this file (or - for stdin) instead of --note")
}

// runEraWrite builds the shared RunE for the era write paths: create
// (mustExist=false, the sole mint path), amend (mustExist=true), and the bare
// `era <name>` alias (mustExist=true). It rejects a reserved/flag-shaped name
// before any write, then dispatches over [router.WriteEra] — deterministic and
// agent-free — reusing the same append-only merge path as `lucid injury`.
func runEraWrite(mustExist bool) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		name := strings.Join(args, " ")
		if err := reservedEraName(name); err != nil {
			return emitErr(cmd, err)
		}
		r, err := bootedRouter(cmd)
		if err != nil {
			return err
		}
		f := cmd.Flags()
		req := router.EraWriteRequest{
			Name:      name,
			Now:       clockNow(),
			MustExist: mustExist,
		}
		req.Start, _ = f.GetString(flagStart)
		req.End, _ = f.GetString(flagEnd)
		// --note-file supplies the note off the command line; it is mutually
		// exclusive with the inline --note.
		note, err := resolveOptionalText(cmd, "era", "note", flagNote, "note-file")
		if err != nil {
			return emitErr(cmd, err)
		}
		req.Note = note

		res, err := r.WriteEra(req)
		if err != nil {
			// The root silences returned errors, so a rejected --start/--end or a
			// non-matching amend would otherwise be a bare exit code. A strict flag
			// has to say what it wanted (error-states.md §B-3, §B-4).
			return emitErr(cmd, err)
		}
		return renderRegistryWrite(cmd, res)
	}
}

// renderEraList prints the read-only era listing: the --json view, or
// human-first bullets. An empty store prints an honest "No eras recorded yet.";
// a dateless chapter drops the (span) parenthetical rather than printing "()".
func renderEraList(cmd *cobra.Command, eras []router.EraSummary) error {
	if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
		return writeJSON(cmd.OutOrStdout(), eraListViewOf(eras))
	}
	out := cmd.OutOrStdout()
	if len(eras) == 0 {
		_, _ = fmt.Fprintln(out, "No eras recorded yet.")
		return nil
	}
	for _, e := range eras {
		if e.Span != "" {
			_, _ = fmt.Fprintf(out, "• %s (%s) — %s\n", e.DisplayName, e.Span, e.Key)
			continue
		}
		_, _ = fmt.Fprintf(out, "• %s — %s\n", e.DisplayName, e.Key)
	}
	return nil
}
