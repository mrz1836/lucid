package storage

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
)

// TestReadWitnessContract_Stub: the fresh scaffold stub reads as an
// unconfirmed, not-lapsed contract.
func TestReadWitnessContract_Stub(t *testing.T) {
	a := newEngineAdapter(t)
	w, err := a.ReadWitnessContract()
	require.NoError(t, err)
	assert.False(t, w.IsConfirmed())
	assert.False(t, w.IsLapsed())
	assert.False(t, w.L2Armed())
}

// TestWriteReadWitnessContract round-trips a fully provisioned contract.
func TestWriteReadWitnessContract(t *testing.T) {
	a := newEngineAdapter(t)
	at := "2026-07-05T18:20:00-04:00"
	want := engine.WitnessContract{
		WitnessName: "J.", Channel: &engine.WitnessChannel{Kind: "discord_channel", ID: "chan_w"},
		ConfirmedAt: &at, ConfirmationText: "confirmed — I'll ask about it, once",
		Sees: []string{"streak", "declared_mode"}, StakeShared: true, L2Enabled: true,
		StatusHistory: []engine.WitnessTransition{{At: at, Status: engine.WitnessConfirmed}},
	}
	require.NoError(t, a.WriteWitnessContract(want))

	got, err := a.ReadWitnessContract()
	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.True(t, got.L2Armed())
}

// TestAppendStormEvents appends to storm.json's history in order.
func TestAppendStormEvents(t *testing.T) {
	a := newEngineAdapter(t)
	require.NoError(t, a.AppendStormEvent(engine.StormEvent{At: "2026-07-14T07:05:00Z", Event: engine.StormDeclared, Label: "clause-1"}))
	require.NoError(t, a.AppendStormEvents(
		engine.StormEvent{At: "2026-07-14T09:40:00Z", Event: engine.StormConfirmed, Through: "2026-07-28"},
		engine.StormEvent{At: "2026-07-29T09:00:00Z", Event: engine.StormExpired},
	))

	h, err := a.ReadStormState()
	require.NoError(t, err)
	require.Len(t, h.History, 3)
	assert.Equal(t, engine.StormDeclared, h.History[0].Event)
	assert.Equal(t, engine.StormExpired, h.History[2].Event)

	// A no-op append leaves the file untouched.
	require.NoError(t, a.AppendStormEvents())
	h, err = a.ReadStormState()
	require.NoError(t, err)
	assert.Len(t, h.History, 3)
}

// TestTripwireState_MissingIsZero: a never-run Ledger reads the zero state, and
// a written state round-trips.
func TestTripwireState_MissingIsZero(t *testing.T) {
	a := newEngineAdapter(t)
	s, err := a.ReadTripwireState()
	require.NoError(t, err)
	assert.Equal(t, TripwireState{}, s)

	want := TripwireState{LastHeartbeatMonth: "2026-07", LastRunDate: "2026-07-06"}
	require.NoError(t, a.WriteTripwireState(want))
	got, err := a.ReadTripwireState()
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

// TestSetEngineEscalation_PersistsAndPreserves: the tripwire sets
// escalation_state, and a subsequent plain rebuild (the close-out path)
// preserves it rather than clearing the ladder.
func TestSetEngineEscalation_PersistsAndPreserves(t *testing.T) {
	a := newEngineAdapter(t)
	require.NoError(t, a.WriteEngineDay(completedRecord("2026-07-05", 3)))

	base, err := a.RebuildEngineStatus(time.UTC)
	require.NoError(t, err)
	assert.Equal(t, engine.EscalationNone, base.EscalationState)

	set, err := a.SetEngineEscalation(time.UTC, engine.EscalationL2, false)
	require.NoError(t, err)
	assert.Equal(t, engine.EscalationL2, set.EscalationState)

	// A plain rebuild (close-out / mode / status) preserves the ladder.
	preserved, err := a.RebuildEngineStatus(time.UTC)
	require.NoError(t, err)
	assert.Equal(t, engine.EscalationL2, preserved.EscalationState)

	// Deleting status.json and rebuilding clears the ladder — the dead-man
	// tension (an absent day is not a stored input), documented behavior.
	require.NoError(t, os.Remove(a.statusPath()))
	fresh, err := a.RebuildEngineStatus(time.UTC)
	require.NoError(t, err)
	assert.Equal(t, engine.EscalationNone, fresh.EscalationState)
}

// TestRebuildEngineStatus_DerivesWitnessLapsed: witness_lapsed is derived from
// witness.json on every rebuild.
func TestRebuildEngineStatus_DerivesWitnessLapsed(t *testing.T) {
	a := newEngineAdapter(t)
	require.NoError(t, a.WriteEngineDay(completedRecord("2026-07-05", 3)))

	st, err := a.RebuildEngineStatus(time.UTC)
	require.NoError(t, err)
	assert.False(t, st.WitnessLapsed, "the stub witness is not lapsed")

	require.NoError(t, a.WriteWitnessContract(engine.WitnessContract{
		StatusHistory: []engine.WitnessTransition{
			{Status: engine.WitnessConfirmed}, {Status: engine.WitnessResigned},
		},
	}))
	st, err = a.RebuildEngineStatus(time.UTC)
	require.NoError(t, err)
	assert.True(t, st.WitnessLapsed, "a resigned witness lapses the contract")
}

// TestReadWitnessContract_CorruptAndMissing surfaces a bad or absent
// witness.json rather than reading it as an empty contract.
func TestReadWitnessContract_CorruptAndMissing(t *testing.T) {
	a := newEngineAdapter(t)
	require.NoError(t, os.WriteFile(a.witnessPath(), []byte("{bad"), 0o600))
	_, err := a.ReadWitnessContract()
	require.Error(t, err)

	require.NoError(t, os.Remove(a.witnessPath()))
	_, err = a.ReadWitnessContract()
	require.Error(t, err)
}

// TestAppendStormEvents_CorruptStormErrors: an append over a corrupt storm.json
// surfaces the read error rather than clobbering it.
func TestAppendStormEvents_CorruptStormErrors(t *testing.T) {
	a := newEngineAdapter(t)
	require.NoError(t, os.WriteFile(a.stormPath(), []byte("{bad"), 0o600))
	err := a.AppendStormEvent(engine.StormEvent{Event: engine.StormDeclared})
	assert.Error(t, err)
}

// TestReadTripwireState_CorruptErrors surfaces a malformed tripwire.json.
func TestReadTripwireState_CorruptErrors(t *testing.T) {
	a := newEngineAdapter(t)
	require.NoError(t, os.WriteFile(a.tripwirePath(), []byte("{bad"), 0o600))
	_, err := a.ReadTripwireState()
	assert.Error(t, err)
}

// TestWriteFailuresSurfaced: a read-only engine tree fails the witness/tripwire
// writes rather than silently dropping them.
func TestWriteFailuresSurfaced(t *testing.T) {
	skipIfRoot(t)
	a := newEngineAdapter(t)
	require.NoError(t, os.Chmod(a.witnessPath(), 0o400))
	t.Cleanup(func() { _ = os.Chmod(a.witnessPath(), 0o600) })
	require.Error(t, a.WriteWitnessContract(engine.WitnessContract{}))

	require.NoError(t, os.Chmod(a.engineDir(), 0o500))
	t.Cleanup(func() { _ = os.Chmod(a.engineDir(), 0o700) })
	assert.Error(t, a.WriteTripwireState(TripwireState{LastRunDate: "2026-07-06"}))
}
