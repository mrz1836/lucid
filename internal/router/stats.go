package router

import (
	"bytes"
	"fmt"
	"strconv"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/observations"
)

// StatsOptions carries the resolved `lucid stats` flag intent. The CLI fills it
// from the cobra flags plus their Changed bits (LastSet is `--last`'s Changed
// bit), so the router — not the command — owns range resolution and all date
// validation. Keeping the policy here makes it testable with an injected now
// and mirrors day.go, where the router resolves the requested date.
type StatsOptions struct {
	LastSet bool
	Last    int
	From    string
	To      string
}

// StatsDay is one logical day's volume row: the raw-entry count, the
// observation count, and their sum. JSON field order matches the documented
// example (docs/usage/commands.md §stats).
type StatsDay struct {
	Date         string `json:"date"`
	RawEntries   int    `json:"raw_entries"`
	Observations int    `json:"observations"`
	TotalEvents  int    `json:"total_events"`
}

// kindCount is one enabled kind's observation count, carried in config order.
type kindCount struct {
	Kind  string
	Count int
}

// kindCounts is the dense observations_by_kind breakdown: every enabled kind in
// config order, zeros included. It is deliberately NOT a map[string]int —
// encoding/json marshals map keys in alphabetical order, which would emit
// elimination,intake,mood,pain and could not guarantee a zero for an absent
// kind. The stats --json contract (docs §stats) is a stable key set and
// order across every run, so the ordered slice with a custom MarshalJSON owns
// the on-the-wire shape instead.
type kindCounts []kindCount

// MarshalJSON renders the ordered kind counts as a JSON object in slice
// (config) order, zeros included, so the key set and order are byte-stable. A
// nil/empty slice marshals to {} rather than null, so the field is always an
// object. Kinds are simple ASCII identifiers, so strconv.Quote yields a valid
// JSON string and the encoding never fails.
func (k kindCounts) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, kc := range k {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Quote(kc.Kind))
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(kc.Count))
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// StatsView is the machine-readable stats projection (ADR-0007). Field
// order matches the documented example: from, to, logical_day, raw_entries,
// observations, observations_by_kind, total_events, days.
type StatsView struct {
	From               string     `json:"from"`
	To                 string     `json:"to"`
	LogicalDay         bool       `json:"logical_day"`
	RawEntries         int        `json:"raw_entries"`
	Observations       int        `json:"observations"`
	ObservationsByKind kindCounts `json:"observations_by_kind"`
	TotalEvents        int        `json:"total_events"`
	Days               []StatsDay `json:"days"`
}

// StatsResult is the read-only `lucid stats` surface: the assembled JSON view
// and the human-readable lines rendered from it. The CLI prints the lines for a
// person and the view as JSON for a harness — the counts are read from one
// place and never recomputed downstream.
type StatsResult struct {
	View  StatsView
	Lines []string
}

// Stats executes `lucid stats` (docs/usage/commands.md §stats): the read-only
// Ledger-volume rollup over a window of logical days. It is a pure projection —
// for each day it reuses the exact `/day` join (storage.ReadDayView) and counts
// raw entries and the day's own observation events, so a day's numbers match
// what `lucid day` reports, with the two documented divergences (raw entries
// follow recorded-civil-date bucketing while observations are rollover-correct;
// a spanning observation counts once, on its start day). No model is reachable
// from this path (Sanctuary), and nothing is written beyond the idempotent
// observation- and engine-tree scaffolds the read verbs already perform.
func (r *Router) Stats(opts StatsOptions, now time.Time) (StatsResult, error) {
	now = whenOr(now)
	loc := now.Location()
	if err := r.prepareObservations(); err != nil {
		return StatsResult{}, err
	}
	if err := r.prepareEngine(); err != nil {
		return StatsResult{}, err
	}

	from, to, err := resolveStatsRange(opts, now)
	if err != nil {
		return StatsResult{}, err
	}

	cfg, err := r.store.ReadObservationsConfig()
	if err != nil {
		return StatsResult{}, err
	}

	byKind := make(map[observations.Kind]int, len(cfg.KindsEnabled))
	days := make([]StatsDay, 0)
	var totalRaw, totalObs int
	for _, d := range logicalDayRange(from, to) {
		dateStr := engine.DateString(d)
		view, verr := r.store.ReadDayView(dateStr, loc)
		if verr != nil {
			return StatsResult{}, verr
		}
		// Raw entries: the civil-date-bucketed join `lucid day` uses (zero
		// drift). Observations: the day's OWN events only — Obs.Events excludes
		// spanning RangeEvents (range events that started earlier), so a
		// spanning observation is counted once, on its start day.
		raw := len(view.RawEntryIDs)
		obs := len(view.Obs.Events)
		for _, e := range view.Obs.Events {
			byKind[e.Kind]++
		}
		days = append(days, StatsDay{Date: dateStr, RawEntries: raw, Observations: obs, TotalEvents: raw + obs})
		totalRaw += raw
		totalObs += obs
	}

	// Dense by-kind over the enabled kinds in config order, zeros included, so
	// the --json key set is stable across runs.
	dense := make(kindCounts, 0, len(cfg.KindsEnabled))
	for _, kind := range cfg.KindsEnabled {
		dense = append(dense, kindCount{Kind: string(kind), Count: byKind[kind]})
	}

	view := StatsView{
		From:               engine.DateString(from),
		To:                 engine.DateString(to),
		LogicalDay:         true,
		RawEntries:         totalRaw,
		Observations:       totalObs,
		ObservationsByKind: dense,
		TotalEvents:        totalRaw + totalObs,
		Days:               days,
	}
	return StatsResult{View: view, Lines: statsLines(view, dense)}, nil
}

// resolveStatsRange resolves the inclusive [from,to] logical-day window from the
// flag intent. Bare → today's logical day only; `--last N` → the N days
// ending at and including today; `--from/--to` → the explicit inclusive range,
// with `--to` defaulting to today and `--from` defaulting to `--to` (a single
// day). `--last` is mutually exclusive with `--from/--to`. "today" resolves
// through the same rollover the day view uses (observations.LogicalBaseDate),
// so stats' today is byte-identical to `lucid day`'s. Every rejection carries
// "invalid argument" so the CLI maps it to the usage exit code.
func resolveStatsRange(opts StatsOptions, now time.Time) (from, to time.Time, err error) {
	loc := now.Location()
	today := observations.LogicalBaseDate(now, observations.DefaultRolloverMin)
	hasRange := opts.From != "" || opts.To != ""

	if opts.LastSet && hasRange {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid argument: --last cannot be combined with --from/--to")
	}

	switch {
	case opts.LastSet:
		if opts.Last < 1 {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid argument: --last must be a positive number of days")
		}
		to = today
		from = engine.AddDays(today, -(opts.Last - 1))
	case hasRange:
		if opts.To != "" {
			to, err = observations.ParseDate(opts.To, loc)
			if err != nil {
				return time.Time{}, time.Time{}, fmt.Errorf("invalid argument: --to %q is not a YYYY-MM-DD date", opts.To)
			}
		} else {
			to = today
		}
		if opts.From != "" {
			from, err = observations.ParseDate(opts.From, loc)
			if err != nil {
				return time.Time{}, time.Time{}, fmt.Errorf("invalid argument: --from %q is not a YYYY-MM-DD date", opts.From)
			}
		} else {
			from = to
		}
		if engine.DateOf(from).After(engine.DateOf(to)) {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid argument: --from %s is after --to %s",
				engine.DateString(from), engine.DateString(to))
		}
	default:
		from, to = today, today
	}
	return from, to, nil
}

// logicalDayRange enumerates the inclusive civil-day sequence from..to on the
// engine day-arithmetic primitives. It is intentionally a small,
// cleanly-named unexported helper rather than a second, independent window loop:
// whichever sibling read command (Ledger-volume `stats` vs practice-quality
// `metrics`) needs a shared windowed-day substrate can promote this loop to a
// shared engine helper without a rewrite, so a given day's boundaries never
// drift between commands.
func logicalDayRange(from, to time.Time) []time.Time {
	from, to = engine.DateOf(from), engine.DateOf(to)
	n := engine.DaysBetween(from, to)
	if n < 0 {
		return nil
	}
	days := make([]time.Time, 0, n+1)
	for i := 0; i <= n; i++ {
		days = append(days, engine.AddDays(from, i))
	}
	return days
}

// statsLines renders the human, inventory-only volume surface: the range header
// with its logical-day basis, the raw-entry and observation totals with the
// sparse (nonzero-only) by-kind sub-lines in config order, the total, and the
// per-day breakdown. It is a count of what was recorded — never a score,
// target, or streak.
func statsLines(v StatsView, dense kindCounts) []string {
	lines := []string{
		fmt.Sprintf("Stats %s..%s (logical days)", v.From, v.To),
		fmt.Sprintf("Raw entries: %d", v.RawEntries),
		fmt.Sprintf("Observations: %d", v.Observations),
	}
	for _, kc := range dense {
		if kc.Count > 0 {
			lines = append(lines, fmt.Sprintf("  %s: %d", kc.Kind, kc.Count))
		}
	}
	lines = append(lines, fmt.Sprintf("Total events: %d", v.TotalEvents), "", "By day:")
	for _, d := range v.Days {
		lines = append(lines, fmt.Sprintf("  %s: %d entries, %d observations, %d total",
			d.Date, d.RawEntries, d.Observations, d.TotalEvents))
	}
	return lines
}
