package engine

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rec builds a day record for scoring tests with explicit outcome flags. The
// link map only needs to reflect floor_day; completion/miss are set directly.
func rec(date, mode string, completed, missed, floor, storm bool) DayRecord {
	d, _ := time.Parse(dateLayout, date)
	return DayRecord{
		DayID:       DayID(d),
		LogicalDate: date,
		Mode:        mode,
		Completed:   completed,
		Missed:      missed,
		FloorDay:    floor,
		Storm:       storm,
		Links:       map[string]string{},
		Corrections: []Correction{},
	}
}

func completedDay(date, mode string) DayRecord { return rec(date, mode, true, false, false, false) }
func floorDay(date, mode string) DayRecord     { return rec(date, mode, true, false, true, false) }
func missedDay(date string) DayRecord          { return rec(date, ModeGreen, false, true, false, false) }
func stormMissDay(date string) DayRecord       { return rec(date, ModeRed, false, true, false, true) }

func defChain() ChainConfig { return DefaultChain() }

// TestBuildStatus_YellowFloorDayScores1 is the AC-5 mode-relative case: a
// Yellow day executed at floors is completed, so it scores 1.0 — the MVP
// scores links only, and floors count as full completions.
func TestBuildStatus_YellowFloorDayScores1(t *testing.T) {
	recs := []DayRecord{floorDay("2026-07-05", ModeYellow)}
	st := BuildStatus(StatusInput{Records: recs, Chain: defChain(), Loc: time.UTC})

	assert.InDelta(t, 1.0, st.Adherence7d.Adherence, 0)
	assert.Equal(t, 1, st.Adherence7d.Completed)
	assert.Equal(t, 1, st.Adherence7d.Decided)
	assert.Equal(t, 1, st.Adherence7d.FloorDays)
	assert.InDelta(t, 1.0, st.Adherence7d.FloorDayRatio, 0)
	assert.Equal(t, 1, st.Adherence7d.DaysAccounted)
}

// TestBuildStatus_ScoresLinksOnly confirms Green, Yellow, and Red completed
// days all score 1.0 — the MVP has no Crux, so mode changes the floor, not
// the score once the (mode-relative) completion is met.
func TestBuildStatus_ScoresLinksOnly(t *testing.T) {
	for _, mode := range []string{ModeGreen, ModeYellow, ModeRed} {
		st := BuildStatus(StatusInput{
			Records: []DayRecord{completedDay("2026-07-05", mode)},
			Chain:   defChain(), Loc: time.UTC,
		})
		assert.InDeltaf(t, 1.0, st.Adherence7d.Adherence, 0, "%s completed day should score 1.0", mode)
	}
}

// TestBuildStatus_ErrorBudgetHandComputed is the AC-5 hand-computed 30-day
// fixture: 29 completed days and exactly one isolated miss ⇒ burn 1 against
// the default budget of 4, with no consecutive misses.
func TestBuildStatus_ErrorBudgetHandComputed(t *testing.T) {
	var recs []DayRecord
	for d := 1; d <= 30; d++ {
		date := time.Date(2026, 7, d, 0, 0, 0, 0, time.UTC).Format(dateLayout)
		if d == 15 {
			recs = append(recs, missedDay(date))
			continue
		}
		recs = append(recs, completedDay(date, ModeGreen))
	}
	st := BuildStatus(StatusInput{Records: recs, Chain: defChain(), Loc: time.UTC})

	assert.Equal(t, ErrorBudget{Budget: 4, Burn: 1, Remaining: 3, Exceeded: false}, st.ErrorBudget)
	// Adherence over the full month: 29 completed of 30 decided.
	assert.Equal(t, 29, st.Adherence30d.Completed)
	assert.Equal(t, 30, st.Adherence30d.Decided)
	assert.InDelta(t, 29.0/30.0, st.Adherence30d.Adherence, 1e-12)
	// A single interior miss breaks the streak into 14 + 15; current is 15.
	assert.Equal(t, 15, st.CurrentStreak)
	assert.Equal(t, 15, st.LongestStreak)
	// The miss is isolated — no consecutive-miss run at the reference day.
	assert.Equal(t, 0, st.ConsecutiveMisses)
}

// TestBuildStatus_ConsecutiveMissesNotIsolated: two calendar-adjacent misses
// are a breach run, not isolated spends — burn stays 0 while the consecutive
// counter reads 2.
func TestBuildStatus_ConsecutiveMissesNotIsolated(t *testing.T) {
	recs := []DayRecord{
		completedDay("2026-07-14", ModeGreen),
		missedDay("2026-07-15"),
		missedDay("2026-07-16"),
	}
	st := BuildStatus(StatusInput{Records: recs, Chain: defChain(), Loc: time.UTC})
	assert.Equal(t, 0, st.ErrorBudget.Burn, "a consecutive pair is a breach, not an isolated spend")
	assert.Equal(t, 2, st.ConsecutiveMisses)
}

// TestBuildStatus_ErrorBudgetExceeded: more isolated misses than the budget
// clamps remaining at 0 and marks the budget exceeded; a miss older than the
// trailing 30 days is excluded from the burn.
func TestBuildStatus_ErrorBudgetExceeded(t *testing.T) {
	recs := make([]DayRecord, 0, 8)
	recs = append(recs, missedDay("2026-06-01")) // out of the 30-day window
	// Six isolated misses (gaps of three days keep each one isolated).
	for _, d := range []int{2, 5, 8, 11, 14, 17} {
		date := time.Date(2026, 7, d, 0, 0, 0, 0, time.UTC).Format(dateLayout)
		recs = append(recs, missedDay(date))
	}
	recs = append(recs, completedDay("2026-07-30", ModeGreen)) // ref anchor
	st := BuildStatus(StatusInput{Records: recs, Chain: defChain(), Loc: time.UTC})

	assert.Equal(t, 6, st.ErrorBudget.Burn, "the June miss is outside the window")
	assert.Equal(t, 4, st.ErrorBudget.Budget)
	assert.Equal(t, 0, st.ErrorBudget.Remaining)
	assert.True(t, st.ErrorBudget.Exceeded)
}

// TestBuildStatus_StormMissSpendsZeroBudget: a missed day under a storm burns
// no error budget (engine-module.md §tripwire).
func TestBuildStatus_StormMissSpendsZeroBudget(t *testing.T) {
	recs := []DayRecord{
		completedDay("2026-07-14", ModeGreen),
		stormMissDay("2026-07-15"),
		completedDay("2026-07-16", ModeGreen),
	}
	st := BuildStatus(StatusInput{Records: recs, Chain: defChain(), Loc: time.UTC})
	assert.Equal(t, 0, st.ErrorBudget.Burn)
	assert.Equal(t, 4, st.ErrorBudget.Remaining)
}

// TestBuildStatus_ConsecutiveResetsAtStormExit: storm misses are unaccrued for
// breach math, and the first post-storm miss starts a fresh run of 1.
func TestBuildStatus_ConsecutiveResetsAtStormExit(t *testing.T) {
	// Two storm misses, then the storm exits and a plain miss lands.
	recs := []DayRecord{
		stormMissDay("2026-07-10"),
		stormMissDay("2026-07-11"),
		missedDay("2026-07-12"),
	}
	st := BuildStatus(StatusInput{Records: recs, Chain: defChain(), Loc: time.UTC})
	assert.Equal(t, 1, st.ConsecutiveMisses, "post-storm miss resets the run to 1")

	// A storm miss sitting at the reference day is itself unaccrued.
	recs = []DayRecord{stormMissDay("2026-07-11")}
	st = BuildStatus(StatusInput{Records: recs, Chain: defChain(), Loc: time.UTC})
	assert.Equal(t, 0, st.ConsecutiveMisses)
}

// TestBuildStatus_StandingStormCarriesThrough is the AC-5 storm-state case:
// a confirmed storm still within its through date makes status carry
// storm_state=standing with the through date and the active profile.
func TestBuildStatus_StandingStormCarriesThrough(t *testing.T) {
	storm := StormHistory{
		DurationDays: 14,
		History: []StormEvent{
			{At: "2026-07-14T09:40:00Z", Event: StormConfirmed, Through: "2026-07-28"},
		},
	}
	recs := []DayRecord{stormMissDay("2026-07-20")}
	st := BuildStatus(StatusInput{Records: recs, Chain: defChain(), Storm: storm, Profile: "nights", Loc: time.UTC})

	assert.Equal(t, StormStandingState, st.StormState)
	require.NotNil(t, st.StormThrough)
	assert.Equal(t, "2026-07-28", *st.StormThrough)
	assert.Equal(t, "nights", st.ActiveProfile)
}

// TestBuildStatus_ExpiredStormIsNone: past the through date, the storm no
// longer stands and status reports none with a nil through.
func TestBuildStatus_ExpiredStormIsNone(t *testing.T) {
	storm := StormHistory{History: []StormEvent{
		{Event: StormConfirmed, Through: "2026-07-15"},
	}}
	recs := []DayRecord{completedDay("2026-07-20", ModeGreen)}
	st := BuildStatus(StatusInput{Records: recs, Chain: defChain(), Storm: storm, Loc: time.UTC})
	assert.Equal(t, StormNone, st.StormState)
	assert.Nil(t, st.StormThrough)
}

func TestStormStanding(t *testing.T) {
	loc := time.UTC
	at := func(s string) time.Time { d, _ := time.Parse(dateLayout, s); return d }

	t.Run("confirmed within through", func(t *testing.T) {
		h := StormHistory{History: []StormEvent{{Event: StormConfirmed, Through: "2026-07-28"}}}
		standing, through := StormStanding(h, at("2026-07-20"), loc)
		assert.True(t, standing)
		assert.Equal(t, "2026-07-28", through)
	})
	t.Run("confirmed on the through date is still standing", func(t *testing.T) {
		h := StormHistory{History: []StormEvent{{Event: StormConfirmed, Through: "2026-07-28"}}}
		standing, _ := StormStanding(h, at("2026-07-28"), loc)
		assert.True(t, standing)
	})
	t.Run("past through is not standing", func(t *testing.T) {
		h := StormHistory{History: []StormEvent{{Event: StormConfirmed, Through: "2026-07-28"}}}
		standing, through := StormStanding(h, at("2026-07-29"), loc)
		assert.False(t, standing)
		assert.Empty(t, through)
	})
	t.Run("declared but unconfirmed is not standing", func(t *testing.T) {
		h := StormHistory{History: []StormEvent{{Event: StormDeclared, Label: "clause-1"}}}
		standing, _ := StormStanding(h, at("2026-07-20"), loc)
		assert.False(t, standing)
	})
	t.Run("ended is not standing", func(t *testing.T) {
		h := StormHistory{History: []StormEvent{
			{Event: StormConfirmed, Through: "2026-07-28"},
			{Event: StormEnded},
		}}
		standing, _ := StormStanding(h, at("2026-07-20"), loc)
		assert.False(t, standing)
	})
	t.Run("entered ambush window via history", func(t *testing.T) {
		h := StormHistory{History: []StormEvent{{Event: StormEntered, Through: "2026-11-09"}}}
		standing, through := StormStanding(h, at("2026-11-05"), loc)
		assert.True(t, standing)
		assert.Equal(t, "2026-11-09", through)
	})
	t.Run("bare ambush window with no history stands", func(t *testing.T) {
		h := StormHistory{Windows: []StormWindow{{Label: "w1", Start: "2026-11-02", End: "2026-11-09"}}}
		standing, through := StormStanding(h, at("2026-11-05"), loc)
		assert.True(t, standing)
		assert.Equal(t, "2026-11-09", through)
	})
	t.Run("window not containing asOf", func(t *testing.T) {
		h := StormHistory{Windows: []StormWindow{{Start: "2026-11-02", End: "2026-11-09"}}}
		standing, _ := StormStanding(h, at("2026-12-01"), loc)
		assert.False(t, standing)
	})
	t.Run("confirmed with empty through is not standing", func(t *testing.T) {
		h := StormHistory{History: []StormEvent{{Event: StormConfirmed}}}
		standing, _ := StormStanding(h, at("2026-07-20"), loc)
		assert.False(t, standing)
	})
	t.Run("nil loc defaults to UTC", func(t *testing.T) {
		h := StormHistory{History: []StormEvent{{Event: StormConfirmed, Through: "2026-07-28"}}}
		standing, _ := StormStanding(h, at("2026-07-20"), nil)
		assert.True(t, standing)
	})
	t.Run("empty history and no windows", func(t *testing.T) {
		standing, _ := StormStanding(StormHistory{}, at("2026-07-20"), loc)
		assert.False(t, standing)
	})
}

func TestBuildStatus_DaysToNextGate(t *testing.T) {
	start := "2026-07-01"
	recs := []DayRecord{completedDay("2026-07-10", ModeGreen)} // elapsed 9
	st := BuildStatus(StatusInput{Records: recs, Chain: defChain(), ChainStart: &start, Loc: time.UTC})
	require.NotNil(t, st.DaysToNextGate)
	assert.Equal(t, 21, *st.DaysToNextGate) // 30 - 9

	// No chain_start yet ⇒ no gate.
	st = BuildStatus(StatusInput{Records: recs, Chain: defChain(), Loc: time.UTC})
	assert.Nil(t, st.DaysToNextGate)

	// Past the final gate ⇒ no gate.
	early := "2026-01-01"
	st = BuildStatus(StatusInput{Records: recs, Chain: defChain(), ChainStart: &early, Loc: time.UTC})
	assert.Nil(t, st.DaysToNextGate)

	// Malformed chain_start is treated as no gate, not a panic.
	bad := "not-a-date"
	st = BuildStatus(StatusInput{Records: recs, Chain: defChain(), ChainStart: &bad, Loc: time.UTC})
	assert.Nil(t, st.DaysToNextGate)
}

// TestBuildStatus_EmptyRecords: with no records the windows are empty, the
// budget is full, and there is no storm, gate, or streak.
func TestBuildStatus_EmptyRecords(t *testing.T) {
	st := BuildStatus(StatusInput{Chain: defChain(), Loc: time.UTC})
	assert.Equal(t, 0, st.CurrentStreak)
	assert.Equal(t, 7, st.Adherence7d.Length)
	assert.Equal(t, 30, st.Adherence30d.Length)
	assert.Equal(t, 0, st.Adherence7d.Decided)
	assert.Equal(t, ErrorBudget{Budget: 4, Burn: 0, Remaining: 4, Exceeded: false}, st.ErrorBudget)
	assert.Equal(t, StormNone, st.StormState)
	assert.Nil(t, st.StormThrough)
	assert.Nil(t, st.DaysToNextGate)
	assert.Equal(t, DefaultProfile, st.ActiveProfile)
	assert.Equal(t, EscalationNone, st.EscalationState)
}

// TestBuildStatus_UndecidedDayAccountedNotDecided: a mode-only day (declared,
// not yet closed out) is accounted but not decided, so it never drags
// adherence — the honest co-number reveals it instead.
func TestBuildStatus_UndecidedDayAccountedNotDecided(t *testing.T) {
	recs := []DayRecord{rec("2026-07-05", ModeGreen, false, false, false, false)}
	st := BuildStatus(StatusInput{Records: recs, Chain: defChain(), Loc: time.UTC})
	assert.Equal(t, 1, st.Adherence7d.DaysAccounted)
	assert.Equal(t, 0, st.Adherence7d.Decided)
	assert.InDelta(t, 0.0, st.Adherence7d.Adherence, 0)
	assert.Equal(t, 0, st.ConsecutiveMisses, "an undecided day is not a miss")
}

// TestBuildStatus_WindowExcludesOutOfRange: a day older than 7 days is out of
// the 7-day window but still inside the 30-day one.
func TestBuildStatus_WindowExcludesOutOfRange(t *testing.T) {
	recs := []DayRecord{
		completedDay("2026-07-01", ModeGreen), // 29 days before ref
		completedDay("2026-07-30", ModeGreen), // ref
	}
	st := BuildStatus(StatusInput{Records: recs, Chain: defChain(), Loc: time.UTC})
	assert.Equal(t, 1, st.Adherence7d.DaysAccounted, "only the ref day is within 7 days")
	assert.Equal(t, 2, st.Adherence30d.DaysAccounted)
}

// TestBuildStatus_Deterministic asserts BuildStatus is a pure function — the
// same inputs yield a deeply-equal Status (the byte-reproducibility root).
func TestBuildStatus_Deterministic(t *testing.T) {
	storm := StormHistory{History: []StormEvent{{Event: StormConfirmed, Through: "2026-07-28"}}}
	start := "2026-07-01"
	recs := []DayRecord{
		completedDay("2026-07-14", ModeGreen),
		missedDay("2026-07-15"),
		stormMissDay("2026-07-20"),
	}
	in := StatusInput{Records: recs, Chain: defChain(), Storm: storm, ChainStart: &start, Profile: "nights", Loc: time.UTC}
	assert.True(t, reflect.DeepEqual(BuildStatus(in), BuildStatus(in)))
}

// TestBuildStatus_SkipsMalformedDate: a record with an unparseable logical
// date is ignored by the window/anchor math rather than crashing.
func TestBuildStatus_SkipsMalformedDate(t *testing.T) {
	recs := []DayRecord{
		{LogicalDate: "not-a-date", Completed: true},
		completedDay("2026-07-05", ModeGreen),
	}
	st := BuildStatus(StatusInput{Records: recs, Chain: defChain(), Loc: time.UTC})
	assert.Equal(t, 1, st.Adherence7d.Completed)
}
