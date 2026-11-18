package storage

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mrz1836/lucid/data"
	"github.com/mrz1836/lucid/internal/identity"
)

// The identity curation writes (data-model.md §"Status, claims and move-ref";
// ADR-0016). Each one decides everything from the people tree and the folded
// identity log before it writes, then appends its events — its own event plus
// the pins it settles — as one append to identity/identity.jsonl. None of them
// ever writes people/, contacts/, or processed/.

// ErrIdentitySubjectNotCanonical is an identity write aimed at a key that is
// not a live canonical person record. The router resolves every subject to a
// canonical record first, so this only guards a direct storage caller.
var ErrIdentitySubjectNotCanonical = errors.New("storage: identity write needs a live canonical person")

// ClaimConflictError is a claim refused because different active people hold
// the form (error-states.md §P-10): its slug-path holder, an exact
// display_name/aka holder, or its current claimant. Holders are their
// person_keys, sorted. Nothing was written.
type ClaimConflictError struct {
	Form    string
	Holders []string
}

// Error names the form and the active holders.
func (e *ClaimConflictError) Error() string {
	return fmt.Sprintf("storage: the form %q is held by other active people: %s", e.Form, strings.Join(e.Holders, ", "))
}

// PinnedMention is one held mention a curation write settled: the raw entry,
// the form as the hold recorded it, and the person it is now pinned to.
type PinnedMention struct {
	RawID     string
	Form      string
	PersonKey string
}

// IdentityWrite reports what one identity curation write appended. The zero
// value is a no-op: nothing was appended and there is no receipt.
type IdentityWrite struct {
	// Receipt is the id of the verb's own event — the write's receipt. It is
	// empty on a no-op.
	Receipt string
	// Events are every event appended, in append order: the verb's own
	// events, then the pins.
	Events []identity.Event
	// Pinned are the held mentions the write settled, sorted by raw entry and
	// form.
	Pinned []PinnedMention
	// StillHeld counts the pending held mentions of the affected forms the
	// write left held.
	StillHeld int
}

// NoOp reports a write that appended nothing.
func (w IdentityWrite) NoOp() bool { return len(w.Events) == 0 }

// ClaimWrite reports what a claim appended, beyond the shared receipt.
type ClaimWrite struct {
	IdentityWrite

	// Displaced are the active holders a forced claim overrode, sorted.
	Displaced []string
	// ReleasedDormant is the dormant claimant whose inert claim was released
	// first, if any.
	ReleasedDormant string
}

// MergeIdentity reports what a merge appended to keep the identity log valid.
// Both lists hold forms as the source's claims wrote them, sorted by
// normalized form.
type MergeIdentity struct {
	// Moved are the source's claims re-pointed to the target.
	Moved []string
	// Released are the source's claims released because another active person
	// holds the form exactly.
	Released []string
	// Events are every event appended.
	Events []identity.Event
}

// SetPersonStatus marks the canonical person at key dormant or active again
// (`lucid person dormant [--restore]`). It appends one status event — with the
// optional one-line reason — then pins every pending held mention of the
// person's forms that now resolves to exactly one person (cause dormant or
// restore). Writing the status the person already has is a no-op: nothing is
// appended. people/, contacts/, and processed/ are never written.
func (a *Adapter) SetPersonStatus(key, status, reason string, at time.Time) (IdentityWrite, error) {
	var cause string
	switch status {
	case identity.StatusDormant:
		cause = identity.CauseDormant
	case identity.StatusActive:
		cause = identity.CauseRestore
		if reason != "" {
			return IdentityWrite{}, errors.New("storage: a status reason applies only to dormant")
		}
	default:
		return IdentityWrite{}, fmt.Errorf("storage: unknown person status %q", status)
	}
	ix, events, st, err := a.identitySnapshot()
	if err != nil {
		return IdentityWrite{}, err
	}
	rec, err := canonicalSubject(ix, key)
	if err != nil {
		return IdentityWrite{}, err
	}
	if st.Status(key) == status {
		return IdentityWrite{}, nil
	}
	forms := statusForms(st, rec)
	ev := identity.NewStatus(formatAt(at), identity.SourceCLI, key, status, reason)
	return a.appendWithPins(ix, events, []identity.Event{ev}, forms, cause, at)
}

// ClaimForm makes form resolve to the canonical person at key for every
// mention resolved after it (`lucid person claim`). It is refused with a
// [ClaimConflictError], before any write, when a different active person
// holds the form — its slug-path holder, an exact display_name/aka holder, or
// its active claimant — unless force is set; a dormant holder, a dormant
// claimant, and someone who only shares the first name never conflict. A
// standing claimant other than key is released first (a forced override, or a
// dormant claimant's inert claim). It then pins every pending held mention of
// the form that now resolves to exactly one person (cause claim). Claiming a
// form the person already claims is a no-op. people/ is never written.
func (a *Adapter) ClaimForm(key, form string, force bool, at time.Time) (ClaimWrite, error) {
	if identity.NormalizeForm(form) == "" {
		return ClaimWrite{}, identity.ErrBlankForm
	}
	ix, events, st, err := a.identitySnapshot()
	if err != nil {
		return ClaimWrite{}, err
	}
	if _, err = canonicalSubject(ix, key); err != nil {
		return ClaimWrite{}, err
	}
	standing, hasClaimant := st.Claimant(form)
	if hasClaimant && standing.PersonKey == key {
		return ClaimWrite{}, nil
	}
	holders := activeFormHolders(ix, st, form, key)
	if len(holders) > 0 && !force {
		return ClaimWrite{}, &ClaimConflictError{Form: form, Holders: holders}
	}

	stamp := formatAt(at)
	var evs []identity.Event
	var releasedDormant string
	if hasClaimant {
		// One claimant per form: the standing claim ends before the new one
		// starts, so the CLI never writes an inert duplicate.
		evs = append(evs, identity.NewRelease(stamp, identity.SourceCLI, standing.Form, standing.PersonKey))
		if _, live := ix.canonical(standing.PersonKey); live && st.IsDormant(standing.PersonKey) {
			releasedDormant = standing.PersonKey
		}
	}
	evs = append(evs, identity.NewClaim(stamp, identity.SourceCLI, form, key, len(holders) > 0, holders))
	w, err := a.appendWithPins(ix, events, evs, formSet(form), identity.CauseClaim, at)
	if err != nil {
		return ClaimWrite{}, err
	}
	return ClaimWrite{IdentityWrite: w, Displaced: holders, ReleasedDormant: releasedDormant}, nil
}

// ReleaseForm ends the canonical person at key's claim on form (`lucid person
// release`) for mentions resolved after it, then pins every pending held
// mention of the form that now resolves to exactly one person (cause release).
// Releasing a form the person does not claim is a no-op. people/ is never
// written.
func (a *Adapter) ReleaseForm(key, form string, at time.Time) (IdentityWrite, error) {
	if identity.NormalizeForm(form) == "" {
		return IdentityWrite{}, identity.ErrBlankForm
	}
	ix, events, st, err := a.identitySnapshot()
	if err != nil {
		return IdentityWrite{}, err
	}
	if _, err = canonicalSubject(ix, key); err != nil {
		return IdentityWrite{}, err
	}
	if c, ok := st.Claimant(form); !ok || c.PersonKey != key {
		return IdentityWrite{}, nil
	}
	ev := identity.NewRelease(formatAt(at), identity.SourceCLI, form, key)
	return a.appendWithPins(ix, events, []identity.Event{ev}, formSet(form), identity.CauseRelease, at)
}

// PinPendingHolds re-resolves every pending held mention of the given forms
// under capture's rules and pins each one that now resolves to exactly one
// existing person, for cause. It never mints: a held mention that would need a
// new record stays held, and so does the aka-only capture hold. It appends
// only pin events — none when nothing settles — so the write has no receipt.
func (a *Adapter) PinPendingHolds(forms []string, cause string, at time.Time) (IdentityWrite, error) {
	ix, _, st, err := a.identitySnapshot()
	if err != nil {
		return IdentityWrite{}, err
	}
	set := map[string]bool{}
	for _, f := range forms {
		if n := identity.NormalizeForm(f); n != "" {
			set[n] = true
		}
	}
	pins, pinned, still, err := a.planPins(ix, st, set, cause, at)
	if err != nil {
		return IdentityWrite{}, err
	}
	w := IdentityWrite{Pinned: pinned, StillHeld: still}
	if len(pins) == 0 {
		return w, nil
	}
	if w.Events, err = a.AppendIdentityEvents(pins); err != nil {
		return IdentityWrite{}, err
	}
	return w, nil
}

// RepointIdentityOnMerge keeps the identity log valid after `person merge`
// folded source into target (data-model.md §"Status, claims and move-ref").
// The target keeps its status. A dormant source gets a clearing active status
// event, so no status sits on a tombstone. Each claim the source held is
// re-pointed to the target (a release plus a claim), or — when another active
// person holds the form exactly — only released. Every event carries source
// merge. A merge with no identity facts appends nothing and never creates the
// log; pins and move-refs need nothing, since they canonicalize through the
// new redirect on read.
func (a *Adapter) RepointIdentityOnMerge(source, target string, at time.Time) (MergeIdentity, error) {
	events, err := a.ReadIdentityEvents()
	if err != nil {
		return MergeIdentity{}, err
	}
	if len(events) == 0 {
		return MergeIdentity{}, nil
	}
	ix, err := a.loadPeopleIndex()
	if err != nil {
		return MergeIdentity{}, err
	}
	st := identity.Fold(events, ix.canonKey)
	// The source's own claims: every claim folded with the redirect graph
	// except the source's new tombstone, so its claims still read as its own.
	view := identity.Fold(events, func(k string) string {
		if k == source {
			return source
		}
		return ix.canonKey(k)
	})

	stamp := formatAt(at)
	var out MergeIdentity
	var evs []identity.Event
	if st.IsDormant(source) {
		evs = append(evs, identity.NewStatus(stamp, identity.SourceMerge, source, identity.StatusActive, ""))
	}
	for _, c := range view.ClaimsBy(source) {
		evs = append(evs, identity.NewRelease(stamp, identity.SourceMerge, c.Form, source))
		if otherActiveExactHolder(ix, st, c.Form, source, target) {
			out.Released = append(out.Released, c.Form)
			continue
		}
		evs = append(evs, identity.NewClaim(stamp, identity.SourceMerge, c.Form, target, false, nil))
		out.Moved = append(out.Moved, c.Form)
	}
	if len(evs) == 0 {
		return out, nil
	}
	if out.Events, err = a.AppendIdentityEvents(evs); err != nil {
		return MergeIdentity{}, err
	}
	return out, nil
}

// identitySnapshot reads the people tree and the identity log once and folds
// them, so one write decides everything from a single consistent view.
func (a *Adapter) identitySnapshot() (peopleIndex, []identity.Event, identity.State, error) {
	ix, err := a.loadPeopleIndex()
	if err != nil {
		return peopleIndex{}, nil, identity.State{}, err
	}
	events, err := a.ReadIdentityEvents()
	if err != nil {
		return peopleIndex{}, nil, identity.State{}, err
	}
	return ix, events, identity.Fold(events, ix.canonKey), nil
}

// appendWithPins appends a verb's own events, then the pins they settle, as
// one append. The pins are decided against the state the verb's events
// produce — folded in memory, with the ids the append will assign — so the
// whole write is decided before anything is written. The receipt is the last
// of the verb's own events (a claim that first releases a standing claimant
// is receipted by its claim).
func (a *Adapter) appendWithPins(ix peopleIndex, prior, verb []identity.Event, forms map[string]bool, cause string, at time.Time) (IdentityWrite, error) {
	stamped, err := a.stampIdentityEvents(verb)
	if err != nil {
		return IdentityWrite{}, err
	}
	all := append(slices.Clone(prior), stamped...)
	st := identity.Fold(all, ix.canonKey)
	pins, pinned, still, err := a.planPins(ix, st, forms, cause, at)
	if err != nil {
		return IdentityWrite{}, err
	}
	out, err := a.AppendIdentityEvents(append(slices.Clone(verb), pins...))
	if err != nil {
		return IdentityWrite{}, err
	}
	return IdentityWrite{
		Receipt:   out[len(verb)-1].ID,
		Events:    out,
		Pinned:    pinned,
		StillHeld: still,
	}, nil
}

// planPins re-resolves every pending held mention whose normalized form is in
// forms under capture's rules — with no prior, since the prior is the hold
// itself — and builds a pin (cause) for each one that resolves to exactly one
// existing person. A held mention that would mint, stays ambiguous, or is the
// aka-only capture hold stays held and is counted. Pending holds are already
// sorted by raw entry and form, so the result is deterministic.
func (a *Adapter) planPins(ix peopleIndex, st identity.State, forms map[string]bool, cause string, at time.Time) ([]identity.Event, []PinnedMention, int, error) {
	stamp := formatAt(at)
	var pins []identity.Event
	var pinned []PinnedMention
	still := 0
	for _, h := range st.PendingHolds() {
		if !forms[identity.NormalizeForm(h.Form)] {
			continue
		}
		res, err := a.resolveCapture(ix, st, h.Form, ResolveOptions{Mode: ResolveCapture, RawID: h.RawID})
		if err != nil {
			return nil, nil, 0, err
		}
		if res.Outcome != NameResolved {
			still++
			continue
		}
		pins = append(pins, identity.NewPin(stamp, identity.SourceCLI, h.RawID, h.Form, res.PersonKey, cause))
		pinned = append(pinned, PinnedMention{RawID: h.RawID, Form: h.Form, PersonKey: res.PersonKey})
	}
	return pins, pinned, still, nil
}

// statusForms returns the normalized forms a dormant or restore write
// affects: the person's display_name and aka forms, their first name, and the
// form of every pending hold that lists them as a candidate.
func statusForms(st identity.State, rec PersonRecord) map[string]bool {
	set := map[string]bool{}
	add := func(f string) {
		if f != "" {
			set[f] = true
		}
	}
	add(identity.NormalizeForm(rec.DisplayName))
	for _, aka := range rec.Aka {
		add(identity.NormalizeForm(aka))
	}
	add(data.NicknameForm(rec.DisplayName))
	for _, h := range st.PendingHolds() {
		if slices.Contains(h.Candidates, rec.PersonKey) {
			add(identity.NormalizeForm(h.Form))
		}
	}
	return set
}

// activeFormHolders returns the active people other than self who hold form —
// its slug-path holders, its exact display_name/aka holders, and its live
// claimant — sorted and de-duplicated. These are the people a claim conflicts
// with (§P-10).
func activeFormHolders(ix peopleIndex, st identity.State, form, self string) []string {
	keys := append(ix.slugPath(form), ix.formMatch(form)...)
	if c, ok := st.Claimant(form); ok {
		if canon, live := ix.canonical(c.PersonKey); live {
			keys = append(keys, canon.PersonKey)
		}
	}
	slices.Sort(keys)
	keys = slices.Compact(keys)
	out := keys[:0]
	for _, k := range keys {
		if k != self && !st.IsDormant(k) {
			out = append(out, k)
		}
	}
	return out
}

// otherActiveExactHolder reports whether an active person other than source
// and target holds form exactly (display_name or aka) — the case where a
// merge releases the source's claim instead of moving it.
func otherActiveExactHolder(ix peopleIndex, st identity.State, form, source, target string) bool {
	for _, k := range ix.formMatch(form) {
		if k != source && k != target && !st.IsDormant(k) {
			return true
		}
	}
	return false
}

// canonicalSubject returns the live canonical record at key, refusing a key
// that is absent, a tombstone, or dangling.
func canonicalSubject(ix peopleIndex, key string) (PersonRecord, error) {
	rec, ok := ix.recs[key]
	if !ok || rec.IsTombstone() {
		return PersonRecord{}, fmt.Errorf("%w: %q", ErrIdentitySubjectNotCanonical, key)
	}
	return rec, nil
}

// formSet is the one-form affected set a claim or release re-resolves.
func formSet(form string) map[string]bool {
	return map[string]bool{identity.NormalizeForm(form): true}
}

// formatAt renders an identity event's `at`: RFC3339 with the instant's own
// offset, the wall clock when at is zero.
func formatAt(at time.Time) string {
	if at.IsZero() {
		at = time.Now()
	}
	return at.Format(time.RFC3339)
}
