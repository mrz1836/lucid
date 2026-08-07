package router

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mrz1836/lucid/internal/storage"
)

// ErrPersonRejected marks a deterministic input rejection of a person curation
// write — a blank subject or value, a malformed date of birth (error-states.md
// §P-9). It wraps the fixed, model-free reason so the CLI can print the copy on
// stderr and map it to a non-zero exit; the rejection happens before any disk
// write, so a bad input never mutates the Ledger. It mirrors [ErrSelfRejected].
var ErrPersonRejected = errors.New("person write rejected")

// The state rejections the curation verbs raise, distinct from the input one:
// these are about which person a subject names and whether the requested change
// is coherent given the store (error-states.md §P-4..§P-8).
var (
	// ErrPersonUnknownSubject — no live record matches the subject (§P-4).
	ErrPersonUnknownSubject = errors.New("person subject not recorded")
	// ErrPersonAmbiguousSubject — the subject matches more than one live
	// record; the verb refuses rather than guess (§P-5).
	ErrPersonAmbiguousSubject = errors.New("person subject ambiguous")
	// ErrPersonMergeSelf — merge whose source and target are the same person
	// (§P-6).
	ErrPersonMergeSelf = errors.New("person merge into self")
	// ErrPersonAliasTaken — an alias form already resolves to a different live
	// person (§P-7).
	ErrPersonAliasTaken = errors.New("person alias form taken")
	// ErrPersonNameTaken — a rename onto a name that already identifies a
	// different live person (§P-8).
	ErrPersonNameTaken = errors.New("person name taken")
)

// PersonRejectedError is a deterministic person-curation rejection: a fixed,
// model-free Reason that is already a complete user-facing sentence, and a Cause
// that classifies it so a caller matches the class with errors.Is while the CLI
// prints Reason verbatim. It mirrors [SelfRejectedError].
type PersonRejectedError struct {
	Reason string
	Cause  error
}

// Error returns the fixed reason, a complete user-facing sentence.
func (e *PersonRejectedError) Error() string { return e.Reason }

// Unwrap exposes the classifying sentinel to errors.Is.
func (e *PersonRejectedError) Unwrap() error { return e.Cause }

// rejectPerson builds a [PersonRejectedError] from a sentinel and a formatted
// reason. Every reason ends with the nothing-was-saved clause.
func rejectPerson(cause error, format string, args ...any) error {
	return &PersonRejectedError{Reason: fmt.Sprintf(format, args...), Cause: cause}
}

// resolvePersonSubject resolves a raw subject string to one live canonical
// person record, the way `/person` matches: an exact person_key wins (a
// tombstone key resolves forward to its canonical), otherwise the string is
// matched against display_name / aka[] with tombstones skipped. No live match is
// §P-4; more than one is §P-5 — the verb never guesses which person was meant.
func (r *Router) resolvePersonSubject(raw string) (storage.PersonRecord, error) {
	subject := strings.TrimSpace(raw)
	if subject == "" {
		return storage.PersonRecord{}, rejectPerson(ErrPersonRejected, "a person name or key is required; nothing was saved.")
	}

	if rec, ok, err := r.exactKeySubject(subject); err != nil {
		return storage.PersonRecord{}, err
	} else if ok {
		return rec, nil
	}

	matches, err := r.matchPeople(subject)
	if err != nil {
		return storage.PersonRecord{}, err
	}
	switch len(matches) {
	case 0:
		return storage.PersonRecord{}, rejectPerson(ErrPersonUnknownSubject,
			"no one matches %q — check the name or run `lucid person <name>`; nothing was saved.", subject)
	case 1:
		return matches[0], nil
	default:
		return storage.PersonRecord{}, rejectPerson(ErrPersonAmbiguousSubject,
			"%q matches more than one person — disambiguate with a person_key (%s); nothing was saved.",
			subject, candidateKeys(matches))
	}
}

// exactKeySubject resolves a subject that is an exact person_key, following a
// tombstone forward to its canonical record. It reports ok=false (never an
// error) when the string is not a usable key — a bad slug, an absent record, or
// a dangling tombstone — so the caller falls through to a name match.
func (r *Router) exactKeySubject(subject string) (storage.PersonRecord, bool, error) {
	rec, found, err := r.store.ReadPerson(subject)
	if err != nil || !found {
		// A subject that is not a usable key (a bad slug, an absent or unreadable
		// record) simply is not an exact-key hit — fall through to the name match,
		// which re-surfaces a genuine read error while scanning the tree.
		return storage.PersonRecord{}, false, nil //nolint:nilerr // intentional: a non-key subject falls through to matchPeople
	}
	canonKey, err := r.store.ResolvePersonRedirect(subject)
	if err != nil {
		return storage.PersonRecord{}, false, err
	}
	if canonKey == subject {
		if rec.IsTombstone() {
			return storage.PersonRecord{}, false, nil
		}
		return rec, true, nil
	}
	canon, cfound, err := r.store.ReadPerson(canonKey)
	if err != nil {
		return storage.PersonRecord{}, false, err
	}
	if !cfound {
		return storage.PersonRecord{}, false, nil
	}
	return canon, true, nil
}

// candidateKeys renders the sorted person_keys of the ambiguous matches for the
// §P-5 disambiguation copy.
func candidateKeys(matches []storage.PersonRecord) string {
	keys := make([]string, 0, len(matches))
	for _, m := range matches {
		keys = append(keys, m.PersonKey)
	}
	return strings.Join(keys, " / ")
}

// PersonMergeRequest is one `lucid person merge` intent: fold Source into
// Target. Both are raw subjects resolved in the router.
type PersonMergeRequest struct {
	Source string
	Target string
}

// PersonMergeResult reports the merged canonical record and the inventory-only
// ack.
type PersonMergeResult struct {
	Record storage.PersonRecord
	Ack    string
}

// PersonMerge executes `lucid person merge <source> <target>`: collapse a
// duplicate person onto a canonical one (data-model.md §"Merge & redirect"). The
// source becomes a redirect tombstone; nothing is deleted. Merging a person into
// themselves is rejected (§P-6). Deterministic and model-free.
func (r *Router) PersonMerge(req PersonMergeRequest) (PersonMergeResult, error) {
	source, err := r.resolvePersonSubject(req.Source)
	if err != nil {
		return PersonMergeResult{}, err
	}
	target, err := r.resolvePersonSubject(req.Target)
	if err != nil {
		return PersonMergeResult{}, err
	}
	if source.PersonKey == target.PersonKey {
		return PersonMergeResult{}, rejectPerson(ErrPersonMergeSelf,
			"%q is already that person; nothing was saved.", target.DisplayName)
	}
	merged, err := r.store.MergePersons(source.PersonKey, target.PersonKey)
	if err != nil {
		return PersonMergeResult{}, fmt.Errorf("person: merge: %w", err)
	}
	return PersonMergeResult{Record: merged, Ack: personMergeAck(source, merged)}, nil
}

// personMergeAck builds the inventory ack for a merge: what folded into what, and
// the resulting mention count — no evaluative language (engine §0).
func personMergeAck(source, merged storage.PersonRecord) string {
	return fmt.Sprintf("Merged %q into %q — %d entr%s now resolve to one record.",
		source.DisplayName, merged.DisplayName, len(merged.EntryRefs), plural(len(merged.EntryRefs)))
}

// PersonAliasRequest is one `lucid person alias` intent: record Form as another
// written form of Subject.
type PersonAliasRequest struct {
	Subject string
	Form    string
}

// PersonAliasResult reports the updated record and the inventory-only ack.
type PersonAliasResult struct {
	Record storage.PersonRecord
	Ack    string
}

// PersonAlias executes `lucid person alias <subject> <form>`: record another
// written form and plant a redirect tombstone so a future bare mention of it
// resolves to this person. A form that already belongs to a different live
// record is rejected (§P-7). Idempotent.
func (r *Router) PersonAlias(req PersonAliasRequest) (PersonAliasResult, error) {
	form := strings.TrimSpace(req.Form)
	if form == "" {
		return PersonAliasResult{}, rejectPerson(ErrPersonRejected, "a form to record is required; nothing was saved.")
	}
	subject, err := r.resolvePersonSubject(req.Subject)
	if err != nil {
		return PersonAliasResult{}, err
	}
	rec, err := r.store.AddPersonAka(subject.PersonKey, form)
	if err != nil {
		if errors.Is(err, storage.ErrPersonAkaConflict) {
			return PersonAliasResult{}, rejectPerson(ErrPersonAliasTaken,
				"the form %q already belongs to another person — use `lucid person merge` if they are the same; nothing was saved.", form)
		}
		return PersonAliasResult{}, fmt.Errorf("person: alias: %w", err)
	}
	return PersonAliasResult{Record: rec, Ack: fmt.Sprintf("Recorded %q as another form of %q.", form, rec.DisplayName)}, nil
}

// PersonRenameRequest is one `lucid person rename` intent: change Subject's
// display name to NewName.
type PersonRenameRequest struct {
	Subject string
	NewName string
}

// PersonRenameResult reports the renamed record and the inventory-only ack.
type PersonRenameResult struct {
	Record storage.PersonRecord
	Ack    string
}

// PersonRename executes `lucid person rename <subject> <new-name...>`: change the
// display name, folding the old form into aka[]. The person_key does not change
// (stable id, mutable address — the anchor rename / self move model). Renaming
// onto a name that already identifies a different live record is rejected (§P-8).
func (r *Router) PersonRename(req PersonRenameRequest) (PersonRenameResult, error) {
	newName := strings.TrimSpace(req.NewName)
	if newName == "" {
		return PersonRenameResult{}, rejectPerson(ErrPersonRejected, "a new name is required; nothing was saved.")
	}
	subject, err := r.resolvePersonSubject(req.Subject)
	if err != nil {
		return PersonRenameResult{}, err
	}
	oldName := subject.DisplayName
	renamed, err := r.store.RenamePerson(subject.PersonKey, newName)
	if err != nil {
		if errors.Is(err, storage.ErrPersonAkaConflict) {
			return PersonRenameResult{}, rejectPerson(ErrPersonNameTaken,
				"%q already identifies another person — use `lucid person merge` if they are the same; nothing was saved.", newName)
		}
		return PersonRenameResult{}, fmt.Errorf("person: rename: %w", err)
	}
	return PersonRenameResult{Record: renamed, Ack: fmt.Sprintf("Renamed %q to %q — the person_key is unchanged, and the old form is kept.", oldName, renamed.DisplayName)}, nil
}

// PersonSetRequest is one `lucid person set` intent: record the user-authored
// durable fields. A nil field was not given (left unchanged); a non-nil pointer
// to "" clears the field.
type PersonSetRequest struct {
	Subject      string
	Dob          *string
	Relationship *string
	Note         *string
}

// PersonSetResult reports the updated record and the inventory-only ack.
type PersonSetResult struct {
	Record storage.PersonRecord
	Ack    string
}

// PersonSet executes `lucid person set <subject> [--dob --relationship --note]`:
// record the user-authored durable fields. It never touches identity. A dob that
// is not a civil YYYY-MM-DD is rejected (§P-9); a set with no fields is rejected
// as well, so the verb never silently no-ops.
func (r *Router) PersonSet(req PersonSetRequest) (PersonSetResult, error) {
	if req.Dob == nil && req.Relationship == nil && req.Note == nil {
		return PersonSetResult{}, rejectPerson(ErrPersonRejected,
			"nothing to set — provide --dob, --relationship, or --note; nothing was saved.")
	}
	if req.Dob != nil && *req.Dob != "" && !storage.ValidCivilDate(*req.Dob) {
		return PersonSetResult{}, rejectPerson(ErrPersonRejected,
			"could not read the date of birth %q (want YYYY-MM-DD); nothing was saved.", *req.Dob)
	}
	subject, err := r.resolvePersonSubject(req.Subject)
	if err != nil {
		return PersonSetResult{}, err
	}
	rec, err := r.store.SetPerson(subject.PersonKey, storage.PersonPatch{
		Dob:          req.Dob,
		Relationship: req.Relationship,
		Notes:        req.Note,
	})
	if err != nil {
		return PersonSetResult{}, fmt.Errorf("person: set: %w", err)
	}
	return PersonSetResult{Record: rec, Ack: personSetAck(rec, req)}, nil
}

// personSetAck builds the inventory ack for a set: the fields that were written,
// in a fixed order, nothing evaluative.
func personSetAck(rec storage.PersonRecord, req PersonSetRequest) string {
	parts := make([]string, 0, 3)
	if req.Dob != nil {
		parts = append(parts, "dob "+valueOrCleared(*req.Dob))
	}
	if req.Relationship != nil {
		parts = append(parts, "relationship "+valueOrCleared(*req.Relationship))
	}
	if req.Note != nil {
		parts = append(parts, "note "+valueOrCleared(*req.Note))
	}
	return fmt.Sprintf("Recorded on %q: %s.", rec.DisplayName, strings.Join(parts, ", "))
}

// valueOrCleared renders a set field value, naming an emptied field as cleared.
func valueOrCleared(v string) string {
	if strings.TrimSpace(v) == "" {
		return "cleared"
	}
	return v
}

// PersonOffLimitsRequest is one `lucid person off-limits` intent: toggle P-3
// redaction for Subject. Restore clears it.
type PersonOffLimitsRequest struct {
	Subject string
	Restore bool
}

// PersonOffLimitsResult reports the affected record, its resulting off-limits
// state, and the inventory-only ack.
type PersonOffLimitsResult struct {
	Record    storage.PersonRecord
	OffLimits bool
	Ack       string
}

// PersonOffLimits executes `lucid person off-limits <subject> [--restore]`: the
// CLI writer for the P-3 redaction registry. Nothing is deleted; the record is
// kept and only its inference visibility changes. Idempotent — marking someone
// already off-limits (or restoring someone who is not) is a no-op ack.
func (r *Router) PersonOffLimits(req PersonOffLimitsRequest) (PersonOffLimitsResult, error) {
	subject, err := r.resolvePersonSubject(req.Subject)
	if err != nil {
		return PersonOffLimitsResult{}, err
	}
	keys, err := r.store.ReadOffLimitsPersonKeys()
	if err != nil {
		return PersonOffLimitsResult{}, fmt.Errorf("person: read off-limits: %w", err)
	}
	// Build the set directly (not via toSet, which returns nil for an empty
	// registry — the common first-toggle case).
	set := make(map[string]bool, len(keys)+1)
	for _, k := range keys {
		set[k] = true
	}
	_, already := set[subject.PersonKey]

	changed := false
	if req.Restore {
		if already {
			delete(set, subject.PersonKey)
			changed = true
		}
	} else if !already {
		set[subject.PersonKey] = true
		changed = true
	}
	if changed {
		out := make([]string, 0, len(set))
		for k := range set {
			out = append(out, k)
		}
		slices.Sort(out)
		if err = r.store.WriteOffLimitsPersonKeys(out); err != nil {
			return PersonOffLimitsResult{}, fmt.Errorf("person: write off-limits: %w", err)
		}
	}
	return PersonOffLimitsResult{
		Record:    subject,
		OffLimits: !req.Restore,
		Ack:       personOffLimitsAck(subject, req.Restore),
	}, nil
}

// personOffLimitsAck builds the inventory ack for an off-limits toggle: the
// person and the resulting state, and that the record is kept either way.
func personOffLimitsAck(rec storage.PersonRecord, restore bool) string {
	if restore {
		return fmt.Sprintf("%q is no longer off-limits to inference.", rec.DisplayName)
	}
	return fmt.Sprintf("%q is now off-limits to inference — the raw record is kept.", rec.DisplayName)
}
