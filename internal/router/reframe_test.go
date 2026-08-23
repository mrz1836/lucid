package router

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/reframes"
)

// Distinct logical days for the rotation tests, all past 04:00 so each now maps
// to its own civil logical day under the shared rollover (edt/nowEDT live in
// observation_test.go).
func day1() time.Time { return time.Date(2026, 7, 2, 21, 45, 0, 0, edt) }
func day2() time.Time { return time.Date(2026, 7, 3, 21, 45, 0, 0, edt) }
func day3() time.Time { return time.Date(2026, 7, 4, 21, 45, 0, 0, edt) }

// bootedReframes returns a booted router over a fresh scaffolded Ledger.
func bootedReframes(t *testing.T) *Router {
	t.Helper()
	r := New(newScaffolded(t))
	_, err := r.Boot()
	require.NoError(t, err)
	return r
}

func addReframe(t *testing.T, r *Router, catch, flip string, now time.Time) reframes.Reframe {
	t.Helper()
	res, err := r.AddReframe(AddReframeRequest{Catch: catch, Flip: flip, Now: now})
	require.NoError(t, err)
	return res.Reframe
}

// TestAddReframe_ReturnsReceiptAndPersists: add stamps schema/source, assigns a
// reframe_ id, and the entry reads back through ListReframes.
func TestAddReframe_ReturnsReceiptAndPersists(t *testing.T) {
	r := bootedReframes(t)

	res, err := r.AddReframe(AddReframeRequest{
		Catch: "I can't do this", Flip: "I can learn this", Now: day1(),
	})
	require.NoError(t, err)
	assert.Equal(t, "reframe_2026_07_02_001", res.Reframe.ID)
	assert.Equal(t, reframes.Schema, res.Reframe.Schema)
	assert.Equal(t, reframes.SourceReframe, res.Reframe.Source)
	assert.Contains(t, res.Ack, "reframe_2026_07_02_001")

	list, err := r.ListReframes()
	require.NoError(t, err)
	assert.Equal(t, 1, list.View.Count)
	require.Len(t, list.View.Reframes, 1)
	assert.Equal(t, "I can't do this", list.View.Reframes[0].Catch)
}

// TestAddReframe_EmptyPairWritesNothing: an empty catch or flip is a clean error
// and nothing lands.
func TestAddReframe_EmptyPairWritesNothing(t *testing.T) {
	r := bootedReframes(t)

	_, err := r.AddReframe(AddReframeRequest{Catch: "", Flip: "I can learn this", Now: day1()})
	require.Error(t, err)
	_, err = r.AddReframe(AddReframeRequest{Catch: "I can't do this", Flip: "  ", Now: day1()})
	require.Error(t, err)

	list, err := r.ListReframes()
	require.NoError(t, err)
	assert.Equal(t, 0, list.View.Count)
}

// TestAddReframe_DayStrictRejectIsDayRejectedError: a bad --day is refused as a
// DayRejectedError (the type the CLI prints), writing nothing.
func TestAddReframe_DayStrictRejectIsDayRejectedError(t *testing.T) {
	r := bootedReframes(t)

	_, err := r.AddReframe(AddReframeRequest{
		Catch: "x", Flip: "y", DayArg: "@yesterdya", Now: day1(),
	})
	require.Error(t, err)
	var refused *DayRejectedError
	require.ErrorAs(t, err, &refused)

	list, err := r.ListReframes()
	require.NoError(t, err)
	assert.Equal(t, 0, list.View.Count, "a refused day writes nothing")
}

// TestSurfaceReframe_ColdStartLowestID: with nothing surfaced yet, selection
// breaks the all-unsurfaced tie by reframe id ascending (reframes.md §4).
func TestSurfaceReframe_ColdStartLowestID(t *testing.T) {
	r := bootedReframes(t)
	addReframe(t, r, "A catch", "A flip", day1())
	addReframe(t, r, "B catch", "B flip", day1())

	res, err := r.SurfaceReframe(day1())
	require.NoError(t, err)
	require.NotNil(t, res.View.Reframe)
	assert.Equal(t, "reframe_2026_07_02_001", res.View.Reframe.ID, "lowest id wins the cold-start tie")
	assert.Equal(t, "2026-07-02", res.View.Day)
	require.Len(t, res.Lines, 1, "surface prints exactly one line")
}

// TestSurfaceReframe_IdempotentWithinDay: a repeated surface the same logical day
// returns the same pick and does not advance rotation (reframes.md §4).
func TestSurfaceReframe_IdempotentWithinDay(t *testing.T) {
	r := bootedReframes(t)
	addReframe(t, r, "A catch", "A flip", day1())
	addReframe(t, r, "B catch", "B flip", day1())

	first, err := r.SurfaceReframe(day1())
	require.NoError(t, err)
	// A second same-day call at a later instant of the same logical day.
	again, err := r.SurfaceReframe(time.Date(2026, 7, 2, 23, 15, 0, 0, edt))
	require.NoError(t, err)
	require.NotNil(t, first.View.Reframe)
	require.NotNil(t, again.View.Reframe)
	assert.Equal(t, first.View.Reframe.ID, again.View.Reframe.ID, "same pick within the day")
}

// TestSurfaceReframe_RotatesAcrossDays: successive logical days rotate the
// least-recently-surfaced entry, unsurfaced-first then oldest-date-first
// (reframes.md §4).
func TestSurfaceReframe_RotatesAcrossDays(t *testing.T) {
	r := bootedReframes(t)
	addReframe(t, r, "A catch", "A flip", day1()) // reframe_..._001
	addReframe(t, r, "B catch", "B flip", day1()) // reframe_..._002

	d1, err := r.SurfaceReframe(day1())
	require.NoError(t, err)
	require.NotNil(t, d1.View.Reframe)
	assert.Equal(t, "reframe_2026_07_02_001", d1.View.Reframe.ID, "day 1 → lowest id (all unsurfaced)")

	d2, err := r.SurfaceReframe(day2())
	require.NoError(t, err)
	require.NotNil(t, d2.View.Reframe)
	assert.Equal(t, "reframe_2026_07_02_002", d2.View.Reframe.ID, "day 2 → the still-unsurfaced entry")

	d3, err := r.SurfaceReframe(day3())
	require.NoError(t, err)
	require.NotNil(t, d3.View.Reframe)
	assert.Equal(t, "reframe_2026_07_02_001", d3.View.Reframe.ID, "day 3 → back to the least-recently-surfaced")
}

// TestSurfaceReframe_EmptyPool: nothing to surface is a clean nil pick with the
// add hint, never an error.
func TestSurfaceReframe_EmptyPool(t *testing.T) {
	r := bootedReframes(t)

	res, err := r.SurfaceReframe(day1())
	require.NoError(t, err)
	assert.Nil(t, res.View.Reframe)
	assert.Equal(t, "2026-07-02", res.View.Day)
	require.Len(t, res.Lines, 1)
	assert.Contains(t, res.Lines[0], "No reframes to surface yet")
}

// TestListReframes_FoldsCorrections: a correction (refs.corrects) drops the
// superseded entry from the live pool the router lists (reframes.md §2).
func TestListReframes_FoldsCorrections(t *testing.T) {
	r := bootedReframes(t)
	original := addReframe(t, r, "I always fail", "I'm still learning", day1())

	// A correction is a new entry naming the id it supersedes. AddReframe does not
	// set refs, so append the correction through the storage adapter directly —
	// the same path a future correction UX would use.
	_, err := r.Store().AppendReframe(reframes.Reframe{
		Schema:      reframes.Schema,
		Catch:       "I always mess up",
		Flip:        "I'm still practicing",
		RecordedAt:  day2().Format(time.RFC3339),
		LogicalDate: "2026-07-03",
		Source:      reframes.SourceReframe,
		Refs:        map[string]any{"corrects": original.ID},
	})
	require.NoError(t, err)

	list, err := r.ListReframes()
	require.NoError(t, err)
	assert.Equal(t, 1, list.View.Count, "the superseded entry is folded out")
	require.Len(t, list.View.Reframes, 1)
	assert.Equal(t, "I'm still practicing", list.View.Reframes[0].Flip)
}
