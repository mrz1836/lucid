package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
)

// --- scaffold.go ------------------------------------------------------------

// TestCovEnsureDir_MkdirFails: with the parent unwritable, creating a missing
// Mirror directory surfaces the mkdir failure rather than reporting it created.
func TestCovEnsureDir_MkdirFails(t *testing.T) {
	skipIfRoot(t)
	parent := t.TempDir()
	require.NoError(t, os.Chmod(parent, 0o500))
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	a := New(parent)
	_, err := a.ensureDir(filepath.Join(parent, "sub"))
	require.Error(t, err)
}

// TestCovEnsureDir_ExistingDirKeepWriteFails: an already-present but unwritable
// directory fails when its missing .keep marker cannot be written — the fill is
// refused rather than silently skipped.
func TestCovEnsureDir_ExistingDirKeepWriteFails(t *testing.T) {
	skipIfRoot(t)
	a := New(t.TempDir())
	dir := filepath.Join(t.TempDir(), "existing")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.Chmod(dir, 0o500)) // readable, so stat sees the dir; the .keep write fails
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	_, err := a.ensureDir(dir)
	require.Error(t, err)
}

// TestCovEnsureDir_NotADirectory: a path that exists as a file is rejected
// rather than treated as the directory it must be.
func TestCovEnsureDir_NotADirectory(t *testing.T) {
	a := New(t.TempDir())
	f := filepath.Join(t.TempDir(), "afile")
	require.NoError(t, os.WriteFile(f, []byte("x"), 0o600))

	_, err := a.ensureDir(f)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a directory")
}

// --- engine.go --------------------------------------------------------------

// TestCovScaffoldEngine_DaysMkdirFails: a file sitting where engine/ must be
// makes the days-dir creation fail with ENOTDIR, surfaced rather than leaving a
// half-built tree.
func TestCovScaffoldEngine_DaysMkdirFails(t *testing.T) {
	a := New(t.TempDir())
	require.NoError(t, os.MkdirAll(a.home, dirPerm))
	require.NoError(t, os.WriteFile(a.engineDir(), []byte("not a dir"), 0o600))

	require.Error(t, a.ScaffoldEngine())
}

// TestCovWriteEngineDay_ShardMkdirFails: a file where the year shard belongs
// makes the day's directory creation fail, so the record is refused rather than
// written to a broken path.
func TestCovWriteEngineDay_ShardMkdirFails(t *testing.T) {
	a := newEngineAdapter(t)
	require.NoError(t, os.WriteFile(filepath.Join(a.engineDir(), engineDaysDir, "2026"), []byte("x"), 0o600))

	require.Error(t, a.WriteEngineDay(completedRecord("2026-07-05", 3)))
}

// TestCovAppendEngineCorrection_RewriteFails: a read-only day file lets the
// record be read but fails the correction rewrite, so the failure surfaces
// rather than being silently dropped.
func TestCovAppendEngineCorrection_RewriteFails(t *testing.T) {
	skipIfRoot(t)
	a := newEngineAdapter(t)
	require.NoError(t, a.WriteEngineDay(completedRecord("2026-07-05", 3)))
	path, err := a.engineDayPath("day_2026_07_05")
	require.NoError(t, err)
	require.NoError(t, os.Chmod(path, 0o400)) // readable, so the fold reads; the rewrite fails
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	err = a.AppendEngineCorrection("day_2026_07_05", engine.Correction{Fields: map[string]any{"capacity": 4}})
	require.Error(t, err)
}

// TestCovBuildAndWriteStatus_StatusWriteFails: with engine/ read-only every
// input reads but the derived status.json write fails, so a rebuild surfaces the
// failure rather than reporting a status it never persisted.
func TestCovBuildAndWriteStatus_StatusWriteFails(t *testing.T) {
	skipIfRoot(t)
	a := newEngineAdapter(t)
	require.NoError(t, os.Chmod(a.engineDir(), 0o500))
	t.Cleanup(func() { _ = os.Chmod(a.engineDir(), 0o700) })

	_, err := a.RebuildEngineStatus(time.UTC)
	require.Error(t, err)
}

// TestCovBuildAndWriteStatus_ChainStartWriteFails: with a completed day present
// the rebuild stamps chain_start once; an unwritable engine/ makes that
// chain.json write fail, and the rebuild surfaces it rather than proceeding on
// an unpersisted start.
func TestCovBuildAndWriteStatus_ChainStartWriteFails(t *testing.T) {
	skipIfRoot(t)
	a := newEngineAdapter(t)
	require.NoError(t, a.WriteEngineDay(completedRecord("2026-07-05", 3)))
	require.NoError(t, os.Chmod(a.engineDir(), 0o500))
	t.Cleanup(func() { _ = os.Chmod(a.engineDir(), 0o700) })

	_, err := a.RebuildEngineStatus(time.UTC)
	require.Error(t, err)
}
