package workout

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

// sessionEvent builds a synthetic logged workout session on the given day at
// the given UTC clock time, the shape `workout log` writes.
func sessionEvent(id, day, clock string, payload map[string]any) observations.Event {
	at := day + "T" + clock + "Z"
	return observations.Event{
		ID: id, Schema: observations.Schema, Kind: observations.KindWorkout,
		RecordedAt: at, OccurredAt: at, OccurredAtPrecision: observations.PrecisionExact,
		LogicalDate: day, Payload: payload, Refs: map[string]any{},
	}
}

// TestBuildSessions_EchoesFoldedFields proves the echo reads a session's
// current folded state: an RPE added by a correction shows on the one session,
// the fields it didn't touch keep their logged values, and the correction
// itself is never echoed as a second session.
func TestBuildSessions_EchoesFoldedFields(t *testing.T) {
	t.Parallel()

	session := sessionEvent("obs_2026_07_19_001", "2026-07-19", "18:00:00", map[string]any{
		"type": "climbing", "duration_min": float64(60), "body_parts": []any{"fingers", "forearms"},
		"movements": []any{"bouldering"}, "note": "  kept verbatim\nsecond line",
	})
	correction := sessionEvent("obs_2026_07_19_002", "2026-07-19", "18:00:00", map[string]any{"rpe": float64(4)})
	correction.RecordedAt = "2026-07-19T21:00:00.5Z"
	correction.Refs = map[string]any{observations.RefCorrects: session.ID}

	folded := observations.FoldWorkoutAmendments([]observations.Event{session, correction})
	got := BuildSessions(folded, mustTime(t, mondayNoon), time.UTC)

	require.Len(t, got, 1, "one session, not one per correction")
	s := got[0]
	assert.Equal(t, session.ID, s.ID)
	require.NotNil(t, s.RPE)
	assert.Equal(t, 4, *s.RPE, "the corrected RPE reads back")
	require.NotNil(t, s.DurationMin)
	assert.Equal(t, 60, *s.DurationMin, "an untouched field keeps its logged value")
	assert.Equal(t, "climbing", s.Type)
	assert.Equal(t, []string{"fingers", "forearms"}, s.BodyParts)
	assert.Equal(t, []string{"bouldering"}, s.Movements)
	assert.Equal(t, "  kept verbatim\nsecond line", s.Note, "the note is echoed exactly as stored")
	assert.Equal(t, "2026-07-19T18:00:00Z", s.OccurredAt)
	assert.Equal(t, "2026-07-19", s.LogicalDate)
}

// TestBuildSessions_JSONOmitsUnrecordedFields proves the projection's wire
// shape: a field the session never recorded is absent rather than a hollow
// zero, a recorded zero RPE is kept (0 is a real reading), the two dates are
// always present, and an empty window projects as `[]`, never `null`.
func TestBuildSessions_JSONOmitsUnrecordedFields(t *testing.T) {
	t.Parallel()

	now := mustTime(t, mondayNoon)
	got := BuildSessions([]observations.Event{
		sessionEvent("obs_2026_07_20_001", "2026-07-20", "09:00:00", map[string]any{"rpe": 0}),
	}, now, time.UTC)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.JSONEq(t,
		`[{"id":"obs_2026_07_20_001","rpe":0,"occurred_at":"2026-07-20T09:00:00Z","logical_date":"2026-07-20"}]`,
		string(b))

	empty := BuildSessions(nil, now, nil)
	require.NotNil(t, empty)
	b, err = json.Marshal(empty)
	require.NoError(t, err)
	assert.JSONEq(t, `[]`, string(b))
}

// TestBuildSessions_WindowAnchorsAndOrder proves which events the echo lists
// and in what order: logged sessions whose day is inside the trend window
// ending today, newest first with the id breaking a tie; never an anchor-only
// capture (it isn't a session amend could correct), another kind, a session
// outside the window, or one whose day can't be resolved.
func TestBuildSessions_WindowAnchorsAndOrder(t *testing.T) {
	t.Parallel()

	bodyState := sessionEvent("obs_2026_07_20_009", "2026-07-20", "10:00:00", map[string]any{"part": "knee"})
	bodyState.Kind = observations.KindBodyState
	undated := sessionEvent("obs_2026_07_18_009", "2026-07-18", "10:00:00", map[string]any{"type": "push"})
	undated.LogicalDate, undated.OccurredAt = "", "not a time"

	events := []observations.Event{
		sessionEvent("obs_2026_07_17_001", "2026-07-17", "07:30:00", map[string]any{"type": "pull"}),
		sessionEvent("obs_2026_07_20_001", "2026-07-20", "08:00:00", map[string]any{"type": "legs"}),
		sessionEvent("obs_2026_07_20_002", "2026-07-20", "08:00:00", map[string]any{"type": "cardio"}),
		sessionEvent("obs_2026_07_20_003", "2026-07-20", "09:00:00", map[string]any{"anchor": true}),
		sessionEvent("obs_2026_07_20_004", "2026-07-20", "09:30:00", map[string]any{"anchor": true, "type": "push"}),
		sessionEvent("obs_2026_06_22_001", "2026-06-22", "08:00:00", map[string]any{"type": "push"}),
		sessionEvent("obs_2026_06_23_001", "2026-06-23", "08:00:00", map[string]any{"type": "push"}),
		bodyState,
		undated,
	}

	sessions := BuildSessions(events, mustTime(t, mondayNoon), time.UTC)
	ids := make([]string, 0, len(sessions))
	for _, s := range sessions {
		ids = append(ids, s.ID)
	}
	assert.Equal(t, []string{
		"obs_2026_07_20_004", // newest; a session that also logged the anchor is a session
		"obs_2026_07_20_002", // same instant as _001: the id breaks the tie
		"obs_2026_07_20_001",
		"obs_2026_07_17_001",
		"obs_2026_06_23_001", // 27 days back: the window's oldest day
	}, ids, "2026-06-22 is 28 days back, outside the four-week window")
}
