package router

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/agents/intake"
	"github.com/mrz1836/lucid/internal/agents/reflection"
	"github.com/mrz1836/lucid/internal/agents/safety"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/storage"
)

// e2eExtraction is a well-formed structuring reply carrying one emotion and one
// theme and no people — enough signal to clear reflection.propose's §R-4
// empty-artifact short-circuit without depending on person_key derivation.
const e2eExtraction = `{
  "emotions": [{"name": "annoyed", "rationale": "user said 'annoyed'"}],
  "themes":   [{"name": "voice-not-heard", "rationale": "pushed back then dropped it"}],
  "people":   [],
  "notes": null
}`

// e2eBundle is a first-person bundle that clears Intake's authorship floor (the
// same shape the satisfied-checkin acceptance case uses).
const e2eBundle = "Rough dinner. I pushed back and dropped it. Annoyed and embarrassed."

// e2eCheckin drives one full /checkin (two follow-ups → satisfied) at instant
// `at`, structures the resulting raw entry, and returns the processed id (which
// equals the raw id). It is the capture+structure half of the Mirror, run over
// scripted fake providers — no live vendor auth (ADR-0006).
func e2eCheckin(t *testing.T, r *Router, at time.Time, opening string) string {
	t.Helper()
	cp := &provider.Fake{Script: []provider.Exchange{
		askEx("What happened?"), askEx("How did it land?"), doneEx(), bundleEx(e2eBundle),
	}}
	resp := &scriptResponder{turns: []intake.Turn{ans("I pushed back and dropped it."), ans("Annoyed and embarrassed.")}}
	cres, err := r.Checkin(context.Background(), CheckinRequest{
		Opening: opening, Now: at, Source: "cli", Harness: "cli", ChannelID: "cli",
		Provider: cp, Responder: resp,
	})
	require.NoError(t, err)
	require.True(t, cres.Wrote, "checkin should persist a raw entry")

	sp := &provider.Fake{Script: []provider.Exchange{extractEx(e2eExtraction)}}
	sres, err := r.Structure(context.Background(), StructureRequest{RawID: cres.RawID, Now: at, Provider: sp})
	require.NoError(t, err)
	require.True(t, sres.Wrote, "structuring should persist a processed artifact")
	require.Equal(t, cres.RawID, sres.ProcessedID)
	return sres.ProcessedID
}

// TestPipelineE2E_MirrorEndToEnd is the fake-backed capstone proof (Q5=A): it
// drives the whole Mirror end-to-end over the provider.Fake — CI never touches
// a live model — and asserts the three DoD-critical behaviors in one store:
//   - /checkin → structuring → resonance-gated proposal → a validated insight
//     written with full provenance (AC-8);
//   - /ask returns in-slice citations, and an out-of-slice citation is
//     Safety-blocked to the fixed fallback (AC-9);
//   - /reflect writes a reflection record but proposes nothing — no new insight
//     file is created (AC-9).
func TestPipelineE2E_MirrorEndToEnd(t *testing.T) {
	r, a, home := newBootedRouter(t)

	// Two capture→structure cycles in the same ISO week give reflection.propose a
	// non-empty recent window over a non-empty current artifact.
	priorAt := time.Date(2026, time.May, 4, 20, 0, 0, 0, time.UTC)
	currentAt := time.Date(2026, time.May, 6, 19, 42, 0, 0, time.UTC)
	priorID := e2eCheckin(t, r, priorAt, "Rough evening.")
	currentID := e2eCheckin(t, r, currentAt, "Another rough dinner.")

	// Validate the current artifact: a scripted proposal citing both entries is
	// gated through Safety (pass), accepted by the user, and persisted.
	vp := &provider.Fake{Script: []provider.Exchange{
		proposeReply("One possible pattern: you test an idea once, then back off.", "voice-fold-when-quiet", currentID, priorID),
	}}
	vresp := &scriptValidation{proposal: ProposalResponse{Kind: RespAccepted, Text: "Yes, that fits."}, rule: RuleResponse{}}
	vres, err := r.Validate(context.Background(), ValidateRequest{
		ProcessedID: currentID, Now: currentAt, Provider: vp, Responder: vresp,
	})
	require.NoError(t, err)
	require.Equal(t, reflection.OutcomeProposal, vres.Outcome)
	require.Equal(t, safety.Pass, vres.Decision)
	require.True(t, vres.Wrote)
	require.NotEmpty(t, vres.InsightID)

	// The insight landed accepted, with full provenance pointing back through the
	// pipeline (AC-8).
	ins, err := a.ReadInsight(vres.InsightID)
	require.NoError(t, err)
	assert.Equal(t, storage.InsightStatusAccepted, ins.Status)
	assert.Equal(t, storage.ResponseAccepted, ins.Provenance.UserResponseKind)
	assert.Equal(t, currentID, ins.Provenance.ProcessedArtifactID)
	assert.Equal(t, []string{currentID, priorID}, ins.Provenance.RawEntryIDs)
	assert.NotEmpty(t, ins.Provenance.ReflectionPromptVersion)
	assert.Equal(t, 1, countFiles(t, home, "insights"), "exactly one validated insight")

	// ── /ask is strictly read-only; snapshot the tree to prove it never mutates. ──
	beforeAsk := hashTree(t, home)

	// In-slice: the answer cites the insight the pipeline just wrote → Safety passes.
	inSlice := &provider.Fake{Script: []provider.Exchange{askAnswer(vres.InsightID)}}
	ares, err := r.Ask(context.Background(), AskRequest{Question: "how do I act when I disagree?", Provider: inSlice})
	require.NoError(t, err)
	assert.Equal(t, reflection.OutcomeAnswer, ares.Outcome)
	assert.Equal(t, safety.Pass, ares.Decision)
	assert.False(t, ares.Blocked)
	require.Len(t, ares.Citations, 1)
	assert.Equal(t, vres.InsightID, ares.Citations[0].ID, "the only citation is in the supplied slice")

	// Out-of-slice: the answer cites an id not in the store (twice, so the retry
	// cannot recover) → Safety blocks it to the fixed fallback.
	outOfSlice := &provider.Fake{Script: []provider.Exchange{
		askAnswer("i_2026_99_99_z"), askAnswer("i_2026_99_99_z"),
	}}
	bres, err := r.Ask(context.Background(), AskRequest{Question: "what about groups?", Provider: outOfSlice})
	require.NoError(t, err)
	assert.True(t, bres.Blocked)
	assert.Equal(t, safety.Block, bres.Decision)
	assert.Equal(t, askFallback, bres.Message)

	assert.Equal(t, beforeAsk, hashTree(t, home), "/ask writes nothing on either the in-slice or blocked path")

	// ── /reflect surfaces recall and writes a record, but proposes nothing. ──
	insightsBefore := countFiles(t, home, "insights")
	rp := &provider.Fake{Script: []provider.Exchange{recallReplyFor(vres.InsightID)}}
	rresp := &scriptRecall{def: RecallResponse{Status: storage.RecallConfirmed}}
	rres, err := r.Reflect(context.Background(), ReflectRequest{Now: currentAt, Provider: rp, Responder: rresp})
	require.NoError(t, err)
	require.True(t, rres.Wrote)
	assert.NotEmpty(t, rres.RecordID)
	require.Len(t, rres.Surfaces, 1, "the past-week insight is surfaced for recall")
	assert.Equal(t, 1, countFiles(t, home, "reflections"), "one reflection record for the week")
	assert.Equal(t, insightsBefore, countFiles(t, home, "insights"), "/reflect creates no new insight file")
}
