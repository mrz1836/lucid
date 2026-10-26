package router

import (
	"fmt"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/rep"
)

// AddRepRequest is one `lucid rep add` turn (reps.md §3): the required rep text,
// an optional solo variant, an optional level (0 = unlevelled), optional
// obs-parity #tags, the strict-tier --day value, and now (injected so
// backdating and ids are deterministic in tests).
type AddRepRequest struct {
	Text   string
	Solo   string
	Level  int
	Tags   []string
	DayArg string
	Now    time.Time
}

// AddRepResult reports the appended entry and its inventory ack — "added" plus
// the receipt id, zero evaluative language (reps.md §0, §3).
type AddRepResult struct {
	Rep rep.Rep
	Ack string
}

// ListRepsRequest selects the list view: active-only by default, or the full
// audit view (active + retired) when IncludeRetired is set (reps.md §5, §6).
type ListRepsRequest struct {
	IncludeRetired bool
}

// ListRepsView is the `lucid rep list --json` payload: the folded rep pool
// (retirement markers removed, state resolved) and its count. Reps is never
// null so automation always reads an array; the JSON field order matches the
// read verbs' convention (ADR-0007).
type ListRepsView struct {
	Count int       `json:"count"`
	Reps  []rep.Rep `json:"reps"`
}

// ListRepsResult carries the machine view and the human-first lines the CLI
// shares through emit (ADR-0007).
type ListRepsResult struct {
	View  ListRepsView
	Lines []string
}

// SurfaceRepView is the `lucid rep surface --json` payload: the single active
// pick for the logical day — the full entry, solo variant included — or a null
// rep when the active pool is empty (reps.md §4, §6). Day is the logical-day key
// the pick was recorded under. It carries exactly these two keys: no solo-day
// flag, no capacity field, and no done/not-done field (reps.md §0).
type SurfaceRepView struct {
	Day string   `json:"day"`
	Rep *rep.Rep `json:"rep"`
}

// SurfaceRepResult carries the machine view and the human-first line.
type SurfaceRepResult struct {
	View  SurfaceRepView
	Lines []string
}

// RetireRepRequest is one `lucid rep retire <id>` turn (reps.md §5): the id of
// the rep to retire, and now (injected so the retirement event's logical day
// and timestamp are deterministic in tests).
type RetireRepRequest struct {
	ID  string
	Now time.Time
}

// RetireRepResult reports the appended retirement event and its ack. The event
// is a new append that references the target id — nothing is deleted, so the
// retired rep stays on the record for the audit view (reps.md §5).
type RetireRepResult struct {
	Rep rep.Rep
	Ack string
}

// AddRep appends one immutable practice rep to the Ledger (reps.md §2, §3). It is
// deterministic and agent-free — no LLM in the path (architecture P9). An empty
// text writes nothing (a clean usage error), as does a negative level; a level
// of 0 stores the rep unlevelled (the CLI additionally refuses an explicitly
// given level below 1). A backdating `--day` runs on the strict tier: an
// unreadable token or a future day is a [DayRejectedError] and nothing is
// written (error-states.md §St-1). `recorded_at` is always the real write time;
// only `logical_date` follows `--day`. The solo variant is stored verbatim; it
// is never synthesized.
func (r *Router) AddRep(req AddRepRequest) (AddRepResult, error) {
	if strings.TrimSpace(req.Text) == "" {
		return AddRepResult{}, fmt.Errorf("a rep needs some text; nothing was saved")
	}
	if req.Level < 0 {
		return AddRepResult{}, fmt.Errorf("a rep level must be a positive whole number; nothing was saved")
	}
	if err := r.prepareReps(); err != nil {
		return AddRepResult{}, err
	}

	// Strict-tier `--day`: reuse the one shared capture grammar so the accepted
	// forms, the 04:00 rollover, and the future ceiling match every other dated
	// capture. A refusal is a DayRejectedError the CLI prints; nothing lands.
	when, err := resolveCaptureWhen(req.DayArg, whenOr(req.Now))
	if err != nil {
		return AddRepResult{}, err
	}

	entry := rep.Rep{
		Schema:      rep.Schema,
		Text:        req.Text,
		Solo:        req.Solo,
		Level:       req.Level,
		State:       rep.StateActive,
		RecordedAt:  whenOr(req.Now).Format(time.RFC3339),
		LogicalDate: when.LogicalDate,
		Source:      rep.SourceRep,
		Tags:        req.Tags,
	}
	stored, err := r.store.AppendRep(entry)
	if err != nil {
		return AddRepResult{}, fmt.Errorf("could not add the rep; nothing was saved: %w", err)
	}
	return AddRepResult{
		Rep: stored,
		Ack: fmt.Sprintf("Added rep as `%s`.", stored.ID),
	}, nil
}

// ListReps reads the rep pool sorted by id with state folded (reps.md §5, §6) —
// active reps only by default, the full audit view (active + retired) when the
// request sets IncludeRetired. It writes nothing.
func (r *Router) ListReps(req ListRepsRequest) (ListRepsResult, error) {
	entries, err := r.store.ReadReps()
	if err != nil {
		return ListRepsResult{}, fmt.Errorf("could not read the reps: %w", err)
	}
	if !req.IncludeRetired {
		entries = activeReps(entries)
	}
	if entries == nil {
		entries = []rep.Rep{}
	}
	return ListRepsResult{
		View:  ListRepsView{Count: len(entries), Reps: entries},
		Lines: repListLines(entries, req.IncludeRetired),
	}, nil
}

// SurfaceRep returns exactly one active rep for the logical day that now falls
// on, together with its solo variant, and records that it was shown (reps.md
// §4). It is idempotent within the day — a repeated call the same logical day
// returns the same pick without advancing — and rotates least-recently-surfaced
// across days. Retired reps are excluded from selection. It takes no solo-day or
// capacity input and never asks whether a rep was done (reps.md §0). The only
// write is the surface-state projection; no entry is touched. An empty active
// pool is a clean "nothing to surface," never an error.
func (r *Router) SurfaceRep(now time.Time) (SurfaceRepResult, error) {
	dayKey := rep.LogicalDay(whenOr(now))
	pick, ok, err := r.store.SurfaceRep(dayKey)
	if err != nil {
		return SurfaceRepResult{}, err
	}
	view := SurfaceRepView{Day: dayKey}
	if !ok {
		return SurfaceRepResult{
			View:  view,
			Lines: []string{"No reps to surface yet — add one with `lucid rep add <text>`."},
		}, nil
	}
	picked := pick
	view.Rep = &picked
	return SurfaceRepResult{
		View:  view,
		Lines: []string{repSurfaceLine(pick)},
	}, nil
}

// RetireRep retires an active rep by appending a retirement event that
// references it by id (reps.md §5) — the rep is never deleted, so history is
// preserved and `list --all` still shows it. An empty id is a clean usage error;
// an id that names no rep, or one already retired, is rejected by the storage
// adapter and nothing is appended. `recorded_at` and the retirement event's
// logical day are resolved from now.
func (r *Router) RetireRep(req RetireRepRequest) (RetireRepResult, error) {
	if strings.TrimSpace(req.ID) == "" {
		return RetireRepResult{}, fmt.Errorf("a rep id is required to retire; nothing was changed")
	}
	if err := r.prepareReps(); err != nil {
		return RetireRepResult{}, err
	}
	now := whenOr(req.Now)
	marker, err := r.store.RetireRep(req.ID, rep.LogicalDay(now), now.Format(time.RFC3339))
	if err != nil {
		return RetireRepResult{}, err
	}
	return RetireRepResult{
		Rep: marker,
		Ack: fmt.Sprintf("Retired `%s`.", req.ID),
	}, nil
}

// prepareReps scaffolds the reps tree idempotently, wrapping any failure with the
// shared message the rep verbs report (mirrors [Router.prepareFocus]).
func (r *Router) prepareReps() error {
	if err := r.store.ScaffoldReps(); err != nil {
		return fmt.Errorf("could not prepare the reps tree: %w", err)
	}
	return nil
}

// activeReps returns only the active reps from a folded pool, preserving order
// (state has already been resolved by the storage read). It always returns a
// non-nil slice so the default list marshals as [] rather than null.
func activeReps(in []rep.Rep) []rep.Rep {
	out := make([]rep.Rep, 0, len(in))
	for _, e := range in {
		if e.State == rep.StateActive {
			out = append(out, e)
		}
	}
	return out
}

// repListLines renders the human-first list: a count header then one line per
// rep, `<id>  <text>`, with its level when set, a `(retired)` marker when the
// audit view is showing retired reps, and a trailing solo variant when one is
// set. An empty pool prints the add hint, so the read is never a bare blank.
func repListLines(entries []rep.Rep, includeRetired bool) []string {
	if len(entries) == 0 {
		if includeRetired {
			return []string{"No reps yet — add one with `lucid rep add <text>`."}
		}
		return []string{"No active reps — add one with `lucid rep add <text>`."}
	}
	lines := make([]string, 0, len(entries)+1)
	lines = append(lines, fmt.Sprintf("%d rep(s):", len(entries)))
	for _, e := range entries {
		line := fmt.Sprintf("  %s  %s", e.ID, e.Text)
		if e.Level > 0 {
			line += fmt.Sprintf(" (level %d)", e.Level)
		}
		if e.State == rep.StateRetired {
			line += " (retired)"
		}
		if e.Solo != "" {
			line += fmt.Sprintf("  — Solo: %s", e.Solo)
		}
		lines = append(lines, line)
	}
	return lines
}

// repSurfaceLine renders the one-per-day surface line, `<text> (level N) — Solo:
// <solo>`, omitting the level when the rep is unlevelled and the solo segment
// when it has no solo variant, so the morning surface reads as one clean line.
func repSurfaceLine(e rep.Rep) string {
	line := e.Text
	if e.Level > 0 {
		line += fmt.Sprintf(" (level %d)", e.Level)
	}
	if e.Solo != "" {
		line += fmt.Sprintf(" — Solo: %s", e.Solo)
	}
	return line
}
