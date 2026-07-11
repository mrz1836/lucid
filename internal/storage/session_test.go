package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestWriteSession_RecordsAgentAndModel(t *testing.T) {
	a, home := newRawAdapter(t)
	s := syntheticSession(fixedTime(), "session_2026_07_05_18_41")
	s.Agent = "agent-x"
	s.Model = "model-y"
	_, err := a.WriteSession(s)
	require.NoError(t, err)

	m := readSessionJSON(t, home, "session_2026_07_05_18_41")
	assert.Equal(t, "agent-x", m["agent"])
	assert.Equal(t, "model-y", m["model"])

	// agent/model land immediately after thread_id, matching data-model.md.
	raw, err := os.ReadFile(filepath.Join(home, "sessions", "session_2026_07_05_18_41.json"))
	require.NoError(t, err)
	text := string(raw)
	assert.Less(t, indexOf(t, text, `"thread_id"`), indexOf(t, text, `"agent"`))
	assert.Less(t, indexOf(t, text, `"agent"`), indexOf(t, text, `"model"`))
	assert.Less(t, indexOf(t, text, `"model"`), indexOf(t, text, `"command"`))
}

func TestWriteSession_OmitsAgentAndModelWhenEmpty(t *testing.T) {
	a, home := newRawAdapter(t)
	// syntheticSession leaves Agent/Model empty — the plain terminal path.
	_, err := a.WriteSession(syntheticSession(fixedTime(), "session_2026_07_05_18_41"))
	require.NoError(t, err)

	m := readSessionJSON(t, home, "session_2026_07_05_18_41")
	_, hasAgent := m["agent"]
	_, hasModel := m["model"]
	assert.False(t, hasAgent, "omitempty drops agent for a plain terminal capture")
	assert.False(t, hasModel, "omitempty drops model for a plain terminal capture")
}

// TestSessionRecord_LegacyJSONParses proves a session written before the
// provenance fields existed (no agent/model keys, no schema_version) still
// decodes cleanly — readers tolerate the missing fields.
func TestSessionRecord_LegacyJSONParses(t *testing.T) {
	legacy := `{
  "id": "session_2026_07_05_18_41",
  "started_at": "2026-07-05T18:41:00-04:00",
  "ended_at": "",
  "harness": "cli",
  "channel_id": "cli",
  "thread_id": "",
  "command": "/log",
  "raw_entry_ids": ["raw_2026_07_05_18_41"],
  "processed_artifact_ids": [],
  "insight_ids": [],
  "rejected_proposal_count": 0,
  "agent_versions": {"intake": "intake-2026.05.0"}
}`
	var rec sessionRecord
	require.NoError(t, json.Unmarshal([]byte(legacy), &rec))
	assert.Equal(t, "session_2026_07_05_18_41", rec.ID)
	assert.Empty(t, rec.Agent, "missing agent zero-values cleanly")
	assert.Empty(t, rec.Model, "missing model zero-values cleanly")
	assert.Equal(t, "/log", rec.Command)
}

// indexOf returns the byte offset of substr in s, failing the test if it is
// absent — a small helper so the field-order assertions read cleanly.
func indexOf(t *testing.T, s, substr string) int {
	t.Helper()
	i := strings.Index(s, substr)
	require.GreaterOrEqual(t, i, 0, "expected %q in session JSON", substr)
	return i
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
