package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/router"
)

// The window flags and the redo override. `--since` opens an inclusive
// civil-date window — passing it is what selects ranged mode — `--until`
// closes it, and `--force` re-structures an entry that already has an
// artifact.
const (
	structureFlagSince = "since"
	structureFlagUntil = "until"
	structureFlagForce = "force"
)

// structureDateLayout is the civil-date form the window flags speak, named in
// the parse error so a rejected value says what shape it wanted.
const structureDateLayout = "YYYY-MM-DD"

// The two --json mode labels, so a harness reads which form ran rather than
// inferring it from whether the window fields are populated.
const (
	structureModeEntry = "entry"
	structureModeRange = "range"
)

// structureEmpty is the calm copy printed when a window holds no raw entry at
// all — there was nothing to distill, which is not the same as a run that
// failed.
const structureEmpty = "Nothing to structure in that window."

// structureEntryView is one entry's line under --json: the raw entry id and
// the single most informative label for what became of it (wrote | skipped |
// degraded | failed).
type structureEntryView struct {
	RawID   string `json:"raw_id"`
	Outcome string `json:"outcome"`
}

// structureView is the --json projection of a [router.StructurePassResult],
// built CLI-side with stable snake_case names so a harness branches on the
// counts rather than parsing the summary line.
//
// written and degraded overlap by design: a degraded extraction still produces
// an artifact, so an entry can move both counters. The per-entry outcome
// resolves that to one label instead, so the two answer different questions —
// how much landed, and what happened to a given entry.
type structureView struct {
	Mode      string               `json:"mode"`
	Since     string               `json:"since"`
	Until     string               `json:"until"`
	Force     bool                 `json:"force"`
	Attempted int                  `json:"attempted"`
	Written   int                  `json:"written"`
	Skipped   int                  `json:"skipped"`
	Degraded  int                  `json:"degraded"`
	Failed    int                  `json:"failed"`
	Entries   []structureEntryView `json:"entries"`
}

// newStructureCmd wires `lucid structure`: the standalone Structuring pass over
// raw entries this run did not capture (agent-contracts.md §"How contracts
// compose"). It is provider-backed (the lucid.json `provider` block) and runs
// Structuring alone — it never proposes a pattern, which stays inside a
// check-in session, and it never modifies the raw entry it read.
//
// Mode is decided by arg-vs-flag: a positional raw entry id structures that one
// entry, `--since` structures an inclusive civil-date window whatever command
// captured each entry. An entry that already has a processed artifact is
// skipped without a model call unless `--force` is passed, which is what makes
// an interrupted window run resumable and a repeat run over a settled window
// free.
func newStructureCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "structure [raw_id]",
		Short: "Structure a raw entry you did not just capture, or a window of them",
		Long: `structure runs the Structuring pass over raw entries this run did not capture,
writing each one's processed artifact. The pass otherwise runs only as a side
effect of a guided check-in, so an entry captured any other way has no route to
a processed artifact.

A positional raw entry id structures that one entry. --since structures every
raw entry in an inclusive civil-date window — whatever command captured it —
with --until defaulting to today. An entry that already has a processed
artifact is skipped without a model call unless --force is passed, so an
interrupted window run resumes for free.

Every run reports attempted / written / skipped / degraded / failed. In ranged
mode one progress line per entry goes to stderr while stdout carries only the
summary, so --json output stays parseable.`,
		Example: `  # Structure one raw entry.
  lucid structure raw_2026_01_14_09_30

  # Re-structure it, overwriting the existing artifact.
  lucid structure raw_2026_01_14_09_30 --force

  # Structure every raw entry from a day through today.
  lucid structure --since 2026-01-01

  # Structure a closed window, machine-readable.
  lucid structure --since 2026-01-01 --until 2026-01-07 --json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return emitErr(cmd, runStructure(cmd, args))
		},
	}
	cmd.Flags().String(structureFlagSince, "",
		"Structure every raw entry from this "+structureDateLayout+" day onward (inclusive)")
	cmd.Flags().String(structureFlagUntil, "",
		"Close the window on this "+structureDateLayout+" day (inclusive; defaults to today)")
	cmd.Flags().Bool(structureFlagForce, false,
		"Re-structure an entry that already has a processed artifact")
	return cmd
}

// runStructure resolves the mode, boots the router, and runs the pass. The
// window is resolved before the router boots so a malformed range is rejected
// on its own terms, rather than after a scaffold and a provider build.
func runStructure(cmd *cobra.Command, args []string) error {
	now := clockNow()
	req, err := structureRequest(cmd, args, now)
	if err != nil {
		return err
	}
	r, err := bootedRouter(cmd)
	if err != nil {
		return err
	}
	p, err := buildProvider(r.Config().Provider)
	if err != nil {
		return err
	}
	req.Now = now
	req.Provider = p
	// Only a window shows progress: single-entry mode's summary is already the
	// whole output, so a progress line would just repeat it.
	if req.RawID == "" {
		req.Progress = structureProgress(cmd)
	}
	res, err := r.StructurePass(cmd.Context(), req)
	if err != nil {
		return err
	}
	return renderStructure(cmd, res, req.Force)
}

// structureRequest maps the positional arg and the window flags onto a pass
// request, rejecting every ambiguous combination.
//
// The rejections are phrased in cobra's own invalid-argument dialect so misuse
// maps to [ExitUsage] rather than reading like a failed run — the same
// treatment `reflect week --since` gives a bad date.
func structureRequest(cmd *cobra.Command, args []string, now time.Time) (router.StructurePassRequest, error) {
	flags := cmd.Flags()
	force, _ := flags.GetBool(structureFlagForce)
	hasArg := len(args) > 0
	hasSince, hasUntil := flags.Changed(structureFlagSince), flags.Changed(structureFlagUntil)

	switch {
	case hasArg && hasSince:
		return router.StructurePassRequest{}, fmt.Errorf(
			"invalid argument %q for %q flag: one entry and a window are separate modes — pass one or the other",
			args[0], "--"+structureFlagSince,
		)
	case hasUntil && !hasSince:
		raw, _ := flags.GetString(structureFlagUntil)
		return router.StructurePassRequest{}, fmt.Errorf(
			"invalid argument %q for %q flag: it closes a window opened by --%s, which was not given",
			raw, "--"+structureFlagUntil, structureFlagSince,
		)
	case !hasArg && !hasSince:
		return router.StructurePassRequest{}, fmt.Errorf(
			"requires at least a raw entry id or --%s <%s>", structureFlagSince, structureDateLayout,
		)
	case hasArg:
		id := strings.TrimSpace(args[0])
		if id == "" {
			return router.StructurePassRequest{}, fmt.Errorf("invalid argument %q: needs a raw entry id", args[0])
		}
		return router.StructurePassRequest{RawID: id, Force: force}, nil
	}
	return structureWindow(cmd, now, force)
}

// structureWindow resolves the inclusive civil-date bounds of a ranged pass.
// Both ends parse through the shared date parser in the clock's own location,
// so a bound means the day the user meant; an omitted --until closes the window
// on today.
func structureWindow(cmd *cobra.Command, now time.Time, force bool) (router.StructurePassRequest, error) {
	since, err := structureDay(cmd, structureFlagSince, now.Location())
	if err != nil {
		return router.StructurePassRequest{}, err
	}
	until := observations.DateOf(now)
	if cmd.Flags().Changed(structureFlagUntil) {
		if until, err = structureDay(cmd, structureFlagUntil, now.Location()); err != nil {
			return router.StructurePassRequest{}, err
		}
	}
	if since.After(until) {
		return router.StructurePassRequest{}, fmt.Errorf(
			"invalid argument %q for %q flag: it starts after --%s (%s)",
			observations.DateString(since), "--"+structureFlagSince,
			structureFlagUntil, observations.DateString(until),
		)
	}
	return router.StructurePassRequest{Since: since, Until: until, Force: force}, nil
}

// structureDay parses one civil-date flag, naming the shape it wanted when the
// value does not parse.
func structureDay(cmd *cobra.Command, name string, loc *time.Location) (time.Time, error) {
	raw, _ := cmd.Flags().GetString(name)
	day, err := observations.ParseDate(strings.TrimSpace(raw), loc)
	if err != nil {
		return time.Time{}, fmt.Errorf(
			"invalid argument %q for %q flag: needs a %s date", raw, "--"+name, structureDateLayout,
		)
	}
	return day, nil
}

// structureProgress builds the per-entry progress callback. It writes to
// stderr, never stdout, so a wide window shows forward motion without putting
// anything ahead of the --json object a harness is parsing.
func structureProgress(cmd *cobra.Command) func(done, total int, rawID, outcome string) {
	errOut := cmd.ErrOrStderr()
	return func(done, total int, rawID, outcome string) {
		_, _ = fmt.Fprintf(errOut, "[%d/%d] %s → %s\n", done, total, rawID, outcome)
	}
}

// renderStructure prints what the pass did: the --json view, or Discord-friendly
// prose (no markdown tables — a summary line and, for a single entry, one short
// line saying what became of it). A window with nothing in it prints the calm
// fallback.
func renderStructure(cmd *cobra.Command, res router.StructurePassResult, force bool) error {
	if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
		if err := writeJSON(cmd.OutOrStdout(), structureViewOf(res, force)); err != nil {
			return err
		}
		return structureFailure(res)
	}

	out := cmd.OutOrStdout()
	switch {
	case len(res.Entries) == 0:
		_, _ = fmt.Fprintln(out, structureEmpty)
	case res.Ranged:
		_, _ = fmt.Fprintln(out, structureSummary(res))
	default:
		_, _ = fmt.Fprintln(out, structureEntryLine(res))
		_, _ = fmt.Fprintln(out, structureSummary(res))
	}
	return structureFailure(res)
}

// structureEntryLine is single-entry mode's short form: what became of the one
// entry, in the plainest words that stay true. A skip names the flag that
// overrides it, so the honest "nothing happened" answer also says how to make
// something happen.
func structureEntryLine(res router.StructurePassResult) string {
	e := res.Entries[0]
	switch e.Outcome {
	case router.StructureOutcomeSkipped:
		return e.RawID + " → already structured (pass --" + structureFlagForce + " to redo)"
	case router.StructureOutcomeDegraded:
		if res.Wrote > 0 {
			return e.RawID + " → written from a degraded extraction"
		}
		return e.RawID + " → degraded; nothing written"
	case router.StructureOutcomeFailed:
		return e.RawID + " → not structured"
	default:
		return e.RawID + " → written"
	}
}

// structureSummary is the one-line count report both modes print.
func structureSummary(res router.StructurePassResult) string {
	total := len(res.Entries)
	return fmt.Sprintf("Structured %d of %d %s — %d written, %d skipped, %d degraded, %d failed.",
		res.Attempted, total, structureEntryNoun(total),
		res.Wrote, res.Skipped, res.Degraded, res.Failed)
}

// structureEntryNoun picks the singular or plural noun for a count.
func structureEntryNoun(n int) string {
	if n == 1 {
		return "entry"
	}
	return "entries"
}

// structureFailure maps a pass that could not structure an entry onto a
// non-zero exit, while a pass that only skipped stays a success — skipping is
// the resumable default, not a shortfall. The message deliberately avoids
// cobra's usage vocabulary so a real failure exits [ExitErr] rather than being
// read as misuse.
func structureFailure(res router.StructurePassResult) error {
	if res.Failed == 0 {
		return nil
	}
	return fmt.Errorf("lucid structure: %d of %d %s could not be structured",
		res.Failed, len(res.Entries), structureEntryNoun(len(res.Entries)))
}

// structureViewOf projects a pass result into the stable --json shape. The
// entries array is normalized to [] rather than null so a harness can index it
// unconditionally.
func structureViewOf(res router.StructurePassResult, force bool) structureView {
	entries := make([]structureEntryView, 0, len(res.Entries))
	for _, e := range res.Entries {
		entries = append(entries, structureEntryView{RawID: e.RawID, Outcome: e.Outcome})
	}
	mode := structureModeEntry
	if res.Ranged {
		mode = structureModeRange
	}
	return structureView{
		Mode:      mode,
		Since:     res.Since,
		Until:     res.Until,
		Force:     force,
		Attempted: res.Attempted,
		Written:   res.Wrote,
		Skipped:   res.Skipped,
		Degraded:  res.Degraded,
		Failed:    res.Failed,
		Entries:   entries,
	}
}
