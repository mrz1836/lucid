package cli

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/storage"
)

// TestReflectWeek_Integration threads the whole weekly deep-dive through the
// real CLI surfaces — read-only `lucid reflect week` → parse the surfaced
// candidate → `lucid reflect week apply` → a tracked, resonance-gated,
// lens-labeled insight — over a rich fixture, and proves the thin and
// missing-context inputs degrade safely (no crash, no orphan write). Unlike the
// unit tests, the apply leg consumes the exact candidate the read leg emitted,
// so this is the Rock 1 DoD ("a weekly reflection produces a validated,
// resonance-gated pattern") proven end-to-end across both commands.
func TestReflectWeek_Integration(t *testing.T) {
	t.Run("rich_end_to_end_persists_lens_labeled_insight", func(t *testing.T) {
		home := isolatedHome(t)
		withClock(t, reflectNow())

		// A rich week: two citable raw entries inside the ISO week, plus a
		// processed artifact for the apply leg to anchor the insight to.
		citeID := seedWeekRaw(t, home, reflectNow().Add(-3*24*time.Hour), "over-prepared for the review call again")
		seedWeekRaw(t, home, reflectNow().Add(-2*24*time.Hour), "went quiet in standup")
		seedProcessedArtifact(t, home, "p_2026_05_08_a", reflectNow().Add(-24*time.Hour))

		// ── Read leg: one scripted deep-dive reply surfaces a cited candidate. ──
		// Snapshot the record counts the read leg must never grow (AC-2 read-only:
		// no new insight/reflection/raw file). The bundle's projections may touch
		// idempotent engine/metrics scaffolds, so full-tree byte-equality is too
		// strict — the contract is the record trees, not every derived file.
		rawBefore := countHomeFiles(t, home, "raw")
		processedBefore := countHomeFiles(t, home, "processed")
		withServeProvider(t, &provider.Fake{Script: []provider.Exchange{weekDeepReply("prep-as-safety", citeID)}})

		readOut, err := runReflectWeek(t, "--json")
		require.NoError(t, err)

		var view reflectWeekView
		require.NoError(t, json.Unmarshal([]byte(readOut), &view))
		require.NotNil(t, view.Pattern, "a rich week surfaces a candidate")
		assert.Equal(t, "prep-as-safety", view.Pattern.ShapeTag)
		assert.Equal(t, []string{citeID}, view.Pattern.SupportingEntryIDs, "the candidate cites the raw entry that grounds it")

		// The read leg persisted no record: no insight, no reflection, and neither
		// the raw nor the processed corpus grew (AC-2 read-only).
		assert.Equal(t, 0, countHomeFiles(t, home, "insights"), "the read leg persists no insight")
		assert.Equal(t, 0, countHomeFiles(t, home, "reflections"), "the read leg persists no reflection record")
		assert.Equal(t, rawBefore, countHomeFiles(t, home, "raw"), "the read leg adds no raw entry")
		assert.Equal(t, processedBefore, countHomeFiles(t, home, "processed"), "the read leg adds no processed artifact")

		// ── Apply leg: pipe the surfaced candidate + an accept back through the
		// existing resonance gate, stamped with the active lens label. ──
		payload, err := json.Marshal(reflectWeekApplyEnvelope{
			Candidate: reflectWeekPatternView{
				ProposalText:       view.Pattern.ProposalText,
				ShapeTag:           view.Pattern.ShapeTag,
				SupportingEntryIDs: view.Pattern.SupportingEntryIDs,
			},
			Framework: "stoicism v1",
			Response:  reflectWeekApplyResponse{Kind: "accepted", Text: "Yes, that fits."},
		})
		require.NoError(t, err)

		withServeProvider(t, &provider.Fake{})
		applyOut, err := runReflectWeekApply(t, string(payload), "--json")
		require.NoError(t, err)

		var applyView reflectWeekApplyView
		require.NoError(t, json.Unmarshal([]byte(applyOut), &applyView))
		require.True(t, applyView.Wrote, "an accepted candidate persists a tracked insight")
		require.NotEmpty(t, applyView.InsightID)

		// Rock 1 DoD: exactly one tracked insight, carrying its provenance.framework
		// lens label and the raw-entry-id citation that backs it (AC-6, AC-8).
		assert.Equal(t, 1, countHomeFiles(t, home, "insights"))
		ins, err := storage.New(home).ReadInsight(applyView.InsightID)
		require.NoError(t, err)
		require.NotNil(t, ins.Provenance.Framework, "the persisted insight is lens-labeled")
		assert.Equal(t, "stoicism v1", *ins.Provenance.Framework)
		assert.Equal(t, []string{citeID}, ins.Provenance.RawEntryIDs)
	})

	t.Run("thin_week_narrates_without_a_candidate", func(t *testing.T) {
		home := isolatedHome(t)
		withClock(t, reflectNow())
		seedWeekRaw(t, home, reflectNow().Add(-2*24*time.Hour), "a single quiet entry")

		// A thin-week reply carries the narrative but no candidate.
		withServeProvider(t, &provider.Fake{Script: []provider.Exchange{weekDeepReply("", "")}})

		out, err := runReflectWeek(t, "--json")
		require.NoError(t, err)

		var view reflectWeekView
		require.NoError(t, json.Unmarshal([]byte(out), &view))
		assert.NotEmpty(t, view.Summary, "a thin week still narrates")
		assert.Nil(t, view.Pattern, "a thin week surfaces no candidate")
		assert.Equal(t, 0, countHomeFiles(t, home, "insights"), "nothing is written")
	})

	t.Run("missing_context_degrades_with_no_model_call", func(t *testing.T) {
		home := isolatedHome(t)
		withClock(t, reflectNow())

		// An empty store must short-circuit before the model boundary.
		fake := &provider.Fake{}
		withServeProvider(t, fake)

		out, err := runReflectWeek(t)
		require.NoError(t, err)
		assert.Contains(t, out, reflectWeekEmpty, "an empty week prints the calm fallback")
		assert.Equal(t, 0, fake.Calls(), "no model call over an empty store")
		assert.Equal(t, 0, countHomeFiles(t, home, "insights"), "nothing is written")
	})
}
