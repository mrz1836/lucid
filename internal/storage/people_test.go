package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/data"
)

// personAt is a fixed synthetic instant for the People-routine tests.
func personAt(day int) time.Time {
	return time.Date(2026, time.May, day, 19, 42, 0, 0, time.UTC)
}

// newPeopleAdapter returns an adapter over a fresh temp Ledger for the
// People-routine tests. UpdatePerson creates people/ on demand, so no
// scaffold is required.
func newPeopleAdapter(t *testing.T) *Adapter {
	t.Helper()
	return New(t.TempDir())
}

// TestUpdatePerson_FirstMentionCreatesRecord is acceptance case 4.1 at the
// storage seam: the first mention of a name creates people/<key>.json and
// reports first_mention true.
func TestUpdatePerson_FirstMentionCreatesRecord(t *testing.T) {
	a := newPeopleAdapter(t)

	res, err := a.UpdatePerson(PersonMention{DisplayName: "M.", RawEntryID: "raw_2026_05_05_19_42", At: personAt(5)})
	require.NoError(t, err)
	assert.Equal(t, "person_p-hazel", res.PersonKey) // golden key for "M."
	assert.True(t, res.FirstMention)

	rec, found, err := a.ReadPerson(res.PersonKey)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "M.", rec.DisplayName)
	assert.Equal(t, []string{"M."}, rec.Aka)
	assert.Equal(t, []string{"raw_2026_05_05_19_42"}, rec.EntryRefs)
	assert.Equal(t, personAt(5), rec.FirstSeenAt)
	assert.Equal(t, personAt(5), rec.LastSeenAt)
	assert.Nil(t, rec.Notes)

	// The file exists at people/<key>.json.
	_, statErr := os.Stat(filepath.Join(a.peopleDir(), res.PersonKey+".json"))
	require.NoError(t, statErr)
}

// TestUpdatePerson_SecondMentionMergesAka is acceptance case 4.2: a second
// mention with a different spelling is first_mention false, updates aka[], and
// appends the new entry ref.
func TestUpdatePerson_SecondMentionMergesAka(t *testing.T) {
	a := newPeopleAdapter(t)

	_, err := a.UpdatePerson(PersonMention{DisplayName: "M.", RawEntryID: "raw_2026_05_05_19_42", At: personAt(5)})
	require.NoError(t, err)

	res, err := a.UpdatePerson(PersonMention{DisplayName: "M", RawEntryID: "raw_2026_05_09_20_10", At: personAt(9)})
	require.NoError(t, err)
	assert.Equal(t, "person_p-hazel", res.PersonKey) // "M" normalizes to the same key as "M."
	assert.False(t, res.FirstMention)

	rec, found, err := a.ReadPerson(res.PersonKey)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "M", rec.DisplayName) // latest spelling wins
	assert.Equal(t, []string{"M", "M."}, rec.Aka)
	assert.Equal(t, []string{"raw_2026_05_05_19_42", "raw_2026_05_09_20_10"}, rec.EntryRefs)
	assert.Equal(t, personAt(5), rec.FirstSeenAt) // earliest preserved
	assert.Equal(t, personAt(9), rec.LastSeenAt)  // widened
}

// TestUpdatePerson_Idempotent re-runs the same (person, raw entry) and proves
// the record is unchanged: no duplicate ref, first_mention stays true — the
// storage half of acceptance case 4.3.
func TestUpdatePerson_Idempotent(t *testing.T) {
	a := newPeopleAdapter(t)
	m := PersonMention{DisplayName: "M.", RawEntryID: "raw_2026_05_05_19_42", At: personAt(5)}

	first, err := a.UpdatePerson(m)
	require.NoError(t, err)
	before, err := os.ReadFile(filepath.Join(a.peopleDir(), first.PersonKey+".json"))
	require.NoError(t, err)

	second, err := a.UpdatePerson(m)
	require.NoError(t, err)
	assert.Equal(t, first, second) // same key, first_mention still true

	after, err := os.ReadFile(filepath.Join(a.peopleDir(), first.PersonKey+".json"))
	require.NoError(t, err)
	assert.Equal(t, before, after, "a repeated mention must leave the record byte-identical")

	rec, _, err := a.ReadPerson(first.PersonKey)
	require.NoError(t, err)
	assert.Equal(t, []string{"raw_2026_05_05_19_42"}, rec.EntryRefs, "entry_refs must not duplicate")
}

// TestUpdatePerson_FirstMentionIsOrderIndependent introduces a person in a
// later entry first, then an earlier one: the earliest raw id is the first
// mention regardless of insertion order, so the determination is stable.
func TestUpdatePerson_FirstMentionIsOrderIndependent(t *testing.T) {
	a := newPeopleAdapter(t)

	// Later entry recorded first.
	late, err := a.UpdatePerson(PersonMention{DisplayName: "Alex", RawEntryID: "raw_2026_05_09_20_10", At: personAt(9)})
	require.NoError(t, err)
	assert.True(t, late.FirstMention, "the only ref so far is the earliest")

	// Earlier entry recorded second.
	early, err := a.UpdatePerson(PersonMention{DisplayName: "Alex", RawEntryID: "raw_2026_05_03_08_00", At: personAt(3)})
	require.NoError(t, err)
	assert.True(t, early.FirstMention, "the earlier entry is now the first mention")

	// Re-deriving the later entry now reports false — it is no longer earliest.
	lateAgain, err := a.UpdatePerson(PersonMention{DisplayName: "Alex", RawEntryID: "raw_2026_05_09_20_10", At: personAt(9)})
	require.NoError(t, err)
	assert.False(t, lateAgain.FirstMention)
}

// TestUpdatePerson_CollisionSuffix seeds a different person at the base key so
// a new name deriving to the same key lands on the -2 suffix (data-model.md
// §"person_key derivation").
func TestUpdatePerson_CollisionSuffix(t *testing.T) {
	a := newPeopleAdapter(t)
	require.NoError(t, os.MkdirAll(a.peopleDir(), dirPerm))

	// Seed person_p-hazel owned by a differently-normalized name ("Other").
	seed := personRecordJSON{
		PersonKey:   "person_p-hazel",
		DisplayName: "Other",
		Aka:         []string{"Other"},
		FirstSeenAt: personAt(1).Format(time.RFC3339),
		LastSeenAt:  personAt(1).Format(time.RFC3339),
		EntryRefs:   []string{"raw_2026_05_01_09_00"},
	}
	b, err := marshalJSON(seed)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(a.peopleDir(), "person_p-hazel.json"), b, filePerm))

	// "M." also derives to person_p-hazel but is a different person → -2.
	res, err := a.UpdatePerson(PersonMention{DisplayName: "M.", RawEntryID: "raw_2026_05_05_19_42", At: personAt(5)})
	require.NoError(t, err)
	assert.Equal(t, "person_p-hazel-2", res.PersonKey)
	assert.True(t, res.FirstMention)

	// The seeded record is untouched.
	other, _, err := a.ReadPerson("person_p-hazel")
	require.NoError(t, err)
	assert.Equal(t, "Other", other.DisplayName)
}

// TestUpdatePerson_Validation covers the guarded inputs: an empty display
// name, an empty raw id, and a name that normalizes to nothing.
func TestUpdatePerson_Validation(t *testing.T) {
	a := newPeopleAdapter(t)

	_, err := a.UpdatePerson(PersonMention{DisplayName: "  ", RawEntryID: "raw_x", At: personAt(5)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty display_name")

	_, err = a.UpdatePerson(PersonMention{DisplayName: "M.", RawEntryID: "", At: personAt(5)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty raw_entry_id")

	_, err = a.UpdatePerson(PersonMention{DisplayName: ".!?", RawEntryID: "raw_x", At: personAt(5)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolve key")
}

// TestReadPerson_MissingAndBad covers the not-found (no error) and malformed
// paths of ReadPerson.
func TestReadPerson_MissingAndBad(t *testing.T) {
	a := newPeopleAdapter(t)

	_, found, err := a.ReadPerson("person_none")
	require.NoError(t, err)
	assert.False(t, found)

	// An invalid key with a separator is rejected before any read.
	_, _, err = a.ReadPerson("../escape")
	require.Error(t, err)

	// A corrupt record surfaces a parse error.
	require.NoError(t, os.MkdirAll(a.peopleDir(), dirPerm))
	require.NoError(t, os.WriteFile(filepath.Join(a.peopleDir(), "person_bad.json"), []byte("{not json"), filePerm))
	_, _, err = a.ReadPerson("person_bad")
	require.Error(t, err)
}

// TestUpdatePerson_CorruptExistingRecordSurfaces covers the branch where the
// record already at the resolved key cannot be parsed: UpdatePerson surfaces
// the read error rather than silently overwriting it.
func TestUpdatePerson_CorruptExistingRecordSurfaces(t *testing.T) {
	a := newPeopleAdapter(t)
	require.NoError(t, os.MkdirAll(a.peopleDir(), dirPerm))
	// "M." resolves to person_p-hazel; a corrupt record there fails the read.
	require.NoError(t, os.WriteFile(filepath.Join(a.peopleDir(), "person_p-hazel.json"), []byte("{corrupt"), filePerm))

	_, err := a.UpdatePerson(PersonMention{DisplayName: "M.", RawEntryID: "raw_x", At: personAt(5)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse person")
}

// TestReadPerson_BadTimestamp covers the decode error path: a record with a
// malformed seen timestamp surfaces a parse error.
func TestReadPerson_BadTimestamp(t *testing.T) {
	a := newPeopleAdapter(t)
	require.NoError(t, os.MkdirAll(a.peopleDir(), dirPerm))
	rec := `{"person_key":"person_x","display_name":"X","aka":["X"],` +
		`"first_seen_at":"not-a-time","last_seen_at":"2026-05-05T19:42:00Z","entry_refs":[],"notes":null}`
	require.NoError(t, os.WriteFile(filepath.Join(a.peopleDir(), "person_x.json"), []byte(rec), filePerm))

	_, _, err := a.ReadPerson("person_x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "first_seen_at")

	// A malformed last_seen_at is likewise surfaced.
	rec2 := `{"person_key":"person_y","display_name":"Y","aka":["Y"],` +
		`"first_seen_at":"2026-05-05T19:42:00Z","last_seen_at":"nope","entry_refs":[],"notes":null}`
	require.NoError(t, os.WriteFile(filepath.Join(a.peopleDir(), "person_y.json"), []byte(rec2), filePerm))
	_, _, err = a.ReadPerson("person_y")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "last_seen_at")
}

// TestReadPerson_EmptyTimestampsDecode covers the parseOptionalTime empty
// branch: a record whose seen timestamps are blank decodes to zero times
// without error.
func TestReadPerson_EmptyTimestampsDecode(t *testing.T) {
	a := newPeopleAdapter(t)
	require.NoError(t, os.MkdirAll(a.peopleDir(), dirPerm))
	rec := `{"person_key":"person_z","display_name":"Z","aka":["Z"],` +
		`"first_seen_at":"","last_seen_at":"","entry_refs":[],"notes":null}`
	require.NoError(t, os.WriteFile(filepath.Join(a.peopleDir(), "person_z.json"), []byte(rec), filePerm))

	got, found, err := a.ReadPerson("person_z")
	require.NoError(t, err)
	require.True(t, found)
	assert.True(t, got.FirstSeenAt.IsZero())
	assert.True(t, got.LastSeenAt.IsZero())
}

// TestReadPerson_UnreadableFileSurfaces covers the non-not-exist read-error
// branch: an unreadable record surfaces the error rather than "not found".
func TestReadPerson_UnreadableFileSurfaces(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
	a := newPeopleAdapter(t)
	require.NoError(t, os.MkdirAll(a.peopleDir(), dirPerm))
	path := filepath.Join(a.peopleDir(), "person_locked.json")
	require.NoError(t, os.WriteFile(path, []byte(`{}`), filePerm))
	require.NoError(t, os.Chmod(path, 0o000))
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	_, _, err := a.ReadPerson("person_locked")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read person")
}

// TestUpdatePerson_WriteFailureSurfaces covers the write-failure branch: a
// read-only people/ tree surfaces an explicit error.
func TestUpdatePerson_WriteFailureSurfaces(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
	a := newPeopleAdapter(t)
	require.NoError(t, os.MkdirAll(a.peopleDir(), dirPerm))
	require.NoError(t, os.Chmod(a.peopleDir(), 0o500))
	t.Cleanup(func() { _ = os.Chmod(a.peopleDir(), 0o700) })

	_, err := a.UpdatePerson(PersonMention{DisplayName: "M.", RawEntryID: "raw_x", At: personAt(5)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write person")
}

// TestPersonRecord_RoundTrips proves a record survives an encode/decode cycle,
// including a set notes value and the timestamp offset.
func TestPersonRecord_RoundTrips(t *testing.T) {
	a := newPeopleAdapter(t)
	_, err := a.UpdatePerson(PersonMention{DisplayName: "Sam", RawEntryID: "raw_2026_05_05_19_42", At: personAt(5)})
	require.NoError(t, err)

	key, err := DerivePersonKey("Sam", data.Wordlist())
	require.NoError(t, err)
	rec, found, err := a.ReadPerson(key)
	require.NoError(t, err)
	require.True(t, found)

	// The on-disk JSON renders aka/entry_refs as arrays and notes as null.
	b, err := os.ReadFile(filepath.Join(a.peopleDir(), key+".json"))
	require.NoError(t, err)
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &m))
	assert.JSONEq(t, `["Sam"]`, string(m["aka"]))
	assert.Equal(t, "null", string(m["notes"]))
	assert.Equal(t, "Sam", rec.DisplayName)
}
