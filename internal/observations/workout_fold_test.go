package observations

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// workoutSession builds a base KindWorkout session logged on date at 19:05
// local — the shape `workout log` writes.
func workoutSession(id, date string, payload map[string]any) Event {
	return Event{
		ID: id, Schema: Schema, Kind: KindWorkout,
		RecordedAt: date + "T19:05:00-05:00", OccurredAt: date + "T19:05:00-05:00",
		OccurredAtPrecision: PrecisionExact,
		LogicalDate:         date, Source: SourceMicrolog,
		Payload: payload, Refs: map[string]any{},
	}
}

// workoutCorrection builds a non-redate correction: a KindWorkout event
// carrying refs.corrects plus only the changed fields, filed under the
// session's current day (date) with its own nanosecond recorded_at.
func workoutCorrection(id, target, date, recordedAt string, payload map[string]any) Event {
	return Event{
		ID: id, Schema: Schema, Kind: KindWorkout,
		RecordedAt: recordedAt, OccurredAt: date + "T19:05:00-05:00",
		OccurredAtPrecision: PrecisionExact,
		LogicalDate:         date, Source: SourceMicrolog,
		Payload: payload, Refs: map[string]any{RefCorrects: target},
	}
}

// workoutRedate builds a redate-marked correction moving the shared test
// session (workoutBase) to newDate at approximate precision — the bare-day
// `--day` form.
func workoutRedate(id, newDate, recordedAt string, payload map[string]any) Event {
	if payload == nil {
		payload = map[string]any{}
	}
	return Event{
		ID: id, Schema: Schema, Kind: KindWorkout,
		RecordedAt: recordedAt, OccurredAt: newDate + "T00:00:00-05:00",
		OccurredAtPrecision: PrecisionApproximate,
		LogicalDate:         newDate, Source: SourceMicrolog,
		Payload: payload, Refs: map[string]any{RefCorrects: workoutBase, RefRedate: true},
	}
}

const workoutBase = "obs_2026_01_15_001"

// TestFoldWorkoutAmendments_OnlyStatedFieldsChange proves an RPE-only
// correction sets the RPE and leaves the session's duration and type exactly
// as logged (AC-4).
func TestFoldWorkoutAmendments_OnlyStatedFieldsChange(t *testing.T) {
	events := []Event{
		workoutSession(workoutBase, "2026-01-15", map[string]any{"type": "climbing", "duration_min": 60}),
		workoutCorrection("obs_2026_01_15_002", workoutBase, "2026-01-15", "2026-01-15T21:30:00.123456789-05:00",
			map[string]any{"rpe": 4}),
	}
	folded := FoldWorkoutAmendments(events)
	require.Len(t, folded, 1, "the correction is dropped, the session folded")
	got := findEvent(t, folded, workoutBase)
	assert.Equal(t, 4, got.Payload["rpe"], "the stated field is set")
	assert.Equal(t, 60, got.Payload["duration_min"], "an unstated field keeps its logged value")
	assert.Equal(t, "climbing", got.Payload["type"], "an unstated field keeps its logged value")
	assert.Equal(t, "2026-01-15", got.LogicalDate, "a non-redate correction never moves the session")
	assert.Equal(t, "2026-01-15T19:05:00-05:00", got.OccurredAt)
	assert.Equal(t, PrecisionExact, got.OccurredAtPrecision)
	_, isCorrection := got.Refs[RefCorrects]
	assert.False(t, isCorrection, "the folded session never reads as a correction")
}

// TestFoldWorkoutAmendments_ChainLastWriteWins proves two RPE corrections fold
// in order — the second wins — and both stay in the input history (AC-6).
func TestFoldWorkoutAmendments_ChainLastWriteWins(t *testing.T) {
	events := []Event{
		workoutSession(workoutBase, "2026-01-15", map[string]any{"type": "climbing"}),
		workoutCorrection("obs_2026_01_15_002", workoutBase, "2026-01-15", "2026-01-15T21:30:00-05:00",
			map[string]any{"rpe": 4}),
		workoutCorrection("obs_2026_01_15_003", workoutBase, "2026-01-15", "2026-01-15T21:45:00-05:00",
			map[string]any{"rpe": 6}),
	}
	folded := FoldWorkoutAmendments(events)
	require.Len(t, folded, 1, "one session, however often it is amended")
	assert.Greater(t, len(events), len(folded), "both corrections remain in the input history")
	assert.Equal(t, 6, findEvent(t, folded, workoutBase).Payload["rpe"], "the latest correction wins")
	assert.Equal(t, 4, events[1].Payload["rpe"], "the earlier correction is untouched history")
}

// TestFoldWorkoutAmendments_RedateMovesDateTrio proves a redate-marked
// correction moves occurred_at, precision, and logical_date together, and a
// later non-redate field edit keeps the new dates (AC-7, fold mechanics).
func TestFoldWorkoutAmendments_RedateMovesDateTrio(t *testing.T) {
	t.Run("redate moves the trio", func(t *testing.T) {
		events := []Event{
			workoutSession(workoutBase, "2026-01-15", map[string]any{"type": "climbing"}),
			workoutRedate("obs_2026_01_14_001", "2026-01-14", "2026-01-15T21:31:00-05:00", nil),
		}
		got := findEvent(t, FoldWorkoutAmendments(events), workoutBase)
		assert.Equal(t, "2026-01-14", got.LogicalDate)
		assert.Equal(t, "2026-01-14T00:00:00-05:00", got.OccurredAt)
		assert.Equal(t, PrecisionApproximate, got.OccurredAtPrecision)
		assert.Equal(t, "climbing", got.Payload["type"], "a date-only correction leaves the payload")
		assert.Equal(t, workoutBase, got.ID, "the session keeps its own id")
	})

	t.Run("a later field edit keeps the new dates", func(t *testing.T) {
		events := []Event{
			workoutSession(workoutBase, "2026-01-15", map[string]any{"type": "climbing"}),
			workoutRedate("obs_2026_01_14_001", "2026-01-14", "2026-01-15T21:31:00-05:00", nil),
			// Filed under the session's current (re-dated) day, as the write path does.
			workoutCorrection("obs_2026_01_14_002", workoutBase, "2026-01-14", "2026-01-15T21:40:00-05:00",
				map[string]any{"rpe": 4}),
		}
		got := findEvent(t, FoldWorkoutAmendments(events), workoutBase)
		assert.Equal(t, 4, got.Payload["rpe"])
		assert.Equal(t, "2026-01-14", got.LogicalDate, "the non-redate edit never reverts the re-date")
		assert.Equal(t, PrecisionApproximate, got.OccurredAtPrecision)
	})

	t.Run("a non-redate correction carrying other dates never moves the session", func(t *testing.T) {
		events := []Event{
			workoutSession(workoutBase, "2026-01-15", map[string]any{"type": "climbing"}),
			workoutCorrection("obs_2026_01_10_001", workoutBase, "2026-01-10", "2026-01-15T21:40:00-05:00",
				map[string]any{"rpe": 4}),
		}
		got := findEvent(t, FoldWorkoutAmendments(events), workoutBase)
		assert.Equal(t, "2026-01-15", got.LogicalDate, "dates come only from redate-marked corrections")
		assert.Equal(t, "2026-01-15T19:05:00-05:00", got.OccurredAt)
	})

	t.Run("the latest redate wins", func(t *testing.T) {
		events := []Event{
			workoutSession(workoutBase, "2026-01-15", map[string]any{"type": "climbing"}),
			workoutRedate("obs_2026_01_14_001", "2026-01-14", "2026-01-15T21:31:00-05:00", nil),
			workoutRedate("obs_2026_01_12_001", "2026-01-12", "2026-01-15T21:32:00-05:00", nil),
		}
		got := findEvent(t, FoldWorkoutAmendments(events), workoutBase)
		assert.Equal(t, "2026-01-12", got.LogicalDate)
	})

	t.Run("a range end moves with the trio", func(t *testing.T) {
		redate := workoutRedate("obs_2026_01_14_001", "2026-01-14", "2026-01-15T21:31:00-05:00", nil)
		redate.OccurredAt = "2026-01-14T18:00:00-05:00"
		redate.OccurredAtPrecision = PrecisionRange
		end := "2026-01-14T19:00:00-05:00"
		redate.OccurredAtEnd = &end
		events := []Event{workoutSession(workoutBase, "2026-01-15", map[string]any{"type": "climbing"}), redate}

		got := findEvent(t, FoldWorkoutAmendments(events), workoutBase)
		require.NotNil(t, got.OccurredAtEnd)
		assert.Equal(t, end, *got.OccurredAtEnd)
		assert.Equal(t, PrecisionRange, got.OccurredAtPrecision)
		end = "mutated"
		assert.Equal(t, "2026-01-14T19:00:00-05:00", *got.OccurredAtEnd, "the folded end is a copy, not the correction's pointer")
	})
}

// TestFoldWorkoutAmendments_ChronologicalOrder proves corrections fold by
// recorded_at, not by input order or id: a re-date files under the earlier
// logical day, so a later correction can carry a lexically earlier id.
func TestFoldWorkoutAmendments_ChronologicalOrder(t *testing.T) {
	t.Run("input order does not matter", func(t *testing.T) {
		events := []Event{
			workoutCorrection("obs_2026_01_15_003", workoutBase, "2026-01-15", "2026-01-15T21:45:00-05:00",
				map[string]any{"rpe": 6}),
			workoutSession(workoutBase, "2026-01-15", map[string]any{"type": "climbing"}),
			workoutCorrection("obs_2026_01_15_002", workoutBase, "2026-01-15", "2026-01-15T21:30:00-05:00",
				map[string]any{"rpe": 4}),
		}
		assert.Equal(t, 6, findEvent(t, FoldWorkoutAmendments(events), workoutBase).Payload["rpe"])
	})

	t.Run("a later correction with a lexically earlier id wins", func(t *testing.T) {
		events := []Event{
			workoutSession(workoutBase, "2026-01-15", map[string]any{"type": "climbing"}),
			// Recorded second, but its re-dated id sorts before the first correction's.
			workoutRedate("obs_2026_01_10_001", "2026-01-10", "2026-01-15T21:50:00-05:00",
				map[string]any{"rpe": 7}),
			workoutCorrection("obs_2026_01_15_002", workoutBase, "2026-01-15", "2026-01-15T21:30:00-05:00",
				map[string]any{"rpe": 4}),
		}
		got := findEvent(t, FoldWorkoutAmendments(events), workoutBase)
		assert.Equal(t, 7, got.Payload["rpe"], "recorded_at order, not id order")
		assert.Equal(t, "2026-01-10", got.LogicalDate)
	})

	t.Run("instants compare across offsets and sub-second precision", func(t *testing.T) {
		events := []Event{
			workoutSession(workoutBase, "2026-01-15", map[string]any{"type": "climbing"}),
			// 21:30:00.9-05:00 is 02:30:00.9Z — 0.4s after the UTC stamp below,
			// though it sorts first both as a string and by id.
			workoutCorrection("obs_2026_01_15_002", workoutBase, "2026-01-15", "2026-01-15T21:30:00.9-05:00",
				map[string]any{"rpe": 8}),
			workoutCorrection("obs_2026_01_15_003", workoutBase, "2026-01-15", "2026-01-16T02:30:00.5Z",
				map[string]any{"rpe": 3}),
		}
		assert.Equal(t, 8, findEvent(t, FoldWorkoutAmendments(events), workoutBase).Payload["rpe"])
	})

	t.Run("an equal recorded_at falls back to the id", func(t *testing.T) {
		const at = "2026-01-15T21:30:00.000000001-05:00"
		events := []Event{
			workoutSession(workoutBase, "2026-01-15", map[string]any{"type": "climbing"}),
			workoutCorrection("obs_2026_01_15_003", workoutBase, "2026-01-15", at, map[string]any{"rpe": 6}),
			workoutCorrection("obs_2026_01_15_002", workoutBase, "2026-01-15", at, map[string]any{"rpe": 4}),
		}
		assert.Equal(t, 6, findEvent(t, FoldWorkoutAmendments(events), workoutBase).Payload["rpe"])
	})

	t.Run("an unparseable recorded_at never outranks a well-formed one", func(t *testing.T) {
		events := []Event{
			workoutSession(workoutBase, "2026-01-15", map[string]any{"type": "climbing"}),
			workoutCorrection("obs_2026_01_15_002", workoutBase, "2026-01-15", "2026-01-15T21:30:00-05:00",
				map[string]any{"rpe": 4}),
			workoutCorrection("obs_2026_01_15_003", workoutBase, "2026-01-15", "not-a-time",
				map[string]any{"rpe": 9}),
		}
		assert.Equal(t, 4, findEvent(t, FoldWorkoutAmendments(events), workoutBase).Payload["rpe"])
	})
}

// TestFoldWorkoutAmendments_Lists proves a list field carries its full
// replacement list (Q4 → replace), including the []any shape a disk read
// decodes to.
func TestFoldWorkoutAmendments_Lists(t *testing.T) {
	amend := workoutCorrection("obs_2026_01_15_002", workoutBase, "2026-01-15", "2026-01-15T21:30:00-05:00",
		map[string]any{"body_parts": []string{"fingers", "forearms"}})
	raw, err := json.Marshal(amend)
	require.NoError(t, err)
	var decoded Event
	require.NoError(t, json.Unmarshal(raw, &decoded))

	events := []Event{
		workoutSession(workoutBase, "2026-01-15", map[string]any{"type": "climbing", "body_parts": []string{"back"}}),
		decoded,
	}
	got := findEvent(t, FoldWorkoutAmendments(events), workoutBase)
	assert.Equal(t, []any{"fingers", "forearms"}, got.Payload["body_parts"], "the stated list replaces the logged one")
}

// TestFoldWorkoutAmendments_Passthrough covers the shape guarantees the fold
// shares with the memory fold: other kinds untouched, bare corrections
// dropped, refs.cleared honored, input never mutated, order preserved.
func TestFoldWorkoutAmendments_Passthrough(t *testing.T) {
	t.Run("non-workout events pass through untouched", func(t *testing.T) {
		soreness := Event{
			ID: "obs_2026_01_15_002", Schema: Schema, Kind: KindBodyState,
			RecordedAt: "2026-01-15T19:06:00-05:00", OccurredAt: "2026-01-15T19:05:00-05:00",
			OccurredAtPrecision: PrecisionExact, LogicalDate: "2026-01-15", Source: SourceMicrolog,
			Payload: map[string]any{"body_part": "fingers", "soreness": 3},
		}
		// A memory correction is the memory fold's business, not this one's.
		memAmend := memoryAmendment("obs_2026_01_15_004", "obs_2026_01_15_005", "2026-01-15T21:00:00-05:00",
			map[string]any{MemoryFieldText: "x"}, nil)
		events := []Event{
			workoutSession(workoutBase, "2026-01-15", map[string]any{"type": "climbing"}),
			soreness,
			workoutCorrection("obs_2026_01_15_003", workoutBase, "2026-01-15", "2026-01-15T21:30:00-05:00",
				map[string]any{"rpe": 4}),
			memAmend,
		}
		folded := FoldWorkoutAmendments(events)
		require.Len(t, folded, 3, "session + soreness + memory correction; the workout correction dropped")
		assert.Equal(t, 3, findEvent(t, folded, soreness.ID).Payload["soreness"])
		assert.Equal(t, memAmend.ID, findEvent(t, folded, memAmend.ID).ID)
		assert.Equal(t, 4, findEvent(t, folded, workoutBase).Payload["rpe"])
	})

	t.Run("a workout correction never folds onto another kind", func(t *testing.T) {
		mem := memoryEvent("obs_2026_01_15_001", "2026-01-15", map[string]any{MemoryFieldText: "t"}, nil)
		events := []Event{
			mem,
			workoutCorrection("obs_2026_01_15_002", mem.ID, "2026-01-15", "2026-01-15T21:30:00-05:00",
				map[string]any{"rpe": 4}),
		}
		folded := FoldWorkoutAmendments(events)
		require.Len(t, folded, 1)
		_, has := folded[0].Payload["rpe"]
		assert.False(t, has, "the memory is untouched")
	})

	t.Run("a bare correction with an absent session is dropped", func(t *testing.T) {
		events := []Event{
			workoutRedate("obs_2026_01_14_001", "2026-01-14", "2026-01-15T21:31:00-05:00",
				map[string]any{"rpe": 4}),
		}
		assert.Empty(t, FoldWorkoutAmendments(events), "a correction never counts as a session of its own")
	})

	t.Run("refs.cleared removes a field (parity)", func(t *testing.T) {
		amend := workoutCorrection("obs_2026_01_15_002", workoutBase, "2026-01-15", "2026-01-15T21:30:00-05:00", nil)
		amend.Refs[RefCleared] = []string{"note"}
		events := []Event{
			workoutSession(workoutBase, "2026-01-15", map[string]any{"type": "climbing", "note": "drop me"}),
			amend,
		}
		got := findEvent(t, FoldWorkoutAmendments(events), workoutBase)
		_, present := got.Payload["note"]
		assert.False(t, present)
		assert.Equal(t, "climbing", got.Payload["type"])
	})

	t.Run("the input session is not mutated", func(t *testing.T) {
		base := workoutSession(workoutBase, "2026-01-15", map[string]any{"type": "climbing", "duration_min": 60})
		events := []Event{
			base,
			workoutRedate("obs_2026_01_14_001", "2026-01-14", "2026-01-15T21:31:00-05:00",
				map[string]any{"rpe": 4, "type": "bouldering"}),
		}
		_ = FoldWorkoutAmendments(events)
		assert.Equal(t, "climbing", base.Payload["type"], "fold must not mutate the caller's payload")
		_, has := base.Payload["rpe"]
		assert.False(t, has)
		assert.Equal(t, "2026-01-15", base.LogicalDate, "fold must not mutate the caller's dates")
		assert.Empty(t, base.Refs, "fold must not mutate the caller's refs")
		assert.Equal(t, "climbing", events[0].Payload["type"], "the slice element is untouched too")
	})

	t.Run("input order is preserved, even across a re-date", func(t *testing.T) {
		s1 := workoutSession("obs_2026_01_12_001", "2026-01-12", map[string]any{"type": "run"})
		s2 := workoutSession(workoutBase, "2026-01-15", map[string]any{"type": "climbing"})
		events := []Event{
			s1,
			workoutRedate("obs_2026_01_10_001", "2026-01-10", "2026-01-15T21:31:00-05:00", nil),
			s2,
		}
		folded := FoldWorkoutAmendments(events)
		require.Len(t, folded, 2)
		assert.Equal(t, s1.ID, folded[0].ID)
		assert.Equal(t, s2.ID, folded[1].ID, "the folded session keeps its base position")
		assert.Equal(t, "2026-01-10", folded[1].LogicalDate)
	})

	t.Run("a non-bool redate marker is not a re-date", func(t *testing.T) {
		redate := workoutRedate("obs_2026_01_14_001", "2026-01-14", "2026-01-15T21:31:00-05:00", nil)
		redate.Refs[RefRedate] = "true"
		events := []Event{workoutSession(workoutBase, "2026-01-15", map[string]any{"type": "climbing"}), redate}
		assert.Equal(t, "2026-01-15", findEvent(t, FoldWorkoutAmendments(events), workoutBase).LogicalDate)
	})
}

// TestIsWorkoutAnchorOnly pins which workout events are daily-anchor captures
// with no session on them — the one rule amend's anchor refusal and the
// `workout --json` sessions echo share: the marker or items with no session
// field is anchor-only; any session field (even alongside the anchor) makes it
// a session; a bare partial session is a session; another kind never is.
func TestIsWorkoutAnchorOnly(t *testing.T) {
	cases := []struct {
		name    string
		kind    Kind
		payload map[string]any
		want    bool
	}{
		{name: "bare anchor marker", kind: KindWorkout, payload: map[string]any{"anchor": true}, want: true},
		{
			name: "anchor items", kind: KindWorkout,
			payload: map[string]any{"anchor": true, "anchor_items": []any{map[string]any{"name": "squats", "count": 55}}},
			want:    true,
		},
		{name: "session that also logged the anchor", kind: KindWorkout, payload: map[string]any{"anchor": true, "type": "push"}},
		{name: "anchor with a note", kind: KindWorkout, payload: map[string]any{"anchor": true, "note": "quick floor"}},
		{name: "plain session", kind: KindWorkout, payload: map[string]any{"type": "climbing", "rpe": 4}},
		{name: "bare partial session", kind: KindWorkout, payload: map[string]any{"parse": ParseMarkerPartial}},
		{name: "another kind with an anchor key", kind: KindBodyState, payload: map[string]any{"anchor": true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := workoutSession(workoutBase, "2026-01-15", tc.payload)
			ev.Kind = tc.kind
			assert.Equal(t, tc.want, IsWorkoutAnchorOnly(ev))
		})
	}
}
