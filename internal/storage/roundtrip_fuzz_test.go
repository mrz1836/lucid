package storage

import (
	"encoding/json"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
)

// FuzzDayRecordRoundTrip is the byte-stability guard for the engine day record
// — the append the derived status.json and /day view are folded from, whose
// determinism criterion is "delete and rebuild reproduces it byte-for-byte"
// (engine-module.md §status.json). It builds a DayRecord from fuzzed fields,
// marshals it with the storage writer, then asserts the marshal/unmarshal pair
// reaches a fixed point: re-parsing and re-marshaling yields identical bytes. A
// regression (a dropped field, an omitempty flip, non-deterministic map order)
// would break the Ledger's byte reproducibility.
func FuzzDayRecordRoundTrip(f *testing.F) {
	f.Add("day_2026_07_05", "green", "journal", "done", 3, true, "wrist")
	f.Add("day_2026_07_06", "red", "", "", 0, false, "")

	f.Fuzz(func(t *testing.T, dayID, mode, linkKey, linkVal string, capacity int, completed bool, corrField string) {
		// Records that reach the Ledger carry valid-UTF-8 fields (values arrive
		// via JSON/argv); invalid UTF-8 can never be marshaled to disk, so it is
		// out of scope for the on-disk byte-stability property.
		for _, s := range []string{dayID, mode, linkKey, linkVal, corrField} {
			if !utf8.ValidString(s) {
				return
			}
		}
		rec := engine.DayRecord{
			DayID:       dayID,
			LogicalDate: "2026-07-05",
			RecordedAt:  "2026-07-05T22:00:00Z",
			Mode:        engine.Mode(mode),
			Links:       map[string]string{linkKey: linkVal},
			Completed:   completed,
			Capacity:    capacity,
			Corrections: []engine.Correction{
				{At: "2026-07-05T22:05:00Z", Fields: map[string]any{corrField: true}, Reason: "fix", Source: "cli"},
			},
		}

		b1, err := marshalJSON(rec)
		require.NoError(t, err)

		var rec2 engine.DayRecord
		require.NoError(t, json.Unmarshal(b1, &rec2), "marshaled record must re-parse")

		b2, err := marshalJSON(rec2)
		require.NoError(t, err)

		require.Equal(t, string(b1), string(b2),
			"marshal must reach a fixed point — the Ledger byte-reproducibility guarantee")
	})
}
