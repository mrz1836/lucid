package storage

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

// wheelNow is a deterministic write timestamp for the wheel tests (EDT, shared
// `loc` from observations_test.go).
func wheelNow() time.Time { return time.Date(2026, 9, 6, 17, 12, 40, 0, loc) }

// syntheticWheel returns a full, valid snapshot with every pillar of the
// current pillar set (the nine of pillar set 2) rated score — invented test data
// only.
func syntheticWheel(score int) observations.WheelSnapshot {
	pillars := map[string]observations.PillarScore{}
	for _, key := range observations.WheelPillarKeys() {
		pillars[key] = observations.PillarScore{Score: score}
	}
	return observations.WheelSnapshot{Type: observations.WheelEventSnapshot, Pillars: pillars}
}

func newWheelStore(t *testing.T) *Adapter {
	t.Helper()
	a := New(t.TempDir())
	_, err := a.Scaffold()
	require.NoError(t, err)
	return a
}

// TestWheelSameMonthLatestWins_Storage: two appends for one month leave two
// snapshots in one file, each with its own receipt; the first is not mutated,
// and the second wins the fold — amend by append (wheel.md §4).
func TestWheelSameMonthLatestWins_Storage(t *testing.T) {
	a := newWheelStore(t)

	entry, first, err := a.AppendWheelSnapshot("2026-09", syntheticWheel(5), wheelNow())
	require.NoError(t, err)
	assert.Equal(t, "wheel_2026_09_001", first.ID)
	assert.Equal(t, wheelNow().Format(time.RFC3339), first.At, "at is the real write time")
	assert.Equal(t, observations.WheelEventSnapshot, first.Type)
	assert.Equal(t, "wheel_2026-09", entry.Key)
	assert.Len(t, entry.History, 1)

	later := wheelNow().Add(time.Hour)
	entry, second, err := a.AppendWheelSnapshot("2026-09", syntheticWheel(8), later)
	require.NoError(t, err)
	assert.Equal(t, "wheel_2026_09_002", second.ID, "the second write mints its own receipt")

	got, found, err := a.ReadWheel("2026-09")
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, got.History, 2, "both snapshots are kept in one file")
	assert.Equal(t, first.ID, got.History[0].ID)
	assert.Equal(t, 5, got.History[0].Pillars[observations.PillarHealth].Score, "the first snapshot is never mutated")
	assert.Equal(t, wheelNow().Format(time.RFC3339), got.CreatedAt)
	assert.Equal(t, later.Format(time.RFC3339), got.UpdatedAt)

	current, ok := got.Fold()
	require.True(t, ok)
	assert.Equal(t, second.ID, current.ID, "the latest snapshot wins on read")
	assert.Equal(t, 8, current.Pillars[observations.PillarHealth].Score)
	assert.Equal(t, entry.Key, got.Key)

	matches, err := filepath.Glob(filepath.Join(a.Home(), "registries", "wheel", "*.json"))
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join(a.Home(), "registries", "wheel", "wheel_2026-09.json")}, matches,
		"one file per month, named by the month key")
}

// TestWheelStorage_ReadWheelAll_EnumeratesMonths: every recorded month is read,
// oldest first; stray files and directories beside them are ignored, and a
// missing tree is an empty read.
func TestWheelStorage_ReadWheelAll_EnumeratesMonths(t *testing.T) {
	a := New(t.TempDir())
	all, err := a.ReadWheelAll()
	require.NoError(t, err)
	assert.Empty(t, all, "no tree yet is an empty read")

	for _, month := range []string{"2026-09", "2026-07", "2026-08"} {
		_, _, err = a.AppendWheelSnapshot(month, syntheticWheel(6), wheelNow())
		require.NoError(t, err)
	}
	dir := filepath.Join(a.Home(), "registries", "wheel")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.json"), []byte("{bad"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "wheel_2026-13.json"), []byte("{bad"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "wheel_2026-06.txt"), []byte("x"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "wheel_2026-05.json"), 0o700))

	all, err = a.ReadWheelAll()
	require.NoError(t, err)
	months := make([]string, 0, len(all))
	for _, e := range all {
		months = append(months, e.Month)
	}
	assert.Equal(t, []string{"2026-07", "2026-08", "2026-09"}, months)
}

// TestWheelStorage_ReadErrors: a missing month is not found (not an error), a
// corrupt month file surfaces as a parse error through both reads, and an
// unreadable directory surfaces from ReadWheelAll.
func TestWheelStorage_ReadErrors(t *testing.T) {
	a := newWheelStore(t)
	_, found, err := a.ReadWheel("2026-09")
	require.NoError(t, err)
	assert.False(t, found)

	require.NoError(t, a.ScaffoldWheel())
	path := filepath.Join(a.Home(), "registries", "wheel", "wheel_2026-09.json")
	require.NoError(t, os.WriteFile(path, []byte("{bad"), 0o600))
	_, _, err = a.ReadWheel("2026-09")
	require.ErrorContains(t, err, "parse wheel")
	_, err = a.ReadWheelAll()
	require.Error(t, err)
	_, _, err = a.AppendWheelSnapshot("2026-09", syntheticWheel(6), wheelNow())
	require.Error(t, err, "a corrupt month is never overwritten by an append")

	b := New(t.TempDir())
	require.NoError(t, os.MkdirAll(filepath.Join(b.Home(), "registries"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(b.Home(), "registries", "wheel"), []byte("x"), 0o600))
	_, err = b.ReadWheelAll()
	require.ErrorContains(t, err, "read wheel dir")
	require.ErrorContains(t, b.ScaffoldWheel(), "create wheel dir")
	_, _, err = b.AppendWheelSnapshot("2026-09", syntheticWheel(6), wheelNow())
	require.Error(t, err)
}

// TestWheelSelfRatingRequired_Storage: the adapter validates before it writes —
// a snapshot with a pillar missing or off the 1–10 scale, or a malformed month,
// is refused and nothing reaches disk.
func TestWheelSelfRatingRequired_Storage(t *testing.T) {
	a := newWheelStore(t)

	missing := syntheticWheel(6)
	delete(missing.Pillars, observations.PillarFinances)
	_, _, err := a.AppendWheelSnapshot("2026-09", missing, wheelNow())
	require.ErrorContains(t, err, "missing rating(s) for finances")

	off := syntheticWheel(11)
	_, _, err = a.AppendWheelSnapshot("2026-09", off, wheelNow())
	require.Error(t, err)

	for _, month := range []string{"2026-9", "../2026-09", "", "2026-09/x"} {
		_, _, err = a.AppendWheelSnapshot(month, syntheticWheel(6), wheelNow())
		require.ErrorContainsf(t, err, "invalid wheel month", "%q", month)
		_, _, err = a.ReadWheel(month)
		require.ErrorContainsf(t, err, "invalid wheel month", "%q", month)
	}

	all, err := a.ReadWheelAll()
	require.NoError(t, err)
	assert.Empty(t, all, "no refused write reached disk")
}

// TestWheelStorage_RefusesNewerSchema: a month written by a newer build reads
// fine (read what you understand) but is not appended to, since this build
// cannot write it back faithfully (wheel.md §4 Versioning).
func TestWheelStorage_RefusesNewerSchema(t *testing.T) {
	a := newWheelStore(t)
	require.NoError(t, a.ScaffoldWheel())
	path := filepath.Join(a.Home(), "registries", "wheel", "wheel_2026-09.json")
	newer := `{"key":"wheel_2026-09","kind":"wheel","schema":3,"month":"2026-09","history":[],` +
		`"created_at":"2026-09-06T17:12:40-04:00","updated_at":"2026-09-06T17:12:40-04:00","future":true}`
	require.NoError(t, os.WriteFile(path, []byte(newer), 0o600))

	got, found, err := a.ReadWheel("2026-09")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, 3, got.Schema)

	_, _, err = a.AppendWheelSnapshot("2026-09", syntheticWheel(6), wheelNow())
	require.ErrorContains(t, err, "unsupported wheel schema")
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, newer, string(b), "a newer month is never written back")
}

// schema1WheelMonth is a raw schema-1 month file exactly as every month before
// pillar set 2 was stored: no pillar_set anywhere, the eight set-1 pillars —
// invented test data.
const schema1WheelMonth = `{
  "key": "wheel_2026-08",
  "kind": "wheel",
  "schema": 1,
  "month": "2026-08",
  "history": [
    {
      "id": "wheel_2026_08_001",
      "at": "2026-09-01T09:30:00-04:00",
      "type": "snapshot",
      "pillars": {
        "health": { "score": 5, "note": "synthetic walk note" },
        "relationships": { "score": 7, "suggested": 4 },
        "career": { "score": 6 },
        "finances": { "score": 4 },
        "growth": { "score": 7 },
        "fun": { "score": 3 },
        "environment": { "score": 6 },
        "contribution": { "score": 5 }
      },
      "vision_reviewed": true,
      "vision_reflection": "Synthetic reflection."
    }
  ],
  "created_at": "2026-09-01T09:30:00-04:00",
  "updated_at": "2026-09-01T09:30:00-04:00"
}
`

// seedSchema1Month writes [schema1WheelMonth] straight to disk and returns its
// path.
func seedSchema1Month(t *testing.T, a *Adapter) string {
	t.Helper()
	require.NoError(t, a.ScaffoldWheel())
	path := filepath.Join(a.Home(), "registries", "wheel", "wheel_2026-08.json")
	require.NoError(t, os.WriteFile(path, []byte(schema1WheelMonth), 0o600))
	return path
}

// rawWheelHistory decodes a month file's history as raw JSON objects, so a test
// can see exactly which keys a stored snapshot carries.
func rawWheelHistory(t *testing.T, b []byte) []map[string]any {
	t.Helper()
	var raw struct {
		History []map[string]any `json:"history"`
	}
	require.NoError(t, json.Unmarshal(b, &raw))
	return raw.History
}

// TestWheelStorage_StampsCurrentPillarSet: the adapter is the single writer of
// the pillar set — every append stores the current set (2) and a fresh month is
// schema 2, whatever set the caller passed; a caller cannot author a set-1
// snapshot, since an eight-pillar wheel passed as set 1 is validated as set 2
// and refused with nothing written (wheel.md §4).
func TestWheelStorage_StampsCurrentPillarSet(t *testing.T) {
	a := newWheelStore(t)

	unstamped := syntheticWheel(6)
	unstamped.PillarSet = 0
	entry, snap, err := a.AppendWheelSnapshot("2026-09", unstamped, wheelNow())
	require.NoError(t, err)
	assert.Equal(t, observations.CurrentWheelPillarSet, snap.PillarSet)
	assert.Equal(t, 2, snap.PillarSet)
	assert.Equal(t, 2, entry.Schema, "a fresh month is schema 2")

	b, err := os.ReadFile(filepath.Join(a.Home(), "registries", "wheel", "wheel_2026-09.json"))
	require.NoError(t, err)
	var stored struct {
		Schema int `json:"schema"`
	}
	require.NoError(t, json.Unmarshal(b, &stored))
	assert.Equal(t, 2, stored.Schema)
	history := rawWheelHistory(t, b)
	require.Len(t, history, 1)
	assert.InDelta(t, 2, history[0]["pillar_set"], 0, `the stored snapshot carries "pillar_set": 2`)
	pillars, ok := history[0]["pillars"].(map[string]any)
	require.True(t, ok)
	assert.Len(t, pillars, 9)

	// A caller that passes set 1 with the eight set-1 pillars is still validated
	// in the current set — refused, and nothing is written.
	set1 := observations.WheelSnapshot{Type: observations.WheelEventSnapshot, PillarSet: 1, Pillars: map[string]observations.PillarScore{}}
	keys, _ := observations.WheelPillarKeysOf(1)
	for _, key := range keys {
		set1.Pillars[key] = observations.PillarScore{Score: 6}
	}
	_, _, err = a.AppendWheelSnapshot("2026-08", set1, wheelNow())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown pillar(s) relationships")
	_, found, err := a.ReadWheel("2026-08")
	require.NoError(t, err)
	assert.False(t, found, "a refused append writes nothing — not even an empty month")

	// The same set-1 wheel beside an existing month leaves its bytes untouched.
	_, _, err = a.AppendWheelSnapshot("2026-09", set1, wheelNow().Add(time.Hour))
	require.Error(t, err)
	after, err := os.ReadFile(filepath.Join(a.Home(), "registries", "wheel", "wheel_2026-09.json"))
	require.NoError(t, err)
	assert.Equal(t, b, after, "the existing month is left byte-for-byte as it was")
}

// TestWheelStorage_AmendSet1MonthWithSet2: a schema-1 month (no pillar_set)
// amended with a set-2 snapshot keeps both in one file — the set-1 snapshot
// exactly as stored, with no pillar_set added to it — folds to the set-2 one,
// mints distinct receipts, and raises the entry to schema 2 (wheel.md §4).
func TestWheelStorage_AmendSet1MonthWithSet2(t *testing.T) {
	a := newWheelStore(t)
	path := seedSchema1Month(t, a)
	seed := rawWheelHistory(t, []byte(schema1WheelMonth))

	entry, snap, err := a.AppendWheelSnapshot("2026-08", syntheticWheel(6), wheelNow())
	require.NoError(t, err)
	assert.Equal(t, "wheel_2026_08_002", snap.ID, "the amend mints its own receipt")
	assert.Equal(t, 2, snap.PillarSet)
	assert.Equal(t, 2, entry.Schema, "the entry is raised to the schema its newest snapshot needs")

	got, found, err := a.ReadWheel("2026-08")
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, got.History, 2, "both snapshots are kept")
	assert.Equal(t, "wheel_2026_08_001", got.History[0].ID)
	assert.Equal(t, 1, got.History[0].EffectivePillarSet())
	assert.Equal(t, 2, got.Schema)
	assert.Equal(t, "2026-09-01T09:30:00-04:00", got.CreatedAt, "the month keeps its creation time")
	current, ok := got.Fold()
	require.True(t, ok)
	assert.Equal(t, "wheel_2026_08_002", current.ID, "the set-2 snapshot wins on read")
	assert.Equal(t, 2, current.EffectivePillarSet())
	assert.NotEqual(t, got.History[0].ID, current.ID, "distinct receipts")

	b, err := os.ReadFile(path)
	require.NoError(t, err)
	history := rawWheelHistory(t, b)
	require.Len(t, history, 2)
	assert.Equal(t, seed[0], history[0], "the set-1 snapshot's stored object is unchanged")
	assert.NotContains(t, history[0], "pillar_set", "no pillar_set is added to the set-1 snapshot")
	assert.InDelta(t, 2, history[1]["pillar_set"], 0)
}

// TestWheelStorage_Schema1FileUntouchedByRead: reading a schema-1 month — one
// month or the whole tree — never rewrites it: its bytes and mtime are left
// exactly as found (wheel.md §4: stored wheels are never migrated).
func TestWheelStorage_Schema1FileUntouchedByRead(t *testing.T) {
	a := newWheelStore(t)
	path := seedSchema1Month(t, a)
	old := time.Date(2026, 9, 1, 9, 30, 0, 0, loc)
	require.NoError(t, os.Chtimes(path, old, old))
	before, err := os.Stat(path)
	require.NoError(t, err)

	got, found, err := a.ReadWheel("2026-08")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, 1, got.Schema)
	snap, ok := got.Fold()
	require.True(t, ok)
	assert.Equal(t, 1, snap.EffectivePillarSet())
	assert.Equal(t, 7, snap.Pillars[observations.PillarRelationships].Score, "a set-1 month reads as recorded")
	require.NoError(t, snap.Validate(), "validated in its own set")

	all, err := a.ReadWheelAll()
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, 1, all[0].Schema)

	b, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.True(t, bytes.Equal([]byte(schema1WheelMonth), b), "the bytes are unchanged")
	after, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, before.ModTime(), after.ModTime(), "the mtime is unchanged")
}
