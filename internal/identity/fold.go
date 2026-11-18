package identity

import (
	"slices"
	"sort"
	"strings"
	"time"
)

// StatusFact is a person's folded status: the latest status event written for
// their exact person_key.
type StatusFact struct {
	// Status is active or dormant.
	Status string
	// Date is the local YYYY-MM-DD the status was recorded ("Dormant since").
	Date string
	// Reason is the one-line --reason-file text, if any.
	Reason string
	// EventID is the status event's receipt id.
	EventID string
}

// Claim is a form's effective claim.
type Claim struct {
	// Form is the form as written on the claim event.
	Form string
	// PersonKey is the claimant, canonicalized through redirects.
	PersonKey string
	// EventID is the claim event's receipt id.
	EventID string
}

// Pin is the settlement of one raw entry's mention of a form.
type Pin struct {
	RawID string
	// Form is the form as written on the pin event.
	Form string
	// PersonKey is the person the mention is pinned to, canonicalized.
	PersonKey string
	// Cause is what settled the mention.
	Cause string
	// EventID is the pin event's receipt id.
	EventID string
}

// Move is one move-ref on a raw entry: its mention moves from one person onto
// another on read. Both keys are canonicalized through redirects.
type Move struct {
	From    string
	To      string
	EventID string
}

// Hold is a pending held mention: one raw entry's mention of a form that no
// pin has settled.
type Hold struct {
	RawID string
	// Form is the form as written on the hold event.
	Form string
	// Reason is exact or first_name.
	Reason string
	// Candidates are the person_keys the mention could mean, canonicalized,
	// sorted, and de-duplicated.
	Candidates []string
	// EventID is the hold event's receipt id.
	EventID string
}

// State is the folded identity log: the current identity facts. The zero
// State (no events) reads as everyone active, with no claims, holds, pins, or
// move-refs.
type State struct {
	status     map[string]StatusFact
	claims     map[string]Claim
	pins       map[mentionKey]Pin
	moves      map[string][]Move
	holds      []Hold
	duplicates []Event
}

// mentionKey addresses one raw entry's mention of a normalized form.
type mentionKey struct {
	rawID string
	form  string
}

// Fold folds events into the current identity facts (data-model.md §"The
// fold"). Events apply in (at, id) order whatever order they are passed in,
// and an event that does not validate is skipped. canon canonicalizes a
// person_key through merge redirects; a nil canon is the identity.
//
//   - Status is keyed on the exact person_key it was written for; the latest
//     wins, and no event means active.
//   - A normalized form has at most one claimant. A claim on a form another
//     person still claims is a duplicate and inert — it never takes effect,
//     even after the standing claim is released — and is reported by
//     [State.Duplicates]. Re-claiming a form one already claims changes
//     nothing. A release by anyone but the claimant changes nothing.
//   - The first pin for a (raw_id, form) settles it; a later one is ignored.
//     A hold is pending while no pin settles its (raw_id, form).
//   - Move-refs apply in order per raw entry.
//
// Claims, pins, move-refs, and hold candidates canonicalize through canon.
func Fold(events []Event, canon func(string) string) State {
	if canon == nil {
		canon = func(k string) string { return k }
	}
	st := State{
		status: map[string]StatusFact{},
		claims: map[string]Claim{},
		pins:   map[mentionKey]Pin{},
		moves:  map[string][]Move{},
	}
	var holds []Event
	for _, e := range sortedEvents(events) {
		switch e.Kind {
		case KindStatus:
			st.status[e.PersonKey] = StatusFact{Status: e.Status, Date: localDate(e.At), Reason: e.Reason, EventID: e.ID}
		case KindClaim:
			if foldClaim(st.claims, e, canon) {
				st.duplicates = append(st.duplicates, e)
			}
		case KindRelease:
			form := NormalizeForm(e.Form)
			if c, ok := st.claims[form]; ok && c.PersonKey == canon(e.PersonKey) {
				delete(st.claims, form)
			}
		case KindPin:
			k := mentionKey{rawID: e.RawID, form: NormalizeForm(e.Form)}
			if _, ok := st.pins[k]; !ok {
				st.pins[k] = Pin{RawID: e.RawID, Form: e.Form, PersonKey: canon(e.PersonKey), Cause: e.Cause, EventID: e.ID}
			}
		case KindMoveRef:
			st.moves[e.RawID] = append(st.moves[e.RawID], Move{From: canon(e.From), To: canon(e.To), EventID: e.ID})
		case KindHold:
			holds = append(holds, e)
		}
	}
	st.holds = pendingHolds(holds, st.pins, canon)
	return st
}

// Status returns the status written for the exact person_key — the latest
// status event — defaulting to active. Status is never read through a
// redirect.
func (s State) Status(key string) string {
	if f, ok := s.status[key]; ok {
		return f.Status
	}
	return StatusActive
}

// IsDormant reports whether the exact person_key's folded status is dormant.
func (s State) IsDormant(key string) bool { return s.Status(key) == StatusDormant }

// StatusEvent returns the latest status event folded for the exact
// person_key — its date and reason, for the person view — and ok=false when
// none was ever written.
func (s State) StatusEvent(key string) (StatusFact, bool) {
	f, ok := s.status[key]
	return f, ok
}

// StatusKeys returns every person_key with a folded status event, sorted.
func (s State) StatusKeys() []string {
	keys := make([]string, 0, len(s.status))
	for k := range s.status {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// Claimant returns the form's effective claim, compared by normalized form.
// The claimant may be dormant — see [State.ActiveClaimant].
func (s State) Claimant(form string) (Claim, bool) {
	c, ok := s.claims[NormalizeForm(form)]
	return c, ok
}

// ActiveClaimant returns the form's effective claim when its claimant is
// active. A dormant claimant's claim stays recorded but is inert: resolution
// skips it, and it takes effect again on restore.
func (s State) ActiveClaimant(form string) (Claim, bool) {
	c, ok := s.Claimant(form)
	if !ok || s.IsDormant(c.PersonKey) {
		return Claim{}, false
	}
	return c, true
}

// ClaimsBy returns the canonical person_key's effective claims, sorted by
// normalized form.
func (s State) ClaimsBy(key string) []Claim {
	var out []Claim
	for _, c := range s.claims {
		if c.PersonKey == key {
			out = append(out, c)
		}
	}
	sortClaims(out)
	return out
}

// Claims returns every effective claim, sorted by normalized form.
func (s State) Claims() []Claim {
	out := make([]Claim, 0, len(s.claims))
	for _, c := range s.claims {
		out = append(out, c)
	}
	sortClaims(out)
	return out
}

// Pin returns the pin that settled the raw entry's mention of the form,
// compared by normalized form.
func (s State) Pin(rawID, form string) (Pin, bool) {
	p, ok := s.pins[mentionKey{rawID: rawID, form: NormalizeForm(form)}]
	return p, ok
}

// PinsFor returns every pin on the raw entry, sorted by normalized form.
func (s State) PinsFor(rawID string) []Pin {
	var out []Pin
	for k, p := range s.pins {
		if k.rawID == rawID {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return lessForm(out[i].Form, out[j].Form)
	})
	return out
}

// MovesFor returns the raw entry's move-refs in fold order. Apply them in
// order: each moves the mention off From and onto To.
func (s State) MovesFor(rawID string) []Move {
	return slices.Clone(s.moves[rawID])
}

// TouchedRawIDs returns every raw entry a pin or a move-ref touches, sorted —
// the raw entries whose attribution a read must re-derive from the fold.
func (s State) TouchedRawIDs() []string {
	seen := map[string]bool{}
	for k := range s.pins {
		seen[k.rawID] = true
	}
	for raw := range s.moves {
		seen[raw] = true
	}
	out := make([]string, 0, len(seen))
	for raw := range seen {
		out = append(out, raw)
	}
	slices.Sort(out)
	return out
}

// PendingHolds returns every held mention no pin has settled, sorted by
// (raw_id, normalized form). A move-ref without --from settles a hold through
// its pin (cause move_ref).
func (s State) PendingHolds() []Hold {
	out := make([]Hold, len(s.holds))
	for i, h := range s.holds {
		out[i] = h
		out[i].Candidates = slices.Clone(h.Candidates)
	}
	return out
}

// Duplicates returns every claim event that folded as a duplicate — written
// while a different person claimed the same normalized form — in fold order.
// Such a claim is inert; lucid validate reports it.
func (s State) Duplicates() []Event {
	return slices.Clone(s.duplicates)
}

// foldClaim applies one claim event to the claims: it takes effect only when
// no other person already claims the form, and reports a duplicate — a claim
// written while a different person claimed the form — so the fold can record
// it as inert.
func foldClaim(claims map[string]Claim, e Event, canon func(string) string) (duplicate bool) {
	form := NormalizeForm(e.Form)
	key := canon(e.PersonKey)
	if c, ok := claims[form]; ok {
		return c.PersonKey != key
	}
	claims[form] = Claim{Form: e.Form, PersonKey: key, EventID: e.ID}
	return false
}

// pendingHolds keeps the first hold per (raw_id, normalized form) that no pin
// settles, canonicalizing its candidates, sorted by (raw_id, normalized form).
// Capture never holds a pinned form, so in practice the settling pin is always
// the later event.
func pendingHolds(holds []Event, pins map[mentionKey]Pin, canon func(string) string) []Hold {
	seen := map[mentionKey]bool{}
	var out []Hold
	for _, e := range holds {
		k := mentionKey{rawID: e.RawID, form: NormalizeForm(e.Form)}
		if seen[k] {
			continue
		}
		seen[k] = true
		if _, pinned := pins[k]; pinned {
			continue
		}
		cands := make([]string, 0, len(e.Candidates))
		for _, c := range e.Candidates {
			cands = append(cands, canon(c))
		}
		slices.Sort(cands)
		out = append(out, Hold{RawID: e.RawID, Form: e.Form, Reason: e.Reason, Candidates: slices.Compact(cands), EventID: e.ID})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].RawID != out[j].RawID {
			return out[i].RawID < out[j].RawID
		}
		return lessForm(out[i].Form, out[j].Form)
	})
	return out
}

// sortedEvents returns the events that validate, in fold order: by the
// instant each was recorded, then by id (slot-aware, so `_z` precedes `_aa`).
func sortedEvents(events []Event) []Event {
	type timed struct {
		e  Event
		at time.Time
	}
	valid := make([]timed, 0, len(events))
	for _, e := range events {
		if e.Validate() != nil {
			continue
		}
		at, _ := time.Parse(time.RFC3339, e.At) // Validate parsed it
		valid = append(valid, timed{e: e, at: at})
	}
	sort.SliceStable(valid, func(i, j int) bool {
		if !valid[i].at.Equal(valid[j].at) {
			return valid[i].at.Before(valid[j].at)
		}
		return CompareIDs(valid[i].e.ID, valid[j].e.ID) < 0
	})
	out := make([]Event, len(valid))
	for i, t := range valid {
		out[i] = t.e
	}
	return out
}

// CompareIDs orders identity ids by date, then slot (so `_z` comes before
// `_aa`), then as strings for anything that is not a well-formed identity id.
func CompareIDs(x, y string) int {
	dx, okx := Date(x)
	dy, oky := Date(y)
	if okx && oky {
		if c := strings.Compare(dx, dy); c != 0 {
			return c
		}
		sx, _ := Slot(x)
		sy, _ := Slot(y)
		switch {
		case sx < sy:
			return -1
		case sx > sy:
			return 1
		}
	}
	return strings.Compare(x, y)
}

// localDate is the YYYY-MM-DD date an RFC3339 instant falls on in its own
// offset — the host-local day it was recorded.
func localDate(at string) string {
	t, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return ""
	}
	return t.Format(dateLayout)
}

// sortClaims orders claims by normalized form, then as written.
func sortClaims(cs []Claim) {
	sort.Slice(cs, func(i, j int) bool { return lessForm(cs[i].Form, cs[j].Form) })
}

// lessForm orders forms by their normalized form, then as written.
func lessForm(a, b string) bool {
	na, nb := NormalizeForm(a), NormalizeForm(b)
	if na != nb {
		return na < nb
	}
	return a < b
}
