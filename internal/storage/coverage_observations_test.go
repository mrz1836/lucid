package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

// TestCovScaffoldObservations_ProjectionsMkdirFails surfaces the projections
// mkdir failure rather than leaving a half-built observations tree: a file
// sitting where projections/ must be makes MkdirAll fail with ENOTDIR.
func TestCovScaffoldObservations_ProjectionsMkdirFails(t *testing.T) {
	home := t.TempDir()
	a := New(home)
	require.NoError(t, os.WriteFile(a.projectionsDir(), []byte("not a dir"), 0o600))
	assert.Error(t, a.ScaffoldObservations())
}

// TestCovEnsureObservationsConfig_StatError: a non-ENOENT stat failure (a file
// where observations/ belongs, so config.json's parent is not a directory) is
// surfaced, never mistaken for "config missing, write a fresh one".
func TestCovEnsureObservationsConfig_StatError(t *testing.T) {
	home := t.TempDir()
	a := New(home)
	require.NoError(t, os.WriteFile(a.observationsDir(), []byte("not a dir"), 0o600))
	assert.Error(t, a.ensureObservationsConfig())
}

// TestCovEnsureObservationsConfig_WriteFails: when observations/ is unwritable
// the fresh config write surfaces its error rather than silently proceeding.
func TestCovEnsureObservationsConfig_WriteFails(t *testing.T) {
	skipIfRoot(t)
	home := t.TempDir()
	a := New(home)
	require.NoError(t, os.MkdirAll(a.observationsDir(), 0o700))
	require.NoError(t, os.Chmod(a.observationsDir(), 0o500))
	t.Cleanup(func() { _ = os.Chmod(a.observationsDir(), 0o700) })
	assert.Error(t, a.ensureObservationsConfig())
}

// TestCovAppendObservation_SeqReadError: a day file that is a directory fails
// the next-seq read, so no id is assigned and no partial line is appended.
func TestCovAppendObservation_SeqReadError(t *testing.T) {
	a := newObsStore(t)
	ev := microEvent(observations.KindPain, "2026-07-02", map[string]any{"score": 3})
	mkdirAt(t, a.obsDayPath("2026-07-02"))
	_, err := a.AppendObservation(ev)
	assert.Error(t, err)
}

// TestCovAppendObservation_AppendFsyncError: an unwritable shard makes the
// whole-line append fail, and the failure is returned rather than swallowed.
func TestCovAppendObservation_AppendFsyncError(t *testing.T) {
	skipIfRoot(t)
	a := newObsStore(t)
	shard := filepath.Dir(a.obsDayPath("2026-07-02"))
	require.NoError(t, os.MkdirAll(shard, 0o700))
	require.NoError(t, os.Chmod(shard, 0o500))
	t.Cleanup(func() { _ = os.Chmod(shard, 0o700) })
	_, err := a.AppendObservation(microEvent(observations.KindPain, "2026-07-02", map[string]any{"score": 3}))
	assert.Error(t, err)
}

// TestCovReadObservationsRange_DayReadError: a day file in the range that is a
// directory surfaces as a read error rather than being silently skipped.
func TestCovReadObservationsRange_DayReadError(t *testing.T) {
	a := newObsStore(t)
	mkdirAt(t, a.obsDayPath("2026-07-01"))
	_, err := a.ReadObservationsRange("2026-07-01", "2026-07-02")
	assert.Error(t, err)
}

// TestCovReadObservationsKind_WalkError: an unreadable subtree surfaces from
// the whole-tree walk rather than being read as an empty kind.
func TestCovReadObservationsKind_WalkError(t *testing.T) {
	skipIfRoot(t)
	a := newObsStore(t)
	sub := filepath.Join(a.observationsDir(), "2026")
	require.NoError(t, os.MkdirAll(sub, 0o700))
	require.NoError(t, os.Chmod(sub, 0o000))
	t.Cleanup(func() { _ = os.Chmod(sub, 0o700) })
	_, err := a.ReadObservationsKind(observations.KindPain)
	assert.Error(t, err)
}

// TestCovReadObservationsKind_FileReadError: an unreadable obs file surfaces
// its read error rather than being skipped like a merely-malformed line.
func TestCovReadObservationsKind_FileReadError(t *testing.T) {
	skipIfRoot(t)
	a := newObsStore(t)
	path := a.obsDayPath("2026-07-02")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(`{"kind":"pain"}`), 0o600))
	require.NoError(t, os.Chmod(path, 0o000))
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	_, err := a.ReadObservationsKind(observations.KindPain)
	assert.Error(t, err)
}

// TestCovReadRegistry_BadKind: an unknown registry kind is rejected at path
// resolution, before any file is touched.
func TestCovReadRegistry_BadKind(t *testing.T) {
	a := newObsStore(t)
	_, _, err := a.ReadRegistry("not-a-kind", "key")
	assert.Error(t, err)
}

// TestCovReadRegistryKind_MissingAndFileDir covers both the absent-tree (empty,
// no error) and the unreadable-tree (a file where the kind dir belongs) paths.
func TestCovReadRegistryKind_MissingAndFileDir(t *testing.T) {
	t.Run("missing is empty", func(t *testing.T) {
		a := New(t.TempDir())
		recs, err := a.ReadRegistryKind(observations.RegistryInjury)
		require.NoError(t, err)
		assert.Empty(t, recs)
	})
	t.Run("file where dir belongs errors", func(t *testing.T) {
		a := newObsStore(t)
		dir := filepath.Join(a.registriesDir(), "injuries")
		require.NoError(t, os.RemoveAll(dir))
		require.NoError(t, os.WriteFile(dir, []byte("not a dir"), 0o600))
		_, err := a.ReadRegistryKind(observations.RegistryInjury)
		assert.Error(t, err)
	})
}

// TestCovUpdateRegistry_ReadError: a corrupt existing record aborts the merge
// rather than being overwritten from an empty base.
func TestCovUpdateRegistry_ReadError(t *testing.T) {
	a := newObsStore(t)
	path, err := a.registryPath(observations.RegistryInjury, "inj_x")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("{bad"), 0o600))
	_, err = a.UpdateRegistry(observations.RegistryInjury, "inj_x",
		observations.RegistryPatch{DisplayName: "Knee", At: "2026-07-01T09:00:00Z"})
	assert.Error(t, err)
}

// TestCovUpdateRegistry_EnsureDirError: when the kind directory cannot be
// created the write is refused before any record lands.
func TestCovUpdateRegistry_EnsureDirError(t *testing.T) {
	skipIfRoot(t)
	a := New(t.TempDir())
	require.NoError(t, os.MkdirAll(a.registriesDir(), 0o700))
	require.NoError(t, os.Chmod(a.registriesDir(), 0o500)) // no new "injuries" child
	t.Cleanup(func() { _ = os.Chmod(a.registriesDir(), 0o700) })
	_, err := a.UpdateRegistry(observations.RegistryInjury, "inj_x",
		observations.RegistryPatch{DisplayName: "Knee", At: "2026-07-01T09:00:00Z"})
	assert.Error(t, err)
}

// TestCovUpdateRegistry_WriteError: an unwritable kind directory surfaces the
// record write failure rather than reporting a phantom success.
func TestCovUpdateRegistry_WriteError(t *testing.T) {
	skipIfRoot(t)
	a := New(t.TempDir())
	injuries := filepath.Join(a.registriesDir(), "injuries")
	require.NoError(t, os.MkdirAll(injuries, 0o700))
	require.NoError(t, os.Chmod(injuries, 0o500))
	t.Cleanup(func() { _ = os.Chmod(injuries, 0o700) })
	_, err := a.UpdateRegistry(observations.RegistryInjury, "inj_x",
		observations.RegistryPatch{DisplayName: "Knee", At: "2026-07-01T09:00:00Z"})
	assert.Error(t, err)
}

// TestCovReadDayView_NilLocDefaults: a nil location defaults to UTC and the
// join still assembles on an empty Ledger.
func TestCovReadDayView_NilLocDefaults(t *testing.T) {
	a := newObsStore(t)
	view, err := a.ReadDayView("2026-07-02", nil)
	require.NoError(t, err)
	assert.Equal(t, "2026-07-02", view.Date)
}

// TestCovReadDayView_DayReadError: a day file that is a directory aborts the
// join at the observation read.
func TestCovReadDayView_DayReadError(t *testing.T) {
	a := newObsStore(t)
	mkdirAt(t, a.obsDayPath("2026-07-02"))
	_, err := a.ReadDayView("2026-07-02", loc)
	assert.Error(t, err)
}

// TestCovReadDayView_RangeIndexError: an unreadable range index aborts the join
// rather than dropping the spanning-event lookup.
func TestCovReadDayView_RangeIndexError(t *testing.T) {
	a := newObsStore(t)
	mkdirAt(t, filepath.Join(a.projectionsDir(), rangeIndexFile))
	_, err := a.ReadDayView("2026-07-02", loc)
	assert.Error(t, err)
}

// TestCovRangeCandidatesFor_EventReadError: a spanning index entry whose start
// day file is unreadable surfaces the error rather than dropping the event.
func TestCovRangeCandidatesFor_EventReadError(t *testing.T) {
	a := newObsStore(t)
	entry, err := json.Marshal(rangeIndexEntry{ID: "obs_2026_07_01_1", Start: "2026-07-01", End: "2026-07-05"})
	require.NoError(t, err)
	require.NoError(t, appendLineFsync(filepath.Join(a.projectionsDir(), rangeIndexFile), entry))
	mkdirAt(t, a.obsDayPath("2026-07-01"))
	_, err = a.rangeCandidatesFor("2026-07-03")
	assert.Error(t, err)
}

// TestCovRawIDsForDate_ReadErrorAndSkip covers both the unreadable-shard error
// (a file where the shard belongs) and the skip of a non-entry directory entry.
func TestCovRawIDsForDate_ReadErrorAndSkip(t *testing.T) {
	t.Run("file where shard belongs errors", func(t *testing.T) {
		a := New(t.TempDir())
		require.NoError(t, os.MkdirAll(filepath.Join(a.home, rawDirName, "2026"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(a.home, rawDirName, "2026", "07"), []byte("x"), 0o600))
		_, err := a.rawIDsForDate("2026-07-02")
		assert.Error(t, err)
	})
	t.Run("subdir entry is skipped", func(t *testing.T) {
		a := New(t.TempDir())
		require.NoError(t, os.MkdirAll(filepath.Join(a.home, rawDirName, "2026", "07", "nested"), 0o700))
		ids, err := a.rawIDsForDate("2026-07-02")
		require.NoError(t, err)
		assert.Empty(t, ids)
	})
}

// --- exports.go error paths ---

// TestCovExportSeriesCSV_NilLocCorruptDay: a nil location defaults to UTC, and a
// corrupt engine day surfaces from the capacity read rather than silently
// dropping the capacity column.
func TestCovExportSeriesCSV_NilLocCorruptDay(t *testing.T) {
	a := newObsStore(t)
	require.NoError(t, a.ScaffoldEngine())
	shard := filepath.Join(a.engineDir(), "days", "2026", "07")
	require.NoError(t, os.MkdirAll(shard, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(shard, "day_2026_07_05.json"), []byte("{bad"), 0o600))
	_, err := a.ExportSeriesCSV(time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC), nil)
	assert.Error(t, err)
}

// TestCovExportSeriesCSV_AppendLogError: an unwritable disclosure log fails the
// export after the series file is written — the log is never skipped silently.
func TestCovExportSeriesCSV_AppendLogError(t *testing.T) {
	a := newObsStore(t)
	mkdirAt(t, a.exportsLogPath())
	_, err := a.ExportSeriesCSV(time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC), time.UTC)
	assert.Error(t, err)
}

// TestCovExport_ScaffoldFails: a file where projections/ belongs fails the lazy
// scaffold both exports open with.
func TestCovExport_ScaffoldFails(t *testing.T) {
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	t.Run("series", func(t *testing.T) {
		a := New(t.TempDir())
		require.NoError(t, os.WriteFile(a.projectionsDir(), []byte("x"), 0o600))
		_, err := a.ExportSeriesCSV(now, time.UTC)
		assert.Error(t, err)
	})
	t.Run("clinician", func(t *testing.T) {
		a := New(t.TempDir())
		require.NoError(t, os.WriteFile(a.projectionsDir(), []byte("x"), 0o600))
		_, err := a.ExportClinicianPacket(now, time.UTC, "", false)
		assert.Error(t, err)
	})
}

// TestCovExportClinicianPacket_BuildError: a corrupt engine day surfaces from
// the packet's in-window engine-facts read.
func TestCovExportClinicianPacket_BuildError(t *testing.T) {
	a := newObsStore(t)
	require.NoError(t, a.ScaffoldEngine())
	shard := filepath.Join(a.engineDir(), "days", "2026", "07")
	require.NoError(t, os.MkdirAll(shard, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(shard, "day_2026_07_05.json"), []byte("{bad"), 0o600))
	_, err := a.ExportClinicianPacket(time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC), time.UTC, "", false)
	assert.Error(t, err)
}

// TestCovExportClinicianPacket_AppendLogError: with --all (which skips reading
// the log for the window), an unwritable disclosure log still surfaces.
func TestCovExportClinicianPacket_AppendLogError(t *testing.T) {
	a := newObsStore(t)
	mkdirAt(t, a.exportsLogPath())
	_, err := a.ExportClinicianPacket(time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC), time.UTC, "", true)
	assert.Error(t, err)
}

// TestCovBuildClinicianInput_InWindow exercises the in-window predicate's
// branches: an event after the window end is excluded, and --all admits every
// in-range event.
func TestCovBuildClinicianInput_InWindow(t *testing.T) {
	a := newObsStore(t)
	_, err := a.AppendObservation(microEvent(observations.KindPain, "2026-07-04", map[string]any{"score": 2}))
	require.NoError(t, err)
	_, err = a.AppendObservation(microEvent(observations.KindPain, "2026-09-01", map[string]any{"score": 5}))
	require.NoError(t, err)

	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	// Windowed run: the 09-01 event is after the window end and excluded.
	res, err := a.ExportClinicianPacket(now, time.UTC, "2026-07-01", false)
	require.NoError(t, err)
	assert.NotEmpty(t, res.Path)
	// --all run: every event in range is admitted.
	res, err = a.ExportClinicianPacket(now, time.UTC, "", true)
	require.NoError(t, err)
	assert.NotEmpty(t, res.Path)
}

// TestCovExports_RegistryReadError: an unreadable injury registry surfaces from
// both the clinician packet and the InjuryContext projection.
func TestCovExports_RegistryReadError(t *testing.T) {
	skipIfRoot(t)
	a := newObsStore(t)
	injuries := filepath.Join(a.registriesDir(), "injuries")
	require.NoError(t, os.Chmod(injuries, 0o000))
	t.Cleanup(func() { _ = os.Chmod(injuries, 0o700) })

	_, err := a.ExportClinicianPacket(time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC), time.UTC, "", false)
	require.Error(t, err)
	_, err = a.InjuryContext()
	require.Error(t, err)
}

// TestCovExportClinicianPacket_WriteProjectionError: an unwritable projections
// dir surfaces the packet write failure.
func TestCovExportClinicianPacket_WriteProjectionError(t *testing.T) {
	skipIfRoot(t)
	a := newObsStore(t)
	require.NoError(t, os.Chmod(a.projectionsDir(), 0o500))
	t.Cleanup(func() { _ = os.Chmod(a.projectionsDir(), 0o700) })
	_, err := a.ExportClinicianPacket(time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC), time.UTC, "", true)
	assert.Error(t, err)
}

// TestCovLastClinicianExport_SkipsNonClinician: a series disclosure line is
// skipped when resolving the since-last-export window for a clinician packet.
func TestCovLastClinicianExport_SkipsNonClinician(t *testing.T) {
	a := newObsStore(t)
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	_, err := a.ExportSeriesCSV(now, time.UTC) // writes a "series" disclosure line
	require.NoError(t, err)
	res, err := a.ExportClinicianPacket(now, time.UTC, "", false)
	require.NoError(t, err)
	assert.NotEmpty(t, res.Path)
}

// --- fetch_enrichment.go error paths ---

// TestCovRunEnrichment_NilLoc: a nil location defaults to UTC and the run
// completes on an empty Ledger.
func TestCovRunEnrichment_NilLoc(t *testing.T) {
	a := newObsStore(t)
	_, err := a.RunEnrichment(time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC), nil)
	assert.NoError(t, err)
}

// TestCovEnrichment_ScaffoldFails: a file where projections/ belongs fails the
// lazy scaffold every enrichment entry point opens with.
func TestCovEnrichment_ScaffoldFails(t *testing.T) {
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	t.Run("run", func(t *testing.T) {
		a := New(t.TempDir())
		require.NoError(t, os.WriteFile(a.projectionsDir(), []byte("x"), 0o600))
		_, err := a.RunEnrichment(now, time.UTC)
		assert.Error(t, err)
	})
	t.Run("packet pointer", func(t *testing.T) {
		a := New(t.TempDir())
		require.NoError(t, os.WriteFile(a.projectionsDir(), []byte("x"), 0o600))
		_, err := a.ShouldShowPacketPointer(now)
		assert.Error(t, err)
	})
	t.Run("curiosity", func(t *testing.T) {
		a := New(t.TempDir())
		require.NoError(t, os.WriteFile(a.projectionsDir(), []byte("x"), 0o600))
		err := a.WriteCuriosityState(observations.CuriosityState{Day: "2026-07-02"})
		assert.Error(t, err)
	})
}

// TestCovRunEnrichment_DayReadError: a day file in the backfill window that is a
// directory surfaces from the per-day read.
func TestCovRunEnrichment_DayReadError(t *testing.T) {
	a := enrichStore(t)
	mkdirAt(t, a.obsDayPath("2026-07-07"))
	_, err := a.RunEnrichment(time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC), time.UTC)
	assert.Error(t, err)
}

// TestCovRunEnrichment_PlaceRegistryError: a corrupt place registry aborts the
// weather enricher (via placeCoords) rather than fetching against nothing.
func TestCovRunEnrichment_PlaceRegistryError(t *testing.T) {
	a := enrichStore(t)
	a.SetEnrichmentFetcher(okWeatherFetcher)
	seedLocation(t, a, "2026-07-01", "place_x")
	path, err := a.registryPath(observations.RegistryPlace, "place_x")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("{bad"), 0o600))
	_, err = a.RunEnrichment(time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC), time.UTC)
	assert.Error(t, err)
}

// TestCovLocationOnFile_ReadError: an unreadable observations subtree surfaces
// from the location-on-file read.
func TestCovLocationOnFile_ReadError(t *testing.T) {
	skipIfRoot(t)
	a := newObsStore(t)
	sub := filepath.Join(a.observationsDir(), "2026")
	require.NoError(t, os.MkdirAll(sub, 0o700))
	require.NoError(t, os.Chmod(sub, 0o000))
	t.Cleanup(func() { _ = os.Chmod(sub, 0o700) })
	_, err := a.LocationOnFile("2026-07-02")
	assert.Error(t, err)
}

// TestCovShouldShowPacketPointer_WriteError: an unwritable projections dir fails
// the pointer-shown write after the read reset to the zero state.
func TestCovShouldShowPacketPointer_WriteError(t *testing.T) {
	skipIfRoot(t)
	a := newObsStore(t)
	require.NoError(t, os.Chmod(a.projectionsDir(), 0o500))
	t.Cleanup(func() { _ = os.Chmod(a.projectionsDir(), 0o700) })
	_, err := a.ShouldShowPacketPointer(time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC))
	assert.Error(t, err)
}

// TestCovAppendObservation_MarshalLineError: a payload that JSON cannot render
// (a channel) passes envelope validation but fails the line marshal, so no id
// is assigned and nothing is appended.
func TestCovAppendObservation_MarshalLineError(t *testing.T) {
	a := newObsStore(t)
	ev := observations.Event{
		Schema: observations.Schema, Kind: observations.KindPain,
		RecordedAt: "2026-07-02T21:45:00-04:00", OccurredAt: "2026-07-02T21:45:00-04:00",
		OccurredAtPrecision: observations.PrecisionExact, LogicalDate: "2026-07-02",
		Source: observations.SourceMicrolog, Payload: map[string]any{"bad": make(chan int)},
	}
	_, err := a.AppendObservation(ev)
	assert.Error(t, err)
}

// TestCovAppendObservation_IndexRangeError: a multi-day range event whose
// range-index append fails (unwritable projections) surfaces the error after the
// day line landed.
func TestCovAppendObservation_IndexRangeError(t *testing.T) {
	skipIfRoot(t)
	a := newObsStore(t)
	require.NoError(t, os.Chmod(a.projectionsDir(), 0o500))
	t.Cleanup(func() { _ = os.Chmod(a.projectionsDir(), 0o700) })
	end := "2026-07-05T10:00:00-04:00"
	ev := observations.Event{
		Schema: observations.Schema, Kind: observations.KindPain,
		RecordedAt: "2026-07-01T10:00:00-04:00", OccurredAt: "2026-07-01T10:00:00-04:00",
		OccurredAtPrecision: observations.PrecisionRange, OccurredAtEnd: &end, LogicalDate: "2026-07-01",
		Source: observations.SourceMicrolog, Payload: map[string]any{"score": 3},
	}
	_, err := a.AppendObservation(ev)
	assert.Error(t, err)
}

// TestCovExportSeriesCSV_KindReadError: an unreadable observations subtree
// surfaces from the series' pain-events read.
func TestCovExportSeriesCSV_KindReadError(t *testing.T) {
	skipIfRoot(t)
	a := newObsStore(t)
	sub := filepath.Join(a.observationsDir(), "2026")
	require.NoError(t, os.MkdirAll(sub, 0o700))
	require.NoError(t, os.Chmod(sub, 0o000))
	t.Cleanup(func() { _ = os.Chmod(sub, 0o700) })
	_, err := a.ExportSeriesCSV(time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC), time.UTC)
	assert.Error(t, err)
}

// TestCovExportClinicianPacket_NilLoc: a nil location defaults to UTC and the
// packet still renders on an empty Ledger.
func TestCovExportClinicianPacket_NilLoc(t *testing.T) {
	a := newObsStore(t)
	res, err := a.ExportClinicianPacket(time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC), nil, "", false)
	require.NoError(t, err)
	assert.NotEmpty(t, res.Path)
}

// TestCovResolvePacketWindowStart_BadEnd: a malformed window end (with no prior
// clinician export to fall back on) surfaces a parse error.
func TestCovResolvePacketWindowStart_BadEnd(t *testing.T) {
	a := newObsStore(t)
	_, err := a.resolvePacketWindowStart("not-a-date", "", false)
	assert.Error(t, err)
}

// TestCovBuildClinicianInput_KindReadError: an unreadable observations subtree
// surfaces from the packet's in-window pain read.
func TestCovBuildClinicianInput_KindReadError(t *testing.T) {
	skipIfRoot(t)
	a := newObsStore(t)
	sub := filepath.Join(a.observationsDir(), "2026")
	require.NoError(t, os.MkdirAll(sub, 0o700))
	require.NoError(t, os.Chmod(sub, 0o000))
	t.Cleanup(func() { _ = os.Chmod(sub, 0o700) })
	_, err := a.ExportClinicianPacket(time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC), time.UTC, "", false)
	assert.Error(t, err)
}

// TestCovWriteProjection_EnsureDirError: a projection path whose parent is a
// file fails the on-demand projections-dir creation.
func TestCovWriteProjection_EnsureDirError(t *testing.T) {
	a := newObsStore(t)
	require.NoError(t, os.WriteFile(filepath.Join(a.home, "afile"), []byte("x"), 0o600))
	err := a.writeProjection(filepath.Join(a.home, "afile", "sub", "f.csv"), []byte("data"))
	assert.Error(t, err)
}

// TestCovRunEnrichment_LocationReadError: an unreadable observations subtree
// surfaces from the enrichment job's location read.
func TestCovRunEnrichment_LocationReadError(t *testing.T) {
	skipIfRoot(t)
	a := enrichStore(t)
	sub := filepath.Join(a.observationsDir(), "2026")
	require.NoError(t, os.MkdirAll(sub, 0o700))
	require.NoError(t, os.Chmod(sub, 0o000))
	t.Cleanup(func() { _ = os.Chmod(sub, 0o700) })
	_, err := a.RunEnrichment(time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC), time.UTC)
	assert.Error(t, err)
}

// TestCovRunEnricher_CalendarBadDate: a malformed date fails the calendar-frame
// payload (pure local compute, no fetch).
func TestCovRunEnricher_CalendarBadDate(t *testing.T) {
	a := newObsStore(t)
	var rep EnrichmentReport
	err := a.runEnricher(observations.EnricherCalendarFrame, "bad-date", nil,
		time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC), time.UTC, &rep)
	assert.Error(t, err)
}

// TestCovAppendContextDay_Errors covers both the event-build failure (a bad
// date) and the append failure (a day file that is a directory).
func TestCovAppendContextDay_Errors(t *testing.T) {
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	t.Run("build fails on bad date", func(t *testing.T) {
		a := newObsStore(t)
		var rep EnrichmentReport
		err := a.appendContextDay("weather", "bad-date", map[string]any{}, now, time.UTC, &rep)
		assert.Error(t, err)
	})
	t.Run("append fails", func(t *testing.T) {
		a := newObsStore(t)
		mkdirAt(t, a.obsDayPath("2026-07-02"))
		var rep EnrichmentReport
		err := a.appendContextDay("weather", "2026-07-02", map[string]any{"weekday": "Sunday"}, now, time.UTC, &rep)
		assert.Error(t, err)
	})
}
