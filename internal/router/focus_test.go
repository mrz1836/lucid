package router

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/focus"
)

// bootedFocus returns a booted router over a fresh scaffolded Ledger (day1/day2/
// day3 and edt live in reframe_test.go / observation_test.go — same package).
func bootedFocus(t *testing.T) *Router {
	t.Helper()
	r := New(newScaffolded(t))
	_, err := r.Boot()
	require.NoError(t, err)
	return r
}

func addFocus(t *testing.T, r *Router, text, success string, now time.Time) focus.Focus {
	t.Helper()
	res, err := r.AddFocus(AddFocusRequest{Text: text, SuccessCriterion: success, Now: now})
	require.NoError(t, err)
	return res.Focus
}

// TestAddFocus_ReturnsReceiptAndPersists: add stamps schema/source/state, assigns
// a focus_ id, keeps the success criterion verbatim, and the entry reads back
// through ListFocus.
func TestAddFocus_ReturnsReceiptAndPersists(t *testing.T) {
	r := bootedFocus(t)

	res, err := r.AddFocus(AddFocusRequest{
		Text: "Take a walk after lunch", SuccessCriterion: "I stepped outside", Now: day1(),
	})
	require.NoError(t, err)
	assert.Equal(t, "focus_2026_07_02_001", res.Focus.ID)
	assert.Equal(t, focus.Schema, res.Focus.Schema)
	assert.Equal(t, focus.SourceFocus, res.Focus.Source)
	assert.Equal(t, focus.StateActive, res.Focus.State)
	assert.Equal(t, "I stepped outside", res.Focus.SuccessCriterion)
	assert.Contains(t, res.Ack, "focus_2026_07_02_001")

	list, err := r.ListFocus(ListFocusRequest{})
	require.NoError(t, err)
	assert.Equal(t, 1, list.View.Count)
	require.Len(t, list.View.Focus, 1)
	assert.Equal(t, "Take a walk after lunch", list.View.Focus[0].Text)
	assert.Equal(t, "I stepped outside", list.View.Focus[0].SuccessCriterion)
}

// TestAddFocus_OptionalSuccessCriterion: a focus added with no success criterion
// stores an empty string (never synthesized).
func TestAddFocus_OptionalSuccessCriterion(t *testing.T) {
	r := bootedFocus(t)

	res := addFocus(t, r, "A plain work-on", "", day1())
	assert.Empty(t, res.SuccessCriterion)
}

// TestAddFocus_EmptyTextWritesNothing: an empty (or whitespace) text is a clean
// error and nothing lands.
func TestAddFocus_EmptyTextWritesNothing(t *testing.T) {
	r := bootedFocus(t)

	_, err := r.AddFocus(AddFocusRequest{Text: "", Now: day1()})
	require.Error(t, err)
	_, err = r.AddFocus(AddFocusRequest{Text: "   ", Now: day1()})
	require.Error(t, err)

	list, err := r.ListFocus(ListFocusRequest{})
	require.NoError(t, err)
	assert.Equal(t, 0, list.View.Count)
}

// TestAddFocus_DayStrictRejectIsDayRejectedError: a bad --day is refused as a
// DayRejectedError (the type the CLI prints), writing nothing.
func TestAddFocus_DayStrictRejectIsDayRejectedError(t *testing.T) {
	r := bootedFocus(t)

	_, err := r.AddFocus(AddFocusRequest{Text: "x", DayArg: "@yesterdya", Now: day1()})
	require.Error(t, err)
	var refused *DayRejectedError
	require.ErrorAs(t, err, &refused)

	list, err := r.ListFocus(ListFocusRequest{})
	require.NoError(t, err)
	assert.Equal(t, 0, list.View.Count, "a refused day writes nothing")
}

// TestListFocus_ActiveOnlyByDefaultAllIncludesRetired: retiring an item drops it
// from the default (active-only) list but keeps it in the --all audit view, with
// its state resolved to retired.
func TestListFocus_ActiveOnlyByDefaultAllIncludesRetired(t *testing.T) {
	r := bootedFocus(t)
	first := addFocus(t, r, "First work-on", "", day1())  // focus_..._001
	second := addFocus(t, r, "Second work-on", "", day1()) // focus_..._002

	_, err := r.RetireFocus(RetireFocusRequest{ID: first.ID, Now: day2()})
	require.NoError(t, err)

	active, err := r.ListFocus(ListFocusRequest{})
	require.NoError(t, err)
	assert.Equal(t, 1, active.View.Count, "the retired item drops out of the default list")
	require.Len(t, active.View.Focus, 1)
	assert.Equal(t, second.ID, active.View.Focus[0].ID)

	all, err := r.ListFocus(ListFocusRequest{IncludeRetired: true})
	require.NoError(t, err)
	assert.Equal(t, 2, all.View.Count, "the audit view keeps the retired item")
	byID := map[string]focus.Focus{}
	for _, f := range all.View.Focus {
		byID[f.ID] = f
	}
	assert.Equal(t, focus.StateRetired, byID[first.ID].State)
	assert.Equal(t, focus.StateActive, byID[second.ID].State)
}

// TestSurfaceFocus_ColdStartLowestID: with nothing surfaced yet, selection breaks
// the all-unsurfaced tie by focus id ascending (focus.md §4).
func TestSurfaceFocus_ColdStartLowestID(t *testing.T) {
	r := bootedFocus(t)
	addFocus(t, r, "A work-on", "", day1())
	addFocus(t, r, "B work-on", "", day1())

	res, err := r.SurfaceFocus(day1())
	require.NoError(t, err)
	require.NotNil(t, res.View.Focus)
	assert.Equal(t, "focus_2026_07_02_001", res.View.Focus.ID, "lowest id wins the cold-start tie")
	assert.Equal(t, "2026-07-02", res.View.Day)
	require.Len(t, res.Lines, 1, "surface prints exactly one line")
}

// TestSurfaceFocus_IdempotentWithinDay: a repeated surface the same logical day
// returns the same pick and does not advance rotation (focus.md §4).
func TestSurfaceFocus_IdempotentWithinDay(t *testing.T) {
	r := bootedFocus(t)
	addFocus(t, r, "A work-on", "", day1())
	addFocus(t, r, "B work-on", "", day1())

	first, err := r.SurfaceFocus(day1())
	require.NoError(t, err)
	again, err := r.SurfaceFocus(time.Date(2026, 7, 2, 23, 15, 0, 0, edt))
	require.NoError(t, err)
	require.NotNil(t, first.View.Focus)
	require.NotNil(t, again.View.Focus)
	assert.Equal(t, first.View.Focus.ID, again.View.Focus.ID, "same pick within the day")
}

// TestSurfaceFocus_RotatesAcrossDays: successive logical days rotate the
// least-recently-surfaced active item, unsurfaced-first then oldest-date-first
// (focus.md §4).
func TestSurfaceFocus_RotatesAcrossDays(t *testing.T) {
	r := bootedFocus(t)
	addFocus(t, r, "A work-on", "", day1()) // focus_..._001
	addFocus(t, r, "B work-on", "", day1()) // focus_..._002

	d1, err := r.SurfaceFocus(day1())
	require.NoError(t, err)
	require.NotNil(t, d1.View.Focus)
	assert.Equal(t, "focus_2026_07_02_001", d1.View.Focus.ID, "day 1 → lowest id (all unsurfaced)")

	d2, err := r.SurfaceFocus(day2())
	require.NoError(t, err)
	require.NotNil(t, d2.View.Focus)
	assert.Equal(t, "focus_2026_07_02_002", d2.View.Focus.ID, "day 2 → the still-unsurfaced item")

	d3, err := r.SurfaceFocus(day3())
	require.NoError(t, err)
	require.NotNil(t, d3.View.Focus)
	assert.Equal(t, "focus_2026_07_02_001", d3.View.Focus.ID, "day 3 → back to the least-recently-surfaced")
}

// TestSurfaceFocus_SkipsRetired: a retired item is never surfaced — the rotation
// only picks from the active pool.
func TestSurfaceFocus_SkipsRetired(t *testing.T) {
	r := bootedFocus(t)
	first := addFocus(t, r, "A work-on", "", day1()) // focus_..._001
	addFocus(t, r, "B work-on", "", day1())          // focus_..._002

	_, err := r.RetireFocus(RetireFocusRequest{ID: first.ID, Now: day1()})
	require.NoError(t, err)

	res, err := r.SurfaceFocus(day1())
	require.NoError(t, err)
	require.NotNil(t, res.View.Focus)
	assert.Equal(t, "focus_2026_07_02_002", res.View.Focus.ID, "the retired item is skipped")
}

// TestSurfaceFocus_EmptyPool: nothing to surface is a clean nil pick with the add
// hint, never an error.
func TestSurfaceFocus_EmptyPool(t *testing.T) {
	r := bootedFocus(t)

	res, err := r.SurfaceFocus(day1())
	require.NoError(t, err)
	assert.Nil(t, res.View.Focus)
	assert.Equal(t, "2026-07-02", res.View.Day)
	require.Len(t, res.Lines, 1)
	assert.Contains(t, res.Lines[0], "No focus items to surface yet")
}

// TestRetireFocus_RejectsUnknown: retiring an id that names no focus item is a
// clean error and nothing is appended.
func TestRetireFocus_RejectsUnknown(t *testing.T) {
	r := bootedFocus(t)
	addFocus(t, r, "A work-on", "", day1())

	_, err := r.RetireFocus(RetireFocusRequest{ID: "focus_2026_07_02_999", Now: day1()})
	require.Error(t, err)

	all, err := r.ListFocus(ListFocusRequest{IncludeRetired: true})
	require.NoError(t, err)
	assert.Equal(t, 1, all.View.Count, "a rejected retire appends nothing")
	assert.Equal(t, focus.StateActive, all.View.Focus[0].State)
}

// TestRetireFocus_RejectsDouble: retiring an already-retired item is a clean
// error (no second marker).
func TestRetireFocus_RejectsDouble(t *testing.T) {
	r := bootedFocus(t)
	first := addFocus(t, r, "A work-on", "", day1())

	_, err := r.RetireFocus(RetireFocusRequest{ID: first.ID, Now: day1()})
	require.NoError(t, err)
	_, err = r.RetireFocus(RetireFocusRequest{ID: first.ID, Now: day2()})
	require.Error(t, err, "a second retire of the same item is rejected")
}

// TestRetireFocus_EmptyIDRejected: an empty id is a clean usage error, before the
// store is touched.
func TestRetireFocus_EmptyIDRejected(t *testing.T) {
	r := bootedFocus(t)
	_, err := r.RetireFocus(RetireFocusRequest{ID: "  ", Now: day1()})
	require.Error(t, err)
}
