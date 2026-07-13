package router

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	sort.Strings(files)
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
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := &provider.Fake{Script: []provider.Exchange{askAnswer(id)}}
			_, err := r.Ask(context.Background(), AskRequest{Question: "how do I act in groups?", Provider: p})
			assert.NoError(t, err)
		}()
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
	for i := 0; i < 5; i++ {
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
