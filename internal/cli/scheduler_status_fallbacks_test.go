package cli

import (
	"context"
	"encoding/json"
	"fmt"
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

	"github.com/mrz1836/lucid/internal/schedstatus"
)

// statusFallbackQueue is the queue every synthetic fallback-fixture job runs on.
const statusFallbackQueue = "fixture"

// statusTimeLayout mirrors the status report's text time layout, so an expected
// fallback line is built exactly as the report renders it.
const statusTimeLayout = "2006-01-02 15:04 MST"

// Synthetic provider error details a fixture marker carries. They belong in the
// --json entries only and must never reach the text report.
const (
	syntheticOutageError   = "synthetic provider error: endpoint unreachable"
	syntheticUnusableError = "synthetic provider error: reply could not be used"
)

// statusFallbackArgs is a synthetic job's args: the run output its worker hands
// back verbatim, which is exactly where a real worker's fallback marker lands.
type statusFallbackArgs struct {
	Output json.RawMessage `json:"output"`
}

// statusFallbackWorker stands in for every fallback-capable daemon worker: it
// registers under the given kind and returns its args' output as the run output.
type statusFallbackWorker struct{ kind string }

func (w statusFallbackWorker) Kind() string { return w.kind }

func (statusFallbackWorker) Work(_ context.Context, job *flywheel.Job[statusFallbackArgs]) (flywheel.Result, error) {
	return flywheel.Result{Output: job.Args.Output}, nil
}

// statusFallbackSeed is one delivered-but-degraded send to finalize in a fixture
// store: its job kind, the instant it finalizes at, and the marker's reason and
// error detail.
type statusFallbackSeed struct {
	kind    string
	at      time.Time
	reason  string
	errText string
}

// seedStatusFallbacks migrates (creating if needed) the job store at path and
// drives each seed through a real flywheel runner under a fixed clock at its
// instant, so every job succeeds with a compose-fallback marker as its output
// and finalizes at exactly that time. The handle is closed before returning so
// the store file is complete before the command reads it.
func seedStatusFallbacks(t *testing.T, path string, seeds ...statusFallbackSeed) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)&_txlock=immediate"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	defer func() {
		sqlDB, derr := db.DB()
		require.NoError(t, derr)
		require.NoError(t, sqlDB.Close())
	}()
	require.NoError(t, flywheel.Migrate(db))

	reg := flywheel.NewRegistry()
	for _, s := range seeds {
		flywheel.Register(reg, statusFallbackWorker{kind: s.kind})
	}
	runner, err := flywheel.NewRunner(flywheel.RunnerConfig{
		DB: db, Driver: flywheel.NewSQLiteDriver(db), Registry: reg,
		Queues: []string{statusFallbackQueue}, ClaimAnyClass: true, Concurrency: 1,
	})
	require.NoError(t, err)
	client := flywheel.NewClient(db)

	for _, s := range seeds {
		marker, merr := json.Marshal(map[string]any{"compose_fallback": true, "reason": s.reason, "error": s.errText})
		require.NoError(t, merr)
		payload, perr := json.Marshal(statusFallbackArgs{Output: marker})
		require.NoError(t, perr)
		ctx := models.WithClock(context.Background(), models.NewFixedClock(s.at))
		_, err = flywheel.Enqueue(ctx, client, s.kind, payload, flywheel.InsertOpts{Queue: statusFallbackQueue, MaxAttempts: 1})
		require.NoError(t, err)
		require.NoError(t, runner.RunUntilIdle(ctx))
	}
}

// relocateFallbackStores points the witness-report and workout job-store env
// overrides at fresh temp paths and returns them, so a test can seed the exact
// stores the compose-fallback scan reads.
func relocateFallbackStores(t *testing.T) (witnessDB, workoutDB string) {
	t.Helper()
	witnessDB = filepath.Join(t.TempDir(), "witness-report.db")
	workoutDB = filepath.Join(t.TempDir(), "workout.db")
	t.Setenv("LUCID_WITNESS_REPORT_DB", witnessDB)
	t.Setenv("LUCID_WORKOUT_DB", workoutDB)
	return witnessDB, workoutDB
}

// writeNotAJobStore writes a regular file that is not a database at a fresh
// temp path — a store that exists but cannot be read.
func writeNotAJobStore(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "workout.db")
	require.NoError(t, os.WriteFile(path, []byte("synthetic: this file is not a job store\n"), 0o600))
	return path
}

// fallbackLine renders the text line the report prints for one fallback, in the
// host's local zone exactly as the report prints run times.
func fallbackLine(at time.Time, kind, reason string) string {
	local := at.Local() //nolint:gosmopolitan // the report prints run times in the host's local zone
	return fmt.Sprintf("  %s  %s  %s", local.Format(statusTimeLayout), kind, reason)
}

// lineIndex returns the index of the first line with the given prefix, or -1.
func lineIndex(lines []string, prefix string) int {
	for i, l := range lines {
		if strings.HasPrefix(l, prefix) {
			return i
		}
	}
	return -1
}

// statusFallbacksDoc decodes the parts of `lucid scheduler status --json` the
// compose-fallback tests assert on. Entries and checks stay raw so a test can
// inspect key presence and compare whole documents.
type statusFallbacksDoc struct {
	Verdict   string          `json:"verdict"`
	Checks    json.RawMessage `json:"checks"`
	JobStores []struct {
		Name string `json:"name"`
		Path string `json:"path"`
	} `json:"job_stores"`
	ComposeFallbacks struct {
		Window  string          `json:"window"`
		Count   int             `json:"count"`
		Entries json.RawMessage `json:"entries"`
		Notes   []string        `json:"notes"`
	} `json:"compose_fallbacks"`
}

// decodeStatusFallbacks decodes a --json status document and its fallback
// entries (as key/value maps, so an omitted key is observable).
func decodeStatusFallbacks(t *testing.T, out string) (statusFallbacksDoc, []map[string]any) {
	t.Helper()
	var doc statusFallbacksDoc
	require.NoError(t, json.Unmarshal([]byte(out), &doc))
	var entries []map[string]any
	require.NoError(t, json.Unmarshal(doc.ComposeFallbacks.Entries, &entries))
	return doc, entries
}

// TestSchedulerStatus_ComposeFallbacks_TextAndJSON proves the section end to
// end: one fallback in each of the companion, workout, and witness-report stores
// (the latter two relocated by env) is listed after `Recent runs:` as
// `<time>  <kind>  <reason>`, newest first, with no error detail in the text,
// while `--json` carries the full entries — error only where the marker set one —
// under a top-level compose_fallbacks object. The job-store block names the very
// paths the scan read, and a healthy verdict stays ok.
func TestSchedulerStatus_ComposeFallbacks_TextAndJSON(t *testing.T) {
	home, schedulerDB, companionDB := seedScheduler(t, true, "PROMPT BODY")
	seedHealthyDBs(t, home, schedulerDB, companionDB)
	witnessDB, workoutDB := relocateFallbackStores(t)

	now := statusMorning()
	morningAt := now.Add(-55 * time.Minute)
	workoutAt := now.Add(-22 * time.Hour)
	witnessAt := now.Add(-3 * 24 * time.Hour)
	seedStatusFallbacks(t, companionDB, statusFallbackSeed{schedstatus.KindCompanionMorning, morningAt, "provider_outage", syntheticOutageError})
	seedStatusFallbacks(t, workoutDB, statusFallbackSeed{schedstatus.KindWorkoutDaily, workoutAt, "unusable_reply", syntheticUnusableError})
	seedStatusFallbacks(t, witnessDB, statusFallbackSeed{schedstatus.KindWitnessReportWeekly, witnessAt, "safety_tripped", ""})
	withClock(t, now)
	withHostProbe(t, unknownHost()...)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "status")
	require.NoError(t, err, "fallbacks never lower a healthy verdict")
	assert.Contains(t, out, "Scheduler status: OK")

	lines := strings.Split(out, "\n")
	runsAt := lineIndex(lines, "Recent runs:")
	require.GreaterOrEqual(t, runsAt, 0)
	require.Greater(t, len(lines), runsAt+5)
	assert.Equal(t, "Recent compose fallbacks: 3 in the last 7d", lines[runsAt+1], "the section sits directly after Recent runs")
	assert.Equal(t, []string{
		fallbackLine(morningAt, "companion-morning", "provider_outage"),
		fallbackLine(workoutAt, "workout", "unusable_reply"),
		fallbackLine(witnessAt, "witness", "safety_tripped"),
	}, lines[runsAt+2:runsAt+5], "one line per fallback, newest first")
	assert.Equal(t, "Host:", lines[runsAt+5], "the section ends before Host")
	assert.NotContains(t, out, syntheticOutageError, "the error detail is JSON-only")
	assert.NotContains(t, out, syntheticUnusableError, "the error detail is JSON-only")
	assert.NotContains(t, out, "Issues:", "a fallback is never an issue")

	jsonOut, _, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "status", "--json")
	require.NoError(t, err)
	doc, entries := decodeStatusFallbacks(t, jsonOut)
	assert.Equal(t, "ok", doc.Verdict)
	assert.Equal(t, "168h", doc.ComposeFallbacks.Window)
	assert.Equal(t, 3, doc.ComposeFallbacks.Count)
	assert.Empty(t, doc.ComposeFallbacks.Notes)
	require.Len(t, entries, 3)

	assert.Equal(t, map[string]any{
		"kind": "companion-morning", "job_kind": schedstatus.KindCompanionMorning, "store": "companion",
		"finished_at": morningAt.UTC().Format(time.RFC3339), "reason": "provider_outage", "error": syntheticOutageError,
	}, entries[0])
	assert.Equal(t, map[string]any{
		"kind": "workout", "job_kind": schedstatus.KindWorkoutDaily, "store": "workout",
		"finished_at": workoutAt.UTC().Format(time.RFC3339), "reason": "unusable_reply", "error": syntheticUnusableError,
	}, entries[1])
	assert.Equal(t, map[string]any{
		"kind": "witness", "job_kind": schedstatus.KindWitnessReportWeekly, "store": "witness-report",
		"finished_at": witnessAt.UTC().Format(time.RFC3339), "reason": "safety_tripped",
	}, entries[2], "an empty error is omitted")

	paths := map[string]string{}
	for _, s := range doc.JobStores {
		paths[s.Name] = s.Path
	}
	assert.Equal(t, companionDB, paths["companion"], "the companion path printed is the one scanned")
	assert.Equal(t, workoutDB, paths["workout"], "the workout path printed is the one scanned")
	assert.Equal(t, witnessDB, paths["witness-report"], "the witness-report path printed is the one scanned")
}

// TestSchedulerStatus_ComposeFallbacks_NoneByDefault: a healthy scheduler with
// no fallback runs prints the explicit empty-state line and an empty (never null)
// entries array. Because the test suite relocates every job store, this also
// proves nothing leaks in from this host's real stores — and the scan creates
// none of the absent stores it was pointed at.
func TestSchedulerStatus_ComposeFallbacks_NoneByDefault(t *testing.T) {
	home, schedulerDB, companionDB := seedScheduler(t, true, "PROMPT BODY")
	seedHealthyDBs(t, home, schedulerDB, companionDB)
	withClock(t, statusMorning())
	withHostProbe(t, unknownHost()...)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "status")
	require.NoError(t, err)
	assert.Contains(t, strings.Split(out, "\n"), "Recent compose fallbacks: none in the last 7d")
	assert.NotContains(t, out, "note:")

	jsonOut, _, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "status", "--json")
	require.NoError(t, err)
	doc, entries := decodeStatusFallbacks(t, jsonOut)
	assert.Equal(t, "168h", doc.ComposeFallbacks.Window)
	assert.Equal(t, 0, doc.ComposeFallbacks.Count)
	assert.JSONEq(t, "[]", string(doc.ComposeFallbacks.Entries), "entries is an empty array, never null")
	assert.Empty(t, entries)

	var raw struct {
		ComposeFallbacks map[string]json.RawMessage `json:"compose_fallbacks"`
	}
	require.NoError(t, json.Unmarshal([]byte(jsonOut), &raw))
	assert.NotContains(t, raw.ComposeFallbacks, "notes", "notes is omitted when every store read cleanly")

	assert.NoFileExists(t, os.Getenv("LUCID_WORKOUT_DB"), "the scan never creates an absent store")
	assert.NoFileExists(t, os.Getenv("LUCID_WITNESS_REPORT_DB"), "the scan never creates an absent store")
}

// statusOutcome is what a health cron or reader acts on, captured from one text
// run and one --json run of `scheduler status`: both exit codes, the text verdict
// line and issues block, and the JSON verdict and flat check list. The fallback
// count and notes are captured too, only to prove a "with fallbacks" run really
// had some.
type statusOutcome struct {
	textCode, jsonCode int
	header             string
	issues             []string
	verdict            string
	checks             string
	fallbackCount      int
	notes              int
}

// captureStatusOutcome runs `scheduler status` in text and --json mode and
// captures the [statusOutcome].
func captureStatusOutcome(t *testing.T) statusOutcome {
	t.Helper()
	out, _, textErr := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "status")
	jsonOut, _, jsonErr := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "status", "--json")
	doc, _ := decodeStatusFallbacks(t, jsonOut)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	var issues []string
	if at := lineIndex(lines, "Issues:"); at >= 0 {
		issues = lines[at:]
	}
	return statusOutcome{
		textCode:      exitCodeForError(textErr),
		jsonCode:      exitCodeForError(jsonErr),
		header:        lines[0],
		issues:        issues,
		verdict:       doc.Verdict,
		checks:        string(doc.Checks),
		fallbackCount: doc.ComposeFallbacks.Count,
		notes:         len(doc.ComposeFallbacks.Notes),
	}
}

// TestSchedulerStatus_ComposeFallbacks_VerdictAndExitUnchanged locks in that
// fallbacks are informational: for a healthy (ok) and a companion-disabled
// (warn) scheduler, a run with fallbacks in the window — plus an unreadable
// workout store — exits identically and reports the identical verdict, checks,
// and issues as the same scheduler without them, in text and in --json. The
// redeploy self-heal gates on exit 0, so a fallback must never move it.
func TestSchedulerStatus_ComposeFallbacks_VerdictAndExitUnchanged(t *testing.T) {
	tests := []struct {
		name     string
		wantCode int
		setup    func(t *testing.T) (companionDB string)
	}{
		{
			name:     "healthy",
			wantCode: ExitOK,
			setup: func(t *testing.T) string {
				home, schedulerDB, companionDB := seedScheduler(t, true, "PROMPT BODY")
				seedHealthyDBs(t, home, schedulerDB, companionDB)
				return companionDB
			},
		},
		{
			name:     "companion disabled warn",
			wantCode: 1,
			setup: func(t *testing.T) string {
				_, schedulerDB, _ := seedScheduler(t, false, "PROMPT BODY")
				seedStatusJobDB(
					t, schedulerDB,
					flywheel.PeriodicSpec{Slug: schedstatus.SlugTripwire, Kind: "lucid_tripwire", Cron: "0 6 * * *", Queue: "lucid", Active: true},
					flywheel.PeriodicSpec{Slug: schedstatus.SlugBell, Kind: "lucid_bell", Cron: "0 19 * * *", Queue: "lucid", Active: true},
				)
				// The companion store stays absent here: creating it would change
				// the companion classification for reasons unrelated to fallbacks.
				return ""
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			companionDB := tc.setup(t)
			now := statusMorning()
			withClock(t, now)
			withHostProbe(t, unknownHost()...)

			without := captureStatusOutcome(t)
			require.Equal(t, tc.wantCode, without.textCode)
			require.Zero(t, without.fallbackCount)

			witnessDB, _ := relocateFallbackStores(t)
			want := 1
			seedStatusFallbacks(t, witnessDB, statusFallbackSeed{schedstatus.KindWitnessReportWeekly, now.Add(-2 * 24 * time.Hour), "provider_outage", syntheticOutageError})
			if companionDB != "" {
				seedStatusFallbacks(t, companionDB, statusFallbackSeed{schedstatus.KindCompanionNight, now.Add(-12 * time.Hour), "unusable_reply", ""})
				want++
			}
			t.Setenv("LUCID_WORKOUT_DB", writeNotAJobStore(t))

			with := captureStatusOutcome(t)
			require.Equal(t, want, with.fallbackCount, "the with-fallbacks run really lists fallbacks")
			require.Equal(t, 1, with.notes, "and really notes the unreadable store")

			assert.Equal(t, without.textCode, with.textCode, "text exit code unchanged")
			assert.Equal(t, without.jsonCode, with.jsonCode, "--json exit code unchanged")
			assert.Equal(t, with.textCode, with.jsonCode, "text and --json still exit identically")
			assert.Equal(t, without.header, with.header, "text verdict unchanged")
			assert.Equal(t, without.issues, with.issues, "text issues unchanged")
			assert.Equal(t, without.verdict, with.verdict, "JSON verdict unchanged")
			assert.JSONEq(t, without.checks, with.checks, "JSON checks unchanged")
		})
	}
}

// TestSchedulerStatus_ComposeFallbacks_UnreadableStoreNote: a workout store that
// exists but is not a database yields exactly one note naming the store and its
// path — in the text section and in the JSON notes — and the healthy verdict and
// exit code stand.
func TestSchedulerStatus_ComposeFallbacks_UnreadableStoreNote(t *testing.T) {
	home, schedulerDB, companionDB := seedScheduler(t, true, "PROMPT BODY")
	seedHealthyDBs(t, home, schedulerDB, companionDB)
	notADB := writeNotAJobStore(t)
	t.Setenv("LUCID_WORKOUT_DB", notADB)
	withClock(t, statusMorning())
	withHostProbe(t, unknownHost()...)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "status")
	require.NoError(t, err, "an unreadable store never lowers the verdict")
	assert.Equal(t, ExitOK, exitCodeForError(err))
	assert.Contains(t, out, "Scheduler status: OK")

	var notes []string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "note:") {
			notes = append(notes, l)
		}
	}
	require.Len(t, notes, 1, "exactly one note")
	assert.True(t, strings.HasPrefix(notes[0], "  note: workout job store at "+notADB+" is unreadable"), "note names the store and path: %q", notes[0])

	jsonOut, _, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "status", "--json")
	require.NoError(t, err)
	doc, entries := decodeStatusFallbacks(t, jsonOut)
	assert.Equal(t, "ok", doc.Verdict)
	require.Len(t, doc.ComposeFallbacks.Notes, 1)
	assert.Contains(t, doc.ComposeFallbacks.Notes[0], "workout job store at "+notADB+" is unreadable")
	assert.Equal(t, 0, doc.ComposeFallbacks.Count)
	assert.Empty(t, entries)
}
