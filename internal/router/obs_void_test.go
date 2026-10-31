package router

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

// obsTreeSnapshot returns the bytes of every file under the observations tree —
// day files, the config, the range index — keyed by path, so a void test can
// prove the file list and every byte are unchanged by a refusal, and that a
// void touched exactly one file.
func obsTreeSnapshot(t *testing.T, r *Router) map[string]string {
	t.Helper()
	out := map[string]string{}
	root := filepath.Join(r.Store().Home(), "observations")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		out[path] = string(b)
		return nil
	})
	require.NoError(t, err)
	return out
}

// appendKindEvent hand-appends one minimal event of kind on the fixture day, so
// the refusal tests hold a workout or memory id without enabling those kinds.
func appendKindEvent(t *testing.T, r *Router, kind observations.Kind, payload map[string]any) observations.Event {
	t.Helper()
	ev, err := r.Store().AppendObservation(observations.Event{
		Schema:              observations.Schema,
		Kind:                kind,
		RecordedAt:          nowEDT().Format(time.RFC3339),
		OccurredAt:          nowEDT().Format(time.RFC3339),
		OccurredAtPrecision: observations.PrecisionExact,
		LogicalDate:         "2026-07-02",
		Source:              observations.SourceMicrolog,
		Payload:             payload,
	})
	require.NoError(t, err)
	return ev
}

// TestVoidObservation_AppendsOneVoidEvent: a void appends exactly one
// measurement event — refs.corrects naming the reading plus refs.void, an empty
// payload and no tags, exact precision, filed under the current logical day —
// and touches no other file; the reading's own line is never rewritten.
func TestVoidObservation_AppendsOneVoidEvent(t *testing.T) {
	r := bootedObs(t)
	reading := capture(t, r, "measurement", "weight", "70.5", "kg")
	require.Equal(t, "2026-07-02", reading.LogicalDate)

	before := obsTreeSnapshot(t, r)
	now := time.Date(2026, 7, 3, 10, 0, 0, 0, edt)
	res, err := r.VoidObservation(ObsVoidRequest{ID: " " + reading.EventID + " ", Now: now})
	require.NoError(t, err)

	assert.False(t, res.Rejected)
	assert.Equal(t, reading.EventID, res.TargetID, "the id is trimmed")
	assert.Equal(t, "2026-07-02", res.TargetDate)
	assert.Equal(t, "2026-07-03", res.LogicalDate, "the void files under the current logical day")
	assert.Equal(t, "obs_2026_07_03_001", res.EventID)
	assert.Equal(t,
		"Voided measurement `"+reading.EventID+"` (2026-07-02) as `obs_2026_07_03_001` — it no longer "+
			"counts in the measurement export or the packet; both records are kept.",
		res.Ack)

	after := obsTreeSnapshot(t, r)
	voidPath, err := filepath.Abs(filepath.Join(r.Store().Home(), "observations", "2026", "07", "obs_2026_07_03.jsonl"))
	require.NoError(t, err)
	for path, body := range before {
		assert.Equalf(t, body, after[path], "%s is unchanged", path)
	}
	require.Len(t, after, len(before)+1, "exactly one new file — the current day's")
	line, ok := after[voidPath]
	require.True(t, ok, "the void lands in the current logical day's file: %v", after)
	assert.Equal(t, 1, strings.Count(line, "\n"), "exactly one line is appended")
	assert.Contains(t, line, `"payload":{}`)
	assert.Contains(t, line, `"tags":[]`)

	ev := eventByID(t, r, res.LogicalDate, res.EventID)
	require.NoError(t, ev.Validate())
	assert.Equal(t, observations.KindMeasurement, ev.Kind)
	assert.Empty(t, ev.Payload)
	assert.Empty(t, ev.Tags)
	assert.Equal(t, map[string]any{"corrects": reading.EventID, "void": true}, ev.Refs)
	assert.Equal(t, observations.PrecisionExact, ev.OccurredAtPrecision)
	assert.Equal(t, now.Format(time.RFC3339), ev.OccurredAt)
	assert.Equal(t, "2026-07-03", ev.LogicalDate)
	target, isVoid := observations.MeasurementVoidTarget(ev)
	assert.True(t, isVoid)
	assert.Equal(t, reading.EventID, target)
}

// TestVoidObservation_Refusals pins every strict-tier refusal (error-states.md
// Ov-1..Ov-5): each returns the bare fixed reason ending "nothing was saved"
// (the CLI adds the verb prefix) and leaves the observations tree byte-identical.
func TestVoidObservation_Refusals(t *testing.T) {
	r := bootedObs(t)
	capture(t, r, "measurement", "bp-systolic", "121", "mmHg")
	retracted := capture(t, r, "measurement", "bp-systolic", "119", "mmHg")
	pain := capture(t, r, "pain", "6", "knee")
	voided, err := r.VoidObservation(ObsVoidRequest{ID: retracted.EventID, Now: nowEDT()})
	require.NoError(t, err)
	session := appendKindEvent(t, r, observations.KindWorkout, map[string]any{"type": "push"})
	story := appendKindEvent(t, r, observations.KindMemory, map[string]any{"text": "a walk by the river"})

	tests := []struct {
		name string
		id   string
		want string
	}{
		{"ov1 unparseable id", "yesterday's weight", `no observation "yesterday's weight"; nothing was saved`},
		{"ov1 empty id", "  ", `no observation ""; nothing was saved`},
		{"ov1 missing id", "obs_2026_07_02_009", `no observation "obs_2026_07_02_009"; nothing was saved`},
		{
			"ov2 void of a void", voided.EventID,
			`"` + voided.EventID + `" is itself a void — a void can't be voided; log the reading again to restore it; nothing was saved`,
		},
		{
			"ov3 already voided", retracted.EventID,
			`"` + retracted.EventID + `" is already voided (by "` + voided.EventID + `"); nothing was saved`,
		},
		{
			"ov4 workout", session.ID,
			`"` + session.ID + `" is a workout observation — correct it with lucid workout amend; nothing was saved`,
		},
		{
			"ov4 memory", story.ID,
			`"` + story.ID + `" is a memory observation — correct it with lucid memory amend; nothing was saved`,
		},
		{
			"ov5 other kind", pain.EventID,
			`"` + pain.EventID + `" is a pain observation — only measurement readings can be voided yet; nothing was saved`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := obsTreeSnapshot(t, r)
			res, err := r.VoidObservation(ObsVoidRequest{ID: tc.id, Now: nowEDT()})
			require.Error(t, err)
			assert.Equal(t, tc.want, err.Error())
			assert.NotContains(t, err.Error(), "lucid obs void:", "the router returns the bare reason")
			assert.False(t, res.Rejected)
			assert.Empty(t, res.EventID)
			assert.Equal(t, before, obsTreeSnapshot(t, r), "a refused void writes nothing")
		})
	}
}

// TestVoidObservation_DisabledKindHintWritesNothing: with the measurement kind
// disabled, the gate is checked first (Ov-6) — the enable hint, a nil error and
// nothing written, whatever the id names.
func TestVoidObservation_DisabledKindHintWritesNothing(t *testing.T) {
	r := bootedObs(t)
	reading := capture(t, r, "measurement", "weight", "70.5", "kg")
	pain := capture(t, r, "pain", "6", "knee")

	cfg, err := r.Store().ReadObservationsConfig()
	require.NoError(t, err)
	cfg.KindsEnabled = []observations.Kind{observations.KindPain}
	require.NoError(t, r.Store().SaveObservationsConfig(cfg))

	for _, id := range []string{reading.EventID, pain.EventID, "not-an-id"} {
		before := obsTreeSnapshot(t, r)
		res, err := r.VoidObservation(ObsVoidRequest{ID: id, Now: nowEDT()})
		require.NoErrorf(t, err, "id %q: a disabled kind is a graceful skip, not a failure", id)
		assert.True(t, res.Rejected)
		assert.Empty(t, res.EventID)
		assert.Equal(t, observations.EnableHint(observations.KindMeasurement), res.Ack)
		assert.Equalf(t, before, obsTreeSnapshot(t, r), "id %q: a gated void writes nothing", id)
	}
}

// TestVoidObservation_ConfigReadError: an unreadable observations config is a
// runtime fault, returned before any id is looked up.
func TestVoidObservation_ConfigReadError(t *testing.T) {
	r := brokenObsRouter(t)
	_, err := r.VoidObservation(ObsVoidRequest{ID: "obs_2026_07_02_001", Now: nowEDT()})
	require.Error(t, err)
}

// TestVoidObservation_DayFileReadError: a day file that cannot be read is a
// fault reported as "nothing was saved", never mistaken for a missing id.
func TestVoidObservation_DayFileReadError(t *testing.T) {
	r := bootedObs(t)
	reading := capture(t, r, "measurement", "weight", "70.5", "kg")
	dayFile := filepath.Join(r.Store().Home(), "observations", "2026", "07", "obs_2026_07_02.jsonl")
	require.NoError(t, os.Remove(dayFile))
	require.NoError(t, os.Mkdir(dayFile, 0o700))

	_, err := r.VoidObservation(ObsVoidRequest{ID: reading.EventID, Now: nowEDT()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not read the observation; nothing was saved")
}
