package observations

import (
	"slices"
	"strconv"
	"strings"
	"time"
)

// Kind is an observation kind (observations.md §3) — the vocabulary a capture
// verb resolves to. A defined string type so the compiler keeps an observation
// kind from being confused with a registry kind (injury/thread/place/era) or a
// place key; it marshals byte-identically to its underlying string, so the
// event envelope is unchanged.
type Kind string

// Observation kinds (observations.md §3, the initial vocabulary). Kinds are
// enabled per-instance; the parser resolves a verb to one of these and never
// invents a value — capture never blocks, so an unparseable head lands on the
// partial path with the invoked kind preserved.
const (
	KindPain         Kind = "pain"
	KindSymptom      Kind = "symptom"
	KindIntake       Kind = "intake"
	KindElimination  Kind = "elimination"
	KindMood         Kind = "mood"
	KindSleep        Kind = "sleep"
	KindMed          Kind = "med"
	KindIntervention Kind = "intervention"
	KindMeasurement  Kind = "measurement"
	KindMemory       Kind = "memory"
	KindLocation     Kind = "context.location"
	KindContextDay   Kind = "context.day"

	// Companion-context kinds (observations.md §3): capturable signals the daily
	// companion renders when present and omits gracefully when absent. They are
	// enable-gated and off by default — a fresh Ledger never carries them, so
	// they change nothing until an operator adds them to kinds_enabled.
	KindWithdrawal  Kind = "withdrawal"
	KindHabitChange Kind = "habit_change"
	KindCommitment  Kind = "commitment"
)

// ParseMarkerPartial is the payload.parse value stamped on a capture that
// could not be structured (observations.md §4 "Defaults for bare forms"):
// the invoked kind is kept, the full text goes to payload.note verbatim, and
// projections treat it as an unknown-valued point rather than dropping it.
const ParseMarkerPartial = "partial"

// Memory payload keys (mvp/life-archive.md §3, the story convention on the
// frozen envelope). text/certainty are the base memory grammar [parseMemory]
// captures; tone, why_it_matters, follow_up, and people are the excavation
// convention keys a structured story capture fills. They are payload keys, not
// new envelope fields — the envelope stays frozen at schema 1 (observations.md
// §2: "new needs go in payload/tags/refs, never a new top-level field").
const (
	MemoryFieldText         = "text"
	MemoryFieldCertainty    = "certainty"
	MemoryFieldTone         = "tone"
	MemoryFieldWhyItMatters = "why_it_matters"
	MemoryFieldFollowUp     = "follow_up"
	MemoryFieldPeople       = "people"
)

// IsMemoryCertainty reports whether tok is a memory certainty keyword
// (observations.md §3 memory: vivid / hazy / reconstructed). It is the shared
// vocabulary gate for both the token grammar [parseMemory] and the structured
// story-capture verb, so the two paths never diverge on what counts as valid.
func IsMemoryCertainty(tok string) bool {
	switch tok {
	case "vivid", "hazy", "reconstructed":
		return true
	default:
		return false
	}
}

// isIntakeClass reports whether tok names an intake class (observations.md §3
// intake): a generic `/obs intake <class> …` names one; `/ate` and `/drank`
// preset food and liquid.
func isIntakeClass(tok string) bool {
	switch tok {
	case "food", "liquid", "supplement":
		return true
	default:
		return false
	}
}

// ResolveVerb maps a capture verb to its (kind, class). The named shorthands
// are aliases into the one observation.capture intent (observations.md §4:
// "/pain, /ate, /drank, /bm, /mood, /slept are aliases into the same single
// router intent"); every other enabled kind is reached through the generic
// `/obs <kind>` form, where the verb is the kind itself. It returns ok=false
// for a verb that names no known kind — the router rejects that as unknown.
func ResolveVerb(verb string) (kind Kind, class string, ok bool) {
	switch strings.ToLower(verb) {
	case "pain":
		return KindPain, "", true
	case "ate":
		return KindIntake, "food", true
	case "drank":
		return KindIntake, "liquid", true
	case "bm":
		return KindElimination, "bm", true
	case "urine":
		return KindElimination, "urine", true
	case "mood":
		return KindMood, "", true
	case "slept":
		return KindSleep, "", true
	case "where":
		return KindLocation, "", true
	}
	if IsCapturableKind(Kind(verb)) {
		return Kind(verb), "", true
	}
	return "", "", false
}

// IsCapturableKind reports whether kind may be captured by a command
// (observations.md §3). context.day is enricher-written only, so it is not
// capturable; hypothesis/verdict are Scientist-layer, out of MVP scope.
func IsCapturableKind(kind Kind) bool {
	switch kind { //nolint:exhaustive // deliberately partial: context.day is enricher-written only, so it (and any unknown kind) is not capturable and returns false via the default
	case KindPain, KindSymptom, KindIntake, KindElimination, KindMood,
		KindSleep, KindMed, KindIntervention, KindMeasurement, KindMemory, KindLocation,
		KindWithdrawal, KindHabitChange, KindCommitment:
		return true
	default:
		return false
	}
}

// ParseInput is one capture turn's raw material: the resolved kind and class,
// the argument tokens after the verb, and the clocks the parser needs to
// resolve @-backdating and default occurred_at.
type ParseInput struct {
	Kind      Kind
	Class     string
	Args      []string
	Now       time.Time
	SpelledOK bool
}

// ParseResult is the structured product of one capture: the fields the
// storage adapter needs to build a frozen [Event], plus the place name for a
// location capture (the router resolves that to a registry key), and the
// Partial flag for the capture-never-blocks path.
type ParseResult struct {
	Kind        Kind
	OccurredAt  time.Time
	Precision   string
	OccurredEnd *time.Time
	Payload     map[string]any
	Tags        []string
	Refs        map[string]any
	PlaceName   string // set for context.location; router resolves place_ref
	Partial     bool
}

// ParseMicrolog turns one capture turn into a [ParseResult] (observations.md
// §4, the capture grammar). It is deterministic and total — every input
// yields a well-formed result, because capture never blocks: a head that
// fails its kind's grammar takes the partial path (kind kept, full text to
// payload.note). @-backdating is honored anywhere in the line; #tags are
// copied into Tags and left verbatim in the note.
func ParseMicrolog(in ParseInput) ParseResult {
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}

	// Resolve occurred_at / precision from any @-token, then strip the tokens
	// it consumed so the head parser sees only the value text.
	occ, prec, end, rest := extractBackdate(in.Args, now)

	res := ParseResult{
		Kind:       in.Kind,
		OccurredAt: occ,
		Precision:  prec,
		Payload:    map[string]any{},
		Refs:       map[string]any{},
	}
	res.Tags = collectTags(rest)

	// Sleep always describes the night ending this morning: a range whose
	// logical day is the night's start, regardless of the input form.
	if in.Kind == KindSleep {
		parseSleep(&res, rest, now)
		res.Precision = PrecisionRange
		if res.OccurredEnd == nil {
			res.OccurredAt = DateOf(now).AddDate(0, 0, -1).Add(23 * time.Hour)
			e := now
			res.OccurredEnd = &e
			res.Precision = PrecisionApproximate // time-less: an approximate anchor
		}
		return res
	}
	res.OccurredEnd = end
	parseKindHead(&res, rest, in)
	return res
}

// parseKindHead dispatches to the per-kind head parser for a micro-log whose
// occurred_at, precision, tags, and range end are already resolved. Sleep is
// handled ahead of this by ParseMicrolog (it rewrites occurred_at wholesale);
// every other capturable kind fills its payload here, and an unknown-but-
// capturable kind keeps the free text. Splitting the dispatch out keeps
// ParseMicrolog's totality contract readable.
func parseKindHead(res *ParseResult, rest []string, in ParseInput) {
	switch in.Kind { //nolint:exhaustive // deliberately partial: sleep is handled ahead of this dispatch, and context.day / any unknown kind fall to the free-text default
	case KindPain:
		parseScaleKind(res, rest, in, "intensity", 0, 10, true, parsePainTail)
	case KindMood:
		parseScaleKind(res, rest, in, "level", 1, 5, true, parseMoodTail)
	case KindElimination:
		res.Payload["class"] = classOr(in.Class, "bm")
		parseScaleKind(res, rest, in, "bristol", 1, 7, false, nil)
	case KindSymptom:
		parseSymptom(res, rest, in)
	case KindIntake:
		parseIntake(res, rest, in)
	case KindMed:
		parseMed(res, rest)
	case KindIntervention:
		setFree(res, "what", rest)
	case KindMeasurement:
		parseMeasurement(res, rest)
	case KindMemory:
		parseMemory(res, rest)
	case KindLocation:
		place := strings.TrimSpace(strings.Join(rest, " "))
		if place == "" {
			markPartial(res, rest)
			return
		}
		res.PlaceName = place
	case KindWithdrawal:
		// Optional 0–10 severity head, trailing text to note (voice-to-text:
		// `/obs withdrawal 6 rough morning`; `/obs withdrawal groggy` → note).
		parseScaleKind(res, rest, in, "severity", 0, 10, false, nil)
	case KindHabitChange:
		// Optional 0–10 change-load head, trailing text to note
		// (`/obs habit_change 7 cut coffee`).
		parseScaleKind(res, rest, in, "load", 0, 10, false, nil)
	case KindCommitment:
		// Free-text commitment testimony (`/obs commitment call the dentist`).
		setFree(res, "what", rest)
	default:
		// A capturable kind with no special head rule: keep the free text.
		setFree(res, "note", rest)
	}
}

// scaleTail is a per-kind hook that fills the fields after a parsed scale
// digit (site/side for pain, word for mood). It receives the tokens after the
// digit; nil means "no tail, trailing text is the note".
type scaleTail func(res *ParseResult, tail []string)

// parseScaleKind handles a kind whose head is a scale digit. When required is
// true (pain intensity, mood level) a missing or non-numeric head takes the
// partial path (observations.md §4: "/mood wired → partial"); when false
// (bristol) a missing head is a valid bare event but a present-and-out-of-
// range digit still takes the partial path (error-states: "/bm 9 → partial,
// never clamped"). field is the payload key for the parsed value.
func parseScaleKind(res *ParseResult, tokens []string, in ParseInput, field string, lo, hi int, required bool, tail scaleTail) {
	if len(tokens) == 0 {
		if required {
			markPartial(res, tokens)
		}
		return
	}
	head := tokens[0]
	val, numeric := parseScaleToken(head, in.SpelledOK)
	switch {
	case numeric && val >= lo && val <= hi:
		res.Payload[field] = val
		if tail != nil {
			tail(res, tokens[1:])
		} else {
			setNote(res, tokens[1:])
		}
	case numeric:
		// A digit was given but it is out of range — never silently clamped.
		markPartial(res, tokens)
	case required:
		// Required scale, non-numeric head (e.g. `/mood wired`).
		markPartial(res, tokens)
	default:
		// Optional scale, non-numeric head: no value, treat the text as note.
		if tail != nil {
			tail(res, tokens)
		} else {
			setNote(res, tokens)
		}
	}
}

// parsePainTail fills site/side and the trailing note for a pain event. Site
// is one token after the digit (registry matching is unambiguous-only and
// deferred); a left/right/bilateral token anywhere sets side.
func parsePainTail(res *ParseResult, tail []string) {
	if side, rest, ok := takeSide(tail); ok {
		res.Payload["side"] = side
		tail = rest
	}
	if len(tail) > 0 {
		res.Payload["site"] = tail[0]
		tail = tail[1:]
	}
	setNote(res, tail)
}

// parseMoodTail fills the one-word mood label and the trailing note.
func parseMoodTail(res *ParseResult, tail []string) {
	if len(tail) > 0 {
		res.Payload["word"] = tail[0]
		tail = tail[1:]
	}
	setNote(res, tail)
}

// parseSymptom sets the symptom name and an optional 0–10 severity.
func parseSymptom(res *ParseResult, tokens []string, in ParseInput) {
	if len(tokens) == 0 {
		markPartial(res, tokens)
		return
	}
	res.Payload["name"] = tokens[0]
	rest := tokens[1:]
	if len(rest) == 0 {
		return
	}
	v, numeric := parseScaleToken(rest[0], in.SpelledOK)
	switch {
	case !numeric:
		setNote(res, rest) // a non-numeric tail is the note, not a severity
	case v < 0 || v > 10:
		markPartial(res, tokens) // an out-of-range severity → partial
	default:
		res.Payload["severity"] = v
		setNote(res, rest[1:])
	}
}

// parseIntake sets class/amount/what (observations.md §4 head rules): a
// leading unit-suffixed token is amount, a bare integer belongs to what.
func parseIntake(res *ParseResult, tokens []string, in ParseInput) {
	class := in.Class
	if class == "" && len(tokens) > 0 && isIntakeClass(strings.ToLower(tokens[0])) {
		class = strings.ToLower(tokens[0])
		tokens = tokens[1:]
	}
	res.Payload["class"] = classOr(class, "food")
	if len(tokens) > 0 && isAmount(tokens[0]) {
		res.Payload["amount"] = tokens[0]
		tokens = tokens[1:]
	}
	res.Payload["what"] = strings.Join(tokens, " ")
}

// parseMed sets what/dose and taken (default true; a deliberate skip is
// logged as taken:false — the adherence record, never enforcement).
func parseMed(res *ParseResult, tokens []string) {
	if len(tokens) == 0 {
		markPartial(res, tokens)
		return
	}
	res.Payload["taken"] = true
	res.Payload["what"] = tokens[0]
	rest := tokens[1:]
	if len(rest) > 0 && startsWithDigit(rest[0]) {
		// A dose is a number, bare (400 → mg implied) or unit-suffixed (400mg).
		res.Payload["dose"] = rest[0]
		rest = rest[1:]
	}
	setNote(res, rest)
}

// parseMeasurement sets metric/value/unit for a numeric reading.
func parseMeasurement(res *ParseResult, tokens []string) {
	if len(tokens) < 2 {
		markPartial(res, tokens)
		return
	}
	res.Payload["metric"] = tokens[0]
	res.Payload["value"] = tokens[1]
	rest := tokens[2:]
	if len(rest) > 0 {
		res.Payload["unit"] = rest[0]
		rest = rest[1:]
	}
	setNote(res, rest)
}

// parseMemory sets certainty (when the head is a certainty keyword) and the
// verbatim memory text — the quick token grammar for a memory. The rich
// convention keys (tone/why_it_matters/follow_up/people) are filled by the
// structured story-capture verb through [ParseMemoryFields], which shares this
// key vocabulary.
func parseMemory(res *ParseResult, tokens []string) {
	if len(tokens) > 0 && IsMemoryCertainty(strings.ToLower(tokens[0])) {
		res.Payload[MemoryFieldCertainty] = strings.ToLower(tokens[0])
		tokens = tokens[1:]
	}
	if len(tokens) == 0 {
		markPartial(res, nil)
		return
	}
	res.Payload[MemoryFieldText] = strings.Join(tokens, " ")
}

// MemoryInput carries the structured convention fields for one excavated story
// memory (mvp/life-archive.md §3) — the rich counterpart to the `/obs memory`
// token grammar [parseMemory]. Only Text anchors the memory; every other field
// is optional. It keeps the memory vocabulary in this pure package beside the
// envelope it writes to, so the life-archive write verb builds a payload without
// re-encoding the field names.
type MemoryInput struct {
	Text         string
	Certainty    string
	Tone         string
	WhyItMatters string
	FollowUp     string
	People       []string
}

// ParseMemoryFields builds a KindMemory payload from the structured convention
// fields on the frozen envelope (mvp/life-archive.md §3; schema stays 1, no new
// top-level field). It keeps the capture-never-blocks contract: an empty Text
// yields the partial payload ({note, parse}) exactly like [parseMemory]'s
// empty-text path, so a structured story with nothing to anchor it is kept, not
// dropped. An out-of-vocabulary certainty is omitted rather than stored (the
// write verb validates it up front); people are trimmed and de-blanked, kept as
// testimony even when no person key resolves.
func ParseMemoryFields(in MemoryInput) (payload map[string]any, partial bool) {
	text := strings.TrimSpace(in.Text)
	if text == "" {
		return map[string]any{"note": "", "parse": ParseMarkerPartial}, true
	}
	payload = map[string]any{MemoryFieldText: text}
	if c := strings.ToLower(strings.TrimSpace(in.Certainty)); IsMemoryCertainty(c) {
		payload[MemoryFieldCertainty] = c
	}
	putPayloadStr(payload, MemoryFieldTone, in.Tone)
	putPayloadStr(payload, MemoryFieldWhyItMatters, in.WhyItMatters)
	putPayloadStr(payload, MemoryFieldFollowUp, in.FollowUp)
	if people := cleanTokens(in.People); len(people) > 0 {
		payload[MemoryFieldPeople] = people
	}
	return payload, false
}

// putPayloadStr sets a payload key only when its trimmed value is non-empty, so
// an unset convention field leaves no key.
func putPayloadStr(m map[string]any, key, val string) {
	if v := strings.TrimSpace(val); v != "" {
		m[key] = v
	}
}

// cleanTokens trims each entry and drops blanks, returning nil when nothing
// survives — an unset people list leaves no payload key.
func cleanTokens(in []string) []string {
	var out []string
	for _, s := range in {
		if v := strings.TrimSpace(s); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// parseSleep sets quality and optional bed/wake times (observations.md §3
// sleep): bed → occurred_at on the prior evening, wake → occurred_at_end this
// morning, so the logical day is the night's start.
func parseSleep(res *ParseResult, tokens []string, now time.Time) {
	var times []time.Time
	var rest []string
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		if q, ok := parseQuality(t); ok {
			res.Payload["quality"] = q
			continue
		}
		if t == "quality" && i+1 < len(tokens) {
			if q, err := strconv.Atoi(tokens[i+1]); err == nil {
				res.Payload["quality"] = q
				i++
				continue
			}
		}
		if hm, ok := parseClockHM(t); ok {
			times = append(times, hm)
			continue
		}
		rest = append(rest, t)
	}
	if len(times) >= 2 {
		bed := DateOf(now).AddDate(0, 0, -1).Add(time.Duration(times[0].Hour())*time.Hour + time.Duration(times[0].Minute())*time.Minute)
		wake := DateOf(now).Add(time.Duration(times[1].Hour())*time.Hour + time.Duration(times[1].Minute())*time.Minute)
		res.OccurredAt = bed
		res.OccurredEnd = &wake
	}
	setNote(res, rest)
}

// ResolveBackdate resolves a single date token (a `--day` flag) through the
// same @-grammar [ParseMicrolog] applies inline (observations.md §4), so a
// structured write verb backdates identically to an inline @-token. A leading
// @ is optional. An empty arg is now at exact precision; a bare date or
// @yesterday is approximate (a placeholder past date, never rolled over so an
// old memory keeps its own calendar day); a range yields an end. It never
// blocks — an unrecognized token falls back to now at exact precision, keeping
// capture total (product-principles.md P10).
func ResolveBackdate(dayArg string, now time.Time) (occ time.Time, precision string, end *time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	arg := strings.TrimSpace(dayArg)
	if arg == "" {
		return now, PrecisionExact, nil
	}
	if !strings.HasPrefix(arg, "@") {
		arg = "@" + arg
	}
	occ, precision, end, _ = extractBackdate([]string{arg}, now)
	return occ, precision, end
}

// extractBackdate scans the argument tokens for an @-token and resolves it to
// (occurred_at, precision, occurred_at_end), returning the tokens with the
// @-token (and any bare time it consumed) removed (observations.md §4
// "Backdating"). With no @-token the default is now at exact precision.
func extractBackdate(args []string, now time.Time) (occ time.Time, prec string, end *time.Time, rest []string) {
	rest = make([]string, 0, len(args))
	occ, prec = now, PrecisionExact
	handled := false
	for i := 0; i < len(args); i++ {
		tok := args[i]
		if handled || !strings.HasPrefix(tok, "@") {
			rest = append(rest, tok)
			continue
		}
		body := strings.TrimPrefix(tok, "@")
		following := ""
		if i+1 < len(args) {
			following = args[i+1]
		}
		o, p, e, consumed, ok := parseAtToken(body, following, now)
		if !ok {
			// Not a recognized @-form — leave it as text (still verbatim).
			rest = append(rest, tok)
			continue
		}
		occ, prec, end = o, p, e
		handled = true
		if consumed {
			i++ // skip the following bare time token it absorbed
		}
	}
	return occ, prec, end, rest
}

// parseAtToken resolves one @-token body (observations.md §4): a time is
// today at that time (exact); a range yields occurred_at_end; a bare date is
// approximate midnight; a date is promoted to exact when a full time is
// present (either inside the token or as the following bare token).
func parseAtToken(body, following string, now time.Time) (occ time.Time, prec string, end *time.Time, consumedFollowing, ok bool) {
	loc := now.Location()

	// Range within today: HH:MM-HH:MM.
	if a, b, isRange := splitTimeRange(body); isRange {
		start, sok := parseClockHM(a)
		fin, fok := parseClockHM(b)
		if sok && fok {
			s := atTime(DateOf(now), start)
			f := atTime(DateOf(now), fin)
			return s, PrecisionRange, &f, false, true
		}
	}

	// Bare time today (exact moment).
	if hm, isTime := parseClockHM(body); isTime {
		return atTime(DateOf(now), hm), PrecisionExact, nil, false, true
	}

	// yesterday, optionally followed by a bare time.
	if strings.EqualFold(body, "yesterday") {
		day := DateOf(now).AddDate(0, 0, -1)
		if hm, isTime := parseClockHM(following); isTime {
			return atTime(day, hm), PrecisionExact, nil, true, true
		}
		return day, PrecisionApproximate, nil, false, true
	}

	// Full timestamp inside the token: YYYY-MM-DDTHH:MM[:SS].
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04"} {
		if t, err := time.ParseInLocation(layout, body, loc); err == nil {
			return t, PrecisionExact, nil, false, true
		}
	}

	// Bare date (approximate midnight), optionally promoted by a following time.
	if d, err := time.ParseInLocation(dateLayout, body, loc); err == nil {
		if hm, isTime := parseClockHM(following); isTime {
			return atTime(d, hm), PrecisionExact, nil, true, true
		}
		return d, PrecisionApproximate, nil, false, true
	}

	return time.Time{}, "", nil, false, false
}

// collectTags returns the #tag names (without the leading #) in order,
// deduplicated. Tags are copied here but left verbatim in the note text
// (observations.md §4 "Tags and note").
func collectTags(tokens []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range tokens {
		if strings.HasPrefix(t, "#") && len(t) > 1 {
			name := strings.TrimPrefix(t, "#")
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	return out
}

// markPartial routes a capture that failed its kind's grammar onto the
// partial path: the kind stays, payload is exactly {note, parse} with the
// full text verbatim (observations.md §4). It clears any partial head fields
// so the on-disk shape matches the documented partial payload.
func markPartial(res *ParseResult, tokens []string) {
	res.Partial = true
	res.Payload = map[string]any{
		"note":  strings.Join(tokens, " "),
		"parse": ParseMarkerPartial,
	}
}

// setNote assigns the verbatim trailing note when non-empty.
func setNote(res *ParseResult, tokens []string) {
	if note := strings.Join(tokens, " "); note != "" {
		res.Payload["note"] = note
	}
}

// setFree assigns all tokens as one free-text field, or the partial path when
// there is nothing to record.
func setFree(res *ParseResult, field string, tokens []string) {
	v := strings.TrimSpace(strings.Join(tokens, " "))
	if v == "" {
		markPartial(res, tokens)
		return
	}
	res.Payload[field] = v
}

// takeSide pulls a left/right/bilateral token from anywhere in tail, returning
// the side, the remaining tokens (order preserved), and whether one was found.
func takeSide(tail []string) (side string, rest []string, ok bool) {
	for i, t := range tail {
		switch strings.ToLower(t) {
		case "left", "right", "bilateral":
			rest = slices.Concat(tail[:i], tail[i+1:])
			return strings.ToLower(t), rest, true
		}
	}
	return "", tail, false
}

// parseScaleToken parses a scale digit — a plain integer or, when SpelledOK,
// a spelled digit zero–ten (dictation tolerance). It returns numeric=false
// for anything that is not a number, so the caller decides partial vs. note.
func parseScaleToken(tok string, spelledOK bool) (val int, numeric bool) {
	if n, err := strconv.Atoi(tok); err == nil {
		return n, true
	}
	if spelledOK {
		if n, ok := spelledDigit(tok); ok {
			return n, true
		}
	}
	return 0, false
}

// parseQuality parses a q<n> dictation form (observations.md §4).
func parseQuality(tok string) (int, bool) {
	low := strings.ToLower(tok)
	if strings.HasPrefix(low, "q") && len(low) > 1 {
		if n, err := strconv.Atoi(low[1:]); err == nil {
			return n, true
		}
	}
	return 0, false
}

// parseClockHM parses a wall-clock token in HH:MM or colon-less HHMM form
// (observations.md §4 "colon-less times"). It returns a time whose only
// meaningful components are hour and minute.
func parseClockHM(tok string) (time.Time, bool) {
	if tok == "" {
		return time.Time{}, false
	}
	if strings.Contains(tok, ":") {
		if t, err := time.Parse("15:04", tok); err == nil {
			return t, true
		}
		if t, err := time.Parse("3:04", tok); err == nil {
			return t, true
		}
		return time.Time{}, false
	}
	// Colon-less HHMM (e.g. 2340, 0710).
	if len(tok) == 4 {
		if t, err := time.Parse("1504", tok); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// splitTimeRange splits an HH:MM-HH:MM range body, reporting whether it looks
// like a range at all.
func splitTimeRange(body string) (a, b string, isRange bool) {
	parts := strings.SplitN(body, "-", 2)
	if len(parts) != 2 || !strings.Contains(parts[0], ":") {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// atTime returns day at the hour/minute of hm.
func atTime(day, hm time.Time) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), hm.Hour(), hm.Minute(), 0, 0, day.Location())
}

// isAmount reports whether a token is a unit-suffixed amount (500ml, 40g, x2)
// rather than a bare quantity that belongs to the free-text head.
func isAmount(tok string) bool {
	if tok == "" {
		return false
	}
	if strings.HasPrefix(strings.ToLower(tok), "x") {
		if _, err := strconv.Atoi(tok[1:]); err == nil {
			return true
		}
	}
	// A leading digit run followed by a non-digit unit suffix (ml, g, mg…).
	digits := 0
	for digits < len(tok) && tok[digits] >= '0' && tok[digits] <= '9' {
		digits++
	}
	return digits > 0 && digits < len(tok)
}

// startsWithDigit reports whether a token begins with an ASCII digit — the
// cheap test for "this leading token is a dose/value, not free text".
func startsWithDigit(tok string) bool {
	return tok != "" && tok[0] >= '0' && tok[0] <= '9'
}

// classOr returns v, or the fallback when v is empty.
func classOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// spelledDigit maps a spelled English digit zero–ten to its value.
func spelledDigit(tok string) (int, bool) {
	switch strings.ToLower(tok) {
	case "zero":
		return 0, true
	case "one":
		return 1, true
	case "two":
		return 2, true
	case "three":
		return 3, true
	case "four":
		return 4, true
	case "five":
		return 5, true
	case "six":
		return 6, true
	case "seven":
		return 7, true
	case "eight":
		return 8, true
	case "nine":
		return 9, true
	case "ten":
		return 10, true
	default:
		return 0, false
	}
}
