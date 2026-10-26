package router

import (
	"fmt"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
)

// MetricsResult is the read-only `lucid metrics` surface: the derived Metrics
// projection plus the human-readable lines rendered from it. The CLI prints the
// lines for a person and the Metrics as JSON for a script (a harness), so the
// practice's real numbers are read from one place and never recomputed
// downstream.
type MetricsResult struct {
	Metrics engine.Metrics
	Lines   []string
}

// Metrics executes `lucid metrics` (engine-module.md §Commands): the read-only
// practice-quality rollup. It reads the folded day records, the chain config
// (for the SLO budget and gate marks), the active profile's clocks, the
// append-only anchors store and the anchor event store, then folds them
// through [engine.BuildMetrics] with the lucid.json anchors thresholds.
// Streak/adherence/misses reuse the existing chain folds; the anchor numbers
// are the per-kind math (ADR-0013). A Ledger with no event store projects
// exactly as it did before kinds existed. It writes nothing beyond the
// idempotent engine-tree scaffold, and no model is reachable from this path
// (Sanctuary).
func (r *Router) Metrics(now time.Time) (MetricsResult, error) {
	now = whenOr(now)
	loc := now.Location()
	if err := r.prepareEngine(); err != nil {
		return MetricsResult{}, err
	}

	days, err := r.store.ReadEngineDays()
	if err != nil {
		return MetricsResult{}, err
	}
	chain, err := r.store.ReadChainConfig()
	if err != nil {
		return MetricsResult{}, err
	}
	profile, err := r.store.ReadProfileState()
	if err != nil {
		return MetricsResult{}, err
	}
	anchorLog, err := r.store.ReadAnchors()
	if err != nil {
		return MetricsResult{}, err
	}
	events, err := r.store.ReadAnchorEvents()
	if err != nil {
		return MetricsResult{}, err
	}

	// Resolve the active profile's clocks for the rollover boundary days-since
	// counts against. A blank or undefined active profile (e.g. a hand-edited
	// profile.json) falls back to the default clocks rather than failing the read.
	clocks, err := chain.ClocksFor(profile.Active)
	if err != nil {
		clocks, err = chain.ClocksFor(engine.DefaultProfile)
		if err != nil {
			return MetricsResult{}, err
		}
	}

	m := engine.BuildMetrics(engine.MetricsInput{
		Records:    days,
		Chain:      chain,
		Anchors:    engine.LatestAnchors(anchorLog),
		Now:        now,
		Clocks:     clocks,
		Loc:        loc,
		AnchorLog:  anchorLog,
		Events:     events,
		Thresholds: r.anchorThresholds(),
	})
	return MetricsResult{Metrics: m, Lines: metricsLines(m)}, nil
}

// metricsLines renders the honest-number-paired practice-quality surface,
// mirroring statusLines: the streak, the default 30-day adherence window with
// its floor-day and accounted co-numbers, the longer gate windows, the
// error-budget burn, and one line per active anchor in its kind's language
// (anchorMetricsLine). No score theater, one line each.
// windowLine/budgetLine/percent are shared with the status surface (same
// package).
func metricsLines(m engine.Metrics) []string {
	lines := []string{
		fmt.Sprintf("Streak: %d (longest %d).", m.CurrentStreak, m.LongestStreak),
		fmt.Sprintf("%d-day ", m.Adherence.Length) + windowLine(m.Adherence),
	}
	// The default window already surfaces the 30-day gate; print only the longer
	// gate rollups so no window is reported twice.
	for _, g := range m.Gates {
		if g.Length == m.Adherence.Length {
			continue
		}
		lines = append(lines, fmt.Sprintf("%d-day gate ", g.Length)+windowLine(g.Adherence))
	}
	lines = append(lines, budgetLine(m.ErrorBudget))
	for _, a := range m.Anchors {
		lines = append(lines, anchorMetricsLine(a))
	}
	return lines
}

// anchorMetricsLine renders one active anchor in its kind's language — the copy
// usage/commands.md §metrics fixes. A milestone line is byte-for-byte the line
// every anchor printed before kinds existed, so a Ledger with no kind events
// prints exactly what it printed before. The label is verbatim; a count takes
// its singular at exactly 1.
func anchorMetricsLine(a engine.AnchorDaysSince) string {
	switch {
	case a.Quit != nil:
		return quitMetricsLine(a.Label, *a.Quit)
	case a.Limit != nil:
		return limitMetricsLine(a.Label, *a.Limit)
	case a.Habit != nil:
		return habitMetricsLine(a.Label, *a.Habit)
	}
	return fmt.Sprintf("Days since %s: %d.", a.Label, a.DaysSince)
}

// quitMetricsLine: days since the last slip, with the longest clean run and
// the slip count beside it. With no slips the run is exactly a milestone's
// count, and the line says so rather than printing a longest equal to it.
func quitMetricsLine(label string, q engine.QuitStats) string {
	if q.Slips == 0 {
		return fmt.Sprintf("Days since %s: %d (no slips).", label, q.CurrentRun)
	}
	return fmt.Sprintf("Days since %s: %d (longest %d, %s).",
		label, q.CurrentRun, q.LongestRun, countOf(q.Slips, "slip"))
}

// limitMetricsLine: use-days spent against the allowance over the rolling
// window, split by intent, then the days since the last use. A planned use is
// spend, never failure; only going past the allowance is called out.
func limitMetricsLine(label string, l engine.LimitStats) string {
	line := fmt.Sprintf("%s: %d of %d in the last %s (%d planned, %s); ",
		label, l.Used, l.Allowance, countOf(l.WindowDays, "day"), l.Planned, countOf(l.Slips, "slip"))
	if l.DaysSinceLastUse == nil {
		line += "no uses yet"
	} else {
		line += countOf(*l.DaysSinceLastUse, "day") + " since last"
	}
	if l.OverAllowance {
		line += " — over allowance"
	}
	return line + "."
}

// habitMetricsLine: kept days out of accounted days, then how far the habit is
// confirmed, then the two signals worth a look — a miss run at the alarm and
// graduation eligibility. A habit with nothing accounted says so, never a
// hollow 100%: an unconfirmed day is unaccounted, not kept.
func habitMetricsLine(label string, h engine.HabitStats) string {
	parts := make([]string, 0, 4)
	if h.Accounted == 0 {
		parts = append(parts, "no accounted days yet")
	} else {
		parts = append(parts, fmt.Sprintf("kept %d of %s, %s",
			h.Kept, countOf(h.Accounted, "accounted day"), countOfPlural(h.Misses, "miss", "misses")))
	}
	if h.LastConfirmed == "" {
		parts = append(parts, "not confirmed yet")
	} else {
		parts = append(parts, "confirmed through "+h.LastConfirmed)
	}
	if h.Alarm {
		parts = append(parts, fmt.Sprintf("missed %d days in a row", h.CurrentMissRun))
	}
	if h.GraduationEligible {
		parts = append(parts, "eligible to graduate")
	}
	return label + ": " + strings.Join(parts, "; ") + "."
}

// countOfPlural is countOf for a noun whose plural is not noun+"s".
func countOfPlural(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, plural)
}
