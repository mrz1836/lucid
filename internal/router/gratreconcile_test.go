package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/provider"
)

// gratreconcile_test.go covers `lucid gratitude reconcile` (gratreconcile.go;
// gratitude.md §7.9): the dry run that proposes and writes nothing, the
// `--apply` that folds exactly the tier-2 High proposals through the ordinary
// merge path, the fold direction, the one-proposal-per-entry rule, and the
// optional tier-3 pass — advisory only, minimal egress, and degrading to tier 2
// on any outage. Every tier-3 test drives the judge with provider.Fake, so none
// needs a live model or the network (ADR-0006). Every phrase here is synthetic.

// gratitudeFiles reads every file in the gratitude tree, byte for byte — the
// strongest "nothing was written" check a dry run can be held to.
func gratitudeFiles(t *testing.T, r *Router) map[string]string {
	t.Helper()
	dir := filepath.Join(r.store.Home(), "registries", "gratitude")
	des, err := os.ReadDir(dir)
	require.NoError(t, err)
	files := make(map[string]string, len(des))
	for _, de := range des {
		b, rerr := os.ReadFile(filepath.Join(dir, de.Name()))
		require.NoError(t, rerr)
		files[de.Name()] = string(b)
	}
	return files
}

// reconcileSeed is the tally [seedReconcileTally] builds, by entry.
type reconcileSeed struct {
	coffee, coffees, work, home GratitudeWriteResult
}

// seedReconcileTally builds a tally with one clear duplicate by wording (a
// singular and its plural: tier-2 High, so --apply folds it), one close call (an
// exact tie by wording: advisory only), and two look-alikes that share no word
// ("my dog" / "my dad": never paired). The coffee entry has the higher count, so
// the plural folds into it; the walk to work was created first, so the walk home
// folds into it.
func seedReconcileTally(t *testing.T, r *Router) reconcileSeed {
	t.Helper()
	var s reconcileSeed
	s.coffee = addGratitude(t, r, "my morning coffee", day1())
	addGratitude(t, r, "my morning coffee", day2())
	var err error
	s.coffees, err = r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "morning coffees", ForceNew: true, Now: day2()})
	require.NoError(t, err)
	s.work = addGratitude(t, r, "the walk to work", day1())
	s.home, err = r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "the walk home", ForceNew: true, Now: day2()})
	require.NoError(t, err)
	addGratitude(t, r, "my dog", day1())
	addGratitude(t, r, "my dad", day1())
	return s
}

// TestGratitudeReconcileDryRunWritesNothing: a plain `reconcile` proposes folds
// and writes nothing (gratitude.md §7.9). The clear duplicate by wording is
// marked to apply, the lower count folding into the higher; the exact tie is
// advisory, with the merge command that folds it by hand; the no-shared-word
// look-alikes are never proposed; and every file in the tally is byte-identical
// afterwards.
func TestGratitudeReconcileDryRunWritesNothing(t *testing.T) {
	r := bootedGratitude(t)
	seed := seedReconcileTally(t, r)
	coffee, coffees, work, home := seed.coffee, seed.coffees, seed.work, seed.home
	before := gratitudeFiles(t, r)

	res, err := r.ReconcileGratitude(t.Context(), ReconcileGratitudeRequest{Now: day3()})
	require.NoError(t, err)

	assert.Equal(t, []GratitudeReconcileProposal{
		{
			Source: coffees.Key, SourceThing: "morning coffees",
			Target: coffee.Key, TargetThing: "my morning coffee",
			Score: 1, MatchTier: 2, Band: observations.GratitudeBandHigh, WillApply: true,
			Command: fmt.Sprintf("lucid gratitude merge %s %s", coffees.Key, coffee.Key),
		},
		{
			Source: home.Key, SourceThing: "the walk home",
			Target: work.Key, TargetThing: "the walk to work",
			Score: 0.5, MatchTier: 2, Band: observations.GratitudeBandAmbiguous, WillApply: false,
			Command: fmt.Sprintf("lucid gratitude merge %s %s", home.Key, work.Key),
		},
	}, res.View.Proposals)
	assert.Equal(t, GratitudeTier3Disabled, res.View.Tier3, "tier 3 is opt-in, and the report says so")
	assert.NotNil(t, res.View.Applied)
	assert.Empty(t, res.View.Applied, "a dry run applies nothing")
	assert.Equal(t, before, gratitudeFiles(t, r), "a dry run writes nothing")

	joined := strings.Join(res.Lines, "\n")
	assert.Contains(t, joined, "2 possible duplicates (dry run — nothing was changed):")
	assert.Contains(t, joined, fmt.Sprintf("fold  %s %q into %s %q — by wording, tier 2 (1.00)",
		coffees.Key, "morning coffees", coffee.Key, "my morning coffee"))
	assert.Contains(t, joined, fmt.Sprintf("look  %s %q into %s %q — by wording, tier 2 (0.50)",
		home.Key, "the walk home", work.Key, "the walk to work"))
	assert.Contains(t, joined, fmt.Sprintf("lucid gratitude merge %s %s", home.Key, work.Key),
		"an advisory pair shows the command that folds it by hand")
	assert.Contains(t, joined, "reconcile --apply")
	assert.Contains(t, joined, "lucid backup", "the undo path is named before anything is folded")
	assert.Contains(t, joined, "proposed by wording only")
}

// TestGratitudeReconcileApplyFoldsViaMerge: `--apply` folds exactly the pairs the
// dry run marked — no more — each through the ordinary merge path (gratitude.md
// §7.9, §4): a merge event on the target naming the absorbed source, whose
// receipt the result returns, and the source rewritten as a redirect tombstone,
// with the single-hop invariant kept for a tombstone that already pointed at the
// source. The advisory pair is left alone. A second --apply has nothing left to
// fold.
func TestGratitudeReconcileApplyFoldsViaMerge(t *testing.T) {
	r := bootedGratitude(t)
	seed := seedReconcileTally(t, r)
	coffee, coffees, work, home := seed.coffee, seed.coffees, seed.work, seed.home

	// An older duplicate already merged into the plural: after the plural folds
	// away, that tombstone must forward straight to the coffee entry (one hop).
	older := addGratitude(t, r, "a cup of coffee at dawn", day1())
	_, err := r.MergeGratitude(GratitudeMergeRequest{Source: older.Key, Target: coffees.Key, Now: day2()})
	require.NoError(t, err)

	dry, err := r.ReconcileGratitude(t.Context(), ReconcileGratitudeRequest{Now: day3()})
	require.NoError(t, err)

	res, err := r.ReconcileGratitude(t.Context(), ReconcileGratitudeRequest{Apply: true, Now: day3()})
	require.NoError(t, err)
	assert.Equal(t, dry.View.Proposals, res.View.Proposals, "the applied run acts on the set the dry run showed")
	assert.Empty(t, res.View.Tier3, "an apply never consults the model")
	require.Len(t, res.View.Applied, 1, "only the pair marked to apply is folded")
	fold := res.View.Applied[0]
	assert.Equal(t, coffees.Key, fold.Source)
	assert.Equal(t, coffee.Key, fold.Target)

	target := gratitudeEntry(t, r, coffee.Key)
	last := target.History[len(target.History)-1]
	assert.Equal(t, fold.Receipt, last.ID, "the fold's receipt is the merge event's")
	assert.Equal(t, observations.GratitudeEventMerge, last.Type)
	assert.Equal(t, coffees.Key, last.SourceKey)
	assert.Equal(t, 2, last.SourceCount, "the plural's own occurrence and the older duplicate it absorbed")
	assert.Equal(t, 4, target.Tally().Count)
	assert.Contains(t, target.Aka, "morning coffees", "the target absorbs the source's wordings")

	source := gratitudeEntry(t, r, coffees.Key)
	assert.Equal(t, coffee.Key, source.RedirectTo, "the source is a redirect tombstone, kept not deleted")
	assert.Equal(t, coffee.Key, gratitudeEntry(t, r, older.Key).RedirectTo, "the redirect graph stays single-hop")

	for _, key := range []string{work.Key, home.Key} {
		e := gratitudeEntry(t, r, key)
		assert.Falsef(t, e.IsTombstone(), "the advisory pair's %s is never folded", key)
		assert.Equalf(t, 1, e.Tally().Count, "the advisory pair's %s is untouched", key)
	}

	joined := strings.Join(res.Lines, "\n")
	assert.Contains(t, joined, "Folded 1 duplicate:")
	assert.Contains(t, joined, fold.Receipt, "each fold prints its receipt")
	assert.Contains(t, joined, "Worth a look, not folded:")

	again, err := r.ReconcileGratitude(t.Context(), ReconcileGratitudeRequest{Apply: true, Now: day3()})
	require.NoError(t, err)
	assert.Empty(t, again.View.Applied)
	assert.Contains(t, strings.Join(again.Lines, "\n"), "Nothing to fold")
}

// TestGratitudeReconcileEmptyTally: with nothing, or one entry, to pair there is
// no proposal and no tier-3 pass to report, and the report is never a bare blank.
func TestGratitudeReconcileEmptyTally(t *testing.T) {
	r := bootedTier3(t)
	fake := &provider.Fake{}

	res, err := r.ReconcileGratitude(t.Context(), ReconcileGratitudeRequest{Provider: fake})
	require.NoError(t, err)
	assert.NotNil(t, res.View.Proposals)
	assert.Empty(t, res.View.Proposals)
	assert.Empty(t, res.View.Tier3, "tier 3 is not needed with nothing to pair")
	assert.Equal(t, []string{"No likely duplicates in the gratitude tally — nothing to fold."}, res.Lines)

	addGratitude(t, r, "my morning coffee", day1())
	res, err = r.ReconcileGratitude(t.Context(), ReconcileGratitudeRequest{Provider: fake})
	require.NoError(t, err)
	assert.Empty(t, res.View.Proposals)
	assert.Empty(t, res.View.Tier3)
	assert.Zero(t, fake.Calls(), "one entry has no pair, so the model is never asked")
}

// TestGratitudeReconcileFoldDirection pins which entry of a pair folds into
// which (gratitude.md §7.9): the lower count into the higher, a count tie the
// later-created into the earlier (compared as instants, across offsets), and a
// tie of both the greater key into the lesser.
func TestGratitudeReconcileFoldDirection(t *testing.T) {
	entry := func(key, created string, occurrences int) observations.GratitudeEntry {
		e := observations.NewGratitudeEntry(key, key, created)
		for range occurrences {
			e.History = append(e.History, observations.GratitudeEvent{Type: observations.GratitudeEventOccurrence, Date: "2026-07-02"})
		}
		return e
	}
	tests := []struct {
		name       string
		x, y       observations.GratitudeEntry
		wantSource string
	}{
		{
			name:       "the lower count folds into the higher",
			x:          entry("gratitude_a-one", "2026-07-01T21:00:00-04:00", 3),
			y:          entry("gratitude_b-two", "2026-06-01T21:00:00-04:00", 1),
			wantSource: "gratitude_b-two",
		},
		{
			name:       "a count tie folds the later-created into the earlier",
			x:          entry("gratitude_a-one", "2026-07-01T21:00:00-04:00", 1),
			y:          entry("gratitude_b-two", "2026-06-01T21:00:00-04:00", 1),
			wantSource: "gratitude_a-one",
		},
		{
			name:       "creation instants compare across offsets",
			x:          entry("gratitude_a-one", "2026-07-01T23:00:00-04:00", 1), // 03:00Z on the 2nd
			y:          entry("gratitude_b-two", "2026-07-02T01:00:00Z", 1),      // 01:00Z on the 2nd
			wantSource: "gratitude_a-one",
		},
		{
			name:       "a tie of both folds the greater key into the lesser",
			x:          entry("gratitude_a-one", "2026-07-01T21:00:00-04:00", 1),
			y:          entry("gratitude_b-two", "2026-07-01T21:00:00-04:00", 1),
			wantSource: "gratitude_b-two",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src, dst := gratitudeFoldDirection(tt.x, tt.y)
			assert.Equal(t, tt.wantSource, src.Key)
			swappedSrc, swappedDst := gratitudeFoldDirection(tt.y, tt.x)
			assert.Equal(t, src.Key, swappedSrc.Key, "the direction does not depend on argument order")
			assert.Equal(t, dst.Key, swappedDst.Key)
		})
	}
}

// TestGratitudeReconcileEachEntryOnce: every entry appears in at most one
// proposal (gratitude.md §7.9), so no fold chains through a tombstone it just
// made and every printed merge command still runs after the others. The
// apply-able tier-2 High pair is placed first, so no advisory pair — not even a
// higher-scoring tier-3 one — crowds it out; the advisory pairs then take the
// entries left, highest score first.
func TestGratitudeReconcileEachEntryOnce(t *testing.T) {
	const a, b, c, d = "gratitude_a-one", "gratitude_b-two", "gratitude_c-three", "gratitude_d-four"
	live := []observations.GratitudeEntry{
		observations.NewGratitudeEntry(a, "one", "2026-07-01T21:00:00-04:00"),
		observations.NewGratitudeEntry(b, "two", "2026-07-02T21:00:00-04:00"),
		observations.NewGratitudeEntry(c, "three", "2026-07-03T21:00:00-04:00"),
		observations.NewGratitudeEntry(d, "four", "2026-07-04T21:00:00-04:00"),
	}
	banded := func(x, y string, score float64, band observations.GratitudeBand) observations.GratitudePair {
		p := observations.NewGratitudePair(x, y, score)
		p.Band = band
		return p
	}
	tier2 := []observations.GratitudePair{
		banded(a, b, 0.9, observations.GratitudeBandHigh),
		banded(b, c, 0.7, observations.GratitudeBandAmbiguous),
		banded(c, d, 0.6, observations.GratitudeBandAmbiguous),
	}
	tier3 := []observations.GratitudePair{banded(a, d, 0.99, observations.GratitudeBandHigh)}

	got := selectGratitudeProposals(tier2, tier3, live)
	require.Len(t, got, 2)
	assert.Equal(t, [2]string{b, a}, [2]string{got[0].Source, got[0].Target}, "the apply-able pair comes first")
	assert.True(t, got[0].WillApply)
	assert.Equal(t, [2]string{d, c}, [2]string{got[1].Source, got[1].Target}, "the entries left pair up, best first")
	assert.False(t, got[1].WillApply)

	only3 := selectGratitudeProposals(nil, tier3, live)
	require.Len(t, only3, 1)
	assert.Equal(t, 3, only3[0].MatchTier)
	assert.Equal(t, observations.GratitudeBandHigh, only3[0].Band)
	assert.False(t, only3[0].WillApply, "a tier-3 pair is never applied, however confident")
}

// reconcileSlatePositions maps each live entry's key to its 1-based position in
// the slate the router sends the reconcile judge — computed by the production
// slate rule, so a scripted reply names the entries a test intends.
func reconcileSlatePositions(t *testing.T, r *Router) map[string]int {
	t.Helper()
	live, err := r.liveGratitude()
	require.NoError(t, err)
	slate := gratitudeReconcileSlate(live, observations.Tier2Pairs(live), r.gratitudeMatchConfig().Tier3MaxCandidates)
	pos := make(map[string]int, len(slate))
	for i, e := range slate {
		pos[e.Key] = i + 1
	}
	return pos
}

// reconcileReply scripts a reconcile-judge answer pairing entries by key.
func reconcileReply(t *testing.T, r *Router, pairs ...struct {
	X, Y  string
	Score float64
},
) provider.Exchange {
	t.Helper()
	pos := reconcileSlatePositions(t, r)
	type match struct {
		A     int     `json:"a"`
		B     int     `json:"b"`
		Score float64 `json:"score"`
	}
	out := make([]match, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, match{A: pos[p.X], B: pos[p.Y], Score: p.Score})
	}
	b, err := json.Marshal(map[string][]match{"pairs": out})
	require.NoError(t, err)
	return provider.Exchange{Content: string(b)}
}

// scoredPair is one reconcileReply argument.
func scoredPair(x, y string, score float64) struct {
	X, Y  string
	Score float64
} {
	return struct {
		X, Y  string
		Score float64
	}{x, y, score}
}

// TestGratitudeReconcileTier3Advisory: on a dry run with tier 3 enabled, one
// gratitude.reconcile call finds the by-meaning duplicate tier 2 cannot see —
// two wordings with no word in common — and it is proposed as advisory, never
// marked to apply however confident, with the merge command that folds it by
// hand (gratitude.md §7.9). The request carries only the numbered wordings, no
// key, count, or date. `--apply` never consults the model and never folds a
// tier-3 pair.
func TestGratitudeReconcileTier3Advisory(t *testing.T) {
	r := bootedTier3(t)
	wheels := addGratitude(t, r, "the two wheels that carry me to work", day1())
	addGratitude(t, r, "the two wheels that carry me to work", day2())
	bike := addGratitude(t, r, "my bike", day2())
	addGratitude(t, r, "clean drinking water from the tap", day3())
	before := gratitudeFiles(t, r)

	live, err := r.liveGratitude()
	require.NoError(t, err)
	require.Empty(t, observations.Tier2Pairs(live), "no two wordings share a word: tier 2 finds nothing")

	fake := &provider.Fake{Script: []provider.Exchange{reconcileReply(t, r, scoredPair(bike.Key, wheels.Key, 0.97))}}
	res, err := r.ReconcileGratitude(t.Context(), ReconcileGratitudeRequest{Provider: fake, Now: day3()})
	require.NoError(t, err)
	require.Equal(t, 1, fake.Calls())
	assert.Equal(t, GratitudeTier3Used, res.View.Tier3)
	assert.Equal(t, []GratitudeReconcileProposal{{
		Source: bike.Key, SourceThing: "my bike",
		Target: wheels.Key, TargetThing: "the two wheels that carry me to work",
		Score: 0.97, MatchTier: 3, Band: observations.GratitudeBandHigh, WillApply: false,
		Command: fmt.Sprintf("lucid gratitude merge %s %s", bike.Key, wheels.Key),
	}}, res.View.Proposals)
	assert.Contains(t, strings.Join(res.Lines, "\n"), "— by meaning, tier 3 (0.97)")
	assert.Equal(t, before, gratitudeFiles(t, r), "a dry run writes nothing")

	req := fake.Requests[0]
	assert.Equal(t, gratitudeReconcileIntent, req.Intent)
	assert.Equal(t, gratitudeReconcileSystem, req.System)
	require.Len(t, req.Messages, 1)
	dec := json.NewDecoder(strings.NewReader(req.Messages[0].Content))
	dec.DisallowUnknownFields()
	var in gratitudeReconcileInput
	require.NoError(t, dec.Decode(&in), "the message holds only the numbered wordings")
	require.Len(t, in.Items, 3, "every live entry is on the slate")
	for i, it := range in.Items {
		assert.Equal(t, i+1, it.N)
	}
	for _, leak := range []string{wheels.Key, bike.Key, "grat_", "2026", `"count"`} {
		assert.NotContainsf(t, req.Messages[0].Content, leak, "no %q leaves the process", leak)
	}

	unused := &provider.Fake{}
	applied, err := r.ReconcileGratitude(t.Context(), ReconcileGratitudeRequest{Apply: true, Provider: unused, Now: day3()})
	require.NoError(t, err)
	assert.Zero(t, unused.Calls(), "an apply never consults the model")
	assert.Empty(t, applied.View.Applied, "a tier-3 pair is never applied")
	assert.Empty(t, applied.View.Proposals, "an apply lists the deterministic tier-2 set only")
	assert.Equal(t, before, gratitudeFiles(t, r))
}

// TestGratitudeReconcileTier3Degrades: tier 3 is never load-bearing in reconcile
// either (gratitude.md §7.6, §7.9). Disabled, unreachable, failing, timing out,
// or answering outside the contract, the pass still completes with the tier-2
// proposals, reports why tier 3 added nothing, and writes nothing. A fenced but
// otherwise valid reply is trusted. Only the caller's own cancellation aborts.
func TestGratitudeReconcileTier3Degrades(t *testing.T) {
	setup := func(t *testing.T, mutate func(*config.GratitudeMatchConfig)) (*Router, GratitudeWriteResult, GratitudeWriteResult) {
		t.Helper()
		r := bootedGratitude(t)
		withTier3(r, mutate)
		seed := seedReconcileTally(t, r)
		return r, seed.coffee, seed.coffees
	}
	requireTier2Only := func(t *testing.T, res GratitudeReconcileResult, coffee, coffees GratitudeWriteResult) {
		t.Helper()
		require.Len(t, res.View.Proposals, 2, "the tier-2 proposals stand")
		assert.Equal(t, coffees.Key, res.View.Proposals[0].Source)
		assert.Equal(t, coffee.Key, res.View.Proposals[0].Target)
		for _, p := range res.View.Proposals {
			assert.Equal(t, 2, p.MatchTier, "no tier-3 pair was trusted")
		}
	}

	t.Run("disabled", func(t *testing.T) {
		r, coffee, coffees := setup(t, func(m *config.GratitudeMatchConfig) { m.Tier3Enabled = false })
		fake := &provider.Fake{}
		res, err := r.ReconcileGratitude(t.Context(), ReconcileGratitudeRequest{Provider: fake})
		require.NoError(t, err)
		assert.Zero(t, fake.Calls(), "a disabled judge is never called")
		assert.Equal(t, GratitudeTier3Disabled, res.View.Tier3)
		assert.Contains(t, strings.Join(res.Lines, "\n"), "Meaning match is off — proposed by wording only.")
		requireTier2Only(t, res, coffee, coffees)
	})

	t.Run("no judge reachable", func(t *testing.T) {
		r, coffee, coffees := setup(t, func(*config.GratitudeMatchConfig) {})
		res, err := r.ReconcileGratitude(t.Context(), ReconcileGratitudeRequest{Provider: nil})
		require.NoError(t, err)
		assert.Equal(t, GratitudeTier3Unavailable, res.View.Tier3)
		assert.Contains(t, strings.Join(res.Lines, "\n"), "Meaning match unavailable — proposed by wording only.")
		requireTier2Only(t, res, coffee, coffees)
	})

	replies := map[string]provider.Exchange{
		"unavailable":         {Err: fmt.Errorf("dial: %w", provider.ErrUnavailable)},
		"timeout":             {Err: fmt.Errorf("slow: %w", provider.ErrTimeout)},
		"any other error":     {Err: errors.New("boom")},
		"prose":               {Content: `The first and second items look the same.`},
		"thinking preamble":   {Content: "Let me think.\n{\"pairs\": []}"},
		"no pairs list":       {Content: `{"matches": []}`},
		"unknown position":    {Content: `{"pairs": [{"a": 1, "b": 99, "score": 0.9}]}`},
		"an item with itself": {Content: `{"pairs": [{"a": 2, "b": 2, "score": 0.9}]}`},
		"a repeated pair":     {Content: `{"pairs": [{"a": 1, "b": 2, "score": 0.9}, {"a": 2, "b": 1, "score": 0.8}]}`},
		"score out of range":  {Content: `{"pairs": [{"a": 1, "b": 2, "score": 1.5}]}`},
		"a missing score":     {Content: `{"pairs": [{"a": 1, "b": 2}]}`},
		"a missing position":  {Content: `{"pairs": [{"a": 1, "score": 0.9}]}`},
	}
	for name, reply := range replies {
		t.Run(name, func(t *testing.T) {
			r, coffee, coffees := setup(t, func(*config.GratitudeMatchConfig) {})
			before := gratitudeFiles(t, r)
			fake := &provider.Fake{Script: []provider.Exchange{reply}}
			res, err := r.ReconcileGratitude(t.Context(), ReconcileGratitudeRequest{Provider: fake})
			require.NoError(t, err, "a model outage is never a reconcile failure")
			assert.Equal(t, 1, fake.Calls())
			assert.Equal(t, GratitudeTier3Unavailable, res.View.Tier3)
			requireTier2Only(t, res, coffee, coffees)
			assert.Equal(t, before, gratitudeFiles(t, r))
		})
	}

	t.Run("a fenced reply is trusted", func(t *testing.T) {
		r, _, _ := setup(t, func(*config.GratitudeMatchConfig) {})
		dog, _, _ := r.store.ReadGratitude(mustKey(t, r, "my dog"))
		dad, _, _ := r.store.ReadGratitude(mustKey(t, r, "my dad"))
		inner := reconcileReply(t, r, scoredPair(dog.Key, dad.Key, 0.2)).Content
		fake := &provider.Fake{Script: []provider.Exchange{{Content: "```json\n" + inner + "\n```"}}}
		res, err := r.ReconcileGratitude(t.Context(), ReconcileGratitudeRequest{Provider: fake})
		require.NoError(t, err)
		assert.Equal(t, GratitudeTier3Used, res.View.Tier3)
		for _, p := range res.View.Proposals {
			assert.NotEqual(t, 3, p.MatchTier, "a pair below the floor is never proposed")
		}
	})

	t.Run("cancellation aborts, nothing changed", func(t *testing.T) {
		r, _, _ := setup(t, func(*config.GratitudeMatchConfig) {})
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		fake := &provider.Fake{Script: []provider.Exchange{{Content: `{"pairs": []}`}}}
		_, err := r.ReconcileGratitude(ctx, ReconcileGratitudeRequest{Provider: fake, Now: time.Now()})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "canceled; nothing was changed")
	})
}

// mustKey resolves a phrase's canonical key and fails the test on error.
func mustKey(t *testing.T, r *Router, phrase string) string {
	t.Helper()
	key, err := r.store.ResolveGratitudeKey(phrase)
	require.NoError(t, err)
	return key
}

// TestGratitudeReconcileApplyStopsCleanly: a fold that cannot land stops the
// apply with a clean error naming how far it got, never a silent partial.
func TestGratitudeReconcileApplyStopsCleanly(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
	r := bootedGratitude(t)
	coffee := seedReconcileTally(t, r).coffee
	// A read-only gratitude tree: the proposals are read fine, but the fold's
	// first write (the target's merge event) cannot land.
	path := filepath.Join(r.store.Home(), "registries", "gratitude", coffee.Key+".json")
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.Chmod(filepath.Dir(path), 0o500))
	t.Cleanup(func() { _ = os.Chmod(filepath.Dir(path), 0o700) })

	_, err = r.ReconcileGratitude(t.Context(), ReconcileGratitudeRequest{Apply: true, Now: day3()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gratitude reconcile stopped after 0 of 1 folds")

	require.NoError(t, os.Chmod(filepath.Dir(path), 0o700))
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(content), string(after), "the failed fold changed nothing")
}
