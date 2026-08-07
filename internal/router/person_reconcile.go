package router

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/mrz1836/lucid/internal/storage"
)

// The fixed /person reconcile copy. None of it is agent-authored — reconcile is
// a deterministic, no-LLM detector (P9).
const reconcileNoCandidates = "No likely-duplicate people found."

// reconcile proximity thresholds (data-model.md §"Merge & redirect"; scope.md §4).
const (
	// reconcileMaxEditDistance bounds the Levenshtein signal.
	reconcileMaxEditDistance = 2
	// reconcileMinEditLen is the shortest form the edit-distance signal accepts,
	// so short names are not paired on coincidence.
	reconcileMinEditLen = 4
	// reconcileMinPrefixLen is the shortest diminutive prefix accepted (so "sam"
	// ⊂ "sammy" fires but a one- or two-letter prefix never does).
	reconcileMinPrefixLen = 3
)

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

	var cands []ReconcileCandidate
	for i := range people {
		for j := i + 1; j < len(people); j++ {
			if reason, ok := reconcileSignal(people[i], people[j]); ok {
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

// reconcileSignal reports whether two people are a likely duplicate and why. A
// shared normalized form is checked first (the strongest, cheapest signal), then
// name proximity. The reason names the specific forms that fired so the output
// is self-explanatory and deterministic.
func reconcileSignal(a, b reconcilePerson) (string, bool) {
	if shared := firstShared(a.forms, b.forms); shared != "" {
		return fmt.Sprintf("share the written form %q", shared), true
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

// proximateForms reports whether two distinct normalized forms are close enough
// to suggest the same person: a short diminutive prefix (sam ⊂ sammy), or a
// bounded edit distance between two forms both long enough to trust it.
func proximateForms(a, b string) bool {
	if a == b {
		return false // an exact match is the shared-form signal, not proximity
	}
	if isDiminutive(a, b) || isDiminutive(b, a) {
		return true
	}
	if utf8.RuneCountInString(a) >= reconcileMinEditLen &&
		utf8.RuneCountInString(b) >= reconcileMinEditLen &&
		levenshtein(a, b) <= reconcileMaxEditDistance {
		return true
	}
	return false
}

// isDiminutive reports whether short is a strict, long-enough prefix of long —
// the "sam" ⊂ "sammy" rule.
func isDiminutive(short, long string) bool {
	sl := utf8.RuneCountInString(short)
	return sl >= reconcileMinPrefixLen && sl < utf8.RuneCountInString(long) && strings.HasPrefix(long, short)
}

// levenshtein returns the rune-level edit distance between a and b — a small,
// pure helper (no dependency exists). It is used only for the bounded proximity
// signal, so its cost is fine at the short lengths of normalized names.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		cur := make([]int, lb+1)
		cur[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[lb]
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
