package workout

import (
	"fmt"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/observations"
)

// defaultPainThreshold is the body_state.pain value at or above which the
// recommender emits a hard stop when a program does not set its own
// pain_flag_threshold (workout-module.md §"The generic program schema").
const defaultPainThreshold = 5

// recoveryScanDays bounds the forward rotation scan the recovery veto walks when
// today's focus is still recovering — one full week is enough to find the next
// clear card in any weekly rotation.
const recoveryScanDays = 7

// SafetyOption is a named back-off choice the recommender emits when a pain
// signal (a high body_state.pain on a targeted part, or an active injury naming
// one) warrants resting rather than training. It is a real third door in the
// message, not a scolding: an offer to protect the part, with a plain reason.
type SafetyOption struct {
	Name      string   `json:"name"`
	Movements []string `json:"movements"`
	Reason    string   `json:"reason"`
}

// RecommendInput is everything the deterministic core folds into today's pick:
// the loaded program, the recent workout and body-state events (a bounded slice
// the caller reads — recency is the caller's concern), the active injury-registry
// records the pain guardrail reads, the Engine metrics (streak/adherence, carried
// for the surface and trend, not the pick), and the clocks. The core reads these
// values and touches no disk.
type RecommendInput struct {
	Program        Program
	RecentWorkouts []observations.Event
	BodyState      []observations.Event
	Injuries       []observations.Registry
	Metrics        engine.Metrics
	Now            time.Time
	Loc            *time.Location
}

// Recommendation is the already-decided plan the surface renders and the model
// only phrases. Primary is today's recommended card; Fallback is always present
// (the easier door); HardStop is set only when a pain signal warrants backing
// off; Reason is one deterministic line explaining the pick or its downshift; and
// Vetoes records every card/part vetoed this run and why — for the tests and the
// `--json` projection.
type Recommendation struct {
	Primary  Card          `json:"primary"`
	Fallback Card          `json:"fallback"`
	HardStop *SafetyOption `json:"hard_stop,omitempty"`
	Reason   string        `json:"reason"`
	Vetoes   []string      `json:"vetoes,omitempty"`
}

// Recommend picks and vetoes today's workout deterministically — zero model
// calls, zero disk I/O — so the guardrails the success criteria mandate are
// unit-testable and the daily surface completes even with no model available. The
// rules run in the order the module spec fixes (workout-module.md §"The
// deterministic recommender contract"): pick today's card, filter movement/focus
// guardrails, apply the recovery-window veto (the no-leg-day-twice guardrail),
// apply the pain-flag hard stop, then the equipment/time veto. With no recent
// events it falls to the plain program calendar — missing data never blocks a
// recommendation.
func Recommend(in RecommendInput) Recommendation {
	loc := in.Loc
	if loc == nil {
		loc = time.UTC
	}
	now := in.Now.In(loc)
	logDay := observations.LogicalBaseDate(now, observations.DefaultRolloverMin)
	prog := in.Program

	candidate, ok := pickCard(prog, logDay)
	if !ok {
		return restRecommendation(prog, "No card is scheduled for today, so today is an easy recovery day.")
	}

	var rec Recommendation

	// Rule 4: guardrail filter — a scheduled card that conflicts with a movement
	// or focus guardrail is never Primary; it downshifts to a safe recovery
	// session before the veto pass runs.
	if reason, blocked := guardrailBlock(prog, candidate); blocked {
		rec.Vetoes = append(rec.Vetoes, vetoLine(candidate, reason))
		return finish(prog, downshiftCard(prog), rec,
			"Today's scheduled card conflicts with a movement guardrail, so it downshifts to a safe recovery session.")
	}

	primary := candidate
	reason := calendarReason(in, primary)

	// Rule 2: recovery-window veto — a focus loaded non-light inside its recovery
	// window cannot be the hard focus again (the no-leg-day-every-day guardrail).
	if part, vetoed := recoveryVeto(prog, primary, in.RecentWorkouts, now, loc); vetoed {
		rec.Vetoes = append(rec.Vetoes, recoveryVetoLine(primary, part))
		primary, reason = rotateForRecovery(prog, logDay, in.RecentWorkouts, now, loc, part)
	}

	// Rule 3: pain-flag hard stop — a high pain on a targeted part, or an active
	// injury naming one, backs the session off. A specific joint pain wins over
	// the calendar.
	if hs, part, triggered := painHardStop(prog, primary, in.BodyState, in.Injuries); triggered {
		rec.HardStop = hs
		rec.Vetoes = append(rec.Vetoes, painVetoLine(primary, part))
		primary = downshiftCard(prog)
		reason = fmt.Sprintf(
			"A pain signal on %s means backing off today — an easy recovery session is the safe choice.", humanizePart(part),
		)
	}

	// Rule 5: equipment / time veto — a card the operator cannot run downshifts to
	// its easier variant.
	if !canRun(prog, primary) {
		rec.Vetoes = append(rec.Vetoes, vetoLine(primary, "needs more equipment or time than the program allows"))
		primary = easierAsCard(primary)
	}

	return finish(prog, primary, rec, reason)
}

// finish completes a partially-built recommendation with the chosen primary, its
// fallback door, and the reason line.
func finish(prog Program, primary Card, rec Recommendation, reason string) Recommendation {
	rec.Primary = primary
	rec.Fallback = fallbackFor(prog, primary)
	rec.Reason = reason
	return rec
}

// restRecommendation is the honest recovery-day result for a day with no
// scheduled card (an empty rotation, an unknown card id, or a missing program).
func restRecommendation(prog Program, reason string) Recommendation {
	rest := downshiftCard(prog)
	return Recommendation{
		Primary:  rest,
		Fallback: fallbackFor(prog, rest),
		Reason:   reason,
	}
}

// pickCard resolves today's card: a dated calendar override wins, else the
// weekday rotation.
func pickCard(prog Program, logDay time.Time) (Card, bool) {
	id, ok := scheduledCardID(prog, logDay)
	if !ok {
		return Card{}, false
	}
	return prog.card(id)
}

// scheduledCardID returns the card id scheduled for logDay — a matching dated
// calendar entry first, then the weekday rotation.
func scheduledCardID(prog Program, logDay time.Time) (string, bool) {
	ds := logDay.Format(dateLayout)
	for _, c := range prog.Calendar {
		if c.Date == ds {
			return c.Card, true
		}
	}
	wd := logDay.Weekday()
	for _, r := range prog.Rotation {
		if matchWeekday(r.Weekday, wd) {
			return r.Card, true
		}
	}
	return "", false
}

// calendarReason is the default reason line before any veto rewrites it: the
// missing-data path names the absence of logs; otherwise it names the calendar
// card.
func calendarReason(in RecommendInput, primary Card) string {
	if len(in.RecentWorkouts) == 0 && len(in.BodyState) == 0 {
		return fmt.Sprintf(
			"No recent workout or body-state logs yet, so today follows the program calendar: %s.", primary.Name,
		)
	}
	return fmt.Sprintf("On the program calendar today: %s.", primary.Name)
}

// guardrailBlock reports whether a card conflicts with a movement or focus
// guardrail, returning a human reason for the veto record.
func guardrailBlock(prog Program, c Card) (string, bool) {
	g := prog.Guardrails
	for _, m := range c.Movements {
		if matchesAny(m, g.AvoidMovements) {
			return fmt.Sprintf("movement %q is on the avoid list", m), true
		}
		if matchesAny(m, g.ProvocativePositions) {
			return fmt.Sprintf("movement %q is a provocative position", m), true
		}
	}
	for _, f := range c.Focus {
		if matchesAny(f, g.NoStrengthen) {
			return fmt.Sprintf("focus %q is on the do-not-strengthen list", f), true
		}
	}
	return "", false
}

// recoveryVeto reports whether a card's focus is still inside a recovery window
// from a recent non-light load. A light or recovery card never opens a recovery
// debt, so it is never vetoed here.
func recoveryVeto(prog Program, c Card, recent []observations.Event, now time.Time, loc *time.Location) (string, bool) {
	if isLightLoad(c.Load) {
		return "", false
	}
	for _, part := range c.Focus {
		hours, ok := prog.RecoveryHours[part]
		if !ok || hours <= 0 {
			continue
		}
		last, found := lastNonLightLoad(prog, recent, part, loc)
		if !found {
			continue
		}
		if now.Before(last.Add(time.Duration(hours) * time.Hour)) {
			return part, true
		}
	}
	return "", false
}

// lastNonLightLoad returns the most recent time a non-light workout loaded the
// given body part, reading each event's load from the program card its type
// names (or an rpe-inferred load when it names no card).
func lastNonLightLoad(prog Program, recent []observations.Event, part string, loc *time.Location) (time.Time, bool) {
	var best time.Time
	found := false
	for _, ev := range recent {
		if ev.Kind != observations.KindWorkout {
			continue
		}
		parts, load, at := workoutLoad(prog, ev, loc)
		if isLightLoad(load) || !partInList(part, parts) {
			continue
		}
		if !found || at.After(best) {
			best, found = at, true
		}
	}
	return best, found
}

// workoutLoad derives the body parts a workout event loaded, its load level, and
// when it occurred. It matches the event's free-text type to a program card
// (contributing that card's focus and load) and unions any explicit body_parts;
// with no card match it infers the load from the session rpe, defaulting a
// logged-but-unqualified session to a real (moderate) load.
func workoutLoad(prog Program, ev observations.Event, loc *time.Location) ([]string, string, time.Time) {
	at := eventTime(ev, loc)
	var parts []string
	load := ""
	if typ, ok := payloadString(ev.Payload, "type"); ok {
		if c, ok := matchCard(prog, typ); ok {
			parts = append(parts, c.Focus...)
			load = c.Load
		}
	}
	parts = append(parts, payloadStrings(ev.Payload, "body_parts")...)
	if load == "" {
		load = inferredLoad(ev.Payload)
	}
	return parts, load, at
}

// matchCard matches a workout event's free-text type to a program card by id or
// name (case-insensitive, punctuation-normalized).
func matchCard(prog Program, typ string) (Card, bool) {
	nt := normalize(typ)
	if nt == "" {
		return Card{}, false
	}
	for _, c := range prog.Cards {
		if normalize(c.ID) == nt || normalize(c.Name) == nt {
			return c, true
		}
	}
	return Card{}, false
}

// inferredLoad maps a session rpe to a load level when no program card is
// matched; a session with no rpe counts as a real (moderate) load, so an
// unqualified workout still opens a recovery window rather than being ignored.
func inferredLoad(p map[string]any) string {
	rpe, ok := payloadInt(p, "rpe")
	if !ok {
		return LoadModerate
	}
	switch {
	case rpe <= 3:
		return LoadLight
	case rpe <= 6:
		return LoadModerate
	default:
		return LoadHard
	}
}

// eventTime parses an event's occurred_at (RFC3339, the stored form), falling
// back to its logical date. An unparseable stamp yields the zero time, which
// reads as fully recovered — a safe degrade that never fabricates a fresh load.
func eventTime(ev observations.Event, loc *time.Location) time.Time {
	if t, err := time.Parse(time.RFC3339, ev.OccurredAt); err == nil {
		return t.In(loc)
	}
	if t, err := time.ParseInLocation(dateLayout, ev.LogicalDate, loc); err == nil {
		return t
	}
	return time.Time{}
}

// rotateForRecovery picks the replacement for a recovery-vetoed card: the next
// clear rotation card, or — when none is clear — a downshift to recovery. Both
// paths carry a reason line naming why today rotated.
func rotateForRecovery(prog Program, logDay time.Time, recent []observations.Event, now time.Time, loc *time.Location, vetoedPart string) (Card, string) {
	if alt, ok := nextClearCard(prog, logDay, recent, now, loc); ok {
		return alt, fmt.Sprintf(
			"%s worked recently and is still inside its recovery window, so today rotates to %s instead of repeating the load.",
			humanizePart(vetoedPart), alt.Name,
		)
	}
	rest := downshiftCard(prog)
	return rest, fmt.Sprintf(
		"%s is still recovering and no other focus is clear, so today downshifts to %s.",
		humanizePart(vetoedPart), rest.Name,
	)
}

// nextClearCard scans the coming week's rotation for the first distinct card that
// is neither guardrail-blocked nor recovery-vetoed — the alternative focus the
// recovery veto rotates to.
func nextClearCard(prog Program, logDay time.Time, recent []observations.Event, now time.Time, loc *time.Location) (Card, bool) {
	seen := make(map[string]bool, recoveryScanDays)
	for offset := 1; offset <= recoveryScanDays; offset++ {
		id, ok := scheduledCardID(prog, logDay.AddDate(0, 0, offset))
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		c, ok := prog.card(id)
		if !ok {
			continue
		}
		if _, blocked := guardrailBlock(prog, c); blocked {
			continue
		}
		if _, vetoed := recoveryVeto(prog, c, recent, now, loc); vetoed {
			continue
		}
		return c, true
	}
	return Card{}, false
}

// painHardStop reports whether a pain signal warrants backing off today's card:
// a recent body_state.pain at or above the program threshold on a targeted part,
// or an active injury-registry record naming a targeted part. It returns the
// safety option, the part, and whether it triggered.
func painHardStop(prog Program, c Card, bodyState []observations.Event, injuries []observations.Registry) (*SafetyOption, string, bool) {
	threshold := prog.PainFlagThreshold
	if threshold <= 0 {
		threshold = defaultPainThreshold
	}
	if part, ok := painFlaggedPart(c, bodyState, threshold); ok {
		return safetyOption(part), part, true
	}
	if part, ok := injuredTargetPart(c, injuries); ok {
		return safetyOption(part), part, true
	}
	return nil, "", false
}

// painFlaggedPart returns the first targeted part with a body_state.pain at or
// above the threshold in the recent slice.
func painFlaggedPart(c Card, bodyState []observations.Event, threshold int) (string, bool) {
	for _, ev := range bodyState {
		if ev.Kind != observations.KindBodyState {
			continue
		}
		part, ok := payloadString(ev.Payload, "body_part")
		if !ok {
			continue
		}
		pain, ok := payloadInt(ev.Payload, "pain")
		if !ok || pain < threshold {
			continue
		}
		if partTargeted(c, part) {
			return part, true
		}
	}
	return "", false
}

// injuredTargetPart returns the first active injury-registry part the candidate
// card targets — an active injury on a targeted part is its own back-off signal,
// independent of any logged pain.
func injuredTargetPart(c Card, injuries []observations.Registry) (string, bool) {
	for _, inj := range injuries {
		if !strings.EqualFold(inj.Status, observations.StatusActive) {
			continue
		}
		part := injuryPart(inj)
		if part != "" && partTargeted(c, part) {
			return part, true
		}
	}
	return "", false
}

// injuryPart reads the affected body part from an injury registry record — a
// named field first, then the display name.
func injuryPart(inj observations.Registry) string {
	for _, key := range []string{"body_part", "part", "site", "location"} {
		if v, ok := inj.Fields[key].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return inj.DisplayName
}

// safetyOption builds the named back-off door for a flagged part — an offer to
// protect it, never a scolding.
func safetyOption(part string) *SafetyOption {
	human := humanizePart(part)
	return &SafetyOption{
		Name: "Back off — protect " + human,
		Movements: []string{
			"gentle mobility only, no loaded work on " + human,
			"stop entirely if it feels sharp",
		},
		Reason: "A pain signal on " + human + " is a reason to rest it today rather than train through it.",
	}
}

// downshiftCard returns the program's recovery card (a load-none card, then a
// load-light one), or a synthesized rest card when the program authors none.
func downshiftCard(prog Program) Card {
	if c, ok := prog.cardByLoad(LoadNone); ok {
		return c
	}
	if c, ok := prog.cardByLoad(LoadLight); ok {
		return c
	}
	return restCard()
}

// restCard is the generic recovery/mobility session used when a program has no
// recovery card of its own.
func restCard() Card {
	return Card{
		ID:        "rest",
		Name:      "Recovery + mobility",
		Load:      LoadNone,
		Movements: []string{"gentle mobility", "easy walk", "light stretching"},
	}
}

// fallbackFor builds the always-present easier door for a primary card: its
// easier variant when it has one (and that variant clears the guardrails), the
// card itself when it is already a light/recovery session, else a downshift to
// recovery.
func fallbackFor(prog Program, primary Card) Card {
	if primary.Easier != nil {
		fb := easierAsCard(primary)
		if _, blocked := guardrailBlock(prog, fb); !blocked {
			return fb
		}
	}
	if isLightLoad(primary.Load) {
		return primary
	}
	return downshiftCard(prog)
}

// easierAsCard promotes a card's easier variant into a full Card, inheriting the
// parent's id and focus and defaulting a missing load to light.
func easierAsCard(c Card) Card {
	if c.Easier == nil {
		return c
	}
	e := *c.Easier
	if e.ID == "" {
		e.ID = c.ID + "_easier"
	}
	if len(e.Focus) == 0 {
		e.Focus = c.Focus
	}
	if e.Load == "" {
		e.Load = LoadLight
	}
	e.Easier = nil
	return e
}

// canRun reports whether the operator can run a card given the program's
// equipment and session-minute budget. Cards carry no equipment/minute
// requirements in the synthetic example, so this veto stays inert unless a
// program opts a card into them.
func canRun(prog Program, c Card) bool {
	if c.Minutes > 0 && prog.SessionMinutes > 0 && c.Minutes > prog.SessionMinutes {
		return false
	}
	for _, e := range c.Equipment {
		if !matchesAny(e, prog.Equipment) {
			return false
		}
	}
	return true
}

// partTargeted reports whether the card's focus includes the given part.
func partTargeted(c Card, part string) bool {
	return partInList(part, c.Focus)
}

// partInList reports whether part relates to any entry in list under normalized,
// bidirectional-substring matching.
func partInList(part string, list []string) bool {
	return matchesAny(part, list)
}

// matchesAny reports whether s relates to any entry in list — each side
// normalized (lowercased, punctuation to spaces) and compared with a
// bidirectional substring test so "shoulder" relates to "rear_shoulders" and a
// movement contains an avoided phrase.
func matchesAny(s string, list []string) bool {
	ns := normalize(s)
	if ns == "" {
		return false
	}
	for _, item := range list {
		if overlaps(ns, normalize(item)) {
			return true
		}
	}
	return false
}

// overlaps reports whether either normalized term is a substring of the other.
func overlaps(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return strings.Contains(a, b) || strings.Contains(b, a)
}

// isLightLoad reports whether a load is light, none, or unset — the loads that
// never open a recovery window.
func isLightLoad(load string) bool {
	return load == LoadLight || load == LoadNone || load == ""
}

// humanizePart renders a body-part token for display (underscores to spaces),
// falling back to a neutral phrase for an empty part.
func humanizePart(part string) string {
	n := normalize(part)
	if n == "" {
		return "that area"
	}
	return n
}

// normalize lowercases, turns underscores and hyphens into spaces, and collapses
// runs of whitespace — the shared form the guardrail and part matchers compare.
func normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.ReplaceAll(s, "-", " ")
	return strings.Join(strings.Fields(s), " ")
}

// vetoLine formats a generic veto record ("<card>: <reason>").
func vetoLine(c Card, reason string) string {
	return fmt.Sprintf("%s: %s", cardLabel(c), reason)
}

// recoveryVetoLine formats a recovery-window veto record.
func recoveryVetoLine(c Card, part string) string {
	return fmt.Sprintf("%s: %s still inside its recovery window", cardLabel(c), humanizePart(part))
}

// painVetoLine formats a pain-flag veto record.
func painVetoLine(c Card, part string) string {
	return fmt.Sprintf("%s: pain signal on %s", cardLabel(c), humanizePart(part))
}

// cardLabel is a card's id, or its name when the id is empty.
func cardLabel(c Card) string {
	if c.ID != "" {
		return c.ID
	}
	return c.Name
}

// payloadString reads a non-empty, trimmed string-valued payload field.
func payloadString(p map[string]any, key string) (string, bool) {
	s, ok := p[key].(string)
	if !ok {
		return "", false
	}
	if s = strings.TrimSpace(s); s == "" {
		return "", false
	}
	return s, true
}

// payloadInt reads an int-valued payload field, tolerating the int a freshly
// parsed capture carries and the float64 a JSONL round-trip decodes to.
func payloadInt(p map[string]any, key string) (int, bool) {
	switch n := p[key].(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

// payloadStrings reads a string-slice payload field, tolerating both the
// []string a freshly parsed capture carries and the []any a JSONL round-trip
// decodes to.
func payloadStrings(p map[string]any, key string) []string {
	switch v := p[key].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				if s = strings.TrimSpace(s); s != "" {
					out = append(out, s)
				}
			}
		}
		return out
	default:
		return nil
	}
}
