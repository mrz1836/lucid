package engine

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"
	"time"

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

// TestAnchorIdentity_MintedAndLegacy covers both identity forms: a record
// that carries an id folds under it verbatim, while one written before ids
// existed folds under a synthetic identity derived from its label.
func TestAnchorIdentity_MintedAndLegacy(t *testing.T) {
	cases := []struct {
		name   string
		anchor Anchor
		want   string
	}{
		{"minted", Anchor{ID: "anchor_2026_08_04_a", Label: "gate-30"}, "anchor_2026_08_04_a"},
		{"legacy", Anchor{Label: "gate-30"}, "legacy:gate-30"},
		{"legacy keeps label verbatim", Anchor{Label: "quit-x"}, "legacy:quit-x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, AnchorIdentity(tc.anchor))
		})
	}
}

// TestLatestAnchors_FoldsOnIdentityNotLabel is the case the identity model
// exists for: a retired milestone and a later milestone reusing its display
// name are two distinct anchors, not one that paused and resumed. Under label
// identity the second would have superseded the first and the sunset would
// have vanished.
func TestLatestAnchors_FoldsOnIdentityNotLabel(t *testing.T) {
	log := AnchorLog{Version: AnchorVersion, History: []Anchor{
		{ID: "anchor_2026_01_01_a", Label: "gate-30", Date: "2026-01-01", RecordedAt: "2026-01-01T10:00:00Z"},
		{
			ID: "anchor_2026_01_01_a", Label: "gate-30", Date: "2026-01-01",
			RecordedAt: "2026-08-04T10:00:00Z", State: AnchorStateSunset,
		},
		{ID: "anchor_2026_08_04_a", Label: "gate-30", Date: "2026-08-01", RecordedAt: "2026-08-04T11:00:00Z"},
	}}
	got := LatestAnchors(log)
	require.Len(t, got, 2, "same label, different ids folds to two anchors")

	sunset, ok := LatestSunsetFor(got, "gate-30")
	require.True(t, ok)
	assert.Equal(t, "anchor_2026_01_01_a", sunset.ID)

	active, ok := FindActiveAnchor(got, "gate-30")
	require.True(t, ok)
	assert.Equal(t, "anchor_2026_08_04_a", active.ID)
	assert.Equal(t, "2026-08-01", active.Date)
}

// TestLatestAnchors_LegacyRecordUnchanged proves the re-key costs a pre-id
// ledger nothing: an id-less history folds exactly as it always did, because
// every record's synthetic identity is its label.
func TestLatestAnchors_LegacyRecordUnchanged(t *testing.T) {
	log := AnchorLog{Version: 1, History: []Anchor{
		{Label: "gate-30", Date: "2026-01-01", RecordedAt: "2026-01-01T10:00:00Z"},
		{Label: "sobriety", Date: "2025-06-01", RecordedAt: "2025-06-01T10:00:00Z"},
		{Label: "gate-30", Date: "2026-02-01", RecordedAt: "2026-02-01T10:00:00Z"},
	}}
	got := LatestAnchors(log)
	require.Len(t, got, 2)
	assert.Equal(t, "gate-30", got[0].Label)
	assert.Equal(t, "2026-02-01", got[0].Date, "same label still means same anchor")
	assert.Empty(t, got[0].ID, "folding never stamps an id onto a legacy record")
	assert.Equal(t, "sobriety", got[1].Label)
}

// TestLatestAnchors_MixedLegacyAndMinted covers the coexistence the store
// lives with permanently: a pre-id record and a minted one sharing a display
// name are two anchors, ordered deterministically rather than by whatever the
// map happened to yield.
func TestLatestAnchors_MixedLegacyAndMinted(t *testing.T) {
	log := AnchorLog{Version: AnchorVersion, History: []Anchor{
		{Label: "gate-30", Date: "2026-01-01", RecordedAt: "2026-01-01T10:00:00Z"},
		{ID: "anchor_2026_08_04_a", Label: "gate-30", Date: "2026-08-01", RecordedAt: "2026-08-04T10:00:00Z"},
	}}
	for range 20 { // a map-iteration regression would surface as a flapping order
		got := LatestAnchors(log)
		require.Len(t, got, 2)
		assert.Equal(t, "anchor_2026_08_04_a", got[0].ID, "anchor_ sorts before legacy: within a label")
		assert.Empty(t, got[1].ID)
	}
}

// TestNextAnchorID_SlotsPerDay covers the minting grammar: per-day slots that
// advance, restart on a new day, and are never reissued once used — including
// by an anchor that has since been sunset, which is why the allocator scans
// the whole history rather than the folded set.
func TestNextAnchorID_SlotsPerDay(t *testing.T) {
	day := time.Date(2026, 8, 4, 14, 15, 0, 0, time.UTC)

	t.Run("empty log takes the first slot", func(t *testing.T) {
		assert.Equal(t, "anchor_2026_08_04_a", NextAnchorID(AnchorLog{}, day))
	})

	t.Run("a used slot advances", func(t *testing.T) {
		log := AnchorLog{History: []Anchor{
			{ID: "anchor_2026_08_04_a", Label: "gate-30", Date: "2026-08-01", RecordedAt: "2026-08-04T10:00:00Z"},
		}}
		assert.Equal(t, "anchor_2026_08_04_b", NextAnchorID(log, day))
	})

	t.Run("a sunset anchor's slot is not reissued", func(t *testing.T) {
		log := AnchorLog{History: []Anchor{
			{ID: "anchor_2026_08_04_a", Label: "gate-30", Date: "2026-08-01", RecordedAt: "2026-08-04T10:00:00Z"},
			{
				ID: "anchor_2026_08_04_a", Label: "gate-30", Date: "2026-08-01",
				RecordedAt: "2026-08-04T12:00:00Z", State: AnchorStateSunset,
			},
		}}
		assert.Equal(t, "anchor_2026_08_04_b", NextAnchorID(log, day))
	})

	t.Run("another day restarts at the first slot", func(t *testing.T) {
		log := AnchorLog{History: []Anchor{
			{ID: "anchor_2026_08_04_a", Label: "gate-30", Date: "2026-08-01", RecordedAt: "2026-08-04T10:00:00Z"},
			{ID: "anchor_2026_08_04_b", Label: "sobriety", Date: "2026-08-02", RecordedAt: "2026-08-04T11:00:00Z"},
		}}
		next := NextAnchorID(log, day.AddDate(0, 0, 1))
		assert.Equal(t, "anchor_2026_08_05_a", next)
	})

	t.Run("legacy records hold no slot", func(t *testing.T) {
		log := AnchorLog{History: []Anchor{
			{Label: "gate-30", Date: "2026-01-01", RecordedAt: "2026-01-01T10:00:00Z"},
		}}
		assert.Equal(t, "anchor_2026_08_04_a", NextAnchorID(log, day))
	})

	t.Run("the id carries no part of any label", func(t *testing.T) {
		log := AnchorLog{History: []Anchor{
			{Label: "gate-30", Date: "2026-01-01", RecordedAt: "2026-01-01T10:00:00Z"},
		}}
		assert.NotContains(t, NextAnchorID(log, day), "gate")
	})
}

// TestSlotLabel_Bijective pins the shared per-day slot grammar: bijective
// base-26, so a day never runs out of ids and no slot renders empty.
func TestSlotLabel_Bijective(t *testing.T) {
	cases := map[int]string{0: "a", 25: "z", 26: "aa", 27: "ab", 51: "az", 52: "ba"}
	for n, want := range cases {
		assert.Equal(t, want, SlotLabel(n))
	}
}

// TestFindActiveAnchor_Hit finds the active anchor holding a label and skips
// unrelated ones.
func TestFindActiveAnchor_Hit(t *testing.T) {
	latest := []Anchor{
		{ID: "anchor_2026_08_04_a", Label: "gate-30", Date: "2026-08-01"},
		{ID: "anchor_2026_08_04_b", Label: "sobriety", Date: "2026-06-01"},
	}
	got, ok := FindActiveAnchor(latest, "sobriety")
	require.True(t, ok)
	assert.Equal(t, "anchor_2026_08_04_b", got.ID)
}

// TestFindActiveAnchor_Miss reports no hit for an unrecorded label.
func TestFindActiveAnchor_Miss(t *testing.T) {
	latest := []Anchor{{ID: "anchor_2026_08_04_a", Label: "gate-30", Date: "2026-08-01"}}
	_, ok := FindActiveAnchor(latest, "gate-3")
	assert.False(t, ok)
}

// TestFindActiveAnchor_SunsetOnly proves a retired anchor does not answer to
// its label: the name is free, which is what lets it be re-added or renamed
// onto.
func TestFindActiveAnchor_SunsetOnly(t *testing.T) {
	latest := []Anchor{{
		ID: "anchor_2026_01_01_a", Label: "gate-30", Date: "2026-01-01",
		RecordedAt: "2026-08-04T10:00:00Z", State: AnchorStateSunset,
	}}
	_, ok := FindActiveAnchor(latest, "gate-30")
	assert.False(t, ok)
}

// TestFindActiveAnchor_AtMostOneActivePerLabel asserts the invariant the bare
// label arguments rest on, rather than letting a first-hit scan quietly hide a
// duplicate: recording an existing active label targets that anchor, so a
// folded set can never hold two active anchors under one name.
func TestFindActiveAnchor_AtMostOneActivePerLabel(t *testing.T) {
	log := AnchorLog{Version: AnchorVersion, History: []Anchor{
		{ID: "anchor_2026_08_04_a", Label: "gate-30", Date: "2026-08-01", RecordedAt: "2026-08-04T10:00:00Z"},
		{ID: "anchor_2026_08_04_a", Label: "gate-30", Date: "2026-07-01", RecordedAt: "2026-08-04T11:00:00Z"},
		{
			ID: "anchor_2026_01_01_a", Label: "gate-30", Date: "2026-01-01",
			RecordedAt: "2026-08-04T09:00:00Z", State: AnchorStateSunset,
		},
	}}
	latest := LatestAnchors(log)

	active := 0
	for _, a := range latest {
		if !a.IsSunset() && a.Label == "gate-30" {
			active++
		}
	}
	assert.Equal(t, 1, active, "a correction reuses the id rather than minting a second active anchor")

	got, ok := FindActiveAnchor(latest, "gate-30")
	require.True(t, ok)
	assert.Equal(t, "2026-07-01", got.Date, "the correction is the one that answers")
}

// TestLatestSunsetFor covers the retired-record lookup: the newest sunset
// under a label, nothing for an active-only label, and nothing for an
// unrecorded one.
func TestLatestSunsetFor(t *testing.T) {
	latest := []Anchor{
		{ID: "anchor_2026_08_04_a", Label: "sobriety", Date: "2026-06-01", RecordedAt: "2026-08-04T10:00:00Z"},
		{
			ID: "anchor_2026_01_01_a", Label: "gate-30", Date: "2026-01-01",
			RecordedAt: "2026-03-01T10:00:00Z", State: AnchorStateSunset,
		},
		{
			ID: "anchor_2026_04_01_a", Label: "gate-30", Date: "2026-04-01",
			RecordedAt: "2026-08-04T10:00:00Z", State: AnchorStateSunset,
		},
	}

	got, ok := LatestSunsetFor(latest, "gate-30")
	require.True(t, ok)
	assert.Equal(t, "anchor_2026_04_01_a", got.ID, "the most recently appended sunset wins")

	_, ok = LatestSunsetFor(latest, "sobriety")
	assert.False(t, ok, "an active anchor is not a sunset one")

	_, ok = LatestSunsetFor(latest, "gate-3")
	assert.False(t, ok)
}

// TestAnchorLabels_SortedDeduped covers the list an unrecorded-label
// rejection names: active labels only, sorted, deduped, and empty rather than
// nil when nothing is recorded.
func TestAnchorLabels_SortedDeduped(t *testing.T) {
	latest := []Anchor{
		{ID: "anchor_2026_08_04_b", Label: "sobriety", Date: "2026-06-01"},
		{ID: "anchor_2026_08_04_a", Label: "gate-30", Date: "2026-08-01"},
		{ID: "anchor_2026_08_04_c", Label: "gate-30", Date: "2026-08-02"},
		{
			ID: "anchor_2026_01_01_a", Label: "retired-one", Date: "2026-01-01",
			RecordedAt: "2026-08-04T10:00:00Z", State: AnchorStateSunset,
		},
	}
	assert.Equal(t, []string{"gate-30", "sobriety"}, AnchorLabels(latest))

	empty := AnchorLabels(nil)
	assert.NotNil(t, empty, "an empty store yields an empty list, not nil")
	assert.Empty(t, empty)
}

// TestSunsetDate renders the civil retirement date, and degrades to printing
// what is there when a stored timestamp is too short to carry one.
func TestSunsetDate(t *testing.T) {
	cases := []struct {
		name       string
		recordedAt string
		want       string
	}{
		{"rfc3339", "2026-08-04T17:03:49Z", "2026-08-04"},
		{"offset timestamp", "2026-08-04T17:03:49-04:00", "2026-08-04"},
		{"already a civil date", "2026-08-04", "2026-08-04"},
		{"too short passes through", "2026-08", "2026-08"},
		{"empty passes through", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, SunsetDate(Anchor{RecordedAt: tc.recordedAt}))
		})
	}
}

// TestAnchor_IsSunset covers the state discriminator: absent means active, so
// every record written before the state existed stays valid untouched.
func TestAnchor_IsSunset(t *testing.T) {
	assert.False(t, Anchor{Label: "gate-30"}.IsSunset(), "state absent means active")
	assert.True(t, Anchor{Label: "gate-30", State: AnchorStateSunset}.IsSunset())
}

// TestProjectAnchor_ResolvesID is the projection-boundary guard: a minted id is
// echoed verbatim, while a record written before ids existed publishes its
// synthetic identity rather than an empty address. Every other field is carried
// through unchanged.
func TestProjectAnchor_ResolvesID(t *testing.T) {
	minted := Anchor{
		ID: "anchor_2026_08_04_a", Label: "gate-30", Date: "2026-02-01",
		Note: "the thirty-day gate", RecordedAt: "2026-08-04T17:03:49Z",
	}
	assert.Equal(t, AnchorRecord{
		ID: "anchor_2026_08_04_a", Label: "gate-30", Date: "2026-02-01",
		Note: "the thirty-day gate", RecordedAt: "2026-08-04T17:03:49Z",
	}, ProjectAnchor(minted), "a minted id is echoed verbatim")

	legacy := Anchor{Label: "gate-30", Date: "2026-02-01", RecordedAt: "2026-02-01T09:00:00Z"}
	assert.Equal(t, "legacy:gate-30", ProjectAnchor(legacy).ID, "a pre-id record still publishes an address")

	retired := Anchor{
		ID: "anchor_2026_08_04_a", Label: "gate-30", Date: "2026-02-01",
		RecordedAt: "2026-08-04T17:03:49Z", State: AnchorStateSunset,
	}
	assert.Equal(t, AnchorStateSunset, ProjectAnchor(retired).State, "a retirement is visible to a JSON caller")
}

// TestProjectAnchor_JSONShape pins the published key set: id is unconditional
// so a caller can always address what it read, note and state are omitted when
// empty, and nothing else leaks from the stored record.
func TestProjectAnchor_JSONShape(t *testing.T) {
	b, err := json.Marshal(ProjectAnchor(Anchor{
		Label: "gate-30", Date: "2026-02-01", RecordedAt: "2026-02-01T09:00:00Z",
	}))
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(b, &got))
	keys := slices.Sorted(maps.Keys(got))
	assert.Equal(t, []string{"date", "id", "label", "recorded_at"}, keys)
	assert.Equal(t, "legacy:gate-30", got["id"])
}
