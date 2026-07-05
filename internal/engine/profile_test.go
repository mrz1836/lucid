package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGoverningProfile_DefaultBeforeAnySwitch(t *testing.T) {
	assert.Equal(t, DefaultProfile, GoverningProfile(at(2026, 7, 8, 11, 0), nil, time.UTC))
}

func TestGoverningProfile_ByWallDate(t *testing.T) {
	// Switch to nights effective 2026-07-08.
	history := []ProfileSwitch{{From: DefaultProfile, To: "nights", Effective: "2026-07-08"}}

	// The switch night (07-07): still default clocks.
	assert.Equal(t, DefaultProfile, GoverningProfile(at(2026, 7, 7, 22, 30), history, time.UTC))
	// The next day (07-08), even at 11:00 (before the nights rollover): nights.
	assert.Equal(t, "nights", GoverningProfile(at(2026, 7, 8, 11, 0), history, time.UTC))
}

func TestGoverningProfile_LatestApplicableWins(t *testing.T) {
	history := []ProfileSwitch{
		{To: "nights", Effective: "2026-07-08"},
		{To: DefaultProfile, Effective: "2026-07-12"},
	}
	assert.Equal(t, "nights", GoverningProfile(at(2026, 7, 10, 9, 0), history, time.UTC))
	assert.Equal(t, DefaultProfile, GoverningProfile(at(2026, 7, 12, 9, 0), history, time.UTC))
}

func TestGoverningProfile_SkipsMalformedEffective(t *testing.T) {
	history := []ProfileSwitch{{To: "nights", Effective: "garbage"}}
	assert.Equal(t, DefaultProfile, GoverningProfile(at(2026, 7, 8, 11, 0), history, nil))
}

// TestComputeSwitch_EffectiveNextLogicalDay is the "effective from the next
// logical day, never the current" rule: a switch after tonight's bell
// takes effect tomorrow.
func TestComputeSwitch_EffectiveNextLogicalDay(t *testing.T) {
	chain := DefaultChain()
	state := DefaultProfileState()
	sw, err := ComputeSwitch(chain, state, "nights", at(2026, 7, 7, 21, 50))
	require.NoError(t, err)
	assert.Equal(t, DefaultProfile, sw.From)
	assert.Equal(t, "nights", sw.To)
	assert.Equal(t, "2026-07-08", sw.Effective) // current logical day 07-07 + 1
	_, err = time.Parse(time.RFC3339, sw.At)
	assert.NoError(t, err)
}

func TestComputeSwitch_UndefinedProfileRejected(t *testing.T) {
	_, err := ComputeSwitch(DefaultChain(), DefaultProfileState(), "weekends", at(2026, 7, 7, 21, 50))
	assert.Error(t, err)
}

func TestWithSwitch_AppendsAndSetsActive(t *testing.T) {
	state := DefaultProfileState()
	sw := ProfileSwitch{From: DefaultProfile, To: "nights", Effective: "2026-07-08"}
	next := state.WithSwitch(sw)
	assert.Equal(t, "nights", next.Active)
	assert.Len(t, next.History, 1)
	// Original state is unchanged (append-only, copy semantics).
	assert.Equal(t, DefaultProfile, state.Active)
	assert.Empty(t, state.History)
}

func TestBuildStatus(t *testing.T) {
	start := "2026-07-03"
	recs := []DayRecord{completed("2026-07-03"), completed("2026-07-04"), completed("2026-07-05")}
	st := BuildStatus(recs, &start, "nights", time.UTC)
	assert.Equal(t, 3, st.CurrentStreak)
	assert.Equal(t, 3, st.LongestStreak)
	assert.Equal(t, &start, st.ChainStart)
	assert.Equal(t, 3, st.RawDaysAccounted)
	assert.Equal(t, EscalationNone, st.EscalationState)
	assert.Equal(t, StormNone, st.StormState)
	assert.Equal(t, "nights", st.ActiveProfile)

	// Empty active profile defaults.
	assert.Equal(t, DefaultProfile, BuildStatus(nil, nil, "", time.UTC).ActiveProfile)
}
