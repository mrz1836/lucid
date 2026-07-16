package schedstatus

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	flywheel "github.com/mrz1836/go-flywheel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/storage"
)

// seedJobDB creates a migrated flywheel job DB at path and upserts the given
// periodics, so GatherDB (which opens read-only) has real rows to read back. The
// parent dir is created; a caller passing a space-containing dir exercises the
// read-only DSN's path encoding.
func seedJobDB(t *testing.T, path string, specs ...flywheel.PeriodicSpec) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	require.NoError(t, flywheel.Migrate(db))
	for _, s := range specs {
		require.NoError(t, flywheel.UpsertPeriodic(context.Background(), db, s))
		// A fresh insert's active column defaults true regardless of the spec, so
		// an intended-inactive periodic must be deactivated explicitly (mirroring
		// how the daemon suppresses the bell).
		if !s.Active {
			require.NoError(t, flywheel.SetPeriodicActive(context.Background(), db, s.Slug, false))
		}
	}
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
}

// seededLedger points a storage adapter at a fresh temp home with the Ledger and
// engine tree scaffolded, so chain marks and companion receipts can be read back.
func seededLedger(t *testing.T) *storage.Adapter {
	t.Helper()
	a := storage.New(t.TempDir())
	_, err := a.Scaffold()
	require.NoError(t, err)
	require.NoError(t, a.ScaffoldEngine())
	return a
}

// TestGatherDB_Missing: a path that does not exist is Missing (never a panic, and
// no file is created), so Assemble can classify a never-run scheduler.
func TestGatherDB_Missing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.db")
	in := GatherDB(path)
	assert.True(t, in.Missing, "a missing DB file is reported Missing")
	assert.Empty(t, in.Err)
	assert.Nil(t, in.Periodics)
	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr), "GatherDB must not create the missing file")
}

// TestGatherDB_ReadsPeriodics: a seeded DB (under a space-containing dir, the
// macOS default job-DB home shape) reads its periodics back with slug, cron,
// active, and present set — proving the read-only DSN encodes the path correctly.
func TestGatherDB_ReadsPeriodics(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Application Support", "lucid")
	path := filepath.Join(dir, "flywheel.db")
	seedJobDB(t, path,
		flywheel.PeriodicSpec{Slug: SlugBell, Kind: "lucid_bell", Cron: "0 19 * * *", Queue: "lucid", Active: true},
		flywheel.PeriodicSpec{Slug: SlugTripwire, Kind: "lucid_tripwire", Cron: "0 6 * * *", Queue: "lucid", Active: false},
	)

	in := GatherDB(path)
	require.Empty(t, in.Err, "a valid DB reads cleanly")
	require.False(t, in.Missing)
	require.Len(t, in.Periodics, 2)

	bell, ok := findAny(in.Periodics, SlugBell)
	require.True(t, ok)
	assert.True(t, bell.Present)
	assert.True(t, bell.Active)
	assert.Equal(t, "0 19 * * *", bell.Cron)
	assert.NotNil(t, bell.NextRun, "an active periodic carries a next-run")

	trip, ok := findAny(in.Periodics, SlugTripwire)
	require.True(t, ok)
	assert.True(t, trip.Present)
	assert.False(t, trip.Active, "an inactive periodic is read as inactive, not missing")
}

// TestGatherDB_Malformed: a file that is not a sqlite DB yields Err (malformed),
// classified as an error by Assemble — never a panic.
func TestGatherDB_Malformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "garbage.db")
	require.NoError(t, os.WriteFile(path, []byte("this is not a sqlite database"), 0o600))
	in := GatherDB(path)
	assert.False(t, in.Missing)
	assert.NotEmpty(t, in.Err, "an unreadable/malformed DB is reported via Err")
}

// TestGatherDB_Directory: a directory path is reported as an error, not opened as
// a DB.
func TestGatherDB_Directory(t *testing.T) {
	dir := t.TempDir()
	in := GatherDB(dir)
	assert.NotEmpty(t, in.Err)
	assert.Contains(t, in.Err, "directory")
}

// TestGatherReceipts: a written morning receipt reads back present+verified while
// the never-fired night window is not present ("no receipt yet").
func TestGatherReceipts(t *testing.T) {
	a := seededLedger(t)
	require.NoError(t, a.WriteCompanionReceipt(storage.CompanionReceipt{
		Date: "2026-07-16", Window: windowMorning, MessageID: "111", ChannelID: "c",
		Verified: true, DeliveredAt: "2026-07-16T06:00:05Z",
	}))

	got := GatherReceipts(a)
	require.Len(t, got, 2)

	morning := receiptByWindow(t, got, windowMorning)
	assert.True(t, morning.Present)
	assert.True(t, morning.Verified)
	assert.Equal(t, "2026-07-16", morning.Date)
	assert.Equal(t, "111", morning.MessageID)

	night := receiptByWindow(t, got, windowNight)
	assert.False(t, night.Present, "a never-fired window has no receipt yet")
}

// TestGatherCompanion: the projected companion info reflects enabled, the
// backend, the effective compose model (companion override wins over provider),
// and per-prompt existence — statting paths without reading their bodies.
func TestGatherCompanion(t *testing.T) {
	dir := t.TempDir()
	sys := filepath.Join(dir, "system.md")
	require.NoError(t, os.WriteFile(sys, []byte("SECRET VOICE PROMPT BODY"), 0o600))
	missing := filepath.Join(dir, "missing.md")

	cfg := config.Default()
	cfg.Provider = config.ProviderConfig{Backend: "ollama", Model: "provider-model"}
	cfg.Companion = config.CompanionConfig{
		Enabled: true, Model: "companion-model",
		SystemPrompt: sys, MorningTemplate: missing, NightTemplate: "",
	}

	info := GatherCompanion(cfg)
	assert.True(t, info.Enabled)
	assert.Equal(t, "ollama", info.ProviderBackend)
	assert.Equal(t, "companion-model", info.ProviderModel, "the companion model overrides the provider model")
	require.Len(t, info.Prompts, 3)

	assert.True(t, promptByRole(t, info.Prompts, roleSystem).Exists)
	assert.False(t, promptByRole(t, info.Prompts, roleMorning).Exists, "a configured-but-absent prompt is missing")
	assert.False(t, promptByRole(t, info.Prompts, roleNight).Exists, "an empty prompt path is missing")
}

// TestGatherCompanion_InheritsProviderModel: with no companion.model override the
// effective model is the provider's.
func TestGatherCompanion_InheritsProviderModel(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = config.ProviderConfig{Backend: "openai", Model: "gpt-x"}
	cfg.Companion = config.CompanionConfig{Enabled: false}
	info := GatherCompanion(cfg)
	assert.Equal(t, "gpt-x", info.ProviderModel)
	assert.False(t, info.Enabled)
}

// TestGatherChain: the default scaffold's chain resolves the documented bell and
// tripwire marks.
func TestGatherChain(t *testing.T) {
	a := seededLedger(t)
	marks, err := GatherChain(a)
	require.NoError(t, err)
	assert.Equal(t, "19:00", marks.BellTime)
	assert.Equal(t, "06:00", marks.TripwireTime)
}

// TestGatherChain_Missing: an unscaffolded Ledger (no chain.json) surfaces a read
// error rather than a bogus default, and Gather propagates it as a runtime error.
func TestGatherChain_Missing(t *testing.T) {
	a := storage.New(t.TempDir())
	_, err := GatherChain(a)
	require.Error(t, err)

	_, gerr := Gather(GatherParams{Config: config.Default(), Store: a})
	require.Error(t, gerr, "Gather surfaces an unreadable chain config")
}

// TestGather_AssemblesEndToEnd: Gather reads seeded state + a fake probe into
// Inputs that Assemble classifies healthy (companion enabled, all periodics
// active, both receipts verified for the elapsed windows).
func TestGather_AssemblesEndToEnd(t *testing.T) {
	a := seededLedger(t)
	// Both windows delivered "today" so no elapsed-window miss.
	now := time.Date(2026, 7, 16, 20, 0, 0, 0, time.UTC)
	today := "2026-07-16"
	require.NoError(t, a.WriteCompanionReceipt(storage.CompanionReceipt{Date: today, Window: windowMorning, MessageID: "m", Verified: true}))
	require.NoError(t, a.WriteCompanionReceipt(storage.CompanionReceipt{Date: today, Window: windowNight, MessageID: "n", Verified: true}))

	teeth := filepath.Join(t.TempDir(), "flywheel.db")
	comp := filepath.Join(t.TempDir(), "companion.db")
	seedJobDB(t, teeth,
		flywheel.PeriodicSpec{Slug: SlugTripwire, Kind: "lucid_tripwire", Cron: "0 6 * * *", Queue: "lucid", Active: true},
		flywheel.PeriodicSpec{Slug: SlugBell, Kind: "lucid_bell", Cron: "0 19 * * *", Queue: "lucid", Active: false},
	)
	seedJobDB(t, comp,
		flywheel.PeriodicSpec{Slug: SlugCompanionMorning, Kind: "lucid_companion_morning", Cron: "0 6 * * *", Queue: "lucid-companion", Active: true},
		flywheel.PeriodicSpec{Slug: SlugCompanionNight, Kind: "lucid_companion_night", Cron: "0 19 * * *", Queue: "lucid-companion", Active: true},
	)

	promptDir := t.TempDir()
	sys := filepath.Join(promptDir, "system.md")
	morn := filepath.Join(promptDir, "morning.md")
	night := filepath.Join(promptDir, "night.md")
	for _, p := range []string{sys, morn, night} {
		require.NoError(t, os.WriteFile(p, []byte("prompt body"), 0o600))
	}
	cfg := config.Default()
	cfg.Companion = config.CompanionConfig{Enabled: true, SystemPrompt: sys, MorningTemplate: morn, NightTemplate: night}

	in, err := Gather(GatherParams{
		Config: cfg, Store: a, SchedulerDB: teeth, CompanionDB: comp,
		Probe: fakeProbe{checks: []Check{okCheck("host.daemon", "up")}},
	})
	require.NoError(t, err)
	assert.True(t, in.Teeth.Periodics != nil && in.CompanionJobs.Periodics != nil)
	assert.Len(t, in.Receipts, 2)
	assert.Len(t, in.Host, 1)

	rep := Assemble(in, now)
	assert.Equal(t, string(Ok), rep.Verdict, "a fully-healthy seeded state is ok; report=%+v", rep.Checks)
}

// TestPeriodicStatuses maps flywheel views, carrying next-run / last-enqueue only
// when set.
func TestPeriodicStatuses(t *testing.T) {
	next := time.Date(2026, 7, 17, 6, 0, 0, 0, time.UTC)
	last := time.Date(2026, 7, 16, 6, 0, 0, 0, time.UTC)
	views := []flywheel.PeriodicView{
		{Slug: "a", Cron: "0 6 * * *", Active: true, NextRunAt: next, LastEnqueuedAt: &last},
		{Slug: "b", Active: false}, // zero next-run, nil last-enqueue
	}
	got := periodicStatuses(views)
	require.Len(t, got, 2)
	assert.Equal(t, "a", got[0].Slug)
	assert.True(t, got[0].Present)
	require.NotNil(t, got[0].NextRun)
	assert.Equal(t, next, *got[0].NextRun)
	require.NotNil(t, got[0].LastEnqueue)
	assert.Equal(t, last, *got[0].LastEnqueue)
	assert.Nil(t, got[1].NextRun, "a zero next-run is left nil")
	assert.Nil(t, got[1].LastEnqueue, "a nil last-enqueue stays nil")
}

// TestRunFailures maps flywheel failure views, formatting the finalized time and
// treating an empty slice as nil (the healthy case).
func TestRunFailures(t *testing.T) {
	assert.Nil(t, runFailures(nil))
	fin := time.Date(2026, 7, 16, 6, 0, 1, 0, time.UTC)
	got := runFailures([]flywheel.FailureView{
		{Kind: "lucid_companion_morning", ErrorClass: "timeout", ErrorMessage: "discord timed out", FinalizedAt: fin},
	})
	require.Len(t, got, 1)
	assert.Equal(t, "lucid_companion_morning", got[0].Kind)
	assert.Equal(t, "timeout", got[0].ErrorClass)
	assert.Equal(t, "discord timed out", got[0].Message)
	assert.Equal(t, fin.Format(time.RFC3339), got[0].FinalizedAt)
}

// TestReadonlyDSN encodes a space-containing path into a valid read-only file URI.
func TestReadonlyDSN(t *testing.T) {
	dsn := readonlyDSN("/Users/x/Library/Application Support/lucid/flywheel.db")
	assert.Contains(t, dsn, "file:")
	assert.Contains(t, dsn, "mode=ro")
	assert.Contains(t, dsn, "Application%20Support", "a space is percent-encoded, not left raw")
	assert.NotContains(t, dsn, "Application Support", "no raw space survives in the DSN")
}

// TestPromptExists covers the stat-only existence check across present, absent,
// empty, and directory paths — never reading a body.
func TestPromptExists(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "p.md")
	require.NoError(t, os.WriteFile(file, []byte("body"), 0o600))
	assert.True(t, promptExists(file))
	assert.False(t, promptExists(filepath.Join(dir, "nope.md")))
	assert.False(t, promptExists(""))
	assert.False(t, promptExists("   "))
	assert.False(t, promptExists(dir), "a directory is not a usable prompt file")
}

// ── small test lookups ───────────────────────────────────────────────────────

func findAny(ps []PeriodicStatus, slug string) (PeriodicStatus, bool) {
	for _, p := range ps {
		if p.Slug == slug {
			return p, true
		}
	}
	return PeriodicStatus{}, false
}

func receiptByWindow(t *testing.T, rs []ReceiptStatus, window string) ReceiptStatus {
	t.Helper()
	for _, r := range rs {
		if r.Window == window {
			return r
		}
	}
	t.Fatalf("no receipt for window %q", window)
	return ReceiptStatus{}
}

func promptByRole(t *testing.T, ps []PromptPath, role string) PromptPath {
	t.Helper()
	for _, p := range ps {
		if p.Role == role {
			return p
		}
	}
	t.Fatalf("no prompt for role %q", role)
	return PromptPath{}
}
