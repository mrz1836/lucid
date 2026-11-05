package observations

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fullWheelSet1 returns a synthetic pillar-set-1 snapshot pillar map with all
// eight set-1 pillars rated — the shape every snapshot before pillar set 2 was
// stored in; every value is invented test data.
func fullWheelSet1() map[string]PillarScore {
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

// TestWheelPillars_CanonicalOrder: the current pillar set is pillar set 2, and
// its nine pillars come back in the documented canonical order with their labels
// — romance and friends in relationships' old slot (wheel.md §2) — as a fresh
// slice each call; the retired relationships key is not a current pillar.
func TestWheelPillars_CanonicalOrder(t *testing.T) {
	assert.Equal(t, 2, CurrentWheelPillarSet, "new wheels are rated in pillar set 2")
	assert.Equal(t, 2, WheelSchema)
	assert.Equal(t, []string{
		"health", "romance", "friends", "career", "finances", "growth", "fun", "environment", "contribution",
	}, WheelPillarKeys())

	labels := make([]string, 0, 9)
	for _, p := range WheelPillars() {
		labels = append(labels, p.Label)
	}
	assert.Equal(t, []string{
		"health", "romance/partner", "friends & family", "career/work", "finances", "personal growth",
		"fun/recreation", "environment", "contribution",
	}, labels)
	assert.False(t, IsWheelPillar(PillarRelationships), "relationships is retired, never repurposed")
	into, retired := RetiredWheelPillar(PillarRelationships)
	require.True(t, retired, "relationships is retired in the current set")
	assert.Equal(t, []string{PillarRomance, PillarFriends}, into)
	for _, key := range []string{PillarRomance, PillarFriends, PillarHealth, "wealth"} {
		_, retired = RetiredWheelPillar(key)
		assert.Falsef(t, retired, "%q is not retired", key)
	}

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

// TestWheelSelfRatingRequired_SnapshotValidate: a new (current-set) snapshot
// must rate all nine pillars on the 1–10 scale — a missing pillar is refused,
// never defaulted; an unknown pillar, an off-scale rating, and an off-scale
// suggestion are refused too (wheel.md §3, §5).
func TestWheelSelfRatingRequired_SnapshotValidate(t *testing.T) {
	current := func(p map[string]PillarScore) WheelSnapshot {
		return WheelSnapshot{Type: WheelEventSnapshot, PillarSet: CurrentWheelPillarSet, Pillars: p}
	}
	require.NoError(t, current(fullWheelSet2()).Validate())

	missing := fullWheelSet2()
	delete(missing, PillarCareer)
	delete(missing, PillarFun)
	err := current(missing).Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing rating(s) for career, fun")
	assert.Equal(t, []string{PillarCareer, PillarFun}, MissingWheelPillars(missing), "canonical order")
	assert.Empty(t, MissingWheelPillars(fullWheelSet2()))
	assert.Equal(t, []string{PillarRomance, PillarFriends}, MissingWheelPillars(fullWheelSet1()),
		"the eight set-1 pillars miss both new spokes of the current set")

	unknown := fullWheelSet2()
	unknown["wealth"] = PillarScore{Score: 5}
	err = current(unknown).Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown pillar(s) wealth")
	assert.Equal(t, []string{"wealth"}, UnknownWheelPillars(unknown))
	assert.Equal(t, []string{PillarRelationships}, UnknownWheelPillars(fullWheelSet1()),
		"the retired key is not a pillar of the current set")

	for _, bad := range []int{0, 11, -3} {
		off := fullWheelSet2()
		off[PillarHealth] = PillarScore{Score: bad}
		err = current(off).Validate()
		require.Errorf(t, err, "score %d", bad)
		assert.Contains(t, err.Error(), "rating for health")
	}

	offSuggested := fullWheelSet2()
	offSuggested[PillarHealth] = PillarScore{Score: 6, Suggested: intPtr(0)}
	err = current(offSuggested).Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "suggestion for health")

	withSuggested := fullWheelSet2()
	withSuggested[PillarHealth] = PillarScore{Score: 6, Suggested: intPtr(3)}
	assert.NoError(t, current(withSuggested).Validate(), "a valid suggestion is optional and accepted")
}

// TestWheelSuggestedIsSeparateFromScore: a suggestion is stored in its own
// field — WheelScores carries the self-ratings only — and a snapshot without one
// marshals no suggested key at all (wheel.md §4, §5).
func TestWheelSuggestedIsSeparateFromScore(t *testing.T) {
	pillars := fullWheelSet2()
	pillars[PillarHealth] = PillarScore{Score: 6, Note: "synthetic note", Suggested: intPtr(2)}
	snap := WheelSnapshot{Type: WheelEventSnapshot, PillarSet: CurrentWheelPillarSet, Pillars: pillars}

	scores := snap.WheelScores()
	assert.Equal(t, 6, scores[PillarHealth], "the score is the self-rating, never the suggestion")
	assert.Len(t, scores, 9)

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

	first := fullWheelSet2()
	second := fullWheelSet2()
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

// fullWheelSet2 returns a synthetic pillar-set-2 snapshot pillar map with all
// nine pillars rated — every value is invented test data.
func fullWheelSet2() map[string]PillarScore {
	return map[string]PillarScore{
		PillarHealth:       {Score: 6},
		PillarRomance:      {Score: 3},
		PillarFriends:      {Score: 9},
		PillarCareer:       {Score: 5},
		PillarFinances:     {Score: 4},
		PillarGrowth:       {Score: 7},
		PillarFun:          {Score: 4},
		PillarEnvironment:  {Score: 6},
		PillarContribution: {Score: 5},
	}
}

func wheelSetKeysAndLabels(t *testing.T, set int) (keys, labels []string) {
	t.Helper()
	pillars, ok := WheelPillarsOf(set)
	require.Truef(t, ok, "pillar set %d is known", set)
	for _, p := range pillars {
		keys = append(keys, p.Key)
		labels = append(labels, p.Label)
	}
	return keys, labels
}

// TestWheelPillarSets_Table: the one pillar-set table holds set 1 (the
// original eight) and set 2 (nine, relationships split into romance and
// friends in its old slot) in canonical order with stable per-key labels, plus
// set 2's change entry; the current-set accessors read the current set, and an
// unknown set is unknown (wheel.md §2).
func TestWheelPillarSets_Table(t *testing.T) {
	keys1, labels1 := wheelSetKeysAndLabels(t, 1)
	assert.Equal(t, []string{
		"health", "relationships", "career", "finances", "growth", "fun", "environment", "contribution",
	}, keys1)
	assert.Equal(t, []string{
		"health", "relationships", "career/work", "finances", "personal growth", "fun/recreation",
		"environment", "contribution",
	}, labels1)

	keys2, labels2 := wheelSetKeysAndLabels(t, 2)
	assert.Equal(t, []string{
		"health", "romance", "friends", "career", "finances", "growth", "fun", "environment", "contribution",
	}, keys2)
	assert.Equal(t, []string{
		"health", "romance/partner", "friends & family", "career/work", "finances", "personal growth",
		"fun/recreation", "environment", "contribution",
	}, labels2)

	keysOf2, ok := WheelPillarKeysOf(2)
	require.True(t, ok)
	assert.Equal(t, keys2, keysOf2)

	// Labels are stable per key: the all-sets lookup agrees with every set's row.
	for _, set := range []int{1, 2} {
		pillars, _ := WheelPillarsOf(set)
		for _, p := range pillars {
			got, found := WheelPillarLabel(p.Key)
			require.Truef(t, found, "%s is a known key", p.Key)
			assert.Equalf(t, p.Label, got, "set %d label for %s", set, p.Key)
		}
	}
	label, ok := WheelPillarLabel(PillarRomance)
	require.True(t, ok)
	assert.Equal(t, "romance/partner", label)
	label, ok = WheelPillarLabel(PillarFriends)
	require.True(t, ok)
	assert.Equal(t, "friends & family", label)
	label, ok = WheelPillarLabel(PillarRelationships)
	require.True(t, ok, "the retired set-1 key keeps its label so set-1 months read as recorded")
	assert.Equal(t, "relationships", label)
	_, ok = WheelPillarLabel("partner")
	assert.False(t, ok)

	// Membership is per set, by exact key.
	assert.True(t, IsWheelPillarIn(1, PillarRelationships))
	assert.False(t, IsWheelPillarIn(1, PillarRomance))
	assert.False(t, IsWheelPillarIn(1, PillarFriends))
	assert.True(t, IsWheelPillarIn(2, PillarRomance))
	assert.True(t, IsWheelPillarIn(2, PillarFriends))
	assert.False(t, IsWheelPillarIn(2, PillarRelationships), "relationships is retired in set 2, never repurposed")
	assert.False(t, IsWheelPillarIn(3, PillarHealth), "an unknown set names no pillar")

	// Unknown sets are unknown.
	for _, set := range []int{-1, 0, 3} {
		_, known := WheelPillarsOf(set)
		assert.Falsef(t, known, "pillar set %d is not known", set)
		_, known = WheelPillarKeysOf(set)
		assert.Falsef(t, known, "pillar set %d is not known", set)
		assert.Falsef(t, KnownWheelPillarSet(set), "pillar set %d", set)
	}
	assert.True(t, KnownWheelPillarSet(1))
	assert.True(t, KnownWheelPillarSet(2))

	// Each call returns a fresh slice.
	first, _ := WheelPillarsOf(2)
	first[1].Label = "mutated"
	again, _ := WheelPillarsOf(2)
	assert.Equal(t, "romance/partner", again[1].Label, "each call returns a fresh slice")

	// The current-set accessors read the current set.
	current, ok := WheelPillarsOf(CurrentWheelPillarSet)
	require.True(t, ok, "the current set is a known set")
	assert.Equal(t, current, WheelPillars())
	currentKeys, _ := WheelPillarKeysOf(CurrentWheelPillarSet)
	assert.Equal(t, currentKeys, WheelPillarKeys())
	for _, key := range []string{PillarHealth, PillarRomance, PillarFriends, PillarRelationships, "wealth"} {
		assert.Equalf(t, IsWheelPillarIn(CurrentWheelPillarSet, key), IsWheelPillar(key), "IsWheelPillar(%q) is current-set", key)
	}

	// Set 2's change entry: relationships → romance + friends, relative to the
	// set that is current. At set 1 nothing is retired.
	split, ok := wheelPillarRetirementAt(2, PillarRelationships)
	require.True(t, ok)
	assert.Equal(t, WheelPillarSplit{Set: 2, Retired: PillarRelationships, Into: []string{PillarRomance, PillarFriends}}, split)
	for _, key := range []string{PillarRomance, PillarFriends, PillarHealth, "wealth"} {
		_, ok = wheelPillarRetirementAt(2, key)
		assert.Falsef(t, ok, "%q is not retired in set 2", key)
	}
	_, ok = wheelPillarRetirementAt(1, PillarRelationships)
	assert.False(t, ok, "relationships is a live pillar of set 1")
	_, ok = wheelPillarRetirementAt(3, PillarRelationships)
	assert.False(t, ok, "an unknown current set retires nothing")

	// The change entry agrees with the set rows it connects.
	retired, added := WheelPillarSetChange(1, 2)
	assert.Equal(t, []string{split.Retired}, retired)
	assert.Equal(t, split.Into, added)

	// The exported wrappers are the same lookup at the current set.
	gotSplit, gotOK := WheelPillarRetirement(PillarRelationships)
	wantSplit, wantOK := wheelPillarRetirementAt(CurrentWheelPillarSet, PillarRelationships)
	assert.Equal(t, wantOK, gotOK)
	assert.Equal(t, wantSplit, gotSplit)
	into, gotOK := RetiredWheelPillar(PillarRelationships)
	assert.Equal(t, wantOK, gotOK)
	if wantOK {
		assert.Equal(t, wantSplit.Into, into)
	} else {
		assert.Nil(t, into)
	}
}

// TestWheelSnapshot_EffectivePillarSet: an absent pillar_set reads as set 1, so
// every stored snapshot reads unchanged with no migration; a recorded set reads
// as itself; and an unset set marshals no pillar_set key, while a set one sits
// after type (wheel.md §4).
func TestWheelSnapshot_EffectivePillarSet(t *testing.T) {
	assert.Equal(t, 1, WheelSnapshot{}.EffectivePillarSet(), "absent reads as set 1")
	assert.Equal(t, 1, WheelSnapshot{PillarSet: 1}.EffectivePillarSet())
	assert.Equal(t, 2, WheelSnapshot{PillarSet: 2}.EffectivePillarSet())

	var stored WheelSnapshot
	require.NoError(t, json.Unmarshal([]byte(
		`{"id":"wheel_2026_08_001","at":"2026-08-30T10:00:00-04:00","type":"snapshot","pillars":{},"vision_reviewed":false}`,
	), &stored))
	assert.Equal(t, 0, stored.PillarSet)
	assert.Equal(t, 1, stored.EffectivePillarSet(), "a schema-1 snapshot with no pillar_set is set 1")

	var set2 WheelSnapshot
	require.NoError(t, json.Unmarshal([]byte(
		`{"id":"wheel_2026_09_001","at":"2026-09-06T17:12:40-04:00","type":"snapshot","pillar_set":2,"pillars":{},"vision_reviewed":false}`,
	), &set2))
	assert.Equal(t, 2, set2.EffectivePillarSet())

	b, err := json.Marshal(WheelSnapshot{ID: "wheel_2026_08_001", Type: WheelEventSnapshot, Pillars: fullWheelSet1()})
	require.NoError(t, err)
	assert.NotContains(t, string(b), "pillar_set", "an unset pillar set is omitted, so a set-1 write reads as before")

	b, err = json.Marshal(WheelSnapshot{ID: "wheel_2026_09_001", Type: WheelEventSnapshot, PillarSet: 2, Pillars: fullWheelSet2()})
	require.NoError(t, err)
	out := string(b)
	assert.Contains(t, out, `"pillar_set":2`)
	typeAt := strings.Index(out, `"type"`)
	setAt := strings.Index(out, `"pillar_set"`)
	pillarsAt := strings.Index(out, `"pillars"`)
	assert.Less(t, typeAt, setAt, "pillar_set follows type")
	assert.Less(t, setAt, pillarsAt, "pillar_set precedes pillars")
}

// TestWheelSnapshot_ValidateOwnSet: a snapshot is validated against its OWN
// pillar set — eight for set 1, nine for set 2 — so the retired key is unknown
// in set 2, a set-2 pillar is unknown in set 1, a missing set-2 pillar is
// refused by name, the range check covers the set's own pillars, and a set
// this build does not know is refused (wheel.md §3, §4).
func TestWheelSnapshot_ValidateOwnSet(t *testing.T) {
	require.NoError(t, WheelSnapshot{Pillars: fullWheelSet1()}.Validate(), "an absent set validates on the eight")
	require.NoError(t, WheelSnapshot{PillarSet: 1, Pillars: fullWheelSet1()}.Validate(), "set 1 validates on the eight")
	require.NoError(t, WheelSnapshot{PillarSet: 2, Pillars: fullWheelSet2()}.Validate(), "set 2 validates on the nine")

	// The eight are not a valid set-2 wheel, and the nine are not a valid set-1 wheel.
	err := WheelSnapshot{PillarSet: 2, Pillars: fullWheelSet1()}.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown pillar(s) relationships")
	err = WheelSnapshot{Pillars: fullWheelSet2()}.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown pillar(s) friends, romance")

	// The retired key beside the nine is still unknown in set 2.
	withRetired := fullWheelSet2()
	withRetired[PillarRelationships] = PillarScore{Score: 6}
	err = WheelSnapshot{PillarSet: 2, Pillars: withRetired}.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown pillar(s) relationships")
	assert.Equal(t, []string{PillarRelationships}, UnknownWheelPillarsIn(2, withRetired))
	assert.Empty(t, UnknownWheelPillarsIn(1, fullWheelSet1()))

	// A missing set-2 pillar is refused by name, in canonical order.
	noFriends := fullWheelSet2()
	delete(noFriends, PillarFriends)
	err = WheelSnapshot{PillarSet: 2, Pillars: noFriends}.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing rating(s) for friends")
	noBoth := fullWheelSet2()
	delete(noBoth, PillarFriends)
	delete(noBoth, PillarRomance)
	assert.Equal(t, []string{PillarRomance, PillarFriends}, MissingWheelPillarsIn(2, noBoth), "canonical order")
	assert.Empty(t, MissingWheelPillarsIn(2, fullWheelSet2()))

	// The range checks cover the set's own pillars.
	offRomance := fullWheelSet2()
	offRomance[PillarRomance] = PillarScore{Score: 0}
	err = WheelSnapshot{PillarSet: 2, Pillars: offRomance}.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rating for romance")
	offFriendsSuggested := fullWheelSet2()
	offFriendsSuggested[PillarFriends] = PillarScore{Score: 7, Suggested: intPtr(11)}
	err = WheelSnapshot{PillarSet: 2, Pillars: offFriendsSuggested}.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "suggestion for friends")

	// A set this build does not know is refused, never read in the wrong set.
	for _, set := range []int{3, -1} {
		err = WheelSnapshot{PillarSet: set, Pillars: fullWheelSet2()}.Validate()
		require.Errorf(t, err, "pillar set %d", set)
		assert.Contains(t, err.Error(), fmt.Sprintf("pillar set %d is not known to this build", set))
	}
	assert.Empty(t, MissingWheelPillarsIn(3, fullWheelSet2()), "an unknown set names no pillar to miss")
	assert.Len(t, UnknownWheelPillarsIn(3, fullWheelSet2()), 9, "an unknown set knows none of the keys")
}

// TestWheelPillarSetChange: the change between two pillar sets is the keys the
// earlier one loses and the keys the later one gains, each in its own set's
// canonical order; equal sets, and a set this build does not know, change
// nothing (wheel.md §7.2).
func TestWheelPillarSetChange(t *testing.T) {
	retired, added := WheelPillarSetChange(1, 2)
	assert.Equal(t, []string{PillarRelationships}, retired)
	assert.Equal(t, []string{PillarRomance, PillarFriends}, added)

	retired, added = WheelPillarSetChange(2, 1)
	assert.Equal(t, []string{PillarRomance, PillarFriends}, retired, "the reverse")
	assert.Equal(t, []string{PillarRelationships}, added)

	for _, pair := range [][2]int{{1, 1}, {2, 2}, {1, 3}, {3, 2}, {0, 2}} {
		retired, added = WheelPillarSetChange(pair[0], pair[1])
		assert.Emptyf(t, retired, "%d→%d", pair[0], pair[1])
		assert.Emptyf(t, added, "%d→%d", pair[0], pair[1])
	}
}
