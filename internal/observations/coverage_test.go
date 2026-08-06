package observations

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConfig_MarshalError surfaces a marshal failure rather than writing a
// corrupt config — an unserializable value in agent_slice_optins makes
// encoding/json fail, and Marshal returns the wrapped error.
func TestConfig_MarshalError(t *testing.T) {
	c := Config{
		Version:          ConfigVersion,
		KeySalt:          "x",
		AgentSliceOptins: map[string]any{"bad": make(chan int)}, // channels are not JSON
	}
	_, err := c.Marshal()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "marshal config")
}

// TestCuriosityContext_conditionHoldsUnknownID locks the default arm: an id
// outside the closed template table never triggers, so a stray id can't fire
// a question.
func TestCuriosityContext_conditionHoldsUnknownID(t *testing.T) {
	ctx := CuriosityContext{Day: "2026-07-02", HasLocation: false, PainWithoutSite: true}
	assert.False(t, ctx.conditionHolds("no-such-template"))
}

// TestCuriositySuppressThrough_BadDayFailsOpen: an unparseable day yields an
// empty suppression window (fail open — curiosity is skippable by design), so
// a bad clock never wedges a template into permanent suppression.
func TestCuriositySuppressThrough_BadDayFailsOpen(t *testing.T) {
	assert.Empty(t, curiositySuppressThrough("not-a-date"))
	assert.Equal(t, "2026-07-09", curiositySuppressThrough("2026-07-02"))
}

// TestEventLine_EmptyPayload renders an event with no payload as bare kind and
// id — the body-less branch of the inventory line.
func TestEventLine_EmptyPayload(t *testing.T) {
	e := Event{ID: "obs_2026_07_02_001", Kind: KindElimination}
	assert.Equal(t, "elimination (obs_2026_07_02_001)", eventLine(e, false))
	// A spanning body-less event still carries its marker.
	assert.Equal(t, "elimination (spanning) (obs_2026_07_02_001)", eventLine(e, true))
}

// TestEndLogicalDate_NilLocDefaultsUTC covers the nil-location guard: a nil
// zone resolves through UTC rather than panicking.
func TestEndLogicalDate_NilLocDefaultsUTC(t *testing.T) {
	d, ok := endLogicalDate("2026-07-02T07:10:00Z", nil)
	require.True(t, ok)
	assert.Equal(t, "2026-07-02", DateString(d))
}

// TestAsOfPlaceRef_SkipsNonLocationAndEmptyRef confirms the resolver ignores a
// non-location event and a location whose place_ref is missing or the wrong
// type, so only a real place on or before the target can win.
func TestAsOfPlaceRef_SkipsNonLocationAndEmptyRef(t *testing.T) {
	events := []Event{
		// A non-location event on the target day is skipped (kind guard).
		{ID: "obs_2026_07_02_001", Kind: KindPain, LogicalDate: "2026-07-02", Payload: map[string]any{"place_ref": "place_wrong"}},
		// A location whose place_ref is a non-string is skipped.
		{ID: "obs_2026_07_02_002", Kind: KindLocation, LogicalDate: "2026-07-02", Payload: map[string]any{"place_ref": 42}},
		// A location whose place_ref is the empty string is skipped.
		{ID: "obs_2026_07_02_003", Kind: KindLocation, LogicalDate: "2026-07-02", Payload: map[string]any{"place_ref": ""}},
		// The only usable location, from an earlier day.
		locationEvent("obs_2026_07_01_001", "2026-07-01", "place_real"),
	}
	ref, ok := AsOfPlaceRef(events, "2026-07-02")
	require.True(t, ok)
	assert.Equal(t, "place_real", ref)
}

// TestMarshalLine_Error surfaces a marshal failure rather than emitting a
// half-written JSONL line — an unserializable payload value makes json.Marshal
// fail and MarshalLine returns the wrapped error.
func TestMarshalLine_Error(t *testing.T) {
	e := Event{
		ID: "obs_2026_07_02_001", Schema: Schema, Kind: KindPain,
		RecordedAt: "x", OccurredAt: "x", OccurredAtPrecision: PrecisionExact,
		LogicalDate: "2026-07-02", Source: SourceMicrolog,
		Payload: map[string]any{"bad": make(chan int)}, // channels are not JSON
	}
	_, err := e.MarshalLine()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "marshal event")
}

// TestIsIntakeClass pins the closed intake-class vocabulary — the three preset
// classes resolve, anything else does not.
func TestIsIntakeClass(t *testing.T) {
	for _, ok := range []string{"food", "liquid", "supplement"} {
		assert.Truef(t, isIntakeClass(ok), "%q is an intake class", ok)
	}
	for _, no := range []string{"", "solid", "snack", "pain"} {
		assert.Falsef(t, isIntakeClass(no), "%q is not an intake class", no)
	}
}

// TestParseScaleKind_OptionalNonNumericHeadRunsTail exercises the optional
// scale with a non-numeric head and a tail hook: no value is recorded and the
// tail receives the full token run verbatim (the note-forwarding branch).
func TestParseScaleKind_OptionalNonNumericHeadRunsTail(t *testing.T) {
	res := &ParseResult{Payload: map[string]any{}}
	var gotTail []string
	tail := func(_ *ParseResult, tokens []string) { gotTail = tokens }

	parseScaleKind(res, []string{"groggy", "morning"}, ParseInput{SpelledOK: false}, "severity", 0, 10, false, tail)

	assert.NotContains(t, res.Payload, "severity", "a non-numeric head records no value")
	assert.Equal(t, []string{"groggy", "morning"}, gotTail, "the tail hook receives the whole run")
}

// TestParseBodyState_KeywordWithNonNumericFollower: a soreness/pain keyword
// followed by a non-numeric word is treated as ordinary note text (present but
// not ok), so a note like "pain achy" never trips the partial path.
func TestParseBodyState_KeywordWithNonNumericFollower(t *testing.T) {
	res := parse(KindBodyState, "", "knee", "pain", "achy")
	assert.False(t, res.Partial, "a keyword with no numeric follower stays a valid note")
	assert.Equal(t, "knee", res.Payload["body_part"])
	assert.NotContains(t, res.Payload, "pain", "no scale is recorded")
	assert.Equal(t, "pain achy", res.Payload["note"])
}

// TestParseWorkout_SpacedRPENonNumericIsPartial: the spaced `rpe <value>` form
// with a non-numeric value is present-but-not-ok, so the workout takes the
// partial path rather than clamping (error-states W-4).
func TestParseWorkout_SpacedRPENonNumericIsPartial(t *testing.T) {
	res := parse(KindWorkout, "", "run", "rpe", "abc")
	assert.True(t, res.Partial)
	assert.Equal(t, KindWorkout, res.Kind)
	assert.Equal(t, ParseMarkerPartial, res.Payload["parse"])
}

// TestResolveBackdate_ZeroNowFallsBackToWallClock covers the zero-now guard:
// with no injected clock the permissive tier substitutes the wall clock and an
// empty arg resolves to now at exact precision (never an error).
func TestResolveBackdate_ZeroNowFallsBackToWallClock(t *testing.T) {
	occ, prec, end := ResolveBackdate("", time.Time{})
	assert.Equal(t, PrecisionExact, prec)
	assert.Nil(t, end)
	assert.False(t, occ.IsZero(), "the wall-clock fallback yields a real moment")
}

// TestResolveRegistryKey_DeriveError propagates the derivation error rather
// than returning a bogus key: an unknown kind fails before any collision check.
func TestResolveRegistryKey_DeriveError(t *testing.T) {
	_, err := ResolveRegistryKey("nonsense", "x", "s", wordlist(t), nil)
	require.Error(t, err)
}

// TestCloneAnyMap_Nil returns a fresh empty map for a nil input so a patched
// record never carries a nil fields map.
func TestCloneAnyMap_Nil(t *testing.T) {
	got := cloneAnyMap(nil)
	require.NotNil(t, got)
	assert.Empty(t, got)
}
