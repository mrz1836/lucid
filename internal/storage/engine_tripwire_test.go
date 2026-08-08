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

// TestAppendStormEvents_MintsEpisodeWhenRunOpens: every append mints an id for a
// run it opens and stamps the version, while confirmed/renewed continue a run
// and mint nothing, and a fresh declaration after an end opens a second episode.
func TestAppendStormEvents_MintsEpisodeWhenRunOpens(t *testing.T) {
	a := newEngineAdapter(t)

	// A declaration opens a run — exactly one episode, keyed on the opener.
	require.NoError(t, a.AppendStormEvent(engine.StormEvent{At: "2026-07-14T07:05:00Z", Event: engine.StormDeclared, Label: "clause-a"}))
	h, err := a.ReadStormState()
	require.NoError(t, err)
	require.Len(t, h.Episodes, 1)
	assert.Equal(t, "storm_2026_07_14_a", h.Episodes[0].ID)
	assert.Equal(t, "2026-07-14T07:05:00Z", h.Episodes[0].OpenerAt)
	assert.Equal(t, engine.StormVersion, h.Version)

	// Confirmed then renewed continue the open run — no new episode.
	require.NoError(t, a.AppendStormEvents(
		engine.StormEvent{At: "2026-07-14T09:40:00Z", Event: engine.StormConfirmed, Through: "2026-07-28"},
		engine.StormEvent{At: "2026-07-27T08:00:00Z", Event: engine.StormRenewed, Through: "2026-08-11"},
	))
	h, err = a.ReadStormState()
	require.NoError(t, err)
	assert.Len(t, h.Episodes, 1, "confirmed/renewed continue the run and mint nothing")

	// An end closes the run, then a fresh declaration opens a second episode.
	require.NoError(t, a.AppendStormEvents(
		engine.StormEvent{At: "2026-08-12T06:00:00Z", Event: engine.StormExpired},
		engine.StormEvent{At: "2026-09-01T07:00:00Z", Event: engine.StormDeclared, Label: "clause-a"},
	))
	h, err = a.ReadStormState()
	require.NoError(t, err)
	require.Len(t, h.Episodes, 2, "a fresh declaration after a terminal event opens a second episode")
	assert.Equal(t, "storm_2026_09_01_a", h.Episodes[1].ID)
	assert.Equal(t, "2026-09-01T07:00:00Z", h.Episodes[1].OpenerAt)
	assert.Len(t, h.History, 5, "identity lives in episodes[]; history carries only the events")
}

// TestAppendStormEvents_StatusUnchangedByEpisodes proves the minted episodes[]
// index perturbs no derived status field: the status built from the real
// (episodes-carrying) storm.json is byte-identical to the one built from the
// same history with episodes[] and version stripped.
func TestAppendStormEvents_StatusUnchangedByEpisodes(t *testing.T) {
	a := newEngineAdapter(t)
	// A completed day inside the standing window anchors the reference date, so
	// the storm actually stands and the equality is non-vacuous.
	require.NoError(t, a.WriteEngineDay(completedRecord("2026-07-20", 3)))
	require.NoError(t, a.AppendStormEvents(
		engine.StormEvent{At: "2026-07-14T07:05:00Z", Event: engine.StormDeclared, Label: "clause-a"},
		engine.StormEvent{At: "2026-07-14T09:40:00Z", Event: engine.StormConfirmed, Through: "2026-07-28"},
	))

	h, err := a.ReadStormState()
	require.NoError(t, err)
	require.Len(t, h.Episodes, 1, "the opened run is indexed")
	assert.Equal(t, engine.StormVersion, h.Version)
	require.Len(t, h.History, 2, "history carries only the two events")

	withEp, err := a.RebuildEngineStatus(time.UTC)
	require.NoError(t, err)
	require.Equal(t, engine.StormStandingState, withEp.StormState, "the storm stands at the reference date")

	// Rewrite the identical history with episodes[] and version stripped, then
	// rebuild: byte-identical status means the index changed nothing derived.
	stripped := h
	stripped.Episodes = nil
	stripped.Version = 0
	content, err := marshalJSON(stripped)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(a.stormPath(), content, filePerm))

	withoutEp, err := a.RebuildEngineStatus(time.UTC)
	require.NoError(t, err)
	assert.Equal(t, withoutEp, withEp, "the episodes index changes no derived status field")
}

// TestWriteStormEpisodes_AppendsAndLeavesHistory: WriteStormEpisodes appends to
// episodes[] and stamps the version without touching history[], and a no-op call
// leaves the file untouched.
func TestWriteStormEpisodes_AppendsAndLeavesHistory(t *testing.T) {
	a := newEngineAdapter(t)
	require.NoError(t, a.AppendStormEvent(engine.StormEvent{At: "2026-07-14T07:05:00Z", Event: engine.StormDeclared, Label: "clause-a"}))

	before, err := a.ReadStormState()
	require.NoError(t, err)
	require.Len(t, before.Episodes, 1, "the append already minted one episode forward")

	require.NoError(t, a.WriteStormEpisodes(engine.StormEpisode{ID: "storm_2099_01_01_a", OpenerAt: "2099-01-01T00:00:00Z"}))
	after, err := a.ReadStormState()
	require.NoError(t, err)
	require.Len(t, after.Episodes, 2, "the record was appended to episodes[]")
	assert.Equal(t, "storm_2099_01_01_a", after.Episodes[1].ID)
	assert.Equal(t, before.History, after.History, "history[] is never touched")
	assert.Equal(t, engine.StormVersion, after.Version)

	// A no-op call reads and writes nothing.
	beforeBytes, err := os.ReadFile(a.stormPath())
	require.NoError(t, err)
	require.NoError(t, a.WriteStormEpisodes())
	afterBytes, err := os.ReadFile(a.stormPath())
	require.NoError(t, err)
	assert.Equal(t, beforeBytes, afterBytes, "a no-op append leaves storm.json untouched")
}

// TestTripwireState_MissingIsZero: a never-run Ledger reads the zero state, and
// a written state round-trips.
func TestTripwireState_MissingIsZero(t *testing.T) {
	a := newEngineAdapter(t)
	s, err := a.ReadTripwireState()
	require.NoError(t, err)
	assert.Equal(t, TripwireState{}, s)

	want := TripwireState{LastRunDate: "2026-07-06"}
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
