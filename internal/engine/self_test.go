package engine

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixedDay is the civil day every minting test in this file mints against, so
// the expected ids read literally rather than being recomputed from the clock.
func fixedDay(t *testing.T) time.Time {
	t.Helper()
	return time.Date(2026, time.August, 5, 9, 12, 0, 0, time.UTC)
}

// TestNextSelfFactID_MintsSequentialSlots proves the per-day slot grammar is
// the shared one: the first id of a day is _a, the second _b, matching the
// insight and anchor stores.
func TestNextSelfFactID_MintsSequentialSlots(t *testing.T) {
	day := fixedDay(t)
	log := SelfLog{Version: SelfVersion}

	first := NextSelfFactID(log, day)
	assert.Equal(t, "self_2026_08_05_a", first)

	log.History = append(log.History, SelfFact{ID: first, Key: "identity.generation", Value: "elder millennial"})
	assert.Equal(t, "self_2026_08_05_b", NextSelfFactID(log, day))
}

// TestNextSelfFactID_SkipsSlotsHeldByRetiredFacts is the reason minting scans
// the whole history rather than the folded set: reissuing a retired fact's slot
// would give a live fact and a retired one the same identity, and they would
// fold into each other.
func TestNextSelfFactID_SkipsSlotsHeldByRetiredFacts(t *testing.T) {
	day := fixedDay(t)
	log := SelfLog{Version: SelfVersion, History: []SelfFact{
		{ID: "self_2026_08_05_a", Key: "pref.editor", Value: "vim", RecordedAt: "2026-08-05T09:12:00Z"},
		{
			ID: "self_2026_08_05_a", Key: "pref.editor", Value: "vim",
			RecordedAt: "2026-08-06T09:12:00Z", State: SelfStateRetired,
		},
	}}

	assert.Equal(t, "self_2026_08_05_b", NextSelfFactID(log, day),
		"a slot held by a retired fact must never be reissued")
}

// TestNextSelfFactID_IsPerDay confirms the slot counter resets with the date:
// yesterday's slots never constrain today's.
func TestNextSelfFactID_IsPerDay(t *testing.T) {
	log := SelfLog{Version: SelfVersion, History: []SelfFact{
		{ID: "self_2026_08_05_a", Key: "identity.name", Value: "sample"},
		{ID: "self_2026_08_05_b", Key: "body.handedness", Value: "left"},
	}}

	next := time.Date(2026, time.August, 6, 7, 0, 0, 0, time.UTC)
	assert.Equal(t, "self_2026_08_06_a", NextSelfFactID(log, next))
}

// TestSelfFact_IsRetired covers the state discriminator: absent means active.
func TestSelfFact_IsRetired(t *testing.T) {
	assert.False(t, SelfFact{Key: "body.handedness"}.IsRetired())
	assert.True(t, SelfFact{Key: "body.handedness", State: SelfStateRetired}.IsRetired())
}

// TestSelfFactRecord_ProjectionRoundTrips pins the published JSON contract:
// every field maps to the documented name, an active record omits since/note/
// state, and a retired one carries state. The id is never omitted — this store
// has no id-less record, so an empty id in the surface would mean corruption
// rather than a legacy anchor's synthetic identity.
func TestSelfFactRecord_ProjectionRoundTrips(t *testing.T) {
	fact := SelfFact{
		ID:         "self_2026_08_05_c",
		Key:        "pref.editor",
		Value:      "vim",
		Since:      "2014-09",
		Note:       "switched years ago",
		RecordedAt: "2026-08-05T09:15:00-04:00",
		State:      SelfStateRetired,
	}

	raw, err := json.Marshal(ProjectSelfFact(fact))
	require.NoError(t, err)

	var back SelfFactRecord
	require.NoError(t, json.Unmarshal(raw, &back))
	assert.Equal(t, ProjectSelfFact(fact), back)

	for _, want := range []string{
		`"id":"self_2026_08_05_c"`,
		`"key":"pref.editor"`,
		`"value":"vim"`,
		`"since":"2014-09"`,
		`"note":"switched years ago"`,
		`"recorded_at":"2026-08-05T09:15:00-04:00"`,
		`"state":"retired"`,
	} {
		assert.Contains(t, string(raw), want)
	}

	plain, err := json.Marshal(ProjectSelfFact(SelfFact{
		ID: "self_2026_08_05_a", Key: "identity.generation",
		Value: "elder millennial", RecordedAt: "2026-08-05T09:12:00-04:00",
	}))
	require.NoError(t, err)
	assert.Contains(t, string(plain), `"id":"self_2026_08_05_a"`)
	assert.NotContains(t, string(plain), `"since"`)
	assert.NotContains(t, string(plain), `"note"`)
	assert.NotContains(t, string(plain), `"state"`)
}

// TestSelfFactHistory_SpansAMove is the identity model's payoff in test form:
// a fact re-categorized under a new key keeps every record it ever had,
// including the ones written under its previous key. Resolving history by key
// instead of by id would silently drop exactly those.
func TestSelfFactHistory_SpansAMove(t *testing.T) {
	log := SelfLog{Version: SelfVersion, History: []SelfFact{
		{ID: "self_2026_08_05_b", Key: "misc.handedness", Value: "left", RecordedAt: "2026-08-05T09:13:00Z"},
		{ID: "self_2026_08_05_a", Key: "identity.name", Value: "sample", RecordedAt: "2026-08-05T09:12:00Z"},
		{ID: "self_2026_08_05_b", Key: "body.handedness", Value: "left", RecordedAt: "2026-08-06T18:40:00Z"},
		{ID: "self_2026_08_05_b", Key: "body.handedness", Value: "ambidextrous", RecordedAt: "2026-09-01T07:55:00Z"},
	}}

	history := SelfFactHistory(log, "self_2026_08_05_b")
	require.Len(t, history, 3, "the records written under the previous key are still this fact's history")
	assert.Equal(t, "misc.handedness", history[0].Key, "append order is preserved")
	assert.Equal(t, "body.handedness", history[1].Key)
	assert.Equal(t, "ambidextrous", history[2].Value)

	assert.Empty(t, SelfFactHistory(log, "self_2026_08_05_z"))
	assert.NotNil(t, SelfFactHistory(log, "self_2026_08_05_z"), "empty, never nil")
}

// TestSelfNamespaces_PriorityOrderAndCopy pins the taxonomy and its order, and
// proves the accessor hands back a copy rather than shared state.
func TestSelfNamespaces_PriorityOrderAndCopy(t *testing.T) {
	assert.Equal(t, []string{"identity", "constraint", "body", "pref", "misc"}, SelfNamespaces())

	mutated := SelfNamespaces()
	mutated[0] = "clobbered"
	assert.Equal(t, "identity", SelfNamespaces()[0], "the caller cannot mutate the taxonomy")

	assert.Equal(t, 0, SelfNamespacePriority("identity"))
	assert.Equal(t, 1, SelfNamespacePriority("constraint"))
	assert.Equal(t, 4, SelfNamespacePriority("misc"))
	assert.Equal(t, len(SelfNamespaces()), SelfNamespacePriority("health"),
		"an unrecognized namespace ranks last rather than landing arbitrarily")
}

// TestValidateSelfKey_Taxonomy covers the namespace boundary: every accepted
// namespace passes with a free-form leaf (dots included), and every malformed
// shape is refused before any disk touch.
func TestValidateSelfKey_Taxonomy(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		wantErr bool
	}{
		{"identity", "identity.generation", false},
		{"constraint", "constraint.diet.dairy", false},
		{"body", "body.handedness", false},
		{"pref", "pref.editor", false},
		{"misc catch-all", "misc.trivia", false},
		{"digits and separators in the leaf", "body.blood_type-2", false},
		{"unknown namespace", "health.blood_type", true},
		{"no dot", "handedness", true},
		{"empty leaf", "body.", true},
		{"empty key", "", true},
		{"whitespace key", "   ", true},
		{"leading whitespace", " identity.name", true},
		{"whitespace in the leaf", "body.blood type", true},
		{"uppercase namespace", "Identity.name", true},
		{"uppercase leaf", "identity.Name", true},
		{"namespace only", "identity", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSelfKey(tc.key)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

// TestValidateSelfKey_UnknownNamespaceNamesTheLandingSpot is the catch-all
// promise: a rejection lists every accepted namespace *and* points at misc., so
// no fact is ever turned away — it only ever needs a named home. The namespace
// is never auto-coerced, because silently filing a typo would hide it forever.
func TestValidateSelfKey_UnknownNamespaceNamesTheLandingSpot(t *testing.T) {
	err := ValidateSelfKey("health.blood_type")
	require.Error(t, err)

	msg := err.Error()
	for _, ns := range SelfNamespaces() {
		assert.Contains(t, msg, ns+".", "the rejection names every accepted namespace")
	}
	assert.Contains(t, msg, "misc.", "the rejection always names the always-available landing spot")
	assert.Contains(t, msg, "health", "the rejection names what was actually typed")

	noNamespace := ValidateSelfKey("handedness")
	require.Error(t, noNamespace, "a key with no namespace is refused too")
	assert.Contains(t, noNamespace.Error(), "misc.")
}

// TestValidateSelfFact_Value covers the value guard: non-empty, and within the
// grounding-cost ceiling.
func TestValidateSelfFact_Value(t *testing.T) {
	require.NoError(t, ValidateSelfFact("identity.generation", "elder millennial"))

	require.Error(t, ValidateSelfFact("identity.generation", ""))
	require.Error(t, ValidateSelfFact("identity.generation", "   "))
	require.Error(t, ValidateSelfFact("nope.generation", "elder millennial"), "the key is validated too")

	require.NoError(t, ValidateSelfFact("misc.long", strings.Repeat("x", SelfValueMax)))

	err := ValidateSelfFact("misc.long", strings.Repeat("x", SelfValueMax+1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "limit")
}

// TestValidateSelfFact_ValueCeilingCountsRunes proves the ceiling is measured
// in runes, not bytes: a multi-byte value is not refused for being multi-byte.
func TestValidateSelfFact_ValueCeilingCountsRunes(t *testing.T) {
	value := strings.Repeat("é", SelfValueMax)
	require.Greater(t, len(value), SelfValueMax, "the fixture is genuinely multi-byte")
	require.NoError(t, ValidateSelfFact("misc.accented", value))
}

// TestLatestSelfFacts_CorrectionIsDecidedByAppendOrder proves the fold is per
// id and by append order, not by RecordedAt: a correction whose timestamp is
// *earlier* than the record it supersedes still wins.
func TestLatestSelfFacts_CorrectionIsDecidedByAppendOrder(t *testing.T) {
	log := SelfLog{Version: SelfVersion, History: []SelfFact{
		{ID: "self_2026_08_05_a", Key: "body.handedness", Value: "left", RecordedAt: "2026-08-05T09:00:00Z"},
		{ID: "self_2026_08_05_a", Key: "body.handedness", Value: "ambidextrous", RecordedAt: "2026-08-01T09:00:00Z"},
	}}

	got := LatestSelfFacts(log)
	require.Len(t, got, 1, "both records share one identity")
	assert.Equal(t, "ambidextrous", got[0].Value, "the later append wins even though its timestamp is earlier")
}

// TestLatestSelfFacts_FoldsPerIDAcrossAMove proves a move does not fork the
// fact: two keys, one identity, one folded record showing only the new key.
func TestLatestSelfFacts_FoldsPerIDAcrossAMove(t *testing.T) {
	log := SelfLog{Version: SelfVersion, History: []SelfFact{
		{ID: "self_2026_08_05_b", Key: "misc.handedness", Value: "left", RecordedAt: "2026-08-05T09:13:00Z"},
		{ID: "self_2026_08_05_b", Key: "body.handedness", Value: "left", RecordedAt: "2026-08-06T18:40:00Z"},
	}}

	got := LatestSelfFacts(log)
	require.Len(t, got, 1)
	assert.Equal(t, "body.handedness", got[0].Key)
	assert.Equal(t, "self_2026_08_05_b", got[0].ID)
}

// TestLatestSelfFacts_SortedByNamespaceThenKey pins the total, stable order the
// grouped render reads, and confirms an empty log yields an empty slice.
func TestLatestSelfFacts_SortedByNamespaceThenKey(t *testing.T) {
	log := SelfLog{Version: SelfVersion, History: []SelfFact{
		{ID: "d", Key: "pref.editor", Value: "vim", RecordedAt: "2026-08-05T09:00:00Z"},
		{ID: "e", Key: "misc.trivia", Value: "sample", RecordedAt: "2026-08-05T09:01:00Z"},
		{ID: "c", Key: "body.handedness", Value: "left", RecordedAt: "2026-08-05T09:02:00Z"},
		{ID: "b", Key: "constraint.diet.dairy", Value: "none", RecordedAt: "2026-08-05T09:03:00Z"},
		{ID: "a", Key: "identity.generation", Value: "elder millennial", RecordedAt: "2026-08-05T09:04:00Z"},
	}}

	got := LatestSelfFacts(log)
	keys := make([]string, 0, len(got))
	for _, f := range got {
		keys = append(keys, f.Key)
	}
	assert.Equal(t, []string{
		"identity.generation", "constraint.diet.dairy", "body.handedness", "pref.editor", "misc.trivia",
	}, keys)

	assert.Empty(t, LatestSelfFacts(SelfLog{Version: SelfVersion}))
}

// TestFindActiveSelfFact_OneActivePerKey covers the invariant every verb leans
// on: a key addresses at most one live fact, and a retired record leaves it
// free rather than shadowing it.
func TestFindActiveSelfFact_OneActivePerKey(t *testing.T) {
	latest := LatestSelfFacts(SelfLog{Version: SelfVersion, History: []SelfFact{
		{ID: "self_2026_08_05_a", Key: "pref.editor", Value: "vim", RecordedAt: "2026-08-05T09:00:00Z"},
		{
			ID: "self_2026_08_05_a", Key: "pref.editor", Value: "vim",
			RecordedAt: "2026-08-06T09:00:00Z", State: SelfStateRetired,
		},
		{ID: "self_2026_08_07_a", Key: "pref.editor", Value: "helix", RecordedAt: "2026-08-07T09:00:00Z"},
	}})

	found, ok := FindActiveSelfFact(latest, "pref.editor")
	require.True(t, ok)
	assert.Equal(t, "self_2026_08_07_a", found.ID, "the retired record does not shadow the live one")
	assert.Equal(t, "helix", found.Value)

	_, ok = FindActiveSelfFact(latest, "pref.shell")
	assert.False(t, ok)

	retired, ok := LatestRetiredSelfFactFor(latest, "pref.editor")
	require.True(t, ok)
	assert.Equal(t, "self_2026_08_05_a", retired.ID)
	assert.Equal(t, "2026-08-06", RetiredDate(retired))

	_, ok = LatestRetiredSelfFactFor(latest, "pref.shell")
	assert.False(t, ok)
}

// TestSelfFactKeys_ActiveSortedAndEmptyNotNil covers the remedy that travels
// with an unrecorded-key rejection: active keys only, sorted, deduped, and an
// empty slice rather than nil so a caller can say "nothing recorded" instead of
// printing a dangling list.
func TestSelfFactKeys_ActiveSortedAndEmptyNotNil(t *testing.T) {
	latest := LatestSelfFacts(SelfLog{Version: SelfVersion, History: []SelfFact{
		{ID: "a", Key: "pref.editor", Value: "vim", RecordedAt: "2026-08-05T09:00:00Z"},
		{ID: "b", Key: "identity.generation", Value: "elder millennial", RecordedAt: "2026-08-05T09:01:00Z"},
		{
			ID: "c", Key: "misc.trivia", Value: "sample",
			RecordedAt: "2026-08-05T09:02:00Z", State: SelfStateRetired,
		},
	}})

	assert.Equal(t, []string{"identity.generation", "pref.editor"}, SelfFactKeys(latest),
		"retired keys are not listed — they cannot be retired or moved")

	empty := SelfFactKeys(nil)
	assert.Empty(t, empty)
	assert.NotNil(t, empty, "empty, never nil")
}

// TestActiveSelfFacts_ExcludesRetired covers the profile filter.
func TestActiveSelfFacts_ExcludesRetired(t *testing.T) {
	latest := []SelfFact{
		{ID: "a", Key: "identity.generation", Value: "elder millennial"},
		{ID: "b", Key: "misc.trivia", Value: "sample", State: SelfStateRetired},
	}

	active := ActiveSelfFacts(latest)
	require.Len(t, active, 1)
	assert.Equal(t, "identity.generation", active[0].Key)
	assert.NotNil(t, ActiveSelfFacts(nil), "empty, never nil")
}

// TestLatestSelfFacts_TotalOrderOnSharedKeysAndCorruptKeys covers all three
// tiers of the ordering: keys inside one namespace sort alphabetically; two
// facts that share a key (a retired one and its live replacement) break the tie
// on id rather than on map iteration; and a hand-corrupted key with no
// namespace at all still renders — it ranks last instead of panicking or
// landing arbitrarily. Validation refuses such a key on the write path, so it
// can only reach a read surface from a hand-edited file, and a read surface
// must not be the thing that breaks.
func TestLatestSelfFacts_TotalOrderOnSharedKeysAndCorruptKeys(t *testing.T) {
	log := SelfLog{Version: SelfVersion, History: []SelfFact{
		{ID: "corrupt", Key: "handedness", Value: "left", RecordedAt: "2026-08-05T09:00:00Z"},
		{ID: "self_2026_08_05_c", Key: "pref.shell", Value: "zsh", RecordedAt: "2026-08-05T09:01:00Z"},
		{
			ID: "self_2026_08_05_b", Key: "pref.editor", Value: "vim",
			RecordedAt: "2026-08-06T09:00:00Z", State: SelfStateRetired,
		},
		{ID: "self_2026_08_05_a", Key: "pref.editor", Value: "helix", RecordedAt: "2026-08-07T09:00:00Z"},
	}}

	got := LatestSelfFacts(log)
	require.Len(t, got, 4)
	assert.Equal(t, "self_2026_08_05_a", got[0].ID, "facts sharing a key break the tie on id")
	assert.Equal(t, "self_2026_08_05_b", got[1].ID)
	assert.Equal(t, "pref.shell", got[2].Key, "inside one namespace, keys sort alphabetically")
	assert.Equal(t, "handedness", got[3].Key, "a key with no namespace ranks last, and still renders")
}

// TestRetiredDate_DegradesOnAShortTimestamp pins the read-path degradation: a
// hand-corrupted RecordedAt too short to carry a civil date prints what is
// there rather than slicing out of range.
func TestRetiredDate_DegradesOnAShortTimestamp(t *testing.T) {
	assert.Equal(t, "2026-08-06", RetiredDate(SelfFact{RecordedAt: "2026-08-06T09:00:00Z"}))
	assert.Equal(t, "2026-08", RetiredDate(SelfFact{RecordedAt: "2026-08"}))
	assert.Empty(t, RetiredDate(SelfFact{}))
}

// TestGroundingSelfFacts_OrderIsNamespacePriorityNotRecency is the load-bearing
// test of the grounding contract. The fixture is appended in deliberately
// adversarial order — the lowest-priority facts written most recently — so a
// recency sort would produce a visibly different slice. The assertion is that
// the slice is identical either way: an atemporal fact recorded in year one
// must not be evicted by last week's trivia.
func TestGroundingSelfFacts_OrderIsNamespacePriorityNotRecency(t *testing.T) {
	log := SelfLog{Version: SelfVersion, History: []SelfFact{
		{ID: "a", Key: "identity.generation", Value: "elder millennial", RecordedAt: "2020-01-01T09:00:00Z"},
		{ID: "b", Key: "constraint.diet.dairy", Value: "none", RecordedAt: "2021-01-01T09:00:00Z"},
		{ID: "c", Key: "body.handedness", Value: "left", RecordedAt: "2024-01-01T09:00:00Z"},
		{ID: "d", Key: "pref.editor", Value: "vim", RecordedAt: "2026-08-04T09:00:00Z"},
		{ID: "e", Key: "misc.trivia", Value: "sample", RecordedAt: "2026-08-05T09:00:00Z"},
	}}

	latest := LatestSelfFacts(log)
	got := GroundingSelfFacts(latest, 10)

	keys := make([]string, 0, len(got))
	for _, f := range got {
		keys = append(keys, f.Key)
	}
	want := []string{
		"identity.generation", "constraint.diet.dairy", "body.handedness", "pref.editor", "misc.trivia",
	}
	assert.Equal(t, want, keys)

	// The fixture proves the point rather than passing vacuously: sorted by
	// recency the newest record is misc.trivia, so a recency sort would have
	// led with exactly the fact this ordering puts last.
	newest := log.History[len(log.History)-1]
	assert.Equal(t, "misc.trivia", newest.Key)
	assert.NotEqual(t, newest.Key, keys[0], "a recency sort would have produced a different slice")

	// Append order must not matter: rebuild the same profile written backwards
	// and assert the slice is byte-identical.
	reversed := SelfLog{Version: SelfVersion}
	for i := len(log.History) - 1; i >= 0; i-- {
		reversed.History = append(reversed.History, log.History[i])
	}
	assert.Equal(t, got, GroundingSelfFacts(LatestSelfFacts(reversed), 10),
		"the slice is identical regardless of the order the facts were recorded in")
}

// TestGroundingSelfFacts_TruncatesWithoutReordering proves the cap cuts the
// tail of the priority order rather than sampling or resorting, and that a
// non-positive cap yields an empty slice — the Engine core does not depend on a
// caller having validated the config.
func TestGroundingSelfFacts_TruncatesWithoutReordering(t *testing.T) {
	latest := LatestSelfFacts(SelfLog{Version: SelfVersion, History: []SelfFact{
		{ID: "e", Key: "misc.trivia", Value: "sample", RecordedAt: "2026-08-05T09:00:00Z"},
		{ID: "d", Key: "pref.editor", Value: "vim", RecordedAt: "2026-08-05T09:01:00Z"},
		{ID: "b", Key: "constraint.diet.dairy", Value: "none", RecordedAt: "2026-08-05T09:02:00Z"},
		{ID: "a", Key: "identity.generation", Value: "elder millennial", RecordedAt: "2026-08-05T09:03:00Z"},
	}})

	got := GroundingSelfFacts(latest, 2)
	require.Len(t, got, 2)
	assert.Equal(t, "identity.generation", got[0].Key)
	assert.Equal(t, "constraint.diet.dairy", got[1].Key)

	assert.Empty(t, GroundingSelfFacts(latest, 0))
	assert.Empty(t, GroundingSelfFacts(latest, -1))
	assert.NotNil(t, GroundingSelfFacts(latest, 0), "empty, never nil")
}

// TestGroundingSelfFacts_ExcludesRetired confirms a retired fact leaves the
// grounding slice, matching the rendered profile.
func TestGroundingSelfFacts_ExcludesRetired(t *testing.T) {
	latest := LatestSelfFacts(SelfLog{Version: SelfVersion, History: []SelfFact{
		{ID: "a", Key: "identity.generation", Value: "elder millennial", RecordedAt: "2026-08-05T09:00:00Z"},
		{ID: "b", Key: "body.handedness", Value: "left", RecordedAt: "2026-08-05T09:01:00Z"},
		{
			ID: "b", Key: "body.handedness", Value: "left",
			RecordedAt: "2026-08-06T09:01:00Z", State: SelfStateRetired,
		},
	}})

	got := GroundingSelfFacts(latest, 10)
	require.Len(t, got, 1)
	assert.Equal(t, "identity.generation", got[0].Key)
}

// TestGroundingSelfFacts_DoesNotMutateItsInput guards the read path: /ask folds
// once and may slice more than once, so the grounding sort must not reorder the
// caller's folded set underneath it.
func TestGroundingSelfFacts_DoesNotMutateItsInput(t *testing.T) {
	latest := []SelfFact{
		{ID: "e", Key: "misc.trivia", Value: "sample"},
		{ID: "a", Key: "identity.generation", Value: "elder millennial"},
	}
	before := append([]SelfFact(nil), latest...)

	_ = GroundingSelfFacts(latest, 10)
	assert.Equal(t, before, latest)
}
