package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func strptr(s string) *string { return &s }

// TestWitnessContract_IsConfirmed: confirmed_at must be set and non-empty.
func TestWitnessContract_IsConfirmed(t *testing.T) {
	assert.False(t, WitnessContract{}.IsConfirmed(), "nil confirmed_at is not confirmed")
	assert.False(t, WitnessContract{ConfirmedAt: strptr("")}.IsConfirmed(), "empty confirmed_at is not confirmed")
	assert.True(t, WitnessContract{ConfirmedAt: strptr("2026-07-05T18:20:00-04:00")}.IsConfirmed())
}

// TestWitnessContract_LatestStatus reads the last lifecycle transition.
func TestWitnessContract_LatestStatus(t *testing.T) {
	assert.Empty(t, WitnessContract{}.LatestStatus(), "an empty history has no status")
	w := WitnessContract{StatusHistory: []WitnessTransition{
		{At: "t1", Status: WitnessBriefed},
		{At: "t2", Status: WitnessConfirmed},
	}}
	assert.Equal(t, WitnessConfirmed, w.LatestStatus())
}

// TestWitnessContract_IsLapsed: only a resignation or a lapse leaves the
// contract lapsed; a never-provisioned or freshly-confirmed witness is not.
func TestWitnessContract_IsLapsed(t *testing.T) {
	assert.False(t, WitnessContract{}.IsLapsed(), "unset witness is not lapsed")
	confirmed := WitnessContract{StatusHistory: []WitnessTransition{{Status: WitnessConfirmed}}}
	assert.False(t, confirmed.IsLapsed())
	for _, s := range []string{WitnessLapsedS, WitnessResigned} {
		w := WitnessContract{StatusHistory: []WitnessTransition{{Status: WitnessConfirmed}, {Status: s}}}
		assert.Truef(t, w.IsLapsed(), "latest status %q lapses the contract", s)
	}
	// A replacement re-confirmation clears the lapse.
	replaced := WitnessContract{StatusHistory: []WitnessTransition{{Status: WitnessResigned}, {Status: WitnessConfirmed}}}
	assert.False(t, replaced.IsLapsed())
}

// TestWitnessContract_L2Armed: L2 delivery requires confirmed + l2_enabled +
// not lapsed (engine-module.md §Consent amendment, §witness.json Lifecycle).
func TestWitnessContract_L2Armed(t *testing.T) {
	confirmedAt := strptr("2026-07-05T18:20:00-04:00")

	assert.False(t, WitnessContract{ConfirmedAt: confirmedAt, L2Enabled: false}.L2Armed(),
		"confirmed but l2 not enabled is disarmed")
	assert.False(t, WitnessContract{L2Enabled: true}.L2Armed(),
		"l2 enabled but unconfirmed is disarmed")
	assert.True(t, WitnessContract{ConfirmedAt: confirmedAt, L2Enabled: true}.L2Armed())

	lapsed := WitnessContract{
		ConfirmedAt:   confirmedAt,
		L2Enabled:     true,
		StatusHistory: []WitnessTransition{{Status: WitnessConfirmed}, {Status: WitnessResigned}},
	}
	assert.False(t, lapsed.L2Armed(), "a lapsed contract degrades to L1-only")
}
