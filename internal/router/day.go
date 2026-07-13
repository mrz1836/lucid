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

	empty := view.EngineDay == nil && view.Obs.Empty() &&
		len(view.RawEntryIDs) == 0 && len(view.Media) == 0
	if empty {
		lines := []string{fmt.Sprintf("No record for %s.", date)}
		return DayViewResult{Date: date, Empty: true, View: view, Lines: lines}, nil
	}
	lines := dayLines(date, view)
	if note, err := r.noLocationNote(date, view); err != nil {
		return DayViewResult{}, err
	} else if note != "" {
		lines = append(lines, note)
	}
	return DayViewResult{Date: date, View: view, Lines: lines}, nil
}

// noLocationNote returns the one-time "no location on file" note for a day
// (observations-module.md §Error states: "Enricher config lacks coordinates →
// skip that enricher; /day shows 'no location on file' once"). It fires only
// when the weather enricher is enabled, the day carries no weather event, and
// no location was on file on or before the day — so the user learns why weather
// is absent without any push.
func (r *Router) noLocationNote(date string, view storage.DayView) (string, error) {
	cfg, err := r.store.ReadObservationsConfig()
	if err != nil {
		return "", err
	}
	weatherOn := false
	for _, e := range cfg.Enrichers {
		if e.Name == observations.EnricherWeather && e.Enabled {
			weatherOn = true
		}
	}
	if !weatherOn || dayHasWeather(view) {
		return "", nil
	}
	onFile, err := r.store.LocationOnFile(date)
	if err != nil {
		return "", err
	}
	if onFile {
		return "", nil
	}
	return "weather: no location on file", nil
}

// dayHasWeather reports whether the day view already carries a weather
// enricher event (so the note is suppressed once weather is present).
func dayHasWeather(view storage.DayView) bool {
	src := observations.EnricherSource(observations.EnricherWeather)
	for _, e := range view.Obs.Events {
		if e.Kind == observations.KindContextDay && e.Source == src {
			return true
		}
	}
	return false
}

// resolveDayArg maps the optional `/day` argument to a logical date: empty is
// today's logical day, "yesterday" is the logical day before, and anything
// else is taken as the given YYYY-MM-DD (the storage read validates it). It
// resolves "today"/"yesterday" on the rollover boundary — the same one events
// file under — so a pre-rollover `/day` shows the day just lived, not an empty
// new one.
func resolveDayArg(arg string, now time.Time) string {
	base := observations.LogicalBaseDate(now, observations.DefaultRolloverMin)
	switch strings.TrimSpace(arg) {
	case "":
		return observations.DateString(base)
	case "yesterday":
		return observations.DateString(base.AddDate(0, 0, -1))
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
	if len(view.Media) > 0 {
		lines = append(lines, "Media:")
		for _, m := range view.Media {
			lines = append(lines, "  "+mediaLine(m))
		}
	}
	return lines
}

// mediaLine renders one stored attachment as an inventory line: the stored
// filename and its caption when present — never the referencing raw body,
// never a score. It matches the day view's inventory-only voice: what was
// attached, not a judgment of it.
func mediaLine(rec storage.MediaRecord) string {
	if strings.TrimSpace(rec.Caption) == "" {
		return rec.ID
	}
	return rec.ID + " — " + rec.Caption
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
