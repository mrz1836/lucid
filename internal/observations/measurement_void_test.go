package observations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// measurementReading builds a KindMeasurement reading the shape
// `lucid obs measurement <metric> <value> <unit>` writes.
func measurementReading(id, date, metric, value, unit string) Event {
	payload := map[string]any{"metric": metric, "value": value}
	if unit != "" {
		payload["unit"] = unit
	}
	return Event{
		ID: id, Schema: Schema, Kind: KindMeasurement,
		RecordedAt: date + "T08:10:00-04:00", OccurredAt: date + "T08:10:00-04:00",
		OccurredAtPrecision: PrecisionExact,
		LogicalDate:         date, Source: SourceMicrolog,
		Payload: payload, Tags: []string{}, Refs: map[string]any{},
	}
}

// measurementVoid builds the void `lucid obs void <target>` writes: a
// KindMeasurement event with an empty payload carrying refs.corrects and the
// refs.void marker, filed under its own (current) day.
func measurementVoid(id, date, target string) Event {
	return Event{
		ID: id, Schema: Schema, Kind: KindMeasurement,
		RecordedAt: date + "T09:00:00-04:00", OccurredAt: date + "T09:00:00-04:00",
		OccurredAtPrecision: PrecisionExact,
		LogicalDate:         date, Source: SourceMicrolog,
		Payload: map[string]any{}, Tags: []string{},
		Refs: map[string]any{RefCorrects: target, RefVoid: true},
	}
}

// eventIDs lists the ids of events in order.
func eventIDs(events []Event) []string {
	ids := make([]string, 0, len(events))
	for _, e := range events {
		ids = append(ids, e.ID)
	}
	return ids
}

// TestMeasurementVoidTarget_Marker proves only a measurement event with a JSON
// boolean refs.void and a non-empty string refs.corrects reads as a void.
func TestMeasurementVoidTarget_Marker(t *testing.T) {
	const target = "obs_2026_03_04_002"

	t.Run("void", func(t *testing.T) {
		got, ok := MeasurementVoidTarget(measurementVoid("obs_2026_03_05_001", "2026-03-05", target))
		require.True(t, ok)
		assert.Equal(t, target, got)
	})

	t.Run("string_true_is_not_a_void", func(t *testing.T) {
		e := measurementVoid("obs_2026_03_05_001", "2026-03-05", target)
		e.Refs[RefVoid] = "true"
		_, ok := MeasurementVoidTarget(e)
		assert.False(t, ok)
	})

	t.Run("false_is_not_a_void", func(t *testing.T) {
		e := measurementVoid("obs_2026_03_05_001", "2026-03-05", target)
		e.Refs[RefVoid] = false
		_, ok := MeasurementVoidTarget(e)
		assert.False(t, ok)
	})

	t.Run("missing_corrects", func(t *testing.T) {
		e := measurementVoid("obs_2026_03_05_001", "2026-03-05", target)
		delete(e.Refs, RefCorrects)
		_, ok := MeasurementVoidTarget(e)
		assert.False(t, ok)
	})

	t.Run("empty_corrects", func(t *testing.T) {
		e := measurementVoid("obs_2026_03_05_001", "2026-03-05", "")
		_, ok := MeasurementVoidTarget(e)
		assert.False(t, ok)
	})

	t.Run("nil_refs", func(t *testing.T) {
		e := measurementVoid("obs_2026_03_05_001", "2026-03-05", target)
		e.Refs = nil
		_, ok := MeasurementVoidTarget(e)
		assert.False(t, ok)
	})

	t.Run("non_measurement_kind", func(t *testing.T) {
		e := measurementVoid("obs_2026_03_05_001", "2026-03-05", target)
		e.Kind = KindPain
		_, ok := MeasurementVoidTarget(e)
		assert.False(t, ok)
	})

	t.Run("plain_reading", func(t *testing.T) {
		_, ok := MeasurementVoidTarget(measurementReading("obs_2026_03_04_001", "2026-03-04", "weight", "70.5", "kg"))
		assert.False(t, ok)
	})
}

// TestFoldMeasurementVoids_DropsTargetAndVoid proves the fold removes a void
// and the reading it names — even when the void is filed on a later day —
// keeps every other reading in input order, drops a void of a missing id by
// itself, and never mutates its input.
func TestFoldMeasurementVoids_DropsTargetAndVoid(t *testing.T) {
	events := []Event{
		measurementReading("obs_2026_03_04_001", "2026-03-04", "bp-systolic", "121", "mmHg"),
		measurementReading("obs_2026_03_04_002", "2026-03-04", "bp-systolic", "125", "mmHg"),
		measurementReading("obs_2026_03_05_001", "2026-03-05", "weight", "70.5", "kg"),
		measurementVoid("obs_2026_03_09_001", "2026-03-09", "obs_2026_03_04_002"),
		measurementVoid("obs_2026_03_09_002", "2026-03-09", "obs_2026_01_01_009"), // names nothing present
	}
	before := make([]Event, len(events))
	copy(before, events)

	folded := FoldMeasurementVoids(events)
	assert.Equal(t, []string{"obs_2026_03_04_001", "obs_2026_03_05_001"}, eventIDs(folded))
	assert.Equal(t, before, events, "the input slice is never mutated")

	assert.Empty(t, FoldMeasurementVoids(nil), "an empty history folds to nothing")
}

// TestFoldMeasurementVoids_OtherKindsUntouched proves a workout correction, a
// memory amendment, and a pain reading pass through the measurement fold
// byte-equal, even when a void names one of their ids.
func TestFoldMeasurementVoids_OtherKindsUntouched(t *testing.T) {
	workout := workoutCorrection("obs_2026_01_15_002", workoutBase, "2026-01-15",
		"2026-01-15T21:30:00.123456789-05:00", map[string]any{"rpe": 4})
	memory := memoryAmendment("obs_2026_07_02_002", "obs_2026_07_02_001",
		"2026-07-02T13:00:00-04:00", map[string]any{"tone": "warm"}, nil)
	pain := Event{
		ID: "obs_2026_07_02_003", Schema: Schema, Kind: KindPain,
		RecordedAt: "2026-07-02T08:00:00-04:00", OccurredAt: "2026-07-02T08:00:00-04:00",
		OccurredAtPrecision: PrecisionExact, LogicalDate: "2026-07-02", Source: SourceMicrolog,
		Payload: map[string]any{"intensity": 6, "site": "knee"}, Tags: []string{}, Refs: map[string]any{},
	}
	events := []Event{
		workout,
		memory,
		pain,
		measurementVoid("obs_2026_07_03_001", "2026-07-03", pain.ID), // a stray void naming a non-measurement
	}

	folded := FoldMeasurementVoids(events)
	require.Len(t, folded, 3, "only the void is dropped; it never removes another kind")
	for i, want := range []Event{workout, memory, pain} {
		wantLine, err := want.MarshalLine()
		require.NoError(t, err)
		gotLine, err := folded[i].MarshalLine()
		require.NoError(t, err)
		assert.Equal(t, string(wantLine), string(gotLine), "event %s passes through byte-equal", want.ID)
	}
}
