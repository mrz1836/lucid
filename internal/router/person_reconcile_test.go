package router

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPersonReconcile_TwoSignals seeds a diminutive pair (Sam/Sammy) and a
// shared-form pair (Alex/Andy share "A.") and confirms both surface, with a
// deterministic direction and byte-stable output across two runs.
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
	assert.Contains(t, samSammy.Reason, "similar names")

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

// TestLevenshtein covers the pure edit-distance helper.
func TestLevenshtein(t *testing.T) {
	assert.Equal(t, 0, levenshtein("alex", "alex"))
	assert.Equal(t, 1, levenshtein("alex", "alexx"))
	assert.Equal(t, 2, levenshtein("alexx", "alx"))
	assert.Equal(t, 4, levenshtein("", "andy"))
	assert.Equal(t, 4, levenshtein("andy", ""))
}

// TestProximateForms covers the two proximity signals and their length floors.
func TestProximateForms(t *testing.T) {
	// Diminutive prefix (>= 3 runes, strict).
	assert.True(t, proximateForms("sam", "sammy"))
	assert.True(t, proximateForms("sammy", "sam"))
	assert.False(t, proximateForms("al", "alex"), "a 2-char prefix is too short")
	// Bounded edit distance, both >= 4 runes.
	assert.True(t, proximateForms("alex", "alexx"))
	assert.False(t, proximateForms("sam", "pam"), "short forms are not edit-paired")
	// Exact match is the shared signal, not proximity.
	assert.False(t, proximateForms("alex", "alex"))
	// Far apart.
	assert.False(t, proximateForms("alexandra", "beatrice"))
}
