package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/storage"
)

// enableMemoryHome points LUCID_HOME at an isolated home, scaffolds it, and
// enables the (enable-gated) memory kind so the story-capture CLI tests exercise
// the write path. It returns the home path.
func enableMemoryHome(t *testing.T) string {
	t.Helper()
	home := isolatedHome(t)
	a := storage.New(home)
	_, err := a.Scaffold()
	require.NoError(t, err)
	require.NoError(t, a.ScaffoldObservations())
	cfg, err := a.ReadObservationsConfig()
	require.NoError(t, err)
	cfg.KindsEnabled = append(cfg.KindsEnabled, observations.KindMemory)
	require.NoError(t, a.SaveObservationsConfig(cfg))
	return home
}

// TestMemory_Registered confirms the verb is on the spine and self-documents in
// --help with its convention flags.
func TestMemory_Registered(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})
	got := map[string]bool{}
	for _, c := range root.Commands() {
		got[c.Name()] = true
	}
	assert.True(t, got["memory"], "memory verb not registered")

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "memory", "--help")
	require.NoError(t, err)
	assert.Contains(t, out, "memory")
	assert.Contains(t, out, "--certainty")
	assert.Contains(t, out, "--era")
	assert.Contains(t, out, "--attach")
}

// TestMemory_CLI_TextOnly runs a bare text story: it acks inventory-only and
// writes one memory event with the text payload and no invented refs.
func TestMemory_CLI_TextOnly(t *testing.T) {
	home := enableMemoryHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "memory", "just a plain memory")
	require.NoError(t, err)
	assert.Contains(t, out, "Logged memory as `obs_")

	events := readObsEvents(t, home)
	require.Len(t, events, 1)
	assert.Equal(t, observations.KindMemory, events[0].Kind)
	assert.Equal(t, "just a plain memory", events[0].Payload[observations.MemoryFieldText])
	assert.NotContains(t, events[0].Refs, "era")
}

// TestMemory_CLI_JSON confirms the --json shape carries the fields a harness
// needs: the event id, logical day, and the resolved refs (era verbatim, place
// resolved to a key).
func TestMemory_CLI_JSON(t *testing.T) {
	enableMemoryHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"memory", "the coast at 2am", "--certainty", "vivid",
		"--era", "era_wild-summer", "--place", "Ocean City", "--why", "felt free", "--json")
	require.NoError(t, err)

	var view memoryWriteView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.NotEmpty(t, view.EventID)
	assert.False(t, view.Partial)
	assert.False(t, view.Rejected)
	assert.Equal(t, "era_wild-summer", view.Refs["era"], "era is referenced verbatim")
	assert.NotEmpty(t, view.Refs["place"], "place is resolved to a key")
}

// TestMemory_CLI_BackdatedDay proves an excavated story files under its own past
// day at approximate precision — the bitemporal capture.
func TestMemory_CLI_BackdatedDay(t *testing.T) {
	home := enableMemoryHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"memory", "the summer everything changed", "--certainty", "hazy", "--day", "1999-06-01", "--json")
	require.NoError(t, err)

	var view memoryWriteView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.Equal(t, "1999-06-01", view.LogicalDate)

	events := readObsEvents(t, home)
	require.Len(t, events, 1)
	assert.Equal(t, observations.PrecisionApproximate, events[0].OccurredAtPrecision)
}

// TestMemory_CLI_Attach proves a story with --attach reuses `lucid attach` and
// references the returned raw entry id from refs.entry; a media file lands.
func TestMemory_CLI_Attach(t *testing.T) {
	withClock(t, time.Date(2026, 7, 5, 18, 41, 0, 0, time.UTC))
	home := enableMemoryHome(t)

	photo := filepath.Join(t.TempDir(), "beach.jpg")
	require.NoError(t, os.WriteFile(photo, []byte("synthetic-bytes"), 0o600))

	out, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"memory", "the photo from that night", "--attach", photo, "--caption", "the pier", "--json")
	require.NoError(t, err)

	var view memoryWriteView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	entry, ok := view.Refs["entry"].(string)
	require.True(t, ok, "refs.entry is the linked raw id")
	assert.Equal(t, "raw_2026_07_05_18_41", entry)

	// A media file was stored under the media/ tree.
	var mediaCount int
	err = filepath.WalkDir(filepath.Join(home, "media"), func(_ string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if !d.IsDir() {
			mediaCount++
		}
		return nil
	})
	require.NoError(t, err)
	assert.Positive(t, mediaCount, "the attached photo is stored")
}

// TestMemory_CLI_TextOnlyNeverGatedByMedia proves the text-only path writes with
// no media reference — media never gates a story.
func TestMemory_CLI_TextOnlyNeverGatedByMedia(t *testing.T) {
	home := enableMemoryHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "memory", "no photo here")
	require.NoError(t, err)

	events := readObsEvents(t, home)
	require.Len(t, events, 1)
	assert.NotContains(t, events[0].Refs, "entry")
}

// TestMemory_CLI_DisabledKind prints the enable hint and writes nothing when the
// memory kind is off (a fresh Ledger keeps it disabled) — no error, exit 0.
func TestMemory_CLI_DisabledKind(t *testing.T) {
	home := isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "memory", "should not land")
	require.NoError(t, err)
	assert.Contains(t, out, "isn't enabled")

	// Nothing was written under observations/.
	_, statErr := os.Stat(filepath.Join(home, "observations"))
	if statErr == nil {
		assert.Empty(t, readObsEvents(t, home), "a disabled kind writes no event")
	}
}

// TestMemory_CLI_RequiresText confirms a bare `memory` is a usage error (exit 2).
func TestMemory_CLI_RequiresText(t *testing.T) {
	enableMemoryHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "memory")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
}
