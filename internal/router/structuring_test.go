package router

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/storage"
)

// mKey is the golden person_key for the synthetic name "M." (pinned by the
// storage person_key derivation tests).
const mKey = "person_p-hazel"

// structExtraction is a well-formed extraction reply mentioning "M.".
const structExtraction = `{
  "emotions": [{"name": "annoyed", "rationale": "user said 'annoyed'"}],
  "themes":   [{"name": "voice-not-heard", "rationale": "pushed back then dropped it"}],
  "people":   [{"display_name": "M."}],
  "notes": null
}`

// writeRawAt writes a synthetic raw entry with the given body at instant at,
// returning its id and on-disk path. It stands in for a prior /log or
// /checkin capture that Structuring now runs over downstream.
func writeRawAt(t *testing.T, a *storage.Adapter, home, body string, at time.Time) (id, path string) {
	t.Helper()
	res, err := a.WriteRaw(storage.RawEntry{
		RecordedAt:          at,
		OccurredAt:          at,
		OccurredAtPrecision: storage.PrecisionExact,
		Source:              "cli",
		Command:             "/log",
		Body:                body,
	})
	require.NoError(t, err)
	path = filepath.Join(home, "raw", fmt.Sprintf("%04d", at.Year()), fmt.Sprintf("%02d", int(at.Month())), res.RawID+".md")
	return res.RawID, path
}

// structReq wires a Structuring request at the fixed instant.
func structReq(rawID string, p provider.Provider) StructureRequest {
	return StructureRequest{RawID: rawID, Now: fixedNow(), Provider: p}
}

// extractEx is a scripted extraction completion.
func extractEx(jsonBody string) provider.Exchange { return provider.Exchange{Content: jsonBody} }

// readProcessedMap reads processed/<id>.json into a generic map for shape
// assertions.
func readProcessedMap(t *testing.T, home, id string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, "processed", id+".json"))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	return m
}

// TestStructure_FirstMention is acceptance case 4.1: the first mention of "M."
// produces a validated artifact with a back-filled person_key and
// first_mention true, plus the people/<key>.json record from update_person.
func TestStructure_FirstMention(t *testing.T) {
	r, a, home := newBootedRouter(t)
	id, _ := writeRawAt(t, a, home, "Dinner with M. I pushed back then dropped it.", fixedNow())
	p := &provider.Fake{Script: []provider.Exchange{extractEx(structExtraction)}}

	res, err := r.Structure(context.Background(), structReq(id, p))
	require.NoError(t, err)
	assert.True(t, res.Wrote)
	assert.Equal(t, id, res.ProcessedID)
	require.Len(t, res.People, 1)
	assert.Equal(t, mKey, res.People[0].PersonKey)
	assert.True(t, res.People[0].FirstMention)

	// The artifact validates and carries the resolved key + a structuring stamp.
	b, err := os.ReadFile(filepath.Join(home, "processed", id+".json"))
	require.NoError(t, err)
	require.NoError(t, storage.ValidateProcessedArtifact(b))
	m := readProcessedMap(t, home, id)
	assert.Equal(t, id, m["id"])
	assert.Equal(t, id, m["entry_id"])
	assert.Contains(t, m["agent_version"], "structuring-")
	people := m["people"].([]any)
	require.Len(t, people, 1)
	person := people[0].(map[string]any)
	assert.Equal(t, "M.", person["display_name"])
	assert.Equal(t, mKey, person["person_key"])
	assert.Equal(t, true, person["first_mention"])

	// update_person wrote people/person_p-hazel.json.
	_, statErr := os.Stat(filepath.Join(home, "people", mKey+".json"))
	require.NoError(t, statErr)
}

// TestStructure_SecondMention is acceptance case 4.2: a second entry
// mentioning the same person (different spelling) is first_mention false and
// merges aka on the people record.
func TestStructure_SecondMention(t *testing.T) {
	r, a, home := newBootedRouter(t)

	// First entry introduces M.
	id1, _ := writeRawAt(t, a, home, "First dinner with M.", time.Date(2026, time.May, 3, 20, 0, 0, 0, time.UTC))
	p1 := &provider.Fake{Script: []provider.Exchange{extractEx(structExtraction)}}
	_, err := r.Structure(context.Background(), StructureRequest{RawID: id1, Now: fixedNow(), Provider: p1})
	require.NoError(t, err)

	// Second entry, later, spells the name "M".
	id2, _ := writeRawAt(t, a, home, "Saw M again today.", time.Date(2026, time.May, 9, 20, 10, 0, 0, time.UTC))
	second := `{"emotions":[],"themes":[],"people":[{"display_name":"M"}],"notes":"brief mention"}`
	p2 := &provider.Fake{Script: []provider.Exchange{extractEx(second)}}
	res, err := r.Structure(context.Background(), StructureRequest{RawID: id2, Now: fixedNow(), Provider: p2})
	require.NoError(t, err)
	require.Len(t, res.People, 1)
	assert.Equal(t, mKey, res.People[0].PersonKey)
	assert.False(t, res.People[0].FirstMention)

	// aka[] carries both spellings; entry_refs carries both entries.
	rec, found, err := a.ReadPerson(mKey)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, []string{"M", "M."}, rec.Aka)
	assert.Equal(t, []string{id1, id2}, rec.EntryRefs)
}

// TestStructure_IdempotentDiffOnlyProducedAt is acceptance case 4.3: rerunning
// Structuring overwrites the artifact with a diff only in produced_at, leaves
// the raw entry untouched, and does not duplicate the people record.
func TestStructure_IdempotentDiffOnlyProducedAt(t *testing.T) {
	r, a, home := newBootedRouter(t)
	id, rawPath := writeRawAt(t, a, home, "Dinner with M.", fixedNow())

	rawBefore, err := os.ReadFile(rawPath)
	require.NoError(t, err)

	p1 := &provider.Fake{Script: []provider.Exchange{extractEx(structExtraction)}}
	_, err = r.Structure(context.Background(), StructureRequest{RawID: id, Now: fixedNow(), Provider: p1})
	require.NoError(t, err)
	first := readProcessedMap(t, home, id)

	// Rerun at a later instant.
	later := fixedNow().Add(2 * time.Hour)
	p2 := &provider.Fake{Script: []provider.Exchange{extractEx(structExtraction)}}
	_, err = r.Structure(context.Background(), StructureRequest{RawID: id, Now: later, Provider: p2})
	require.NoError(t, err)
	second := readProcessedMap(t, home, id)

	// produced_at differs; every other field is identical.
	assert.NotEqual(t, first["produced_at"], second["produced_at"])
	delete(first, "produced_at")
	delete(second, "produced_at")
	assert.Equal(t, first, second, "reruns must differ only in produced_at")

	// The raw entry is byte-identical.
	rawAfter, err := os.ReadFile(rawPath)
	require.NoError(t, err)
	assert.Equal(t, rawBefore, rawAfter, "Structuring must never modify the raw entry")

	// Exactly one people record, one entry ref.
	assert.Equal(t, 1, countFiles(t, home, "people"))
	rec, _, err := a.ReadPerson(mKey)
	require.NoError(t, err)
	assert.Equal(t, []string{id}, rec.EntryRefs)
}

// TestStructure_EmptyBody is acceptance case 4.4: an empty body yields empty
// arrays + notes "raw body empty", makes no model call, and changes no people
// record.
func TestStructure_EmptyBody(t *testing.T) {
	r, a, home := newBootedRouter(t)
	id, _ := writeRawAt(t, a, home, "", fixedNow())
	p := &provider.Fake{} // no scripted calls — none expected

	res, err := r.Structure(context.Background(), structReq(id, p))
	require.NoError(t, err)
	assert.True(t, res.Wrote)
	assert.Equal(t, 0, p.Calls())

	m := readProcessedMap(t, home, id)
	assert.Empty(t, m["emotions"])
	assert.Empty(t, m["themes"])
	assert.Empty(t, m["people"])
	assert.Equal(t, storage.NotesRawBodyEmpty, m["notes"])
	assert.Equal(t, 0, countFiles(t, home, "people"))
}

// TestStructure_MalformedTwice is acceptance case 4.5: malformed JSON twice
// yields empty arrays + notes "structuring failed (parse)"; the artifact is
// still written.
func TestStructure_MalformedTwice(t *testing.T) {
	r, a, home := newBootedRouter(t)
	id, _ := writeRawAt(t, a, home, "some real content", fixedNow())
	p := &provider.Fake{Script: []provider.Exchange{extractEx("garbage"), extractEx("still garbage")}}

	res, err := r.Structure(context.Background(), structReq(id, p))
	require.NoError(t, err)
	assert.True(t, res.Wrote)

	m := readProcessedMap(t, home, id)
	assert.Empty(t, m["people"])
	assert.Equal(t, storage.NotesStructuringFailed, m["notes"])
}

// TestStructure_DiagnosticNotesRetriedThenClean is acceptance case 4.6: a
// first reply carrying diagnostic notes is rejected, and a clean retry is
// accepted and written with the resolved person.
func TestStructure_DiagnosticNotesRetriedThenClean(t *testing.T) {
	r, a, home := newBootedRouter(t)
	id, _ := writeRawAt(t, a, home, "dinner with M.", fixedNow())
	diagnostic := `{"emotions":[],"themes":[],"people":[{"display_name":"M."}],"notes":"anxious attachment showing"}`
	clean := `{"emotions":[],"themes":[],"people":[{"display_name":"M."}],"notes":"mentions M. once"}`
	p := &provider.Fake{Script: []provider.Exchange{extractEx(diagnostic), extractEx(clean)}}

	res, err := r.Structure(context.Background(), structReq(id, p))
	require.NoError(t, err)
	assert.True(t, res.Wrote)
	require.Len(t, res.People, 1)
	assert.Equal(t, mKey, res.People[0].PersonKey)

	m := readProcessedMap(t, home, id)
	assert.Equal(t, "mentions M. once", m["notes"])
}

// TestStructure_DeduplicatesRepeatedMention proves a name mentioned twice in
// one entry yields exactly one people[] entry (deduplicated by resolved key).
func TestStructure_DeduplicatesRepeatedMention(t *testing.T) {
	r, a, home := newBootedRouter(t)
	id, _ := writeRawAt(t, a, home, "M. and M. again", fixedNow())
	twice := `{"emotions":[],"themes":[],"people":[{"display_name":"M."},{"display_name":"M"}],"notes":"x"}`
	p := &provider.Fake{Script: []provider.Exchange{extractEx(twice)}}

	res, err := r.Structure(context.Background(), structReq(id, p))
	require.NoError(t, err)
	assert.Len(t, res.People, 1, "the same person mentioned twice collapses to one entry")

	m := readProcessedMap(t, home, id)
	assert.Len(t, m["people"].([]any), 1)
}

// TestStructure_SchemaSweep is the acceptance-criteria.md Phase 4 jq sweep:
// every processed artifact on disk validates and no person_key is null,
// across the happy, empty-body, and degraded paths in one Ledger.
func TestStructure_SchemaSweep(t *testing.T) {
	r, a, home := newBootedRouter(t)

	writes := []struct {
		body   string
		script []provider.Exchange
	}{
		{"Dinner with M.", []provider.Exchange{extractEx(structExtraction)}},
		{"", nil},
		{"content", []provider.Exchange{extractEx("garbage"), extractEx("garbage")}},
		{"Quiet day.", []provider.Exchange{extractEx(`{"emotions":[{"name":"calm","rationale":"'felt fine'"}],"themes":[],"people":[],"notes":null}`)}},
	}
	at := fixedNow()
	for i, w := range writes {
		id, _ := writeRawAt(t, a, home, w.body, at.Add(time.Duration(i)*time.Minute))
		p := &provider.Fake{Script: w.script}
		_, err := r.Structure(context.Background(), StructureRequest{RawID: id, Now: fixedNow(), Provider: p})
		require.NoErrorf(t, err, "write %d", i)
	}

	// Sweep every processed artifact: schema valid, no null person_key.
	procRoot := filepath.Join(home, "processed")
	var seen int
	err := filepath.WalkDir(procRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		b, rErr := os.ReadFile(path)
		require.NoError(t, rErr)
		require.NoErrorf(t, storage.ValidateProcessedArtifact(b), "artifact %s failed schema", d.Name())
		var m map[string]any
		require.NoError(t, json.Unmarshal(b, &m))
		for _, pRaw := range m["people"].([]any) {
			person := pRaw.(map[string]any)
			assert.NotNil(t, person["person_key"])
			assert.NotEmpty(t, person["person_key"])
		}
		seen++
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, 4, seen)
}

// TestStructure_ReadRawErrorSurfaces confirms a missing raw entry is an
// infrastructure fault (error), not a silent degrade.
func TestStructure_ReadRawErrorSurfaces(t *testing.T) {
	r, _, _ := newBootedRouter(t)
	p := &provider.Fake{Script: []provider.Exchange{extractEx(structExtraction)}}
	_, err := r.Structure(context.Background(), structReq("raw_2026_05_05_00_00", p))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read raw")
}

// TestStructure_PeopleRoutineFailureDegrades is error-states.md §S-5: when
// update_person cannot write, the pass degrades — no artifact, raw stays
// captured, replayable.
func TestStructure_PeopleRoutineFailureDegrades(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
	r, a, home := newBootedRouter(t)
	id, _ := writeRawAt(t, a, home, "dinner with M.", fixedNow())
	peopleDir := filepath.Join(home, "people")
	require.NoError(t, os.Chmod(peopleDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(peopleDir, 0o700) })

	p := &provider.Fake{Script: []provider.Exchange{extractEx(structExtraction)}}
	res, err := r.Structure(context.Background(), structReq(id, p))
	require.NoError(t, err)
	assert.False(t, res.Wrote)
	assert.True(t, res.Degraded)
	assert.Contains(t, res.Ack, "trouble organizing the people")
	assert.Equal(t, 0, countFiles(t, home, "processed"))
}

// TestStructure_WriteProcessedFailureDegrades is error-states.md §St-3: when
// write_processed fails, the entry is captured-but-unprocessed and the pass is
// replayable. An entry with no people isolates the processed-write failure.
func TestStructure_WriteProcessedFailureDegrades(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
	r, a, home := newBootedRouter(t)
	id, _ := writeRawAt(t, a, home, "a quiet, personless day", fixedNow())
	procDir := filepath.Join(home, "processed")
	require.NoError(t, os.Chmod(procDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(procDir, 0o700) })

	noPeople := `{"emotions":[{"name":"calm","rationale":"'quiet'"}],"themes":[],"people":[],"notes":null}`
	p := &provider.Fake{Script: []provider.Exchange{extractEx(noPeople)}}
	res, err := r.Structure(context.Background(), structReq(id, p))
	require.NoError(t, err)
	assert.False(t, res.Wrote)
	assert.True(t, res.Degraded)
	assert.Contains(t, res.Ack, "catch up on the rest later")
}

// TestStructure_ZeroNowUsesWallClock covers the default-clock branch.
func TestStructure_ZeroNowUsesWallClock(t *testing.T) {
	r, a, home := newBootedRouter(t)
	id, _ := writeRawAt(t, a, home, "Dinner with M.", fixedNow())
	p := &provider.Fake{Script: []provider.Exchange{extractEx(structExtraction)}}
	res, err := r.Structure(context.Background(), StructureRequest{RawID: id, Provider: p}) // no Now
	require.NoError(t, err)
	assert.True(t, res.Wrote)
}

// TestEntryOccurredAt covers the frontmatter timestamp reader across the
// shapes yaml may hand back (time.Time, RFC3339 string), plus the missing and
// unparseable fallbacks.
func TestEntryOccurredAt(t *testing.T) {
	fb := fixedNow()
	tt := time.Date(2026, time.May, 5, 18, 0, 0, 0, time.UTC)

	assert.Equal(t, tt, entryOccurredAt(map[string]any{"occurred_at": tt}, fb))
	assert.Equal(t, tt, entryOccurredAt(map[string]any{"occurred_at": tt.Format(time.RFC3339)}, fb))
	assert.Equal(t, fb, entryOccurredAt(map[string]any{}, fb))
	assert.Equal(t, fb, entryOccurredAt(map[string]any{"occurred_at": "not-a-date"}, fb))
	assert.Equal(t, fb, entryOccurredAt(map[string]any{"occurred_at": 42}, fb))
}
