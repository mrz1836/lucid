package router

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/agents/reflection"
	"github.com/mrz1836/lucid/internal/agents/safety"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/storage"
)

// askAnswer builds a valid grounded-answer completion citing one insight id.
func askAnswer(id string) provider.Exchange {
	return provider.Exchange{Content: fmt.Sprintf(
		`{"outcome":"answer","answer_text":"Based on %s, you tend to go quiet.","citations":[{"kind":"insight","id":%q}]}`,
		id, id,
	)}
}

// hashTree returns a stable digest of every file under home, so a test can
// assert ~/.lucid/ is byte-identical before and after a read-only command.
func hashTree(t *testing.T, home string) string {
	t.Helper()
	var files []string
	require.NoError(t, filepath.WalkDir(home, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, p)
		}
		return nil
	}))
	slices.Sort(files)
	h := sha256.New()
	for _, f := range files {
		b, err := os.ReadFile(f)
		require.NoError(t, err)
		_, _ = fmt.Fprintf(h, "%s\x00%x\x00", f, sha256.Sum256(b))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TestAsk_7_1_PopulatedStoreAnswers is acceptance case 7.1: /ask over a populated
// store returns an answer whose citations are all in the supplied slice.
func TestAsk_7_1_PopulatedStoreAnswers(t *testing.T) {
	r, a, home := newBootedRouter(t)
	id := seedInsight(t, a, reflectWeekNow().Add(-2*24*time.Hour), "I go quiet in groups.")

	before := hashTree(t, home)
	p := &provider.Fake{Script: []provider.Exchange{askAnswer(id)}}
	res, err := r.Ask(context.Background(), AskRequest{Question: "how do I act in groups?", Provider: p})
	require.NoError(t, err)

	assert.Equal(t, reflection.OutcomeAnswer, res.Outcome)
	assert.Equal(t, safety.Pass, res.Decision)
	require.Len(t, res.Citations, 1)
	assert.Equal(t, id, res.Citations[0].ID)
	assert.False(t, res.Blocked)
	assert.Equal(t, before, hashTree(t, home), "/ask writes nothing")
}

// TestAsk_7_2_EmptyStore_NoLLM is acceptance case 7.2: /ask over an empty store
// returns insufficient without an LLM call and points at /checkin or /log.
func TestAsk_7_2_EmptyStore_NoLLM(t *testing.T) {
	r, _, home := newBootedRouter(t)

	before := hashTree(t, home)
	p := &provider.Fake{}
	res, err := r.Ask(context.Background(), AskRequest{Question: "anything?", Provider: p})
	require.NoError(t, err)

	assert.Equal(t, reflection.OutcomeInsufficient, res.Outcome)
	assert.Equal(t, 0, p.Calls(), "no model call over an empty store")
	assert.Contains(t, res.Message, "/checkin")
	assert.Contains(t, res.Message, "/log")
	assert.Equal(t, before, hashTree(t, home))
}

// TestAsk_7_3_OutOfSliceCitation_SafetyBlocks is acceptance case 7.3: an answer
// citing an id not in the slice is blocked by Safety (Sf-7); the user sees the
// fallback.
func TestAsk_7_3_OutOfSliceCitation_SafetyBlocks(t *testing.T) {
	r, a, home := newBootedRouter(t)
	seedInsight(t, a, reflectWeekNow().Add(-2*24*time.Hour), "I go quiet in groups.")

	before := hashTree(t, home)
	// The model cites an id that is not in the store; it does so twice, so the
	// agent's retry cannot recover and the answer reaches Safety.
	p := &provider.Fake{Script: []provider.Exchange{askAnswer("i_2026_99_99_z"), askAnswer("i_2026_99_99_z")}}
	res, err := r.Ask(context.Background(), AskRequest{Question: "q?", Provider: p})
	require.NoError(t, err)

	assert.True(t, res.Blocked)
	assert.Equal(t, safety.Block, res.Decision)
	assert.Equal(t, safety.ReasonUnverifiedClaim, res.ReasonCode)
	assert.Equal(t, askFallback, res.Message)
	assert.Equal(t, before, hashTree(t, home))
}

// TestAsk_7_4_Advice_SafetyBlocks is acceptance case 7.4: an answer giving advice
// is blocked by Safety (Sf-8); the user sees the fallback.
func TestAsk_7_4_Advice_SafetyBlocks(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	id := seedInsight(t, a, reflectWeekNow().Add(-2*24*time.Hour), "I go quiet in groups.")

	advice := fmt.Sprintf(
		`{"outcome":"answer","answer_text":"You should journal more about this.","citations":[{"kind":"insight","id":%q}]}`, id,
	)
	p := &provider.Fake{Script: []provider.Exchange{{Content: advice}}}
	res, err := r.Ask(context.Background(), AskRequest{Question: "what do I do?", Provider: p})
	require.NoError(t, err)

	assert.True(t, res.Blocked)
	assert.Equal(t, safety.Block, res.Decision)
	assert.Equal(t, safety.ReasonAgentSelfAttempt, res.ReasonCode)
	assert.Equal(t, askFallback, res.Message)
}

// TestAsk_7_5_ByteIdenticalAndSerialized is acceptance case 7.5: /ask never
// mutates ~/.lucid/, and concurrent reads leave the tree byte-identical.
func TestAsk_7_5_ByteIdenticalAndSerialized(t *testing.T) {
	r, a, home := newBootedRouter(t)
	id := seedInsight(t, a, reflectWeekNow().Add(-2*24*time.Hour), "I go quiet in groups.")

	before := hashTree(t, home)

	// Two concurrent /ask turns, each with its own (single-use) fake provider,
	// exercise the read-only path under contention (St-6).
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			p := &provider.Fake{Script: []provider.Exchange{askAnswer(id)}}
			_, err := r.Ask(context.Background(), AskRequest{Question: "how do I act in groups?", Provider: p})
			assert.NoError(t, err)
		})
	}
	wg.Wait()

	assert.Equal(t, before, hashTree(t, home), "~/.lucid/ is byte-identical after every /ask")
}

// TestAsk_7_6_TransportTimeout is acceptance case 7.6: a persistent transport
// failure surfaces the transient message with no partial output and no write.
func TestAsk_7_6_TransportTimeout(t *testing.T) {
	r, a, home := newBootedRouter(t)
	seedInsight(t, a, reflectWeekNow().Add(-2*24*time.Hour), "I go quiet in groups.")

	before := hashTree(t, home)
	p := &provider.Fake{Script: []provider.Exchange{
		{Err: fmt.Errorf("net: %w", provider.ErrTimeout)},
		{Err: fmt.Errorf("net: %w", provider.ErrTimeout)},
	}}
	res, err := r.Ask(context.Background(), AskRequest{Question: "q?", Provider: p})
	require.NoError(t, err)

	assert.Equal(t, reflection.OutcomeInsufficient, res.Outcome)
	assert.Empty(t, res.Citations, "no partial output")
	assert.Equal(t, 2, p.Calls(), "one retry")
	assert.Equal(t, before, hashTree(t, home))
}

// TestAsk_CitesReflection covers the reflections-slice path end to end: a
// weekly reflection is included in the slice and the grounded answer cites it.
func TestAsk_CitesReflection(t *testing.T) {
	r, a, home := newBootedRouter(t)
	id := seedInsight(t, a, reflectWeekNow().Add(-2*24*time.Hour), "I go quiet in groups.")
	now := reflectWeekNow()
	_, err := a.WriteReflection(storage.Reflection{
		ID: "reflection_2026_w19", ISOWeek: "2026-W19", WindowStart: now.Add(-2 * 24 * time.Hour),
		WindowEnd: now.Add(4 * 24 * time.Hour), CreatedAt: now, AgentVersion: "reflection-2026.05.0",
		Summary: "Confirmed one insight this week.",
	})
	require.NoError(t, err)

	before := hashTree(t, home)
	content := `{"outcome":"answer","answer_text":"Your week 19 recall confirmed it.",` +
		`"citations":[{"kind":"reflection","id":"reflection_2026_w19"},{"kind":"insight","id":"` + id + `"}]}`
	p := &provider.Fake{Script: []provider.Exchange{{Content: content}}}
	res, err := r.Ask(context.Background(), AskRequest{Question: "what did I confirm?", Provider: p})
	require.NoError(t, err)

	assert.Equal(t, reflection.OutcomeAnswer, res.Outcome)
	assert.Equal(t, safety.Pass, res.Decision, "an in-slice reflection citation passes Safety")
	require.Len(t, res.Citations, 2)
	assert.Equal(t, before, hashTree(t, home))
}

// TestAsk_SliceCapsRespected confirms the router applies ask_insights_cap when
// building the slice handed to the agent: with five accepted insights and a cap
// of two, the model sees exactly two insight blocks.
func TestAsk_SliceCapsRespected(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	r.cfg.AskInsightsCap = 2 // force a tiny cap so truncation is provable

	base := reflectWeekNow()
	for i := range 5 {
		seedInsight(t, a, base.Add(time.Duration(-i)*24*time.Hour), fmt.Sprintf("insight %d", i))
	}

	captured := &strings.Builder{}
	p := &recordingProvider{
		inner: &provider.Fake{Script: []provider.Exchange{{Content: `{"outcome":"insufficient","answer_text":"nope","citations":[]}`}}},
		seen:  captured,
	}
	_, err := r.Ask(context.Background(), AskRequest{Question: "q?", Provider: p})
	require.NoError(t, err)

	// Each insight in the slice renders exactly one "- id:" line.
	assert.Equal(t, 2, strings.Count(captured.String(), "- id:"), "insights slice is capped at ask_insights_cap")
}

// seedSelfFact records one self fact through the router's own write path and
// returns its minted id. Values are synthetic throughout.
func seedSelfFact(t *testing.T, r *Router, key, value string) string {
	t.Helper()
	res, err := r.SelfSet(SelfSetRequest{Key: key, Value: value, Now: reflectWeekNow()})
	require.NoError(t, err)
	return res.Fact.ID
}

// askSelfFactAnswer builds a valid grounded-answer completion citing one
// self-fact id.
func askSelfFactAnswer(id string) provider.Exchange {
	return provider.Exchange{Content: fmt.Sprintf(
		`{"outcome":"answer","answer_text":"You recorded that about yourself.","citations":[{"kind":"self_fact","id":%q}]}`,
		id,
	)}
}

// TestAsk_SelfFactsEnterTheSlice confirms the router composes active self facts
// into the agent's slice — as values, never as a location — and that /ask stays
// read-only over the store it just read.
func TestAsk_SelfFactsEnterTheSlice(t *testing.T) {
	r, _, home := newBootedRouter(t)
	seedSelfFact(t, r, "identity.generation", "elder millennial")

	before := hashTree(t, home)
	captured := &strings.Builder{}
	p := &recordingProvider{
		inner: &provider.Fake{Script: []provider.Exchange{{Content: `{"outcome":"insufficient","answer_text":"nope","citations":[]}`}}},
		seen:  captured,
	}
	_, err := r.Ask(context.Background(), AskRequest{Question: "what generation am I?", Provider: p})
	require.NoError(t, err)

	body := captured.String()
	assert.Contains(t, body, "SELF FACTS")
	assert.Contains(t, body, "identity.generation")
	assert.Contains(t, body, "elder millennial")
	assert.NotContains(t, body, "engine/", "a fact grounds as a composed value, never as a path")
	assert.Equal(t, before, hashTree(t, home), "/ask writes nothing")
}

// TestAsk_SelfFactCitationIsAuthorized is the payoff of the minted-id identity
// model: a self fact in the slice is in Safety's authorized set, so a grounded
// self-fact citation passes rather than being blocked as unverified (§Sf-7).
func TestAsk_SelfFactCitationIsAuthorized(t *testing.T) {
	r, _, home := newBootedRouter(t)
	id := seedSelfFact(t, r, "identity.generation", "elder millennial")

	before := hashTree(t, home)
	p := &provider.Fake{Script: []provider.Exchange{askSelfFactAnswer(id)}}
	res, err := r.Ask(context.Background(), AskRequest{Question: "what generation am I?", Provider: p})
	require.NoError(t, err)

	assert.Equal(t, reflection.OutcomeAnswer, res.Outcome)
	assert.Equal(t, safety.Pass, res.Decision, "an in-slice self-fact citation passes Safety")
	assert.False(t, res.Blocked)
	require.Len(t, res.Citations, 1)
	assert.Equal(t, reflection.CitationSelfFact, res.Citations[0].Kind)
	assert.Equal(t, id, res.Citations[0].ID)
	assert.Equal(t, before, hashTree(t, home))
}

// TestAsk_SelfFactCitationOutOfSliceIsBlocked confirms the authorized set is a
// real boundary and not a formality: a fact pushed past the cap is not in the
// slice, so citing it is blocked to the fallback (§Sf-7).
func TestAsk_SelfFactCitationOutOfSliceIsBlocked(t *testing.T) {
	r, _, home := newBootedRouter(t)
	inSlice := seedSelfFact(t, r, "identity.generation", "elder millennial")
	pastCap := seedSelfFact(t, r, "pref.tea", "strong")
	r.cfg.SelfFactsCap = 1 // only the identity fact survives the cap

	before := hashTree(t, home)
	// Cited twice, so the agent's retry cannot recover and the answer reaches Safety.
	p := &provider.Fake{Script: []provider.Exchange{askSelfFactAnswer(pastCap), askSelfFactAnswer(pastCap)}}
	res, err := r.Ask(context.Background(), AskRequest{Question: "q?", Provider: p})
	require.NoError(t, err)

	assert.NotEqual(t, inSlice, pastCap)
	assert.True(t, res.Blocked)
	assert.Equal(t, safety.Block, res.Decision)
	assert.Equal(t, safety.ReasonUnverifiedClaim, res.ReasonCode)
	assert.Equal(t, askFallback, res.Message)
	assert.Equal(t, before, hashTree(t, home))
}

// TestAsk_SelfFactsSliceOrderIsNamespacePriorityNotRecency is the load-bearing
// ordering guard. Self facts are atemporal, so the slice is ordered by namespace
// priority then key — never by recency, which would evict a first-year fact
// (blood type) in favor of last week's trivia. The facts are recorded in
// deliberately adversarial order and the cap is set to one, so a recency sort
// would demonstrably have produced a different slice.
func TestAsk_SelfFactsSliceOrderIsNamespacePriorityNotRecency(t *testing.T) {
	r, _, _ := newBootedRouter(t)
	// Lowest-priority namespaces recorded FIRST and LAST; identity in between.
	seedSelfFact(t, r, "misc.trivia", "collects stamps")
	seedSelfFact(t, r, "identity.generation", "elder millennial")
	newest := seedSelfFact(t, r, "pref.tea", "strong")
	r.cfg.SelfFactsCap = 1

	captured := &strings.Builder{}
	p := &recordingProvider{
		inner: &provider.Fake{Script: []provider.Exchange{{Content: `{"outcome":"insufficient","answer_text":"nope","citations":[]}`}}},
		seen:  captured,
	}
	_, err := r.Ask(context.Background(), AskRequest{Question: "q?", Provider: p})
	require.NoError(t, err)

	body := captured.String()
	assert.Contains(t, body, "identity.generation", "the highest-priority namespace leads the slice")
	assert.NotContains(t, body, "pref.tea", "the most recently recorded fact is not the one kept")
	assert.NotContains(t, body, "misc.trivia")
	assert.NotContains(t, body, newest, "a recency sort would have kept this id instead")
}

// TestAsk_LeavesSelfFactsStoreAlone extends the read-only guarantee to the new
// store, both ways: an existing self.json is byte-identical after /ask, and a
// Ledger scaffolded before this release — one with no self.json at all — does
// not gain one, because the read tolerates the absence rather than scaffolding.
func TestAsk_LeavesSelfFactsStoreAlone(t *testing.T) {
	t.Run("absent stays absent", func(t *testing.T) {
		r, _, home := newBootedRouter(t)
		selfPath := filepath.Join(home, "engine", "self.json")
		require.NoFileExists(t, selfPath, "the engine tree is unscaffolded here, as on a pre-upgrade Ledger")

		p := &provider.Fake{}
		_, err := r.Ask(context.Background(), AskRequest{Question: "q?", Provider: p})
		require.NoError(t, err, "a missing self.json is not an error on the read-only path")
		assert.NoFileExists(t, selfPath, "/ask never scaffolds")
	})

	t.Run("present stays byte-identical", func(t *testing.T) {
		r, _, home := newBootedRouter(t)
		id := seedSelfFact(t, r, "identity.generation", "elder millennial")
		selfPath := filepath.Join(home, "engine", "self.json")
		before, err := os.ReadFile(selfPath)
		require.NoError(t, err)

		p := &provider.Fake{Script: []provider.Exchange{askSelfFactAnswer(id)}}
		_, err = r.Ask(context.Background(), AskRequest{Question: "q?", Provider: p})
		require.NoError(t, err)

		after, err := os.ReadFile(selfPath)
		require.NoError(t, err)
		assert.Equal(t, before, after, "engine/self.json is byte-identical after /ask")
	})
}

// recordingProvider records the last slice body sent, then delegates.
type recordingProvider struct {
	inner *provider.Fake
	seen  *strings.Builder
}

func (p *recordingProvider) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	if len(req.Messages) > 0 {
		p.seen.Reset()
		p.seen.WriteString(req.Messages[0].Content)
	}
	return p.inner.Complete(ctx, req)
}
