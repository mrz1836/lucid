package workout

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/observations"
)

// trendNow is a Monday (2026-07-20) at midday — past the 04:00 rollover, so the
// logical day is its own civil date. All the day-distance math in these tests
// counts backward from this instant.
const trendNow = "2026-07-20T12:00:00Z"

// --- Streak / adherence come straight from the Engine fold. ---

// TestBuildTrendStreakFromMetrics proves the streak and adherence are copied from
// the Engine metrics, never recomputed as a score on workout events.
func TestBuildTrendStreakFromMetrics(t *testing.T) {
	t.Parallel()

	tr := BuildTrend(TrendInput{
		Metrics: engine.Metrics{CurrentStreak: 7, Adherence: engine.Window{Adherence: 0.8}},
		Now:     mustTime(t, trendNow),
		Loc:     time.UTC,
	})

	assert.Equal(t, 7, tr.Streak, "streak is the Engine chain's, copied from metrics")
	assert.InDelta(t, 0.8, tr.Adherence, 1e-9, "adherence is copied from the Engine fold")
}

// --- Missing data: an honest empty trend. ---

// TestBuildTrendEmptyLedgerIsHonestEmpty proves the projection computes with zero
// data: no sessions, the whole default window skipped, a flat direction, and no
// body signals — never a crash or a fabricated number.
func TestBuildTrendEmptyLedgerIsHonestEmpty(t *testing.T) {
	t.Parallel()

	tr := BuildTrend(TrendInput{Now: mustTime(t, trendNow), Loc: time.UTC})

	assert.Equal(t, 0, tr.Streak)
	assert.Equal(t, defaultTrendWindowDays, tr.WindowDays, "an unset window falls to the four-week default")
	assert.Equal(t, 0, tr.Sessions)
	assert.Equal(t, 0, tr.ThisWeek)
	assert.Equal(t, 0, tr.PriorWeek)
	assert.Equal(t, DirectionFlat, tr.Direction, "no sessions either week reads as flat, not up/down")
	assert.Equal(t, defaultTrendWindowDays, tr.SkippedDays, "with nothing logged the whole window is skipped")
	assert.Empty(t, tr.BodyResponse)
}

// --- Volume / frequency direction. ---

// TestBuildTrendVolumeAndDirection proves the distinct-day session counts and the
// this-week-vs-prior-week frequency direction, and that two sessions on one day
// count once.
func TestBuildTrendVolumeAndDirection(t *testing.T) {
	t.Parallel()

	tr := BuildTrend(TrendInput{
		Workouts: []observations.Event{
			workoutOn("2026-07-20"), // today, ds 0 — this week
			workoutOn("2026-07-20"), // same day again — must not double-count
			workoutOn("2026-07-18"), // ds 2 — this week
			workoutOn("2026-07-15"), // ds 5 — this week
			workoutOn("2026-07-11"), // ds 9 — prior week
			workoutOn("2026-07-08"), // ds 12 — prior week
		},
		Now: mustTime(t, trendNow),
		Loc: time.UTC,
	})

	assert.Equal(t, 3, tr.ThisWeek, "three distinct days in the trailing week (the double log counts once)")
	assert.Equal(t, 2, tr.PriorWeek, "two distinct days in the week before")
	assert.Equal(t, 5, tr.Sessions, "five distinct session days inside the window")
	assert.Equal(t, DirectionUp, tr.Direction, "3 this week vs 2 the week before trends up")
	assert.Equal(t, defaultTrendWindowDays-5, tr.SkippedDays, "skipped = window minus distinct session days")
}

// TestFrequencyDirection covers the three-way direction mapping directly.
func TestFrequencyDirection(t *testing.T) {
	t.Parallel()

	assert.Equal(t, DirectionUp, frequencyDirection(3, 2))
	assert.Equal(t, DirectionDown, frequencyDirection(1, 3))
	assert.Equal(t, DirectionFlat, frequencyDirection(2, 2))
	assert.Equal(t, DirectionFlat, frequencyDirection(0, 0))
}

// --- Skipped-day count over a custom window. ---

// TestBuildTrendSkippedDaysCustomWindow proves the skipped-day count honors a
// caller-supplied window and never goes negative.
func TestBuildTrendSkippedDaysCustomWindow(t *testing.T) {
	t.Parallel()

	tr := BuildTrend(TrendInput{
		Workouts: []observations.Event{
			workoutOn("2026-07-20"),
			workoutOn("2026-07-17"),
		},
		Now:        mustTime(t, trendNow),
		Loc:        time.UTC,
		WindowDays: 7,
	})

	assert.Equal(t, 7, tr.WindowDays)
	assert.Equal(t, 2, tr.Sessions)
	assert.Equal(t, 5, tr.SkippedDays, "7-day window minus two session days")
}

// TestBuildTrendSkippedNeverNegative proves the skipped count floors at zero even
// when more distinct session days than the window somehow appear (e.g. a tiny
// window with several logs).
func TestBuildTrendSkippedNeverNegative(t *testing.T) {
	t.Parallel()

	tr := BuildTrend(TrendInput{
		Workouts: []observations.Event{
			workoutOn("2026-07-20"),
			workoutOn("2026-07-19"),
			workoutOn("2026-07-18"),
		},
		Now:        mustTime(t, trendNow),
		Loc:        time.UTC,
		WindowDays: 2,
	})

	assert.Equal(t, 0, tr.SkippedDays, "a fuller-than-window log never yields a negative skipped count")
}

// TestBuildTrendFutureSessionNotCounted proves a session logged ahead of the
// clock cannot inflate the counts.
func TestBuildTrendFutureSessionNotCounted(t *testing.T) {
	t.Parallel()

	tr := BuildTrend(TrendInput{
		Workouts: []observations.Event{
			workoutOn("2026-07-21"), // tomorrow — must not count
			workoutOn("2026-07-20"), // today
		},
		Now: mustTime(t, trendNow),
		Loc: time.UTC,
	})

	assert.Equal(t, 1, tr.ThisWeek, "a future-dated session is excluded")
	assert.Equal(t, 1, tr.Sessions)
}

// --- Body response: most-recent reading per part, in range, sorted. ---

// TestBuildTrendBodyResponseMostRecentPerPart proves the body response is the
// most-recent soreness/pain reading per part inside the window, sorted by part,
// and that an out-of-window reading is dropped.
func TestBuildTrendBodyResponseMostRecentPerPart(t *testing.T) {
	t.Parallel()

	tr := BuildTrend(TrendInput{
		BodyState: []observations.Event{
			bodyStateOn("2026-07-17", "legs", 4, -1),     // older legs reading
			bodyStateOn("2026-07-19", "legs", 6, -1),     // newer legs reading — wins
			bodyStateOn("2026-07-18", "shoulder", -1, 3), // shoulder pain
			bodyStateOn("2026-01-01", "back", 9, -1),     // far out of window — dropped
		},
		Now: mustTime(t, trendNow),
		Loc: time.UTC,
	})

	require.Len(t, tr.BodyResponse, 2, "the out-of-window back reading is excluded")

	assert.Equal(t, "legs", tr.BodyResponse[0].Part, "sorted by part: legs before shoulder")
	require.NotNil(t, tr.BodyResponse[0].Soreness)
	assert.Equal(t, 6, *tr.BodyResponse[0].Soreness, "the newer legs soreness wins over the older one")
	assert.Nil(t, tr.BodyResponse[0].Pain, "no pain reported for legs")
	assert.Equal(t, "2026-07-19", tr.BodyResponse[0].AsOf)

	assert.Equal(t, "shoulder", tr.BodyResponse[1].Part)
	require.NotNil(t, tr.BodyResponse[1].Pain)
	assert.Equal(t, 3, *tr.BodyResponse[1].Pain)
	assert.Nil(t, tr.BodyResponse[1].Soreness)
}

// TestBuildTrendBodyResponseIgnoresNonBodyStateKinds proves a workout event never
// leaks into the body-response fold.
func TestBuildTrendBodyResponseIgnoresNonBodyStateKinds(t *testing.T) {
	t.Parallel()

	tr := BuildTrend(TrendInput{
		BodyState: []observations.Event{workoutOn("2026-07-19")},
		Now:       mustTime(t, trendNow),
		Loc:       time.UTC,
	})
	assert.Empty(t, tr.BodyResponse, "a workout event is not a body-state reading")
}

// --- Purity / robustness. ---

// TestBuildTrendIsDeterministic proves the projection is pure: the same input
// yields a byte-identical trend across calls.
func TestBuildTrendIsDeterministic(t *testing.T) {
	t.Parallel()

	in := TrendInput{
		Workouts:  []observations.Event{workoutOn("2026-07-20"), workoutOn("2026-07-18")},
		BodyState: []observations.Event{bodyStateOn("2026-07-19", "legs", 5, -1)},
		Metrics:   engine.Metrics{CurrentStreak: 4},
		Now:       mustTime(t, trendNow),
		Loc:       time.UTC,
	}
	first := BuildTrend(in)
	second := BuildTrend(in)
	assert.Equal(t, first, second)
}

// TestBuildTrendNilLocationDefaultsUTC proves a nil location does not panic.
func TestBuildTrendNilLocationDefaultsUTC(t *testing.T) {
	t.Parallel()

	tr := BuildTrend(TrendInput{Workouts: []observations.Event{workoutOn("2026-07-20")}, Now: mustTime(t, trendNow)})
	assert.Equal(t, 1, tr.Sessions)
}

// TestBuildTrendWorkoutDaysSkipsUnusable proves the workout-day fold ignores a
// non-workout kind and a workout whose day cannot be resolved, counting only the
// real, dateable session.
func TestBuildTrendWorkoutDaysSkipsUnusable(t *testing.T) {
	t.Parallel()

	tr := BuildTrend(TrendInput{
		Workouts: []observations.Event{
			{Kind: observations.KindBodyState, OccurredAt: "2026-07-20T12:00:00Z"}, // wrong kind — skipped
			{Kind: observations.KindWorkout, OccurredAt: "nonsense"},               // unresolvable day — skipped
			workoutOn("2026-07-20"), // the one real session
		},
		Now: mustTime(t, trendNow),
		Loc: time.UTC,
	})
	assert.Equal(t, 1, tr.Sessions, "only the dateable workout counts")
}

// TestBuildTrendBodyResponseSkipsUnusable proves the body-response fold ignores a
// reading with no body_part and one whose day cannot be resolved.
func TestBuildTrendBodyResponseSkipsUnusable(t *testing.T) {
	t.Parallel()

	tr := BuildTrend(TrendInput{
		BodyState: []observations.Event{
			{Kind: observations.KindBodyState, OccurredAt: "2026-07-19T12:00:00Z", Payload: map[string]any{"soreness": 5}}, // no body_part
			{Kind: observations.KindBodyState, OccurredAt: "nonsense", Payload: map[string]any{"body_part": "legs", "pain": 6}},
			bodyStateOn("2026-07-19", "back", 3, -1),
		},
		Now: mustTime(t, trendNow),
		Loc: time.UTC,
	})
	require.Len(t, tr.BodyResponse, 1, "only the well-formed, dateable reading survives")
	assert.Equal(t, "back", tr.BodyResponse[0].Part)
}

// TestEventLogicalDay covers the day resolver: a stored logical_date wins, else
// occurred_at is used, and an event with neither usable is skipped.
func TestEventLogicalDay(t *testing.T) {
	t.Parallel()

	stored, ok := eventLogicalDay(observations.Event{LogicalDate: "2026-07-15"}, time.UTC)
	require.True(t, ok)
	assert.Equal(t, "2026-07-15", stored.Format(dateLayout))

	fromOccurred, ok := eventLogicalDay(observations.Event{OccurredAt: "2026-07-16T12:00:00Z"}, time.UTC)
	require.True(t, ok)
	assert.Equal(t, "2026-07-16", fromOccurred.Format(dateLayout))

	_, ok = eventLogicalDay(observations.Event{OccurredAt: "nonsense"}, time.UTC)
	assert.False(t, ok, "an event with no resolvable day is skipped, not mis-dated")
}

// --- helpers ---

// workoutOn builds a KindWorkout event on the given civil date (logged at midday,
// past the rollover, so its logical day is that date). It carries no LogicalDate,
// exercising the occurred_at fallback in eventLogicalDay.
func workoutOn(date string) observations.Event {
	return observations.Event{
		Kind:       observations.KindWorkout,
		OccurredAt: date + "T12:00:00Z",
		Payload:    map[string]any{"type": "session"},
	}
}

// bodyStateOn builds a KindBodyState event on the given date pairing a part with
// a soreness and/or pain reading; a negative score means that field is absent.
// Both OccurredAt and LogicalDate are set so recency ordering is deterministic.
func bodyStateOn(date, part string, soreness, pain int) observations.Event {
	payload := map[string]any{"body_part": part}
	if soreness >= 0 {
		payload["soreness"] = soreness
	}
	if pain >= 0 {
		payload["pain"] = pain
	}
	return observations.Event{
		Kind:        observations.KindBodyState,
		OccurredAt:  date + "T12:00:00Z",
		LogicalDate: date,
		Payload:     payload,
	}
}
