package identity

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every key in these tests is synthetic: person_s-oak is an old "Sam",
// person_s-river is "Sam Rivera" — the examples the data model documents.
const (
	synthAt  = "2026-11-17T09:14:07-05:00"
	oldSam   = "person_s-oak"
	samRiver = "person_s-river"
	synthRaw = "raw_2026_11_18_09_00"
)

// docSchemaLines are the six example events data-model.md §"Identity log"
// documents, verbatim. The code must read and re-emit each byte for byte.
func docSchemaLines() []string {
	return []string{
		`{"schema":"identity.v1","id":"identity_2026_11_17_a","kind":"status","at":"2026-11-17T09:14:07-05:00","source":"cli","person_key":"person_s-oak","status":"dormant","reason":"an old contact from one passing mention"}`,
		`{"schema":"identity.v1","id":"identity_2026_11_17_b","kind":"claim","at":"2026-11-17T09:15:30-05:00","source":"cli","form":"Sam","person_key":"person_s-river"}`,
		`{"schema":"identity.v1","id":"identity_2026_11_17_c","kind":"release","at":"2026-11-17T09:20:02-05:00","source":"cli","form":"Sam","person_key":"person_s-river"}`,
		`{"schema":"identity.v1","id":"identity_2026_11_18_a","kind":"hold","at":"2026-11-18T09:00:12-05:00","source":"structure","raw_id":"raw_2026_11_18_09_00","form":"Sam","reason":"exact","candidates":["person_s-oak","person_s-river"]}`,
		`{"schema":"identity.v1","id":"identity_2026_11_18_b","kind":"pin","at":"2026-11-18T10:02:45-05:00","source":"cli","raw_id":"raw_2026_11_18_09_00","form":"Sam","person_key":"person_s-river","cause":"claim"}`,
		`{"schema":"identity.v1","id":"identity_2026_11_19_a","kind":"move_ref","at":"2026-11-19T08:30:00-05:00","source":"cli","raw_id":"raw_2026_11_18_21_40","from":"person_s-oak","to":"person_s-river"}`,
	}
}

// TestIdentityEvent_ValidateAndRoundTrip: every constructor builds an event that
// validates and round-trips through its JSONL line byte for byte; the
// documented schema lines parse, validate, and re-marshal unchanged; and every
// structural defect is refused.
func TestIdentityEvent_ValidateAndRoundTrip(t *testing.T) {
	built := []Event{
		NewStatus(synthAt, SourceCLI, oldSam, StatusDormant, "an old contact"),
		NewStatus(synthAt, SourceMerge, oldSam, StatusActive, ""),
		NewClaim(synthAt, SourceCLI, "Sam", samRiver, false, nil),
		NewClaim(synthAt, SourceCLI, "Sam", samRiver, true, []string{oldSam}),
		NewRelease(synthAt, SourceCLI, "Sam", samRiver),
		NewMoveRef(synthAt, SourceCLI, synthRaw, oldSam, samRiver),
		NewHold(synthAt, SourceStructure, synthRaw, "Sam", HoldExact, []string{samRiver, oldSam}),
		NewHold(synthAt, SourceStructure, synthRaw, "Sam", HoldFirstName, []string{samRiver}),
		NewPin(synthAt, SourceStructure, synthRaw, "Sam", samRiver, CauseFirstName),
		NewPin(synthAt, SourceCLI, synthRaw, "Sam", samRiver, CauseMoveRef),
	}
	for i, e := range built {
		e.ID = ID("2026-11-17", i)
		require.NoErrorf(t, e.Validate(), "event %d (%s)", i, e.Kind)
		line, err := e.MarshalLine()
		require.NoError(t, err)
		back, err := UnmarshalEventLine(line)
		require.NoError(t, err)
		assert.Equalf(t, e, back, "event %d round-trips", i)
		again, err := back.MarshalLine()
		require.NoError(t, err)
		assert.Equal(t, string(line), string(again), "the same event always marshals to the same bytes")
	}

	// The constructors sort and de-duplicate their key lists, and a displaced
	// list implies force.
	hold := NewHold(synthAt, SourceStructure, synthRaw, "Sam", HoldExact, []string{samRiver, oldSam, samRiver})
	assert.Equal(t, []string{oldSam, samRiver}, hold.Candidates)
	forced := NewClaim(synthAt, SourceCLI, "Sam", samRiver, false, []string{oldSam})
	assert.True(t, forced.Force)
	plain := NewClaim(synthAt, SourceCLI, "Sam", samRiver, false, []string{})
	assert.Nil(t, plain.Displaced, "an empty list is omitted, never marshaled as []")
	line, err := plain.MarshalLine()
	require.NoError(t, err)
	assert.JSONEq(t, `{"schema":"identity.v1","id":"","kind":"claim","at":"2026-11-17T09:14:07-05:00","source":"cli","form":"Sam","person_key":"person_s-river"}`, string(line))
	assert.NotContains(t, string(line), "displaced")

	for _, doc := range docSchemaLines() {
		e, perr := UnmarshalEventLine([]byte(doc))
		require.NoError(t, perr)
		require.NoErrorf(t, e.Validate(), "documented line validates: %s", doc)
		out, merr := e.MarshalLine()
		require.NoError(t, merr)
		assert.Equal(t, doc, string(out), "the documented schema line is the marshaled event, byte for byte")
	}

	_, err = UnmarshalEventLine([]byte(`{"schema":"identity.v1","id":"identity_2026_11_17_a","ki`))
	require.Error(t, err, "a torn line is a decode error")

	valid := NewStatus(synthAt, SourceCLI, oldSam, StatusDormant, "")
	cases := []struct {
		name string
		edit func(e Event) Event
	}{
		{"schema", func(e Event) Event { e.Schema = "identity.v0"; return e }},
		{"at", func(e Event) Event { e.At = "2026-11-17 09:14"; return e }},
		{"source", func(e Event) Event { e.Source = "harness"; return e }},
		{"kind", func(e Event) Event { e.Kind = "wave"; return e }},
		{"id wrong date", func(e Event) Event { e.ID = ID("2026-11-18", 0); return e }},
		{"id malformed", func(e Event) Event { e.ID = "contact_2026_11_17_a"; return e }},
		{"status person", func(e Event) Event { e.PersonKey = " "; return e }},
		{"status person whitespace", func(e Event) Event { e.PersonKey = "person_s oak"; return e }},
		{"status value", func(e Event) Event { e.Status = "asleep"; return e }},
		{"status reason lines", func(e Event) Event { e.Reason = "one\ntwo"; return e }},
		{"claim blank form", func(Event) Event { return NewClaim(synthAt, SourceCLI, "..", samRiver, false, nil) }},
		{"claim multiline form", func(Event) Event { return NewClaim(synthAt, SourceCLI, "Sam\nRivera", samRiver, false, nil) }},
		{"claim no person", func(Event) Event { return NewClaim(synthAt, SourceCLI, "Sam", "", false, nil) }},
		{"claim displaced without force", func(Event) Event {
			e := NewClaim(synthAt, SourceCLI, "Sam", samRiver, true, []string{oldSam})
			e.Force = false
			return e
		}},
		{"claim displaced unsorted", func(Event) Event {
			e := NewClaim(synthAt, SourceCLI, "Sam", samRiver, true, nil)
			e.Displaced = []string{samRiver, oldSam}
			return e
		}},
		{"release no form", func(Event) Event { return NewRelease(synthAt, SourceCLI, "", samRiver) }},
		{"move-ref no raw", func(Event) Event { return NewMoveRef(synthAt, SourceCLI, "", oldSam, samRiver) }},
		{"move-ref no from", func(Event) Event { return NewMoveRef(synthAt, SourceCLI, synthRaw, "", samRiver) }},
		{"move-ref no to", func(Event) Event { return NewMoveRef(synthAt, SourceCLI, synthRaw, oldSam, "") }},
		{"move-ref to itself", func(Event) Event { return NewMoveRef(synthAt, SourceCLI, synthRaw, oldSam, oldSam) }},
		{"hold no raw", func(Event) Event { return NewHold(synthAt, SourceStructure, "", "Sam", HoldExact, []string{oldSam}) }},
		{"hold blank form", func(Event) Event {
			return NewHold(synthAt, SourceStructure, synthRaw, " ", HoldExact, []string{oldSam})
		}},
		{"hold reason", func(Event) Event {
			return NewHold(synthAt, SourceStructure, synthRaw, "Sam", "nickname", []string{oldSam})
		}},
		{"hold no candidates", func(Event) Event { return NewHold(synthAt, SourceStructure, synthRaw, "Sam", HoldExact, nil) }},
		{"hold blank candidate", func(Event) Event {
			e := NewHold(synthAt, SourceStructure, synthRaw, "Sam", HoldExact, nil)
			e.Candidates = []string{""}
			return e
		}},
		{"pin no raw", func(Event) Event { return NewPin(synthAt, SourceCLI, "", "Sam", samRiver, CauseClaim) }},
		{"pin no person", func(Event) Event { return NewPin(synthAt, SourceCLI, synthRaw, "Sam", "", CauseClaim) }},
		{"pin cause", func(Event) Event { return NewPin(synthAt, SourceCLI, synthRaw, "Sam", samRiver, "guess") }},
	}
	require.NoError(t, valid.Validate())
	for _, tc := range cases {
		require.Errorf(t, tc.edit(valid).Validate(), "%s is refused", tc.name)
	}
	reasonErr := NewStatus(synthAt, SourceCLI, oldSam, StatusDormant, "one\r\ntwo").Validate()
	require.ErrorIs(t, reasonErr, ErrReasonNotOneLine)
	require.ErrorIs(t, NewRelease(synthAt, SourceCLI, "--", samRiver).Validate(), ErrBlankForm)
}

// TestIdentityID_SlotGrammar: ids follow the anchor-event slot grammar across
// the single-to-double letter boundary, Date and Slot invert ID, and anything
// that is not a well-formed identity id is refused.
func TestIdentityID_SlotGrammar(t *testing.T) {
	assert.Equal(t, "identity_2026_11_17_a", ID("2026-11-17", 0))
	assert.Equal(t, "identity_2026_11_17_a", ID("2026-11-17", -3), "a negative slot is treated as 0")
	assert.Equal(t, "identity_2026_11_17_aa", ID("2026-11-17", 26))
	for _, n := range []int{0, 1, 25, 26, 27, 51, 52, 701, 702} {
		id := ID("2026-11-17", n)
		got, ok := Slot(id)
		require.True(t, ok)
		assert.Equal(t, n, got, "slot %d", n)
		d, ok := Date(id)
		require.True(t, ok)
		assert.Equal(t, "2026-11-17", d)
	}
	for _, bad := range []string{
		"", "identity_2026_11_17_", "identity_2026_11_17_A", "identity_2026_11_17", "contact_2026_11_17_a",
		"identity_2026_13_01_a", "identity_2026_02_30_a", "identity_2026_11_17_a_b",
	} {
		_, ok := Date(bad)
		assert.False(t, ok, "%q", bad)
		_, ok = Slot(bad)
		assert.False(t, ok, "%q", bad)
	}
}

// TestIdentityCompareIDs: the id order is date, then slot (`_z` before `_aa`),
// then string order for anything that is not an identity id.
func TestIdentityCompareIDs(t *testing.T) {
	assert.Negative(t, CompareIDs(ID("2026-11-17", 25), ID("2026-11-17", 26)))
	assert.Positive(t, CompareIDs(ID("2026-11-18", 0), ID("2026-11-17", 30)))
	assert.Zero(t, CompareIDs(ID("2026-11-17", 3), ID("2026-11-17", 3)))
	assert.Negative(t, CompareIDs("", ID("2026-11-17", 0)))
	assert.Positive(t, CompareIDs("x", "b"))
	assert.Equal(t, "sam", NormalizeForm("S.a.M "))
}
