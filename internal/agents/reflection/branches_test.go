package reflection

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/provider"
)

// TestPropose_ReflectOnlyOutcomesRejected proves a propose reply carrying an
// outcome that belongs to /reflect or /ask (recall/answer/insufficient) is
// invalid for a propose turn and degrades to no_pattern.
func TestPropose_ReflectOnlyOutcomesRejected(t *testing.T) {
	for _, oc := range []string{"recall", "answer", "insufficient"} {
		t.Run(oc, func(t *testing.T) {
			reply := `{"outcome":"` + oc + `","message_text":"belongs to another verb"}`
			got := Propose(context.Background(), baseInput(), fake(reply, reply))
			assert.Equal(t, OutcomeNoPattern, got.Outcome, "a non-propose outcome must be rejected")
		})
	}
}

// TestFromReply_UnknownOutcomeIsNoPattern covers the defensive default of the
// reply mapper: an outcome the switch does not name maps to no_pattern rather
// than a partial or panicking result.
func TestFromReply_UnknownOutcomeIsNoPattern(t *testing.T) {
	got := fromReply(proposalReply{Outcome: "totally-unknown"})
	assert.Equal(t, OutcomeNoPattern, got.Outcome)
	assert.Equal(t, noPatternMessage, got.MessageText)
	assert.False(t, got.NoLLM, "a model-path mapping is never the deterministic no-LLM path")
}

// TestWriteView_RendersAllFieldsIncludingNotes proves the id-anchored view
// renderer emits each present field — including the optional notes line the
// model may cite.
func TestWriteView_RendersAllFieldsIncludingNotes(t *testing.T) {
	var b strings.Builder
	writeView(&b, ProcessedView{
		ID:       "raw_2026_05_05_19_42",
		Emotions: []string{"calm"},
		Themes:   []string{"rest"},
		People:   []string{"M."},
		Notes:    "a quiet note",
	})
	out := b.String()
	assert.Contains(t, out, "id: raw_2026_05_05_19_42")
	assert.Contains(t, out, "emotions: calm")
	assert.Contains(t, out, "themes: rest")
	assert.Contains(t, out, "people: M.")
	assert.Contains(t, out, "notes: a quiet note")

	// A blank-notes view omits the notes line entirely.
	var b2 strings.Builder
	writeView(&b2, ProcessedView{ID: "raw_x", Notes: "   "})
	assert.NotContains(t, b2.String(), "notes:")
}

// TestDeepDive_DropsInvalidCandidate covers each candidate-validation reject:
// empty text, a non-grammar shape_tag, too many shape segments, and no
// supporting citation. In every case the narrative survives and the candidate
// is dropped — anything surfaced is always cited (AC-7).
func TestDeepDive_DropsInvalidCandidate(t *testing.T) {
	const narrative = `"summary":"A steadier week.","wins":[],"misses":[],` +
		`"body_pain":[],"habit_change":[],"next_week":[]`
	const citable = "raw_2026_07_01_09_00"

	cases := map[string]string{
		"empty proposal text": `{"proposal_text":"   ","shape_tag":"prep-as-safety","supporting_entry_ids":["` + citable + `"]}`,
		"bad shape grammar":   `{"proposal_text":"One possible pattern.","shape_tag":"Prep_Safety","supporting_entry_ids":["` + citable + `"]}`,
		"too many segments":   `{"proposal_text":"One possible pattern.","shape_tag":"a-b-c-d-e-f-g","supporting_entry_ids":["` + citable + `"]}`,
		"no supporting ids":   `{"proposal_text":"One possible pattern.","shape_tag":"prep-as-safety","supporting_entry_ids":[]}`,
	}
	for name, candidate := range cases {
		t.Run(name, func(t *testing.T) {
			body := "{" + narrative + `,"candidate":` + candidate + "}"
			f := &provider.Fake{Script: []provider.Exchange{{Content: body}}}
			res := DeepDive(context.Background(), richDeepInput(), f)
			assert.NotEmpty(t, res.Summary, "the narrative survives a dropped candidate")
			assert.Nil(t, res.Candidate, "an invalid candidate is never surfaced")
		})
	}
}

// TestSurfaceForRecall_WrongOutcomeDegrades proves a reply whose outcome is not
// "recall" fails validation on both attempts and degrades to surfacing every
// insight verbatim (error-states.md §R-8), never a novel or partial recall.
func TestSurfaceForRecall_WrongOutcomeDegrades(t *testing.T) {
	in := RecallInput{
		Scope:        ScopeWeek,
		Window:       []InsightView{{ID: "i_1", Statement: "I go quiet in groups."}},
		AgentVersion: agentV,
	}
	wrong := `{"outcome":"proposal","ordered_insights":[{"id":"i_1","surface_text":"does this still hold?"}]}`
	f := &provider.Fake{Script: []provider.Exchange{{Content: wrong}, {Content: wrong}}}

	res := SurfaceForRecall(context.Background(), in, f)
	assert.True(t, res.Fallback, "a non-recall outcome degrades to the verbatim fallback")
	assert.Equal(t, OutcomeRecall, res.Outcome, "the recall result outcome is always recall")
	require.Len(t, res.Ordered, 1, "every window insight is surfaced verbatim")
}
