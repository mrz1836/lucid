package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file is a characterization golden for the wheel's schema-1 records
// (wheel.md §4): a synthetic Ledger written as raw month files with no
// pillar_set anywhere, and the exact read-back of `wheel show`, `wheel show
// --json`, `wheel show --month`, `wheel list`, and `wheel list --json` captured
// from the code that wrote schema 1. Stored wheels are never migrated, so every
// later build must read these records back unchanged. Only the two additive
// `show --json` keys a later schema documents are dropped before comparing.
//
// The file owns its fixture, clock, and helpers and leans only on the generic
// CLI harness (isolatedHome, withClock, runRoot), so changes to the wheel's
// write-path test helpers never touch it. All data is invented.

// schema1GoldenJuly is a raw schema-1 month file holding one snapshot.
const schema1GoldenJuly = `{
  "key": "wheel_2026-07",
  "kind": "wheel",
  "schema": 1,
  "month": "2026-07",
  "history": [
    {
      "id": "wheel_2026_07_001",
      "at": "2026-08-02T18:20:00Z",
      "type": "snapshot",
      "pillars": {
        "health": { "score": 6 },
        "relationships": { "score": 7 },
        "career": { "score": 5 },
        "finances": { "score": 4 },
        "growth": { "score": 6 },
        "fun": { "score": 3 },
        "environment": { "score": 7 },
        "contribution": { "score": 5 }
      },
      "vision_reviewed": false
    }
  ],
  "created_at": "2026-08-02T18:20:00Z",
  "updated_at": "2026-08-02T18:20:00Z"
}
`

// schema1GoldenAugust is a raw schema-1 month file amended once: two
// snapshots, the second wins. The winner carries notes on two pillars, a
// calibration suggestion on one, and a vision reflection.
const schema1GoldenAugust = `{
  "key": "wheel_2026-08",
  "kind": "wheel",
  "schema": 1,
  "month": "2026-08",
  "history": [
    {
      "id": "wheel_2026_08_001",
      "at": "2026-09-06T10:00:00Z",
      "type": "snapshot",
      "pillars": {
        "health": { "score": 5 },
        "relationships": { "score": 6 },
        "career": { "score": 6 },
        "finances": { "score": 4 },
        "growth": { "score": 7 },
        "fun": { "score": 4 },
        "environment": { "score": 7 },
        "contribution": { "score": 5 }
      },
      "vision_reviewed": false
    },
    {
      "id": "wheel_2026_08_002",
      "at": "2026-09-06T16:45:00Z",
      "type": "snapshot",
      "pillars": {
        "health": { "score": 8, "note": "synthetic: more walks, steadier sleep" },
        "relationships": { "score": 6 },
        "career": { "score": 6, "suggested": 4 },
        "finances": { "score": 2, "note": "synthetic: an unplanned repair bill" },
        "growth": { "score": 7 },
        "fun": { "score": 5 },
        "environment": { "score": 7 },
        "contribution": { "score": 4 }
      },
      "vision_reviewed": true,
      "vision_reflection": "Synthetic reflection: the long-range picture still fits."
    }
  ],
  "created_at": "2026-09-06T10:00:00Z",
  "updated_at": "2026-09-06T16:45:00Z"
}
`

// schema1GoldenNow pins the clock after the fixture's last write; the
// current logical month is 2026-09, so both fixture months are past.
func schema1GoldenNow() time.Time { return time.Date(2026, 9, 6, 17, 12, 40, 0, time.UTC) }

// seedSchema1GoldenLedger writes the raw schema-1 month files into home's
// wheel registry and returns their paths, July first.
func seedSchema1GoldenLedger(t *testing.T, home string) []string {
	t.Helper()
	dir := filepath.Join(home, "registries", "wheel")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	paths := []string{
		filepath.Join(dir, "wheel_2026-07.json"),
		filepath.Join(dir, "wheel_2026-08.json"),
	}
	require.NoError(t, os.WriteFile(paths[0], []byte(schema1GoldenJuly), 0o600))
	require.NoError(t, os.WriteFile(paths[1], []byte(schema1GoldenAugust), 0o600))
	return paths
}

// runSchema1Golden runs one CLI command and returns its stdout; the command
// must succeed with nothing on stderr.
func runSchema1Golden(t *testing.T, args ...string) string {
	t.Helper()
	out, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, args...)
	require.NoError(t, err, stderr)
	assert.Empty(t, stderr)
	return out
}

// decodeSchema1GoldenJSON decodes one JSON object for a decoded deep-equal.
func decodeSchema1GoldenJSON(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(s), &m), s)
	return m
}

// The expected read-back below was captured verbatim from the schema-1 code.

// schema1GoldenShowHuman is `wheel show`: August against July.
const schema1GoldenShowHuman = `wheel: 2026-08 (vs 2026-07)
health: 8 (+2) ▅▆
relationships: 6 (-1) ▆▅
career/work: 6 (+1) ▄▅
finances: 2 (-2) ▃▂
personal growth: 7 (+1) ▅▆
fun/recreation: 5 (+2) ▃▄
environment: 7 (0) ▆▆
contribution: 4 (-1) ▄▃
lowest: finances (2)
biggest drop: finances (-2)
notes:
- health: synthetic: more walks, steadier sleep
- finances: synthetic: an unplanned repair bill
vision reviewed: yes
vision reflection: Synthetic reflection: the long-range picture still fits.
`

// schema1GoldenShowJSON is `wheel show --json`, compared decoded.
const schema1GoldenShowJSON = `{
  "month": "2026-08",
  "prior_month": "2026-07",
  "receipt_id": "wheel_2026_08_002",
  "months": [
    "2026-07",
    "2026-08"
  ],
  "pillars": [
    {
      "pillar": "health",
      "label": "health",
      "score": 8,
      "delta": 2,
      "trend": [
        6,
        8
      ],
      "sparkline": "▅▆",
      "note": "synthetic: more walks, steadier sleep"
    },
    {
      "pillar": "relationships",
      "label": "relationships",
      "score": 6,
      "delta": -1,
      "trend": [
        7,
        6
      ],
      "sparkline": "▆▅",
      "note": ""
    },
    {
      "pillar": "career",
      "label": "career/work",
      "score": 6,
      "delta": 1,
      "trend": [
        5,
        6
      ],
      "sparkline": "▄▅",
      "note": ""
    },
    {
      "pillar": "finances",
      "label": "finances",
      "score": 2,
      "delta": -2,
      "trend": [
        4,
        2
      ],
      "sparkline": "▃▂",
      "note": "synthetic: an unplanned repair bill"
    },
    {
      "pillar": "growth",
      "label": "personal growth",
      "score": 7,
      "delta": 1,
      "trend": [
        6,
        7
      ],
      "sparkline": "▅▆",
      "note": ""
    },
    {
      "pillar": "fun",
      "label": "fun/recreation",
      "score": 5,
      "delta": 2,
      "trend": [
        3,
        5
      ],
      "sparkline": "▃▄",
      "note": ""
    },
    {
      "pillar": "environment",
      "label": "environment",
      "score": 7,
      "delta": 0,
      "trend": [
        7,
        7
      ],
      "sparkline": "▆▆",
      "note": ""
    },
    {
      "pillar": "contribution",
      "label": "contribution",
      "score": 4,
      "delta": -1,
      "trend": [
        5,
        4
      ],
      "sparkline": "▄▃",
      "note": ""
    }
  ],
  "lowest": [
    {
      "pillar": "finances",
      "score": 2
    }
  ],
  "biggest_drop": [
    {
      "pillar": "finances",
      "delta": -2
    }
  ],
  "vision_reviewed": true,
  "vision_reflection": "Synthetic reflection: the long-range picture still fits.",
  "calibration": {
    "suggested": {
      "career": 4
    }
  }
}
`

// schema1GoldenShowMonthHuman is `wheel show --month 2026-07`: the first
// stored month, with no prior month to compare against.
const schema1GoldenShowMonthHuman = `wheel: 2026-07 (no prior month)
health: 6 ▅
relationships: 7 ▆
career/work: 5 ▄
finances: 4 ▃
personal growth: 6 ▅
fun/recreation: 3 ▃
environment: 7 ▆
contribution: 5 ▄
lowest: fun/recreation (3)
biggest drop: no prior month
vision reviewed: no
`

// schema1GoldenListHuman is `wheel list`, most recent first.
const schema1GoldenListHuman = `2026-08: wheel_2026_08_002 (2 snapshots) · vision reviewed
2026-07: wheel_2026_07_001
`

// schema1GoldenListJSON is `wheel list --json`, compared decoded.
const schema1GoldenListJSON = `{
  "months": [
    {
      "month": "2026-08",
      "entry_id": "wheel_2026-08",
      "receipt_id": "wheel_2026_08_002",
      "snapshots": 2,
      "recorded_at": "2026-09-06T16:45:00Z",
      "vision_reviewed": true
    },
    {
      "month": "2026-07",
      "entry_id": "wheel_2026-07",
      "receipt_id": "wheel_2026_07_001",
      "snapshots": 1,
      "recorded_at": "2026-08-02T18:20:00Z",
      "vision_reviewed": false
    }
  ]
}
`

// TestWheelSchema1Golden_ReadsUnchanged: a schema-1 Ledger (no pillar_set
// anywhere) reads back through the CLI exactly as the schema-1 code printed
// it — the human lines byte for byte and the JSON decoded value for value,
// apart from the two additive `show --json` keys (pillar_set, pillars_changed)
// that are dropped when present — and the reads rewrite no stored byte.
func TestWheelSchema1Golden_ReadsUnchanged(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, schema1GoldenNow())
	paths := seedSchema1GoldenLedger(t, home)

	t.Run("show_human", func(t *testing.T) {
		assert.Equal(t, schema1GoldenShowHuman, runSchema1Golden(t, "wheel", "show"))
	})
	t.Run("show_json", func(t *testing.T) {
		got := decodeSchema1GoldenJSON(t, runSchema1Golden(t, "wheel", "show", "--json"))
		delete(got, "pillar_set")
		delete(got, "pillars_changed")
		assert.Equal(t, decodeSchema1GoldenJSON(t, schema1GoldenShowJSON), got)
	})
	t.Run("show_month_human", func(t *testing.T) {
		assert.Equal(t, schema1GoldenShowMonthHuman, runSchema1Golden(t, "wheel", "show", "--month", "2026-07"))
	})
	t.Run("list_human", func(t *testing.T) {
		assert.Equal(t, schema1GoldenListHuman, runSchema1Golden(t, "wheel", "list"))
	})
	t.Run("list_json", func(t *testing.T) {
		got := decodeSchema1GoldenJSON(t, runSchema1Golden(t, "wheel", "list", "--json"))
		assert.Equal(t, decodeSchema1GoldenJSON(t, schema1GoldenListJSON), got)
	})

	for i, want := range []string{schema1GoldenJuly, schema1GoldenAugust} {
		got, err := os.ReadFile(paths[i])
		require.NoError(t, err)
		assert.Equal(t, want, string(got), "a read never rewrites a stored schema-1 file")
	}
}
