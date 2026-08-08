package storage

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/observations"
)

// The remedy commands a refusal names when a subject key is well-formed but
// carries no written id yet: the id backfills are explicit maintenance verbs
// (never automatic), so an anchor or storm episode that predates ids is
// reachable only after one of these runs (data-model.md §"Link ledger",
// engine-module.md §"Backfilling ids"). Naming the remedy in the refusal is the
// whole point — "there is an error" is not the requirement, "the error tells
// you what to do" is.
const (
	anchorBackfillCmd = "lucid anchor backfill-ids"
	stormBackfillCmd  = "lucid storm backfill-episodes"
)

// SubjectResolver reports whether key names an existing subject of its kind,
// returning a refusal that names the problem when it does not. Registering one
// is the entire cost of making a new kind linkable — the link ledger stores
// kind and key opaquely and never learns either, so a new kind is one resolver
// and zero ledger-schema changes (data-model.md §"Link ledger", the open
// taxonomy). A resolver is a read: it never writes, and resolution always runs
// before an event is appended, so a refused --to leaves the ledger untouched.
type SubjectResolver func(a *Adapter, key string) error

// RegisterSubjectKind adds a resolver for a new subject kind, additively. It is
// the structural promise of the open taxonomy: a future kind (a pet, say, and
// anything after) becomes linkable by registering one resolver here, with no
// change to links.go or the on-disk record. The kind must match
// ^[a-z][a-z0-9_]*$, and an already-registered kind is refused rather than
// silently replaced — a silent replace would let a caller hijack person.
func (a *Adapter) RegisterSubjectKind(kind string, fn SubjectResolver) error {
	if !validSubjectKind(kind) {
		return fmt.Errorf("storage: subject kind %q is not a valid kind name (want ^[a-z][a-z0-9_]*$)", kind)
	}
	if fn == nil {
		return fmt.Errorf("storage: subject kind %q resolver must not be nil", kind)
	}
	m := a.subjectResolvers()
	if _, exists := m[kind]; exists {
		return fmt.Errorf("storage: subject kind %q is already registered", kind)
	}
	m[kind] = fn
	return nil
}

// ResolveSubject reports whether kind:key names an existing subject, dispatching
// through the resolver registry. An unknown kind is refused by name — with every
// accepted kind listed, so a typo is corrected without reading docs — and a
// known kind's own resolver decides whether the key resolves. It writes nothing:
// the router calls it up front, before any AppendLinkEvent, so a bad subject can
// never leave a half-written ledger.
func (a *Adapter) ResolveSubject(kind, key string) error {
	fn, ok := a.subjectResolvers()[kind]
	if !ok {
		return fmt.Errorf("storage: unknown subject kind %q; accepted kinds are %s",
			kind, strings.Join(a.SubjectKinds(), ", "))
	}
	return fn(a, key)
}

// SubjectKinds returns the registered kinds in sorted order, so a refusal can
// name every accepted kind rather than stopping at "unknown kind".
func (a *Adapter) SubjectKinds() []string {
	m := a.subjectResolvers()
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// subjectResolvers returns the adapter's resolver table, lazily seeded from
// defaultSubjectResolvers on first use. Storing it on the adapter — rather than
// a package-level mutable global — keeps a registered kind from leaking between
// instances or tests, and keeps the linter's no-globals rule satisfied.
func (a *Adapter) subjectResolvers() map[string]SubjectResolver {
	if a.subjects == nil {
		a.subjects = defaultSubjectResolvers()
	}
	return a.subjects
}

// defaultSubjectResolvers returns the twelve launch resolvers, each keyed by its
// kind and resolving through that family's own native key helper — no new key
// scheme (data-model.md §"Link ledger" kind table). memory is an alias over a
// KindMemory observation, not its own record family (D11); a new kind such as a
// pet joins for free the moment its resolver is registered.
func defaultSubjectResolvers() map[string]SubjectResolver {
	return map[string]SubjectResolver{
		"person":      resolvePersonSubject,
		"injury":      registrySubjectResolver(observations.RegistryInjury),
		"thread":      registrySubjectResolver(observations.RegistryThread),
		"era":         registrySubjectResolver(observations.RegistryEra),
		"place":       registrySubjectResolver(observations.RegistryPlace),
		"raw":         resolveRawSubject,
		"day":         resolveDaySubject,
		"observation": resolveObservationSubject,
		"anchor":      resolveAnchorSubject,
		"self":        resolveSelfSubject,
		"storm":       resolveStormSubject,
		"memory":      resolveMemorySubject,
	}
}

// resolvePersonSubject resolves a person_<slug> against the people tree.
func resolvePersonSubject(a *Adapter, key string) error {
	_, found, err := a.ReadPerson(key)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("storage: no person is recorded under %q", key)
	}
	return nil
}

// registrySubjectResolver builds the resolver for a registry-backed kind
// (injury, thread, era, place): the key is the kind-prefixed registry key, and
// resolution is a straight ReadRegistry lookup — no rename/alias layer, because
// a registry key is stable once assigned.
func registrySubjectResolver(kind string) SubjectResolver {
	return func(a *Adapter, key string) error {
		_, found, err := a.ReadRegistry(kind, key)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("storage: no %s is registered under %q", kind, key)
		}
		return nil
	}
}

// resolveRawSubject resolves a raw_YYYY_MM_DD_HH_MM id against the raw tree. A
// malformed id and a missing entry both surface as the same clean refusal — the
// key does not name an existing raw entry either way.
func resolveRawSubject(a *Adapter, key string) error {
	if _, err := a.ReadRaw(key); err != nil {
		return fmt.Errorf("storage: no raw entry exists under %q", key)
	}
	return nil
}

// resolveDaySubject resolves a YYYY-MM-DD logical day. A logical day is a
// calendar fact the day view joins, not a stored record family, so it resolves
// on shape and the future ceiling alone (derived decision 2) — requiring a
// non-empty day would refuse a legitimately quiet day, which is exactly a day a
// photo might be about.
//
// The ceiling compares civil-date strings — both are zero-padded YYYY-MM-DD, so
// lexical order is chronological — against today's wall-clock date, matching the
// capture verbs' "that day has not happened yet" ceiling. It reads the date off
// time.Now without a locale-bound time.Local reference, and the resolver
// contract (a, key) exposes no clock seam to inject one.
func resolveDaySubject(_ *Adapter, key string) error {
	if _, err := time.Parse(time.DateOnly, key); err != nil {
		return fmt.Errorf("storage: day %q is not a valid YYYY-MM-DD date", key)
	}
	if key > time.Now().Format(time.DateOnly) {
		return fmt.Errorf("storage: day %q is in the future", key)
	}
	return nil
}

// resolveObservationSubject resolves an obs_YYYY_MM_DD_NNN id against the
// observation store.
func resolveObservationSubject(a *Adapter, key string) error {
	_, found, err := a.ReadObservationByID(key)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("storage: no observation exists under %q", key)
	}
	return nil
}

// resolveMemorySubject resolves memory:<observation_id> as an alias over the
// observation store (D11): the observation must exist and its event kind must be
// KindMemory. An observation of any other kind is refused, naming the kind it
// actually is — this is the one resolver that can find a record and still
// correctly refuse it.
func resolveMemorySubject(a *Adapter, key string) error {
	ev, found, err := a.ReadObservationByID(key)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("storage: no observation exists under %q", key)
	}
	if ev.Kind != observations.KindMemory {
		return fmt.Errorf("storage: observation %q is a %q event, not a memory", key, ev.Kind)
	}
	return nil
}

// resolveAnchorSubject resolves an anchor_YYYY_MM_DD_<slot> id against the
// append-only anchor history. A legacy:<label> key is refused outright: it is a
// read-time synthetic identity that is never written and never a durable link
// key (D12), so the refusal names the backfill remedy rather than accepting it.
// A well-formed id absent from the history is likewise refused with the remedy,
// because the most likely reason is that the anchor predates ids (D19). A
// missing engine tree reads as an empty history — a fresh Ledger simply has no
// anchor to name — rather than a confusing file-not-found error.
func resolveAnchorSubject(a *Adapter, key string) error {
	if strings.HasPrefix(key, engine.LegacyAnchorIDPrefix) {
		return fmt.Errorf("storage: %q is a read-time legacy anchor identity, never a durable link key; run %q to give this anchor a written id first",
			key, anchorBackfillCmd)
	}
	log, err := a.ReadAnchors()
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, an := range log.History {
		if an.ID == key {
			return nil
		}
	}
	return fmt.Errorf("storage: no anchor carries the id %q; if this anchor predates ids, run %q to give it one",
		key, anchorBackfillCmd)
}

// resolveSelfSubject resolves a self_YYYY_MM_DD_<slot> id against the append-only
// self-fact history. Unlike anchors, this store has carried ids since birth, so
// there is no legacy form to refuse and no backfill remedy to name.
func resolveSelfSubject(a *Adapter, key string) error {
	log, err := a.ReadSelfFacts()
	if err != nil {
		return err
	}
	for _, f := range log.History {
		if f.ID == key {
			return nil
		}
	}
	return fmt.Errorf("storage: no self fact carries the id %q", key)
}

// resolveStormSubject resolves a storm_YYYY_MM_DD_<slot> episode id against the
// episodes[] index in storm.json. A well-formed id absent from the index is
// refused with the backfill remedy named (D19): an episode that predates the
// index is reachable only after the storm backfill runs. A missing engine tree
// reads as an empty index rather than a file-not-found error.
func resolveStormSubject(a *Adapter, key string) error {
	h, err := a.ReadStormState()
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, ep := range h.Episodes {
		if ep.ID == key {
			return nil
		}
	}
	return fmt.Errorf("storage: no storm episode carries the id %q; if this episode predates ids, run %q to give it one",
		key, stormBackfillCmd)
}

// validSubjectKind reports whether kind matches ^[a-z][a-z0-9_]*$: a lowercase
// letter first, then lowercase letters, digits, or underscores. It is the one
// shape both the ledger's opaque kind and a newly registered kind must satisfy,
// hand-rolled rather than via a package-level compiled regexp so no global is
// introduced.
func validSubjectKind(kind string) bool {
	if kind == "" {
		return false
	}
	for i, r := range kind {
		switch {
		case r >= 'a' && r <= 'z':
			// a lowercase letter is legal anywhere, including the first position
		case i > 0 && (r == '_' || (r >= '0' && r <= '9')):
			// digits and underscores are legal only after the first character
		default:
			return false
		}
	}
	return true
}
