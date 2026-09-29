package router

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/provider"
)

// gratmatch_test.go covers the gratitude match pipeline (gratmatch.go;
// gratitude.md §7.1–§7.6): the tier-1 fast path, the single-hop tombstone
// forward, the tier-2 token match and its configured bands, the ambiguous band's
// write-nothing suggestion, and the optional tier-3 judge — its request, its
// bands and fallback, and its P9 degradation. Every tier-3 test drives the judge
// with provider.Fake, so none needs a live model or the network (ADR-0006).
// Every phrase here is synthetic.

// corruptGratitudeFile plants an unreadable gratitude record at a key no phrase
// can derive (a derived key has a single initial before the hyphen), so any path
// that scans the whole tally fails loudly while a single-key read never sees it.
func corruptGratitudeFile(t *testing.T, r *Router) string {
	t.Helper()
	path := filepath.Join(r.store.Home(), "registries", "gratitude", "gratitude_zz-corrupt.json")
	require.NoError(t, os.WriteFile(path, []byte("{bad"), 0o600))
	return path
}

// gratitudeEntry reads one stored entry and fails the test if it is missing.
func gratitudeEntry(t *testing.T, r *Router, key string) observations.GratitudeEntry {
	t.Helper()
	e, found, err := r.store.ReadGratitude(key)
	require.NoError(t, err)
	require.True(t, found, "entry %q exists", key)
	return e
}

// TestGratitudeTier1FastPath: an exact canonical-key hit bumps immediately with
// the v1 behavior and output byte-for-byte (gratitude.md §7.1) — the same entry,
// the display refreshed to tonight's wording, a v1-shaped occurrence with no
// match attribution, and the v1 ack — and it never reaches tier 2. The proof is
// a corrupt record planted in the tally: tier 2 must read every live entry, so a
// path that touched it would fail, and the tier-1 bump succeeds regardless while
// a phrase that misses tier 1 trips over it. Because tier 3 (the only model step)
// can only follow tier 2, a path that never reaches tier 2 makes zero model calls.
func TestGratitudeTier1FastPath(t *testing.T) {
	r := bootedGratitude(t)
	first := addGratitude(t, r, "my morning coffee", day1())
	corrupt := corruptGratitudeFile(t, r)

	dec, err := r.matchGratitude(t.Context(), "My Morning Coffee!", false, nil)
	require.NoError(t, err)
	assert.Equal(t, gratitudeMatchDecision{
		Action: gratitudeMatchBump, Key: first.Key, Tier: 1, MakePrimary: true,
	}, dec, "a tier-1 hit is decided from the one keyed record")

	res, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "My Morning Coffee!", Now: day2()})
	require.NoError(t, err, "the fast path never scans the tally, so the corrupt record is never read")
	assert.Equal(t, first.Key, res.Key)
	assert.False(t, res.Created)
	assert.Equal(t, 2, res.Count)
	assert.Zero(t, res.MatchTier, "a tier-1 bump carries no match attribution")
	assert.Zero(t, res.MatchScore)
	assert.Equal(t,
		fmt.Sprintf("Tallied %q (×%d) as `%s`.", "My Morning Coffee!", 2, res.Receipt),
		res.Ack, "the tier-1 ack is the v1 ack, byte for byte")

	entry := gratitudeEntry(t, r, first.Key)
	assert.Equal(t, "My Morning Coffee!", entry.DisplayName, "a tier-1 hit refreshes the display, as v1 did")
	require.Len(t, entry.History, 2)
	assert.Equal(t, observations.GratitudeEvent{
		ID:     res.Receipt,
		At:     entry.History[1].At,
		Type:   observations.GratitudeEventOccurrence,
		Date:   observations.DateString(observations.DateOf(day2())),
		Source: observations.GratitudeSourceGratitude,
	}, entry.History[1], "the occurrence is exactly the v1 shape")

	// Contrast: a phrase that misses tier 1 must scan the tally, and trips over
	// the corrupt record — nothing is written.
	_, err = r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "clean drinking water", Now: day2()})
	require.Error(t, err, "tier 2 reads every live entry")
	assert.Contains(t, err.Error(), "could not read the gratitude tally")
	assert.Contains(t, err.Error(), "nothing was saved")

	require.NoError(t, os.Remove(corrupt))
	list, err := r.GratitudeList()
	require.NoError(t, err)
	assert.Equal(t, 1, list.View.Count, "the refused tier-2 add wrote nothing")
}

// TestGratitudeTier1ForwardsThroughTombstone: a phrase whose canonical key names
// a merge tombstone lands on the tombstone's live destination (gratitude.md §7.1,
// ADR-0012 §11) — the wording joins the destination's aka[], its display stays,
// and the tombstone stays a tombstone — instead of refusing as v1 did.
func TestGratitudeTier1ForwardsThroughTombstone(t *testing.T) {
	r := bootedGratitude(t)
	roof := addGratitude(t, r, "a roof over my head", day1())
	house := addGratitude(t, r, "my house", day1())
	require.NotEqual(t, roof.Key, house.Key, "the two wordings share no word, so they start apart")
	_, err := r.MergeGratitude(GratitudeMergeRequest{Source: house.Key, Target: roof.Key, Now: day2()})
	require.NoError(t, err)

	dec, err := r.matchGratitude(t.Context(), "my house", false, nil)
	require.NoError(t, err)
	assert.Equal(t, gratitudeMatchBump, dec.Action)
	assert.Equal(t, roof.Key, dec.Key, "the tombstone key forwards its single hop")
	assert.Equal(t, 1, dec.Tier)
	assert.Equal(t, house.Key, dec.ForwardedFrom)
	assert.False(t, dec.MakePrimary, "a forwarded wording never replaces the destination's display")

	res, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "My House", Now: day3()})
	require.NoError(t, err)
	assert.Equal(t, roof.Key, res.Key)
	assert.Equal(t, 3, res.Count, "the destination's own, the merged, and tonight's occurrence")
	assert.False(t, res.Created)
	assert.Zero(t, res.MatchTier, "tier 1 carries no match attribution")

	dst := gratitudeEntry(t, r, roof.Key)
	assert.Equal(t, "a roof over my head", dst.DisplayName)
	assert.Contains(t, dst.Aka, "My House")
	assert.True(t, gratitudeEntry(t, r, house.Key).IsTombstone(), "the tombstone is never revived")

	list, err := r.GratitudeList()
	require.NoError(t, err)
	assert.Equal(t, 1, list.View.Count)
}

// TestGratitudeTier2AutoBump: a phrase that misses tier 1 but is a clear tier-2
// winner (High band: over the cutoff, by the margin) is bumped automatically
// (gratitude.md §7.4) — the canonical display is kept, tonight's wording joins
// aka[], the result is attributed to tier 2 with its score, and the ack says how
// it matched so the step is legible and correctable.
func TestGratitudeTier2AutoBump(t *testing.T) {
	r := bootedGratitude(t)
	walk := addGratitude(t, r, "a morning walk by a river", day1())
	addGratitude(t, r, "a quiet river", day1()) // a weak runner-up

	res, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "the morning walks by the river", Now: day2()})
	require.NoError(t, err)
	assert.Equal(t, walk.Key, res.Key)
	assert.False(t, res.Created)
	assert.Equal(t, 2, res.Count)
	assert.Equal(t, 2, res.MatchTier)
	assert.InDelta(t, 1.0, res.MatchScore, 1e-9)
	assert.Equal(t,
		fmt.Sprintf("Tallied %q (×2) as `%s` — matched %q by wording (tier 2).",
			"a morning walk by a river", res.Receipt, "the morning walks by the river"),
		res.Ack)

	entry := gratitudeEntry(t, r, walk.Key)
	assert.Equal(t, "a morning walk by a river", entry.DisplayName, "an automatic bump keeps the canonical display")
	assert.Contains(t, entry.Aka, "the morning walks by the river", "tonight's wording joins aka[] as match evidence")

	list, err := r.GratitudeList()
	require.NoError(t, err)
	assert.Equal(t, 2, list.View.Count, "no near-duplicate row was created")
}

// TestGratitudeTier2AmbiguousSuggestsWritesNothing: an ambiguous-band phrase (an
// exact tie between two entries) neither merges nor creates (gratitude.md §7.4):
// it writes nothing and returns a GratitudeSuggestionError carrying the band, the
// tier, and both candidates, whose sentence names them and the two resolutions.
// Re-running with --into bumps the chosen entry; re-running with --new creates.
func TestGratitudeTier2AmbiguousSuggestsWritesNothing(t *testing.T) {
	setup := func(t *testing.T) (*Router, GratitudeWriteResult, GratitudeWriteResult) {
		t.Helper()
		r := bootedGratitude(t)
		return r, addGratitude(t, r, "the walk to work", day1()), addGratitude(t, r, "a quiet home", day1())
	}

	r, work, home := setup(t)
	_, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "the walk home", Now: day2()})
	require.Error(t, err)
	var sugg *GratitudeSuggestionError
	require.ErrorAs(t, err, &sugg)
	assert.Equal(t, "the walk home", sugg.Thing)
	assert.Equal(t, observations.GratitudeBandAmbiguous, sugg.Band)
	assert.Equal(t, 2, sugg.MatchTier)
	require.Len(t, sugg.Candidates, 2)
	ids := []string{sugg.Candidates[0].ID, sugg.Candidates[1].ID}
	assert.ElementsMatch(t, []string{work.Key, home.Key}, ids)
	for _, c := range sugg.Candidates {
		assert.InDelta(t, 0.5, c.Score, 1e-9)
	}
	msg := err.Error()
	for _, want := range []string{work.Key, home.Key, "--into <id>", "--new", "nothing was saved"} {
		assert.Contains(t, msg, want)
	}

	// Nothing was written: no new entry, and neither candidate moved.
	key, kerr := r.store.ResolveGratitudeKey("the walk home")
	require.NoError(t, kerr)
	_, found, rerr := r.store.ReadGratitude(key)
	require.NoError(t, rerr)
	assert.False(t, found, "an ambiguous phrase never silently creates")
	list, err := r.GratitudeList()
	require.NoError(t, err)
	require.Equal(t, 2, list.View.Count)
	for _, e := range list.View.Entries {
		assert.Equal(t, 1, e.Count, "an ambiguous phrase never silently merges")
	}

	t.Run("--into resolves to the chosen entry", func(t *testing.T) {
		r, work, _ := setup(t)
		res, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "the walk home", Into: work.Key, Now: day2()})
		require.NoError(t, err)
		assert.Equal(t, work.Key, res.Key)
		assert.Equal(t, 2, res.Count)
		assert.Zero(t, res.MatchTier, "a human-chosen bump carries no match attribution")
	})

	t.Run("--new resolves to a new entry", func(t *testing.T) {
		r, work, home := setup(t)
		res, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "the walk home", ForceNew: true, Now: day2()})
		require.NoError(t, err)
		assert.True(t, res.Created)
		assert.NotEqual(t, work.Key, res.Key)
		assert.NotEqual(t, home.Key, res.Key)
		list, err := r.GratitudeList()
		require.NoError(t, err)
		assert.Equal(t, 3, list.View.Count)
	})
}

// TestGratitudeTier2NearMissCreates: a look-alike that shares no word — "my dog"
// against a stored "my dad" — is Low band and starts its own entry; tier 2 has no
// edit distance to pair them.
func TestGratitudeTier2NearMissCreates(t *testing.T) {
	r := bootedGratitude(t)
	dad := addGratitude(t, r, "my dad", day1())

	dog, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "my dog", Now: day2()})
	require.NoError(t, err)
	assert.True(t, dog.Created)
	assert.NotEqual(t, dad.Key, dog.Key)
	assert.Equal(t, 1, dog.Count)
	assert.Equal(t, 1, gratitudeEntry(t, r, dad.Key).Tally().Count, "the look-alike was not bumped")
}

// TestGratitudeForceNew: `--new` skips the tier-2 match — a phrase that would
// auto-bump by wording starts its own entry instead — but tier 1 still applies,
// since a phrase that normalizes equal to a live key IS that entry (gratitude.md
// §3). `--new` with `--into` is a contradiction and a clean error.
func TestGratitudeForceNew(t *testing.T) {
	r := bootedGratitude(t)
	coffee := addGratitude(t, r, "morning coffee", day1())

	dec, err := r.matchGratitude(t.Context(), "coffee in the morning", false, nil)
	require.NoError(t, err)
	require.Equal(t, gratitudeMatchBump, dec.Action, "without --new the wording would auto-bump")

	fresh, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "coffee in the morning", ForceNew: true, Now: day2()})
	require.NoError(t, err)
	assert.True(t, fresh.Created, "--new skips tier 2 and creates")
	assert.NotEqual(t, coffee.Key, fresh.Key)

	same, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "Morning coffee!", ForceNew: true, Now: day2()})
	require.NoError(t, err)
	assert.Equal(t, coffee.Key, same.Key, "tier 1 still applies under --new")
	assert.Equal(t, 2, same.Count)

	_, err = r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "morning coffee", Into: coffee.Key, ForceNew: true, Now: day3()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--into and --new cannot be combined")
	assert.Equal(t, 2, gratitudeEntry(t, r, coffee.Key).Tally().Count, "the refused add wrote nothing")
}

// TestGratitudeBands: the tier-2 decision follows the configured gratitude.match
// bands (gratitude.md §7.2) — the same phrase and tally land High, Ambiguous, or
// Low as tier2_high, tier2_margin, and ambiguous_floor move. A single 0.8 match is
// a suggestion under the documented defaults, an automatic bump once the high
// cutoff drops below it, and a create once the floor rises above it; a strong
// top-1 with a close runner-up is a suggestion until the margin is loosened. A
// router that was never booted matches on the documented defaults.
func TestGratitudeBands(t *testing.T) {
	r := bootedGratitude(t)
	run := addGratitude(t, r, "a sunny morning run", day1())
	quiet := addGratitude(t, r, "a quiet evening walk along the river", day1())
	// Seeded with --new: as a plain add it is itself a clear tier-2 winner (8/9,
	// no runner-up) and would auto-bump the entry above rather than start its own.
	_, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "an evening walk along the river", ForceNew: true, Now: day1()})
	require.NoError(t, err)

	decide := func(m config.GratitudeMatchConfig, phrase string) gratitudeMatchDecision {
		t.Helper()
		r.cfg.Gratitude.Match = m
		dec, merr := r.matchGratitude(t.Context(), phrase, false, nil)
		require.NoError(t, merr)
		return dec
	}
	defaults := config.DefaultGratitudeMatch()

	// Plausible single match, score 0.8.
	dec := decide(defaults, "sunny morning")
	assert.Equal(t, gratitudeMatchSuggest, dec.Action, "0.8 is below the 0.85 cutoff: suggest")
	assert.Equal(t, observations.GratitudeBandAmbiguous, dec.Band)
	require.Len(t, dec.Candidates, 1)
	assert.Equal(t, run.Key, dec.Candidates[0].Key)

	lowHigh := defaults
	lowHigh.Tier2High = 0.75
	dec = decide(lowHigh, "sunny morning")
	assert.Equal(t, gratitudeMatchBump, dec.Action, "over a lowered cutoff with no runner-up: auto-bump")
	assert.Equal(t, observations.GratitudeBandHigh, dec.Band)
	assert.Equal(t, run.Key, dec.Key)
	assert.Equal(t, 2, dec.Tier)
	assert.InDelta(t, 0.8, dec.Score, 1e-9)

	highFloor := defaults
	highFloor.AmbiguousFloor, highFloor.Tier2High = 0.9, 0.95
	dec = decide(highFloor, "sunny morning")
	assert.Equal(t, gratitudeMatchCreate, dec.Action, "below a raised floor: create")
	assert.Equal(t, observations.GratitudeBandLow, dec.Band)

	// Near-tie: top-1 1.0, runner-up 8/9 — a margin of ~0.11.
	dec = decide(defaults, "quiet evening walk along river")
	assert.Equal(t, gratitudeMatchSuggest, dec.Action, "a close runner-up under the 0.15 margin: suggest")
	assert.Len(t, dec.Candidates, 2)

	looseMargin := defaults
	looseMargin.Tier2Margin = 0.1
	dec = decide(looseMargin, "quiet evening walk along river")
	assert.Equal(t, gratitudeMatchBump, dec.Action, "a loosened margin makes the same top-1 a clear winner")
	assert.Equal(t, quiet.Key, dec.Key)

	// An unbooted router (zero config) matches on the documented defaults, never
	// on zero cutoffs that would auto-bump every overlap.
	r.cfg = config.Config{}
	dec, err = r.matchGratitude(t.Context(), "sunny morning", false, nil)
	require.NoError(t, err)
	assert.Equal(t, gratitudeMatchSuggest, dec.Action)

	// The pipeline decides; it never writes.
	list, err := r.GratitudeList()
	require.NoError(t, err)
	assert.Equal(t, 3, list.View.Count)
	for _, e := range list.View.Entries {
		assert.Equal(t, 1, e.Count)
	}

	t.Run("each tier is banded by its own configured cutoffs", testGratitudeBandsPerTier)
}

// testGratitudeBandsPerTier drives the band step itself — the configured cutoffs
// ([gratitudeTierCutoffs]) through the one pure rule into a decision — over
// ranked score lists for each tier under the documented defaults (tier 2: 0.85
// with a 0.15 lead; tier 3: 0.90 with a 0.20 lead; a shared 0.50 floor). High
// needs the cutoff AND the margin; a near-tie or an exact tie is Ambiguous even
// when top-1 is strong; a tier-3 score that would clear tier 2's cutoff or margin
// but not tier 3's stricter one is Ambiguous; below the floor, or no candidate at
// all, is Low and creates at the phrase's own key.
func testGratitudeBandsPerTier(t *testing.T) {
	const key = "gratitude_n-phrase"
	keys := []string{"gratitude_a-river", "gratitude_b-stone", "gratitude_c-field", "gratitude_d-cedar"}
	defaults := config.DefaultGratitudeMatch()

	for _, tc := range []struct {
		name      string
		tier      int
		scores    []float64 // ranked best first, one per keys[i]
		band      observations.GratitudeBand
		suggested int // how many candidates an Ambiguous decision lists
	}{
		{name: "tier 2 clear single winner", tier: 2, scores: []float64{0.9}, band: observations.GratitudeBandHigh},
		{name: "tier 2 at the cutoff exactly", tier: 2, scores: []float64{0.85}, band: observations.GratitudeBandHigh},
		{name: "tier 2 winner by the margin", tier: 2, scores: []float64{1, 0.85}, band: observations.GratitudeBandHigh},
		{name: "tier 2 near-tie", tier: 2, scores: []float64{0.95, 0.85}, band: observations.GratitudeBandAmbiguous, suggested: 2},
		{name: "tier 2 exact tie", tier: 2, scores: []float64{0.5, 0.5}, band: observations.GratitudeBandAmbiguous, suggested: 2},
		{name: "tier 2 unconfident single", tier: 2, scores: []float64{0.8}, band: observations.GratitudeBandAmbiguous, suggested: 1},
		{name: "tier 2 at the floor", tier: 2, scores: []float64{0.5, 0.4}, band: observations.GratitudeBandAmbiguous, suggested: 1},
		{
			name: "tier 2 suggestion capped at three", tier: 2, scores: []float64{0.7, 0.7, 0.6, 0.55},
			band: observations.GratitudeBandAmbiguous, suggested: observations.GratitudeSuggestionLimit,
		},
		{name: "tier 2 below the floor", tier: 2, scores: []float64{0.4}, band: observations.GratitudeBandLow},
		{name: "tier 2 no candidate", tier: 2, scores: nil, band: observations.GratitudeBandLow},
		{name: "tier 3 clear single winner", tier: 3, scores: []float64{0.9}, band: observations.GratitudeBandHigh},
		{name: "tier 3 winner by its margin", tier: 3, scores: []float64{0.95, 0.75}, band: observations.GratitudeBandHigh},
		{name: "tier 3 under its own cutoff", tier: 3, scores: []float64{0.88}, band: observations.GratitudeBandAmbiguous, suggested: 1},
		{name: "tier 3 under its own margin", tier: 3, scores: []float64{0.97, 0.8}, band: observations.GratitudeBandAmbiguous, suggested: 2},
		{name: "tier 3 near-tie", tier: 3, scores: []float64{0.93, 0.88}, band: observations.GratitudeBandAmbiguous, suggested: 2},
		{name: "tier 3 below the floor", tier: 3, scores: []float64{0.3}, band: observations.GratitudeBandLow},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cands := make([]observations.GratitudeCandidate, 0, len(tc.scores))
			for i, s := range tc.scores {
				cands = append(cands, observations.GratitudeCandidate{Key: keys[i], Thing: "wording " + keys[i], Score: s})
			}
			dec, err := bandGratitudeDecision(key, tc.tier, cands, gratitudeTierCutoffs(defaults, tc.tier))
			require.NoError(t, err)
			assert.Equal(t, tc.band, dec.Band)
			assert.Equal(t, tc.tier, dec.Tier)
			switch tc.band {
			case observations.GratitudeBandHigh:
				assert.Equal(t, gratitudeMatchBump, dec.Action)
				assert.Equal(t, keys[0], dec.Key, "High bumps the top-1 entry")
				assert.InDelta(t, tc.scores[0], dec.Score, 1e-9)
				assert.True(t, dec.automatic())
			case observations.GratitudeBandAmbiguous:
				assert.Equal(t, gratitudeMatchSuggest, dec.Action)
				assert.Empty(t, dec.Key, "a suggestion names no entry to write")
				require.Len(t, dec.Candidates, tc.suggested)
				assert.Equal(t, keys[0], dec.Candidates[0].Key, "best first")
			case observations.GratitudeBandLow:
				assert.Equal(t, gratitudeMatchCreate, dec.Action)
				assert.Equal(t, key, dec.Key, "Low creates at the phrase's own key")
			}
		})
	}
}

// TestGratitudeSuggestionError_Sentence pins the ambiguous-band sentence a
// non-interactive caller sees on stderr: each candidate's id, wording, and score,
// then how to resolve, then that nothing was saved.
func TestGratitudeSuggestionError_Sentence(t *testing.T) {
	err := error(&GratitudeSuggestionError{
		Thing: "the walk home", Band: observations.GratitudeBandAmbiguous, MatchTier: 2,
		Candidates: []GratitudeSuggestionCandidate{
			{ID: "gratitude_a-river", Thing: "the walk to work", Score: 0.5},
			{ID: "gratitude_b-stone", Thing: "a quiet home", Score: 0.5},
		},
	})
	assert.Equal(t,
		`"the walk home" is close to an entry you already keep: gratitude_a-river "the walk to work" (0.50), `+
			`gratitude_b-stone "a quiet home" (0.50) — re-run with --into <id> to bump one, or --new to start a new entry; nothing was saved`,
		err.Error())
	var target *GratitudeSuggestionError
	assert.ErrorAs(t, err, &target)
}

// judgeSlatePositions maps each live entry's key to its 1-based position in the
// slate the router sends the judge for phrase — computed by the production slate
// rule (off-limits-linked entries withheld), so a scripted reply names the entry
// a test intends.
func judgeSlatePositions(t *testing.T, r *Router, phrase string) map[string]int {
	t.Helper()
	live, err := r.liveGratitude()
	require.NoError(t, err)
	slate := gratitudeJudgeSlate(observations.Tier2(phrase, live), r.judgeableGratitude(live), r.gratitudeMatchConfig().Tier3MaxCandidates)
	pos := make(map[string]int, len(slate))
	for i, e := range slate {
		pos[e.Key] = i + 1
	}
	return pos
}

// judgeReply scripts a judge answer scoring the given entries (by key) at their
// slate positions for phrase, in position order.
func judgeReply(t *testing.T, r *Router, phrase string, scores map[string]float64) provider.Exchange {
	t.Helper()
	pos := judgeSlatePositions(t, r, phrase)
	type match struct {
		N     int     `json:"n"`
		Score float64 `json:"score"`
	}
	matches := make([]match, 0, len(scores))
	for key, score := range scores {
		n, ok := pos[key]
		require.Truef(t, ok, "entry %q is on the judge slate", key)
		matches = append(matches, match{N: n, Score: score})
	}
	slices.SortFunc(matches, func(x, y match) int { return x.N - y.N })
	b, err := json.Marshal(map[string][]match{"matches": matches})
	require.NoError(t, err)
	return provider.Exchange{Content: string(b)}
}

// judgeInput decodes the one user message a judge request carries, rejecting any
// field beyond the documented phrase + numbered wordings.
func judgeInput(t *testing.T, req provider.Request) gratitudeJudgeInput {
	t.Helper()
	require.Len(t, req.Messages, 1, "the judge receives exactly one message")
	assert.Equal(t, provider.RoleUser, req.Messages[0].Role)
	dec := json.NewDecoder(strings.NewReader(req.Messages[0].Content))
	dec.DisallowUnknownFields()
	var in gratitudeJudgeInput
	require.NoError(t, dec.Decode(&in))
	return in
}

// withTier3 opts the router in to the tier-3 judge for one test — the
// documented defaults with tier3_enabled set, as an operator enables it — then
// applies mutate.
func withTier3(r *Router, mutate func(*config.GratitudeMatchConfig)) {
	m := config.DefaultGratitudeMatch()
	m.Tier3Enabled = true
	mutate(&m)
	r.cfg.Gratitude.Match = m
}

// bootedTier3 is a booted gratitude router opted in to the tier-3 judge.
func bootedTier3(t *testing.T) *Router {
	t.Helper()
	r := bootedGratitude(t)
	withTier3(r, func(*config.GratitudeMatchConfig) {})
	return r
}

// TestGratitudeZeroOverlap: a phrase that shares no word with the entry it names
// — "my bike" against a stored "the two wheels that carry me to work" — is out of
// tier 2's reach (no candidate at all, so alone it would create a duplicate), but
// tier 3 judges it against the whole live list, not tier 2's lexical shortlist,
// and a clear by-meaning winner is bumped automatically (gratitude.md §7.1, §7.4).
// The wording joins aka[], so the next night the same phrase is an exact tier-2
// hit and the model is not consulted again.
func TestGratitudeZeroOverlap(t *testing.T) {
	r := bootedTier3(t)
	bike := addGratitude(t, r, "the two wheels that carry me to work", day1())
	addGratitude(t, r, "clean drinking water from the tap", day2())

	live, err := r.liveGratitude()
	require.NoError(t, err)
	assert.Empty(t, observations.Tier2("my bike", live), "the two wordings share no token: tier 2 has no candidate")

	dec, err := r.matchGratitude(t.Context(), "my bike", false, nil)
	require.NoError(t, err)
	assert.Equal(t, gratitudeMatchCreate, dec.Action, "without the judge the phrase would start a duplicate")

	fake := &provider.Fake{Script: []provider.Exchange{judgeReply(t, r, "my bike", map[string]float64{bike.Key: 0.95})}}
	res, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "my bike", Provider: fake, Now: day3()})
	require.NoError(t, err)
	assert.Equal(t, 1, fake.Calls())
	assert.Equal(t, bike.Key, res.Key, "tier 3 reached the zero-overlap entry")
	assert.False(t, res.Created)
	assert.Equal(t, 2, res.Count)
	assert.Equal(t, 3, res.MatchTier)
	assert.InDelta(t, 0.95, res.MatchScore, 1e-9)
	assert.Equal(t, GratitudeTier3Used, res.Tier3)
	assert.Equal(t,
		fmt.Sprintf("Tallied %q (×2) as `%s` — matched %q by meaning (tier 3).",
			"the two wheels that carry me to work", res.Receipt, "my bike"),
		res.Ack)

	var carried bool
	for _, it := range judgeInput(t, fake.Requests[0]).Items {
		carried = carried || slices.Contains(it.Wordings, "the two wheels that carry me to work")
	}
	assert.True(t, carried, "the judge saw the entry tier 2 could not surface")

	entry := gratitudeEntry(t, r, bike.Key)
	assert.Equal(t, "the two wheels that carry me to work", entry.DisplayName, "the canonical display is kept")
	assert.Contains(t, entry.Aka, "my bike", "tonight's wording joins aka[]")

	again := &provider.Fake{}
	res, err = r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "my bike!", Provider: again, Now: day3()})
	require.NoError(t, err)
	assert.Equal(t, bike.Key, res.Key)
	assert.Equal(t, 2, res.MatchTier, "the learned wording is now an exact tier-2 hit")
	assert.Zero(t, again.Calls(), "a clear tier-2 winner never consults the model")
	assert.Empty(t, res.Tier3)
}

// TestGratitudeJudgePayloadMinimal: the judge is sent exactly the documented
// slice (gratitude.md §7.5) — the gratitude.match intent, the fixed instruction,
// and one user message holding only the new phrase and each candidate's
// wordings, numbered from 1 (entries tier 2 scored first, then most recently
// tallied). No entry key, receipt, count, date, or anything else from the Ledger
// travels: the message decodes into the two documented fields and nothing more.
func TestGratitudeJudgePayloadMinimal(t *testing.T) {
	r := bootedTier3(t)
	wheels := addGratitude(t, r, "the two wheels that carry me to work", day1())
	_, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "cycling to the office", Into: wheels.Key, Now: day1()})
	require.NoError(t, err)
	addGratitude(t, r, "clean drinking water from the tap", day2())
	addGratitude(t, r, "a quiet morning with a good book", day3())

	fake := &provider.Fake{Script: []provider.Exchange{{Content: `{"matches": []}`}}}
	res, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "my bike", Provider: fake, Now: day3()})
	require.NoError(t, err)
	assert.True(t, res.Created, "a judge that names no entry leaves the tier-2 Low create standing")
	assert.Equal(t, GratitudeTier3Used, res.Tier3)

	require.Len(t, fake.Requests, 1)
	req := fake.Requests[0]
	assert.Equal(t, "gratitude.match", req.Intent)
	assert.Equal(t, gratitudeJudgeSystem, req.System, "the instruction is fixed text, carrying no Ledger data")
	assert.Equal(t, gratitudeJudgeInput{
		Phrase: "my bike",
		Items: []gratitudeJudgeItem{
			{N: 1, Wordings: []string{"a quiet morning with a good book"}},
			{N: 2, Wordings: []string{"clean drinking water from the tap"}},
			{N: 3, Wordings: []string{"the two wheels that carry me to work", "cycling to the office"}},
		},
	}, judgeInput(t, req), "only the phrase and the wordings, most recently tallied first")

	body := req.Messages[0].Content
	for _, leak := range []string{"gratitude_", "grat_", "2026-", "count", "history", "source", "key"} {
		assert.NotContains(t, body, leak, "nothing but wordings leaves the process")
	}
	for _, leak := range []string{"gratitude_", "grat_", "2026-"} {
		assert.NotContains(t, req.System, leak)
	}
}

// TestGratitudeProviderDisabled: tier 3 is never load-bearing (gratitude.md
// §7.6, architecture P9). Switched off, unreachable (no provider), timing out, or
// failing, the add completes on tiers 1–2 with no error — a create stays a
// create, a tier-2 ambiguity stays a suggestion (never a silent create) — and the
// Tier3 status says why. Disabled makes no model call at all, and `list` never
// needs one.
func TestGratitudeProviderDisabled(t *testing.T) {
	seed := func(t *testing.T) *Router {
		t.Helper()
		r := bootedTier3(t)
		addGratitude(t, r, "the walk to work", day1())
		addGratitude(t, r, "a quiet home", day1())
		return r
	}
	answering := func() *provider.Fake {
		return &provider.Fake{Script: []provider.Exchange{{Content: `{"matches": [{"n": 1, "score": 1}]}`}}}
	}

	t.Run("off by default", func(t *testing.T) {
		r := bootedGratitude(t) // the shipped defaults: tier 3 is an opt-in
		addGratitude(t, r, "the walk to work", day1())
		fake := answering()
		res, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "fresh bread", Provider: fake, Now: day2()})
		require.NoError(t, err)
		assert.True(t, res.Created)
		assert.Equal(t, GratitudeTier3Disabled, res.Tier3)
		assert.Zero(t, fake.Calls(), "nothing is sent until tier 3 is enabled")
	})

	t.Run("disabled makes no model call", func(t *testing.T) {
		r := seed(t)
		withTier3(r, func(m *config.GratitudeMatchConfig) { m.Tier3Enabled = false })
		fake := answering()
		res, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "fresh bread", Provider: fake, Now: day2()})
		require.NoError(t, err)
		assert.True(t, res.Created)
		assert.Equal(t, GratitudeTier3Disabled, res.Tier3)
		assert.Zero(t, fake.Calls(), "a disabled judge is never called")

		_, err = r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "the walk home", Provider: fake, Now: day2()})
		var sugg *GratitudeSuggestionError
		require.ErrorAs(t, err, &sugg)
		assert.Equal(t, GratitudeTier3Disabled, sugg.Tier3)
		assert.Equal(t, 2, sugg.MatchTier, "the tier-2 suggestion stands")
		assert.Zero(t, fake.Calls())
	})

	t.Run("no provider is unavailable", func(t *testing.T) {
		r := seed(t)
		res, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "fresh bread", Now: day2()})
		require.NoError(t, err, "a missing model is never an add failure")
		assert.True(t, res.Created)
		assert.Equal(t, GratitudeTier3Unavailable, res.Tier3)
		assert.Equal(t, fmt.Sprintf("Started tally for %q (×1) as `%s`.", "fresh bread", res.Receipt), res.Ack)
	})

	for name, outage := range map[string]error{
		"timeout":     fmt.Errorf("judge: %w", provider.ErrTimeout),
		"unavailable": fmt.Errorf("judge: %w", provider.ErrUnavailable),
		"other error": fmt.Errorf("judge: exit status 1"),
	} {
		t.Run(name+" degrades to tiers 1-2", func(t *testing.T) {
			r := seed(t)
			fake := &provider.Fake{Script: []provider.Exchange{{Err: outage}, {Err: outage}}}
			res, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "fresh bread", Provider: fake, Now: day2()})
			require.NoError(t, err)
			assert.True(t, res.Created, "tier-2 Low still creates")
			assert.Equal(t, GratitudeTier3Unavailable, res.Tier3)
			assert.Zero(t, res.MatchTier)

			_, err = r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "the walk home", Provider: fake, Now: day2()})
			var sugg *GratitudeSuggestionError
			require.ErrorAs(t, err, &sugg, "a tier-2 ambiguity is still a suggestion, never a silent create")
			assert.Equal(t, GratitudeTier3Unavailable, sugg.Tier3)
			assert.Equal(t, 2, sugg.MatchTier)
			assert.Len(t, sugg.Candidates, 2)
			assert.Equal(t, 2, fake.Calls(), "each add tried the judge once")
		})
	}

	t.Run("list needs no model", func(t *testing.T) {
		r := seed(t)
		withTier3(r, func(m *config.GratitudeMatchConfig) { m.Tier3Enabled = false })
		list, err := r.GratitudeList()
		require.NoError(t, err)
		assert.Equal(t, 2, list.View.Count)
	})
}

// TestGratitudeTier3Bands: the judge's scores are banded with the tier-3 cutoffs
// (0.90 / margin 0.20 / floor 0.50 by default) and combined with tier 2 by the
// safer-outcome rule (gratitude.md §7.3): tier-3 High bumps the tier-3 winner,
// tier-3 Ambiguous (a near-tie, or a plausible-but-unconfident single score)
// suggests the tier-3 candidates, and tier-3 Low falls back to the tier-2 band —
// a tier-2 tie is still suggested, never silently created. A suggestion writes
// nothing.
func TestGratitudeTier3Bands(t *testing.T) {
	type outcome struct {
		action    gratitudeMatchAction
		tier      int
		winner    string // "work" / "home" for a bump
		suggested []string
	}
	for _, tc := range []struct {
		name   string
		phrase string // "the walk home" is a tier-2 tie; "my commute" has no tier-2 candidate
		scores map[string]float64
		want   outcome
	}{
		{
			name: "tier-3 High breaks a tier-2 tie", phrase: "the walk home",
			scores: map[string]float64{"work": 0.95, "home": 0.4},
			want:   outcome{action: gratitudeMatchBump, tier: 3, winner: "work"},
		},
		{
			name: "tier-3 High reaches a tier-2 Low", phrase: "my commute",
			scores: map[string]float64{"work": 0.92},
			want:   outcome{action: gratitudeMatchBump, tier: 3, winner: "work"},
		},
		{
			name: "tier-3 near-tie suggests", phrase: "my commute",
			scores: map[string]float64{"work": 0.93, "home": 0.88},
			want:   outcome{action: gratitudeMatchSuggest, tier: 3, suggested: []string{"work", "home"}},
		},
		{
			name: "tier-3 High without the margin suggests", phrase: "my commute",
			scores: map[string]float64{"work": 0.95, "home": 0.8},
			want:   outcome{action: gratitudeMatchSuggest, tier: 3, suggested: []string{"work", "home"}},
		},
		{
			name: "tier-3 unconfident single suggests", phrase: "my commute",
			scores: map[string]float64{"home": 0.7},
			want:   outcome{action: gratitudeMatchSuggest, tier: 3, suggested: []string{"home"}},
		},
		{
			name: "tier-3 Low keeps the tier-2 suggestion", phrase: "the walk home",
			scores: map[string]float64{},
			want:   outcome{action: gratitudeMatchSuggest, tier: 2, suggested: []string{"work", "home"}},
		},
		{
			name: "tier-3 below the floor keeps the tier-2 suggestion", phrase: "the walk home",
			scores: map[string]float64{"work": 0.3},
			want:   outcome{action: gratitudeMatchSuggest, tier: 2, suggested: []string{"work", "home"}},
		},
		{
			name: "tier-3 Low on a tier-2 Low creates", phrase: "my commute",
			scores: map[string]float64{},
			want:   outcome{action: gratitudeMatchCreate, tier: 2},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := bootedTier3(t)
			keys := map[string]string{
				"work": addGratitude(t, r, "the walk to work", day1()).Key,
				"home": addGratitude(t, r, "a quiet home", day1()).Key,
			}
			scores := make(map[string]float64, len(tc.scores))
			for name, s := range tc.scores {
				scores[keys[name]] = s
			}
			fake := &provider.Fake{Script: []provider.Exchange{judgeReply(t, r, tc.phrase, scores)}}

			dec, err := r.matchGratitude(t.Context(), tc.phrase, false, fake)
			require.NoError(t, err)
			assert.Equal(t, 1, fake.Calls())
			assert.Equal(t, tc.want.action, dec.Action)
			assert.Equal(t, tc.want.tier, dec.Tier)
			assert.Equal(t, GratitudeTier3Used, dec.Tier3)
			if tc.want.winner != "" {
				assert.Equal(t, keys[tc.want.winner], dec.Key)
				assert.Equal(t, observations.GratitudeBandHigh, dec.Band)
			}
			suggested := make([]string, 0, len(dec.Candidates))
			for _, c := range dec.Candidates {
				suggested = append(suggested, c.Key)
			}
			want := make([]string, 0, len(tc.want.suggested))
			for _, name := range tc.want.suggested {
				want = append(want, keys[name])
			}
			assert.ElementsMatch(t, want, suggested)
			if tc.want.tier == 3 && tc.want.action == gratitudeMatchSuggest {
				assert.Equal(t, keys[tc.want.suggested[0]], suggested[0], "tier-3 candidates are ranked best first")
			}

			list, err := r.GratitudeList()
			require.NoError(t, err)
			assert.Equal(t, 2, list.View.Count, "the pipeline decides; it never writes")
		})
	}
}

// TestGratitudeTier3MalformedReplyDegrades: a judge reply that does not parse or
// breaks the contract — no matches list, an unknown or repeated position, a
// missing field, a score outside [0, 1], prose or markup around the JSON — is
// never partially trusted (gratitude.md §7.5): it is treated as unavailable and
// the tier-2 decision stands.
func TestGratitudeTier3MalformedReplyDegrades(t *testing.T) {
	for name, reply := range map[string]string{
		"not json":           "the first one, probably",
		"prose then fence":   "Here you go:\n```json\n{\"matches\": [{\"n\": 1, \"score\": 0.95}]}\n```",
		"unclosed fence":     "```json\n{\"matches\": [{\"n\": 1, \"score\": 0.95}]}",
		"fence of prose":     "```\nthe first one\n```",
		"thinking preamble":  "<think>hmm</think>{\"matches\": [{\"n\": 1, \"score\": 0.95}]}",
		"no matches key":     `{"result": []}`,
		"null matches":       `{"matches": null}`,
		"position zero":      `{"matches": [{"n": 0, "score": 0.95}]}`,
		"position past list": `{"matches": [{"n": 3, "score": 0.95}]}`,
		"repeated position":  `{"matches": [{"n": 1, "score": 0.95}, {"n": 1, "score": 0.2}]}`,
		"missing score":      `{"matches": [{"n": 1}]}`,
		"missing position":   `{"matches": [{"score": 0.95}]}`,
		"score above one":    `{"matches": [{"n": 1, "score": 1.5}]}`,
		"negative score":     `{"matches": [{"n": 1, "score": -0.1}]}`,
		"empty reply":        "",
	} {
		t.Run(name, func(t *testing.T) {
			r := bootedTier3(t)
			addGratitude(t, r, "the walk to work", day1())
			addGratitude(t, r, "a quiet home", day1())
			fake := &provider.Fake{Script: []provider.Exchange{{Content: reply}}}

			res, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "fresh bread", Provider: fake, Now: day2()})
			require.NoError(t, err)
			assert.Equal(t, 1, fake.Calls())
			assert.True(t, res.Created, "the tier-2 Low create stands")
			assert.Zero(t, res.MatchTier, "an untrusted reply never lands a match")
			assert.Equal(t, GratitudeTier3Unavailable, res.Tier3)
		})
	}
}

// TestGratitudeTier3FencedReply: a reply that is exactly one markdown code fence
// around the JSON object — a habit of some hosted models even when told not to —
// is read as the object inside it (gratitude.md §7.5); the contract checks still
// apply to what is inside.
func TestGratitudeTier3FencedReply(t *testing.T) {
	for name, fence := range map[string]string{
		"json fence":  "```json\n%s\n```",
		"plain fence": "```\n%s\n```",
		"padded":      "\n  ```json\n%s\n```  \n",
	} {
		t.Run(name, func(t *testing.T) {
			r := bootedTier3(t)
			wheels := addGratitude(t, r, "the two wheels that carry me to work", day1())
			reply := judgeReply(t, r, "my bike", map[string]float64{wheels.Key: 0.95})
			fake := &provider.Fake{Script: []provider.Exchange{{Content: fmt.Sprintf(fence, reply.Content)}}}

			res, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "my bike", Provider: fake, Now: day2()})
			require.NoError(t, err)
			assert.Equal(t, wheels.Key, res.Key)
			assert.Equal(t, 3, res.MatchTier)
			assert.Equal(t, GratitudeTier3Used, res.Tier3)
		})
	}

	r := bootedTier3(t)
	addGratitude(t, r, "the two wheels that carry me to work", day1())
	fake := &provider.Fake{Script: []provider.Exchange{{Content: "```json\n{\"matches\": [{\"n\": 2, \"score\": 0.95}]}\n```"}}}
	res, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "my bike", Provider: fake, Now: day2()})
	require.NoError(t, err)
	assert.Equal(t, GratitudeTier3Unavailable, res.Tier3, "a fenced reply naming an unknown position is still untrusted")
	assert.True(t, res.Created)
}

// TestGratitudeTier3NotNeeded: the judge runs only when tiers 1–2 did not
// produce a confident landing (gratitude.md §7.3). A tier-1 hit, a clear tier-2
// winner, an `--into` bump, a `--new` create, and a first entry (nothing live to
// judge against) never call it, and report no tier-3 status — a v1-shaped add
// stays v1-shaped.
func TestGratitudeTier3NotNeeded(t *testing.T) {
	r := bootedTier3(t)
	judge := &provider.Fake{}
	add := func(req AddGratitudeRequest) GratitudeWriteResult {
		t.Helper()
		req.Provider, req.Now = judge, day2()
		res, err := r.AddGratitude(t.Context(), req)
		require.NoError(t, err)
		return res
	}

	first := add(AddGratitudeRequest{Thing: "a morning walk by a river"})
	assert.True(t, first.Created, "a first entry has nothing to be judged against")
	assert.Empty(t, first.Tier3)

	assert.Empty(t, add(AddGratitudeRequest{Thing: "A morning walk by a river!"}).Tier3, "tier 1")
	assert.Equal(t, 2, add(AddGratitudeRequest{Thing: "the morning walks by the river"}).MatchTier, "tier-2 High")
	assert.Empty(t, add(AddGratitudeRequest{Thing: "fresh bread", Into: first.Key}).Tier3, "--into")
	fresh := add(AddGratitudeRequest{Thing: "fresh bread", ForceNew: true})
	assert.True(t, fresh.Created, "--new")
	assert.Empty(t, fresh.Tier3)

	assert.Zero(t, judge.Calls(), "no confident-or-explicit landing consults the model")
}

// TestGratitudeTier3SlateCap: above tier3_max_candidates live entries, the one
// judge request carries the entries tier 2 scored first (best first), then the
// most recently tallied, up to the cap (gratitude.md §7.5) — so an old entry that
// shares a word still makes the cut over newer, unrelated ones.
func TestGratitudeTier3SlateCap(t *testing.T) {
	r := bootedTier3(t)
	addGratitude(t, r, "a long walk in the hills", day1())
	addGratitude(t, r, "clean drinking water from the tap", day1())
	addGratitude(t, r, "a quiet morning with a good book", day2())
	addGratitude(t, r, "fresh bread from the corner bakery", day3())
	withTier3(r, func(m *config.GratitudeMatchConfig) { m.Tier3MaxCandidates = 2 })

	fake := &provider.Fake{Script: []provider.Exchange{{Content: `{"matches": []}`}}}
	_, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "my evening walk", Provider: fake, Now: day3()})
	require.NoError(t, err)

	in := judgeInput(t, fake.Requests[0])
	require.Len(t, in.Items, 2, "the slate is capped")
	assert.Equal(t, []string{"a long walk in the hills"}, in.Items[0].Wordings, "the tier-2-scored entry comes first")
	assert.Equal(t, []string{"fresh bread from the corner bakery"}, in.Items[1].Wordings, "then the most recently tallied")
	assert.Equal(t, []int{1, 2}, []int{in.Items[0].N, in.Items[1].N})
}

// TestGratitudeTier3CanceledWritesNothing: a caller that cancels while the judge
// is thinking gets a clean error and nothing saved — cancellation is the
// caller's own choice, not a model outage to degrade past.
func TestGratitudeTier3CanceledWritesNothing(t *testing.T) {
	r := bootedTier3(t)
	addGratitude(t, r, "the walk to work", day1())

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	fake := &provider.Fake{Script: []provider.Exchange{{Content: `{"matches": []}`}}}
	_, err := r.AddGratitude(ctx, AddGratitudeRequest{Thing: "fresh bread", Provider: fake, Now: day2()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "canceled")
	assert.Contains(t, err.Error(), "nothing was saved")

	list, err := r.GratitudeList()
	require.NoError(t, err)
	assert.Equal(t, 1, list.View.Count)
}

// gratitudeSnapshot is the observable state of the tally a never-rule test pins:
// every live entry's count, display wording, and aka[] forms, keyed by id.
type gratitudeSnapshot map[string]struct {
	Count int
	Thing string
	Aka   []string
}

// snapshotGratitude reads the live tally into a [gratitudeSnapshot].
func snapshotGratitude(t *testing.T, r *Router) gratitudeSnapshot {
	t.Helper()
	list, err := r.GratitudeList()
	require.NoError(t, err)
	snap := make(gratitudeSnapshot, len(list.View.Entries))
	for _, e := range list.View.Entries {
		snap[e.ID] = struct {
			Count int
			Thing string
			Aka   []string
		}{Count: e.Count, Thing: e.Thing, Aka: e.Aka}
	}
	return snap
}

// requireGratitudeSuggestion asserts an add was refused in the ambiguous band —
// a [GratitudeSuggestionError] naming at least one live candidate — and that the
// tally is exactly as it was before: nothing merged into any entry, and no entry
// at the phrase's own canonical key.
func requireGratitudeSuggestion(t *testing.T, r *Router, err error, phrase string, before gratitudeSnapshot) *GratitudeSuggestionError {
	t.Helper()
	var sugg *GratitudeSuggestionError
	require.ErrorAs(t, err, &sugg, "the ambiguous band surfaces a suggestion")
	assert.Equal(t, observations.GratitudeBandAmbiguous, sugg.Band)
	require.NotEmpty(t, sugg.Candidates, "the suggestion names what the phrase is close to")
	for _, c := range sugg.Candidates {
		assert.Contains(t, before, c.ID, "every candidate is a live entry")
	}
	assert.Equal(t, before, snapshotGratitude(t, r), "nothing was merged, bumped, or created")

	key, kerr := r.store.ResolveGratitudeKey(phrase)
	require.NoError(t, kerr)
	_, found, rerr := r.store.ReadGratitude(key)
	require.NoError(t, rerr)
	assert.False(t, found, "no entry was started at the phrase's own key")
	return sugg
}

// TestGratitudeNeverAutoMergeDifferentMeaning is the first "never" rule
// (gratitude.md §7.2, §7.4; ADR-0012 §4): a look-alike that names a different
// thing — "morning tea" beside a stored "my morning coffee" — is never folded
// into it automatically, however high the similarity, while the evidence is in
// the ambiguous band. Each case is a scored judge reply that a weaker rule would
// auto-merge: a strong near-tie (dropping the margin would bump), a single score
// that clears tier 2's cutoff but not tier 3's stricter one (banding tier 3 with
// tier 2's numbers would bump), and a strong top-1 whose lead meets tier 2's
// margin but not tier 3's. The tier-2 word overlap alone (0.5, the floor) is
// ambiguous too. Every one refuses with a suggestion and writes nothing.
func TestGratitudeNeverAutoMergeDifferentMeaning(t *testing.T) {
	const phrase = "morning tea"
	for _, tc := range []struct {
		name   string
		tier3  bool
		scores map[string]float64 // by entry name: "coffee" / "kettle"
		tier   int                // the tier whose band produced the suggestion
	}{
		{name: "strong near-tie", tier3: true, scores: map[string]float64{"coffee": 0.95, "kettle": 0.93}, tier: 3},
		{name: "clears tier 2's cutoff, not tier 3's", tier3: true, scores: map[string]float64{"coffee": 0.89}, tier: 3},
		{name: "tier 2's margin, not tier 3's", tier3: true, scores: map[string]float64{"coffee": 0.98, "kettle": 0.8}, tier: 3},
		{name: "word overlap alone, judge off", tier3: false, tier: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := bootedGratitude(t)
			withTier3(r, func(m *config.GratitudeMatchConfig) { m.Tier3Enabled = tc.tier3 })
			keys := map[string]string{
				"coffee": addGratitude(t, r, "my morning coffee", day1()).Key,
				"kettle": addGratitude(t, r, "the old kettle on the stove", day1()).Key,
			}
			before := snapshotGratitude(t, r)

			fake := &provider.Fake{}
			if tc.tier3 {
				scores := make(map[string]float64, len(tc.scores))
				for name, s := range tc.scores {
					scores[keys[name]] = s
				}
				fake.Script = []provider.Exchange{judgeReply(t, r, phrase, scores)}
			}

			res, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: phrase, Provider: fake, Now: day2()})
			assert.Empty(t, res.Receipt, "no event was appended")
			sugg := requireGratitudeSuggestion(t, r, err, phrase, before)
			assert.Equal(t, tc.tier, sugg.MatchTier)
			assert.Equal(t, keys["coffee"], sugg.Candidates[0].ID, "the look-alike is offered, not taken")
			if tc.tier3 {
				assert.Equal(t, 1, fake.Calls())
				assert.Equal(t, GratitudeTier3Used, sugg.Tier3)
			} else {
				assert.Zero(t, fake.Calls())
				assert.Equal(t, GratitudeTier3Disabled, sugg.Tier3)
			}
			assert.NotContains(t, gratitudeEntry(t, r, keys["coffee"]).Aka, phrase, "the wording was not recorded as evidence")
		})
	}
}

// TestGratitudeNeverSilentCreateInAmbiguous is the second "never" rule
// (gratitude.md §7.3–§7.4; ADR-0012 §4): a phrase whose evidence lands in the
// ambiguous band never quietly starts a new row — the suggestion is surfaced
// instead — on every path where a create would be the easy answer: a tier-2 tie
// with the judge switched off, unreachable, timing out, or answering "no match"
// (tier-3 Low falls back to the tier-2 suggestion, never to a create); a
// plausible-but-unconfident single tier-2 match; and a phrase tier 2 cannot reach
// at all that the judge finds only plausibly close. Only an explicit `--new`
// then creates.
func TestGratitudeNeverSilentCreateInAmbiguous(t *testing.T) {
	for _, tc := range []struct {
		name   string
		phrase string
		tier3  bool
		judge  func(t *testing.T, r *Router, keys map[string]string) provider.Provider
		tier   int
	}{
		{name: "tier-2 tie, judge off", phrase: "the walk home", tier: 2},
		{
			name: "tier-2 tie, no judge reachable", phrase: "the walk home", tier3: true, tier: 2,
			judge: func(*testing.T, *Router, map[string]string) provider.Provider { return nil },
		},
		{
			name: "tier-2 tie, judge times out", phrase: "the walk home", tier3: true, tier: 2,
			judge: func(*testing.T, *Router, map[string]string) provider.Provider {
				return &provider.Fake{Script: []provider.Exchange{{Err: fmt.Errorf("judge: %w", provider.ErrTimeout)}}}
			},
		},
		{
			name: "tier-2 tie, judge finds no match", phrase: "the walk home", tier3: true, tier: 2,
			judge: func(*testing.T, *Router, map[string]string) provider.Provider {
				return &provider.Fake{Script: []provider.Exchange{{Content: `{"matches": []}`}}}
			},
		},
		{name: "tier-2 unconfident single, judge off", phrase: "sunny morning", tier: 2},
		{
			name: "no word overlap, judge plausibly close", phrase: "my commute", tier3: true, tier: 3,
			judge: func(t *testing.T, r *Router, keys map[string]string) provider.Provider {
				return &provider.Fake{Script: []provider.Exchange{
					judgeReply(t, r, "my commute", map[string]float64{keys["work"]: 0.7}),
				}}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := bootedGratitude(t)
			withTier3(r, func(m *config.GratitudeMatchConfig) { m.Tier3Enabled = tc.tier3 })
			keys := map[string]string{
				"work": addGratitude(t, r, "the walk to work", day1()).Key,
				"home": addGratitude(t, r, "a quiet home", day1()).Key,
				"run":  addGratitude(t, r, "a sunny morning run", day1()).Key,
			}
			before := snapshotGratitude(t, r)
			var judge provider.Provider
			if tc.judge != nil {
				judge = tc.judge(t, r, keys)
			}

			res, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: tc.phrase, Provider: judge, Now: day2()})
			assert.False(t, res.Created, "nothing was created")
			sugg := requireGratitudeSuggestion(t, r, err, tc.phrase, before)
			assert.Equal(t, tc.tier, sugg.MatchTier)

			fresh, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: tc.phrase, ForceNew: true, Now: day2()})
			require.NoError(t, err)
			assert.True(t, fresh.Created, "only the explicit --new answer creates")
			assert.Len(t, snapshotGratitude(t, r), len(before)+1)
		})
	}
}

// TestGratitudeAutoBumpRecordsMatchTier: an automatic landing is auditable on the
// record itself, not just in the ack (gratitude.md §2, §7.7; ADR-0012 §5). A
// clear tier-2 winner and a clear tier-3 winner each append an occurrence that
// carries its receipt, the deciding tier, and the winning score; tonight's
// wording joins the entry's aka[] while the canonical display stays; and no
// entry is started at the phrase's own key. Every landing a person decided or
// the canonical key decided — a create, a tier-1 bump, a forward through a merge
// tombstone, `--into`, and `--new` — carries no attribution, the v1 shape.
func TestGratitudeAutoBumpRecordsMatchTier(t *testing.T) {
	requireNoOwnEntry := func(t *testing.T, r *Router, phrase string) {
		t.Helper()
		key, err := r.store.ResolveGratitudeKey(phrase)
		require.NoError(t, err)
		_, found, err := r.store.ReadGratitude(key)
		require.NoError(t, err)
		assert.False(t, found, "an automatic bump never starts an entry at the phrase's own key")
	}
	lastEvent := func(t *testing.T, r *Router, key string) observations.GratitudeEvent {
		t.Helper()
		e := gratitudeEntry(t, r, key)
		require.NotEmpty(t, e.History)
		return e.History[len(e.History)-1]
	}

	t.Run("tier 2 by wording", func(t *testing.T) {
		r := bootedGratitude(t)
		walk := addGratitude(t, r, "a morning walk by a river", day1())
		addGratitude(t, r, "a quiet river", day1())

		res, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "the morning walks by the river", Now: day2()})
		require.NoError(t, err)
		require.Equal(t, walk.Key, res.Key)

		ev := lastEvent(t, r, walk.Key)
		assert.Equal(t, res.Receipt, ev.ID, "the attributed occurrence is the one the receipt names")
		assert.Equal(t, observations.GratitudeEventOccurrence, ev.Type)
		assert.Equal(t, 2, ev.MatchTier)
		assert.InDelta(t, res.MatchScore, ev.MatchScore, 1e-9)
		assert.InDelta(t, 1.0, ev.MatchScore, 1e-9)

		entry := gratitudeEntry(t, r, walk.Key)
		assert.Equal(t, "a morning walk by a river", entry.DisplayName, "the canonical display is kept")
		assert.Contains(t, entry.Aka, "the morning walks by the river", "tonight's wording joins aka[]")
		requireNoOwnEntry(t, r, "the morning walks by the river")
	})

	t.Run("tier 3 by meaning", func(t *testing.T) {
		r := bootedTier3(t)
		bike := addGratitude(t, r, "the two wheels that carry me to work", day1())
		addGratitude(t, r, "clean drinking water from the tap", day2())

		fake := &provider.Fake{Script: []provider.Exchange{judgeReply(t, r, "my bike", map[string]float64{bike.Key: 0.95})}}
		res, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "my bike", Provider: fake, Now: day3()})
		require.NoError(t, err)
		require.Equal(t, bike.Key, res.Key)

		ev := lastEvent(t, r, bike.Key)
		assert.Equal(t, res.Receipt, ev.ID)
		assert.Equal(t, 3, ev.MatchTier)
		assert.InDelta(t, 0.95, ev.MatchScore, 1e-9)

		entry := gratitudeEntry(t, r, bike.Key)
		assert.Equal(t, "the two wheels that carry me to work", entry.DisplayName)
		assert.Equal(t, []string{"the two wheels that carry me to work", "my bike"}, entry.Aka)
		requireNoOwnEntry(t, r, "my bike")
	})

	t.Run("a person's or the key's landing carries none", func(t *testing.T) {
		r := bootedGratitude(t)
		coffee := addGratitude(t, r, "my morning coffee", day1()) // create
		addGratitude(t, r, "My Morning Coffee!", day2())          // tier 1
		roof := addGratitude(t, r, "a roof over my head", day1()) // create
		house := addGratitude(t, r, "my house", day1())           // create
		_, err := r.MergeGratitude(GratitudeMergeRequest{Source: house.Key, Target: roof.Key, Now: day2()})
		require.NoError(t, err)
		addGratitude(t, r, "my house", day3()) // tier 1, forwarded through the tombstone
		_, err = r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "hot tea", Into: coffee.Key, Now: day3()})
		require.NoError(t, err)
		_, err = r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "coffee in the morning", ForceNew: true, Now: day3()})
		require.NoError(t, err)

		all, err := r.store.ReadGratitudeAll()
		require.NoError(t, err)
		var occurrences int
		for _, e := range all {
			for _, ev := range e.History {
				if ev.Type != observations.GratitudeEventOccurrence {
					continue
				}
				occurrences++
				assert.Zerof(t, ev.MatchTier, "%s %s carries no match_tier", e.Key, ev.ID)
				assert.Zerof(t, ev.MatchScore, "%s %s carries no match_score", e.Key, ev.ID)
			}
		}
		assert.Equal(t, 7, occurrences, "every landing above appended one occurrence")
	})
}
