package storage

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

// mkdirAt replaces a path with a directory, so a later file write or read
// against that exact path fails deterministically — the seam for exercising
// the adapter's I/O-error branches without chmod flakiness.
func mkdirAt(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.RemoveAll(path))
	require.NoError(t, os.MkdirAll(path, 0o700))
}

func TestExportSeriesCSV_WriteError(t *testing.T) {
	a := newObsStore(t)
	// A directory sitting where series.csv should be forces the write to fail.
	mkdirAt(t, filepath.Join(a.projectionsDir(), seriesFileName))
	_, err := a.ExportSeriesCSV(time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC), time.UTC)
	require.Error(t, err)
}

func TestExportClinicianPacket_ConfigReadError(t *testing.T) {
	a := newObsStore(t)
	// A directory where config.json belongs makes the config read fail.
	mkdirAt(t, a.obsConfigPath())
	_, err := a.ExportClinicianPacket(time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC), time.UTC, "", false)
	require.Error(t, err, "an unreadable config aborts the packet")
}

func TestRunEnrichment_ConfigReadError(t *testing.T) {
	a := newObsStore(t)
	mkdirAt(t, a.obsConfigPath())
	_, err := a.RunEnrichment(time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC), time.UTC)
	require.Error(t, err)
}

func TestExportClinicianPacket_ExportsLogWriteError(t *testing.T) {
	a := newObsStore(t)
	// A directory at exports.log fails the disclosure-log append (read as a dir).
	mkdirAt(t, a.exportsLogPath())
	_, err := a.ExportClinicianPacket(time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC), time.UTC, "", false)
	require.Error(t, err)
}

func TestWriteCuriosityState_WriteError(t *testing.T) {
	a := newObsStore(t)
	mkdirAt(t, a.curiosityStatePath())
	err := a.WriteCuriosityState(observations.CuriosityState{Day: "2026-07-02"})
	require.Error(t, err)
}

func TestShouldShowPacketPointer_ReadError(t *testing.T) {
	a := newObsStore(t)
	mkdirAt(t, a.discoveryStatePath())
	_, err := a.ShouldShowPacketPointer(time.Now())
	require.Error(t, err)
}

func TestFetchEnrichment_AuditWriteError(t *testing.T) {
	a := enrichStore(t)
	a.SetEnrichmentFetcher(okWeatherFetcher)
	mkdirAt(t, a.enrichmentAuditPath())
	url := observations.BuildOpenMeteoURL(1.0, 2.0, "2026-07-01")
	_, err := a.FetchEnrichment(observations.EnricherWeather, url, time.Now())
	require.Error(t, err, "an unwritable audit log surfaces, never silently drops the record")
}

func TestRunEnrichment_MalformedWeatherBodyIsFailure(t *testing.T) {
	a := enrichStore(t)
	// A 200 with an unparseable body counts as a failure — no event written.
	a.SetEnrichmentFetcher(func(string) (int, []byte, error) { return 200, []byte("{not json"), nil })
	seedPlace(t, a, "place_a-river", "Lisbon", 38.72, -9.14)
	seedLocation(t, a, "2026-06-01", "place_a-river")

	rep, err := a.RunEnrichment(time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC), time.UTC)
	require.NoError(t, err)
	require.Positive(t, rep.Failures)
	require.Empty(t, weatherEventsFor(t, a, "2026-07-05"))
}

func TestReadEnrichmentAudit_ReadError(t *testing.T) {
	a := newObsStore(t)
	mkdirAt(t, a.enrichmentAuditPath())
	_, err := a.ReadEnrichmentAudit()
	require.Error(t, err)
}

func TestBuildClinicianInput_RegistryReadError(t *testing.T) {
	a := newObsStore(t)
	require.NoError(t, a.ScaffoldEngine())
	// A file where the injuries registry directory belongs fails the read.
	dir := filepath.Join(a.registriesDir(), "injuries")
	require.NoError(t, os.RemoveAll(dir))
	require.NoError(t, os.WriteFile(dir, []byte("not a dir"), 0o600))
	_, err := a.ExportClinicianPacket(time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC), time.UTC, "", false)
	require.Error(t, err)
}

func TestFetcherDefaultPath(t *testing.T) {
	// With no injected fetcher, fetcher() returns the default getter, which
	// works against a local server (no external network).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	a := newObsStore(t)
	status, body, err := a.fetcher()(srv.URL)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "ok", string(body))
}
