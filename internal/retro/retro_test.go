package retro

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// synthPark returns a synthetic park event. Every retro item in these tests is
// invented — real parked items live only in the private Ledger (retro.md §7).
func synthPark(id, item, source, logicalDate string) Retro {
	return Retro{
		EventID:     EventID(logicalDate, 1),
		Schema:      Schema,
		EventType:   EventPark,
		ID:          id,
		Item:        item,
		Status:      StatusOpen,
		Source:      source,
		RecordedAt:  "2026-08-23T21:45:10-04:00",
		LogicalDate: logicalDate,
	}
}

// TestRetroSchemaRoundTrip proves the raw-event envelope — the receipt/type/
// item-id fields and every transition field — marshals to one compact line and
// unmarshals back byte-for-value, and that the folded item carries exactly the
// documented fields (retro.md §2). It is the schema contract behind AC-9.
func TestRetroSchemaRoundTrip(t *testing.T) {
	park := Retro{
		EventID:     "retro_event_2026_08_23_001",
		Schema:      Schema,
		EventType:   EventPark,
		ID:          "R-001",
		Item:        "Revisit whether the Sunday walk opens with the deferred items",
		Status:      StatusOpen,
		Source:      "chat",
		RecordedAt:  "2026-08-23T21:45:10-04:00",
		LogicalDate: "2026-08-23",
		Tags:        []string{"process"},
	}
	require.NoError(t, park.Validate())

	line, err := park.MarshalLine()
	require.NoError(t, err)

	// Collection fields are stable: tags round-trip verbatim, and a nil refs
	// marshals as {} rather than null.
	var shape map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(line, &shape))
	assert.JSONEq(t, `["process"]`, string(shape["tags"]), "tags round-trip verbatim")
	assert.JSONEq(t, `{}`, string(shape["refs"]), "nil refs marshals as {}")

	// A bare event with no tags/refs normalizes both to [] / {} — never null.
	bareLine, err := Retro{
		EventID: "retro_event_2026_08_23_002", Schema: Schema, EventType: EventPark, ID: "R-002",
		Item: "bare", Status: StatusOpen, Source: SourceRetro,
		RecordedAt: "2026-08-23T21:45:11-04:00", LogicalDate: "2026-08-23",
	}.MarshalLine()
	require.NoError(t, err)
	var bareShape map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(bareLine, &bareShape))
	assert.JSONEq(t, `[]`, string(bareShape["tags"]), "nil tags marshals as []")
	assert.JSONEq(t, `{}`, string(bareShape["refs"]), "nil refs marshals as {}")
	for _, field := range []string{
		"event_id", "schema", "event_type", "id", "item", "status", "source",
		"resolution", "resolved_date", "defer_reason", "recorded_at", "logical_date",
	} {
		_, ok := shape[field]
		assert.Truef(t, ok, "the on-disk envelope always emits %q", field)
	}

	got, err := UnmarshalLine(line)
	require.NoError(t, err)
	got.Tags = park.Tags // normalized [] vs nil is not a value difference for the round-trip
	assert.Equal(t, park, roundTripStrip(got), "the event round-trips byte-for-value")

	// A resolve transition round-trips its resolution + resolved-date.
	resolve := Retro{
		EventID:      "retro_event_2026_08_30_004",
		Schema:       Schema,
		EventType:    EventResolve,
		ID:           "R-001",
		Status:       StatusResolved,
		Source:       SourceRetro,
		Resolution:   "Adopted it — the walk now opens with deferred items",
		ResolvedDate: "2026-08-30",
		RecordedAt:   "2026-08-30T10:00:00-04:00",
		LogicalDate:  "2026-08-30",
	}
	require.NoError(t, resolve.Validate())
	rLine, err := resolve.MarshalLine()
	require.NoError(t, err)
	rGot, err := UnmarshalLine(rLine)
	require.NoError(t, err)
	assert.Equal(t, "Adopted it — the walk now opens with deferred items", rGot.Resolution)
	assert.Equal(t, "2026-08-30", rGot.ResolvedDate)

	// The folded item carries exactly the eight documented fields.
	items := FoldState([]Retro{park, resolve})
	require.Len(t, items, 1)
	itemJSON, err := json.Marshal(items[0])
	require.NoError(t, err)
	var itemShape map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(itemJSON, &itemShape))
	assert.Len(t, itemShape, 8, "the folded item carries exactly eight fields")
	for _, field := range []string{
		"id", "parked_date", "source", "item", "status", "resolution", "resolved_date", "defer_reason",
	} {
		_, ok := itemShape[field]
		assert.Truef(t, ok, "the folded item carries %q", field)
	}
	assert.Equal(t, StatusResolved, items[0].Status)
	assert.Equal(t, "2026-08-30", items[0].ResolvedDate)
	assert.Equal(t, "2026-08-23", items[0].ParkedDate, "the parked date is the park event's logical date")
}

// roundTripStrip normalizes the nil-vs-[]/{} collection fields so a value
// comparison ignores the marshaling normalization (which is asserted separately).
func roundTripStrip(r Retro) Retro {
	if len(r.Refs) == 0 {
		r.Refs = nil
	}
	return r
}

// TestRetroFoldState_TransitionsAndOrder proves the fold: a park opens an item,
// resolve/defer transition it (most recent wins), a transition referencing no
// park is ignored, and the output is ascending-park order preserved by id.
func TestRetroFoldState_TransitionsAndOrder(t *testing.T) {
	events := []Retro{
		synthPark("R-001", "first", "chat", "2026-08-20"),
		synthPark("R-002", "second", "retro", "2026-08-21"),
		synthPark("R-003", "third", "retro", "2026-08-22"),
		{
			EventID: "retro_event_2026_08_23_001", Schema: Schema, EventType: EventResolve, ID: "R-001",
			Status: StatusResolved, Source: SourceRetro, Resolution: "did it", ResolvedDate: "2026-08-23",
			RecordedAt: "2026-08-23T09:00:00-04:00", LogicalDate: "2026-08-23",
		},
		{
			EventID: "retro_event_2026_08_24_001", Schema: Schema, EventType: EventDefer, ID: "R-002",
			Status: StatusDeferred, Source: SourceRetro, DeferReason: "someday",
			RecordedAt: "2026-08-24T09:00:00-04:00", LogicalDate: "2026-08-24",
		},
		// A transition naming an unparked id conjures nothing (index 5).
		{
			EventID: "retro_event_2026_08_25_001", Schema: Schema, EventType: EventResolve, ID: "R-099",
			Status: StatusResolved, Source: SourceRetro, Resolution: "ghost", ResolvedDate: "2026-08-25",
			RecordedAt: "2026-08-25T09:00:00-04:00", LogicalDate: "2026-08-25",
		},
		// A later resolve of R-002 (index 6): the first fold sees the prefix through
		// the defer, the second the whole stream — so no append grows the slice.
		{
			EventID: "retro_event_2026_08_26_001", Schema: Schema, EventType: EventResolve, ID: "R-002",
			Status: StatusResolved, Source: SourceRetro, Resolution: "finally", ResolvedDate: "2026-08-26",
			RecordedAt: "2026-08-26T09:00:00-04:00", LogicalDate: "2026-08-26",
		},
	}

	items := FoldState(events[:6]) // the stream through the R-002 defer (the ghost is index 5)
	require.Len(t, items, 3, "exactly the three parked items, no ghost")

	byID := map[string]Item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	assert.Equal(t, StatusResolved, byID["R-001"].Status)
	assert.Equal(t, "did it", byID["R-001"].Resolution)
	assert.Equal(t, "2026-08-23", byID["R-001"].ResolvedDate)
	assert.Equal(t, StatusDeferred, byID["R-002"].Status)
	assert.Equal(t, "someday", byID["R-002"].DeferReason)
	assert.Equal(t, StatusOpen, byID["R-003"].Status)

	// A deferred item that is later resolved folds to the most recent transition.
	items = FoldState(events)
	for _, it := range items {
		if it.ID == "R-002" {
			assert.Equal(t, StatusResolved, it.Status, "the most recent transition wins")
			assert.Equal(t, "finally", it.Resolution)
		}
	}
}

// TestRetroIDMinter proves the R-NNN grammar: zero-padded, numerically parsed,
// wider values legal, and every non-R-NNN string rejected.
func TestRetroIDMinter(t *testing.T) {
	assert.Equal(t, "R-001", ID(1))
	assert.Equal(t, "R-028", ID(28))
	assert.Equal(t, "R-1000", ID(1000))

	seq, ok := ParseSeq("R-001")
	assert.True(t, ok)
	assert.Equal(t, 1, seq)
	seq, ok = ParseSeq("R-1000")
	assert.True(t, ok)
	assert.Equal(t, 1000, seq)

	for _, bad := range []string{"", "R-", "R-abc", "focus_2026_08_23_001", "retro_event_2026_08_23_001", "12", "R-1.0"} {
		_, ok := ParseSeq(bad)
		assert.Falsef(t, ok, "%q is not a well-formed R-NNN", bad)
	}
}

// TestRetroEventIDMinter proves the per-write receipt grammar mirrors focus's
// date-and-seq id, and rejects non-receipt strings (including a bare R-NNN).
func TestRetroEventIDMinter(t *testing.T) {
	assert.Equal(t, "retro_event_2026_08_23_001", EventID("2026-08-23", 1))
	assert.Equal(t, "retro_event_2026_08_23_028", EventID("2026-08-23", 28))

	seq, ok := ParseEventSeq("retro_event_2026_08_23_007")
	assert.True(t, ok)
	assert.Equal(t, 7, seq)

	for _, bad := range []string{"", "R-001", "focus_2026_08_23_001", "retro_event_", "retro_event_2026_08_23_x"} {
		_, ok := ParseEventSeq(bad)
		assert.Falsef(t, ok, "%q is not a well-formed receipt", bad)
	}
}

// TestRetroValidate covers the structural guards: a good park and transition
// pass; a bad schema, unknown event type, malformed id, empty park item, unknown
// status, and malformed logical_date each fail.
func TestRetroValidate(t *testing.T) {
	good := synthPark("R-001", "a thing", "chat", "2026-08-23")
	require.NoError(t, good.Validate())

	bad := good
	bad.Schema = 99
	require.Error(t, bad.Validate(), "unsupported schema")

	bad = good
	bad.EventType = "delete"
	require.Error(t, bad.Validate(), "unknown event type")

	bad = good
	bad.ID = "007"
	require.Error(t, bad.Validate(), "malformed id")

	bad = good
	bad.Item = "   "
	require.Error(t, bad.Validate(), "empty item on a park")

	bad = good
	bad.Status = "archived"
	require.Error(t, bad.Validate(), "unknown status")

	bad = good
	bad.LogicalDate = "2026-13-40"
	require.Error(t, bad.Validate(), "malformed logical_date")

	// A resolve carries no item and still validates (the item requirement is
	// waived for a transition).
	resolve := Retro{
		EventID: "retro_event_2026_08_30_001", Schema: Schema, EventType: EventResolve, ID: "R-001",
		Status: StatusResolved, Source: SourceRetro, Resolution: "done", ResolvedDate: "2026-08-30",
		RecordedAt: "2026-08-30T10:00:00-04:00", LogicalDate: "2026-08-30",
	}
	require.NoError(t, resolve.Validate())
}
