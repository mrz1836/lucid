package workout

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

// mondayNoon is a Monday in the example program's rotation (legs day) at midday —
// well past the 04:00 rollover, so the logical day is its own civil date.
const mondayNoon = "2026-07-20T12:00:00Z"

// --- Rotation: today's card resolves from the calendar/rotation. ---

// TestRecommendRotationPicksTodaysCard proves the plain rotation resolves today's
// card from the weekday, and that a dated calendar override wins over it — rule 1
// of the recommender contract.
func TestRecommendRotationPicksTodaysCard(t *testing.T) {
	t.Parallel()

	in := RecommendInput{
		Program: ExampleProgram(),
		Now:     mustTime(t, mondayNoon),
		Loc:     time.UTC,
	}
	got := Recommend(in)

	assert.Equal(t, "legs", got.Primary.ID, "Monday resolves the legs card from the rotation")
	assert.Equal(t, "legs_easier", got.Fallback.ID, "the fallback is the card's easier variant")
	assert.Nil(t, got.HardStop, "an ordinary day emits no hard stop")

	// A dated calendar entry for today overrides the weekday rotation.
	prog := ExampleProgram()
	prog.Calendar = []CalendarEntry{{Date: "2026-07-20", Card: "recovery"}}
	in.Program = prog
	override := Recommend(in)
	assert.Equal(t, "recovery", override.Primary.ID, "a dated calendar entry wins over the rotation")
}

// --- Missing data: no recent events → plain-calendar fallback. ---

// TestRecommendMissingDataFallsToCalendar proves that with no workout or
// body-state history the recommender follows the plain program calendar, emits no
// hard stop and no veto, and says so in the reason — rule 6. Missing data never
// blocks a recommendation.
func TestRecommendMissingDataFallsToCalendar(t *testing.T) {
	t.Parallel()

	got := Recommend(RecommendInput{
		Program: ExampleProgram(),
		Now:     mustTime(t, mondayNoon),
		Loc:     time.UTC,
	})

	assert.Equal(t, "legs", got.Primary.ID)
	assert.NotEmpty(t, got.Fallback.Name, "a fallback door is always present")
	assert.Nil(t, got.HardStop)
	assert.Empty(t, got.Vetoes, "nothing is vetoed with no history")
	assert.Contains(t, got.Reason, "No recent workout or body-state logs")
}

// TestRecommendEmptyProgramIsHonestRest proves a program with no card scheduled
// for today degrades to an honest recovery day rather than an empty or panicking
// result.
func TestRecommendEmptyProgramIsHonestRest(t *testing.T) {
	t.Parallel()

	got := Recommend(RecommendInput{
		Program: Program{Version: ProgramSchema, ProgramID: "empty", Cards: []Card{{ID: "x", Name: "X", Load: LoadModerate}}},
		Now:     mustTime(t, mondayNoon),
		Loc:     time.UTC,
	})

	assert.Equal(t, "rest", got.Primary.ID, "no scheduled card → the synthesized rest card")
	assert.Equal(t, LoadNone, got.Primary.Load)
	assert.NotEmpty(t, got.Reason)
}

// --- Recovery constraint: no leg day twice inside the recovery window. ---

// TestRecommendRecoveryVetoRotatesFocus proves the no-repeat-without-recovery
// guardrail: legs loaded hard yesterday cannot be the hard focus again today, so
// the recommender rotates to the next clear card and records the veto — rule 2.
func TestRecommendRecoveryVetoRotatesFocus(t *testing.T) {
	t.Parallel()

	got := Recommend(RecommendInput{
		Program: ExampleProgram(),
		Now:     mustTime(t, mondayNoon),
		// Legs, hard, ~18h ago — inside the 48h legs recovery window.
		RecentWorkouts: []observations.Event{workoutEvent("2026-07-19T18:00:00Z", "legs", nil, 0)},
		Loc:            time.UTC,
	})

	assert.NotEqual(t, "legs", got.Primary.ID, "legs is still recovering — never leg day twice")
	assert.Equal(t, "push", got.Primary.ID, "the rotation's next clear focus is picked instead")
	require.NotEmpty(t, got.Vetoes)
	assert.Contains(t, got.Vetoes[0], "recovery window")
	assert.Contains(t, got.Reason, "rotates to")
}

// TestRecommendRecoveryVetoDownshiftsWhenNothingClear proves the pathological
// "legs every day" program can't push leg day twice: when every upcoming rotation
// card is still recovering, the recommender downshifts to a recovery session — the
// second half of rule 2.
func TestRecommendRecoveryVetoDownshiftsWhenNothingClear(t *testing.T) {
	t.Parallel()

	prog := legsEveryDayProgram()
	require.NoError(t, prog.Validate())

	got := Recommend(RecommendInput{
		Program:        prog,
		Now:            mustTime(t, mondayNoon),
		RecentWorkouts: []observations.Event{workoutEvent("2026-07-19T18:00:00Z", "legs", nil, 0)},
		Loc:            time.UTC,
	})

	assert.Equal(t, "recovery", got.Primary.ID, "no clear focus anywhere → downshift to recovery")
	assert.Equal(t, LoadNone, got.Primary.Load)
	require.NotEmpty(t, got.Vetoes)
	assert.Contains(t, got.Reason, "downshifts")
}

// TestRecommendRecoveredFocusIsNotVetoed proves the veto releases once the window
// clears: a legs session three days ago is fully recovered, so today's legs card
// stands.
func TestRecommendRecoveredFocusIsNotVetoed(t *testing.T) {
	t.Parallel()

	got := Recommend(RecommendInput{
		Program: ExampleProgram(),
		Now:     mustTime(t, mondayNoon),
		// Legs, 3 days ago — well past the 48h window.
		RecentWorkouts: []observations.Event{workoutEvent("2026-07-17T12:00:00Z", "legs", nil, 0)},
		Loc:            time.UTC,
	})

	assert.Equal(t, "legs", got.Primary.ID, "a recovered focus is no longer vetoed")
	assert.Empty(t, got.Vetoes)
}

// TestRecommendLightLoadDoesNotOpenRecoveryWindow proves a light session never
// opens a recovery debt: an easy legs session yesterday does not veto today's
// legs card.
func TestRecommendLightLoadDoesNotOpenRecoveryWindow(t *testing.T) {
	t.Parallel()

	got := Recommend(RecommendInput{
		Program: ExampleProgram(),
		Now:     mustTime(t, mondayNoon),
		// An explicit light session (rpe 2) loading legs yesterday.
		RecentWorkouts: []observations.Event{workoutEvent("2026-07-19T18:00:00Z", "easy legs", []string{"legs"}, 2)},
		Loc:            time.UTC,
	})

	assert.Equal(t, "legs", got.Primary.ID, "a light load never triggers the recovery veto")
	assert.Empty(t, got.Vetoes)
}

// --- Pain-flag hard stop. ---

// TestRecommendPainFlagHardStop proves a body_state.pain at or above the program
// threshold on a targeted part emits a hard stop and downshifts the primary —
// rule 3.
func TestRecommendPainFlagHardStop(t *testing.T) {
	t.Parallel()

	got := Recommend(RecommendInput{
		Program:   ExampleProgram(),
		Now:       mustTime(t, mondayNoon),
		BodyState: []observations.Event{bodyStateEvent("legs", 7)},
		Loc:       time.UTC,
	})

	require.NotNil(t, got.HardStop, "a pain flag on a targeted part must emit a hard stop")
	assert.Contains(t, got.HardStop.Name, "Back off")
	assert.Contains(t, got.HardStop.Name, "legs")
	assert.Equal(t, "recovery", got.Primary.ID, "the primary downshifts to recovery")
	assert.Contains(t, got.Reason, "pain signal on legs")
	require.NotEmpty(t, got.Vetoes)
	assert.Contains(t, got.Vetoes[0], "pain signal")
}

// TestRecommendPainOnUntargetedPartDoesNotStop proves the hard stop is targeted:
// pain on a part today's card does not load does not back the session off.
func TestRecommendPainOnUntargetedPartDoesNotStop(t *testing.T) {
	t.Parallel()

	got := Recommend(RecommendInput{
		Program:   ExampleProgram(),
		Now:       mustTime(t, mondayNoon),
		BodyState: []observations.Event{bodyStateEvent("shoulders", 8)},
		Loc:       time.UTC,
	})

	assert.Nil(t, got.HardStop, "pain on an untargeted part is not a hard stop for today's card")
	assert.Equal(t, "legs", got.Primary.ID)
}

// TestRecommendPainBelowThresholdDoesNotStop proves a pain below the threshold is
// inventory, not a back-off signal.
func TestRecommendPainBelowThresholdDoesNotStop(t *testing.T) {
	t.Parallel()

	got := Recommend(RecommendInput{
		Program:   ExampleProgram(),
		Now:       mustTime(t, mondayNoon),
		BodyState: []observations.Event{bodyStateEvent("legs", 3)},
		Loc:       time.UTC,
	})

	assert.Nil(t, got.HardStop)
	assert.Equal(t, "legs", got.Primary.ID)
}

// TestRecommendActiveInjuryHardStop proves an active injury naming a targeted
// part is its own back-off signal even with no logged pain, and that a resolved
// injury is not — rule 3's injury-registry branch.
func TestRecommendActiveInjuryHardStop(t *testing.T) {
	t.Parallel()

	active := Recommend(RecommendInput{
		Program: ExampleProgram(),
		Now:     mustTime(t, mondayNoon),
		Injuries: []observations.Registry{{
			Kind: observations.RegistryInjury, Status: observations.StatusActive,
			DisplayName: "left knee", Fields: map[string]any{"body_part": "legs"},
		}},
		Loc: time.UTC,
	})
	require.NotNil(t, active.HardStop, "an active injury on a targeted part hard-stops")
	assert.Equal(t, "recovery", active.Primary.ID)

	resolved := Recommend(RecommendInput{
		Program: ExampleProgram(),
		Now:     mustTime(t, mondayNoon),
		Injuries: []observations.Registry{{
			Kind: observations.RegistryInjury, Status: observations.StatusResolved,
			DisplayName: "left knee", Fields: map[string]any{"body_part": "legs"},
		}},
		Loc: time.UTC,
	})
	assert.Nil(t, resolved.HardStop, "a resolved injury is not a hard stop")
	assert.Equal(t, "legs", resolved.Primary.ID)
}

// --- Purity / determinism. ---

// TestRecommendIsDeterministic proves the core is pure: the same input yields a
// byte-identical recommendation. The signature carries no provider and no storage
// handle, so zero model calls and zero disk I/O are structural, not incidental.
func TestRecommendIsDeterministic(t *testing.T) {
	t.Parallel()

	in := RecommendInput{
		Program:        ExampleProgram(),
		Now:            mustTime(t, mondayNoon),
		RecentWorkouts: []observations.Event{workoutEvent("2026-07-19T18:00:00Z", "legs", nil, 0)},
		BodyState:      []observations.Event{bodyStateEvent("back", 2)},
		Loc:            time.UTC,
	}
	first := Recommend(in)
	second := Recommend(in)
	assert.Equal(t, first, second)
}

// TestRecommendNilLocationDefaultsUTC proves a nil location does not panic and
// resolves the day in UTC.
func TestRecommendNilLocationDefaultsUTC(t *testing.T) {
	t.Parallel()

	got := Recommend(RecommendInput{Program: ExampleProgram(), Now: mustTime(t, mondayNoon)})
	assert.Equal(t, "legs", got.Primary.ID)
}

// --- Guardrail filter + equipment/time veto (rules 4 and 5). ---

// TestRecommendGuardrailDownshiftsAvoidMovement proves a scheduled card whose
// movement is on the avoid list is never Primary — it downshifts to a safe
// recovery session with the veto recorded (rule 4, movement branch).
func TestRecommendGuardrailDownshiftsAvoidMovement(t *testing.T) {
	t.Parallel()

	prog := ExampleProgram()
	prog.Guardrails.AvoidMovements = []string{"goblet squat"} // a legs-card movement

	got := Recommend(RecommendInput{Program: prog, Now: mustTime(t, mondayNoon), Loc: time.UTC})

	assert.Equal(t, "recovery", got.Primary.ID)
	require.NotEmpty(t, got.Vetoes)
	assert.Contains(t, got.Vetoes[0], "avoid list")
}

// TestRecommendGuardrailDownshiftsNoStrengthenFocus proves a card whose focus is
// on the do-not-strengthen list is filtered out (rule 4, focus branch).
func TestRecommendGuardrailDownshiftsNoStrengthenFocus(t *testing.T) {
	t.Parallel()

	prog := ExampleProgram()
	prog.Guardrails.NoStrengthen = []string{"legs"}

	got := Recommend(RecommendInput{Program: prog, Now: mustTime(t, mondayNoon), Loc: time.UTC})

	assert.Equal(t, "recovery", got.Primary.ID)
	require.NotEmpty(t, got.Vetoes)
	assert.Contains(t, got.Vetoes[0], "do-not-strengthen list")
}

// TestRecommendEquipmentVetoDropsToEasier proves a card the operator cannot run
// (equipment it lacks) is vetoed to its easier variant (rule 5).
func TestRecommendEquipmentVetoDropsToEasier(t *testing.T) {
	t.Parallel()

	prog := ExampleProgram()
	for i := range prog.Cards {
		if prog.Cards[i].ID == "legs" {
			prog.Cards[i].Equipment = []string{"barbell"} // not in the program's equipment
		}
	}

	got := Recommend(RecommendInput{Program: prog, Now: mustTime(t, mondayNoon), Loc: time.UTC})

	assert.Equal(t, "legs_easier", got.Primary.ID, "an unrunnable card drops to its easier variant")
	require.NotEmpty(t, got.Vetoes)
	assert.Contains(t, got.Vetoes[0], "equipment or time")
}

// --- Pure-helper coverage: payload decoders, clocks, load inference. ---

// TestPayloadHelpers exercises the payload decoders across the int/float64/[]any
// forms a freshly parsed capture and a JSONL round-trip each produce.
func TestPayloadHelpers(t *testing.T) {
	t.Parallel()

	p := map[string]any{
		"s":       "  hi  ",
		"empty":   "   ",
		"i":       7,
		"i64":     int64(9),
		"f":       6.0,
		"parts":   []any{"legs", "", 3, "back"},
		"partsss": []string{"chest"},
	}

	s, ok := payloadString(p, "s")
	assert.True(t, ok)
	assert.Equal(t, "hi", s)
	_, ok = payloadString(p, "empty")
	assert.False(t, ok, "a whitespace-only value is not a value")
	_, ok = payloadString(p, "missing")
	assert.False(t, ok)

	for key, want := range map[string]int{"i": 7, "i64": 9, "f": 6} {
		v, iok := payloadInt(p, key)
		assert.True(t, iok, key)
		assert.Equal(t, want, v, key)
	}
	_, ok = payloadInt(p, "s")
	assert.False(t, ok, "a string is not an int")

	assert.Equal(t, []string{"legs", "back"}, payloadStrings(p, "parts"), "[]any drops non-strings and blanks")
	assert.Equal(t, []string{"chest"}, payloadStrings(p, "partsss"))
	assert.Nil(t, payloadStrings(p, "missing"))
}

// TestEventTimeFallbacks proves occurred_at parses as RFC3339, falls back to the
// logical date, and yields the zero time on a fully unparseable stamp (which
// reads as fully recovered — a safe degrade).
func TestEventTimeFallbacks(t *testing.T) {
	t.Parallel()

	rfc := eventTime(observations.Event{OccurredAt: "2026-07-19T18:00:00Z"}, time.UTC)
	assert.Equal(t, mustTime(t, "2026-07-19T18:00:00Z"), rfc)

	logical := eventTime(observations.Event{OccurredAt: "nonsense", LogicalDate: "2026-07-19"}, time.UTC)
	assert.Equal(t, mustTime(t, "2026-07-19T00:00:00Z"), logical)

	assert.True(t, eventTime(observations.Event{OccurredAt: "nonsense"}, time.UTC).IsZero())
}

// TestInferredLoad proves the rpe→load mapping and the unqualified-session
// default (a logged session of unknown intensity is a real load).
func TestInferredLoad(t *testing.T) {
	t.Parallel()

	assert.Equal(t, LoadModerate, inferredLoad(map[string]any{}), "no rpe → a real load, not ignored")
	assert.Equal(t, LoadLight, inferredLoad(map[string]any{"rpe": 2}))
	assert.Equal(t, LoadModerate, inferredLoad(map[string]any{"rpe": 5}))
	assert.Equal(t, LoadHard, inferredLoad(map[string]any{"rpe": 9}))
}

// TestMatchCardByName proves a workout event's type matches a card by name as
// well as by id, so an unqualified card load still opens the right recovery
// window. Here a hard "Legs + hips" session logged by its display name yesterday
// vetoes today's legs card.
func TestMatchCardByName(t *testing.T) {
	t.Parallel()

	got := Recommend(RecommendInput{
		Program:        ExampleProgram(),
		Now:            mustTime(t, mondayNoon),
		RecentWorkouts: []observations.Event{workoutEvent("2026-07-19T18:00:00Z", "Legs + hips", nil, 0)},
		Loc:            time.UTC,
	})
	assert.NotEqual(t, "legs", got.Primary.ID, "a session matched by card name still opens the recovery window")
}

// TestUnqualifiedWorkoutOpensRecoveryWindow proves a workout that names no card
// but lists a body part (a high-rpe "hard" session) opens that part's window via
// inferred load.
func TestUnqualifiedWorkoutOpensRecoveryWindow(t *testing.T) {
	t.Parallel()

	got := Recommend(RecommendInput{
		Program:        ExampleProgram(),
		Now:            mustTime(t, mondayNoon),
		RecentWorkouts: []observations.Event{workoutEvent("2026-07-19T18:00:00Z", "garage session", []string{"legs"}, 8)},
		Loc:            time.UTC,
	})
	assert.NotEqual(t, "legs", got.Primary.ID, "an inferred-hard load on legs still vetoes leg day")
}

// TestCanRunMinuteBudget proves the time half of the equipment/time veto.
func TestCanRunMinuteBudget(t *testing.T) {
	t.Parallel()

	prog := Program{SessionMinutes: 50}
	assert.True(t, canRun(prog, Card{Minutes: 45}))
	assert.False(t, canRun(prog, Card{Minutes: 90}))
	assert.True(t, canRun(prog, Card{}), "a card with no requirements is always runnable")
}

// TestDownshiftCardPrefersProgramRecovery proves the downshift prefers a
// program-authored recovery/light card, then a light one, then the synthesized
// rest card.
func TestDownshiftCardPrefersProgramRecovery(t *testing.T) {
	t.Parallel()

	none := Program{Cards: []Card{{ID: "r", Load: LoadNone}}}
	assert.Equal(t, "r", downshiftCard(none).ID)

	lightOnly := Program{Cards: []Card{{ID: "hard", Load: LoadHard}, {ID: "l", Load: LoadLight}}}
	assert.Equal(t, "l", downshiftCard(lightOnly).ID)

	neither := Program{Cards: []Card{{ID: "hard", Load: LoadHard}}}
	assert.Equal(t, "rest", downshiftCard(neither).ID, "no recovery card → the synthesized rest card")
}

// TestEasierAsCardInheritance proves the easier-variant promotion inherits the
// parent id/focus and defaults a missing load to light, and returns the card
// unchanged when it has no easier variant.
func TestEasierAsCardInheritance(t *testing.T) {
	t.Parallel()

	parent := Card{ID: "push", Focus: []string{"chest"}, Load: LoadModerate, Easier: &Card{Name: "Easy push"}}
	e := easierAsCard(parent)
	assert.Equal(t, "push_easier", e.ID)
	assert.Equal(t, []string{"chest"}, e.Focus)
	assert.Equal(t, LoadLight, e.Load, "a missing easier load defaults to light")
	assert.Nil(t, e.Easier)

	noEasier := Card{ID: "solo", Load: LoadLight}
	assert.Equal(t, noEasier, easierAsCard(noEasier))
}

// TestMatchingEdgeCases covers the empty-input branches of the string matchers so
// a blank part or guardrail never false-matches.
func TestMatchingEdgeCases(t *testing.T) {
	t.Parallel()

	assert.False(t, matchesAny("", []string{"legs"}))
	assert.False(t, matchesAny("legs", []string{""}))
	assert.False(t, overlaps("", "legs"))
	assert.False(t, overlaps("legs", ""))
	assert.Equal(t, "that area", humanizePart("   "))
	assert.Equal(t, "back", cardLabel(Card{Name: "back"}), "an id-less card labels by name")
}

// --- helpers ---

// mustTime parses an RFC3339 stamp or fails the test.
func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	require.NoError(t, err)
	return ts
}

// workoutEvent builds a KindWorkout event with the given occurred_at, type, and
// optional body parts / session rpe — the shape the storage adapter writes.
func workoutEvent(occurredAt, typ string, bodyParts []string, rpe int) observations.Event {
	payload := map[string]any{"type": typ}
	if len(bodyParts) > 0 {
		payload["body_parts"] = bodyParts
	}
	if rpe > 0 {
		payload["rpe"] = rpe
	}
	return observations.Event{
		Kind:       observations.KindWorkout,
		OccurredAt: occurredAt,
		Payload:    payload,
	}
}

// bodyStateEvent builds a KindBodyState event pairing a body part with a pain
// score — the recommender's hard-stop signal. Its occurred_at is a fixed morning
// stamp: the recommender reads recency from the caller's bounded slice, so the
// exact time does not affect the pain guardrail.
func bodyStateEvent(part string, pain int) observations.Event {
	return observations.Event{
		Kind:       observations.KindBodyState,
		OccurredAt: "2026-07-20T07:00:00Z",
		Payload:    map[string]any{"body_part": part, "pain": pain},
	}
}

// legsEveryDayProgram is the pathological rotation the recovery guardrail must
// tame: every weekday is legs, with a recovery card to downshift to.
func legsEveryDayProgram() Program {
	return Program{
		Version:   ProgramSchema,
		ProgramID: "legs_only",
		Cards: []Card{
			{
				ID: "legs", Name: "Legs", Focus: []string{"legs"}, Load: LoadHard, Movements: []string{"squat"},
				Easier: &Card{Name: "Easy legs", Load: LoadLight, Movements: []string{"bodyweight squat"}},
			},
			{ID: "recovery", Name: "Recovery", Load: LoadNone, Movements: []string{"walk"}},
		},
		Rotation: []RotationEntry{
			{Weekday: "mon", Card: "legs"},
			{Weekday: "tue", Card: "legs"},
			{Weekday: "wed", Card: "legs"},
			{Weekday: "thu", Card: "legs"},
			{Weekday: "fri", Card: "legs"},
			{Weekday: "sat", Card: "legs"},
			{Weekday: "sun", Card: "legs"},
		},
		RecoveryHours:     map[string]int{"legs": 48},
		PainFlagThreshold: 5,
	}
}
