package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEvaluateTripwire_NilLocDefaultsUTC: an unset location falls back to UTC,
// so a run wired without a resolved profile still evaluates the dead-man rather
// than panicking.
func TestEvaluateTripwire_NilLocDefaultsUTC(t *testing.T) {
	dec := EvaluateTripwire(TripwireInput{
		Now:       ts(2026, 7, 6, 9, 0),
		Loc:       nil,
		Reference: refDay("2026-07-05"),
		Chain:     startedChain("2026-07-01"),
		Records:   recMap(completedDay("2026-07-04", ModeGreen)), // 07-05 absent → one miss
	})
	require.Len(t, dec.Sends, 1)
	assert.Equal(t, SendL1, dec.Sends[0].Kind)
}

// TestEvaluateTripwire_L2CarriesReferenceDayMode: when the reference day itself
// is a logged (but not completed) miss carrying a mode, the L2 to the witness
// reports that mode rather than the green default — the one path where the
// missed day's own declared mode surfaces.
func TestEvaluateTripwire_L2CarriesReferenceDayMode(t *testing.T) {
	// 07-04 missed and 07-05 present as a Red-mode miss ⇒ two consecutive.
	refMiss := DayRecord{LogicalDate: "2026-07-05", Mode: ModeRed, Missed: true}
	dec := EvaluateTripwire(TripwireInput{
		Now:       ts(2026, 7, 6, 9, 0),
		Loc:       nil,
		Reference: refDay("2026-07-05"),
		Chain:     startedChain("2026-07-01"),
		Witness:   armedWitness(),
		Streak:    3,
		Records:   recMap(missedDay("2026-07-04"), refMiss),
	})
	require.Len(t, dec.Sends, 1)
	s := dec.Sends[0]
	assert.Equal(t, SendL2, s.Kind)
	assert.Equal(t, ModeRed, s.Mode, "the reference day's declared mode is carried onto the L2")
}

// TestEvaluateTripwire_MalformedChainStartIsSilent: a chain_start that does not
// parse reads as "not started", so the tripwire stays silent rather than
// escalating against a corrupt date.
func TestEvaluateTripwire_MalformedChainStartIsSilent(t *testing.T) {
	chain := DefaultChain()
	bad := "not-a-date"
	chain.ChainStart = &bad
	dec := EvaluateTripwire(TripwireInput{
		Now:       ts(2026, 7, 6, 9, 0),
		Loc:       nil,
		Reference: refDay("2026-07-05"),
		Chain:     chain, // 07-05 absent, but chain_start is unparseable
	})
	assert.Empty(t, dec.Sends)
	assert.Equal(t, EscalationNone, dec.EscalationState)
}

// TestChainStarted_MalformedStartIsFalse pins the parse-failure branch directly.
func TestChainStarted_MalformedStartIsFalse(t *testing.T) {
	bad := "garbage"
	assert.False(t, chainStarted(&bad, refDay("2026-07-05"), time.UTC))
}

// TestEscalationRun_StopsAtChainStartLowerBound: with no completed day at or
// after chain_start, the run counts every day back to chain_start and then
// stops at the lower bound rather than walking into the pre-chain past.
func TestEscalationRun_StopsAtChainStartLowerBound(t *testing.T) {
	start := "2026-07-05"
	// Reference 07-06, no records at all: 07-06 and 07-05 (the bound) count,
	// then 07-04 is before chain_start ⇒ the walk breaks.
	assert.Equal(t, 2, escalationRun(map[string]DayRecord{}, refDay("2026-07-06"), &start, time.UTC))
}
