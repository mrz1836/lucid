package router

import (
	"fmt"
	"strings"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/observations"
)

// gratmatch.go is the gratitude match pipeline (gratitude.md §7.1–§7.4): the
// step behind the v1 canonical-key seam that decides which entry a plain nightly
// `add` lands on. It runs the tiers cheapest and most certain first and stops at
// the first confident answer: tier 1, the canonical key (unchanged from v1, plus
// a single-hop forward through a merge tombstone), then tier 2, the deterministic
// token match over every live entry's wordings, banded by the configured cutoffs
// into bump / suggest / create. Both tiers are model-free (architecture P9): the
// tier-2 scorer and the band rule are pure functions in internal/observations,
// and this file only reads the live set through the storage adapter (P3) and
// applies the decision.

// gratitudeMatchAction is what a match decides an `add` does (gratitude.md §7.4).
type gratitudeMatchAction string

const (
	// gratitudeMatchBump appends the occurrence to an existing live entry.
	gratitudeMatchBump gratitudeMatchAction = "bump"
	// gratitudeMatchSuggest writes nothing and surfaces the ambiguous-band
	// candidates — never a silent merge, never a silent create.
	gratitudeMatchSuggest gratitudeMatchAction = "suggest"
	// gratitudeMatchCreate starts a new entry at the phrase's canonical key.
	gratitudeMatchCreate gratitudeMatchAction = "create"
)

// gratitudeMatchDecision is the outcome of the match pipeline for one phrase: a
// Bump of a live entry, a Suggest of ranked candidates, or a Create at a fresh
// canonical key (gratitude.md §7.3). Tier names the tier whose answer decided
// (0 for an `--into` bump, where no matching runs); Score and Band carry the
// tier-2 evidence. MakePrimary is true only for a direct tier-1 hit, the one
// landing that refreshes display_name exactly as v1 did — every other bump keeps
// the canonical display and records tonight's wording into aka[].
type gratitudeMatchDecision struct {
	Action      gratitudeMatchAction
	Key         string
	Tier        int
	Score       float64
	Band        observations.GratitudeBand
	Candidates  []observations.GratitudeCandidate
	MakePrimary bool
	// ForwardedFrom names the merge tombstone a tier-1 key resolved through
	// (gratitude.md §7.1, ADR-0012 §11); empty on every other decision.
	ForwardedFrom string
}

// automatic reports whether the decision is an automatic by-wording (or, later,
// by-meaning) bump — the landing the occurrence and the ack attribute to its
// tier (gratitude.md §7.4 High band). A tier-1 bump is the v1 canonical key and
// an `--into` bump is the human's call, so neither counts.
func (d gratitudeMatchDecision) automatic() bool {
	return d.Action == gratitudeMatchBump && d.Tier >= 2
}

// gratitudeMatchConfig returns the effective gratitude.match knobs: the booted
// config's block, or the documented defaults for a router that was never booted
// (whose zero config would otherwise carry zero cutoffs).
func (r *Router) gratitudeMatchConfig() config.GratitudeMatchConfig {
	return r.cfg.Gratitude.Match.OrDefault()
}

// matchGratitude runs the match pipeline for a plain `add` (gratitude.md §7.3).
//
// Tier 1 — the canonical key — is the fast path: a live entry at the phrase's
// key is bumped immediately, reading only that one record, so tier 2 never runs
// and no model is ever consulted (tier 3 only follows tier 2). A key naming a
// merge tombstone forwards its single hop to the live destination. Only when no
// record holds the key does matching continue: with skipFuzzy (`--new`) the
// phrase creates at that key; otherwise tier 2 scores it against every live
// entry and the tier-2 band decides — High bumps the clear winner, Ambiguous
// suggests the candidates, Low creates.
func (r *Router) matchGratitude(thing string, skipFuzzy bool) (gratitudeMatchDecision, error) {
	key, err := r.store.ResolveGratitudeKey(thing)
	if err != nil {
		return gratitudeMatchDecision{}, fmt.Errorf("could not resolve the gratitude key; nothing was saved: %w", err)
	}
	existing, found, err := r.store.ReadGratitude(key)
	if err != nil {
		return gratitudeMatchDecision{}, fmt.Errorf("could not read the gratitude entry; nothing was saved: %w", err)
	}
	if found {
		if !existing.IsTombstone() {
			return gratitudeMatchDecision{Action: gratitudeMatchBump, Key: key, Tier: 1, MakePrimary: true}, nil
		}
		return r.forwardGratitudeTombstone(existing)
	}
	if skipFuzzy {
		return gratitudeMatchDecision{Action: gratitudeMatchCreate, Key: key, Tier: 1}, nil
	}

	all, err := r.store.ReadGratitudeAll()
	if err != nil {
		return gratitudeMatchDecision{}, fmt.Errorf("could not read the gratitude tally; nothing was saved: %w", err)
	}
	live := make([]observations.GratitudeEntry, 0, len(all))
	for _, e := range all {
		if !e.IsTombstone() {
			live = append(live, e)
		}
	}

	m := r.gratitudeMatchConfig()
	cands := observations.Tier2(thing, live)
	band := observations.ClassifyGratitudeBand(cands, observations.GratitudeBandCutoffs{
		High:   m.Tier2High,
		Margin: m.Tier2Margin,
		Floor:  m.AmbiguousFloor,
	})
	switch band {
	case observations.GratitudeBandHigh:
		return gratitudeMatchDecision{
			Action: gratitudeMatchBump, Key: cands[0].Key, Tier: 2, Score: cands[0].Score, Band: band,
		}, nil
	case observations.GratitudeBandAmbiguous:
		return gratitudeMatchDecision{
			Action: gratitudeMatchSuggest, Tier: 2, Band: band,
			Candidates: observations.GratitudeSuggestions(cands, m.AmbiguousFloor),
		}, nil
	case observations.GratitudeBandLow:
		return gratitudeMatchDecision{Action: gratitudeMatchCreate, Key: key, Tier: 2, Band: band}, nil
	default:
		return gratitudeMatchDecision{}, fmt.Errorf("gratitude match: unknown band %q; nothing was saved", band)
	}
}

// forwardGratitudeTombstone resolves a tier-1 key that names a merge tombstone to
// the live entry it was folded into (gratitude.md §7.1; ADR-0012 §11): a wording
// merged away last month now lands on its destination — joining that entry's
// aka[] — instead of refusing. The redirect graph is single-hop by invariant, so
// a destination that is missing or itself a tombstone is a broken store, reported
// as a clean error that writes nothing rather than chased further.
func (r *Router) forwardGratitudeTombstone(tomb observations.GratitudeEntry) (gratitudeMatchDecision, error) {
	dst, found, err := r.store.ReadGratitude(tomb.RedirectTo)
	if err != nil {
		return gratitudeMatchDecision{}, fmt.Errorf("could not read the gratitude entry; nothing was saved: %w", err)
	}
	if !found || dst.IsTombstone() {
		return gratitudeMatchDecision{}, fmt.Errorf(
			"gratitude entry %q was merged into %q, which is not a live entry; nothing was saved",
			tomb.Key, tomb.RedirectTo,
		)
	}
	return gratitudeMatchDecision{
		Action: gratitudeMatchBump, Key: dst.Key, Tier: 1, ForwardedFrom: tomb.Key,
	}, nil
}

// GratitudeSuggestionCandidate is one entry an ambiguous-band suggestion offers
// (gratitude.md §7.4): its stable id, its display wording, and its score.
type GratitudeSuggestionCandidate struct {
	ID    string  `json:"id"`
	Thing string  `json:"thing"`
	Score float64 `json:"score"`
}

// GratitudeSuggestionError is the ambiguous-band outcome of a plain `add`
// (gratitude.md §7.4): the phrase is close to one or more existing entries but
// not a clear single match, so the add neither merges nor creates — it writes
// nothing and defers to the caller, who resolves it by re-running with an
// explicit `--into <id>` (bump) or `--new` (create). It is an error so a
// non-interactive caller exits non-zero with nothing saved; it carries the
// structured suggestion — the band, the tier whose band produced it, and the
// candidates best first, at most three — so a surface can render or prompt from
// the fields rather than parse the sentence.
type GratitudeSuggestionError struct {
	Thing      string
	Band       observations.GratitudeBand
	MatchTier  int
	Candidates []GratitudeSuggestionCandidate
}

// Error renders the suggestion as one user-facing sentence naming each candidate
// and the two ways to resolve it, confirming nothing was saved.
func (e *GratitudeSuggestionError) Error() string {
	parts := make([]string, 0, len(e.Candidates))
	for _, c := range e.Candidates {
		parts = append(parts, fmt.Sprintf("%s %q (%.2f)", c.ID, c.Thing, c.Score))
	}
	return fmt.Sprintf(
		"%q is close to an entry you already keep: %s — re-run with --into <id> to bump one, or --new to start a new entry; nothing was saved",
		e.Thing, strings.Join(parts, ", "),
	)
}

// suggestionError builds the [GratitudeSuggestionError] for a suggest decision.
func (d gratitudeMatchDecision) suggestionError(thing string) *GratitudeSuggestionError {
	cands := make([]GratitudeSuggestionCandidate, 0, len(d.Candidates))
	for _, c := range d.Candidates {
		cands = append(cands, GratitudeSuggestionCandidate{ID: c.Key, Thing: c.Thing, Score: c.Score})
	}
	return &GratitudeSuggestionError{Thing: thing, Band: d.Band, MatchTier: d.Tier, Candidates: cands}
}
