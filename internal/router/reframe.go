package router

import (
	"fmt"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/reframes"
)

// AddReframeRequest is one `lucid reframe add` turn (reframes.md §3): the
// required catch→flip pair, optional obs-parity #tags, the strict-tier --day
// value, and now (injected so backdating and ids are deterministic in tests).
type AddReframeRequest struct {
	Catch  string
	Flip   string
	Tags   []string
	DayArg string
	Now    time.Time
}

// AddReframeResult reports the appended entry and its inventory ack — "added"
// plus the receipt id, zero evaluative language (reframes.md §0, §3).
type AddReframeResult struct {
	Reframe reframes.Reframe
	Ack     string
}

// ListReframesView is the `lucid reframe list --json` payload: the live pool
// (corrections folded, superseded entries omitted) and its count. The JSON
// field order matches the read verbs' convention (ADR-0007); Reframes is never
// null so automation always reads an array.
type ListReframesView struct {
	Count    int                `json:"count"`
	Reframes []reframes.Reframe `json:"reframes"`
}

// ListReframesResult carries the machine view and the human-first lines the CLI
// shares through emit (ADR-0007).
type ListReframesResult struct {
	View  ListReframesView
	Lines []string
}

// SurfaceReframeView is the `lucid reframe surface --json` payload: the single
// pick for the logical day, or a null reframe when the pool is empty
// (reframes.md §4, §5). Day is the logical-day key the pick was recorded under.
type SurfaceReframeView struct {
	Day     string            `json:"day"`
	Reframe *reframes.Reframe `json:"reframe"`
}

// SurfaceReframeResult carries the machine view and the human-first line.
type SurfaceReframeResult struct {
	View  SurfaceReframeView
	Lines []string
}

// AddReframe appends one immutable catch→flip entry to the Ledger (reframes.md
// §2, §3). It is deterministic and agent-free — no LLM in the path (architecture
// P9). An empty catch or flip writes nothing (a clean usage error), and a
// backdating `--day` runs on the strict tier: an unreadable token or a future
// day is a [DayRejectedError] and nothing is written (error-states.md §St-1),
// exactly as the observation micro-log's `--day` behaves. `recorded_at` is
// always the real write time; only `logical_date` follows `--day`.
func (r *Router) AddReframe(req AddReframeRequest) (AddReframeResult, error) {
	if strings.TrimSpace(req.Catch) == "" || strings.TrimSpace(req.Flip) == "" {
		return AddReframeResult{}, fmt.Errorf("a reframe needs both a catch and a flip; nothing was saved")
	}
	if err := r.prepareReframes(); err != nil {
		return AddReframeResult{}, err
	}

	// Strict-tier `--day`: reuse the one shared capture grammar so the accepted
	// forms, the 04:00 rollover, and the future ceiling match every other dated
	// capture. A refusal is a DayRejectedError the CLI prints; nothing lands.
	when, err := resolveCaptureWhen(req.DayArg, whenOr(req.Now))
	if err != nil {
		return AddReframeResult{}, err
	}

	rf := reframes.Reframe{
		Schema:      reframes.Schema,
		Catch:       req.Catch,
		Flip:        req.Flip,
		RecordedAt:  whenOr(req.Now).Format(time.RFC3339),
		LogicalDate: when.LogicalDate,
		Source:      reframes.SourceReframe,
		Tags:        req.Tags,
	}
	stored, err := r.store.AppendReframe(rf)
	if err != nil {
		return AddReframeResult{}, fmt.Errorf("could not add the reframe; nothing was saved: %w", err)
	}
	return AddReframeResult{
		Reframe: stored,
		Ack:     fmt.Sprintf("Added reframe as `%s`.", stored.ID),
	}, nil
}

// ListReframes reads the live reframe pool sorted by id with corrections folded
// (reframes.md §5). It writes nothing.
func (r *Router) ListReframes() (ListReframesResult, error) {
	entries, err := r.store.ReadReframes()
	if err != nil {
		return ListReframesResult{}, fmt.Errorf("could not read the reframes: %w", err)
	}
	if entries == nil {
		entries = []reframes.Reframe{}
	}
	return ListReframesResult{
		View:  ListReframesView{Count: len(entries), Reframes: entries},
		Lines: reframeListLines(entries),
	}, nil
}

// SurfaceReframe returns exactly one reframe for the logical day that now falls
// on and records that it was shown (reframes.md §4). It is idempotent within the
// day — a repeated call the same logical day returns the same pick without
// advancing — and rotates least-recently-surfaced across days. The only write is
// the surface-state projection; no entry is touched. An empty pool is a clean
// "nothing to surface," never an error.
func (r *Router) SurfaceReframe(now time.Time) (SurfaceReframeResult, error) {
	dayKey := reframes.LogicalDay(whenOr(now))
	pick, ok, err := r.store.SurfaceReframe(dayKey)
	if err != nil {
		return SurfaceReframeResult{}, err
	}
	view := SurfaceReframeView{Day: dayKey}
	if !ok {
		return SurfaceReframeResult{
			View:  view,
			Lines: []string{"No reframes to surface yet — add one with `lucid reframe add <catch> <flip>`."},
		}, nil
	}
	picked := pick
	view.Reframe = &picked
	return SurfaceReframeResult{
		View:  view,
		Lines: []string{fmt.Sprintf("%s → %s", pick.Catch, pick.Flip)},
	}, nil
}

// prepareReframes scaffolds the reframes tree idempotently, wrapping any failure
// with the shared message the reframe verbs report (mirrors
// [Router.prepareObservations]).
func (r *Router) prepareReframes() error {
	if err := r.store.ScaffoldReframes(); err != nil {
		return fmt.Errorf("could not prepare the reframes tree: %w", err)
	}
	return nil
}

// reframeListLines renders the human-first list: a count header then one line
// per reframe, `<id>  <catch> → <flip>`. An empty pool prints the add hint, so
// the read is never a bare blank.
func reframeListLines(entries []reframes.Reframe) []string {
	if len(entries) == 0 {
		return []string{"No reframes yet — add one with `lucid reframe add <catch> <flip>`."}
	}
	lines := make([]string, 0, len(entries)+1)
	lines = append(lines, fmt.Sprintf("%d reframe(s):", len(entries)))
	for _, rf := range entries {
		lines = append(lines, fmt.Sprintf("  %s  %s → %s", rf.ID, rf.Catch, rf.Flip))
	}
	return lines
}
