package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

// createMemoryCLI writes one story through the CLI and returns its obs id, so the
// amend tests have a real base memory to correct.
func createMemoryCLI(t *testing.T, args ...string) string {
	t.Helper()
	full := append([]string{"memory"}, args...)
	full = append(full, "--json")
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, full...)
	require.NoError(t, err)
	var view memoryWriteView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	require.NotEmpty(t, view.EventID)
	return view.EventID
}

// foldedCLIMemory reads the ledger, folds the amendments, and returns the current
// (folded) state of one story — what every read surface will show after an amend.
func foldedCLIMemory(t *testing.T, home, id string) observations.Event {
	t.Helper()
	for _, e := range observations.FoldMemoryAmendments(readObsEvents(t, home)) {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("memory %s not found after fold", id)
	return observations.Event{}
}

// TestMemoryAmend_Registered confirms `memory amend` is a subcommand of memory,
// self-documents its flags, and — per Q3 — carries no --caption.
func TestMemoryAmend_Registered(t *testing.T) {
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "memory", "amend", "--help")
	require.NoError(t, err)
	assert.Contains(t, out, "--era")
	assert.Contains(t, out, "--certainty")
	assert.Contains(t, out, "--followup")
	assert.Contains(t, out, "--clear-followup")
	assert.Contains(t, out, "--body-file")
	assert.NotContains(t, out, "--caption", "caption is out of scope for amend (Q3)")
}

// TestMemoryAmend_BareCreateStillWorks proves adding the amend subcommand did not
// disturb the bare `memory <text>` create path.
func TestMemoryAmend_BareCreateStillWorks(t *testing.T) {
	home := enableMemoryHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "memory", "just a plain memory")
	require.NoError(t, err)
	assert.Contains(t, out, "Logged memory as `obs_")

	events := readObsEvents(t, home)
	require.Len(t, events, 1)
	assert.Equal(t, "just a plain memory", events[0].Payload[observations.MemoryFieldText])
}

// TestMemoryAmend_BodyFileHappyPath proves the corrected body arrives via
// --body-file and folds onto the story, and the --json view names the amendment
// and its target.
func TestMemoryAmend_BodyFileHappyPath(t *testing.T) {
	home := enableMemoryHome(t)
	id := createMemoryCLI(t, "the old wording")

	bodyPath := filepath.Join(t.TempDir(), "corrected.txt")
	require.NoError(t, os.WriteFile(bodyPath, []byte("the corrected wording\n"), 0o600))

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "memory", "amend", id, "--body-file", bodyPath, "--json")
	require.NoError(t, err)

	var view memoryAmendView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.Equal(t, id, view.TargetID)
	assert.NotEmpty(t, view.EventID)
	assert.NotEqual(t, id, view.EventID, "the amendment is a distinct appended event")

	assert.Equal(t, "the corrected wording", foldedCLIMemory(t, home, id).Payload[observations.MemoryFieldText])
}

// TestMemoryAmend_MissingBodyFile proves an unreadable --body-file errors and
// writes no amendment.
func TestMemoryAmend_MissingBodyFile(t *testing.T) {
	home := enableMemoryHome(t)
	id := createMemoryCLI(t, "a memory")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "memory", "amend", id, "--body-file", filepath.Join(t.TempDir(), "nope.txt"))
	require.Error(t, err)

	// Only the base event exists — the missing body file wrote nothing.
	assert.Len(t, readObsEvents(t, home), 1)
}

// TestMemoryAmend_ClearFollowupParsed proves --clear-followup removes the
// follow-up field on the folded story.
func TestMemoryAmend_ClearFollowupParsed(t *testing.T) {
	home := enableMemoryHome(t)
	id := createMemoryCLI(t, "a memory", "--followup", "the old thread")
	require.Equal(t, "the old thread", readObsEvents(t, home)[0].Payload[observations.MemoryFieldFollowUp])

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "memory", "amend", id, "--clear-followup")
	require.NoError(t, err)

	assert.NotContains(t, foldedCLIMemory(t, home, id).Payload, observations.MemoryFieldFollowUp)
}

// TestMemoryAmend_BareEmptyFollowupRejected proves a bare `--followup ""` is
// ambiguous (set-empty vs clear) and is rejected, pointing at --clear-followup,
// with nothing written.
func TestMemoryAmend_BareEmptyFollowupRejected(t *testing.T) {
	home := enableMemoryHome(t)
	id := createMemoryCLI(t, "a memory")

	_, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "memory", "amend", id, "--followup", "")
	require.Error(t, err)
	assert.Contains(t, errOut, "clear-followup")

	assert.Len(t, readObsEvents(t, home), 1, "an ambiguous empty follow-up writes nothing")
}

// TestMemoryAmend_SetAndClearRejected proves setting and clearing the follow-up in
// one call is contradictory and writes nothing.
func TestMemoryAmend_SetAndClearRejected(t *testing.T) {
	home := enableMemoryHome(t)
	id := createMemoryCLI(t, "a memory")

	_, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "memory", "amend", id, "--followup", "x", "--clear-followup")
	require.Error(t, err)
	assert.Contains(t, errOut, "not both")

	assert.Len(t, readObsEvents(t, home), 1)
}

// TestMemoryAmend_UnknownIDErrorsCleanly proves an unknown id is a clean error
// (not a crash) and changes nothing.
func TestMemoryAmend_UnknownIDErrorsCleanly(t *testing.T) {
	home := enableMemoryHome(t)

	_, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "memory", "amend", "obs_2099_01_01_001", "--certainty", "vivid")
	require.Error(t, err)
	assert.Contains(t, errOut, "not found")

	_, statErr := os.Stat(filepath.Join(home, "observations"))
	if statErr == nil {
		assert.Empty(t, readObsEvents(t, home))
	}
}
