package storage

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mrz1836/lucid/data"
)

// ErrPersonAkaConflict is returned by [Adapter.AddPersonAka] when the requested
// form already resolves to a *different* live person — planting a tombstone
// there would hijack that person's slug, so the alias is refused (error-states.md
// §P-7). The router maps it to the fixed §P-7 rejection copy.
var ErrPersonAkaConflict = errors.New("storage: alias form resolves to a different person")

// PersonPatch carries the user-authored durable fields [Adapter.SetPerson] may
// write. A nil field is left unchanged; a non-nil pointer to "" clears the
// field; any other value sets it. Identity fields (key, aka, redirect) are
// deliberately absent — SetPerson never touches identity.
type PersonPatch struct {
	Dob          *string
	Relationship *string
	Notes        *string
}

// civilDateLayout is the one accepted shape for a person's dob: a civil date,
// no time and no zone (data-model.md §"People references").
const civilDateLayout = "2006-01-02"

// ValidCivilDate reports whether s parses as a civil YYYY-MM-DD date. It is the
// writer-side guard [Adapter.SetPerson] applies, exported so the router can run
// the same check to produce the §P-9 rejection copy before any write.
func ValidCivilDate(s string) bool {
	_, err := time.Parse(civilDateLayout, s)
	return err == nil
}

// MergePersons folds the source person into the target (error-states.md §P-6;
// data-model.md §"Merge & redirect"). Both keys are canonicalized first, so
// passing any form of either — including an already-merged tombstone slug —
// resolves to the live records; merging a person into themselves is a no-op
// (idempotent, and it returns the canonical record so the caller can still ack).
//
// The target absorbs the source's aka[] (plus the source display_name) and
// entry_refs[], its seen window widens to cover both, and any off-limits mark
// carries across. The source record is rewritten as a redirect tombstone
// forwarding to the target, and every tombstone that already pointed at the
// source is flattened onto the target so the graph stays single-hop. Nothing is
// deleted. It returns the merged target record.
func (a *Adapter) MergePersons(sourceKey, targetKey string) (PersonRecord, error) {
	sourceCanon, err := a.ResolvePersonRedirect(sourceKey)
	if err != nil {
		return PersonRecord{}, err
	}
	targetCanon, err := a.ResolvePersonRedirect(targetKey)
	if err != nil {
		return PersonRecord{}, err
	}
	if sourceCanon == targetCanon {
		rec, _, rerr := a.ReadPerson(targetCanon)
		return rec, rerr
	}

	source, found, err := a.ReadPerson(sourceCanon)
	if err != nil {
		return PersonRecord{}, err
	}
	if !found {
		return PersonRecord{}, fmt.Errorf("storage: merge: no source record %q", sourceCanon)
	}
	target, found, err := a.ReadPerson(targetCanon)
	if err != nil {
		return PersonRecord{}, err
	}
	if !found {
		return PersonRecord{}, fmt.Errorf("storage: merge: no target record %q", targetCanon)
	}

	// Union the source's forms and refs into the target; widen the window.
	for _, aka := range source.Aka {
		target.Aka = addUnique(target.Aka, aka)
	}
	target.Aka = addUnique(target.Aka, source.DisplayName)
	slices.Sort(target.Aka)
	for _, ref := range source.EntryRefs {
		target.EntryRefs = addUnique(target.EntryRefs, ref)
	}
	slices.Sort(target.EntryRefs)
	if !source.FirstSeenAt.IsZero() && (target.FirstSeenAt.IsZero() || source.FirstSeenAt.Before(target.FirstSeenAt)) {
		target.FirstSeenAt = source.FirstSeenAt
	}
	if source.LastSeenAt.After(target.LastSeenAt) {
		target.LastSeenAt = source.LastSeenAt
	}
	if err = a.writePerson(target); err != nil {
		return PersonRecord{}, err
	}

	// Rewrite the source as a tombstone forwarding to the target; every other
	// field stays frozen, including the display_name that witnesses its slug.
	source.RedirectTo = targetCanon
	if err = a.writePerson(source); err != nil {
		return PersonRecord{}, err
	}

	if err = a.flattenRedirects(sourceCanon, targetCanon); err != nil {
		return PersonRecord{}, err
	}
	if err = a.carryOffLimits(sourceCanon, targetCanon); err != nil {
		return PersonRecord{}, err
	}
	return target, nil
}

// flattenRedirects re-points every tombstone forwarding to oldTarget so it
// forwards to newTarget instead, keeping the redirect graph single-hop after a
// merge would otherwise chain two canonical records together.
func (a *Adapter) flattenRedirects(oldTarget, newTarget string) error {
	keys, err := a.ListPeopleKeys()
	if err != nil {
		return err
	}
	for _, key := range keys {
		rec, found, rerr := a.ReadPerson(key)
		if rerr != nil {
			return rerr
		}
		if !found || !rec.IsTombstone() || rec.RedirectTo != oldTarget {
			continue
		}
		rec.RedirectTo = newTarget
		if werr := a.writePerson(rec); werr != nil {
			return werr
		}
	}
	return nil
}

// carryOffLimits inherits the source's off-limits status onto the merge target:
// if the source was redacted, the target becomes redacted and the source's
// now-tombstoned key is dropped from the registry so it names only live
// canonical records. A no-op when the source was not off-limits.
func (a *Adapter) carryOffLimits(sourceCanon, targetCanon string) error {
	keys, err := a.ReadOffLimitsPersonKeys()
	if err != nil {
		return err
	}
	if !slices.Contains(keys, sourceCanon) {
		return nil
	}
	set := make(map[string]bool, len(keys))
	for _, k := range keys {
		set[k] = true
	}
	delete(set, sourceCanon)
	set[targetCanon] = true
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	slices.Sort(out)
	return a.WriteOffLimitsPersonKeys(out)
}

// AddPersonAka records another written form on the canonical person at key.
// When a future bare mention of that form would land on a slug that is free or
// already this person's, it plants a redirect tombstone there so the mention
// resolves forward here (data-model.md §"Merge & redirect"). If the form already
// resolves to a *different* live record it returns [ErrPersonAkaConflict] — an
// alias never hijacks another person's slug (error-states.md §P-7). Idempotent:
// re-aliasing an already-recorded form leaves the store byte-identical.
func (a *Adapter) AddPersonAka(key, form string) (PersonRecord, error) {
	form = strings.TrimSpace(form)
	if form == "" {
		return PersonRecord{}, errors.New("storage: add_aka: empty form")
	}

	target, found, err := a.ReadPerson(key)
	if err != nil {
		return PersonRecord{}, err
	}
	if !found || target.IsTombstone() {
		return PersonRecord{}, fmt.Errorf("storage: add_aka: no canonical record at %q", key)
	}

	if err = a.plantAliasResolver(key, form, target); err != nil {
		return PersonRecord{}, err
	}

	target.Aka = addUnique(target.Aka, form)
	slices.Sort(target.Aka)
	if err = a.writePerson(target); err != nil {
		return PersonRecord{}, err
	}
	return target, nil
}

// RenamePerson changes the canonical person's display_name, folding the old form
// into aka[] (error-states.md §P-8; data-model.md §"Merge & redirect"). The
// person_key does **not** change — it is a stable id and a mutable address, the
// same model as anchor rename / self move. A redirect tombstone is planted at the
// new name's slug so a future bare mention of it folds in, exactly as an alias
// does; renaming onto a name that already resolves to a *different* live record
// returns [ErrPersonAkaConflict] (a rename never creates a duplicate identity).
// It returns the renamed record.
func (a *Adapter) RenamePerson(key, newName string) (PersonRecord, error) {
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return PersonRecord{}, errors.New("storage: rename: empty name")
	}

	rec, found, err := a.ReadPerson(key)
	if err != nil {
		return PersonRecord{}, err
	}
	if !found || rec.IsTombstone() {
		return PersonRecord{}, fmt.Errorf("storage: rename: no canonical record at %q", key)
	}

	if err = a.plantAliasResolver(key, newName, rec); err != nil {
		return PersonRecord{}, err
	}

	rec.Aka = addUnique(rec.Aka, rec.DisplayName) // the old form is kept
	rec.Aka = addUnique(rec.Aka, newName)
	slices.Sort(rec.Aka)
	rec.DisplayName = newName
	if err = a.writePerson(rec); err != nil {
		return PersonRecord{}, err
	}
	return rec, nil
}

// plantAliasResolver ensures a future bare mention of form resolves to the
// canonical record at key: it plants a redirect tombstone at the form's landing
// slug when that slug is free, is a no-op when the form already resolves to key,
// and returns [ErrPersonAkaConflict] when the form resolves to a *different* live
// record. It is the shared core of AddPersonAka and RenamePerson.
func (a *Adapter) plantAliasResolver(key, form string, canonical PersonRecord) error {
	landing, err := ResolvePersonKey(form, data.Wordlist(), a.personKeyOwner)
	if err != nil {
		return err
	}
	canon, err := a.ResolvePersonRedirect(landing)
	if err != nil {
		return err
	}
	if canon == key {
		return nil // a bare mention already lands here — no tombstone needed
	}
	other, otherFound, err := a.ReadPerson(canon)
	if err != nil {
		return err
	}
	if otherFound && !other.IsTombstone() {
		return ErrPersonAkaConflict
	}
	return a.writePerson(newAliasTombstone(landing, form, key, canonical))
}

// newAliasTombstone builds the tombstone an alias plants at a free slug: its
// display_name is the aliased form (the collision-oracle witness for that slug),
// it forwards to the canonical key, and it inherits the canonical's seen window
// so it carries an honest lifecycle rather than the zero time. It holds no
// entry_refs of its own — no mention ever landed on it.
func newAliasTombstone(landing, form, target string, canonical PersonRecord) PersonRecord {
	return PersonRecord{
		PersonKey:   landing,
		DisplayName: form,
		Aka:         []string{form},
		FirstSeenAt: canonical.FirstSeenAt,
		LastSeenAt:  canonical.LastSeenAt,
		EntryRefs:   []string{},
		RedirectTo:  target,
	}
}

// SetPerson writes the user-authored durable fields (dob, relationship, notes)
// onto the canonical person at key. It never touches identity — no key, aka, or
// redirect changes — and it is the one people write that records fields the user
// typed rather than the People routine extracted (data-model.md §"People
// references"). A nil patch field is left unchanged; an empty value clears it; a
// dob must be a civil YYYY-MM-DD. It returns the updated record.
func (a *Adapter) SetPerson(key string, patch PersonPatch) (PersonRecord, error) {
	if patch.Dob != nil && *patch.Dob != "" && !ValidCivilDate(*patch.Dob) {
		return PersonRecord{}, fmt.Errorf("storage: set_person: dob %q is not %s", *patch.Dob, civilDateLayout)
	}

	rec, found, err := a.ReadPerson(key)
	if err != nil {
		return PersonRecord{}, err
	}
	if !found || rec.IsTombstone() {
		return PersonRecord{}, fmt.Errorf("storage: set_person: no canonical record at %q", key)
	}

	applyPatchField(&rec.Dob, patch.Dob)
	applyPatchField(&rec.Relationship, patch.Relationship)
	applyPatchField(&rec.Notes, patch.Notes)
	if err = a.writePerson(rec); err != nil {
		return PersonRecord{}, err
	}
	return rec, nil
}

// applyPatchField applies one optional patch field: nil leaves the current
// value, "" clears it to nil (so the field is omitted on disk), any other value
// sets it.
func applyPatchField(cur **string, in *string) {
	if in == nil {
		return
	}
	if *in == "" {
		*cur = nil
		return
	}
	v := *in
	*cur = &v
}
