package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

	t.Run("read_does_not_advance_the_cursor", func(t *testing.T) {
		home := isolatedHome(t)
		withClock(t, reflectNow())

		// Two weeks of material, so the catch-up window is unmistakably wider
		// than the ISO week containing today.
		seedWeekRaw(t, home, reflectNow().Add(-13*24*time.Hour), "started the long stretch")
		seedWeekRaw(t, home, reflectNow().Add(-2*24*time.Hour), "went quiet in standup")
		withServeProvider(t, &provider.Fake{Script: []provider.Exchange{
			weekDeepReply("", ""), weekDeepReply("", ""),
		}})

		first := runReflectWeekJSON(t)
		requireNoReflectionCursor(t, home)
		assert.Equal(t, 14, first.DaysCovered, "the first-run window spans both weeks")

		// Reading again re-reads exactly the same span. That is the point: an
		// opened-but-abandoned sit-down costs a repeat read, never a lost day.
		second := runReflectWeekJSON(t)
		requireNoReflectionCursor(t, home)
		assert.Equal(t, first.WindowStart, second.WindowStart)
		assert.Equal(t, first.WindowEnd, second.WindowEnd)
		assert.Equal(t, first.DaysCovered, second.DaysCovered, "a read never narrows its own next window")
	})

	t.Run("close_then_next_run_starts_from_the_cursor", func(t *testing.T) {
		home := isolatedHome(t)
		withClock(t, reflectNow())
		seedWeekRaw(t, home, reflectNow().Add(-13*24*time.Hour), "started the long stretch")
		seedWeekRaw(t, home, reflectNow().Add(-2*24*time.Hour), "went quiet in standup")
		withServeProvider(t, &provider.Fake{Script: []provider.Exchange{
			weekDeepReply("", ""), weekDeepReply("", ""),
		}})

		before := runReflectWeekJSON(t)
		assert.Equal(t, "2026-04-26", before.WindowStart, "the first read catches up from the earliest entry")

		// Closing is the explicit act — and the only read-path act — that moves
		// the cursor forward.
		_, err := runReflectWeekClose(t)
		require.NoError(t, err)

		// Two days on, with a fresh entry logged.
		later := reflectNow().Add(2 * 24 * time.Hour)
		withClock(t, later)
		seedWeekRaw(t, home, later, "back at it after the close")

		after := runReflectWeekJSON(t)
		assert.Equal(t, "2026-05-09", after.WindowStart,
			"the next window starts on the day the close landed, so that evening's entries are re-read")
		assert.Equal(t, "2026-05-11", after.WindowEnd)
		assert.Equal(t, 3, after.DaysCovered)
		assert.Greater(t, after.WindowStart, before.WindowStart, "the already-reflected span is not re-read")
	})

	t.Run("skipped_week_is_caught_up", func(t *testing.T) {
		home := isolatedHome(t)

		// A reflection closed a fortnight ago…
		withClock(t, reflectNow().Add(-14*24*time.Hour))
		_, err := runReflectWeekClose(t)
		require.NoError(t, err)

		// …then two weeks pass with entries logged in BOTH, and only the second
		// week's sit-down actually happens. Before the catch-up window, the
		// first week's entries fell outside this read and were never seen.
		withClock(t, reflectNow())
		skippedID := seedWeekRaw(t, home, reflectNow().Add(-12*24*time.Hour), "the week nobody reflected on")
		recentID := seedWeekRaw(t, home, reflectNow().Add(-2*24*time.Hour), "the week that got read")

		fake := &provider.Fake{Script: []provider.Exchange{weekDeepReply("", "")}}
		withServeProvider(t, fake)

		view := runReflectWeekJSON(t)
		assert.Equal(t, "2026-04-25", view.WindowStart, "the window reaches back to the close, not to Monday")
		assert.GreaterOrEqual(t, view.DaysCovered, 14, "a skipped week is caught up, not dropped")
		assert.False(t, view.Capped, "a fortnight sits well inside the ceiling")

		// A wide window is not the same as the older entries being read: only
		// the digest handed to the model proves the skipped week was seen.
		prompt := deepDivePrompt(fake)
		assert.Contains(t, prompt, skippedID, "the skipped week's entry reaches the deep-dive")
		assert.Contains(t, prompt, recentID, "alongside the recent week's")
	})

	t.Run("cap_states_the_shortfall", func(t *testing.T) {
		home := isolatedHome(t)

		// A 96-day gap — far past the 35-day ceiling.
		withClock(t, reflectNow().Add(-95*24*time.Hour))
		_, err := runReflectWeekClose(t)
		require.NoError(t, err)

		withClock(t, reflectNow())
		seedWeekRaw(t, home, reflectNow().Add(-2*24*time.Hour), "an entry inside the capped span")
		withServeProvider(t, &provider.Fake{Script: []provider.Exchange{
			weekDeepReply("", ""), weekDeepReply("", ""),
		}})

		view := runReflectWeekJSON(t)
		assert.True(t, view.Capped)
		assert.Equal(t, "2026-04-05", view.WindowStart, "the read covers the most recent ceiling's worth")
		assert.Equal(t, 35, view.DaysCovered)
		assert.Equal(t, 61, view.UncoveredDays, "and states the days it deferred")

		// The cap is only acceptable because it is honest: the human form says
		// the shortfall out loud and names the way to read the full span.
		out, err := runReflectWeek(t)
		require.NoError(t, err)
		assert.Contains(t, out,
			"Covering the last 35 of 96 un-reflected days — pass --since to read the full span.")
	})
}

// runReflectWeekJSON runs the default (catch-up) `lucid reflect week --json` and
// decodes the view, so a window assertion reads as one line.
func runReflectWeekJSON(t *testing.T) reflectWeekView {
	t.Helper()
	out, err := runReflectWeek(t, "--json")
	require.NoError(t, err)
	var view reflectWeekView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	return view
}

// requireNoReflectionCursor asserts the reflected-through cursor is absent from
// disk. The read leg resolves its window by reading that receipt, and reading a
// missing one must never create it — this is the guard on that.
func requireNoReflectionCursor(t *testing.T, home string) {
	t.Helper()
	_, err := os.Stat(filepath.Join(home, "engine", "reflection", "receipt.json"))
	require.ErrorIs(t, err, os.ErrNotExist, "the read leg never stamps the reflected-through cursor")
}

// deepDivePrompt concatenates every message the fake provider was handed, so a
// test can assert what the deep-dive actually showed the model rather than
// trusting the window arithmetic alone.
func deepDivePrompt(f *provider.Fake) string {
	var b strings.Builder
	for _, req := range f.Requests {
		for _, m := range req.Messages {
			b.WriteString(m.Content)
			b.WriteString("\n")
		}
	}
	return b.String()
}
