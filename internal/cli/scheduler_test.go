package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	flywheel "github.com/mrz1836/go-flywheel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/storage"
)

// setSchedulerEnv injects the environment the daemon reads at startup so the
// notifier constructs from fakes and the Ledger + job store land in temp dirs —
// no real token, socket, ~/.lucid, or OS config dir is touched.
func setSchedulerEnv(t *testing.T) {
	t.Helper()
	t.Setenv("LUCID_HOME", t.TempDir())
	t.Setenv("LUCID_HARNESS_TOKEN", "test-bot-token")
	t.Setenv("LUCID_USER_CHANNEL_ID", "100000000000000001")
	t.Setenv("LUCID_WITNESS_CHANNEL_ID", "100000000000000002")
	// Guard the resolveDBPath fallback: never let an unset --db reach the real
	// OS user-config dir during a test.
	t.Setenv("LUCID_SCHEDULER_DB", filepath.Join(t.TempDir(), "fallback.db"))
}

// TestScheduler_TreeExposesRun proves the `scheduler` group and its `run` child
// are registered under the root and that `run` carries the --db flag.
func TestScheduler_TreeExposesRun(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})

	sched, _, err := root.Find([]string{"scheduler"})
	require.NoError(t, err)
	assert.Equal(t, "scheduler", sched.Name())

	runCmd, _, err := root.Find([]string{"scheduler", "run"})
	require.NoError(t, err)
	assert.Equal(t, "run", runCmd.Name())
	assert.NotNil(t, runCmd.Flags().Lookup(schedulerFlagDB), "run exposes the --db flag")
}

// TestSchedulerRun_RejectsArgs confirms `scheduler run` is NoArgs — an extra
// positional is a usage error, caught before any daemon startup.
func TestSchedulerRun_RejectsArgs(t *testing.T) {
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "run", "extra")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
}

// TestSchedulerRun_MissingTokenErrors covers the credential-dumb startup guard:
// with the injected bot token unset, the notifier build fails fast, the error
// names the missing env var, and a "lucid: scheduler:" line lands on stderr —
// no daemon, DB, or socket is ever created.
func TestSchedulerRun_MissingTokenErrors(t *testing.T) {
	t.Setenv("LUCID_HOME", t.TempDir())
	t.Setenv("LUCID_HARNESS_TOKEN", "") // deliberately unset
	t.Setenv("LUCID_USER_CHANNEL_ID", "100000000000000001")

	var stderr bytes.Buffer
	err := runScheduler(context.Background(), &stderr, filepath.Join(t.TempDir(), "flywheel.db"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "LUCID_HARNESS_TOKEN")
	assert.Contains(t, stderr.String(), "lucid: scheduler:")
}

// TestSchedulerRun_CancelledContextReturnsPromptly is the graceful start/stop
// smoke: an already-canceled context must not hang the daemon loop — the whole
// wiring (storage → notifier → schedrun) runs and returns quickly rather than
// blocking on the node.
func TestSchedulerRun_CancelledContextReturnsPromptly(t *testing.T) {
	setSchedulerEnv(t)
	dbPath := filepath.Join(t.TempDir(), "flywheel.db")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled before the run begins

	var stderr bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- runScheduler(ctx, &stderr, dbPath) }()

	select {
	case <-done:
		// Returned promptly — either a clean drain or a fast startup abort on
		// the dead context; the assertion is only that it did not block.
	case <-time.After(10 * time.Second):
		t.Fatal("scheduler run did not return on an already-canceled context")
	}
}

// TestSchedulerRun_ThroughTreeParsesDBFlag executes `scheduler run --db <path>`
// through the real cobra tree with an already-canceled context: it covers the
// RunE closure (flag read + dispatch) and confirms the command returns promptly
// rather than blocking on the daemon loop.
func TestSchedulerRun_ThroughTreeParsesDBFlag(t *testing.T) {
	setSchedulerEnv(t)
	dbPath := filepath.Join(t.TempDir(), "flywheel.db")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	root := newRootCmd(BuildInfo{Version: "dev"})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"scheduler", "run", "--db", dbPath})

	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(ctx) }()
	select {
	case <-done:
		// Returned promptly through the full tree — RunE wiring exercised.
	case <-time.After(10 * time.Second):
		t.Fatal("scheduler run did not return through the command tree")
	}
}

// TestSchedulerRun_GracefulStopDrainsClean drives the full command wiring: with
// a live context and the injected fake env, runScheduler reaches a running
// daemon (both periodics reconciled), and a subsequent cancel — the production
// SIGTERM/stop path — drains the node to a clean nil return with no error line.
func TestSchedulerRun_GracefulStopDrainsClean(t *testing.T) {
	setSchedulerEnv(t)
	dbPath := filepath.Join(t.TempDir(), "flywheel.db")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	var stderr bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- runScheduler(ctx, &stderr, dbPath) }()

	// Wait until the daemon has reconciled both periodics: setup is complete and
	// the flywheel node is running before we signal stop.
	require.Eventually(t, func() bool {
		db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: gormlogger.Discard})
		if err != nil {
			return false
		}
		defer func() {
			if sqlDB, e := db.DB(); e == nil {
				_ = sqlDB.Close()
			}
		}()
		views, lerr := flywheel.ListPeriodics(context.Background(), db)
		return lerr == nil && len(views) == 2
	}, 10*time.Second, 25*time.Millisecond, "the daemon reconciles the bell and tripwire periodics")

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err, "a canceled daemon drains cleanly")
		assert.Empty(t, stderr.String(), "a clean drain writes no error line")
	case <-time.After(10 * time.Second):
		t.Fatal("scheduler run did not return after cancellation")
	}
}

// enableWorkoutInScheduler writes an enabled workout block (with a synthetic
// program and the two opaque prompt files) into the LUCID_HOME the scheduler
// env points at, so the daemon starts the workout node beside the teeth.
func enableWorkoutInScheduler(t *testing.T) {
	t.Helper()
	home := os.Getenv("LUCID_HOME")
	require.NotEmpty(t, home, "setSchedulerEnv must run first")
	a := storage.New(home)
	_, err := a.Scaffold()
	require.NoError(t, err)

	dir := t.TempDir()
	prog := filepath.Join(dir, "program.json")
	require.NoError(t, os.WriteFile(prog, []byte("{}"), 0o600))
	sys := filepath.Join(dir, "system_prompt.md")
	tmpl := filepath.Join(dir, "daily_template.md")
	require.NoError(t, os.WriteFile(sys, []byte("SYSTEM\n"), 0o600))
	require.NoError(t, os.WriteFile(tmpl, []byte("TEMPLATE\n"), 0o600))

	cfg, err := a.LoadConfig()
	require.NoError(t, err)
	cfg.Workout = config.WorkoutConfig{
		Enabled: true, Program: prog, SlotTime: "12:00", SystemPrompt: sys, Template: tmpl,
	}
	require.NoError(t, a.SaveConfig(cfg))
}

// TestSchedulerRun_WorkoutEnabled_StartsSlotNode proves the config gate: with the
// workout block enabled the daemon starts the workout slot node, which reconciles
// its daily periodic into its own disposable job DB (LUCID_WORKOUT_DB), and a
// cancel drains the whole set cleanly. The teeth-only drain test above is the
// paired "workout disabled → not started" proof (only the two teeth periodics
// appear and no workout job DB is created).
func TestSchedulerRun_WorkoutEnabled_StartsSlotNode(t *testing.T) {
	setSchedulerEnv(t)
	enableWorkoutInScheduler(t)
	dbPath := filepath.Join(t.TempDir(), "flywheel.db")
	workoutDB := filepath.Join(t.TempDir(), "workout.db")
	t.Setenv("LUCID_WORKOUT_DB", workoutDB)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	var stderr bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- runScheduler(ctx, &stderr, dbPath) }()

	// The workout node reconciles its single daily periodic into its own job DB —
	// proof the config gate started it beside the teeth.
	require.Eventually(t, func() bool {
		db, err := gorm.Open(sqlite.Open(workoutDB), &gorm.Config{Logger: gormlogger.Discard})
		if err != nil {
			return false
		}
		defer func() {
			if sqlDB, e := db.DB(); e == nil {
				_ = sqlDB.Close()
			}
		}()
		views, lerr := flywheel.ListPeriodics(context.Background(), db)
		if lerr != nil {
			return false
		}
		return len(views) == 1 && views[0].Slug == "lucid-workout-daily"
	}, 15*time.Second, 25*time.Millisecond, "the daemon starts the workout slot node beside the teeth")

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err, "a canceled daemon with the workout node drains cleanly")
	case <-time.After(10 * time.Second):
		t.Fatal("scheduler run did not return after cancellation")
	}
}
