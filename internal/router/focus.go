package router

import (
	"fmt"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/focus"
)

// AddFocusRequest is one `lucid focus add` turn (focus.md §3): the required
// work-on text, an optional success criterion, optional obs-parity #tags, the
// strict-tier --day value, and now (injected so backdating and ids are
// deterministic in tests).
type AddFocusRequest struct {
	Text             string
	SuccessCriterion string
	Tags             []string
	DayArg           string
	Now              time.Time
}

// AddFocusResult reports the appended entry and its inventory ack — "added"
// plus the receipt id, zero evaluative language (focus.md §0, §3).
type AddFocusResult struct {
	Focus focus.Focus
	Ack   string
}

// ListFocusRequest selects the list view: active-only by default, or the full
// audit view (active + retired) when IncludeRetired is set (focus.md §5). It is
// the read-side of the active/retired state — the default hides retired items,
// the audit view shows them.
type ListFocusRequest struct {
	IncludeRetired bool
}

// ListFocusView is the `lucid focus list --json` payload: the folded item pool
// (retirement markers removed, state resolved) and its count. Focus is never
// null so automation always reads an array; the JSON field order matches the
// read verbs' convention (ADR-0007).
type ListFocusView struct {
	Count int           `json:"count"`
	Focus []focus.Focus `json:"focus"`
}

// ListFocusResult carries the machine view and the human-first lines the CLI
// shares through emit (ADR-0007).
type ListFocusResult struct {
	View  ListFocusView
	Lines []string
}

// SurfaceFocusView is the `lucid focus surface --json` payload: the single
// active pick for the logical day, or a null focus when the active pool is
// empty (focus.md §4, §5). Day is the logical-day key the pick was recorded
// under.
type SurfaceFocusView struct {
	Day   string       `json:"day"`
	Focus *focus.Focus `json:"focus"`
}

// SurfaceFocusResult carries the machine view and the human-first line.
type SurfaceFocusResult struct {
	View  SurfaceFocusView
	Lines []string
}

// RetireFocusRequest is one `lucid focus retire <id>` turn (focus.md §2): the id
// of the focus item to retire, and now (injected so the retirement event's
// logical day and timestamp are deterministic in tests).
type RetireFocusRequest struct {
	ID  string
	Now time.Time
}

// RetireFocusResult reports the appended retirement event and its ack. The
// event is a new append that references the target id — nothing is deleted, so
// the retired item stays on the record for the audit view (focus.md §2).
type RetireFocusResult struct {
	Focus focus.Focus
	Ack   string
}

// AddFocus appends one immutable focus item to the Ledger (focus.md §2, §3). It
// is deterministic and agent-free — no LLM in the path (architecture P9). An
// empty text writes nothing (a clean usage error), and a backdating `--day`
// runs on the strict tier: an unreadable token or a future day is a
// [DayRejectedError] and nothing is written (error-states.md §St-1), exactly as
// the observation micro-log's `--day` behaves. `recorded_at` is always the real
// write time; only `logical_date` follows `--day`. The optional success
// criterion is stored verbatim; it is never synthesized.
func (r *Router) AddFocus(req AddFocusRequest) (AddFocusResult, error) {
	if strings.TrimSpace(req.Text) == "" {
		return AddFocusResult{}, fmt.Errorf("a focus needs some text; nothing was saved")
	}
	if err := r.prepareFocus(); err != nil {
		return AddFocusResult{}, err
	}

	// Strict-tier `--day`: reuse the one shared capture grammar so the accepted
	// forms, the 04:00 rollover, and the future ceiling match every other dated
	// capture. A refusal is a DayRejectedError the CLI prints; nothing lands.
	when, err := resolveCaptureWhen(req.DayArg, whenOr(req.Now))
	if err != nil {
		return AddFocusResult{}, err
	}

	f := focus.Focus{
		Schema:           focus.Schema,
		Text:             req.Text,
		SuccessCriterion: req.SuccessCriterion,
		State:            focus.StateActive,
		RecordedAt:       whenOr(req.Now).Format(time.RFC3339),
		LogicalDate:      when.LogicalDate,
		Source:           focus.SourceFocus,
		Tags:             req.Tags,
	}
	stored, err := r.store.AppendFocus(f)
	if err != nil {
		return AddFocusResult{}, fmt.Errorf("could not add the focus; nothing was saved: %w", err)
	}
	return AddFocusResult{
		Focus: stored,
		Ack:   fmt.Sprintf("Added focus as `%s`.", stored.ID),
	}, nil
}

// ListFocus reads the focus pool sorted by id with state folded (focus.md §5) —
// active items only by default, the full audit view (active + retired) when the
// request sets IncludeRetired. It writes nothing.
func (r *Router) ListFocus(req ListFocusRequest) (ListFocusResult, error) {
	entries, err := r.store.ReadFocus()
	if err != nil {
		return ListFocusResult{}, fmt.Errorf("could not read the focus items: %w", err)
	}
	if !req.IncludeRetired {
		entries = activeFocusItems(entries)
	}
	if entries == nil {
		entries = []focus.Focus{}
	}
	return ListFocusResult{
		View:  ListFocusView{Count: len(entries), Focus: entries},
		Lines: focusListLines(entries, req.IncludeRetired),
	}, nil
}

// SurfaceFocus returns exactly one active focus item for the logical day that
// now falls on and records that it was shown (focus.md §4). It is idempotent
// within the day — a repeated call the same logical day returns the same pick
// without advancing — and rotates least-recently-surfaced across days. Retired
// items are excluded from selection. The only write is the surface-state
// projection; no entry is touched. An empty active pool is a clean "nothing to
// surface," never an error.
func (r *Router) SurfaceFocus(now time.Time) (SurfaceFocusResult, error) {
	dayKey := focus.LogicalDay(whenOr(now))
	pick, ok, err := r.store.SurfaceFocus(dayKey)
	if err != nil {
		return SurfaceFocusResult{}, err
	}
	view := SurfaceFocusView{Day: dayKey}
	if !ok {
		return SurfaceFocusResult{
			View:  view,
			Lines: []string{"No focus items to surface yet — add one with `lucid focus add <text>`."},
		}, nil
	}
	picked := pick
	view.Focus = &picked
	return SurfaceFocusResult{
		View:  view,
		Lines: []string{focusSurfaceLine(pick)},
	}, nil
}

// RetireFocus retires an active focus item by appending a retirement event that
// references it by id (focus.md §2) — the item is never deleted, so history is
// preserved and `list --all` still shows it. An empty id is a clean usage error;
// an id that names no focus item, or one already retired, is rejected by the
// storage adapter and nothing is appended. `recorded_at` and the retirement
// event's logical day are resolved from now.
func (r *Router) RetireFocus(req RetireFocusRequest) (RetireFocusResult, error) {
	if strings.TrimSpace(req.ID) == "" {
		return RetireFocusResult{}, fmt.Errorf("a focus id is required to retire; nothing was changed")
	}
	if err := r.prepareFocus(); err != nil {
		return RetireFocusResult{}, err
	}
	now := whenOr(req.Now)
	marker, err := r.store.RetireFocus(req.ID, focus.LogicalDay(now), now.Format(time.RFC3339))
	if err != nil {
		return RetireFocusResult{}, err
	}
	return RetireFocusResult{
		Focus: marker,
		Ack:   fmt.Sprintf("Retired `%s`.", req.ID),
	}, nil
}

// prepareFocus scaffolds the focus tree idempotently, wrapping any failure with
// the shared message the focus verbs report (mirrors [Router.prepareReframes]).
func (r *Router) prepareFocus() error {
	if err := r.store.ScaffoldFocus(); err != nil {
		return fmt.Errorf("could not prepare the focus tree: %w", err)
	}
	return nil
}

// activeFocusItems returns only the active items from a folded pool, preserving
// order (state has already been resolved by the storage read). It always returns
// a non-nil slice so the default list marshals as [] rather than null.
func activeFocusItems(in []focus.Focus) []focus.Focus {
	out := make([]focus.Focus, 0, len(in))
	for _, f := range in {
		if f.State == focus.StateActive {
			out = append(out, f)
		}
	}
	return out
}

// focusListLines renders the human-first list: a count header then one line per
// focus item, `<id>  <text>`, with a `(retired)` marker when the audit view is
// showing retired items and a trailing success criterion when one is set. An
// empty pool prints the add hint, so the read is never a bare blank.
func focusListLines(entries []focus.Focus, includeRetired bool) []string {
	if len(entries) == 0 {
		if includeRetired {
			return []string{"No focus items yet — add one with `lucid focus add <text>`."}
		}
		return []string{"No active focus items — add one with `lucid focus add <text>`."}
	}
	lines := make([]string, 0, len(entries)+1)
	lines = append(lines, fmt.Sprintf("%d focus item(s):", len(entries)))
	for _, f := range entries {
		line := fmt.Sprintf("  %s  %s", f.ID, f.Text)
		if f.State == focus.StateRetired {
			line += " (retired)"
		}
		if f.SuccessCriterion != "" {
			line += fmt.Sprintf("  — Success: %s", f.SuccessCriterion)
		}
		lines = append(lines, line)
	}
	return lines
}

// focusSurfaceLine renders the one-per-day surface line: the work-on text, plus
// its success criterion when one is set, so the morning surface reads as one
// clean line.
func focusSurfaceLine(f focus.Focus) string {
	if f.SuccessCriterion != "" {
		return fmt.Sprintf("%s — Success: %s", f.Text, f.SuccessCriterion)
	}
	return f.Text
}
