package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/engine"
)

// --- engine.go ---

// TestCovScaffoldEngine_WriteExclFails: an unwritable engine dir surfaces the
// default-file write failure rather than a half-scaffolded tree.
func TestCovScaffoldEngine_WriteExclFails(t *testing.T) {
	skipIfRoot(t)
	a := New(filepath.Join(t.TempDir(), ".lucid"))
	require.NoError(t, os.MkdirAll(filepath.Join(a.engineDir(), "days"), 0o700))
	require.NoError(t, os.Chmod(a.engineDir(), 0o500))
	t.Cleanup(func() { _ = os.Chmod(a.engineDir(), 0o700) })
	assert.Error(t, a.ScaffoldEngine())
}

// TestCovWriteEngineDay_WriteExclFails: an unwritable day shard fails the
// exclusive write of a new day record.
func TestCovWriteEngineDay_WriteExclFails(t *testing.T) {
	skipIfRoot(t)
	a := newEngineAdapter(t)
	shard := filepath.Join(a.engineDir(), "days", "2026", "07")
	require.NoError(t, os.MkdirAll(shard, 0o700))
	require.NoError(t, os.Chmod(shard, 0o500))
	t.Cleanup(func() { _ = os.Chmod(shard, 0o700) })
	assert.Error(t, a.WriteEngineDay(completedRecord("2026-07-05", 3)))
}

// TestCovFillEngineDayMode_WriteFails: a read-only day file fails the guarded
// mode gap-fill rewrite rather than reporting a phantom success.
func TestCovFillEngineDayMode_WriteFails(t *testing.T) {
	skipIfRoot(t)
	a := newEngineAdapter(t)
	rec := completedRecord("2026-07-05", 3)
	rec.Mode = engine.ModeGreen
	rec.ModeDeclaredAt = "" // leave the gap the fill exists for
	require.NoError(t, a.WriteEngineDay(rec))
	dayPath, err := a.engineDayPath("day_2026_07_05")
	require.NoError(t, err)
	require.NoError(t, os.Chmod(dayPath, 0o400))
	t.Cleanup(func() { _ = os.Chmod(dayPath, 0o600) })
	assert.Error(t, a.FillEngineDayMode("day_2026_07_05", engine.ModeYellow, "2026-07-05T22:00:00Z"))
}

// TestCovReadEngineDays_WalkAndFileErrors: an unreadable shard, and an
// unreadable day file, each surface from the whole-tree walk.
func TestCovReadEngineDays_WalkAndFileErrors(t *testing.T) {
	skipIfRoot(t)
	t.Run("unreadable shard", func(t *testing.T) {
		a := newEngineAdapter(t)
		sub := filepath.Join(a.engineDir(), "days", "2026")
		require.NoError(t, os.MkdirAll(sub, 0o700))
		require.NoError(t, os.Chmod(sub, 0o000))
		t.Cleanup(func() { _ = os.Chmod(sub, 0o700) })
		_, err := a.ReadEngineDays()
		assert.Error(t, err)
	})
	t.Run("unreadable day file", func(t *testing.T) {
		a := newEngineAdapter(t)
		require.NoError(t, a.WriteEngineDay(completedRecord("2026-07-05", 3)))
		dayPath, err := a.engineDayPath("day_2026_07_05")
		require.NoError(t, err)
		require.NoError(t, os.Chmod(dayPath, 0o000))
		t.Cleanup(func() { _ = os.Chmod(dayPath, 0o600) })
		_, err = a.ReadEngineDays()
		assert.Error(t, err)
	})
}

// TestCovAppendSelfFacts_ReadError: a corrupt self store aborts the append
// rather than burying it under a fresh empty log.
func TestCovAppendSelfFacts_ReadError(t *testing.T) {
	a := newEngineAdapter(t)
	require.NoError(t, os.WriteFile(a.selfPath(), []byte("{bad"), 0o600))
	assert.Error(t, a.AppendSelfFact(engine.SelfFact{}))
}

// TestCovSetEngineEscalation_DefaultsEmpty: an empty escalation is normalized to
// "none" and the status rebuilds.
func TestCovSetEngineEscalation_DefaultsEmpty(t *testing.T) {
	a := newEngineAdapter(t)
	_, err := a.SetEngineEscalation(time.UTC, "", false)
	assert.NoError(t, err)
}

// TestCovMarshalJSON_Error: a value JSON cannot render (a channel) surfaces the
// marshal failure the shared writer wraps.
func TestCovMarshalJSON_Error(t *testing.T) {
	_, err := marshalJSON(make(chan int))
	assert.Error(t, err)
}

// --- engine_tripwire.go ---

// TestCovWriteWitnessContract_WriteError: a directory where witness.json belongs
// fails the contract write.
func TestCovWriteWitnessContract_WriteError(t *testing.T) {
	a := newEngineAdapter(t)
	mkdirAt(t, a.witnessPath())
	assert.Error(t, a.WriteWitnessContract(engine.WitnessContract{}))
}

// TestCovAppendStormEvents_WriteError: a read-only storm.json fails the
// history append after a clean read.
func TestCovAppendStormEvents_WriteError(t *testing.T) {
	skipIfRoot(t)
	a := newEngineAdapter(t)
	require.NoError(t, os.Chmod(a.stormPath(), 0o400))
	t.Cleanup(func() { _ = os.Chmod(a.stormPath(), 0o600) })
	assert.Error(t, a.AppendStormEvent(engine.StormEvent{}))
}

// TestCovWriteTripwireState_WriteError: a directory where tripwire.json belongs
// fails the state write.
func TestCovWriteTripwireState_WriteError(t *testing.T) {
	a := newEngineAdapter(t)
	mkdirAt(t, a.tripwirePath())
	assert.Error(t, a.WriteTripwireState(TripwireState{LastRunDate: "2026-07-05"}))
}

// --- receipt.go / reflection_receipt.go / witness_receipt.go ---

// TestCovWriteCompanionReceipt_WriteError: a directory where the receipt belongs
// fails the idempotency-guard write.
func TestCovWriteCompanionReceipt_WriteError(t *testing.T) {
	a := newEngineAdapter(t)
	path, err := a.companionReceiptPath("morning")
	require.NoError(t, err)
	mkdirAt(t, path)
	assert.Error(t, a.WriteCompanionReceipt(CompanionReceipt{Window: "morning"}))
}

// TestCovWriteReflectionReceipt_Errors: both the ensure-dir failure (a file
// where engine/reflection belongs) and the write failure (a dir where the
// receipt belongs) surface.
func TestCovWriteReflectionReceipt_Errors(t *testing.T) {
	t.Run("ensure dir fails", func(t *testing.T) {
		a := newEngineAdapter(t)
		require.NoError(t, os.WriteFile(a.reflectionDir(), []byte("x"), 0o600))
		assert.Error(t, a.WriteReflectionReceipt(ReflectionReceipt{Source: "close"}))
	})
	t.Run("write fails", func(t *testing.T) {
		a := newEngineAdapter(t)
		mkdirAt(t, a.reflectionReceiptPath())
		assert.Error(t, a.WriteReflectionReceipt(ReflectionReceipt{Source: "close"}))
	})
}

// TestCovWriteWitnessReportReceipt_Errors: both the ensure-dir and write
// failures surface for the weekly-report receipt.
func TestCovWriteWitnessReportReceipt_Errors(t *testing.T) {
	t.Run("ensure dir fails", func(t *testing.T) {
		a := newEngineAdapter(t)
		require.NoError(t, os.WriteFile(a.witnessReportDir(), []byte("x"), 0o600))
		assert.Error(t, a.WriteWitnessReportReceipt(WitnessReportReceipt{Week: "2026-W28"}))
	})
	t.Run("write fails", func(t *testing.T) {
		a := newEngineAdapter(t)
		mkdirAt(t, a.witnessReportReceiptPath())
		assert.Error(t, a.WriteWitnessReportReceipt(WitnessReportReceipt{Week: "2026-W28"}))
	})
}

// --- scaffold.go ---

// TestCovEnsureDir_Errors exercises the failure branches of the scaffold's
// directory guard: an unreadable parent (default stat error), an unwritable
// parent on the create path, and both ensure-keep failure modes.
func TestCovEnsureDir_Errors(t *testing.T) {
	skipIfRoot(t)
	t.Run("stat error", func(t *testing.T) {
		a := New(t.TempDir())
		blocked := filepath.Join(a.home, "blocked")
		require.NoError(t, os.MkdirAll(blocked, 0o700))
		require.NoError(t, os.Chmod(blocked, 0o000))
		t.Cleanup(func() { _ = os.Chmod(blocked, 0o700) })
		_, err := a.ensureDir(filepath.Join(blocked, "child"))
		assert.Error(t, err)
	})
	t.Run("mkdir error", func(t *testing.T) {
		a := New(t.TempDir())
		ro := filepath.Join(a.home, "ro")
		require.NoError(t, os.MkdirAll(ro, 0o500))
		t.Cleanup(func() { _ = os.Chmod(ro, 0o700) })
		_, err := a.ensureDir(filepath.Join(ro, "child"))
		assert.Error(t, err)
	})
	t.Run("ensure-keep write error", func(t *testing.T) {
		a := New(t.TempDir())
		d := filepath.Join(a.home, "existing")
		require.NoError(t, os.MkdirAll(d, 0o700))
		require.NoError(t, os.Chmod(d, 0o500)) // no .keep inside, unwritable
		t.Cleanup(func() { _ = os.Chmod(d, 0o700) })
		_, err := a.ensureDir(d)
		assert.Error(t, err)
	})
	t.Run("ensure-keep stat error", func(t *testing.T) {
		a := New(t.TempDir())
		d := filepath.Join(a.home, "opaque")
		require.NoError(t, os.MkdirAll(d, 0o700))
		require.NoError(t, os.Chmod(d, 0o000))
		t.Cleanup(func() { _ = os.Chmod(d, 0o700) })
		_, err := a.ensureDir(d)
		assert.Error(t, err)
	})
}

// TestCovEnsureConfig_StatError: an unreadable home surfaces a non-ENOENT stat
// failure rather than trying to write a fresh config.
func TestCovEnsureConfig_StatError(t *testing.T) {
	skipIfRoot(t)
	a := New(t.TempDir())
	require.NoError(t, os.Chmod(a.home, 0o000))
	t.Cleanup(func() { _ = os.Chmod(a.home, 0o700) })
	_, err := a.ensureConfig(config.Default())
	assert.Error(t, err)
}

// TestCovScaffold_EnsureConfigWriteError: with the config gone and the home
// unwritable, a re-scaffold surfaces the config write failure.
func TestCovScaffold_EnsureConfigWriteError(t *testing.T) {
	skipIfRoot(t)
	a := New(filepath.Join(t.TempDir(), ".lucid"))
	_, err := a.Scaffold()
	require.NoError(t, err)
	require.NoError(t, os.Remove(a.ConfigPath()))
	require.NoError(t, os.Chmod(a.home, 0o500))
	t.Cleanup(func() { _ = os.Chmod(a.home, 0o700) })
	_, err = a.Scaffold()
	assert.Error(t, err)
}

// --- session.go ---

// TestCovWriteSession_EnsureDirError: a file where sessions/ belongs fails the
// session dir guard.
func TestCovWriteSession_EnsureDirError(t *testing.T) {
	a := New(t.TempDir())
	require.NoError(t, os.WriteFile(filepath.Join(a.home, sessionsDirName), []byte("x"), 0o600))
	_, err := a.WriteSession(Session{StartedAt: fixedTime()})
	assert.Error(t, err)
}

// TestCovAllocateSession_WriteExclError: an unwritable sessions dir fails the
// derived-id write.
func TestCovAllocateSession_WriteExclError(t *testing.T) {
	skipIfRoot(t)
	a := New(t.TempDir())
	dir := filepath.Join(a.home, sessionsDirName)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	_, err := a.WriteSession(Session{StartedAt: fixedTime()})
	assert.Error(t, err)
}

// --- fsutil.go ---

// TestCovWriteFileAtomic_RenameError: when the target path is an existing
// directory the atomic rename fails and the staged temp file is discarded.
func TestCovWriteFileAtomic_RenameError(t *testing.T) {
	d := t.TempDir()
	target := filepath.Join(d, "adir")
	require.NoError(t, os.Mkdir(target, 0o700))
	err := writeFileAtomic(target, []byte("x"), "self.json")
	require.Error(t, err)
	// The staged temp file left no residue beside the target.
	entries, rerr := os.ReadDir(d)
	require.NoError(t, rerr)
	assert.Len(t, entries, 1, "only the target dir remains; the temp file was removed")
}

// --- adapter.go ---

// TestCovOpen_DefaultHomeError: with LUCID_HOME unset and no home directory,
// Open surfaces the resolution failure rather than defaulting silently.
func TestCovOpen_DefaultHomeError(t *testing.T) {
	t.Setenv(EnvHome, "")
	t.Setenv("HOME", "")
	_, err := Open()
	assert.Error(t, err)
}

// --- proposals.go ---

// TestCovAppendProposal_ValidationError: an on-disk artifact that no longer
// satisfies the schema is refused after the proposal append rather than being
// overwritten with a broken record.
func TestCovAppendProposal_ValidationError(t *testing.T) {
	a := New(t.TempDir())
	require.NoError(t, os.MkdirAll(a.processedDir(), 0o700))
	id := "raw_2026_07_05_18_41"
	invalid := `{
  "id": "` + id + `",
  "entry_id": "` + id + `",
  "produced_at": "2026-07-05T18:41:00Z",
  "agent_version": "structuring-2026.05.0",
  "emotions": [],
  "themes": [],
  "people": [],
  "notes": null,
  "rejected_proposals": [],
  "unanswered_proposals": []
}`
	require.NoError(t, os.WriteFile(filepath.Join(a.processedDir(), id+processedExt), []byte(invalid), 0o600))
	err := a.AppendRejectedProposal(id, RejectedProposal{
		At: fixedTime(), ReflectionPromptVersion: "reflection-2026.05.0",
		ProposalText: "p", UserResponseText: "no", ShapeTag: "shape_x",
	})
	assert.Error(t, err)
}

// TestCovWriteProposalPauseState_Errors: both a home that is a file (mkdir
// fails) and a directory where the state file belongs (write fails) surface.
func TestCovWriteProposalPauseState_Errors(t *testing.T) {
	t.Run("home is a file", func(t *testing.T) {
		home := filepath.Join(t.TempDir(), "home")
		require.NoError(t, os.WriteFile(home, []byte("x"), 0o600))
		assert.Error(t, New(home).WriteProposalPauseState(ProposalPauseState{}))
	})
	t.Run("state path is a dir", func(t *testing.T) {
		a := New(t.TempDir())
		mkdirAt(t, filepath.Join(a.home, proposalPauseFile))
		assert.Error(t, a.WriteProposalPauseState(ProposalPauseState{}))
	})
}

// TestCovWriteOffLimits_Errors: both a home that is a file and a directory where
// the registry file belongs surface as write failures.
func TestCovWriteOffLimits_Errors(t *testing.T) {
	t.Run("home is a file", func(t *testing.T) {
		home := filepath.Join(t.TempDir(), "home")
		require.NoError(t, os.WriteFile(home, []byte("x"), 0o600))
		assert.Error(t, New(home).WriteOffLimitsPersonKeys([]string{"person_x"}))
	})
	t.Run("registry path is a dir", func(t *testing.T) {
		a := New(t.TempDir())
		mkdirAt(t, filepath.Join(a.home, offLimitsFile))
		assert.Error(t, a.WriteOffLimitsPersonKeys([]string{"person_x"}))
	})
}

// --- people.go ---

// TestCovListPeopleKeys_ScanErrorAndSkip: a file where people/ belongs surfaces
// a scan error, and a non-record directory entry is skipped.
func TestCovListPeopleKeys_ScanErrorAndSkip(t *testing.T) {
	t.Run("file where dir belongs errors", func(t *testing.T) {
		a := New(t.TempDir())
		require.NoError(t, os.WriteFile(a.peopleDir(), []byte("x"), 0o600))
		_, err := a.ListPeopleKeys()
		assert.Error(t, err)
	})
	t.Run("subdir entry skipped", func(t *testing.T) {
		a := New(t.TempDir())
		require.NoError(t, os.MkdirAll(filepath.Join(a.peopleDir(), "nested"), 0o700))
		keys, err := a.ListPeopleKeys()
		require.NoError(t, err)
		assert.Empty(t, keys)
	})
}

// TestCovUpdatePerson_EnsureDirError: an unwritable home fails to create people/
// so the record write is refused before anything lands.
func TestCovUpdatePerson_EnsureDirError(t *testing.T) {
	skipIfRoot(t)
	a := New(t.TempDir())
	require.NoError(t, os.Chmod(a.home, 0o500))
	t.Cleanup(func() { _ = os.Chmod(a.home, 0o700) })
	_, err := a.UpdatePerson(PersonMention{DisplayName: "M.", RawEntryID: "raw_2026_05_05_19_42", At: fixedTime()})
	assert.Error(t, err)
}

// --- processed.go ---

// TestCovListProcessedIDs_ScanErrorAndSkip: a file where processed/ belongs
// surfaces a scan error, and a non-artifact directory entry is skipped.
func TestCovListProcessedIDs_ScanErrorAndSkip(t *testing.T) {
	t.Run("file where dir belongs errors", func(t *testing.T) {
		a := New(t.TempDir())
		require.NoError(t, os.WriteFile(a.processedDir(), []byte("x"), 0o600))
		_, err := a.ListProcessedIDs()
		assert.Error(t, err)
	})
	t.Run("subdir entry skipped", func(t *testing.T) {
		a := New(t.TempDir())
		require.NoError(t, os.MkdirAll(filepath.Join(a.processedDir(), "nested"), 0o700))
		ids, err := a.ListProcessedIDs()
		require.NoError(t, err)
		assert.Empty(t, ids)
	})
}
