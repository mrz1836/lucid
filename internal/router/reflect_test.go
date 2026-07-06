package router

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/isoweek"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/storage"
)

// reflectWeekNow is a Saturday in ISO week 19, 2026 — the deterministic instant
// the recall tests reflect at.
func reflectWeekNow() time.Time {
	return time.Date(2026, time.May, 9, 20, 10, 14, 0, time.UTC)
}

// seedInsight writes one accepted insight created at `at` with the given body
// and returns its id.
func seedInsight(t *testing.T, a *storage.Adapter, at time.Time, body string) string {
	t.Helper()
	res, err := a.WriteInsight(storage.Insight{
		CreatedAt: at,
		Status:    storage.InsightStatusAccepted,
		Provenance: storage.InsightProvenance{
			RawEntryIDs:             []string{"raw_2026_05_05_19_42"},
			ProcessedArtifactID:     "raw_2026_05_05_19_42",
			ReflectionPromptVersion: "reflection-2026.05.0",
			UserResponseKind:        storage.ResponseAccepted,
			UserResponseText:        "Yes, that fits.",
		},
		Body: body,
	})
	require.NoError(t, err)
	return res.InsightID
}

// seedRuledInsight writes an accepted insight and attaches a rule to it.
func seedRuledInsight(t *testing.T, a *storage.Adapter, at time.Time, body, rule string) string {
	t.Helper()
	id := seedInsight(t, a, at, body)
	require.NoError(t, a.SetInsightRule(id, rule, at))
	return id
}

// recallReplyFor builds a valid recall completion surfacing every id with a
// clean, blocklist-safe resonance line.
func recallReplyFor(ids ...string) provider.Exchange {
	entries := make([]string, 0, len(ids))
	for _, id := range ids {
		entries = append(entries, fmt.Sprintf(`{"id":%q,"surface_text":%q}`, id, "Does this still fit for you?"))
	}
	return provider.Exchange{Content: fmt.Sprintf(`{"outcome":"recall","ordered_insights":[%s]}`, strings.Join(entries, ","))}
}

// scriptRecall is a fixed-script RecallResponder: per-insight answers by id with
// a default, recording every surface it was shown so tests can assert the
// verbatim rule question.
type scriptRecall struct {
	byID     map[string]RecallResponse
	def      RecallResponse
	surfaces map[string]string
	err      error
}

func (s *scriptRecall) RespondToRecall(id, surface string) (RecallResponse, error) {
	if s.surfaces == nil {
		s.surfaces = map[string]string{}
	}
	s.surfaces[id] = surface
	if s.err != nil {
		return RecallResponse{}, s.err
	}
	if r, ok := s.byID[id]; ok {
		return r, nil
	}
	return s.def, nil
}

// TestReflect_6_1_ThreePastWeekEachSurfaced is acceptance case 6.1: three
// past-week insights are each surfaced, their status_history is updated per the
// user's response, and the ISO-week reflection record is written.
func TestReflect_6_1_ThreePastWeekEachSurfaced(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	now := reflectWeekNow()
	idA := seedInsight(t, a, now.Add(-4*24*time.Hour), "I go quiet in groups.")
	idB := seedInsight(t, a, now.Add(-3*24*time.Hour), "I over-prepare before hard talks.")
	idC := seedInsight(t, a, now.Add(-2*24*time.Hour), "I test an idea once and back off.")

	p := &provider.Fake{Script: []provider.Exchange{recallReplyFor(idC, idB, idA)}}
	resp := &scriptRecall{byID: map[string]RecallResponse{
		idA: {Status: storage.RecallConfirmed},
		idB: {Status: storage.RecallSoftened},
		idC: {Status: storage.RecallRetired},
	}}

	res, err := r.Reflect(context.Background(), ReflectRequest{Now: now, Provider: p, Responder: resp})
	require.NoError(t, err)
	require.True(t, res.Wrote)
	assert.Len(t, res.Surfaces, 3, "each past-week insight is surfaced")

	for id, wantKind := range map[string]string{
		idA: storage.RecallConfirmed, idB: storage.RecallSoftened, idC: storage.RecallRetired,
	} {
		ins, readErr := a.ReadInsight(id)
		require.NoError(t, readErr)
		last := ins.StatusHistory[len(ins.StatusHistory)-1]
		assert.Equal(t, wantKind, last.Kind, "insight %s got its transition", id)
		assert.True(t, last.At.Equal(now), "the transition is provenance-stamped")
	}

	// The reflection record is keyed by the ISO week of now and its window
	// bounds match the isoweek helper.
	assert.Equal(t, isoweek.ID(now), res.RecordID)
	rec, err := a.ReadReflection(res.RecordID)
	require.NoError(t, err)
	assert.Len(t, rec.Surfaced, 3)
	wantStart, wantEnd := isoweek.Bounds(now)
	assert.True(t, rec.WindowStart.Equal(wantStart))
	assert.True(t, rec.WindowEnd.Equal(wantEnd))
	assert.Equal(t, isoweek.Label(now), rec.ISOWeek)

	// /reflect never creates a new insight file.
	assert.Equal(t, 3, countFiles(t, aHome(a), "insights"))
}

// TestReflect_6_2_EmptyWeekFallback is acceptance case 6.2 / §R-7: with no
// insight validated in the last seven days, the router surfaces the two most
// recent regardless of age, adds the /log prompt, and — because unreflected
// entries exist — the /checkin pointer line. No proposal is generated.
func TestReflect_6_2_EmptyWeekFallback(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	now := time.Date(2026, time.May, 20, 20, 0, 0, 0, time.UTC)
	// Two insights well outside the seven-day window.
	old1 := seedInsight(t, a, now.Add(-15*24*time.Hour), "an older insight")
	old2 := seedInsight(t, a, now.Add(-12*24*time.Hour), "a slightly newer old insight")
	// An unreflected processed entry → the pointer line should appear.
	seedProc(t, a, "raw_2026_05_18_10_00", now.Add(-2*24*time.Hour), nil, "theme")

	p := &provider.Fake{} // the empty-week path is router-side, no model call
	resp := &scriptRecall{def: RecallResponse{Status: storage.RecallConfirmed}}

	res, err := r.Reflect(context.Background(), ReflectRequest{Now: now, Provider: p, Responder: resp})
	require.NoError(t, err)
	assert.Contains(t, res.Message, reflectQuietWeek)
	assert.Contains(t, res.Message, reflectLogPrompt)
	assert.Contains(t, res.Message, reflectCheckinPointer, "unreflected entries add the pointer line")
	assert.Len(t, res.Surfaces, 2, "the two most recent regardless of age")
	assert.Equal(t, 0, p.Calls(), "no proposal / no model call")

	// The two surfaced insights were the most recent (by age), and both use the
	// verbatim resonance the router builds.
	assert.Contains(t, resp.surfaces[old2], "Earlier you saved:")
	assert.Contains(t, resp.surfaces[old1], "Earlier you saved:")
	assert.Equal(t, 2, countFiles(t, aHome(a), "insights"), "no new insight file")
}

// TestReflect_6_2_EmptyWeekNoPointerWhenNoEntries confirms the pointer line is
// omitted when no unreflected entries have accumulated.
func TestReflect_6_2_EmptyWeekNoPointerWhenNoEntries(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	now := time.Date(2026, time.May, 20, 20, 0, 0, 0, time.UTC)
	seedInsight(t, a, now.Add(-15*24*time.Hour), "an older insight")

	res, err := r.Reflect(context.Background(), ReflectRequest{
		Now: now, Provider: &provider.Fake{}, Responder: &scriptRecall{def: RecallResponse{Status: storage.RecallConfirmed}},
	})
	require.NoError(t, err)
	assert.Contains(t, res.Message, reflectQuietWeek)
	assert.NotContains(t, res.Message, reflectCheckinPointer, "no entries → no pointer line")
}

// TestReflect_6_3_NothingValidated is acceptance case 6.3 / §E-3: zero insights
// anywhere returns the fixed line and writes no record.
func TestReflect_6_3_NothingValidated(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	res, err := r.Reflect(context.Background(), ReflectRequest{
		Now: reflectWeekNow(), Provider: &provider.Fake{}, Responder: &scriptRecall{},
	})
	require.NoError(t, err)
	assert.Equal(t, reflectNothingValidated, res.Message)
	assert.False(t, res.Wrote)
	assert.Empty(t, res.RecordID)
	assert.Equal(t, 0, countFiles(t, aHome(a), "reflections"), "no record written")
}

// TestReflect_6_4_TwiceSameWeekAppends is acceptance case 6.4: a second
// /reflect in the same ISO week appends to the same file's change log and
// insights_surfaced without duplicating the body.
func TestReflect_6_4_TwiceSameWeekAppends(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	now := reflectWeekNow()
	id := seedInsight(t, a, now.Add(-1*24*time.Hour), "I test an idea once and back off.")
	resp := &scriptRecall{def: RecallResponse{Status: storage.RecallConfirmed}}

	first, err := r.Reflect(context.Background(), ReflectRequest{
		Now: now, Provider: &provider.Fake{Script: []provider.Exchange{recallReplyFor(id)}}, Responder: resp,
	})
	require.NoError(t, err)

	second, err := r.Reflect(context.Background(), ReflectRequest{
		Now: now.Add(time.Hour), Provider: &provider.Fake{Script: []provider.Exchange{recallReplyFor(id)}}, Responder: resp,
	})
	require.NoError(t, err)
	assert.Equal(t, first.RecordID, second.RecordID, "same ISO-week record")

	rec, err := a.ReadReflection(first.RecordID)
	require.NoError(t, err)
	assert.Len(t, rec.Surfaced, 2, "both passes appended")
	assert.Len(t, rec.ChangeLog, 2)
	assert.Equal(t, 1, countFiles(t, aHome(a), "reflections"), "one file for the week")
}

// TestReflect_6_5_MalformedFallsBackVerbatim is acceptance case 6.5 / §R-8: a
// malformed recall reply degrades to surfacing insights verbatim with no novel
// framing.
func TestReflect_6_5_MalformedFallsBackVerbatim(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	now := reflectWeekNow()
	seedInsight(t, a, now.Add(-1*24*time.Hour), "I test an idea once and back off.")
	p := &provider.Fake{Script: []provider.Exchange{{Content: "not json"}, {Content: "{bad"}}}
	resp := &scriptRecall{def: RecallResponse{Status: storage.RecallConfirmed}}

	res, err := r.Reflect(context.Background(), ReflectRequest{Now: now, Provider: p, Responder: resp})
	require.NoError(t, err)
	assert.True(t, res.Fallback)
	require.Len(t, res.Surfaces, 1)
	assert.Contains(t, res.Surfaces[0].Surface, "Earlier you saved: 'I test an idea once and back off.'")
}

// TestReflect_6_6_RuledInsightRuleVerbatim is acceptance case 6.6: a ruled
// insight surfaces its rule verbatim, a "lapsed" answer appends {kind: lapsed}
// to rule_history, the surface is judgment-free, and a later "kept" appends
// normally.
func TestReflect_6_6_RuledInsightRuleVerbatim(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	now := reflectWeekNow()
	rule := "When I catch myself folding mid-sentence, finish the sentence."
	id := seedRuledInsight(t, a, now.Add(-1*24*time.Hour), "I test an idea once and back off.", rule)

	resp := &scriptRecall{def: RecallResponse{Status: storage.RecallConfirmed, Rule: storage.RuleLapsed}}
	_, err := r.Reflect(context.Background(), ReflectRequest{
		Now: now, Provider: &provider.Fake{Script: []provider.Exchange{recallReplyFor(id)}}, Responder: resp,
	})
	require.NoError(t, err)

	surface := resp.surfaces[id]
	assert.Contains(t, surface, rule, "the rule surfaces verbatim")
	assert.Contains(t, surface, "kept, lapsed, or retire it?")
	for _, judgment := range []string{"streak", "score", "shame", "keep it up", "good job"} {
		assert.NotContains(t, strings.ToLower(surface), judgment, "the surface is judgment-free")
	}

	ins, err := a.ReadInsight(id)
	require.NoError(t, err)
	require.Len(t, ins.RuleHistory, 2, "stated + lapsed")
	assert.Equal(t, storage.RuleLapsed, ins.RuleHistory[1].Kind)

	// A later "kept" appends normally.
	resp2 := &scriptRecall{def: RecallResponse{Status: storage.RecallConfirmed, Rule: storage.RuleKept}}
	_, err = r.Reflect(context.Background(), ReflectRequest{
		Now: now.Add(48 * time.Hour), Provider: &provider.Fake{Script: []provider.Exchange{recallReplyFor(id)}}, Responder: resp2,
	})
	require.NoError(t, err)
	ins, err = a.ReadInsight(id)
	require.NoError(t, err)
	assert.Equal(t, storage.RuleKept, ins.RuleHistory[len(ins.RuleHistory)-1].Kind)
}

// TestReflect_6_7_GatePanelAllSurface is acceptance case 6.7: /reflect gate
// over 12 accepted (5 ruled) surfaces all 12, the panel numbers match a hand
// count, no dominance line appears (no dominant person), and no new insight ids
// are created.
func TestReflect_6_7_GatePanelAllSurface(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	base := time.Date(2026, time.April, 1, 12, 0, 0, 0, time.UTC)
	ids := make([]string, 0, 12)
	for i := 0; i < 12; i++ {
		at := base.Add(time.Duration(i) * 24 * time.Hour)
		if i < 5 {
			ids = append(ids, seedRuledInsight(t, a, at, fmt.Sprintf("insight %d", i), "a stated rule"))
		} else {
			ids = append(ids, seedInsight(t, a, at, fmt.Sprintf("insight %d", i)))
		}
	}
	before := countFiles(t, aHome(a), "insights")

	// The gate window returns most-recent-first; surface every id.
	reversed := make([]string, len(ids))
	for i, id := range ids {
		reversed[len(ids)-1-i] = id
	}
	p := &provider.Fake{Script: []provider.Exchange{recallReplyFor(reversed...)}}
	resp := &scriptRecall{def: RecallResponse{Status: storage.RecallConfirmed}}

	res, err := r.Reflect(context.Background(), ReflectRequest{
		Scope: ReflectGate, Now: time.Date(2026, time.May, 9, 20, 0, 0, 0, time.UTC), Provider: p, Responder: resp,
	})
	require.NoError(t, err)
	assert.Len(t, res.Surfaces, 12, "all 12 surface (cap 50 not hit)")
	require.Len(t, res.Panel, 1, "panel line, no dominance line")
	assert.Equal(t, "Gate panel — 12 accepted this window, 5 rules stated, 5 rules standing.", res.Panel[0])
	assert.Equal(t, before, countFiles(t, aHome(a), "insights"), "no new insight ids")
}

// TestReflect_Gate_DominanceLine covers the dominance branch: a person who
// appears in more than the threshold share of entries yields one hypothesis-
// framed line; an off-limits person is never named.
func TestReflect_Gate_DominanceLine(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	now := time.Date(2026, time.May, 9, 20, 0, 0, 0, time.UTC)
	seedInsight(t, a, now.Add(-24*time.Hour), "an insight")

	m := []storage.ProcessedPerson{{DisplayName: "M.", PersonKey: "person_a-river", FirstMention: true}}
	seedProc(t, a, "raw_2026_05_01_10_00", now.Add(-8*24*time.Hour), m, "t")
	seedProc(t, a, "raw_2026_05_02_10_00", now.Add(-7*24*time.Hour), m, "t")
	seedProc(t, a, "raw_2026_05_03_10_00", now.Add(-6*24*time.Hour), nil, "t")

	res, err := r.Reflect(context.Background(), ReflectRequest{
		Scope: ReflectGate, Now: now, Provider: &provider.Fake{Script: []provider.Exchange{recallReplyFor()}},
		Responder: &scriptRecall{def: RecallResponse{Status: storage.RecallConfirmed}},
	})
	require.NoError(t, err)
	require.Len(t, res.Panel, 2, "panel + dominance line")
	assert.Contains(t, res.Panel[1], "M. appears in 67% of entries")
	assert.True(t, strings.HasSuffix(res.Panel[1], "?"), "dominance is hypothesis-framed")
}

// TestReflect_Gate_DominanceRespectsOffLimits confirms an off-limits person is
// excluded from the dominance computation (never named).
func TestReflect_Gate_DominanceRespectsOffLimits(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	now := time.Date(2026, time.May, 9, 20, 0, 0, 0, time.UTC)
	seedInsight(t, a, now.Add(-24*time.Hour), "an insight")
	require.NoError(t, a.WriteOffLimitsPersonKeys([]string{"person_a-river"}))

	m := []storage.ProcessedPerson{{DisplayName: "M.", PersonKey: "person_a-river", FirstMention: true}}
	seedProc(t, a, "raw_2026_05_01_10_00", now.Add(-8*24*time.Hour), m, "t")
	seedProc(t, a, "raw_2026_05_02_10_00", now.Add(-7*24*time.Hour), m, "t")

	res, err := r.Reflect(context.Background(), ReflectRequest{
		Scope: ReflectGate, Now: now, Provider: &provider.Fake{Script: []provider.Exchange{recallReplyFor()}},
		Responder: &scriptRecall{def: RecallResponse{Status: storage.RecallConfirmed}},
	})
	require.NoError(t, err)
	assert.Len(t, res.Panel, 1, "an off-limits person never yields a dominance line")
}

// TestReflect_Gate_EmptyAcceptedButRetiredExist returns the gate-empty copy when
// no accepted insight remains but an insight file (retired) is present.
func TestReflect_Gate_EmptyAcceptedButRetiredExist(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	now := reflectWeekNow()
	id := seedInsight(t, a, now.Add(-24*time.Hour), "x")
	require.NoError(t, a.UpdateInsightStatus(id, storage.RecallRetired, now))

	res, err := r.Reflect(context.Background(), ReflectRequest{
		Scope: ReflectGate, Now: now, Provider: &provider.Fake{}, Responder: &scriptRecall{},
	})
	require.NoError(t, err)
	assert.Equal(t, reflectGateEmpty, res.Message)
	assert.False(t, res.Wrote)
}

// TestReflect_R9_IdempotentDuplicateConfirm is §R-9: a repeat confirm within a
// week appends a second confirmed entry (monotonic status_history) rather than
// erroring.
func TestReflect_R9_IdempotentDuplicateConfirm(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	now := reflectWeekNow()
	id := seedInsight(t, a, now.Add(-24*time.Hour), "I test an idea once and back off.")
	resp := &scriptRecall{def: RecallResponse{Status: storage.RecallConfirmed}}

	_, err := r.Reflect(context.Background(), ReflectRequest{
		Now: now, Provider: &provider.Fake{Script: []provider.Exchange{recallReplyFor(id)}}, Responder: resp,
	})
	require.NoError(t, err)
	_, err = r.Reflect(context.Background(), ReflectRequest{
		Now: now.Add(time.Hour), Provider: &provider.Fake{Script: []provider.Exchange{recallReplyFor(id)}}, Responder: resp,
	})
	require.NoError(t, err)

	ins, err := a.ReadInsight(id)
	require.NoError(t, err)
	require.Len(t, ins.StatusHistory, 3, "accepted + two confirms")
	assert.False(t, ins.StatusHistory[2].At.Before(ins.StatusHistory[1].At), "monotonic")
}

// TestReflect_R15_UnmappedRuleAnswerRecordsNothing is §R-15: a rule answer that
// maps to none of kept/lapsed/retire records nothing on the rule while the
// insight-status part is processed independently.
func TestReflect_R15_UnmappedRuleAnswerRecordsNothing(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	now := reflectWeekNow()
	id := seedRuledInsight(t, a, now.Add(-24*time.Hour), "I test an idea once and back off.", "a stated rule")
	resp := &scriptRecall{def: RecallResponse{Status: storage.RecallConfirmed, Rule: "maybe later"}}

	_, err := r.Reflect(context.Background(), ReflectRequest{
		Now: now, Provider: &provider.Fake{Script: []provider.Exchange{recallReplyFor(id)}}, Responder: resp,
	})
	require.NoError(t, err)

	ins, err := a.ReadInsight(id)
	require.NoError(t, err)
	assert.Len(t, ins.RuleHistory, 1, "only the original stated entry — the unmapped answer records nothing")
	assert.Equal(t, storage.RecallConfirmed, ins.StatusHistory[len(ins.StatusHistory)-1].Kind,
		"the status answer is still processed")
}

// TestReflect_Unanswered records an insight the user let pass as unanswered on
// the record without touching the insight's status_history.
func TestReflect_Unanswered(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	now := reflectWeekNow()
	id := seedInsight(t, a, now.Add(-24*time.Hour), "I test an idea once and back off.")
	resp := &scriptRecall{def: RecallResponse{}} // no status answer

	res, err := r.Reflect(context.Background(), ReflectRequest{
		Now: now, Provider: &provider.Fake{Script: []provider.Exchange{recallReplyFor(id)}}, Responder: resp,
	})
	require.NoError(t, err)
	require.Len(t, res.Surfaces, 1)
	assert.Equal(t, responseKindUnanswered, res.Surfaces[0].ResponseKind)

	ins, err := a.ReadInsight(id)
	require.NoError(t, err)
	assert.Len(t, ins.StatusHistory, 1, "an unanswered surface advances nothing on the insight")
}

// TestReflect_ResponderErrorSurfaces confirms a responder failure aborts the
// turn rather than silently dropping the pass.
func TestReflect_ResponderErrorSurfaces(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	now := reflectWeekNow()
	id := seedInsight(t, a, now.Add(-24*time.Hour), "x pattern")
	resp := &scriptRecall{err: fmt.Errorf("thread closed")}

	_, err := r.Reflect(context.Background(), ReflectRequest{
		Now: now, Provider: &provider.Fake{Script: []provider.Exchange{recallReplyFor(id)}}, Responder: resp,
	})
	require.Error(t, err)
}

// TestReflect_EmptyWeek_AllRetiredNoRecord covers the empty-week branch where
// every insight has been retired: nothing is surfaced and no record is written.
func TestReflect_EmptyWeek_AllRetiredNoRecord(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	now := time.Date(2026, time.May, 20, 20, 0, 0, 0, time.UTC)
	id := seedInsight(t, a, now.Add(-15*24*time.Hour), "an insight")
	require.NoError(t, a.UpdateInsightStatus(id, storage.RecallRetired, now.Add(-14*24*time.Hour)))

	res, err := r.Reflect(context.Background(), ReflectRequest{
		Now: now, Provider: &provider.Fake{}, Responder: &scriptRecall{def: RecallResponse{Status: storage.RecallConfirmed}},
	})
	require.NoError(t, err)
	assert.Contains(t, res.Message, reflectQuietWeek)
	assert.Empty(t, res.Surfaces, "nothing left to surface")
	assert.False(t, res.Wrote)
	assert.Equal(t, 0, countFiles(t, aHome(a), "reflections"))
}

// TestReflect_EmptyWeek_PointerRespectsPriorReflection exercises the
// hasUnreflectedEntries `ok` branch: a processed entry after the last
// reflection adds the pointer; one only before it does not.
func TestReflect_EmptyWeek_PointerRespectsPriorReflection(t *testing.T) {
	newRun := func(t *testing.T, procAt time.Time) ReflectResult {
		r, a, _ := newBootedRouter(t)
		now := time.Date(2026, time.May, 20, 20, 0, 0, 0, time.UTC)
		seedInsight(t, a, now.Add(-15*24*time.Hour), "old insight")
		reflAt := now.Add(-10 * 24 * time.Hour)
		_, err := a.WriteReflection(storage.Reflection{
			ID: "reflection_2026_w19", ISOWeek: "2026-W19",
			WindowStart: reflAt.Add(-2 * 24 * time.Hour), WindowEnd: reflAt.Add(4 * 24 * time.Hour),
			CreatedAt: reflAt, AgentVersion: "reflection-2026.05.0", Summary: "s",
		})
		require.NoError(t, err)
		seedProc(t, a, "raw_2026_05_15_10_00", procAt, nil, "t")
		res, err := r.Reflect(context.Background(), ReflectRequest{
			Now: now, Provider: &provider.Fake{}, Responder: &scriptRecall{def: RecallResponse{Status: storage.RecallConfirmed}},
		})
		require.NoError(t, err)
		return res
	}

	after := newRun(t, time.Date(2026, time.May, 15, 10, 0, 0, 0, time.UTC)) // after the reflection
	assert.Contains(t, after.Message, reflectCheckinPointer)

	before := newRun(t, time.Date(2026, time.May, 8, 10, 0, 0, 0, time.UTC)) // before the reflection
	assert.NotContains(t, before.Message, reflectCheckinPointer)
}

// TestReflect_GateResonanceBlockedFallsBackVerbatim covers the Safety block
// branch of gateResonance: a model surface that trips a coaching phrase is
// replaced by the verbatim resonance line rather than shown as advice.
func TestReflect_GateResonanceBlockedFallsBackVerbatim(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	now := reflectWeekNow()
	id := seedInsight(t, a, now.Add(-24*time.Hour), "I test an idea once and back off.")
	advice := provider.Exchange{Content: fmt.Sprintf(
		`{"outcome":"recall","ordered_insights":[{"id":%q,"surface_text":"you should journal daily about this"}]}`, id,
	)}
	resp := &scriptRecall{def: RecallResponse{Status: storage.RecallConfirmed}}

	_, err := r.Reflect(context.Background(), ReflectRequest{
		Now: now, Provider: &provider.Fake{Script: []provider.Exchange{advice}}, Responder: resp,
	})
	require.NoError(t, err)
	assert.Contains(t, resp.surfaces[id], "Earlier you saved:", "blocked advice degrades to verbatim")
	assert.NotContains(t, resp.surfaces[id], "you should", "the advice never reaches the user")
}

// TestReflect_Gate_LapsedRuleNotStanding confirms a lapsed rule is counted as
// stated but not standing in the gate panel.
func TestReflect_Gate_LapsedRuleNotStanding(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	now := time.Date(2026, time.May, 9, 20, 0, 0, 0, time.UTC)
	kept := seedRuledInsight(t, a, now.Add(-3*24*time.Hour), "kept insight", "keep this")
	lapsed := seedRuledInsight(t, a, now.Add(-2*24*time.Hour), "lapsed insight", "lapse this")
	require.NoError(t, a.UpdateInsightRuleStatus(lapsed, storage.RuleLapsed, now.Add(-1*24*time.Hour)))

	res, err := r.Reflect(context.Background(), ReflectRequest{
		Scope: ReflectGate, Now: now,
		Provider:  &provider.Fake{Script: []provider.Exchange{recallReplyFor(lapsed, kept)}},
		Responder: &scriptRecall{def: RecallResponse{Status: storage.RecallConfirmed}},
	})
	require.NoError(t, err)
	assert.Equal(t, "Gate panel — 2 accepted this window, 2 rules stated, 1 rules standing.", res.Panel[0])
}

// TestReflect_Gate_DominanceBelowThreshold confirms no dominance line appears
// when the top person's share does not exceed the threshold.
func TestReflect_Gate_DominanceBelowThreshold(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	now := time.Date(2026, time.May, 9, 20, 0, 0, 0, time.UTC)
	seedInsight(t, a, now.Add(-24*time.Hour), "an insight")
	m := []storage.ProcessedPerson{{DisplayName: "M.", PersonKey: "person_a-river", FirstMention: true}}
	seedProc(t, a, "raw_2026_05_01_10_00", now.Add(-8*24*time.Hour), m, "t")
	seedProc(t, a, "raw_2026_05_02_10_00", now.Add(-7*24*time.Hour), nil, "t")
	seedProc(t, a, "raw_2026_05_03_10_00", now.Add(-6*24*time.Hour), nil, "t")

	res, err := r.Reflect(context.Background(), ReflectRequest{
		Scope: ReflectGate, Now: now, Provider: &provider.Fake{Script: []provider.Exchange{recallReplyFor()}},
		Responder: &scriptRecall{def: RecallResponse{Status: storage.RecallConfirmed}},
	})
	require.NoError(t, err)
	assert.Len(t, res.Panel, 1, "33% share is below the 0.5 threshold — no dominance line")
}

// TestRuleStanding covers every branch of the standing predicate directly.
func TestRuleStanding(t *testing.T) {
	rule := "r"
	assert.False(t, ruleStanding(storage.Insight{}), "no rule → not standing")
	assert.True(t, ruleStanding(storage.Insight{Rule: &rule}), "ruled, no history yet → standing")
	standing := map[string]bool{
		storage.RuleStated: true, storage.RuleKept: true, storage.RuleLapsed: false, storage.RuleRetire: false,
	}
	for kind, want := range standing {
		got := ruleStanding(storage.Insight{Rule: &rule, RuleHistory: []storage.TimedEvent{{Kind: kind}}})
		assert.Equal(t, want, got, "latest rule_history kind %q", kind)
	}
}

// aHome exposes the adapter home for the countFiles helper (the router tests
// already hold it, but the reflect helpers take the adapter).
func aHome(a *storage.Adapter) string { return a.Home() }
