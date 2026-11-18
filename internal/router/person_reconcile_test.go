package router

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/data"
	"github.com/mrz1836/lucid/internal/identity"
)

// TestPersonReconcile_TwoSignals seeds a nickname-map pair (Sam/Sammy, both in the
// "samuel" cluster) and a shared-form pair (Alex/Andy share "A.") and confirms
// both surface, with a deterministic direction and byte-stable output across two
// runs.
func TestPersonReconcile_TwoSignals(t *testing.T) {
	r, _, home := newBootedRouter(t)
	day1 := personSeedTime()
	day2 := personSeedTime().Add(24 * time.Hour)
	writePersonFile(t, home, "person_s-sam", "Sam", []string{"Sam"}, day1)
	writePersonFile(t, home, "person_s-sammy", "Sammy", []string{"Sammy"}, day2)
	writePersonFile(t, home, "person_a-alex", "Alex", []string{"A."}, day1)
	writePersonFile(t, home, "person_a-andy", "Andy", []string{"A."}, day2)

	res, err := r.PersonReconcile(PersonReconcileRequest{})
	require.NoError(t, err)
	require.Len(t, res.Candidates, 2)

	// Sorted by the unordered pair (person_a-* before person_s-*).
	alexAndy := res.Candidates[0]
	assert.Equal(t, "person_a-alex", alexAndy.TargetKey)
	assert.Equal(t, "person_a-andy", alexAndy.SourceKey, "the later-seen record folds into the earlier")
	assert.Contains(t, alexAndy.Reason, "share the written form")

	samSammy := res.Candidates[1]
	assert.Equal(t, "person_s-sam", samSammy.TargetKey)
	assert.Equal(t, "person_s-sammy", samSammy.SourceKey)
	assert.Contains(t, samSammy.Reason, "linked nicknames")

	// Byte-stable across repeated runs (S-22).
	again, err := r.PersonReconcile(PersonReconcileRequest{})
	require.NoError(t, err)
	assert.Equal(t, res.Text, again.Text)
}

// TestPersonReconcile_ExcludesOffLimits proves an off-limits person is never
// surfaced as a reconcile candidate.
func TestPersonReconcile_ExcludesOffLimits(t *testing.T) {
	r, a, home := newBootedRouter(t)
	writePersonFile(t, home, "person_s-sam", "Sam", []string{"Sam"}, personSeedTime())
	writePersonFile(t, home, "person_s-sammy", "Sammy", []string{"Sammy"}, personSeedTime())
	require.NoError(t, a.WriteOffLimitsPersonKeys([]string{"person_s-sammy"}))

	res, err := r.PersonReconcile(PersonReconcileRequest{})
	require.NoError(t, err)
	assert.Empty(t, res.Candidates)
	assert.Equal(t, reconcileNoCandidates, res.Text)
}

// TestPersonReconcile_CoOccurringNotPaired proves co-occurrence (shared
// entry_refs) is *not* a signal: two dissimilar people seen in the same entry
// are not paired.
func TestPersonReconcile_CoOccurringNotPaired(t *testing.T) {
	r, _, home := newBootedRouter(t)
	// writePersonFile gives every record the same entry_ref, so these two share
	// an entry — yet their names are unrelated.
	writePersonFile(t, home, "person_a-alex", "Alex", []string{"Alex"}, personSeedTime())
	writePersonFile(t, home, "person_b-bob", "Bob", []string{"Bob"}, personSeedTime())

	res, err := r.PersonReconcile(PersonReconcileRequest{})
	require.NoError(t, err)
	assert.Empty(t, res.Candidates, "people seen together are usually different people")
}

// TestPersonReconcile_EmptyAndTombstones proves an empty store is an honest empty
// result and a tombstone is never a candidate.
func TestPersonReconcile_EmptyAndTombstones(t *testing.T) {
	r, _, home := newBootedRouter(t)

	res, err := r.PersonReconcile(PersonReconcileRequest{})
	require.NoError(t, err)
	assert.Empty(t, res.Candidates)
	assert.Equal(t, reconcileNoCandidates, res.Text)

	// A canonical Sam plus a tombstone at a Sam-like slug: the tombstone is
	// skipped, so no pair is produced.
	writePersonFile(t, home, "person_s-sam", "Sam", []string{"Sam"}, personSeedTime())
	writeTombstone(t, home, "person_s-sammy", "Sammy", "person_s-sam", personSeedTime())

	res, err = r.PersonReconcile(PersonReconcileRequest{})
	require.NoError(t, err)
	assert.Empty(t, res.Candidates, "a tombstone is never a reconcile candidate")
}

// TestReconcileMergesInto covers the deterministic direction: fewer mentions
// fold in first, then later first-seen, then larger key.
func TestReconcileMergesInto(t *testing.T) {
	// Fewer refs → source.
	assert.True(t, mergesInto(
		reconcilePerson{key: "person_a", refs: 1},
		reconcilePerson{key: "person_b", refs: 5},
	))
	assert.False(t, mergesInto(
		reconcilePerson{key: "person_a", refs: 5},
		reconcilePerson{key: "person_b", refs: 1},
	))
	// Tie on refs → later first_seen folds into earlier.
	assert.True(t, mergesInto(
		reconcilePerson{key: "person_a", refs: 2, firstSeen: "2026-05-09"},
		reconcilePerson{key: "person_b", refs: 2, firstSeen: "2026-05-01"},
	))
	// Tie on refs and first_seen → larger key is the source.
	assert.True(t, mergesInto(
		reconcilePerson{key: "person_z", refs: 2, firstSeen: "2026-05-01"},
		reconcilePerson{key: "person_a", refs: 2, firstSeen: "2026-05-01"},
	))
}

// TestProximateForms covers the sole remaining proximity signal — the diminutive
// prefix — and proves the removed edit-distance path no longer fires.
func TestProximateForms(t *testing.T) {
	// Diminutive prefix (>= 3 runes, strict) still fires.
	assert.True(t, proximateForms("sam", "sammy"))
	assert.True(t, proximateForms("sammy", "sam"))
	assert.False(t, proximateForms("al", "alex"), "a 2-char prefix is too short")
	// The bare edit-distance signal was removed: names close in edit distance but
	// not in a strict-prefix relationship no longer pair.
	assert.False(t, proximateForms("braden", "brandon"), "unrelated short names are not paired")
	assert.False(t, proximateForms("dana", "dania"), "a one-edit short pair is not a prefix")
	assert.False(t, proximateForms("sam", "pam"), "a substitution is not a prefix")
	// Exact match is the shared signal, not proximity.
	assert.False(t, proximateForms("alex", "alex"))
	// Far apart.
	assert.False(t, proximateForms("alexandra", "beatrice"))
}

// TestReconcileShortNameNoFalsePositive proves the precision goal: edit-distance-
// close short names and pure co-occurrence are no longer signals, so the synthetic
// collisions "Braden"/"Brandon" and "Dana"/"Dania" — and two unrelated people seen
// in the same entry — are never flagged.
func TestReconcileShortNameNoFalsePositive(t *testing.T) {
	r, _, home := newBootedRouter(t)
	day := personSeedTime()
	// Two edit-distance-close but genuinely different short given names.
	writePersonFile(t, home, "person_b-braden", "Braden", []string{"Braden"}, day)
	writePersonFile(t, home, "person_b-brandon", "Brandon", []string{"Brandon"}, day)
	// A one-edit short pair.
	writePersonFile(t, home, "person_d-dana", "Dana", []string{"Dana"}, day)
	writePersonFile(t, home, "person_d-dania", "Dania", []string{"Dania"}, day)
	// Two people who co-occur (writePersonFile shares one entry_ref) but whose
	// names give no signal — co-occurrence must not pair them.
	writePersonFile(t, home, "person_q-quinn", "Quinn", []string{"Quinn"}, day)
	writePersonFile(t, home, "person_z-zachary", "Zachary", []string{"Zachary"}, day)

	res, err := r.PersonReconcile(PersonReconcileRequest{})
	require.NoError(t, err)
	assert.Empty(t, res.Candidates, "edit-distance-close short names and co-occurrence are not signals")
	assert.Equal(t, reconcileNoCandidates, res.Text)
}

// TestReconcileCatchesRealDuplicates proves the three corroborating signals still
// catch real duplicates: a shared written form, a nickname-map hit (via the
// first-component rule, "Mike" ~ "Michael Torres"), and a diminutive prefix.
func TestReconcileCatchesRealDuplicates(t *testing.T) {
	r, _, home := newBootedRouter(t)
	day := personSeedTime()
	// (1) Shared written form: both records carry the aka "Olivia Grant".
	writePersonFile(t, home, "person_o-olivia", "Olivia Grant", []string{"Olivia Grant"}, day)
	writePersonFile(t, home, "person_o-liv", "Liv", []string{"Olivia Grant"}, day)
	// (2) Nickname-map hit via the first-component rule: "Mike" ~ "Michael Torres".
	writePersonFile(t, home, "person_m-mike", "Mike", []string{"Mike"}, day)
	writePersonFile(t, home, "person_m-michael", "Michael Torres", []string{"Michael Torres"}, day)
	// (3) Diminutive prefix, not in the nickname map: "Steph" ⊂ "Stephanie".
	writePersonFile(t, home, "person_s-steph", "Steph", []string{"Steph"}, day)
	writePersonFile(t, home, "person_s-stephanie", "Stephanie", []string{"Stephanie"}, day)

	res, err := r.PersonReconcile(PersonReconcileRequest{})
	require.NoError(t, err)

	shared, ok := candidateFor(res.Candidates, "person_o-olivia", "person_o-liv")
	require.True(t, ok, "a shared written form is a real duplicate")
	assert.Contains(t, shared.Reason, "share the written form")

	nick, ok := candidateFor(res.Candidates, "person_m-mike", "person_m-michael")
	require.True(t, ok, "a nickname-map hit is a real duplicate")
	assert.Contains(t, nick.Reason, "linked nicknames")

	dim, ok := candidateFor(res.Candidates, "person_s-steph", "person_s-stephanie")
	require.True(t, ok, "a diminutive prefix is a real duplicate")
	assert.Contains(t, dim.Reason, "similar names")

	assert.Len(t, res.Candidates, 3, "exactly the three real duplicates, no extras")
}

// TestReconcileReadOnly proves reconcile never writes: the store is byte-identical
// after a scan that surfaces a real duplicate, so nothing is auto-merged.
func TestReconcileReadOnly(t *testing.T) {
	r, _, home := newBootedRouter(t)
	day := personSeedTime()
	// A real duplicate so the scan does actual work.
	writePersonFile(t, home, "person_m-mike", "Mike", []string{"Mike"}, day)
	writePersonFile(t, home, "person_m-michael", "Michael Torres", []string{"Michael Torres"}, day)

	before := snapshotTree(t, home)

	res, err := r.PersonReconcile(PersonReconcileRequest{})
	require.NoError(t, err)
	require.NotEmpty(t, res.Candidates, "the fixture has a real duplicate to surface")

	after := snapshotTree(t, home)
	assert.Equal(t, before, after, "reconcile is read-only — nothing is merged or written")
}

// TestReconcilePrecision is the table-driven umbrella over reconcileSignal: the
// three corroborating signals fire, while edit-distance-only pairs and pure
// co-occurrence never do.
func TestReconcilePrecision(t *testing.T) {
	links := data.NicknameLinks()
	cases := []struct {
		name      string
		a, b      reconcilePerson
		wantFire  bool
		reasonSub string
	}{
		{
			name:      "shared written form fires",
			a:         reconcilePerson{forms: []string{"sarahkim"}, nickForms: []string{"sarah"}},
			b:         reconcilePerson{forms: []string{"sarahkim"}, nickForms: []string{"sarah"}},
			wantFire:  true,
			reasonSub: "share the written form",
		},
		{
			name:      "nickname-map link fires",
			a:         reconcilePerson{forms: []string{"mike"}, nickForms: []string{"mike"}},
			b:         reconcilePerson{forms: []string{"michaeltorres"}, nickForms: []string{"michael"}},
			wantFire:  true,
			reasonSub: "linked nicknames",
		},
		{
			name:      "diminutive prefix fires",
			a:         reconcilePerson{forms: []string{"steph"}, nickForms: []string{"steph"}},
			b:         reconcilePerson{forms: []string{"stephanie"}, nickForms: []string{"stephanie"}},
			wantFire:  true,
			reasonSub: "similar names",
		},
		{
			name:     "edit-distance-only short names do not fire (braden/brandon)",
			a:        reconcilePerson{forms: []string{"braden"}, nickForms: []string{"braden"}},
			b:        reconcilePerson{forms: []string{"brandon"}, nickForms: []string{"brandon"}},
			wantFire: false,
		},
		{
			name:     "one-edit short names do not fire (dana/dania)",
			a:        reconcilePerson{forms: []string{"dana"}, nickForms: []string{"dana"}},
			b:        reconcilePerson{forms: []string{"dania"}, nickForms: []string{"dania"}},
			wantFire: false,
		},
		{
			name:     "unrelated co-occurring names do not fire",
			a:        reconcilePerson{forms: []string{"quinn"}, nickForms: []string{"quinn"}},
			b:        reconcilePerson{forms: []string{"zachary"}, nickForms: []string{"zachary"}},
			wantFire: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason, ok := reconcileSignal(tc.a, tc.b, links)
			assert.Equal(t, tc.wantFire, ok)
			if tc.wantFire {
				assert.Contains(t, reason, tc.reasonSub)
			} else {
				assert.Empty(t, reason)
			}
		})
	}
}

// candidateFor returns the candidate for the unordered key pair (k1, k2), or false
// when the pair was not flagged.
func candidateFor(cands []ReconcileCandidate, k1, k2 string) (ReconcileCandidate, bool) {
	for _, c := range cands {
		if (c.SourceKey == k1 && c.TargetKey == k2) || (c.SourceKey == k2 && c.TargetKey == k1) {
			return c, true
		}
	}
	return ReconcileCandidate{}, false
}

// snapshotTree reads every regular file under root into a rel-path→contents map,
// so a before/after comparison proves a read-only operation touched nothing.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		out[rel] = string(b)
		return nil
	})
	require.NoError(t, err)
	return out
}

// The identity reconcile fixtures use synthetic people only: two people named
// "Sam" ("Sam" and "Sam Rivera"), plus "Sammy", "Jo", "Jo Lark", "Jordan Lee",
// "Alex", "Alex Kim", and "Alex Stone".

// reconcileFixture is a booted router over hand-written people records, with
// the identity verbs on a clock that ticks forward so events fold in the order
// a test writes them.
type reconcileFixture struct {
	t    *testing.T
	r    *Router
	home string
	tick int
}

// newReconcileFixture returns an empty booted Ledger.
func newReconcileFixture(t *testing.T) *reconcileFixture {
	t.Helper()
	r, _, home := newBootedRouter(t)
	return &reconcileFixture{t: t, r: r, home: home}
}

// person writes one canonical record first seen on the given day of May 2026.
func (f *reconcileFixture) person(key, display string, day int, aka ...string) {
	f.t.Helper()
	writePersonFile(f.t, f.home, key, display, append([]string{display}, aka...),
		time.Date(2026, time.May, day, 19, 42, 11, 0, time.UTC))
}

// now returns the next verb instant, one minute after the last.
func (f *reconcileFixture) now() time.Time {
	f.tick++
	return time.Date(2026, time.November, 17, 9, 0, 0, 0, time.UTC).Add(time.Duration(f.tick) * time.Minute)
}

func (f *reconcileFixture) dormant(key string) {
	f.t.Helper()
	_, err := f.r.PersonDormant(PersonDormantRequest{Subject: key, Now: f.now()})
	require.NoError(f.t, err)
}

func (f *reconcileFixture) restore(key string) {
	f.t.Helper()
	_, err := f.r.PersonDormant(PersonDormantRequest{Subject: key, Restore: true, Now: f.now()})
	require.NoError(f.t, err)
}

func (f *reconcileFixture) claim(key, form string, force bool) {
	f.t.Helper()
	_, err := f.r.PersonClaim(PersonClaimRequest{Subject: key, Form: form, Force: force, Now: f.now()})
	require.NoError(f.t, err)
}

// reconcile runs the scan and proves it byte-stable across a second run.
func (f *reconcileFixture) reconcile(all bool) PersonReconcileResult {
	f.t.Helper()
	res, err := f.r.PersonReconcile(PersonReconcileRequest{All: all})
	require.NoError(f.t, err)
	again, err := f.r.PersonReconcile(PersonReconcileRequest{All: all})
	require.NoError(f.t, err)
	require.Equal(f.t, res.Text, again.Text, "reconcile is byte-stable (S-22)")
	return res
}

// TestPersonReconcile_ListsHeldMentionsWithFix: every pending held mention
// (§P-11) is listed under "Held mentions" with its raw id, the form, the people
// it could mean — active first, a dormant one marked — and the three fixes.
// Settling a mention drops it, an off-limits candidate is never named, and the
// scan writes nothing.
func TestPersonReconcile_ListsHeldMentionsWithFix(t *testing.T) {
	f := newIDFixture(t)
	oldSam, river, samRaw := heldSamPair(f)
	alex := f.person("Alex")
	kim := f.person("Alex Kim")
	stone := f.person("Alex Stone")
	f.dormant(alex)
	alexRaw := f.hold("Alex")

	samCands := []ReconcileHeldCandidate{{PersonKey: oldSam, DisplayName: "Sam"}, {PersonKey: river, DisplayName: "Sam Rivera"}}
	slices.SortFunc(samCands, func(a, b ReconcileHeldCandidate) int { return strings.Compare(a.PersonKey, b.PersonKey) })
	alexCands := make([]ReconcileHeldCandidate, 0, 3)
	alexCands = append(alexCands,
		ReconcileHeldCandidate{PersonKey: kim, DisplayName: "Alex Kim"},
		ReconcileHeldCandidate{PersonKey: stone, DisplayName: "Alex Stone"})
	slices.SortFunc(alexCands, func(a, b ReconcileHeldCandidate) int { return strings.Compare(a.PersonKey, b.PersonKey) })
	alexCands = append(alexCands, ReconcileHeldCandidate{PersonKey: alex, DisplayName: "Alex", Dormant: true})

	before := snapshotTree(t, f.home)
	res, err := f.r.PersonReconcile(PersonReconcileRequest{})
	require.NoError(t, err)
	assert.Equal(t, before, snapshotTree(t, f.home), "reconcile is read-only")
	assert.Equal(t, []ReconcileHeld{
		{RawID: samRaw, Form: "Sam", Reason: identity.HoldExact, Candidates: samCands},
		{RawID: alexRaw, Form: "Alex", Reason: identity.HoldFirstName, Candidates: alexCands},
	}, res.Held)

	wantBlock := func(raw, form string, cands []ReconcileHeldCandidate) string {
		names := make([]string, 0, len(cands))
		for _, c := range cands {
			label := c.PersonKey
			if c.Dormant {
				label += ", dormant"
			}
			names = append(names, fmt.Sprintf("%q (%s)", c.DisplayName, label))
		}
		return fmt.Sprintf("- %s: %q could mean %s.\n", raw, form, strings.Join(names, " / ")) +
			fmt.Sprintf("    → lucid person claim <key> %q\n", form) +
			"    → lucid person dormant <key>\n" +
			fmt.Sprintf("    → lucid person move-ref %s --to <key>", raw)
	}
	assert.Contains(t, res.Text, "\n"+reconcileHeldHeader+"\n"+
		wantBlock(samRaw, "Sam", samCands)+"\n"+wantBlock(alexRaw, "Alex", alexCands),
		"held mentions follow the pairs in (raw id, form) order")
	assert.True(t, strings.HasSuffix(res.Text, "--to <key>"), "the text ends on the last fix, no trailing newline")

	// Settling the Sam mention with a claim drops it from the list.
	f.claim("Sam Rivera", "Sam", true)
	res, err = f.r.PersonReconcile(PersonReconcileRequest{})
	require.NoError(t, err)
	require.Len(t, res.Held, 1)
	assert.Equal(t, alexRaw, res.Held[0].RawID)
	assert.NotContains(t, res.Text, samRaw)

	// An off-limits candidate is never named; the mention is still listed.
	require.NoError(t, f.a.WriteOffLimitsPersonKeys([]string{stone}))
	res, err = f.r.PersonReconcile(PersonReconcileRequest{})
	require.NoError(t, err)
	require.Len(t, res.Held, 1)
	assert.NotContains(t, res.Text, "Alex Stone")
	assert.Len(t, res.Held[0].Candidates, 2)

	// With every candidate off-limits the mention is not listed at all.
	require.NoError(t, f.a.WriteOffLimitsPersonKeys([]string{stone, kim, alex}))
	res, err = f.r.PersonReconcile(PersonReconcileRequest{})
	require.NoError(t, err)
	assert.Empty(t, res.Held)
	assert.NotContains(t, res.Text, reconcileHeldHeader)
}

// TestPersonReconcile_FirstNameOverlapAttachesToMergePair: "Sam" and "Sam
// Rivera" are already a diminutive merge pair, so their first-name overlap
// attaches to that pair — one entry offers merge (same person) or claim /
// dormant (different people) — rather than repeating it in its own section. A
// second overlap between the same two people is listed on its own.
func TestPersonReconcile_FirstNameOverlapAttachesToMergePair(t *testing.T) {
	f := newReconcileFixture(t)
	f.person("person_s-oak", "Sam", 5)
	f.person("person_s-river", "Sam Rivera", 6)

	res := f.reconcile(false)
	require.Len(t, res.Candidates, 1)
	c := res.Candidates[0]
	assert.Equal(t, "person_s-oak", c.TargetKey)
	assert.Equal(t, "person_s-river", c.SourceKey)
	require.NotNil(t, c.Overlap)
	assert.Equal(t, ReconcileOverlap{
		Form: "Sam", HolderKey: "person_s-oak", HolderName: "Sam", HolderFirstSeen: "2026-05-05",
		SharerKey: "person_s-river", SharerName: "Sam Rivera",
	}, *c.Overlap)
	assert.Equal(t, `lucid person claim person_s-river "Sam"`, c.Overlap.SuggestedClaim())
	assert.Equal(t, "lucid person dormant person_s-oak", c.Overlap.SuggestedDormant())
	assert.Empty(t, res.Overlaps, "an attached overlap is not repeated")
	assert.Equal(t, `Found 1 likely-duplicate pair — review each; nothing has changed:
- "Sam" (person_s-oak) and "Sam Rivera" (person_s-river) — similar names ("sam" ~ "samrivera").
    → lucid person merge person_s-river person_s-oak
  Or keep them apart: a bare "Sam" goes to "Sam" (person_s-oak, first seen 2026-05-05), and "Sam Rivera" (person_s-river) also has the first name "Sam".
    → lucid person claim person_s-river "Sam"
    → lucid person dormant person_s-oak`, res.Text)

	// Two overlaps between one pair: "Jordan Lee" holds "Jo" (aka) while "Jo
	// Lark" has the first name Jo, and "Jo Lark" holds "Jordan" while "Jordan
	// Lee" has the first name Jordan. The first by form ("Jo") attaches; the
	// second is listed under First-name overlaps.
	g := newReconcileFixture(t)
	g.person("person_j-lee", "Jordan Lee", 5, "Jo")
	g.person("person_j-lark", "Jo Lark", 6, "Jordan")
	res = g.reconcile(false)
	require.Len(t, res.Candidates, 1)
	require.NotNil(t, res.Candidates[0].Overlap)
	assert.Equal(t, "Jo", res.Candidates[0].Overlap.Form)
	assert.Equal(t, "person_j-lee", res.Candidates[0].Overlap.HolderKey)
	require.Len(t, res.Overlaps, 1)
	assert.Equal(t, ReconcileOverlap{
		Form: "Jordan", HolderKey: "person_j-lark", HolderName: "Jo Lark", HolderFirstSeen: "2026-05-06",
		SharerKey: "person_j-lee", SharerName: "Jordan Lee",
	}, res.Overlaps[0])
	assert.Contains(t, res.Text, "\n"+reconcileOverlapHeader+"\n"+
		`- A bare "Jordan" goes to "Jo Lark" (person_j-lark, first seen 2026-05-06), and "Jordan Lee" (person_j-lee) also has the first name "Jordan".`)
}

// TestPersonReconcile_OverlapStandaloneSection: "Jo" and "Jo Lark" share a
// first name but a two-letter prefix is no diminutive, so there is no merge
// pair and the overlap is listed under its own "First-name overlaps" header,
// right after the honest empty pairs line. An off-limits holder is never named.
func TestPersonReconcile_OverlapStandaloneSection(t *testing.T) {
	f := newReconcileFixture(t)
	f.person("person_j-fern", "Jo", 5)
	f.person("person_j-lark", "Jo Lark", 6)

	res := f.reconcile(false)
	assert.Empty(t, res.Candidates)
	assert.Equal(t, []ReconcileOverlap{{
		Form: "Jo", HolderKey: "person_j-fern", HolderName: "Jo", HolderFirstSeen: "2026-05-05",
		SharerKey: "person_j-lark", SharerName: "Jo Lark",
	}}, res.Overlaps)
	assert.Equal(t, reconcileNoCandidates+`
First-name overlaps — a bare name goes to one person while another shares that first name:
- A bare "Jo" goes to "Jo" (person_j-fern, first seen 2026-05-05), and "Jo Lark" (person_j-lark) also has the first name "Jo".
    → lucid person claim person_j-lark "Jo"
    → lucid person dormant person_j-fern`, res.Text)

	// An off-limits holder or sharer is never named.
	a := f.r.store
	require.NoError(t, a.WriteOffLimitsPersonKeys([]string{"person_j-fern"}))
	res = f.reconcile(false)
	assert.Empty(t, res.Overlaps)
	assert.Equal(t, reconcileNoCandidates, res.Text)
	require.NoError(t, a.WriteOffLimitsPersonKeys([]string{"person_j-lark"}))
	res = f.reconcile(false)
	assert.Empty(t, res.Overlaps)
}

// TestPersonReconcile_OverlapDropsWhenClaimedOrDormant: the overlap line drops
// once the form is claimed — by either person — or either person is dormant,
// and comes back on restore or when the claimant goes dormant (their claim is
// then inert). A form no one holds exactly is never listed.
func TestPersonReconcile_OverlapDropsWhenClaimedOrDormant(t *testing.T) {
	seed := func(t *testing.T) *reconcileFixture {
		f := newReconcileFixture(t)
		f.person("person_s-oak", "Sam", 5)
		f.person("person_s-river", "Sam Rivera", 6)
		f.person("person_j-lee", "Jordan Lee", 7)
		require.NotNil(t, f.reconcile(false).Candidates[0].Overlap, "the overlap starts out listed")
		return f
	}
	hasOverlap := func(res PersonReconcileResult) bool {
		for _, c := range res.Candidates {
			if c.Overlap != nil {
				return true
			}
		}
		return len(res.Overlaps) > 0 || strings.Contains(res.Text, "also has the first name")
	}
	cases := []struct {
		name string
		act  func(f *reconcileFixture)
		want bool
	}{
		{"claimed for the sharer", func(f *reconcileFixture) { f.claim("person_s-river", "Sam", true) }, false},
		{"claimed for the holder", func(f *reconcileFixture) { f.claim("person_s-oak", "Sam", false) }, false},
		{"holder dormant", func(f *reconcileFixture) { f.dormant("person_s-oak") }, false},
		{"sharer dormant", func(f *reconcileFixture) { f.dormant("person_s-river") }, false},
		{"holder restored", func(f *reconcileFixture) {
			f.dormant("person_s-oak")
			f.restore("person_s-oak")
		}, true},
		{"claimant dormant, claim inert", func(f *reconcileFixture) {
			f.claim("person_j-lee", "Sam", true)
			f.dormant("person_j-lee")
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := seed(t)
			tc.act(f)
			assert.Equal(t, tc.want, hasOverlap(f.reconcile(false)))
		})
	}

	t.Run("no exact holder", func(t *testing.T) {
		f := newReconcileFixture(t)
		f.person("person_a-kim", "Alex Kim", 5)
		f.person("person_a-stone", "Alex Stone", 6)
		res := f.reconcile(false)
		assert.False(t, hasOverlap(res), "two people sharing a first name no one holds exactly is not an overlap")
	})
}

// TestPersonReconcile_ActiveSharesFormWithDormant: an active record that shares
// an exact normalized form with a dormant one — a fresh "Sam" minted under the
// next suffix after the old "Sam" went dormant — is flagged by default with the
// restore-then-merge fix. Under --all the two are a merge pair, so the restore
// step attaches to it instead. An off-limits person is never named.
func TestPersonReconcile_ActiveSharesFormWithDormant(t *testing.T) {
	f := newIDFixture(t)
	old := f.person("Sam")
	f.dormant(old)
	fresh := f.person("Sam")
	require.NotEqual(t, old, fresh, "a dormant-only holder mints under the next suffix")

	res, err := f.r.PersonReconcile(PersonReconcileRequest{})
	require.NoError(t, err)
	assert.Empty(t, res.Candidates, "dormant people join merge pairs only under --all")
	share := ReconcileDormantShare{Form: "sam", ActiveKey: fresh, ActiveName: "Sam", DormantKey: old, DormantName: "Sam"}
	assert.Equal(t, []ReconcileDormantShare{share}, res.DormantShares)
	assert.Equal(t, reconcileNoCandidates+"\n"+reconcileDormantHeader+"\n"+
		fmt.Sprintf(`- "Sam" (%s) shares the written form "sam" with the dormant "Sam" (%s). If they are the same person:`, fresh, old)+"\n"+
		fmt.Sprintf("    → lucid person dormant %s --restore\n", old)+
		fmt.Sprintf("    → lucid person merge %s %s", fresh, old), res.Text)

	all, err := f.r.PersonReconcile(PersonReconcileRequest{All: true})
	require.NoError(t, err)
	require.Len(t, all.Candidates, 1)
	c := all.Candidates[0]
	assert.Equal(t, old, c.TargetKey)
	assert.True(t, c.TargetDormant)
	assert.False(t, c.SourceDormant)
	require.NotNil(t, c.DormantShare)
	assert.Equal(t, share, *c.DormantShare)
	assert.Empty(t, all.DormantShares, "an attached look-alike is not repeated")
	assert.Equal(t, "Found 1 likely-duplicate pair — review each; nothing has changed:\n"+
		fmt.Sprintf(`- "Sam" (%s, dormant) and "Sam" (%s) — share the written form "sam".`, old, fresh)+"\n"+
		fmt.Sprintf("    → lucid person merge %s %s\n", fresh, old)+
		fmt.Sprintf(`  "Sam" (%s) is dormant — restore them before merging:`, old)+"\n"+
		fmt.Sprintf("    → lucid person dormant %s --restore", old), all.Text)

	require.NoError(t, f.a.WriteOffLimitsPersonKeys([]string{old}))
	res, err = f.r.PersonReconcile(PersonReconcileRequest{})
	require.NoError(t, err)
	assert.Empty(t, res.DormantShares, "an off-limits person is never named")
	assert.Equal(t, reconcileNoCandidates, res.Text)
}

// TestPersonReconcile_ExcludesDormantUnlessAll: a dormant person leaves the
// merge suggestions by default; --all brings them back, marked dormant inside
// the parentheses. Off-limits people stay out either way, and restore returns
// the pair to the default scan.
func TestPersonReconcile_ExcludesDormantUnlessAll(t *testing.T) {
	f := newReconcileFixture(t)
	f.person("person_s-sam", "Sam", 5)
	f.person("person_s-sammy", "Sammy", 6)
	require.Len(t, f.reconcile(false).Candidates, 1)

	f.dormant("person_s-sammy")
	res := f.reconcile(false)
	assert.Empty(t, res.Candidates)
	assert.Equal(t, reconcileNoCandidates, res.Text)

	all := f.reconcile(true)
	require.Len(t, all.Candidates, 1)
	c := all.Candidates[0]
	assert.Equal(t, "person_s-sammy", c.SourceKey)
	assert.True(t, c.SourceDormant)
	assert.False(t, c.TargetDormant)
	assert.Equal(t, `Found 1 likely-duplicate pair — review each; nothing has changed:
- "Sam" (person_s-sam) and "Sammy" (person_s-sammy, dormant) — linked nicknames ("sam" ~ "sammy").
    → lucid person merge person_s-sammy person_s-sam`, all.Text)

	a := f.r.store
	require.NoError(t, a.WriteOffLimitsPersonKeys([]string{"person_s-sammy"}))
	assert.Empty(t, f.reconcile(true).Candidates, "an off-limits person stays out under --all")
	require.NoError(t, a.WriteOffLimitsPersonKeys(nil))

	f.restore("person_s-sammy")
	assert.Len(t, f.reconcile(false).Candidates, 1, "restore returns the pair to the default scan")
}
