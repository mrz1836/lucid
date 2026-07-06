package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
)

// skipIfRoot skips chmod-based failure-injection tests under root, where
// permission bits are a no-op (mirrors the raw.go disk-full tests).
func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
}

func TestWriteEngineDay_BadID(t *testing.T) {
	a := newEngineAdapter(t)
	err := a.WriteEngineDay(engine.DayRecord{DayID: "not-a-day-id"})
	assert.Error(t, err)
}

func TestWriteEngineDay_DirCreateFails(t *testing.T) {
	skipIfRoot(t)
	a := newEngineAdapter(t)
	daysDir := filepath.Join(a.engineDir(), "days")
	require.NoError(t, os.Chmod(daysDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(daysDir, 0o700) })

	err := a.WriteEngineDay(completedRecord("2026-07-05", 3))
	assert.Error(t, err)
}

func TestReadEngineDay_MalformedJSON(t *testing.T) {
	a := newEngineAdapter(t)
	shard := filepath.Join(a.engineDir(), "days", "2026", "07")
	require.NoError(t, os.MkdirAll(shard, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(shard, "day_2026_07_05.json"), []byte("{not json"), 0o600))

	_, _, err := a.ReadEngineDay("day_2026_07_05")
	assert.ErrorContains(t, err, "parse")
}

func TestReadEngineDays_MalformedFileErrors(t *testing.T) {
	a := newEngineAdapter(t)
	shard := filepath.Join(a.engineDir(), "days", "2026", "07")
	require.NoError(t, os.MkdirAll(shard, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(shard, "day_2026_07_05.json"), []byte("{bad"), 0o600))

	_, err := a.ReadEngineDays()
	assert.Error(t, err)
}

func TestReadEngineDays_IgnoresNonDayFiles(t *testing.T) {
	a := newEngineAdapter(t)
	shard := filepath.Join(a.engineDir(), "days", "2026", "07")
	require.NoError(t, os.MkdirAll(shard, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(shard, "notes.txt"), []byte("ignore me"), 0o600))
	require.NoError(t, a.WriteEngineDay(completedRecord("2026-07-05", 3)))

	recs, err := a.ReadEngineDays()
	require.NoError(t, err)
	assert.Len(t, recs, 1)
}

func TestAppendEngineCorrection_BadID(t *testing.T) {
	a := newEngineAdapter(t)
	err := a.AppendEngineCorrection("bad-id", engine.Correction{Fields: map[string]any{"completed": true}})
	assert.Error(t, err)
}

func TestWriteChainConfig_WriteFails(t *testing.T) {
	skipIfRoot(t)
	a := newEngineAdapter(t)
	require.NoError(t, os.Chmod(a.chainPath(), 0o400))
	t.Cleanup(func() { _ = os.Chmod(a.chainPath(), 0o600) })

	err := a.WriteChainConfig(engine.DefaultChain())
	assert.Error(t, err)
}

func TestAppendProfileEvent_WriteFails(t *testing.T) {
	skipIfRoot(t)
	a := newEngineAdapter(t)
	require.NoError(t, os.Chmod(a.profilePath(), 0o400))
	t.Cleanup(func() { _ = os.Chmod(a.profilePath(), 0o600) })

	err := a.AppendProfileEvent(engine.ProfileSwitch{To: "nights", Effective: "2026-07-08"})
	assert.Error(t, err)
}

func TestRebuildEngineStatus_MissingChainErrors(t *testing.T) {
	a := New(filepath.Join(t.TempDir(), ".lucid"))
	_, err := a.RebuildEngineStatus(time.UTC)
	assert.Error(t, err)
}

func TestRebuildEngineStatus_WriteFails(t *testing.T) {
	skipIfRoot(t)
	a := newEngineAdapter(t)
	require.NoError(t, a.WriteEngineDay(completedRecord("2026-07-05", 3)))
	require.NoError(t, os.Chmod(a.engineDir(), 0o500))
	t.Cleanup(func() { _ = os.Chmod(a.engineDir(), 0o700) })

	_, err := a.RebuildEngineStatus(time.UTC)
	assert.Error(t, err)
}

func TestReadEngineDays_MissingTreeIsEmpty(t *testing.T) {
	// No ScaffoldEngine: the days dir does not exist yet.
	a := New(filepath.Join(t.TempDir(), ".lucid"))
	recs, err := a.ReadEngineDays()
	require.NoError(t, err)
	assert.Empty(t, recs)
}

func TestReadEngineDayFolded_NotFound(t *testing.T) {
	a := newEngineAdapter(t)
	_, found, err := a.ReadEngineDayFolded("day_2026_07_05")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestReadChainConfig_MalformedJSON(t *testing.T) {
	a := newEngineAdapter(t)
	require.NoError(t, os.WriteFile(a.chainPath(), []byte("{bad"), 0o600))
	_, err := a.ReadChainConfig()
	assert.ErrorContains(t, err, "parse")
}

func TestReadProfileState_MalformedJSON(t *testing.T) {
	a := newEngineAdapter(t)
	require.NoError(t, os.WriteFile(a.profilePath(), []byte("{bad"), 0o600))
	_, err := a.ReadProfileState()
	assert.ErrorContains(t, err, "parse")
}

func TestReadEngineStatus_MalformedJSON(t *testing.T) {
	a := newEngineAdapter(t)
	_, err := a.RebuildEngineStatus(time.UTC)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(a.statusPath(), []byte("{bad"), 0o600))
	_, err = a.ReadEngineStatus()
	assert.ErrorContains(t, err, "parse")
}

func TestRebuildEngineStatus_ChainStartWriteFails(t *testing.T) {
	skipIfRoot(t)
	a := newEngineAdapter(t)
	require.NoError(t, a.WriteEngineDay(completedRecord("2026-07-05", 3)))
	// chain_start is still nil; a completed day means the rebuild will try
	// to stamp it — but chain.json is read-only, so the write fails.
	require.NoError(t, os.Chmod(a.chainPath(), 0o400))
	t.Cleanup(func() { _ = os.Chmod(a.chainPath(), 0o600) })

	_, err := a.RebuildEngineStatus(time.UTC)
	assert.Error(t, err)
}

func TestAppendEngineCorrection_WriteFails(t *testing.T) {
	skipIfRoot(t)
	a := newEngineAdapter(t)
	require.NoError(t, a.WriteEngineDay(completedRecord("2026-07-05", 3)))
	dayPath, err := a.engineDayPath("day_2026_07_05")
	require.NoError(t, err)
	require.NoError(t, os.Chmod(dayPath, 0o400))
	t.Cleanup(func() { _ = os.Chmod(dayPath, 0o600) })

	err = a.AppendEngineCorrection("day_2026_07_05", engine.Correction{Fields: map[string]any{"capacity": 4}})
	assert.Error(t, err)
}

func TestAppendProfileEvent_ReadFails(t *testing.T) {
	a := newEngineAdapter(t)
	require.NoError(t, os.WriteFile(a.profilePath(), []byte("{bad"), 0o600))
	err := a.AppendProfileEvent(engine.ProfileSwitch{To: "nights", Effective: "2026-07-08"})
	assert.Error(t, err)
}

func TestRebuildEngineStatus_ReadDaysFails(t *testing.T) {
	a := newEngineAdapter(t)
	shard := filepath.Join(a.engineDir(), "days", "2026", "07")
	require.NoError(t, os.MkdirAll(shard, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(shard, "day_2026_07_05.json"), []byte("{bad"), 0o600))

	_, err := a.RebuildEngineStatus(time.UTC)
	assert.Error(t, err)
}

func TestRebuildEngineStatus_ReadProfileFails(t *testing.T) {
	a := newEngineAdapter(t)
	require.NoError(t, a.WriteEngineDay(completedRecord("2026-07-05", 3)))
	require.NoError(t, os.WriteFile(a.profilePath(), []byte("{bad"), 0o600))

	_, err := a.RebuildEngineStatus(time.UTC)
	assert.Error(t, err)
}

func TestScaffoldEngine_MkdirFails(t *testing.T) {
	skipIfRoot(t)
	a := New(filepath.Join(t.TempDir(), ".lucid"))
	_, err := a.Scaffold()
	require.NoError(t, err)
	require.NoError(t, os.Chmod(a.home, 0o500))
	t.Cleanup(func() { _ = os.Chmod(a.home, 0o700) })

	err = a.ScaffoldEngine()
	assert.Error(t, err)
}
