package observations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gratmatch_test.go covers the deterministic tier-2 gratitude match
// (gratmatch.go; gratitude.md §7.1–§7.2): the token normalization, the Dice
// scorer over live entries, and the pure confidence-band rule. Every phrase and
// entry here is synthetic — invented gratitudes, never a real tally.

// tier2Cutoffs are the documented tier-2 band defaults (gratitude.md §9:
// tier2_high 0.85, tier2_margin 0.15, ambiguous_floor 0.50), spelled out so the
// table below reads against the numbers it is tuned to.
var tier2Cutoffs = GratitudeBandCutoffs{High: 0.85, Margin: 0.15, Floor: 0.50} //nolint:gochecknoglobals // read-only test fixture

// gEntry builds a synthetic live gratitude entry with extra aka wordings.
func gEntry(key, display string, aka ...string) GratitudeEntry {
	e := NewGratitudeEntry(key, display, "2026-07-01T21:00:00-04:00")
	e.Aka = append(e.Aka, aka...)
	return e
}

// TestTier2Tokens pins the tier-2 normalization (gratitude.md §7.1): case and
// punctuation fold away, the stopword list drops, a possessive 's drops, and light
// stemming meets a plural with its singular — without cutting a short word below
// three letters or folding "houses" to "hous".
func TestTier2Tokens(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"My Morning Coffee!", []string{"coffee", "morning"}},
		{"the morning walks", []string{"morning", "walk"}},
		{"walk, walks, WALK", []string{"walk"}},
		{"Sam's garden", []string{"garden", "sam"}},
		{"Sam’s garden", []string{"garden", "sam"}},
		{"kids' toys", []string{"kid", "toy"}},
		{"don't rush", []string{"dont", "rush"}},
		{"fresh strawberries", []string{"fresh", "strawberry"}},
		{"a fresh strawberry", []string{"fresh", "strawberry"}},
		{"cookies", []string{"cooky"}},
		{"a cookie", []string{"cooky"}},
		{"glasses of water", []string{"glass", "water"}},
		{"a glass", []string{"glass"}},
		{"boxes", []string{"box"}},
		{"wishes", []string{"wish"}},
		{"the houses", []string{"house"}},
		{"a house", []string{"house"}},
		{"pies", []string{"pie"}},
		{"bus", []string{"bus"}},
		{"gas", []string{"gas"}},
		{"rock-climbing", []string{"climbing", "rock"}},
		{"café mornings", []string{"café", "morning"}},
		{"my dog", []string{"dog"}},
		{"my dad", []string{"dad"}},
		{"the and of some", []string{}},
		{"   ", []string{}},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			assert.Equal(t, tc.want, Tier2Tokens(tc.in))
		})
	}
}

// TestTier2Match is the tier-2 table over synthetic phrases (gratitude.md §7.1,
// §7.2), each scored against a small live tally and banded with the documented
// tier-2 cutoffs. It covers a true match, stopword/plural/possessive variants, an
// aka hit, a near-miss that must NOT match ("my dog" vs "my dad" share nothing),
// the tie that must suggest and never auto-merge, a near-tie with a strong top-1,
// a plausible-but-unconfident single candidate, a clear winner over a weak
// runner-up, and a genuinely new phrase in a busy tally that must create.
func TestTier2Match(t *testing.T) {
	coffee := gEntry("gratitude_a-river", "my morning coffee", "the first cup of the day")
	water := gEntry("gratitude_b-stone", "clean drinking water")
	walkWork := gEntry("gratitude_c-fern", "the walk to work")
	quietHome := gEntry("gratitude_d-moss", "a quiet home")
	busy := []GratitudeEntry{coffee, water, walkWork, quietHome}

	tests := []struct {
		name        string
		phrase      string
		entries     []GratitudeEntry
		wantBand    GratitudeBand
		wantTop     string   // the top-ranked candidate key; "" ⇒ no candidate at all
		wantScore   float64  // the top score (checked when wantTop is set)
		wantSuggest []string // the ambiguous-band suggestion keys, best first
	}{
		{
			name:     "true match — case and punctuation differ",
			phrase:   "My Morning Coffee!",
			entries:  busy,
			wantBand: GratitudeBandHigh, wantTop: coffee.Key, wantScore: 1.0,
		},
		{
			name:   "stopword and plural variant",
			phrase: "the morning walks by the river",
			entries: []GratitudeEntry{
				gEntry("gratitude_a-river", "a morning walk by a river"),
				gEntry("gratitude_b-stone", "a quiet river"),
			},
			wantBand: GratitudeBandHigh, wantTop: "gratitude_a-river", wantScore: 1.0,
		},
		{
			name:     "-ies plural meets its singular",
			phrase:   "fresh strawberries",
			entries:  []GratitudeEntry{gEntry("gratitude_a-river", "a fresh strawberry")},
			wantBand: GratitudeBandHigh, wantTop: "gratitude_a-river", wantScore: 1.0,
		},
		{
			name:     "-ie word and possessive fold alike",
			phrase:   "cookies with the kids",
			entries:  []GratitudeEntry{gEntry("gratitude_a-river", "a cookie with my kid's friends")},
			wantBand: GratitudeBandAmbiguous, wantTop: "gratitude_a-river", wantScore: 0.8,
			wantSuggest: []string{"gratitude_a-river"},
		},
		{
			name:     "an aka wording is match evidence; the candidate is the canonical entry",
			phrase:   "the first cup of the day",
			entries:  busy,
			wantBand: GratitudeBandHigh, wantTop: coffee.Key, wantScore: 1.0,
		},
		{
			name:     "near-miss must NOT match — my dog vs my dad",
			phrase:   "my dog",
			entries:  []GratitudeEntry{gEntry("gratitude_a-river", "my dad")},
			wantBand: GratitudeBandLow,
		},
		{
			name:   "exact tie at a strong score suggests, never auto-merges",
			phrase: "the coffee, in the morning",
			entries: []GratitudeEntry{
				gEntry("gratitude_a-river", "morning coffee"),
				gEntry("gratitude_b-stone", "coffee in the morning"),
			},
			wantBand: GratitudeBandAmbiguous, wantTop: "gratitude_a-river", wantScore: 1.0,
			wantSuggest: []string{"gratitude_a-river", "gratitude_b-stone"},
		},
		{
			name:     "exact tie at the floor suggests both",
			phrase:   "the walk home",
			entries:  busy,
			wantBand: GratitudeBandAmbiguous, wantTop: walkWork.Key, wantScore: 0.5,
			wantSuggest: []string{walkWork.Key, quietHome.Key},
		},
		{
			name:   "near-tie — a strong top-1 with a close runner-up is ambiguous",
			phrase: "quiet evening walk along river",
			entries: []GratitudeEntry{
				gEntry("gratitude_a-river", "a quiet evening walk along the river"),
				gEntry("gratitude_b-stone", "an evening walk along the river"),
			},
			wantBand: GratitudeBandAmbiguous, wantTop: "gratitude_a-river", wantScore: 1.0,
			wantSuggest: []string{"gratitude_a-river", "gratitude_b-stone"},
		},
		{
			name:     "plausible but unconfident single candidate is suggested, not created",
			phrase:   "sunny morning",
			entries:  []GratitudeEntry{gEntry("gratitude_a-river", "a sunny morning run")},
			wantBand: GratitudeBandAmbiguous, wantTop: "gratitude_a-river", wantScore: 0.8,
			wantSuggest: []string{"gratitude_a-river"},
		},
		{
			name:   "clear winner over a weak runner-up auto-bumps",
			phrase: "morning coffee outside",
			entries: []GratitudeEntry{
				gEntry("gratitude_a-river", "morning coffee outside on the porch"),
				gEntry("gratitude_b-stone", "coffee with a neighbor"),
			},
			wantBand: GratitudeBandHigh, wantTop: "gratitude_a-river", wantScore: 6.0 / 7.0,
		},
		{
			name:     "a short phrase against an elaborated entry falls to Low (tier 3's case)",
			phrase:   "my bike",
			entries:  []GratitudeEntry{gEntry("gratitude_a-river", "the bike that carries me to work")},
			wantBand: GratitudeBandLow, wantTop: "gratitude_a-river", wantScore: 0.4,
		},
		{
			name:     "genuinely new phrase in a busy tally creates",
			phrase:   "fresh bread",
			entries:  busy,
			wantBand: GratitudeBandLow,
		},
		{
			name:     "an all-stopword phrase has no candidates",
			phrase:   "the and of",
			entries:  busy,
			wantBand: GratitudeBandLow,
		},
		{
			name:     "an empty tally has no candidates",
			phrase:   "my morning coffee",
			entries:  nil,
			wantBand: GratitudeBandLow,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cands := Tier2(tc.phrase, tc.entries)
			band := ClassifyGratitudeBand(cands, tier2Cutoffs)
			assert.Equal(t, tc.wantBand, band)

			if tc.wantTop == "" {
				assert.Empty(t, cands, "no entry shares a word with the phrase")
			} else {
				require.NotEmpty(t, cands)
				assert.Equal(t, tc.wantTop, cands[0].Key)
				assert.InDelta(t, tc.wantScore, cands[0].Score, 1e-9)
			}

			suggest := GratitudeSuggestions(cands, tier2Cutoffs.Floor)
			if band == GratitudeBandAmbiguous {
				keys := make([]string, 0, len(suggest))
				for _, s := range suggest {
					keys = append(keys, s.Key)
				}
				assert.Equal(t, tc.wantSuggest, keys, "the suggestion lists every candidate at or above the floor")
			}
			if len(tc.wantSuggest) > 1 {
				assert.NotEqual(t, GratitudeBandHigh, band, "a tie or near-tie is never an automatic bump")
			}
		})
	}
}

// TestTier2SkipsTombstonesAndReadsAka: a merge tombstone is never a tier-2 target
// even when its wording matches exactly (a merged-away duplicate resolves through
// its redirect, not through matching), while a live entry's aka[] wordings are
// scored alongside its display name and the candidate carries the display name.
func TestTier2SkipsTombstonesAndReadsAka(t *testing.T) {
	tomb := gEntry("gratitude_b-stone", "slow mornings")
	tomb.RedirectTo = "gratitude_a-river"
	live := gEntry("gratitude_a-river", "unhurried starts to the day", "slow mornings")

	cands := Tier2("slow mornings", []GratitudeEntry{tomb, live})
	require.Len(t, cands, 1, "the tombstone is not scored")
	assert.Equal(t, "gratitude_a-river", cands[0].Key, "the only candidate is the canonical live entry")
	assert.Equal(t, "unhurried starts to the day", cands[0].Thing, "the candidate names the entry's display wording")
	assert.InDelta(t, 1.0, cands[0].Score, 1e-9, "an entry scores by its best wording")
}

// TestTier2Score pins the Dice coefficient: identical sets score 1, disjoint sets
// 0, an empty side 0, and a one-word phrase against a five-word wording 1/3 —
// Dice, not containment, so a single shared word is never a confident match.
func TestTier2Score(t *testing.T) {
	assert.InDelta(t, 1.0, Tier2Score([]string{"coffee", "morning"}, []string{"coffee", "morning"}), 1e-9)
	assert.InDelta(t, 0.0, Tier2Score([]string{"dog"}, []string{"dad"}), 1e-9)
	assert.InDelta(t, 0.0, Tier2Score(nil, []string{"dad"}), 1e-9)
	assert.InDelta(t, 0.0, Tier2Score([]string{"dog"}, []string{}), 1e-9)
	assert.InDelta(t, 1.0/3.0,
		Tier2Score([]string{"bike"}, []string{"bike", "carry", "me", "two", "wheel"}), 1e-9)
}

// TestTier2BandRule pins the pure band classification (gratitude.md §7.2) at its
// edges: High needs both the cutoff and the margin (a lone candidate's runner-up
// is 0), the floor is inclusive, a margin that equals the configured margin
// exactly still counts despite float noise, and a zero score is no evidence.
func TestTier2BandRule(t *testing.T) {
	c := func(scores ...float64) []GratitudeCandidate {
		out := make([]GratitudeCandidate, 0, len(scores))
		for i, s := range scores {
			out = append(out, GratitudeCandidate{Key: string(rune('a' + i)), Score: s})
		}
		return out
	}
	tests := []struct {
		name  string
		cands []GratitudeCandidate
		want  GratitudeBand
	}{
		{"no candidates", nil, GratitudeBandLow},
		{"lone strong candidate", c(0.9), GratitudeBandHigh},
		{"clear winner", c(1.0, 0.5), GratitudeBandHigh},
		{"margin exactly the configured margin", c(1.0, 0.85), GratitudeBandHigh},
		{"near-tie above the cutoff", c(1.0, 0.9), GratitudeBandAmbiguous},
		{"exact tie", c(0.9, 0.9), GratitudeBandAmbiguous},
		{"below the cutoff, above the floor", c(0.84), GratitudeBandAmbiguous},
		{"exactly at the floor", c(0.5), GratitudeBandAmbiguous},
		{"just below the floor", c(0.49), GratitudeBandLow},
		{"zero score is no evidence", c(0), GratitudeBandLow},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ClassifyGratitudeBand(tc.cands, tier2Cutoffs))
		})
	}

	// Zero cutoffs never promote an empty list or a zero score.
	assert.Equal(t, GratitudeBandLow, ClassifyGratitudeBand(nil, GratitudeBandCutoffs{}))
	assert.Equal(t, GratitudeBandLow, ClassifyGratitudeBand(c(0), GratitudeBandCutoffs{}))
}

// TestTier2SuggestionsCapAndFloor: a suggestion lists only candidates at or above
// the floor, best first, and never more than three.
func TestTier2SuggestionsCapAndFloor(t *testing.T) {
	cands := []GratitudeCandidate{
		{Key: "a", Score: 0.8},
		{Key: "b", Score: 0.7},
		{Key: "c", Score: 0.6},
		{Key: "d", Score: 0.55},
		{Key: "e", Score: 0.3},
	}
	got := GratitudeSuggestions(cands, 0.5)
	require.Len(t, got, GratitudeSuggestionLimit)
	assert.Equal(t, "a", got[0].Key)
	assert.Equal(t, "c", got[2].Key)

	below := GratitudeSuggestions([]GratitudeCandidate{{Key: "e", Score: 0.3}}, 0.5)
	assert.Empty(t, below, "a candidate below the floor is never suggested")
	assert.NotNil(t, below)

	// Ranking is score-descending with a stable key tie-break.
	tied := []GratitudeCandidate{{Key: "b", Score: 0.5}, {Key: "a", Score: 0.5}, {Key: "c", Score: 0.9}}
	RankGratitudeCandidates(tied)
	assert.Equal(t, []string{"c", "a", "b"}, []string{tied[0].Key, tied[1].Key, tied[2].Key})
}

// TestTier2Pairs: the reconcile pass scores every pair of live entries with the
// tier-2 scorer (gratitude.md §7.9) — the best pair of wordings, one from each
// entry's display_name and aka[] — keeps only pairs with any overlap, never
// pairs a tombstone, and returns them best first. "my dog" and "my dad" share no
// token, so they are never a pair.
func TestTier2Pairs(t *testing.T) {
	tomb := gEntry("gratitude_e-tomb", "morning coffee")
	tomb.RedirectTo = "gratitude_a-coffee"
	entries := []GratitudeEntry{
		gEntry("gratitude_g-market", "fresh fruit at the market"),
		gEntry("gratitude_a-coffee", "my morning coffee"),
		gEntry("gratitude_b-coffee", "morning coffees"),
		gEntry("gratitude_c-dog", "my dog"),
		gEntry("gratitude_d-dad", "my dad"),
		tomb,
		gEntry("gratitude_f-bread", "fresh bread", "fresh fruit"),
	}

	got := Tier2Pairs(entries)
	require.Len(t, got, 2, "only overlapping live pairs: no dog/dad, no tombstone")
	assert.Equal(t, NewGratitudePair("gratitude_a-coffee", "gratitude_b-coffee", 1), got[0],
		"a plural and its singular meet, best first")
	assert.Equal(t, "gratitude_f-bread", got[1].A, "a pair is spelled with its keys in order")
	assert.Equal(t, "gratitude_g-market", got[1].B)
	assert.InDelta(t, 0.8, got[1].Score, 1e-9, "the best wording pair counts: an aka form (0.8), not the display (0.4)")
	for _, p := range got {
		assert.Empty(t, p.Band, "Tier2Pairs only scores; banding is separate")
	}

	assert.Empty(t, Tier2Pairs(entries[:1]), "one entry has nothing to pair with")
	assert.Empty(t, Tier2Pairs(nil))
}

// TestBandGratitudePairs pins the pair band rule (gratitude.md §7.9): a pair is
// High only when each entry is the other's clear single winner — over the high
// cutoff and ahead of that entry's runner-up by the margin, read from both sides
// — Ambiguous at or above the floor otherwise, and dropped below it. The same
// rule runs with each tier's own cutoffs.
func TestBandGratitudePairs(t *testing.T) {
	const a, b, c, d = "gratitude_a-one", "gratitude_b-two", "gratitude_c-three", "gratitude_d-four"
	pair := func(x, y string, score float64, band GratitudeBand) GratitudePair {
		p := NewGratitudePair(x, y, score)
		p.Band = band
		return p
	}
	tier3Cutoffs := GratitudeBandCutoffs{High: 0.90, Margin: 0.20, Floor: 0.50}

	tests := []struct {
		name  string
		pairs []GratitudePair
		c     GratitudeBandCutoffs
		want  []GratitudePair
	}{
		{
			name:  "each other's clear winner is High",
			pairs: []GratitudePair{NewGratitudePair(a, b, 1)},
			c:     tier2Cutoffs,
			want:  []GratitudePair{pair(a, b, 1, GratitudeBandHigh)},
		},
		{
			name:  "a clear winner amid weaker pairs; the weaker pair is only worth a look, the weakest is dropped",
			pairs: []GratitudePair{NewGratitudePair(a, b, 1), NewGratitudePair(a, c, 0.6), NewGratitudePair(b, d, 0.3)},
			c:     tier2Cutoffs,
			want:  []GratitudePair{pair(a, b, 1, GratitudeBandHigh), pair(a, c, 0.6, GratitudeBandAmbiguous)},
		},
		{
			name:  "a near-tie on one side is never High, even when the other side is clear",
			pairs: []GratitudePair{NewGratitudePair(a, b, 1), NewGratitudePair(b, c, 0.9)},
			c:     tier2Cutoffs,
			want:  []GratitudePair{pair(a, b, 1, GratitudeBandAmbiguous), pair(b, c, 0.9, GratitudeBandAmbiguous)},
		},
		{
			name:  "an exact tie is Ambiguous",
			pairs: []GratitudePair{NewGratitudePair(a, b, 0.5), NewGratitudePair(a, c, 0.5)},
			c:     tier2Cutoffs,
			want:  []GratitudePair{pair(a, b, 0.5, GratitudeBandAmbiguous), pair(a, c, 0.5, GratitudeBandAmbiguous)},
		},
		{
			name:  "a lone pair under the high cutoff is Ambiguous",
			pairs: []GratitudePair{NewGratitudePair(a, b, 0.7)},
			c:     tier2Cutoffs,
			want:  []GratitudePair{pair(a, b, 0.7, GratitudeBandAmbiguous)},
		},
		{
			name:  "below the floor is dropped",
			pairs: []GratitudePair{NewGratitudePair(a, b, 0.4)},
			c:     tier2Cutoffs,
			want:  []GratitudePair{},
		},
		{
			name:  "tier 3 bands with its own, stricter cutoffs",
			pairs: []GratitudePair{NewGratitudePair(a, b, 0.88)},
			c:     tier3Cutoffs,
			want:  []GratitudePair{pair(a, b, 0.88, GratitudeBandAmbiguous)},
		},
		{
			name:  "the same score clears tier 2's cutoff",
			pairs: []GratitudePair{NewGratitudePair(a, b, 0.88)},
			c:     tier2Cutoffs,
			want:  []GratitudePair{pair(a, b, 0.88, GratitudeBandHigh)},
		},
		{
			name:  "nothing in, nothing out",
			pairs: nil,
			c:     tier2Cutoffs,
			want:  []GratitudePair{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, BandGratitudePairs(tt.pairs, tt.c))
		})
	}
}

// TestNewGratitudePair: a pair is unordered, so either spelling yields the same
// value, keys in order.
func TestNewGratitudePair(t *testing.T) {
	assert.Equal(t, NewGratitudePair("gratitude_a-one", "gratitude_b-two", 0.5),
		NewGratitudePair("gratitude_b-two", "gratitude_a-one", 0.5))
	assert.Equal(t, "gratitude_a-one", NewGratitudePair("gratitude_b-two", "gratitude_a-one", 0.5).A)
}
