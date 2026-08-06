package workout

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

// TestRecommendGuardrailDownshiftsProvocativePosition proves the second movement
// branch of the guardrail filter: a scheduled card whose movement is a
// provocative position is never Primary — it downshifts to recovery with the
// veto naming the reason (rule 4, provocative-position branch).
func TestRecommendGuardrailDownshiftsProvocativePosition(t *testing.T) {
	t.Parallel()

	prog := ExampleProgram()
	prog.Guardrails.ProvocativePositions = []string{"goblet squat"} // a legs-card movement

	got := Recommend(RecommendInput{Program: prog, Now: mustTime(t, mondayNoon), Loc: time.UTC})

	assert.Equal(t, "recovery", got.Primary.ID)
	require.NotEmpty(t, got.Vetoes)
	assert.Contains(t, got.Vetoes[0], "provocative position")
}

// TestRecoveryVetoSkipsPartWithoutRecoveryHours proves the recovery pass ignores a
// focus part the program declares no recovery window for: a non-light card whose
// focus is absent from RecoveryHours opens no debt, so it is never vetoed even
// with a recent non-light session on that part.
func TestRecoveryVetoSkipsPartWithoutRecoveryHours(t *testing.T) {
	t.Parallel()

	prog := ExampleProgram() // RecoveryHours has no "arms" entry
	card := Card{Focus: []string{"arms"}, Load: LoadHard}
	recent := []observations.Event{workoutEvent("2026-07-19T18:00:00Z", "garage", []string{"arms"}, 8)}

	part, vetoed := recoveryVeto(prog, card, recent, mustTime(t, mondayNoon), time.UTC)
	assert.False(t, vetoed, "a focus with no declared recovery window is never vetoed")
	assert.Empty(t, part)
}

// TestLastNonLightLoadSkipsNonWorkout proves the load scan ignores a body-state
// event mixed into the recent slice, reading its answer only from the real
// workout that loaded the part.
func TestLastNonLightLoadSkipsNonWorkout(t *testing.T) {
	t.Parallel()

	recent := []observations.Event{
		bodyStateEvent("legs", 3), // wrong kind — must be skipped
		workoutEvent("2026-07-19T18:00:00Z", "legs", nil, 0),
	}
	at, found := lastNonLightLoad(ExampleProgram(), recent, "legs", time.UTC)
	require.True(t, found, "the real workout still registers past the skipped body-state event")
	assert.Equal(t, mustTime(t, "2026-07-19T18:00:00Z"), at)
}

// TestMatchCardEmptyType proves a blank workout type matches no card (the
// normalized-empty guard), so an untyped session never mis-binds to a library
// card.
func TestMatchCardEmptyType(t *testing.T) {
	t.Parallel()

	_, ok := matchCard(ExampleProgram(), "   ")
	assert.False(t, ok, "a whitespace-only type resolves to no card")
}

// TestNextClearCardSkipsUnresolvedAndBlocked proves the forward scan steps over a
// scheduled id that resolves to no library card and over a card the guardrails
// block, returning no clear alternative when every scanned day is unusable.
func TestNextClearCardSkipsUnresolvedAndBlocked(t *testing.T) {
	t.Parallel()

	logDay := mustTime(t, mondayNoon) // Monday; the scan starts on Tuesday

	// A rotation entry naming a card id that is not in the library: the scan
	// resolves the id but prog.card misses, so the day is skipped.
	unresolved := Program{
		Cards:    []Card{{ID: "real", Name: "Real", Load: LoadHard}},
		Rotation: []RotationEntry{{Weekday: "tue", Card: "ghost"}},
	}
	_, ok := nextClearCard(unresolved, logDay, nil, logDay, time.UTC)
	assert.False(t, ok, "a schedule id with no library card yields no alternative")

	// The only upcoming card is guardrail-blocked, so the scan skips it too.
	blocked := Program{
		Cards:      []Card{{ID: "bad", Name: "Bad", Load: LoadHard, Movements: []string{"goblet squat"}}},
		Rotation:   []RotationEntry{{Weekday: "tue", Card: "bad"}},
		Guardrails: Guardrails{AvoidMovements: []string{"goblet squat"}},
	}
	_, ok = nextClearCard(blocked, logDay, nil, logDay, time.UTC)
	assert.False(t, ok, "a guardrail-blocked alternative is not a clear card")
}

// TestPainHardStopDefaultsThreshold proves a program that sets no
// pain_flag_threshold falls to the default (5): a body_state.pain at the default
// on a targeted part still hard-stops.
func TestPainHardStopDefaultsThreshold(t *testing.T) {
	t.Parallel()

	prog := ExampleProgram()
	prog.PainFlagThreshold = 0 // unset — the recommender applies the default

	got := Recommend(RecommendInput{
		Program:   prog,
		Now:       mustTime(t, mondayNoon),
		BodyState: []observations.Event{bodyStateEvent("legs", defaultPainThreshold)},
		Loc:       time.UTC,
	})

	require.NotNil(t, got.HardStop, "pain at the default threshold on a targeted part hard-stops")
	assert.Equal(t, "recovery", got.Primary.ID)
}

// TestPainFlaggedPartSkipsNonBodyStateAndMissingPart proves the pain scan ignores
// a non-body-state event and a body-state reading carrying no body_part, flagging
// only the well-formed reading at or above the threshold.
func TestPainFlaggedPartSkipsNonBodyStateAndMissingPart(t *testing.T) {
	t.Parallel()

	legs, ok := ExampleProgram().card("legs")
	require.True(t, ok)

	events := []observations.Event{
		workoutEvent("2026-07-19T18:00:00Z", "legs", nil, 0),                   // wrong kind — skipped
		{Kind: observations.KindBodyState, Payload: map[string]any{"pain": 7}}, // no body_part — skipped
		bodyStateEvent("legs", 7),                                              // the real flag
	}
	part, flagged := painFlaggedPart(legs, events, 5)
	require.True(t, flagged, "the well-formed reading flags past the two skipped events")
	assert.Equal(t, "legs", part)
}

// TestInjuryPartFallsBackToDisplayName proves the injury-part reader prefers a
// named field but falls back to the record's display name when none is present.
func TestInjuryPartFallsBackToDisplayName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "legs", injuryPart(observations.Registry{Fields: map[string]any{"body_part": "legs"}}),
		"a named field wins")
	assert.Equal(t, "left knee", injuryPart(observations.Registry{DisplayName: "left knee"}),
		"with no named field the display name is the part")
}

// TestFallbackForDownshiftsHardCardWithoutEasier proves the last fallback door:
// a non-light primary with no easier variant downshifts to the program's recovery
// card rather than offering itself again.
func TestFallbackForDownshiftsHardCardWithoutEasier(t *testing.T) {
	t.Parallel()

	fb := fallbackFor(ExampleProgram(), Card{ID: "x", Load: LoadHard})
	assert.Equal(t, "recovery", fb.ID, "a hard card with no easier variant falls back to recovery")
	assert.Equal(t, LoadNone, fb.Load)
}
