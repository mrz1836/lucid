package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recMap keys day records by their logical date, as the tripwire consumes them.
func recMap(recs ...DayRecord) map[string]DayRecord {
	m := make(map[string]DayRecord, len(recs))
	for _, r := range recs {
		m[r.LogicalDate] = r
	}
	return m
}

// startedChain returns the default chain with chain_start stamped.
//
//nolint:unparam // start is kept explicit for fixture readability even though the cases share a date
func startedChain(start string) ChainConfig {
	c := DefaultChain()
	c.ChainStart = &start
	return c
}

// armedWitness is a confirmed, l2-enabled witness contract.
func armedWitness() WitnessContract {
	at := "2026-07-01T18:20:00Z"
	return WitnessContract{
		WitnessName: "J.", ConfirmedAt: &at, L2Enabled: true,
		StatusHistory: []WitnessTransition{{Status: WitnessConfirmed}},
	}
}

func findSend(sends []Send, kind string) (Send, bool) {
	for _, s := range sends {
		if s.Kind == kind {
			return s, true
		}
	}
	return Send{}, false
}

func refDay(date string) time.Time { d, _ := time.Parse(dateLayout, date); return d }

// TestEvaluateTripwire_CompletedNoSend: a completed reference day resets the
// ladder and sends nothing.
func TestEvaluateTripwire_CompletedNoSend(t *testing.T) {
	dec := EvaluateTripwire(TripwireInput{
		Now:       ts(2026, 7, 6, 9, 0),
		Loc:       time.UTC,
		Reference: refDay("2026-07-05"),
		Chain:     startedChain("2026-07-01"),
		Records:   recMap(completedDay("2026-07-05", ModeGreen)),
	})
	assert.Empty(t, dec.Sends)
	assert.Equal(t, EscalationNone, dec.EscalationState)
}

// TestEvaluateTripwire_ChainNotStarted: before the first completed close-out
// there is nothing to escalate, even on an absent day.
func TestEvaluateTripwire_ChainNotStarted(t *testing.T) {
	dec := EvaluateTripwire(TripwireInput{
		Now:       ts(2026, 7, 6, 9, 0),
		Loc:       time.UTC,
		Reference: refDay("2026-07-05"),
		Chain:     DefaultChain(), // chain_start nil
	})
	assert.Empty(t, dec.Sends)
	assert.Equal(t, EscalationNone, dec.EscalationState)
}

// TestEvaluateTripwire_OneMissL1: one miss with the day before completed ⇒
// exactly one L1 to the user with the survival floor named.
func TestEvaluateTripwire_OneMissL1(t *testing.T) {
	dec := EvaluateTripwire(TripwireInput{
		Now:       ts(2026, 7, 6, 9, 0),
		Loc:       time.UTC,
		Reference: refDay("2026-07-05"),
		Chain:     startedChain("2026-07-01"),
		Records:   recMap(completedDay("2026-07-04", ModeGreen)), // 07-05 absent
	})
	require.Len(t, dec.Sends, 1)
	s := dec.Sends[0]
	assert.Equal(t, ChannelUser, s.Channel)
	assert.Equal(t, SendL1, s.Kind)
	assert.False(t, s.Storm)
	assert.Equal(t, "one line, spoken or typed", s.Floor, "L1 names the survival floor")
	assert.Equal(t, EscalationL1, dec.EscalationState)
}

// TestEvaluateTripwire_TwoConsecutiveL2Witness: two consecutive misses with an
// armed witness ⇒ exactly one L2 to the witness, streak/mode only.
func TestEvaluateTripwire_TwoConsecutiveL2Witness(t *testing.T) {
	dec := EvaluateTripwire(TripwireInput{
		Now:       ts(2026, 7, 6, 9, 0),
		Loc:       time.UTC,
		Reference: refDay("2026-07-05"),
		Chain:     startedChain("2026-07-01"),
		Witness:   armedWitness(),
		Streak:    7,
		Records: recMap(
			completedDay("2026-07-03", ModeGreen),
			missedDay("2026-07-04"), // 07-05 absent → two consecutive
		),
	})
	require.Len(t, dec.Sends, 1)
	s := dec.Sends[0]
	assert.Equal(t, ChannelWitness, s.Channel)
	assert.Equal(t, SendL2, s.Kind)
	assert.Equal(t, 7, s.Streak)
	assert.Equal(t, ModeGreen, s.Mode)
	assert.Equal(t, EscalationL2, dec.EscalationState)
}

// TestEvaluateTripwire_L2BlockedWhenWitnessUnconfirmed: two consecutive misses
// with an unarmed witness ⇒ the L2 stage is reached (escalation l2_fired) but
// delivery is blocked and the user is notified instead.
func TestEvaluateTripwire_L2BlockedWhenWitnessUnconfirmed(t *testing.T) {
	dec := EvaluateTripwire(TripwireInput{
		Now:       ts(2026, 7, 6, 9, 0),
		Loc:       time.UTC,
		Reference: refDay("2026-07-05"),
		Chain:     startedChain("2026-07-01"),
		Records: recMap(
			completedDay("2026-07-03", ModeGreen),
			missedDay("2026-07-04"),
		),
	})
	require.Len(t, dec.Sends, 1)
	s := dec.Sends[0]
	assert.Equal(t, ChannelUser, s.Channel)
	assert.Equal(t, SendL2Blocked, s.Kind)
	assert.Equal(t, EscalationL2, dec.EscalationState, "the breach still reaches l2_fired")
}

// TestEvaluateTripwire_StormVariants: a standing storm fires the storm variants
// of L1 and L2 and never leaves the escalation ladder.
func TestEvaluateTripwire_StormVariants(t *testing.T) {
	storm := StormHistory{History: []StormEvent{
		{At: "2026-07-01T08:00:00Z", Event: StormDeclared},
		{At: "2026-07-01T09:00:00Z", Event: StormConfirmed, Through: "2026-07-28"},
	}}

	t.Run("one miss ⇒ L1 storm variant", func(t *testing.T) {
		dec := EvaluateTripwire(TripwireInput{
			Now: ts(2026, 7, 6, 9, 0), Loc: time.UTC, Reference: refDay("2026-07-05"),
			Chain: startedChain("2026-07-01"), Storm: storm,
			Records: recMap(completedDay("2026-07-04", ModeGreen)),
		})
		require.Len(t, dec.Sends, 1)
		assert.Equal(t, SendL1, dec.Sends[0].Kind)
		assert.True(t, dec.Sends[0].Storm, "a standing storm selects the L1 storm variant")
	})

	t.Run("two consecutive ⇒ L2 storm variant", func(t *testing.T) {
		dec := EvaluateTripwire(TripwireInput{
			Now: ts(2026, 7, 6, 9, 0), Loc: time.UTC, Reference: refDay("2026-07-05"),
			Chain: startedChain("2026-07-01"), Storm: storm, Witness: armedWitness(),
			Records: recMap(completedDay("2026-07-03", ModeGreen), missedDay("2026-07-04")),
		})
		require.Len(t, dec.Sends, 1)
		assert.Equal(t, SendL2, dec.Sends[0].Kind)
		assert.True(t, dec.Sends[0].Storm)
		assert.Equal(t, "2026-07-01", dec.Sends[0].ConfirmedDate)
		assert.Equal(t, EscalationL2, dec.EscalationState)
	})
}

// TestEvaluateTripwire_StormLapseNote: a pending declaration past 72h lapses
// with a user-channel note and appends the lapse event, with no escalation.
func TestEvaluateTripwire_StormLapseNote(t *testing.T) {
	storm := StormHistory{History: []StormEvent{{At: "2026-07-01T07:00:00Z", Event: StormDeclared, Label: "clause-1"}}}
	dec := EvaluateTripwire(TripwireInput{
		Now: ts(2026, 7, 5, 9, 0), Loc: time.UTC, Reference: refDay("2026-07-04"),
		Chain: startedChain("2026-07-01"), Storm: storm,
		Records: recMap(completedDay("2026-07-04", ModeGreen)),
	})
	s, ok := findSend(dec.Sends, SendStormLapse)
	require.True(t, ok)
	assert.Equal(t, ChannelUser, s.Channel)
	require.Len(t, dec.StormEvents, 1)
	assert.Equal(t, StormLapsed, dec.StormEvents[0].Event)
	assert.Equal(t, EscalationNone, dec.EscalationState)
}

// TestEscalationRun_AbsentDaysCount: absent days count as misses (the dead-man
// point), bounded by chain_start, and a completed day stops the run.
func TestEscalationRun_AbsentDaysCount(t *testing.T) {
	loc := time.UTC
	start := "2026-07-01"
	// 07-01 completed (chain start), 07-02..07-05 all absent.
	records := recMap(completedDay("2026-07-01", ModeGreen))
	assert.Equal(t, 4, escalationRun(records, refDay("2026-07-05"), &start, loc))

	// A completed day mid-run stops it.
	records = recMap(completedDay("2026-07-04", ModeGreen))
	assert.Equal(t, 1, escalationRun(records, refDay("2026-07-05"), &start, loc))

	// No chain_start ⇒ a single step, never an unbounded walk.
	assert.Equal(t, 1, escalationRun(map[string]DayRecord{}, refDay("2026-07-05"), nil, loc))
}

// TestSurvivalFloor falls back to the chain label when the survival link is
// missing.
func TestSurvivalFloor(t *testing.T) {
	assert.Equal(t, "one line, spoken or typed", survivalFloor(DefaultChain()))
	c := DefaultChain()
	c.SurvivalLink = "nope"
	assert.Equal(t, c.Label, survivalFloor(c))
}
