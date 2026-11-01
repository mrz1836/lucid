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

	// AnchorDetail is the opt-in full anchor detail: the week's anchor events,
	// notes verbatim. It is filled only when witness_report.anchor_detail is
	// full — the composer's one note-reaching reader is wired in no other mode
	// — and only after the model pass, so it is never part of the model's input
	// and never reaches the witness-safe scan. It is omitted from --json when
	// empty, so a summary-mode report carries no anchor_detail key.
	AnchorDetail []router.AnchorDetailEvent `json:"anchor_detail,omitempty"`

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

	// Fallback diagnostics — why the deterministic path fired, for the scheduled
	// worker's log line, job-run marker, and ping only. FallbackReason is the
	// composekit fallback reason (provider_outage / unusable_reply /
	// safety_tripped, "" on a model-composed report); FallbackError is the
	// provider error string behind an outage; FallbackDetail is the backend's
	// own reported reason (e.g. the CLI's "Not logged in"). They are never
	// serialized, so `lucid witness report --json` keeps its exact shape.
	FallbackReason string `json:"-"`
	FallbackError  string `json:"-"`
	FallbackDetail string `json:"-"`
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

	// "This week" ends at the current logical day, not the latest record, so a
	// stretch of silence reads as a thin week rather than borrowing an older
	// full one. Anchor the fold at civil midnight in loc, where the day records'
	// dates are parsed.
	ref, loc := weekRef(now)
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
	r.WatchOuts = buildWatchOuts(r, mr.Thresholds.WithDefaults().MissAlarm)
	r.Asks = DraftAsks(r)
	return r, nil
}

// weekRef is the report's reference day and location: civil midnight of now in
// now's location (UTC when it has none). BuildReport folds "this week" as the
// seven calendar days ending at ref, and the week-notes freshness check reads
// the same ref, so the card's "This week" and the notes window are one window
// and can never drift apart (ADR-0015 §3).
func weekRef(now time.Time) (ref time.Time, loc *time.Location) {
	loc = now.Location()
	if loc == nil {
		loc = time.UTC
	}
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc), loc
}

// buildWatchOuts derives the watch-outs section from real signals only. Each
// entry names a concrete number from the report, so the section is honest and
// omitted entirely (nil, not an empty header) when the record supports nothing
// — a strong week shows no watch-outs. A quiet week is never suppressed: its
// thin logging is itself the first watch-out. missAlarm is the miss-run length
// the projection's habit math alarmed at, so the habit line agrees with it.
func buildWatchOuts(r Report, missAlarm int) []string {
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

	// The kind-aware anchor signals, in projection order.
	for _, a := range r.Anchors {
		out = append(out, anchorWatchOuts(a, missAlarm)...)
	}

	return out
}

// anchorWatchOuts derives one anchor's watch-outs from its kind's note-free
// numbers in the projection — never a note, never an event date, so the lines
// are the same in both anchor-detail modes (usage/witness-report.md §"Anchors
// in the report"). A limit over its allowance, a habit miss run reaching
// missAlarm that touches the week (miss_run_7d, so a mid-week slide is not
// hidden by a recovery before the report), and any slip this week on a quit or
// a limit each raise a line. A planned use is spend, never a signal, and a
// milestone or a long quit run raises nothing: a long run is a win, not a
// worry.
func anchorWatchOuts(a engine.AnchorDaysSince, missAlarm int) []string {
	var out []string
	if l := a.Limit; l != nil && l.OverAllowance {
		out = append(out, fmt.Sprintf("%s: over allowance — %d of %d in the last %s.",
			a.Label, l.Used, l.Allowance, plural(l.WindowDays, "day", "days")))
	}
	if h := a.Habit; h != nil && h.MissRun7d >= missAlarm {
		out = append(out, fmt.Sprintf("%s: missed %d days in a row.", a.Label, h.MissRun7d))
	}
	slips := 0
	switch {
	case a.Quit != nil:
		slips = a.Quit.Slips7d
	case a.Limit != nil:
		slips = a.Limit.Slips7d
	}
	if slips > 0 {
		out = append(out, fmt.Sprintf("%s on %s this week.", plural(slips, "slip", "slips"), a.Label))
	}
	return out
}

// plural renders a count with its noun, singular at exactly 1.
func plural(n int, singular, pluralNoun string) string {
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, pluralNoun)
}

// pct renders a 0..1 ratio as a whole-number percent for the honest-number
// copy. It rounds to the nearest (half-up at the .5 midpoint) — the identical
// rule the router's status/metrics/person surfaces and the companion's status
// panel apply, so a witness report and the daily surfaces never disagree by a
// point (e.g. 2/3 reads 67% everywhere, not 66% here and 67% there).
func pct(ratio float64) int { return int(ratio*100 + 0.5) }
