package structuring_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/agents/structuring"
	"github.com/mrz1836/lucid/internal/provider"
)

// input builds the standard synthetic Structuring input.
func input(body string) structuring.Input {
	return structuring.Input{RawID: "raw_2026_05_05_19_42", Body: body, AgentVersion: "structuring-2026.05.0"}
}

// reply is a scripted extraction completion carrying the given JSON.
func reply(jsonBody string) provider.Exchange { return provider.Exchange{Content: jsonBody} }

const goodExtraction = `{
  "emotions": [{"name": "annoyed", "rationale": "user said 'annoyed'"}],
  "themes":   [{"name": "voice-not-heard", "rationale": "pushed back then dropped it"}],
  "people":   [{"display_name": "M."}],
  "notes": null
}`

// TestExtract_HappyPath is the core extraction: valid JSON yields parsed
// emotions/themes/people with person_key left to the People routine, notes
// null, exactly one model call, and no degrade.
func TestExtract_HappyPath(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{reply(goodExtraction)}}
	res := structuring.Extract(context.Background(), input("Rough dinner with M. I pushed back then dropped it. Annoyed."), p)

	assert.False(t, res.Degraded)
	assert.Equal(t, "structuring-2026.05.0", res.AgentVersion)
	require.Len(t, res.Emotions, 1)
	assert.Equal(t, "annoyed", res.Emotions[0].Name)
	require.Len(t, res.Themes, 1)
	require.Len(t, res.People, 1)
	assert.Equal(t, "M.", res.People[0].DisplayName)
	assert.Empty(t, res.Notes)
	assert.Equal(t, 1, p.Calls())

	// The model saw only the entry body, under the extract intent.
	require.Len(t, p.Requests, 1)
	assert.Equal(t, "structuring.extract", p.Requests[0].Intent)
	require.Len(t, p.Requests[0].Messages, 1)
	assert.Equal(t, provider.RoleUser, p.Requests[0].Messages[0].Role)
	assert.Contains(t, p.Requests[0].Messages[0].Content, "Rough dinner")
}

// TestExtract_EmptyBody is error-states.md §S-3: an empty body degrades to
// notes "raw body empty" with no model call.
func TestExtract_EmptyBody(t *testing.T) {
	for _, body := range []string{"", "   \n\t "} {
		p := &provider.Fake{}
		res := structuring.Extract(context.Background(), input(body), p)
		assert.True(t, res.Degraded)
		assert.Equal(t, structuring.NotesRawBodyEmpty, res.Notes)
		assert.Empty(t, res.Emotions)
		assert.Empty(t, res.Themes)
		assert.Empty(t, res.People)
		assert.Equal(t, 0, p.Calls(), "empty body must make no model call")
	}
}

// TestExtract_MalformedTwiceDowngrades is §S-2: malformed JSON on both
// attempts degrades to notes "structuring failed (parse)" after two calls.
func TestExtract_MalformedTwiceDowngrades(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{reply("not json"), reply("still not json")}}
	res := structuring.Extract(context.Background(), input("some real content"), p)
	assert.True(t, res.Degraded)
	assert.Equal(t, structuring.NotesStructuringFailed, res.Notes)
	assert.Empty(t, res.People)
	assert.Equal(t, 2, p.Calls())
}

// TestExtract_MalformedThenValid retries once and accepts the valid second
// reply — no degrade.
func TestExtract_MalformedThenValid(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{reply("garbage"), reply(goodExtraction)}}
	res := structuring.Extract(context.Background(), input("real content"), p)
	assert.False(t, res.Degraded)
	require.Len(t, res.Emotions, 1)
	assert.Equal(t, 2, p.Calls())
}

// TestExtract_DiagnosticNotesRejectedThenClean is acceptance case 4.6: a
// first reply with diagnostic notes is rejected, and a clean retry is
// accepted.
func TestExtract_DiagnosticNotesRejectedThenClean(t *testing.T) {
	diagnostic := `{"emotions":[],"themes":[],"people":[{"display_name":"M."}],"notes":"shows anxious attachment"}`
	clean := `{"emotions":[],"themes":[],"people":[{"display_name":"M."}],"notes":"mentions M. once"}`
	p := &provider.Fake{Script: []provider.Exchange{reply(diagnostic), reply(clean)}}

	res := structuring.Extract(context.Background(), input("dinner with M."), p)
	assert.False(t, res.Degraded)
	assert.Equal(t, "mentions M. once", res.Notes)
	assert.Equal(t, 2, p.Calls())
}

// TestExtract_DiagnosticNotesTwiceDowngrades is acceptance case 4.6's
// persistent branch: diagnostic notes on both attempts downgrade to the
// parse-failed path (4.5).
func TestExtract_DiagnosticNotesTwiceDowngrades(t *testing.T) {
	diagnostic := `{"emotions":[],"themes":[],"people":[],"notes":"clearly a trauma response"}`
	p := &provider.Fake{Script: []provider.Exchange{reply(diagnostic), reply(diagnostic)}}

	res := structuring.Extract(context.Background(), input("dinner"), p)
	assert.True(t, res.Degraded)
	assert.Equal(t, structuring.NotesStructuringFailed, res.Notes)
}

// TestExtract_InvalidPayloadsRejected covers the validation rules that make a
// well-formed-JSON-but-bad payload a failed attempt: a missing rationale, a
// person with no display name, and empty-arrays-without-notes.
func TestExtract_InvalidPayloadsRejected(t *testing.T) {
	cases := map[string]string{
		"missing rationale":  `{"emotions":[{"name":"annoyed","rationale":""}],"themes":[],"people":[],"notes":"x"}`,
		"person no name":     `{"emotions":[],"themes":[],"people":[{"display_name":""}],"notes":"x"}`,
		"empty without note": `{"emotions":[],"themes":[],"people":[],"notes":null}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			p := &provider.Fake{Script: []provider.Exchange{reply(payload), reply(payload)}}
			res := structuring.Extract(context.Background(), input("real content"), p)
			assert.True(t, res.Degraded, "%s should be rejected and degrade", name)
			assert.Equal(t, structuring.NotesStructuringFailed, res.Notes)
		})
	}
}

// TestExtract_TransportErrorDowngrades treats a provider transport failure as
// a failed attempt: two failures degrade, one failure then success is
// accepted.
func TestExtract_TransportErrorDowngrades(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{{Err: provider.ErrTimeout}, {Err: provider.ErrUnavailable}}}
	res := structuring.Extract(context.Background(), input("content"), p)
	assert.True(t, res.Degraded)
	assert.Equal(t, structuring.NotesStructuringFailed, res.Notes)

	p2 := &provider.Fake{Script: []provider.Exchange{{Err: provider.ErrTimeout}, reply(goodExtraction)}}
	res2 := structuring.Extract(context.Background(), input("content"), p2)
	assert.False(t, res2.Degraded)
	require.Len(t, res2.Emotions, 1)
}

// TestExtract_NotesWithStructureAccepted proves a valid payload with a set
// notes value alongside structure round-trips the notes text.
func TestExtract_NotesWithStructureAccepted(t *testing.T) {
	payload := `{"emotions":[{"name":"calm","rationale":"'felt fine'"}],"themes":[],"people":[],"notes":"short entry"}`
	p := &provider.Fake{Script: []provider.Exchange{reply(payload)}}
	res := structuring.Extract(context.Background(), input("Quiet day. Felt fine."), p)
	assert.False(t, res.Degraded)
	assert.Equal(t, "short entry", res.Notes)
	require.Len(t, res.Emotions, 1)
}

// TestExtract_ContextCanceledDowngrades confirms a canceled context surfaces
// as a failed attempt (the provider honors ctx first) and degrades rather
// than panicking.
func TestExtract_ContextCanceledDowngrades(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &provider.Fake{Script: []provider.Exchange{reply(goodExtraction), reply(goodExtraction)}}
	res := structuring.Extract(ctx, input("content"), p)
	assert.True(t, res.Degraded)
}

// TestHasDiagnosticLanguage checks the diagnostic / labeling blocklist subset
// against both hits and legitimate extractive text.
func TestHasDiagnosticLanguage(t *testing.T) {
	hits := []string{
		"shows anxious attachment",
		"avoidant tendencies here",
		"clearly a trauma response",
		"you always fold",
		"he is a narcissist",
		"reads as borderline",
		"I diagnose burnout",
		"you're suffering from something",
		"you're an anxious attacher", // labeling noun-phrase form
	}
	for _, s := range hits {
		assert.Truef(t, structuring.HasDiagnosticLanguage(s), "expected a diagnostic hit for %q", s)
	}
	clean := []string{
		"mentions M. once",
		"annoyed at himself for folding",
		"short entry; not much signal",
		"attached the file", // "attach" alone is not a labeling hit
		"",
	}
	for _, s := range clean {
		assert.Falsef(t, structuring.HasDiagnosticLanguage(s), "did not expect a diagnostic hit for %q", s)
	}
}

// TestExtract_ErrorsAreNotReturned documents that Extract never returns an
// error value — every failure becomes an honest artifact (the loop is never
// blocked on Structuring).
func TestExtract_ErrorsAreNotReturned(t *testing.T) {
	// A structural compile-time guarantee: Extract's signature has no error.
	// This test simply exercises the worst input and asserts a usable result.
	p := &provider.Fake{Script: []provider.Exchange{{Err: errors.New("boom")}, {Err: errors.New("boom")}}}
	res := structuring.Extract(context.Background(), input("x"), p)
	assert.True(t, res.Degraded)
	assert.NotEmpty(t, res.Notes)
}
