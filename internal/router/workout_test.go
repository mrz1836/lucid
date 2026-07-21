package router

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/provider"
)

// bootedWorkout returns a booted router whose observations config enables
// exactly the given kinds — so a test can exercise the workout-kind and
// body_state-kind gates independently.
func bootedWorkout(t *testing.T, kinds ...observations.Kind) *Router {
	t.Helper()
	a := newScaffolded(t)
	require.NoError(t, a.ScaffoldObservations())
	cfg, err := a.ReadObservationsConfig()
	require.NoError(t, err)
	cfg.KindsEnabled = kinds
	require.NoError(t, a.SaveObservationsConfig(cfg))
	r := New(a)
	_, err = r.Boot()
	require.NoError(t, err)
	return r
}

func intPtr(n int) *int { return &n }

// eventByID reads back one appended event by id from its logical day.
func eventByID(t *testing.T, r *Router, date, id string) observations.Event {
	t.Helper()
	events, _, err := r.Store().ReadObservationsDay(date)
	require.NoError(t, err)
	for _, ev := range events {
		if ev.ID == id {
			return ev
		}
	}
	t.Fatalf("event %s not found in %s", id, date)
	return observations.Event{}
}

// TestWorkoutLog_StructuredWritesEvents: the structured path writes one valid
// workout envelope plus one body_state envelope per reading, and both round-trip
// with their payloads intact (AC-2, AC-6).
func TestWorkoutLog_StructuredWritesEvents(t *testing.T) {
	r := bootedWorkout(t, observations.KindWorkout, observations.KindBodyState)
	res, err := r.WorkoutLog(WorkoutLogRequest{
		Type:        "push",
		Movements:   []string{"bench", "ohp"},
		DurationMin: 45,
		RPE:         intPtr(7),
		BodyParts:   []string{"chest", "shoulders"},
		BodyStates: []BodyStateInput{
			{Part: "shoulder", Soreness: intPtr(4)},
			{Part: "knee", Pain: intPtr(6)},
		},
		Notes: "felt strong",
		Now:   nowEDT(),
	})
	require.NoError(t, err)
	assert.False(t, res.Rejected)
	assert.Equal(t, observations.KindWorkout, res.Kind)
	require.NotEmpty(t, res.WorkoutID)
	require.Len(t, res.BodyStateIDs, 2)
	assert.Contains(t, res.Ack, "Logged workout as `")
	assert.Contains(t, res.Ack, "2 body-state readings")

	w := eventByID(t, r, res.LogicalDate, res.WorkoutID)
	require.NoError(t, w.Validate())
	assert.Equal(t, observations.KindWorkout, w.Kind)
	assert.Equal(t, observations.SourceMicrolog, w.Source)
	assert.Equal(t, "push", w.Payload["type"])
	assert.EqualValues(t, 45, w.Payload["duration_min"])
	assert.EqualValues(t, 7, w.Payload["rpe"])
	assert.Equal(t, "felt strong", w.Payload["note"])
	require.Contains(t, w.Payload, "body_parts")
	require.Contains(t, w.Payload, "movements")

	sh := eventByID(t, r, res.LogicalDate, res.BodyStateIDs[0])
	assert.Equal(t, observations.KindBodyState, sh.Kind)
	assert.Equal(t, "shoulder", sh.Payload["body_part"])
	assert.EqualValues(t, 4, sh.Payload["soreness"])
	assert.NotContains(t, sh.Payload, "pain")

	kn := eventByID(t, r, res.LogicalDate, res.BodyStateIDs[1])
	assert.Equal(t, "knee", kn.Payload["body_part"])
	assert.EqualValues(t, 6, kn.Payload["pain"])
}

// TestWorkoutLog_DisabledKindRejected: a disabled workout kind is rejected with
// the enable hint and nothing is written (AC-2 gate).
func TestWorkoutLog_DisabledKindRejected(t *testing.T) {
	r := bootedWorkout(t) // no kinds enabled
	res, err := r.WorkoutLog(WorkoutLogRequest{Type: "push", Now: nowEDT()})
	require.NoError(t, err)
	assert.True(t, res.Rejected)
	assert.Empty(t, res.WorkoutID)
	assert.Equal(t, observations.EnableHint(observations.KindWorkout), res.Ack)

	events, _, err := r.Store().ReadObservationsDay(observations.DateString(observations.DateOf(nowEDT())))
	require.NoError(t, err)
	assert.Empty(t, events, "a rejected capture writes nothing")
}

// TestWorkoutLog_BodyStateDisabledSkipsReadings: with only the workout kind
// enabled, the session still logs but its body-state readings are skipped —
// capture is never blocked on a disabled sibling kind.
func TestWorkoutLog_BodyStateDisabledSkipsReadings(t *testing.T) {
	r := bootedWorkout(t, observations.KindWorkout)
	res, err := r.WorkoutLog(WorkoutLogRequest{
		Type:       "legs",
		BodyStates: []BodyStateInput{{Part: "quads", Soreness: intPtr(5)}},
		Now:        nowEDT(),
	})
	require.NoError(t, err)
	require.NotEmpty(t, res.WorkoutID)
	assert.Empty(t, res.BodyStateIDs)
	assert.NotContains(t, res.Ack, "body-state")
}

// TestWorkoutLog_EmptyRequestCapturesPartial: an entirely empty request still
// records a partial workout event (capture never blocks — AC-11 missing data).
func TestWorkoutLog_EmptyRequestCapturesPartial(t *testing.T) {
	r := bootedWorkout(t, observations.KindWorkout)
	res, err := r.WorkoutLog(WorkoutLogRequest{Now: nowEDT()})
	require.NoError(t, err)
	require.NotEmpty(t, res.WorkoutID)
	w := eventByID(t, r, res.LogicalDate, res.WorkoutID)
	require.NoError(t, w.Validate())
	assert.Equal(t, observations.ParseMarkerPartial, w.Payload["parse"])
}

// TestWorkoutLog_SkipsValuelessBodyState: a body-state input with a part but no
// soreness or pain is dropped rather than written as an empty reading.
func TestWorkoutLog_SkipsValuelessBodyState(t *testing.T) {
	r := bootedWorkout(t, observations.KindWorkout, observations.KindBodyState)
	res, err := r.WorkoutLog(WorkoutLogRequest{
		Type:       "pull",
		BodyStates: []BodyStateInput{{Part: "lats"}, {Part: "biceps", Soreness: intPtr(2)}},
		Now:        nowEDT(),
	})
	require.NoError(t, err)
	require.Len(t, res.BodyStateIDs, 1, "the valueless reading is dropped")
	bs := eventByID(t, r, res.LogicalDate, res.BodyStateIDs[0])
	assert.Equal(t, "biceps", bs.Payload["body_part"])
}

// TestWorkoutLog_ProvenanceStamped: relay provenance rides in payload.provenance
// exactly as a micro-log capture stamps it.
func TestWorkoutLog_ProvenanceStamped(t *testing.T) {
	r := bootedWorkout(t, observations.KindWorkout)
	res, err := r.WorkoutLog(WorkoutLogRequest{Type: "run", Now: nowEDT(), Harness: "discord", Agent: "assistant"})
	require.NoError(t, err)
	w := eventByID(t, r, res.LogicalDate, res.WorkoutID)
	prov, ok := w.Payload["provenance"].(map[string]any)
	require.True(t, ok, "expected a provenance sub-object")
	assert.Equal(t, "discord", prov["harness"])
	assert.Equal(t, "assistant", prov["agent"])
}

const workoutExtractReply = `{
  "type": "pull",
  "duration_min": 50,
  "rpe": 6,
  "body_parts": ["back", "biceps"],
  "soreness": [{"part": "shoulder", "soreness": 2, "pain": null}],
  "pain_flags": ["lower_back"],
  "notes": "shoulder felt fine"
}`

// TestWorkoutLogFromText_ExtractsAndWrites: the spoken path extracts and writes
// the workout event, a quantified soreness reading, and a pain-flag reading
// recorded at PainFlagLevel (AC-6 spoken).
func TestWorkoutLogFromText_ExtractsAndWrites(t *testing.T) {
	r := bootedWorkout(t, observations.KindWorkout, observations.KindBodyState)
	p := &provider.Fake{Script: []provider.Exchange{{Content: workoutExtractReply}}}
	res, err := r.WorkoutLogFromText(context.Background(), WorkoutLogTextRequest{
		Text: "did pull, back and biceps, shoulder felt fine, lower back a bit sore, ~50 min",
		Now:  nowEDT(),
	}, p)
	require.NoError(t, err)
	assert.False(t, res.Degraded)
	assert.Equal(t, 1, p.Calls())
	require.NotEmpty(t, res.WorkoutID)
	require.Len(t, res.BodyStateIDs, 2)

	w := eventByID(t, r, res.LogicalDate, res.WorkoutID)
	assert.Equal(t, "pull", w.Payload["type"])
	assert.EqualValues(t, 50, w.Payload["duration_min"])
	assert.EqualValues(t, 6, w.Payload["rpe"])

	// The unquantified pain flag records at PainFlagLevel so the recommender can
	// act on it.
	flag := eventByID(t, r, res.LogicalDate, res.BodyStateIDs[1])
	assert.Equal(t, "lower_back", flag.Payload["body_part"])
	assert.EqualValues(t, PainFlagLevel, flag.Payload["pain"])
}

// TestWorkoutLogFromText_DegradePreservesDrop: two malformed replies degrade the
// extraction, but the raw drop is still captured as the workout note — a spoken
// capture is never lost (AC-11 degrade).
func TestWorkoutLogFromText_DegradePreservesDrop(t *testing.T) {
	r := bootedWorkout(t, observations.KindWorkout)
	p := &provider.Fake{Script: []provider.Exchange{{Content: "nope"}, {Content: "still nope"}}}
	drop := "did something, not sure what"
	res, err := r.WorkoutLogFromText(context.Background(), WorkoutLogTextRequest{Text: drop, Now: nowEDT()}, p)
	require.NoError(t, err)
	assert.True(t, res.Degraded)
	assert.Equal(t, 2, p.Calls())
	w := eventByID(t, r, res.LogicalDate, res.WorkoutID)
	assert.Equal(t, drop, w.Payload["note"])
}

// TestWorkoutLogFromText_DisabledKindNoModelCall: a disabled workout kind is
// rejected before any model call is made.
func TestWorkoutLogFromText_DisabledKindNoModelCall(t *testing.T) {
	r := bootedWorkout(t) // no kinds enabled
	p := &provider.Fake{Script: []provider.Exchange{{Content: workoutExtractReply}}}
	res, err := r.WorkoutLogFromText(context.Background(), WorkoutLogTextRequest{Text: "did pull", Now: nowEDT()}, p)
	require.NoError(t, err)
	assert.True(t, res.Rejected)
	assert.Equal(t, 0, p.Calls(), "a disabled kind must not spend a model call")
}
