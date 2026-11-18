package router

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/mrz1836/lucid/data"
	"github.com/mrz1836/lucid/internal/identity"
	"github.com/mrz1836/lucid/internal/storage"
)

// The fixed /person reconcile copy. None of it is agent-authored — reconcile is
// a deterministic, no-LLM detector (P9). The section headers and lines below
// are documented verbatim in docs/usage/commands.md §"person reconcile".
const (
	reconcileNoCandidates  = "No likely-duplicate people found."
	reconcileHeldHeader    = "Held mentions — counted for no one until you settle each:"
	reconcileOverlapHeader = "First-name overlaps — a bare name goes to one person while another shares that first name:"
	reconcileDormantHeader = "Dormant look-alikes — an active person shares a written form with a dormant one:"
)

// reconcileMinPrefixLen is the shortest diminutive prefix accepted (so "sam" ⊂
// "sammy" fires but a one- or two-letter prefix never does). The bare
// edit-distance signal was removed — it paired unrelated short names like
// "Braden"/"Brandon" and "Dana"/"Dania" — so every suggestion now needs a
// corroborating signal: a shared written form, a curated nickname link, or this
// diminutive prefix (data-model.md §"Alias-aware person resolution"; scope.md §4).
const reconcileMinPrefixLen = 3

// PersonReconcileRequest selects what `lucid person reconcile` scans. All
// brings dormant people back into the merge suggestions, each marked dormant,
// and attaches a dormant look-alike to its pair. Off-limits people stay out of
// every line either way.
type PersonReconcileRequest struct {
	All bool
}

// ReconcileCandidate is one suggested duplicate pair: the source (fold away) and
// target (canonical) records, why they were paired, and the exact merge that
// would collapse them. Direction is deterministic — the record with fewer
// mentions folds into the one with more (ties: later first_seen, then key).
// SourceDormant/TargetDormant mark a dormant person in a pair (only under
// --all). Overlap and DormantShare carry the identity finding attached to the
// pair, when there is one.
type ReconcileCandidate struct {
	SourceKey     string
	SourceName    string
	SourceDormant bool
	TargetKey     string
	TargetName    string
	TargetDormant bool
	Reason        string
	Overlap       *ReconcileOverlap
	DormantShare  *ReconcileDormantShare
}

// SuggestedMerge is the exact command that folds the pair's source into its
// target.
func (c ReconcileCandidate) SuggestedMerge() string {
	return fmt.Sprintf("lucid person merge %s %s", c.SourceKey, c.TargetKey)
}

// ReconcileOverlap is one first-name overlap (R2): an unclaimed one-word form
// exactly one active person holds (the holder — a bare mention goes to them),
// while another active person (the sharer) has that form as their first name.
// It is advice, never a hold.
type ReconcileOverlap struct {
	// Form is the form as the holder writes it.
	Form string
	// HolderKey/HolderName are the person a bare Form goes to now, and
	// HolderFirstSeen is their first-seen date (YYYY-MM-DD).
	HolderKey       string
	HolderName      string
	HolderFirstSeen string
	// SharerKey/SharerName are the other active person whose first name is Form.
	SharerKey  string
	SharerName string
}

// SuggestedClaim is the fix when the sharer is who the bare form should mean.
func (o ReconcileOverlap) SuggestedClaim() string {
	return fmt.Sprintf("lucid person claim %s %q", o.SharerKey, o.Form)
}

// SuggestedDormant is the fix when the holder is no longer who the bare form
// should mean.
func (o ReconcileOverlap) SuggestedDormant() string {
	return "lucid person dormant " + o.HolderKey
}

// ReconcileDormantShare is one dormant look-alike (R6): an active person who
// shares an exact normalized written form with a dormant one — often the same
// person mentioned again after the old record went dormant.
type ReconcileDormantShare struct {
	// Form is the lexically smallest normalized form the two share.
	Form        string
	ActiveKey   string
	ActiveName  string
	DormantKey  string
	DormantName string
}

// SuggestedRestore is the first step of the fix: make the dormant record
// active again.
func (d ReconcileDormantShare) SuggestedRestore() string {
	return fmt.Sprintf("lucid person dormant %s --restore", d.DormantKey)
}

// SuggestedMerge is the second step: fold the new record into the old one.
func (d ReconcileDormantShare) SuggestedMerge() string {
	return fmt.Sprintf("lucid person merge %s %s", d.ActiveKey, d.DormantKey)
}

// ReconcileHeld is one pending held mention (§P-11): a raw entry's mention
// capture could not assign, with the people it could mean — active first,
// then dormant, each by key.
type ReconcileHeld struct {
	RawID      string
	Form       string
	Reason     string
	Candidates []ReconcileHeldCandidate
}

// ReconcileHeldCandidate is one person a held mention could mean.
type ReconcileHeldCandidate struct {
	PersonKey   string
	DisplayName string
	Dormant     bool
}

// PersonReconcileResult reports the reconcile scan. Candidates is always a
// (possibly empty) slice — an honest empty result, never an error. Held,
// Overlaps (the standalone first-name overlaps — an attached one rides its
// pair), and DormantShares (likewise standalone) are empty when there is
// nothing to list. Text is the byte-stable rendered output.
type PersonReconcileResult struct {
	Candidates    []ReconcileCandidate
	Held          []ReconcileHeld
	Overlaps      []ReconcileOverlap
	DormantShares []ReconcileDormantShare
	Text          string
}

// reconcilePerson is one live canonical record reduced to what the detector
// needs: its key, display name, normalized forms, the tie-break inputs, and
// whether the person is dormant or off-limits.
type reconcilePerson struct {
	key       string
	display   string
	forms     []string // sorted, unique normalized display + aka forms
	nickForms []string // sorted, unique nickname lookup forms (data.NicknameForm of display + aka)
	refs      int
	firstSeen string // YYYY-MM-DD, for the deterministic tie-break and the overlap line
	rec       storage.PersonRecord
	dormant   bool
	offLimits bool
}

// reconcileScan is everything one reconcile pass reads: every live canonical
// person (dormant and off-limits ones included, flagged) sorted by key, and
// the folded identity log.
type reconcileScan struct {
	people []reconcilePerson
	byKey  map[string]reconcilePerson
	state  identity.State
}

// PersonReconcile executes `lucid person reconcile`: a deterministic, no-LLM
// detector that lists likely-duplicate people so the user can `person merge`
// them (scope.md §4; data-model.md §"Merge & redirect"), followed by the
// identity loose ends — held mentions, first-name overlaps, and dormant
// look-alikes — each with its one-command fix (data-model.md §"Status, claims
// and move-ref"). It never writes and never calls a model. Off-limits people
// are excluded from every line, dormant people join the merge suggestions only
// under req.All, redirect tombstones are skipped, and the output is
// byte-stable across repeated runs on the same store (S-22). Finding nothing
// is an honest empty result, not an error. A Ledger with no identity log
// prints exactly what reconcile always has, apart from first-name overlaps.
func (r *Router) PersonReconcile(req PersonReconcileRequest) (PersonReconcileResult, error) {
	scan, err := r.reconcileScan()
	if err != nil {
		return PersonReconcileResult{}, err
	}

	cands := reconcilePairs(scan.people, req.All)
	overlaps := attachOverlaps(cands, reconcileOverlaps(scan))
	shares := reconcileDormantShares(scan.people)
	if req.All {
		shares = attachDormantShares(cands, shares)
	}

	res := PersonReconcileResult{
		Candidates:    cands,
		Held:          reconcileHeldMentions(scan),
		Overlaps:      overlaps,
		DormantShares: shares,
	}
	res.Text = renderReconcile(res)
	return res, nil
}

// reconcilePairs runs the pairwise duplicate detector over the people eligible
// for a merge suggestion — never an off-limits person, and a dormant one only
// when all is set — sorted by the unordered pair.
func reconcilePairs(people []reconcilePerson, all bool) []ReconcileCandidate {
	eligible := make([]reconcilePerson, 0, len(people))
	for _, p := range people {
		if p.offLimits || (p.dormant && !all) {
			continue
		}
		eligible = append(eligible, p)
	}

	// The curated nickname map is the same for every pair, so parse it once for
	// the whole scan and thread it through reconcileSignal.
	links := data.NicknameLinks()

	var cands []ReconcileCandidate
	for i := range eligible {
		for j := i + 1; j < len(eligible); j++ {
			if reason, ok := reconcileSignal(eligible[i], eligible[j], links); ok {
				cands = append(cands, buildCandidate(eligible[i], eligible[j], reason))
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
	return cands
}

// reconcileScan loads every live canonical person (tombstones skipped;
// dormant and off-limits people kept and flagged), reduced to the detector's
// inputs and sorted by key so every scan — and its output — is deterministic,
// plus the folded identity log. A Ledger without the log reads as everyone
// active.
func (r *Router) reconcileScan() (reconcileScan, error) {
	keys, err := r.store.ListPeopleKeys()
	if err != nil {
		return reconcileScan{}, fmt.Errorf("reconcile: list people: %w", err)
	}
	offLimits, err := r.store.ReadOffLimitsPersonKeys()
	if err != nil {
		return reconcileScan{}, fmt.Errorf("reconcile: read off-limits: %w", err)
	}
	deny := toSet(offLimits)
	st, err := r.store.IdentityState()
	if err != nil {
		return reconcileScan{}, fmt.Errorf("reconcile: read identity log: %w", err)
	}
	// Mention counts are references on read: entry_refs with every pin and
	// move-ref folded in (data-model.md §"Attribution on read").
	fold, err := r.store.MentionFold()
	if err != nil {
		return reconcileScan{}, fmt.Errorf("reconcile: fold mentions: %w", err)
	}

	out := make([]reconcilePerson, 0, len(keys))
	for _, key := range keys {
		rec, found, err := r.store.ReadPerson(key)
		if err != nil {
			return reconcileScan{}, fmt.Errorf("reconcile: read %q: %w", key, err)
		}
		if !found || rec.IsTombstone() {
			continue
		}
		out = append(out, reconcilePerson{
			key:       rec.PersonKey,
			display:   rec.DisplayName,
			forms:     normalizedForms(rec),
			nickForms: nicknameForms(rec),
			refs:      len(fold.EntryRefs(rec)),
			firstSeen: rec.FirstSeenAt.Format(personDateLayout),
			rec:       rec,
			dormant:   st.IsDormant(rec.PersonKey),
			offLimits: deny[rec.PersonKey],
		})
	}
	// ListPeopleKeys is already sorted; keep the slice in that order explicitly.
	slices.SortFunc(out, func(a, b reconcilePerson) int { return cmp.Compare(a.key, b.key) })
	byKey := make(map[string]reconcilePerson, len(out))
	for _, p := range out {
		byKey[p.key] = p
	}
	return reconcileScan{people: out, byKey: byKey, state: st}, nil
}

// reconcileHeldMentions lists every pending held mention (§P-11) in (raw_id,
// form) order, its candidates read through the merge redirects, active first
// then dormant, each by key. An off-limits candidate — or one whose record is
// gone — is left out, and a held mention with no candidate left to name is not
// listed.
func reconcileHeldMentions(scan reconcileScan) []ReconcileHeld {
	var out []ReconcileHeld
	for _, h := range scan.state.PendingHolds() {
		var active, dormant []ReconcileHeldCandidate
		for _, key := range h.Candidates {
			p, ok := scan.byKey[key]
			if !ok || p.offLimits {
				continue
			}
			c := ReconcileHeldCandidate{PersonKey: p.key, DisplayName: p.display, Dormant: p.dormant}
			if p.dormant {
				dormant = append(dormant, c)
			} else {
				active = append(active, c)
			}
		}
		if len(active)+len(dormant) == 0 {
			continue
		}
		out = append(out, ReconcileHeld{
			RawID:      h.RawID,
			Form:       h.Form,
			Reason:     h.Reason,
			Candidates: append(active, dormant...),
		})
	}
	return out
}

// reconcileOverlaps finds every first-name overlap (R2): an unclaimed one-word
// form exactly one active person holds — as their display_name or an aka form,
// the /person exact-holder rule — while another active person has that form
// as their first name (data.NicknameForm of their display_name, the
// first-name rule capture uses). A form no one holds exactly is never listed,
// a claimed form or a dormant person drops the line, and an off-limits holder
// or sharer is never named. Sorted by form, then holder key, then sharer key.
func reconcileOverlaps(scan reconcileScan) []ReconcileOverlap {
	var active []reconcilePerson
	firstNames := map[string][]reconcilePerson{}
	for _, p := range scan.people {
		if p.dormant {
			continue
		}
		active = append(active, p)
		if first := data.NicknameForm(p.display); first != "" {
			firstNames[first] = append(firstNames[first], p)
		}
	}

	var out []ReconcileOverlap
	seen := map[string]bool{}
	for _, p := range active {
		for _, raw := range append([]string{p.rec.DisplayName}, p.rec.Aka...) {
			form := strings.TrimSpace(raw)
			fold := strings.ToLower(form)
			if len(strings.Fields(form)) != 1 || seen[fold] {
				continue
			}
			seen[fold] = true
			out = append(out, formOverlaps(scan.state, active, firstNames[data.NicknameForm(form)], form)...)
		}
	}
	slices.SortFunc(out, func(a, b ReconcileOverlap) int {
		return cmp.Or(
			cmp.Compare(identity.NormalizeForm(a.Form), identity.NormalizeForm(b.Form)),
			cmp.Compare(a.Form, b.Form),
			cmp.Compare(a.HolderKey, b.HolderKey),
			cmp.Compare(a.SharerKey, b.SharerKey),
		)
	})
	return out
}

// formOverlaps returns the overlaps of one one-word form: none when the form
// is claimed by an active claimant, when no one else has it as a first name,
// or when it has anything but exactly one active exact holder; otherwise one
// per sharer, skipping an off-limits holder or sharer.
func formOverlaps(st identity.State, active, sharers []reconcilePerson, form string) []ReconcileOverlap {
	if _, claimed := st.ActiveClaimant(form); claimed || len(sharers) == 0 {
		return nil
	}
	var holders []reconcilePerson
	for _, p := range active {
		if holdsFormExactly(p.rec, form) {
			holders = append(holders, p)
		}
	}
	if len(holders) != 1 || holders[0].offLimits {
		return nil
	}
	holder := holders[0]
	var out []ReconcileOverlap
	for _, s := range sharers {
		if s.key == holder.key || s.offLimits {
			continue
		}
		out = append(out, ReconcileOverlap{
			Form:            form,
			HolderKey:       holder.key,
			HolderName:      holder.display,
			HolderFirstSeen: holder.firstSeen,
			SharerKey:       s.key,
			SharerName:      s.display,
		})
	}
	return out
}

// reconcileDormantShares finds every dormant look-alike (R6): an active person
// who shares an exact normalized written form with a dormant one, naming the
// lexically smallest shared form. Off-limits people are never named. Sorted by
// the active key, then the dormant key.
func reconcileDormantShares(people []reconcilePerson) []ReconcileDormantShare {
	var out []ReconcileDormantShare
	for _, a := range people {
		if a.dormant || a.offLimits {
			continue
		}
		for _, d := range people {
			if !d.dormant || d.offLimits {
				continue
			}
			if shared := firstShared(a.forms, d.forms); shared != "" {
				out = append(out, ReconcileDormantShare{
					Form:        shared,
					ActiveKey:   a.key,
					ActiveName:  a.display,
					DormantKey:  d.key,
					DormantName: d.display,
				})
			}
		}
	}
	return out
}

// attachOverlaps attaches each overlap, in order, to the merge suggestion for
// the same two people when that pair has no overlap yet, and returns the rest —
// the standalone overlaps.
func attachOverlaps(cands []ReconcileCandidate, overlaps []ReconcileOverlap) []ReconcileOverlap {
	var rest []ReconcileOverlap
	for _, o := range overlaps {
		if i := pairIndex(cands, o.HolderKey, o.SharerKey); i >= 0 && cands[i].Overlap == nil {
			cands[i].Overlap = &o
			continue
		}
		rest = append(rest, o)
	}
	return rest
}

// attachDormantShares attaches each dormant look-alike, in order, to the merge
// suggestion for the same two people (only under --all, when dormant people
// join the pairs), and returns the rest.
func attachDormantShares(cands []ReconcileCandidate, shares []ReconcileDormantShare) []ReconcileDormantShare {
	var rest []ReconcileDormantShare
	for _, d := range shares {
		if i := pairIndex(cands, d.ActiveKey, d.DormantKey); i >= 0 && cands[i].DormantShare == nil {
			cands[i].DormantShare = &d
			continue
		}
		rest = append(rest, d)
	}
	return rest
}

// pairIndex returns the index of the merge suggestion for the unordered key
// pair (x, y), or -1 when the two are not suggested.
func pairIndex(cands []ReconcileCandidate, x, y string) int {
	return slices.IndexFunc(cands, func(c ReconcileCandidate) bool {
		return (c.SourceKey == x && c.TargetKey == y) || (c.SourceKey == y && c.TargetKey == x)
	})
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
		SourceKey:     source.key,
		SourceName:    source.display,
		SourceDormant: source.dormant,
		TargetKey:     target.key,
		TargetName:    target.display,
		TargetDormant: target.dormant,
		Reason:        reason,
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

// renderReconcile builds the byte-stable prose. The pairs come first: one
// honest line when there are none, otherwise a header plus two lines per
// candidate — the finding and the exact merge command — and any identity
// finding attached to the pair. Then, each only when there is something to
// list and never separated by a blank line: held mentions, standalone
// first-name overlaps, and standalone dormant look-alikes. A Ledger with
// nothing beyond pairs prints exactly what reconcile always has.
func renderReconcile(res PersonReconcileResult) string {
	var b strings.Builder
	renderReconcilePairs(&b, res.Candidates)
	if len(res.Held) > 0 {
		b.WriteString(reconcileHeldHeader + "\n")
		for _, h := range res.Held {
			renderHeldMention(&b, h)
		}
	}
	if len(res.Overlaps) > 0 {
		b.WriteString(reconcileOverlapHeader + "\n")
		for _, o := range res.Overlaps {
			fmt.Fprintf(&b, "- A bare %q goes to %q (%s, first seen %s), and %q (%s) also has the first name %q.\n",
				o.Form, o.HolderName, o.HolderKey, o.HolderFirstSeen, o.SharerName, o.SharerKey, o.Form)
			renderOverlapFixes(&b, o)
		}
	}
	if len(res.DormantShares) > 0 {
		b.WriteString(reconcileDormantHeader + "\n")
		for _, d := range res.DormantShares {
			fmt.Fprintf(&b, "- %q (%s) shares the written form %q with the dormant %q (%s). If they are the same person:\n",
				d.ActiveName, d.ActiveKey, d.Form, d.DormantName, d.DormantKey)
			fmt.Fprintf(&b, "    → %s\n", d.SuggestedRestore())
			fmt.Fprintf(&b, "    → %s\n", d.SuggestedMerge())
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderReconcilePairs writes the merge suggestions — or the honest empty line
// — with any overlap or dormant look-alike attached to a pair.
func renderReconcilePairs(b *strings.Builder, cands []ReconcileCandidate) {
	if len(cands) == 0 {
		b.WriteString(reconcileNoCandidates + "\n")
		return
	}
	fmt.Fprintf(b, "Found %d likely-duplicate pair%s — review each; nothing has changed:\n",
		len(cands), plural2(len(cands)))
	for _, c := range cands {
		fmt.Fprintf(b, "- %q (%s) and %q (%s) — %s.\n",
			c.TargetName, reconcileKeyLabel(c.TargetKey, c.TargetDormant),
			c.SourceName, reconcileKeyLabel(c.SourceKey, c.SourceDormant), c.Reason)
		fmt.Fprintf(b, "    → %s\n", c.SuggestedMerge())
		if o := c.Overlap; o != nil {
			fmt.Fprintf(b, "  Or keep them apart: a bare %q goes to %q (%s, first seen %s), and %q (%s) also has the first name %q.\n",
				o.Form, o.HolderName, o.HolderKey, o.HolderFirstSeen, o.SharerName, o.SharerKey, o.Form)
			renderOverlapFixes(b, *o)
		}
		if d := c.DormantShare; d != nil {
			fmt.Fprintf(b, "  %q (%s) is dormant — restore them before merging:\n", d.DormantName, d.DormantKey)
			fmt.Fprintf(b, "    → %s\n", d.SuggestedRestore())
		}
	}
}

// renderOverlapFixes writes an overlap's two one-command fixes: claim the form
// for the sharer, or make the holder dormant.
func renderOverlapFixes(b *strings.Builder, o ReconcileOverlap) {
	fmt.Fprintf(b, "    → %s\n", o.SuggestedClaim())
	fmt.Fprintf(b, "    → %s\n", o.SuggestedDormant())
}

// renderHeldMention writes one held mention — its raw id, the form, the people
// it could mean (a dormant one marked) — and its three fixes, where <key> is the
// candidate the user means.
func renderHeldMention(b *strings.Builder, h ReconcileHeld) {
	names := make([]string, 0, len(h.Candidates))
	for _, c := range h.Candidates {
		names = append(names, fmt.Sprintf("%q (%s)", c.DisplayName, reconcileKeyLabel(c.PersonKey, c.Dormant)))
	}
	fmt.Fprintf(b, "- %s: %q could mean %s.\n", h.RawID, h.Form, strings.Join(names, " / "))
	fmt.Fprintf(b, "    → lucid person claim <key> %q\n", h.Form)
	b.WriteString("    → lucid person dormant <key>\n")
	fmt.Fprintf(b, "    → lucid person move-ref %s --to <key>\n", h.RawID)
}

// reconcileKeyLabel renders a person_key for a parenthetical, marking a
// dormant person — `person_c-wave, dormant`.
func reconcileKeyLabel(key string, dormant bool) string {
	if dormant {
		return key + ", dormant"
	}
	return key
}

// plural2 returns the "s" plural suffix for a count.
func plural2(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
