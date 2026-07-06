package storage

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/observations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var loc = time.FixedZone("EDT", -4*3600) //nolint:gochecknoglobals // deterministic test-fixture zone

// newObsStore returns a scaffolded adapter over an isolated temp home so the
// real ~/.lucid/ is never touched (plan.md Approach §"Isolated test home").
func newObsStore(t *testing.T) *Adapter {
	t.Helper()
	a := New(t.TempDir())
	_, err := a.Scaffold()
	require.NoError(t, err)
	require.NoError(t, a.ScaffoldObservations())
	return a
}

func microEvent(kind, logicalDate string, payload map[string]any) observations.Event {
	return observations.Event{
		Schema: observations.Schema, Kind: kind,
		RecordedAt: "2026-07-02T21:45:00-04:00", OccurredAt: "2026-07-02T21:45:00-04:00",
		OccurredAtPrecision: observations.PrecisionExact, LogicalDate: logicalDate,
		Source: observations.SourceMicrolog, Payload: payload,
	}
}

func (a *Adapter) dayFileBytes(t *testing.T, date string) []byte {
	t.Helper()
	b, err := os.ReadFile(a.obsDayPath(date))
	require.NoError(t, err)
	return b
}

func TestScaffoldObservations_IdempotentWithSalt(t *testing.T) {
	a := New(t.TempDir())
	_, err := a.Scaffold()
	require.NoError(t, err)
	require.NoError(t, a.ScaffoldObservations())

	cfg, err := a.ReadObservationsConfig()
	require.NoError(t, err)
	assert.NotEmpty(t, cfg.KeySalt, "key_salt is generated at first run")
	assert.NoError(t, cfg.Validate())

	// The registry subtrees and projections dir exist.
	for _, d := range []string{"registries/injuries", "registries/places", "projections", "observations"} {
		info, statErr := os.Stat(filepath.Join(a.home, d))
		require.NoError(t, statErr)
		assert.True(t, info.IsDir())
	}

	// A second scaffold never rewrites the config (salt is stable).
	require.NoError(t, a.ScaffoldObservations())
	cfg2, err := a.ReadObservationsConfig()
	require.NoError(t, err)
	assert.Equal(t, cfg.KeySalt, cfg2.KeySalt)
}

func TestAppendObservation_SeqAndSingleLine(t *testing.T) {
	a := newObsStore(t)

	e1, err := a.AppendObservation(microEvent(observations.KindPain, "2026-07-02", map[string]any{"intensity": 6}))
	require.NoError(t, err)
	assert.Equal(t, "obs_2026_07_02_001", e1.ID)

	e2, err := a.AppendObservation(microEvent(observations.KindMood, "2026-07-02", map[string]any{"level": 3}))
	require.NoError(t, err)
	assert.Equal(t, "obs_2026_07_02_002", e2.ID)

	// A different logical day starts its own file at seq 1.
	e3, err := a.AppendObservation(microEvent(observations.KindPain, "2026-07-03", map[string]any{"intensity": 4}))
	require.NoError(t, err)
	assert.Equal(t, "obs_2026_07_03_001", e3.ID)

	// The day file holds one whole JSON line per event, newline-terminated.
	body := a.dayFileBytes(t, "2026-07-02")
	assert.True(t, strings.HasSuffix(string(body), "\n"))
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	require.Len(t, lines, 2)

	// A missing logical_date is rejected.
	_, err = a.AppendObservation(observations.Event{Kind: observations.KindPain})
	require.Error(t, err)
}

// TestReadObservationsDay_SkipsMalformedAndSeqIgnoresIt: a truncated line is
// skipped and counted; the next id derives from max well-formed seq + 1, never
// the line count (observations.md §2; error-states JSONL corruption).
func TestReadObservationsDay_SkipsMalformedAndSeqIgnoresIt(t *testing.T) {
	a := newObsStore(t)
	_, err := a.AppendObservation(microEvent(observations.KindPain, "2026-07-02", map[string]any{"intensity": 6}))
	require.NoError(t, err)

	// Inject a truncated line directly into the day file.
	require.NoError(t, appendLineFsync(a.obsDayPath("2026-07-02"), []byte(`{"id":"obs_2026_07_02_002","sch`)))

	events, skipped, err := a.ReadObservationsDay("2026-07-02")
	require.NoError(t, err)
	assert.Len(t, events, 1, "the malformed line is skipped")
	assert.Equal(t, 1, skipped, "and its count reported")

	// Seq derivation ignores the malformed line: next id is 002, not 003.
	next, err := a.AppendObservation(microEvent(observations.KindMood, "2026-07-02", map[string]any{"level": 3}))
	require.NoError(t, err)
	assert.Equal(t, "obs_2026_07_02_002", next.ID)
}

// TestAppendObservation_CorrectionLeavesOriginalByteIdentical: JSONL lines are
// never rewritten; a correction is a new appended event (observations.md §2).
func TestAppendObservation_CorrectionLeavesOriginalByteIdentical(t *testing.T) {
	a := newObsStore(t)
	orig, err := a.AppendObservation(microEvent(observations.KindPain, "2026-07-02", map[string]any{"intensity": 6}))
	require.NoError(t, err)

	before := a.dayFileBytes(t, "2026-07-02")
	firstLine := strings.SplitN(string(before), "\n", 2)[0]

	correction := microEvent(observations.KindPain, "2026-07-02", map[string]any{"intensity": 5})
	correction.Refs = map[string]any{"corrects": orig.ID}
	_, err = a.AppendObservation(correction)
	require.NoError(t, err)

	after := a.dayFileBytes(t, "2026-07-02")
	assert.Equal(t, firstLine, strings.SplitN(string(after), "\n", 2)[0],
		"the corrected event's original line stays byte-identical")
	assert.True(t, bytes.HasPrefix(after, before),
		"the correction is appended, never a rewrite")
}

func TestReadObservationsRangeAndKind(t *testing.T) {
	a := newObsStore(t)
	_, err := a.AppendObservation(microEvent(observations.KindPain, "2026-07-01", map[string]any{"intensity": 3}))
	require.NoError(t, err)
	_, err = a.AppendObservation(microEvent(observations.KindMood, "2026-07-02", map[string]any{"level": 4}))
	require.NoError(t, err)
	_, err = a.AppendObservation(microEvent(observations.KindPain, "2026-07-03", map[string]any{"intensity": 5}))
	require.NoError(t, err)

	rng, err := a.ReadObservationsRange("2026-07-01", "2026-07-02")
	require.NoError(t, err)
	assert.Len(t, rng, 2)

	pains, err := a.ReadObservationsKind(observations.KindPain)
	require.NoError(t, err)
	require.Len(t, pains, 2)
	assert.Equal(t, "obs_2026_07_01_001", pains[0].ID, "kind read is sorted by id")

	// A missing day is not an error.
	empty, skipped, err := a.ReadObservationsDay("2026-07-09")
	require.NoError(t, err)
	assert.Empty(t, empty)
	assert.Zero(t, skipped)
}

func TestRegistry_ResolveCreateMerge(t *testing.T) {
	a := newObsStore(t)

	// Key derivation is stable within the instance (same salt).
	k1, err := a.ResolveRegistryKey(observations.RegistryPlace, "Lisbon")
	require.NoError(t, err)
	k2, err := a.ResolveRegistryKey(observations.RegistryPlace, "Lisbon")
	require.NoError(t, err)
	assert.Equal(t, k1, k2, "salted registry key is stable within an instance")
	assert.Contains(t, k1, "place_")

	rec, err := a.UpdateRegistry(observations.RegistryPlace, k1, observations.RegistryPatch{
		DisplayName: "Lisbon", At: "2026-07-02T10:00:00-04:00",
	})
	require.NoError(t, err)
	assert.Equal(t, observations.StatusActive, rec.Status)
	require.Len(t, rec.StatusHistory, 1)

	// Re-logging the same place merges (a second history entry).
	rec, err = a.UpdateRegistry(observations.RegistryPlace, k1, observations.RegistryPatch{
		DisplayName: "Lisbon", At: "2026-07-03T10:00:00-04:00",
		Fields: map[string]any{"lat": 38.72, "lon": -9.14},
	})
	require.NoError(t, err)
	require.Len(t, rec.StatusHistory, 2)
	assert.InDelta(t, 38.72, rec.Fields["lat"], 0.001)

	// ReadRegistry / ReadRegistryKind round-trip.
	got, found, err := a.ReadRegistry(observations.RegistryPlace, k1)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, k1, got.Key)

	all, err := a.ReadRegistryKind(observations.RegistryPlace)
	require.NoError(t, err)
	require.Len(t, all, 1)

	_, found, err = a.ReadRegistry(observations.RegistryPlace, "place_absent")
	require.NoError(t, err)
	assert.False(t, found)

	_, err = a.UpdateRegistry("nope", "k", observations.RegistryPatch{})
	require.Error(t, err)
}

func TestReadDayView_JoinsTreesAndSpanningRange(t *testing.T) {
	a := newObsStore(t)
	require.NoError(t, a.ScaffoldEngine())

	// An engine day record for the day.
	require.NoError(t, a.WriteEngineDay(engine.DayRecord{
		DayID: "day_2026_07_02", LogicalDate: "2026-07-02", Mode: engine.ModeGreen,
		Completed: true, Capacity: 3, Links: map[string]string{"floor": engine.StatusDone},
	}))

	// A raw entry recorded that day.
	_, err := a.WriteRaw(RawEntry{
		RecordedAt: time.Date(2026, 7, 2, 8, 15, 0, 0, loc), OccurredAt: time.Date(2026, 7, 2, 8, 15, 0, 0, loc),
		OccurredAtPrecision: PrecisionExact, Source: "cli", Command: "/log", Body: "morning",
	})
	require.NoError(t, err)

	// A same-day observation.
	_, err = a.AppendObservation(microEvent(observations.KindPain, "2026-07-02", map[string]any{"intensity": 6}))
	require.NoError(t, err)

	// A range event on the prior night spanning into the day.
	end := "2026-07-02T07:10:00-04:00"
	night := observations.Event{
		Schema: observations.Schema, Kind: observations.KindSleep,
		RecordedAt: end, OccurredAt: "2026-07-01T23:00:00-04:00",
		OccurredAtPrecision: observations.PrecisionRange, OccurredAtEnd: &end,
		LogicalDate: "2026-07-01", Source: observations.SourceMicrolog,
		Payload: map[string]any{"quality": 3},
	}
	_, err = a.AppendObservation(night)
	require.NoError(t, err)

	view, err := a.ReadDayView("2026-07-02", loc)
	require.NoError(t, err)
	require.NotNil(t, view.EngineDay)
	assert.True(t, view.EngineDay.Completed)
	require.Len(t, view.Obs.Events, 1)
	require.Len(t, view.Obs.RangeEvents, 1, "the prior night's sleep spans into today")
	assert.Equal(t, observations.KindSleep, view.Obs.RangeEvents[0].Kind)
	assert.NotEmpty(t, view.RawEntryIDs)
}

func TestReadDayView_EmptyDay(t *testing.T) {
	a := newObsStore(t)
	require.NoError(t, a.ScaffoldEngine())
	view, err := a.ReadDayView("2026-07-09", loc)
	require.NoError(t, err)
	assert.Nil(t, view.EngineDay)
	assert.True(t, view.Obs.Empty())
	assert.Empty(t, view.RawEntryIDs)
}
