package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The Engine-level honesty invariants of the anchor kinds, one test each.
// Each sweeps a range of days or histories rather than checking one point,
// because an invariant that holds only at the example is not an invariant.

// TestHonestyPlannedUseNeverResets: a planned use is spend, never failure. It
// never moves the anchor's date or day count, never touches the slip numbers,
// and never zeroes anything — each one only spends one more use-day of
// allowance.
func TestHonestyPlannedUseNeverResets(t *testing.T) {
	a := newKindAnchor(t, "late-night-snacks", "2025-06-01")
	a.kind("2025-06-02", AnchorKindLimit, 4, 90)
	a.use("2026-02-01", AnchorIntentSlip)
	a.use("2026-03-04", AnchorIntentSlip)

	const today = "2026-03-10"
	prev := a.l.metrics(today, AnchorThresholds{})
	before := a.rowIn(prev)
	require.NotNil(t, before.Limit)
	for _, day := range []string{"2026-03-05", "2026-03-06", "2026-03-06", "2026-03-08", "2026-03-10"} {
		a.use(day, AnchorIntentPlanned)
		m := a.l.metrics(today, AnchorThresholds{})
		was, got := a.rowIn(prev), a.rowIn(m)
		require.NotNil(t, got.Limit)

		assert.Equal(t, before.Date, got.Date, "a planned use never re-dates the anchor")
		assert.Equal(t, before.DaysSince, got.DaysSince, "a planned use never resets the day count")
		assert.Equal(t, before.Limit.Slips, got.Limit.Slips, "a planned use is never a slip")
		assert.Equal(t, before.Limit.Slips7d, got.Limit.Slips7d, "a planned use never reaches the witness week")
		assert.Contains(t, []int{was.Limit.Used, was.Limit.Used + 1}, got.Limit.Used, "it spends at most one use-day")
		assert.Equal(t, got.Limit.Used, got.Limit.Planned+got.Limit.Slips)
		assert.Equal(t, prev.CurrentStreak, m.CurrentStreak, "the chain numbers never move")
		assert.Equal(t, prev.Adherence, m.Adherence)
		assert.Equal(t, prev.ErrorBudget, m.ErrorBudget)
		prev = m
	}
	assert.True(t, a.rowIn(prev).Limit.OverAllowance, "spending past the allowance is reported, never hidden")
}

// TestHonestyUnconfirmedDayNeverKept: for every day of a habit's life, kept
// days are exactly the confirmed days without a miss inside the window —
// never a day nobody confirmed, however quiet it was.
func TestHonestyUnconfirmedDayNeverKept(t *testing.T) {
	a := newKindAnchor(t, "morning-walk", "2026-01-01")
	a.kind("2026-01-01", AnchorKindHabit, 0, 14)
	a.confirm("2026-01-10", "2026-01-15")
	a.miss("2026-01-12")
	a.miss("2026-01-20") // a miss alone accounts its day, but never keeps it

	confirmed := map[string]bool{}
	for d := civil("2026-01-10"); !d.After(civil("2026-01-15")); d = d.AddDate(0, 0, 1) {
		confirmed[DateString(d)] = true
	}
	f := a.facts()
	for today := civil("2026-01-01"); !today.After(civil("2026-02-28")); today = today.AddDate(0, 0, 1) {
		want := 0
		for d := today.AddDate(0, 0, -13); !d.After(today); d = d.AddDate(0, 0, 1) {
			if s := DateString(d); confirmed[s] && s != "2026-01-12" {
				want++
			}
		}
		h := ComputeHabitStats(f, today, AnchorThresholds{})
		assert.Equal(t, want, h.Kept, "kept on %s", DateString(today))
		assert.LessOrEqual(t, h.Kept, h.Accounted)
	}
}

// TestHonestySingleMissNeverAlarms: an isolated miss — between kept days, or
// beside an unaccounted gap — never alarms and never restarts the graduation
// clock, at every miss_alarm, including a 1 passed in by mistake.
func TestHonestySingleMissNeverAlarms(t *testing.T) {
	a := newKindAnchor(t, "morning-walk", "2026-01-01")
	a.kind("2026-01-01", AnchorKindHabit, 0, 0)
	a.confirm("2026-01-01", "2026-04-30")
	for d := civil("2026-01-02"); d.Before(civil("2026-04-30")); d = d.AddDate(0, 0, 2) {
		a.miss(DateString(d)) // every other day: never two in a row
	}
	a.miss("2026-05-02") // beside an unaccounted 2026-05-01, after the last confirm

	f := a.facts()
	for _, th := range []AnchorThresholds{{}, {MissAlarm: 1}, {MissAlarm: 2}, {MissAlarm: 5}} {
		for today := civil("2026-01-01"); !today.After(civil("2026-05-03")); today = today.AddDate(0, 0, 1) {
			h := ComputeHabitStats(f, today, th)
			assert.False(t, h.Alarm, "no alarm on %s at miss_alarm %d", DateString(today), th.MissAlarm)
			assert.LessOrEqual(t, h.CurrentMissRun, 1)
			assert.LessOrEqual(t, h.MissRun7d, 1)
		}
	}
	assert.True(t, ComputeHabitStats(f, civil("2026-05-03"), AnchorThresholds{}).GraduationEligible,
		"isolated misses never restart the graduation clock")
}

// TestHonestyFirstConfirmNeverClaimsPreHabitDays: however long ago the anchor
// was recorded, a first confirm starts the day it became a habit; only an
// explicit --from reaches back.
func TestHonestyFirstConfirmNeverClaimsPreHabitDays(t *testing.T) {
	a := newKindAnchor(t, "morning-walk", "2025-06-01")
	a.kind("2026-01-10", AnchorKindHabit, 0, 0)
	f := a.facts()

	for through := civil("2025-12-01"); !through.After(civil("2026-02-01")); through = through.AddDate(0, 0, 1) {
		start, _, empty, err := ConfirmSpan(f, through, nil)
		require.NoError(t, err)
		assert.Equal(t, "2026-01-10", DateString(start), "through %s", DateString(through))
		assert.Equal(t, through.Before(civil("2026-01-10")), empty, "a pre-habit through-day claims nothing")
	}

	start, last, _, err := ConfirmSpan(f, civil("2026-01-16"), nil)
	require.NoError(t, err)
	a.confirm(DateString(start), DateString(last))
	h := ComputeHabitStats(a.facts(), civil("2026-01-17"), AnchorThresholds{})
	assert.Equal(t, 7, h.Accounted, "only the days since the cutover are accounted")
	assert.Equal(t, "2026-01-10", h.HabitSince)

	from := civil("2025-12-01")
	start, _, _, err = ConfirmSpan(f, civil("2026-01-16"), &from)
	require.NoError(t, err)
	assert.Equal(t, "2025-12-01", DateString(start), "--from is the only way back")
}

// TestHonestyConversionNeverLengthensQuitRun: whatever use-days a limit or a
// quit logged, and whether or not the reclassify's compat re-date landed, the
// quit's current run — and the older binary's plain day count — is never
// longer than the days since the latest use-day, nor since the start.
func TestHonestyConversionNeverLengthensQuitRun(t *testing.T) {
	const today = "2026-04-10"
	start := civil("2026-01-01")
	for first := 0; first <= 60; first += 12 {
		for second := first; second <= 66; second += 11 {
			for _, intent := range []string{AnchorIntentPlanned, AnchorIntentSlip} {
				for _, redate := range []bool{true, false} {
					u1, u2 := DateString(start.AddDate(0, 0, first)), DateString(start.AddDate(0, 0, second))
					assertConversionNeverLengthens(t, u1, u2, intent, redate, today)
				}
			}
		}
	}

	t.Run("a pending conversion re-date already floors an older binary", func(t *testing.T) {
		a := newKindAnchor(t, "late-night-snacks", "2026-01-01")
		a.kind("2026-01-01", AnchorKindLimit, 4, 90)
		a.use("2026-03-01", AnchorIntentPlanned)
		a.paired(AnchorEvent{Type: AnchorEventKind, Day: "2026-03-20", Kind: AnchorKindQuit},
			AnchorPairCompatFirst, "2026-03-01", true, false) // re-date landed, kind event did not
		r := a.row(today)
		assert.Equal(t, AnchorKindLimit, r.Kind, "still a limit until the kind event lands")
		assert.Equal(t, DaysSince(civil("2026-03-01"), civil(today)), r.DaysSince)
	})

	t.Run("a habit or milestone converted to quit counts from its start", func(t *testing.T) {
		a := newKindAnchor(t, "morning-walk", "2026-01-01")
		a.kind("2026-01-01", AnchorKindHabit, 0, 0)
		a.miss("2026-02-01")
		a.kind("2026-03-01", AnchorKindQuit, 0, 0)
		q := ComputeQuitStats(a.facts(), time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC))
		assert.Equal(t, DaysSince(start, civil(today)), q.CurrentRun, "a miss is not a use-day")
		assert.Zero(t, q.Slips)
	})
}

// assertConversionNeverLengthens logs a planned use on u1 and a use of the given
// intent on u2 under a limit, reclassifies to quit (with or without its compat
// re-date landing), and asserts no reading of the run is longer than the
// truth: the days since u2, and since the start.
func assertConversionNeverLengthens(t *testing.T, u1, u2, intent string, redate bool, today string) {
	t.Helper()
	const startDay = "2026-01-01"
	a := newKindAnchor(t, "late-night-snacks", startDay)
	a.kind(startDay, AnchorKindLimit, 4, 90)
	a.use(u1, AnchorIntentPlanned)
	a.use(u2, intent)
	a.paired(AnchorEvent{Type: AnchorEventKind, Day: "2026-03-20", Kind: AnchorKindQuit},
		AnchorPairCompatFirst, u2, redate, true) // without redate, the re-date never landed

	truth := DaysSince(civil(u2), civil(today))
	r := a.row(today)
	require.NotNil(t, r.Quit, "uses %s, %s", u1, u2)
	assert.LessOrEqual(t, r.Quit.CurrentRun, truth, "uses %s, %s redate=%v", u1, u2, redate)
	assert.LessOrEqual(t, r.Quit.CurrentRun, DaysSince(civil(startDay), civil(today)))
	assert.LessOrEqual(t, r.Quit.LongestRun, DaysSince(civil(startDay), civil(today)))
	if redate {
		assert.LessOrEqual(t, r.DaysSince, truth, "the older binary's reading is never longer either")
	}
}
