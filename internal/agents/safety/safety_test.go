package safety

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/provider"
)

// rewriteFake returns a provider that replies with out once (the single
// rewrite completion Safety is allowed).
func rewriteFake(out string) *provider.Fake {
	return &provider.Fake{Script: []provider.Exchange{{Content: out}}}
}

// proposeCand builds a propose_pattern candidate with a valid shape_tag.
func proposeCand(text string) Candidate {
	return Candidate{FromAgent: FromReflection, Intent: IntentProposePattern, Text: text, ShapeTag: "voice-fold-when-m"}
}

// TestEvaluate_ExamplesTable drives every row of the agent-contracts.md §4
// examples table, the canonical acceptance surface for Safety/Consent.
func TestEvaluate_ExamplesTable(t *testing.T) {
	ctx := context.Background()
	sc := SessionContext{Command: "/checkin"}

	// Row 1: overclaim on a proposal → rewrite (phrase_blocklist).
	rw := "I noticed a possible pattern: when M. is in the room, you tend to fold. Does this resonate?"
	got := Evaluate(ctx, proposeCand("You always fold when M. is in the room."), sc, rewriteFake(rw))
	assert.Equal(t, Rewrite, got.Decision)
	assert.Equal(t, ReasonPhraseBlocklist, got.ReasonCode)
	assert.Equal(t, rw, got.Text)

	// Row 2: external-action verb → block (external_action_attempt).
	got = Evaluate(ctx, proposeCand("I'll send M. a follow-up message tonight."), sc, nil)
	assert.Equal(t, Block, got.Decision)
	assert.Equal(t, ReasonExternalActionAttempt, got.ReasonCode)
	assert.Empty(t, got.Text)

	// Row 3: clean no_pattern → pass (ok).
	clean := "I don't have enough yet to say anything useful — want to keep going?"
	got = Evaluate(ctx, Candidate{FromAgent: FromReflection, Intent: IntentNoPattern, Text: clean}, sc, nil)
	assert.Equal(t, Pass, got.Decision)
	assert.Equal(t, ReasonOK, got.ReasonCode)
	assert.Equal(t, clean, got.Text)

	// Row 4: diagnostic label on a proposal → block (diagnostic_language).
	got = Evaluate(ctx, proposeCand("You're an avoidant attacher."), sc, nil)
	assert.Equal(t, Block, got.Decision)
	assert.Equal(t, ReasonDiagnosticLanguage, got.ReasonCode)

	// Row 5: capture ack → pass (ok).
	ack := "Saved as raw_2026_05_05_19_42."
	got = Evaluate(ctx, Candidate{FromAgent: FromStructuringRendered, Intent: IntentAckCapture, Text: ack}, sc, nil)
	assert.Equal(t, Pass, got.Decision)
	assert.Equal(t, ack, got.Text)

	// Row 6: grounded answer citing an in-slice id → pass (ok).
	answer := "Based on i_2026_05_05_a, you've noted that you tend to test an idea once."
	got = Evaluate(ctx, Candidate{
		FromAgent: FromReflection, Intent: IntentAnswer, Text: answer,
		Citations: []string{"i_2026_05_05_a"}, AuthorizedIDs: []string{"i_2026_05_05_a"},
	}, sc, nil)
	assert.Equal(t, Pass, got.Decision)

	// Row 7: advice in an answer → block (agent_self_attempt).
	got = Evaluate(ctx, Candidate{FromAgent: FromReflection, Intent: IntentAnswer, Text: "You should start journaling daily about this."}, sc, nil)
	assert.Equal(t, Block, got.Decision)
	assert.Equal(t, ReasonAgentSelfAttempt, got.ReasonCode)

	// Row 8: answer citing an out-of-slice id → block (unverified_claim).
	got = Evaluate(ctx, Candidate{
		FromAgent: FromReflection, Intent: IntentAnswer, Text: "Based on i_2026_99_99_z, here is a thought.",
		Citations: []string{"i_2026_99_99_z"}, AuthorizedIDs: []string{"i_2026_05_05_a"},
	}, sc, nil)
	assert.Equal(t, Block, got.Decision)
	assert.Equal(t, ReasonUnverifiedClaim, got.ReasonCode)
}

// TestEvaluate_EmptyCandidateBlocksOK is §Sf-5: nothing to say blocks with ok.
func TestEvaluate_EmptyCandidateBlocksOK(t *testing.T) {
	got := Evaluate(context.Background(), Candidate{FromAgent: FromReflection, Intent: IntentNoPattern, Text: "   "}, SessionContext{}, nil)
	assert.Equal(t, Block, got.Decision)
	assert.Equal(t, ReasonOK, got.ReasonCode)
}

// TestEvaluate_ProposeMissingShapeTag is the §4 structural rule: a
// propose_pattern without a shape_tag is a scope violation.
func TestEvaluate_ProposeMissingShapeTag(t *testing.T) {
	got := Evaluate(context.Background(), Candidate{
		FromAgent: FromReflection, Intent: IntentProposePattern, Text: "One possible pattern: you test then back off.",
	}, SessionContext{}, nil)
	assert.Equal(t, Block, got.Decision)
	assert.Equal(t, ReasonScopeViolation, got.ReasonCode)
}

// TestEvaluate_BootstrapBlocksProposal is §Sf-6: a proposal during bootstrap
// is a scope violation even with a valid shape_tag.
func TestEvaluate_BootstrapBlocksProposal(t *testing.T) {
	got := Evaluate(context.Background(), proposeCand("One possible pattern: you test then back off."),
		SessionContext{Command: "/checkin", BootstrapMode: true}, nil)
	assert.Equal(t, Block, got.Decision)
	assert.Equal(t, ReasonScopeViolation, got.ReasonCode)
}

// TestEvaluate_UserAuthoredExempt proves verbatim user text quoted back is
// exempt from the external-action and diagnostic rules (§4 scope): "need to
// call the doctor" in the user's own note passes.
func TestEvaluate_UserAuthoredExempt(t *testing.T) {
	got := Evaluate(context.Background(), Candidate{
		FromAgent: FromStructuringRendered, Intent: IntentAckCapture,
		Text: "You logged: need to call the doctor.", UserAuthored: true,
	}, SessionContext{}, nil)
	assert.Equal(t, Pass, got.Decision, "verbatim user text is testimony, not an action attempt")
}

// TestEvaluate_RewriteFailPathsBlock proves every rewrite failure downgrades to
// block and never falls through to pass (§Sf-4): a nil provider, a transport
// error, an empty reply, a still-flagged reply, and a dropped supporting id.
func TestEvaluate_RewriteFailPathsBlock(t *testing.T) {
	ctx := context.Background()
	sc := SessionContext{Command: "/checkin"}
	over := proposeCand("You always fold.")

	// nil provider: rewrite is required but impossible.
	got := Evaluate(ctx, over, sc, nil)
	assert.Equal(t, Block, got.Decision)
	assert.Equal(t, ReasonPhraseBlocklist, got.ReasonCode)

	// transport error.
	got = Evaluate(ctx, over, sc, &provider.Fake{Script: []provider.Exchange{{Err: provider.ErrTimeout}}})
	assert.Equal(t, Block, got.Decision)

	// empty rewrite.
	got = Evaluate(ctx, over, sc, rewriteFake("   "))
	assert.Equal(t, Block, got.Decision)

	// rewrite still hits the blocklist.
	got = Evaluate(ctx, over, sc, rewriteFake("You always fold, obviously."))
	assert.Equal(t, Block, got.Decision)

	// rewrite drops a supporting id that was present in the original.
	cand := Candidate{
		FromAgent: FromReflection, Intent: IntentProposePattern, ShapeTag: "voice-fold",
		Text: "You always fold, per raw_2026_05_05_19_42.", SupportingEntryIDs: []string{"raw_2026_05_05_19_42"},
	}
	got = Evaluate(ctx, cand, sc, rewriteFake("I noticed a possible pattern: you tend to fold."))
	assert.Equal(t, Block, got.Decision, "a rewrite that drops a cited id is not allowed to pass")
}

// TestEvaluate_RewritePreservesSupportingID confirms a rewrite that keeps the
// cited id is accepted.
func TestEvaluate_RewritePreservesSupportingID(t *testing.T) {
	cand := Candidate{
		FromAgent: FromReflection, Intent: IntentProposePattern, ShapeTag: "voice-fold",
		Text: "You always fold, per raw_2026_05_05_19_42.", SupportingEntryIDs: []string{"raw_2026_05_05_19_42"},
	}
	rw := "I noticed a possible pattern: you tend to fold (raw_2026_05_05_19_42). Does this resonate?"
	got := Evaluate(context.Background(), cand, SessionContext{Command: "/checkin"}, rewriteFake(rw))
	assert.Equal(t, Rewrite, got.Decision)
	assert.Equal(t, rw, got.Text)
}

// TestEvaluate_ExternalActionBeatsRewrite proves an external-action verb blocks
// even alongside an overclaim — the harder rule wins (precedence).
func TestEvaluate_ExternalActionBeatsRewrite(t *testing.T) {
	got := Evaluate(context.Background(), proposeCand("You always email M. afterward."), SessionContext{}, rewriteFake("softened"))
	assert.Equal(t, Block, got.Decision)
	assert.Equal(t, ReasonExternalActionAttempt, got.ReasonCode)
}

// TestMatchesBlocklist covers the union check across all four categories plus
// a clean control.
func TestMatchesBlocklist(t *testing.T) {
	hits := []string{
		"you always do this", "you're an avoidant type", "clearly this is it",
		"I diagnose you", "attachment style", "OMG", "please send it",
		"auto-send it", "you should stop",
	}
	for _, h := range hits {
		assert.Truef(t, MatchesBlocklist(h), "expected a blocklist hit: %q", h)
	}
	clean := []string{
		"I noticed a possible pattern here.", "It sounds like part of you hoped for that.",
		"Saved as raw_2026_05_05_19_42.", "one possible reading is worth a look",
	}
	for _, c := range clean {
		assert.Falsef(t, MatchesBlocklist(c), "expected no blocklist hit: %q", c)
	}
}

// TestPhraseBlocklistFile is the source-of-truth check on
// scripts/phrase_blocklist.regex: it exists, every non-comment line compiles
// under Go's regexp (RE2), and the compiled union hits the documented "avoid"
// phrases while leaving the "prefer" phrases clean — the same behavior the
// compiled Go categories give (product-principles.md §6).
func TestPhraseBlocklistFile(t *testing.T) {
	path := filepath.Join("..", "..", "..", "scripts", "phrase_blocklist.regex")
	f, err := os.Open(path)
	require.NoError(t, err, "scripts/phrase_blocklist.regex must exist")
	t.Cleanup(func() { _ = f.Close() })

	var patterns []*regexp.Regexp
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		re, cErr := regexp.Compile("(?i)" + line)
		require.NoErrorf(t, cErr, "line must compile under Go regexp: %q", line)
		patterns = append(patterns, re)
	}
	require.NoError(t, sc.Err())
	require.NotEmpty(t, patterns, "the blocklist file must carry patterns")

	fileHits := func(s string) bool { return matchesAny(patterns, s) }
	for _, avoid := range []string{"you always", "you're an avoidant style", "clearly", "attachment style", "please send", "you should"} {
		assert.Truef(t, fileHits(avoid), "file blocklist must hit %q", avoid)
	}
	for _, prefer := range []string{"I noticed you mentioned X again today.", "It sounds like part of you was hoping for Y.", "One possible pattern: Z."} {
		assert.Falsef(t, fileHits(prefer), "file blocklist must not hit the preferred phrase %q", prefer)
	}
}

// TestAgentPromptsAreBlocklistClean asserts the agent-authored system prompt in
// this package carries no blocklist phrase — the "zero hits across prompt
// files" gate applied to the strings we actually ship (acceptance-criteria.md
// Phase 5).
func TestAgentPromptsAreBlocklistClean(t *testing.T) {
	assert.False(t, MatchesBlocklist(rewriteSystem()), "the rewrite system prompt must be blocklist-clean")
}
