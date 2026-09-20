package workout

import (
	"strings"
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

// --- Adherence comes from the Engine fold; the streak does not. ---

// TestBuildTrendAdherenceFromMetricsStreakIsNot proves adherence is still copied
// from the Engine metrics while the streak is the workout record's own: a live
// Engine chain streak with no logged workout day yields 0, never a borrowed
// number.
func TestBuildTrendAdherenceFromMetricsStreakIsNot(t *testing.T) {
	t.Parallel()

	tr := BuildTrend(TrendInput{
		Metrics: engine.Metrics{CurrentStreak: 7, Adherence: engine.Window{Adherence: 0.8}},
		Now:     mustTime(t, trendNow),
		Loc:     time.UTC,
	})

	assert.InDelta(t, 0.8, tr.Adherence, 1e-9, "adherence is copied from the Engine fold")
	assert.Equal(t, 0, tr.Streak, "the workout streak is never borrowed from the Engine chain")
}

// --- The real workout streak, counted from logged days. ---

// TestBuildTrendWorkoutStreak covers the streak the panel shows: zero with no
// logged day, today counted when today carries a workout, yesterday keeping the
// run alive while today is still in progress, a two-day gap ending it, and an
// anchor-only day closing a day exactly like a full session.
func TestBuildTrendWorkoutStreak(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		workouts []observations.Event
		want     int
		reason   string
	}{{
		name:   "no logged workout day is an honest zero",
		want:   0,
		reason: "nothing logged yields 0, which the panel renders as the build",
	}, {
		name:     "a workout today counts today",
		workouts: []observations.Event{workoutOn("2026-07-20")},
		want:     1,
		reason:   "today carries a workout, so the run anchors on today",
	}, {
		name: "a run ending today counts every consecutive day",
		workouts: []observations.Event{
			workoutOn("2026-07-20"),
			workoutOn("2026-07-19"),
			workoutOn("2026-07-18"),
		},
		want:   3,
		reason: "three consecutive days ending today",
	}, {
		name:     "yesterday alone keeps the run alive — today is in progress",
		workouts: []observations.Event{workoutOn("2026-07-19")},
		want:     1,
		reason:   "a day is not scored against the user before it is over",
	}, {
		name: "a run ending yesterday still holds",
		workouts: []observations.Event{
			workoutOn("2026-07-19"),
			workoutOn("2026-07-18"),
		},
		want:   2,
		reason: "the run anchors on yesterday when today is still open",
	}, {
		name:     "a two-day gap ends the run",
		workouts: []observations.Event{workoutOn("2026-07-18"), workoutOn("2026-07-17")},
		want:     0,
		reason:   "neither today nor yesterday carries a workout",
	}, {
		name: "an older run does not resume after a break",
		workouts: []observations.Event{
			workoutOn("2026-07-20"),
			// 07-19 missing — the run stops here
			workoutOn("2026-07-18"),
			workoutOn("2026-07-17"),
		},
		want:   1,
		reason: "only the consecutive run ending today counts",
	}, {
		name:     "an anchor-only day closes the day like a session",
		workouts: []observations.Event{anchorOn("2026-07-20"), anchorOn("2026-07-19")},
		want:     2,
		reason:   "a completed daily anchor is a workout event, so it closes the day",
	}, {
		name:     "an anchor and a session on one day count once",
		workouts: []observations.Event{anchorOn("2026-07-20"), workoutOn("2026-07-20")},
		want:     1,
		reason:   "the streak counts distinct days, not events",
	}, {
		name:     "a future-dated workout cannot start a run",
		workouts: []observations.Event{workoutOn("2026-07-22")},
		want:     0,
		reason:   "a log ahead of the clock never inflates the streak",
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tr := BuildTrend(TrendInput{
				Workouts: tc.workouts,
				Metrics:  engine.Metrics{CurrentStreak: 99}, // must never leak into the streak
				Now:      mustTime(t, trendNow),
				Loc:      time.UTC,
			})
			assert.Equal(t, tc.want, tr.Streak, tc.reason)
		})
	}
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

// --- Insight fold: per-part next-day pain-response trend (Q3=A). ---

// TestBuildTrendPainResponseNextDay proves the per-part next-day pain-response
// fold: it pairs each non-light targeted session day with the FOLLOWING logical
// day's max(pain, soreness), reads the chronological direction, and reports an
// explicit insufficient-data state below 3 paired days. Same-day readings never
// pair — only the following day answers the load.
func TestBuildTrendPainResponseNextDay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		workouts   []observations.Event
		body       []observations.Event
		wantDir    string
		wantPaired int
		wantInsuff bool
	}{{
		name: "rising next-day response",
		workouts: []observations.Event{
			sessionOn("2026-07-01", "knee", 8),
			sessionOn("2026-07-05", "knee", 8),
			sessionOn("2026-07-09", "knee", 8),
		},
		body: []observations.Event{
			bodyStateOn("2026-07-02", "knee", 3, -1),
			bodyStateOn("2026-07-06", "knee", 5, -1),
			bodyStateOn("2026-07-10", "knee", 7, -1),
		},
		wantDir:    ResponseRising,
		wantPaired: 3,
	}, {
		name: "easing next-day response",
		workouts: []observations.Event{
			sessionOn("2026-07-01", "knee", 8),
			sessionOn("2026-07-05", "knee", 8),
			sessionOn("2026-07-09", "knee", 8),
		},
		body: []observations.Event{
			bodyStateOn("2026-07-02", "knee", 7, -1),
			bodyStateOn("2026-07-06", "knee", 5, -1),
			bodyStateOn("2026-07-10", "knee", 3, -1),
		},
		wantDir:    ResponseEasing,
		wantPaired: 3,
	}, {
		name: "stable next-day response",
		workouts: []observations.Event{
			sessionOn("2026-07-01", "knee", 8),
			sessionOn("2026-07-05", "knee", 8),
			sessionOn("2026-07-09", "knee", 8),
		},
		body: []observations.Event{
			bodyStateOn("2026-07-02", "knee", 5, -1),
			bodyStateOn("2026-07-06", "knee", 5, -1),
			bodyStateOn("2026-07-10", "knee", 5, -1),
		},
		wantDir:    ResponseStable,
		wantPaired: 3,
	}, {
		name: "two paired days is insufficient",
		workouts: []observations.Event{
			sessionOn("2026-07-01", "knee", 8),
			sessionOn("2026-07-05", "knee", 8),
		},
		body: []observations.Event{
			bodyStateOn("2026-07-02", "knee", 3, -1),
			bodyStateOn("2026-07-06", "knee", 9, -1),
		},
		wantDir:    "",
		wantPaired: 2,
		wantInsuff: true,
	}, {
		name: "same-day readings do not pair",
		workouts: []observations.Event{
			sessionOn("2026-07-01", "knee", 8),
			sessionOn("2026-07-05", "knee", 8),
			sessionOn("2026-07-09", "knee", 8),
		},
		body: []observations.Event{
			// readings on the session days themselves — never the following day.
			bodyStateOn("2026-07-01", "knee", 9, -1),
			bodyStateOn("2026-07-05", "knee", 9, -1),
			bodyStateOn("2026-07-09", "knee", 9, -1),
		},
		wantDir:    "",
		wantPaired: 0,
		wantInsuff: true,
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tr := BuildTrend(TrendInput{
				Workouts:  tc.workouts,
				BodyState: tc.body,
				Now:       mustTime(t, trendNow),
				Loc:       time.UTC,
			})
			pt := partTrend(t, tr.PainResponse, "knee")
			assert.True(t, pt.Loaded, "a non-light session marks the part loaded")
			assert.Equal(t, tc.wantPaired, pt.PairedDays, "paired-day count")
			assert.Equal(t, tc.wantInsuff, pt.Insufficient, "insufficient-data state")
			assert.Equal(t, tc.wantDir, pt.Direction, "chronological direction")
		})
	}
}

// TestBuildTrendPainResponsePerPart proves the fold attributes each part
// separately: a rising knee and an easing back are read independently and never
// flattened into a single session-global number.
func TestBuildTrendPainResponsePerPart(t *testing.T) {
	t.Parallel()

	tr := BuildTrend(TrendInput{
		Workouts: []observations.Event{
			sessionOn("2026-07-01", "knee", 8),
			sessionOn("2026-07-05", "knee", 8),
			sessionOn("2026-07-09", "knee", 8),
			sessionOn("2026-07-02", "back", 8),
			sessionOn("2026-07-06", "back", 8),
			sessionOn("2026-07-10", "back", 8),
		},
		BodyState: []observations.Event{
			bodyStateOn("2026-07-02", "knee", 3, -1),
			bodyStateOn("2026-07-06", "knee", 5, -1),
			bodyStateOn("2026-07-10", "knee", 7, -1),
			bodyStateOn("2026-07-03", "back", 7, -1),
			bodyStateOn("2026-07-07", "back", 5, -1),
			bodyStateOn("2026-07-11", "back", 3, -1),
		},
		Now: mustTime(t, trendNow),
		Loc: time.UTC,
	})

	knee := partTrend(t, tr.PainResponse, "knee")
	assert.Equal(t, ResponseRising, knee.Direction, "knee's own next-day response is rising")
	back := partTrend(t, tr.PainResponse, "back")
	assert.Equal(t, ResponseEasing, back.Direction, "back's own next-day response is easing, read independently")
}

// TestBuildTrendResponseUsesMaxPainSoreness proves the next-day response is the
// greater of that day's pain and soreness — whichever is higher wins, so a pain
// spike is not hidden behind a low soreness and vice versa.
func TestBuildTrendResponseUsesMaxPainSoreness(t *testing.T) {
	t.Parallel()

	tr := BuildTrend(TrendInput{
		Workouts: []observations.Event{
			sessionOn("2026-07-01", "knee", 8),
			sessionOn("2026-07-05", "knee", 8),
			sessionOn("2026-07-09", "knee", 8),
		},
		BodyState: []observations.Event{
			bodyStateOn("2026-07-02", "knee", 1, 3), // pain 3 > soreness 1 → 3
			bodyStateOn("2026-07-06", "knee", 2, 5), // pain 5 > soreness 2 → 5
			bodyStateOn("2026-07-10", "knee", 8, 1), // soreness 8 > pain 1 → 8
		},
		Now: mustTime(t, trendNow),
		Loc: time.UTC,
	})

	pt := partTrend(t, tr.PainResponse, "knee")
	assert.Equal(t, 3, pt.PairedDays)
	assert.Equal(t, ResponseRising, pt.Direction, "the max(pain, soreness) series 3→5→8 reads rising")
}

// --- Insight fold: per-part load-vs-pain pattern (AC-4). ---

// TestBuildTrendLoadPattern proves the ordinal load-vs-response comparison:
// `tracks higher load` only when the heavier-load pairs' mean next-day response
// exceeds the lighter ones', and `no tracked increase` on the equal-mean and
// single-load-level cases.
func TestBuildTrendLoadPattern(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		workouts []observations.Event
		body     []observations.Event
		want     string
	}{{
		name: "heavier load tracks a higher response",
		workouts: []observations.Event{
			sessionOn("2026-07-01", "knee", 5), // moderate
			sessionOn("2026-07-05", "knee", 5), // moderate
			sessionOn("2026-07-09", "knee", 8), // hard
		},
		body: []observations.Event{
			bodyStateOn("2026-07-02", "knee", 2, -1), // moderate → 2
			bodyStateOn("2026-07-06", "knee", 3, -1), // moderate → 3 (mean 2.5)
			bodyStateOn("2026-07-10", "knee", 8, -1), // hard → 8
		},
		want: LoadPatternTracksHigher,
	}, {
		name: "equal means report no tracked increase",
		workouts: []observations.Event{
			sessionOn("2026-07-01", "knee", 5), // moderate
			sessionOn("2026-07-05", "knee", 5), // moderate
			sessionOn("2026-07-09", "knee", 8), // hard
		},
		body: []observations.Event{
			bodyStateOn("2026-07-02", "knee", 2, -1), // moderate → 2
			bodyStateOn("2026-07-06", "knee", 4, -1), // moderate → 4 (mean 3)
			bodyStateOn("2026-07-10", "knee", 3, -1), // hard → 3 (mean 3, not greater)
		},
		want: LoadPatternNoIncrease,
	}, {
		name: "a single load level reports no tracked increase",
		workouts: []observations.Event{
			sessionOn("2026-07-01", "knee", 8), // hard
			sessionOn("2026-07-05", "knee", 8), // hard
			sessionOn("2026-07-09", "knee", 8), // hard
		},
		body: []observations.Event{
			bodyStateOn("2026-07-02", "knee", 3, -1),
			bodyStateOn("2026-07-06", "knee", 5, -1),
			bodyStateOn("2026-07-10", "knee", 7, -1),
		},
		want: LoadPatternNoIncrease,
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tr := BuildTrend(TrendInput{
				Workouts:  tc.workouts,
				BodyState: tc.body,
				Now:       mustTime(t, trendNow),
				Loc:       time.UTC,
			})
			pp := partPattern(t, tr.LoadPattern, "knee")
			assert.Equal(t, tc.want, pp.Pattern)
		})
	}
}

// --- Insight fold: stateless post-workout checkpoint scaffold (Q2/Q6=A). ---

// TestBuildTrendCheckpointScaffold proves the stateless relative-time scaffold: a
// logged checkpoint_eligible session yields three checkpoints computed off that
// session's own time (never a fixed calendar day, never a done/pending tracker),
// and it is nil when no qualifying session or no config is present.
func TestBuildTrendCheckpointScaffold(t *testing.T) {
	t.Parallel()

	prog := checkpointProgram()
	require.NoError(t, prog.Validate())

	t.Run("qualifying session yields a relative scaffold", func(t *testing.T) {
		t.Parallel()

		const sessionAt = "2026-07-19T18:30:00Z"
		tr := BuildTrend(TrendInput{
			Program:  prog,
			Workouts: []observations.Event{workoutEvent(sessionAt, "sport", nil, 8)},
			Now:      mustTime(t, trendNow),
			Loc:      time.UTC,
		})

		require.NotNil(t, tr.Checkpoints)
		assert.Equal(t, "the loaded area", tr.Checkpoints.Subject)
		assert.Equal(t, "Post-workout check-in", tr.Checkpoints.Label)
		assert.Equal(t, "note how it feels", tr.Checkpoints.Copy)
		require.Len(t, tr.Checkpoints.Checkpoints, 3)

		base := mustTime(t, sessionAt)
		assert.True(t, base.Equal(tr.Checkpoints.SessionAt), "the scaffold anchors on the session time")
		assert.Equal(t, []int{0, 12, 24}, []int{
			tr.Checkpoints.Checkpoints[0].OffsetHours,
			tr.Checkpoints.Checkpoints[1].OffsetHours,
			tr.Checkpoints.Checkpoints[2].OffsetHours,
		})
		assert.True(t, base.Equal(tr.Checkpoints.Checkpoints[0].At), "right-after is the session time")
		assert.True(t, base.Add(12*time.Hour).Equal(tr.Checkpoints.Checkpoints[1].At))
		assert.True(t, base.Add(24*time.Hour).Equal(tr.Checkpoints.Checkpoints[2].At))

		// A session logged at a different clock time shifts every checkpoint by the
		// same delta — proof the scaffold keys off the logged time, not a calendar day.
		shifted := BuildTrend(TrendInput{
			Program:  prog,
			Workouts: []observations.Event{workoutEvent("2026-07-19T06:00:00Z", "sport", nil, 8)},
			Now:      mustTime(t, trendNow),
			Loc:      time.UTC,
		})
		require.NotNil(t, shifted.Checkpoints)
		assert.False(t, tr.Checkpoints.Checkpoints[0].At.Equal(shifted.Checkpoints.Checkpoints[0].At),
			"the scaffold is relative to the logged session time, not a fixed calendar day")
	})

	t.Run("most-recent eligible session wins", func(t *testing.T) {
		t.Parallel()

		tr := BuildTrend(TrendInput{
			Program: prog,
			Workouts: []observations.Event{
				workoutEvent("2026-07-15T09:00:00Z", "sport", nil, 8),
				workoutEvent("2026-07-18T20:00:00Z", "sport", nil, 8), // more recent
			},
			Now: mustTime(t, trendNow),
			Loc: time.UTC,
		})
		require.NotNil(t, tr.Checkpoints)
		assert.True(t, mustTime(t, "2026-07-18T20:00:00Z").Equal(tr.Checkpoints.SessionAt),
			"the most-recent qualifying session anchors the scaffold")
	})

	t.Run("no qualifying session yields no scaffold", func(t *testing.T) {
		t.Parallel()

		tr := BuildTrend(TrendInput{
			Program:  prog,
			Workouts: []observations.Event{workoutEvent("2026-07-19T18:00:00Z", "easy", nil, 2)}, // not eligible
			Now:      mustTime(t, trendNow),
			Loc:      time.UTC,
		})
		assert.Nil(t, tr.Checkpoints, "a non-eligible session raises no scaffold")
	})

	t.Run("no checkpoint config yields no scaffold", func(t *testing.T) {
		t.Parallel()

		noCfg := checkpointProgram()
		noCfg.Checkpoints = nil
		tr := BuildTrend(TrendInput{
			Program:  noCfg,
			Workouts: []observations.Event{workoutEvent("2026-07-19T18:00:00Z", "sport", nil, 8)},
			Now:      mustTime(t, trendNow),
			Loc:      time.UTC,
		})
		assert.Nil(t, tr.Checkpoints, "no config, no scaffold")
	})
}

// --- Insight fold: watch-outs (AC-6). ---

// TestBuildTrendWatchOuts proves the watch-outs frame concrete signals — a rising
// next-day response names its part and paired-day count — and are omitted entirely
// when nothing warrants them.
func TestBuildTrendWatchOuts(t *testing.T) {
	t.Parallel()

	t.Run("a rising response surfaces a concrete watch-out", func(t *testing.T) {
		t.Parallel()

		tr := BuildTrend(TrendInput{
			Workouts: []observations.Event{
				sessionOn("2026-07-01", "knee", 8),
				sessionOn("2026-07-05", "knee", 8),
				sessionOn("2026-07-09", "knee", 8),
			},
			BodyState: []observations.Event{
				bodyStateOn("2026-07-02", "knee", 3, -1),
				bodyStateOn("2026-07-06", "knee", 5, -1),
				bodyStateOn("2026-07-10", "knee", 7, -1),
			},
			Now: mustTime(t, trendNow),
			Loc: time.UTC,
		})

		require.NotEmpty(t, tr.WatchOuts)
		joined := strings.Join(tr.WatchOuts, "\n")
		assert.Contains(t, joined, "knee", "the watch-out names the part from the data")
		assert.Contains(t, joined, "rising")
		assert.Contains(t, joined, "3 logged days", "each line names a concrete number")
	})

	t.Run("a stable, single-load part warrants no watch-out", func(t *testing.T) {
		t.Parallel()

		tr := BuildTrend(TrendInput{
			Workouts: []observations.Event{
				sessionOn("2026-07-01", "knee", 8),
				sessionOn("2026-07-05", "knee", 8),
				sessionOn("2026-07-09", "knee", 8),
			},
			BodyState: []observations.Event{
				bodyStateOn("2026-07-02", "knee", 5, -1),
				bodyStateOn("2026-07-06", "knee", 5, -1),
				bodyStateOn("2026-07-10", "knee", 5, -1),
			},
			Now: mustTime(t, trendNow),
			Loc: time.UTC,
		})
		assert.Empty(t, tr.WatchOuts, "a clean, stable read shows no watch-outs")
	})
}

// --- Correlation synthetic-fixture cases: present / absent / insufficient (AC-7). ---

// TestBuildTrendCorrelationCases exercises the three required load-then-response
// cases in one place: correlation present (`tracks higher load`, rising), absent
// (`no tracked increase`, stable), and insufficient-data (fewer than 3 paired days
// per part).
func TestBuildTrendCorrelationCases(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		workouts    []observations.Event
		body        []observations.Event
		wantPattern string
		wantDir     string
		wantPaired  int
		wantInsuff  bool
	}{{
		name: "present — heavier load tracks a higher, rising response",
		workouts: []observations.Event{
			sessionOn("2026-07-01", "knee", 5), // moderate
			sessionOn("2026-07-05", "knee", 8), // hard
			sessionOn("2026-07-09", "knee", 8), // hard
		},
		body: []observations.Event{
			bodyStateOn("2026-07-02", "knee", 2, -1), // moderate → 2 (lower mean 2)
			bodyStateOn("2026-07-06", "knee", 6, -1), // hard → 6
			bodyStateOn("2026-07-10", "knee", 8, -1), // hard → 8 (higher mean 7)
		},
		wantPattern: LoadPatternTracksHigher,
		wantDir:     ResponseRising,
		wantPaired:  3,
	}, {
		name: "absent — the response does not track load",
		workouts: []observations.Event{
			sessionOn("2026-07-01", "knee", 5), // moderate
			sessionOn("2026-07-05", "knee", 5), // moderate
			sessionOn("2026-07-09", "knee", 8), // hard
		},
		body: []observations.Event{
			bodyStateOn("2026-07-02", "knee", 4, -1), // moderate → 4
			bodyStateOn("2026-07-06", "knee", 4, -1), // moderate → 4 (mean 4)
			bodyStateOn("2026-07-10", "knee", 4, -1), // hard → 4 (mean 4, not greater)
		},
		wantPattern: LoadPatternNoIncrease,
		wantDir:     ResponseStable,
		wantPaired:  3,
	}, {
		name: "insufficient — fewer than three paired days",
		workouts: []observations.Event{
			sessionOn("2026-07-01", "knee", 8),
			sessionOn("2026-07-05", "knee", 8),
		},
		body: []observations.Event{
			bodyStateOn("2026-07-02", "knee", 3, -1),
			bodyStateOn("2026-07-06", "knee", 9, -1),
		},
		wantPattern: LoadPatternNoIncrease,
		wantDir:     "",
		wantPaired:  2,
		wantInsuff:  true,
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tr := BuildTrend(TrendInput{
				Workouts:  tc.workouts,
				BodyState: tc.body,
				Now:       mustTime(t, trendNow),
				Loc:       time.UTC,
			})
			pt := partTrend(t, tr.PainResponse, "knee")
			assert.Equal(t, tc.wantPaired, pt.PairedDays, "paired-day count")
			assert.Equal(t, tc.wantInsuff, pt.Insufficient, "insufficient-data state")
			assert.Equal(t, tc.wantDir, pt.Direction, "chronological direction")

			pp := partPattern(t, tr.LoadPattern, "knee")
			assert.Equal(t, tc.wantPattern, pp.Pattern, "load-vs-pain classification")
		})
	}
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

// anchorOn builds the daily-anchor form of a KindWorkout event: the same kind,
// marked with the anchor payload flag and carrying no body parts, so it closes a
// day for the streak without opening a recovery window.
func anchorOn(date string) observations.Event {
	return observations.Event{
		Kind:       observations.KindWorkout,
		OccurredAt: date + "T12:00:00Z",
		Payload:    map[string]any{"anchor": true},
	}
}

// sessionOn builds a non-light targeted workout on the given civil date: a
// KindWorkout carrying the part as an explicit body_part and the given session rpe
// (5 → moderate, 8 → hard under inferredLoad), which is exactly what the
// pain-response fold pairs against the following day's reading. It names no card
// type, so the load comes from the rpe alone.
func sessionOn(date, part string, rpe int) observations.Event {
	return workoutEvent(date+"T12:00:00Z", "", []string{part}, rpe)
}

// partTrend returns the PartTrend for the named part or fails the test — a small
// helper so a missing fold entry surfaces as a clear failure, not a zero value.
func partTrend(t *testing.T, trends []PartTrend, part string) PartTrend {
	t.Helper()
	for _, pt := range trends {
		if pt.Part == part {
			return pt
		}
	}
	t.Fatalf("no PartTrend for %q in %+v", part, trends)
	return PartTrend{}
}

// partPattern returns the PartPattern for the named part or fails the test.
func partPattern(t *testing.T, patterns []PartPattern, part string) PartPattern {
	t.Helper()
	for _, pp := range patterns {
		if pp.Part == part {
			return pp
		}
	}
	t.Fatalf("no PartPattern for %q in %+v", part, patterns)
	return PartPattern{}
}

// checkpointProgram is a synthetic program with a checkpoint-eligible session card
// and a complete checkpoint block (0/12/24h) — no personal content, so the
// scaffold fold is exercised without touching the operator's real program.
func checkpointProgram() Program {
	return Program{
		Version:   ProgramSchema,
		ProgramID: "checkpoint_fixture",
		Cards: []Card{
			{ID: "sport", Name: "Sport session", Load: LoadHard, CheckpointEligible: true, Movements: []string{"drill"}},
			{ID: "easy", Name: "Easy day", Load: LoadLight, Movements: []string{"walk"}},
		},
		Rotation: []RotationEntry{{Weekday: "mon", Card: "sport"}},
		Checkpoints: &CheckpointConfig{
			Subject:     "the loaded area",
			Label:       "Post-workout check-in",
			Copy:        "note how it feels",
			OffsetHours: []int{0, 12, 24},
		},
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
