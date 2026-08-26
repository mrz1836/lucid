package router

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

// TestGratitudeList_ReadError_IsWrappedWithIntent: a corrupt gratitude entry
// file surfaces through GratitudeList as a wrapped, intent-named error rather
// than a bare storage return (Finding 5 — the read-path sibling of the write
// wraps).
func TestGratitudeList_ReadError_IsWrappedWithIntent(t *testing.T) {
	r := bootedGratitude(t)
	res := addGratitude(t, r, "a warm house", day1())

	// Corrupt the entry's JSON so ReadGratitudeAll's per-entry decode fails.
	path := filepath.Join(r.store.Home(), "registries", "gratitude", res.Key+".json")
	require.NoError(t, os.WriteFile(path, []byte("{bad"), 0o600))

	_, err := r.GratitudeList()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not read the gratitude tally")
}

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
// seq is per-entry, not per-day, so two same-day bumps still differ. Every
// mutation kind is exercised: plain `add`, same-day `add`, `add --into`,
// `import`, and `merge`.
func TestGratitudeWritesReturnDistinctReceipts(t *testing.T) {
	r := bootedGratitude(t)

	seen := map[string]bool{}
	record := func(receipt string) {
		require.NotEmpty(t, receipt, "every mutation returns a receipt id")
		assert.False(t, seen[receipt], "receipt %q repeated", receipt)
		seen[receipt] = true
	}

	var key string
	for _, now := range []time.Time{day1(), day2(), day3()} {
		res := addGratitude(t, r, "my morning coffee", now)
		record(res.Receipt)
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
	record(a.Receipt)
	record(b.Receipt)

	// A targeted `--into` bump mints its own receipt against the same entry.
	into, err := r.AddGratitude(AddGratitudeRequest{Thing: "the first cup of the day", Into: key, Now: day2()})
	require.NoError(t, err)
	assert.Equal(t, key, into.Key)
	record(into.Receipt)

	// A one-time `import` seed mints its own receipt on a fresh entry.
	seed, err := r.ImportGratitude(ImportGratitudeRequest{
		Thing: "clean drinking water", Count: 9, First: "2026-01-01", Last: "2026-08-01", Now: day1(),
	})
	require.NoError(t, err)
	record(seed.Receipt)

	// A `merge` mints its own receipt on the destination.
	merged, err := r.MergeGratitude(GratitudeMergeRequest{Source: seed.Key, Target: key, Now: day3()})
	require.NoError(t, err)
	assert.Equal(t, key, merged.Key)
	record(merged.Receipt)

	assert.Len(t, seen, 8, "each of the eight mutations minted a distinct receipt")
}

// TestGratitudeAddInto: `add --into <id>` bumps a specific entry regardless of
// tonight's wording — count + last-date refreshed — while keeping the canonical
// display and recording the new wording into aka[]. An id naming no live entry is
// a clean error that writes nothing (AC-5).
func TestGratitudeAddInto(t *testing.T) {
	r := bootedGratitude(t)

	roof := addGratitude(t, r, "a roof over my head", day1())
	addGratitude(t, r, "my morning coffee", day1()) // a distractor entry

	// A differing wording bumps the targeted entry, not a canonical-key match.
	res, err := r.AddGratitude(AddGratitudeRequest{Thing: "my house", Into: roof.Key, Now: day2()})
	require.NoError(t, err)
	assert.False(t, res.Created, "--into bumps, it never creates")
	assert.Equal(t, roof.Key, res.Key, "the targeted entry is the one bumped")
	assert.Equal(t, 2, res.Count)
	assert.Equal(t, observations.DateString(observations.DateOf(day2())), res.Last, "the last date refreshes")
	assert.NotEqual(t, roof.Receipt, res.Receipt, "the bump mints its own receipt")

	// The canonical display stays the stored phrase; tonight's wording joins aka[].
	entry, found, err := r.store.ReadGratitude(roof.Key)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "a roof over my head", entry.DisplayName, "--into keeps the canonical wording")
	assert.Contains(t, entry.Aka, "my house", "the differing wording is recorded into aka")

	// No new entry was created from the differently-worded bump.
	list, err := r.GratitudeList()
	require.NoError(t, err)
	assert.Equal(t, 2, list.View.Count)

	// An --into id that resolves to no live entry is a clean error, nothing written.
	_, err = r.AddGratitude(AddGratitudeRequest{Thing: "my house", Into: "gratitude_does-not-exist", Now: day2()})
	require.Error(t, err)
	after, err := r.GratitudeList()
	require.NoError(t, err)
	assert.Equal(t, 2, after.View.Count, "the rejected --into wrote nothing")
}

// TestGratitudeImportExplicitCounts: `import` (seed) writes exactly one entry
// carrying a single seed event with the explicit Count/First/Last, fabricating no
// per-occurrence dates; a later nightly add accumulates on top; a bad count/date/
// span is a clean error that writes nothing (AC-10).
func TestGratitudeImportExplicitCounts(t *testing.T) {
	r := bootedGratitude(t)

	res, err := r.ImportGratitude(ImportGratitudeRequest{
		Thing: "clean drinking water", Count: 22, First: "2025-11-02", Last: "2026-08-20", Now: day1(),
	})
	require.NoError(t, err)
	assert.True(t, res.Created)
	assert.Equal(t, 22, res.Count)
	assert.Equal(t, "2025-11-02", res.First)
	assert.Equal(t, "2026-08-20", res.Last)
	assert.Contains(t, res.Receipt, "grat_2026_08_20_", "the seed receipt encodes its last date")

	// Exactly one seed event: no per-occurrence dates are fabricated.
	entry, found, err := r.store.ReadGratitude(res.Key)
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, entry.History, 1, "import writes exactly one event")
	ev := entry.History[0]
	assert.Equal(t, observations.GratitudeEventSeed, ev.Type)
	assert.Equal(t, 22, ev.Count)
	assert.Equal(t, "2025-11-02", ev.First)
	assert.Equal(t, "2026-08-20", ev.Last)
	assert.Equal(t, observations.GratitudeSourceMigration, ev.Source)
	assert.Empty(t, ev.Date, "a seed carries no single occurrence date")

	// A later nightly add accumulates on top of the seeded count.
	bump, err := r.AddGratitude(AddGratitudeRequest{Thing: "clean drinking water", Now: day2()})
	require.NoError(t, err)
	assert.Equal(t, 23, bump.Count, "a nightly add accumulates on the seed")

	// Guard rails: bad count, malformed date, inverted span each write nothing.
	for _, bad := range []ImportGratitudeRequest{
		{Thing: "shelter", Count: 0, First: "2025-11-02", Last: "2026-08-20", Now: day1()},
		{Thing: "shelter", Count: 3, First: "not-a-date", Last: "2026-08-20", Now: day1()},
		{Thing: "shelter", Count: 3, First: "2026-08-20", Last: "2025-11-02", Now: day1()},
	} {
		_, ierr := r.ImportGratitude(bad)
		require.Error(t, ierr)
	}
	list, err := r.GratitudeList()
	require.NoError(t, err)
	assert.Equal(t, 1, list.View.Count, "the rejected imports wrote nothing")
}

// TestGratitudeMerge: `merge <src> <dst>` folds the source's whole count and
// span into the target via one merge event, retains the source as an auditable
// redirect tombstone omitted from the active tally, and refuses a self-merge or a
// missing/tombstoned entry (AC-6).
func TestGratitudeMerge(t *testing.T) {
	r := bootedGratitude(t)

	dst := addGratitude(t, r, "a roof over my head", day1()) // ×1, last day1
	src := addGratitude(t, r, "my house", day2())            // ×1
	src2 := addGratitude(t, r, "my house", day3())           // ×2, first day2 last day3
	require.Equal(t, src.Key, src2.Key)
	require.NotEqual(t, dst.Key, src.Key, "the two wordings are distinct v1 entries")

	res, err := r.MergeGratitude(GratitudeMergeRequest{Source: src.Key, Target: dst.Key, Now: day3()})
	require.NoError(t, err)
	assert.Equal(t, dst.Key, res.Key, "the merge folds into the target")
	assert.Equal(t, 3, res.Count, "the target absorbs the source's whole count")
	assert.Equal(t, observations.DateString(observations.DateOf(day1())), res.First, "the span widens to the earliest date")
	assert.Equal(t, observations.DateString(observations.DateOf(day3())), res.Last, "the span widens to the latest date")
	require.NotEmpty(t, res.Receipt)
	assert.NotEqual(t, res.Key, res.Receipt, "the merge mints its own receipt, distinct from the entry id")

	// The source is auditably retained as a redirect tombstone.
	srcRec, found, err := r.store.ReadGratitude(src.Key)
	require.NoError(t, err)
	require.True(t, found, "the merged-away source is kept, never deleted")
	assert.True(t, srcRec.IsTombstone())
	assert.Equal(t, dst.Key, srcRec.RedirectTo)

	// The active tally holds only the canonical entry, at the folded count.
	list, err := r.GratitudeList()
	require.NoError(t, err)
	require.Len(t, list.View.Entries, 1, "the tombstone is omitted from the active tally")
	assert.Equal(t, dst.Key, list.View.Entries[0].ID)
	assert.Equal(t, 3, list.View.Entries[0].Count)
	assert.Contains(t, list.View.Entries[0].Aka, "my house", "the target absorbs the source's wording")

	// The fold is one auditable merge event naming the source it absorbed.
	dstRec, _, err := r.store.ReadGratitude(dst.Key)
	require.NoError(t, err)
	mergeEvents := 0
	for _, ev := range dstRec.History {
		if ev.Type == observations.GratitudeEventMerge {
			mergeEvents++
			assert.Equal(t, src.Key, ev.SourceKey)
			assert.Equal(t, 2, ev.SourceCount)
		}
	}
	assert.Equal(t, 1, mergeEvents, "exactly one merge event records the fold")

	// Error paths change nothing — and each refusal reads as clean user prose,
	// never leaking the storage: package prefix (Finding 2; the CLI prints it raw).
	_, err = r.MergeGratitude(GratitudeMergeRequest{Source: dst.Key, Target: dst.Key, Now: day3()})
	require.Error(t, err, "a self-merge is refused")
	assert.NotContains(t, err.Error(), "storage:", "the refusal reaches the user as prose, not a package leak")
	assert.Contains(t, err.Error(), "into itself")
	assert.Contains(t, err.Error(), "nothing was changed")
	_, err = r.MergeGratitude(GratitudeMergeRequest{Source: "gratitude_missing", Target: dst.Key, Now: day3()})
	require.Error(t, err, "a missing source is refused")
	assert.NotContains(t, err.Error(), "storage:")
	_, err = r.MergeGratitude(GratitudeMergeRequest{Source: dst.Key, Target: src.Key, Now: day3()})
	require.Error(t, err, "merging into a tombstone is refused")
	assert.NotContains(t, err.Error(), "storage:")
	_, err = r.MergeGratitude(GratitudeMergeRequest{Source: src.Key, Target: dst.Key, Now: day3()})
	require.Error(t, err, "merging a tombstoned source is refused")
	assert.NotContains(t, err.Error(), "storage:")

	after, err := r.GratitudeList()
	require.NoError(t, err)
	require.Len(t, after.View.Entries, 1, "the rejected merges changed nothing")
	assert.Equal(t, 3, after.View.Entries[0].Count)
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
