package storage

import (
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

// syntheticWheel returns a full, valid snapshot with every pillar rated score —
// invented test data only.
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
	newer := `{"key":"wheel_2026-09","kind":"wheel","schema":2,"month":"2026-09","history":[],` +
		`"created_at":"2026-09-06T17:12:40-04:00","updated_at":"2026-09-06T17:12:40-04:00","future":true}`
	require.NoError(t, os.WriteFile(path, []byte(newer), 0o600))

	got, found, err := a.ReadWheel("2026-09")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, 2, got.Schema)

	_, _, err = a.AppendWheelSnapshot("2026-09", syntheticWheel(6), wheelNow())
	require.ErrorContains(t, err, "unsupported wheel schema")
}
