package router

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/data"
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

	res, err := r.PersonReconcile()
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
	again, err := r.PersonReconcile()
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

	res, err := r.PersonReconcile()
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

	res, err := r.PersonReconcile()
	require.NoError(t, err)
	assert.Empty(t, res.Candidates, "people seen together are usually different people")
}

// TestPersonReconcile_EmptyAndTombstones proves an empty store is an honest empty
// result and a tombstone is never a candidate.
func TestPersonReconcile_EmptyAndTombstones(t *testing.T) {
	r, _, home := newBootedRouter(t)

	res, err := r.PersonReconcile()
	require.NoError(t, err)
	assert.Empty(t, res.Candidates)
	assert.Equal(t, reconcileNoCandidates, res.Text)

	// A canonical Sam plus a tombstone at a Sam-like slug: the tombstone is
	// skipped, so no pair is produced.
	writePersonFile(t, home, "person_s-sam", "Sam", []string{"Sam"}, personSeedTime())
	writeTombstone(t, home, "person_s-sammy", "Sammy", "person_s-sam", personSeedTime())

	res, err = r.PersonReconcile()
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

	res, err := r.PersonReconcile()
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

	res, err := r.PersonReconcile()
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

	res, err := r.PersonReconcile()
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
