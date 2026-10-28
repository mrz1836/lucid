package router

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/storage"
)

// metricsDocsToday is the "today" of the synthetic metrics example in
// usage/commands.md §metrics: the read runs in the evening of this logical day.
func metricsDocsToday() time.Time { return time.Date(2026, time.January, 25, 18, 0, 0, 0, time.UTC) }

// metricsDocsAnchorLines are the four anchor lines usage/commands.md §metrics
// documents, one per kind, in projection order (sorted by label).
func metricsDocsAnchorLines() []string {
	return []string{
		"Late-night snacks: 1 of 4 in the last 90 days (1 planned, 0 slips); 7 days since last.",
		"Morning walk: running 83 days (since 2025-11-03); last 30 days: kept 29 of 30 accounted, 1 miss; confirmed through 2026-01-24.",
		"Days since first gate: 40.",
		"Days since sobriety: 102 (longest 140, 1 slip).",
	}
}

// metricsDocsAnchorsJSON is the synthetic anchors[] usage/commands.md §metrics
// shows for the same Ledger, verbatim.
const metricsDocsAnchorsJSON = `[
  {"id": "anchor_2025_09_02_a", "label": "Late-night snacks", "date": "2025-09-02",
   "days_since": 145, "kind": "limit",
   "limit": {"allowance": 4, "window_days": 90, "used": 1, "remaining": 3,
             "planned": 1, "slips": 0, "slips_7d": 0, "last_use": "2026-01-18",
             "days_since_last_use": 7, "over_allowance": false}},
  {"id": "anchor_2025_11_03_a", "label": "Morning walk", "date": "2025-11-03",
   "days_since": 83, "kind": "habit",
   "habit": {"window_days": 30, "habit_since": "2025-11-03", "accounted": 30,
             "kept": 29, "misses": 1, "current_miss_run": 1, "alarm": false,
             "miss_run_7d": 1, "last_confirmed": "2026-01-24", "next_confirm_from": "2026-01-25",
             "graduation_eligible": false}},
  {"id": "anchor_2025_12_16_a", "label": "first gate", "date": "2025-12-16",
   "days_since": 40},
  {"id": "anchor_2025_05_28_a", "label": "sobriety", "date": "2025-10-15",
   "days_since": 102, "kind": "quit",
   "quit": {"current_run": 102, "longest_run": 140, "slips": 1, "slips_7d": 0,
            "last_slip": "2025-10-15"}}
]`

// metricsDocsLedger writes the documented example through the verbs, in
// calendar order, each on its own day: a quit with one slip, a limit with one
// planned use, a habit confirmed from the day it became one through yesterday
// with a miss today, and a plain milestone. Every label is synthetic.
func metricsDocsLedger(t *testing.T) (*Router, *storage.Adapter) {
	t.Helper()
	r, a, _ := newBootedRouter(t)
	c := newKindsClock()

	c.noon(t, "2025-05-28")
	addAnchorOfKind(t, r, c, "sobriety", "2025-05-28", engine.AnchorKindQuit, 0, 0)
	c.noon(t, "2025-09-02")
	addAnchorOfKind(t, r, c, "Late-night snacks", "2025-09-02", engine.AnchorKindLimit, 4, 90)
	c.noon(t, "2025-10-15")
	_, err := r.AnchorSlip(AnchorSlipRequest{Label: "sobriety", Now: c.next()})
	require.NoError(t, err)
	c.noon(t, "2025-11-03")
	addAnchorOfKind(t, r, c, "Morning walk", "2025-11-03", engine.AnchorKindHabit, 0, 0)
	c.noon(t, "2025-12-16")
	_, err = r.AnchorAdd(AnchorAddRequest{Label: "first gate", Date: "2025-12-16", Now: c.next()})
	require.NoError(t, err)
	c.noon(t, "2026-01-18")
	_, err = r.AnchorUse(AnchorUseRequest{Label: "Late-night snacks", Intent: engine.AnchorIntentPlanned, Now: c.next()})
	require.NoError(t, err)
	c.noon(t, "2026-01-25")
	confirm(t, r, AnchorConfirmRequest{Labels: []string{"Morning walk"}, Now: c.next()})
	_, err = r.AnchorMiss(AnchorMissRequest{Label: "Morning walk", Now: c.next()})
	require.NoError(t, err)
	return r, a
}

// anchorLines is the anchor tail of the metrics prose: everything after the
// error-budget line, which is always the last chain line.
func anchorLines(t *testing.T, lines []string) []string {
	t.Helper()
	for i, line := range lines {
		if strings.HasPrefix(line, "Error budget") {
			return lines[i+1:]
		}
	}
	require.Fail(t, "no error-budget line in the metrics prose")
	return nil
}

// TestMetricsKindLines: `lucid metrics` prose renders one line per active anchor
// in its kind's language — the four documented forms verbatim, written through
// the verbs; the no-slip quit and the never-confirmed habit forms; and every
// variant the copy table in usage/commands.md §metrics names. A milestone line
// stays exactly the line every anchor printed before kinds existed.
func TestMetricsKindLines(t *testing.T) {
	t.Run("the four documented forms", func(t *testing.T) {
		r, _ := metricsDocsLedger(t)
		res, err := r.Metrics(metricsDocsToday())
		require.NoError(t, err)
		assert.Equal(t, metricsDocsAnchorLines(), anchorLines(t, res.Lines))
	})

	t.Run("a quit with no slips and a habit with nothing accounted", func(t *testing.T) {
		r, _, _ := newBootedRouter(t)
		c := newKindsClock()
		addAnchorOfKind(t, r, c, "sobriety", "2026-03-01", engine.AnchorKindQuit, 0, 0)
		addAnchorOfKind(t, r, c, "morning-walk", "2026-04-01", engine.AnchorKindHabit, 0, 0)
		addAnchorOfKind(t, r, c, "late-night-snacks", "2026-04-01", engine.AnchorKindLimit, 4, 90)

		res, err := r.Metrics(time.Date(2026, time.April, 10, 18, 0, 0, 0, time.UTC))
		require.NoError(t, err)
		assert.Equal(t, []string{
			"late-night-snacks: 0 of 4 in the last 90 days (0 planned, 0 slips); no uses yet.",
			"morning-walk: running 9 days (since 2026-04-01; a habit since 2026-04-10); no accounted days yet; first confirm pending.",
			"Days since sobriety: 40 (no slips).",
		}, anchorLines(t, res.Lines), "no hollow 100%: a habit nobody confirmed has nothing accounted")
	})

	t.Run("every variant of the copy", func(t *testing.T) {
		one, seven := 1, 7
		tests := []struct {
			name string
			row  engine.AnchorDaysSince
			want string
		}{
			{
				name: "milestone is today's line",
				row:  engine.AnchorDaysSince{Label: "first-gate", DaysSince: 40},
				want: "Days since first-gate: 40.",
			},
			{
				name: "quit reads its current run, not the anchor date",
				row: engine.AnchorDaysSince{
					Label: "sobriety", DaysSince: 900, Kind: engine.AnchorKindQuit,
					Quit: &engine.QuitStats{CurrentRun: 12, LongestRun: 140, Slips: 2},
				},
				want: "Days since sobriety: 12 (longest 140, 2 slips).",
			},
			{
				name: "quit with no slips",
				row: engine.AnchorDaysSince{
					Label: "sobriety", DaysSince: 30, Kind: engine.AnchorKindQuit,
					Quit: &engine.QuitStats{CurrentRun: 30, LongestRun: 30},
				},
				want: "Days since sobriety: 30 (no slips).",
			},
			{
				name: "limit with a slip, one day since the last use",
				row: engine.AnchorDaysSince{
					Label: "late-night-snacks", Kind: engine.AnchorKindLimit,
					Limit: &engine.LimitStats{
						Allowance: 4, WindowDays: 90, Used: 2, Remaining: 2, Planned: 1, Slips: 1,
						DaysSinceLastUse: &one,
					},
				},
				want: "late-night-snacks: 2 of 4 in the last 90 days (1 planned, 1 slip); 1 day since last.",
			},
			{
				name: "limit over the allowance",
				row: engine.AnchorDaysSince{
					Label: "late-night-snacks", Kind: engine.AnchorKindLimit,
					Limit: &engine.LimitStats{
						Allowance: 4, WindowDays: 90, Used: 5, Planned: 3, Slips: 2,
						DaysSinceLastUse: &seven, OverAllowance: true,
					},
				},
				want: "late-night-snacks: 5 of 4 in the last 90 days (3 planned, 2 slips); 7 days since last — over allowance.",
			},
			{
				name: "limit with no use yet",
				row: engine.AnchorDaysSince{
					Label: "late-night-snacks", Kind: engine.AnchorKindLimit,
					Limit: &engine.LimitStats{Allowance: 1, WindowDays: 7, Remaining: 1},
				},
				want: "late-night-snacks: 0 of 1 in the last 7 days (0 planned, 0 slips); no uses yet.",
			},
			{
				name: "habit with no miss",
				row: engine.AnchorDaysSince{
					Label: "morning-walk", Date: "2026-01-02", DaysSince: 23, Kind: engine.AnchorKindHabit,
					Habit: &engine.HabitStats{
						WindowDays: 30, HabitSince: "2026-01-02", Accounted: 1, Kept: 1, LastConfirmed: "2026-01-24",
					},
				},
				want: "morning-walk: running 23 days (since 2026-01-02); last 30 days: kept 1 of 1 accounted, 0 misses; confirmed through 2026-01-24.",
			},
			{
				name: "habit never confirmed, misses only",
				row: engine.AnchorDaysSince{
					Label: "morning-walk", Date: "2026-01-02", DaysSince: 23, Kind: engine.AnchorKindHabit,
					Habit: &engine.HabitStats{
						WindowDays: 30, HabitSince: "2026-01-02", Accounted: 1, Misses: 1, CurrentMissRun: 1,
					},
				},
				want: "morning-walk: running 23 days (since 2026-01-02); last 30 days: kept 0 of 1 accounted, 1 miss; first confirm pending.",
			},
			{
				name: "habit at the miss alarm",
				row: engine.AnchorDaysSince{
					Label: "morning-walk", Date: "2026-01-02", DaysSince: 23, Kind: engine.AnchorKindHabit,
					Habit: &engine.HabitStats{
						WindowDays: 30, HabitSince: "2026-01-02",
						Accounted: 20, Kept: 17, Misses: 3, CurrentMissRun: 3, Alarm: true, LastConfirmed: "2026-01-21",
					},
				},
				want: "morning-walk: running 23 days (since 2026-01-02); last 30 days: kept 17 of 20 accounted, 3 misses; confirmed through 2026-01-21; missed 3 days in a row.",
			},
			{
				name: "habit eligible to graduate",
				row: engine.AnchorDaysSince{
					Label: "morning-walk", Date: "2026-01-02", DaysSince: 23, Kind: engine.AnchorKindHabit,
					Habit: &engine.HabitStats{
						WindowDays: 30, HabitSince: "2026-01-02",
						Accounted: 30, Kept: 30, LastConfirmed: "2026-01-24", GraduationEligible: true,
					},
				},
				want: "morning-walk: running 23 days (since 2026-01-02); last 30 days: kept 30 of 30 accounted, 0 misses; confirmed through 2026-01-24; eligible to graduate.",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				assert.Equal(t, tt.want, anchorMetricsLine(tt.row))
			})
		}
	})
}

// TestMetricsJSONKindObjects: `lucid metrics --json` keeps every existing field
// and its meaning, and adds kind plus one kind object per quit, limit or habit
// anchor — the documented example, byte-for-byte equivalent. A milestone row
// carries neither (absent kind means milestone), days_since stays the days
// since the anchor's folded date for every kind, and a Ledger with no
// graduation omits anchors_graduated entirely.
func TestMetricsJSONKindObjects(t *testing.T) {
	r, _ := metricsDocsLedger(t)
	res, err := r.Metrics(metricsDocsToday())
	require.NoError(t, err)

	raw, err := json.Marshal(res.Metrics)
	require.NoError(t, err)
	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &top))

	keys := make([]string, 0, len(top))
	for k := range top {
		keys = append(keys, k)
	}
	assert.ElementsMatch(t, []string{
		"current_streak", "longest_streak", "adherence", "misses_in_window", "error_budget",
		"gates", "anchors", "anchors_sunset", "ref",
	}, keys, "every existing field, and no anchors_graduated without a graduation")
	assert.JSONEq(t, metricsDocsAnchorsJSON, string(top["anchors"]))
	assert.JSONEq(t, `[]`, string(top["anchors_sunset"]))

	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(top["anchors"], &rows))
	require.Len(t, rows, 4)
	byLabel := map[string]map[string]json.RawMessage{}
	for _, row := range rows {
		var label string
		require.NoError(t, json.Unmarshal(row["label"], &label))
		byLabel[label] = row
	}

	milestone := byLabel["first gate"]
	milestoneKeys := make([]string, 0, len(milestone))
	for k := range milestone {
		milestoneKeys = append(milestoneKeys, k)
	}
	assert.ElementsMatch(t, []string{"id", "label", "date", "days_since"}, milestoneKeys,
		"a milestone carries neither kind nor a kind object")

	for label, kind := range map[string]string{
		"sobriety": engine.AnchorKindQuit, "Late-night snacks": engine.AnchorKindLimit, "Morning walk": engine.AnchorKindHabit,
	} {
		row := byLabel[label]
		assert.JSONEq(t, `"`+kind+`"`, string(row["kind"]), label)
		for _, other := range []string{engine.AnchorKindQuit, engine.AnchorKindLimit, engine.AnchorKindHabit} {
			_, present := row[other]
			assert.Equal(t, other == kind, present, "%s carries only its own kind object", label)
		}
	}

	t.Run("days_since keeps its meaning for every kind", func(t *testing.T) {
		for _, row := range res.Metrics.Anchors {
			assert.Equal(t, engine.DaysSince(civil(t, row.Date), metricsDocsToday()), row.DaysSince,
				"%s: days since its folded date", row.Label)
		}
	})

	t.Run("a Ledger without kinds carries no kind field at all", func(t *testing.T) {
		r2, _, _ := newBootedRouter(t)
		c := newKindsClock()
		_, err := r2.AnchorAdd(AnchorAddRequest{Label: "first-gate", Date: "2026-03-01", Now: c.next()})
		require.NoError(t, err)
		res2, err := r2.Metrics(time.Date(2026, time.April, 10, 18, 0, 0, 0, time.UTC))
		require.NoError(t, err)
		raw2, err := json.Marshal(res2.Metrics.Anchors)
		require.NoError(t, err)
		assert.JSONEq(t,
			`[{"id":"anchor_2026_04_10_a","label":"first-gate","date":"2026-03-01","days_since":40}]`, string(raw2))
		assert.Equal(t, []string{"Days since first-gate: 40."}, anchorLines(t, res2.Lines))
	})
}

// TestMetricsGraduatedSeparateFromSunset: an anchor retired with sunset
// --graduated projects into anchors_graduated[] and never into
// anchors_sunset[], a plain sunset stays in anchors_sunset[], and neither
// counts on the prose surface. Without a graduation the array is omitted.
func TestMetricsGraduatedSeparateFromSunset(t *testing.T) {
	r, _, _ := newBootedRouter(t)
	c := newKindsClock()
	addAnchorOfKind(t, r, c, "morning-walk", "2026-01-05", engine.AnchorKindHabit, 0, 0)
	_, err := r.AnchorAdd(AnchorAddRequest{Label: "gate-30", Date: "2026-02-01", Now: c.next()})
	require.NoError(t, err)
	_, err = r.AnchorAdd(AnchorAddRequest{Label: "first-gate", Date: "2026-03-01", Now: c.next()})
	require.NoError(t, err)

	read := func() (MetricsResult, map[string]json.RawMessage) {
		res, readErr := r.Metrics(time.Date(2026, time.April, 10, 18, 0, 0, 0, time.UTC))
		require.NoError(t, readErr)
		raw, readErr := json.Marshal(res.Metrics)
		require.NoError(t, readErr)
		var top map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &top))
		return res, top
	}

	_, err = r.AnchorSunset(AnchorSunsetRequest{Label: "gate-30", Reason: "mistyped label", Now: c.next()})
	require.NoError(t, err)
	_, top := read()
	_, present := top["anchors_graduated"]
	assert.False(t, present, "a plain sunset alone leaves anchors_graduated omitted")

	grad, err := r.AnchorSunset(AnchorSunsetRequest{
		Label: "morning-walk", Reason: "part of every morning now", Graduated: true, Now: c.next(),
	})
	require.NoError(t, err)
	res, top := read()

	m := res.Metrics
	require.Len(t, m.Anchors, 1)
	assert.Equal(t, "first-gate", m.Anchors[0].Label)
	require.Len(t, m.AnchorsSunset, 1)
	assert.Equal(t, "gate-30", m.AnchorsSunset[0].Label)
	require.Len(t, m.AnchorsGraduated, 1)
	assert.Equal(t, engine.AnchorSunset{
		ID: grad.Anchor.ID, Label: "morning-walk", Date: "2026-01-05",
		Note: "part of every morning now", SunsetAt: grad.Anchor.RecordedAt,
	}, m.AnchorsGraduated[0], "the same shape as a sunset row")

	assert.JSONEq(t, `[{"id":"`+grad.Anchor.ID+`","label":"morning-walk","date":"2026-01-05",`+
		`"note":"part of every morning now","sunset_at":"`+grad.Anchor.RecordedAt+`"}]`,
		string(top["anchors_graduated"]))
	assert.NotContains(t, string(top["anchors_sunset"]), "morning-walk", "graduated, not abandoned")

	assert.Equal(t, []string{"Days since first-gate: 40."}, anchorLines(t, res.Lines),
		"neither a sunset nor a graduated anchor counts on the prose surface")
}

// TestMetricsThresholdsFromConfig: the lucid.json anchors block reaches the
// metrics read. A run of two misses alarms at the default miss_alarm of 2 and
// not at 3; graduation_days moves the bar a habit clears, and an alarm run
// restarts its graduation clock. The prose and the JSON agree.
func TestMetricsThresholdsFromConfig(t *testing.T) {
	// A habit since 2026-03-01, confirmed through 2026-04-07, then missed on
	// 2026-04-08 and 2026-04-09: 40 accounted days in its history, 29 of them in
	// the 30-day window ending 2026-04-10 (today itself is unaccounted).
	ledger := func(t *testing.T, missAlarm, graduationDays int) MetricsResult {
		t.Helper()
		r, a, _ := newBootedRouter(t)
		if missAlarm != 0 || graduationDays != 0 {
			cfg := r.Config()
			if missAlarm != 0 {
				cfg.Anchors.MissAlarm = missAlarm
			}
			if graduationDays != 0 {
				cfg.Anchors.GraduationDays = graduationDays
			}
			require.NoError(t, a.SaveConfig(cfg))
			_, err := r.Boot()
			require.NoError(t, err)
		}
		c := newKindsClock()
		c.noon(t, "2026-03-01")
		addAnchorOfKind(t, r, c, "morning-walk", "2026-03-01", engine.AnchorKindHabit, 0, 0)
		c.noon(t, "2026-04-10")
		confirm(t, r, AnchorConfirmRequest{Labels: []string{"morning-walk"}, ThroughArg: "2026-04-07", Now: c.next()})
		for _, day := range []string{"2026-04-08", "2026-04-09"} {
			_, err := r.AnchorMiss(AnchorMissRequest{Label: "morning-walk", DayArg: day, Now: c.next()})
			require.NoError(t, err)
		}
		res, err := r.Metrics(time.Date(2026, time.April, 10, 18, 0, 0, 0, time.UTC))
		require.NoError(t, err)
		require.Len(t, res.Metrics.Anchors, 1)
		require.NotNil(t, res.Metrics.Anchors[0].Habit)
		return res
	}
	const head = "morning-walk: running 40 days (since 2026-03-01); last 30 days: kept 27 of 29 accounted, 2 misses; confirmed through 2026-04-07"

	tests := []struct {
		name                      string
		missAlarm, graduationDays int
		alarm, eligible           bool
		line                      string
	}{
		{
			name:  "defaults: two in a row alarms, 90 days not reached",
			alarm: true, line: head + "; missed 2 days in a row.",
		},
		{
			name: "miss_alarm 3: two in a row is below the alarm", missAlarm: 3,
			line: head + ".",
		},
		{
			name: "miss_alarm 3, graduation_days 30: 40 accounted days clear the bar", missAlarm: 3, graduationDays: 30,
			eligible: true, line: head + "; eligible to graduate.",
		},
		{
			name: "graduation_days 30 with the default alarm: the alarm run restarts the clock", graduationDays: 30,
			alarm: true, line: head + "; missed 2 days in a row.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := ledger(t, tt.missAlarm, tt.graduationDays)
			h := res.Metrics.Anchors[0].Habit
			assert.Equal(t, 2, h.CurrentMissRun)
			assert.Equal(t, tt.alarm, h.Alarm)
			assert.Equal(t, tt.eligible, h.GraduationEligible)
			assert.Equal(t, []string{tt.line}, anchorLines(t, res.Lines))
		})
	}
}
