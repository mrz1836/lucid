package router

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/provider"
)

// gratreconcile.go is `lucid gratitude reconcile` (gratitude.md §7.9): the pass
// that looks for duplicates already in the tally — two entries that name the
// same thing — and proposes folding one into the other. It is dry-run by
// default and writes nothing; `--apply` is the explicit confirmation, and it
// folds only the deterministic tier-2 High pairs, each through the ordinary
// merge path (merge event + redirect tombstone, single-hop kept), one receipt
// per fold. The tier-2 pass is a pure function of the store
// ([observations.Tier2Pairs], [observations.BandGratitudePairs]), so the set a
// dry run marks is exactly the set `--apply` folds. The optional tier-3 pass
// asks the by-meaning judge for pairs; its pairs are always advisory — a
// model's answer can differ between the dry run a person read and the run that
// applies — so it runs on a dry run only and never feeds `--apply` (P9: an
// apply never needs a model).

// gratitudeReconcileIntent is the audit label on the tier-3 reconcile request
// (gratitude.md §7.9; agent-contracts.md §"Note — the gratitude match reach").
const gratitudeReconcileIntent = "gratitude.reconcile"

// gratitudeReconcileSystem is the fixed instruction framing the tier-3 reconcile
// judge (gratitude.md §7.9). Like the add-time judge's it carries no Ledger
// data — the numbered wordings travel in the one user message — its examples
// are synthetic, and it asks only for scored pairs; the band rule decides.
const gratitudeReconcileSystem = `You compare a numbered list of things a person keeps a gratitude tally for. Each item gives one or more wordings of one thing.

Find the pairs of items that name the SAME thing: the same object, person, animal, place, activity, or experience, even when the wordings share no words (a short "my bike" can be the same thing as "the two wheels that carry me to work"). Two items that are only related, in the same category, or different things that merely look or sound alike are NOT the same thing ("my dog" is not "my dad"; "morning tea" is not "my morning coffee").

Reply with one JSON object and nothing else — no code fence, no markdown, no commentary:
{"pairs": [{"a": <item number>, "b": <item number>, "score": <number from 0 to 1>}]}
List only the pairs you judge to be the same thing, each pair once; score is your confidence that the two items are the same thing (1 means certainly the same). If no two items are the same thing, reply {"pairs": []}.`

// ReconcileGratitudeRequest is one `lucid gratitude reconcile` turn
// (gratitude.md §7.9): Apply is the explicit `--apply` confirmation, Provider the
// optional tier-3 judge a dry run reaches through (nil when none could be built;
// never consulted when Apply is set), and Now the fold time, injected so
// receipts are deterministic in tests.
type ReconcileGratitudeRequest struct {
	Apply    bool
	Provider provider.Provider
	Now      time.Time
}

// GratitudeReconcileProposal is one proposed fold (gratitude.md §7.9): the
// source entry that would fold into the target, both with their display
// wordings, the pair's score, the tier and band that found it, whether
// `--apply` folds it (tier-2 High only), and the exact merge command that folds
// it by hand.
type GratitudeReconcileProposal struct {
	Source      string                     `json:"source"`
	SourceThing string                     `json:"source_thing"`
	Target      string                     `json:"target"`
	TargetThing string                     `json:"target_thing"`
	Score       float64                    `json:"score"`
	MatchTier   int                        `json:"match_tier"`
	Band        observations.GratitudeBand `json:"band"`
	WillApply   bool                       `json:"will_apply"`
	Command     string                     `json:"command"`
}

// GratitudeReconcileFold is one fold `--apply` made: the merge event's receipt,
// the source folded away, and the target it folded into.
type GratitudeReconcileFold struct {
	Receipt string `json:"receipt"`
	Source  string `json:"source"`
	Target  string `json:"target"`
}

// GratitudeReconcileView is the `lucid gratitude reconcile --json` payload
// (gratitude.md §7.9): the proposals, what became of the tier-3 pass (omitted
// when it was not consulted — an `--apply`, or a tally with fewer than two live
// entries), and the folds applied. Proposals and Applied are never null;
// Applied is [] on a dry run.
type GratitudeReconcileView struct {
	Proposals []GratitudeReconcileProposal `json:"proposals"`
	Tier3     GratitudeTier3Status         `json:"tier3,omitempty"`
	Applied   []GratitudeReconcileFold     `json:"applied"`
}

// GratitudeReconcileResult carries the machine view and the human-first lines
// the CLI shares through emit (ADR-0007).
type GratitudeReconcileResult struct {
	View  GratitudeReconcileView
	Lines []string
}

// ReconcileGratitude scans the live tally for likely duplicates and proposes
// folds (gratitude.md §7.9). Every live entry is paired with every other and
// scored by tier 2; the pairs are banded from both sides with the tier-2
// cutoffs. On a dry run, when tier 3 is enabled and reachable, one bounded
// judge call adds by-meaning pairs, banded with the tier-3 cutoffs; any provider
// error or untrustworthy reply leaves the tier-2 proposals standing (P9), and
// only the caller's own cancellation aborts. Each entry appears in at most one
// proposal ([selectGratitudeProposals]), and the lower-count entry of a pair
// folds into the higher ([gratitudeFoldDirection]).
//
// Without Apply it writes nothing. With Apply it folds each tier-2 High
// proposal through [Router.MergeGratitude] — the merge event, the redirect
// tombstone, the single-hop invariant, one receipt per fold — and never an
// advisory one. A fold that fails stops the pass with a clean error; the folds
// before it have landed, each receipted.
func (r *Router) ReconcileGratitude(ctx context.Context, req ReconcileGratitudeRequest) (GratitudeReconcileResult, error) {
	now := whenOr(req.Now)
	live, err := r.liveGratitude()
	if err != nil {
		return GratitudeReconcileResult{}, fmt.Errorf("could not read the gratitude tally; nothing was changed: %w", err)
	}
	m := r.gratitudeMatchConfig()

	raw := observations.Tier2Pairs(live)
	tier2 := observations.BandGratitudePairs(raw, gratitudeTierCutoffs(m, 2))

	var (
		tier3  []observations.GratitudePair
		status GratitudeTier3Status
	)
	if !req.Apply && len(live) > 1 {
		tier3, status, err = r.judgeGratitudePairs(ctx, live, raw, m, req.Provider)
		if err != nil {
			return GratitudeReconcileResult{}, err
		}
	}

	view := GratitudeReconcileView{
		Proposals: selectGratitudeProposals(tier2, tier3, live),
		Tier3:     status,
		Applied:   []GratitudeReconcileFold{},
	}
	if !req.Apply {
		return GratitudeReconcileResult{View: view, Lines: gratitudeReconcileLines(view, nil, false)}, nil
	}

	toFold := 0
	for _, p := range view.Proposals {
		if p.WillApply {
			toFold++
		}
	}
	acks := make([]string, 0, toFold)
	for _, p := range view.Proposals {
		if !p.WillApply {
			continue
		}
		res, merr := r.MergeGratitude(GratitudeMergeRequest{Source: p.Source, Target: p.Target, Now: now})
		if merr != nil {
			return GratitudeReconcileResult{}, fmt.Errorf(
				"gratitude reconcile stopped after %d of %d folds (each fold before it landed with its receipt): %w",
				len(view.Applied), toFold, merr,
			)
		}
		view.Applied = append(view.Applied, GratitudeReconcileFold{Receipt: res.Receipt, Source: p.Source, Target: p.Target})
		acks = append(acks, res.Ack)
	}
	return GratitudeReconcileResult{View: view, Lines: gratitudeReconcileLines(view, acks, true)}, nil
}

// judgeGratitudePairs is reconcile's tier-3 pass (gratitude.md §7.9): disabled,
// or with no judge reachable, it adds no pairs and the status says why;
// otherwise one bounded call scores pairs over the live list — the same slate
// rule as the add-time judge ([gratitudeJudgeSlate]; an entry's tier-2 evidence
// is its best pair score), with every entry linked to an off-limits person
// withheld ([Router.judgeableGratitude]) — and the pairs are banded with the
// tier-3 cutoffs.
// Any provider error or untrustworthy reply degrades to no tier-3 pairs; only
// the caller's own cancellation is returned as an error.
func (r *Router) judgeGratitudePairs(
	ctx context.Context, live []observations.GratitudeEntry, raw []observations.GratitudePair,
	m config.GratitudeMatchConfig, judge provider.Provider,
) ([]observations.GratitudePair, GratitudeTier3Status, error) {
	if !m.Tier3Enabled {
		return nil, GratitudeTier3Disabled, nil
	}
	if judge == nil {
		return nil, GratitudeTier3Unavailable, nil
	}

	slate := gratitudeReconcileSlate(r.judgeableGratitude(live), raw, m.Tier3MaxCandidates)
	if len(slate) < 2 {
		return nil, "", nil // a capped or withheld slate of one has no pair to judge
	}

	pairs, err := judgeGratitudeReconcile(ctx, judge, slate)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, "", fmt.Errorf("gratitude reconcile was canceled; nothing was changed: %w", ctxErr)
		}
		return nil, GratitudeTier3Unavailable, nil
	}
	return observations.BandGratitudePairs(pairs, gratitudeTierCutoffs(m, 3)), GratitudeTier3Used, nil
}

// gratitudeReconcileSlate picks and orders the live entries one tier-3
// reconcile call carries, by the add-time judge's slate rule
// ([gratitudeJudgeSlate], gratitude.md §7.5): an entry's tier-2 evidence is its
// best pair score, so entries with any tier-2 pair come first, best first, then
// the most recently tallied, then by stable key — at most limit of them.
func gratitudeReconcileSlate(
	live []observations.GratitudeEntry, raw []observations.GratitudePair, limit int,
) []observations.GratitudeEntry {
	best := make(map[string]float64, len(live))
	for _, p := range raw {
		best[p.A] = max(best[p.A], p.Score)
		best[p.B] = max(best[p.B], p.Score)
	}
	evidence := make([]observations.GratitudeCandidate, 0, len(best))
	for key, score := range best {
		evidence = append(evidence, observations.GratitudeCandidate{Key: key, Score: score})
	}
	return gratitudeJudgeSlate(evidence, live, limit)
}

// gratitudeReconcileInput is the one user message the tier-3 reconcile judge
// receives: each slate entry's wordings, numbered positionally (gratitude.md
// §7.9, the §7.5 slice rules). It is the whole egress — no ids or keys, counts,
// dates, people links, or journal text.
type gratitudeReconcileInput struct {
	Items []gratitudeJudgeItem `json:"items"`
}

// gratitudeReconcileReply is the judge's answer, `{"pairs": [{"a", "b",
// "score"}]}`. Pointers tell a missing key from a zero, so a reply that omits
// one is rejected rather than read as a zero.
type gratitudeReconcileReply struct {
	Pairs *[]gratitudeReconcileMatch `json:"pairs"`
}

// gratitudeReconcileMatch is one scored pair of positions in the judge's reply.
type gratitudeReconcileMatch struct {
	A     *int     `json:"a"`
	B     *int     `json:"b"`
	Score *float64 `json:"score"`
}

// gratitudeReconcileRequest builds the bounded tier-3 reconcile request: the
// fixed instruction and one user message carrying only the slate's wordings,
// numbered from 1 in slate order.
func gratitudeReconcileRequest(slate []observations.GratitudeEntry) (provider.Request, error) {
	in := gratitudeReconcileInput{Items: make([]gratitudeJudgeItem, 0, len(slate))}
	for i, e := range slate {
		in.Items = append(in.Items, gratitudeJudgeItem{N: i + 1, Wordings: gratitudeWordings(e)})
	}
	body, err := json.Marshal(in)
	if err != nil {
		return provider.Request{}, fmt.Errorf("gratitude reconcile judge: marshal request: %w", err)
	}
	return provider.Request{
		Intent:   gratitudeReconcileIntent,
		System:   gratitudeReconcileSystem,
		Messages: []provider.Message{{Role: provider.RoleUser, Content: string(body)}},
	}, nil
}

// judgeGratitudeReconcile asks the judge which slate entries name the same thing
// as each other and returns the scored pairs, unbanded. A transport error is
// returned as-is; a reply that is not the JSON object (alone, or inside one
// enclosing markdown code fence, [unfenceGratitudeReply]) or that breaks the
// contract — no pairs list, a position outside the slate, an item paired with
// itself, a pair listed twice, a missing position or score, or a score outside
// [0, 1] — is [errGratitudeJudgeReply]. Every error means "untrusted": the caller
// degrades.
func judgeGratitudeReconcile(
	ctx context.Context, p provider.Provider, slate []observations.GratitudeEntry,
) ([]observations.GratitudePair, error) {
	req, err := gratitudeReconcileRequest(slate)
	if err != nil {
		return nil, err
	}
	resp, err := p.Complete(ctx, req)
	if err != nil {
		return nil, err
	}
	var reply gratitudeReconcileReply
	if err = json.Unmarshal([]byte(unfenceGratitudeReply(resp.Content)), &reply); err != nil {
		return nil, fmt.Errorf("%w: not the JSON object: %w", errGratitudeJudgeReply, err)
	}
	if reply.Pairs == nil {
		return nil, fmt.Errorf("%w: no pairs list", errGratitudeJudgeReply)
	}
	seen := make(map[[2]int]bool, len(*reply.Pairs))
	pairs := make([]observations.GratitudePair, 0, len(*reply.Pairs))
	for _, mt := range *reply.Pairs {
		if mt.A == nil || mt.B == nil || mt.Score == nil {
			return nil, fmt.Errorf("%w: a pair is missing a position or its score", errGratitudeJudgeReply)
		}
		a, b, score := *mt.A, *mt.B, *mt.Score
		if a < 1 || a > len(slate) || b < 1 || b > len(slate) || a == b {
			return nil, fmt.Errorf("%w: pair (%d, %d) names an unknown position or pairs an item with itself",
				errGratitudeJudgeReply, a, b)
		}
		id := [2]int{min(a, b), max(a, b)}
		if seen[id] {
			return nil, fmt.Errorf("%w: pair (%d, %d) is repeated", errGratitudeJudgeReply, a, b)
		}
		if score < 0 || score > 1 {
			return nil, fmt.Errorf("%w: score %v is outside [0, 1]", errGratitudeJudgeReply, score)
		}
		seen[id] = true
		pairs = append(pairs, observations.NewGratitudePair(slate[a-1].Key, slate[b-1].Key, score))
	}
	return pairs, nil
}

// selectGratitudeProposals turns the banded pairs into proposals, each entry in
// at most one (gratitude.md §7.9), so an applied set never chains a fold through
// a tombstone it just made and every printed merge command still runs after the
// others. The tier-2 High pairs — the set `--apply` folds — are placed first, so
// no advisory pair can crowd one out and the applied set stays a pure function
// of the store; the advisory pairs (tier-2 Ambiguous and every tier-3 pair)
// follow, highest score first, tier 2 before tier 3 on a tie, then by key.
func selectGratitudeProposals(
	tier2, tier3 []observations.GratitudePair, live []observations.GratitudeEntry,
) []GratitudeReconcileProposal {
	type tiered struct {
		pair observations.GratitudePair
		tier int
	}
	var fold, advisory []tiered
	for _, p := range tier2 {
		if p.Band == observations.GratitudeBandHigh {
			fold = append(fold, tiered{pair: p, tier: 2})
		} else {
			advisory = append(advisory, tiered{pair: p, tier: 2})
		}
	}
	for _, p := range tier3 {
		advisory = append(advisory, tiered{pair: p, tier: 3})
	}
	slices.SortStableFunc(advisory, func(x, y tiered) int {
		if c := cmp.Compare(y.pair.Score, x.pair.Score); c != 0 {
			return c
		}
		if c := cmp.Compare(x.tier, y.tier); c != 0 {
			return c
		}
		if c := cmp.Compare(x.pair.A, y.pair.A); c != 0 {
			return c
		}
		return cmp.Compare(x.pair.B, y.pair.B)
	})

	byKey := make(map[string]observations.GratitudeEntry, len(live))
	for _, e := range live {
		byKey[e.Key] = e
	}
	used := make(map[string]bool)
	out := make([]GratitudeReconcileProposal, 0, len(fold)+len(advisory))
	for _, t := range append(fold, advisory...) {
		if used[t.pair.A] || used[t.pair.B] {
			continue
		}
		used[t.pair.A], used[t.pair.B] = true, true
		src, dst := gratitudeFoldDirection(byKey[t.pair.A], byKey[t.pair.B])
		out = append(out, GratitudeReconcileProposal{
			Source:      src.Key,
			SourceThing: src.DisplayName,
			Target:      dst.Key,
			TargetThing: dst.DisplayName,
			Score:       t.pair.Score,
			MatchTier:   t.tier,
			Band:        t.pair.Band,
			WillApply:   t.tier == 2 && t.pair.Band == observations.GratitudeBandHigh,
			Command:     fmt.Sprintf("lucid gratitude merge %s %s", src.Key, dst.Key),
		})
	}
	return out
}

// gratitudeFoldDirection decides which entry of a pair folds into which
// (gratitude.md §7.9): the lower count folds into the higher, so the fold keeps
// the entry you have returned to most; on a tie the later-created folds into the
// earlier; and on a tie of that too, the greater key into the lesser.
func gratitudeFoldDirection(x, y observations.GratitudeEntry) (source, target observations.GratitudeEntry) {
	if cx, cy := x.Tally().Count, y.Tally().Count; cx != cy {
		if cx < cy {
			return x, y
		}
		return y, x
	}
	if c := compareCreated(x.CreatedAt, y.CreatedAt); c != 0 {
		if c > 0 {
			return x, y
		}
		return y, x
	}
	if x.Key > y.Key {
		return x, y
	}
	return y, x
}

// compareCreated orders two created_at stamps by the instant they name, so two
// offsets compare correctly; a stamp that does not parse falls back to a string
// comparison, which is still deterministic.
func compareCreated(x, y string) int {
	tx, errX := time.Parse(time.RFC3339, x)
	ty, errY := time.Parse(time.RFC3339, y)
	if errX != nil || errY != nil {
		return cmp.Compare(x, y)
	}
	return tx.Compare(ty)
}

// gratitudeReconcileLines renders the human-first reconcile report. A dry run
// lists each proposal — "fold" for a pair `--apply` folds, "look" for an
// advisory one, with its merge command — and how to apply; an applied run lists
// each fold's merge ack, then the advisory pairs left for a person to judge.
// When the by-meaning pass was off or could not be reached, the report says the
// proposals are by wording only.
func gratitudeReconcileLines(view GratitudeReconcileView, acks []string, applied bool) []string {
	var lines []string
	var marked, advisory []GratitudeReconcileProposal
	for _, p := range view.Proposals {
		if p.WillApply {
			marked = append(marked, p)
		} else {
			advisory = append(advisory, p)
		}
	}

	switch {
	case applied && len(acks) > 0:
		lines = append(lines, fmt.Sprintf("Folded %d duplicate%s:", len(acks), plural2(len(acks))))
		for _, ack := range acks {
			lines = append(lines, "  "+ack)
		}
		if len(advisory) > 0 {
			lines = append(lines, "Worth a look, not folded:")
			lines = append(lines, gratitudeProposalLines(advisory)...)
		}
	case applied:
		lines = append(lines, "Nothing to fold — no pair is a clear match by wording.")
		if len(advisory) > 0 {
			lines = append(lines, "Worth a look, not folded:")
			lines = append(lines, gratitudeProposalLines(advisory)...)
		}
	case len(view.Proposals) == 0:
		lines = append(lines, "No likely duplicates in the gratitude tally — nothing to fold.")
	default:
		n := len(view.Proposals)
		lines = append(lines, fmt.Sprintf("%d possible duplicate%s (dry run — nothing was changed):", n, plural2(n)))
		lines = append(lines, gratitudeProposalLines(view.Proposals)...)
		if len(marked) > 0 {
			lines = append(lines,
				"`lucid gratitude reconcile --apply` folds the pairs marked fold — take a `lucid backup` first.")
		}
		if len(advisory) > 0 {
			lines = append(lines, "A pair marked look is never folded for you; run its merge if it is the same thing.")
		}
	}

	switch view.Tier3 {
	case GratitudeTier3Disabled:
		lines = append(lines, "Meaning match is off — proposed by wording only.")
	case GratitudeTier3Unavailable:
		lines = append(lines, "Meaning match unavailable — proposed by wording only.")
	case GratitudeTier3Used, GratitudeTier3OffLimits:
	}
	return lines
}

// gratitudeProposalLines renders proposals, one line each — marked fold or look,
// the source and the target it would fold into, and how and how strongly it
// matched — with an advisory pair's merge command on the line below.
func gratitudeProposalLines(proposals []GratitudeReconcileProposal) []string {
	lines := make([]string, 0, 2*len(proposals))
	for _, p := range proposals {
		mark, how := "look", "by wording"
		if p.WillApply {
			mark = "fold"
		}
		if p.MatchTier >= 3 {
			how = "by meaning"
		}
		lines = append(lines, fmt.Sprintf("  %s  %s %q into %s %q — %s, tier %d (%.2f)",
			mark, p.Source, p.SourceThing, p.Target, p.TargetThing, how, p.MatchTier, p.Score))
		if !p.WillApply {
			lines = append(lines, "        "+p.Command)
		}
	}
	return lines
}
