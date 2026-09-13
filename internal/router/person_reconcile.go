package router

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/mrz1836/lucid/data"
	"github.com/mrz1836/lucid/internal/storage"
)

// The fixed /person reconcile copy. None of it is agent-authored — reconcile is
// a deterministic, no-LLM detector (P9).
const reconcileNoCandidates = "No likely-duplicate people found."

// reconcileMinPrefixLen is the shortest diminutive prefix accepted (so "sam" ⊂
// "sammy" fires but a one- or two-letter prefix never does). The bare
// edit-distance signal was removed — it paired unrelated short names like
// "Braden"/"Brandon" and "Dana"/"Dania" — so every suggestion now needs a
// corroborating signal: a shared written form, a curated nickname link, or this
// diminutive prefix (data-model.md §"Alias-aware person resolution"; scope.md §4).
const reconcileMinPrefixLen = 3

// ReconcileCandidate is one suggested duplicate pair: the source (fold away) and
// target (canonical) records, why they were paired, and the exact merge that
// would collapse them. Direction is deterministic — the record with fewer
// mentions folds into the one with more (ties: later first_seen, then key).
type ReconcileCandidate struct {
	SourceKey  string
	SourceName string
	TargetKey  string
	TargetName string
	Reason     string
}

// PersonReconcileResult reports the reconcile scan. Candidates is always a
// (possibly empty) slice — an honest empty result, never an error. Text is the
// byte-stable rendered output.
type PersonReconcileResult struct {
	Candidates []ReconcileCandidate
	Text       string
}

// reconcilePerson is one live canonical record reduced to what the detector
// needs: its key, display name, normalized forms, and the tie-break inputs.
type reconcilePerson struct {
	key       string
	display   string
	forms     []string // sorted, unique normalized display + aka forms
	nickForms []string // sorted, unique nickname lookup forms (data.NicknameForm of display + aka)
	refs      int
	firstSeen string // RFC3339, for the deterministic tie-break
}

// PersonReconcile executes `lucid person reconcile`: a deterministic, no-LLM
// detector that lists likely-duplicate people so the user can `person merge`
// them (scope.md §4; data-model.md §"Merge & redirect"). It never writes and
// never calls a model. Off-limits people are excluded, redirect tombstones are
// skipped, and the output is byte-stable across repeated runs on the same store
// (S-22). Finding nothing is an honest empty result, not an error.
func (r *Router) PersonReconcile() (PersonReconcileResult, error) {
	people, err := r.liveReconcilePeople()
	if err != nil {
		return PersonReconcileResult{}, err
	}

	// The curated nickname map is the same for every pair, so parse it once for
	// the whole scan and thread it through reconcileSignal.
	links := data.NicknameLinks()

	var cands []ReconcileCandidate
	for i := range people {
		for j := i + 1; j < len(people); j++ {
			if reason, ok := reconcileSignal(people[i], people[j], links); ok {
				cands = append(cands, buildCandidate(people[i], people[j], reason))
			}
		}
	}

	// Sort by the unordered pair (lower key, higher key) so the list is stable
	// regardless of the suggested direction.
	slices.SortFunc(cands, func(a, b ReconcileCandidate) int {
		alo, ahi := sortedPair(a.SourceKey, a.TargetKey)
		blo, bhi := sortedPair(b.SourceKey, b.TargetKey)
		return cmp.Or(cmp.Compare(alo, blo), cmp.Compare(ahi, bhi))
	})

	return PersonReconcileResult{Candidates: cands, Text: renderReconcile(cands)}, nil
}

// liveReconcilePeople loads every live canonical person (tombstones and
// off-limits people excluded), reduced to the detector's inputs and sorted by
// key so the pairwise scan — and its output — is deterministic.
func (r *Router) liveReconcilePeople() ([]reconcilePerson, error) {
	keys, err := r.store.ListPeopleKeys()
	if err != nil {
		return nil, fmt.Errorf("reconcile: list people: %w", err)
	}
	offLimits, err := r.store.ReadOffLimitsPersonKeys()
	if err != nil {
		return nil, fmt.Errorf("reconcile: read off-limits: %w", err)
	}
	deny := toSet(offLimits)

	out := make([]reconcilePerson, 0, len(keys))
	for _, key := range keys {
		rec, found, err := r.store.ReadPerson(key)
		if err != nil {
			return nil, fmt.Errorf("reconcile: read %q: %w", key, err)
		}
		if !found || rec.IsTombstone() || deny[key] {
			continue
		}
		out = append(out, reconcilePerson{
			key:       rec.PersonKey,
			display:   rec.DisplayName,
			forms:     normalizedForms(rec),
			nickForms: nicknameForms(rec),
			refs:      len(rec.EntryRefs),
			firstSeen: rec.FirstSeenAt.Format(personDateLayout),
		})
	}
	// ListPeopleKeys is already sorted; keep the slice in that order explicitly.
	slices.SortFunc(out, func(a, b reconcilePerson) int { return cmp.Compare(a.key, b.key) })
	return out, nil
}

// normalizedForms returns the sorted, unique set of normalized forms of a
// record — its display name and every aka — which are what the detector compares.
func normalizedForms(rec storage.PersonRecord) []string {
	seen := map[string]bool{}
	var forms []string
	for _, raw := range append([]string{rec.DisplayName}, rec.Aka...) {
		n := storage.NormalizeName(raw)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		forms = append(forms, n)
	}
	slices.Sort(forms)
	return forms
}

// nicknameForms returns the sorted, unique nickname lookup forms of a record:
// data.NicknameForm applied to its display name and every aka. These are the
// forms the curated nickname map is compared across — the normalized first
// whitespace-delimited component of each raw name — so "Michael Torres" reduces to
// "michael" and links to a bare "Mike", while a surname is never treated as a
// given name. The same helper feeds capture-time routing, keeping the two
// consumers in lockstep.
func nicknameForms(rec storage.PersonRecord) []string {
	seen := map[string]bool{}
	var forms []string
	for _, raw := range append([]string{rec.DisplayName}, rec.Aka...) {
		n := data.NicknameForm(raw)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		forms = append(forms, n)
	}
	slices.Sort(forms)
	return forms
}

// reconcileSignal reports whether two people are a likely duplicate and why.
// Every suggestion needs a corroborating signal, checked strongest-first: a
// shared normalized written form, a curated nickname link (Mike ~ Michael), or a
// diminutive prefix (sam ⊂ sammy). The bare edit-distance path was removed, so
// unrelated short names ("Braden"/"Brandon") are never paired, and entry
// co-occurrence is deliberately not a signal. The reason names the specific forms
// that fired so the output is self-explanatory and deterministic.
func reconcileSignal(a, b reconcilePerson, links map[string][]string) (string, bool) {
	if shared := firstShared(a.forms, b.forms); shared != "" {
		return fmt.Sprintf("share the written form %q", shared), true
	}
	if fa, fb := linkedNicknames(a.nickForms, b.nickForms, links); fa != "" {
		return fmt.Sprintf("linked nicknames (%q ~ %q)", fa, fb), true
	}
	for _, fa := range a.forms {
		for _, fb := range b.forms {
			if proximateForms(fa, fb) {
				return fmt.Sprintf("similar names (%q ~ %q)", fa, fb), true
			}
		}
	}
	return "", false
}

// firstShared returns the lexically smallest normalized form the two sorted
// form sets have in common, or "" when they are disjoint.
func firstShared(a, b []string) string {
	set := make(map[string]bool, len(a))
	for _, f := range a {
		set[f] = true
	}
	for _, f := range b { // b is sorted → the first hit is the smallest
		if set[f] {
			return f
		}
	}
	return ""
}

// linkedNicknames returns the first pair of nickname lookup forms — one from each
// person — that the curated map links, or "","" when none are linked. Both slices
// are sorted, so the chosen pair (and thus the reason string) is deterministic.
// The map is symmetric and carries no self-links, so scanning a's neighbors for
// each of b's forms is sufficient and never pairs two identical forms (that is the
// shared-form signal, handled earlier).
func linkedNicknames(a, b []string, links map[string][]string) (string, string) {
	for _, fa := range a {
		neighbors := links[fa]
		if len(neighbors) == 0 {
			continue
		}
		for _, fb := range b {
			if slices.Contains(neighbors, fb) {
				return fa, fb
			}
		}
	}
	return "", ""
}

// proximateForms reports whether two distinct normalized forms are close enough
// to suggest the same person via the diminutive-prefix rule (sam ⊂ sammy). The
// bare edit-distance signal was removed — it paired unrelated short names — so a
// strict, long-enough prefix is now the only proximity signal it fires on.
func proximateForms(a, b string) bool {
	if a == b {
		return false // an exact match is the shared-form signal, not proximity
	}
	return isDiminutive(a, b) || isDiminutive(b, a)
}

// isDiminutive reports whether short is a strict, long-enough prefix of long —
// the "sam" ⊂ "sammy" rule.
func isDiminutive(short, long string) bool {
	sl := utf8.RuneCountInString(short)
	return sl >= reconcileMinPrefixLen && sl < utf8.RuneCountInString(long) && strings.HasPrefix(long, short)
}

// buildCandidate orients a pair into a source→target suggestion: the record with
// fewer mentions folds into the one with more, breaking ties by later first_seen
// then larger key so the direction is fully deterministic.
func buildCandidate(a, b reconcilePerson, reason string) ReconcileCandidate {
	source, target := a, b
	if !mergesInto(a, b) {
		source, target = b, a
	}
	return ReconcileCandidate{
		SourceKey:  source.key,
		SourceName: source.display,
		TargetKey:  target.key,
		TargetName: target.display,
		Reason:     reason,
	}
}

// mergesInto reports whether a should fold into b: a has fewer mentions, or on a
// tie is the later-seen (then larger-keyed) record. The "less established" record
// becomes the source.
func mergesInto(a, b reconcilePerson) bool {
	if a.refs != b.refs {
		return a.refs < b.refs
	}
	if a.firstSeen != b.firstSeen {
		return a.firstSeen > b.firstSeen // later-seen folds into earlier
	}
	return a.key > b.key
}

// sortedPair returns the two keys in lexical order, for stable pair sorting.
func sortedPair(x, y string) (lo, hi string) {
	if x <= y {
		return x, y
	}
	return y, x
}

// renderReconcile builds the byte-stable prose: one honest line when nothing is
// found, otherwise a header plus two lines per candidate — the finding and the
// exact merge command to run.
func renderReconcile(cands []ReconcileCandidate) string {
	if len(cands) == 0 {
		return reconcileNoCandidates
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Found %d likely-duplicate pair%s — review each; nothing has changed:\n",
		len(cands), plural2(len(cands)))
	for _, c := range cands {
		fmt.Fprintf(&b, "- %q (%s) and %q (%s) — %s.\n",
			c.TargetName, c.TargetKey, c.SourceName, c.SourceKey, c.Reason)
		fmt.Fprintf(&b, "    → lucid person merge %s %s\n", c.SourceKey, c.TargetKey)
	}
	return strings.TrimRight(b.String(), "\n")
}

// plural2 returns the "s" plural suffix for a count.
func plural2(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
