package engine

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStormEpisode_EmptyHistoryMarshalsUnchanged proves the two additive
// fields are invisible on a history that carries neither: version omits at 0
// and episodes omits at nil, so a storm.json written before episodes existed
// marshals byte-identically to today.
func TestStormEpisode_EmptyHistoryMarshalsUnchanged(t *testing.T) {
	h := StormHistory{
		Clauses:      []string{"clause-a"},
		Windows:      []StormWindow{{Label: "window-a", Start: "2026-11-02", End: "2026-11-09"}},
		DurationDays: 14,
		History:      []StormEvent{{At: "2026-07-14T07:05:00Z", Event: StormDeclared, Label: "clause-a"}},
	}
	b, err := json.Marshal(h)
	require.NoError(t, err)
	const want = `{"clauses":["clause-a"],"windows":[{"label":"window-a","start":"2026-11-02","end":"2026-11-09"}],"duration_days":14,"history":[{"at":"2026-07-14T07:05:00Z","event":"declared","label":"clause-a"}]}`
	// Byte-exact on purpose: JSONEq ignores key order, but this guards that the
	// on-disk bytes are unchanged — no new key appears and the field order the
	// old shape had is preserved. That is the whole claim of the checkpoint.
	assert.Equal(t, want, string(b)) //nolint:testifylint // byte-exact is the intent; JSONEq would not catch a field-order change
}

// TestNextStormEpisodeID mirrors the NextAnchorID contract: per-day slots,
// advancing past every id already in the index, fresh on a new day.
func TestNextStormEpisodeID(t *testing.T) {
	day := ts(2026, 7, 14, 7, 5)
	h := StormHistory{}
	assert.Equal(t, "storm_2026_07_14_a", NextStormEpisodeID(h, day))

	h.Episodes = []StormEpisode{{ID: "storm_2026_07_14_a"}, {ID: "storm_2026_07_14_b"}}
	assert.Equal(t, "storm_2026_07_14_c", NextStormEpisodeID(h, day))

	// A different day starts its own slot sequence.
	assert.Equal(t, "storm_2026_07_15_a", NextStormEpisodeID(h, ts(2026, 7, 15, 0, 0)))
}

func TestDeriveStormRuns_SingleRunAcrossLifecycle(t *testing.T) {
	h := StormHistory{History: []StormEvent{
		{At: "2026-07-01T08:00:00Z", Event: StormDeclared, Label: "clause-a"},
		{At: "2026-07-01T10:00:00Z", Event: StormConfirmed, Through: "2026-07-15"},
		{At: "2026-07-14T09:00:00Z", Event: StormRenewed, Through: "2026-07-29"},
		{At: "2026-07-30T06:00:00Z", Event: StormExpired},
	}}
	runs := DeriveStormRuns(h, ts(2026, 8, 1, 0, 0))
	require.Len(t, runs, 1, "declared→confirmed→renewed→expired is one episode")
	assert.Equal(t, "2026-07-01T08:00:00Z", runs[0].OpenerAt)
	assert.True(t, runs[0].Closed)
}

func TestDeriveStormRuns_TwoSequentialRuns(t *testing.T) {
	h := StormHistory{History: []StormEvent{
		{At: "2026-01-01T08:00:00Z", Event: StormDeclared},
		{At: "2026-01-02T06:00:00Z", Event: StormEnded},
		{At: "2026-03-01T08:00:00Z", Event: StormDeclared},
		{At: "2026-03-02T06:00:00Z", Event: StormEnded},
	}}
	runs := DeriveStormRuns(h, ts(2026, 4, 1, 0, 0))
	require.Len(t, runs, 2, "two sequential declared→ended runs are two episodes")
	assert.Equal(t, "2026-01-01T08:00:00Z", runs[0].OpenerAt)
	assert.Equal(t, "2026-03-01T08:00:00Z", runs[1].OpenerAt)
	assert.True(t, runs[0].Closed)
	assert.True(t, runs[1].Closed)
}

func TestDeriveStormRuns_TrailingOpenRun(t *testing.T) {
	h := StormHistory{History: []StormEvent{
		{At: "2026-07-01T08:00:00Z", Event: StormDeclared},
		{At: "2026-07-01T10:00:00Z", Event: StormConfirmed, Through: "2026-07-15"},
	}}
	runs := DeriveStormRuns(h, ts(2026, 7, 10, 0, 0))
	require.Len(t, runs, 1)
	assert.False(t, runs[0].Closed, "a run with no terminal event is still open")
}

func TestDeriveStormRuns_EnteredWindowNotDoubleCounted(t *testing.T) {
	h := StormHistory{
		Windows: []StormWindow{{Label: "window-a", Start: "2026-05-02", End: "2026-05-09"}},
		History: []StormEvent{
			{At: "2026-05-02T00:00:00Z", Event: StormEntered, Label: "window-a", Through: "2026-05-09"},
			{At: "2026-05-05T12:00:00Z", Event: StormEnded},
		},
	}
	runs := DeriveStormRuns(h, ts(2026, 6, 1, 0, 0))
	require.Len(t, runs, 1, "the entered event is the run; the window is not a second one")
	assert.Equal(t, "2026-05-02T00:00:00Z", runs[0].OpenerAt)
	assert.Empty(t, runs[0].WindowLabel)
}

func TestDeriveStormRuns_BareWindowStartedIsEpisodeFutureIsNot(t *testing.T) {
	h := StormHistory{Windows: []StormWindow{
		{Label: "window-b", Start: "2026-09-01", End: "2026-09-08"},
		{Label: "window-c", Start: "2026-12-01", End: "2026-12-08"},
	}}

	// now sits inside window-b; window-c has not started.
	runs := DeriveStormRuns(h, ts(2026, 9, 5, 0, 0))
	require.Len(t, runs, 1, "a bare window that has started is an episode; a future one is not")
	assert.Equal(t, "window-b", runs[0].WindowLabel)
	assert.Equal(t, "2026-09-01", runs[0].WindowStart)
	assert.Empty(t, runs[0].OpenerAt)
	assert.False(t, runs[0].Closed, "now is inside the window")

	// now sits after window-b ends and still before window-c: one closed run.
	runs = DeriveStormRuns(h, ts(2026, 9, 20, 0, 0))
	require.Len(t, runs, 1)
	assert.Equal(t, "window-b", runs[0].WindowLabel)
	assert.True(t, runs[0].Closed, "the window has ended")
}

func TestPlanStormEpisodes_MintsOnePerRunAndIsIdempotent(t *testing.T) {
	h := StormHistory{
		Windows: []StormWindow{{Label: "window-b", Start: "2026-09-01", End: "2026-09-08"}},
		History: []StormEvent{
			{At: "2026-01-05T08:00:00Z", Event: StormDeclared},
			{At: "2026-01-20T06:00:00Z", Event: StormExpired},
			{At: "2026-07-01T08:00:00Z", Event: StormDeclared},
			{At: "2026-07-01T10:00:00Z", Event: StormConfirmed, Through: "2026-07-15"},
		},
	}
	now := ts(2026, 9, 5, 0, 0)

	planned := PlanStormEpisodes(h, now)
	require.Len(t, planned, 3, "two event-opened runs plus one bare-window run")
	assert.Equal(t, "storm_2026_01_05_a", planned[0].ID)
	assert.Equal(t, "2026-01-05T08:00:00Z", planned[0].OpenerAt)
	assert.Equal(t, "storm_2026_07_01_a", planned[1].ID)
	assert.Equal(t, "storm_2026_09_01_a", planned[2].ID)
	assert.Equal(t, "window-b", planned[2].WindowLabel)
	assert.Equal(t, "2026-09-01", planned[2].WindowStart)

	// Re-planning against the history that already carries these indexes nothing.
	h.Episodes = append(h.Episodes, planned...)
	assert.Empty(t, PlanStormEpisodes(h, now), "already-indexed runs mint nothing")
}

func TestPlanStormEpisodes_SameDayOpensGetDistinctSlots(t *testing.T) {
	h := StormHistory{History: []StormEvent{
		{At: "2026-06-01T08:00:00Z", Event: StormDeclared},
		{At: "2026-06-01T09:00:00Z", Event: StormEnded},
		{At: "2026-06-01T10:00:00Z", Event: StormDeclared},
		{At: "2026-06-01T11:00:00Z", Event: StormEnded},
	}}
	planned := PlanStormEpisodes(h, ts(2026, 7, 1, 0, 0))
	require.Len(t, planned, 2)
	assert.Equal(t, "storm_2026_06_01_a", planned[0].ID)
	assert.Equal(t, "storm_2026_06_01_b", planned[1].ID, "two runs opening the same day get distinct slots")
}

// TestStormStatus_UnchangedByEpisodesIndex is the SC-13 promise, proven
// mechanically: attaching the planned episodes[] index (and stamping version)
// changes neither StormStanding nor any field of the derived Status, across a
// table of reference dates spanning every run and the gaps between them. It
// also asserts DeriveStormRuns never mutates the history it reads.
func TestStormStatus_UnchangedByEpisodesIndex(t *testing.T) {
	h := StormHistory{
		Clauses:      []string{"clause-a", "clause-b"},
		DurationDays: 14,
		Windows: []StormWindow{
			{Label: "window-a", Start: "2026-05-02", End: "2026-05-09"}, // entered in history below
			{Label: "window-b", Start: "2026-09-01", End: "2026-09-08"}, // bare, never entered
		},
		History: []StormEvent{
			{At: "2026-01-05T08:00:00Z", Event: StormDeclared, Label: "clause-a"},
			{At: "2026-01-05T10:00:00Z", Event: StormConfirmed, By: "w", Text: "ok", Through: "2026-01-19"},
			{At: "2026-01-20T06:00:00Z", Event: StormExpired}, // run 1 closed (expired)
			{At: "2026-03-01T08:00:00Z", Event: StormDeclared, Label: "clause-b"},
			{At: "2026-03-04T09:00:00Z", Event: StormLapsed}, // run 2 closed (declared→lapsed)
			{At: "2026-05-02T00:00:00Z", Event: StormEntered, Label: "window-a", Through: "2026-05-09"},
			{At: "2026-05-05T12:00:00Z", Event: StormEnded}, // run 3 closed (entered→ended)
			{At: "2026-07-01T08:00:00Z", Event: StormDeclared, Label: "clause-a"},
			{At: "2026-07-01T10:00:00Z", Event: StormConfirmed, By: "w", Text: "ok", Through: "2026-07-15"}, // run 4 open (standing)
		},
	}
	now := ts(2026, 9, 5, 12, 0)

	// DeriveStormRuns is a pure read: the history it walks is untouched.
	histBefore := slices.Clone(h.History)
	_ = DeriveStormRuns(h, now)
	assert.Equal(t, histBefore, h.History, "DeriveStormRuns must not mutate history")

	planned := PlanStormEpisodes(h, now)
	require.NotEmpty(t, planned, "the multi-run fixture must yield episodes to attach")

	withEpisodes := h
	withEpisodes.Version = StormVersion
	withEpisodes.Episodes = append(slices.Clone(h.Episodes), planned...)

	refDates := []string{
		"2026-01-10", "2026-02-01", "2026-03-02", "2026-04-15", "2026-05-04",
		"2026-06-10", "2026-07-10", "2026-08-01", "2026-09-05",
	}
	for _, date := range refDates {
		asOf, err := time.Parse(dateLayout, date)
		require.NoError(t, err)

		sb, tb := StormStanding(h, asOf, time.UTC)
		sa, ta := StormStanding(withEpisodes, asOf, time.UTC)
		assert.Equalf(t, sb, sa, "standing bool changed at %s", date)
		assert.Equalf(t, tb, ta, "standing through changed at %s", date)

		recs := []DayRecord{completedDay(date, ModeGreen)}
		before := mustStatusJSON(t, BuildStatus(StatusInput{Records: recs, Chain: defChain(), Storm: h, Loc: time.UTC}))
		after := mustStatusJSON(t, BuildStatus(StatusInput{Records: recs, Chain: defChain(), Storm: withEpisodes, Loc: time.UTC}))
		assert.Equalf(t, before, after, "derived status changed at %s", date)
	}
}

func mustStatusJSON(t *testing.T, s Status) string {
	t.Helper()
	b, err := json.Marshal(s)
	require.NoError(t, err)
	return string(b)
}
