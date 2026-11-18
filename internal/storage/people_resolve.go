package storage

import (
	"fmt"
	"slices"
	"strings"

	"github.com/mrz1836/lucid/data"
	"github.com/mrz1836/lucid/internal/identity"
)

// ResolveMode selects which of the shared resolver's two modes runs
// (data-model.md §"Resolving a bare name"). Both modes share every decision
// step; they differ only in what a name with no single answer becomes.
type ResolveMode int

const (
	// ResolveLookup answers "who does this name mean now?" for the read
	// surfaces — `lucid person <name>`, the contact subject, gratitude, and
	// memory linking. It never mints and never writes. It is the zero value, so
	// an unset mode can never mint.
	ResolveLookup ResolveMode = iota
	// ResolveCapture answers "where does this new mention go?" for the People
	// routine ([Adapter.UpdatePerson]). It may answer "hold" or "mint", which
	// the routine then carries out.
	ResolveCapture
)

// NameOutcome is the kind of answer the resolver gave.
type NameOutcome int

const (
	// NameNoMatch is lookup's answer when there is no exact holder and no
	// claimant (§P-1).
	NameNoMatch NameOutcome = iota
	// NameResolved is the answer when the name means exactly one existing
	// person, PersonKey.
	NameResolved
	// NameAmbiguous is the answer when the name could mean any of Candidates.
	// Lookup shows §P-2; capture holds the mention (§P-11).
	NameAmbiguous
	// NameMint is capture's answer when the mention needs a fresh record
	// (MintKey).
	NameMint
)

// ResolveStep names the rule that decided a resolution, in the order the
// rules run.
type ResolveStep string

const (
	// StepKey is an exact person_key (lookup only).
	StepKey ResolveStep = "key"
	// StepPin is a pin that settled this raw entry's mention of the form
	// (capture only).
	StepPin ResolveStep = "pin"
	// StepPrior is the raw entry's prior processed artifact, which already
	// recorded the mention — a person or a hold — that stickiness keeps
	// (capture only).
	StepPrior ResolveStep = "prior"
	// StepClaim is the form's active claimant.
	StepClaim ResolveStep = "claim"
	// StepExact is lookup's sole active exact holder, or an ambiguity among
	// exact holders (both modes).
	StepExact ResolveStep = "exact"
	// StepSlug is capture's ordinary fold: the form's sole active slug-path
	// holder, with no other active exact holder.
	StepSlug ResolveStep = "slug"
	// StepFirstName is the first-name rule: every exact holder is dormant, and
	// the single-word form goes to (or is ambiguous among) the active people
	// with that first name.
	StepFirstName ResolveStep = "first_name"
	// StepDormant is lookup's single dormant exact holder, when no active
	// person takes the name.
	StepDormant ResolveStep = "dormant"
	// StepMint is capture's existing mint branch, run unchanged when the form
	// has no slug-path holder at all (nickname-map routing included).
	StepMint ResolveStep = "mint"
	// StepMintNext is capture's mint under the next free collision suffix,
	// because every slug-path holder is dormant.
	StepMintNext ResolveStep = "mint_next"
	// StepNone is lookup's answer when nothing matched.
	StepNone ResolveStep = "none"
)

// ResolveOptions carries what a resolution needs beyond the form itself.
type ResolveOptions struct {
	// Mode is lookup (the zero value) or capture.
	Mode ResolveMode
	// RawID is the raw entry a captured mention is in (capture only): it keys
	// the pin lookup.
	RawID string
	// PriorKey is the person_key the raw entry's prior processed artifact
	// already recorded for this form, if any (capture only; stickiness).
	PriorKey string
	// PriorHeld is the held_people[] entry the raw entry's prior processed
	// artifact already recorded for this form, if any (capture only;
	// stickiness).
	PriorHeld *HeldPerson
}

// NameResolution is the resolver's answer.
type NameResolution struct {
	// Outcome is the kind of answer.
	Outcome NameOutcome
	// PersonKey is the canonical person the name means (NameResolved).
	PersonKey string
	// Step names the rule that decided it.
	Step ResolveStep
	// Dormant reports that PersonKey is dormant (NameResolved).
	Dormant bool
	// Candidates are the people an ambiguous name could mean (NameAmbiguous):
	// active people first, then dormant ones, each group sorted by key.
	Candidates []string
	// DormantAlso names the dormant exact holders of the form other than the
	// person it resolved to, sorted by key — the one line a lookup of an
	// active person adds (lookup only).
	DormantAlso []string
	// MintKey is the key a fresh record takes (NameMint).
	MintKey string
	// HoldReason is why an ambiguous capture holds the mention: exact or
	// first_name (NameAmbiguous).
	HoldReason string
	// SlugHolder reports that PersonKey is the form's sole active slug-path
	// holder with no other active exact holder — the case where capture folds
	// the mention exactly as it always has, the form joining aka[] (capture
	// only).
	SlugHolder bool
}

// ResolvePersonName is the one deterministic, model-free resolver every
// people surface shares (data-model.md §"Resolving a bare name"; ADR-0016).
// It reads the people tree and the identity log and never writes either.
//
// Lookup mode resolves who a name means now: an exact person_key, then the
// form's active claimant, then its single active exact holder, then — when
// every exact holder is dormant — the one active person with that first name
// (a single-word form) or the single dormant holder, labeled.
//
// Capture mode resolves where a new mention goes: a pin for the raw entry and
// form, then the raw entry's prior resolution (opts.PriorKey/PriorHeld), then
// the active claimant, then the slug path. The slug path — the derived slug,
// its redirect, and the same-name suffix chain — is read slot by slot from
// each slot's own record; aka[] is never scanned on it. The discovery read of
// exact display_name/aka holders can only hold a mention, never assign one.
func (a *Adapter) ResolvePersonName(form string, opts ResolveOptions) (NameResolution, error) {
	ix, err := a.loadPeopleIndex()
	if err != nil {
		return NameResolution{}, err
	}
	st, err := a.identityStateWith(ix)
	if err != nil {
		return NameResolution{}, err
	}
	return a.resolveName(ix, st, form, opts)
}

// resolveName dispatches to the mode's rules over one snapshot of the people
// tree and the folded identity state.
func (a *Adapter) resolveName(ix peopleIndex, st identity.State, form string, opts ResolveOptions) (NameResolution, error) {
	if opts.Mode == ResolveCapture {
		return a.resolveCapture(ix, st, form, opts)
	}
	return resolveLookup(ix, st, form), nil
}

// resolveLookup is lookup mode (never mints, never writes).
func resolveLookup(ix peopleIndex, st identity.State, form string) NameResolution {
	form = strings.TrimSpace(form)

	// 1. An exact person_key wins; a tombstone key resolves forward.
	if _, ok := ix.recs[form]; ok {
		if canon, live := ix.canonical(form); live {
			return resolvedTo(st, canon.PersonKey, StepKey)
		}
	}

	exact := ix.formMatch(form)

	// 2. The form's active claimant wins.
	if key, ok := activeClaimant(ix, st, form); ok {
		res := resolvedTo(st, key, StepClaim)
		res.DormantAlso = dormantOthers(st, exact, key)
		return res
	}

	// 3. Exactly one active exact holder wins; two or more are ambiguous.
	active, dormant := splitByStatus(st, exact)
	switch {
	case len(active) == 1:
		res := resolvedTo(st, active[0], StepExact)
		res.DormantAlso = dormant
		return res
	case len(active) > 1:
		return ambiguous(st, StepExact, identity.HoldExact, exact)
	case len(exact) == 0:
		// 5. No exact holder and no claimant: no match (§P-1).
		return NameResolution{Outcome: NameNoMatch, Step: StepNone}
	}

	// 4. Every exact holder is dormant.
	if singleWord(form) {
		switch first := ix.activeFirstNameMatches(st, form); len(first) {
		case 1:
			res := resolvedTo(st, first[0], StepFirstName)
			res.DormantAlso = dormant
			return res
		case 0:
		default:
			return ambiguous(st, StepFirstName, identity.HoldFirstName, append(first, dormant...))
		}
	}
	if len(dormant) == 1 {
		return resolvedTo(st, dormant[0], StepDormant)
	}
	return ambiguous(st, StepExact, identity.HoldExact, dormant)
}

// resolveCapture is capture mode: where a new mention of form in opts.RawID
// goes. It reports a hold or a mint as an outcome and writes nothing.
func (a *Adapter) resolveCapture(ix peopleIndex, st identity.State, form string, opts ResolveOptions) (NameResolution, error) {
	// The slug path needs a derivable key; a form that normalizes to nothing
	// is refused before any rule runs, exactly as before.
	if _, err := DerivePersonKey(form, data.Wordlist()); err != nil {
		return NameResolution{}, err
	}
	path := ix.slugPath(form)
	exact := ix.formMatch(form)

	// 1. A pin for this (raw_id, form) wins.
	if opts.RawID != "" {
		if p, ok := st.Pin(opts.RawID, form); ok {
			if canon, live := ix.canonical(p.PersonKey); live {
				return stickyTo(st, canon.PersonKey, StepPin, path, exact), nil
			}
		}
	}

	// 2. Otherwise the raw's prior processed entry for the form wins.
	if opts.PriorKey != "" {
		if canon, live := ix.canonical(opts.PriorKey); live {
			return stickyTo(st, canon.PersonKey, StepPrior, path, exact), nil
		}
	}
	if opts.PriorHeld != nil {
		return NameResolution{
			Outcome:    NameAmbiguous,
			Step:       StepPrior,
			Candidates: slices.Clone(opts.PriorHeld.Candidates),
			HoldReason: opts.PriorHeld.Reason,
		}, nil
	}

	// 3. Otherwise the form's active claimant wins.
	if key, ok := activeClaimant(ix, st, form); ok {
		return resolvedTo(st, key, StepClaim), nil
	}

	// 4. The slug path (S: its active holders) against the active exact
	// holders found only through discovery (X).
	s, x, slugDormant := splitSlugPath(st, path, exact)
	allExact := unionKeys(path, exact)
	switch {
	case len(s)+len(x) >= 2:
		return ambiguous(st, StepExact, identity.HoldExact, allExact), nil
	case len(s) == 1:
		res := resolvedTo(st, s[0], StepSlug)
		res.SlugHolder = true
		return res, nil
	case len(x) == 1 && len(slugDormant) > 0:
		// The discovery read can only hold a mention, never assign one.
		return ambiguous(st, StepExact, identity.HoldExact, allExact), nil
	case len(slugDormant) == 0:
		// 6. No slug-path holder at all: the existing mint branch runs
		// unchanged, nickname-map routing included.
		key, err := ResolvePersonKey(form, data.Wordlist(), ix.owner)
		if err != nil {
			return NameResolution{}, err
		}
		return NameResolution{Outcome: NameMint, Step: StepMint, MintKey: ix.canonKey(key)}, nil
	}

	// 5. Every slug-path holder is dormant and no one else holds the form.
	if singleWord(form) {
		switch first := ix.activeFirstNameMatches(st, form); len(first) {
		case 1:
			return resolvedTo(st, first[0], StepFirstName), nil
		case 0:
		default:
			return ambiguous(st, StepFirstName, identity.HoldFirstName, append(first, allExact...)), nil
		}
	}
	key, err := ResolvePersonKey(form, data.Wordlist(), a.mintOwner(st, NormalizeName(form)))
	if err != nil {
		return NameResolution{}, err
	}
	return NameResolution{Outcome: NameMint, Step: StepMintNext, MintKey: key}, nil
}

// stickyTo resolves a pinned or previously recorded mention to key, marking it
// a slug-holder fold when key is exactly what the ordinary fold would choose —
// so a re-run of an ordinary mention behaves exactly as it always has.
func stickyTo(st identity.State, key string, step ResolveStep, path, exact []string) NameResolution {
	res := resolvedTo(st, key, step)
	s, x, _ := splitSlugPath(st, path, exact)
	res.SlugHolder = len(s) == 1 && len(x) == 0 && s[0] == key
	return res
}

// resolvedTo builds a resolution to one canonical person.
func resolvedTo(st identity.State, key string, step ResolveStep) NameResolution {
	return NameResolution{Outcome: NameResolved, PersonKey: key, Step: step, Dormant: st.IsDormant(key)}
}

// ambiguous builds an ambiguous resolution over keys, ordered active first.
func ambiguous(st identity.State, step ResolveStep, reason string, keys []string) NameResolution {
	return NameResolution{
		Outcome:    NameAmbiguous,
		Step:       step,
		Candidates: orderCandidates(st, keys),
		HoldReason: reason,
	}
}

// activeClaimant returns the form's active claimant when its record is a live
// canonical person. A claim on a person that no longer exists is skipped
// (lucid validate reports it).
func activeClaimant(ix peopleIndex, st identity.State, form string) (string, bool) {
	c, ok := st.ActiveClaimant(form)
	if !ok {
		return "", false
	}
	canon, live := ix.canonical(c.PersonKey)
	if !live || st.IsDormant(canon.PersonKey) {
		return "", false
	}
	return canon.PersonKey, true
}

// splitSlugPath partitions the form's holders for capture step 4: s is the
// active slug-path holders, x the active exact holders found only through
// discovery (not on the slug path), and slugDormant the dormant slug-path
// holders.
func splitSlugPath(st identity.State, path, exact []string) (s, x, slugDormant []string) {
	for _, k := range path {
		if st.IsDormant(k) {
			slugDormant = append(slugDormant, k)
		} else {
			s = append(s, k)
		}
	}
	for _, k := range exact {
		if !slices.Contains(path, k) && !st.IsDormant(k) {
			x = append(x, k)
		}
	}
	return s, x, slugDormant
}

// splitByStatus partitions keys into active and dormant, preserving order.
func splitByStatus(st identity.State, keys []string) (active, dormant []string) {
	for _, k := range keys {
		if st.IsDormant(k) {
			dormant = append(dormant, k)
		} else {
			active = append(active, k)
		}
	}
	return active, dormant
}

// dormantOthers returns the dormant keys among exact other than self.
func dormantOthers(st identity.State, exact []string, self string) []string {
	var out []string
	for _, k := range exact {
		if k != self && st.IsDormant(k) {
			out = append(out, k)
		}
	}
	return out
}

// orderCandidates de-duplicates keys and orders them active first, then
// dormant, each group sorted by key — the order every candidate list shows.
func orderCandidates(st identity.State, keys []string) []string {
	uniq := slices.Clone(keys)
	slices.Sort(uniq)
	uniq = slices.Compact(uniq)
	active, dormant := splitByStatus(st, uniq)
	return append(active, dormant...)
}

// unionKeys returns the sorted, de-duplicated union of two key lists.
func unionKeys(a, b []string) []string {
	out := append(slices.Clone(a), b...)
	slices.Sort(out)
	return slices.Compact(out)
}

// singleWord reports whether the form has exactly one whitespace-delimited
// field.
func singleWord(form string) bool { return len(strings.Fields(form)) == 1 }

// mintOwner wraps [Adapter.personKeyOwner] for capture's dormant-holder mint
// and `person create` (ADR-0016): a same-name slot whose canonical record is
// dormant — or no longer a live record — counts as taken, so the new record
// takes the next free collision suffix. Every other slot answers exactly as
// personKeyOwner does; [ResolvePersonKey] and [DerivePersonKey] are unchanged.
func (a *Adapter) mintOwner(st identity.State, self string) KeyOwnerFunc {
	return func(candidate string) (string, bool) {
		normalized, exists := a.personKeyOwner(candidate)
		if !exists || normalized != self {
			return normalized, exists
		}
		canon, err := a.ResolvePersonRedirect(candidate)
		if err != nil {
			return normalized + "\x00", true
		}
		rec, found, err := a.ReadPerson(canon)
		if err != nil || !found || rec.IsTombstone() || st.IsDormant(canon) {
			return normalized + "\x00", true // taken: never the same person again
		}
		return normalized, true
	}
}

// peopleIndex is one read of the people tree: every record on disk by key.
// The resolver works over a single snapshot so one resolution scans the tree
// once.
type peopleIndex struct {
	keys []string
	recs map[string]PersonRecord
}

// loadPeopleIndex reads every person record. A missing people/ tree is the
// empty index; an unreadable record surfaces its error, as the /person scan
// always has.
func (a *Adapter) loadPeopleIndex() (peopleIndex, error) {
	keys, err := a.ListPeopleKeys()
	if err != nil {
		return peopleIndex{}, err
	}
	ix := peopleIndex{keys: keys, recs: make(map[string]PersonRecord, len(keys))}
	for _, k := range keys {
		rec, found, rerr := a.ReadPerson(k)
		if rerr != nil {
			return peopleIndex{}, rerr
		}
		if found {
			ix.recs[k] = rec
		}
	}
	return ix, nil
}

// identityStateWith folds the identity log, canonicalizing through the
// index's redirect graph. A Ledger without the log reads as everyone active.
func (a *Adapter) identityStateWith(ix peopleIndex) (identity.State, error) {
	events, err := a.ReadIdentityEvents()
	if err != nil {
		return identity.State{}, err
	}
	if len(events) == 0 {
		return identity.Fold(nil, nil), nil
	}
	return identity.Fold(events, ix.canonKey), nil
}

// canonKey follows a single-hop redirect: a tombstone's key becomes the key it
// forwards to; any other key is itself.
func (ix peopleIndex) canonKey(key string) string {
	if rec, ok := ix.recs[key]; ok && rec.IsTombstone() {
		return rec.RedirectTo
	}
	return key
}

// canonical returns the live canonical record a key resolves to — the record
// itself, or the one its tombstone forwards to — and ok=false for an absent
// key, a dangling tombstone, or a chained one.
func (ix peopleIndex) canonical(key string) (PersonRecord, bool) {
	rec, ok := ix.recs[key]
	if !ok {
		return PersonRecord{}, false
	}
	if !rec.IsTombstone() {
		return rec, true
	}
	target, ok := ix.recs[rec.RedirectTo]
	if !ok || target.IsTombstone() {
		return PersonRecord{}, false
	}
	return target, true
}

// owner is [Adapter.personKeyOwner] over the snapshot.
func (ix peopleIndex) owner(candidate string) (string, bool) {
	rec, ok := ix.recs[candidate]
	if !ok {
		return "", false
	}
	return NormalizeName(rec.DisplayName), true
}

// slugPath returns the form's slug-path holders: the canonical people its
// derived slug and each same-name suffix slot (-2, -3, …) resolve to, in chain
// order, de-duplicated, walked until the first free slot. Each slot is read
// from its own record — a slot belongs to the form only when its own
// display_name normalizes to it — so aka[] is never scanned on the
// slug-resolution path and the collision oracle is untouched. A slot of a
// different name is skipped, exactly as the collision-suffix rule skips it.
func (ix peopleIndex) slugPath(form string) []string {
	base, err := DerivePersonKey(form, data.Wordlist())
	if err != nil {
		return nil
	}
	self := NormalizeName(form)
	var holders []string
	for n := 1; ; n++ {
		slot := base
		if n > 1 {
			slot = fmt.Sprintf("%s-%d", base, n)
		}
		rec, ok := ix.recs[slot]
		if !ok {
			return holders
		}
		if NormalizeName(rec.DisplayName) != self {
			continue
		}
		canon, live := ix.canonical(slot)
		if live && !slices.Contains(holders, canon.PersonKey) {
			holders = append(holders, canon.PersonKey)
		}
	}
}

// formMatch returns the form's exact holders — the live canonical records
// whose display_name or an aka equals it case-insensitively (the /person
// matching rule), sorted by key. It is a discovery read: capture may hold a
// mention on it, never assign one.
func (ix peopleIndex) formMatch(form string) []string {
	query := strings.TrimSpace(form)
	var out []string
	for _, k := range ix.keys {
		rec, ok := ix.recs[k]
		if !ok || rec.IsTombstone() {
			continue
		}
		if formMatches(rec, query) {
			out = append(out, k)
		}
	}
	return out
}

// formMatches reports whether query equals the record's display_name or any
// aka, case-insensitively.
func formMatches(rec PersonRecord, query string) bool {
	if strings.EqualFold(strings.TrimSpace(rec.DisplayName), query) {
		return true
	}
	return slices.ContainsFunc(rec.Aka, func(aka string) bool {
		return strings.EqualFold(strings.TrimSpace(aka), query)
	})
}

// activeFirstNameMatches returns the active live canonical people whose first
// name — the shared lookup form of their display_name ([data.NicknameForm]) —
// equals the form's, sorted by key.
func (ix peopleIndex) activeFirstNameMatches(st identity.State, form string) []string {
	first := data.NicknameForm(form)
	if first == "" {
		return nil
	}
	var out []string
	for _, k := range ix.keys {
		rec, ok := ix.recs[k]
		if !ok || rec.IsTombstone() || st.IsDormant(k) {
			continue
		}
		if data.NicknameForm(rec.DisplayName) == first {
			out = append(out, k)
		}
	}
	return out
}
