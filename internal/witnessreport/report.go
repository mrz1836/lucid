package witnessreport

import (
	"fmt"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/isoweek"
	"github.com/mrz1836/lucid/internal/router"
)

// Deterministic thresholds the report reads real signals against. They are the
// only tuning knobs in the pure core; each maps a concrete number to a concrete
// watch-out or ask so nothing is ever inferred by a model.
const (
	// quietWeekThreshold is the fewest days of the 7-day week that may be
	// accounted before the week is "thin" — a quiet-week accountability risk
	// surfaced honestly rather than hidden (a week with fewer than this many
	// logged days is LowSignal).
	quietWeekThreshold = 3
	// weekMissWatchOut is the fewest misses inside the week that raise a
	// this-week-misses watch-out (and the paired midweek check-in ask).
	weekMissWatchOut = 2
	// adherenceFloor is the 30-day adherence line below which the report flags a
	// dip. It sits under the chain's gate threshold so a passing chain does not
	// nag, but a genuine slide still shows.
	adherenceFloor = 0.80
	// agingAnchorMark is the days-since count past which a recorded anchor is
	// surfaced as "worth a check-in". It is deliberately high so a normal anchor
	// never trips it; the operator reviews every report during the preview
	// period, so an ambiguous anchor is caught before it reaches friends.
	agingAnchorMark = 45
)

// Report is the witness-safe weekly report data model — the "deterministic
// scaffold". Every number here is copied verbatim from the metrics projection
// (BuildReport never recomputes streaks or adherence) or from the 7-day
// WeekWindow over the same day records, so the honest-number guarantee holds by
// construction. WatchOuts and Asks are derived deterministically from those
// signals. The narrative slots (Faults, Progress, Narrative) are empty here and
// filled by the model-backed compose pass in a later phase; the numbers never
// enter a model slot.
type Report struct {
	ISOWeek string `json:"iso_week"`

	// Honest numbers, copied straight from the projection.
	Streak        int                      `json:"streak"`
	LongestStreak int                      `json:"longest_streak"`
	Adherence     engine.Window            `json:"adherence"` // trailing 30 days
	Week          engine.Window            `json:"week"`      // this week (7 days)
	WeekMisses    int                      `json:"week_misses"`
	ErrorBudget   engine.ErrorBudget       `json:"error_budget"`
	Anchors       []engine.AnchorDaysSince `json:"anchors"`

	// Derived deterministically from the signals above.
	WatchOuts []string `json:"watch_outs"`
	Asks      []string `json:"asks"`
	LowSignal bool     `json:"low_signal"`

	// Narrative slots — empty in the deterministic core, filled by the compose
	// pass. The numbers are never carried here.
	Faults    string `json:"faults,omitempty"`
	Progress  string `json:"progress,omitempty"`
	Narrative string `json:"narrative,omitempty"`

	// Compose metadata — how the report was reached, set by the model-allowed
	// compose pass (empty/false in the pure core). UsedLLM records the model
	// filled the narrative slots; Fallback records the deterministic path fired
	// (the model was unreachable or returned nothing usable) so the caller can
	// still alert that only the warmth was lost; SafetyTripped records the
	// witness-safe output scan caught private detail and the prose was discarded
	// in favor of the metrics-only report. The numbers still land on every path.
	UsedLLM       bool `json:"used_llm,omitempty"`
	Fallback      bool `json:"fallback,omitempty"`
	SafetyTripped bool `json:"safety_tripped,omitempty"`
}

// NumbersReader is the honest-numbers seam: the derived metrics projection and
// nothing else. It is exactly router.Router.Metrics, so the CLI and the
// scheduler node inject the real router while tests inject a fake. No other
// data source reaches the report — there is deliberately no observations,
// journal, or raw-entry reader on this interface.
type NumbersReader interface {
	Metrics(now time.Time) (router.MetricsResult, error)
}

// RecordsReader is the day-record seam for the 7-day WeekWindow. It is exactly
// storage.Adapter.ReadEngineDays — the engine tree only, never raw/ or
// processed/. Together with NumbersReader it is the entire input surface of the
// deterministic core; the structural firewall is that no wider reader exists.
type RecordsReader interface {
	ReadEngineDays() ([]engine.DayRecord, error)
}

// BuildReport folds the metrics projection and the week's day records into the
// deterministic Report. It is pure given its readers: it copies the honest
// numbers, computes this week's 7-day window, derives the watch-outs and the
// auto-drafted asks from real signals, and marks a quiet week — it never
// fabricates activity and never reaches a model. now anchors the ISO-week label
// and the "this week" window; the 30-day adherence stays anchored to the
// latest recorded logical day inside the projection, matching `lucid metrics`.
func BuildReport(now time.Time, numbers NumbersReader, records RecordsReader) (Report, error) {
	mr, err := numbers.Metrics(now)
	if err != nil {
		return Report{}, fmt.Errorf("witnessreport: read metrics: %w", err)
	}
	days, err := records.ReadEngineDays()
	if err != nil {
		return Report{}, fmt.Errorf("witnessreport: read engine days: %w", err)
	}

	loc := now.Location()
	if loc == nil {
		loc = time.UTC
	}
	// "This week" ends at the current logical day, not the latest record, so a
	// stretch of silence reads as a thin week rather than borrowing an older
	// full one. Anchor the fold at civil midnight in loc, where the day records'
	// dates are parsed.
	ref := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	week := engine.WeekWindow(days, ref, loc)

	m := mr.Metrics
	r := Report{
		ISOWeek:       isoweek.Label(now),
		Streak:        m.CurrentStreak,
		LongestStreak: m.LongestStreak,
		Adherence:     m.Adherence,
		Week:          week,
		WeekMisses:    week.Decided - week.Completed,
		ErrorBudget:   m.ErrorBudget,
		Anchors:       m.Anchors,
	}
	r.LowSignal = week.DaysAccounted < quietWeekThreshold
	r.WatchOuts = buildWatchOuts(r)
	r.Asks = DraftAsks(r)
	return r, nil
}

// buildWatchOuts derives the watch-outs section from real signals only. Each
// entry names a concrete number from the report, so the section is honest and
// omitted entirely (nil, not an empty header) when the record supports nothing
// — a strong week shows no watch-outs. A quiet week is never suppressed: its
// thin logging is itself the first watch-out.
func buildWatchOuts(r Report) []string {
	var out []string

	// Thin logging / quiet week — the low signal is itself the accountability
	// risk, surfaced honestly rather than hidden.
	if r.LowSignal {
		out = append(out, fmt.Sprintf(
			"Logged only %d of 7 days this week — accountability risk.", r.Week.DaysAccounted,
		))
	}

	// This week's misses.
	if r.WeekMisses >= weekMissWatchOut {
		out = append(out, fmt.Sprintf(
			"Missed %d of %d decided days this week.", r.WeekMisses, r.Week.Decided,
		))
	}

	// 30-day adherence dip — only once the window has decided days, so a
	// cold-start empty window never reads as a slide.
	if r.Adherence.Decided > 0 && r.Adherence.Adherence < adherenceFloor {
		out = append(out, fmt.Sprintf(
			"30-day adherence at %d%% — below the %d%% line.",
			pct(r.Adherence.Adherence), pct(adherenceFloor),
		))
	}

	// Error budget spent.
	if r.ErrorBudget.Exceeded {
		out = append(out, fmt.Sprintf(
			"Error budget spent — %d isolated misses against a budget of %d.",
			r.ErrorBudget.Burn, r.ErrorBudget.Budget,
		))
	}

	// An anchor aging past its mark — surfaced neutrally as worth a check-in.
	for _, a := range r.Anchors {
		if a.DaysSince >= agingAnchorMark {
			out = append(out, fmt.Sprintf(
				"%d days since %s — worth a check-in.", a.DaysSince, a.Label,
			))
		}
	}

	return out
}

// pct renders a 0..1 ratio as a whole-number percent for the honest-number
// copy. It truncates toward zero, so a reported figure never rounds up past the
// real adherence.
func pct(ratio float64) int { return int(ratio * 100) }
