package engine

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Synthetic anchor ids shared by the event tests.
const (
	evAnchorA = "anchor_2026_01_01_a"
	evAnchorB = "anchor_2026_01_05_a"
)

// anchorEv builds an event of the given type, stamped 21:00 UTC on its day.
func anchorEv(id, anchorID, eventType, day string) AnchorEvent {
	return AnchorEvent{ID: id, AnchorID: anchorID, Type: eventType, Day: day, RecordedAt: day + "T21:00:00Z"}
}

// kindEv builds a kind event carrying the given kind and parameters.
func kindEv(id, anchorID, day, kind string, allowance, window int) AnchorEvent {
	e := anchorEv(id, anchorID, AnchorEventKind, day)
	e.Kind, e.Allowance, e.WindowDays = kind, allowance, window
	return e
}

// voidEv builds a void of target.
func voidEv(id, anchorID, day, target string) AnchorEvent {
	e := anchorEv(id, anchorID, AnchorEventVoid, day)
	e.Target = target
	return e
}

// anchorDef builds a definition record for the given id/label/date.
func anchorDef(id, label, date, recordedAt string) Anchor {
	return Anchor{ID: id, Label: label, Date: date, RecordedAt: recordedAt}
}

// TestFoldAnchorKindLatestNonVoidedWins: the latest kind event in append order
// is the effective kind, with its own parameters and day. A kind event is never
// voidable, so a void aimed at one is ignored and the latest still wins, while
// a void aimed at a slip drops it from the facts.
func TestFoldAnchorKindLatestNonVoidedWins(t *testing.T) {
	anchors := AnchorLog{Version: AnchorVersion, History: []Anchor{
		anchorDef(evAnchorA, "late-night-snacks", "2026-01-01", "2026-01-01T09:00:00Z"),
	}}
	log := AnchorEventLog{Version: AnchorEventsVersion, Events: []AnchorEvent{
		kindEv("anchorev_2026_01_02_a", evAnchorA, "2026-01-02", AnchorKindQuit, 0, 0),
		anchorEv("anchorev_2026_01_20_a", evAnchorA, AnchorEventSlip, "2026-01-20"),
		kindEv("anchorev_2026_02_01_a", evAnchorA, "2026-02-01", AnchorKindLimit, 4, 90),
		voidEv("anchorev_2026_02_02_a", evAnchorA, "2026-02-02", "anchorev_2026_02_01_a"), // illegal: a kind event
		voidEv("anchorev_2026_02_02_b", evAnchorA, "2026-02-02", "anchorev_2026_01_20_a"),
	}}

	f := FoldAnchorEvents(log, anchors)[evAnchorA]

	assert.Equal(t, AnchorKindLimit, f.Kind, "the latest kind event wins")
	assert.Equal(t, 4, f.Allowance)
	assert.Equal(t, 90, f.WindowDays)
	assert.Equal(t, "2026-02-01", f.KindSince, "the day the anchor became a limit")
	assert.Len(t, f.KindHistory, 2, "a void never removes a kind event")
	assert.Empty(t, f.Slips, "the voided slip drops out of the facts")
	assert.Equal(t, map[string]string{"anchorev_2026_01_20_a": "anchorev_2026_02_02_b"}, f.Voided)
	require.Len(t, f.Voids, 1, "only the void that applied is a fact")
	assert.Equal(t, "anchorev_2026_02_02_b", f.Voids[0].ID)
}

// TestFoldAnchorNoKindEventIsMilestone: an anchor no kind event targets is a
// milestone with no parameters — with no event store at all, and when the
// store only holds events for other anchors. Every anchor in anchors.json gets
// an entry, sunset and pre-id anchors included.
func TestFoldAnchorNoKindEventIsMilestone(t *testing.T) {
	anchors := AnchorLog{Version: AnchorVersion, History: []Anchor{
		{Label: "first-gate", Date: "2026-02-14", RecordedAt: "2026-02-14T09:00:00Z"},
		anchorDef(evAnchorA, "sobriety", "2026-01-01", "2026-01-01T09:00:00Z"),
		anchorDef(evAnchorB, "morning-walk", "2026-01-05", "2026-01-05T09:00:00Z"),
		{ID: evAnchorB, Label: "morning-walk", Date: "2026-01-05", RecordedAt: "2026-03-01T09:00:00Z", State: AnchorStateSunset},
	}}

	for name, log := range map[string]AnchorEventLog{
		"no store": {},
		"store for another anchor": {Version: AnchorEventsVersion, Events: []AnchorEvent{
			kindEv("anchorev_2026_02_01_a", evAnchorA, "2026-02-01", AnchorKindQuit, 0, 0),
		}},
	} {
		t.Run(name, func(t *testing.T) {
			facts := FoldAnchorEvents(log, anchors)
			require.Len(t, facts, 3, "every folded anchor gets an entry")
			for _, id := range []string{"legacy:first-gate", evAnchorB} {
				f, ok := facts[id]
				require.True(t, ok, id)
				assert.Equal(t, AnchorKindMilestone, f.Kind, id)
				assert.Zero(t, f.Allowance, id)
				assert.Zero(t, f.WindowDays, id)
				assert.Empty(t, f.KindSince, id)
				assert.Empty(t, f.KindHistory, id)
			}
			assert.True(t, facts[evAnchorB].Anchor.IsSunset(), "a sunset anchor is folded too")
			assert.False(t, facts[evAnchorB].Graduated, "a plain sunset is not a graduation")
			assert.Equal(t, "2026-02-14", facts["legacy:first-gate"].Start)
		})
	}

	assert.Empty(t, FoldAnchorEvents(AnchorEventLog{}, AnchorLog{}), "an empty Ledger folds to nothing")
}

// TestValidateKindParams: a limit needs both a positive allowance and window; a
// habit may carry a positive window only; a milestone or quit takes neither;
// an unknown kind names the four.
func TestValidateKindParams(t *testing.T) {
	cases := []struct {
		name      string
		kind      string
		allowance int
		window    int
		wantErr   string
	}{
		{name: "milestone", kind: AnchorKindMilestone},
		{name: "quit", kind: AnchorKindQuit},
		{name: "limit", kind: AnchorKindLimit, allowance: 4, window: 90},
		{name: "habit default window", kind: AnchorKindHabit},
		{name: "habit window", kind: AnchorKindHabit, window: 14},
		{name: "unknown kind", kind: "streak", wantErr: "the kinds are milestone, quit, limit and habit"},
		{name: "empty kind", kind: "", wantErr: "the kinds are milestone, quit, limit and habit"},
		{name: "limit missing allowance", kind: AnchorKindLimit, window: 90, wantErr: "a limit needs both --allowance N and --window D"},
		{name: "limit missing window", kind: AnchorKindLimit, allowance: 4, wantErr: "a limit needs both --allowance N and --window D"},
		{name: "limit negative allowance", kind: AnchorKindLimit, allowance: -1, window: 90, wantErr: "--allowance is a positive whole number"},
		{name: "limit negative window", kind: AnchorKindLimit, allowance: 4, window: -90, wantErr: "--window is a positive whole number"},
		{name: "allowance on a habit", kind: AnchorKindHabit, allowance: 2, wantErr: "only a limit takes it"},
		{name: "allowance on a quit", kind: AnchorKindQuit, allowance: 2, wantErr: "only a limit takes it"},
		{name: "window on a quit", kind: AnchorKindQuit, window: 30, wantErr: "only a limit or a habit takes it"},
		{name: "window on a milestone", kind: AnchorKindMilestone, window: 30, wantErr: "only a limit or a habit takes it"},
		{name: "negative habit window", kind: AnchorKindHabit, window: -1, wantErr: "--window is a positive whole number"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateKindParams(tc.kind, tc.allowance, tc.window)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// TestKindParamHistoryKept: every change of kind or parameters is a new dated
// kind event, so the whole history is kept in append order while the fold
// reads the latest. A habit with no window takes the 30-day default; one that
// names a window keeps it.
func TestKindParamHistoryKept(t *testing.T) {
	anchors := AnchorLog{Version: AnchorVersion, History: []Anchor{
		anchorDef(evAnchorA, "late-night-snacks", "2026-01-01", "2026-01-01T09:00:00Z"),
	}}
	events := make([]AnchorEvent, 0, 4)
	events = append(events,
		kindEv("anchorev_2026_02_01_a", evAnchorA, "2026-02-01", AnchorKindLimit, 4, 90),
		kindEv("anchorev_2026_03_01_a", evAnchorA, "2026-03-01", AnchorKindLimit, 2, 30),
		kindEv("anchorev_2026_04_01_a", evAnchorA, "2026-04-01", AnchorKindHabit, 0, 0),
	)

	f := FoldAnchorEvents(AnchorEventLog{Version: AnchorEventsVersion, Events: events}, anchors)[evAnchorA]
	assert.Equal(t, events, f.KindHistory, "every parameter change is kept, in append order")
	assert.Equal(t, AnchorKindHabit, f.Kind)
	assert.Zero(t, f.Allowance, "a habit carries no allowance")
	assert.Equal(t, DefaultHabitWindowDays, f.WindowDays, "a habit with no window takes the default")
	assert.Equal(t, "2026-04-01", f.KindSince)

	events = append(events, kindEv("anchorev_2026_05_01_a", evAnchorA, "2026-05-01", AnchorKindHabit, 0, 14))
	f = FoldAnchorEvents(AnchorEventLog{Version: AnchorEventsVersion, Events: events}, anchors)[evAnchorA]
	assert.Len(t, f.KindHistory, 4)
	assert.Equal(t, 14, f.WindowDays, "a habit keeps the window it names")
	assert.Equal(t, "2026-05-01", f.KindSince)
}

// TestNextAnchorEventIDSlotGrammar: event ids share the per-day slot grammar,
// advance past every event and every id a pair intent reserved (so an id is
// never reissued, even for an operation that never completed), ignore other
// days, and never take the anchor_ namespace.
func TestNextAnchorEventIDSlotGrammar(t *testing.T) {
	day := time.Date(2026, time.March, 14, 23, 30, 0, 0, time.UTC)

	assert.Equal(t, "anchorev_2026_03_14_a", NextAnchorEventID(AnchorEventLog{}, day))

	log := AnchorEventLog{
		Events: []AnchorEvent{
			{ID: "anchorev_2026_03_14_a"},
			{ID: "anchorev_2026_03_14_b"},
			{ID: "anchorev_2026_03_13_d"},    // another day: never consumes a slot here
			{ID: "anchor_2026_03_14_c"},      // an anchor id: a different namespace
			{ID: "anchorev_2026_03_14_aaaa"}, // a far slot does not block the next free one
		},
		PairIntents: []AnchorPairIntent{{Event: AnchorEvent{ID: "anchorev_2026_03_14_c"}}},
	}
	id := NextAnchorEventID(log, day)
	assert.Equal(t, "anchorev_2026_03_14_d", id, "a reserved id is never reissued")
	assert.True(t, validSlotID(id, AnchorEventIDPrefix))
	assert.NotRegexp(t, "^"+AnchorIDPrefix, id, "an event id never collides with an anchor id")

	full := AnchorEventLog{}
	for n := range 26 {
		full.Events = append(full.Events, AnchorEvent{ID: "anchorev_2026_03_14_" + SlotLabel(n)})
	}
	assert.Equal(t, "anchorev_2026_03_14_aa", NextAnchorEventID(full, day), "the slot rolls over like an anchor id")
}

// TestAnchorEventJSONRoundTrip: every event type and a pair intent survive a
// marshal/unmarshal round trip with their documented field names; an unused
// pair_intents array is omitted; unknown fields — in an event, an intent or
// the envelope — are tolerated on read.
func TestAnchorEventJSONRoundTrip(t *testing.T) {
	use := anchorEv("anchorev_2026_03_14_a", evAnchorB, AnchorEventUse, "2026-03-14")
	use.Intent, use.Note = AnchorIntentPlanned, "a slice of cake at a birthday"
	confirm := anchorEv("anchorev_2026_03_15_a", evAnchorA, AnchorEventConfirm, "2026-03-14")
	confirm.From = "2026-03-08"
	slip := anchorEv("anchorev_2026_04_02_a", evAnchorA, AnchorEventSlip, "2026-04-01")
	log := AnchorEventLog{
		Version: AnchorEventsVersion,
		Events: []AnchorEvent{
			kindEv("anchorev_2026_03_02_a", evAnchorB, "2026-03-02", AnchorKindLimit, 4, 90),
			use,
			anchorEv("anchorev_2026_03_14_b", evAnchorA, AnchorEventMiss, "2026-03-14"),
			confirm,
			slip,
			voidEv("anchorev_2026_04_03_a", evAnchorA, "2026-04-03", "anchorev_2026_03_14_b"),
			anchorEv("anchorev_2026_05_01_a", evAnchorA, AnchorEventGraduate, "2026-05-01"),
		},
		PairIntents: []AnchorPairIntent{{
			Order:  AnchorPairCompatFirst,
			Event:  slip,
			Compat: anchorDef(evAnchorA, "sobriety", "2026-04-01", slip.RecordedAt),
		}},
	}

	b, err := json.Marshal(log)
	require.NoError(t, err)
	var back AnchorEventLog
	require.NoError(t, json.Unmarshal(b, &back))
	assert.Equal(t, log, back, "every field round-trips")

	got, err := json.Marshal(log.Events[0])
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"anchorev_2026_03_02_a","anchor_id":"anchor_2026_01_05_a","type":"kind",`+
		`"day":"2026-03-02","recorded_at":"2026-03-02T21:00:00Z","kind":"limit","allowance":4,"window_days":90}`, string(got))
	got, err = json.Marshal(use)
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"anchorev_2026_03_14_a","anchor_id":"anchor_2026_01_05_a","type":"use",`+
		`"day":"2026-03-14","recorded_at":"2026-03-14T21:00:00Z","intent":"planned","note":"a slice of cake at a birthday"}`, string(got))
	got, err = json.Marshal(log.PairIntents[0])
	require.NoError(t, err)
	assert.Contains(t, string(got), `"order":"compat_first","event":{`)
	assert.Contains(t, string(got), `"compat":{"id":"anchor_2026_01_01_a"`)

	got, err = json.Marshal(AnchorEventLog{Version: AnchorEventsVersion, Events: []AnchorEvent{slip}})
	require.NoError(t, err)
	assert.NotContains(t, string(got), "pair_intents", "an unused pair_intents array is absent")

	tolerant := `{"version":2,"later":{"x":1},"events":[{"id":"anchorev_2026_04_02_a","anchor_id":"anchor_2026_01_01_a",` +
		`"type":"slip","day":"2026-04-01","recorded_at":"2026-04-01T21:00:00Z","mood":"tired"}],` +
		`"pair_intents":[{"order":"compat_first","reason":"x","event":{"id":"anchorev_2026_04_02_a"},"compat":{"label":"sobriety","extra":true}}]}`
	var read AnchorEventLog
	require.NoError(t, json.Unmarshal([]byte(tolerant), &read), "unknown fields are tolerated")
	assert.Equal(t, 2, read.Version)
	assert.Equal(t, []AnchorEvent{slip}, read.Events)
	require.Len(t, read.PairIntents, 1)
	assert.Equal(t, "sobriety", read.PairIntents[0].Compat.Label)
}

// TestValidateAnchorEvent: the per-record schema the fold skips on and
// `lucid validate` reports — id grammar, a minted anchor id, a known type, a
// civil day, an RFC3339 recorded_at, and each type's own fields.
func TestValidateAnchorEvent(t *testing.T) {
	good := func(eventType string) AnchorEvent {
		return anchorEv("anchorev_2026_03_14_a", evAnchorA, eventType, "2026-03-14")
	}
	with := func(e AnchorEvent, edit func(*AnchorEvent)) AnchorEvent { edit(&e); return e }

	valid := []AnchorEvent{
		with(good(AnchorEventKind), func(e *AnchorEvent) { e.Kind = AnchorKindQuit }),
		good(AnchorEventSlip),
		with(good(AnchorEventUse), func(e *AnchorEvent) { e.Intent = AnchorIntentSlip }),
		good(AnchorEventMiss),
		with(good(AnchorEventConfirm), func(e *AnchorEvent) { e.From = "2026-03-14" }),
		with(good(AnchorEventVoid), func(e *AnchorEvent) { e.Target = "anchorev_2026_03_13_b" }),
		good(AnchorEventGraduate),
		with(good(AnchorEventSlip), func(e *AnchorEvent) { e.RecordedAt = "2026-03-15T00:40:00-04:00" }),
	}
	for _, e := range valid {
		require.NoError(t, ValidateAnchorEvent(e), e.Type)
	}

	invalid := map[string]AnchorEvent{
		"id without a slot":       with(good(AnchorEventSlip), func(e *AnchorEvent) { e.ID = "anchorev_2026_03_14_" }),
		"id with an upper slot":   with(good(AnchorEventSlip), func(e *AnchorEvent) { e.ID = "anchorev_2026_03_14_A" }),
		"id with a bad date":      with(good(AnchorEventSlip), func(e *AnchorEvent) { e.ID = "anchorev_2026_02_30_a" }),
		"id in the anchor space":  with(good(AnchorEventSlip), func(e *AnchorEvent) { e.ID = "anchor_2026_03_14_a" }),
		"legacy anchor target":    with(good(AnchorEventSlip), func(e *AnchorEvent) { e.AnchorID = "legacy:sobriety" }),
		"empty anchor target":     with(good(AnchorEventSlip), func(e *AnchorEvent) { e.AnchorID = "" }),
		"partial day":             with(good(AnchorEventSlip), func(e *AnchorEvent) { e.Day = "2026-03" }),
		"bad recorded_at":         with(good(AnchorEventSlip), func(e *AnchorEvent) { e.RecordedAt = "yesterday" }),
		"unknown type":            good("lapse"),
		"kind with a bad kind":    with(good(AnchorEventKind), func(e *AnchorEvent) { e.Kind = "keep" }),
		"limit without window":    with(good(AnchorEventKind), func(e *AnchorEvent) { e.Kind, e.Allowance = AnchorKindLimit, 4 }),
		"use without intent":      good(AnchorEventUse),
		"use with a bad intent":   with(good(AnchorEventUse), func(e *AnchorEvent) { e.Intent = "maybe" }),
		"confirm without from":    good(AnchorEventConfirm),
		"confirm from after day":  with(good(AnchorEventConfirm), func(e *AnchorEvent) { e.From = "2026-03-15" }),
		"void without target":     good(AnchorEventVoid),
		"void of an anchor id":    with(good(AnchorEventVoid), func(e *AnchorEvent) { e.Target = evAnchorA }),
		"anchor id without slot":  with(good(AnchorEventSlip), func(e *AnchorEvent) { e.AnchorID = "anchor_2026_01_01" }),
		"anchor id bad separator": with(good(AnchorEventSlip), func(e *AnchorEvent) { e.AnchorID = "anchor_2026_01_01xa" }),
	}
	for name, e := range invalid {
		assert.Error(t, ValidateAnchorEvent(e), name)
	}
}

// TestFoldAnchorSkipsInvalidAndDanglingEvents: a record that fails validation,
// a second record reusing an id, and an event whose anchor no record holds are
// all ignored by the fold, so a hand-corrupted store never panics a read and
// never reaches the math.
func TestFoldAnchorSkipsInvalidAndDanglingEvents(t *testing.T) {
	anchors := AnchorLog{Version: AnchorVersion, History: []Anchor{
		anchorDef(evAnchorA, "sobriety", "2026-01-01", "2026-01-01T09:00:00Z"),
	}}
	firstSlip := anchorEv("anchorev_2026_03_01_a", evAnchorA, AnchorEventSlip, "2026-03-01")
	dupe := anchorEv("anchorev_2026_03_01_a", evAnchorA, AnchorEventSlip, "2026-03-05")
	log := AnchorEventLog{Events: []AnchorEvent{
		kindEv("anchorev_2026_01_02_a", evAnchorA, "2026-01-02", AnchorKindQuit, 0, 0),
		kindEv("anchorev_2026_01_03_a", evAnchorA, "2026-01-03", AnchorKindLimit, 0, 0), // invalid params: skipped
		{ID: "garbage", Type: AnchorEventSlip},
		{},
		firstSlip,
		dupe,
		anchorEv("anchorev_2026_03_02_a", "anchor_2026_09_09_z", AnchorEventSlip, "2026-03-02"), // dangling
		voidEv("anchorev_2026_03_03_a", evAnchorA, "2026-03-03", "anchorev_2026_09_09_a"),       // unknown target
	}}

	facts := FoldAnchorEvents(log, anchors)
	require.Len(t, facts, 1, "a dangling event never conjures an anchor")
	f := facts[evAnchorA]
	assert.Equal(t, AnchorKindQuit, f.Kind, "a kind event with invalid parameters is skipped")
	assert.Equal(t, []AnchorEvent{firstSlip}, f.Slips, "an id is a receipt: the first record under it wins")
	assert.Empty(t, f.Voided, "a void of an unknown id applies to nothing")
	assert.Empty(t, f.Voids)
}

// TestFoldAnchorVoids: a void drops its target from the facts and records who
// voided it; the first void of a target wins; a void aimed across anchors, at
// a void, or at a kind event applies to nothing; a voided graduation is no
// graduation.
func TestFoldAnchorVoids(t *testing.T) {
	anchors := AnchorLog{Version: AnchorVersion, History: []Anchor{
		anchorDef(evAnchorA, "morning-walk", "2026-01-01", "2026-01-01T09:00:00Z"),
		anchorDef(evAnchorB, "late-night-snacks", "2026-01-05", "2026-01-05T09:00:00Z"),
		{ID: evAnchorA, Label: "morning-walk", Date: "2026-01-01", RecordedAt: "2026-06-01T09:00:00Z", State: AnchorStateSunset},
	}}
	use := anchorEv("anchorev_2026_02_01_a", evAnchorB, AnchorEventUse, "2026-02-01")
	use.Intent = AnchorIntentPlanned
	confirm := anchorEv("anchorev_2026_02_08_a", evAnchorA, AnchorEventConfirm, "2026-02-07")
	confirm.From = "2026-02-01"
	log := AnchorEventLog{Events: []AnchorEvent{
		kindEv("anchorev_2026_01_10_a", evAnchorA, "2026-01-10", AnchorKindHabit, 0, 0),
		kindEv("anchorev_2026_01_10_b", evAnchorB, "2026-01-10", AnchorKindLimit, 4, 90),
		anchorEv("anchorev_2026_02_03_a", evAnchorA, AnchorEventMiss, "2026-02-03"),
		confirm,
		use,
		voidEv("anchorev_2026_02_10_a", evAnchorA, "2026-02-10", "anchorev_2026_02_03_a"),
		voidEv("anchorev_2026_02_10_b", evAnchorA, "2026-02-10", "anchorev_2026_02_03_a"), // already voided
		voidEv("anchorev_2026_02_10_c", evAnchorA, "2026-02-10", "anchorev_2026_02_01_a"), // across anchors
		voidEv("anchorev_2026_02_10_d", evAnchorA, "2026-02-10", "anchorev_2026_02_10_a"), // a void
		voidEv("anchorev_2026_02_10_e", evAnchorB, "2026-02-10", "anchorev_2026_01_10_b"), // a kind event
		anchorEv("anchorev_2026_06_01_a", evAnchorA, AnchorEventGraduate, "2026-06-01"),
		voidEv("anchorev_2026_06_02_a", evAnchorA, "2026-06-02", "anchorev_2026_06_01_a"),
	}}

	facts := FoldAnchorEvents(log, anchors)
	walk, snacks := facts[evAnchorA], facts[evAnchorB]

	assert.Empty(t, walk.Misses, "the voided miss drops out")
	assert.Equal(t, []AnchorEvent{confirm}, walk.Confirms)
	assert.Equal(t, map[string]string{
		"anchorev_2026_02_03_a": "anchorev_2026_02_10_a",
		"anchorev_2026_06_01_a": "anchorev_2026_06_02_a",
	}, walk.Voided, "the first void wins")
	require.Len(t, walk.Voids, 2)
	assert.Empty(t, walk.Graduates)
	assert.False(t, walk.Graduated, "a voided graduation reads as a plain retirement")

	assert.Equal(t, []AnchorEvent{use}, snacks.Uses, "a void aimed from another anchor applies to nothing")
	assert.Equal(t, AnchorKindLimit, snacks.Kind, "a void aimed at a kind event applies to nothing")
	assert.Empty(t, snacks.Voided)
}

// TestFoldCompatPairing: pair intents are matched against the two files by
// their exact halves, and each state folds conservatively — a completed
// re-date never moves the quit start, a pending one floors the effective date,
// an intent alone applies nothing, an add --kind definition is a real
// definition, and a graduation counts only once its sunset half exists.
func TestFoldCompatPairing(t *testing.T) {
	def := anchorDef(evAnchorA, "sobriety", "2026-01-01", "2026-01-01T09:00:00Z")
	quit := kindEv("anchorev_2026_01_02_a", evAnchorA, "2026-01-02", AnchorKindQuit, 0, 0)
	slip := anchorEv("anchorev_2026_03_01_a", evAnchorA, AnchorEventSlip, "2026-03-01")
	slipRedate := anchorDef(evAnchorA, "sobriety", "2026-03-01", slip.RecordedAt)
	slipIntent := AnchorPairIntent{Order: AnchorPairCompatFirst, Event: slip, Compat: slipRedate}

	t.Run("complete slip", func(t *testing.T) {
		anchors := AnchorLog{History: []Anchor{def, slipRedate}}
		log := AnchorEventLog{Events: []AnchorEvent{quit, slip}, PairIntents: []AnchorPairIntent{slipIntent}}

		states := AnchorPairStates(log, anchors)
		require.Len(t, states, 1)
		assert.Equal(t, AnchorPairComplete, states[0].Status)
		assert.Equal(t, 1, states[0].CompatIndex)

		f := FoldAnchorEvents(log, anchors)[evAnchorA]
		assert.Equal(t, "2026-01-01", f.Start, "a completed re-date never moves the start")
		assert.Empty(t, f.Floor)
		assert.Equal(t, []AnchorEvent{slip}, f.Slips)
		assert.Empty(t, f.Pending)
		assert.Equal(t, "2026-03-01", f.Anchor.Date, "the plain fold still reads the re-date, as an older binary does")
	})

	t.Run("pending slip floors the effective date", func(t *testing.T) {
		anchors := AnchorLog{History: []Anchor{def, slipRedate}}
		log := AnchorEventLog{Events: []AnchorEvent{quit}, PairIntents: []AnchorPairIntent{slipIntent}}

		states := AnchorPairStates(log, anchors)
		assert.Equal(t, AnchorPairCompatOnly, states[0].Status)

		f := FoldAnchorEvents(log, anchors)[evAnchorA]
		assert.Equal(t, "2026-01-01", f.Start)
		assert.Equal(t, "2026-03-01", f.Floor, "a pending re-date is never ignored")
		assert.Empty(t, f.Slips, "the slip is not a fact until its event exists")
		require.Len(t, f.Pending, 1)
		assert.Equal(t, slip.ID, f.Pending[0].Intent.Event.ID)
	})

	t.Run("intent only applies nothing but reserves its id", func(t *testing.T) {
		anchors := AnchorLog{History: []Anchor{def}}
		log := AnchorEventLog{Events: []AnchorEvent{quit}, PairIntents: []AnchorPairIntent{slipIntent}}

		assert.Equal(t, AnchorPairIntentOnly, AnchorPairStates(log, anchors)[0].Status)
		f := FoldAnchorEvents(log, anchors)[evAnchorA]
		assert.Equal(t, "2026-01-01", f.Start)
		assert.Empty(t, f.Floor)
		assert.Empty(t, f.Slips, "a planned event is never read as a fact")
		assert.Len(t, f.Pending, 1)
		assert.Equal(t, "anchorev_2026_03_01_b", NextAnchorEventID(log, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)))
	})

	t.Run("event-first slip void, then complete", func(t *testing.T) {
		void := voidEv("anchorev_2026_03_05_a", evAnchorA, "2026-03-05", slip.ID)
		restore := anchorDef(evAnchorA, "sobriety", "2026-01-01", void.RecordedAt)
		voidIntent := AnchorPairIntent{Order: AnchorPairEventFirst, Event: void, Compat: restore}
		log := AnchorEventLog{Events: []AnchorEvent{quit, slip, void}, PairIntents: []AnchorPairIntent{slipIntent, voidIntent}}

		anchors := AnchorLog{History: []Anchor{def, slipRedate}}
		states := AnchorPairStates(log, anchors)
		assert.Equal(t, AnchorPairComplete, states[0].Status)
		assert.Equal(t, AnchorPairEventOnly, states[1].Status)
		f := FoldAnchorEvents(log, anchors)[evAnchorA]
		assert.Empty(t, f.Slips, "the new binary reads the corrected history")
		assert.Equal(t, "2026-01-01", f.Start)
		assert.Empty(t, f.Floor)
		assert.Equal(t, "2026-03-01", f.Anchor.Date, "the older binary stays conservatively shorter")

		anchors.History = append(anchors.History, restore)
		assert.Equal(t, AnchorPairComplete, AnchorPairStates(log, anchors)[1].Status)
		f = FoldAnchorEvents(log, anchors)[evAnchorA]
		assert.Equal(t, "2026-01-01", f.Start, "the restoring record is a re-date, never a correction")
		assert.Empty(t, f.Pending)
	})

	t.Run("a record that keeps the date never moves the start", func(t *testing.T) {
		renamed := anchorDef(evAnchorA, "clear-head", "2026-03-01", "2026-04-01T09:00:00Z")
		anchors := AnchorLog{History: []Anchor{def, slipRedate, renamed}}
		log := AnchorEventLog{Events: []AnchorEvent{quit, slip}, PairIntents: []AnchorPairIntent{slipIntent}}

		f := FoldAnchorEvents(log, anchors)[evAnchorA]
		assert.Equal(t, "2026-01-01", f.Start, "a rename after a slip carries the re-date forward, not a correction")
		assert.Equal(t, "clear-head", f.Anchor.Label)

		corrected := anchorDef(evAnchorA, "clear-head", "2025-12-15", "2026-04-02T09:00:00Z")
		anchors.History = append(anchors.History, corrected)
		f = FoldAnchorEvents(log, anchors)[evAnchorA]
		assert.Equal(t, "2025-12-15", f.Start, "a record that changes the date is a correction and moves the start")
	})

	t.Run("add --kind writes a real definition", func(t *testing.T) {
		habit := kindEv("anchorev_2026_01_05_a", evAnchorB, "2026-01-05", AnchorKindHabit, 0, 0)
		walk := anchorDef(evAnchorB, "morning-walk", "2026-01-05", habit.RecordedAt)
		addIntent := AnchorPairIntent{Order: AnchorPairEventFirst, Event: habit, Compat: walk}
		log := AnchorEventLog{Events: []AnchorEvent{habit}, PairIntents: []AnchorPairIntent{addIntent}}

		pending := FoldAnchorEvents(log, AnchorLog{History: []Anchor{def}})
		assert.NotContains(t, pending, evAnchorB, "a dangling kind event conjures no anchor")
		assert.Equal(t, AnchorPairEventOnly, AnchorPairStates(log, AnchorLog{History: []Anchor{def}})[0].Status)

		f := FoldAnchorEvents(log, AnchorLog{History: []Anchor{def, walk}})[evAnchorB]
		assert.Equal(t, AnchorKindHabit, f.Kind)
		assert.Equal(t, "2026-01-05", f.Start, "the definition an add --kind writes sets the start")
		assert.Empty(t, f.Floor)
		assert.Empty(t, f.Pending)
	})

	t.Run("graduation counts once its sunset half exists", func(t *testing.T) {
		grad := anchorEv("anchorev_2026_06_01_a", evAnchorA, AnchorEventGraduate, "2026-06-01")
		sunset := Anchor{ID: evAnchorA, Label: "sobriety", Date: "2026-01-01", RecordedAt: grad.RecordedAt, State: AnchorStateSunset}
		gradIntent := AnchorPairIntent{Order: AnchorPairEventFirst, Event: grad, Compat: sunset}
		log := AnchorEventLog{Events: []AnchorEvent{quit, grad}, PairIntents: []AnchorPairIntent{gradIntent}}

		f := FoldAnchorEvents(log, AnchorLog{History: []Anchor{def}})[evAnchorA]
		assert.False(t, f.Graduated, "an active anchor is never graduated")

		// An older binary's plain sunset is not the planned half: still pending.
		plain := Anchor{ID: evAnchorA, Label: "sobriety", Date: "2026-01-01", RecordedAt: "2026-06-02T08:00:00Z", State: AnchorStateSunset}
		f = FoldAnchorEvents(log, AnchorLog{History: []Anchor{def, plain}})[evAnchorA]
		assert.True(t, f.Anchor.IsSunset())
		assert.False(t, f.Graduated, "a graduation whose own sunset half is missing is not recognized")

		f = FoldAnchorEvents(log, AnchorLog{History: []Anchor{def, sunset}})[evAnchorA]
		assert.True(t, f.Graduated)
		assert.Equal(t, "2026-01-01", f.Start, "a sunset keeps the date")
	})

	t.Run("a shared id and recorded_at are not proof of a pair", func(t *testing.T) {
		lookalike := anchorDef(evAnchorA, "sobriety", "2026-02-20", slip.RecordedAt)
		anchors := AnchorLog{History: []Anchor{def, lookalike}}
		log := AnchorEventLog{Events: []AnchorEvent{quit}, PairIntents: []AnchorPairIntent{slipIntent}}

		assert.Equal(t, AnchorPairIntentOnly, AnchorPairStates(log, anchors)[0].Status)
		f := FoldAnchorEvents(log, anchors)[evAnchorA]
		assert.Equal(t, "2026-02-20", f.Start, "an unpaired record is an ordinary correction")
		assert.Empty(t, f.Floor)
	})

	t.Run("a malformed intent is ignored", func(t *testing.T) {
		bad := slipIntent
		bad.Order = AnchorPairEventFirst // a slip always re-dates first
		anchors := AnchorLog{History: []Anchor{def, slipRedate}}
		log := AnchorEventLog{Events: []AnchorEvent{quit, slip}, PairIntents: []AnchorPairIntent{bad}}

		states := AnchorPairStates(log, anchors)
		assert.Equal(t, AnchorPairMalformed, states[0].Status)
		require.Error(t, states[0].Err)
		assert.Equal(t, -1, states[0].CompatIndex)

		f := FoldAnchorEvents(log, anchors)[evAnchorA]
		assert.Equal(t, "2026-03-01", f.Start, "a record no well-formed intent claims is an ordinary correction")
		assert.Empty(t, f.Pending)
	})

	t.Run("each intent claims its own record", func(t *testing.T) {
		anchors := AnchorLog{History: []Anchor{def, slipRedate}}
		log := AnchorEventLog{Events: []AnchorEvent{quit, slip}, PairIntents: []AnchorPairIntent{slipIntent, slipIntent}}
		states := AnchorPairStates(log, anchors)
		assert.Equal(t, AnchorPairComplete, states[0].Status)
		assert.Equal(t, AnchorPairEventOnly, states[1].Status, "one record completes one intent")
	})
}

// TestValidateAnchorPairIntent: a well-formed intent per two-file verb, and
// each way an intent can be malformed.
func TestValidateAnchorPairIntent(t *testing.T) {
	slip := anchorEv("anchorev_2026_03_01_a", evAnchorA, AnchorEventSlip, "2026-03-01")
	redate := anchorDef(evAnchorA, "sobriety", "2026-03-01", slip.RecordedAt)
	void := voidEv("anchorev_2026_03_05_a", evAnchorA, "2026-03-05", slip.ID)
	grad := anchorEv("anchorev_2026_06_01_a", evAnchorA, AnchorEventGraduate, "2026-06-01")
	toQuit := kindEv("anchorev_2026_04_01_a", evAnchorA, "2026-04-01", AnchorKindQuit, 0, 0)
	addHabit := kindEv("anchorev_2026_01_05_a", evAnchorB, "2026-01-05", AnchorKindHabit, 0, 0)
	toLimit := kindEv("anchorev_2026_04_01_b", evAnchorA, "2026-04-01", AnchorKindLimit, 4, 90)
	miss := anchorEv("anchorev_2026_04_02_a", evAnchorA, AnchorEventMiss, "2026-04-02")
	rec := func(e AnchorEvent, date, state string) Anchor {
		return Anchor{ID: e.AnchorID, Label: "x", Date: date, RecordedAt: e.RecordedAt, State: state}
	}

	valid := []AnchorPairIntent{
		{Order: AnchorPairCompatFirst, Event: slip, Compat: redate},
		{Order: AnchorPairEventFirst, Event: void, Compat: rec(void, "2026-01-01", "")},
		{Order: AnchorPairEventFirst, Event: grad, Compat: rec(grad, "2026-01-01", AnchorStateSunset)},
		{Order: AnchorPairCompatFirst, Event: toQuit, Compat: rec(toQuit, "2026-03-20", "")},
		{Order: AnchorPairEventFirst, Event: addHabit, Compat: rec(addHabit, "2026-01-05", "")},
	}
	for _, p := range valid {
		require.NoError(t, ValidateAnchorPairIntent(p), p.Event.Type)
	}

	otherTime := redate
	otherTime.RecordedAt = "2026-03-02T08:00:00Z"
	otherAnchor := redate
	otherAnchor.ID = evAnchorB
	badDate := redate
	badDate.Date = "2026-3-1"
	badEvent := slip
	badEvent.Day = ""
	invalid := map[string]AnchorPairIntent{
		"unknown order":              {Order: "both", Event: slip, Compat: redate},
		"malformed event":            {Order: AnchorPairCompatFirst, Event: badEvent, Compat: redate},
		"slip written event first":   {Order: AnchorPairEventFirst, Event: slip, Compat: redate},
		"void written compat first":  {Order: AnchorPairCompatFirst, Event: void, Compat: rec(void, "2026-01-01", "")},
		"compat-first non-quit kind": {Order: AnchorPairCompatFirst, Event: toLimit, Compat: rec(toLimit, "2026-03-20", "")},
		"a miss is never paired":     {Order: AnchorPairEventFirst, Event: miss, Compat: rec(miss, "2026-01-01", "")},
		"halves on two anchors":      {Order: AnchorPairCompatFirst, Event: slip, Compat: otherAnchor},
		"halves at two times":        {Order: AnchorPairCompatFirst, Event: slip, Compat: otherTime},
		"record with a bad date":     {Order: AnchorPairCompatFirst, Event: slip, Compat: badDate},
		"graduation without sunset":  {Order: AnchorPairEventFirst, Event: grad, Compat: rec(grad, "2026-01-01", "")},
		"re-date that retires":       {Order: AnchorPairCompatFirst, Event: slip, Compat: rec(slip, "2026-03-01", AnchorStateSunset)},
	}
	for name, p := range invalid {
		assert.Error(t, ValidateAnchorPairIntent(p), name)
	}
}

// TestFoldAnchorStartSkipsMalformedDates: a definition record whose date does
// not parse never sets the start, and an anchor whose only records are
// completed re-dates still gets a start rather than an empty one.
func TestFoldAnchorStartSkipsMalformedDates(t *testing.T) {
	history := []Anchor{
		{ID: evAnchorA, Label: "sobriety", Date: "2026-01-01"},
		{ID: evAnchorA, Label: "sobriety", Date: "not-a-date"},
		{ID: evAnchorA, Label: "sobriety", Date: "2026-01-01"},
	}
	start, floor := foldAnchorStart(history, []int{0, 1, 2}, nil)
	assert.Equal(t, "2026-01-01", start)
	assert.Empty(t, floor)

	start, floor = foldAnchorStart(history, []int{0}, map[int]string{0: AnchorPairComplete})
	assert.Equal(t, "2026-01-01", start, "a start never reads empty while a valid record exists")
	assert.Empty(t, floor)

	start, floor = foldAnchorStart(history, []int{1}, nil)
	assert.Empty(t, start, "no valid record, no start")
	assert.Empty(t, floor)
}
