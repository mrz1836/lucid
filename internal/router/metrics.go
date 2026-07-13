package router

import (
	"fmt"
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
// (for the SLO budget and gate marks), the active profile's clocks, and the
// append-only anchors store, then folds them through [engine.BuildMetrics].
// Streak/adherence/misses reuse the existing chain folds; only days-since is
// new math. It writes nothing beyond the idempotent engine-tree scaffold, and
// no model is reachable from this path (Sanctuary).
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
		Records: days,
		Chain:   chain,
		Anchors: engine.LatestAnchors(anchorLog),
		Now:     now,
		Clocks:  clocks,
		Loc:     loc,
	})
	return MetricsResult{Metrics: m, Lines: metricsLines(m)}, nil
}

// metricsLines renders the honest-number-paired practice-quality surface,
// mirroring statusLines: the streak, the default 30-day adherence window with
// its floor-day and accounted co-numbers, the longer gate windows, the
// error-budget burn, and one days-since line per recorded anchor. No score
// theater, one line each. windowLine/budgetLine/percent are shared with the
// status surface (same package).
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
		lines = append(lines, fmt.Sprintf("Days since %s: %d.", a.Label, a.DaysSince))
	}
	return lines
}
