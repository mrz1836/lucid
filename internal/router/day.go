package router

import (
	"fmt"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/storage"
)

// DayViewResult is the `/day` read-only join surface: the rendered lines for a
// person and the assembled view for a script.
type DayViewResult struct {
	Date  string
	Lines []string
	Empty bool
	View  storage.DayView
}

// DayView executes `/day [date|yesterday]` (observations-module.md §Commands):
// a read-only join of the engine day record, the day's observations (plus any
// spanning range event), and the raw entry ids for one logical day. It writes
// nothing. An empty day is honest — "No record for <date>." — never a hollow
// zero. The render is deterministic, so repeated calls are byte-stable.
func (r *Router) DayView(dateArg string, now time.Time) (DayViewResult, error) {
	now = whenOr(now)
	loc := now.Location()
	if err := r.store.ScaffoldObservations(); err != nil {
		return DayViewResult{}, fmt.Errorf("could not prepare the observations tree: %w", err)
	}
	if err := r.store.ScaffoldEngine(); err != nil {
		return DayViewResult{}, fmt.Errorf("could not prepare the engine tree: %w", err)
	}

	date := resolveDayArg(dateArg, now)
	view, err := r.store.ReadDayView(date, loc)
	if err != nil {
		return DayViewResult{}, err
	}

	empty := view.EngineDay == nil && view.Obs.Empty() && len(view.RawEntryIDs) == 0
	if empty {
		lines := []string{fmt.Sprintf("No record for %s.", date)}
		return DayViewResult{Date: date, Empty: true, View: view, Lines: lines}, nil
	}
	return DayViewResult{Date: date, View: view, Lines: dayLines(date, view)}, nil
}

// resolveDayArg maps the optional `/day` argument to a logical date: empty is
// today, "yesterday" is the day before, and anything else is taken as the
// given YYYY-MM-DD (the storage read validates it).
func resolveDayArg(arg string, now time.Time) string {
	switch strings.TrimSpace(arg) {
	case "":
		return observations.DateString(observations.DateOf(now))
	case "yesterday":
		return observations.DateString(observations.DateOf(now).AddDate(0, 0, -1))
	default:
		return strings.TrimSpace(arg)
	}
}

// dayLines renders the joined day view as deterministic, inventory-only lines:
// the engine record's practice facts, the observation events, and the raw
// entry ids — no adherence theater, no evaluative language.
func dayLines(date string, view storage.DayView) []string {
	lines := []string{fmt.Sprintf("Day %s", date)}
	if view.EngineDay != nil {
		lines = append(lines, engineDayLine(*view.EngineDay))
	}
	obs := view.Obs.Lines()
	if len(obs) > 0 {
		lines = append(lines, "Observations:")
		for _, l := range obs {
			lines = append(lines, "  "+l)
		}
	}
	if len(view.RawEntryIDs) > 0 {
		lines = append(lines, "Entries: "+strings.Join(view.RawEntryIDs, ", "))
	}
	return lines
}

// engineDayLine renders the practice facts of a folded engine day record as
// one inventory line (completed/missed, mode, capacity) — never a score.
func engineDayLine(rec engine.DayRecord) string {
	state := "recorded"
	switch {
	case rec.Completed:
		state = "closed out"
	case rec.Missed:
		state = "missed"
	case rec.Partial:
		state = "partial"
	}
	parts := []string{fmt.Sprintf("Engine: %s", state)}
	if rec.Mode != "" {
		parts = append(parts, "mode "+rec.Mode)
	}
	if rec.Capacity > 0 {
		parts = append(parts, fmt.Sprintf("capacity %d", rec.Capacity))
	}
	return strings.Join(parts, ", ")
}
