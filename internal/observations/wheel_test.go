package observations

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fullWheel returns a synthetic snapshot pillar map with all eight pillars
// rated — every value is invented test data.
func fullWheel() map[string]PillarScore {
	return map[string]PillarScore{
		PillarHealth:        {Score: 6},
		PillarRelationships: {Score: 7},
		PillarCareer:        {Score: 5},
		PillarFinances:      {Score: 4},
		PillarGrowth:        {Score: 7},
		PillarFun:           {Score: 4},
		PillarEnvironment:   {Score: 6},
		PillarContribution:  {Score: 5},
	}
}

func intPtr(n int) *int { return &n }

// TestWheelPillars_CanonicalOrder: the eight pillars come back in the documented
// canonical order with their labels (wheel.md §2), as a fresh slice each call.
func TestWheelPillars_CanonicalOrder(t *testing.T) {
	assert.Equal(t, []string{
		"health", "relationships", "career", "finances", "growth", "fun", "environment", "contribution",
	}, WheelPillarKeys())

	labels := make([]string, 0, 8)
	for _, p := range WheelPillars() {
		labels = append(labels, p.Label)
	}
	assert.Equal(t, []string{
		"health", "relationships", "career/work", "finances", "personal growth", "fun/recreation",
		"environment", "contribution",
	}, labels)

	first := WheelPillars()
	first[0].Label = "mutated"
	assert.Equal(t, "health", WheelPillars()[0].Label, "each call returns a fresh slice")

	label, ok := WheelPillarLabel(PillarGrowth)
	require.True(t, ok)
	assert.Equal(t, "personal growth", label)
	_, ok = WheelPillarLabel("wealth")
	assert.False(t, ok)
	assert.True(t, IsWheelPillar(PillarFun))
	assert.False(t, IsWheelPillar("Fun"), "pillar keys are exact")
}

// TestWheelScaleRejectsOutOfRangeAndNonInteger: the wheel's own 1–10 scale
// accepts only a plain whole number in range — never a decimal, a quoted JSON
// string, a sign, an exponent, zero, or eleven (wheel.md §3).
func TestWheelScaleRejectsOutOfRangeAndNonInteger(t *testing.T) {
	for raw, want := range map[string]int{"1": 1, "10": 10, "6": 6, " 7 ": 7, "07": 7} {
		got, ok := ParseWheelScore(raw)
		require.Truef(t, ok, "%q is a rating", raw)
		assert.Equalf(t, want, got, "%q", raw)
	}
	for _, raw := range []string{"", " ", "0", "11", "-1", "+5", "2.5", "6.0", `"6"`, "1e1", "six", "5/10", "99999999999999999999"} {
		_, ok := ParseWheelScore(raw)
		assert.Falsef(t, ok, "%q is not a 1–10 rating", raw)
	}

	assert.True(t, ValidWheelScore(1))
	assert.True(t, ValidWheelScore(10))
	assert.False(t, ValidWheelScore(0))
	assert.False(t, ValidWheelScore(11))
}

// TestWheelSelfRatingRequired_SnapshotValidate: a snapshot must rate all eight
// pillars on the 1–10 scale — a missing pillar is refused, never defaulted; an
// unknown pillar, an off-scale rating, and an off-scale suggestion are refused
// too (wheel.md §3, §5).
func TestWheelSelfRatingRequired_SnapshotValidate(t *testing.T) {
	ok := WheelSnapshot{Type: WheelEventSnapshot, Pillars: fullWheel()}
	require.NoError(t, ok.Validate())

	missing := fullWheel()
	delete(missing, PillarCareer)
	delete(missing, PillarFun)
	err := WheelSnapshot{Pillars: missing}.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing rating(s) for career, fun")
	assert.Equal(t, []string{PillarCareer, PillarFun}, MissingWheelPillars(missing), "canonical order")
	assert.Empty(t, MissingWheelPillars(fullWheel()))

	unknown := fullWheel()
	unknown["wealth"] = PillarScore{Score: 5}
	err = WheelSnapshot{Pillars: unknown}.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown pillar(s) wealth")
	assert.Equal(t, []string{"wealth"}, UnknownWheelPillars(unknown))

	for _, bad := range []int{0, 11, -3} {
		off := fullWheel()
		off[PillarHealth] = PillarScore{Score: bad}
		err = WheelSnapshot{Pillars: off}.Validate()
		require.Errorf(t, err, "score %d", bad)
		assert.Contains(t, err.Error(), "rating for health")
	}

	offSuggested := fullWheel()
	offSuggested[PillarHealth] = PillarScore{Score: 6, Suggested: intPtr(0)}
	err = WheelSnapshot{Pillars: offSuggested}.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "suggestion for health")

	withSuggested := fullWheel()
	withSuggested[PillarHealth] = PillarScore{Score: 6, Suggested: intPtr(3)}
	assert.NoError(t, WheelSnapshot{Pillars: withSuggested}.Validate(), "a valid suggestion is optional and accepted")
}

// TestWheelSuggestedIsSeparateFromScore: a suggestion is stored in its own
// field — WheelScores carries the self-ratings only — and a snapshot without one
// marshals no suggested key at all (wheel.md §4, §5).
func TestWheelSuggestedIsSeparateFromScore(t *testing.T) {
	pillars := fullWheel()
	pillars[PillarHealth] = PillarScore{Score: 6, Note: "synthetic note", Suggested: intPtr(2)}
	snap := WheelSnapshot{Type: WheelEventSnapshot, Pillars: pillars}

	scores := snap.WheelScores()
	assert.Equal(t, 6, scores[PillarHealth], "the score is the self-rating, never the suggestion")
	assert.Len(t, scores, 8)

	b, err := json.Marshal(snap.Pillars[PillarHealth])
	require.NoError(t, err)
	assert.JSONEq(t, `{"score":6,"note":"synthetic note","suggested":2}`, string(b))

	b, err = json.Marshal(snap.Pillars[PillarCareer])
	require.NoError(t, err)
	assert.JSONEq(t, `{"score":5}`, string(b), "unset note and suggestion are omitted")
}

// TestWheelFold_LatestWins: the month's wheel is the latest snapshot appended
// (amend by append, latest wins); an unknown event type is skipped, and an empty
// history folds to nothing (wheel.md §4).
func TestWheelFold_LatestWins(t *testing.T) {
	e := NewWheelEntry("2026-09", "2026-09-06T17:12:40-04:00")
	_, ok := e.Fold()
	assert.False(t, ok, "no snapshot yet")
	assert.Equal(t, 0, e.SnapshotCount())

	first := fullWheel()
	second := fullWheel()
	second[PillarHealth] = PillarScore{Score: 8}
	e.History = append(e.History,
		WheelSnapshot{ID: "wheel_2026_09_001", Type: WheelEventSnapshot, Pillars: first},
		WheelSnapshot{ID: "wheel_2026_09_002", Type: WheelEventSnapshot, Pillars: second},
		WheelSnapshot{ID: "wheel_2026_09_003", Type: "future-kind"},
	)

	got, ok := e.Fold()
	require.True(t, ok)
	assert.Equal(t, "wheel_2026_09_002", got.ID, "the latest snapshot wins; an unknown type is skipped")
	assert.Equal(t, 8, got.Pillars[PillarHealth].Score)
	assert.Equal(t, 2, e.SnapshotCount())
}

// TestWheelEntry_NewAndNormalized: a fresh entry carries the month key, kind,
// schema, and an empty (never null) history.
func TestWheelEntry_NewAndNormalized(t *testing.T) {
	e := NewWheelEntry("2026-09", "2026-09-06T17:12:40-04:00")
	assert.Equal(t, "wheel_2026-09", e.Key)
	assert.Equal(t, RegistryWheel, e.Kind)
	assert.Equal(t, WheelSchema, e.Schema)
	assert.Equal(t, "2026-09", e.Month)
	assert.Equal(t, e.CreatedAt, e.UpdatedAt)
	require.NoError(t, e.Validate())

	e.History = nil
	assert.NotNil(t, e.Normalized().History)
}

// TestWheelEntry_Validate: an entry refuses an unwritable schema, a wrong kind,
// a malformed month, and a key that is not the month's own.
func TestWheelEntry_Validate(t *testing.T) {
	base := NewWheelEntry("2026-09", "2026-09-06T17:12:40-04:00")

	newer := base
	newer.Schema = WheelSchema + 1
	require.ErrorContains(t, newer.Validate(), "unsupported wheel schema")

	zero := base
	zero.Schema = 0
	require.ErrorContains(t, zero.Validate(), "unsupported wheel schema")

	kind := base
	kind.Kind = RegistryGratitude
	require.ErrorContains(t, kind.Validate(), "wrong kind")

	month := base
	month.Month = "2026-9"
	require.ErrorContains(t, month.Validate(), "malformed month")

	key := base
	key.Key = "wheel_2026-08"
	require.ErrorContains(t, key.Validate(), "does not match month")
}

// TestWheelIDGrammar: the stable entry id is wheel_YYYY-MM and each snapshot's
// receipt is wheel_YYYY_MM_<seq> — distinct shapes that never collide; the seq
// is max+1 over well-formed receipts, never a count (wheel.md §4 Ids).
func TestWheelIDGrammar(t *testing.T) {
	assert.Equal(t, "wheel_2026-09", WheelEntryKey("2026-09"))
	assert.Equal(t, "wheel_2026_09_001", WheelReceiptID("2026-09", 1))
	assert.Equal(t, "wheel_2026_09_1000", WheelReceiptID("2026-09", 1000), "wider values are legal")

	for id, want := range map[string]int{"wheel_2026_09_001": 1, "wheel_2026_09_042": 42, "wheel_2026_09_1000": 1000} {
		got, ok := ParseWheelReceiptSeq(id)
		require.Truef(t, ok, id)
		assert.Equalf(t, want, got, id)
	}
	for _, id := range []string{
		"wheel_2026-09", "grat_2026_09_01_001", "wheel_2026_13_001", "wheel_2026_09", "wheel_2026_09_x1",
		"wheel_2026_09_", "wheel_2026_09_-1", "", "wheel_2026_09_001_002",
	} {
		_, ok := ParseWheelReceiptSeq(id)
		assert.Falsef(t, ok, "%q is not a wheel receipt", id)
	}

	assert.Equal(t, 1, NextWheelSeq(nil))
	assert.Equal(t, 8, NextWheelSeq([]WheelSnapshot{
		{ID: "wheel_2026_09_001"}, {ID: "wheel_2026_09_007"}, {ID: "hand-edited"},
	}), "max+1 over well-formed receipts; a stray id never perturbs the seq")
}

// TestWheelMonth: the month key is a strict YYYY-MM, and the default month is
// the month of the logical day — 04:00 rollover — so a review finished just
// after midnight on the 1st files under the month it reviewed (wheel.md §7.1).
func TestWheelMonth(t *testing.T) {
	for _, m := range []string{"2026-09", "2026-12", "1999-01"} {
		assert.Truef(t, ValidWheelMonth(m), m)
	}
	for _, m := range []string{"", "2026-9", "2026-13", "2026-00", "26-09", "2026/09", "2026-09-01", "@2026-09", " 2026-09", "../x"} {
		assert.Falsef(t, ValidWheelMonth(m), "%q", m)
	}

	assert.Equal(t, "2026-09", WheelMonthOf(time.Date(2026, 10, 1, 2, 30, 0, 0, loc)), "before 04:00 on the 1st is still last month")
	assert.Equal(t, "2026-10", WheelMonthOf(time.Date(2026, 10, 1, 4, 0, 0, 0, loc)))
	assert.Equal(t, "2026-09", WheelMonthOf(time.Date(2026, 9, 15, 12, 0, 0, 0, loc)))
}

// TestRegistryDir_Wheel: the wheel is a registry kind living under
// registries/wheel/.
func TestRegistryDir_Wheel(t *testing.T) {
	dir, ok := RegistryDir(RegistryWheel)
	require.True(t, ok)
	assert.Equal(t, "wheel", dir)
}
