package router

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/mrz1836/lucid/internal/identity"
	"github.com/mrz1836/lucid/internal/storage"
)

// This file is the router's single seam for resolving a person by name
// (data-model.md §"Resolving a bare name"; ADR-0016). Every surface that takes a
// person subject reaches it through one of two helpers, never through its own
// name matching:
//
//   - resolvePersonNow — who a name means now, for the read surfaces: the
//     shared resolver's lookup mode (an exact key, the active claimant, the one
//     active exact holder, the first-name rule, the lone dormant holder).
//   - resolvePersonRecord — record targeting, for the curation writes: an
//     exact key, else an exact display_name/aka match across active and dormant
//     records, refused (§P-5) whenever that match disagrees with who the name
//     means now.
//
// The low-level matchers (exactKeySubject, matchPeople, recordMatchesName) live
// here too, so no other router file grows a name match of its own.

// PersonMatch is one person a subject could name, with their folded status.
type PersonMatch struct {
	Record  storage.PersonRecord
	Dormant bool
}

// PersonSubject is who a name means now ([Router.resolvePersonNow]): the
// resolved record and whether they are dormant, the dormant people who also
// hold the queried form, and — when the name is ambiguous — the candidates,
// active first, then dormant.
type PersonSubject struct {
	Record      storage.PersonRecord
	Dormant     bool
	DormantAlso []PersonMatch
	Candidates  []PersonMatch
}

// resolvePersonNow resolves a subject to who it means now, through the shared
// resolver's lookup mode. It never mints and never writes. No match is §P-4;
// an ambiguous name is §P-5 — and the returned subject still carries the
// candidates, so a read surface can list them as §P-2 instead. A blank subject
// is §P-9. With no dormant people and no claims it resolves exactly as the
// display_name/aka match always has, and refuses with the same copy.
func (r *Router) resolvePersonNow(raw string) (PersonSubject, error) {
	subject := strings.TrimSpace(raw)
	if subject == "" {
		return PersonSubject{}, rejectPerson(ErrPersonRejected, "a person name or key is required; nothing was saved.")
	}
	res, err := r.store.ResolvePersonName(subject, storage.ResolveOptions{Mode: storage.ResolveLookup})
	if err != nil {
		return PersonSubject{}, fmt.Errorf("person: resolve %q: %w", subject, err)
	}
	st, err := r.store.IdentityState()
	if err != nil {
		return PersonSubject{}, fmt.Errorf("person: read identity log: %w", err)
	}
	switch res.Outcome {
	case storage.NameResolved:
		rec, rerr := r.readCanonicalPerson(res.PersonKey)
		if rerr != nil {
			return PersonSubject{}, rerr
		}
		also, rerr := r.personMatches(st, res.DormantAlso)
		if rerr != nil {
			return PersonSubject{}, rerr
		}
		return PersonSubject{Record: rec, Dormant: res.Dormant, DormantAlso: also}, nil
	case storage.NameAmbiguous:
		cands, rerr := r.personMatches(st, res.Candidates)
		if rerr != nil {
			return PersonSubject{}, rerr
		}
		return PersonSubject{Candidates: cands}, ambiguousSubject(subject, cands)
	case storage.NameNoMatch, storage.NameMint:
		// Lookup never mints; anything but a person or an ambiguity is no one.
	}
	return PersonSubject{}, unknownSubject(subject)
}

// resolvePersonRecord resolves a curation verb's subject to the one record it
// targets (R7): an exact person_key wins (a tombstone key resolves forward),
// else the exact display_name/aka match E across active and dormant records —
// only dormant records when restoreOnly (`person dormant --restore`) — checked
// against N, who the name means now:
//
//   - no exact match: N's one person when it resolves (a claimed form that is
//     no one's exact form), §P-5 when N is ambiguous, otherwise §P-4;
//   - one exact match: used when N agrees, when N names no one, or for
//     restoreOnly;
//   - otherwise — several exact matches, or one that N disagrees with — §P-5,
//     listing the candidates (E ∪ N; only E for restoreOnly), active first and
//     each dormant key marked. A write never guesses.
//
// With no dormant people and no claims every outcome and every copy is exactly
// what the display_name/aka match always gave.
func (r *Router) resolvePersonRecord(raw string, restoreOnly bool) (storage.PersonRecord, error) {
	subject := strings.TrimSpace(raw)
	if subject == "" {
		return storage.PersonRecord{}, rejectPerson(ErrPersonRejected, "a person name or key is required; nothing was saved.")
	}
	if rec, ok, err := r.exactKeySubject(subject); err != nil {
		return storage.PersonRecord{}, err
	} else if ok {
		return rec, nil
	}

	exact, err := r.matchPeople(subject)
	if err != nil {
		return storage.PersonRecord{}, err
	}
	st, err := r.store.IdentityState()
	if err != nil {
		return storage.PersonRecord{}, fmt.Errorf("person: read identity log: %w", err)
	}
	if restoreOnly {
		exact = slices.DeleteFunc(exact, func(rec storage.PersonRecord) bool { return !st.IsDormant(rec.PersonKey) })
	}
	now, err := r.store.ResolvePersonName(subject, storage.ResolveOptions{Mode: storage.ResolveLookup})
	if err != nil {
		return storage.PersonRecord{}, fmt.Errorf("person: resolve %q: %w", subject, err)
	}

	switch {
	case len(exact) == 0 && now.Outcome == storage.NameResolved:
		return r.readCanonicalPerson(now.PersonKey)
	case len(exact) == 0 && now.Outcome != storage.NameAmbiguous:
		return storage.PersonRecord{}, unknownSubject(subject)
	case len(exact) == 1 && (restoreOnly || now.Outcome == storage.NameNoMatch ||
		(now.Outcome == storage.NameResolved && now.PersonKey == exact[0].PersonKey)):
		return exact[0], nil
	}

	keys := make([]string, 0, len(exact)+len(now.Candidates)+1)
	for _, rec := range exact {
		keys = append(keys, rec.PersonKey)
	}
	if !restoreOnly || len(exact) == 0 {
		keys = append(keys, now.Candidates...)
		if now.Outcome == storage.NameResolved {
			keys = append(keys, now.PersonKey)
		}
	}
	cands, err := r.personMatches(st, keys)
	if err != nil {
		return storage.PersonRecord{}, err
	}
	return storage.PersonRecord{}, ambiguousSubject(subject, cands)
}

// personMatches reads each key's canonical record with its folded status,
// de-duplicated and ordered active first, then dormant, each group by key —
// the order every candidate list shows.
func (r *Router) personMatches(st identity.State, keys []string) ([]PersonMatch, error) {
	uniq := slices.Clone(keys)
	slices.Sort(uniq)
	uniq = slices.Compact(uniq)
	out := make([]PersonMatch, 0, len(uniq))
	for _, k := range uniq {
		rec, err := r.readCanonicalPerson(k)
		if err != nil {
			return nil, err
		}
		out = append(out, PersonMatch{Record: rec, Dormant: st.IsDormant(rec.PersonKey)})
	}
	slices.SortStableFunc(out, func(a, b PersonMatch) int {
		if a.Dormant != b.Dormant {
			if a.Dormant {
				return 1
			}
			return -1
		}
		return cmp.Compare(a.Record.PersonKey, b.Record.PersonKey)
	})
	return out, nil
}

// readCanonicalPerson reads the live record at a canonical key the resolver
// named.
func (r *Router) readCanonicalPerson(key string) (storage.PersonRecord, error) {
	rec, found, err := r.store.ReadPerson(key)
	if err != nil {
		return storage.PersonRecord{}, fmt.Errorf("person: read %q: %w", key, err)
	}
	if !found || rec.IsTombstone() {
		return storage.PersonRecord{}, fmt.Errorf("person: %q is not a live person record", key)
	}
	return rec, nil
}

// unknownSubject is the §P-4 refusal: no live record holds the subject.
func unknownSubject(subject string) error {
	return rejectPerson(ErrPersonUnknownSubject,
		"no one matches %q — check the name or run `lucid person <name>`; nothing was saved.", subject)
}

// ambiguousSubject is the §P-5 refusal: the subject could name any of the
// candidates. With no dormant candidate the copy is exactly the
// display_name/aka match's.
func ambiguousSubject(subject string, cands []PersonMatch) error {
	return rejectPerson(ErrPersonAmbiguousSubject,
		"%q matches more than one person — disambiguate with a person_key (%s); nothing was saved.",
		subject, candidateKeys(cands))
}

// candidateKeys renders the candidate person_keys for the §P-5 copy in the
// order given (active first), each dormant key followed by " (dormant)".
func candidateKeys(cands []PersonMatch) string {
	keys := make([]string, 0, len(cands))
	for _, c := range cands {
		if c.Dormant {
			keys = append(keys, c.Record.PersonKey+" (dormant)")
			continue
		}
		keys = append(keys, c.Record.PersonKey)
	}
	return strings.Join(keys, " / ")
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

// matchPeople returns every canonical people record whose display_name or an
// aka variant equals the query (case-insensitive), sorted by person_key so the
// candidate order — and any output built from it — is deterministic. It does
// not look at status: a dormant record matches like any other.
//
// Redirect tombstones are skipped: a merge unions the source's forms into the
// canonical's aka[], so a merged-away form still matches the canonical and
// yields exactly one match rather than a spurious §P-2. Resolution never scans
// a tombstone (data-model.md §"Merge & redirect").
func (r *Router) matchPeople(query string) ([]storage.PersonRecord, error) {
	keys, err := r.store.ListPeopleKeys()
	if err != nil {
		return nil, fmt.Errorf("person: list people: %w", err)
	}
	var matches []storage.PersonRecord
	for _, key := range keys {
		rec, found, err := r.store.ReadPerson(key)
		if err != nil {
			return nil, fmt.Errorf("person: read %q: %w", key, err)
		}
		if found && !rec.IsTombstone() && recordMatchesName(rec, query) {
			matches = append(matches, rec)
		}
	}
	slices.SortStableFunc(matches, func(a, b storage.PersonRecord) int { return cmp.Compare(a.PersonKey, b.PersonKey) })
	return matches, nil
}

// recordMatchesName reports whether the queried name equals the record's
// display_name or any of its aka variants, case-insensitively.
func recordMatchesName(rec storage.PersonRecord, query string) bool {
	if strings.EqualFold(strings.TrimSpace(rec.DisplayName), query) {
		return true
	}
	return slices.ContainsFunc(rec.Aka, func(aka string) bool {
		return strings.EqualFold(strings.TrimSpace(aka), query)
	})
}
