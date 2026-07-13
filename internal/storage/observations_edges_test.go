package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/observations"
)

func TestSaveObservationsConfig_RoundTrip(t *testing.T) {
	a := newObsStore(t)
	cfg, err := a.ReadObservationsConfig()
	require.NoError(t, err)

	cfg.KindsEnabled = append(cfg.KindsEnabled, observations.KindLocation)
	cfg.Packet.ClinicalContext = []string{"in recovery — flag anything habit-forming"}
	require.NoError(t, a.SaveObservationsConfig(cfg))

	got, err := a.ReadObservationsConfig()
	require.NoError(t, err)
	assert.Contains(t, got.KindsEnabled, observations.KindLocation)
	assert.Equal(t, []string{"in recovery — flag anything habit-forming"}, got.Packet.ClinicalContext)
}

// TestReadDayView_RawEntryIDFromEngineRecord exercises the branch that folds
// the engine day's journal raw id into the entry list (containsStr guard).
func TestReadDayView_RawEntryIDFromEngineRecord(t *testing.T) {
	a := newObsStore(t)
	require.NoError(t, a.ScaffoldEngine())
	require.NoError(t, a.WriteEngineDay(engine.DayRecord{
		DayID: "day_2026_07_02", LogicalDate: "2026-07-02", Mode: engine.ModeGreen,
		Completed: true, RawEntryID: "raw_2026_07_02_21_45",
	}))
	view, err := a.ReadDayView("2026-07-02", loc)
	require.NoError(t, err)
	require.NotNil(t, view.EngineDay)
	assert.Contains(t, view.RawEntryIDs, "raw_2026_07_02_21_45")
}

func TestReadDayView_BadDateErrors(t *testing.T) {
	a := newObsStore(t)
	require.NoError(t, a.ScaffoldEngine())
	_, err := a.ReadDayView("not-a-date", loc)
	require.Error(t, err)
}

// TestReadDayView_JoinsMediaForDay proves the day-view join surfaces media
// attachments attributed to the logical day (AC-8) and only that day: a media
// record filed under 2026-07-05 appears in that day's view and not the next
// day's. The read stays pure — nothing is written.
func TestReadDayView_JoinsMediaForDay(t *testing.T) {
	a := newObsStore(t)
	require.NoError(t, a.ScaffoldMedia())

	content := []byte("\xff\xd8\xff opaque bytes")
	rec, err := a.WriteMedia(syntheticMedia("clinic intake form", "scan.pdf", content))
	require.NoError(t, err)

	view, err := a.ReadDayView("2026-07-05", loc)
	require.NoError(t, err)
	require.Len(t, view.Media, 1, "the day view joins the day's media")
	assert.Equal(t, rec.ID, view.Media[0].ID)
	assert.Equal(t, "clinic intake form", view.Media[0].Caption)
	assert.Equal(t, hashOf(content), view.Media[0].SHA256)
	assert.Equal(t, rec.StoredPath, view.Media[0].StoredPath, "StoredPath resolves for the reader")

	other, err := a.ReadDayView("2026-07-06", loc)
	require.NoError(t, err)
	assert.Empty(t, other.Media, "media stays on its own logical day")
}

// TestReadDayView_MediaReadErrorSurfaces proves a failing media read inside the
// day-view join is surfaced, never swallowed: planting a file where the media
// day shard directory would be makes the read fail with ENOTDIR.
func TestReadDayView_MediaReadErrorSurfaces(t *testing.T) {
	a := newObsStore(t)
	require.NoError(t, a.ScaffoldMedia())

	shard := filepath.Join(a.Home(), "media", "2026", "07")
	require.NoError(t, os.MkdirAll(filepath.Dir(shard), 0o700))
	require.NoError(t, os.WriteFile(shard, []byte("not a directory"), 0o600))

	_, err := a.ReadDayView("2026-07-05", loc)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "media")
}

func TestReadObservationsRange_BadBounds(t *testing.T) {
	a := newObsStore(t)
	_, err := a.ReadObservationsRange("bad", "2026-07-02")
	require.Error(t, err)
	_, err = a.ReadObservationsRange("2026-07-01", "bad")
	require.Error(t, err)
}

func TestUpdateRegistry_CreateWithStatusAndFields(t *testing.T) {
	a := newObsStore(t)
	key, err := a.ResolveRegistryKey(observations.RegistryInjury, "left knee")
	require.NoError(t, err)
	rec, err := a.UpdateRegistry(observations.RegistryInjury, key, observations.RegistryPatch{
		DisplayName: "left knee", Status: observations.StatusManaged, At: "2026-07-02T10:00:00-04:00",
		Fields: map[string]any{"onset": "2025-11"},
	})
	require.NoError(t, err)
	assert.Equal(t, observations.StatusManaged, rec.Status)
	require.Len(t, rec.StatusHistory, 1)
	assert.Equal(t, observations.StatusManaged, rec.StatusHistory[0].Status)
	assert.Equal(t, "2025-11", rec.Fields["onset"])
}

func TestReadRegistryKind_UnknownAndEmpty(t *testing.T) {
	a := newObsStore(t)
	_, err := a.ReadRegistryKind("nope")
	require.Error(t, err)

	empty, err := a.ReadRegistryKind(observations.RegistryEra)
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestReadRegistry_CorruptFileErrors(t *testing.T) {
	a := newObsStore(t)
	path, err := a.registryPath(observations.RegistryPlace, "place_a-river")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("{ not json"), filePerm))
	_, _, err = a.ReadRegistry(observations.RegistryPlace, "place_a-river")
	require.Error(t, err)
}

// TestIndexRangeEvent_SingleDayAndBadEnd: a same-day range is not indexed, and
// an unparseable end is skipped rather than failing the append.
func TestIndexRangeEvent_SingleDayAndBadEnd(t *testing.T) {
	a := newObsStore(t)

	// Same-day range (09:00–12:30 today): lives in its own file, not the index.
	sameDayEnd := "2026-07-02T12:30:00-04:00"
	_, err := a.AppendObservation(observations.Event{
		Schema: observations.Schema, Kind: observations.KindPain,
		RecordedAt: sameDayEnd, OccurredAt: "2026-07-02T09:00:00-04:00",
		OccurredAtPrecision: observations.PrecisionRange, OccurredAtEnd: &sameDayEnd,
		LogicalDate: "2026-07-02", Source: observations.SourceMicrolog,
		Payload: map[string]any{"intensity": 8},
	})
	require.NoError(t, err)

	// A range event whose end is unparseable is still appended (just unindexed).
	badEnd := "not-a-timestamp"
	_, err = a.AppendObservation(observations.Event{
		Schema: observations.Schema, Kind: observations.KindPain,
		RecordedAt: "2026-07-02T09:00:00-04:00", OccurredAt: "2026-07-02T09:00:00-04:00",
		OccurredAtPrecision: observations.PrecisionRange, OccurredAtEnd: &badEnd,
		LogicalDate: "2026-07-02", Source: observations.SourceMicrolog,
		Payload: map[string]any{"intensity": 5},
	})
	require.NoError(t, err)

	// Neither produced a spanning candidate for the next day.
	view, err := a.ReadDayView("2026-07-03", loc)
	require.NoError(t, err)
	assert.Empty(t, view.Obs.RangeEvents)
}

func TestRawIDsForDate_MissingShardAndBadDate(t *testing.T) {
	a := newObsStore(t)
	require.NoError(t, a.ScaffoldEngine())

	// A day with no raw shard yields no entries, no error.
	ids, err := a.rawIDsForDate("2026-07-02")
	require.NoError(t, err)
	assert.Empty(t, ids)

	_, err = a.rawIDsForDate("bad")
	require.Error(t, err)
}

func TestReadObservationsKind_MissingTreeIsEmpty(t *testing.T) {
	a := New(t.TempDir()) // never scaffolded
	got, err := a.ReadObservationsKind(observations.KindPain)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestReadDayView_RawIDAlreadyPresentNotDuplicated(t *testing.T) {
	a := newObsStore(t)
	require.NoError(t, a.ScaffoldEngine())

	res, err := a.WriteRaw(RawEntry{
		RecordedAt: time.Date(2026, 7, 2, 21, 45, 0, 0, loc), OccurredAt: time.Date(2026, 7, 2, 21, 45, 0, 0, loc),
		OccurredAtPrecision: PrecisionExact, Source: "cli", Command: "/closeout", Body: "journal",
	})
	require.NoError(t, err)
	require.NoError(t, a.WriteEngineDay(engine.DayRecord{
		DayID: "day_2026_07_02", LogicalDate: "2026-07-02", Completed: true, RawEntryID: res.RawID,
	}))

	view, err := a.ReadDayView("2026-07-02", loc)
	require.NoError(t, err)
	// The journal raw id appears exactly once (the shard scan already had it).
	var n int
	for _, id := range view.RawEntryIDs {
		if id == res.RawID {
			n++
		}
	}
	assert.Equal(t, 1, n, "the engine journal id is not double-listed")
}

// TestResolveRegistryKey_OwnerFindsExisting: resolving a key for a name whose
// record already exists exercises the owner-found branch (the collision check
// sees the same referent and keeps the base key).
func TestResolveRegistryKey_OwnerFindsExisting(t *testing.T) {
	a := newObsStore(t)
	key, err := a.ResolveRegistryKey(observations.RegistryPlace, "Lisbon")
	require.NoError(t, err)
	_, err = a.UpdateRegistry(observations.RegistryPlace, key,
		observations.RegistryPatch{DisplayName: "Lisbon", At: "2026-07-02T10:00:00-04:00"})
	require.NoError(t, err)

	again, err := a.ResolveRegistryKey(observations.RegistryPlace, "Lisbon")
	require.NoError(t, err)
	assert.Equal(t, key, again, "the same referent keeps its base key after creation")
}

// TestReadRegistryKind_SkipsNonJSON: a stray non-.json entry in a registry dir
// is skipped, not parsed.
func TestReadRegistryKind_SkipsNonJSON(t *testing.T) {
	a := newObsStore(t)
	key, err := a.ResolveRegistryKey(observations.RegistryPlace, "Lisbon")
	require.NoError(t, err)
	_, err = a.UpdateRegistry(observations.RegistryPlace, key,
		observations.RegistryPatch{DisplayName: "Lisbon", At: "2026-07-02T10:00:00-04:00"})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(a.registriesDir(), "places", "README.txt"), []byte("note"), filePerm))

	all, err := a.ReadRegistryKind(observations.RegistryPlace)
	require.NoError(t, err)
	assert.Len(t, all, 1, "the stray file is skipped")
}

// TestRangeCandidates_DedupesRepeatedIndexEntry covers the seen-id guard.
func TestRangeCandidates_DedupesRepeatedIndexEntry(t *testing.T) {
	a := newObsStore(t)
	require.NoError(t, a.ScaffoldEngine())
	end := "2026-07-02T07:10:00-04:00"
	ev, err := a.AppendObservation(observations.Event{
		Schema: observations.Schema, Kind: observations.KindSleep,
		RecordedAt: end, OccurredAt: "2026-07-01T23:00:00-04:00",
		OccurredAtPrecision: observations.PrecisionRange, OccurredAtEnd: &end,
		LogicalDate: "2026-07-01", Source: observations.SourceMicrolog,
		Payload: map[string]any{"quality": 3},
	})
	require.NoError(t, err)
	// Duplicate the index line so the seen[] dedup path runs.
	dupe := `{"id":"` + ev.ID + `","start":"2026-07-01","end":"2026-07-02"}`
	require.NoError(t, appendLineFsync(filepath.Join(a.projectionsDir(), rangeIndexFile), []byte(dupe)))

	view, err := a.ReadDayView("2026-07-02", loc)
	require.NoError(t, err)
	assert.Len(t, view.Obs.RangeEvents, 1, "a repeated index entry surfaces the event once")
}

func TestReadObservationsConfig_MissingErrors(t *testing.T) {
	a := New(t.TempDir()) // never scaffolded
	_, err := a.ReadObservationsConfig()
	require.Error(t, err)
}

func TestResolveRegistryKey_UnscaffoldedErrors(t *testing.T) {
	a := New(t.TempDir())
	_, err := a.ResolveRegistryKey(observations.RegistryPlace, "Lisbon")
	require.Error(t, err, "resolving a key needs the config salt")
}

func TestAppendObservation_InvalidEnvelopeRejected(t *testing.T) {
	a := newObsStore(t)
	_, err := a.AppendObservation(observations.Event{
		Kind: observations.KindPain, RecordedAt: "x", OccurredAt: "x",
		OccurredAtPrecision: "bogus", LogicalDate: "2026-07-02", Source: observations.SourceMicrolog,
	})
	require.Error(t, err, "an invalid precision fails validation before any write")
}

func TestRangeCandidates_SkipsMalformedIndexLine(t *testing.T) {
	a := newObsStore(t)
	require.NoError(t, a.ScaffoldEngine())
	// A garbage line in the range index is skipped, not fatal.
	require.NoError(t, appendLineFsync(filepath.Join(a.projectionsDir(), rangeIndexFile), []byte("{ not json")))
	view, err := a.ReadDayView("2026-07-02", loc)
	require.NoError(t, err)
	assert.Empty(t, view.Obs.RangeEvents)
}

func TestAppendLineFsync_MkdirFailure(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), filePerm))
	// A path whose parent is a file cannot have its dir created.
	err := appendLineFsync(filepath.Join(blocker, "child.jsonl"), []byte("line"))
	require.Error(t, err)
}

func TestAppendLineFsync_OpenDirErrors(t *testing.T) {
	// The target path is an existing directory → OpenFile for append fails.
	err := appendLineFsync(t.TempDir(), []byte("line"))
	require.Error(t, err)
}

func TestReadJSONLLines_DirectoryErrors(t *testing.T) {
	// Reading a directory as a file is an error that is not "not exist".
	_, err := readJSONLLines(t.TempDir())
	require.Error(t, err)
}

// TestRangeCandidates_DanglingIndexEntrySkipped: an index line pointing at an
// id no longer in its day file is skipped (readObsEventByID not-found path).
func TestRangeCandidates_DanglingIndexEntrySkipped(t *testing.T) {
	a := newObsStore(t)
	require.NoError(t, a.ScaffoldEngine())
	require.NoError(t, appendLineFsync(filepath.Join(a.projectionsDir(), rangeIndexFile),
		[]byte(`{"id":"obs_2026_07_01_999","start":"2026-07-01","end":"2026-07-03"}`)))
	view, err := a.ReadDayView("2026-07-02", loc)
	require.NoError(t, err)
	assert.Empty(t, view.Obs.RangeEvents, "a dangling index entry surfaces nothing")
}

func TestScaffoldObservations_HomeIsFileErrors(t *testing.T) {
	dir := t.TempDir()
	homeFile := filepath.Join(dir, "home")
	require.NoError(t, os.WriteFile(homeFile, []byte("x"), filePerm))
	a := New(homeFile) // home is a file → MkdirAll fails
	require.Error(t, a.ScaffoldObservations())
}

func TestReadRegistryKind_CorruptRecordErrors(t *testing.T) {
	a := newObsStore(t)
	path, err := a.registryPath(observations.RegistryInjury, "injury_a-cedar")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("{ not json"), filePerm))
	_, err = a.ReadRegistryKind(observations.RegistryInjury)
	require.Error(t, err)
}

// TestWriteFailures_OnReadOnlyTargets covers the write-error branches when the
// target cannot be written: a read-only config file blocks the config
// overwrite, and a read-only tree blocks creating a new day shard. Skipped
// under root (which bypasses the permission bits).
func TestWriteFailures_OnReadOnlyTargets(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permission bits")
	}
	a := newObsStore(t)

	// A read-only config.json blocks SaveObservationsConfig (an overwrite
	// truncates the existing file, which needs the file to be writable).
	cfg, err := a.ReadObservationsConfig()
	require.NoError(t, err)
	require.NoError(t, os.Chmod(a.obsConfigPath(), 0o400))
	t.Cleanup(func() { _ = os.Chmod(a.obsConfigPath(), 0o600) })
	require.Error(t, a.SaveObservationsConfig(cfg))

	// A read-only observations tree blocks creating a new day shard (MkdirAll
	// of a not-yet-existing subdir in a non-writable parent fails reliably,
	// unlike creating a file in a read-only dir, which some macOS temp-dir
	// ACLs still permit).
	obsDir := a.observationsDir()
	require.NoError(t, os.Chmod(obsDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(obsDir, 0o700) })
	_, err = a.AppendObservation(microEvent(observations.KindPain, "2026-08-02", map[string]any{"intensity": 6}))
	require.Error(t, err)
}

func TestReadDayView_SurfacesSpanningFromIndex(t *testing.T) {
	a := newObsStore(t)
	require.NoError(t, a.ScaffoldEngine())
	end := "2026-07-02T07:10:00-04:00"
	_, err := a.AppendObservation(observations.Event{
		Schema: observations.Schema, Kind: observations.KindSleep,
		RecordedAt: end, OccurredAt: "2026-07-01T23:00:00-04:00",
		OccurredAtPrecision: observations.PrecisionRange, OccurredAtEnd: &end,
		LogicalDate: "2026-07-01", Source: observations.SourceMicrolog,
		Payload: map[string]any{"quality": 3},
	})
	require.NoError(t, err)

	view, err := a.ReadDayView("2026-07-02", time.UTC)
	require.NoError(t, err)
	require.Len(t, view.Obs.RangeEvents, 1)
	assert.Equal(t, observations.KindSleep, view.Obs.RangeEvents[0].Kind)
}
