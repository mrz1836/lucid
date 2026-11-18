package storage

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/identity"
)

// The move-ref and fold fixtures use synthetic people only: two people named
// "Sam" ("Sam" and "Sam Rivera"), plus "Jordan Lee" and "Ghost".

// moveRefAt is the n-th fixed instant of the move-ref storage tests.
func moveRefAt(n int) time.Time {
	return time.Date(2026, time.November, 18, 9, n, 0, 0, time.FixedZone("EST", -5*3600))
}

// writeRawT captures a raw entry at the n-th fixture instant and returns its
// id.
func writeRawT(t *testing.T, a *Adapter, n int) string {
	t.Helper()
	res, err := a.WriteRaw(RawEntry{
		RecordedAt: moveRefAt(n), OccurredAt: moveRefAt(n), OccurredAtPrecision: PrecisionExact,
		Source: "cli", Command: "/log", Body: "A synthetic entry.",
	})
	require.NoError(t, err)
	return res.RawID
}

// writeArtifactT writes a processed artifact for raw with the given people
// and held mentions.
func writeArtifactT(t *testing.T, a *Adapter, raw string, people []ProcessedPerson, held []HeldPerson) {
	t.Helper()
	notes := "a synthetic entry"
	require.NoError(t, a.WriteProcessed(ProcessedArtifact{
		ID: raw, EntryID: raw, ProducedAt: moveRefAt(0), AgentVersion: "structuring-2026.05.0",
		People: people, HeldPeople: held, Notes: &notes,
	}))
}

// mentionT records one mention of display in raw through the People routine
// and returns the key it resolved to.
func mentionT(t *testing.T, a *Adapter, display, raw string) string {
	t.Helper()
	res, err := a.UpdatePerson(PersonMention{DisplayName: display, RawEntryID: raw, At: personAt(3)})
	require.NoError(t, err)
	require.False(t, res.Held)
	return res.PersonKey
}

// readPersonT reads a live record.
func readPersonT(t *testing.T, a *Adapter, key string) PersonRecord {
	t.Helper()
	rec, found, err := a.ReadPerson(key)
	require.NoError(t, err)
	require.True(t, found)
	return rec
}

// TestRawExists_NamesOnlyCapturedEntries: a captured raw id exists; an absent
// one, a malformed one, and one carrying a path separator never do — and none
// of them is an error.
func TestRawExists_NamesOnlyCapturedEntries(t *testing.T) {
	a := New(t.TempDir())
	raw := writeRawT(t, a, 1)
	for _, tc := range []struct {
		id   string
		want bool
	}{
		{id: raw, want: true},
		{id: "raw_2026_01_01_00_00", want: false},
		{id: "raw_2026", want: false},
		{id: "raw_20x6_11_18_09_01", want: false},
		{id: "raw_2026_1a_18_09_01", want: false},
		{id: "raw__11_18_09_01_x", want: false},
		{id: "raw_2026_11_../../x_y", want: false},
		{id: `raw_2026_11_18\09_01`, want: false},
		{id: "", want: false},
	} {
		got, err := a.RawExists(tc.id)
		require.NoError(t, err, tc.id)
		assert.Equal(t, tc.want, got, tc.id)
	}
}

// TestRawExists_StatFailureSurfaces: a raw shard that cannot be read is an
// error, not a silent "no entry".
func TestRawExists_StatFailureSurfaces(t *testing.T) {
	skipIfRoot(t)
	home := t.TempDir()
	a := New(home)
	raw := writeRawT(t, a, 1)
	shard := filepath.Join(home, rawDirName, "2026", "11")
	require.NoError(t, os.Chmod(shard, 0o000))
	t.Cleanup(func() { _ = os.Chmod(shard, 0o700) })
	_, err := a.RawExists(raw)
	require.Error(t, err)
	_, err = a.MoveRef(raw, "", "person_x-none", moveRefAt(2))
	require.Error(t, err)
}

// TestMentionFold_NoLogReadsAsStored: without the identity log the fold is
// the artifacts and entry_refs exactly as stored — mention keys canonicalized
// through merge redirects, references unchanged — and building it creates
// nothing.
func TestMentionFold_NoLogReadsAsStored(t *testing.T) {
	a := New(t.TempDir())
	raw1, raw2 := writeRawT(t, a, 1), writeRawT(t, a, 2)
	sam := mentionT(t, a, "Sam", raw1)
	ghost := mentionT(t, a, "Ghost", raw2)
	writeArtifactT(t, a, raw1, []ProcessedPerson{{DisplayName: "Sam", PersonKey: sam}}, nil)
	writeArtifactT(t, a, raw2, []ProcessedPerson{{DisplayName: "Ghost", PersonKey: ghost}, {DisplayName: "Sam", PersonKey: sam}}, nil)
	_, err := a.MergePersons(ghost, sam)
	require.NoError(t, err)

	fold, err := a.MentionFold()
	require.NoError(t, err)
	art, err := a.ReadProcessed(raw2)
	require.NoError(t, err)
	assert.Equal(t, []string{sam}, fold.MentionKeys(art), "the merged-away key folds onto the canonical record")
	assert.Equal(t, ghost, fold.Mentions(art)[0].StoredKey)
	assert.Equal(t, sam, fold.Mentions(art)[0].PersonKey)
	assert.Equal(t, sam, fold.Canon(ghost))
	assert.Empty(t, fold.Canon(""))
	assert.Empty(t, MentionFold{}.Canon(""))
	assert.Equal(t, "x", MentionFold{}.Canon("x"))

	rec := readPersonT(t, a, sam)
	assert.Equal(t, rec.EntryRefs, fold.EntryRefs(rec))
	refs, err := a.FoldedEntryRefs(rec)
	require.NoError(t, err)
	assert.Equal(t, rec.EntryRefs, refs)
	keys, err := a.FoldedMentionKeys(art)
	require.NoError(t, err)
	assert.Equal(t, []string{sam}, keys)
	assert.False(t, identityLogExists(t, a), "a fold read creates no identity log")
}

// TestMentionFold_PinsAndMovesFoldInOrder: a raw entry's mentions on read are
// its people[] keys (a pin for a mention outranks the stored key) plus each
// pinned held mention, with the raw's move-refs applied in order; a person's
// references are re-derived for every touched raw entry, falling back to the
// stored reference when the touched artifact is missing.
func TestMentionFold_PinsAndMovesFoldInOrder(t *testing.T) {
	a := New(t.TempDir())
	raw1, raw2, raw3, raw4 := writeRawT(t, a, 1), writeRawT(t, a, 2), writeRawT(t, a, 3), writeRawT(t, a, 4)
	sam := mentionT(t, a, "Sam", raw1)
	river := mentionT(t, a, "Sam Rivera", raw2)
	jordan := mentionT(t, a, "Jordan Lee", raw3)
	mentionT(t, a, "Sam", raw4) // raw4's artifact is never written
	writeArtifactT(t, a, raw1, []ProcessedPerson{{DisplayName: "Sam", PersonKey: sam}}, nil)
	writeArtifactT(t, a, raw2, []ProcessedPerson{{DisplayName: "Sam Rivera", PersonKey: river}}, []HeldPerson{
		{DisplayName: "Jo", Reason: identity.HoldExact, Candidates: sortedKeys(jordan, sam)},
		{DisplayName: "Al", Reason: identity.HoldExact, Candidates: sortedKeys(jordan, river)},
	})
	writeArtifactT(t, a, raw3, []ProcessedPerson{{DisplayName: "Jordan Lee", PersonKey: jordan}}, nil)

	at := func(n int) string { return moveRefAt(n).Format(time.RFC3339) }
	appendIdentityT(t, a, identity.NewPin(at(10), identity.SourceCLI, raw2, "Jo", jordan, identity.CauseClaim))
	appendIdentityT(t, a, identity.NewMoveRef(at(11), identity.SourceCLI, raw1, sam, river))
	appendIdentityT(t, a, identity.NewMoveRef(at(12), identity.SourceCLI, raw1, river, jordan))
	appendIdentityT(t, a, identity.NewPin(at(13), identity.SourceCLI, raw3, "Jordan Lee", sam, identity.CauseMoveRef))
	appendIdentityT(t, a, identity.NewMoveRef(at(14), identity.SourceCLI, raw4, sam, river))

	fold, err := a.MentionFold()
	require.NoError(t, err)
	art1, err := a.ReadProcessed(raw1)
	require.NoError(t, err)
	assert.Equal(t, []string{jordan}, fold.MentionKeys(art1), "move-refs apply in order: sam → river → jordan")
	art2, err := a.ReadProcessed(raw2)
	require.NoError(t, err)
	assert.Equal(t, []FoldedMention{
		{DisplayName: "Sam Rivera", StoredKey: river, PersonKey: river},
		{DisplayName: "Jo", StoredKey: jordan, PersonKey: jordan},
	}, fold.Mentions(art2), "the pinned held mention is attributed; the unpinned one is not")
	art3, err := a.ReadProcessed(raw3)
	require.NoError(t, err)
	assert.Equal(t, []string{sam}, fold.MentionKeys(art3), "a pin for a people[] mention outranks its stored key")

	samRec := readPersonT(t, a, sam)
	assert.Equal(t, []string{raw3, raw4}, fold.EntryRefs(samRec), "raw1 moved off; raw3 pinned on; raw4's artifact is missing, so its ref stays")
	assert.Equal(t, []string{raw2}, fold.EntryRefs(readPersonT(t, a, river)))
	assert.Equal(t, []string{raw1, raw2}, fold.EntryRefs(readPersonT(t, a, jordan)), "raw3 moved off by the pin")
	refs, err := a.FoldedEntryRefs(samRec)
	require.NoError(t, err)
	assert.Equal(t, []string{raw3, raw4}, refs)
	keys, err := a.FoldedMentionKeys(art1)
	require.NoError(t, err)
	assert.Equal(t, []string{jordan}, keys)

	// An untouched record reads exactly as stored; a tombstone is never
	// re-derived.
	assert.Empty(t, fold.EntryRefs(PersonRecord{PersonKey: "person_x-none"}))
	tomb := PersonRecord{PersonKey: "person_x-tomb", RedirectTo: sam, EntryRefs: []string{raw3}}
	assert.Equal(t, []string{raw3}, fold.EntryRefs(tomb))
}

// TestMoveRef_StorageRefusalsDecideBeforeWriting: every move-ref refusal is
// decided before anything is appended — an unknown raw entry, a --from or
// --to that is not a live canonical record, the same person twice, no
// mention under --from (including no artifact at all), and, without --from,
// no held mention or several.
func TestMoveRef_StorageRefusalsDecideBeforeWriting(t *testing.T) {
	a := New(t.TempDir())
	raw1, raw2, bare := writeRawT(t, a, 1), writeRawT(t, a, 2), writeRawT(t, a, 3)
	sam := mentionT(t, a, "Sam", raw1)
	river := mentionT(t, a, "Sam Rivera", raw2)
	ghost := mentionT(t, a, "Ghost", raw2)
	_, err := a.MergePersons(ghost, river)
	require.NoError(t, err)
	writeArtifactT(t, a, raw1, []ProcessedPerson{{DisplayName: "Sam", PersonKey: sam}}, nil)
	writeArtifactT(t, a, raw2, []ProcessedPerson{{DisplayName: "Sam Rivera", PersonKey: river}}, nil)
	at := moveRefAt(9).Format(time.RFC3339)
	appendIdentityT(t, a, identity.NewHold(at, identity.SourceStructure, raw2, "Sam", identity.HoldExact, sortedKeys(river, sam)))
	appendIdentityT(t, a, identity.NewHold(at, identity.SourceStructure, raw2, "Al", identity.HoldExact, sortedKeys(river, sam)))
	before, err := os.ReadFile(a.identityPath())
	require.NoError(t, err)

	_, err = a.MoveRef("raw_2026_01_01_00_00", sam, river, moveRefAt(10))
	require.ErrorIs(t, err, ErrMoveRefUnknownRaw)
	_, err = a.MoveRef(raw1, sam, ghost, moveRefAt(10))
	require.ErrorIs(t, err, ErrIdentitySubjectNotCanonical)
	_, err = a.MoveRef(raw1, ghost, river, moveRefAt(10))
	require.ErrorIs(t, err, ErrIdentitySubjectNotCanonical)
	_, err = a.MoveRef(raw1, sam, sam, moveRefAt(10))
	require.ErrorIs(t, err, ErrMoveRefSamePerson)
	_, err = a.MoveRef(raw1, river, sam, moveRefAt(10))
	require.ErrorIs(t, err, ErrMoveRefNoMention)
	_, err = a.MoveRef(bare, sam, river, moveRefAt(10))
	require.ErrorIs(t, err, ErrMoveRefNoMention, "an entry with no artifact has no mention to move")
	_, err = a.MoveRef(raw1, "", river, moveRefAt(10))
	require.ErrorIs(t, err, ErrMoveRefNoHeld)
	_, err = a.MoveRef(raw2, "", sam, moveRefAt(10))
	var several *MoveRefHeldError
	require.ErrorAs(t, err, &several)
	assert.Equal(t, raw2, several.RawID)
	assert.Equal(t, []string{"Al", "Sam"}, several.Forms, "held forms sorted by normalized form")
	assert.Contains(t, several.Error(), "Al, Sam")

	after, err := os.ReadFile(a.identityPath())
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "no refusal appended anything")

	// The happy paths append exactly one event each.
	w, err := a.MoveRef(raw1, sam, river, moveRefAt(11))
	require.NoError(t, err)
	require.Len(t, w.Events, 1)
	assert.Equal(t, identity.KindMoveRef, w.Events[0].Kind)
	assert.Equal(t, w.Receipt, w.Events[0].ID)
	assert.False(t, w.NoOp())
}

// TestMentionFold_ReadErrorsSurface: an unreadable people tree or identity
// log fails the fold — and every read built on it — rather than silently
// reading as stored.
func TestMentionFold_ReadErrorsSurface(t *testing.T) {
	skipIfRoot(t)
	home := t.TempDir()
	a := New(home)
	raw := writeRawT(t, a, 1)
	sam := mentionT(t, a, "Sam", raw)
	writeArtifactT(t, a, raw, []ProcessedPerson{{DisplayName: "Sam", PersonKey: sam}}, nil)
	appendIdentityT(t, a, identity.NewMoveRef(moveRefAt(5).Format(time.RFC3339), identity.SourceCLI, raw, sam, sam+"-2"))
	rec := readPersonT(t, a, sam)

	people := filepath.Join(home, "people")
	require.NoError(t, os.Chmod(people, 0o000))
	t.Cleanup(func() { _ = os.Chmod(people, 0o700) })
	_, err := a.MentionFold()
	require.Error(t, err)
	_, err = a.FoldedEntryRefs(rec)
	require.Error(t, err)
	_, err = a.FoldedMentionKeys(ProcessedArtifact{ID: raw})
	require.Error(t, err)
	require.NoError(t, os.Chmod(people, 0o700))

	require.NoError(t, os.Chmod(a.identityPath(), 0o000))
	t.Cleanup(func() { _ = os.Chmod(a.identityPath(), 0o600) })
	_, err = a.MentionFold()
	require.Error(t, err)
	_, err = a.FoldedEntryRefs(rec)
	require.Error(t, err)
	_, err = a.MoveRef(raw, sam, sam, moveRefAt(6))
	require.Error(t, err)
	require.NoError(t, os.Chmod(a.identityPath(), 0o600))

	processed := filepath.Join(home, "processed", raw+".json")
	require.NoError(t, os.Chmod(processed, 0o000))
	t.Cleanup(func() { _ = os.Chmod(processed, 0o600) })
	_, err = a.MentionFold()
	require.Error(t, err, "a touched artifact that cannot be read fails the fold")
	require.NotErrorIs(t, err, os.ErrNotExist)
}

// sortedKeys returns the keys sorted, as a hold's candidates are stored.
func sortedKeys(keys ...string) []string {
	out := slices.Clone(keys)
	slices.Sort(out)
	return out
}
