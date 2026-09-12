package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/router"
)

// Memory story-capture flag names (mvp/life-archive.md §3). flagDay and
// flagCaption are shared with `lucid attach` (declared in attach.go); the rest
// are the story convention keys. Every flag is optional — a bare
// `lucid memory "…"` is a valid text-only story.
const (
	flagCertainty = "certainty"
	flagEra       = "era"
	flagPlace     = "place"
	flagPeople    = "people"
	flagTone      = "tone"
	flagWhy       = "why"
	flagFollowup  = "followup"
	flagAttach    = "attach"

	// flagClearFollowup is the explicit follow-up clear on `memory amend` (Q4):
	// setting --followup replaces the thread, --clear-followup removes it, and a
	// bare empty --followup is rejected as ambiguous — so "clear" is never
	// something a script triggers by accident.
	flagClearFollowup = "clear-followup"

	// flagHistory turns `memory show` from the current (folded) view into the
	// current view plus the amendment trail (each amended field as
	// original → amended @ recorded_at).
	flagHistory = "history"
)

// memoryWriteView is the machine-readable projection of a `lucid memory --json`
// turn: the appended event id, its logical day, whether it took the partial
// path, whether it was rejected (memory kind disabled), and the resolved
// relational refs (era/place/person/entry). Built CLI-side with stable
// snake_case names so a harness branches on fields, not prose.
type memoryWriteView struct {
	EventID     string         `json:"event_id"`
	LogicalDate string         `json:"logical_date"`
	Partial     bool           `json:"partial"`
	Rejected    bool           `json:"rejected"`
	Refs        map[string]any `json:"refs"`
}

// renderMemoryWrite prints a story-capture result: the --json view (refs always
// a non-nil object so a harness can index it), or the inventory ack prose.
func renderMemoryWrite(cmd *cobra.Command, res router.MemoryWriteResult) error {
	if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
		refs := res.Refs
		if refs == nil {
			refs = map[string]any{}
		}
		return writeJSON(cmd.OutOrStdout(), memoryWriteView{
			EventID:     res.EventID,
			LogicalDate: res.LogicalDate,
			Partial:     res.Partial,
			Rejected:    res.Rejected,
			Refs:        refs,
		})
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
	return nil
}

// newMemoryCmd wires `lucid memory <text> [flags]`: the story-capture verb of
// the life-archive module (mvp/life-archive.md §3). It writes one KindMemory
// event on the frozen bitemporal envelope, backdated to when the story happened,
// linked to the era it sits in, the place and people in it, and — when --attach
// is present — a photo reused through `lucid attach` (a text-only story simply
// omits the media and is never gated). It is dispatch-only over
// [router.WriteMemory] — deterministic and agent-free (architecture P9); the
// Ledger scaffolds on first use so capture never blocks on setup
// (product-principles.md P10). The memory kind is enable-gated: a disabled kind
// is reported with the enable hint, nothing written.
func newMemoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory <text>",
		Short: "Record a story from your past — backdated, linked, kept",
		Args:  requireTextArgs(0, 1, "body-file"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMemory(cmd, args)
		},
	}
	f := cmd.Flags()
	f.String(flagCertainty, "", "How you recall it: vivid | hazy | reconstructed")
	f.String(flagEra, "", "The era (life chapter) key this story sits in")
	f.String(flagPlace, "", "Where it happened (a place name, registered like a location)")
	f.StringSlice(flagPeople, nil, "Who was there (repeatable, or comma-separated)")
	f.String(flagTone, "", "The emotional color, one phrase")
	f.String(flagWhy, "", "Why it still matters")
	f.String(flagFollowup, "", "The thread to pull next time")
	f.String(flagDay, "", "When it happened: @yesterday, YYYY-MM-DD, or a partial date like 2014 or 2014-09")
	f.String(flagAttach, "", "Optional photo/media file to attach and link to this story")
	f.String(flagCaption, "", "Caption for the attached media")
	registerBodyFileFlag(cmd, "memory story text")
	f.String("tone-file", "", "Read the tone from this file (or - for stdin) instead of --tone")
	f.String("why-file", "", "Read why-it-matters from this file (or - for stdin) instead of --why")
	f.String("followup-file", "", "Read the follow-up from this file (or - for stdin) instead of --followup")
	f.String("caption-file", "", "Read the caption from this file (or - for stdin) instead of --caption")
	// amend and show are subcommands of memory (the era parent pattern): a bare
	// `memory <text>` still creates, while `memory amend <obs_id>` corrects a
	// stored story in place and `memory show <obs_id>` reads one folded story.
	cmd.AddCommand(newMemoryAmendCmd())
	cmd.AddCommand(newMemoryShowCmd())
	return cmd
}

// resolveMemoryText gathers the story body and its four prose-metadata fields.
// Each may be given inline or off the command line via a --*-file flag; the file
// forms are mutually exclusive with their inline counterparts. The structured
// certainty/era/place/people/day/attach inputs are untouched.
func resolveMemoryText(cmd *cobra.Command, args []string) (text, tone, why, followup, caption string, err error) {
	if text, err = resolvePrimaryText(cmd, "memory", "body-file", strings.Join(args, " ")); err != nil {
		return text, tone, why, followup, caption, err
	}
	if tone, err = resolveOptionalText(cmd, "memory", "tone", flagTone, "tone-file"); err != nil {
		return text, tone, why, followup, caption, err
	}
	if why, err = resolveOptionalText(cmd, "memory", "why-it-matters", flagWhy, "why-file"); err != nil {
		return text, tone, why, followup, caption, err
	}
	if followup, err = resolveOptionalText(cmd, "memory", "follow-up", flagFollowup, "followup-file"); err != nil {
		return text, tone, why, followup, caption, err
	}
	caption, err = resolveOptionalText(cmd, "memory", "caption", flagCaption, "caption-file")
	return text, tone, why, followup, caption, err
}

// runMemory executes `lucid memory`: resolve every free-text field, assemble the
// write request, optionally attach media, then persist the story.
func runMemory(cmd *cobra.Command, args []string) error {
	r, err := bootedRouter(cmd)
	if err != nil {
		return err
	}
	// Reject two prose fields both reading stdin before any read drains it.
	if err = ensureSingleStdinFlags(
		cmd, "body-file", "tone-file", "why-file", "followup-file", "caption-file",
	); err != nil {
		return emitErr(cmd, err)
	}
	text, tone, why, followup, caption, err := resolveMemoryText(cmd, args)
	if err != nil {
		return emitErr(cmd, err)
	}
	f := cmd.Flags()
	req := router.MemoryWriteRequest{
		Text: text,
		Now:  clockNow(),
	}
	req.Certainty, _ = f.GetString(flagCertainty)
	req.Era, _ = f.GetString(flagEra)
	req.Place, _ = f.GetString(flagPlace)
	req.People, _ = f.GetStringSlice(flagPeople)
	req.Tone = tone
	req.WhyItMatters = why
	req.FollowUp = followup
	req.Day, _ = f.GetString(flagDay)

	// Optional media reuses `lucid attach`: attach first, then reference
	// the returned raw entry id from refs.entry. A text-only story skips
	// this entirely — media never gates a story.
	if path, _ := f.GetString(flagAttach); strings.TrimSpace(path) != "" {
		if err = r.Store().ScaffoldMedia(); err != nil {
			return fmt.Errorf("lucid memory: %w", err)
		}
		ares, aerr := r.Attach(router.AttachRequest{
			Path:    path,
			Caption: caption,
			DayArg:  req.Day,
			Now:     req.Now,
		})
		if aerr != nil {
			return emitRefusedDay(cmd, aerr)
		}
		req.EntryRef = ares.RawID
	}

	res, err := r.WriteMemory(req)
	if err != nil {
		return emitRefusedDay(cmd, err)
	}
	return renderMemoryWrite(cmd, res)
}

// memoryAmendView is the machine-readable projection of a `lucid memory amend
// --json` turn: the appended amendment event id, the base story it corrects, the
// shared logical day, and the amendment's refs (corrects/era/cleared). Built
// CLI-side with stable snake_case names so a harness branches on fields, not
// prose.
type memoryAmendView struct {
	EventID     string         `json:"event_id"`
	TargetID    string         `json:"target_id"`
	LogicalDate string         `json:"logical_date"`
	Refs        map[string]any `json:"refs"`
}

// renderMemoryAmend prints an amend result: the --json view (refs always a
// non-nil object so a harness can index it), or the inventory ack prose.
func renderMemoryAmend(cmd *cobra.Command, res router.AmendMemoryResult) error {
	if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
		refs := res.Refs
		if refs == nil {
			refs = map[string]any{}
		}
		return writeJSON(cmd.OutOrStdout(), memoryAmendView{
			EventID:     res.EventID,
			TargetID:    res.TargetID,
			LogicalDate: res.LogicalDate,
			Refs:        refs,
		})
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
	return nil
}

// newMemoryAmendCmd wires `lucid memory amend <obs_id> [flags]`: the append-only
// correction of a stored story (mvp/data-model.md). It never rewrites the base
// event — it appends a new KindMemory event carrying only the changed fields
// (refs.corrects keyed to the target), so the original line stays byte-identical
// and its prior values remain recoverable. The stable `obs_…` id is kept, so any
// reference to the memory survives the amend. Body correction comes only via
// --body-file (no positional body, no --caption — a caption lives on the linked
// media entry, not the memory event). It is dispatch-only over
// [router.AmendMemory] — deterministic and agent-free.
func newMemoryAmendCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "amend <obs_id>",
		Short: "Correct or re-file a stored memory in place (append-only; the id is kept)",
		Long: `amend corrects or re-files a stored memory without losing its id. A memory is
one append-only event on a frozen envelope, so amend never edits the original
line — it appends a new event carrying only the fields you change, and every read
surface folds the current values on top of the original. The prior values stay on
disk as the audit trail.

Amend the story text (--body-file), how you recall it (--certainty:
vivid | hazy | reconstructed), the follow-up thread (--followup, or
--clear-followup to remove it), and the era it sits in (--era <key>, which must
already exist — mint it with 'lucid era create' first). Filing a memory under a
chapter minted after it was created is the point: re-file it any time.

A flag left unset leaves that field unchanged. Clearing is explicit: a bare
--followup "" is rejected as ambiguous — use --clear-followup. Running amend with
no field flags is a no-op error, and an unknown id changes nothing.`,
		Args: cobra.ExactArgs(1),
		Example: `  lucid memory amend obs_2010_07_15_001 --certainty vivid
  lucid memory amend obs_2010_07_15_001 --era era_wild-summer
  lucid memory amend obs_2010_07_15_001 --clear-followup --json`,
		RunE: runMemoryAmend,
	}
	f := cmd.Flags()
	f.String(flagEra, "", "Re-file the story under this era key (the chapter must already exist)")
	f.String(flagCertainty, "", "Correct how you recall it: vivid | hazy | reconstructed")
	f.String(flagFollowup, "", "Set the follow-up thread (use --clear-followup to remove it)")
	f.Bool(flagClearFollowup, false, "Clear the follow-up thread")
	registerBodyFileFlag(cmd, "corrected memory story text")
	return cmd
}

// runMemoryAmend executes `lucid memory amend`: gate each changed flag into the
// request (cobra Flags().Changed distinguishes omitted from set), resolve the
// sole body input off --body-file, then dispatch to the append-only amend. Every
// validation lives in the router so a rejected amend writes nothing; the CLI adds
// only the fast, friendly set-and-clear guard before booting.
func runMemoryAmend(cmd *cobra.Command, args []string) error {
	f := cmd.Flags()
	// Reject setting and clearing the follow-up in one call before touching the
	// ledger — the router rejects it too, but catching it here keeps the message
	// close to the flags the user typed.
	if f.Changed(flagFollowup) && f.Changed(flagClearFollowup) {
		return emitErr(cmd, fmt.Errorf("give --followup or --clear-followup, not both; nothing was saved"))
	}

	r, err := bootedRouter(cmd)
	if err != nil {
		return err
	}

	req := router.AmendMemoryRequest{
		ObsID: args[0],
		Now:   clockNow(),
	}
	if f.Changed(flagEra) {
		req.Era, _ = f.GetString(flagEra)
		req.EraChanged = true
	}
	if f.Changed(flagCertainty) {
		req.Certainty, _ = f.GetString(flagCertainty)
		req.CertaintyChanged = true
	}
	if f.Changed(flagFollowup) {
		req.Followup, _ = f.GetString(flagFollowup)
		req.FollowupChanged = true
	}
	if f.Changed(flagClearFollowup) {
		req.ClearFollowup, _ = f.GetBool(flagClearFollowup)
	}
	// The corrected body arrives only via --body-file (one file flag, so
	// ensureSingleStdinFlags is unnecessary); an omitted flag leaves the body
	// unchanged.
	if f.Changed("body-file") {
		body, berr := resolvePrimaryText(cmd, "memory amend", "body-file", "")
		if berr != nil {
			return emitErr(cmd, berr)
		}
		req.Body = body
		req.BodyChanged = true
	}

	res, err := r.AmendMemory(req)
	if err != nil {
		return emitErr(cmd, err)
	}
	return renderMemoryAmend(cmd, res)
}

// memoryChangeView is one field's transition in the `memory show --history --json`
// trail: the field, its prior value (prior_set false when it had none before this
// amendment), the new value (empty when cleared is true), and the amendment's
// recorded_at. Stable snake_case names so a harness branches on fields.
type memoryChangeView struct {
	Field      string `json:"field"`
	Prior      string `json:"prior"`
	PriorSet   bool   `json:"prior_set"`
	New        string `json:"new"`
	Cleared    bool   `json:"cleared"`
	RecordedAt string `json:"recorded_at"`
}

// memoryShowView is the machine-readable projection of a `lucid memory show
// --json` turn: the story's stable id, its logical day, and its current (folded)
// payload/refs. history is present only with --history and carries the ordered
// amendment trail. payload/refs are always non-nil objects so a harness can index
// them unconditionally.
type memoryShowView struct {
	EventID     string             `json:"event_id"`
	LogicalDate string             `json:"logical_date"`
	Payload     map[string]any     `json:"payload"`
	Refs        map[string]any     `json:"refs"`
	History     []memoryChangeView `json:"history,omitempty"`
}

// memoryShowField pairs a memory payload/ref key with its display label; the
// slice fixes the byte-stable render order of the folded story.
type memoryShowField struct {
	key   string
	label string
	ref   bool // true when the value lives in refs rather than payload
}

// memoryShowFieldOrder is the canonical render order for `memory show`: the story
// text first, then the recall/thread fields, then the relational refs. A field
// absent from the folded story is skipped, so a thin memory surfaces only what it
// holds.
var memoryShowFieldOrder = []memoryShowField{ //nolint:gochecknoglobals // the fixed memory-show render order
	{key: observations.MemoryFieldText, label: "Story"},
	{key: observations.MemoryFieldCertainty, label: "Certainty"},
	{key: observations.MemoryFieldTone, label: "Tone"},
	{key: observations.MemoryFieldWhyItMatters, label: "Why it matters"},
	{key: observations.MemoryFieldFollowUp, label: "Follow-up"},
	{key: observations.MemoryFieldPeople, label: "People"},
	{key: observations.RefEra, label: "Era", ref: true},
	{key: "place", label: "Place", ref: true},
	{key: "person", label: "Linked people", ref: true},
	{key: "entry", label: "Photo", ref: true},
}

// newMemoryShowCmd wires `lucid memory show <obs_id> [--history]`: the dedicated
// single-story read that folds amendments on top of the base memory
// (mvp/data-model.md, fold-on-read). By default it prints the story's current
// (folded) values; with --history it also prints the amendment trail (each
// amended field as original → amended @ recorded_at, a cleared field shown as
// "cleared"). --json emits the machine form. It is read-only and agent-free —
// folding is pure and nothing under ~/.lucid/ changes.
func newMemoryShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <obs_id>",
		Short: "Read one stored memory, folded to its current values (--history for the trail)",
		Long: `show reads one memory by its obs_… id and prints its current values with
every amendment folded on. The stored base line is never changed — show composes
the current view on read. With --history it also prints the amendment trail: each
amended field as "original → amended @ <recorded_at>", and a cleared field as
"cleared". An unknown id, or an id that names an amendment rather than a base
memory, errors cleanly and prints nothing.`,
		Args: cobra.ExactArgs(1),
		Example: `  lucid memory show obs_2010_07_15_001
  lucid memory show obs_2010_07_15_001 --history
  lucid memory show obs_2010_07_15_001 --json`,
		RunE: runMemoryShow,
	}
	cmd.Flags().Bool(flagHistory, false, "Also print the amendment trail (original → amended @ timestamp)")
	return cmd
}

// runMemoryShow executes `lucid memory show`: resolve the folded story (plus its
// history), then render the current values and, with --history, the trail.
func runMemoryShow(cmd *cobra.Command, args []string) error {
	r, err := bootedRouter(cmd)
	if err != nil {
		return err
	}
	res, err := r.ShowMemory(args[0])
	if err != nil {
		return emitErr(cmd, err)
	}
	withHistory, _ := cmd.Flags().GetBool(flagHistory)
	return renderMemoryShow(cmd, res, withHistory)
}

// renderMemoryShow prints a folded story: the --json view (payload/refs always a
// non-nil object; history only with --history), or human-first text — a heading
// with the id, the present fields as bullets in canonical order, and, with
// --history, the amendment trail.
func renderMemoryShow(cmd *cobra.Command, res router.ShowMemoryResult, withHistory bool) error {
	ev := res.Memory
	if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
		view := memoryShowView{
			EventID:     ev.ID,
			LogicalDate: ev.LogicalDate,
			Payload:     nonNilMap(ev.Payload),
			Refs:        nonNilMap(ev.Refs),
		}
		if withHistory {
			view.History = memoryChangeViews(res.History)
		}
		return writeJSON(cmd.OutOrStdout(), view)
	}

	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "Memory %s (%s)\n", ev.ID, ev.LogicalDate)
	for _, f := range memoryShowFieldOrder {
		src := ev.Payload
		if f.ref {
			src = ev.Refs
		}
		if v := memoryShowValue(src[f.key]); v != "" {
			_, _ = fmt.Fprintf(out, "• %s: %s\n", f.label, v)
		}
	}
	if withHistory {
		renderMemoryHistory(out, res.History)
	}
	return nil
}

// renderMemoryHistory prints the amendment trail beneath the folded story: one
// bullet per amended field, in the fold's chronological order, as
// "original → amended @ recorded_at". A never-amended story says so honestly.
func renderMemoryHistory(out io.Writer, trail []observations.MemoryFieldChange) {
	if len(trail) == 0 {
		_, _ = fmt.Fprintln(out, "History: none — this is the original memory.")
		return
	}
	_, _ = fmt.Fprintln(out, "History:")
	for _, c := range trail {
		_, _ = fmt.Fprintf(out, "• %s: %s\n", memoryFieldLabel(c.Field), memoryChangeLine(c))
	}
}

// memoryChangeLine renders one field transition as "prior → new @ recorded_at":
// an unset prior shows as "(unset)", and a cleared field shows its new value as
// "(cleared)".
func memoryChangeLine(c observations.MemoryFieldChange) string {
	prior := c.Prior
	if !c.PriorSet {
		prior = "(unset)"
	}
	after := c.New
	if c.Cleared {
		after = "(cleared)"
	}
	return fmt.Sprintf("%s → %s @ %s", prior, after, c.RecordedAt)
}

// memoryChangeViews projects the history trail into its --json shape.
func memoryChangeViews(trail []observations.MemoryFieldChange) []memoryChangeView {
	out := make([]memoryChangeView, 0, len(trail))
	for _, c := range trail {
		out = append(out, memoryChangeView{
			Field:      c.Field,
			Prior:      c.Prior,
			PriorSet:   c.PriorSet,
			New:        c.New,
			Cleared:    c.Cleared,
			RecordedAt: c.RecordedAt,
		})
	}
	return out
}

// memoryFieldLabel maps a memory field key (text/certainty/follow_up/era) to its
// display label, falling back to the raw key for any future field.
func memoryFieldLabel(field string) string {
	switch field {
	case observations.MemoryFieldText:
		return "Story"
	case observations.MemoryFieldCertainty:
		return "Certainty"
	case observations.MemoryFieldFollowUp:
		return "Follow-up"
	case observations.RefEra:
		return "Era"
	default:
		return field
	}
}

// memoryShowValue renders a folded payload/ref value as its display string: a
// string verbatim, a list (as read back from JSON, []any of strings — or []string
// in-memory) joined with ", ", anything else via fmt. Blank list entries drop.
func memoryShowValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(t)
	case []any:
		var parts []string
		for _, e := range t {
			if s, ok := e.(string); ok {
				if s = strings.TrimSpace(s); s != "" {
					parts = append(parts, s)
				}
			}
		}
		return strings.Join(parts, ", ")
	case []string:
		var parts []string
		for _, s := range t {
			if s = strings.TrimSpace(s); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	default:
		return fmt.Sprintf("%v", v)
	}
}

// nonNilMap returns m, or an empty map when m is nil, so a --json projection
// always emits an object rather than null.
func nonNilMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
