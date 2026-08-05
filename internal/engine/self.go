package engine

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"
)

// SelfVersion is the self.json schema version this binary stamps on every
// append, so the number in a file is always true of that file. Like
// AnchorVersion it is a signal, not a gate: nothing validates it on read, and
// no read path branches on it. The store is born at version 1, so there is no
// older shape to be compatible with.
const SelfVersion = 1

// SelfStateRetired marks a record that retires its fact: the attribute stops
// appearing on the rendered profile, in the JSON profile and in the /ask
// grounding slice, while staying in the append-only history in full. State
// absent means active, exactly as in anchors.json.
const SelfStateRetired = "retired"

// SelfFactIDPrefix is the minted-id namespace: self_YYYY_MM_DD_<slot>.
const SelfFactIDPrefix = "self_"

// SelfFact is one recorded self fact (engine-module.md §self.json): a durable,
// atemporal attribute of the subject. Every other store in the Ledger is
// episodic — an event that happened at a time — and this one is semantic: the
// record is simply true, and Since (when present) is an origin rather than a
// logical day. The store is append-only, so a correction and an update are the
// same operation: a new append under the same identity. RecordedAt is the
// append time.
//
// ID is the stable identity; Key is a mutable address, so a fact can be
// corrected or re-categorized (self move) without forking its history, and two
// facts that reuse a key over time stay distinct.
//
// ID is deliberately not omitempty, which is the one place this record
// diverges from Anchor. Anchor.ID is omitempty because records exist that
// predate ids and must keep folding under a synthetic identity. This store is
// version 1 from birth: every record it has ever written carries an id, so a
// record without one is corrupt rather than historical. There is no
// LegacyAnchorIDPrefix equivalent here, no read-time identity derivation, and
// no synthetic identity anywhere in this file. Do not add one by analogy — the
// asymmetry is what makes self move exactly one append where anchor rename
// writes a pair.
type SelfFact struct {
	ID         string `json:"id"`
	Key        string `json:"key"`
	Value      string `json:"value"`
	Since      string `json:"since,omitempty"`
	Note       string `json:"note,omitempty"`
	RecordedAt string `json:"recorded_at"`
	State      string `json:"state,omitempty"`
}

// IsRetired reports whether this record retires its fact.
func (f SelfFact) IsRetired() bool { return f.State == SelfStateRetired }

// SelfLog is self.json: the schema version plus the append-only history of
// every recorded self fact. The full history is kept (audit-faithful);
// LatestSelfFacts folds it to the effective profile every read surface shows.
type SelfLog struct {
	Version int        `json:"version"`
	History []SelfFact `json:"history"`
}

// SelfFactRecord is the projected single-fact JSON surface.
//
// Unlike AnchorRecord it resolves nothing: every stored record already carries
// its id, so the two shapes are identical and ProjectSelfFact is a conversion.
// It exists anyway, and the reason is worth stating because the type otherwise
// reads as a mis-copy of its sibling: the published JSON contract must not be
// whatever the stored struct happens to be. Because the projection is a
// conversion, adding a field to SelfFact alone stops the build until someone
// decides whether the surface should carry it — which is the guard, and is
// stronger than a hand-written copy that would silently keep compiling.
//
// Field order mirrors SelfFact so the stored and projected forms read the same.
type SelfFactRecord struct {
	ID         string `json:"id"`
	Key        string `json:"key"`
	Value      string `json:"value"`
	Since      string `json:"since,omitempty"`
	Note       string `json:"note,omitempty"`
	RecordedAt string `json:"recorded_at"`
	State      string `json:"state,omitempty"`
}

// ProjectSelfFact renders a stored fact as its published JSON form.
func ProjectSelfFact(f SelfFact) SelfFactRecord {
	return SelfFactRecord(f)
}

// NextSelfFactID mints the next free fact id for the given day:
// self_YYYY_MM_DD_<slot>, advancing the slot past every id already present for
// that date. SlotLabel is the shared per-day slot grammar — insight, anchor and
// self ids are the same form.
//
// It scans the whole history rather than the folded latest set on purpose: a
// slot belonging to a fact that has since been retired must never be reissued,
// or a retired fact and a live one would share an identity and fold into each
// other.
//
// The id is never derived from the key. That is the whole point of this
// identity model — the key is a mutable address, and seeding an identity from a
// mutable value is exactly what makes a rename fork its history. Do not
// "improve" this into a readable slug.
func NextSelfFactID(log SelfLog, day time.Time) string {
	prefix := fmt.Sprintf("%s%04d_%02d_%02d_", SelfFactIDPrefix, day.Year(), int(day.Month()), day.Day())
	used := map[string]bool{}
	for _, f := range log.History {
		if slot, ok := strings.CutPrefix(f.ID, prefix); ok {
			used[slot] = true
		}
	}
	for n := 0; ; n++ {
		slot := SlotLabel(n)
		if !used[slot] {
			return prefix + slot
		}
	}
}

// SelfFactHistory returns every record carrying id, in append order.
//
// History is resolved by identity, never by key, and that is load-bearing: a
// fact that has been moved has records written under its previous key, and
// those records are still its history. Filtering by key instead would silently
// drop exactly the records a move exists to preserve. The result is empty
// (never nil) when the id is unknown.
func SelfFactHistory(log SelfLog, id string) []SelfFact {
	out := make([]SelfFact, 0, len(log.History))
	for _, f := range log.History {
		if f.ID == id {
			out = append(out, f)
		}
	}
	return out
}

// selfNamespaceOrder is the single ordered namespace list, and the only place
// the taxonomy is written down. Two consumers read it — the grouped render and
// the /ask grounding sort — so a second list would be a drift waiting to
// happen; this indirection exists to make that impossible.
//
// The order is priority, not alphabetical: identity first because it is what
// grounds who the subject is, constraint next because a standing constraint is
// more often load-bearing for an answer than a measurement, then body, then
// pref, with the misc catch-all last.
//
// It is a function rather than a package var so the list cannot be mutated by a
// caller, and so each caller gets a fresh copy for free.
func selfNamespaceOrder() []string {
	return []string{"identity", "constraint", "body", "pref", "misc"}
}

// SelfNamespaces lists the accepted key namespaces in priority order. The
// returned slice is a fresh copy, so a caller may sort or reorder it — error
// copy and help text both do.
func SelfNamespaces() []string { return selfNamespaceOrder() }

// SelfNamespacePriority returns the sort rank of a namespace: lower sorts
// first. An unrecognized namespace ranks last rather than panicking or landing
// arbitrarily — validation rejects one on the write path, so it can only reach
// here from a hand-edited file, and a read surface should still render it.
func SelfNamespacePriority(ns string) int {
	order := selfNamespaceOrder()
	if i := slices.Index(order, ns); i >= 0 {
		return i
	}
	return len(order)
}

// selfNamespaceOf returns the namespace half of a key: everything before the
// first dot. A key with no dot has no namespace and never validates, so this
// returns the whole key for it and lets it rank last.
func selfNamespaceOf(key string) string {
	ns, _, ok := strings.Cut(key, ".")
	if !ok {
		return ""
	}
	return ns
}

// ValidateSelfKey reports whether key is a legal self-fact key:
// <namespace>.<leaf>, where the namespace is one of the fixed set and the leaf
// is a non-empty run of lowercase letters, digits, dots, underscores and
// hyphens. The leaf may itself contain dots (constraint.diet.dairy), so only
// the first dot separates.
//
// An unrecognized namespace is rejected, and the rejection always names misc.
// as the landing spot — so a fact is never turned away, it only ever needs a
// named home. It is deliberately not auto-coerced into misc.: silently filing a
// typo'd namespace would hide the typo permanently, which is the opposite of
// what the catch-all is for.
//
// The key is validated exactly as given rather than trimmed and rewritten, so
// the key that validates is the key that gets stored. There is no normalizing
// step for a caller to forget, and whitespace is refused by the namespace and
// leaf checks rather than being quietly absorbed. Pure (no clock, no disk, no
// model), so the write path rejects before touching disk.
func ValidateSelfKey(key string) error {
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("engine: self fact key must not be empty")
	}
	ns, leaf, ok := strings.Cut(key, ".")
	if !ok {
		return fmt.Errorf(
			"engine: self fact key %q must be <namespace>.<leaf> using one of %s, or misc. when nothing else fits",
			key, selfNamespaceList())
	}
	if slices.Index(selfNamespaceOrder(), ns) < 0 {
		return fmt.Errorf(
			"engine: self fact key %q has an unknown namespace %q — use one of %s, or file it under misc. when nothing else fits",
			key, ns, selfNamespaceList())
	}
	if leaf == "" {
		return fmt.Errorf(
			"engine: self fact key %q needs a leaf after the namespace, for example %s.generation", key, ns)
	}
	if !validSelfLeaf(leaf) {
		return fmt.Errorf(
			"engine: self fact key %q may use only lowercase letters, digits, dots, underscores and hyphens after the namespace",
			key)
	}
	return nil
}

// selfNamespaceList renders the accepted namespaces as the dotted prefixes a
// user actually types — "identity., constraint., body., pref., misc." — so a
// rejection can be copied straight back into a command.
func selfNamespaceList() string {
	order := selfNamespaceOrder()
	out := make([]string, 0, len(order))
	for _, ns := range order {
		out = append(out, ns+".")
	}
	return strings.Join(out, ", ")
}

// validSelfLeaf reports whether every rune in leaf is in the accepted charset.
// Uppercase and whitespace are refused so a key has exactly one spelling: a
// store meant to be read back for a decade cannot afford Body.Height and
// body.height to be two facts.
func validSelfLeaf(leaf string) bool {
	for _, r := range leaf {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

// SelfValueMax is the ceiling on a fact's value, in runes.
//
// It is a size guard, not a judgment about content. The /ask grounding slice
// costs at most self_facts_cap × value length in context, and the whole point
// of capping that slice is that grounding cost stays flat as the store grows;
// an unbounded value would reintroduce the growth through the other axis. A
// value that genuinely wants more room than this is a journal entry, not an
// attribute. Raising it later costs nothing — no stored record becomes invalid.
const SelfValueMax = 500

// ValidateSelfFact reports whether key/value are a legal fact to record: a
// valid key (ValidateSelfKey) plus a non-empty value within SelfValueMax. Pure,
// so the write path can reject before touching disk.
func ValidateSelfFact(key, value string) error {
	if err := ValidateSelfKey(key); err != nil {
		return err
	}
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("engine: self fact value must not be empty")
	}
	if n := len([]rune(value)); n > SelfValueMax {
		return fmt.Errorf("engine: self fact value is %d characters; the limit is %d", n, SelfValueMax)
	}
	return nil
}

// LatestSelfFacts folds the append-only history to the effective fact per
// identity: the most-recently-appended record wins (append order), so a
// correction whose RecordedAt is *earlier* than the record it supersedes still
// supersedes it — the same rule as LatestAnchors, and the reason a hand-fixed
// timestamp cannot resurrect an old value.
//
// The fold is per ID, never per Key. That is what makes the key a mutable
// address: a moved fact keeps one identity across both of its keys instead of
// forking into an orphaned old key and a history-less new one.
//
// The result is sorted by namespace priority, then key, then id, so two facts
// that share a key have a total, stable order rather than one that depends on
// map iteration. An empty log yields an empty slice.
func LatestSelfFacts(log SelfLog) []SelfFact {
	latest := make(map[string]SelfFact, len(log.History))
	for _, f := range log.History {
		latest[f.ID] = f // last write per identity wins (append order)
	}
	out := make([]SelfFact, 0, len(latest))
	for _, f := range latest {
		out = append(out, f)
	}
	slices.SortFunc(out, compareSelfFacts)
	return out
}

// compareSelfFacts is the store's one ordering: namespace priority, then key,
// then id. It is deliberately independent of RecordedAt — see
// GroundingSelfFacts for why recency is the wrong axis for an atemporal fact.
func compareSelfFacts(a, b SelfFact) int {
	if c := cmp.Compare(SelfNamespacePriority(selfNamespaceOf(a.Key)), SelfNamespacePriority(selfNamespaceOf(b.Key))); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Key, b.Key); c != 0 {
		return c
	}
	return cmp.Compare(a.ID, b.ID)
}

// ActiveSelfFacts filters a folded set to the facts that are still true. latest
// is a folded set (LatestSelfFacts); ordering is preserved. The result is empty
// (never nil).
func ActiveSelfFacts(latest []SelfFact) []SelfFact {
	out := make([]SelfFact, 0, len(latest))
	for _, f := range latest {
		if !f.IsRetired() {
			out = append(out, f)
		}
	}
	return out
}

// FindActiveSelfFact returns the single active fact holding key, if any.
// latest is a folded set (LatestSelfFacts), and at most one active fact can
// hold a key: recording an existing active key targets that fact rather than
// minting a second one, so every verb can take a bare key and still be
// unambiguous without an id.
func FindActiveSelfFact(latest []SelfFact, key string) (SelfFact, bool) {
	for _, f := range latest {
		if !f.IsRetired() && f.Key == key {
			return f, true
		}
	}
	return SelfFact{}, false
}

// LatestRetiredSelfFactFor returns the most-recently-appended retired record
// holding key, if any — the fact a re-add reuses the key of, and the record
// whose retirement date an already-retired rejection cites.
func LatestRetiredSelfFactFor(latest []SelfFact, key string) (SelfFact, bool) {
	var (
		found SelfFact
		ok    bool
	)
	for _, f := range latest {
		if f.IsRetired() && f.Key == key {
			if !ok || f.RecordedAt > found.RecordedAt {
				found, ok = f, true
			}
		}
	}
	return found, ok
}

// SelfFactKeys lists the keys active facts hold, sorted and deduped. There is
// no list verb on this store, so this is what an unrecorded-key rejection
// names and the remedy travels with the error. Only active keys are listed,
// because those are the only ones a caller can retire or move. The result is
// empty (never nil) when nothing is recorded, so a caller can say so rather
// than printing a dangling list with nothing after it.
func SelfFactKeys(latest []SelfFact) []string {
	seen := make(map[string]bool, len(latest))
	out := make([]string, 0, len(latest))
	for _, f := range latest {
		if f.IsRetired() || seen[f.Key] {
			continue
		}
		seen[f.Key] = true
		out = append(out, f.Key)
	}
	slices.Sort(out)
	return out
}

// RetiredDate renders a retired record's civil retirement date: the date half
// of its RFC3339 RecordedAt. A value too short to carry one is returned
// unchanged — a hand-corrupted timestamp degrades to printing what is there
// rather than failing a read path.
func RetiredDate(f SelfFact) string {
	const civilDateLen = 10
	if len(f.RecordedAt) < civilDateLen {
		return f.RecordedAt
	}
	return f.RecordedAt[:civilDateLen]
}

// GroundingSelfFacts returns the active profile slice /ask grounds on: active
// facts only, ordered by namespace priority then key, truncated to limit.
//
// The ordering is the one thing here not copied from the insights slice, and
// the divergence is the point. ReadAcceptedInsights caps by recency, which is
// right for episodic material and exactly backwards for an atemporal one: a
// blood type recorded in year one never changes, so a recency sort would evict
// it in favor of last week's trivia and the slice would get less useful as the
// store got richer. Namespace priority is stable across turns, independent of
// write order, and identical for the same profile however it was accumulated.
// Do not sort this by RecordedAt.
//
// A non-positive limit yields an empty slice. Config validates the cap at >= 1,
// but the Engine core must not depend on a caller having validated anything.
// The parameter is named limit rather than cap only because cap is a builtin.
func GroundingSelfFacts(latest []SelfFact, limit int) []SelfFact {
	if limit <= 0 {
		return []SelfFact{}
	}
	out := ActiveSelfFacts(latest)
	slices.SortFunc(out, compareSelfFacts)
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
