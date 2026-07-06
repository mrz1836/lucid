package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/config"
)

// syntheticSession builds a well-formed /log session record for tests.
func syntheticSession(now time.Time, id string) Session {
	return Session{
		ID:            id,
		StartedAt:     now,
		EndedAt:       now,
		Harness:       "cli",
		ChannelID:     "cli",
		Command:       "/log",
		RawEntryIDs:   []string{"raw_2026_07_05_18_41"},
		AgentVersions: config.Default().AgentVersions,
	}
}

// readSessionJSON reads and decodes a session record file into a map.
func readSessionJSON(t *testing.T, home, id string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, "sessions", id+".json"))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	return m
}

func TestWriteSession_ExplicitID(t *testing.T) {
	a, home := newRawAdapter(t)
	id, err := a.WriteSession(syntheticSession(fixedTime(), "session_2026_07_05_18_41"))
	require.NoError(t, err)
	assert.Equal(t, "session_2026_07_05_18_41", id)

	m := readSessionJSON(t, home, id)
	assert.Equal(t, "/log", m["command"])
	assert.Equal(t, []any{"raw_2026_07_05_18_41"}, m["raw_entry_ids"])
	// Nil id-slices render as empty arrays, not null.
	assert.Equal(t, []any{}, m["processed_artifact_ids"])
	assert.Equal(t, []any{}, m["insight_ids"])
	agents, ok := m["agent_versions"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "intake-2026.05.0", agents["intake"])
}

func TestWriteSession_DuplicateExplicitIDIsError(t *testing.T) {
	a, _ := newRawAdapter(t)
	s := syntheticSession(fixedTime(), "session_2026_07_05_18_41")
	_, err := a.WriteSession(s)
	require.NoError(t, err)
	_, err = a.WriteSession(s)
	require.Error(t, err, "an existing session id is never overwritten")
}

func TestWriteSession_AllocatesAndResolvesCollision(t *testing.T) {
	a, home := newRawAdapter(t)
	now := fixedTime()

	first, err := a.WriteSession(syntheticSession(now, ""))
	require.NoError(t, err)
	assert.Equal(t, "session_2026_07_05_18_41", first)

	second, err := a.WriteSession(syntheticSession(now, ""))
	require.NoError(t, err)
	assert.Equal(t, "session_2026_07_05_18_41_39", second, "same-minute session id carries the _SS suffix")

	// Both files exist.
	for _, id := range []string{first, second} {
		_, statErr := os.Stat(filepath.Join(home, "sessions", id+".json"))
		require.NoError(t, statErr)
	}
}

func TestWriteSession_RequiresStartedAt(t *testing.T) {
	a, _ := newRawAdapter(t)
	_, err := a.WriteSession(Session{ID: "session_x"})
	require.Error(t, err)
}

func TestWriteSession_EndedAtOmittedWhenZero(t *testing.T) {
	a, home := newRawAdapter(t)
	s := syntheticSession(fixedTime(), "session_2026_07_05_18_41")
	s.EndedAt = time.Time{}
	_, err := a.WriteSession(s)
	require.NoError(t, err)

	m := readSessionJSON(t, home, "session_2026_07_05_18_41")
	assert.Empty(t, m["ended_at"])
}

// TestWriteSession_UnwritableDir covers the write-failure path: the
// sessions directory exists but is read-only.
func TestWriteSession_UnwritableDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
	a, home := newRawAdapter(t)
	sessDir := filepath.Join(home, "sessions")
	require.NoError(t, os.Chmod(sessDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(sessDir, 0o700) })

	_, err := a.WriteSession(syntheticSession(fixedTime(), "session_2026_07_05_18_41"))
	require.Error(t, err)
}
