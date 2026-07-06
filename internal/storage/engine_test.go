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

// newEngineAdapter returns an adapter over a fresh scaffolded Ledger with
// the engine tree created.
func newEngineAdapter(t *testing.T) *Adapter {
	t.Helper()
	a := New(filepath.Join(t.TempDir(), ".lucid"))
	_, err := a.Scaffold()
	require.NoError(t, err)
	require.NoError(t, a.ScaffoldEngine())
	return a
}

// completedRecord builds a completed day record for a logical date.
func completedRecord(date string, capacity int) engine.DayRecord {
	d, _ := time.Parse("2006-01-02", date)
	return engine.DayRecord{
		DayID:       engine.DayID(d),
		LogicalDate: date,
		RecordedAt:  date + "T22:00:00Z",
		Mode:        engine.ModeGreen,
		Links:       map[string]string{"journal": engine.StatusDone},
		Completed:   true,
		Capacity:    capacity,
		Profile:     engine.DefaultProfile,
		Corrections: []engine.Correction{},
	}
}

func TestScaffoldEngine_CreatesTreeAndDefaults(t *testing.T) {
	a := newEngineAdapter(t)
	for _, f := range []string{"chain.json", "witness.json", "storm.json", "profile.json"} {
		_, err := os.Stat(filepath.Join(a.engineDir(), f))
		require.NoErrorf(t, err, "%s should exist", f)
	}
	_, err := os.Stat(filepath.Join(a.engineDir(), "days"))
	require.NoError(t, err)

	chain, err := a.ReadChainConfig()
	require.NoError(t, err)
	assert.Equal(t, engine.DefaultChain(), chain)
	assert.Nil(t, chain.ChainStart)

	state, err := a.ReadProfileState()
	require.NoError(t, err)
	assert.Equal(t, engine.DefaultProfile, state.Active)
}

func TestScaffoldEngine_IdempotentPreservesHandEdits(t *testing.T) {
	a := newEngineAdapter(t)
	chain, err := a.ReadChainConfig()
	require.NoError(t, err)
	chain.Label = "hand edited"
	require.NoError(t, a.WriteChainConfig(chain))

	// A second scaffold must not overwrite the hand-edited chain.json.
	require.NoError(t, a.ScaffoldEngine())
	got, err := a.ReadChainConfig()
	require.NoError(t, err)
	assert.Equal(t, "hand edited", got.Label)
}

func TestWriteEngineDay_CreateOnly(t *testing.T) {
	a := newEngineAdapter(t)
	rec := completedRecord("2026-07-05", 3)
	require.NoError(t, a.WriteEngineDay(rec))

	// Landed at the sharded path.
	_, err := os.Stat(filepath.Join(a.engineDir(), "days", "2026", "07", "day_2026_07_05.json"))
	require.NoError(t, err)

	// A second write of the same day-id is refused (append-only per day).
	err = a.WriteEngineDay(rec)
	assert.ErrorContains(t, err, "already exists")
}

func TestReadEngineDay_FoundAndMissing(t *testing.T) {
	a := newEngineAdapter(t)
	require.NoError(t, a.WriteEngineDay(completedRecord("2026-07-05", 3)))

	got, found, err := a.ReadEngineDay("day_2026_07_05")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "2026-07-05", got.LogicalDate)

	_, found, err = a.ReadEngineDay("day_2026_07_04")
	require.NoError(t, err)
	assert.False(t, found)

	_, _, err = a.ReadEngineDay("bad-id")
	assert.Error(t, err)
}

func TestAppendEngineCorrection_FoldsAndRejectsImmutable(t *testing.T) {
	a := newEngineAdapter(t)
	rec := engine.DayRecord{
		DayID: "day_2026_07_05", LogicalDate: "2026-07-05", RecordedAt: "2026-07-05T22:00:00Z",
		Mode: engine.ModeGreen, Links: map[string]string{"journal": engine.StatusFloor},
		Partial: true, Missed: true, Profile: engine.DefaultProfile, Corrections: []engine.Correction{},
	}
	require.NoError(t, a.WriteEngineDay(rec))

	// A correction naming an immutable field is rejected before any write.
	err := a.AppendEngineCorrection("day_2026_07_05", engine.Correction{
		Fields: map[string]any{"mode": engine.ModeRed},
	})
	require.ErrorContains(t, err, "immutable")

	// A legal correction folds to completed.
	require.NoError(t, a.AppendEngineCorrection("day_2026_07_05", engine.Correction{
		Fields: map[string]any{"completed": true, "missed": false, "partial": false}, Source: "user",
	}))
	folded, found, err := a.ReadEngineDayFolded("day_2026_07_05")
	require.NoError(t, err)
	require.True(t, found)
	assert.True(t, folded.Completed)
	assert.False(t, folded.Missed)

	// The original body kept partial:true; only corrections grew.
	raw, _, err := a.ReadEngineDay("day_2026_07_05")
	require.NoError(t, err)
	assert.True(t, raw.Partial)
	assert.Len(t, raw.Corrections, 1)

	// Correcting a missing day is an error.
	err = a.AppendEngineCorrection("day_2026_07_04", engine.Correction{Fields: map[string]any{"completed": true}})
	assert.ErrorContains(t, err, "missing")
}

func TestReadEngineDays_EmptyAndSorted(t *testing.T) {
	a := newEngineAdapter(t)
	recs, err := a.ReadEngineDays()
	require.NoError(t, err)
	assert.Empty(t, recs)

	require.NoError(t, a.WriteEngineDay(completedRecord("2026-07-07", 3)))
	require.NoError(t, a.WriteEngineDay(completedRecord("2026-07-05", 3)))
	require.NoError(t, a.WriteEngineDay(completedRecord("2026-07-06", 3)))

	recs, err = a.ReadEngineDays()
	require.NoError(t, err)
	require.Len(t, recs, 3)
	assert.Equal(t, "2026-07-05", recs[0].LogicalDate)
	assert.Equal(t, "2026-07-06", recs[1].LogicalDate)
	assert.Equal(t, "2026-07-07", recs[2].LogicalDate)
}

func TestReadEngineDays_FoldsCorrections(t *testing.T) {
	a := newEngineAdapter(t)
	rec := completedRecord("2026-07-05", 3)
	rec.Completed = false
	rec.Missed = true
	require.NoError(t, a.WriteEngineDay(rec))
	require.NoError(t, a.AppendEngineCorrection(rec.DayID, engine.Correction{
		Fields: map[string]any{"completed": true, "missed": false},
	}))
	recs, err := a.ReadEngineDays()
	require.NoError(t, err)
	require.Len(t, recs, 1)
	assert.True(t, recs[0].Completed, "ReadEngineDays returns folded records")
}

func TestProfileState_ReadAndAppend(t *testing.T) {
	a := newEngineAdapter(t)
	sw := engine.ProfileSwitch{From: engine.DefaultProfile, To: "nights", Effective: "2026-07-08", At: "2026-07-07T21:50:00Z"}
	require.NoError(t, a.AppendProfileEvent(sw))

	state, err := a.ReadProfileState()
	require.NoError(t, err)
	assert.Equal(t, "nights", state.Active)
	require.Len(t, state.History, 1)
	assert.Equal(t, "2026-07-08", state.History[0].Effective)
}

func TestRebuildEngineStatus_StampsChainStartOnce(t *testing.T) {
	a := newEngineAdapter(t)
	require.NoError(t, a.WriteEngineDay(completedRecord("2026-07-05", 3)))
	require.NoError(t, a.WriteEngineDay(completedRecord("2026-07-06", 3)))

	st, err := a.RebuildEngineStatus(time.UTC)
	require.NoError(t, err)
	assert.Equal(t, 2, st.CurrentStreak)
	require.NotNil(t, st.ChainStart)
	assert.Equal(t, "2026-07-05", *st.ChainStart)

	chain, err := a.ReadChainConfig()
	require.NoError(t, err)
	require.NotNil(t, chain.ChainStart)
	assert.Equal(t, "2026-07-05", *chain.ChainStart)

	// A later completed day must not move chain_start.
	require.NoError(t, a.WriteEngineDay(completedRecord("2026-07-07", 3)))
	st, err = a.RebuildEngineStatus(time.UTC)
	require.NoError(t, err)
	assert.Equal(t, "2026-07-05", *st.ChainStart)
	assert.Equal(t, 3, st.CurrentStreak)
}

func TestRebuildEngineStatus_ByteReproducible(t *testing.T) {
	a := newEngineAdapter(t)
	rec := completedRecord("2026-07-05", 2)
	rec.Completed = false
	require.NoError(t, a.WriteEngineDay(rec))
	require.NoError(t, a.AppendEngineCorrection(rec.DayID, engine.Correction{
		Fields: map[string]any{"completed": true, "capacity": 4},
	}))
	require.NoError(t, a.WriteEngineDay(completedRecord("2026-07-06", 3)))

	_, err := a.RebuildEngineStatus(time.UTC)
	require.NoError(t, err)
	first, err := os.ReadFile(a.statusPath())
	require.NoError(t, err)

	// Delete + rebuild reproduces status.json byte-for-byte, corrections
	// folded (engine-module.md §status.json determinism criterion).
	require.NoError(t, os.Remove(a.statusPath()))
	_, err = a.RebuildEngineStatus(time.UTC)
	require.NoError(t, err)
	second, err := os.ReadFile(a.statusPath())
	require.NoError(t, err)
	assert.Equal(t, string(first), string(second))

	got, err := a.ReadEngineStatus()
	require.NoError(t, err)
	assert.Equal(t, 2, got.CurrentStreak)
}

// missedRecord builds a missed (non-storm) day record.
func missedRecord(date string) engine.DayRecord {
	d, _ := time.Parse("2006-01-02", date)
	return engine.DayRecord{
		DayID:       engine.DayID(d),
		LogicalDate: date,
		Mode:        engine.ModeGreen,
		Missed:      true,
		Links:       map[string]string{},
		Profile:     engine.DefaultProfile,
		Corrections: []engine.Correction{},
	}
}

func TestReadStormState_DefaultStub(t *testing.T) {
	a := newEngineAdapter(t)
	storm, err := a.ReadStormState()
	require.NoError(t, err)
	assert.Empty(t, storm.History)
	assert.Equal(t, 14, storm.DurationDays)
}

func TestReadStormState_MalformedJSON(t *testing.T) {
	a := newEngineAdapter(t)
	require.NoError(t, os.WriteFile(a.stormPath(), []byte("{bad"), 0o600))
	_, err := a.ReadStormState()
	assert.ErrorContains(t, err, "parse")
}

func TestReadStormState_MissingErrors(t *testing.T) {
	a := New(filepath.Join(t.TempDir(), ".lucid"))
	_, err := a.ReadStormState()
	assert.Error(t, err)
}

func TestReadStormState_ParsesConfirmedHistory(t *testing.T) {
	a := newEngineAdapter(t)
	content, err := marshalJSON(engine.StormHistory{
		DurationDays: 14,
		History: []engine.StormEvent{
			{At: "2026-07-14T09:40:00Z", Event: engine.StormConfirmed, By: "J.", Through: "2026-07-28"},
		},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(a.stormPath(), content, 0o600))

	storm, err := a.ReadStormState()
	require.NoError(t, err)
	require.Len(t, storm.History, 1)
	assert.Equal(t, engine.StormConfirmed, storm.History[0].Event)
	assert.Equal(t, "2026-07-28", storm.History[0].Through)
}

// TestRebuildEngineStatus_FailsOnCorruptStorm: a malformed storm.json fails
// the rebuild rather than silently dropping the storm state.
func TestRebuildEngineStatus_FailsOnCorruptStorm(t *testing.T) {
	a := newEngineAdapter(t)
	require.NoError(t, a.WriteEngineDay(completedRecord("2026-07-05", 3)))
	require.NoError(t, os.WriteFile(a.stormPath(), []byte("{bad"), 0o600))
	_, err := a.RebuildEngineStatus(time.UTC)
	assert.Error(t, err)
}

// TestRebuildEngineStatus_ByteReproducibleEnriched drives the AC-5 byte-
// reproducibility criterion across the enriched fields: a corrections-folded
// completion, a plain miss, a storm miss, and a standing storm all fold into
// status.json, and delete+rebuild reproduces every byte.
func TestRebuildEngineStatus_ByteReproducibleEnriched(t *testing.T) {
	a := newEngineAdapter(t)

	// A day completed via a correction (fold is part of the criterion).
	partial := completedRecord("2026-07-14", 2)
	partial.Completed = false
	require.NoError(t, a.WriteEngineDay(partial))
	require.NoError(t, a.AppendEngineCorrection(partial.DayID, engine.Correction{
		Fields: map[string]any{"completed": true, "capacity": 4},
	}))
	// A plain miss and a storm miss.
	require.NoError(t, a.WriteEngineDay(missedRecord("2026-07-15")))
	stormMiss := missedRecord("2026-07-20")
	stormMiss.Storm = true
	require.NoError(t, a.WriteEngineDay(stormMiss))

	// A standing storm through a future date.
	content, err := marshalJSON(engine.StormHistory{
		DurationDays: 14,
		History:      []engine.StormEvent{{Event: engine.StormConfirmed, Through: "2026-07-28"}},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(a.stormPath(), content, 0o600))

	st, err := a.RebuildEngineStatus(time.UTC)
	require.NoError(t, err)
	assert.Equal(t, engine.StormStandingState, st.StormState)
	require.NotNil(t, st.StormThrough)
	assert.Equal(t, "2026-07-28", *st.StormThrough)
	assert.Equal(t, 1, st.ErrorBudget.Burn, "the isolated plain miss burns 1; the storm miss burns nothing")

	first, err := os.ReadFile(a.statusPath())
	require.NoError(t, err)
	require.NoError(t, os.Remove(a.statusPath()))
	_, err = a.RebuildEngineStatus(time.UTC)
	require.NoError(t, err)
	second, err := os.ReadFile(a.statusPath())
	require.NoError(t, err)
	assert.Equal(t, string(first), string(second))
}

func TestEngineDayShard(t *testing.T) {
	y, m, err := engineDayShard("day_2026_07_05")
	require.NoError(t, err)
	assert.Equal(t, "2026", y)
	assert.Equal(t, "07", m)

	_, _, err = engineDayShard("nope")
	assert.Error(t, err)
}

func TestReadChainConfig_MissingErrors(t *testing.T) {
	a := New(filepath.Join(t.TempDir(), ".lucid"))
	_, err := a.ReadChainConfig()
	require.Error(t, err)
	_, err = a.ReadProfileState()
	require.Error(t, err)
	_, err = a.ReadEngineStatus()
	require.Error(t, err)
}
