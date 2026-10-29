package contact

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every fixture in this package is synthetic — real people live only in the
// private Ledger.
const (
	keySam    = "person_s-rivera"
	keyAlex   = "person_a-kim"
	keyJordan = "person_j-lee"
)

// mustWindow parses a window the test knows is valid.
func mustWindow(t *testing.T, s string) Window {
	t.Helper()
	w, err := ParseWindow(s)
	require.NoError(t, err)
	return w
}

// TestContactEventID_SlotGrammar pins the receipt id: contact_YYYY_MM_DD_<slot>
// in the anchor-event slot grammar, and Date as its read-side inverse.
func TestContactEventID_SlotGrammar(t *testing.T) {
	assert.Equal(t, "contact_2026_10_29_a", ID("2026-10-29", 0))
	assert.Equal(t, "contact_2026_10_29_b", ID("2026-10-29", 1))
	assert.Equal(t, "contact_2026_10_29_z", ID("2026-10-29", 25))
	assert.Equal(t, "contact_2026_10_29_aa", ID("2026-10-29", 26))
	assert.Equal(t, "contact_2026_10_29_ab", ID("2026-10-29", 27))
	assert.Equal(t, "contact_2026_10_29_a", ID("2026-10-29", -1), "a negative slot reads as the first")

	for _, slot := range []int{0, 1, 26, 701, 702} {
		d, ok := Date(ID("2026-10-29", slot))
		require.True(t, ok, slot)
		assert.Equal(t, "2026-10-29", d, slot)
	}

	for _, bad := range []string{
		"", "rep_2026_10_29_001", "contact_2026_10_29", "contact_2026_10_29_",
		"contact_2026_13_01_a", "contact_2026_02_30_a", "contact_2026_10_29_A",
		"contact_2026_10_29_1", "contact_2026_10_29_a_b", "contact_26_10_29_a",
	} {
		_, ok := Date(bad)
		assert.Falsef(t, ok, "%q is not a contact id", bad)
	}
}

// TestContactEventMarshalLine_StableShape pins the exact key set per type, in
// the documented order, and the round-trip back to the same bytes. A contact
// always carries via and note; the other types omit what they do not use.
func TestContactEventMarshalLine_StableShape(t *testing.T) {
	const rec = "2026-10-29T09:15:00-04:00"
	withID := func(e Event, id string) Event {
		e.ID = id
		return e
	}
	cases := []struct {
		name string
		ev   Event
		want string
	}{
		{
			name: "cadence set",
			ev:   withID(NewCadence(keySam, "2026-10-29", rec, ActionSet, mustWindow(t, "4w")), "contact_2026_10_29_a"),
			want: `{"id":"contact_2026_10_29_a","schema":1,"type":"cadence","person_key":"person_s-rivera","logical_date":"2026-10-29","recorded_at":"2026-10-29T09:15:00-04:00","source":"contact","action":"set","window":"4w","window_days":28}`,
		},
		{
			name: "cadence pause",
			ev:   withID(NewCadence(keySam, "2026-10-29", rec, ActionPause, Window{}), "contact_2026_10_29_b"),
			want: `{"id":"contact_2026_10_29_b","schema":1,"type":"cadence","person_key":"person_s-rivera","logical_date":"2026-10-29","recorded_at":"2026-10-29T09:15:00-04:00","source":"contact","action":"pause"}`,
		},
		{
			name: "cadence resume carries no window",
			ev:   withID(NewCadence(keySam, "2026-10-29", rec, ActionResume, mustWindow(t, "2w")), "contact_2026_10_29_c"),
			want: `{"id":"contact_2026_10_29_c","schema":1,"type":"cadence","person_key":"person_s-rivera","logical_date":"2026-10-29","recorded_at":"2026-10-29T09:15:00-04:00","source":"contact","action":"resume"}`,
		},
		{
			name: "contact with nothing optional",
			ev:   withID(NewContact(keySam, "2026-10-29", rec, "", ""), "contact_2026_10_29_d"),
			want: `{"id":"contact_2026_10_29_d","schema":1,"type":"contact","person_key":"person_s-rivera","logical_date":"2026-10-29","recorded_at":"2026-10-29T09:15:00-04:00","source":"contact","via":"","note":""}`,
		},
		{
			name: "contact with via and note",
			ev:   withID(NewContact(keySam, "2026-10-28", rec, ViaCall, "coffee walk"), "contact_2026_10_28_a"),
			want: `{"id":"contact_2026_10_28_a","schema":1,"type":"contact","person_key":"person_s-rivera","logical_date":"2026-10-28","recorded_at":"2026-10-29T09:15:00-04:00","source":"contact","via":"call","note":"coffee walk"}`,
		},
		{
			name: "snooze",
			ev:   withID(NewSnooze(keyAlex, "2026-10-29", rec), "contact_2026_10_29_e"),
			want: `{"id":"contact_2026_10_29_e","schema":1,"type":"snooze","person_key":"person_a-kim","logical_date":"2026-10-29","recorded_at":"2026-10-29T09:15:00-04:00","source":"contact"}`,
		},
		{
			name: "void",
			ev:   withID(NewVoid("contact_2026_10_29_e", keyAlex, "2026-10-29", rec), "contact_2026_10_29_f"),
			want: `{"id":"contact_2026_10_29_f","schema":1,"type":"void","person_key":"person_a-kim","logical_date":"2026-10-29","recorded_at":"2026-10-29T09:15:00-04:00","source":"contact","refs":{"voids":"contact_2026_10_29_e"}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, tc.ev.Validate())
			line, err := tc.ev.MarshalLine()
			require.NoError(t, err)
			assert.Equal(t, tc.want, string(line))
			assert.False(t, bytes.ContainsAny(line, "\n"), "one line, no terminator")

			// A receipt that embeds the event marshals the same shape.
			embedded, err := json.Marshal(tc.ev)
			require.NoError(t, err)
			assert.Equal(t, tc.want, string(embedded))

			back, err := UnmarshalEventLine(line)
			require.NoError(t, err)
			assert.Equal(t, tc.ev, back)
			again, err := back.MarshalLine()
			require.NoError(t, err)
			assert.Equal(t, line, again, "byte-stable round-trip")
		})
	}

	_, err := UnmarshalEventLine([]byte(`{"id":`))
	require.Error(t, err, "a malformed line is a decode error the reader skips")
}

// TestContactEventValidate pins the structural refusals: unknown type, action
// or via; a missing person; a void with no target; and the envelope fields.
func TestContactEventValidate(t *testing.T) {
	const rec = "2026-10-29T09:15:00-04:00"
	assert.Equal(t, []string{"call", "text", "video", "in-person"}, ViaValues())
	vv := ViaValues()
	vv[0] = "email"
	assert.Equal(t, "call", ViaValues()[0], "ViaValues returns a fresh slice")
	for _, v := range []string{"", ViaCall, ViaText, ViaVideo, ViaInPerson} {
		assert.True(t, ValidVia(v), v)
	}
	for _, v := range []string{"email", "Call", "in person", "letter"} {
		assert.False(t, ValidVia(v), v)
	}

	for _, ok := range []Event{
		NewCadence(keySam, "2026-10-29", rec, ActionSet, mustWindow(t, "28d")),
		NewCadence(keySam, "2026-10-29", rec, ActionPause, Window{}),
		NewCadence(keySam, "2026-10-29", rec, ActionResume, Window{}),
		NewCadence(keySam, "2026-10-29", rec, ActionRetire, Window{}),
		NewContact(keySam, "2026-10-29", rec, ViaInPerson, "lunch"),
		NewSnooze(keySam, "2026-10-29", rec),
		NewVoid("contact_2026_10_28_a", keySam, "2026-10-29", rec),
	} {
		require.NoError(t, ok.Validate(), ok.Type+"/"+ok.Action)
		ok.ID = ID(ok.LogicalDate, 3)
		require.NoError(t, ok.Validate(), "with an id for its own day")
	}

	good := NewContact(keySam, "2026-10-29", rec, "", "")
	mutate := func(f func(*Event)) Event {
		e := good
		f(&e)
		return e
	}
	refused := map[string]Event{
		"unknown type":            mutate(func(e *Event) { e.Type = "call" }),
		"unknown action":          NewCadence(keySam, "2026-10-29", rec, "snooze", Window{}),
		"missing action":          NewCadence(keySam, "2026-10-29", rec, "", Window{}),
		"empty person_key":        mutate(func(e *Event) { e.PersonKey = "" }),
		"blank person_key":        mutate(func(e *Event) { e.PersonKey = "   " }),
		"void without refs":       mutate(func(e *Event) { e.Type = TypeVoid }),
		"void with empty refs":    mutate(func(e *Event) { e.Type, e.Refs = TypeVoid, &Refs{} }),
		"void of a non-contact":   NewVoid("rep_2026_10_29_001", keySam, "2026-10-29", rec),
		"bad schema":              mutate(func(e *Event) { e.Schema = 2 }),
		"bad logical_date":        mutate(func(e *Event) { e.LogicalDate = "2026-10-32" }),
		"empty logical_date":      mutate(func(e *Event) { e.LogicalDate = "" }),
		"empty recorded_at":       mutate(func(e *Event) { e.RecordedAt = "" }),
		"unreadable recorded_at":  mutate(func(e *Event) { e.RecordedAt = "yesterday" }),
		"empty source":            mutate(func(e *Event) { e.Source = "" }),
		"set without a window":    NewCadence(keySam, "2026-10-29", rec, ActionSet, Window{}),
		"set with a bad window":   NewCadence(keySam, "2026-10-29", rec, ActionSet, Window{Text: "4", Days: 4}),
		"set with mismatched day": NewCadence(keySam, "2026-10-29", rec, ActionSet, Window{Text: "4w", Days: 27}),
		"id for another day":      mutate(func(e *Event) { e.ID = "contact_2026_10_28_a" }),
		"malformed id":            mutate(func(e *Event) { e.ID = "contact_x" }),
	}
	for name, e := range refused {
		require.Errorf(t, e.Validate(), "%s must be refused", name)
	}

	err := mutate(func(e *Event) { e.Via = "email" }).Validate()
	require.ErrorIs(t, err, ErrInvalidVia)
	assert.Equal(t, "--via is one of call, text, video or in-person", err.Error())

	for _, note := range []string{"first\nsecond", "first\r\nsecond", "trailing\n"} {
		err = mutate(func(e *Event) { e.Note = note }).Validate()
		require.ErrorIs(t, err, ErrNoteNotOneLine, note)
	}
	assert.Equal(t, "a contact note is one line", ErrNoteNotOneLine.Error())
}

// TestContactSchema_NoDirectionOrContentRequired pins the record's restraint
// (ADR-0014 §2, §4): beyond the envelope every event carries, a contact needs
// only the person and the day — no via, no note, no content — and the schema
// has no direction, initiator, content, score or streak field at all.
func TestContactSchema_NoDirectionOrContentRequired(t *testing.T) {
	const rec = "2026-10-29T09:15:00-04:00"
	minimal := Event{
		Schema: Schema, Type: TypeContact, Source: SourceContact, RecordedAt: rec,
		PersonKey: keyJordan, LogicalDate: "2026-10-29",
	}
	require.NoError(t, minimal.Validate())
	assert.Equal(t, minimal, NewContact(keyJordan, "2026-10-29", rec, "", ""))

	forbidden := []string{"direction", "initiator", "initiated_by", "content", "score", "streak"}
	for _, e := range []Event{
		minimal,
		NewContact(keyJordan, "2026-10-29", rec, ViaVideo, "quick catch-up"),
		NewCadence(keyJordan, "2026-10-29", rec, ActionSet, mustWindow(t, "3w")),
		NewSnooze(keyJordan, "2026-10-29", rec),
		NewVoid("contact_2026_10_29_a", keyJordan, "2026-10-29", rec),
	} {
		line, err := e.MarshalLine()
		require.NoError(t, err)
		var keys map[string]any
		require.NoError(t, json.Unmarshal(line, &keys))
		for _, k := range forbidden {
			assert.NotContainsf(t, keys, k, "%s event must not carry %q", e.Type, k)
		}
	}

	// The schema itself has no such field, so none can ever be written.
	typ := reflect.TypeFor[Event]()
	for i := range typ.NumField() {
		tag := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		name := strings.ToLower(typ.Field(i).Name)
		for _, k := range forbidden {
			assert.NotEqual(t, k, tag)
			assert.NotContains(t, name, strings.ReplaceAll(k, "_", ""))
		}
	}
}

// TestLogicalDay pins the shared 04:00 rollover: just before it is still the
// previous logical day.
func TestLogicalDay(t *testing.T) {
	loc := time.FixedZone("synthetic", -4*60*60)
	assert.Equal(t, "2026-10-28", LogicalDay(time.Date(2026, 10, 29, 3, 59, 0, 0, loc)))
	assert.Equal(t, "2026-10-29", LogicalDay(time.Date(2026, 10, 29, 4, 0, 0, 0, loc)))
	assert.Equal(t, "2026-10-29", LogicalDay(time.Date(2026, 10, 29, 23, 59, 0, 0, loc)))
}
