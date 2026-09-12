package observations

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// memoryEvent builds a base KindMemory event for the fold tests.
func memoryEvent(id, date string, payload, refs map[string]any) Event {
	return Event{
		ID: id, Schema: Schema, Kind: KindMemory,
		RecordedAt: date + "T12:00:00-04:00", OccurredAt: date + "T12:00:00-04:00",
		OccurredAtPrecision: PrecisionExact,
		LogicalDate:         date, Source: SourceExcavation,
		Payload: payload, Refs: refs,
	}
}

// memoryAmendment builds an amendment: a KindMemory event carrying
// refs.corrects plus only the changed fields. recordedAt distinguishes it from
// the base so history timestamps are meaningful.
func memoryAmendment(id, date, target, recordedAt string, payload, refs map[string]any) Event {
	if refs == nil {
		refs = map[string]any{}
	}
	refs[refsCorrects] = target
	return Event{
		ID: id, Schema: Schema, Kind: KindMemory,
		RecordedAt: recordedAt, OccurredAt: date + "T12:00:00-04:00",
		OccurredAtPrecision: PrecisionExact,
		LogicalDate:         date, Source: SourceExcavation,
		Payload: payload, Refs: refs,
	}
}

// findEvent returns the folded event with the given id, failing the test if it
// is missing.
func findEvent(t *testing.T, events []Event, id string) Event {
	t.Helper()
	for _, e := range events {
		if e.ID == id {
			return e
		}
	}
	require.Failf(t, "event not found", "no event with id %q in %d folded events", id, len(events))
	return Event{}
}

func TestFoldMemoryAmendments(t *testing.T) {
	const base = "obs_2026_07_02_001"

	t.Run("single field amend", func(t *testing.T) {
		events := []Event{
			memoryEvent(base, "2026-07-02", map[string]any{MemoryFieldText: "old", MemoryFieldCertainty: "hazy"}, nil),
			memoryAmendment("obs_2026_07_02_002", "2026-07-02", base, "2026-07-02T13:00:00-04:00",
				map[string]any{MemoryFieldText: "new"}, nil),
		}
		folded := FoldMemoryAmendments(events)
		require.Len(t, folded, 1, "the amendment is dropped, the base folded")
		got := findEvent(t, folded, base)
		assert.Equal(t, "new", got.Payload[MemoryFieldText], "text is amended")
		assert.Equal(t, "hazy", got.Payload[MemoryFieldCertainty], "an untouched field is unchanged")
	})

	t.Run("multiple fields in one amendment", func(t *testing.T) {
		events := []Event{
			memoryEvent(base, "2026-07-02", map[string]any{MemoryFieldText: "old", MemoryFieldCertainty: "hazy"}, nil),
			memoryAmendment("obs_2026_07_02_002", "2026-07-02", base, "2026-07-02T13:00:00-04:00",
				map[string]any{MemoryFieldText: "new", MemoryFieldFollowUp: "call mom"}, nil),
		}
		got := findEvent(t, FoldMemoryAmendments(events), base)
		assert.Equal(t, "new", got.Payload[MemoryFieldText])
		assert.Equal(t, "call mom", got.Payload[MemoryFieldFollowUp])
		assert.Equal(t, "hazy", got.Payload[MemoryFieldCertainty], "untouched field stays")
	})

	t.Run("chain of two amendments is last-write-wins", func(t *testing.T) {
		events := []Event{
			memoryEvent(base, "2026-07-02", map[string]any{MemoryFieldText: "v1"}, nil),
			memoryAmendment("obs_2026_07_02_002", "2026-07-02", base, "2026-07-02T13:00:00-04:00",
				map[string]any{MemoryFieldText: "v2"}, nil),
			memoryAmendment("obs_2026_07_02_003", "2026-07-02", base, "2026-07-02T14:00:00-04:00",
				map[string]any{MemoryFieldText: "v3"}, nil),
		}
		got := findEvent(t, FoldMemoryAmendments(events), base)
		assert.Equal(t, "v3", got.Payload[MemoryFieldText], "latest amendment wins")
	})

	t.Run("chain folds in id order regardless of input order", func(t *testing.T) {
		// Amendments supplied out of order must still fold chronologically by id.
		events := []Event{
			memoryAmendment("obs_2026_07_02_003", "2026-07-02", base, "2026-07-02T14:00:00-04:00",
				map[string]any{MemoryFieldText: "v3"}, nil),
			memoryEvent(base, "2026-07-02", map[string]any{MemoryFieldText: "v1"}, nil),
			memoryAmendment("obs_2026_07_02_002", "2026-07-02", base, "2026-07-02T13:00:00-04:00",
				map[string]any{MemoryFieldText: "v2"}, nil),
		}
		got := findEvent(t, FoldMemoryAmendments(events), base)
		assert.Equal(t, "v3", got.Payload[MemoryFieldText])
	})

	t.Run("clear via refs.cleared (in-memory []string)", func(t *testing.T) {
		events := []Event{
			memoryEvent(base, "2026-07-02", map[string]any{MemoryFieldText: "keep", MemoryFieldFollowUp: "drop me"}, nil),
			memoryAmendment("obs_2026_07_02_002", "2026-07-02", base, "2026-07-02T13:00:00-04:00",
				nil, map[string]any{refsCleared: []string{MemoryFieldFollowUp}}),
		}
		got := findEvent(t, FoldMemoryAmendments(events), base)
		_, present := got.Payload[MemoryFieldFollowUp]
		assert.False(t, present, "cleared field is removed")
		assert.Equal(t, "keep", got.Payload[MemoryFieldText], "other fields survive the clear")
	})

	t.Run("clear survives a JSON round trip ([]any)", func(t *testing.T) {
		amend := memoryAmendment("obs_2026_07_02_002", "2026-07-02", base, "2026-07-02T13:00:00-04:00",
			nil, map[string]any{refsCleared: []string{MemoryFieldFollowUp}})
		// Marshal/unmarshal so refs.cleared decodes as the persisted []any shape,
		// not the in-memory []string — the form a disk read always carries.
		raw, err := json.Marshal(amend)
		require.NoError(t, err)
		var decoded Event
		require.NoError(t, json.Unmarshal(raw, &decoded))
		_, isAnySlice := decoded.Refs[refsCleared].([]any)
		require.True(t, isAnySlice, "a decoded refs.cleared is []any, proving the test exercises the persisted shape")

		events := []Event{
			memoryEvent(base, "2026-07-02", map[string]any{MemoryFieldText: "keep", MemoryFieldFollowUp: "drop me"}, nil),
			decoded,
		}
		got := findEvent(t, FoldMemoryAmendments(events), base)
		_, present := got.Payload[MemoryFieldFollowUp]
		assert.False(t, present, "a persisted []any clear marker still removes the field")
	})

	t.Run("era overlay", func(t *testing.T) {
		events := []Event{
			memoryEvent(base, "2026-07-02", map[string]any{MemoryFieldText: "t"}, map[string]any{refsEra: "era_old"}),
			memoryAmendment("obs_2026_07_02_002", "2026-07-02", base, "2026-07-02T13:00:00-04:00",
				nil, map[string]any{refsEra: "era_new"}),
		}
		got := findEvent(t, FoldMemoryAmendments(events), base)
		assert.Equal(t, "era_new", got.Refs[refsEra], "era ref is re-filed")
	})

	t.Run("amendment events are dropped from output", func(t *testing.T) {
		events := []Event{
			memoryEvent(base, "2026-07-02", map[string]any{MemoryFieldText: "old"}, nil),
			memoryAmendment("obs_2026_07_02_002", "2026-07-02", base, "2026-07-02T13:00:00-04:00",
				map[string]any{MemoryFieldText: "new"}, nil),
		}
		folded := FoldMemoryAmendments(events)
		require.Len(t, folded, 1)
		assert.Equal(t, base, folded[0].ID, "only the base id remains")
	})

	t.Run("non-memory events pass through untouched", func(t *testing.T) {
		pain := Event{
			ID: "obs_2026_07_02_001", Schema: Schema, Kind: KindPain,
			RecordedAt: "2026-07-02T09:00:00-04:00", OccurredAt: "2026-07-02T09:00:00-04:00",
			OccurredAtPrecision: PrecisionExact, LogicalDate: "2026-07-02", Source: SourceMicrolog,
			Payload: map[string]any{"intensity": 6},
		}
		mem := memoryEvent("obs_2026_07_02_002", "2026-07-02", map[string]any{MemoryFieldText: "old"}, nil)
		amend := memoryAmendment("obs_2026_07_02_003", "2026-07-02", mem.ID, "2026-07-02T13:00:00-04:00",
			map[string]any{MemoryFieldText: "new"}, nil)
		folded := FoldMemoryAmendments([]Event{pain, mem, amend})
		require.Len(t, folded, 2, "pain + folded memory; amendment dropped")
		gotPain := findEvent(t, folded, pain.ID)
		assert.Equal(t, KindPain, gotPain.Kind)
		assert.Equal(t, 6, gotPain.Payload["intensity"], "the non-memory event is untouched")
		assert.Equal(t, "new", findEvent(t, folded, mem.ID).Payload[MemoryFieldText])
	})

	t.Run("original base event is not mutated (folded copy)", func(t *testing.T) {
		baseEvent := memoryEvent(base, "2026-07-02", map[string]any{MemoryFieldText: "old", MemoryFieldFollowUp: "keep"}, map[string]any{refsEra: "era_old"})
		events := []Event{
			baseEvent,
			memoryAmendment("obs_2026_07_02_002", "2026-07-02", base, "2026-07-02T13:00:00-04:00",
				map[string]any{MemoryFieldText: "new"}, map[string]any{refsEra: "era_new", refsCleared: []string{MemoryFieldFollowUp}}),
		}
		_ = FoldMemoryAmendments(events)
		assert.Equal(t, "old", baseEvent.Payload[MemoryFieldText], "fold must not mutate the caller's payload")
		assert.Equal(t, "keep", baseEvent.Payload[MemoryFieldFollowUp], "a cleared field is untouched on the original")
		assert.Equal(t, "era_old", baseEvent.Refs[refsEra], "fold must not mutate the caller's refs")
	})

	t.Run("bare amendment with an absent target is dropped", func(t *testing.T) {
		events := []Event{
			memoryAmendment("obs_2026_07_02_002", "2026-07-02", "obs_2026_07_02_001", "2026-07-02T13:00:00-04:00",
				map[string]any{MemoryFieldText: "orphan"}, nil),
		}
		assert.Empty(t, FoldMemoryAmendments(events), "an amendment whose base is absent never renders")
	})

	t.Run("input order is preserved", func(t *testing.T) {
		m1 := memoryEvent("obs_2026_07_02_001", "2026-07-02", map[string]any{MemoryFieldText: "a"}, nil)
		m2 := memoryEvent("obs_2026_07_03_001", "2026-07-03", map[string]any{MemoryFieldText: "b"}, nil)
		folded := FoldMemoryAmendments([]Event{m1, m2})
		require.Len(t, folded, 2)
		assert.Equal(t, m1.ID, folded[0].ID)
		assert.Equal(t, m2.ID, folded[1].ID)
	})
}

func TestFoldMemoryHistory(t *testing.T) {
	const base = "obs_2026_07_02_001"

	t.Run("ordered trail with prior, new, and timestamps", func(t *testing.T) {
		events := []Event{
			memoryEvent(base, "2026-07-02",
				map[string]any{MemoryFieldText: "v1", MemoryFieldCertainty: "hazy", MemoryFieldFollowUp: "x"},
				map[string]any{refsEra: "era_old"}),
			// amend1: change text only.
			memoryAmendment("obs_2026_07_02_002", "2026-07-02", base, "2026-07-02T13:00:00-04:00",
				map[string]any{MemoryFieldText: "v2"}, nil),
			// amend2: bump certainty, clear follow_up, re-file era.
			memoryAmendment("obs_2026_07_02_003", "2026-07-02", base, "2026-07-02T14:00:00-04:00",
				map[string]any{MemoryFieldCertainty: "vivid"},
				map[string]any{refsEra: "era_new", refsCleared: []string{MemoryFieldFollowUp}}),
		}

		trail := FoldMemoryHistory(events, base)
		require.Len(t, trail, 4, "text + certainty + era + follow_up changes")

		// amend1 → text.
		assert.Equal(t, MemoryFieldText, trail[0].Field)
		assert.Equal(t, "v1", trail[0].Prior)
		assert.True(t, trail[0].PriorSet)
		assert.Equal(t, "v2", trail[0].New)
		assert.False(t, trail[0].Cleared)
		assert.Equal(t, "2026-07-02T13:00:00-04:00", trail[0].RecordedAt)

		// amend2 fields in stable (sorted) order: certainty, era, follow_up.
		assert.Equal(t, MemoryFieldCertainty, trail[1].Field)
		assert.Equal(t, "hazy", trail[1].Prior)
		assert.Equal(t, "vivid", trail[1].New)
		assert.Equal(t, "2026-07-02T14:00:00-04:00", trail[1].RecordedAt)

		assert.Equal(t, refsEra, trail[2].Field)
		assert.Equal(t, "era_old", trail[2].Prior)
		assert.Equal(t, "era_new", trail[2].New)
		assert.Equal(t, "2026-07-02T14:00:00-04:00", trail[2].RecordedAt)

		assert.Equal(t, MemoryFieldFollowUp, trail[3].Field)
		assert.Equal(t, "x", trail[3].Prior)
		assert.True(t, trail[3].PriorSet)
		assert.True(t, trail[3].Cleared, "the clear is recorded as a transition")
		assert.Empty(t, trail[3].New)
		assert.Equal(t, "2026-07-02T14:00:00-04:00", trail[3].RecordedAt)
	})

	t.Run("a field set for the first time has no prior", func(t *testing.T) {
		events := []Event{
			memoryEvent(base, "2026-07-02", map[string]any{MemoryFieldText: "v1"}, nil),
			memoryAmendment("obs_2026_07_02_002", "2026-07-02", base, "2026-07-02T13:00:00-04:00",
				map[string]any{MemoryFieldFollowUp: "new note"}, nil),
		}
		trail := FoldMemoryHistory(events, base)
		require.Len(t, trail, 1)
		assert.Equal(t, MemoryFieldFollowUp, trail[0].Field)
		assert.False(t, trail[0].PriorSet, "follow_up was unset before")
		assert.Empty(t, trail[0].Prior)
		assert.Equal(t, "new note", trail[0].New)
	})

	t.Run("unknown id yields no history", func(t *testing.T) {
		events := []Event{memoryEvent(base, "2026-07-02", map[string]any{MemoryFieldText: "v1"}, nil)}
		assert.Nil(t, FoldMemoryHistory(events, "obs_2026_07_02_999"))
	})

	t.Run("a base with no amendments yields no history", func(t *testing.T) {
		events := []Event{memoryEvent(base, "2026-07-02", map[string]any{MemoryFieldText: "v1"}, nil)}
		assert.Nil(t, FoldMemoryHistory(events, base))
	})

	t.Run("an amendment id is not a base and has no history", func(t *testing.T) {
		amendID := "obs_2026_07_02_002"
		events := []Event{
			memoryEvent(base, "2026-07-02", map[string]any{MemoryFieldText: "v1"}, nil),
			memoryAmendment(amendID, "2026-07-02", base, "2026-07-02T13:00:00-04:00",
				map[string]any{MemoryFieldText: "v2"}, nil),
		}
		assert.Nil(t, FoldMemoryHistory(events, amendID), "a correction-event id has no history of its own")
	})
}
