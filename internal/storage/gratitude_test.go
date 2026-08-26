package storage

import (
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
