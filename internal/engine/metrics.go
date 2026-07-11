package engine

import (
	"sort"
	"time"
)

// DaysSince returns the whole civil days elapsed from anchor to current with
// the anchor date as day 0 — "recorded today" reads 0, tomorrow 1. Both
// instants are re-anchored to UTC civil midnight (from each date's own
// Y/M/D) before differencing, so a 23-hour spring-forward day — or any other
// DST transition — cannot make the count drift. This deliberately does not
// reuse DaysBetween, which truncates the span in the host location and would
// under-count by one across such a day.
func DaysSince(anchor, current time.Time) int {
	ay, am, ad := anchor.Date()
	cy, cm, cd := current.Date()
	a := time.Date(ay, am, ad, 0, 0, 0, 0, time.UTC)
	c := time.Date(cy, cm, cd, 0, 0, 0, 0, time.UTC)
	return int(c.Sub(a).Hours() / 24)
}

// GateWindow is one gate-length adherence rollup (30/60/90) carried in the
// metrics projection so a harness reads every gate number without recomputing
// downstream.
type GateWindow struct {
	Length    int    `json:"length"`
	Adherence Window `json:"adherence"`
}

// AnchorDaysSince is one folded anchor with its running day count (day 0 on
// the anchor date).
type AnchorDaysSince struct {
	Label     string `json:"label"`
	Date      string `json:"date"`
	Note      string `json:"note,omitempty"`
	DaysSince int    `json:"days_since"`
}

// Metrics is the derived `lucid metrics` projection — the committed chain's
// *quality* read: current/longest streak, adherence over the default 30-day
// window (plus the 30/60/90 gate rollups), misses in that window, the error
// budget, and days-since for each recorded anchor. Streak/adherence/misses
// reuse the existing chain folds; only days-since is new math. Field order is
// fixed for a stable on-disk form. Ref is the anchoring logical day the
// rolling windows resolve to (nil when no day is decided yet).
type Metrics struct {
	CurrentStreak  int               `json:"current_streak"`
	LongestStreak  int               `json:"longest_streak"`
	Adherence      Window            `json:"adherence"`
	MissesInWindow int               `json:"misses_in_window"`
	ErrorBudget    ErrorBudget       `json:"error_budget"`
	Gates          []GateWindow      `json:"gates"`
	Anchors        []AnchorDaysSince `json:"anchors"`
	Ref            *string           `json:"ref"`
}

// MetricsInput is everything BuildMetrics folds into the projection: the
// already-folded day records, the chain config (for the SLO budget and gate
// marks), the effective anchors (latest per label — fold with LatestAnchors
// before calling), the current instant, the active profile's clocks, and the
// location logical dates are interpreted in.
type MetricsInput struct {
	Records []DayRecord
	Chain   ChainConfig
	Anchors []Anchor
	Now     time.Time
	Clocks  Clocks
	Loc     *time.Location
}

// BuildMetrics computes the metrics projection. It is pure and deterministic
// for the Ledger-anchored fields (streak, adherence, misses, gates), which
// resolve to the latest recorded logical day — a stored anchor, not the wall
// clock. Days-since is clock-relative on purpose: it counts from the current
// logical day (Now through the rollover), because "days since X" advances
// with the wall clock. No model is ever in the path.
func BuildMetrics(in MetricsInput) Metrics {
	loc := in.Loc
	if loc == nil {
		loc = time.UTC
	}
	records := in.Records
	streaks := ComputeStreaks(records, loc)
	budget := in.Chain.SLO.IsolatedMissBudgetPer30d

	m := Metrics{
		CurrentStreak: streaks.Current,
		LongestStreak: streaks.Longest,
		Adherence:     Window{Length: 30},
		ErrorBudget:   ErrorBudget{Budget: budget, Remaining: budget},
		Anchors:       anchorDaysSince(in.Anchors, in.Clocks.BaseLogicalDate(in.Now)),
	}

	ref, hasRef := latestRecordDate(records, loc)
	if hasRef {
		refStr := DateString(DateOf(ref))
		m.Ref = &refStr
		m.Adherence = windowStats(records, ref, 30, loc)
		m.MissesInWindow = m.Adherence.Decided - m.Adherence.Completed
		m.ErrorBudget = errorBudget(records, ref, budget, loc)
	}
	m.Gates = gateWindows(records, ref, hasRef, in.Chain.SLO.Gates, loc)
	return m
}

// gateWindows builds one adherence rollup per gate length (SLO.Gates, e.g.
// 30/60/90). Before any day is decided (hasRef false) each gate stays an
// honest zero-length window (Decided==0), never a hollow 1.0.
func gateWindows(records []DayRecord, ref time.Time, hasRef bool, gates []int, loc *time.Location) []GateWindow {
	out := make([]GateWindow, 0, len(gates))
	for _, g := range gates {
		w := Window{Length: g}
		if hasRef {
			w = windowStats(records, ref, g, loc)
		}
		out = append(out, GateWindow{Length: g, Adherence: w})
	}
	return out
}

// anchorDaysSince maps each folded anchor to its running day count relative to
// the current logical day (day 0 on the anchor date). A malformed date is
// skipped rather than counted, so a hand-corrupted anchors.json cannot panic
// the read. The output is sorted by label for a stable projection regardless
// of input order.
func anchorDaysSince(anchors []Anchor, logicalDay time.Time) []AnchorDaysSince {
	out := make([]AnchorDaysSince, 0, len(anchors))
	for _, a := range anchors {
		d, err := time.Parse(dateLayout, a.Date)
		if err != nil {
			continue
		}
		out = append(out, AnchorDaysSince{
			Label:     a.Label,
			Date:      a.Date,
			Note:      a.Note,
			DaysSince: DaysSince(d, logicalDay),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}
