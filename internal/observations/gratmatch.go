package observations

import (
	"cmp"
	"slices"
	"strings"
	"unicode"
)

// gratmatch.go is the deterministic half of the gratitude match (gratitude.md
// §7.1–§7.2): the tier-2 normalized token scorer and the pure confidence-band
// rule every tier's scores are classified by. Tier 2 connects wordings that share
// words once case, punctuation, stopwords, and plural/possessive endings are set
// aside ("My Morning Coffee!" and "the morning coffees"); it deliberately uses no
// edit distance, so "my dog" and "my dad" never pair. Everything here is a pure
// function of its inputs — the router hands in the live entries, and no path
// touches disk or a model (architecture P3/P9). By-meaning matching (tier 3) is
// the router's optional provider step; it reuses [ClassifyGratitudeBand] so every
// tier is banded by the one rule. The `reconcile` pass (gratitude.md §7.9) scores
// the live entries against each other with the same scorer ([Tier2Pairs]) and
// bands the pairs from both sides with the same rule ([BandGratitudePairs]).

// GratitudeBand is the confidence band a ranked candidate list falls in
// (gratitude.md §7.2). It decides what a nightly `add` does: bump, suggest, or
// create.
type GratitudeBand string

// The three confidence bands (gratitude.md §7.2).
const (
	// GratitudeBandHigh is a clear single winner: top-1 clears the tier's high
	// cutoff AND beats top-2 by the tier's margin. The add auto-bumps it.
	GratitudeBandHigh GratitudeBand = "high"
	// GratitudeBandAmbiguous is a near-tie or a plausible-but-unconfident match:
	// top-1 is at or above the floor but not High. The add suggests — it never
	// silently merges and never silently creates.
	GratitudeBandAmbiguous GratitudeBand = "ambiguous"
	// GratitudeBandLow is nothing close: top-1 is below the floor, or there is
	// no candidate at all. The add creates a new entry.
	GratitudeBandLow GratitudeBand = "low"
)

// GratitudeSuggestionLimit caps how many candidates an ambiguous-band suggestion
// lists (gratitude.md §7.4: every candidate at or above the floor, best first, at
// most three).
const GratitudeSuggestionLimit = 3

// gratitudeBandEpsilon absorbs float noise in the band comparisons, so a score
// that equals a cutoff by construction (1.0 − 0.85 vs a 0.15 margin) is not
// misfiled by a last-bit rounding error.
const gratitudeBandEpsilon = 1e-9

// gratitudeMinStem is the shortest stem light stemming may leave (gratitude.md
// §7.1): an ending is only removed when at least three letters remain, so short
// words like "gas" or "bus" are never mangled.
const gratitudeMinStem = 3

// GratitudeCandidate is one ranked match candidate: a live entry's stable key,
// its display wording, and its score in [0, 1] (for tier 2, the Dice coefficient
// of the phrase's token set against the entry's best-scoring wording).
type GratitudeCandidate struct {
	Key   string
	Thing string
	Score float64
}

// GratitudeBandCutoffs are one tier's band thresholds (gratitude.md §7.2): the
// high cutoff and top-1/top-2 margin an automatic bump needs, and the shared
// ambiguous floor below which nothing is suggested.
type GratitudeBandCutoffs struct {
	High   float64
	Margin float64
	Floor  float64
}

// Tier2Tokens reduces a wording to its tier-2 token set (gratitude.md §7.1):
// lowercased; split on anything that is not a letter or digit (an apostrophe is
// kept only long enough to drop a possessive 's, then removed, so "Sam's" and
// "don't" stay one word each); the fixed stopword list removed; each word lightly
// stemmed ([stemGratitudeToken]); duplicates collapsed. The set is returned sorted
// so it is deterministic. A wording that is all stopwords yields an empty set.
func Tier2Tokens(phrase string) []string {
	var raw []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			raw = append(raw, b.String())
			b.Reset()
		}
	}
	for _, r := range strings.ToLower(phrase) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case r == '\'' || r == '’':
			b.WriteRune('\'')
		default:
			flush()
		}
	}
	flush()

	out := make([]string, 0, len(raw))
	for _, tok := range raw {
		tok = strings.TrimSuffix(tok, "'s")
		tok = strings.ReplaceAll(tok, "'", "")
		if tok == "" || isGratitudeStopword(tok) {
			continue
		}
		tok = stemGratitudeToken(tok)
		if !slices.Contains(out, tok) {
			out = append(out, tok)
		}
	}
	slices.Sort(out)
	return out
}

// isGratitudeStopword reports whether w is on tier 2's small, fixed English
// stopword list (gratitude.md §7.1): articles, possessive determiners, and the
// commonest joining words — the words that make "the walk to work" and "walk to
// work" read alike without carrying what the gratitude is about.
func isGratitudeStopword(w string) bool {
	switch w {
	case "a", "an", "the",
		"my", "our", "your", "his", "her", "their", "its",
		"of", "for", "to", "and", "with", "in", "on", "at",
		"that", "this", "these", "those", "some":
		return true
	}
	return false
}

// stemGratitudeToken applies tier 2's light, plural-focused stemming
// (gratitude.md §7.1), first matching rule wins, and never leaves a stem shorter
// than three letters (a rule that would is skipped, and the next one is tried):
//
//   - -ies → -y ("berries" → "berry"), and a word ending -ie folds to -y the same
//     way so "cookie" and "cookies" agree ("cooky");
//   - -es dropped after ss/x/z/ch/sh ("glasses" → "glass", "boxes" → "box",
//     "wishes" → "wish") — not after a single s, so "houses" keeps its e and
//     meets "house" through the next rule;
//   - otherwise a trailing -s dropped, except after s, u, or i ("walks" → "walk",
//     but "glass", "bus", and "iris" stay whole).
//
// It is deliberately small: enough that a plural and its singular meet, never a
// full morphological stemmer, so two different words are not folded together.
func stemGratitudeToken(w string) string {
	keep := func(stem string) bool { return len([]rune(stem)) >= gratitudeMinStem }

	if s, ok := strings.CutSuffix(w, "ies"); ok && keep(s+"y") {
		return s + "y"
	}
	if s, ok := strings.CutSuffix(w, "ie"); ok && keep(s+"y") {
		return s + "y"
	}
	if s, ok := strings.CutSuffix(w, "es"); ok && keep(s) {
		for _, end := range []string{"ss", "x", "z", "ch", "sh"} {
			if strings.HasSuffix(s, end) {
				return s
			}
		}
	}
	if s, ok := strings.CutSuffix(w, "s"); ok && keep(s) &&
		!strings.HasSuffix(s, "s") && !strings.HasSuffix(s, "u") && !strings.HasSuffix(s, "i") {
		return s
	}
	return w
}

// Tier2Score is the Dice coefficient of two token sets, 2·|A∩B| / (|A|+|B|), in
// [0, 1] (gratitude.md §7.1). Dice rather than containment is deliberate: a
// one-word phrase is not a confident match for every long wording that happens
// to contain that word. Either set empty scores 0. Both inputs are sets (as
// [Tier2Tokens] returns) — a repeated token would overcount.
func Tier2Score(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	shared := 0
	for _, t := range a {
		if slices.Contains(b, t) {
			shared++
		}
	}
	return 2 * float64(shared) / float64(len(a)+len(b))
}

// Tier2 is the deterministic tier-2 match (gratitude.md §7.1): it scores phrase
// against every live entry — each of its wordings, the display_name and every
// aka[] form — and returns the entries with any overlap as ranked candidates, an
// entry's score being its best wording's score. It is a pure function of its
// inputs: the router hands in the entries, and nothing here reads disk or calls a
// model. A merge tombstone is never a match target (the caller drops tombstones
// from the live set, and Tier2 skips any it is handed regardless), so every
// candidate is already the canonical live entry. A phrase that is all stopwords
// yields no candidates.
func Tier2(phrase string, entries []GratitudeEntry) []GratitudeCandidate {
	pt := Tier2Tokens(phrase)
	if len(pt) == 0 {
		return nil
	}
	var out []GratitudeCandidate
	for _, e := range entries {
		if e.IsTombstone() {
			continue
		}
		best := Tier2Score(pt, Tier2Tokens(e.DisplayName))
		for _, w := range e.Aka {
			if s := Tier2Score(pt, Tier2Tokens(w)); s > best {
				best = s
			}
		}
		if best > 0 {
			out = append(out, GratitudeCandidate{Key: e.Key, Thing: e.DisplayName, Score: best})
		}
	}
	RankGratitudeCandidates(out)
	return out
}

// RankGratitudeCandidates sorts candidates best first — score descending, then
// stable key ascending — so a tie always ranks the same way. [ClassifyGratitudeBand]
// and [GratitudeSuggestions] read a list in this order.
func RankGratitudeCandidates(cands []GratitudeCandidate) {
	slices.SortStableFunc(cands, func(x, y GratitudeCandidate) int {
		if c := cmp.Compare(y.Score, x.Score); c != 0 {
			return c
		}
		return cmp.Compare(x.Key, y.Key)
	})
}

// ClassifyGratitudeBand files a ranked candidate list into its confidence band
// (gratitude.md §7.2), the one rule every tier is classified by with its own
// cutoffs. top-1 and top-2 are the best and second-best scores (top-2 is 0 with a
// single candidate):
//
//   - High — top-1 ≥ c.High AND top-1 − top-2 ≥ c.Margin: a clear single winner;
//   - Ambiguous — top-1 ≥ c.Floor but not High: a near-tie (an exact tie always
//     lands here) or a plausible-but-unconfident single match;
//   - Low — top-1 < c.Floor, a zero top-1 (no evidence at all), or no candidates.
//
// cands must be ranked best first ([RankGratitudeCandidates]).
func ClassifyGratitudeBand(cands []GratitudeCandidate, c GratitudeBandCutoffs) GratitudeBand {
	if len(cands) == 0 || cands[0].Score <= 0 {
		return GratitudeBandLow
	}
	top1, top2 := cands[0].Score, 0.0
	if len(cands) > 1 {
		top2 = cands[1].Score
	}
	if atLeast(top1, c.High) && atLeast(top1-top2, c.Margin) {
		return GratitudeBandHigh
	}
	if atLeast(top1, c.Floor) {
		return GratitudeBandAmbiguous
	}
	return GratitudeBandLow
}

// GratitudeSuggestions returns the candidates an ambiguous-band suggestion lists
// (gratitude.md §7.4): every candidate at or above floor, best first, at most
// [GratitudeSuggestionLimit]. cands must be ranked best first. The result is a
// fresh slice; the input is not modified.
func GratitudeSuggestions(cands []GratitudeCandidate, floor float64) []GratitudeCandidate {
	out := make([]GratitudeCandidate, 0, GratitudeSuggestionLimit)
	for _, c := range cands {
		if len(out) == GratitudeSuggestionLimit {
			break
		}
		if c.Score > 0 && atLeast(c.Score, floor) {
			out = append(out, c)
		}
	}
	return out
}

// atLeast reports v ≥ threshold, tolerating float noise of gratitudeBandEpsilon.
func atLeast(v, threshold float64) bool {
	return v >= threshold-gratitudeBandEpsilon
}

// GratitudePair is one pair of live entries a reconcile pass found possibly
// naming the same thing (gratitude.md §7.9): the two stable keys, ordered A < B
// so a pair has one spelling, the pair's score in [0, 1], and — once banded by
// [BandGratitudePairs] — its confidence band. The pair is unordered: which entry
// folds into which is the caller's call, not the score's.
type GratitudePair struct {
	A     string
	B     string
	Score float64
	Band  GratitudeBand
}

// NewGratitudePair spells an unordered pair with its keys in order (A < B).
func NewGratitudePair(x, y string, score float64) GratitudePair {
	if y < x {
		x, y = y, x
	}
	return GratitudePair{A: x, B: y, Score: score}
}

// Tier2Pairs is the tier-2 pass of `reconcile` (gratitude.md §7.9): it scores
// every pair of live entries with the tier-2 scorer — the Dice score of the best
// pair of wordings, one from each entry's display_name and aka[] forms — and
// returns every pair with any overlap, unbanded, best first (then by key). Like
// [Tier2] it is a pure function of the entries it is handed: no disk, no model,
// and a merge tombstone is never paired.
func Tier2Pairs(entries []GratitudeEntry) []GratitudePair {
	type tokened struct {
		key  string
		sets [][]string
	}
	live := make([]tokened, 0, len(entries))
	for _, e := range entries {
		if !e.IsTombstone() {
			live = append(live, tokened{key: e.Key, sets: tier2WordingSets(e)})
		}
	}

	var out []GratitudePair
	for i := range live {
		for j := i + 1; j < len(live); j++ {
			if best := bestTier2Score(live[i].sets, live[j].sets); best > 0 {
				out = append(out, NewGratitudePair(live[i].key, live[j].key, best))
			}
		}
	}
	rankGratitudePairs(out)
	return out
}

// tier2WordingSets returns the tier-2 token set of each of an entry's wordings —
// its display_name and every aka[] form — skipping any that are all stopwords.
func tier2WordingSets(e GratitudeEntry) [][]string {
	sets := make([][]string, 0, len(e.Aka)+1)
	for _, w := range append([]string{e.DisplayName}, e.Aka...) {
		if tok := Tier2Tokens(w); len(tok) > 0 {
			sets = append(sets, tok)
		}
	}
	return sets
}

// bestTier2Score is the best [Tier2Score] over every pair of wordings, one from
// each side — how two entries score against each other.
func bestTier2Score(a, b [][]string) float64 {
	best := 0.0
	for _, x := range a {
		for _, y := range b {
			best = max(best, Tier2Score(x, y))
		}
	}
	return best
}

// BandGratitudePairs files scored pairs into confidence bands (gratitude.md
// §7.9) with one tier's cutoffs, by the same rule [ClassifyGratitudeBand] applies
// to an `add`, read from both sides of the pair:
//
//   - High — each entry is the other's clear single winner: ranking every pair
//     that entry is in, the partner is its top-1, top-1 clears c.High, and it
//     beats that entry's top-2 by c.Margin — checked for A and for B;
//   - Ambiguous — the score is at or above c.Floor, but the pair is not High (a
//     near-tie on either side, or a plausible-but-unconfident score);
//   - Low — below the floor; such a pair is dropped, never proposed.
//
// It returns the High and Ambiguous pairs, best first (then by key). The input
// is not modified.
func BandGratitudePairs(pairs []GratitudePair, c GratitudeBandCutoffs) []GratitudePair {
	ranks := make(map[string][]GratitudeCandidate)
	for _, p := range pairs {
		ranks[p.A] = append(ranks[p.A], GratitudeCandidate{Key: p.B, Score: p.Score})
		ranks[p.B] = append(ranks[p.B], GratitudeCandidate{Key: p.A, Score: p.Score})
	}
	for k := range ranks {
		RankGratitudeCandidates(ranks[k])
	}
	clearWinner := func(of, partner string) bool {
		rank := ranks[of]
		return ClassifyGratitudeBand(rank, c) == GratitudeBandHigh && rank[0].Key == partner
	}

	out := make([]GratitudePair, 0, len(pairs))
	for _, p := range pairs {
		switch {
		case p.Score <= 0:
			continue
		case clearWinner(p.A, p.B) && clearWinner(p.B, p.A):
			p.Band = GratitudeBandHigh
		case atLeast(p.Score, c.Floor):
			p.Band = GratitudeBandAmbiguous
		default:
			continue
		}
		out = append(out, p)
	}
	rankGratitudePairs(out)
	return out
}

// rankGratitudePairs sorts pairs best first — score descending, then A, then B —
// so a tie always ranks the same way.
func rankGratitudePairs(pairs []GratitudePair) {
	slices.SortStableFunc(pairs, func(x, y GratitudePair) int {
		if c := cmp.Compare(y.Score, x.Score); c != 0 {
			return c
		}
		if c := cmp.Compare(x.A, y.A); c != 0 {
			return c
		}
		return cmp.Compare(x.B, y.B)
	})
}
