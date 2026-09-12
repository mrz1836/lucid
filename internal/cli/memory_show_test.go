package cli

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

// TestMemoryShow_Registered confirms `memory show` is a subcommand of memory and
// self-documents its --history flag.
func TestMemoryShow_Registered(t *testing.T) {
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "memory", "show", "--help")
	require.NoError(t, err)
	assert.Contains(t, out, "--history")
}

// TestMemoryShow_FoldsCurrentValues proves `memory show` prints the folded current
// values after an amend — the amended field and every intact field, never the
// pre-amend value (AC-10).
func TestMemoryShow_FoldsCurrentValues(t *testing.T) {
	enableMemoryHome(t)
	id := createMemoryCLI(t, "the old wording", "--certainty", "hazy")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "memory", "amend", id, "--certainty", "vivid")
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "memory", "show", id)
	require.NoError(t, err)
	assert.Contains(t, out, id, "the heading names the stable base id")
	assert.Contains(t, out, "vivid", "show reflects the amended certainty")
	assert.NotContains(t, out, "hazy", "the pre-amend value does not surface")
	assert.Contains(t, out, "the old wording", "an unamended field is intact")
}

// TestMemoryShow_JSONFoldsCurrentValues proves the --json view carries the folded
// payload (AC-10).
func TestMemoryShow_JSONFoldsCurrentValues(t *testing.T) {
	enableMemoryHome(t)
	id := createMemoryCLI(t, "the old wording", "--certainty", "hazy")
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "memory", "amend", id, "--certainty", "vivid")
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "memory", "show", id, "--json")
	require.NoError(t, err)

	var view memoryShowView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.Equal(t, id, view.EventID)
	assert.Equal(t, "vivid", view.Payload[observations.MemoryFieldCertainty])
	assert.Empty(t, view.History, "history is omitted without --history")
}

// TestMemoryShow_History proves --history prints the amendment trail with the
// original value, the amended value, and the recorded_at timestamp (AC-10, AC-12).
func TestMemoryShow_History(t *testing.T) {
	enableMemoryHome(t)
	id := createMemoryCLI(t, "a memory", "--certainty", "hazy")
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "memory", "amend", id, "--certainty", "vivid")
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "memory", "show", id, "--history", "--json")
	require.NoError(t, err)

	var view memoryShowView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	require.Len(t, view.History, 1)
	assert.Equal(t, observations.MemoryFieldCertainty, view.History[0].Field)
	assert.Equal(t, "hazy", view.History[0].Prior, "the trail records the original value")
	assert.True(t, view.History[0].PriorSet)
	assert.Equal(t, "vivid", view.History[0].New, "the trail records the amended value")
	assert.NotEmpty(t, view.History[0].RecordedAt, "the trail carries the amendment timestamp")
}

// TestMemoryShow_HistoryText proves the human trail renders original → amended and
// marks a cleared field as "(cleared)" (AC-10).
func TestMemoryShow_HistoryText(t *testing.T) {
	enableMemoryHome(t)
	id := createMemoryCLI(t, "a memory", "--followup", "the old thread")
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "memory", "amend", id, "--clear-followup")
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "memory", "show", id, "--history")
	require.NoError(t, err)
	assert.Contains(t, out, "History:")
	assert.Contains(t, out, "the old thread", "the trail shows the prior follow-up")
	assert.Contains(t, out, "(cleared)", "a cleared field is marked in the trail")
}

// TestMemoryShow_NoAmendmentsSaysSo proves --history on a never-amended story is an
// honest "original memory" note, not an empty trail.
func TestMemoryShow_NoAmendmentsSaysSo(t *testing.T) {
	enableMemoryHome(t)
	id := createMemoryCLI(t, "a plain memory")

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "memory", "show", id, "--history")
	require.NoError(t, err)
	assert.Contains(t, out, "original memory")
}

// TestMemoryShow_UnknownIDErrorsCleanly proves an unknown id errors cleanly and
// prints nothing to stdout (AC-10).
func TestMemoryShow_UnknownIDErrorsCleanly(t *testing.T) {
	enableMemoryHome(t)

	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "memory", "show", "obs_2099_01_01_001")
	require.Error(t, err)
	assert.Contains(t, errOut, "not found")
	assert.Empty(t, out, "nothing renders for an unknown memory")
}

// TestMemoryShow_RejectsAmendmentID proves a correction-event id is not a showable
// base — it errors and points at the base it corrects (AC-10, AC-12).
func TestMemoryShow_RejectsAmendmentID(t *testing.T) {
	home := enableMemoryHome(t)
	id := createMemoryCLI(t, "a memory")
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "memory", "amend", id, "--certainty", "vivid")
	require.NoError(t, err)

	// The amendment is the memory event carrying refs.corrects.
	var amendID string
	for _, e := range readObsEvents(t, home) {
		if _, ok := e.Refs[observations.RefCorrects]; ok {
			amendID = e.ID
		}
	}
	require.NotEmpty(t, amendID)

	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "memory", "show", amendID)
	require.Error(t, err)
	assert.Contains(t, errOut, "amendment", "a correction-event id is refused as a base")
	assert.Empty(t, out)
}
