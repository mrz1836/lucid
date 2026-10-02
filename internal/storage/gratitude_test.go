package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

// mergeNow is a deterministic fold timestamp for the merge tests (EDT, shared
// `loc` from observations_test.go).
func mergeNow() time.Time { return time.Date(2026, 7, 4, 21, 45, 0, 0, loc) }

// addGratitudeOccurrence tallies one occurrence of phrase on the given logical
// date and returns the resolved stable entry key.
func addGratitudeOccurrence(t *testing.T, a *Adapter, phrase, date string) string {
	t.Helper()
	key, _ := addGratitudeReceipt(t, a, phrase, date)
	return key
}

// addGratitudeReceipt tallies one occurrence of phrase on the given logical date
// and returns the resolved stable entry key and the receipt id the write minted.
func addGratitudeReceipt(t *testing.T, a *Adapter, phrase, date string) (key, receipt string) {
	t.Helper()
	key, err := a.ResolveGratitudeKey(phrase)
	require.NoError(t, err)
	ev := observations.GratitudeEvent{
		Type:   observations.GratitudeEventOccurrence,
		Date:   date,
		Source: observations.GratitudeSourceGratitude,
	}
	_, stored, err := a.AppendGratitudeEvent(key, phrase, date, true, ev, mergeNow())
	require.NoError(t, err)
	return key, stored.ID
}

// writeHistoricalGratitude writes an entry for phrase straight to disk with one
// occurrence per receipt id given — bypassing the minting path, the way an entry
// minted under the old per-entry seq sits in a Ledger today — and returns its
// key. Each occurrence's date is the one its id encodes.
func writeHistoricalGratitude(t *testing.T, a *Adapter, phrase string, ids ...string) string {
	t.Helper()
	require.NoError(t, a.ScaffoldGratitude())
	key, err := a.ResolveGratitudeKey(phrase)
	require.NoError(t, err)
	entry := observations.NewGratitudeEntry(key, phrase, "2026-04-01T21:00:00-04:00")
	for _, id := range ids {
		date, _, ok := observations.ParseGratitudeReceiptID(id)
		require.Truef(t, ok, "fixture id %q must be a well-formed receipt", id)
		entry.History = append(entry.History, observations.GratitudeEvent{
			ID:     id,
			At:     date + "T21:00:00-04:00",
			Type:   observations.GratitudeEventOccurrence,
			Date:   date,
			Source: observations.GratitudeSourceGratitude,
		})
	}
	b, err := json.MarshalIndent(entry, "", "  ")
	require.NoError(t, err)
	path := filepath.Join(a.Home(), "registries", "gratitude", key+".json")
	require.NoError(t, os.WriteFile(path, append(b, '\n'), 0o600))
	return key
}

// registryReceiptCounts reads the whole gratitude registry — live entries and
// tombstones — and counts how many times each receipt id appears.
func registryReceiptCounts(t *testing.T, a *Adapter) map[string]int {
	t.Helper()
	all, err := a.ReadGratitudeAll()
	require.NoError(t, err)
	counts := map[string]int{}
	for _, entry := range all {
		for _, ev := range entry.History {
			counts[ev.ID]++
		}
	}
	return counts
}

// TestMergeGratitude_FoldsAndTombstones: MergeGratitude folds the source's whole
// count and span into the target via one merge event and rewrites the source as a
// redirect tombstone omitted from the live set — never deleted (AC-6).
func TestMergeGratitude_FoldsAndTombstones(t *testing.T) {
	a := newObsStore(t)

	dst := addGratitudeOccurrence(t, a, "a roof over my head", "2026-07-01") // ×1
	src := addGratitudeOccurrence(t, a, "my house", "2026-07-02")
	require.Equal(t, src, addGratitudeOccurrence(t, a, "my house", "2026-07-03")) // ×2

	merged, ev, err := a.MergeGratitude(src, dst, mergeNow())
	require.NoError(t, err)

	tally := merged.Tally()
	assert.Equal(t, 3, tally.Count, "the target absorbs the source's whole count")
	assert.Equal(t, "2026-07-01", tally.First, "the span widens to the earliest date")
	assert.Equal(t, "2026-07-03", tally.Last, "the span widens to the latest date")
	assert.Equal(t, observations.GratitudeEventMerge, ev.Type)
	assert.Equal(t, src, ev.SourceKey)
	assert.Equal(t, 2, ev.SourceCount)
	assert.True(t, strings.HasPrefix(ev.ID, "grat_2026_07_04_"), "the merge receipt encodes the fold day")

	srcRec, found, err := a.ReadGratitude(src)
	require.NoError(t, err)
	require.True(t, found, "the merged-away source is retained, never deleted")
	assert.True(t, srcRec.IsTombstone())
	assert.Equal(t, dst, srcRec.RedirectTo)

	all, err := a.ReadGratitudeAll()
	require.NoError(t, err)
	live := 0
	for _, e := range all {
		if !e.IsTombstone() {
			live++
		}
	}
	assert.Equal(t, 1, live, "only the canonical entry stays live")
}

// TestMergeGratitude_StaysSingleHop: a chained merge re-points an earlier
// tombstone so the redirect graph never grows a second hop (gratitude.md §4).
func TestMergeGratitude_StaysSingleHop(t *testing.T) {
	a := newObsStore(t)

	c := addGratitudeOccurrence(t, a, "gratitude gamma", "2026-07-01")
	b := addGratitudeOccurrence(t, a, "gratitude beta", "2026-07-02")
	aKey := addGratitudeOccurrence(t, a, "gratitude alpha", "2026-07-03")

	_, _, err := a.MergeGratitude(b, aKey, mergeNow()) // b → a
	require.NoError(t, err)
	_, _, err = a.MergeGratitude(aKey, c, mergeNow()) // a → c; b must re-point to c
	require.NoError(t, err)

	bRec, found, err := a.ReadGratitude(b)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, bRec.IsTombstone())
	assert.Equal(t, c, bRec.RedirectTo, "the tombstone graph stays single-hop after a chained merge")
}

// TestMergeGratitude_Rejections: a self-merge, a missing source or target, and a
// tombstoned source or target are each a clean error that changes nothing.
func TestMergeGratitude_Rejections(t *testing.T) {
	a := newObsStore(t)

	dst := addGratitudeOccurrence(t, a, "a roof over my head", "2026-07-01")
	src := addGratitudeOccurrence(t, a, "my house", "2026-07-02")

	_, _, err := a.MergeGratitude(dst, dst, mergeNow())
	require.Error(t, err, "a self-merge is refused")
	_, _, err = a.MergeGratitude("gratitude_missing", dst, mergeNow())
	require.Error(t, err, "a missing source is refused")
	_, _, err = a.MergeGratitude(src, "gratitude_missing", mergeNow())
	require.Error(t, err, "a missing target is refused")

	_, _, err = a.MergeGratitude(src, dst, mergeNow()) // src → dst
	require.NoError(t, err)
	_, _, err = a.MergeGratitude(src, dst, mergeNow())
	require.Error(t, err, "a tombstoned source is refused")
	_, _, err = a.MergeGratitude(dst, src, mergeNow())
	require.Error(t, err, "a tombstoned target is refused")
}

// readGratitudeFile decodes the on-disk JSON of one gratitude entry, so a test
// can pin what was actually written rather than what the adapter returned.
func readGratitudeFile(t *testing.T, a *Adapter, key string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(a.Home(), "registries", "gratitude", key+".json"))
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(b, &out))
	return out
}

// TestAppendGratitudeExpressed_LinksAndIsTallyNeutral: recording that you told
// someone links the person onto the entry (once, grow-only) and appends one
// `expressed` event with its own receipt — minted under the single-writer seq,
// encoding the logical date — while the count and the span stay exactly as they
// were (gratitude.md §2, §8). The wordings are untouched and the file is written
// at schema 2.
func TestAppendGratitudeExpressed_LinksAndIsTallyNeutral(t *testing.T) {
	a := newObsStore(t)
	key := addGratitudeOccurrence(t, a, "coffee with Sam on the porch", "2026-07-01")
	addGratitudeOccurrence(t, a, "coffee with Sam on the porch", "2026-07-02")
	before, _, err := a.ReadGratitude(key)
	require.NoError(t, err)

	entry, ev, err := a.AppendGratitudeExpressed(key, "person_a-river", "2026-07-03", mergeNow())
	require.NoError(t, err)
	assert.Equal(t, "grat_2026_07_03_001", ev.ID, "the first receipt for its logical date, encoding that date")
	assert.Equal(t, observations.GratitudeEventExpressed, ev.Type)
	assert.Equal(t, "person_a-river", ev.Person)
	assert.Equal(t, "2026-07-03", ev.Date)
	assert.Equal(t, mergeNow().Format(time.RFC3339), ev.At, "the real write time")
	assert.Equal(t, []string{"person_a-river"}, entry.People)
	assert.Equal(t, before.Tally(), entry.Tally(), "expression never moves the tally")
	assert.Equal(t, before.Aka, entry.Aka, "the wordings are untouched")
	assert.Equal(t, before.DisplayName, entry.DisplayName)

	_, ev2, err := a.AppendGratitudeExpressed(key, "person_a-river", "2026-07-04", mergeNow())
	require.NoError(t, err)
	assert.NotEqual(t, ev.ID, ev2.ID, "every thank-you returns its own receipt")

	stored, found, err := a.ReadGratitude(key)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, []string{"person_a-river"}, stored.People, "a second thank-you does not re-link")
	assert.Equal(t, map[string]string{"person_a-river": "2026-07-04"}, stored.LastExpressed())
	assert.Equal(t, 2, stored.Tally().Count)
	assert.InDelta(t, 2, readGratitudeFile(t, a, key)["schema"], 0, "written at schema 2")
}

// TestAppendGratitudeExpressed_Refusals: the expressed write never creates an
// entry and never lands on a merge tombstone — a missing key, a tombstone, and a
// blank person are each a clean error that writes nothing (gratitude.md §8).
func TestAppendGratitudeExpressed_Refusals(t *testing.T) {
	a := newObsStore(t)
	dst := addGratitudeOccurrence(t, a, "a roof over my head", "2026-07-01")
	src := addGratitudeOccurrence(t, a, "the house on the hill", "2026-07-02")
	_, _, err := a.MergeGratitude(src, dst, mergeNow())
	require.NoError(t, err)

	_, _, err = a.AppendGratitudeExpressed("gratitude_z-nowhere", "person_a-river", "2026-07-03", mergeNow())
	require.ErrorContains(t, err, "no gratitude entry")
	_, found, rerr := a.ReadGratitude("gratitude_z-nowhere")
	require.NoError(t, rerr)
	assert.False(t, found, "a thank-you never creates an entry")

	tomb := readGratitudeFile(t, a, src)
	_, _, err = a.AppendGratitudeExpressed(src, "person_a-river", "2026-07-03", mergeNow())
	require.ErrorContains(t, err, "was merged into")
	assert.Equal(t, tomb, readGratitudeFile(t, a, src), "the tombstone is untouched")

	live := readGratitudeFile(t, a, dst)
	_, _, err = a.AppendGratitudeExpressed(dst, "  ", "2026-07-03", mergeNow())
	require.ErrorContains(t, err, "needs a person key")
	assert.Equal(t, live, readGratitudeFile(t, a, dst), "a blank person writes nothing")
}

// TestAppendGratitudeExpressed_OccurrencePersonLinks: an occurrence naming a
// person (`add --person`) links that person onto the entry in the same write —
// link-only: the occurrence counts +1 like any other and no expressed event is
// written (gratitude.md §3, §8).
func TestAppendGratitudeExpressed_OccurrencePersonLinks(t *testing.T) {
	a := newObsStore(t)
	key, err := a.ResolveGratitudeKey("coffee with Sam on the porch")
	require.NoError(t, err)
	ev := observations.GratitudeEvent{
		Type: observations.GratitudeEventOccurrence, Date: "2026-07-01",
		Source: observations.GratitudeSourceGratitude, Person: "person_a-river",
	}
	entry, appended, err := a.AppendGratitudeEvent(key, "coffee with Sam on the porch", "2026-07-01", true, ev, mergeNow())
	require.NoError(t, err)
	assert.Equal(t, []string{"person_a-river"}, entry.People)
	assert.Equal(t, "person_a-river", appended.Person)
	assert.Equal(t, 1, entry.Tally().Count)
	assert.Empty(t, entry.LastExpressed(), "linking is not expressing")
}

// TestGratitudeSchema_OneUpgradedOnWrite: a schema-1 entry on disk (written
// before people links existed) reads unchanged and is written back as schema 2
// on its next write, its history intact — no migration pass (gratitude.md §2
// Versioning).
func TestGratitudeSchema_OneUpgradedOnWrite(t *testing.T) {
	a := newObsStore(t)
	require.NoError(t, a.ScaffoldGratitude())
	key, err := a.ResolveGratitudeKey("my morning coffee")
	require.NoError(t, err)
	v1 := `{
  "key": "` + key + `", "kind": "gratitude", "schema": 1,
  "display_name": "my morning coffee", "aka": ["my morning coffee"],
  "history": [
    {"id": "grat_2026_03_10_001", "at": "2026-03-10T21:00:00-04:00", "type": "occurrence", "date": "2026-03-10", "source": "gratitude"}
  ],
  "redirect_to": "", "created_at": "2026-03-10T21:00:00-04:00", "updated_at": "2026-03-10T21:00:00-04:00"
}
`
	path := filepath.Join(a.Home(), "registries", "gratitude", key+".json")
	require.NoError(t, os.WriteFile(path, []byte(v1), 0o600))

	old, found, err := a.ReadGratitude(key)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, 1, old.Schema, "a schema-1 entry reads as written")
	assert.Equal(t, 1, old.Tally().Count)

	addGratitudeOccurrence(t, a, "my morning coffee", "2026-07-01")
	disk := readGratitudeFile(t, a, key)
	assert.InDelta(t, 2, disk["schema"], 0, "the next write stamps schema 2")
	assert.NotContains(t, disk, "people", "an unlinked upgrade adds no people key")
	upgraded, _, err := a.ReadGratitude(key)
	require.NoError(t, err)
	assert.Equal(t, observations.GratitudeTally{Count: 2, First: "2026-03-10", Last: "2026-07-01"}, upgraded.Tally())
	assert.Equal(t, "grat_2026_03_10_001", upgraded.History[0].ID, "the v1 history is kept verbatim")
}

// TestGratitudeExpressed_MergeCarriesPeopleAndDates: a merge carries the
// source's person links into the target's people[] (grow-only, no duplicate) and
// its last-expressed dates onto the merge event as source_expressed, so the
// target's fold of when you last told each person stays local (gratitude.md §2,
// §4). A source never told writes no source_expressed.
func TestGratitudeExpressed_MergeCarriesPeopleAndDates(t *testing.T) {
	a := newObsStore(t)
	dst := addGratitudeOccurrence(t, a, "coffee with Sam on the porch", "2026-07-01")
	src := addGratitudeOccurrence(t, a, "slow coffee outside with Sam", "2026-06-20")
	_, _, err := a.AppendGratitudeExpressed(dst, "person_a-river", "2026-07-01", mergeNow())
	require.NoError(t, err)
	_, _, err = a.AppendGratitudeExpressed(src, "person_a-river", "2026-07-02", mergeNow())
	require.NoError(t, err)
	_, _, err = a.AppendGratitudeExpressed(src, "person_b-stone", "2026-06-21", mergeNow())
	require.NoError(t, err)

	merged, ev, err := a.MergeGratitude(src, dst, mergeNow())
	require.NoError(t, err)
	assert.Equal(t, []string{"person_a-river", "person_b-stone"}, merged.People, "links absorbed, no duplicate")
	assert.Equal(t, map[string]string{"person_a-river": "2026-07-02", "person_b-stone": "2026-06-21"}, ev.SourceExpressed)
	assert.Equal(t, map[string]string{"person_a-river": "2026-07-02", "person_b-stone": "2026-06-21"}, merged.LastExpressed(),
		"the later absorbed date wins")
	assert.Equal(t, 2, merged.Tally().Count, "only the two occurrences count")

	quiet := addGratitudeOccurrence(t, a, "clean drinking water", "2026-07-01")
	other := addGratitudeOccurrence(t, a, "water that runs clean", "2026-07-02")
	_, ev, err = a.MergeGratitude(other, quiet, mergeNow())
	require.NoError(t, err)
	assert.Nil(t, ev.SourceExpressed, "a source never told carries no source_expressed")
}

// TestGratitudeReceiptDistinctAcrossEntriesSameDate: gratitudes tallied into
// different entries on the same logical date each return their own receipt — the
// per-date seq runs across the whole registry, so the second and third entries
// never repeat the first one's _001 (gratitude.md §2 Ids).
func TestGratitudeReceiptDistinctAcrossEntriesSameDate(t *testing.T) {
	a := newObsStore(t)
	const date = "2026-08-15"

	keyA, first := addGratitudeReceipt(t, a, "a warm loaf of bread", date)
	keyB, second := addGratitudeReceipt(t, a, "a quiet walk by the river", date)
	keyC, third := addGratitudeReceipt(t, a, "a letter from an old friend", date)
	require.NotEqual(t, keyA, keyB, "distinct phrases land in distinct entries")
	require.NotEqual(t, keyB, keyC, "distinct phrases land in distinct entries")
	require.NotEqual(t, keyA, keyC, "distinct phrases land in distinct entries")

	assert.NotEqual(t, first, second, "two entries on one date never share a receipt")
	assert.NotEqual(t, second, third, "two entries on one date never share a receipt")
	assert.NotEqual(t, first, third, "two entries on one date never share a receipt")
	assert.Equal(t,
		[]string{"grat_2026_08_15_001", "grat_2026_08_15_002", "grat_2026_08_15_003"},
		[]string{first, second, third},
		"the Nth receipt minted for the date across the registry")
	for _, id := range []string{first, second, third} {
		got, _, ok := observations.ParseGratitudeReceiptID(id)
		require.Truef(t, ok, "%q is a well-formed grat_<date>_<seq> receipt", id)
		assert.Equal(t, date, got, "the receipt encodes the logical date")
	}
}

// TestGratitudeReceiptUniqueAcrossRegistry: across many writes — several
// entries, several dates (some backdated), repeat bumps into one entry, and the
// expressed and merge funnels — every receipt minted is unique, and so is every
// id the registry holds afterwards, tombstone included.
func TestGratitudeReceiptUniqueAcrossRegistry(t *testing.T) {
	a := newObsStore(t)
	phrases := []string{
		"a warm loaf of bread",
		"a quiet walk by the river",
		"the smell of rain",
		"a good night's sleep",
	}
	// Out of order so later writes backdate; 2026-07-04 is the merge fold day.
	dates := []string{"2026-07-04", "2026-07-02", "2026-07-03"}

	minted := map[string]string{}
	record := func(id, what string) {
		t.Helper()
		prev, dup := minted[id]
		require.Falsef(t, dup, "receipt %s minted twice: %s, then %s", id, prev, what)
		minted[id] = what
	}

	keys := map[string]string{}
	for _, d := range dates {
		for _, p := range phrases {
			for i := range 2 { // two bumps into the same entry on the same date
				key, id := addGratitudeReceipt(t, a, p, d)
				keys[p] = key
				record(id, fmt.Sprintf("%q on %s #%d", p, d, i+1))
			}
		}
	}

	_, expressed, err := a.AppendGratitudeExpressed(keys[phrases[0]], "person_a-river", "2026-07-03", mergeNow())
	require.NoError(t, err)
	record(expressed.ID, "expressed")
	assert.Equal(t, "grat_2026_07_03_009", expressed.ID, "the expressed funnel shares the per-date seq")

	_, merge, err := a.MergeGratitude(keys[phrases[3]], keys[phrases[2]], mergeNow())
	require.NoError(t, err)
	record(merge.ID, "merge")
	assert.Equal(t, "grat_2026_07_04_009", merge.ID, "the merge funnel shares the per-date seq")

	assert.Len(t, minted, len(dates)*len(phrases)*2+2)

	counts := registryReceiptCounts(t, a)
	for id, n := range counts {
		assert.Equalf(t, 1, n, "receipt %s appears %d times across the registry", id, n)
	}
	assert.Len(t, counts, len(minted), "the registry holds exactly the receipts minted")
}

// TestGratitudeReceiptFreshDateStartsAtOne: the first receipt on a brand-new
// logical date is _001 — in an empty registry, on a registry already holding
// receipts for other dates, and on an entry whose own history is long (the seq
// is per date, never a running per-entry count).
func TestGratitudeReceiptFreshDateStartsAtOne(t *testing.T) {
	a := newObsStore(t)
	_, first := addGratitudeReceipt(t, a, "the smell of rain", "2026-06-01")
	assert.Equal(t, "grat_2026_06_01_001", first, "an empty registry starts at _001")

	for _, d := range []string{"2026-06-01", "2026-06-02", "2026-06-03"} {
		addGratitudeReceipt(t, a, "the smell of rain", d)
		addGratitudeReceipt(t, a, "a good night's sleep", d)
	}

	_, id := addGratitudeReceipt(t, a, "the smell of rain", "2026-06-04")
	assert.Equal(t, "grat_2026_06_04_001", id, "an entry with four prior events still starts a new date at _001")

	_, id = addGratitudeReceipt(t, a, "a good night's sleep", "2026-06-04")
	assert.Equal(t, "grat_2026_06_04_002", id, "the next receipt that date continues the count")

	_, id = addGratitudeReceipt(t, a, "a warm loaf of bread", "2026-05-20")
	assert.Equal(t, "grat_2026_05_20_001", id, "a backdated write to a date with no receipt starts at _001")
}

// TestGratitudeReceiptHistoricalCollisionsLoad: ids minted before receipts were
// Ledger-unique used a per-entry seq, so two entries can hold the same receipt.
// Such a Ledger still loads — every entry reads, validates, and keeps its
// history verbatim — because a historical cross-entry collision is legitimate
// append-only history, never a schema error (gratitude.md §2 Ids).
func TestGratitudeReceiptHistoricalCollisionsLoad(t *testing.T) {
	a := newObsStore(t)
	const shared = "grat_2026_04_20_003"
	keyA := writeHistoricalGratitude(t, a, "a warm loaf of bread",
		"grat_2026_04_18_001", "grat_2026_04_19_002", shared)
	keyB := writeHistoricalGratitude(t, a, "a quiet walk by the river",
		"grat_2026_04_10_001", "grat_2026_04_15_002", shared)
	require.NotEqual(t, keyA, keyB)

	for _, key := range []string{keyA, keyB} {
		entry, found, err := a.ReadGratitude(key)
		require.NoError(t, err)
		require.True(t, found)
		require.NoError(t, entry.Validate(), "a historical entry still validates")
		assert.Equal(t, 3, entry.Tally().Count)
		require.Len(t, entry.History, 3)
		assert.Equal(t, shared, entry.History[2].ID, "the colliding id is kept verbatim")
	}

	all, err := a.ReadGratitudeAll()
	require.NoError(t, err)
	require.Len(t, all, 2, "both colliding entries load")
	for _, entry := range all {
		require.NoError(t, entry.Validate(), "a cross-entry collision is not a schema error")
	}
	assert.Equal(t, 2, registryReceiptCounts(t, a)[shared], "the historical collision is left as written")
}

// TestGratitudeReceiptBackdatedAvoidsHistorical: a backdated write to a date
// that already carries historical per-entry ids mints above that date's highest
// seq across the registry, so it never reuses a historical id — not the colliding
// _003, and not the _001 a per-entry count would have handed a fresh entry.
func TestGratitudeReceiptBackdatedAvoidsHistorical(t *testing.T) {
	a := newObsStore(t)
	const date = "2026-04-20"
	keyA := writeHistoricalGratitude(t, a, "a warm loaf of bread",
		"grat_2026_04_18_001", "grat_2026_04_19_002", "grat_2026_04_20_003")
	keyB := writeHistoricalGratitude(t, a, "a quiet walk by the river",
		"grat_2026_04_10_001", "grat_2026_04_15_002", "grat_2026_04_20_003", "grat_2026_04_25_004")
	keyC := writeHistoricalGratitude(t, a, "the smell of rain",
		"grat_2026_04_20_001", "grat_2026_04_20_002")
	historical := registryReceiptCounts(t, a)

	keyD, id := addGratitudeReceipt(t, a, "a letter from an old friend", date)
	require.NotContains(t, []string{keyA, keyB, keyC}, keyD, "the write lands in a different entry")
	assert.Equal(t, "grat_2026_04_20_004", id, "above the date's historical max (003)")
	assert.NotContains(t, historical, id, "a new receipt never duplicates a historical id")

	_, id = addGratitudeReceipt(t, a, "a warm loaf of bread", date)
	assert.Equal(t, "grat_2026_04_20_005", id, "a bump into a historical entry continues the date's mark")
	assert.NotContains(t, historical, id)

	_, id = addGratitudeReceipt(t, a, "a letter from an old friend", "2026-04-25")
	assert.Equal(t, "grat_2026_04_25_005", id, "one entry's historical id sets that date's mark")
	assert.NotContains(t, historical, id)

	for rid, n := range registryReceiptCounts(t, a) {
		want := 1
		if rid == "grat_2026_04_20_003" {
			want = 2 // the grandfathered historical collision, never rewritten
		}
		assert.Equalf(t, want, n, "receipt %s appears %d times across the registry", rid, n)
	}
}
