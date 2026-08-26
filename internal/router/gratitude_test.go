package router

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

// bootedGratitude returns a booted router over a fresh scaffolded Ledger.
// (edt / day1..day3 live in the sibling reframe/observation test files, same
// package.)
func bootedGratitude(t *testing.T) *Router {
	t.Helper()
	r := New(newScaffolded(t))
	_, err := r.Boot()
	require.NoError(t, err)
	return r
}

func addGratitude(t *testing.T, r *Router, thing string, now time.Time) GratitudeWriteResult {
	t.Helper()
	res, err := r.AddGratitude(AddGratitudeRequest{Thing: thing, Now: now})
	require.NoError(t, err)
	return res
}

// TestGratitudeAddAccumulates: `add` creates a new entry, a canonical-key repeat
// (a phrasing that normalizes equal) bumps that same entry's count and last
// date, and a genuinely different phrase starts its own entry — v1 matching is
// normalized-key only (AC-4).
func TestGratitudeAddAccumulates(t *testing.T) {
	r := bootedGratitude(t)

	first := addGratitude(t, r, "my morning coffee", day1())
	assert.True(t, first.Created, "the first mention creates the entry")
	assert.Equal(t, 1, first.Count)
	assert.Equal(t, observations.DateString(observations.DateOf(day1())), first.Last)

	// A normalized repeat (case + punctuation differ, letters/digits equal) lands
	// on the SAME entry and bumps the count + last date.
	second := addGratitude(t, r, "My Morning Coffee!", day2())
	assert.False(t, second.Created, "a canonical-key match amends, it does not create")
	assert.Equal(t, first.Key, second.Key, "a normalized repeat resolves to the same entry")
	assert.Equal(t, 2, second.Count)
	assert.Equal(t, observations.DateString(observations.DateOf(day2())), second.Last, "the last date refreshes")

	// A genuinely different phrase starts its own entry (no by-meaning match).
	other := addGratitude(t, r, "clean drinking water", day2())
	assert.True(t, other.Created)
	assert.NotEqual(t, first.Key, other.Key)
	assert.Equal(t, 1, other.Count)

	list, err := r.GratitudeList()
	require.NoError(t, err)
	assert.Equal(t, 2, list.View.Count, "two distinct things are tallied")
}

// TestGratitudeWritesReturnDistinctReceipts: every mutating write returns its own
// unique receipt id — distinct from the stable entry id and distinct across
// repeated writes to the SAME entry, even on the same logical day (AC-3). The
// seq is per-entry, not per-day, so two same-day bumps still differ.
func TestGratitudeWritesReturnDistinctReceipts(t *testing.T) {
	r := bootedGratitude(t)

	seen := map[string]bool{}
	var key string
	for i, now := range []time.Time{day1(), day2(), day3()} {
		res := addGratitude(t, r, "my morning coffee", now)
		require.NotEmpty(t, res.Receipt, "every write returns a receipt id")
		assert.False(t, seen[res.Receipt], "receipt %q repeated on write %d", res.Receipt, i)
		seen[res.Receipt] = true
		if key == "" {
			key = res.Key
		}
		assert.Equal(t, key, res.Key, "every write targets the same stable entry")
		assert.NotEqual(t, res.Key, res.Receipt, "the receipt id is distinct from the stable entry id")
	}

	// Two writes on the SAME logical day to the SAME entry still differ — the seq
	// is per-entry, so the receipt is unique regardless of date.
	a := addGratitude(t, r, "my morning coffee", day1())
	b := addGratitude(t, r, "my morning coffee", day1())
	assert.Equal(t, a.Key, b.Key)
	assert.NotEqual(t, a.Receipt, b.Receipt, "same-day repeats mint distinct receipts")

	assert.Len(t, seen, 3)
}

// TestGratitudeAddBackdated: `--day` files the occurrence under the backdated
// logical day (so the tally's last date reflects it), the receipt encodes that
// logical date, and a future `--day` is refused with nothing written (AC-8).
func TestGratitudeAddBackdated(t *testing.T) {
	r := bootedGratitude(t)

	// An explicit civil day in the past.
	res, err := r.AddGratitude(AddGratitudeRequest{Thing: "a walk outside", DayArg: "2026-06-01", Now: day1()})
	require.NoError(t, err)
	assert.Equal(t, "2026-06-01", res.First)
	assert.Equal(t, "2026-06-01", res.Last, "the occurrence files under the backdated day")
	assert.Contains(t, res.Receipt, "grat_2026_06_01_", "the receipt encodes the logical date, not the write time")

	// @yesterday relative to now, 04:00-rollover aware (shared capture grammar).
	res2, err := r.AddGratitude(AddGratitudeRequest{Thing: "a warm bed", DayArg: "@yesterday", Now: day3()})
	require.NoError(t, err)
	wantDay := observations.DateString(
		observations.LogicalBaseDate(day3(), observations.DefaultRolloverMin).AddDate(0, 0, -1),
	)
	assert.Equal(t, wantDay, res2.Last)

	// A future --day is a strict-tier refusal; nothing lands.
	_, err = r.AddGratitude(AddGratitudeRequest{Thing: "the future", DayArg: "2999-01-01", Now: day1()})
	require.Error(t, err)
	list, err := r.GratitudeList()
	require.NoError(t, err)
	assert.Equal(t, 2, list.View.Count, "the refused future add wrote nothing")
}

// TestGratitudeAddEmptyWritesNothing: an empty phrase is a clean usage error and
// nothing lands.
func TestGratitudeAddEmptyWritesNothing(t *testing.T) {
	r := bootedGratitude(t)

	_, err := r.AddGratitude(AddGratitudeRequest{Thing: "   ", Now: day1()})
	require.Error(t, err)

	list, err := r.GratitudeList()
	require.NoError(t, err)
	assert.Equal(t, 0, list.View.Count)
}

// TestGratitudeListSortedByCountThenRecency: `list` returns the tally sorted by
// count desc, then most-recent last desc, each row carrying its stable id (AC-7).
func TestGratitudeListSortedByCountThenRecency(t *testing.T) {
	r := bootedGratitude(t)

	// coffee ×2 (last day2), water ×2 (last day3), sunlight ×1 (last day1).
	addGratitude(t, r, "my morning coffee", day1())
	addGratitude(t, r, "my morning coffee", day2())
	addGratitude(t, r, "clean drinking water", day1())
	addGratitude(t, r, "clean drinking water", day3())
	addGratitude(t, r, "warm sunlight", day1())

	res, err := r.GratitudeList()
	require.NoError(t, err)
	require.Len(t, res.View.Entries, 3)

	// water and coffee both ×2, but water's last date is later → water first.
	assert.Equal(t, "clean drinking water", res.View.Entries[0].Thing)
	assert.Equal(t, 2, res.View.Entries[0].Count)
	assert.Equal(t, "my morning coffee", res.View.Entries[1].Thing)
	assert.Equal(t, 2, res.View.Entries[1].Count)
	// sunlight ×1 sorts last.
	assert.Equal(t, "warm sunlight", res.View.Entries[2].Thing)
	assert.Equal(t, 1, res.View.Entries[2].Count)

	for _, e := range res.View.Entries {
		assert.Contains(t, e.ID, "gratitude_", "each row carries a stable gratitude_ id")
	}
}
