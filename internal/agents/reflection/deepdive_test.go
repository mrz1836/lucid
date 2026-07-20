package reflection

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/provider"
)

// richDeepInput is a full week: honest numbers, two citable raw entries, a body
// signal, and one prior insight — enough material for the model to author every
// section and cite a candidate.
func richDeepInput() DeepDiveInput {
	return DeepDiveInput{
		ISOWeek: "2026-W27",
		Numbers: []string{"current streak: 3 day(s)", "raw entries this week: 2"},
		Entries: []DeepEntry{
			{ID: "raw_2026_07_01_09_00", Date: "2026-07-01", Text: "went quiet in the standup again"},
			{ID: "raw_2026_07_03_20_10", Date: "2026-07-03", Text: "over-prepared for the review call"},
		},
		Signals:      []DeepSignal{{Kind: "pain", Date: "2026-07-02"}},
		Insights:     []DeepInsight{{ID: "i_1", Statement: "I go quiet in groups."}},
		AgentVersion: "reflection-2026.07.0",
	}
}

// deepReply builds a full, valid deep-dive completion with an optional
// candidate block; a candidate is included when shapeTag is non-empty.
func deepReply(shapeTag string, citeIDs ...string) provider.Exchange {
	body := `{"summary":"A steadier week with two honest entries.",` +
		`"wins":["logged two entries"],"misses":["skipped one closeout"],` +
		`"body_pain":["one pain note mid-week"],"habit_change":["earlier evenings"],` +
		`"next_week":["one small experiment"]`
	if shapeTag != "" {
		cites := ""
		for i, id := range citeIDs {
			if i > 0 {
				cites += ","
			}
			cites += `"` + id + `"`
		}
		body += `,"candidate":{"proposal_text":"One possible pattern: preparation as a way to stay safe.",` +
			`"shape_tag":"` + shapeTag + `","supporting_entry_ids":[` + cites + `]}`
	}
	body += "}"
	return provider.Exchange{Content: body}
}

// TestDeepDive_RichWeekAllSectionsAndCitedCandidate proves a full week yields
// every narrative section plus one candidate carrying an in-slice citation, in
// exactly one model call (AC-3, AC-7).
func TestDeepDive_RichWeekAllSectionsAndCitedCandidate(t *testing.T) {
	fake := &provider.Fake{Script: []provider.Exchange{deepReply("prep-as-safety", "raw_2026_07_03_20_10")}}

	res := DeepDive(context.Background(), richDeepInput(), fake)

	assert.Equal(t, 1, fake.Calls(), "one model call over a non-empty week")
	assert.NotEmpty(t, res.Summary)
	assert.NotEmpty(t, res.Wins)
	assert.NotEmpty(t, res.Misses)
	assert.NotEmpty(t, res.BodyPain)
	assert.NotEmpty(t, res.HabitChange)
	assert.NotEmpty(t, res.NextWeek)
	require.NotNil(t, res.Candidate, "a cited candidate is surfaced")
	assert.Equal(t, "prep-as-safety", res.Candidate.ShapeTag)
	assert.Equal(t, []string{"raw_2026_07_03_20_10"}, res.Candidate.SupportingEntryIDs)
	assert.False(t, res.NoLLM)
	assert.Nil(t, res.AppliedLens, "no lens set ⇒ no applied-lens label")
}

// TestDeepDive_EmptyWeekNoModelCall proves an empty store short-circuits to an
// empty-but-valid result with no model call (AC-11; P9 — no spend over an empty
// store).
func TestDeepDive_EmptyWeekNoModelCall(t *testing.T) {
	fake := &provider.Fake{}

	res := DeepDive(context.Background(), DeepDiveInput{ISOWeek: "2026-W27"}, fake)

	assert.Equal(t, 0, fake.Calls(), "no model call over an empty week")
	assert.True(t, res.NoLLM)
	assert.Empty(t, res.Summary)
	assert.Nil(t, res.Candidate)
	assert.Nil(t, res.AppliedLens)
}

// TestDeepDive_AppliedLensLabel proves the "<id> v<version>" label is stamped on
// the result when a lens frames the run (AC-6 label sourced here).
func TestDeepDive_AppliedLensLabel(t *testing.T) {
	fake := &provider.Fake{Script: []provider.Exchange{deepReply("prep-as-safety", "raw_2026_07_03_20_10")}}
	in := richDeepInput()
	in.ActiveLens = &DeepLens{Name: "Stoicism", Label: "stoicism v1"}

	res := DeepDive(context.Background(), in, fake)

	require.NotNil(t, res.AppliedLens)
	assert.Equal(t, "stoicism v1", *res.AppliedLens)
}

// TestDeepDive_DropsOutOfSliceCitation proves a candidate citing an id not in
// the slice is dropped while the narrative is kept — anything surfaced is always
// cited from the week's own entries (AC-7).
func TestDeepDive_DropsOutOfSliceCitation(t *testing.T) {
	fake := &provider.Fake{Script: []provider.Exchange{
		deepReply("prep-as-safety", "raw_not_in_slice"),
		deepReply("prep-as-safety", "raw_not_in_slice"), // strict retry — still out of slice
	}}

	res := DeepDive(context.Background(), richDeepInput(), fake)

	assert.NotEmpty(t, res.Summary, "the narrative survives a dropped candidate")
	assert.Nil(t, res.Candidate, "an out-of-slice citation is never surfaced")
}

// TestDeepDive_DropsDenylistedShape proves a candidate whose shape_tag is on the
// rejected/unanswered denylist is dropped (one-at-a-time hygiene — never
// re-surface a shape the user already handled).
func TestDeepDive_DropsDenylistedShape(t *testing.T) {
	fake := &provider.Fake{Script: []provider.Exchange{deepReply("prep-as-safety", "raw_2026_07_01_09_00")}}
	in := richDeepInput()
	in.RejectedShapeTags = []string{"prep-as-safety"}

	res := DeepDive(context.Background(), in, fake)

	assert.NotEmpty(t, res.Summary)
	assert.Nil(t, res.Candidate, "a denylisted shape is not re-surfaced")
}

// TestDeepDive_NarrativeWithoutCandidate proves a valid week with no candidate
// block returns the sections and a nil candidate (a "no pattern" week).
func TestDeepDive_NarrativeWithoutCandidate(t *testing.T) {
	fake := &provider.Fake{Script: []provider.Exchange{deepReply("")}}

	res := DeepDive(context.Background(), richDeepInput(), fake)

	assert.NotEmpty(t, res.Summary)
	assert.Nil(t, res.Candidate)
}

// TestDeepDive_DegradesAfterMalformed proves two malformed replies degrade to
// the calm fallback summary rather than an error or a partial parse.
func TestDeepDive_DegradesAfterMalformed(t *testing.T) {
	fake := &provider.Fake{Script: []provider.Exchange{
		{Content: "not json"},
		{Content: `{"summary":""}`}, // parseable but empty summary — unusable
	}}

	res := DeepDive(context.Background(), richDeepInput(), fake)

	assert.Equal(t, 2, fake.Calls(), "one call plus one strict retry")
	assert.True(t, res.Fallback)
	assert.Equal(t, deepDiveFallback, res.Summary)
	assert.Nil(t, res.Candidate)
}

// TestDeepDive_DegradesOnTransportError proves a transport error on both
// attempts degrades to the fallback (never returns an error).
func TestDeepDive_DegradesOnTransportError(t *testing.T) {
	fake := &provider.Fake{Script: []provider.Exchange{
		{Err: provider.ErrUnavailable},
		{Err: provider.ErrUnavailable},
	}}

	res := DeepDive(context.Background(), richDeepInput(), fake)

	assert.True(t, res.Fallback)
	assert.Equal(t, deepDiveFallback, res.Summary)
}
