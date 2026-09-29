package router

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/observations"
)

// gratmatch_test.go covers the gratitude match pipeline (gratmatch.go;
// gratitude.md §7.1–§7.4): the tier-1 fast path, the single-hop tombstone
// forward, the tier-2 token match and its configured bands, and the ambiguous
// band's write-nothing suggestion. Every phrase here is synthetic.

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

	dec, err := r.matchGratitude("My Morning Coffee!", false)
	require.NoError(t, err)
	assert.Equal(t, gratitudeMatchDecision{
		Action: gratitudeMatchBump, Key: first.Key, Tier: 1, MakePrimary: true,
	}, dec, "a tier-1 hit is decided from the one keyed record")

	res, err := r.AddGratitude(AddGratitudeRequest{Thing: "My Morning Coffee!", Now: day2()})
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
	_, err = r.AddGratitude(AddGratitudeRequest{Thing: "clean drinking water", Now: day2()})
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

	dec, err := r.matchGratitude("my house", false)
	require.NoError(t, err)
	assert.Equal(t, gratitudeMatchBump, dec.Action)
	assert.Equal(t, roof.Key, dec.Key, "the tombstone key forwards its single hop")
	assert.Equal(t, 1, dec.Tier)
	assert.Equal(t, house.Key, dec.ForwardedFrom)
	assert.False(t, dec.MakePrimary, "a forwarded wording never replaces the destination's display")

	res, err := r.AddGratitude(AddGratitudeRequest{Thing: "My House", Now: day3()})
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

	res, err := r.AddGratitude(AddGratitudeRequest{Thing: "the morning walks by the river", Now: day2()})
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
	_, err := r.AddGratitude(AddGratitudeRequest{Thing: "the walk home", Now: day2()})
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
		res, err := r.AddGratitude(AddGratitudeRequest{Thing: "the walk home", Into: work.Key, Now: day2()})
		require.NoError(t, err)
		assert.Equal(t, work.Key, res.Key)
		assert.Equal(t, 2, res.Count)
		assert.Zero(t, res.MatchTier, "a human-chosen bump carries no match attribution")
	})

	t.Run("--new resolves to a new entry", func(t *testing.T) {
		r, work, home := setup(t)
		res, err := r.AddGratitude(AddGratitudeRequest{Thing: "the walk home", ForceNew: true, Now: day2()})
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

	dog, err := r.AddGratitude(AddGratitudeRequest{Thing: "my dog", Now: day2()})
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

	dec, err := r.matchGratitude("coffee in the morning", false)
	require.NoError(t, err)
	require.Equal(t, gratitudeMatchBump, dec.Action, "without --new the wording would auto-bump")

	fresh, err := r.AddGratitude(AddGratitudeRequest{Thing: "coffee in the morning", ForceNew: true, Now: day2()})
	require.NoError(t, err)
	assert.True(t, fresh.Created, "--new skips tier 2 and creates")
	assert.NotEqual(t, coffee.Key, fresh.Key)

	same, err := r.AddGratitude(AddGratitudeRequest{Thing: "Morning coffee!", ForceNew: true, Now: day2()})
	require.NoError(t, err)
	assert.Equal(t, coffee.Key, same.Key, "tier 1 still applies under --new")
	assert.Equal(t, 2, same.Count)

	_, err = r.AddGratitude(AddGratitudeRequest{Thing: "morning coffee", Into: coffee.Key, ForceNew: true, Now: day3()})
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
	_, err := r.AddGratitude(AddGratitudeRequest{Thing: "an evening walk along the river", ForceNew: true, Now: day1()})
	require.NoError(t, err)

	decide := func(m config.GratitudeMatchConfig, phrase string) gratitudeMatchDecision {
		t.Helper()
		r.cfg.Gratitude.Match = m
		dec, merr := r.matchGratitude(phrase, false)
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
	dec, err = r.matchGratitude("sunny morning", false)
	require.NoError(t, err)
	assert.Equal(t, gratitudeMatchSuggest, dec.Action)

	// The pipeline decides; it never writes.
	list, err := r.GratitudeList()
	require.NoError(t, err)
	assert.Equal(t, 3, list.View.Count)
	for _, e := range list.View.Entries {
		assert.Equal(t, 1, e.Count)
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
