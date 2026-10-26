package router

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/rep"
)

// bootedReps returns a booted router over a fresh scaffolded Ledger (day1/day2/
// day3 and edt live in reframe_test.go / observation_test.go — same package).
func bootedReps(t *testing.T) *Router {
	t.Helper()
	r := New(newScaffolded(t))
	_, err := r.Boot()
	require.NoError(t, err)
	return r
}

func addRep(t *testing.T, r *Router, req AddRepRequest) rep.Rep {
	t.Helper()
	res, err := r.AddRep(req)
	require.NoError(t, err)
	return res.Rep
}

func surfaceRepID(t *testing.T, r *Router, now time.Time) string {
	t.Helper()
	res, err := r.SurfaceRep(now)
	require.NoError(t, err)
	require.NotNil(t, res.View.Rep, "expected a pick at %s", now)
	return res.View.Rep.ID
}

// TestAddRep_ReturnsReceiptAndPersists: add stamps schema/source/state, assigns a
// rep_ id carried in the ack, keeps solo and level verbatim, and the entry reads
// back through ListReps.
func TestAddRep_ReturnsReceiptAndPersists(t *testing.T) {
	r := bootedReps(t)

	res, err := r.AddRep(AddRepRequest{
		Text: "Ask one question in a meeting", Solo: "Write the question down and send it afterward",
		Level: 2, Now: day1(),
	})
	require.NoError(t, err)
	assert.Equal(t, "rep_2026_07_02_001", res.Rep.ID)
	assert.Equal(t, rep.Schema, res.Rep.Schema)
	assert.Equal(t, rep.SourceRep, res.Rep.Source)
	assert.Equal(t, rep.StateActive, res.Rep.State)
	assert.Equal(t, "2026-07-02", res.Rep.LogicalDate)
	assert.Contains(t, res.Ack, "rep_2026_07_02_001", "the ack carries the receipt id")

	list, err := r.ListReps(ListRepsRequest{})
	require.NoError(t, err)
	assert.Equal(t, 1, list.View.Count)
	require.Len(t, list.View.Reps, 1)
	assert.Equal(t, "Ask one question in a meeting", list.View.Reps[0].Text)
	assert.Equal(t, "Write the question down and send it afterward", list.View.Reps[0].Solo)
	assert.Equal(t, 2, list.View.Reps[0].Level)
}

// TestAddRep_OptionalSoloAndLevel: a rep added with no solo variant and no level
// stores "" and 0 (unlevelled) — neither is ever synthesized.
func TestAddRep_OptionalSoloAndLevel(t *testing.T) {
	r := bootedReps(t)

	plain := addRep(t, r, AddRepRequest{Text: "Try one new route", Now: day1()})
	assert.Empty(t, plain.Solo)
	assert.Equal(t, 0, plain.Level)

	full := addRep(t, r, AddRepRequest{Text: "Read one page aloud", Solo: "Read it to yourself", Level: 6, Now: day1()})
	assert.Equal(t, "Read it to yourself", full.Solo)
	assert.Equal(t, 6, full.Level)
}

// TestAddRep_EmptyTextWritesNothing: an empty (or whitespace) text is a clean
// error and nothing lands.
func TestAddRep_EmptyTextWritesNothing(t *testing.T) {
	r := bootedReps(t)

	_, err := r.AddRep(AddRepRequest{Text: "", Now: day1()})
	require.Error(t, err)
	_, err = r.AddRep(AddRepRequest{Text: "   ", Solo: "a solo", Now: day1()})
	require.Error(t, err)

	list, err := r.ListReps(ListRepsRequest{IncludeRetired: true})
	require.NoError(t, err)
	assert.Equal(t, 0, list.View.Count)
}

// TestAddRep_InvalidLevelWritesNothing: a negative level is refused and nothing
// lands (0 is the unlevelled value, never an error at this layer).
func TestAddRep_InvalidLevelWritesNothing(t *testing.T) {
	r := bootedReps(t)

	_, err := r.AddRep(AddRepRequest{Text: "x", Level: -1, Now: day1()})
	require.Error(t, err)

	list, err := r.ListReps(ListRepsRequest{IncludeRetired: true})
	require.NoError(t, err)
	assert.Equal(t, 0, list.View.Count, "an invalid level writes nothing")
}

// TestAddRep_DayStrictRejectIsDayRejectedError: a bad --day is refused as a
// DayRejectedError (the type the CLI prints), writing nothing.
func TestAddRep_DayStrictRejectIsDayRejectedError(t *testing.T) {
	r := bootedReps(t)

	_, err := r.AddRep(AddRepRequest{Text: "x", DayArg: "@yesterdya", Now: day1()})
	require.Error(t, err)
	var refused *DayRejectedError
	require.ErrorAs(t, err, &refused)

	list, err := r.ListReps(ListRepsRequest{IncludeRetired: true})
	require.NoError(t, err)
	assert.Equal(t, 0, list.View.Count, "a refused day writes nothing")
}

// TestListReps_ActiveOnlyByDefaultAllIncludesRetired: retiring a rep drops it
// from the default (active-only) list but keeps it in the --all audit view, with
// its state resolved to retired.
func TestListReps_ActiveOnlyByDefaultAllIncludesRetired(t *testing.T) {
	r := bootedReps(t)
	first := addRep(t, r, AddRepRequest{Text: "First rep", Now: day1()})   // rep_..._001
	second := addRep(t, r, AddRepRequest{Text: "Second rep", Now: day1()}) // rep_..._002

	_, err := r.RetireRep(RetireRepRequest{ID: first.ID, Now: day2()})
	require.NoError(t, err)

	active, err := r.ListReps(ListRepsRequest{})
	require.NoError(t, err)
	assert.Equal(t, 1, active.View.Count, "the retired rep drops out of the default list")
	require.Len(t, active.View.Reps, 1)
	assert.Equal(t, second.ID, active.View.Reps[0].ID)

	all, err := r.ListReps(ListRepsRequest{IncludeRetired: true})
	require.NoError(t, err)
	assert.Equal(t, 2, all.View.Count, "the audit view keeps the retired rep")
	byID := map[string]rep.Rep{}
	for _, e := range all.View.Reps {
		byID[e.ID] = e
	}
	assert.Equal(t, rep.StateRetired, byID[first.ID].State)
	assert.Equal(t, rep.StateActive, byID[second.ID].State)
	assert.Contains(t, strings.Join(all.Lines, "\n"), "(retired)")
}

// TestListReps_EmptyIsArrayNotNull: an empty pool marshals reps as [] so
// automation always reads an array.
func TestListReps_EmptyIsArrayNotNull(t *testing.T) {
	r := bootedReps(t)

	list, err := r.ListReps(ListRepsRequest{})
	require.NoError(t, err)
	b, err := json.Marshal(list.View)
	require.NoError(t, err)
	assert.JSONEq(t, `{"count":0,"reps":[]}`, string(b))
	require.Len(t, list.Lines, 1)
	assert.Contains(t, list.Lines[0], "No active reps")
}

// TestSurfaceRep_ColdStartLowestID: with nothing surfaced yet, selection breaks
// the all-unsurfaced tie by rep id ascending (reps.md §4).
func TestSurfaceRep_ColdStartLowestID(t *testing.T) {
	r := bootedReps(t)
	addRep(t, r, AddRepRequest{Text: "A rep", Level: 5, Now: day1()})
	addRep(t, r, AddRepRequest{Text: "B rep", Level: 4, Now: day1()})

	res, err := r.SurfaceRep(day1())
	require.NoError(t, err)
	require.NotNil(t, res.View.Rep)
	assert.Equal(t, "rep_2026_07_02_001", res.View.Rep.ID, "lowest id wins the cold-start tie")
	assert.Equal(t, "2026-07-02", res.View.Day)
	require.Len(t, res.Lines, 1, "surface prints exactly one line")
}

// TestSurfaceRep_IncludesSoloVariant: a rep with a solo variant carries it in the
// view's entry and in the prose line after `Solo:`; a rep without one shows no
// `Solo:` segment; the level shows when set. The view has no solo-day field —
// the surface never guesses whether the day is solo (reps.md §4).
func TestSurfaceRep_IncludesSoloVariant(t *testing.T) {
	r := bootedReps(t)
	addRep(t, r, AddRepRequest{
		Text: "Ask one question in a meeting", Solo: "Send the question afterward", Level: 3, Now: day1(),
	}) // rep_..._001 → day 1
	addRep(t, r, AddRepRequest{Text: "Try one new route", Now: day1()}) // rep_..._002 → day 2

	withSolo, err := r.SurfaceRep(day1())
	require.NoError(t, err)
	require.NotNil(t, withSolo.View.Rep)
	assert.Equal(t, "Send the question afterward", withSolo.View.Rep.Solo)
	require.Len(t, withSolo.Lines, 1)
	assert.Equal(t, "Ask one question in a meeting (level 3) — Solo: Send the question afterward", withSolo.Lines[0])

	without, err := r.SurfaceRep(day2())
	require.NoError(t, err)
	require.NotNil(t, without.View.Rep)
	assert.Empty(t, without.View.Rep.Solo)
	require.Len(t, without.Lines, 1)
	assert.Equal(t, "Try one new route", without.Lines[0], "unset level and solo are omitted")
	assert.NotContains(t, without.Lines[0], "Solo:")

	b, err := json.Marshal(withSolo.View)
	require.NoError(t, err)
	var keys map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &keys))
	assert.Len(t, keys, 2, "the view carries only day and rep — no solo-day field")
	assert.Contains(t, keys, "day")
	assert.Contains(t, keys, "rep")
}

// TestSurfaceRep_SyntheticClockFullCycle proves the rotation on a synthetic
// clock (reps.md §4, focus.md §4): within a logical day (04:00 rollover) every
// call returns the same pick; consecutive days differ; a 4-rep pool surfaces each
// rep exactly once before any repeats, in id-ascending cold-start order, and the
// next cycle replays that order. A retired pick never surfaces again and a fresh
// rep enters on the next day (unsurfaced first). Rotation never reads level.
func TestSurfaceRep_SyntheticClockFullCycle(t *testing.T) {
	r := bootedReps(t)
	base := time.Date(2026, 7, 2, 0, 0, 0, 0, edt)
	seedAt := base.Add(8 * time.Hour)
	pool := []AddRepRequest{
		{Text: "Practice A", Level: 5},
		{Text: "Practice B", Level: 7},
		{Text: "Practice C", Level: 4},
		{Text: "Practice D"}, // unlevelled
	}
	seeded := make([]string, 0, len(pool))
	for _, req := range pool {
		req.Now = seedAt
		seeded = append(seeded, addRep(t, r, req).ID)
	}

	// civil returns the civil day d days after base at hh:mm.
	civil := func(d, hh, mm int) time.Time {
		return time.Date(base.Year(), base.Month(), base.Day()+d, hh, mm, 0, 0, edt)
	}

	picks := make([]string, 0, 9)
	for d := 0; d <= 8; d++ {
		morning := surfaceRepID(t, r, civil(d, 9, 0))
		evening := surfaceRepID(t, r, civil(d, 21, 0))
		lateNight := surfaceRepID(t, r, civil(d+1, 3, 30)) // before 04:00 → still logical day d
		assert.Equalf(t, morning, evening, "day %d: same pick within the day", d)
		assert.Equalf(t, morning, lateNight, "day %d: 03:30 next morning is still logical day %d", d, d)
		if d > 0 {
			assert.NotEqualf(t, picks[d-1], morning, "day %d repeats day %d's pick", d, d-1)
		}
		picks = append(picks, morning)
	}

	// Days 0-3: each rep exactly once, cold start in id-ascending order.
	assert.Equal(t, seeded, picks[0:4], "the first cycle surfaces every rep once, id ascending")
	// Days 4-7: the second cycle replays the first.
	assert.Equal(t, picks[0:4], picks[4:8], "the next cycle replays the same order")
	assert.Equal(t, picks[0], picks[8], "day 8 starts the third cycle")

	// Retire day 8's pick and add a fresh rep: the fresh rep (never surfaced)
	// enters the next day, and the retired rep never surfaces again.
	retired := picks[8]
	_, err := r.RetireRep(RetireRepRequest{ID: retired, Now: civil(8, 22, 0)})
	require.NoError(t, err)
	fresh := addRep(t, r, AddRepRequest{Text: "Practice fresh", Level: 6, Now: civil(8, 22, 5)})

	assert.Equal(t, fresh.ID, surfaceRepID(t, r, civil(9, 9, 0)), "a fresh rep enters next (unsurfaced first)")
	for d := 10; d <= 16; d++ {
		assert.NotEqualf(t, retired, surfaceRepID(t, r, civil(d, 9, 0)), "day %d surfaced a retired rep", d)
	}
}

// TestSurfaceRep_SingleRepPoolRepeats: a pool of one surfaces that rep every day.
func TestSurfaceRep_SingleRepPoolRepeats(t *testing.T) {
	r := bootedReps(t)
	only := addRep(t, r, AddRepRequest{Text: "Only rep", Now: day1()})

	assert.Equal(t, only.ID, surfaceRepID(t, r, day1()))
	assert.Equal(t, only.ID, surfaceRepID(t, r, day2()))
	assert.Equal(t, only.ID, surfaceRepID(t, r, day3()))
}

// TestSurfaceRep_EmptyPool: nothing to surface is a clean nil pick with the add
// hint, never an error — including a pool whose every rep is retired.
func TestSurfaceRep_EmptyPool(t *testing.T) {
	r := bootedReps(t)

	res, err := r.SurfaceRep(day1())
	require.NoError(t, err)
	assert.Nil(t, res.View.Rep)
	assert.Equal(t, "2026-07-02", res.View.Day)
	require.Len(t, res.Lines, 1)
	assert.Equal(t, "No reps to surface yet — add one with `lucid rep add <text>`.", res.Lines[0])

	only := addRep(t, r, AddRepRequest{Text: "Soon retired", Now: day1()})
	_, err = r.RetireRep(RetireRepRequest{ID: only.ID, Now: day1()})
	require.NoError(t, err)
	res, err = r.SurfaceRep(day2())
	require.NoError(t, err)
	assert.Nil(t, res.View.Rep, "an all-retired pool has nothing to surface")
}

// TestRepSurface_NoAdherenceSurface: the sanctuary rule (reps.md §0) — neither the
// prose nor the JSON of surface (picked or empty) asks whether a rep was done or
// carries a streak/score, and the view's keys are exactly day and rep.
func TestRepSurface_NoAdherenceSurface(t *testing.T) {
	banned := []string{"did you", "streak", "score", "completed", "done?"}
	check := func(t *testing.T, res SurfaceRepResult) {
		t.Helper()
		b, err := json.Marshal(res.View)
		require.NoError(t, err)
		var keys map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(b, &keys))
		got := make([]string, 0, len(keys))
		for k := range keys {
			got = append(got, k)
		}
		assert.ElementsMatch(t, []string{"day", "rep"}, got)

		text := strings.ToLower(string(b) + "\n" + strings.Join(res.Lines, "\n"))
		for _, w := range banned {
			assert.NotContainsf(t, text, w, "the surface must not carry %q", w)
		}
	}

	r := bootedReps(t)
	empty, err := r.SurfaceRep(day1())
	require.NoError(t, err)
	check(t, empty)

	addRep(t, r, AddRepRequest{Text: "Ask one question in a meeting", Solo: "Send it afterward", Level: 2, Now: day1()})
	picked, err := r.SurfaceRep(day2())
	require.NoError(t, err)
	require.NotNil(t, picked.View.Rep)
	check(t, picked)
}

// TestRetireRep_RejectsUnknown: retiring an id that names no rep is a clean
// error and nothing is appended.
func TestRetireRep_RejectsUnknown(t *testing.T) {
	r := bootedReps(t)
	addRep(t, r, AddRepRequest{Text: "A rep", Now: day1()})

	_, err := r.RetireRep(RetireRepRequest{ID: "rep_2026_07_02_999", Now: day1()})
	require.Error(t, err)

	all, err := r.ListReps(ListRepsRequest{IncludeRetired: true})
	require.NoError(t, err)
	assert.Equal(t, 1, all.View.Count, "a rejected retire appends nothing")
	assert.Equal(t, rep.StateActive, all.View.Reps[0].State)
}

// TestRetireRep_RejectsDouble: retiring an already-retired rep is a clean error
// (no second marker); the first retire's ack carries the target id and its
// marker gets a receipt id of its own.
func TestRetireRep_RejectsDouble(t *testing.T) {
	r := bootedReps(t)
	first := addRep(t, r, AddRepRequest{Text: "A rep", Now: day1()})

	res, err := r.RetireRep(RetireRepRequest{ID: first.ID, Now: day1()})
	require.NoError(t, err)
	assert.Contains(t, res.Ack, first.ID)
	assert.True(t, strings.HasPrefix(res.Rep.ID, "rep_"), "the retirement event carries its own rep_ receipt id")
	assert.NotEqual(t, first.ID, res.Rep.ID)

	_, err = r.RetireRep(RetireRepRequest{ID: first.ID, Now: day2()})
	require.Error(t, err, "a second retire of the same rep is rejected")
}

// TestRetireRep_EmptyIDRejected: an empty id is a clean usage error, before the
// store is touched.
func TestRetireRep_EmptyIDRejected(t *testing.T) {
	r := bootedReps(t)
	_, err := r.RetireRep(RetireRepRequest{ID: "  ", Now: day1()})
	require.Error(t, err)
}
