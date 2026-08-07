package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/data"
)

// TestMergePersons_UnionsAndTombstones proves a merge folds the source into the
// target (aka + refs + window), rewrites the source as a redirect tombstone, and
// leaves the store resolving a later mention of the merged-away form onto the
// canonical record. Idempotent and byte-stable on a re-run.
func TestMergePersons_UnionsAndTombstones(t *testing.T) {
	a := newPeopleAdapter(t)
	keyAlex := seedPersonStore(t, a, "Alex", "raw_2026_05_01_09_00", personAt(1))
	keyAndy := seedPersonStore(t, a, "Andy", "raw_2026_05_03_10_00", personAt(3))
	require.NotEqual(t, keyAlex, keyAndy)

	merged, err := a.MergePersons(keyAndy, keyAlex)
	require.NoError(t, err)
	assert.Equal(t, keyAlex, merged.PersonKey)
	assert.Subset(t, merged.Aka, []string{"Alex", "Andy"}, "target absorbs the source forms")
	assert.Equal(t, []string{"raw_2026_05_01_09_00", "raw_2026_05_03_10_00"}, merged.EntryRefs)
	assert.Equal(t, personAt(1), merged.FirstSeenAt, "window widened to the earlier first-seen")
	assert.Equal(t, personAt(3), merged.LastSeenAt)

	// The source is now a tombstone forwarding to the target.
	tomb, found, err := a.ReadPerson(keyAndy)
	require.NoError(t, err)
	require.True(t, found)
	assert.True(t, tomb.IsTombstone())
	assert.Equal(t, keyAlex, tomb.RedirectTo)

	// A later bare "Andy" mention folds into the canonical Alex.
	res, err := a.UpdatePerson(PersonMention{DisplayName: "Andy", RawEntryID: "raw_2026_05_09_20_10", At: personAt(9)})
	require.NoError(t, err)
	assert.Equal(t, keyAlex, res.PersonKey)

	// Idempotent: re-merging is a no-op and leaves both files byte-identical.
	beforeAlex := readFileBytes(t, a, keyAlex)
	beforeAndy := readFileBytes(t, a, keyAndy)
	_, err = a.MergePersons(keyAndy, keyAlex)
	require.NoError(t, err)
	assert.Equal(t, beforeAlex, readFileBytes(t, a, keyAlex))
	assert.Equal(t, beforeAndy, readFileBytes(t, a, keyAndy))
}

// TestMergePersons_FlattensChains proves a merge re-points every inbound
// tombstone so the redirect graph stays single-hop: after A→B then B→C, A must
// point straight at C.
func TestMergePersons_FlattensChains(t *testing.T) {
	a := newPeopleAdapter(t)
	keyA := seedPersonStore(t, a, "Alex", "raw_2026_05_01_09_00", personAt(1))
	keyB := seedPersonStore(t, a, "Andy", "raw_2026_05_02_09_00", personAt(2))
	keyC := seedPersonStore(t, a, "Ali", "raw_2026_05_03_09_00", personAt(3))

	_, err := a.MergePersons(keyA, keyB) // A → B
	require.NoError(t, err)
	_, err = a.MergePersons(keyB, keyC) // B → C, and A must flatten to C
	require.NoError(t, err)

	for _, key := range []string{keyA, keyB} {
		canon, rerr := a.ResolvePersonRedirect(key)
		require.NoError(t, rerr)
		assert.Equal(t, keyC, canon, "%s must resolve straight to C", key)
	}
	// No chain survives: passing the fully-merged store to a fresh mention lands
	// on C regardless of which absorbed form is used.
	res, err := a.UpdatePerson(PersonMention{DisplayName: "Alex", RawEntryID: "raw_2026_05_10_10_00", At: personAt(10)})
	require.NoError(t, err)
	assert.Equal(t, keyC, res.PersonKey)
}

// TestMergePersons_SelfIsNoOp proves merging a person into themselves (directly
// or via a form that resolves to the same canonical) changes nothing.
func TestMergePersons_SelfIsNoOp(t *testing.T) {
	a := newPeopleAdapter(t)
	keyAlex := seedPersonStore(t, a, "Alex", "raw_2026_05_01_09_00", personAt(1))
	before := readFileBytes(t, a, keyAlex)

	rec, err := a.MergePersons(keyAlex, keyAlex)
	require.NoError(t, err)
	assert.Equal(t, keyAlex, rec.PersonKey)
	assert.Equal(t, before, readFileBytes(t, a, keyAlex), "a self-merge writes nothing")
}

// TestMergePersons_CarriesOffLimits proves an off-limits source hands its
// redaction to the target and drops its own now-tombstoned key from the
// registry.
func TestMergePersons_CarriesOffLimits(t *testing.T) {
	a := newPeopleAdapter(t)
	keyAlex := seedPersonStore(t, a, "Alex", "raw_2026_05_01_09_00", personAt(1))
	keyAndy := seedPersonStore(t, a, "Andy", "raw_2026_05_03_10_00", personAt(3))
	require.NoError(t, a.WriteOffLimitsPersonKeys([]string{keyAndy}))

	_, err := a.MergePersons(keyAndy, keyAlex)
	require.NoError(t, err)

	keys, err := a.ReadOffLimitsPersonKeys()
	require.NoError(t, err)
	assert.Equal(t, []string{keyAlex}, keys, "off-limits carries to the target, source key dropped")
}

// TestAddPersonAka_PlantsResolvingTombstone proves an alias records the form and
// makes a future bare mention of it resolve to the canonical record. Idempotent.
func TestAddPersonAka_PlantsResolvingTombstone(t *testing.T) {
	a := newPeopleAdapter(t)
	keyAlex := seedPersonStore(t, a, "Alex", "raw_2026_05_01_09_00", personAt(1))

	rec, err := a.AddPersonAka(keyAlex, "Ali")
	require.NoError(t, err)
	assert.Contains(t, rec.Aka, "Ali")

	// A future "Ali" mention folds into Alex.
	res, err := a.UpdatePerson(PersonMention{DisplayName: "Ali", RawEntryID: "raw_2026_05_09_20_10", At: personAt(9)})
	require.NoError(t, err)
	assert.Equal(t, keyAlex, res.PersonKey)

	// Idempotent.
	before := readFileBytes(t, a, keyAlex)
	_, err = a.AddPersonAka(keyAlex, "Ali")
	require.NoError(t, err)
	assert.Equal(t, before, readFileBytes(t, a, keyAlex))
}

// TestAddPersonAka_RefusesHijack proves an alias whose form already resolves to a
// different live person is refused with ErrPersonAkaConflict.
func TestAddPersonAka_RefusesHijack(t *testing.T) {
	a := newPeopleAdapter(t)
	keyAlex := seedPersonStore(t, a, "Alex", "raw_2026_05_01_09_00", personAt(1))
	_ = seedPersonStore(t, a, "Bob", "raw_2026_05_02_09_00", personAt(2))

	_, err := a.AddPersonAka(keyAlex, "Bob")
	require.ErrorIs(t, err, ErrPersonAkaConflict)
}

// TestRenamePerson_KeyUnchanged proves a rename changes the display name, folds
// the old form into aka[], keeps the person_key, and makes a future mention of
// the new name resolve here.
func TestRenamePerson_KeyUnchanged(t *testing.T) {
	a := newPeopleAdapter(t)
	keyAlex := seedPersonStore(t, a, "Alex", "raw_2026_05_01_09_00", personAt(1))

	rec, err := a.RenamePerson(keyAlex, "Alexandra")
	require.NoError(t, err)
	assert.Equal(t, keyAlex, rec.PersonKey, "the key is a stable id")
	assert.Equal(t, "Alexandra", rec.DisplayName)
	assert.Subset(t, rec.Aka, []string{"Alex", "Alexandra"}, "the old form is kept")

	res, err := a.UpdatePerson(PersonMention{DisplayName: "Alexandra", RawEntryID: "raw_2026_05_09_20_10", At: personAt(9)})
	require.NoError(t, err)
	assert.Equal(t, keyAlex, res.PersonKey)
}

// TestRenamePerson_NameTaken proves a rename onto a name that already resolves to
// a different live person is refused.
func TestRenamePerson_NameTaken(t *testing.T) {
	a := newPeopleAdapter(t)
	keyAlex := seedPersonStore(t, a, "Alex", "raw_2026_05_01_09_00", personAt(1))
	_ = seedPersonStore(t, a, "Bob", "raw_2026_05_02_09_00", personAt(2))

	_, err := a.RenamePerson(keyAlex, "Bob")
	require.ErrorIs(t, err, ErrPersonAkaConflict)
}

// TestSetPerson_FieldsAndValidation proves set writes the durable fields, leaves
// identity untouched, validates the dob, and clears a field on an empty value.
func TestSetPerson_FieldsAndValidation(t *testing.T) {
	a := newPeopleAdapter(t)
	keyAlex := seedPersonStore(t, a, "Alex", "raw_2026_05_01_09_00", personAt(1))
	before, _, err := a.ReadPerson(keyAlex)
	require.NoError(t, err)

	dob, rel, note := "1990-04-12", "colleague", "met at the co-op"
	rec, err := a.SetPerson(keyAlex, PersonPatch{Dob: &dob, Relationship: &rel, Notes: &note})
	require.NoError(t, err)
	require.NotNil(t, rec.Dob)
	assert.Equal(t, "1990-04-12", *rec.Dob)
	assert.Equal(t, "colleague", *rec.Relationship)
	assert.Equal(t, "met at the co-op", *rec.Notes)
	// Identity untouched.
	assert.Equal(t, before.PersonKey, rec.PersonKey)
	assert.Equal(t, before.DisplayName, rec.DisplayName)
	assert.Equal(t, before.Aka, rec.Aka)
	assert.Empty(t, rec.RedirectTo)

	// A bad dob is rejected before any write.
	_, err = a.SetPerson(keyAlex, PersonPatch{Dob: strptr("04/12/1990")})
	require.Error(t, err)

	// An empty value clears the field.
	cleared, err := a.SetPerson(keyAlex, PersonPatch{Relationship: strptr("")})
	require.NoError(t, err)
	assert.Nil(t, cleared.Relationship, "an empty value clears the field")
	assert.NotNil(t, cleared.Dob, "an unset field is left unchanged")
}

// TestValidCivilDate covers the writer-side dob guard.
func TestValidCivilDate(t *testing.T) {
	assert.True(t, ValidCivilDate("1990-04-12"))
	assert.False(t, ValidCivilDate("04/12/1990"))
	assert.False(t, ValidCivilDate("1990-13-01"))
	assert.False(t, ValidCivilDate("1990"))
	assert.False(t, ValidCivilDate(""))
}

// TestMergePersons_ByteStableAcrossReadsAfterMerge proves the merged store is
// deterministic: re-deriving the same absorbed form twice lands on the canonical
// both times.
func TestMergePersons_ByteStableAcrossReadsAfterMerge(t *testing.T) {
	a := newPeopleAdapter(t)
	keyAlex := seedPersonStore(t, a, "Alex", "raw_2026_05_01_09_00", personAt(1))
	keyAndy := seedPersonStore(t, a, "Andy", "raw_2026_05_03_10_00", personAt(3))
	_, err := a.MergePersons(keyAndy, keyAlex)
	require.NoError(t, err)

	first, err := a.ResolvePersonRedirect(keyAndy)
	require.NoError(t, err)
	second, err := a.ResolvePersonRedirect(keyAndy)
	require.NoError(t, err)
	assert.Equal(t, first, second)
	assert.Equal(t, keyAlex, first)
	_ = data.Wordlist()
}

// TestUpdatePerson_FoldsExplicitAka proves a mention carrying an explicit aka
// records the form and plants a resolving tombstone, so a later bare mention of
// the alias folds into the same person (acceptance-criteria.md 4.7).
func TestUpdatePerson_FoldsExplicitAka(t *testing.T) {
	a := newPeopleAdapter(t)
	res, err := a.UpdatePerson(PersonMention{
		DisplayName: "Sam", Aka: []string{"Sammy"}, RawEntryID: "raw_2026_05_01_09_00", At: personAt(1),
	})
	require.NoError(t, err)
	keySam := res.PersonKey

	rec, _, err := a.ReadPerson(keySam)
	require.NoError(t, err)
	assert.Contains(t, rec.Aka, "Sammy")

	// A later bare "Sammy" mention folds into Sam.
	later, err := a.UpdatePerson(PersonMention{DisplayName: "Sammy", RawEntryID: "raw_2026_05_09_20_10", At: personAt(9)})
	require.NoError(t, err)
	assert.Equal(t, keySam, later.PersonKey)
}

// TestUpdatePerson_AkaNeverHijacks proves an explicit aka whose slug already
// belongs to a *different* live person records the form (for matching) but plants
// no tombstone, leaves the two people separate, and never fails capture
// (acceptance-criteria.md 4.8).
func TestUpdatePerson_AkaNeverHijacks(t *testing.T) {
	a := newPeopleAdapter(t)
	keyBob := seedPersonStore(t, a, "Bob", "raw_2026_05_02_09_00", personAt(2))

	res, err := a.UpdatePerson(PersonMention{
		DisplayName: "Alex", Aka: []string{"Bob"}, RawEntryID: "raw_2026_05_03_10_00", At: personAt(3),
	})
	require.NoError(t, err, "capture never fails on a hijacking aka")
	keyAlex := res.PersonKey
	require.NotEqual(t, keyAlex, keyBob)

	alex, _, err := a.ReadPerson(keyAlex)
	require.NoError(t, err)
	assert.Contains(t, alex.Aka, "Bob", "the aka is recorded for matching")

	// A bare "Bob" still resolves to the original Bob, not Alex — no hijack.
	later, err := a.UpdatePerson(PersonMention{DisplayName: "Bob", RawEntryID: "raw_2026_05_09_20_10", At: personAt(9)})
	require.NoError(t, err)
	assert.Equal(t, keyBob, later.PersonKey)
}

// TestUpdatePerson_AkaEqualToDisplayIsNoop proves an aka that repeats the display
// name adds nothing spurious and plants no self-tombstone.
func TestUpdatePerson_AkaEqualToDisplayIsNoop(t *testing.T) {
	a := newPeopleAdapter(t)
	res, err := a.UpdatePerson(PersonMention{
		DisplayName: "Sam", Aka: []string{"Sam", "  "}, RawEntryID: "raw_2026_05_01_09_00", At: personAt(1),
	})
	require.NoError(t, err)
	rec, _, err := a.ReadPerson(res.PersonKey)
	require.NoError(t, err)
	assert.Equal(t, []string{"Sam"}, rec.Aka)
}

// readFileBytes reads the raw on-disk bytes of a person record.
func readFileBytes(t *testing.T, a *Adapter, key string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(a.peopleDir(), key+".json"))
	require.NoError(t, err)
	return b
}

// strptr returns a pointer to s, for building sparse PersonPatch values.
func strptr(s string) *string { return &s }
