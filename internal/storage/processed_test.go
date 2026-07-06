package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// producedAt is a fixed synthetic instant for the processed-artifact tests.
func producedAt() time.Time {
	return time.Date(2026, time.May, 5, 19, 42, 14, 0, time.UTC)
}

// sampleArtifact is a well-formed extraction artifact for reuse across tests.
func sampleArtifact() ProcessedArtifact {
	notes := "testing-then-folding may be worth flagging."
	return ProcessedArtifact{
		ID:           "raw_2026_05_05_19_42",
		EntryID:      "raw_2026_05_05_19_42",
		ProducedAt:   producedAt(),
		AgentVersion: "structuring-2026.05.0",
		Emotions:     []ProcessedItem{{Name: "annoyed", Rationale: "user said 'annoyed'"}},
		Themes:       []ProcessedItem{{Name: "voice-not-heard", Rationale: "tried to push back, dropped it"}},
		People:       []ProcessedPerson{{DisplayName: "M.", PersonKey: "person_p-hazel", FirstMention: true}},
		Notes:        &notes,
	}
}

// TestWriteReadProcessed_RoundTrips writes then reads an artifact and confirms
// every field survives, and the on-disk JSON carries the documented shape
// (arrays, not null; proposals as []).
func TestWriteReadProcessed_RoundTrips(t *testing.T) {
	a := New(t.TempDir())
	art := sampleArtifact()
	require.NoError(t, a.WriteProcessed(art))

	got, err := a.ReadProcessed(art.ID)
	require.NoError(t, err)
	assert.Equal(t, art.ID, got.ID)
	assert.Equal(t, art.EntryID, got.EntryID)
	assert.Equal(t, art.AgentVersion, got.AgentVersion)
	assert.Equal(t, art.Emotions, got.Emotions)
	assert.Equal(t, art.Themes, got.Themes)
	assert.Equal(t, art.People, got.People)
	require.NotNil(t, got.Notes)
	assert.Equal(t, *art.Notes, *got.Notes)
	assert.True(t, got.ProducedAt.Equal(art.ProducedAt))

	// Empty proposal arrays render as [] and person_key is present.
	b, err := os.ReadFile(filepath.Join(a.processedDir(), art.ID+".json"))
	require.NoError(t, err)
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &m))
	assert.JSONEq(t, `[]`, string(m["rejected_proposals"]))
	assert.JSONEq(t, `[]`, string(m["unanswered_proposals"]))
}

// TestWriteProcessed_EmptyBodyArtifact writes the §S-3 empty-body shape: empty
// arrays render as [], notes is set, and the validator accepts it.
func TestWriteProcessed_EmptyBodyArtifact(t *testing.T) {
	a := New(t.TempDir())
	notes := NotesRawBodyEmpty
	art := ProcessedArtifact{
		ID:           "raw_2026_05_06_07_15",
		EntryID:      "raw_2026_05_06_07_15",
		ProducedAt:   producedAt(),
		AgentVersion: "structuring-2026.05.0",
		Notes:        &notes,
	}
	require.NoError(t, a.WriteProcessed(art))

	b, err := os.ReadFile(filepath.Join(a.processedDir(), art.ID+".json"))
	require.NoError(t, err)
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &m))
	assert.JSONEq(t, `[]`, string(m["emotions"]))
	assert.JSONEq(t, `[]`, string(m["themes"]))
	assert.JSONEq(t, `[]`, string(m["people"]))
	assert.JSONEq(t, `"raw body empty"`, string(m["notes"]))
}

// TestValidateProcessedArtifact_RejectsBadShapes walks every rejection the
// deterministic schema gate enforces (the jq contract in
// acceptance-criteria.md Phase 4).
func TestValidateProcessedArtifact_RejectsBadShapes(t *testing.T) {
	valid := mustRender(t, sampleArtifact())
	require.NoError(t, ValidateProcessedArtifact(valid))

	tests := map[string]func(ProcessedArtifact) ProcessedArtifact{
		"id != entry_id":       func(a ProcessedArtifact) ProcessedArtifact { a.EntryID = "raw_other"; return a },
		"empty id":             func(a ProcessedArtifact) ProcessedArtifact { a.ID = ""; a.EntryID = ""; return a },
		"wrong agent prefix":   func(a ProcessedArtifact) ProcessedArtifact { a.AgentVersion = "reflection-1"; return a },
		"null person_key":      func(a ProcessedArtifact) ProcessedArtifact { a.People[0].PersonKey = ""; return a },
		"empty person display": func(a ProcessedArtifact) ProcessedArtifact { a.People[0].DisplayName = ""; return a },
		"empty without notes":  emptyWithoutNotes,
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			content := mustRender(t, mutate(sampleArtifact()))
			require.Error(t, ValidateProcessedArtifact(content), "expected %s to be rejected", name)
		})
	}

	// A non-ISO produced_at is rejected (bytes crafted directly, since the
	// renderer always formats a valid timestamp).
	bad := `{"id":"raw_x","entry_id":"raw_x","produced_at":"not-a-date","agent_version":"structuring-1",` +
		`"emotions":[],"themes":[],"people":[],"notes":"x","rejected_proposals":[],"unanswered_proposals":[]}`
	require.Error(t, ValidateProcessedArtifact([]byte(bad)))

	// Malformed JSON is rejected.
	require.Error(t, ValidateProcessedArtifact([]byte("{not json")))
}

// emptyWithoutNotes clears every extracted field and the notes so the
// artifact violates the "non-empty or notes" rule.
func emptyWithoutNotes(a ProcessedArtifact) ProcessedArtifact {
	a.Emotions = nil
	a.Themes = nil
	a.People = nil
	a.Notes = nil
	return a
}

// TestWriteProcessed_ValidationBlocksBadArtifact confirms WriteProcessed
// refuses to persist an artifact that fails validation and writes nothing.
func TestWriteProcessed_ValidationBlocksBadArtifact(t *testing.T) {
	a := New(t.TempDir())
	bad := emptyWithoutNotes(sampleArtifact()) // empty + no notes
	err := a.WriteProcessed(bad)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed validation")
	assert.NoFileExists(t, filepath.Join(a.processedDir(), bad.ID+".json"))
}

// TestReadProcessed_MissingAndBad covers the not-found and malformed paths.
func TestReadProcessed_MissingAndBad(t *testing.T) {
	a := New(t.TempDir())
	_, err := a.ReadProcessed("raw_none")
	require.Error(t, err)

	require.NoError(t, os.MkdirAll(a.processedDir(), dirPerm))
	require.NoError(t, os.WriteFile(filepath.Join(a.processedDir(), "raw_bad.json"), []byte("{nope"), filePerm))
	_, err = a.ReadProcessed("raw_bad")
	require.Error(t, err)
}

// mustRender renders an artifact to its on-disk bytes for validator tests.
func mustRender(t *testing.T, art ProcessedArtifact) []byte {
	t.Helper()
	b, err := renderProcessed(art)
	require.NoError(t, err)
	return b
}

// TestWriteProcessed_WriteFailureSurfaces covers the write-failure branch: a
// read-only processed/ tree surfaces an explicit error and persists nothing.
func TestWriteProcessed_WriteFailureSurfaces(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
	a := New(t.TempDir())
	require.NoError(t, os.MkdirAll(a.processedDir(), dirPerm))
	require.NoError(t, os.Chmod(a.processedDir(), 0o500))
	t.Cleanup(func() { _ = os.Chmod(a.processedDir(), 0o700) })

	err := a.WriteProcessed(sampleArtifact())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write processed")
}

// TestWriteProcessed_PreservesProposalArrays confirms non-empty proposal
// arrays round-trip verbatim (they are carried opaquely for later phases) and
// exercises the non-nil orEmptyRaw branch.
func TestWriteProcessed_PreservesProposalArrays(t *testing.T) {
	a := New(t.TempDir())
	art := sampleArtifact()
	art.RejectedProposals = []json.RawMessage{json.RawMessage(`{"shape_tag":"x"}`)}
	art.UnansweredProposals = []json.RawMessage{}
	require.NoError(t, a.WriteProcessed(art))

	b, err := os.ReadFile(filepath.Join(a.processedDir(), art.ID+".json"))
	require.NoError(t, err)
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &m))
	assert.JSONEq(t, `[{"shape_tag":"x"}]`, string(m["rejected_proposals"]))

	got, err := a.ReadProcessed(art.ID)
	require.NoError(t, err)
	require.Len(t, got.RejectedProposals, 1)
	assert.JSONEq(t, `{"shape_tag":"x"}`, string(got.RejectedProposals[0]))
}

// TestWriteProcessed_MkdirFailureSurfaces covers the prepare-dir branch: when
// the processed path is occupied by a file, MkdirAll fails and the error is
// surfaced.
func TestWriteProcessed_MkdirFailureSurfaces(t *testing.T) {
	home := t.TempDir()
	a := New(home)
	// Occupy the processed/ path with a regular file so MkdirAll cannot create it.
	require.NoError(t, os.WriteFile(a.processedDir(), []byte("x"), filePerm))

	err := a.WriteProcessed(sampleArtifact())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prepare processed dir")
}

// TestReadProcessed_BadProducedAt covers decode's timestamp-parse branch: a
// stored artifact with a malformed produced_at surfaces a parse error.
func TestReadProcessed_BadProducedAt(t *testing.T) {
	a := New(t.TempDir())
	require.NoError(t, os.MkdirAll(a.processedDir(), dirPerm))
	rec := `{"id":"raw_x","entry_id":"raw_x","produced_at":"not-a-date","agent_version":"structuring-1",` +
		`"emotions":[],"themes":[],"people":[],"notes":"x","rejected_proposals":[],"unanswered_proposals":[]}`
	require.NoError(t, os.WriteFile(filepath.Join(a.processedDir(), "raw_x.json"), []byte(rec), filePerm))

	_, err := a.ReadProcessed("raw_x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "produced_at")
}

// TestIsISOTimestamp walks the ISO-prefix check across each rejection branch:
// too short, non-digit where a digit is required, and the wrong separator or
// date/time delimiter.
func TestIsISOTimestamp(t *testing.T) {
	assert.True(t, isISOTimestamp("2026-05-05T"))
	assert.True(t, isISOTimestamp("2026-05-05T19:42:14-04:00"))
	assert.False(t, isISOTimestamp("short"))       // too short
	assert.False(t, isISOTimestamp("20X6-05-05T")) // non-digit year
	assert.False(t, isISOTimestamp("2026X05-05T")) // wrong separator
	assert.False(t, isISOTimestamp("2026-05-05X")) // wrong date/time delimiter
	assert.False(t, isISOTimestamp("2026-05-0aT")) // non-digit day
}
