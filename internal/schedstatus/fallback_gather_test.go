package schedstatus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	flywheel "github.com/mrz1836/go-flywheel"
	"github.com/mrz1836/go-foundation/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/mrz1836/lucid/internal/config"
)

// fixtureQueue is the queue every synthetic fallback-fixture job runs on.
const fixtureQueue = "fixture"

// oversizeOutputBytes pushes a fixture's run output past the job store's 64 KiB
// output cap, so the store keeps a truncation marker instead of the output.
const oversizeOutputBytes = 70 * 1024

// fallbackFixtureArgs is a synthetic job's args: the run output its worker hands
// back verbatim (a fallback marker, something else, or nothing), an oversize
// fallback marker the store must truncate, or an error that fails the attempt.
type fallbackFixtureArgs struct {
	Output   json.RawMessage `json:"output,omitempty"`
	Oversize bool            `json:"oversize,omitempty"`
	Fail     string          `json:"fail,omitempty"`
}

// fallbackFixtureWorker stands in for every fallback-capable daemon worker: it
// registers under the given kind and returns its args' output as the run's
// output, which is exactly where a real worker's fallback marker lands.
type fallbackFixtureWorker struct{ kind string }

func (w fallbackFixtureWorker) Kind() string { return w.kind }

func (fallbackFixtureWorker) Work(_ context.Context, job *flywheel.Job[fallbackFixtureArgs]) (flywheel.Result, error) {
	switch {
	case job.Args.Fail != "":
		return flywheel.Result{}, errors.New(job.Args.Fail)
	case job.Args.Oversize:
		return flywheel.Result{Output: map[string]any{
			"compose_fallback": true,
			"reason":           "provider_outage",
			"error":            strings.Repeat("e", oversizeOutputBytes),
		}}, nil
	case len(job.Args.Output) == 0:
		return flywheel.Result{}, nil
	default:
		return flywheel.Result{Output: job.Args.Output}, nil
	}
}

// fallbackSeed is one synthetic job to finalize in a fixture store: its kind,
// the instant it finalizes at, and its args.
type fallbackSeed struct {
	kind string
	at   time.Time
	args fallbackFixtureArgs
}

// markerSeed is a seed whose run output is the given raw JSON.
func markerSeed(kind string, at time.Time, output string) fallbackSeed {
	return fallbackSeed{kind: kind, at: at, args: fallbackFixtureArgs{Output: json.RawMessage(output)}}
}

// fallbackOutput renders a compose-fallback marker the way the daemons write it.
func fallbackOutput(t *testing.T, reason, errText string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"compose_fallback": true, "reason": reason, "error": errText})
	require.NoError(t, err)
	return string(raw)
}

// openFixtureStore opens (creating) a job store at path read-write with the same
// hardened pragmas the daemons open theirs with — WAL journaling included.
func openFixtureStore(t *testing.T, path string) *gorm.DB {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)&_txlock=immediate"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	return db
}

// closeFixtureStore closes a fixture handle so its WAL is checkpointed back into
// the store file before anything reads or hashes it.
func closeFixtureStore(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
}

// seedFallbackStore creates a migrated job store at path and drives each seed
// through a real flywheel runner under a fixed clock at its instant, so every
// job finalizes — succeeded with its output, or discarded on a failure — at
// exactly that time. It returns the job ids in seed order.
func seedFallbackStore(t *testing.T, path string, seeds ...fallbackSeed) []string {
	t.Helper()
	db := openFixtureStore(t, path)
	defer closeFixtureStore(t, db)
	require.NoError(t, flywheel.Migrate(db))

	reg := flywheel.NewRegistry()
	for _, kind := range []string{
		KindCompanionMorning, KindCompanionNight, KindCompanionMorningBackstop,
		KindWorkoutDaily, KindWitnessReportWeekly,
	} {
		flywheel.Register(reg, fallbackFixtureWorker{kind: kind})
	}
	runner, err := flywheel.NewRunner(flywheel.RunnerConfig{
		DB: db, Driver: flywheel.NewSQLiteDriver(db), Registry: reg,
		Queues: []string{fixtureQueue}, ClaimAnyClass: true, Concurrency: 1,
	})
	require.NoError(t, err)
	client := flywheel.NewClient(db)

	ids := make([]string, 0, len(seeds))
	for _, s := range seeds {
		ctx := models.WithClock(context.Background(), models.NewFixedClock(s.at))
		payload, err := json.Marshal(s.args)
		require.NoError(t, err)
		id, err := flywheel.Enqueue(ctx, client, s.kind, payload, flywheel.InsertOpts{Queue: fixtureQueue, MaxAttempts: 1})
		require.NoError(t, err)
		require.NoError(t, runner.RunUntilIdle(ctx))
		ids = append(ids, id)
	}
	return ids
}

// rewriteRunOutput overwrites a seeded job's run output with raw bytes — the
// test-only way to stand up a row whose output is not JSON at all.
func rewriteRunOutput(t *testing.T, path, jobID, raw string) {
	t.Helper()
	db := openFixtureStore(t, path)
	defer closeFixtureStore(t, db)
	res := db.Exec("UPDATE job_runs SET output = ? WHERE job_id = ?", raw, jobID)
	require.NoError(t, res.Error)
	require.Equal(t, int64(1), res.RowsAffected)
}

// readRun reads a seeded job's newest run back through the public read API.
func readRun(t *testing.T, path, jobID string) flywheel.JobRunView {
	t.Helper()
	db, err := openReadOnly(path)
	require.NoError(t, err)
	defer closeDB(db)
	run, ok, err := flywheel.LatestRun(context.Background(), db, jobID)
	require.NoError(t, err)
	require.True(t, ok)
	return run
}

// threeStores builds the three fallback store specs over the given paths,
// exactly as Gather builds them.
func threeStores(companionDB, workoutDB, witnessDB string) []FallbackStore {
	return fallbackStores(GatherParams{CompanionDB: companionDB, WorkoutDB: workoutDB, WitnessReportDB: witnessDB})
}

// fallbackNow is the synthetic instant the fallback window ends at.
func fallbackNow() time.Time { return time.Date(2026, 1, 8, 12, 0, 0, 0, time.UTC) }

// entryByJobKind finds the single entry for a job kind.
func entryByJobKind(t *testing.T, entries []ComposeFallback, jobKind string) ComposeFallback {
	t.Helper()
	var found []ComposeFallback
	for _, e := range entries {
		if e.JobKind == jobKind {
			found = append(found, e)
		}
	}
	require.Len(t, found, 1, "exactly one entry for %s", jobKind)
	return found[0]
}

// fileSHA256 hashes a file's bytes.
func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// TestParseFallbackMarker decodes run outputs: a marker with compose_fallback
// true is listed with its reason (verbatim, even an unknown one) and error; a
// false or absent flag, an empty, blank, or null output is not listed; and
// anything that does not decode into the marker, or that the store truncated, is
// skipped.
func TestParseFallbackMarker(t *testing.T) {
	cases := []struct {
		name      string
		output    string
		truncated bool
		wantOK    bool
		wantWhy   string
		wantError string
	}{
		{"provider outage", `{"compose_fallback":true,"reason":"provider_outage","error":"synthetic: provider unreachable"}`, false, true, "provider_outage", "synthetic: provider unreachable"},
		{"unusable reply", `{"compose_fallback":true,"reason":"unusable_reply","error":"synthetic: empty reply"}`, false, true, "unusable_reply", "synthetic: empty reply"},
		{"safety tripped", `{"compose_fallback":true,"reason":"safety_tripped","error":"synthetic: blocked"}`, false, true, "safety_tripped", "synthetic: blocked"},
		{"unknown reason verbatim", `{"compose_fallback":true,"reason":"novel_reason_v2","error":""}`, false, true, "novel_reason_v2", ""},
		{"padded marker", "  \n{\"compose_fallback\":true,\"reason\":\"unusable_reply\"}\n", false, true, "unusable_reply", ""},
		{"flag false", `{"compose_fallback":false,"reason":"provider_outage"}`, false, false, "", ""},
		{"flag absent", `{"reason":"provider_outage","error":"x"}`, false, false, "", ""},
		{"empty output", ``, false, false, "", ""},
		{"whitespace output", " \t\n ", false, false, "", ""},
		{"null output", `null`, false, false, "", ""},
		{"not json", `compose fell back: provider_outage`, false, false, "", ""},
		{"wrong-typed flag", `{"compose_fallback":"yes","reason":"provider_outage"}`, false, false, "", ""},
		{"top-level array", `[{"compose_fallback":true,"reason":"provider_outage"}]`, false, false, "", ""},
		{"truncated", `{"$truncated":true,"$bytes":70000,"$head":"{\"compose_fallback\":true"}`, true, false, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := flywheel.JobRunView{Output: json.RawMessage(tc.output), OutputTruncated: tc.truncated}
			reason, errText, ok := parseFallbackMarker(run)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.wantWhy, reason)
			assert.Equal(t, tc.wantError, errText)
		})
	}
}

// TestFallbackDaemon maps every fallback-capable kind to the daemon name its
// fallback is logged under — the backstop reporting as companion-morning — and
// returns an unknown kind verbatim.
func TestFallbackDaemon(t *testing.T) {
	cases := map[string]string{
		KindCompanionMorning:         "companion-morning",
		KindCompanionMorningBackstop: "companion-morning",
		KindCompanionNight:           "companion-night",
		KindWorkoutDaily:             "workout",
		KindWitnessReportWeekly:      "witness",
		"lucid_some_future_kind":     "lucid_some_future_kind",
		"":                           "",
	}
	for kind, want := range cases {
		assert.Equal(t, want, FallbackDaemon(kind), "kind %q", kind)
	}
}

// TestSuccessfulRun picks the newest applied success and ignores failed and
// superseded attempts.
func TestSuccessfulRun(t *testing.T) {
	_, ok := successfulRun(nil)
	assert.False(t, ok, "no runs, no successful attempt")

	runs := []flywheel.JobRunView{
		{ID: "r3", Attempt: 3, Outcome: string(flywheel.OutcomeSuccess), Superseded: true},
		{ID: "r2", Attempt: 2, Outcome: string(flywheel.OutcomeSuccess)},
		{ID: "r1", Attempt: 1, Outcome: string(flywheel.OutcomeError)},
	}
	run, ok := successfulRun(runs)
	require.True(t, ok)
	assert.Equal(t, "r2", run.ID, "a superseded success never finalized the job")

	_, ok = successfulRun([]flywheel.JobRunView{{ID: "r1", Outcome: string(flywheel.OutcomeError)}})
	assert.False(t, ok)
}

// TestFallbackEntry_SkipsUnreadableJob: a job with no finalize time, or whose
// runs cannot be read, is skipped on its own rather than failing the scan.
func TestFallbackEntry_SkipsUnreadableJob(t *testing.T) {
	path := filepath.Join(t.TempDir(), "companion.db")
	seedFallbackStore(t, path)
	db, err := openReadOnly(path)
	require.NoError(t, err)

	_, ok := fallbackEntry(context.Background(), db, storeCompanion, flywheel.JobView{ID: "job-1", Kind: KindCompanionMorning})
	assert.False(t, ok, "an unfinalized job is skipped")

	closeDB(db)
	fin := fallbackNow()
	_, ok = fallbackEntry(context.Background(), db, storeCompanion, flywheel.JobView{ID: "job-1", Kind: KindCompanionMorning, FinalizedAt: &fin})
	assert.False(t, ok, "a failed run read skips the job")
}

// TestGatherFallbacks_EachReason: every fallback-capable kind in each of the
// three stores surfaces with its daemon label, job kind, store, reason, error,
// and finalize time — RFC3339 UTC for the contract, the same instant in local
// time for the render.
func TestGatherFallbacks_EachReason(t *testing.T) {
	now := fallbackNow()
	dir := t.TempDir()
	companionDB := filepath.Join(dir, "companion.db")
	workoutDB := filepath.Join(dir, "workout.db")
	witnessDB := filepath.Join(dir, "witness-report.db")

	morningAt := now.Add(-26 * time.Hour)
	backstopAt := now.Add(-25 * time.Hour)
	nightAt := now.Add(-17 * time.Hour)
	workoutAt := now.Add(-3 * time.Hour)
	witnessAt := now.Add(-4 * 24 * time.Hour)
	seedFallbackStore(t, companionDB,
		markerSeed(KindCompanionMorning, morningAt, fallbackOutput(t, "provider_outage", "synthetic: provider unreachable")),
		markerSeed(KindCompanionMorningBackstop, backstopAt, fallbackOutput(t, "unusable_reply", "synthetic: empty reply")),
		markerSeed(KindCompanionNight, nightAt, fallbackOutput(t, "unusable_reply", "synthetic: reply too long")),
	)
	seedFallbackStore(t, workoutDB, markerSeed(KindWorkoutDaily, workoutAt, fallbackOutput(t, "provider_outage", "synthetic: timeout")))
	seedFallbackStore(t, witnessDB, markerSeed(KindWitnessReportWeekly, witnessAt, fallbackOutput(t, "safety_tripped", "")))

	got := GatherComposeFallbacks(context.Background(), now, threeStores(companionDB, workoutDB, witnessDB))
	require.Empty(t, got.Notes)
	require.Len(t, got.Entries, 5)

	cases := []struct {
		jobKind, kind, store, reason, errText string
		at                                    time.Time
	}{
		{KindCompanionMorning, "companion-morning", "companion", "provider_outage", "synthetic: provider unreachable", morningAt},
		{KindCompanionMorningBackstop, "companion-morning", "companion", "unusable_reply", "synthetic: empty reply", backstopAt},
		{KindCompanionNight, "companion-night", "companion", "unusable_reply", "synthetic: reply too long", nightAt},
		{KindWorkoutDaily, "workout", "workout", "provider_outage", "synthetic: timeout", workoutAt},
		{KindWitnessReportWeekly, "witness", "witness-report", "safety_tripped", "", witnessAt},
	}
	for _, tc := range cases {
		e := entryByJobKind(t, got.Entries, tc.jobKind)
		assert.Equal(t, tc.kind, e.Kind, tc.jobKind)
		assert.Equal(t, tc.store, e.Store, tc.jobKind)
		assert.Equal(t, tc.reason, e.Reason, tc.jobKind)
		assert.Equal(t, tc.errText, e.Error, tc.jobKind)
		assert.Equal(t, tc.at.UTC().Format(time.RFC3339), e.FinishedAt, tc.jobKind)
		assert.True(t, strings.HasSuffix(e.FinishedAt, "Z"), "finished_at is UTC: %s", e.FinishedAt)
		assert.True(t, e.At.Equal(tc.at), "%s: At %s equals the seeded finish %s", tc.jobKind, e.At, tc.at)
		assert.Equal(t, time.Local, e.At.Location(), "At is local for the text render") //nolint:gosmopolitan // asserts the localization gather performs
	}
}

// TestGatherFallbacks_SkipsNonFallbackRuns: a model-composed send (no output), a
// null output, and markers whose flag is false or absent produce no entries and
// no notes.
func TestGatherFallbacks_SkipsNonFallbackRuns(t *testing.T) {
	now := fallbackNow()
	dir := t.TempDir()
	companionDB := filepath.Join(dir, "companion.db")
	workoutDB := filepath.Join(dir, "workout.db")
	seedFallbackStore(t, companionDB,
		fallbackSeed{kind: KindCompanionMorning, at: now.Add(-2 * time.Hour)},
		markerSeed(KindCompanionNight, now.Add(-3*time.Hour), `{"compose_fallback":false,"reason":"provider_outage"}`),
		markerSeed(KindCompanionMorningBackstop, now.Add(-4*time.Hour), `{"reason":"unusable_reply","error":"synthetic"}`),
	)
	seedFallbackStore(t, workoutDB, markerSeed(KindWorkoutDaily, now.Add(-5*time.Hour), `null`))

	got := GatherComposeFallbacks(context.Background(), now, threeStores(companionDB, workoutDB, ""))
	assert.Empty(t, got.Entries, "no run carried a fallback marker")
	assert.Empty(t, got.Notes, "every store read cleanly")
}

// TestGatherFallbacks_SkipsMalformedOutput: a row whose output is not JSON, a
// wrong-typed marker, and an output past the store's cap (kept as a truncation
// marker) are each skipped without a note or an error, while a valid fallback in
// the same store is still listed.
func TestGatherFallbacks_SkipsMalformedOutput(t *testing.T) {
	now := fallbackNow()
	companionDB := filepath.Join(t.TempDir(), "companion.db")
	ids := seedFallbackStore(t, companionDB,
		markerSeed(KindCompanionMorning, now.Add(-2*time.Hour), fallbackOutput(t, "provider_outage", "synthetic: down")),
		markerSeed(KindCompanionNight, now.Add(-3*time.Hour), fallbackOutput(t, "unusable_reply", "")),
		markerSeed(KindCompanionNight, now.Add(-27*time.Hour), `{"compose_fallback":"yes","reason":"provider_outage"}`),
		fallbackSeed{kind: KindCompanionMorningBackstop, at: now.Add(-4 * time.Hour), args: fallbackFixtureArgs{Oversize: true}},
	)
	rewriteRunOutput(t, companionDB, ids[1], "compose fell back (not json)")

	// The fixtures are what they claim: the garbage row reads back raw, and the
	// oversize output was kept as a truncation marker.
	garbage := readRun(t, companionDB, ids[1])
	assert.Equal(t, "compose fell back (not json)", string(garbage.Output))
	assert.False(t, garbage.OutputTruncated)
	assert.True(t, readRun(t, companionDB, ids[3]).OutputTruncated, "an output past the cap is stored truncated")

	got := GatherComposeFallbacks(context.Background(), now, threeStores(companionDB, "", ""))
	assert.Empty(t, got.Notes, "a malformed row is skipped, never a store failure")
	require.Len(t, got.Entries, 1, "only the valid fallback is listed")
	assert.Equal(t, KindCompanionMorning, got.Entries[0].JobKind)
	assert.Equal(t, "provider_outage", got.Entries[0].Reason)
}

// TestGatherFallbacks_WindowBound: a fallback finalized 8 days before now is
// outside the 7-day window; one 6 days before is inside.
func TestGatherFallbacks_WindowBound(t *testing.T) {
	now := fallbackNow()
	workoutDB := filepath.Join(t.TempDir(), "workout.db")
	inside := now.Add(-6 * 24 * time.Hour)
	seedFallbackStore(t, workoutDB,
		markerSeed(KindWorkoutDaily, now.Add(-8*24*time.Hour), fallbackOutput(t, "provider_outage", "synthetic: old")),
		markerSeed(KindWorkoutDaily, inside, fallbackOutput(t, "unusable_reply", "synthetic: recent")),
	)

	got := GatherComposeFallbacks(context.Background(), now, threeStores("", workoutDB, ""))
	assert.Empty(t, got.Notes)
	require.Len(t, got.Entries, 1)
	assert.Equal(t, "unusable_reply", got.Entries[0].Reason)
	assert.True(t, got.Entries[0].At.Equal(inside))
}

// TestGatherFallbacks_MissingStoresAreSilent: a store that was never created
// (the feature was never enabled) and a blank, unresolvable path contribute no
// entries and no notes — and the scan creates no file.
func TestGatherFallbacks_MissingStoresAreSilent(t *testing.T) {
	dir := t.TempDir()
	workoutDB := filepath.Join(dir, "workout.db")
	witnessDB := filepath.Join(dir, "never", "witness-report.db")

	got := GatherComposeFallbacks(context.Background(), fallbackNow(), threeStores("   ", workoutDB, witnessDB))
	assert.Empty(t, got.Entries)
	assert.Empty(t, got.Notes, "a missing store is silent")

	for _, p := range []string{workoutDB, witnessDB, filepath.Dir(witnessDB)} {
		_, err := os.Stat(p)
		assert.True(t, os.IsNotExist(err), "the scan must not create %s", p)
	}
}

// TestGatherFallbacks_UnreadableStoreNotes: a store that exists but cannot be
// read — a directory, a file that is not a database, a database without a job
// table — contributes exactly one note naming the store and its path, and none
// of its entries; a healthy store beside it still yields its own.
func TestGatherFallbacks_UnreadableStoreNotes(t *testing.T) {
	now := fallbackNow()
	dir := t.TempDir()
	companionDB := filepath.Join(dir, "companion.db")
	seedFallbackStore(t, companionDB,
		markerSeed(KindCompanionNight, now.Add(-time.Hour), fallbackOutput(t, "provider_outage", "synthetic: down")))

	workoutDir := filepath.Join(dir, "workout.db")
	require.NoError(t, os.Mkdir(workoutDir, 0o755))
	witnessGarbage := filepath.Join(dir, "witness-report.db")
	require.NoError(t, os.WriteFile(witnessGarbage, []byte("this is not a sqlite database"), 0o600))

	got := GatherComposeFallbacks(context.Background(), now, threeStores(companionDB, workoutDir, witnessGarbage))
	require.Len(t, got.Entries, 1, "the healthy store still yields its entry")
	assert.Equal(t, "companion", got.Entries[0].Store)
	require.Len(t, got.Notes, 2, "one note per unreadable store")
	assert.True(t, strings.HasPrefix(got.Notes[0],
		"workout job store at "+workoutDir+" is unreadable — compose fallbacks not scanned ("), got.Notes[0])
	assert.Contains(t, got.Notes[0], "directory")
	assert.True(t, strings.HasPrefix(got.Notes[1],
		"witness-report job store at "+witnessGarbage+" is unreadable — compose fallbacks not scanned ("), got.Notes[1])
	assert.True(t, strings.HasSuffix(got.Notes[1], ")"))

	// A database that holds no job table fails its listing: one note too.
	bare := filepath.Join(dir, "bare.db")
	closeFixtureStore(t, openFixtureStore(t, bare))
	_, err := os.Stat(bare)
	require.NoError(t, err)
	got = GatherComposeFallbacks(context.Background(), now, threeStores("", bare, ""))
	assert.Empty(t, got.Entries)
	require.Len(t, got.Notes, 1)
	assert.Contains(t, got.Notes[0], "workout job store at "+bare+" is unreadable")
}

// TestGatherFallbacks_NeverWrites: the scan is read-only — each store file hashes
// identically before and after, and no write-ahead log with content is left
// beside it (an empty -wal/-shm side file is SQLite's, not store content).
func TestGatherFallbacks_NeverWrites(t *testing.T) {
	now := fallbackNow()
	dir := t.TempDir()
	companionDB := filepath.Join(dir, "companion.db")
	workoutDB := filepath.Join(dir, "workout.db")
	witnessDB := filepath.Join(dir, "witness-report.db")
	seedFallbackStore(t, companionDB, markerSeed(KindCompanionMorning, now.Add(-time.Hour), fallbackOutput(t, "provider_outage", "")))
	seedFallbackStore(t, workoutDB, markerSeed(KindWorkoutDaily, now.Add(-2*time.Hour), fallbackOutput(t, "unusable_reply", "")))
	seedFallbackStore(t, witnessDB, markerSeed(KindWitnessReportWeekly, now.Add(-3*time.Hour), fallbackOutput(t, "safety_tripped", "")))

	paths := []string{companionDB, workoutDB, witnessDB}
	before := make(map[string]string, len(paths))
	for _, p := range paths {
		before[p] = fileSHA256(t, p)
	}

	got := GatherComposeFallbacks(context.Background(), now, threeStores(companionDB, workoutDB, witnessDB))
	require.Len(t, got.Entries, 3, "the scan read every store")
	require.Empty(t, got.Notes)

	for _, p := range paths {
		assert.Equal(t, before[p], fileSHA256(t, p), "the scan must not write %s", filepath.Base(p))
		if info, err := os.Stat(p + "-wal"); err == nil {
			assert.Zero(t, info.Size(), "no write-ahead log content is left beside %s", filepath.Base(p))
		}
	}
}

// TestGatherFallbacks_FailureViewsUnchanged: a fallback is a successful run, so
// it never appears in the failure views — the companion store's recent failures
// list only the discarded job, the fallback is listed as an entry, and the
// assembled run summary counts one failure.
func TestGatherFallbacks_FailureViewsUnchanged(t *testing.T) {
	now := fallbackNow()
	companionDB := filepath.Join(t.TempDir(), "companion.db")
	seedFallbackStore(t, companionDB,
		markerSeed(KindCompanionMorning, now.Add(-2*time.Hour), fallbackOutput(t, "provider_outage", "synthetic: down")),
		fallbackSeed{kind: KindCompanionNight, at: now.Add(-time.Hour), args: fallbackFixtureArgs{Fail: "synthetic delivery failure"}},
	)

	db := GatherDB(context.Background(), companionDB)
	require.Empty(t, db.Err)
	require.Len(t, db.Failures, 1, "only the discarded job is a failure")
	assert.Equal(t, KindCompanionNight, db.Failures[0].Kind)

	in, err := Gather(context.Background(), GatherParams{
		Config: config.Default(), Store: seededLedger(t), CompanionDB: companionDB, Now: now,
	})
	require.NoError(t, err)
	require.Len(t, in.CompanionJobs.Failures, 1)
	require.Len(t, in.ComposeFallbacks.Entries, 1, "the fallback is listed as an entry")
	assert.Equal(t, KindCompanionMorning, in.ComposeFallbacks.Entries[0].JobKind)

	rep := Assemble(in, now)
	assert.Equal(t, 1, rep.Runs.FailureCount, "the fallback still counts as a success")
	require.Len(t, rep.Runs.Failures, 1)
	assert.Equal(t, KindCompanionNight, rep.Runs.Failures[0].Kind)
	assert.Equal(t, 1, rep.ComposeFallbacks.Count)
}
