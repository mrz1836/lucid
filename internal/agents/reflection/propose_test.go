package reflection

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/provider"
)

const (
	curID  = "raw_2026_05_05_19_42"
	winID  = "raw_2026_05_03_21_10"
	agentV = "reflection-2026.05.0"
)

// baseInput builds a propose input with a non-empty current artifact and a
// one-entry recent window — the setup a real proposal needs.
func baseInput() ProposeInput {
	return ProposeInput{
		Current:      ProcessedView{ID: curID, Emotions: []string{"annoyed"}, Themes: []string{"voice-not-heard"}, People: []string{"M."}},
		RecentWindow: []ProcessedView{{ID: winID, Themes: []string{"voice-not-heard"}}},
		AgentVersion: agentV,
	}
}

func fake(contents ...string) *provider.Fake {
	xs := make([]provider.Exchange, 0, len(contents))
	for _, c := range contents {
		xs = append(xs, provider.Exchange{Content: c})
	}
	return &provider.Fake{Script: xs}
}

// TestPropose_DeterministicNoPattern covers the three short-circuits that
// return no_pattern WITHOUT a model call (error-states.md §R-3/§R-4/§R-6).
func TestPropose_DeterministicNoPattern(t *testing.T) {
	cases := map[string]ProposeInput{
		"bootstrap":     func() ProposeInput { in := baseInput(); in.Bootstrap = true; return in }(),
		"empty window":  func() ProposeInput { in := baseInput(); in.RecentWindow = nil; return in }(),
		"empty current": {Current: ProcessedView{ID: curID}, RecentWindow: []ProcessedView{{ID: winID}}, AgentVersion: agentV},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			p := &provider.Fake{} // no scripted calls — none expected
			got := Propose(context.Background(), in, p)
			assert.Equal(t, OutcomeNoPattern, got.Outcome)
			assert.True(t, got.NoLLM, "the deterministic path must not call the model")
			assert.Equal(t, 0, p.Calls())
			assert.Equal(t, noPatternMessage, got.MessageText)
		})
	}
}

// TestPropose_Proposal is the happy path: a valid proposal reply is accepted
// with its shape_tag and supporting ids.
func TestPropose_Proposal(t *testing.T) {
	reply := `{"outcome":"proposal","proposal_text":"One possible pattern: you test an idea once, then back off.","shape_tag":"voice-fold-when-m","supporting_entry_ids":["` + curID + `","` + winID + `"]}`
	got := Propose(context.Background(), baseInput(), fake(reply))
	assert.Equal(t, OutcomeProposal, got.Outcome)
	assert.Equal(t, "voice-fold-when-m", got.ShapeTag)
	assert.Equal(t, []string{curID, winID}, got.SupportingEntryIDs)
	assert.False(t, got.NoLLM)
}

// TestPropose_ThreadsFrameworkOnProposal proves the active lens label threads
// into a proposal result (so the persist path can stamp provenance.framework),
// and that a lens-neutral input leaves it nil.
func TestPropose_ThreadsFrameworkOnProposal(t *testing.T) {
	reply := `{"outcome":"proposal","proposal_text":"One possible pattern: preparation as a way to feel safe.","shape_tag":"prep-as-safety","supporting_entry_ids":["` + curID + `"]}`

	label := "stoicism v1"
	framed := baseInput()
	framed.Framework = &label
	got := Propose(context.Background(), framed, fake(reply))
	require.Equal(t, OutcomeProposal, got.Outcome)
	require.NotNil(t, got.Framework)
	assert.Equal(t, label, *got.Framework)

	// Lens-neutral input carries no label.
	got = Propose(context.Background(), baseInput(), fake(reply))
	require.Equal(t, OutcomeProposal, got.Outcome)
	assert.Nil(t, got.Framework)
}

// TestPropose_FrameworkNotStampedOnNoPattern proves the label never rides a
// non-proposal outcome (only a proposal is ever persisted as an insight).
func TestPropose_FrameworkNotStampedOnNoPattern(t *testing.T) {
	label := "stoicism v1"
	in := baseInput()
	in.Framework = &label
	got := Propose(context.Background(), in, fake(`{"outcome":"no_pattern","message_text":"not yet"}`))
	assert.Equal(t, OutcomeNoPattern, got.Outcome)
	assert.Nil(t, got.Framework, "no_pattern is lens-neutral — nothing is persisted")
}

// TestPropose_SoftContradiction accepts a two-id question ending in "?".
func TestPropose_SoftContradiction(t *testing.T) {
	reply := `{"outcome":"soft_contradiction","message_text":"Earlier you read as X; today more like Y. Want to look at the gap?","supporting_entry_ids":["` + winID + `","` + curID + `"]}`
	got := Propose(context.Background(), baseInput(), fake(reply))
	assert.Equal(t, OutcomeSoftContradiction, got.Outcome)
	assert.Len(t, got.SupportingEntryIDs, 2)
}

// TestPropose_ModelNoPattern accepts an explicit no_pattern reply.
func TestPropose_ModelNoPattern(t *testing.T) {
	got := Propose(context.Background(), baseInput(), fake(`{"outcome":"no_pattern","message_text":"nothing yet"}`))
	assert.Equal(t, OutcomeNoPattern, got.Outcome)
	assert.False(t, got.NoLLM, "an explicit model no_pattern is not the deterministic path")
}

// TestPropose_RetryThenValid is §R-1: a malformed first reply is retried once
// and the valid second reply is accepted.
func TestPropose_RetryThenValid(t *testing.T) {
	valid := `{"outcome":"proposal","proposal_text":"One possible pattern.","shape_tag":"voice-fold","supporting_entry_ids":["` + curID + `"]}`
	p := fake("garbage", valid)
	got := Propose(context.Background(), baseInput(), p)
	assert.Equal(t, OutcomeProposal, got.Outcome)
	assert.Equal(t, 2, p.Calls())
}

// TestPropose_MalformedTwiceDowngrades is §R-2: unusable twice → no_pattern.
func TestPropose_MalformedTwiceDowngrades(t *testing.T) {
	p := fake("garbage", "still garbage")
	got := Propose(context.Background(), baseInput(), p)
	assert.Equal(t, OutcomeNoPattern, got.Outcome)
	assert.False(t, got.NoLLM)
	assert.Equal(t, 2, p.Calls())
}

// TestPropose_RejectedTagRetriedThenDifferent is §R-5: a proposal reusing a
// rejected shape_tag is invalid; the retry proposes a different tag and wins.
func TestPropose_RejectedTagRetriedThenDifferent(t *testing.T) {
	in := baseInput()
	in.RejectedShapeTags = []string{"family-defensiveness-default"}
	reused := `{"outcome":"proposal","proposal_text":"x","shape_tag":"family-defensiveness-default","supporting_entry_ids":["` + curID + `"]}`
	fresh := `{"outcome":"proposal","proposal_text":"One possible pattern.","shape_tag":"voice-fold","supporting_entry_ids":["` + curID + `"]}`
	got := Propose(context.Background(), in, fake(reused, fresh))
	assert.Equal(t, OutcomeProposal, got.Outcome)
	assert.Equal(t, "voice-fold", got.ShapeTag)
}

// TestPropose_RejectedTagTwiceDowngrades is §R-5's downgrade: a rejected tag
// proposed on both attempts falls back to no_pattern.
func TestPropose_RejectedTagTwiceDowngrades(t *testing.T) {
	in := baseInput()
	in.UnansweredShapeTags = []string{"quiet-day-flatness"}
	reused := `{"outcome":"proposal","proposal_text":"x","shape_tag":"quiet-day-flatness","supporting_entry_ids":["` + curID + `"]}`
	got := Propose(context.Background(), in, fake(reused, reused))
	assert.Equal(t, OutcomeNoPattern, got.Outcome)
}

// TestPropose_ValidationRejects covers the per-outcome validation rules: a bad
// shape_tag grammar, too many segments, an out-of-window citation, an empty
// proposal text, a soft contradiction with the wrong id count, and one that
// does not end in a question mark. Each makes both attempts invalid → no_pattern.
func TestPropose_ValidationRejects(t *testing.T) {
	cases := map[string]string{
		"bad grammar":         `{"outcome":"proposal","proposal_text":"x","shape_tag":"Voice_Fold","supporting_entry_ids":["` + curID + `"]}`,
		"too many segments":   `{"outcome":"proposal","proposal_text":"x","shape_tag":"a-b-c-d-e-f-g","supporting_entry_ids":["` + curID + `"]}`,
		"citation off-window": `{"outcome":"proposal","proposal_text":"x","shape_tag":"voice-fold","supporting_entry_ids":["raw_2000_01_01_00_00"]}`,
		"empty proposal text": `{"outcome":"proposal","proposal_text":"   ","shape_tag":"voice-fold","supporting_entry_ids":["` + curID + `"]}`,
		"no supporting ids":   `{"outcome":"proposal","proposal_text":"x","shape_tag":"voice-fold","supporting_entry_ids":[]}`,
		"soft one id":         `{"outcome":"soft_contradiction","message_text":"gap?","supporting_entry_ids":["` + curID + `"]}`,
		"soft no question":    `{"outcome":"soft_contradiction","message_text":"a gap.","supporting_entry_ids":["` + curID + `","` + winID + `"]}`,
		"unknown outcome":     `{"outcome":"lecture","message_text":"you should try harder"}`,
	}
	for name, reply := range cases {
		t.Run(name, func(t *testing.T) {
			got := Propose(context.Background(), baseInput(), fake(reply, reply))
			assert.Equal(t, OutcomeNoPattern, got.Outcome, "invalid reply must degrade to no_pattern")
		})
	}
}

// TestPropose_TransportErrorDowngrades covers the provider-error branch: a
// transport failure on both attempts degrades to no_pattern.
func TestPropose_TransportErrorDowngrades(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{{Err: provider.ErrTimeout}, {Err: provider.ErrTimeout}}}
	got := Propose(context.Background(), baseInput(), p)
	assert.Equal(t, OutcomeNoPattern, got.Outcome)
}

// TestProposeSystem_DenylistAndClean asserts the strict prompt restates the
// denied shapes and that the system prompt is blocklist-clean.
func TestProposeSystem_DenylistAndClean(t *testing.T) {
	in := baseInput()
	in.RejectedShapeTags = []string{"family-defensiveness-default"}
	in.UnansweredShapeTags = []string{"quiet-day-flatness", "family-defensiveness-default"}
	strict := proposeSystem(in, true)
	assert.Contains(t, strict, "family-defensiveness-default")
	assert.Contains(t, strict, "quiet-day-flatness")
	// The denylist is deduplicated across the two sets.
	assert.Equal(t, 1, strings.Count(strict, "family-defensiveness-default"))

	require.NotEmpty(t, windowSlice(in))
}
