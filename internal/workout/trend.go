package workout

// This file owns the workout module's **trend / progress projection**. It is a
// read-only fold over the Ledger's workout and body-state events plus the Engine
// metrics — nothing here is ever written back onto an event (the P3 sanctuary
// rule: observations stay inventory, never a score). The streak is the workout
// record's own, counted on read from the logged workout days; only adherence is
// read from engine.Metrics. The frequency, skipped-day count, and body-response
// are counted from the two new kinds. The projection computes honestly with zero
// data (an empty trend) and with sparse data, and it makes zero model calls and
// zero disk reads — the caller passes the bounded slices in.
// See docs/mvp/workout-module.md §"The trend / progress projection".

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/observations"
)

// defaultTrendWindowDays is the trailing civil-day range BuildTrend projects over
// when the caller names none — four weeks, long enough to read a frequency
// direction without letting a long-ago training block distort the recent picture.
const defaultTrendWindowDays = 28

// trendWeekDays is the length of each of the two trailing windows the frequency
// direction compares: this week (days 0–6) against the week before (days 7–13).
const trendWeekDays = 7

// Frequency-direction labels. The direction is a coarse, deterministic read of
// this week's session count against the prior week's — never a grade, just a
// pointer so the user can see whether the practice is picking up or easing off.
const (
	DirectionUp   = "up"
	DirectionFlat = "flat"
	DirectionDown = "down"
)

// Pain-response direction labels. The direction is the chronological read of a
// part's following-day max(pain, soreness) across its non-light targeted session
// days — never a grade, just whether the body's answer to the load has been
// climbing, holding, or settling.
const (
	ResponseRising = "rising"
	ResponseStable = "stable"
	ResponseEasing = "easing"
)

// Load-vs-pain pattern labels. The pattern is a deterministic read of whether
// heavier training days have tracked a higher next-day response for a part — a
// pattern observation, never a claim of cause.
const (
	LoadPatternTracksHigher = "tracks higher load"
	LoadPatternNoIncrease   = "no tracked increase"
)

// minPairedDays is the fewest paired (non-light session day → following-day
// response) days a part needs before the projection reports a direction or a
// tracked-load pattern rather than an explicit insufficient-data state; below it
// a trend read would be noise, not signal.
const minPairedDays = 3

// PartTrend is one body part's next-day pain-response read: the part it names,
// that a non-light session loaded it (Loaded), the number of chronologically
// paired session→next-day-response days (PairedDays), and either a Direction
// (rising/stable/easing) or an explicit Insufficient state when fewer than
// minPairedDays pairs exist. It restates no dashboard number — it answers "did the
// load show up in how the part felt the next day".
type PartTrend struct {
	Part         string `json:"part"`
	Loaded       bool   `json:"loaded"`
	Direction    string `json:"direction,omitempty"`
	PairedDays   int    `json:"paired_days"`
	Insufficient bool   `json:"insufficient"`
}

// PartPattern is one body part's load-vs-pain read: LoadPatternTracksHigher when
// heavier session days carried a higher mean next-day response than lighter ones,
// else LoadPatternNoIncrease (including the equal-mean and insufficient-data
// cases). It is a pattern read over the same pairs PartTrend uses.
type PartPattern struct {
	Part    string `json:"part"`
	Pattern string `json:"pattern"`
}

// Checkpoint is one relative-time post-workout check-in: the configured offset
// from the logged session and the wall-clock instant that offset lands on. It
// tracks no done/pending state — it is a "when to check in" marker and nothing
// more.
type Checkpoint struct {
	OffsetHours int       `json:"offset_hours"`
	At          time.Time `json:"at"`
}

// CheckpointScaffold is the stateless post-workout check-in guide: the operator's
// configured Subject/Label/Copy (never hardcoded body-part or health copy), the
// logged qualifying session's time, and the checkpoints computed relative to it.
// It is present only when a checkpoint_eligible session is logged and the program
// carries a complete checkpoint block; otherwise the trend leaves it nil.
type CheckpointScaffold struct {
	Subject     string       `json:"subject"`
	Label       string       `json:"label"`
	Copy        string       `json:"copy"`
	SessionAt   time.Time    `json:"session_at"`
	Checkpoints []Checkpoint `json:"checkpoints"`
}

// loadPair is one paired observation the insight folds read: a non-light targeted
// session day, its load ordinal, and the following logical day's max(pain,
// soreness) response for the part.
type loadPair struct {
	day      time.Time
	ordinal  int
	response int
}

// loadedSession is one non-light targeted session day for a part: its logical day
// and the highest load ordinal logged for that part on that day (two sessions on
// one day collapse to the heavier one).
type loadedSession struct {
	day     time.Time
	ordinal int
}

// BodySignal is one body part's most-recent soreness/pain reading inside the
// trend window — how the body answered the load, surfaced as inventory. Soreness
// and pain are pointers so an absent field is omitted rather than rendered as a
// hollow zero (a body_state event may carry either, both, or neither). AsOf is
// the reading's logical day.
type BodySignal struct {
	Part     string `json:"part"`
	Soreness *int   `json:"soreness,omitempty"`
	Pain     *int   `json:"pain,omitempty"`
	AsOf     string `json:"as_of,omitempty"`
}

// Trend is the read-only progress projection the surface shows: the workout
// streak (counted from logged workout days) and the Engine chain's adherence, the
// frequency read (this week vs the week before with a direction), the skipped-day
// count over the window, and the recent body-response per part. It is computed on
// demand and never stored.
type Trend struct {
	Streak       int          `json:"streak"`
	Adherence    float64      `json:"adherence"`
	WindowDays   int          `json:"window_days"`
	Sessions     int          `json:"sessions"`
	ThisWeek     int          `json:"this_week"`
	PriorWeek    int          `json:"prior_week"`
	Direction    string       `json:"direction"`
	SkippedDays  int          `json:"skipped_days"`
	BodyResponse []BodySignal `json:"body_response,omitempty"`
	// Insight fields — the panel's real signal. PainResponse and LoadPattern are
	// per-part folds over the same non-light session→next-day-response pairs;
	// Checkpoints is the stateless post-workout scaffold (nil when none);
	// WatchOuts frames the concrete signals worth a second look (nil when none).
	PainResponse []PartTrend         `json:"pain_response,omitempty"`
	LoadPattern  []PartPattern       `json:"load_pattern,omitempty"`
	Checkpoints  *CheckpointScaffold `json:"checkpoints,omitempty"`
	WatchOuts    []string            `json:"watch_outs,omitempty"`
}

// TrendInput is everything BuildTrend folds into the projection: the bounded
// recent workout and body-state slices (recency is the caller's concern, exactly
// like RecommendInput), the Engine metrics carrying the chain adherence, the
// current instant, the local location, and an optional window override
// (WindowDays ≤ 0 → the four-week default). Nothing here is mutated.
type TrendInput struct {
	Program    Program
	Workouts   []observations.Event
	BodyState  []observations.Event
	Metrics    engine.Metrics
	Now        time.Time
	Loc        *time.Location
	WindowDays int
}

// BuildTrend computes the read-only progress projection. It is pure — zero model
// calls, zero disk I/O — and stores nothing back onto any event (P3 sanctuary).
// The streak is the workout record's own, counted from the logged workout days
// (see workoutStreak) rather than borrowed from the Engine chain; adherence is
// still copied straight from the Engine fold (never recomputed as a score on
// workout events); the session counts and skipped-day count are derived from the
// distinct logical days the workout events fall on; the body-response is the
// most-recent body_state reading per part in range. With no events it yields an
// honest empty trend (a zero streak, zero sessions, the whole window skipped, no
// body signals) rather than a crash or a fabricated number.
func BuildTrend(in TrendInput) Trend {
	loc := in.Loc
	if loc == nil {
		loc = time.UTC
	}
	window := in.WindowDays
	if window <= 0 {
		window = defaultTrendWindowDays
	}
	today := observations.LogicalBaseDate(in.Now.In(loc), observations.DefaultRolloverMin)

	days := workoutDays(in.Workouts, loc)
	thisWeek := daysInBucket(days, today, 0, trendWeekDays-1)
	priorWeek := daysInBucket(days, today, trendWeekDays, 2*trendWeekDays-1)
	sessions := daysInBucket(days, today, 0, window-1)

	pairs, loadedParts := buildLoadPairs(in, today, window, loc)
	painResponse := buildPainResponse(pairs, loadedParts)
	loadPattern := buildLoadPattern(pairs, loadedParts)

	return Trend{
		Streak:       workoutStreak(days, today),
		Adherence:    in.Metrics.Adherence.Adherence,
		WindowDays:   window,
		Sessions:     sessions,
		ThisWeek:     thisWeek,
		PriorWeek:    priorWeek,
		Direction:    frequencyDirection(thisWeek, priorWeek),
		SkippedDays:  max(0, window-sessions),
		BodyResponse: bodyResponse(in.BodyState, today, window, loc),
		PainResponse: painResponse,
		LoadPattern:  loadPattern,
		Checkpoints:  buildCheckpointScaffold(in, loc),
		WatchOuts:    buildWatchOuts(painResponse, loadPattern),
	}
}

// workoutDays buckets the workout events into the set of distinct logical civil
// days they fall on, keyed by the date string so two sessions on one day count
// once. A non-workout event or one whose day cannot be resolved is skipped.
func workoutDays(events []observations.Event, loc *time.Location) map[string]time.Time {
	out := make(map[string]time.Time, len(events))
	for _, ev := range events {
		if ev.Kind != observations.KindWorkout {
			continue
		}
		day, ok := eventLogicalDay(ev, loc)
		if !ok {
			continue
		}
		out[day.Format(dateLayout)] = day
	}
	return out
}

// daysInBucket counts the distinct workout days whose civil-day distance behind
// today falls in the inclusive [lo, hi] band. A future-dated day (distance < 0)
// is never counted, so a session logged ahead of the clock cannot inflate a
// window. The distance uses engine.DaysSince, which re-anchors both instants to
// UTC civil midnight so a DST transition cannot make the count drift.
func daysInBucket(days map[string]time.Time, today time.Time, lo, hi int) int {
	n := 0
	for _, day := range days {
		ds := engine.DaysSince(day, today)
		if ds >= lo && ds <= hi {
			n++
		}
	}
	return n
}

// workoutStreak counts the current run of consecutive logical days that carry a
// logged workout — the surface's own honest number, never the Engine chain's
// (the chain defends the night close-out, a different practice). A day closes on
// either a completed daily anchor or a full session, since both are written as a
// workout event and so land in the same day set.
//
// Today in progress is not a break: the run anchors on today when today already
// carries a workout, and otherwise on yesterday — a day is never scored against
// the user before it is over. A gap of two or more days ends the run, and no
// logged workout day at all yields 0, which the panel renders as the build rather
// than a hollow "0-day streak". Future-dated days (distance < 0) are ignored so a
// log ahead of the clock cannot inflate the count.
func workoutStreak(days map[string]time.Time, today time.Time) int {
	present := make(map[int]struct{}, len(days))
	for _, day := range days {
		if ds := engine.DaysSince(day, today); ds >= 0 {
			present[ds] = struct{}{}
		}
	}

	start := 0
	if _, ok := present[start]; !ok {
		start = 1 // today is still in progress — anchor the run on yesterday
		if _, ok := present[start]; !ok {
			return 0
		}
	}

	n := 0
	for d := start; ; d++ {
		if _, ok := present[d]; !ok {
			return n
		}
		n++
	}
}

// frequencyDirection reads this week's session count against the prior week's — a
// coarse, deterministic pointer, never a grade.
func frequencyDirection(thisWeek, priorWeek int) string {
	switch {
	case thisWeek > priorWeek:
		return DirectionUp
	case thisWeek < priorWeek:
		return DirectionDown
	default:
		return DirectionFlat
	}
}

// bodyResponse folds the body-state events into the most-recent soreness/pain
// reading per part inside the window. Recency is the reading's own instant
// (occurred_at, falling back to its logical day); on a tie the later event in the
// slice wins, so the result is deterministic for a given input. The output is
// sorted by part for a stable projection regardless of the map's iteration order.
func bodyResponse(events []observations.Event, today time.Time, window int, loc *time.Location) []BodySignal {
	type reading struct {
		event   observations.Event
		instant time.Time
		day     time.Time
	}
	latest := make(map[string]reading)
	for _, ev := range events {
		if ev.Kind != observations.KindBodyState {
			continue
		}
		part, ok := payloadString(ev.Payload, "body_part")
		if !ok {
			continue
		}
		day, ok := eventLogicalDay(ev, loc)
		if !ok {
			continue
		}
		if ds := engine.DaysSince(day, today); ds < 0 || ds > window-1 {
			continue
		}
		instant := eventTime(ev, loc)
		if instant.IsZero() {
			instant = day
		}
		key := normalize(part)
		if prev, seen := latest[key]; seen && instant.Before(prev.instant) {
			continue
		}
		latest[key] = reading{event: ev, instant: instant, day: day}
	}

	out := make([]BodySignal, 0, len(latest))
	for part, r := range latest {
		sig := BodySignal{Part: part, AsOf: r.day.Format(dateLayout)}
		if v, ok := payloadInt(r.event.Payload, "soreness"); ok {
			sig.Soreness = &v
		}
		if v, ok := payloadInt(r.event.Payload, "pain"); ok {
			sig.Pain = &v
		}
		out = append(out, sig)
	}
	slices.SortFunc(out, func(a, b BodySignal) int { return cmp.Compare(a.Part, b.Part) })
	return out
}

// eventLogicalDay resolves an event's logical civil day: the stored logical_date
// when present and parseable, else the day derived from its occurred_at under the
// rollover rule. An event with neither a usable logical date nor a parseable
// time is skipped (ok=false) rather than counted on a wrong day.
func eventLogicalDay(ev observations.Event, loc *time.Location) (time.Time, bool) {
	if ev.LogicalDate != "" {
		if d, err := time.ParseInLocation(dateLayout, ev.LogicalDate, loc); err == nil {
			return d, true
		}
	}
	at := eventTime(ev, loc)
	if at.IsZero() {
		return time.Time{}, false
	}
	return observations.LogicalBaseDate(at, observations.DefaultRolloverMin), true
}

// buildLoadPairs pairs each non-light targeted session day with the following
// logical day's max(pain, soreness) for that part, ordered chronologically. It
// returns the per-part pairs and the sorted set of parts a non-light session
// loaded — a loaded part with no next-day reading still appears (with no pairs) so
// the projection can report an explicit insufficient-data state rather than
// silently omitting the part. A session whose following day is still in the future
// (today's own session) yields no pair yet, so a day is never scored before its
// answer exists.
func buildLoadPairs(in TrendInput, today time.Time, window int, loc *time.Location) (map[string][]loadPair, []string) {
	responses := nextDayResponses(in.BodyState, loc)
	sessions := nonLightSessions(in, today, window, loc)

	pairs := make(map[string][]loadPair)
	parts := make([]string, 0, len(sessions))
	for part, byDay := range sessions {
		parts = append(parts, part)
		byResponseDay := responses[part]
		for _, s := range byDay {
			next := s.day.AddDate(0, 0, 1)
			if engine.DaysSince(next, today) < 0 {
				continue
			}
			if v, ok := byResponseDay[next.Format(dateLayout)]; ok {
				pairs[part] = append(pairs[part], loadPair{day: s.day, ordinal: s.ordinal, response: v})
			}
		}
	}
	for part := range pairs {
		slices.SortFunc(pairs[part], func(a, b loadPair) int { return a.day.Compare(b.day) })
	}
	slices.Sort(parts)
	return pairs, parts
}

// nextDayResponses indexes each body-state reading by normalized part and logical
// day to that day's max(pain, soreness) — the value a session's following day is
// paired against. On several readings for one part-day the higher response wins,
// so a single flare is not averaged away. A reading carrying neither pain nor
// soreness contributes nothing.
func nextDayResponses(events []observations.Event, loc *time.Location) map[string]map[string]int {
	out := make(map[string]map[string]int)
	for _, ev := range events {
		if ev.Kind != observations.KindBodyState {
			continue
		}
		part, ok := payloadString(ev.Payload, "body_part")
		if !ok {
			continue
		}
		day, ok := eventLogicalDay(ev, loc)
		if !ok {
			continue
		}
		v, ok := maxPainSoreness(ev.Payload)
		if !ok {
			continue
		}
		key := normalize(part)
		byDay := out[key]
		if byDay == nil {
			byDay = make(map[string]int)
			out[key] = byDay
		}
		if cur, seen := byDay[day.Format(dateLayout)]; !seen || v > cur {
			byDay[day.Format(dateLayout)] = v
		}
	}
	return out
}

// nonLightSessions folds the workout events into, per normalized part, the set of
// distinct in-window days a non-light session targeted that part, keeping the
// higher load ordinal when two sessions land on one day. A light/recovery session
// opens no load pair (isLightLoad), matching the recovery veto's own load rule.
func nonLightSessions(in TrendInput, today time.Time, window int, loc *time.Location) map[string]map[string]loadedSession {
	out := make(map[string]map[string]loadedSession)
	for _, ev := range in.Workouts {
		day, ord, parts, ok := sessionLoad(in.Program, ev, today, window, loc)
		if !ok {
			continue
		}
		recordSession(out, parts, day, ord)
	}
	return out
}

// sessionLoad reports a workout event's logical day, load ordinal, and targeted
// parts when it is a countable non-light, in-window session — ok is false for a
// wrong kind, a light/recovery or partless session, an unresolvable day, or a day
// outside the window (a future-dated log never counts).
func sessionLoad(prog Program, ev observations.Event, today time.Time, window int, loc *time.Location) (time.Time, int, []string, bool) {
	if ev.Kind != observations.KindWorkout {
		return time.Time{}, 0, nil, false
	}
	parts, load, _ := workoutLoad(prog, ev, loc)
	if isLightLoad(load) || len(parts) == 0 {
		return time.Time{}, 0, nil, false
	}
	day, ok := eventLogicalDay(ev, loc)
	if !ok {
		return time.Time{}, 0, nil, false
	}
	if ds := engine.DaysSince(day, today); ds < 0 || ds > window-1 {
		return time.Time{}, 0, nil, false
	}
	return day, loadOrdinal(load), parts, true
}

// recordSession upserts one session's targeted parts into the per-part day set,
// keeping the heavier load when a part is already recorded on that day.
func recordSession(out map[string]map[string]loadedSession, parts []string, day time.Time, ord int) {
	ds := day.Format(dateLayout)
	for _, p := range parts {
		key := normalize(p)
		if key == "" {
			continue
		}
		byDay := out[key]
		if byDay == nil {
			byDay = make(map[string]loadedSession)
			out[key] = byDay
		}
		if cur, seen := byDay[ds]; !seen || ord > cur.ordinal {
			byDay[ds] = loadedSession{day: day, ordinal: ord}
		}
	}
}

// buildPainResponse derives the per-part next-day pain-response trend: for each
// loaded part it reports the chronological direction of the paired responses, or
// an explicit insufficient-data state below minPairedDays pairs. The parts are
// already sorted, so the output is deterministic.
func buildPainResponse(pairs map[string][]loadPair, parts []string) []PartTrend {
	if len(parts) == 0 {
		return nil
	}
	out := make([]PartTrend, 0, len(parts))
	for _, part := range parts {
		pr := pairs[part]
		pt := PartTrend{Part: part, Loaded: true, PairedDays: len(pr)}
		if len(pr) < minPairedDays {
			pt.Insufficient = true
		} else {
			pt.Direction = responseDirection(pr)
		}
		out = append(out, pt)
	}
	return out
}

// responseDirection reads the chronological direction of a part's paired
// responses by comparing the mean of the earlier half against the mean of the
// later half (the middle pair is dropped on an odd count so it never biases one
// side). A rising later half is rising, a falling one easing, an equal one stable.
func responseDirection(pairs []loadPair) string {
	n := len(pairs)
	earlier := meanResponse(pairs[:n/2])
	later := meanResponse(pairs[(n+1)/2:])
	switch {
	case later > earlier:
		return ResponseRising
	case later < earlier:
		return ResponseEasing
	default:
		return ResponseStable
	}
}

// buildLoadPattern derives the per-part load-vs-pain pattern over the same pairs,
// in the same sorted part order as buildPainResponse.
func buildLoadPattern(pairs map[string][]loadPair, parts []string) []PartPattern {
	if len(parts) == 0 {
		return nil
	}
	out := make([]PartPattern, 0, len(parts))
	for _, part := range parts {
		out = append(out, PartPattern{Part: part, Pattern: loadPattern(pairs[part])})
	}
	return out
}

// loadPattern classifies a part's pairs as LoadPatternTracksHigher only when there
// are enough pairs, both a heavier and a lighter group are present, and the
// heavier group's mean next-day response exceeds the lighter group's; every other
// case (too few pairs, one load level, or an equal/lower heavy mean) is
// LoadPatternNoIncrease.
func loadPattern(pairs []loadPair) string {
	if len(pairs) < minPairedDays {
		return LoadPatternNoIncrease
	}
	higher, lower := splitByLoad(pairs)
	if len(higher) == 0 || len(lower) == 0 {
		return LoadPatternNoIncrease
	}
	if meanResponse(higher) > meanResponse(lower) {
		return LoadPatternTracksHigher
	}
	return LoadPatternNoIncrease
}

// splitByLoad partitions pairs into the heaviest-load group and everything below
// it — the two groups the load pattern compares.
func splitByLoad(pairs []loadPair) (higher, lower []loadPair) {
	maxOrd := 0
	for _, p := range pairs {
		if p.ordinal > maxOrd {
			maxOrd = p.ordinal
		}
	}
	for _, p := range pairs {
		if p.ordinal == maxOrd {
			higher = append(higher, p)
		} else {
			lower = append(lower, p)
		}
	}
	return higher, lower
}

// meanResponse is the arithmetic mean of the pairs' responses (0 for an empty
// slice, which the callers never pass).
func meanResponse(pairs []loadPair) float64 {
	if len(pairs) == 0 {
		return 0
	}
	sum := 0
	for _, p := range pairs {
		sum += p.response
	}
	return float64(sum) / float64(len(pairs))
}

// buildWatchOuts frames the concrete signals worth a second look, mirroring the
// weekly witness report's frame-don't-restate vocabulary: a part whose next-day
// response is rising, and a part whose response tracks higher load. Each line
// names a real number, and the list is nil (no header) when nothing warrants it.
func buildWatchOuts(painResponse []PartTrend, loadPattern []PartPattern) []string {
	byPart := make(map[string]string, len(loadPattern))
	for _, lp := range loadPattern {
		byPart[lp.Part] = lp.Pattern
	}
	var out []string
	for _, pt := range painResponse {
		if pt.Insufficient {
			continue
		}
		if pt.Direction == ResponseRising {
			out = append(out, fmt.Sprintf(
				"%s — next-day pain/soreness rising across %d logged days.", humanizePart(pt.Part), pt.PairedDays,
			))
		}
		if byPart[pt.Part] == LoadPatternTracksHigher {
			out = append(out, fmt.Sprintf(
				"%s — next-day response tracks higher training load over %d logged days.", humanizePart(pt.Part), pt.PairedDays,
			))
		}
	}
	return out
}

// buildCheckpointScaffold builds the stateless post-workout check-in guide off the
// most-recent logged checkpoint_eligible session, or nil when the program carries
// no complete checkpoint block, no eligible session is logged, or that session's
// time cannot be resolved. It reads no post-session body_state and tracks no
// done/pending state — the offsets are relative to when the user actually trained.
func buildCheckpointScaffold(in TrendInput, loc *time.Location) *CheckpointScaffold {
	cfg := in.Program.Checkpoints
	if !cfg.complete() {
		return nil
	}
	at, ok := latestEligibleSession(in, loc)
	if !ok {
		return nil
	}
	cps := make([]Checkpoint, 0, len(cfg.OffsetHours))
	for _, off := range cfg.OffsetHours {
		cps = append(cps, Checkpoint{OffsetHours: off, At: at.Add(time.Duration(off) * time.Hour)})
	}
	return &CheckpointScaffold{
		Subject:     cfg.Subject,
		Label:       cfg.Label,
		Copy:        cfg.Copy,
		SessionAt:   at,
		Checkpoints: cps,
	}
}

// latestEligibleSession returns the logged time of the most-recent workout whose
// type resolves to a checkpoint_eligible program card, and whether one exists. An
// anchor-only day (no type) or an unresolvable time never qualifies.
func latestEligibleSession(in TrendInput, loc *time.Location) (time.Time, bool) {
	var best time.Time
	found := false
	for _, ev := range in.Workouts {
		if ev.Kind != observations.KindWorkout {
			continue
		}
		typ, ok := payloadString(ev.Payload, "type")
		if !ok {
			continue
		}
		card, ok := matchCard(in.Program, typ)
		if !ok || !card.CheckpointEligible {
			continue
		}
		at := eventTime(ev, loc)
		if at.IsZero() {
			continue
		}
		if !found || at.After(best) {
			best, found = at, true
		}
	}
	return best, found
}

// loadOrdinal ranks a load level so the load-vs-pain pattern can compare heavier
// against lighter sessions (none < light < moderate < hard).
func loadOrdinal(load string) int {
	switch load {
	case LoadHard:
		return 3
	case LoadModerate:
		return 2
	case LoadLight:
		return 1
	default:
		return 0
	}
}

// maxPainSoreness reads the greater of a body-state reading's pain and soreness —
// the single next-day response value the folds pair against. ok is false when the
// reading carries neither field.
func maxPainSoreness(p map[string]any) (int, bool) {
	pain, hasPain := payloadInt(p, "pain")
	sore, hasSore := payloadInt(p, "soreness")
	switch {
	case hasPain && hasSore:
		return max(pain, sore), true
	case hasPain:
		return pain, true
	case hasSore:
		return sore, true
	default:
		return 0, false
	}
}
