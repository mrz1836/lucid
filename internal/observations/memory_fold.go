package observations

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
)

// Memory amendment refs (mvp/data-model.md, the append-only memory-amendment
// record on the frozen envelope). A `lucid memory` story is one KindMemory
// event and the envelope never changes (envelope.go §Schema), so an amend is a
// *new* appended KindMemory event that carries only the changed fields plus
// these refs — the same append-only-correction convention the storage layer
// documents for every kind (observations_test.go, "a correction is a new
// appended event carrying refs={corrects: orig.ID}; the original line stays
// byte-identical"). refsCorrects names the base memory being amended; refsEra
// overlays the era key; refsCleared names payload fields the amendment removes.
// They are refs, not new envelope fields — the envelope stays frozen at schema
// 1 (observations.md §2).
const (
	refsCorrects = "corrects"
	refsEra      = "era"
	refsCleared  = "cleared"
)

// MemoryFieldChange is one field's transition recorded by a single amendment,
// in chronological order — the per-field trail `lucid memory show --history`
// renders (original → amended value @ timestamp). Prior is the field's value
// just before this amendment (empty with PriorSet=false when the field was not
// yet present); New is its value after (empty when Cleared is true).
type MemoryFieldChange struct {
	Field      string // "text" | "certainty" | "follow_up" | "era"
	Prior      string // the value just before this amendment ("" if it was unset)
	PriorSet   bool   // whether the field had a value before this amendment
	New        string // the value after this amendment ("" when Cleared)
	Cleared    bool   // true when this amendment removed the field
	RecordedAt string // the amendment's recorded_at timestamp
}

// FoldMemoryAmendments collapses base memory events and their amendments into
// the effective current memories (mvp/data-model.md, fold-on-read). An
// amendment is a KindMemory event whose refs.corrects names a base memory; the
// fold overlays only the fields each amendment carries (last-write-wins per
// field, like the engine's Folded() field-merge — not reframe's whole-entry
// supersede), applies its refs.era overlay, and removes every field named in
// refs.cleared. Amendments are dropped from the output; every non-KindMemory
// event passes through untouched, so the fold is safe to call on a mixed /day
// slice. An amendment whose target base is absent from the input is dropped too
// (a bare amendment never renders). The fold is pure: base events are folded
// onto a deep copy, so the input slice is never mutated. Input order is
// preserved — a by-id-sorted input yields a by-id-sorted result.
func FoldMemoryAmendments(events []Event) []Event {
	byTarget := groupMemoryAmendments(events)

	out := make([]Event, 0, len(events))
	for _, e := range events {
		// Drop amendment events — their effect lives on the folded base.
		if _, ok := memoryCorrects(e); ok {
			continue
		}
		// Non-memory events (and memory events without amendments) pass through
		// untouched.
		if e.Kind != KindMemory {
			out = append(out, e)
			continue
		}
		amendments := byTarget[e.ID]
		if len(amendments) == 0 {
			out = append(out, e)
			continue
		}
		folded := cloneForFold(e)
		for _, a := range amendments {
			applyMemoryAmendment(&folded, a)
		}
		out = append(out, folded)
	}
	return out
}

// FoldMemoryHistory returns the ordered per-field change trail for one base
// memory (mvp/data-model.md, the `memory show --history` surface): each
// amendment targeting targetID, in chronological (event-id) order, expanded
// into a MemoryFieldChange per field it touched, carrying the prior value, the
// new value (or Cleared), and the amendment's recorded_at. It returns nil when
// targetID names no KindMemory event in the slice, or when it names an
// amendment rather than a base memory (a correction-event id has no history of
// its own). It is pure — no input is mutated.
func FoldMemoryHistory(events []Event, targetID string) []MemoryFieldChange {
	base, ok := findBaseMemory(events, targetID)
	if !ok {
		return nil
	}
	amendments := memoryAmendmentsFor(events, targetID)
	if len(amendments) == 0 {
		return nil
	}

	// Seed the running field state from the base memory, so the first amendment
	// reports the base value as its prior.
	current := map[string]string{}
	present := map[string]bool{}
	for k, v := range base.Payload {
		current[k] = renderFieldValue(v)
		present[k] = true
	}
	if base.Refs != nil {
		if v, ok := base.Refs[refsEra]; ok {
			current[refsEra] = renderFieldValue(v)
			present[refsEra] = true
		}
	}

	var trail []MemoryFieldChange
	for _, a := range amendments {
		for _, f := range touchedFields(a) {
			change := MemoryFieldChange{
				Field:      f,
				Prior:      current[f],
				PriorSet:   present[f],
				RecordedAt: a.RecordedAt,
			}
			if isClearedField(a, f) {
				change.Cleared = true
				delete(current, f)
				delete(present, f)
			} else {
				nv := renderFieldValue(amendFieldValue(a, f))
				change.New = nv
				current[f] = nv
				present[f] = true
			}
			trail = append(trail, change)
		}
	}
	return trail
}

// groupMemoryAmendments buckets every memory amendment in events by the base id
// its refs.corrects names, each bucket sorted by event id so amendments fold in
// chronological order (ids encode a monotonic per-day seq).
func groupMemoryAmendments(events []Event) map[string][]Event {
	byTarget := map[string][]Event{}
	for _, e := range events {
		if target, ok := memoryCorrects(e); ok {
			byTarget[target] = append(byTarget[target], e)
		}
	}
	for target := range byTarget {
		slices.SortStableFunc(byTarget[target], func(a, b Event) int {
			return cmp.Compare(a.ID, b.ID)
		})
	}
	return byTarget
}

// memoryAmendmentsFor returns every amendment targeting targetID, sorted by
// event id (chronological).
func memoryAmendmentsFor(events []Event, targetID string) []Event {
	var out []Event
	for _, e := range events {
		if target, ok := memoryCorrects(e); ok && target == targetID {
			out = append(out, e)
		}
	}
	slices.SortStableFunc(out, func(a, b Event) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// findBaseMemory returns the base memory event named by targetID — a KindMemory
// event that is not itself an amendment. ok is false when no such event exists
// (unknown id, a non-memory kind, or a correction-event id).
func findBaseMemory(events []Event, targetID string) (Event, bool) {
	for _, e := range events {
		if e.ID != targetID || e.Kind != KindMemory {
			continue
		}
		if _, isAmendment := memoryCorrects(e); isAmendment {
			return Event{}, false
		}
		return e, true
	}
	return Event{}, false
}

// memoryCorrects reports the base memory id a KindMemory amendment corrects, and
// whether the event is such an amendment. A non-memory event, or a memory event
// with no non-empty string refs.corrects, is not an amendment.
func memoryCorrects(e Event) (string, bool) {
	if e.Kind != KindMemory || e.Refs == nil {
		return "", false
	}
	v, ok := e.Refs[refsCorrects]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok || s == "" {
		return "", false
	}
	return s, true
}

// applyMemoryAmendment overlays one amendment onto a (already-cloned) base
// memory: it sets every payload key the amendment carries, overlays refs.era
// when present, and then removes every payload field named in refs.cleared. Set
// keys and cleared fields are disjoint within a single amendment (the write path
// rejects setting and clearing the same field), so clears are applied last as a
// defensive last-write.
func applyMemoryAmendment(base *Event, amend Event) {
	if len(amend.Payload) > 0 {
		if base.Payload == nil {
			base.Payload = map[string]any{}
		}
		for k, v := range amend.Payload {
			base.Payload[k] = v
		}
	}
	if amend.Refs != nil {
		if era, ok := amend.Refs[refsEra]; ok {
			if base.Refs == nil {
				base.Refs = map[string]any{}
			}
			base.Refs[refsEra] = era
		}
	}
	for _, field := range clearedFields(amend.Refs) {
		delete(base.Payload, field)
	}
}

// touchedFields returns the fields an amendment changes — the payload keys it
// sets, era when its refs carry one, and every cleared field — de-duplicated and
// in a stable (sorted) order so the history trail is deterministic.
func touchedFields(a Event) []string {
	seen := map[string]bool{}
	var fields []string
	add := func(f string) {
		if !seen[f] {
			seen[f] = true
			fields = append(fields, f)
		}
	}
	for k := range a.Payload {
		add(k)
	}
	if a.Refs != nil {
		if _, ok := a.Refs[refsEra]; ok {
			add(refsEra)
		}
	}
	for _, f := range clearedFields(a.Refs) {
		add(f)
	}
	slices.Sort(fields)
	return fields
}

// amendFieldValue returns the value an amendment sets for field f — era from its
// refs, every other field from its payload.
func amendFieldValue(a Event, f string) any {
	if f == refsEra {
		if a.Refs != nil {
			return a.Refs[refsEra]
		}
		return nil
	}
	return a.Payload[f]
}

// isClearedField reports whether an amendment's refs.cleared names field f.
func isClearedField(a Event, f string) bool {
	return slices.Contains(clearedFields(a.Refs), f)
}

// clearedFields decodes the refs.cleared marker into the payload field names an
// amendment removes. It tolerates both the persisted JSON shape (a []any of
// strings, the only form an event read back from disk ever carries) and a direct
// in-memory []string, and ignores any non-string / empty entry rather than
// panicking.
func clearedFields(refs map[string]any) []string {
	if refs == nil {
		return nil
	}
	raw, ok := refs[refsCleared]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		out := make([]string, 0, len(v))
		for _, s := range v {
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	case []any:
		var out []string
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// cloneForFold returns a deep-enough copy of a memory event for folding: the
// payload, refs, and tags collections are cloned so overlaying an amendment
// never mutates the caller's event. Nil collections become empty maps so a
// subsequent set has somewhere to write.
func cloneForFold(e Event) Event {
	out := e
	if e.Payload != nil {
		out.Payload = maps.Clone(e.Payload)
	} else {
		out.Payload = map[string]any{}
	}
	if e.Refs != nil {
		out.Refs = maps.Clone(e.Refs)
	} else {
		out.Refs = map[string]any{}
	}
	out.Tags = slices.Clone(e.Tags)
	return out
}

// renderFieldValue renders a payload/ref value as the string the history trail
// shows. Memory amendable fields (text/certainty/follow_up/era) are strings; any
// other shape falls back to fmt's default so the helper never panics.
func renderFieldValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	default:
		return fmt.Sprintf("%v", t)
	}
}
