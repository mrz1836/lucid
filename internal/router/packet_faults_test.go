package router

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/storage"
)

// brokenObsRouter returns a booted router whose observations/config.json has
// been replaced by a directory, so any op that reads the observations config
// fails — the seam for exercising the router's config-read error branches.
func brokenObsRouter(t *testing.T) *Router {
	t.Helper()
	home := t.TempDir()
	a := storage.New(home)
	_, err := a.Scaffold()
	require.NoError(t, err)
	require.NoError(t, a.ScaffoldObservations())
	cfgPath := filepath.Join(home, "observations", "config.json")
	require.NoError(t, os.RemoveAll(cfgPath))
	require.NoError(t, os.Mkdir(cfgPath, 0o700))
	r := New(a)
	_, err = r.Boot() // lucid.json is intact; only the obs config is broken
	require.NoError(t, err)
	return r
}

func TestClinicianPacket_BadArg(t *testing.T) {
	r := bootedObs(t)
	_, err := r.ClinicianPacket("@bad", nowEDT())
	require.Error(t, err, "a malformed window override is rejected")
}

func TestCapture_ConfigReadError(t *testing.T) {
	r := brokenObsRouter(t)
	_, err := r.Capture(CaptureRequest{Tokens: []string{"pain", "6"}, Now: nowEDT()})
	require.Error(t, err)
}

func TestCuriosity_ConfigReadError(t *testing.T) {
	r := brokenObsRouter(t)
	_, err := r.Curiosity(nowEDT(), false)
	require.Error(t, err)
}

func TestClinicianPacket_ConfigReadError(t *testing.T) {
	r := brokenObsRouter(t)
	_, err := r.ClinicianPacket("", nowEDT())
	require.Error(t, err)
}

// TestDayView_NoteConfigReadError covers the /day path where the no-location
// annotation lookup fails: a non-empty day (a seeded engine record) forces the
// annotation lookup, which reads the broken observations config and errors.
func TestDayView_NoteConfigReadError(t *testing.T) {
	r := brokenObsRouter(t)
	require.NoError(t, r.Store().ScaffoldEngine())
	d, _ := time.Parse("2006-01-02", "2026-07-02")
	require.NoError(t, r.Store().WriteEngineDay(engine.DayRecord{
		DayID: engine.DayID(d), LogicalDate: "2026-07-02", RecordedAt: "2026-07-02T22:00:00Z",
		Mode: engine.ModeGreen, Links: map[string]string{"journal": engine.StatusDone},
		Completed: true, Profile: engine.DefaultProfile, Corrections: []engine.Correction{},
	}))
	_, err := r.DayView("2026-07-02", nowEDT())
	require.Error(t, err)
}
