package router

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

var edt = time.FixedZone("EDT", -4*3600) //nolint:gochecknoglobals // deterministic test-fixture zone

func nowEDT() time.Time { return time.Date(2026, 7, 2, 21, 45, 0, 0, edt) }

// bootedObs returns a booted router whose observations config enables every
// capturable kind — the steel-thread surface for capture tests.
func bootedObs(t *testing.T) *Router {
	t.Helper()
	a := newScaffolded(t)
	require.NoError(t, a.ScaffoldObservations())
	cfg, err := a.ReadObservationsConfig()
	require.NoError(t, err)
	cfg.KindsEnabled = []string{
		observations.KindPain, observations.KindSymptom, observations.KindIntake,
		observations.KindElimination, observations.KindMood, observations.KindSleep,
		observations.KindMed, observations.KindIntervention, observations.KindMeasurement,
		observations.KindMemory, observations.KindLocation,
	}
	require.NoError(t, a.SaveObservationsConfig(cfg))
	r := New(a)
	_, err = r.Boot()
	require.NoError(t, err)
	return r
}

func capture(t *testing.T, r *Router, tokens ...string) CaptureResult {
	t.Helper()
	res, err := r.Capture(CaptureRequest{Tokens: tokens, Now: nowEDT()})
	require.NoError(t, err)
	return res
}

func readBack(t *testing.T, r *Router, res CaptureResult) observations.Event {
	t.Helper()
	events, _, err := r.Store().ReadObservationsDay(res.LogicalDate)
	require.NoError(t, err)
	for _, ev := range events {
		if ev.ID == res.EventID {
			return ev
		}
	}
	t.Fatalf("event %s not found in %s", res.EventID, res.LogicalDate)
	return observations.Event{}
}

// TestCapture_ShorthandsWriteValidEnvelopes: every named shorthand and the
// generic form writes a valid frozen envelope with the documented kind (AC-7).
func TestCapture_ShorthandsWriteValidEnvelopes(t *testing.T) {
	r := bootedObs(t)
	cases := []struct {
		tokens []string
		kind   string
	}{
		{[]string{"pain", "6", "knee"}, observations.KindPain},
		{[]string{"ate", "eggs,", "toast"}, observations.KindIntake},
		{[]string{"drank", "500ml", "water"}, observations.KindIntake},
		{[]string{"bm", "4"}, observations.KindElimination},
		{[]string{"mood", "3", "steady"}, observations.KindMood},
		{[]string{"slept", "2340", "0710", "q3"}, observations.KindSleep},
		{[]string{"obs", "symptom", "headache", "4"}, observations.KindSymptom},
		{[]string{"obs", "med", "ibuprofen", "400"}, observations.KindMed},
		{[]string{"obs", "measurement", "weight", "180", "lb"}, observations.KindMeasurement},
	}
	for _, tc := range cases {
		res := capture(t, r, tc.tokens...)
		assert.Equalf(t, tc.kind, res.Kind, "%v", tc.tokens)
		ev := readBack(t, r, res)
		require.NoErrorf(t, ev.Validate(), "%v produced an invalid envelope", tc.tokens)
		assert.Equal(t, tc.kind, ev.Kind)
		assert.Equal(t, observations.SourceMicrolog, ev.Source)
		assert.Equal(t, observations.Schema, ev.Schema)
	}
}

// TestCapture_DisabledKindRejectsWithHint (AC-7 / error-states).
func TestCapture_DisabledKindRejectsWithHint(t *testing.T) {
	a := newScaffolded(t)
	require.NoError(t, a.ScaffoldObservations()) // default config: sleep is NOT enabled
	r := New(a)
	_, err := r.Boot()
	require.NoError(t, err)

	res, err := r.Capture(CaptureRequest{Tokens: []string{"slept", "q3"}, Now: nowEDT()})
	require.NoError(t, err)
	assert.True(t, res.Rejected)
	assert.Empty(t, res.EventID, "no event is written for a disabled kind")
	assert.Contains(t, res.Ack, "sleep")
	assert.Contains(t, res.Ack, "observations/config.json")

	// Nothing landed on disk.
	events, _, err := a.ReadObservationsDay(observations.DateString(observations.DateOf(nowEDT())))
	require.NoError(t, err)
	assert.Empty(t, events)
}

// TestCapture_PartialPathKeepsKind: /mood wired and /pain 15 store a partial
// event, kind preserved (AC-7).
func TestCapture_PartialPathKeepsKind(t *testing.T) {
	r := bootedObs(t)

	moodPartial := capture(t, r, "mood", "wired")
	assert.True(t, moodPartial.Partial)
	ev := readBack(t, r, moodPartial)
	assert.Equal(t, observations.KindMood, ev.Kind)
	assert.Equal(t, "wired", ev.Payload["note"])
	assert.Equal(t, observations.ParseMarkerPartial, ev.Payload["parse"])

	painOOR := capture(t, r, "pain", "15")
	assert.True(t, painOOR.Partial)
	ev = readBack(t, r, painOOR)
	assert.Equal(t, observations.KindPain, ev.Kind)
	assert.Equal(t, "15", ev.Payload["note"])
}

// TestCapture_WhereCreatesPlaceRegistry (AC-7).
func TestCapture_WhereCreatesPlaceRegistry(t *testing.T) {
	r := bootedObs(t)
	res := capture(t, r, "obs", "where", "Lisbon")
	require.NotEmpty(t, res.PlaceKey)
	assert.Contains(t, res.PlaceKey, "place_")

	ev := readBack(t, r, res)
	assert.Equal(t, observations.KindLocation, ev.Kind)
	assert.Equal(t, res.PlaceKey, ev.Payload["place_ref"])
	assert.Equal(t, res.PlaceKey, ev.Refs["place"])

	rec, found, err := r.Store().ReadRegistry(observations.RegistryPlace, res.PlaceKey)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "Lisbon", rec.DisplayName)
}

// TestCapture_AckIsInventoryOnly: no evaluative language in any ack (§0).
func TestCapture_AckIsInventoryOnly(t *testing.T) {
	r := bootedObs(t)
	acks := []string{
		capture(t, r, "bm", "4").Ack,
		capture(t, r, "pain", "6", "knee").Ack,
		capture(t, r, "mood", "wired").Ack,
	}
	for _, ack := range acks {
		assert.Contains(t, ack, "Logged")
		for _, banned := range []string{"streak", "good", "keep it up", "great", "score", "nice"} {
			assert.NotContainsf(t, strings.ToLower(ack), banned,
				"ack must be inventory, never obligation: %q", ack)
		}
	}
}

// TestCapture_BmRoundTripUnderOneSecond (AC-7: `/bm 4` round-trips < 1 s).
func TestCapture_BmRoundTripUnderOneSecond(t *testing.T) {
	r := bootedObs(t)
	start := time.Now()
	res := capture(t, r, "bm", "4")
	assert.Less(t, time.Since(start), time.Second)
	assert.NotEmpty(t, res.EventID)
}

// TestCapture_ObsPrefixStrippedEquivalence: the generic `obs` prefix is
// optional — `obs where X` and `where X` resolve identically.
func TestCapture_ObsPrefixStrippedEquivalence(t *testing.T) {
	r := bootedObs(t)
	withPrefix := capture(t, r, "obs", "where", "Porto")
	withoutPrefix := capture(t, r, "where", "Porto")
	assert.Equal(t, withPrefix.PlaceKey, withoutPrefix.PlaceKey)
	assert.Equal(t, observations.KindLocation, withPrefix.Kind)
	assert.Equal(t, observations.KindLocation, withoutPrefix.Kind)
}

func TestCapture_Errors(t *testing.T) {
	r := bootedObs(t)

	_, err := r.Capture(CaptureRequest{Tokens: []string{"nonsense", "x"}, Now: nowEDT()})
	require.Error(t, err, "an unknown kind is an error")

	_, err = r.Capture(CaptureRequest{Tokens: nil, Now: nowEDT()})
	require.Error(t, err, "nothing to log is an error")

	_, err = r.Capture(CaptureRequest{Tokens: []string{"obs"}, Now: nowEDT()})
	require.Error(t, err, "a bare obs with no kind is an error")
}

// TestCapture_TagsPreserved: #tags reach tags[] and stay in the note.
func TestCapture_TagsPreserved(t *testing.T) {
	r := bootedObs(t)
	res := capture(t, r, "pain", "6", "knee", "#running", "aching")
	ev := readBack(t, r, res)
	assert.Equal(t, []any{"running"}, toAnySlice(ev.Tags))
	assert.Contains(t, ev.Payload["note"], "#running")
}

// toAnySlice adapts a []string to []any for JSON-decoded comparison symmetry.
func toAnySlice(xs []string) []any {
	out := make([]any, len(xs))
	for i, x := range xs {
		out[i] = x
	}
	return out
}

// TestCapture_StampsProvenanceWhenSupplied: a harness capture stamps
// payload.provenance with the NORMALIZED harness and the supplied agent/model/
// channel, while the frozen envelope's source stays microlog and schema stays 1
// (AC-3, AC-6, AC-9).
func TestCapture_StampsProvenanceWhenSupplied(t *testing.T) {
	r := bootedObs(t)
	res, err := r.Capture(CaptureRequest{
		Tokens:  []string{"pain", "6", "knee"},
		Now:     nowEDT(),
		Harness: "  Discord ", // normalized to "discord" via the shared grammar
		Agent:   "agent-x",
		Model:   "model-y",
		Channel: "<channel>",
	})
	require.NoError(t, err)

	ev := readBack(t, r, res)
	require.NoError(t, ev.Validate())
	// Frozen envelope is untouched — provenance is orthogonal to source/schema.
	assert.Equal(t, observations.SourceMicrolog, ev.Source)
	assert.Equal(t, observations.Schema, ev.Schema)

	prov, ok := ev.Payload["provenance"].(map[string]any)
	require.True(t, ok, "payload.provenance is present and a sub-object")
	assert.Equal(t, "discord", prov["harness"], "harness is normalized through NormalizeSource")
	assert.Equal(t, "agent-x", prov["agent"])
	assert.Equal(t, "model-y", prov["model"])
	assert.Equal(t, "<channel>", prov["channel"])
}

// TestCapture_ProvenancePartialFields: provenance carries only the supplied
// keys — an agent-only capture stamps {agent} with no harness normalization and
// no empty keys (AC-9).
func TestCapture_ProvenancePartialFields(t *testing.T) {
	r := bootedObs(t)
	res, err := r.Capture(CaptureRequest{
		Tokens: []string{"mood", "3", "steady"},
		Now:    nowEDT(),
		Agent:  "agent-x",
	})
	require.NoError(t, err)

	ev := readBack(t, r, res)
	prov, ok := ev.Payload["provenance"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "agent-x", prov["agent"])
	assert.NotContains(t, prov, "harness")
	assert.NotContains(t, prov, "model")
	assert.NotContains(t, prov, "channel")
}

// TestCapture_BareCaptureOmitsProvenance: a capture with no harness provenance
// omits the provenance key entirely, so the on-disk event marshals byte-
// identically to the pre-change shape (AC-9 byte-stability).
func TestCapture_BareCaptureOmitsProvenance(t *testing.T) {
	r := bootedObs(t)
	res := capture(t, r, "pain", "6", "knee")

	ev := readBack(t, r, res)
	_, hasProvenance := ev.Payload["provenance"]
	assert.False(t, hasProvenance, "a bare capture writes no provenance key")

	// Byte-stability: the marshaled line carries no provenance token at all.
	line, err := ev.MarshalLine()
	require.NoError(t, err)
	assert.NotContains(t, string(line), "provenance")
}

// TestCapture_MalformedHarnessRejected: a malformed harness token is rejected
// with a clear error and nothing is written — never silently coerced (AC-8/AC-9,
// honest reject).
func TestCapture_MalformedHarnessRejected(t *testing.T) {
	r := bootedObs(t)
	_, err := r.Capture(CaptureRequest{
		Tokens:  []string{"pain", "6", "knee"},
		Now:     nowEDT(),
		Harness: "bad token!",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "harness")

	// Nothing landed on disk for the day.
	events, _, rerr := r.Store().ReadObservationsDay(observations.DateString(observations.DateOf(nowEDT())))
	require.NoError(t, rerr)
	assert.Empty(t, events, "a malformed harness leaves the day file empty")
}

// Guard: the storage adapter is the only writer — a capture leaves the day
// file terminated by exactly one newline per event (whole-line append).
func TestCapture_WholeLineAppend(t *testing.T) {
	r := bootedObs(t)
	capture(t, r, "pain", "6")
	capture(t, r, "mood", "3")
	events, skipped, err := r.Store().ReadObservationsDay(observations.DateString(observations.DateOf(nowEDT())))
	require.NoError(t, err)
	assert.Zero(t, skipped)
	assert.Len(t, events, 2)
}
