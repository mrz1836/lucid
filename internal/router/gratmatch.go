package router

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/provider"
)

// gratmatch.go is the gratitude match pipeline (gratitude.md §7.1–§7.6): the
// step behind the v1 canonical-key seam that decides which entry a plain nightly
// `add` lands on. It runs the tiers cheapest and most certain first and stops at
// the first confident answer: tier 1, the canonical key (unchanged from v1, plus
// a single-hop forward through a merge tombstone), then tier 2, the deterministic
// token match over every live entry's wordings, banded by the configured cutoffs
// into bump / suggest / create, then — only when tier 2 is not a clear winner —
// tier 3, the optional by-meaning judge. Tiers 1–2 are model-free (architecture
// P9): the tier-2 scorer and the band rule are pure functions in
// internal/observations, and this file only reads the live set through the
// storage adapter (P3) and applies the decision. Tier 3 reaches a model only
// through the provider seam, sends only wordings, and is never load-bearing: a
// disabled, unreachable, or garbled judge leaves the tier-2 decision standing.

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

// GratitudeTier3Status says what became of the optional by-meaning judge on one
// `add` (gratitude.md §7.6): it answered, it is switched off, or it could not be
// reached or trusted. It is empty when tier 3 was not needed — a tier-1 or clear
// tier-2 landing, `--into`, `--new`, or a tally with nothing to judge against —
// so a v1-shaped add stays v1-shaped.
type GratitudeTier3Status string

// The tier-3 outcomes an add reports (gratitude.md §7.6).
const (
	// GratitudeTier3Used means the judge answered and its scores were banded.
	GratitudeTier3Used GratitudeTier3Status = "used"
	// GratitudeTier3Disabled means gratitude.match.tier3_enabled is off.
	GratitudeTier3Disabled GratitudeTier3Status = "disabled"
	// GratitudeTier3Unavailable means no judge was reachable, the call failed or
	// timed out, or its reply could not be trusted; the tier-2 decision stood.
	GratitudeTier3Unavailable GratitudeTier3Status = "unavailable"
)

// gratitudeMatchIntent is the audit label on the tier-3 judge request
// (gratitude.md §7.5; agent-contracts.md §"Note — the gratitude match reach").
const gratitudeMatchIntent = "gratitude.match"

// gratitudeJudgeSystem is the fixed instruction framing the tier-3 judge
// (gratitude.md §7.5). It carries no Ledger data — the phrase and the candidate
// wordings travel in the one user message — and its examples are synthetic. It
// asks only for scores; the band rule decides.
const gratitudeJudgeSystem = `You compare a new gratitude phrase with a numbered list of things a person already keeps a gratitude tally for. Each item gives one or more wordings of the same thing.

Decide which items name the SAME thing as the new phrase: the same object, person, animal, place, activity, or experience, even when the wordings share no words (a short "my bike" can be the same thing as "the two wheels that carry me to work"). An item that is only related, in the same category, or a different thing that merely looks or sounds alike is NOT the same thing ("my dog" is not "my dad"; "morning tea" is not "my morning coffee").

Reply with one JSON object and nothing else — no code fence, no markdown, no commentary:
{"matches": [{"n": <item number>, "score": <number from 0 to 1>}]}
List only the items you judge to be the same thing; score is your confidence that it is the same thing (1 means certainly the same). If no item is the same thing, reply {"matches": []}.`

// errGratitudeJudgeReply marks a tier-3 reply that decoded but broke the
// contract (gratitude.md §7.5): no matches list, an unknown or repeated position,
// or a score outside [0, 1]. Such a reply is never partially trusted.
var errGratitudeJudgeReply = errors.New("gratitude judge: reply breaks the match contract")

// gratitudeMatchDecision is the outcome of the match pipeline for one phrase: a
// Bump of a live entry, a Suggest of ranked candidates, or a Create at a fresh
// canonical key (gratitude.md §7.3). Tier names the tier whose answer decided
// (0 for an `--into` bump, where no matching runs); Score and Band carry that
// tier's evidence. MakePrimary is true only for a direct tier-1 hit, the one
// landing that refreshes display_name exactly as v1 did — every other bump keeps
// the canonical display and records tonight's wording into aka[]. Tier3 reports
// what became of the by-meaning judge, empty when it was not needed.
type gratitudeMatchDecision struct {
	Action      gratitudeMatchAction
	Key         string
	Tier        int
	Score       float64
	Band        observations.GratitudeBand
	Candidates  []observations.GratitudeCandidate
	MakePrimary bool
	Tier3       GratitudeTier3Status
	// ForwardedFrom names the merge tombstone a tier-1 key resolved through
	// (gratitude.md §7.1, ADR-0012 §11); empty on every other decision.
	ForwardedFrom string
}

// automatic reports whether the decision is an automatic by-wording (tier 2) or
// by-meaning (tier 3) bump — the landing the occurrence and the ack attribute to
// its tier (gratitude.md §7.4 High band). A tier-1 bump is the v1 canonical key and
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
// and no model is ever consulted. A key naming a merge tombstone forwards its
// single hop to the live destination. Only when no record holds the key does
// matching continue: with skipFuzzy (`--new`) the phrase creates at that key;
// otherwise tier 2 scores it against every live entry and a clear tier-2 winner
// (High) is bumped with no model call. When tier 2 is Ambiguous or Low, tier 3 —
// the optional by-meaning judge — gets its say ([Router.judgeGratitudeMatch]);
// judge is the provider it reaches through, nil when none could be built.
func (r *Router) matchGratitude(ctx context.Context, thing string, skipFuzzy bool, judge provider.Provider) (gratitudeMatchDecision, error) {
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

	live, err := r.liveGratitude()
	if err != nil {
		return gratitudeMatchDecision{}, fmt.Errorf("could not read the gratitude tally; nothing was saved: %w", err)
	}

	cands := observations.Tier2(thing, live)
	dec, err := bandGratitudeDecision(key, 2, cands, gratitudeTierCutoffs(r.gratitudeMatchConfig(), 2))
	if err != nil || dec.Action == gratitudeMatchBump {
		return dec, err
	}
	return r.judgeGratitudeMatch(ctx, thing, dec, cands, live, judge)
}

// liveGratitude reads the whole tally and drops the merge tombstones — the live
// set every tier-2 and tier-3 match is scored against (gratitude.md §7.1).
func (r *Router) liveGratitude() ([]observations.GratitudeEntry, error) {
	all, err := r.store.ReadGratitudeAll()
	if err != nil {
		return nil, err
	}
	live := make([]observations.GratitudeEntry, 0, len(all))
	for _, e := range all {
		if !e.IsTombstone() {
			live = append(live, e)
		}
	}
	return live, nil
}

// gratitudeTierCutoffs returns one tier's band thresholds from the effective
// gratitude.match knobs (gratitude.md §7.2): tier 2's or tier 3's own high cutoff
// and top-1/top-2 margin, and the ambiguous floor both tiers share. Every tier is
// banded by the one pure rule ([observations.ClassifyGratitudeBand]); only these
// numbers differ, so a tier-3 match reaches the automatic High band under the
// same margin rule as tier 2, with its own, stricter defaults.
func gratitudeTierCutoffs(m config.GratitudeMatchConfig, tier int) observations.GratitudeBandCutoffs {
	if tier >= 3 {
		return observations.GratitudeBandCutoffs{High: m.Tier3High, Margin: m.Tier3Margin, Floor: m.AmbiguousFloor}
	}
	return observations.GratitudeBandCutoffs{High: m.Tier2High, Margin: m.Tier2Margin, Floor: m.AmbiguousFloor}
}

// bandGratitudeDecision turns one tier's ranked candidates into a decision by
// the shared band rule (gratitude.md §7.2): High bumps the clear winner,
// Ambiguous suggests the candidates at or above the floor, Low creates at key.
func bandGratitudeDecision(
	key string, tier int, cands []observations.GratitudeCandidate, c observations.GratitudeBandCutoffs,
) (gratitudeMatchDecision, error) {
	band := observations.ClassifyGratitudeBand(cands, c)
	switch band {
	case observations.GratitudeBandHigh:
		return gratitudeMatchDecision{
			Action: gratitudeMatchBump, Key: cands[0].Key, Tier: tier, Score: cands[0].Score, Band: band,
		}, nil
	case observations.GratitudeBandAmbiguous:
		return gratitudeMatchDecision{
			Action: gratitudeMatchSuggest, Tier: tier, Band: band,
			Candidates: observations.GratitudeSuggestions(cands, c.Floor),
		}, nil
	case observations.GratitudeBandLow:
		return gratitudeMatchDecision{Action: gratitudeMatchCreate, Key: key, Tier: tier, Band: band}, nil
	default:
		return gratitudeMatchDecision{}, fmt.Errorf("gratitude match: unknown band %q; nothing was saved", band)
	}
}

// judgeGratitudeMatch is tier 3 (gratitude.md §7.3 steps 3–4, §7.6): it runs
// only after tier 2 came back Ambiguous or Low (tier2 is that decision, tier2Cands
// its ranked candidates) and returns the decision the add acts on.
//
// With nothing live to judge against, tier 3 is not needed and tier 2 stands
// untouched. Disabled, or with no judge reachable, tier 2 stands and the status
// says why. Otherwise one bounded call scores the phrase against the live list
// ([gratitudeJudgeSlate] — semantic retrieval over the whole tally, never tier
// 2's lexical shortlist, so a zero-overlap match is reachable), and the scores
// are banded with the tier-3 cutoffs: High bumps the tier-3 winner, Ambiguous
// suggests the tier-3 candidates, and Low falls back to the tier-2 band — the
// safer-outcome rule, so tier 3 can promote an add to a match but can never turn
// a tier-2 ambiguity into a silent create. Any provider error or untrustworthy
// reply degrades to tier 2 (P9); only the caller's own cancellation aborts the
// add, with nothing saved.
func (r *Router) judgeGratitudeMatch(
	ctx context.Context, thing string, tier2 gratitudeMatchDecision,
	tier2Cands []observations.GratitudeCandidate, live []observations.GratitudeEntry, judge provider.Provider,
) (gratitudeMatchDecision, error) {
	if len(live) == 0 {
		return tier2, nil
	}
	m := r.gratitudeMatchConfig()
	if !m.Tier3Enabled {
		tier2.Tier3 = GratitudeTier3Disabled
		return tier2, nil
	}
	if judge == nil {
		tier2.Tier3 = GratitudeTier3Unavailable
		return tier2, nil
	}

	cands, err := judgeGratitude(ctx, judge, thing, gratitudeJudgeSlate(tier2Cands, live, m.Tier3MaxCandidates))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return gratitudeMatchDecision{}, fmt.Errorf("gratitude add was canceled; nothing was saved: %w", ctxErr)
		}
		tier2.Tier3 = GratitudeTier3Unavailable
		return tier2, nil
	}

	dec, err := bandGratitudeDecision(tier2.Key, 3, cands, gratitudeTierCutoffs(m, 3))
	if err != nil {
		return gratitudeMatchDecision{}, err
	}
	if dec.Action == gratitudeMatchCreate {
		dec = tier2 // tier-3 Low: the tier-2 band stands
	}
	dec.Tier3 = GratitudeTier3Used
	return dec, nil
}

// gratitudeJudgeSlate picks and orders the live entries one tier-3 call carries
// (gratitude.md §7.5): entries with any tier-2 score first, best first, then the
// most recently tallied, then by stable key — at most limit of them. The whole
// live list fits under the default cap, so the cap only bites on a very large
// tally, where it keeps the entries most likely to matter.
func gratitudeJudgeSlate(
	tier2 []observations.GratitudeCandidate, live []observations.GratitudeEntry, limit int,
) []observations.GratitudeEntry {
	score := make(map[string]float64, len(tier2))
	for _, c := range tier2 {
		score[c.Key] = c.Score
	}
	last := make(map[string]string, len(live))
	for _, e := range live {
		last[e.Key] = e.Tally().Last
	}
	slate := slices.Clone(live)
	slices.SortStableFunc(slate, func(x, y observations.GratitudeEntry) int {
		if c := cmp.Compare(score[y.Key], score[x.Key]); c != 0 {
			return c
		}
		if c := cmp.Compare(last[y.Key], last[x.Key]); c != 0 {
			return c
		}
		return cmp.Compare(x.Key, y.Key)
	})
	if limit > 0 && len(slate) > limit {
		slate = slate[:limit]
	}
	return slate
}

// gratitudeJudgeInput is the one user message the tier-3 judge receives: the new
// phrase and each candidate's wordings, numbered positionally (gratitude.md
// §7.5). It is the whole egress — no ids or keys, counts, dates, people links, or
// journal text.
type gratitudeJudgeInput struct {
	Phrase string               `json:"phrase"`
	Items  []gratitudeJudgeItem `json:"items"`
}

// gratitudeJudgeItem is one numbered candidate: its position and its wordings.
type gratitudeJudgeItem struct {
	N        int      `json:"n"`
	Wordings []string `json:"wordings"`
}

// gratitudeJudgeReply is the judge's answer, `{"matches": [{"n", "score"}]}`.
// Pointers tell a missing key from a zero, so a reply that omits one is
// rejected rather than read as a zero.
type gratitudeJudgeReply struct {
	Matches *[]gratitudeJudgeMatch `json:"matches"`
}

// gratitudeJudgeMatch is one scored position in the judge's reply.
type gratitudeJudgeMatch struct {
	N     *int     `json:"n"`
	Score *float64 `json:"score"`
}

// gratitudeJudgeRequest builds the bounded tier-3 request (gratitude.md §7.5):
// the fixed instruction and one user message carrying only the phrase and the
// slate's wordings, numbered from 1 in slate order.
func gratitudeJudgeRequest(phrase string, slate []observations.GratitudeEntry) (provider.Request, error) {
	in := gratitudeJudgeInput{Phrase: phrase, Items: make([]gratitudeJudgeItem, 0, len(slate))}
	for i, e := range slate {
		in.Items = append(in.Items, gratitudeJudgeItem{N: i + 1, Wordings: gratitudeWordings(e)})
	}
	body, err := json.Marshal(in)
	if err != nil {
		return provider.Request{}, fmt.Errorf("gratitude judge: marshal request: %w", err)
	}
	return provider.Request{
		Intent:   gratitudeMatchIntent,
		System:   gratitudeJudgeSystem,
		Messages: []provider.Message{{Role: provider.RoleUser, Content: string(body)}},
	}, nil
}

// gratitudeWordings lists an entry's wordings for the judge: its display_name,
// then each aka[] form, trimmed, blanks and repeats dropped.
func gratitudeWordings(e observations.GratitudeEntry) []string {
	out := make([]string, 0, len(e.Aka)+1)
	for _, w := range append([]string{e.DisplayName}, e.Aka...) {
		w = strings.TrimSpace(w)
		if w != "" && !slices.Contains(out, w) {
			out = append(out, w)
		}
	}
	return out
}

// judgeGratitude asks the judge which slate entries name the same thing as
// phrase and returns them as ranked candidates. A transport error is returned
// as-is; a reply that is not the JSON object — alone, or inside one enclosing
// markdown code fence ([unfenceGratitudeReply]) — or that decodes but breaks the
// contract — no matches list, a position outside the slate or repeated, a
// missing score, or a score outside [0, 1] — is [errGratitudeJudgeReply]. Every
// error means "untrusted": the caller degrades.
func judgeGratitude(
	ctx context.Context, p provider.Provider, phrase string, slate []observations.GratitudeEntry,
) ([]observations.GratitudeCandidate, error) {
	req, err := gratitudeJudgeRequest(phrase, slate)
	if err != nil {
		return nil, err
	}
	resp, err := p.Complete(ctx, req)
	if err != nil {
		return nil, err
	}
	var reply gratitudeJudgeReply
	if err = json.Unmarshal([]byte(unfenceGratitudeReply(resp.Content)), &reply); err != nil {
		return nil, fmt.Errorf("%w: not the JSON object: %w", errGratitudeJudgeReply, err)
	}
	if reply.Matches == nil {
		return nil, fmt.Errorf("%w: no matches list", errGratitudeJudgeReply)
	}
	seen := make(map[int]bool, len(*reply.Matches))
	cands := make([]observations.GratitudeCandidate, 0, len(*reply.Matches))
	for _, mt := range *reply.Matches {
		if mt.N == nil || mt.Score == nil {
			return nil, fmt.Errorf("%w: a match is missing its position or score", errGratitudeJudgeReply)
		}
		n, score := *mt.N, *mt.Score
		if n < 1 || n > len(slate) || seen[n] {
			return nil, fmt.Errorf("%w: position %d is unknown or repeated", errGratitudeJudgeReply, n)
		}
		if score < 0 || score > 1 {
			return nil, fmt.Errorf("%w: score %v is outside [0, 1]", errGratitudeJudgeReply, score)
		}
		seen[n] = true
		e := slate[n-1]
		cands = append(cands, observations.GratitudeCandidate{Key: e.Key, Thing: e.DisplayName, Score: score})
	}
	observations.RankGratitudeCandidates(cands)
	return cands, nil
}

// unfenceGratitudeReply trims a judge reply and, when the whole reply is one
// markdown code fence (an opening ``` or ```json line, a closing ``` line),
// returns just what is inside it — some hosted models fence JSON by habit even
// when told not to (gratitude.md §7.5). Anything else is returned trimmed and
// otherwise untouched, so prose, a thinking preamble, or an unclosed fence
// around the object still fails to decode and the reply is untrusted.
func unfenceGratitudeReply(content string) string {
	s := strings.TrimSpace(content)
	body, ok := strings.CutPrefix(s, "```")
	if !ok {
		return s
	}
	firstLine, rest, ok := strings.Cut(body, "\n")
	if !ok || (firstLine != "" && firstLine != "json") {
		return s
	}
	inner, ok := strings.CutSuffix(strings.TrimSpace(rest), "```")
	if !ok {
		return s
	}
	return strings.TrimSpace(inner)
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
// structured suggestion — the band, the tier whose band produced it, the
// candidates best first (at most three), and what became of the by-meaning judge
// — so a surface can render or prompt from the fields rather than parse the
// sentence.
type GratitudeSuggestionError struct {
	Thing      string
	Band       observations.GratitudeBand
	MatchTier  int
	Candidates []GratitudeSuggestionCandidate
	Tier3      GratitudeTier3Status
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
	return &GratitudeSuggestionError{Thing: thing, Band: d.Band, MatchTier: d.Tier, Candidates: cands, Tier3: d.Tier3}
}
