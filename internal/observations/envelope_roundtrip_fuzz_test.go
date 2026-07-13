package observations

import (
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

// FuzzEventRoundTrip is the byte-stability guard for the observation envelope
// (observations.md §2: a marshaled event "always marshals to the same bytes —
// the property the /day byte-stability and single-writer append discipline rely
// on"). It builds an Event from fuzzed fields, marshals it, then asserts the
// marshal/unmarshal pair reaches a fixed point: re-parsing the marshaled bytes
// and re-marshaling yields byte-identical output. A regression here (a dropped
// field, an omitempty asymmetry, non-deterministic key order) would silently
// corrupt the append-only Ledger — exactly what P6 forbids.
func FuzzEventRoundTrip(f *testing.F) {
	f.Add("obs_2026_07_05_0001", "pain", "range", "left", 7, "flare")
	f.Add("obs_2026_07_05_0002", "mood", "exact", "", 4, "")
	f.Add("x", "", "approximate", "note", 0, "")

	f.Fuzz(func(t *testing.T, id, kind, precision, endVal string, num int, tag string) {
		// Scope the property to the real input domain: every field of a record
		// that reaches the Ledger is valid UTF-8 (kinds are fixed consts; free
		// text arrives via JSON/argv, which are UTF-8). Invalid UTF-8 can never
		// be marshaled to disk, and json.Marshal would sanitize it to U+FFFD on
		// the first write, so a raw invalid-UTF-8 struct is out of scope.
		if !utf8.ValidString(id) || !utf8.ValidString(kind) ||
			!utf8.ValidString(precision) || !utf8.ValidString(endVal) || !utf8.ValidString(tag) {
			return
		}
		ev := Event{
			ID:                  id,
			Schema:              Schema,
			Kind:                Kind(kind),
			RecordedAt:          "2026-07-05T18:41:39Z",
			OccurredAt:          "2026-07-05T18:41:39Z",
			OccurredAtPrecision: precision,
			LogicalDate:         "2026-07-05",
			Source:              SourceMicrolog,
			Payload:             map[string]any{"n": num, "note": tag},
			Tags:                []string{tag},
		}
		if endVal != "" {
			ev.OccurredAtEnd = &endVal
		}

		b1, err := ev.MarshalLine()
		require.NoError(t, err, "a well-formed Event must marshal")

		parsed, err := UnmarshalEventLine(b1)
		require.NoError(t, err, "marshaled bytes must re-parse")

		b2, err := parsed.MarshalLine()
		require.NoError(t, err)

		require.Equal(t, string(b1), string(b2),
			"marshal must reach a fixed point — the Ledger byte-stability guarantee")
	})
}

// FuzzEventLineRoundTrip drives arbitrary bytes through UnmarshalEventLine and,
// for any line that parses, asserts the same marshal fixed point. Seeded with
// real event lines so the mutator explores valid envelopes as well as garbage;
// a malformed line is a skip (the reader's documented behavior), never a crash.
func FuzzEventLineRoundTrip(f *testing.F) {
	f.Add([]byte(`{"id":"obs_2026_07_05_0001","schema":1,"kind":"pain","recorded_at":"2026-07-05T18:41:39Z","occurred_at":"2026-07-05T18:41:39Z","occurred_at_precision":"exact","occurred_at_end":null,"logical_date":"2026-07-05","source":"cli","payload":{"intensity":7},"tags":["flare"],"refs":{}}`))
	f.Add([]byte(`{"id":"x","schema":1,"kind":"mood","recorded_at":"t","occurred_at":"t","occurred_at_precision":"exact","occurred_at_end":null,"logical_date":"d","source":"cli","payload":{},"tags":[],"refs":{}}`))

	f.Fuzz(func(t *testing.T, line []byte) {
		ev, err := UnmarshalEventLine(line)
		if err != nil {
			return
		}
		b1, err := ev.MarshalLine()
		require.NoError(t, err)

		parsed, err := UnmarshalEventLine(b1)
		require.NoError(t, err, "re-parsing marshaled bytes must succeed")

		b2, err := parsed.MarshalLine()
		require.NoError(t, err)

		require.Equal(t, string(b1), string(b2), "marshal must reach a fixed point")
	})
}
