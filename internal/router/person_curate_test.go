package router

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/storage"
)

// TestPersonMerge_UnionsAndRejectsSelf covers the merge happy path (union +
// tombstone, a later mention resolves to the canonical) and the §P-6 self-merge
// rejection.
func TestPersonMerge_UnionsAndRejectsSelf(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	keyAlex := seedPerson(t, a, "Alex", "raw_2026_05_01_09_00", personSeedTime())
	_ = seedPerson(t, a, "Andy", "raw_2026_05_03_10_00", personSeedTime().Add(48*time.Hour))

	res, err := r.PersonMerge(PersonMergeRequest{Source: "Andy", Target: "Alex"})
	require.NoError(t, err)
	assert.Equal(t, keyAlex, res.Record.PersonKey)
	assert.Contains(t, res.Ack, "Merged")
	assert.Contains(t, res.Ack, "resolve to one record")

	// A later "Andy" mention now folds into Alex.
	folded, err := a.UpdatePerson(newMention("Andy", "raw_2026_05_09_20_10", personSeedTime().Add(96*time.Hour)))
	require.NoError(t, err)
	assert.Equal(t, keyAlex, folded.PersonKey)

	// Merging a person into themselves (any resolving form) is §P-6.
	_, err = r.PersonMerge(PersonMergeRequest{Source: "Andy", Target: "Alex"})
	require.ErrorIs(t, err, ErrPersonMergeSelf)
	_, err = r.PersonMerge(PersonMergeRequest{Source: keyAlex, Target: keyAlex})
	require.ErrorIs(t, err, ErrPersonMergeSelf)
}

// TestPersonAlias_IdempotentAndConflict covers the alias happy path (a later
// mention of the form resolves here), idempotency, and the §P-7 hijack refusal.
func TestPersonAlias_IdempotentAndConflict(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	keyAlex := seedPerson(t, a, "Alex", "raw_2026_05_01_09_00", personSeedTime())
	_ = seedPerson(t, a, "Bob", "raw_2026_05_02_09_00", personSeedTime().Add(24*time.Hour))

	res, err := r.PersonAlias(PersonAliasRequest{Subject: "Alex", Form: "Ali"})
	require.NoError(t, err)
	assert.Contains(t, res.Record.Aka, "Ali")

	folded, err := a.UpdatePerson(newMention("Ali", "raw_2026_05_09_20_10", personSeedTime().Add(96*time.Hour)))
	require.NoError(t, err)
	assert.Equal(t, keyAlex, folded.PersonKey)

	// Idempotent.
	_, err = r.PersonAlias(PersonAliasRequest{Subject: "Alex", Form: "Ali"})
	require.NoError(t, err)

	// Aliasing a form that already belongs to Bob is §P-7.
	_, err = r.PersonAlias(PersonAliasRequest{Subject: "Alex", Form: "Bob"})
	require.ErrorIs(t, err, ErrPersonAliasTaken)

	// A blank form is a §P-9 input rejection.
	_, err = r.PersonAlias(PersonAliasRequest{Subject: "Alex", Form: "   "})
	require.ErrorIs(t, err, ErrPersonRejected)
}

// TestPersonRename_KeyStableAndNameTaken covers the rename happy path (key
// unchanged, old form kept) and the §P-8 name-taken refusal.
func TestPersonRename_KeyStableAndNameTaken(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	keyAlex := seedPerson(t, a, "Alex", "raw_2026_05_01_09_00", personSeedTime())
	_ = seedPerson(t, a, "Bob", "raw_2026_05_02_09_00", personSeedTime().Add(24*time.Hour))

	res, err := r.PersonRename(PersonRenameRequest{Subject: "Alex", NewName: "Alexandra"})
	require.NoError(t, err)
	assert.Equal(t, keyAlex, res.Record.PersonKey, "the key is stable")
	assert.Equal(t, "Alexandra", res.Record.DisplayName)
	assert.Contains(t, res.Record.Aka, "Alex")
	assert.Contains(t, res.Ack, "person_key is unchanged")

	// Rename onto Bob's name is §P-8.
	_, err = r.PersonRename(PersonRenameRequest{Subject: "Alexandra", NewName: "Bob"})
	require.ErrorIs(t, err, ErrPersonNameTaken)

	// A blank new name is a §P-9 input rejection.
	_, err = r.PersonRename(PersonRenameRequest{Subject: "Alexandra", NewName: "  "})
	require.ErrorIs(t, err, ErrPersonRejected)
}

// TestPersonSet_FieldsAndValidation covers set writing the durable fields, the
// §P-9 bad-dob rejection, and the no-fields rejection.
func TestPersonSet_FieldsAndValidation(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	_ = seedPerson(t, a, "Alex", "raw_2026_05_01_09_00", personSeedTime())

	dob, rel := "1990-04-12", "colleague"
	res, err := r.PersonSet(PersonSetRequest{Subject: "Alex", Dob: &dob, Relationship: &rel})
	require.NoError(t, err)
	require.NotNil(t, res.Record.Dob)
	assert.Equal(t, "1990-04-12", *res.Record.Dob)
	assert.Equal(t, "colleague", *res.Record.Relationship)
	assert.Contains(t, res.Ack, "dob 1990-04-12")

	bad := "04/12/1990"
	_, err = r.PersonSet(PersonSetRequest{Subject: "Alex", Dob: &bad})
	require.ErrorIs(t, err, ErrPersonRejected)

	_, err = r.PersonSet(PersonSetRequest{Subject: "Alex"})
	require.ErrorIs(t, err, ErrPersonRejected)
}

// TestPersonOffLimits_AddAndRestore covers toggling redaction and its
// idempotency in both directions.
func TestPersonOffLimits_AddAndRestore(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	keyAlex := seedPerson(t, a, "Alex", "raw_2026_05_01_09_00", personSeedTime())

	res, err := r.PersonOffLimits(PersonOffLimitsRequest{Subject: "Alex"})
	require.NoError(t, err)
	assert.True(t, res.OffLimits)
	assert.Contains(t, res.Ack, "now off-limits")
	assertOffLimits(t, a, []string{keyAlex})

	// Idempotent add.
	_, err = r.PersonOffLimits(PersonOffLimitsRequest{Subject: "Alex"})
	require.NoError(t, err)
	assertOffLimits(t, a, []string{keyAlex})

	// Restore, then idempotent restore.
	res, err = r.PersonOffLimits(PersonOffLimitsRequest{Subject: "Alex", Restore: true})
	require.NoError(t, err)
	assert.False(t, res.OffLimits)
	assert.Contains(t, res.Ack, "no longer off-limits")
	assertOffLimits(t, a, nil)

	_, err = r.PersonOffLimits(PersonOffLimitsRequest{Subject: "Alex", Restore: true})
	require.NoError(t, err)
	assertOffLimits(t, a, nil)
}

// TestResolvePersonSubject_UnknownAmbiguousExact covers the three subject
// resolution outcomes: no live match (§P-4), several matches (§P-5), and an
// exact key (including a tombstone key that resolves forward).
func TestResolvePersonSubject_UnknownAmbiguousExact(t *testing.T) {
	r, a, home := newBootedRouter(t)

	// §P-4 on an empty store.
	_, err := r.PersonSet(PersonSetRequest{Subject: "Nobody", Note: strptrR("x")})
	require.ErrorIs(t, err, ErrPersonUnknownSubject)

	// §P-5: two records sharing an aka.
	writePersonFile(t, home, "person_s-quiet", "Sam", []string{"S.", "Sam"}, personSeedTime())
	writePersonFile(t, home, "person_s-river", "Sammy", []string{"S.", "Sammy"}, personSeedTime().Add(24*time.Hour))
	_, err = r.PersonSet(PersonSetRequest{Subject: "S.", Note: strptrR("x")})
	require.ErrorIs(t, err, ErrPersonAmbiguousSubject)

	// Exact key resolves even when it shares a name — no ambiguity.
	res, err := r.PersonSet(PersonSetRequest{Subject: "person_s-quiet", Relationship: strptrR("colleague")})
	require.NoError(t, err)
	assert.Equal(t, "person_s-quiet", res.Record.PersonKey)

	// A tombstone key resolves forward to its canonical.
	keyAlex := seedPerson(t, a, "Alex", "raw_2026_05_05_19_42", personSeedTime())
	keyAndy := seedPerson(t, a, "Andy", "raw_2026_05_06_08_10", personSeedTime().Add(24*time.Hour))
	_, err = r.PersonMerge(PersonMergeRequest{Source: keyAndy, Target: keyAlex})
	require.NoError(t, err)
	res, err = r.PersonSet(PersonSetRequest{Subject: keyAndy, Note: strptrR("via tombstone")})
	require.NoError(t, err)
	assert.Equal(t, keyAlex, res.Record.PersonKey, "the tombstone key resolves to the canonical")
}

// newMention builds a PersonMention for the curation tests.
func newMention(display, rawID string, at time.Time) storage.PersonMention {
	return storage.PersonMention{DisplayName: display, RawEntryID: rawID, At: at}
}

// assertOffLimits asserts the off-limits registry holds exactly want (order- and
// nil/empty-insensitive, since a cleared registry reads back as an empty slice).
func assertOffLimits(t *testing.T, a *storage.Adapter, want []string) {
	t.Helper()
	got, err := a.ReadOffLimitsPersonKeys()
	require.NoError(t, err)
	assert.ElementsMatch(t, want, got)
}

// strptrR returns a pointer to s, for building sparse set requests.
func strptrR(s string) *string { return &s }
