package router

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/agents/reflection"
	"github.com/mrz1836/lucid/internal/agents/safety"
	"github.com/mrz1836/lucid/internal/isoweek"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/storage"
)

// commandReflect is the verb stamped on /reflect reflection records.
const commandReflect = "/reflect"

// recallWindowDays is the "past week" horizon for /reflect's recall window
// (agent-contracts.md §3; error-states.md §R-7). It is a rolling seven days,
// not the ISO week — the reflection record it writes is keyed by ISO week, but
// the insights it surfaces are those validated in the last seven days.
const recallWindowDays = 7

// emptyWindowSurfaceCount is how many insights the empty-week fallback surfaces
// regardless of age (error-states.md §R-7: "the two most recent").
const emptyWindowSurfaceCount = 2

// The fixed /reflect copy. E-3 and R-7 lines are the error-table copy
// (error-states.md §E-3/§R-7); the pointer line is the recall-side router copy
// from agent-contracts.md §3. None of it is agent-authored.
const (
	reflectNothingValidated = "Nothing validated yet — try `/checkin` first."
	reflectQuietWeek        = "Quiet week — nothing landed as a validated insight in the last seven days. " +
		"Want me to surface the two most recent ones from before that?"
	reflectLogPrompt      = "Or /log something new when you're ready."
	reflectCheckinPointer = "There are entries since your last check-in — /checkin when you want to look for a pattern together."
	reflectGateEmpty      = "Nothing accepted to review yet."
)

// ReflectScope selects the recall cadence: the weekly pass (`/reflect`) or the
// gate pass (`/reflect gate`) over every accepted insight.
type ReflectScope string

// The two /reflect scopes.
const (
	ReflectWeek ReflectScope = "week"
	ReflectGate ReflectScope = "gate"
)

// RecallResponse is the user's answer to one surfaced insight. Status is the
// insight-status answer (confirmed | softened | retired) or "" when the user
// lets it pass (recorded as unanswered). Rule is the ruled-insight answer
// (kept | lapsed | retired) or ""; an answer that maps to none of the three
// records nothing on the rule (error-states.md §R-15) while Status is still
// processed independently.
type RecallResponse struct {
	Status string
	Rule   string
}

// RecallResponder supplies the user's per-insight answers during /reflect. In a
// real harness it blocks on the chat thread; in tests it replays a fixed
// script keyed by insight id.
type RecallResponder interface {
	RespondToRecall(insightID, surface string) (RecallResponse, error)
}

// ReflectRequest carries the inputs for one /reflect turn.
type ReflectRequest struct {
	Scope     ReflectScope
	Now       time.Time
	Provider  provider.Provider
	Responder RecallResponder
}

// ReflectSurface is one surfaced insight and how the user answered it — the
// per-insight record of the pass. Surface is the full text the user saw (the
// Safety-gated resonance plus, for a ruled insight, the verbatim rule question).
type ReflectSurface struct {
	InsightID    string
	Surface      string
	ResponseKind string // confirmed | softened | retired | unanswered
	RuleKind     string // kept | lapsed | retired | "" (none recorded)
}

// ReflectResult reports what a /reflect turn surfaced and wrote. Message is the
// fixed copy for the empty / nothing-validated paths (empty otherwise). Panel
// holds the deterministic gate panel + dominance lines (gate scope only). Wrote
// is true when a reflection record was created or appended.
type ReflectResult struct {
	Scope    ReflectScope
	RecordID string
	Surfaces []ReflectSurface
	Panel    []string
	Message  string
	Wrote    bool
	Fallback bool
}

// Reflect executes /reflect (weekly recall) or /reflect gate. It reads the
// recall window, surfaces each accepted insight for the user to confirm,
// soften, or retire (never proposing — agent-contracts.md §3), records their
// status and rule responses, and appends a reflection record to the ISO-week
// file. With zero validated insights anywhere it returns the E-3 line and
// writes nothing (error-states.md §E-3). /reflect never creates a new insight
// file.
func (r *Router) Reflect(ctx context.Context, req ReflectRequest) (ReflectResult, error) {
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}

	ids, err := r.store.ListInsightIDs()
	if err != nil {
		return ReflectResult{}, fmt.Errorf("reflect: list insights: %w", err)
	}
	if len(ids) == 0 {
		// Nothing validated anywhere — no record is written (error-states.md §E-3).
		return ReflectResult{Scope: reflectScopeOrDefault(req.Scope), Message: reflectNothingValidated}, nil
	}

	if reflectScopeOrDefault(req.Scope) == ReflectGate {
		return r.reflectGate(ctx, req, now)
	}
	return r.reflectWeek(ctx, req, now)
}

// reflectScopeOrDefault resolves an empty scope to the weekly pass.
func reflectScopeOrDefault(s ReflectScope) ReflectScope {
	if s == ReflectGate {
		return ReflectGate
	}
	return ReflectWeek
}

// reflectWeek runs the weekly recall pass: surface the accepted insights
// validated in the past seven days, or fall back to the two most recent
// regardless of age when the week is empty (error-states.md §R-7).
func (r *Router) reflectWeek(ctx context.Context, req ReflectRequest, now time.Time) (ReflectResult, error) {
	window, err := r.store.ReadInsightsWindow(now.Add(-recallWindowDays*24*time.Hour), 0)
	if err != nil {
		return ReflectResult{}, fmt.Errorf("reflect: read week window: %w", err)
	}
	if len(window) == 0 {
		return r.reflectEmptyWeek(ctx, req, now)
	}

	surfaced := reflection.SurfaceForRecall(ctx, reflection.RecallInput{
		Scope:        reflection.ScopeWeek,
		Window:       toInsightViews(window),
		AgentVersion: r.cfg.AgentVersions.Reflection,
	}, req.Provider)

	surfaces, err := r.applyRecall(ctx, surfaced.Ordered, req, now)
	if err != nil {
		return ReflectResult{}, err
	}
	rec, err := r.writeReflectionRecord(now, surfaces)
	if err != nil {
		return ReflectResult{}, err
	}
	return ReflectResult{Scope: ReflectWeek, RecordID: rec.ID, Surfaces: surfaces, Wrote: true, Fallback: surfaced.Fallback}, nil
}

// reflectEmptyWeek is the R-7 fallback: no insight was validated in the last
// seven days. The router (not Reflection — agent-contracts.md §3) surfaces the
// two most recent insights regardless of age with the verbatim resonance line,
// prepends the quiet-week + /log copy, and appends the /checkin pointer line
// only when unreflected entries have accumulated. With nothing left to surface
// (every insight retired) it writes no record.
func (r *Router) reflectEmptyWeek(ctx context.Context, req ReflectRequest, now time.Time) (ReflectResult, error) {
	recent, err := r.store.ReadInsightsWindow(time.Time{}, emptyWindowSurfaceCount)
	if err != nil {
		return ReflectResult{}, fmt.Errorf("reflect: read fallback window: %w", err)
	}

	message := reflectQuietWeek + " " + reflectLogPrompt
	unreflected, err := r.hasUnreflectedEntries()
	if err != nil {
		return ReflectResult{}, err
	}
	if unreflected {
		message += " " + reflectCheckinPointer
	}

	if len(recent) == 0 {
		return ReflectResult{Scope: ReflectWeek, Message: message}, nil
	}

	ordered := make([]reflection.SurfacedInsight, 0, len(recent))
	for _, ins := range recent {
		ordered = append(ordered, verbatimSurfaced(ins))
	}
	surfaces, err := r.applyRecall(ctx, ordered, req, now)
	if err != nil {
		return ReflectResult{}, err
	}
	rec, err := r.writeReflectionRecord(now, surfaces)
	if err != nil {
		return ReflectResult{}, err
	}
	return ReflectResult{Scope: ReflectWeek, RecordID: rec.ID, Surfaces: surfaces, Message: message, Wrote: true}, nil
}

// reflectGate runs the gate recall pass over every accepted insight (cap 50 —
// the /ask cap), then appends the deterministic panel numbers and any dominance
// line as fixed router copy (agent-contracts.md §3 "Gate recall"). Panel lines
// surface at gate cadence only and are never written as insights.
func (r *Router) reflectGate(ctx context.Context, req ReflectRequest, now time.Time) (ReflectResult, error) {
	window, err := r.store.ReadInsightsWindow(time.Time{}, r.cfg.AskInsightsCap)
	if err != nil {
		return ReflectResult{}, fmt.Errorf("reflect: read gate window: %w", err)
	}
	if len(window) == 0 {
		return ReflectResult{Scope: ReflectGate, Message: reflectGateEmpty}, nil
	}

	surfaced := reflection.SurfaceForRecall(ctx, reflection.RecallInput{
		Scope:        reflection.ScopeGate,
		Window:       toInsightViews(window),
		AgentVersion: r.cfg.AgentVersions.Reflection,
	}, req.Provider)

	surfaces, err := r.applyRecall(ctx, surfaced.Ordered, req, now)
	if err != nil {
		return ReflectResult{}, err
	}
	panel, err := r.gatePanel(window)
	if err != nil {
		return ReflectResult{}, err
	}
	rec, err := r.writeReflectionRecord(now, surfaces)
	if err != nil {
		return ReflectResult{}, err
	}
	return ReflectResult{
		Scope: ReflectGate, RecordID: rec.ID, Surfaces: surfaces, Panel: panel, Wrote: true, Fallback: surfaced.Fallback,
	}, nil
}

// applyRecall gates each surfaced resonance line through Safety, appends the
// verbatim rule question for ruled insights, awaits the user's answer, and
// records the status and rule transitions. The rule question is never gated —
// it is the user's own testimony quoted back (agent-contracts.md §3 "Rules";
// §4 verbatim-user-text exemption). An unanswered insight advances nothing on
// the insight but is recorded as `unanswered` on the reflection record.
func (r *Router) applyRecall(
	ctx context.Context, ordered []reflection.SurfacedInsight, req ReflectRequest, now time.Time,
) ([]ReflectSurface, error) {
	surfaces := make([]ReflectSurface, 0, len(ordered))
	for _, s := range ordered {
		full := r.gateResonance(ctx, s, req.Provider)
		if s.Ruled {
			full += ruleQuestion(s.Rule)
		}

		resp, err := req.Responder.RespondToRecall(s.ID, full)
		if err != nil {
			return nil, fmt.Errorf("reflect: await recall response for %s: %w", s.ID, err)
		}

		responseKind := responseKindUnanswered
		if isRecallStatusKind(resp.Status) {
			if err := r.store.UpdateInsightStatus(s.ID, resp.Status, now); err != nil {
				return nil, fmt.Errorf("reflect: update insight status %s: %w", s.ID, err)
			}
			responseKind = resp.Status
		}
		ruleKind := ""
		if s.Ruled && isRuleStatusKind(resp.Rule) {
			if err := r.store.UpdateInsightRuleStatus(s.ID, resp.Rule, now); err != nil {
				return nil, fmt.Errorf("reflect: update insight rule status %s: %w", s.ID, err)
			}
			ruleKind = resp.Rule
		}
		surfaces = append(surfaces, ReflectSurface{InsightID: s.ID, Surface: full, ResponseKind: responseKind, RuleKind: ruleKind})
	}
	return surfaces, nil
}

// responseKindUnanswered is the reflection-record response_kind for a surfaced
// insight the user let pass (data-model.md §"Weekly reflections").
const responseKindUnanswered = "unanswered"

// gateResonance runs one recall resonance line through Safety. A pass returns
// the line unchanged; a rewrite returns the softened line; a block degrades to
// the verbatim resonance (which quotes the user's own statement — no novel
// framing) so the surface always says something honest.
func (r *Router) gateResonance(ctx context.Context, s reflection.SurfacedInsight, p provider.Provider) string {
	dec := safety.Evaluate(ctx, safety.Candidate{
		FromAgent: safety.FromReflection,
		Intent:    safety.IntentRecall,
		Text:      s.Resonance,
	}, safety.SessionContext{Command: commandReflect}, p)
	if dec.Decision == safety.Block {
		return reflection.VerbatimResonance(s.Statement)
	}
	return dec.Text
}

// ruleQuestion is the fixed, verbatim rule question appended to a ruled
// insight's surface at recall (agent-contracts.md §3 "Rules"). kept and lapsed
// are both first-class, judgment-free answers.
func ruleQuestion(rule string) string {
	return " You attached: '" + strings.TrimSpace(rule) + "'. Still standing — kept, lapsed, or retire it?"
}

// writeReflectionRecord builds and appends the ISO-week reflection record for
// this pass. The id, label, and window bounds come from the isoweek helper, so
// a second pass in the same week appends to the same file (acceptance-criteria
// 6.4). new_insight_ids is always empty — /reflect never proposes.
func (r *Router) writeReflectionRecord(now time.Time, surfaces []ReflectSurface) (storage.ReflectionResult, error) {
	start, end := isoweek.Bounds(now)
	surfacedEntries := make([]storage.ReflectionSurfaced, 0, len(surfaces))
	changeLog := make([]string, 0, len(surfaces))
	for _, s := range surfaces {
		surfacedEntries = append(surfacedEntries, storage.ReflectionSurfaced{ID: s.InsightID, ResponseKind: s.ResponseKind})
		changeLog = append(changeLog, fmt.Sprintf("%s: Surfaced %s — %s.", now.Format("2006-01-02"), s.InsightID, s.ResponseKind))
	}
	rec := storage.Reflection{
		ID:           isoweek.ID(now),
		ISOWeek:      isoweek.Label(now),
		WindowStart:  start,
		WindowEnd:    end,
		CreatedAt:    now,
		AgentVersion: r.cfg.AgentVersions.Reflection,
		Surfaced:     surfacedEntries,
		ChangeLog:    changeLog,
		Summary:      fmt.Sprintf("This week's recall surfaced %d validated insight(s).", len(surfaces)),
	}
	res, err := r.store.WriteReflection(rec)
	if err != nil {
		return storage.ReflectionResult{}, fmt.Errorf("reflect: write reflection record: %w", err)
	}
	return res, nil
}

// gatePanel computes the deterministic gate panel: insights accepted in the
// window, rules stated, and rules standing (a rule is standing when its latest
// rule_history transition is `stated` or `kept`). It appends a dominance line
// when one person's share of entries exceeds person_dominance_threshold. All of
// it is fixed router copy computed from the ledger — no agent authorship
// (agent-contracts.md §3 "Gate recall").
func (r *Router) gatePanel(window []storage.Insight) ([]string, error) {
	stated, standing := 0, 0
	for _, ins := range window {
		if ins.Rule != nil && strings.TrimSpace(*ins.Rule) != "" {
			stated++
			if ruleStanding(ins) {
				standing++
			}
		}
	}
	lines := []string{fmt.Sprintf(
		"Gate panel — %d accepted this window, %d rules stated, %d rules standing.", len(window), stated, standing)}

	dominance, err := r.dominanceLine()
	if err != nil {
		return nil, err
	}
	if dominance != "" {
		lines = append(lines, dominance)
	}
	return lines, nil
}

// ruleStanding reports whether a ruled insight's rule still stands: its latest
// rule_history transition is `stated` (never revisited) or `kept`, not `lapsed`
// or `retired`. A ruled insight always has at least the `stated` entry.
func ruleStanding(ins storage.Insight) bool {
	if len(ins.RuleHistory) == 0 {
		return ins.Rule != nil
	}
	switch ins.RuleHistory[len(ins.RuleHistory)-1].Kind {
	case storage.RuleStated, storage.RuleKept:
		return true
	default:
		return false
	}
}

// dominanceLine computes the one gate dominance line, or "" when no person
// exceeds the threshold. Share is a person's fraction of processed entries that
// mention them (deterministic, router-side — data-model.md gate row). Off-limits
// people are excluded: they are invisible to inference and must never be named
// (agent-contracts.md cross-cutting redaction). The line is hypothesis-framed
// and appears only at gate cadence.
func (r *Router) dominanceLine() (string, error) {
	ids, err := r.store.ListProcessedIDs()
	if err != nil {
		return "", fmt.Errorf("reflect: list processed for dominance: %w", err)
	}
	total := len(ids)
	if total == 0 {
		return "", nil
	}
	offLimits, err := r.store.ReadOffLimitsPersonKeys()
	if err != nil {
		return "", fmt.Errorf("reflect: read off-limits for dominance: %w", err)
	}
	deny := toSet(offLimits)

	counts := map[string]int{}
	names := map[string]string{}
	for _, id := range ids {
		art, err := r.store.ReadProcessed(id)
		if err != nil {
			return "", fmt.Errorf("reflect: read processed %q for dominance: %w", id, err)
		}
		seen := map[string]bool{}
		for _, p := range art.People {
			if deny[p.PersonKey] || seen[p.PersonKey] {
				continue
			}
			seen[p.PersonKey] = true
			counts[p.PersonKey]++
			names[p.PersonKey] = p.DisplayName
		}
	}

	key, count := topPerson(counts)
	if key == "" {
		return "", nil
	}
	share := float64(count) / float64(total)
	if share <= r.cfg.PersonDominanceThreshold {
		return "", nil
	}
	pct := int(share*100 + 0.5)
	return fmt.Sprintf("%s appears in %d%% of entries this window — worth a look, or expected?", names[key], pct), nil
}

// topPerson returns the person key with the highest entry count, breaking ties
// by the lexically smallest key so the result is deterministic regardless of
// map iteration order.
func topPerson(counts map[string]int) (string, int) {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	best, bestCount := "", 0
	for _, k := range keys {
		if counts[k] > bestCount {
			best, bestCount = k, counts[k]
		}
	}
	return best, bestCount
}

// hasUnreflectedEntries reports whether processed entries have accumulated
// since the last reflection pass — the condition for appending the /checkin
// pointer line in the empty-week fallback (agent-contracts.md §3;
// error-states.md §R-7). With entries but no prior reflection, everything is
// unreflected; otherwise any processed artifact produced after the most recent
// reflection record counts.
func (r *Router) hasUnreflectedEntries() (bool, error) {
	ids, err := r.store.ListProcessedIDs()
	if err != nil {
		return false, fmt.Errorf("reflect: list processed: %w", err)
	}
	if len(ids) == 0 {
		return false, nil
	}
	latest, ok, err := r.store.LatestReflectionCreatedAt()
	if err != nil {
		return false, fmt.Errorf("reflect: latest reflection: %w", err)
	}
	if !ok {
		return true, nil
	}
	for _, id := range ids {
		art, err := r.store.ReadProcessed(id)
		if err != nil {
			return false, fmt.Errorf("reflect: read processed %q: %w", id, err)
		}
		if art.ProducedAt.After(latest) {
			return true, nil
		}
	}
	return false, nil
}

// toInsightViews maps stored insights to the agent's recall slice view: id,
// canonical statement, and rule (empty when unruled).
func toInsightViews(insights []storage.Insight) []reflection.InsightView {
	out := make([]reflection.InsightView, 0, len(insights))
	for _, ins := range insights {
		out = append(out, reflection.InsightView{ID: ins.ID, Statement: ins.Body, Rule: ruleOrEmpty(ins.Rule)})
	}
	return out
}

// verbatimSurfaced builds the router-side surface for an insight in the
// empty-week fallback: the verbatim resonance line and the rule carried through
// (agent-contracts.md §3 — the router, not Reflection, handles this path).
func verbatimSurfaced(ins storage.Insight) reflection.SurfacedInsight {
	rule := ruleOrEmpty(ins.Rule)
	return reflection.SurfacedInsight{
		ID:        ins.ID,
		Statement: ins.Body,
		Resonance: reflection.VerbatimResonance(ins.Body),
		Ruled:     rule != "",
		Rule:      rule,
	}
}

// ruleOrEmpty collapses a nullable rule pointer to a plain string.
func ruleOrEmpty(rule *string) string {
	if rule == nil {
		return ""
	}
	return *rule
}

// isRecallStatusKind reports whether a status answer maps to a status-history
// transition. An empty or unrecognized answer is treated as unanswered.
func isRecallStatusKind(k string) bool {
	return k == storage.RecallConfirmed || k == storage.RecallSoftened || k == storage.RecallRetired
}

// isRuleStatusKind reports whether a rule answer maps to a rule-history
// transition. An answer matching none of the three records nothing on the rule
// (error-states.md §R-15).
func isRuleStatusKind(k string) bool {
	return k == storage.RuleKept || k == storage.RuleLapsed || k == storage.RuleRetire
}
