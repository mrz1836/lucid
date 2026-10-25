package engine

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// AnchorEventsVersion is the anchor event store's schema version, stamped on
// every write (engine-module.md §"Anchor event store"). It is a constant of its
// own, deliberately separate from AnchorVersion: the event store and
// anchors.json evolve independently, and anchors.json stays at version 3. Like
// AnchorVersion it is a signal, not a gate — reads never check it.
const AnchorEventsVersion = 1

// The four anchor kinds. An anchor with no kind event is a milestone, so a
// Ledger without the event store reads exactly as it always has.
const (
	AnchorKindMilestone = "milestone"
	AnchorKindQuit      = "quit"
	AnchorKindLimit     = "limit"
	AnchorKindHabit     = "habit"
)

// The anchor event types the store holds.
const (
	AnchorEventKind     = "kind"
	AnchorEventSlip     = "slip"
	AnchorEventUse      = "use"
	AnchorEventMiss     = "miss"
	AnchorEventConfirm  = "confirm"
	AnchorEventVoid     = "void"
	AnchorEventGraduate = "graduate"
)

// The two intents a limit use is logged with. Intent is required: the binary
// never guesses whether a use was planned.
const (
	AnchorIntentPlanned = "planned"
	AnchorIntentSlip    = "slip"
)

// AnchorEventIDPrefix is the event-id namespace: anchorev_YYYY_MM_DD_<slot>.
// It can never collide with an anchor_ id or an anchor:<id> link subject.
const AnchorEventIDPrefix = "anchorev_"

// DefaultHabitWindowDays is a habit's rolling window when its kind event
// carries none — the same 30 days the adherence window uses.
const DefaultHabitWindowDays = 30

// The two orders a two-file write follows (engine-module.md §"Two-file verbs
// and the write order"). compat_first writes the v3 record before the event;
// event_first writes the event before the v3 record.
const (
	AnchorPairCompatFirst = "compat_first"
	AnchorPairEventFirst  = "event_first"
)

// AnchorEvent is one record in the anchor event store: a kind, or a fact
// logged against an anchor. Every event carries the common fields; each type
// adds its own (kind: Kind/Allowance/WindowDays; use: Intent; confirm: From;
// void: Target). Day is the civil logical day the event is about — for a
// confirm, the through-day of its span. Unknown fields are tolerated on read.
//
// Every field is a string or an int, so the struct is comparable: a pair
// intent's planned event is matched against the store by plain equality.
type AnchorEvent struct {
	ID         string `json:"id"`
	AnchorID   string `json:"anchor_id"`
	Type       string `json:"type"`
	Day        string `json:"day"`
	RecordedAt string `json:"recorded_at"`
	Kind       string `json:"kind,omitempty"`
	Allowance  int    `json:"allowance,omitempty"`
	WindowDays int    `json:"window_days,omitempty"`
	Intent     string `json:"intent,omitempty"`
	From       string `json:"from,omitempty"`
	Target     string `json:"target,omitempty"`
	Note       string `json:"note,omitempty"`
}

// AnchorPairIntent is the transaction record a two-file verb appends before
// either of its domain halves: it reserves Event.ID and holds the exact
// planned event, the exact planned v3 record, and the order they are written
// in. It is metadata, never an applied fact — the fold never reads Event as an
// event. Completion is inferred from the presence of the exact event in the
// store and the exact record in anchors.json; an intent is never edited or
// removed.
type AnchorPairIntent struct {
	Order  string      `json:"order"`
	Event  AnchorEvent `json:"event"`
	Compat Anchor      `json:"compat"`
}

// AnchorEventLog is anchor_events.json: the schema version, the append-only
// events, and the append-only pair intents (absent when unused).
type AnchorEventLog struct {
	Version     int                `json:"version"`
	Events      []AnchorEvent      `json:"events"`
	PairIntents []AnchorPairIntent `json:"pair_intents,omitempty"`
}

// NextAnchorEventID mints the next free event id for the given day:
// anchorev_YYYY_MM_DD_<slot>, advancing the slot past every id already used
// for that date. It scans every event and every id a pair intent has reserved,
// so an id is never reissued — not even one whose operation never completed.
// It shares the per-day slot grammar (SlotLabel) with NextAnchorID.
func NextAnchorEventID(log AnchorEventLog, day time.Time) string {
	prefix := fmt.Sprintf("%s%04d_%02d_%02d_", AnchorEventIDPrefix, day.Year(), int(day.Month()), day.Day())
	used := map[string]bool{}
	mark := func(id string) {
		if slot, ok := strings.CutPrefix(id, prefix); ok {
			used[slot] = true
		}
	}
	for _, e := range log.Events {
		mark(e.ID)
	}
	for _, p := range log.PairIntents {
		mark(p.Event.ID)
	}
	for n := 0; ; n++ {
		slot := SlotLabel(n)
		if !used[slot] {
			return prefix + slot
		}
	}
}

// IsAnchorKind reports whether kind names one of the four anchor kinds.
func IsAnchorKind(kind string) bool {
	switch kind {
	case AnchorKindMilestone, AnchorKindQuit, AnchorKindLimit, AnchorKindHabit:
		return true
	}
	return false
}

// ValidateKindParams reports whether a kind and its parameters are legal for a
// kind event. Zero means "not given". A limit requires both a positive
// allowance (use-days) and a positive window (days); a habit may carry a
// positive window and nothing else; a milestone or a quit takes neither. The
// text after the "engine: " prefix is the user-facing rejection copy.
func ValidateKindParams(kind string, allowance, window int) error {
	if !IsAnchorKind(kind) {
		return fmt.Errorf("engine: %q is not an anchor kind — the kinds are milestone, quit, limit and habit", kind)
	}
	if allowance < 0 || (allowance != 0 && kind != AnchorKindLimit) {
		return fmt.Errorf("engine: --allowance is a positive whole number of use-days, and only a limit takes it")
	}
	if window < 0 || (window != 0 && kind != AnchorKindLimit && kind != AnchorKindHabit) {
		return fmt.Errorf("engine: --window is a positive whole number of days, and only a limit or a habit takes it")
	}
	if kind == AnchorKindLimit && (allowance == 0 || window == 0) {
		return fmt.Errorf("engine: a limit needs both --allowance N and --window D — N use-days per D days")
	}
	return nil
}

// validSlotID reports whether id is prefix + YYYY_MM_DD_<slot>, with a real
// calendar date and a non-empty lowercase slot.
func validSlotID(id, prefix string) bool {
	const dayLen = len("2006_01_02")
	rest, ok := strings.CutPrefix(id, prefix)
	if !ok || len(rest) < dayLen+2 || rest[dayLen] != '_' {
		return false
	}
	if _, err := time.Parse("2006_01_02", rest[:dayLen]); err != nil {
		return false
	}
	for _, c := range rest[dayLen+1:] {
		if c < 'a' || c > 'z' {
			return false
		}
	}
	return true
}

// ValidateAnchorEvent reports whether one stored event is well formed: the
// event-id grammar, a minted anchor id to target (never a read-time legacy
// identity, which is never written), a known type, a civil day, an RFC3339
// recorded_at, and the fields its type requires. It checks the record alone;
// whether its anchor exists, or its void target is legal, depends on the rest
// of the store. The fold skips a record that fails here, and `lucid validate`
// reports it, so a hand-corrupted record never panics a read.
func ValidateAnchorEvent(e AnchorEvent) error {
	if !validSlotID(e.ID, AnchorEventIDPrefix) {
		return fmt.Errorf("engine: anchor event id %q is not anchorev_YYYY_MM_DD_<slot>", e.ID)
	}
	if !validSlotID(e.AnchorID, AnchorIDPrefix) {
		return fmt.Errorf("engine: anchor event %s targets %q, not a minted anchor_ id", e.ID, e.AnchorID)
	}
	if _, err := time.Parse(dateLayout, e.Day); err != nil {
		return fmt.Errorf("engine: anchor event %s has day %q, not a YYYY-MM-DD date", e.ID, e.Day)
	}
	if _, err := time.Parse(time.RFC3339, e.RecordedAt); err != nil {
		return fmt.Errorf("engine: anchor event %s has recorded_at %q, not an RFC3339 time", e.ID, e.RecordedAt)
	}
	switch e.Type {
	case AnchorEventKind:
		if err := ValidateKindParams(e.Kind, e.Allowance, e.WindowDays); err != nil {
			return fmt.Errorf("engine: anchor event %s: %w", e.ID, err)
		}
	case AnchorEventUse:
		if e.Intent != AnchorIntentPlanned && e.Intent != AnchorIntentSlip {
			return fmt.Errorf("engine: anchor event %s is a use with intent %q, not planned or slip", e.ID, e.Intent)
		}
	case AnchorEventConfirm:
		if _, err := time.Parse(dateLayout, e.From); err != nil || e.From > e.Day {
			return fmt.Errorf("engine: anchor event %s is a confirm whose from %q is not a YYYY-MM-DD date on or before %s", e.ID, e.From, e.Day)
		}
	case AnchorEventVoid:
		if !validSlotID(e.Target, AnchorEventIDPrefix) {
			return fmt.Errorf("engine: anchor event %s is a void with target %q, not an anchorev_ id", e.ID, e.Target)
		}
	case AnchorEventSlip, AnchorEventMiss, AnchorEventGraduate:
	default:
		return fmt.Errorf("engine: anchor event %s has unknown type %q", e.ID, e.Type)
	}
	return nil
}

// The states a pair intent can be in, read from the two files.
const (
	// AnchorPairComplete — both halves are present.
	AnchorPairComplete = "complete"
	// AnchorPairIntentOnly — the intent was written, neither half.
	AnchorPairIntentOnly = "intent_only"
	// AnchorPairCompatOnly — the v3 record is present, the event is missing.
	AnchorPairCompatOnly = "compat_only"
	// AnchorPairEventOnly — the event is present, the v3 record is missing.
	AnchorPairEventOnly = "event_only"
	// AnchorPairMalformed — the intent itself is not well formed; the fold
	// ignores it and `lucid validate` reports it.
	AnchorPairMalformed = "malformed"
)

// AnchorPairState is one pair intent with the state the two files put it in.
// CompatIndex is the position in anchors.json's history of the record that is
// its v3 half, or -1 when that record is absent. Err explains a malformed
// intent and is nil otherwise.
type AnchorPairState struct {
	Intent      AnchorPairIntent
	Status      string
	CompatIndex int
	Err         error
}

// ValidateAnchorPairIntent reports whether a pair intent is well formed: a
// known order, a well-formed planned event of a type that is written as a pair
// in the order that type uses, and a planned v3 record for the same anchor,
// stamped with the same recorded_at, whose state matches what the verb writes
// (a plain sunset for a graduation, an active definition otherwise).
func ValidateAnchorPairIntent(p AnchorPairIntent) error {
	if p.Order != AnchorPairCompatFirst && p.Order != AnchorPairEventFirst {
		return fmt.Errorf("engine: pair intent %s has unknown order %q", p.Event.ID, p.Order)
	}
	if err := ValidateAnchorEvent(p.Event); err != nil {
		return fmt.Errorf("engine: pair intent: %w", err)
	}
	var wantOrder string
	switch p.Event.Type {
	case AnchorEventSlip:
		wantOrder = AnchorPairCompatFirst
	case AnchorEventVoid, AnchorEventGraduate:
		wantOrder = AnchorPairEventFirst
	case AnchorEventKind:
		// A reclassify to quit re-dates first; add --kind writes its kind event
		// first. A compat-first kind event is only ever a reclassify to quit.
		if p.Order == AnchorPairCompatFirst && p.Event.Kind != AnchorKindQuit {
			return fmt.Errorf("engine: pair intent %s re-dates ahead of a %s kind event; only a reclassify to quit does", p.Event.ID, p.Event.Kind)
		}
		wantOrder = p.Order
	default:
		return fmt.Errorf("engine: pair intent %s plans a %s event, which is never written as a pair", p.Event.ID, p.Event.Type)
	}
	if p.Order != wantOrder {
		return fmt.Errorf("engine: pair intent %s writes a %s event %s; it is always %s", p.Event.ID, p.Event.Type, p.Order, wantOrder)
	}
	if p.Compat.ID != p.Event.AnchorID {
		return fmt.Errorf("engine: pair intent %s pairs an event for %s with a record for %q", p.Event.ID, p.Event.AnchorID, p.Compat.ID)
	}
	if p.Compat.RecordedAt != p.Event.RecordedAt {
		return fmt.Errorf("engine: pair intent %s halves disagree on recorded_at (%s vs %s)", p.Event.ID, p.Event.RecordedAt, p.Compat.RecordedAt)
	}
	if err := ValidateAnchor(p.Compat.Label, p.Compat.Date); err != nil {
		return fmt.Errorf("engine: pair intent %s: %w", p.Event.ID, err)
	}
	wantState := ""
	if p.Event.Type == AnchorEventGraduate {
		wantState = AnchorStateSunset
	}
	if p.Compat.State != wantState {
		return fmt.Errorf("engine: pair intent %s plans a record with state %q for a %s event; want %q", p.Event.ID, p.Compat.State, p.Event.Type, wantState)
	}
	return nil
}

// redates reports whether the intent's v3 half is a compat re-date — a record
// that moves a quit's effective date — rather than a definition of its own: a
// slip's re-date, a slip void's restoring record, or a reclassify to quit's
// re-date. The definition an add --kind writes and the sunset a graduation
// writes are ordinary records, not re-dates.
func (p AnchorPairIntent) redates() bool {
	switch p.Event.Type {
	case AnchorEventSlip, AnchorEventVoid:
		return true
	case AnchorEventKind:
		return p.Order == AnchorPairCompatFirst
	}
	return false
}

// AnchorPairStates reads every pair intent against the two files, in intent
// order. An intent's v3 half is the first record in anchors.json exactly equal
// to its planned record that no earlier intent has claimed; its event half is
// an event exactly equal to its planned event. A shared anchor id and
// recorded_at alone are never treated as proof of a pair.
func AnchorPairStates(log AnchorEventLog, anchors AnchorLog) []AnchorPairState {
	present := make(map[AnchorEvent]bool, len(log.Events))
	for _, e := range log.Events {
		present[e] = true
	}
	claimed := map[int]bool{}
	out := make([]AnchorPairState, 0, len(log.PairIntents))
	for _, p := range log.PairIntents {
		st := AnchorPairState{Intent: p, CompatIndex: -1}
		if err := ValidateAnchorPairIntent(p); err != nil {
			st.Status, st.Err = AnchorPairMalformed, err
			out = append(out, st)
			continue
		}
		for i, a := range anchors.History {
			if !claimed[i] && a == p.Compat {
				st.CompatIndex = i
				claimed[i] = true
				break
			}
		}
		hasCompat, hasEvent := st.CompatIndex >= 0, present[p.Event]
		switch {
		case hasCompat && hasEvent:
			st.Status = AnchorPairComplete
		case hasCompat:
			st.Status = AnchorPairCompatOnly
		case hasEvent:
			st.Status = AnchorPairEventOnly
		default:
			st.Status = AnchorPairIntentOnly
		}
		out = append(out, st)
	}
	return out
}

// AnchorFacts is one anchor's folded view of both files: its effective kind
// and parameters, its kind history, and its non-voided facts by type, all in
// append order. It is what the per-kind math reads; the fold applies no clock.
//
// Start is the quit start (PD-5): the date of the latest definition record
// that changes the date and is not the re-date half of a completed pair. A
// record that keeps the date it follows — a rename, a plain sunset, an id
// adoption — never moves it. Floor is the latest date of a re-date whose pair
// is still incomplete: a conservative floor on the effective date, never
// ignored merely because an intent names it as a re-date. It is empty when no
// pair is pending.
//
// KindSince is the day of the latest kind event — the day the anchor became
// its current kind, which is where a habit's first confirm starts — and is
// empty for an anchor that never had one. Voided maps each of the anchor's
// voided events to the void that voided it. Graduated is true only for a
// sunset anchor holding a non-voided graduate event whose pair is not
// incomplete: the new binary recognizes a graduation only once its sunset half
// exists. Pending lists the anchor's incomplete pairs.
type AnchorFacts struct {
	AnchorID    string
	Anchor      Anchor
	Kind        string
	Allowance   int
	WindowDays  int
	KindSince   string
	KindHistory []AnchorEvent
	Start       string
	Floor       string
	Slips       []AnchorEvent
	Uses        []AnchorEvent
	Misses      []AnchorEvent
	Confirms    []AnchorEvent
	Graduates   []AnchorEvent
	Voids       []AnchorEvent
	Voided      map[string]string
	Graduated   bool
	Pending     []AnchorPairState
}

// voidableAnchorEvent reports whether a void may target an event of this type.
// A kind event is never voidable — the dated kind events are the parameter
// history — and a void never voids a void.
func voidableAnchorEvent(eventType string) bool {
	switch eventType {
	case AnchorEventSlip, AnchorEventUse, AnchorEventMiss, AnchorEventConfirm, AnchorEventGraduate:
		return true
	}
	return false
}

// applicableAnchorEvents returns the events the fold reads, in append order,
// and the voids that apply. A record that fails ValidateAnchorEvent is
// skipped, as is a later record reusing an id already seen (an id is a
// receipt; a second record under it is a defect, not a second fact). A void
// applies when its target exists, belongs to the same anchor, is of a
// voidable type and is not already voided; the first such void wins.
func applicableAnchorEvents(log AnchorEventLog) (events []AnchorEvent, voided map[string]string) {
	byID := make(map[string]AnchorEvent, len(log.Events))
	events = make([]AnchorEvent, 0, len(log.Events))
	for _, e := range log.Events {
		if ValidateAnchorEvent(e) != nil {
			continue
		}
		if _, dup := byID[e.ID]; dup {
			continue
		}
		byID[e.ID] = e
		events = append(events, e)
	}
	voided = map[string]string{}
	for _, e := range events {
		if e.Type != AnchorEventVoid {
			continue
		}
		target, ok := byID[e.Target]
		if !ok || target.AnchorID != e.AnchorID || !voidableAnchorEvent(target.Type) {
			continue
		}
		if _, already := voided[target.ID]; already {
			continue
		}
		voided[target.ID] = e.ID
	}
	return events, voided
}

// foldAnchorStart walks one anchor's definition records in append order and
// returns its quit start and pending floor (PD-5). Re-date halves of completed
// pairs never move the start; re-date halves of incomplete pairs raise the
// floor instead; any other record moves the start only when its date differs
// from the record before it. A record with an unparseable date is skipped, so
// a hand-corrupted anchors.json cannot set either.
func foldAnchorStart(history []Anchor, indices []int, redate map[int]string) (start, floor string) {
	prev, first := "", ""
	for _, i := range indices {
		rec := history[i]
		if _, err := time.Parse(dateLayout, rec.Date); err != nil {
			continue
		}
		if first == "" {
			first = rec.Date
		}
		switch status, isRedate := redate[i]; {
		case !isRedate:
			if prev == "" || rec.Date != prev {
				start = rec.Date
			}
		case status != AnchorPairComplete:
			floor = max(floor, rec.Date) // civil YYYY-MM-DD orders lexically
		}
		prev = rec.Date
	}
	if start == "" {
		start = first
	}
	return start, floor
}

// anchorPairIndex reads the pair states into what the fold needs: which
// anchors.json records are re-date halves and the state of their pair, the
// event ids whose pair is incomplete, and each anchor's incomplete pairs. A
// malformed intent contributes nothing, so its record reads as an ordinary
// definition.
func anchorPairIndex(log AnchorEventLog, anchors AnchorLog) (
	redate map[int]string, incomplete map[string]bool, pending map[string][]AnchorPairState,
) {
	redate, incomplete, pending = map[int]string{}, map[string]bool{}, map[string][]AnchorPairState{}
	for _, st := range AnchorPairStates(log, anchors) {
		if st.Status == AnchorPairMalformed {
			continue
		}
		if st.Status != AnchorPairComplete {
			incomplete[st.Intent.Event.ID] = true
			pending[st.Intent.Event.AnchorID] = append(pending[st.Intent.Event.AnchorID], st)
		}
		if st.CompatIndex >= 0 && st.Intent.redates() {
			redate[st.CompatIndex] = st.Status
		}
	}
	return redate, incomplete, pending
}

// addFact files one applicable event into its anchor's facts: a voided event
// is recorded as voided and read no further, a kind event joins the kind
// history, and every other type joins its own list. A void is a fact only when
// it is the void that applied to its target.
func (f *AnchorFacts) addFact(e AnchorEvent, voided map[string]string) {
	if by, isVoided := voided[e.ID]; isVoided {
		f.Voided[e.ID] = by
		return
	}
	switch e.Type {
	case AnchorEventKind:
		f.KindHistory = append(f.KindHistory, e)
	case AnchorEventSlip:
		f.Slips = append(f.Slips, e)
	case AnchorEventUse:
		f.Uses = append(f.Uses, e)
	case AnchorEventMiss:
		f.Misses = append(f.Misses, e)
	case AnchorEventConfirm:
		f.Confirms = append(f.Confirms, e)
	case AnchorEventGraduate:
		f.Graduates = append(f.Graduates, e)
	case AnchorEventVoid:
		if voided[e.Target] == e.ID {
			f.Voids = append(f.Voids, e)
		}
	}
}

// settle derives the effective kind from the kind history (the latest kind
// event; none leaves the milestone default), applies the habit window default,
// and decides graduation: a sunset anchor holding a non-voided graduate event
// whose pair is not incomplete.
func (f *AnchorFacts) settle(incomplete map[string]bool) {
	if n := len(f.KindHistory); n > 0 {
		k := f.KindHistory[n-1]
		f.Kind, f.Allowance, f.WindowDays, f.KindSince = k.Kind, k.Allowance, k.WindowDays, k.Day
	}
	if f.Kind == AnchorKindHabit && f.WindowDays == 0 {
		f.WindowDays = DefaultHabitWindowDays
	}
	f.Graduated = f.Anchor.IsSunset() && slices.ContainsFunc(f.Graduates, func(g AnchorEvent) bool {
		return !incomplete[g.ID]
	})
}

// FoldAnchorEvents joins the event store to anchors.json and folds each anchor
// to its AnchorFacts, keyed by folded identity (the adoption remap applied, so
// a backfilled anchor and its pre-id records are one anchor). Every anchor in
// anchors.json gets an entry — sunset and pre-id anchors included — so a
// reader can look any of them up; an anchor no event targets is a milestone.
//
// The kind is the latest kind event in append order. Kind events are never
// voidable, so a void aimed at one is ignored. An event whose anchor is
// unknown is ignored (a dangling add --kind event among them), and so is an
// event that fails validation. A pair intent's planned event is never read as
// a fact; only events in the store are. It is pure: no clock, no disk.
func FoldAnchorEvents(log AnchorEventLog, anchors AnchorLog) map[string]AnchorFacts {
	remap := adoptionRemap(anchors)
	indices := map[string][]int{}
	latest := map[string]Anchor{}
	for i, a := range anchors.History {
		id := foldedAnchorIdentity(a, remap)
		indices[id] = append(indices[id], i)
		latest[id] = a // last write per identity wins (append order)
	}
	redate, incomplete, pending := anchorPairIndex(log, anchors)

	facts := make(map[string]*AnchorFacts, len(latest))
	for id, rec := range latest {
		f := &AnchorFacts{
			AnchorID: id,
			Anchor:   rec,
			Kind:     AnchorKindMilestone,
			Voided:   map[string]string{},
			Pending:  pending[id],
		}
		f.Start, f.Floor = foldAnchorStart(anchors.History, indices[id], redate)
		facts[id] = f
	}

	events, voided := applicableAnchorEvents(log)
	for _, e := range events {
		if f, ok := facts[e.AnchorID]; ok { // no anchor holds a dangling event's id
			f.addFact(e, voided)
		}
	}

	out := make(map[string]AnchorFacts, len(facts))
	for id, f := range facts {
		f.settle(incomplete)
		out[id] = *f
	}
	return out
}
