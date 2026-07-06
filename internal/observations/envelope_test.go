package observations

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loc is the fixed test zone; all logical-day math trusts the host clock, so
// a stable offset keeps the fixtures reproducible (data-model.md TZ rule).
var loc = time.FixedZone("EDT", -4*3600) //nolint:gochecknoglobals // deterministic test-fixture zone

func at(y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, loc)
}

// TestDeriveLogicalDate_AttributionFixtures pins the binding logical-date
// rule (observations.md §2), the file-placement key the /day join relies on.
func TestDeriveLogicalDate_AttributionFixtures(t *testing.T) {
	tests := []struct {
		name      string
		occ       time.Time
		precision string
		want      string
	}{
		{"23:50 exact → today", at(2026, 7, 2, 23, 50), PrecisionExact, "2026-07-02"},
		{"03:50 exact → yesterday (before rollover)", at(2026, 7, 2, 3, 50), PrecisionExact, "2026-07-01"},
		{"04:00 exact → today (at rollover)", at(2026, 7, 2, 4, 0), PrecisionExact, "2026-07-02"},
		{"19:30 exact → same day (no shift)", at(2026, 7, 1, 19, 30), PrecisionExact, "2026-07-01"},
		{"midnight approximate → same calendar date", at(2014, 9, 1, 0, 0), PrecisionApproximate, "2014-09-01"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, DeriveLogicalDate(tc.occ, tc.precision, DefaultRolloverMin))
		})
	}
}

// TestDeriveLogicalDate_RangeKeysOnStart confirms a range event keys on its
// start calendar date (observations.md §2).
func TestDeriveLogicalDate_RangeKeysOnStart(t *testing.T) {
	occ := at(2026, 7, 1, 23, 40) // a night that ends the next morning
	assert.Equal(t, "2026-07-01", DeriveLogicalDate(occ, PrecisionRange, DefaultRolloverMin))
}

func TestEventID_AndParseSeq(t *testing.T) {
	assert.Equal(t, "obs_2026_07_02_003", EventID("2026-07-02", 3))
	assert.Equal(t, "obs_2026_07_02_1000", EventID("2026-07-02", 1000))

	seq, ok := ParseSeq("obs_2026_07_02_003")
	require.True(t, ok)
	assert.Equal(t, 3, seq)

	seq, ok = ParseSeq("obs_2026_07_02_1000")
	require.True(t, ok)
	assert.Equal(t, 1000, seq)

	for _, bad := range []string{"", "raw_2026_07_02_19_42", "obs_", "obs_x", "not-an-id"} {
		_, ok := ParseSeq(bad)
		assert.Falsef(t, ok, "expected %q to be an invalid obs id", bad)
	}
}

// TestMarshalLine_Deterministic guarantees the single-line, byte-stable
// rendering the append discipline and /day byte-stability depend on.
func TestMarshalLine_Deterministic(t *testing.T) {
	e := Event{
		ID: "obs_2026_07_02_001", Schema: Schema, Kind: KindPain,
		RecordedAt: "2026-07-02T21:45:10-04:00", OccurredAt: "2026-07-02T18:00:00-04:00",
		OccurredAtPrecision: PrecisionExact, LogicalDate: "2026-07-02", Source: SourceMicrolog,
		Payload: map[string]any{"intensity": 6, "site": "knee"},
		Tags:    []string{"running"},
		Refs:    map[string]any{"injury": "injury_a-cedar"},
	}
	a, err := e.MarshalLine()
	require.NoError(t, err)
	b, err := e.MarshalLine()
	require.NoError(t, err)
	assert.Equal(t, a, b, "same event must marshal byte-identical")
	assert.NotContains(t, string(a), "\n", "a JSONL line carries no embedded newline")

	// Empty collections render as [] / {} rather than null.
	bare := Event{
		ID: "obs_2026_07_02_002", Schema: Schema, Kind: KindElimination,
		RecordedAt: "x", OccurredAt: "x", OccurredAtPrecision: PrecisionExact,
		LogicalDate: "2026-07-02", Source: SourceMicrolog,
	}
	line, err := bare.MarshalLine()
	require.NoError(t, err)
	var round map[string]any
	require.NoError(t, json.Unmarshal(line, &round))
	assert.Equal(t, []any{}, round["tags"])
	assert.Equal(t, map[string]any{}, round["refs"])
	assert.NotNil(t, round["payload"])
	assert.Nil(t, round["occurred_at_end"])
}

func TestUnmarshalEventLine(t *testing.T) {
	line := []byte(`{"id":"obs_2026_07_02_001","schema":1,"kind":"pain","recorded_at":"x","occurred_at":"x","occurred_at_precision":"exact","occurred_at_end":null,"logical_date":"2026-07-02","source":"microlog","payload":{"intensity":6},"tags":[],"refs":{}}`)
	ev, err := UnmarshalEventLine(line)
	require.NoError(t, err)
	assert.Equal(t, "obs_2026_07_02_001", ev.ID)
	assert.Equal(t, KindPain, ev.Kind)
	assert.InDelta(t, 6, ev.Payload["intensity"], 0.0)

	_, err = UnmarshalEventLine([]byte("{ this is not json"))
	require.Error(t, err)
}

func TestEvent_Validate(t *testing.T) {
	valid := Event{
		Schema: Schema, Kind: KindMood, RecordedAt: "x", OccurredAt: "x",
		OccurredAtPrecision: PrecisionExact, LogicalDate: "2026-07-02", Source: SourceMicrolog,
	}
	require.NoError(t, valid.Validate())

	bad := valid
	bad.Schema = 99
	require.Error(t, bad.Validate())

	bad = valid
	bad.Kind = ""
	require.Error(t, bad.Validate())

	bad = valid
	bad.OccurredAtPrecision = "sometime"
	require.Error(t, bad.Validate())

	bad = valid
	bad.OccurredAtPrecision = PrecisionRange // range without an end
	require.Error(t, bad.Validate())

	bad = valid
	bad.LogicalDate = ""
	require.Error(t, bad.Validate())

	bad = valid
	bad.Source = ""
	require.Error(t, bad.Validate())

	bad = valid
	bad.RecordedAt = ""
	require.Error(t, bad.Validate())

	bad = valid
	bad.OccurredAt = ""
	require.Error(t, bad.Validate())
}

func TestParseDate(t *testing.T) {
	d, err := ParseDate("2026-07-02", loc)
	require.NoError(t, err)
	assert.Equal(t, 2026, d.Year())
	assert.Equal(t, time.July, d.Month())

	_, err = ParseDate("not-a-date", loc)
	require.Error(t, err)

	// nil loc defaults to UTC rather than panicking.
	_, err = ParseDate("2026-07-02", nil)
	require.NoError(t, err)
}
