package router

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// anchorCompatV1 is a version 1 anchors.json: every record id-less, written
// before anchors carried an id or a state. It holds a correction — the same
// label appended again with an earlier date — which must still fold
// latest-wins under the synthetic legacy identity.
const anchorCompatV1 = `{
  "version": 1,
  "history": [
    {"label": "first-gate", "date": "2026-05-01", "recorded_at": "2026-05-01T08:00:00Z"},
    {"label": "sobriety", "date": "2026-03-10", "recorded_at": "2026-03-10T21:00:00Z"},
    {"label": "sobriety", "date": "2026-03-01", "note": "corrected start", "recorded_at": "2026-03-11T07:30:00Z"}
  ]
}
`

// anchorCompatV2 is a version 2 anchors.json: minted ids, one anchor retired
// by a sunset record carrying its reason, and one pre-id record still mixed
// in (a version 2 file never rewrote the records written before it).
const anchorCompatV2 = `{
  "version": 2,
  "history": [
    {"label": "first-gate", "date": "2026-02-14", "recorded_at": "2026-02-14T09:00:00Z"},
    {"id": "anchor_2026_04_01_a", "label": "morning-walk", "date": "2026-04-01", "recorded_at": "2026-04-01T07:00:00Z"},
    {"id": "anchor_2026_04_02_a", "label": "late-night-snacks", "date": "2026-04-02", "note": "kitchen closes at nine", "recorded_at": "2026-04-02T22:00:00Z"},
    {"id": "anchor_2026_04_01_a", "label": "morning-walk", "date": "2026-04-01", "note": "moved to evenings", "recorded_at": "2026-06-01T07:00:00Z", "state": "sunset"}
  ]
}
`

// anchorCompatV3 is a version 3 anchors.json: a pre-id record adopted by the
// id backfill (adopts/adopted_at, recorded_at copied verbatim), a rename of a
// minted anchor (one append under the same id), and the legacy rename pair (an
// id-less sunset stub retiring the old name plus a freshly minted record
// carrying the original date under the new one).
const anchorCompatV3 = `{
  "version": 3,
  "history": [
    {"label": "sobriety", "date": "2026-01-01", "recorded_at": "2026-01-01T09:00:00Z"},
    {"id": "anchor_2026_02_01_a", "label": "gate-30", "date": "2026-02-01", "recorded_at": "2026-02-01T08:00:00Z"},
    {"label": "evening-read", "date": "2026-03-01", "note": "ten pages", "recorded_at": "2026-03-01T08:00:00Z"},
    {"id": "anchor_2026_06_01_a", "label": "sobriety", "date": "2026-01-01", "recorded_at": "2026-01-01T09:00:00Z", "adopts": "legacy:sobriety", "adopted_at": "2026-06-01T10:00:00Z"},
    {"id": "anchor_2026_02_01_a", "label": "first-gate", "date": "2026-02-01", "recorded_at": "2026-06-15T08:00:00Z"},
    {"label": "evening-read", "date": "2026-03-01", "recorded_at": "2026-06-20T08:00:00Z", "state": "sunset"},
    {"id": "anchor_2026_06_20_a", "label": "night-read", "date": "2026-03-01", "note": "ten pages", "recorded_at": "2026-06-20T08:00:00Z"}
  ]
}
`

// anchorCompatChainJSON is the chain half of the frozen projection: no day is
// decided in these Ledgers, so every window is an honest zero and ref is null.
// Each case appends its own anchors half to it.
const anchorCompatChainJSON = `{"current_streak":0,"longest_streak":0,` +
	`"adherence":{"length":30,"adherence":0,"completed":0,"decided":0,"floor_days":0,"floor_day_ratio":0,"days_accounted":0},` +
	`"misses_in_window":0,"error_budget":{"budget":4,"burn":0,"remaining":4,"exceeded":false},` +
	`"gates":[` +
	`{"length":30,"adherence":{"length":30,"adherence":0,"completed":0,"decided":0,"floor_days":0,"floor_day_ratio":0,"days_accounted":0}},` +
	`{"length":60,"adherence":{"length":60,"adherence":0,"completed":0,"decided":0,"floor_days":0,"floor_day_ratio":0,"days_accounted":0}},` +
	`{"length":90,"adherence":{"length":90,"adherence":0,"completed":0,"decided":0,"floor_days":0,"floor_day_ratio":0,"days_accounted":0}}],`

// TestAnchorCompatV1V2V3MetricsUnchanged pins the exact `lucid metrics`
// projection — the prose lines and the JSON bytes — for a version 1, 2 and 3
// anchors.json with no anchor event store present. It was written and frozen
// against the projection as it stood before anchors had kinds, so any later
// change that alters what an existing Ledger reads (a field, an id, a day
// count, a byte of JSON) fails here. A Ledger without kinds must read exactly
// as it always has, and reading it must neither create the event store nor
// touch anchors.json. Every label is synthetic.
func TestAnchorCompatV1V2V3MetricsUnchanged(t *testing.T) {
	// Logical day 2026-07-06 under the default 04:00 rollover.
	now := atUTC(2026, 7, 6, 9, 0)
	chainLines := []string{
		"Streak: 0 (longest 0).",
		"30-day adherence: no decided days yet (0 accounted).",
		"60-day gate adherence: no decided days yet (0 accounted).",
		"90-day gate adherence: no decided days yet (0 accounted).",
		"Error budget: 0/4 isolated misses spent (4 left).",
	}

	cases := []struct {
		name        string
		anchors     string
		anchorLines []string
		anchorsJSON string
	}{
		{
			name:    "v1",
			anchors: anchorCompatV1,
			anchorLines: []string{
				"Days since first-gate: 66.",
				"Days since sobriety: 127.",
			},
			anchorsJSON: `"anchors":[` +
				`{"id":"legacy:first-gate","label":"first-gate","date":"2026-05-01","days_since":66},` +
				`{"id":"legacy:sobriety","label":"sobriety","date":"2026-03-01","note":"corrected start","days_since":127}],` +
				`"anchors_sunset":[],"ref":null}`,
		},
		{
			name:    "v2",
			anchors: anchorCompatV2,
			anchorLines: []string{
				"Days since first-gate: 142.",
				"Days since late-night-snacks: 95.",
			},
			anchorsJSON: `"anchors":[` +
				`{"id":"legacy:first-gate","label":"first-gate","date":"2026-02-14","days_since":142},` +
				`{"id":"anchor_2026_04_02_a","label":"late-night-snacks","date":"2026-04-02","note":"kitchen closes at nine","days_since":95}],` +
				`"anchors_sunset":[` +
				`{"id":"anchor_2026_04_01_a","label":"morning-walk","date":"2026-04-01","note":"moved to evenings","sunset_at":"2026-06-01T07:00:00Z"}],` +
				`"ref":null}`,
		},
		{
			name:    "v3",
			anchors: anchorCompatV3,
			anchorLines: []string{
				"Days since first-gate: 155.",
				"Days since night-read: 127.",
				"Days since sobriety: 186.",
			},
			anchorsJSON: `"anchors":[` +
				`{"id":"anchor_2026_02_01_a","label":"first-gate","date":"2026-02-01","days_since":155},` +
				`{"id":"anchor_2026_06_20_a","label":"night-read","date":"2026-03-01","note":"ten pages","days_since":127},` +
				`{"id":"anchor_2026_06_01_a","label":"sobriety","date":"2026-01-01","days_since":186}],` +
				`"anchors_sunset":[` +
				`{"id":"legacy:evening-read","label":"evening-read","date":"2026-03-01","sunset_at":"2026-06-20T08:00:00Z"}],` +
				`"ref":null}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, _, home := newBootedRouter(t)
			require.NoError(t, r.store.ScaffoldEngine())
			anchorsPath := filepath.Join(home, "engine", "anchors.json")
			require.NoError(t, os.WriteFile(anchorsPath, []byte(tc.anchors), 0o600))

			res, err := r.Metrics(now)
			require.NoError(t, err)

			wantLines := append(append([]string{}, chainLines...), tc.anchorLines...)
			assert.Equal(t, wantLines, res.Lines, "the prose projection is unchanged")

			got, err := json.Marshal(res.Metrics)
			require.NoError(t, err)
			assert.Equal(t, anchorCompatChainJSON+tc.anchorsJSON, string(got),
				"the JSON projection is byte-identical")

			assert.NoFileExists(t, filepath.Join(home, "engine", "anchor_events.json"),
				"a read never creates the anchor event store")
			after, err := os.ReadFile(anchorsPath)
			require.NoError(t, err)
			assert.Equal(t, tc.anchors, string(after), "a read never rewrites anchors.json")
		})
	}
}
