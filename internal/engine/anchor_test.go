package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateAnchor_LabelAndDate covers the pure write-path guard: a
// non-empty label plus a civil YYYY-MM-DD date is accepted (backdatable, so
// any past or future date passes), while an empty/whitespace label or a
// malformed date is rejected before any disk touch.
func TestValidateAnchor_LabelAndDate(t *testing.T) {
	cases := []struct {
		name    string
		label   string
		date    string
		wantErr bool
	}{
		{"good past date", "quit-x", "2025-12-15", false},
		{"good future date", "gate", "2099-01-01", false},
		{"empty label", "", "2026-01-01", true},
		{"whitespace label", "   ", "2026-01-01", true},
		{"empty date", "quit-x", "", true},
		{"malformed date", "quit-x", "2026-13-40", true},
		{"non-date text", "quit-x", "yesterday", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAnchor(tc.label, tc.date)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

// TestLatestAnchors_Correction proves latest-wins is decided by append order,
// not by the date value: a second append whose date is *earlier* than the
// original still supersedes it (a mistyped-date correction).
func TestLatestAnchors_Correction(t *testing.T) {
	log := AnchorLog{Version: AnchorVersion, History: []Anchor{
		{Label: "quit-x", Date: "2026-01-01", RecordedAt: "2026-01-01T10:00:00Z"},
		{Label: "quit-x", Date: "2025-12-15", RecordedAt: "2026-01-02T10:00:00Z"},
	}}
	got := LatestAnchors(log)
	require.Len(t, got, 1)
	assert.Equal(t, "quit-x", got[0].Label)
	assert.Equal(t, "2025-12-15", got[0].Date, "the later append wins even though its date is earlier")
}

// TestLatestAnchors_Reset proves a later-dated append (a genuine reset of the
// count) also supersedes — same append-only operation as a correction.
func TestLatestAnchors_Reset(t *testing.T) {
	log := AnchorLog{Version: AnchorVersion, History: []Anchor{
		{Label: "quit-x", Date: "2026-01-01", RecordedAt: "2026-01-01T10:00:00Z"},
		{Label: "quit-x", Date: "2026-02-01", RecordedAt: "2026-02-01T10:00:00Z"},
	}}
	got := LatestAnchors(log)
	require.Len(t, got, 1)
	assert.Equal(t, "2026-02-01", got[0].Date)
}

// TestLatestAnchors_MultipleLabelsSorted confirms distinct labels each keep
// their own newest record and the result is sorted by label.
func TestLatestAnchors_MultipleLabelsSorted(t *testing.T) {
	log := AnchorLog{Version: AnchorVersion, History: []Anchor{
		{Label: "gate", Date: "2026-01-05", RecordedAt: "2026-01-05T10:00:00Z"},
		{Label: "quit-x", Date: "2026-01-01", RecordedAt: "2026-01-01T10:00:00Z"},
		{Label: "gate", Date: "2026-03-01", RecordedAt: "2026-03-01T10:00:00Z"},
	}}
	got := LatestAnchors(log)
	require.Len(t, got, 2)
	assert.Equal(t, "gate", got[0].Label)
	assert.Equal(t, "2026-03-01", got[0].Date)
	assert.Equal(t, "quit-x", got[1].Label)
	assert.Equal(t, "2026-01-01", got[1].Date)
}

// TestLatestAnchors_Empty yields an empty (non-nil) slice for a fresh log.
func TestLatestAnchors_Empty(t *testing.T) {
	got := LatestAnchors(AnchorLog{Version: AnchorVersion, History: []Anchor{}})
	assert.Empty(t, got)
}
