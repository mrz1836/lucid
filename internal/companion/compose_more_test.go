package companion

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/provider"
)

// TestCompose_MissingSystemPrompt_Errors: a missing system-prompt file is a
// loud error before any model call, exactly like the per-mode template — a
// misconfigured voice is never a silent empty send.
func TestCompose_MissingSystemPrompt_Errors(t *testing.T) {
	comp := writePrompts(t)
	comp.SystemPrompt = filepath.Join(t.TempDir(), "no-system-prompt.md")
	p := &provider.Fake{Script: []provider.Exchange{{Content: "x"}}}
	c, _ := newComposer(t, comp, defaultProvider(), sampleNumbers(), fakeVerdict{}, fakeChain{}, p)

	_, err := c.Compose(context.Background(), ModeMorning, time.Now())
	require.Error(t, err)
	assert.Equal(t, 0, p.Calls(), "no compose is attempted when the system prompt is missing")
}

// TestCompose_ProviderBuildError_IsLoud: a provider backend that cannot be
// constructed is a loud error, not a fallback — the deterministic fallback is
// reserved for a reachable-but-unavailable model, not a misconfigured one.
func TestCompose_ProviderBuildError_IsLoud(t *testing.T) {
	c := New(Deps{
		Companion: writePrompts(t),
		Provider:  defaultProvider(),
		Numbers:   sampleNumbers(),
		Verdict:   fakeVerdict{},
		Chain:     fakeChain{},
		Build:     func(config.ProviderConfig) (provider.Provider, error) { return nil, errors.New("bad backend") },
	})

	res, err := c.Compose(context.Background(), ModeMorning, time.Now())
	require.Error(t, err)
	assert.False(t, res.Fallback, "a build failure is not a deterministic fallback")
	assert.Empty(t, res.Text)
}

// TestCompose_StatusReadError_IsLoud: the status half of the numbers projection
// is the message too, so a status read failure surfaces loudly before any model
// call — the metrics-read companion of TestCompose_MetricsReadError_IsLoud.
func TestCompose_StatusReadError_IsLoud(t *testing.T) {
	comp := writePrompts(t)
	nums := sampleNumbers()
	nums.statErr = errors.New("status boom")
	p := &provider.Fake{Script: []provider.Exchange{{Content: "x"}}}
	c, _ := newComposer(t, comp, defaultProvider(), nums, fakeVerdict{}, fakeChain{}, p)

	_, err := c.Compose(context.Background(), ModeMorning, time.Now())
	require.Error(t, err)
	assert.Equal(t, 0, p.Calls(), "no model call when the status projection cannot be read")
}

// TestObservationLine_PartialAndEdgeCases fills in the per-kind bullet branches
// the happy-path table does not reach: the note-only and bare fallbacks for
// pain, sleep, and symptom, the name-carrying symptom paths, an int64 scale
// value (a native in-process capture), and an unrelated kind that falls to the
// free-text default.
func TestObservationLine_PartialAndEdgeCases(t *testing.T) {
	cases := []struct {
		name string
		ev   observations.Event
		want string
	}{
		{"pain bare", observations.Event{Kind: observations.KindPain, Payload: map[string]any{}}, "pain"},
		{"pain intensity only", observations.Event{Kind: observations.KindPain, Payload: map[string]any{"intensity": 4}}, "pain 4"},
		{"pain site without side", observations.Event{Kind: observations.KindPain, Payload: map[string]any{"intensity": 2, "site": "knee"}}, "pain 2 — knee"},
		{"sleep note only", observations.Event{Kind: observations.KindSleep, Payload: map[string]any{"note": "restless"}}, "sleep — restless"},
		{"sleep quality and note", observations.Event{Kind: observations.KindSleep, Payload: map[string]any{"quality": 7, "note": "woke twice"}}, "sleep · quality 7 — woke twice"},
		{"sleep bare", observations.Event{Kind: observations.KindSleep, Payload: map[string]any{}}, "sleep"},
		{"symptom no name with note", observations.Event{Kind: observations.KindSymptom, Payload: map[string]any{"note": "off"}}, "symptom — off"},
		{"symptom bare", observations.Event{Kind: observations.KindSymptom, Payload: map[string]any{}}, "symptom"},
		{"symptom name and note", observations.Event{Kind: observations.KindSymptom, Payload: map[string]any{"name": "nausea", "note": "after lunch"}}, "nausea — after lunch"},
		{"mood int64 round-trip", observations.Event{Kind: observations.KindMood, Payload: map[string]any{"level": int64(5)}}, "mood 5"},
		{"unrelated kind falls to free text", observations.Event{Kind: observations.KindMed, Payload: map[string]any{"note": "took it"}}, "took it"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, observationLine(tc.ev))
		})
	}
}

// TestPayloadInt_Coercions covers every arm of the payload int reader,
// including the int64 a native capture carries and the fallback for a
// non-numeric field.
func TestPayloadInt_Coercions(t *testing.T) {
	cases := []struct {
		name   string
		in     any
		want   int
		wantOK bool
	}{
		{"int", 3, 3, true},
		{"int64", int64(4), 4, true},
		{"float64", float64(5), 5, true},
		{"string is ignored", "6", 0, false},
		{"missing key", nil, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := map[string]any{}
			if tc.in != nil {
				p["k"] = tc.in
			}
			got, ok := payloadInt(p, "k")
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestPayloadString_TrimsAndRejects covers the non-empty read, the
// whitespace-only value that reads as absent, and the wrong-typed value.
func TestPayloadString_TrimsAndRejects(t *testing.T) {
	t.Run("non-empty value is trimmed", func(t *testing.T) {
		got, ok := payloadString(map[string]any{"k": "  hello  "}, "k")
		assert.True(t, ok)
		assert.Equal(t, "hello", got)
	})
	t.Run("whitespace-only reads as absent", func(t *testing.T) {
		got, ok := payloadString(map[string]any{"k": "   "}, "k")
		assert.False(t, ok)
		assert.Empty(t, got)
	})
	t.Run("a non-string value is rejected", func(t *testing.T) {
		got, ok := payloadString(map[string]any{"k": 7}, "k")
		assert.False(t, ok)
		assert.Empty(t, got)
	})
}

// TestSectionsDigest_SkipsEmptySection: a section carrying no lines contributes
// nothing to the model's compact digest — a header with no signal is never
// handed across.
func TestSectionsDigest_SkipsEmptySection(t *testing.T) {
	sections := []Section{
		{Emoji: "🫀", Label: "Body & state", Meta: "as logged 2026-07-19", Lines: []string{"mood 7 — steady"}},
		{Emoji: "📌", Label: "Commitments", Meta: "as logged 2026-07-15", Lines: nil},
	}
	digest := sectionsDigest(sections)
	assert.Contains(t, digest, "Body & state (as logged 2026-07-19): mood 7 — steady")
	assert.NotContains(t, digest, "Commitments", "a section with no lines is skipped")
}
