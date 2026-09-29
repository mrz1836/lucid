package storage

import (
	"encoding/json"
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
	key, err := a.ResolveGratitudeKey(phrase)
	require.NoError(t, err)
	ev := observations.GratitudeEvent{
		Type:   observations.GratitudeEventOccurrence,
		Date:   date,
		Source: observations.GratitudeSourceGratitude,
	}
	_, _, err = a.AppendGratitudeEvent(key, phrase, date, true, ev, mergeNow())
	require.NoError(t, err)
	return key
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
	assert.Equal(t, "grat_2026_07_03_003", ev.ID, "the next seq, encoding the logical date")
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
