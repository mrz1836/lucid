package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unicode/utf8"

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

// writePersonTombstone writes a redirect tombstone at key pointing forward to
// target — the on-disk shape a merge produces, hand-authored so the read/write
// tests do not depend on the Phase 2 curation verbs.
func writePersonTombstone(t *testing.T, a *Adapter, key, display, target string, at time.Time) {
	t.Helper()
	require.NoError(t, os.MkdirAll(a.peopleDir(), dirPerm))
	rec := personRecordJSON{
		PersonKey:   key,
		DisplayName: display,
		Aka:         []string{display},
		FirstSeenAt: at.Format(time.RFC3339),
		LastSeenAt:  at.Format(time.RFC3339),
		EntryRefs:   []string{"raw_2026_05_01_09_00"},
		RedirectTo:  target,
	}
	b, err := marshalJSON(rec)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(a.peopleDir(), key+".json"), b, filePerm))
}

// TestResolvePersonRedirect covers the three cases: an absent key and a
// canonical record resolve to themselves; a tombstone resolves forward to its
// target (single hop).
func TestResolvePersonRedirect(t *testing.T) {
	a := newPeopleAdapter(t)

	// Absent → identity.
	got, err := a.ResolvePersonRedirect("person_none")
	require.NoError(t, err)
	assert.Equal(t, "person_none", got)

	// Canonical → identity.
	keyAlex := seedPersonStore(t, a, "Alex", "raw_2026_05_05_19_42", personAt(5))
	got, err = a.ResolvePersonRedirect(keyAlex)
	require.NoError(t, err)
	assert.Equal(t, keyAlex, got)

	// Tombstone → target.
	keyAndy, err := DerivePersonKey("Andy", data.Wordlist())
	require.NoError(t, err)
	require.NotEqual(t, keyAlex, keyAndy, "the fixture needs two distinct slugs")
	writePersonTombstone(t, a, keyAndy, "Andy", keyAlex, personAt(4))
	got, err = a.ResolvePersonRedirect(keyAndy)
	require.NoError(t, err)
	assert.Equal(t, keyAlex, got)
}

// TestUpdatePerson_FollowsRedirect proves a mention whose derived slug is a
// tombstone folds into the canonical record it points to, rather than reviving
// the tombstone or minting a new key.
func TestUpdatePerson_FollowsRedirect(t *testing.T) {
	a := newPeopleAdapter(t)
	keyAlex := seedPersonStore(t, a, "Alex", "raw_2026_05_05_19_42", personAt(5))

	// A merge has already turned Andy's slug into a tombstone forwarding to Alex.
	keyAndy, err := DerivePersonKey("Andy", data.Wordlist())
	require.NoError(t, err)
	writePersonTombstone(t, a, keyAndy, "Andy", keyAlex, personAt(4))

	// A later bare "Andy" mention lands on the canonical Alex record.
	res, err := a.UpdatePerson(PersonMention{DisplayName: "Andy", RawEntryID: "raw_2026_05_10_08_00", At: personAt(10)})
	require.NoError(t, err)
	assert.Equal(t, keyAlex, res.PersonKey, "the mention folds into the canonical, not the tombstone")

	rec, found, err := a.ReadPerson(keyAlex)
	require.NoError(t, err)
	require.True(t, found)
	assert.Contains(t, rec.EntryRefs, "raw_2026_05_10_08_00", "the canonical absorbs the new ref")
	assert.Contains(t, rec.Aka, "Andy", "the folded form is recorded on the canonical")

	// The tombstone is untouched — still a frozen forward pointer.
	tomb, found, err := a.ReadPerson(keyAndy)
	require.NoError(t, err)
	require.True(t, found)
	assert.True(t, tomb.IsTombstone())
	assert.Equal(t, keyAlex, tomb.RedirectTo)
	assert.NotContains(t, tomb.EntryRefs, "raw_2026_05_10_08_00", "resolution never writes the tombstone")
}

// TestUpdatePerson_SuffixBumpsPastTombstone proves a tombstone still witnesses
// the slug it occupies: a differently-normalized name deriving to the same base
// key bumps to the -2 suffix instead of colliding with (or resolving through)
// the tombstone.
func TestUpdatePerson_SuffixBumpsPastTombstone(t *testing.T) {
	a := newPeopleAdapter(t)

	// A canonical target for the tombstone to forward to.
	keyTarget := seedPersonStore(t, a, "Sam", "raw_2026_05_02_09_00", personAt(2))

	// A tombstone at person_p-hazel owned by the normalized name "other".
	// "M." derives to person_p-hazel too, but is a different person.
	writePersonTombstone(t, a, "person_p-hazel", "Other", keyTarget, personAt(1))

	res, err := a.UpdatePerson(PersonMention{DisplayName: "M.", RawEntryID: "raw_2026_05_05_19_42", At: personAt(5)})
	require.NoError(t, err)
	assert.Equal(t, "person_p-hazel-2", res.PersonKey, "a distinct name bumps past the tombstone")
	assert.True(t, res.FirstMention)

	// The tombstone is untouched, and "M." got its own fresh record.
	tomb, _, err := a.ReadPerson("person_p-hazel")
	require.NoError(t, err)
	assert.True(t, tomb.IsTombstone())
	rec, found, err := a.ReadPerson("person_p-hazel-2")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "M.", rec.DisplayName)
	assert.False(t, rec.IsTombstone())
}

// TestPersonRecord_TombstoneAndEnrichedRoundTrip proves the three new fields
// survive an encode/decode cycle, and that the on-disk JSON carries redirect_to
// / dob / relationship only when set (omitempty) while notes stays always
// present — the byte-stability contract the fuzz test also guards.
func TestPersonRecord_TombstoneAndEnrichedRoundTrip(t *testing.T) {
	a := newPeopleAdapter(t)
	require.NoError(t, os.MkdirAll(a.peopleDir(), dirPerm))

	dob, rel := "1990-04-12", "colleague"
	notes := "met at the co-op"
	enriched := PersonRecord{
		PersonKey: "person_a-alex", DisplayName: "Alexandra", Aka: []string{"Alex", "Alexandra"},
		FirstSeenAt: personAt(5), LastSeenAt: personAt(9), EntryRefs: []string{"raw_2026_05_05_19_42"},
		Dob: &dob, Relationship: &rel, Notes: &notes,
	}
	b, err := marshalJSON(enriched.encode())
	require.NoError(t, err)
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &m))
	assert.Equal(t, `"1990-04-12"`, string(m["dob"]))
	assert.Equal(t, `"colleague"`, string(m["relationship"]))
	_, hasRedirect := m["redirect_to"]
	assert.False(t, hasRedirect, "a canonical record omits redirect_to")

	// A plain canonical record omits all three optional identity fields but keeps
	// notes: null.
	plain := PersonRecord{
		PersonKey: "person_a-plain", DisplayName: "Andy", Aka: []string{"Andy"},
		FirstSeenAt: personAt(5), LastSeenAt: personAt(5), EntryRefs: []string{"raw_2026_05_05_19_42"},
	}
	pb, err := marshalJSON(plain.encode())
	require.NoError(t, err)
	var pm map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(pb, &pm))
	for _, absent := range []string{"redirect_to", "dob", "relationship"} {
		_, ok := pm[absent]
		assert.Falsef(t, ok, "a plain record must omit %q", absent)
	}
	_, hasNotes := pm["notes"]
	assert.True(t, hasNotes, "notes stays always-present")
}

// FuzzPersonRecordRoundTrip guards the omitempty fixed point: encode→decode→
// encode is byte-stable for any field combination, and an unset optional field
// (redirect_to / dob / relationship) never serializes, so a pre-feature record
// rewrites byte-identically (S-22). A reviewer flipping any of the three to
// always-present breaks this test.
func FuzzPersonRecordRoundTrip(f *testing.F) {
	f.Add("person_a-alex", "Alex", "raw_1", "", "", "", false)
	f.Add("person_a-andy", "Andy", "raw_2", "person_a-alex", "", "", false)
	f.Add("person_a-alex", "Alexandra", "raw_3", "", "1990-04-12", "colleague", true)
	f.Add("dobson", "relationship", "redirect_to", "", "", "", true) // adversarial: values echo field names

	f.Fuzz(func(t *testing.T, key, display, ref, redirect, dob, rel string, hasNotes bool) {
		// Records on disk are always valid UTF-8 (the encoder produced them);
		// invalid UTF-8 is normalized asymmetrically by encoding/json and is out
		// of scope for the on-disk fixed point.
		for _, s := range []string{key, display, ref, redirect, dob, rel} {
			if !utf8.ValidString(s) {
				t.Skip()
			}
		}

		rec := PersonRecord{
			PersonKey:   key,
			DisplayName: display,
			Aka:         []string{display},
			FirstSeenAt: personAt(5),
			LastSeenAt:  personAt(5),
			EntryRefs:   []string{ref},
			RedirectTo:  redirect,
		}
		if dob != "" {
			rec.Dob = &dob
		}
		if rel != "" {
			rec.Relationship = &rel
		}
		if hasNotes {
			n := "note"
			rec.Notes = &n
		}

		b1, err := marshalJSON(rec.encode())
		require.NoError(t, err)

		var j personRecordJSON
		require.NoError(t, json.Unmarshal(b1, &j))
		dec, ok, err := j.decode()
		require.NoError(t, err)
		require.True(t, ok)
		b2, err := marshalJSON(dec.encode())
		require.NoError(t, err)
		assert.Equal(t, string(b1), string(b2), "encode→decode→encode must be a byte-stable fixed point")

		// The omitempty contract, checked structurally (robust to fuzzed values
		// that happen to echo a field name).
		var m map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(b1, &m))
		if redirect == "" {
			_, present := m["redirect_to"]
			assert.False(t, present, "an unset redirect_to must not serialize")
		}
		if dob == "" {
			_, present := m["dob"]
			assert.False(t, present, "an unset dob must not serialize")
		}
		if rel == "" {
			_, present := m["relationship"]
			assert.False(t, present, "an unset relationship must not serialize")
		}
		_, notesPresent := m["notes"]
		assert.True(t, notesPresent, "notes stays always-present")
	})
}

// seedPersonStore records one mention via update_person and returns the
// resolved key — the storage-package analog of the router test's seedPerson.
func seedPersonStore(t *testing.T, a *Adapter, display, rawID string, at time.Time) string {
	t.Helper()
	res, err := a.UpdatePerson(PersonMention{DisplayName: display, RawEntryID: rawID, At: at})
	require.NoError(t, err)
	return res.PersonKey
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
