package storage

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/observations"
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

func microEvent(kind observations.Kind, logicalDate string, payload map[string]any) observations.Event {
	return observations.Event{
		Schema: observations.Schema, Kind: kind,
		RecordedAt: "2026-07-02T21:45:00-04:00", OccurredAt: "2026-07-02T21:45:00-04:00",
		OccurredAtPrecision: observations.PrecisionExact, LogicalDate: logicalDate,
		Source: observations.SourceMicrolog, Payload: payload,
	}
}

// obsDayPathT is obsDayPath with the error asserted away — the tests only ever
// pass well-formed dates, so a rejection is a test bug, not an expected path.
func (a *Adapter) obsDayPathT(t *testing.T, date string) string {
	t.Helper()
	p, err := a.obsDayPath(date)
	require.NoError(t, err)
	return p
}

func (a *Adapter) dayFileBytes(t *testing.T, date string) []byte {
	t.Helper()
	b, err := os.ReadFile(a.obsDayPathT(t, date))
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
	require.NoError(t, appendLineFsync(a.obsDayPathT(t, "2026-07-02"), []byte(`{"id":"obs_2026_07_02_002","sch`)))

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

// TestRecentObservations_WindowSortedUnfiltered proves the companion's read
// seam returns exactly the [now-window, now] slice sorted by id, across every
// kind (the render-relevant filter is the composer's concern, not the reader's),
// and that the window bound and the non-positive clamp behave.
func TestRecentObservations_WindowSortedUnfiltered(t *testing.T) {
	a := newObsStore(t)
	_, err := a.AppendObservation(microEvent(observations.KindMood, "2026-07-05", map[string]any{"level": 4}))
	require.NoError(t, err)
	_, err = a.AppendObservation(microEvent(observations.KindPain, "2026-07-06", map[string]any{"intensity": 3}))
	require.NoError(t, err)
	_, err = a.AppendObservation(microEvent(observations.KindIntake, "2026-07-06", map[string]any{"class": "food", "what": "eggs"}))
	require.NoError(t, err)
	_, err = a.AppendObservation(microEvent(observations.KindPain, "2026-07-08", map[string]any{"intensity": 5}))
	require.NoError(t, err)

	now := time.Date(2026, 7, 8, 9, 0, 0, 0, loc)

	// A 7-day look-back spans 2026-07-01..2026-07-08 and returns every kind in
	// range, sorted by id — the reader does not filter by kind.
	got, err := a.RecentObservations(now, 7)
	require.NoError(t, err)
	require.Len(t, got, 4)
	assert.Equal(t, "obs_2026_07_05_001", got[0].ID, "sorted by id, oldest first")
	assert.Equal(t, "obs_2026_07_08_001", got[3].ID)
	kinds := make([]observations.Kind, len(got))
	for i, ev := range got {
		kinds[i] = ev.Kind
	}
	assert.Contains(t, kinds, observations.KindIntake, "the reader is generic — it does not drop non-render kinds")

	// A tighter 2-day window (start 2026-07-06) drops the 07-05 mood event.
	got2, err := a.RecentObservations(now, 2)
	require.NoError(t, err)
	require.Len(t, got2, 3)
	assert.Equal(t, "obs_2026_07_06_001", got2[0].ID)

	// A zero window reads only today.
	got0, err := a.RecentObservations(now, 0)
	require.NoError(t, err)
	require.Len(t, got0, 1)
	assert.Equal(t, "obs_2026_07_08_001", got0[0].ID)

	// A negative window is clamped to today, never an inverted range.
	gotNeg, err := a.RecentObservations(now, -3)
	require.NoError(t, err)
	require.Len(t, gotNeg, 1)
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

	view, err := a.ReadDayView("2026-07-02", loc, observations.DefaultRolloverMin)
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
	view, err := a.ReadDayView("2026-07-09", loc, observations.DefaultRolloverMin)
	require.NoError(t, err)
	assert.Nil(t, view.EngineDay)
	assert.True(t, view.Obs.Empty())
	assert.Empty(t, view.RawEntryIDs)
}

// writeRawAt writes a synthetic raw entry recorded at recorded and occurring
// at occurred with the given precision, returning its assigned id.
func writeRawAt(t *testing.T, a *Adapter, recorded, occurred time.Time, precision, body string) string {
	t.Helper()
	res, err := a.WriteRaw(RawEntry{
		RecordedAt: recorded, OccurredAt: occurred, OccurredAtPrecision: precision,
		Source: "cli", Command: "/log", Body: body,
	})
	require.NoError(t, err)
	return res.RawID
}

// TestReadDayView_GroupsRawByLogicalDay pins the day view's raw-entry grouping
// to the logical day of occurred_at (observations.md §2), never the raw id's
// recorded date. All four entries belong to 2026-09-27 though none was
// recorded that day: a bare capture at 00:33 the next morning (before the
// 04:00 rollover), two `--day @2026-09-27` captures written minutes later
// (stored at local midnight, approximate), and one backdated to 2026-09-27
// from a week later in another month's shard. The rollover boundary is pinned
// on both sides: 03:59:59 still belongs to 2026-09-27, 04:00 exactly to
// 2026-09-28. Fixtures are wholly synthetic.
func TestReadDayView_GroupsRawByLogicalDay(t *testing.T) {
	a := newObsStore(t)
	require.NoError(t, a.ScaffoldEngine())

	dayMidnight := time.Date(2026, 9, 27, 0, 0, 0, 0, loc)
	bare := time.Date(2026, 9, 28, 0, 33, 0, 0, loc)
	bareID := writeRawAt(t, a, bare, bare, PrecisionExact, "a late note about the day just lived")
	dayFlagID := writeRawAt(t, a, time.Date(2026, 9, 28, 0, 35, 0, 0, loc), dayMidnight,
		PrecisionApproximate, "backdated with an explicit day")
	dayFlagSecondID := writeRawAt(t, a, time.Date(2026, 9, 28, 0, 35, 28, 0, loc), dayMidnight,
		PrecisionApproximate, "a second backdated note in the same minute")
	farBackdatedID := writeRawAt(t, a, time.Date(2026, 10, 5, 10, 0, 0, 0, loc), dayMidnight,
		PrecisionApproximate, "remembered a week later")
	lastSecond := time.Date(2026, 9, 28, 3, 59, 59, 0, loc)
	lastSecondID := writeRawAt(t, a, lastSecond, lastSecond, PrecisionExact, "one second before the rollover")
	atRollover := time.Date(2026, 9, 28, 4, 0, 0, 0, loc)
	atRolloverID := writeRawAt(t, a, atRollover, atRollover, PrecisionExact, "exactly at the rollover")

	prior, err := a.ReadDayView("2026-09-27", loc, observations.DefaultRolloverMin)
	require.NoError(t, err)
	assert.Equal(t,
		[]string{dayFlagID, dayFlagSecondID, farBackdatedID, bareID, lastSecondID},
		prior.RawEntryIDs, "every entry whose logical day is 2026-09-27, by occurred_at then id")

	next, err := a.ReadDayView("2026-09-28", loc, observations.DefaultRolloverMin)
	require.NoError(t, err)
	assert.Equal(t, []string{atRolloverID}, next.RawEntryIDs,
		"the recorded date never groups: only the at-rollover entry is 2026-09-28's")

	// The ids still name the creation time — grouping changes nothing on disk.
	assert.True(t, strings.HasPrefix(bareID, "raw_2026_09_28_"))
	assert.True(t, strings.HasPrefix(farBackdatedID, "raw_2026_10_05_"))
}

// TestReadDayView_RawGroupingHonorsRollover proves the grouping boundary is
// the caller's rollover, not a hidden constant: under a 05:00 rollover a 04:30
// capture still belongs to the previous logical day.
func TestReadDayView_RawGroupingHonorsRollover(t *testing.T) {
	a := newObsStore(t)
	require.NoError(t, a.ScaffoldEngine())
	at := time.Date(2026, 9, 28, 4, 30, 0, 0, loc)
	id := writeRawAt(t, a, at, at, PrecisionExact, "after four, before five")

	under4, err := a.ReadDayView("2026-09-28", loc, 4*60)
	require.NoError(t, err)
	assert.Equal(t, []string{id}, under4.RawEntryIDs)

	under5, err := a.ReadDayView("2026-09-27", loc, 5*60)
	require.NoError(t, err)
	assert.Equal(t, []string{id}, under5.RawEntryIDs)
}

// TestRawIDsForDate_SkipsUnreadableEntries: a raw file whose occurred_at is
// not a timestamp, or that has no frontmatter at all, is skipped rather than
// failing the day view — and the well-formed entry beside it still lists.
func TestRawIDsForDate_SkipsUnreadableEntries(t *testing.T) {
	a := newObsStore(t)
	at := time.Date(2026, 9, 27, 12, 0, 0, 0, loc)
	good := writeRawAt(t, a, at, at, PrecisionExact, "well formed")

	shard := filepath.Join(a.Home(), rawDirName, "2026", "09")
	require.NoError(t, os.WriteFile(filepath.Join(shard, "raw_2026_09_27_13_00.md"),
		[]byte("---\nid: raw_2026_09_27_13_00\noccurred_at: sometime\noccurred_at_precision: exact\n---\nbody\n"), filePerm))
	require.NoError(t, os.WriteFile(filepath.Join(shard, "raw_2026_09_27_14_00.md"), []byte("no frontmatter"), filePerm))
	require.NoError(t, os.WriteFile(filepath.Join(shard, "notes.md"), []byte("not a raw entry"), filePerm))

	ids, err := a.rawIDsForDate("2026-09-27", observations.DefaultRolloverMin)
	require.NoError(t, err)
	assert.Equal(t, []string{good}, ids)
}

// TestRawIDsForDate_MemoizesOccurrences: a multi-day read parses each raw
// entry once per adapter — the second day's scan reuses the first's parsed
// occurrences (raw entries are immutable) and still groups correctly, while
// an entry that failed to parse is not memoized.
func TestRawIDsForDate_MemoizesOccurrences(t *testing.T) {
	a := newObsStore(t)
	first := time.Date(2026, 9, 27, 12, 0, 0, 0, loc)
	second := time.Date(2026, 9, 28, 12, 0, 0, 0, loc)
	firstID := writeRawAt(t, a, first, first, PrecisionExact, "first day")
	secondID := writeRawAt(t, a, second, second, PrecisionExact, "second day")
	shard := filepath.Join(a.Home(), rawDirName, "2026", "09")
	require.NoError(t, os.WriteFile(filepath.Join(shard, "raw_2026_09_28_13_00.md"), []byte("no frontmatter"), filePerm))

	ids, err := a.rawIDsForDate("2026-09-27", observations.DefaultRolloverMin)
	require.NoError(t, err)
	assert.Equal(t, []string{firstID}, ids)
	assert.Len(t, a.rawOccurrences, 2, "the well-formed entries are memoized; the unreadable one is not")

	ids, err = a.rawIDsForDate("2026-09-28", observations.DefaultRolloverMin)
	require.NoError(t, err)
	assert.Equal(t, []string{secondID}, ids)
	assert.Len(t, a.rawOccurrences, 2)
}
