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

// --- The daily anchor -------------------------------------------------------

// anchorItems reads the per-item anchor inventory back off a stored event, where
// it has round-tripped through JSON as a list of objects.
func anchorItems(t *testing.T, ev observations.Event) []map[string]any {
	t.Helper()
	raw, ok := ev.Payload["anchor_items"].([]any)
	require.Truef(t, ok, "expected an anchor_items list, got %T", ev.Payload["anchor_items"])
	out := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		item, itemOK := entry.(map[string]any)
		require.Truef(t, itemOK, "expected an anchor item object, got %T", entry)
		out = append(out, item)
	}
	return out
}

// TestWorkoutLog_AnchorOnlyWritesMarker: a bare "did the anchor" writes one
// workout event carrying the marker — content, not the empty/partial path — and
// no body parts, so it opens no recovery window (AC-6, AC-8, AC-9).
func TestWorkoutLog_AnchorOnlyWritesMarker(t *testing.T) {
	r := bootedWorkout(t, observations.KindWorkout)
	res, err := r.WorkoutLog(WorkoutLogRequest{Anchor: true, Now: nowEDT()})
	require.NoError(t, err)
	require.NotEmpty(t, res.WorkoutID)

	w := eventByID(t, r, res.LogicalDate, res.WorkoutID)
	require.NoError(t, w.Validate())
	assert.Equal(t, observations.KindWorkout, w.Kind)
	assert.Equal(t, true, w.Payload["anchor"])
	assert.NotContains(t, w.Payload, "parse", "an anchor is content, never a partial capture")
	assert.NotContains(t, w.Payload, "body_parts", "the daily floor is not a session")
	assert.NotContains(t, w.Payload, "anchor_items")
	assert.Contains(t, res.Ack, "Logged workout as `")
}

// TestWorkoutLog_AnchorItemsRecordedAsInventory: stated counts ride the event as
// per-item inventory, and an item the user did not count is recorded without one
// rather than as a fabricated zero.
func TestWorkoutLog_AnchorItemsRecordedAsInventory(t *testing.T) {
	r := bootedWorkout(t, observations.KindWorkout)
	res, err := r.WorkoutLog(WorkoutLogRequest{
		Anchor: true,
		AnchorItems: []AnchorCount{
			{Name: "squats", Count: 55, HasCount: true},
			{Name: "core", Count: 50, HasCount: true},
			{Name: "easy push-ups"},
		},
		Now: nowEDT(),
	})
	require.NoError(t, err)

	w := eventByID(t, r, res.LogicalDate, res.WorkoutID)
	require.NoError(t, w.Validate())
	items := anchorItems(t, w)
	require.Len(t, items, 3)
	assert.Equal(t, "squats", items[0]["name"])
	assert.EqualValues(t, 55, items[0]["count"])
	assert.Equal(t, "core", items[1]["name"])
	assert.EqualValues(t, 50, items[1]["count"])
	assert.Equal(t, "easy push-ups", items[2]["name"])
	assert.NotContains(t, items[2], "count", "an uncounted item is recorded as uncounted")
}

// TestWorkoutLog_AnchorItemsImplyTheMarker: naming counts is itself a report that
// the floor was done, so the marker lands even when the flag was not set — and a
// blank item name is dropped rather than written as a nameless entry.
func TestWorkoutLog_AnchorItemsImplyTheMarker(t *testing.T) {
	r := bootedWorkout(t, observations.KindWorkout)
	res, err := r.WorkoutLog(WorkoutLogRequest{
		AnchorItems: []AnchorCount{{Name: "squats", Count: 55, HasCount: true}, {Name: "   "}},
		Now:         nowEDT(),
	})
	require.NoError(t, err)

	w := eventByID(t, r, res.LogicalDate, res.WorkoutID)
	assert.Equal(t, true, w.Payload["anchor"])
	assert.NotContains(t, w.Payload, "parse")
	require.Len(t, anchorItems(t, w), 1)
}

// TestWorkoutLog_AnchorAlongsideSessionKeepsBothRecords: logging the anchor and a
// real session in one capture keeps the session's own fields — the session is a
// real load and still opens its recovery window; only the anchor adds no parts of
// its own.
func TestWorkoutLog_AnchorAlongsideSessionKeepsBothRecords(t *testing.T) {
	r := bootedWorkout(t, observations.KindWorkout)
	res, err := r.WorkoutLog(WorkoutLogRequest{
		Type: "legs", BodyParts: []string{"legs"}, Anchor: true, Now: nowEDT(),
	})
	require.NoError(t, err)

	w := eventByID(t, r, res.LogicalDate, res.WorkoutID)
	assert.Equal(t, true, w.Payload["anchor"])
	assert.Equal(t, "legs", w.Payload["type"])
	assert.Contains(t, w.Payload, "body_parts")
}

// TestWorkoutRequestEmpty_AnchorIsContent covers the empty-check directly: an
// anchor (or a named item) makes a request non-empty, which is what keeps a
// spoken anchor-only drop from falling back to the raw note.
func TestWorkoutRequestEmpty_AnchorIsContent(t *testing.T) {
	assert.True(t, workoutRequestEmpty(WorkoutLogRequest{}))
	assert.False(t, workoutRequestEmpty(WorkoutLogRequest{Anchor: true}))
	assert.False(t, workoutRequestEmpty(WorkoutLogRequest{AnchorItems: []AnchorCount{{Name: "squats"}}}))
	assert.True(t, workoutRequestEmpty(WorkoutLogRequest{AnchorItems: []AnchorCount{{Name: "  "}}}),
		"a blank-named item carries nothing")
}

const workoutAnchorReply = `{
  "type": "",
  "duration_min": 0,
  "rpe": null,
  "body_parts": [],
  "soreness": [],
  "pain_flags": [],
  "anchor": true,
  "anchor_items": [{"name": "squats", "count": 55}, {"name": "core", "count": 50}],
  "notes": null
}`

// TestWorkoutLogFromText_AnchorExtractsAndWrites: a spoken anchor drop writes the
// marker and its counts through the same deterministic capture, and is not lost
// to the raw-note fallback (AC-7).
func TestWorkoutLogFromText_AnchorExtractsAndWrites(t *testing.T) {
	r := bootedWorkout(t, observations.KindWorkout, observations.KindBodyState)
	p := &provider.Fake{Script: []provider.Exchange{{Content: workoutAnchorReply}}}
	res, err := r.WorkoutLogFromText(context.Background(), WorkoutLogTextRequest{
		Text: "did my daily anchor, 55 squats and 50 core",
		Now:  nowEDT(),
	}, p)
	require.NoError(t, err)
	assert.False(t, res.Degraded)
	assert.Equal(t, 1, p.Calls())
	assert.Empty(t, res.BodyStateIDs)

	w := eventByID(t, r, res.LogicalDate, res.WorkoutID)
	require.NoError(t, w.Validate())
	assert.Equal(t, true, w.Payload["anchor"])
	assert.NotContains(t, w.Payload, "note", "the drop was extracted, not preserved raw")
	items := anchorItems(t, w)
	require.Len(t, items, 2)
	assert.Equal(t, "squats", items[0]["name"])
	assert.EqualValues(t, 55, items[0]["count"])
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
