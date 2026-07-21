package workout_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/agents/workout"
	"github.com/mrz1836/lucid/internal/provider"
)

// input builds the standard synthetic extraction input.
func input(text string) workout.Input {
	return workout.Input{Text: text, AgentVersion: "workout-test.0"}
}

// reply is a scripted extraction completion carrying the given JSON.
func reply(jsonBody string) provider.Exchange { return provider.Exchange{Content: jsonBody} }

const goodExtraction = `{
  "type": "pull",
  "duration_min": 50,
  "rpe": 7,
  "body_parts": ["back", "biceps"],
  "soreness": [{"part": "shoulder", "soreness": 3, "pain": null}],
  "pain_flags": [],
  "notes": "felt strong"
}`

// TestExtract_HappyPath is the core extraction: valid JSON yields the parsed
// fields, exactly one model call under the workout.extract intent, and no
// degrade.
func TestExtract_HappyPath(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{reply(goodExtraction)}}
	res := workout.Extract(context.Background(), input("did pull, back and biceps, shoulder a little sore, about 50 min, felt strong"), p)

	assert.False(t, res.Degraded)
	assert.Equal(t, "workout-test.0", res.AgentVersion)
	assert.Equal(t, "pull", res.Type)
	assert.Equal(t, 50, res.DurationMin)
	require.NotNil(t, res.RPE)
	assert.Equal(t, 7, *res.RPE)
	assert.Equal(t, []string{"back", "biceps"}, res.BodyParts)
	require.Len(t, res.Soreness, 1)
	assert.Equal(t, "shoulder", res.Soreness[0].Part)
	require.NotNil(t, res.Soreness[0].Soreness)
	assert.Equal(t, 3, *res.Soreness[0].Soreness)
	assert.Nil(t, res.Soreness[0].Pain)
	assert.Equal(t, "felt strong", res.Notes)
	assert.Equal(t, 1, p.Calls())

	// The model saw only the drop, under the workout.extract intent.
	require.Len(t, p.Requests, 1)
	assert.Equal(t, "workout.extract", p.Requests[0].Intent)
	require.Len(t, p.Requests[0].Messages, 1)
	assert.Equal(t, provider.RoleUser, p.Requests[0].Messages[0].Role)
	assert.Contains(t, p.Requests[0].Messages[0].Content, "did pull")
}

// TestExtract_EmptyText degrades with no model call for an empty or
// whitespace-only drop.
func TestExtract_EmptyText(t *testing.T) {
	for _, text := range []string{"", "   \n\t "} {
		p := &provider.Fake{}
		res := workout.Extract(context.Background(), input(text), p)
		assert.True(t, res.Degraded)
		assert.Equal(t, "workout-test.0", res.AgentVersion)
		assert.Empty(t, res.Type)
		assert.Empty(t, res.BodyParts)
		assert.Equal(t, 0, p.Calls(), "empty text must make no model call")
	}
}

// TestExtract_DefaultsAgentVersion falls back to the package default when the
// input leaves the version blank.
func TestExtract_DefaultsAgentVersion(t *testing.T) {
	p := &provider.Fake{}
	res := workout.Extract(context.Background(), workout.Input{Text: ""}, p)
	assert.True(t, res.Degraded)
	assert.Equal(t, workout.DefaultAgentVersion, res.AgentVersion)
}

// TestExtract_MalformedTwiceDegrades retries once and then degrades after two
// unusable replies.
func TestExtract_MalformedTwiceDegrades(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{reply("not json"), reply("still not json")}}
	res := workout.Extract(context.Background(), input("did a workout"), p)
	assert.True(t, res.Degraded)
	assert.Empty(t, res.Type)
	assert.Equal(t, 2, p.Calls())
}

// TestExtract_MalformedThenValid accepts a valid second reply after a malformed
// first — no degrade.
func TestExtract_MalformedThenValid(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{reply("garbage"), reply(goodExtraction)}}
	res := workout.Extract(context.Background(), input("real drop"), p)
	assert.False(t, res.Degraded)
	assert.Equal(t, "pull", res.Type)
	assert.Equal(t, 2, p.Calls())
}

// TestExtract_TransportErrorDegrades treats a provider transport failure as a
// failed attempt: two failures degrade, one failure then success is accepted.
func TestExtract_TransportErrorDegrades(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{{Err: provider.ErrTimeout}, {Err: provider.ErrUnavailable}}}
	res := workout.Extract(context.Background(), input("drop"), p)
	assert.True(t, res.Degraded)

	p2 := &provider.Fake{Script: []provider.Exchange{{Err: provider.ErrTimeout}, reply(goodExtraction)}}
	res2 := workout.Extract(context.Background(), input("drop"), p2)
	assert.False(t, res2.Degraded)
	assert.Equal(t, "pull", res2.Type)
}

// TestExtract_OutOfRangeScalesRejected covers the never-clamp rule: an
// out-of-range rpe, soreness, or pain makes a well-formed-JSON reply a failed
// attempt.
func TestExtract_OutOfRangeScalesRejected(t *testing.T) {
	cases := map[string]string{
		"rpe over":      `{"type":"push","rpe":11,"body_parts":[],"soreness":[],"pain_flags":[],"notes":null}`,
		"rpe negative":  `{"type":"push","rpe":-1,"body_parts":[],"soreness":[],"pain_flags":[],"notes":null}`,
		"soreness over": `{"type":"push","body_parts":[],"soreness":[{"part":"knee","soreness":12,"pain":null}],"pain_flags":[],"notes":null}`,
		"pain over":     `{"type":"push","body_parts":[],"soreness":[{"part":"knee","soreness":null,"pain":99}],"pain_flags":[],"notes":null}`,
		"duration neg":  `{"type":"push","duration_min":-5,"body_parts":[],"soreness":[],"pain_flags":[],"notes":null}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			p := &provider.Fake{Script: []provider.Exchange{reply(payload), reply(payload)}}
			res := workout.Extract(context.Background(), input("drop"), p)
			assert.Truef(t, res.Degraded, "%s should be rejected and degrade", name)
			assert.Equal(t, 2, p.Calls())
		})
	}
}

// TestExtract_BodyStateWithoutValueRejected: a per-part reading that names a
// part but states neither soreness nor pain is unusable and rejected.
func TestExtract_BodyStateWithoutValueRejected(t *testing.T) {
	payload := `{"type":"","body_parts":[],"soreness":[{"part":"shoulder","soreness":null,"pain":null}],"pain_flags":[],"notes":null}`
	p := &provider.Fake{Script: []provider.Exchange{reply(payload), reply(payload)}}
	res := workout.Extract(context.Background(), input("shoulder"), p)
	assert.True(t, res.Degraded)
}

// TestExtract_AllEmptyRejected: a syntactically-valid but empty reply carries no
// content, so it is a failed attempt rather than a valid empty capture.
func TestExtract_AllEmptyRejected(t *testing.T) {
	empty := `{"type":"","duration_min":0,"rpe":null,"body_parts":[],"soreness":[],"pain_flags":[],"notes":null}`
	p := &provider.Fake{Script: []provider.Exchange{reply(empty), reply(empty)}}
	res := workout.Extract(context.Background(), input("something"), p)
	assert.True(t, res.Degraded)
}

// TestExtract_PainFlagsAndNotesOnly accepts a reply that carries only pain flags
// and notes (no type or numbers) — a real "my knee is killing me" drop.
func TestExtract_PainFlagsAndNotesOnly(t *testing.T) {
	payload := `{"type":"","duration_min":0,"rpe":null,"body_parts":[],"soreness":[],"pain_flags":["knee"],"notes":"knee felt bad on stairs"}`
	p := &provider.Fake{Script: []provider.Exchange{reply(payload)}}
	res := workout.Extract(context.Background(), input("my knee is killing me on the stairs"), p)
	assert.False(t, res.Degraded)
	assert.Equal(t, []string{"knee"}, res.PainFlags)
	assert.Equal(t, "knee felt bad on stairs", res.Notes)
	assert.Nil(t, res.RPE)
}

// TestExtract_TrimsAndDropsBlanks trims string fields and drops blank list
// entries so downstream never sees whitespace tokens.
func TestExtract_TrimsAndDropsBlanks(t *testing.T) {
	payload := `{"type":"  legs  ","body_parts":["quads","  ",""],"soreness":[],"pain_flags":["  ","glutes"],"notes":"  ok  "}`
	p := &provider.Fake{Script: []provider.Exchange{reply(payload)}}
	res := workout.Extract(context.Background(), input("legs day quads"), p)
	assert.False(t, res.Degraded)
	assert.Equal(t, "legs", res.Type)
	assert.Equal(t, []string{"quads"}, res.BodyParts)
	assert.Equal(t, []string{"glutes"}, res.PainFlags)
	assert.Equal(t, "ok", res.Notes)
}

// TestExtract_ContextCanceledDegrades confirms a canceled context surfaces as a
// failed attempt and degrades rather than panicking.
func TestExtract_ContextCanceledDegrades(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &provider.Fake{Script: []provider.Exchange{reply(goodExtraction), reply(goodExtraction)}}
	res := workout.Extract(ctx, input("drop"), p)
	assert.True(t, res.Degraded)
}

// TestExtract_ErrorsAreNotReturned documents that Extract never returns an error
// — every failure becomes an honest degraded result.
func TestExtract_ErrorsAreNotReturned(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{{Err: errors.New("boom")}, {Err: errors.New("boom")}}}
	res := workout.Extract(context.Background(), input("x"), p)
	assert.True(t, res.Degraded)
}
