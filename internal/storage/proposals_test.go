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

// seedProcessed writes a minimal valid processed artifact and returns its id
// and on-disk path. It is the prior Structuring output the proposal appends
// attach to.
func seedProcessed(t *testing.T, a *Adapter) (id, path string) {
	t.Helper()
	id = "raw_2026_05_05_19_42"
	art := ProcessedArtifact{
		ID:           id,
		EntryID:      id,
		ProducedAt:   time.Date(2026, time.May, 5, 19, 42, 14, 0, time.UTC),
		AgentVersion: "structuring-2026.05.0",
		Themes:       []ProcessedItem{{Name: "voice-not-heard", Rationale: "pushed back then dropped it"}},
	}
	require.NoError(t, a.WriteProcessed(art))
	return id, filepath.Join(a.processedDir(), id+processedExt)
}

// readProcessedRaw reads a processed artifact as a generic map for shape checks.
func readProcessedRaw(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	return m
}

// TestAppendRejectedProposal appends a rejection and confirms the entry lands
// with all fields, the artifact still validates, and produced_at is untouched.
func TestAppendRejectedProposal(t *testing.T) {
	a := New(t.TempDir())
	id, path := seedProcessed(t, a)
	before := readProcessedRaw(t, path)

	require.NoError(t, a.AppendRejectedProposal(id, RejectedProposal{
		At:                      time.Date(2026, time.May, 6, 8, 21, 18, 0, time.UTC),
		ReflectionPromptVersion: "reflection-2026.05.0",
		ProposalText:            "One possible pattern: defaulting to defensiveness when family is the topic.",
		UserResponseText:        "No — I'm not defensive in general.",
		ShapeTag:                "family-defensiveness-default",
	}))

	after := readProcessedRaw(t, path)
	assert.Equal(t, before["produced_at"], after["produced_at"], "an append must not re-time the artifact")

	rejected := after["rejected_proposals"].([]any)
	require.Len(t, rejected, 1)
	entry := rejected[0].(map[string]any)
	assert.Equal(t, "family-defensiveness-default", entry["shape_tag"])
	assert.Equal(t, "No — I'm not defensive in general.", entry["user_response_text"])

	art, err := a.ReadProcessed(id)
	require.NoError(t, err)
	assert.Equal(t, []string{"family-defensiveness-default"}, RejectedShapeTags(art))
}

// TestAppendUnansweredProposal appends an unanswered shape and confirms the
// {shape_tag, proposed_at} pair round-trips.
func TestAppendUnansweredProposal(t *testing.T) {
	a := New(t.TempDir())
	id, path := seedProcessed(t, a)

	require.NoError(t, a.AppendUnansweredProposal(id, UnansweredProposal{
		ShapeTag:   "quiet-day-flatness",
		ProposedAt: time.Date(2026, time.May, 6, 8, 21, 18, 0, time.UTC),
	}))

	after := readProcessedRaw(t, path)
	unanswered := after["unanswered_proposals"].([]any)
	require.Len(t, unanswered, 1)
	entry := unanswered[0].(map[string]any)
	assert.Equal(t, "quiet-day-flatness", entry["shape_tag"])
	assert.NotEmpty(t, entry["proposed_at"])

	art, err := a.ReadProcessed(id)
	require.NoError(t, err)
	assert.Equal(t, []string{"quiet-day-flatness"}, UnansweredShapeTags(art))
}

// TestAppendProposal_MissingArtifact surfaces an error when the processed
// artifact does not exist.
func TestAppendProposal_MissingArtifact(t *testing.T) {
	a := New(t.TempDir())
	require.Error(t, a.AppendRejectedProposal("raw_nope", RejectedProposal{ShapeTag: "x"}))
	require.Error(t, a.AppendUnansweredProposal("raw_nope", UnansweredProposal{ShapeTag: "x"}))
}

// TestShapeTagsFrom_SkipsUntagged proves an opaque proposal entry without a
// shape_tag contributes nothing.
func TestShapeTagsFrom_SkipsUntagged(t *testing.T) {
	entries := []json.RawMessage{
		json.RawMessage(`{"shape_tag":"a-tag"}`),
		json.RawMessage(`{"note":"no tag here"}`),
	}
	assert.Equal(t, []string{"a-tag"}, shapeTagsFrom(entries))
}

// TestProposalPauseState_RoundTrip covers the absent → zero default and a
// full write/read of a set pause.
func TestProposalPauseState_RoundTrip(t *testing.T) {
	a := New(t.TempDir())

	st, err := a.ReadProposalPauseState()
	require.NoError(t, err)
	assert.Equal(t, 0, st.ConsecutiveUnanswered)
	assert.Nil(t, st.PausedUntil)

	until := time.Date(2026, time.May, 20, 0, 0, 0, 0, time.UTC)
	require.NoError(t, a.WriteProposalPauseState(ProposalPauseState{ConsecutiveUnanswered: 3, PausedUntil: &until}))

	st, err = a.ReadProposalPauseState()
	require.NoError(t, err)
	assert.Equal(t, 3, st.ConsecutiveUnanswered)
	require.NotNil(t, st.PausedUntil)
	assert.True(t, until.Equal(*st.PausedUntil))
}

// TestProposalPauseState_MalformedPausedUntil surfaces a parse error when the
// paused_until timestamp is not RFC3339.
func TestProposalPauseState_MalformedPausedUntil(t *testing.T) {
	a := New(t.TempDir())
	require.NoError(t, os.MkdirAll(a.home, dirPerm))
	body := `{"consecutive_unanswered": 2, "paused_until": "not-a-time"}`
	require.NoError(t, os.WriteFile(filepath.Join(a.home, proposalPauseFile), []byte(body), filePerm))
	_, err := a.ReadProposalPauseState()
	require.Error(t, err)
}

// TestOffLimits_RoundTrip covers the absent → empty default and a write/read.
func TestOffLimits_RoundTrip(t *testing.T) {
	a := New(t.TempDir())

	keys, err := a.ReadOffLimitsPersonKeys()
	require.NoError(t, err)
	assert.Empty(t, keys)

	require.NoError(t, a.WriteOffLimitsPersonKeys([]string{"person_a-river"}))
	keys, err = a.ReadOffLimitsPersonKeys()
	require.NoError(t, err)
	assert.Equal(t, []string{"person_a-river"}, keys)
}

// TestProposalPauseState_Malformed surfaces a parse error on a corrupt file.
func TestProposalPauseState_Malformed(t *testing.T) {
	a := New(t.TempDir())
	require.NoError(t, os.MkdirAll(a.home, dirPerm))
	require.NoError(t, os.WriteFile(filepath.Join(a.home, proposalPauseFile), []byte("{not json"), filePerm))
	_, err := a.ReadProposalPauseState()
	require.Error(t, err)
}

// TestOffLimits_Malformed surfaces a parse error on a corrupt registry.
func TestOffLimits_Malformed(t *testing.T) {
	a := New(t.TempDir())
	require.NoError(t, os.MkdirAll(a.home, dirPerm))
	require.NoError(t, os.WriteFile(filepath.Join(a.home, offLimitsFile), []byte("{not json"), filePerm))
	_, err := a.ReadOffLimitsPersonKeys()
	require.Error(t, err)
}

// TestListProcessedIDs returns nil for an absent dir and a sorted list once
// artifacts exist.
func TestListProcessedIDs(t *testing.T) {
	a := New(t.TempDir())
	ids, err := a.ListProcessedIDs()
	require.NoError(t, err)
	assert.Empty(t, ids)

	for _, pid := range []string{"raw_2026_05_05_19_42", "raw_2026_05_03_21_10"} {
		require.NoError(t, a.WriteProcessed(ProcessedArtifact{
			ID: pid, EntryID: pid, ProducedAt: time.Now(), AgentVersion: "structuring-2026.05.0",
			Themes: []ProcessedItem{{Name: "t", Rationale: "r"}},
		}))
	}
	ids, err = a.ListProcessedIDs()
	require.NoError(t, err)
	assert.Equal(t, []string{"raw_2026_05_03_21_10", "raw_2026_05_05_19_42"}, ids)
}

// TestAppendProposal_MalformedProcessed surfaces a parse error when the target
// processed artifact is not valid JSON.
func TestAppendProposal_MalformedProcessed(t *testing.T) {
	a := New(t.TempDir())
	require.NoError(t, os.MkdirAll(a.processedDir(), dirPerm))
	require.NoError(t, os.WriteFile(filepath.Join(a.processedDir(), "raw_bad"+processedExt), []byte("{not json"), filePerm))
	require.Error(t, a.AppendRejectedProposal("raw_bad", RejectedProposal{ShapeTag: "x"}))
}

// TestAppendProposal_UnwritableFileErrors covers appendProposal's write branch:
// a read-only processed file makes the write-back fail.
func TestAppendProposal_UnwritableFileErrors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
	a := New(t.TempDir())
	id, path := seedProcessed(t, a)
	require.NoError(t, os.Chmod(path, 0o400))
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	require.Error(t, a.AppendUnansweredProposal(id, UnansweredProposal{ShapeTag: "x", ProposedAt: time.Now()}))
}

// TestState_UnwritableHomeErrors covers the write-failure branch for the two
// root state files when the Ledger home is unwritable.
func TestState_UnwritableHomeErrors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
	home := filepath.Join(t.TempDir(), "ledger")
	require.NoError(t, os.MkdirAll(home, 0o500))
	t.Cleanup(func() { _ = os.Chmod(home, 0o700) })
	a := New(home)
	require.Error(t, a.WriteProposalPauseState(ProposalPauseState{ConsecutiveUnanswered: 1}))
	require.Error(t, a.WriteOffLimitsPersonKeys([]string{"person_a-river"}))
}
