package engine

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// StormEpisodeIDPrefix is the minted-id namespace: storm_YYYY_MM_DD_<slot>.
const StormEpisodeIDPrefix = "storm_"

// StormEpisode names one run of the storm history. OpenerAt identifies a run
// opened by a declared/entered event; WindowLabel+WindowStart identify a bare
// ambush window that stands with no history event of its own.
//
// It lives in the sibling episodes[] index (StormHistory.Episodes), never in
// history[]: writing identity onto a history event — or appending one — would
// move the derived status.json, which StormStanding reads from history's last
// element. See engine-module.md §storm.json "Why identity is a sibling index".
type StormEpisode struct {
	ID          string `json:"id"`
	OpenerAt    string `json:"opener_at,omitempty"`
	WindowLabel string `json:"window_label,omitempty"`
	WindowStart string `json:"window_start,omitempty"`
}

// NextStormEpisodeID mints the next free episode id for the given day:
// storm_YYYY_MM_DD_<slot>, advancing the slot past every id already present in
// the episodes index for that date. It mirrors NextAnchorID exactly, and it
// scans the whole index rather than any filtered subset for the same reason:
// a slot once consumed must never be reissued, or two episodes would share an
// identity. SlotLabel is the shared per-day slot grammar — anchor, insight,
// self, and storm ids are the same form.
func NextStormEpisodeID(h StormHistory, day time.Time) string {
	prefix := fmt.Sprintf("%s%04d_%02d_%02d_", StormEpisodeIDPrefix, day.Year(), int(day.Month()), day.Day())
	used := map[string]bool{}
	for _, e := range h.Episodes {
		if slot, ok := strings.CutPrefix(e.ID, prefix); ok {
			used[slot] = true
		}
	}
	for n := 0; ; n++ {
		slot := SlotLabel(n)
		if !used[slot] {
			return prefix + slot
		}
	}
}

// StormRun is one derived episode boundary: the opener that starts it and
// whether it has been closed by a terminal event (Closed=false for the
// still-open trailing run). An event-opened run carries OpenerAt; a bare
// ambush window run carries WindowLabel+WindowStart with OpenerAt empty.
type StormRun struct {
	OpenerAt    string
	WindowLabel string
	WindowStart string
	Closed      bool
}

// DeriveStormRuns walks the append-only history in order and returns the runs
// it describes (engine-module.md §storm.json "Episodes"). A declared or
// entered event opens a run when no run is open; ended/expired/lapsed closes
// the open run; confirmed/renewed continue it and never open one — a
// confirmation without a declaration is not an episode. A run still open at
// the end of the log is returned with Closed=false.
//
// After the history walk, each ambush window with no matching entered event
// whose start is on or before now yields a bare-window run: a future window is
// not yet an episode, and it becomes one only when its start arrives, which is
// what keeps the index time-monotonic. This is a pure function of the history
// and the reference instant — no clock, no LLM — and it never mutates h.
func DeriveStormRuns(h StormHistory, now time.Time) []StormRun {
	loc := now.Location()

	var runs []StormRun
	open := false
	for _, e := range h.History {
		switch e.Event {
		case StormDeclared, StormEntered:
			if !open {
				runs = append(runs, StormRun{OpenerAt: e.At})
				open = true
			}
		case StormEnded, StormExpired, StormLapsed:
			if open {
				runs[len(runs)-1].Closed = true
				open = false
			}
			// StormConfirmed and StormRenewed continue the open run: they never
			// open one, so they add no run and close none.
		}
	}

	today := DateOf(now.In(loc))
	for _, w := range h.Windows {
		if windowEntered(h, w) {
			continue // already represented by the entered event's run above
		}
		s, ok := dateInLoc(w.Start, loc)
		if !ok || DateOf(s).After(today) {
			continue // malformed, or a future window that has not opened yet
		}
		closed := false
		if e, eok := dateInLoc(w.End, loc); eok && today.After(DateOf(e)) {
			closed = true
		}
		runs = append(runs, StormRun{WindowLabel: w.Label, WindowStart: w.Start, Closed: closed})
	}
	return runs
}

// PlanStormEpisodes returns the episode records the history needs so every run
// carries a written id, in append order. It is pure and idempotent: a history
// whose runs are all already indexed yields nothing.
//
// It derives the runs, matches each against the existing episodes[] (by
// opener_at for an event-opened run, or window_label+window_start for a bare
// window), and mints ids for the unmatched ones — threading the growing index
// through NextStormEpisodeID so two runs opening on the same day get distinct
// slots. No history event is read or written for identity; the index is a
// sibling that leaves history[] untouched.
func PlanStormEpisodes(h StormHistory, now time.Time) []StormEpisode {
	runs := DeriveStormRuns(h, now)

	// A working copy whose episodes grow as records are planned, so same-day
	// mints never collide on a slot.
	working := h
	working.Episodes = slices.Clone(h.Episodes)

	var planned []StormEpisode
	for _, r := range runs {
		if runHasEpisode(h.Episodes, r) {
			continue
		}
		ep := StormEpisode{ID: NextStormEpisodeID(working, episodeMintDay(r, now))}
		if r.OpenerAt != "" {
			ep.OpenerAt = r.OpenerAt
		} else {
			ep.WindowLabel = r.WindowLabel
			ep.WindowStart = r.WindowStart
		}
		planned = append(planned, ep)
		working.Episodes = append(working.Episodes, ep)
	}
	return planned
}

// runHasEpisode reports whether existing already indexes run r — by opener_at
// for an event-opened run, or by window_label+window_start for a bare window.
func runHasEpisode(existing []StormEpisode, r StormRun) bool {
	for _, e := range existing {
		if r.OpenerAt != "" {
			if e.OpenerAt == r.OpenerAt {
				return true
			}
			continue
		}
		if e.WindowLabel == r.WindowLabel && e.WindowStart == r.WindowStart {
			return true
		}
	}
	return false
}

// episodeMintDay resolves the civil day an episode id is dated by: the opener's
// instant for an event-opened run, the window's start for a bare window. It
// falls back to now only if both are unparseable, so the id stays deterministic.
func episodeMintDay(r StormRun, now time.Time) time.Time {
	if r.OpenerAt != "" {
		if at, err := time.Parse(time.RFC3339, r.OpenerAt); err == nil {
			return at
		}
	}
	if r.WindowStart != "" {
		if d, err := time.Parse(dateLayout, r.WindowStart); err == nil {
			return d
		}
	}
	return now
}
