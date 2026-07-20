package router

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/agents/reflection"
	"github.com/mrz1836/lucid/internal/agents/safety"
	"github.com/mrz1836/lucid/internal/frameworks"
	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/storage"
)

// commandReflectWeek is the verb stamped on the Safety session context for the
// weekly deep-dive. It never writes a reflection record — it is the read-only
// `lucid reflect week` surface, distinct from `/reflect`.
const commandReflectWeek = "/reflect week"

// ReflectWeekRequest carries the inputs for one weekly deep-dive. Provider is
// the model boundary the deep-dive reaches through; ActiveLens is the consented
// lens the CLI resolved from config over the embedded framework registry (nil
// for the baseline, lens-neutral voice). The router owns every Ledger read and
// builds the projection-only week bundle.
type ReflectWeekRequest struct {
	Now        time.Time
	Provider   provider.Provider
	ActiveLens *frameworks.Lens
}

// ReflectWeekPattern is the Safety-gated candidate pattern the deep-dive
// surfaced: its (possibly softened) text, the shape_tag, and the raw-entry-id
// citations that back it. It is what the Phase-5 apply path routes through the
// resonance gate; ReflectWeek itself persists nothing.
type ReflectWeekPattern struct {
	ProposalText       string
	ShapeTag           string
	SupportingEntryIDs []string
}

// ReflectWeekResult is the read-only weekly deep-dive: the ISO-week label, the
// six Discord-friendly narrative sections (each line already gated through
// Safety), the optional Safety-gated candidate pattern, and the "<id>
// v<version>" applied-lens label when a lens framed the run. Nothing here is
// persisted — the surface writes no insight, reflection, or raw file.
type ReflectWeekResult struct {
	ISOWeek     string
	Summary     string
	Wins        []string
	Misses      []string
	BodyPain    []string
	HabitChange []string
	NextWeek    []string
	Pattern     *ReflectWeekPattern
	AppliedLens string
}

// ReflectWeek executes the read-only weekly deep-dive. It assembles the
// projection-only week bundle (BuildWeekBundle — sanctuary-safe), runs the
// deep-dive analysis over it framed through the active lens, gates every
// surfaced line through Safety (a block drops the line; a rewrite softens it),
// and returns the narrative plus at most one Safety-gated candidate pattern. It
// writes NOTHING under ~/.lucid/: the proposal pause is read without clearing,
// and no reflection record or insight is created (that is the Phase-5 apply
// path). While a proposal pause is in effect the candidate is suppressed
// entirely — the narrative still surfaces.
func (r *Router) ReflectWeek(ctx context.Context, req ReflectWeekRequest) (ReflectWeekResult, error) {
	now := whenOr(req.Now)

	bundle, err := r.BuildWeekBundle(now)
	if err != nil {
		return ReflectWeekResult{}, fmt.Errorf("reflectweek: build week bundle: %w", err)
	}

	paused, err := r.proposalPausePassive(now)
	if err != nil {
		return ReflectWeekResult{}, err
	}
	rejected, unanswered, err := r.weekShapeDenylist()
	if err != nil {
		return ReflectWeekResult{}, err
	}

	dd := reflection.DeepDive(ctx, reflection.DeepDiveInput{
		ISOWeek:             bundle.ISOWeek,
		Numbers:             weekNumbers(bundle),
		Entries:             toDeepEntries(bundle.RawDigest),
		Signals:             toDeepSignals(bundle.Observations),
		Insights:            toDeepInsights(bundle.AcceptedInsights),
		ActiveLens:          toDeepLens(req.ActiveLens),
		RejectedShapeTags:   rejected,
		UnansweredShapeTags: unanswered,
		AgentVersion:        r.cfg.AgentVersions.Reflection,
	}, req.Provider)

	res := ReflectWeekResult{
		ISOWeek:     bundle.ISOWeek,
		Summary:     r.gateLine(ctx, dd.Summary, req.Provider),
		Wins:        r.gateLines(ctx, dd.Wins, req.Provider),
		Misses:      r.gateLines(ctx, dd.Misses, req.Provider),
		BodyPain:    r.gateLines(ctx, dd.BodyPain, req.Provider),
		HabitChange: r.gateLines(ctx, dd.HabitChange, req.Provider),
		NextWeek:    r.gateLines(ctx, dd.NextWeek, req.Provider),
	}
	if dd.AppliedLens != nil {
		res.AppliedLens = *dd.AppliedLens
	}

	// A candidate is a pattern proposal: suppressed while the pause is in
	// effect, and otherwise only surfaced if Safety clears it (a diagnostic or
	// advice candidate is blocked; a soft overclaim is softened — AC-7).
	if !paused && dd.Candidate != nil {
		if pattern, ok := r.gatePattern(ctx, *dd.Candidate, req.Provider); ok {
			res.Pattern = &pattern
		}
	}
	return res, nil
}

// proposalPausePassive reports whether the silent proposal pause is currently
// in effect WITHOUT mutating it. The mutating check (proposalPauseActive) clears
// an expired pause, which is a write; the read-only weekly surface must never
// write, so it reads the state and evaluates the window itself.
func (r *Router) proposalPausePassive(now time.Time) (bool, error) {
	st, err := r.store.ReadProposalPauseState()
	if err != nil {
		return false, fmt.Errorf("reflectweek: read proposal pause state: %w", err)
	}
	return st.PausedUntil != nil && now.Before(*st.PausedUntil), nil
}

// weekShapeDenylist unions the rejected and unanswered shape tags over the
// recent processed-artifact window (the same read-only source /checkin's propose
// uses), so the deep-dive never re-surfaces a shape the user already rejected or
// let pass. It is a pure read.
func (r *Router) weekShapeDenylist() (rejected, unanswered []string, err error) {
	// An empty currentID excludes nothing: the whole recent window contributes.
	window, err := r.recentWindow("")
	if err != nil {
		return nil, nil, fmt.Errorf("reflectweek: read recent window: %w", err)
	}
	for _, art := range window {
		rejected = append(rejected, storage.RejectedShapeTags(art)...)
		unanswered = append(unanswered, storage.UnansweredShapeTags(art)...)
	}
	return dedupe(rejected), dedupe(unanswered), nil
}

// gateLine runs one narrative line through Safety and returns the surfaced text:
// the line unchanged on pass, the softened text on rewrite, and "" on block (the
// line is dropped rather than surfaced). An empty line is dropped without a
// Safety round-trip, so a clean deep-dive spends no extra model call.
func (r *Router) gateLine(ctx context.Context, text string, p provider.Provider) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	dec := safety.Evaluate(ctx, safety.Candidate{
		FromAgent: safety.FromReflection,
		Intent:    safety.IntentRecall,
		Text:      text,
	}, safety.SessionContext{Command: commandReflectWeek}, p)
	if dec.Decision == safety.Block {
		return ""
	}
	return dec.Text
}

// gateLines gates each line in a section, dropping any Safety blocks and
// keeping the order of the survivors.
func (r *Router) gateLines(ctx context.Context, lines []string, p provider.Provider) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if g := r.gateLine(ctx, l, p); g != "" {
			out = append(out, g)
		}
	}
	return out
}

// gatePattern runs the candidate through Safety as a pattern proposal. It
// carries the shape_tag (required for the propose_pattern structural gate) and
// the supporting ids (for the rewrite citation-preservation check). A block
// drops the candidate; a pass or rewrite surfaces the (possibly softened) text.
func (r *Router) gatePattern(ctx context.Context, c reflection.DeepDiveCandidate, p provider.Provider) (ReflectWeekPattern, bool) {
	dec := safety.Evaluate(ctx, safety.Candidate{
		FromAgent:          safety.FromReflection,
		Intent:             safety.IntentProposePattern,
		Text:               c.ProposalText,
		ShapeTag:           c.ShapeTag,
		SupportingEntryIDs: c.SupportingEntryIDs,
	}, safety.SessionContext{Command: commandReflectWeek}, p)
	if dec.Decision == safety.Block {
		return ReflectWeekPattern{}, false
	}
	return ReflectWeekPattern{
		ProposalText:       dec.Text,
		ShapeTag:           c.ShapeTag,
		SupportingEntryIDs: c.SupportingEntryIDs,
	}, true
}

// weekNumbers renders the honest numbers the deep-dive states, copied from the
// bundle's projections (never recomputed — identity.md: the Mirror copies
// projection numbers). The weekly volume totals sum the per-day rows the bundle
// already carries from the sanctioned `/day` join, exactly as `lucid stats`
// totals them.
func weekNumbers(b WeekBundle) []string {
	var rawTotal, obsTotal int
	for _, d := range b.Stats {
		rawTotal += d.RawEntries
		obsTotal += d.Observations
	}
	return []string{
		fmt.Sprintf("current streak: %d day(s)", b.Metrics.CurrentStreak),
		fmt.Sprintf("longest streak: %d day(s)", b.Metrics.LongestStreak),
		fmt.Sprintf("raw entries this week: %d", rawTotal),
		fmt.Sprintf("body signals this week: %d", obsTotal),
		fmt.Sprintf("accepted insights in window: %d", len(b.AcceptedInsights)),
	}
}

// toDeepEntries maps the bundle's raw digest to the agent's citable-entry view.
func toDeepEntries(digest []RawEntryDigest) []reflection.DeepEntry {
	out := make([]reflection.DeepEntry, 0, len(digest))
	for _, d := range digest {
		out = append(out, reflection.DeepEntry{ID: d.ID, Date: d.Date, Text: d.Text})
	}
	return out
}

// toDeepSignals maps the bundle's observation events to the agent's body-signal
// view: only the kind and the logical day, never the value payload.
func toDeepSignals(events []observations.Event) []reflection.DeepSignal {
	out := make([]reflection.DeepSignal, 0, len(events))
	for _, e := range events {
		out = append(out, reflection.DeepSignal{Kind: string(e.Kind), Date: e.LogicalDate})
	}
	return out
}

// toDeepInsights maps the accepted-insight window to the agent's continuity
// view: id and canonical statement.
func toDeepInsights(insights []storage.Insight) []reflection.DeepInsight {
	out := make([]reflection.DeepInsight, 0, len(insights))
	for _, ins := range insights {
		out = append(out, reflection.DeepInsight{ID: ins.ID, Statement: ins.Body})
	}
	return out
}

// toDeepLens maps a resolved framework lens to the agent's framing view (name +
// "<id> v<version>" label), or nil for the baseline voice.
func toDeepLens(l *frameworks.Lens) *reflection.DeepLens {
	if l == nil {
		return nil
	}
	return &reflection.DeepLens{Name: l.Name, Label: l.Label()}
}
