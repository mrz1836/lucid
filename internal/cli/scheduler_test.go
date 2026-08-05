package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	"github.com/mrz1836/lucid/internal/schedstatus"
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

// slugsIn lists the periodic slugs a job store holds, or nil while the store is
// still being created — the poll predicate the daemon drain tests wait on before
// they signal a stop.
func slugsIn(path string) []string {
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		return nil
	}
	defer func() {
		if sqlDB, e := db.DB(); e == nil {
			_ = sqlDB.Close()
		}
	}()
	views, lerr := flywheel.ListPeriodics(context.Background(), db)
	if lerr != nil {
		return nil
	}
	slugs := make([]string, 0, len(views))
	for _, v := range views {
		slugs = append(slugs, v.Slug)
	}
	return slugs
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
		return lerr == nil && len(views) == 3
	}, 10*time.Second, 25*time.Millisecond, "the daemon reconciles the bell, backstop, and tripwire periodics")

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
// env points at, so the daemon starts the workout node beside the engine.
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
// cancel drains the whole set cleanly. The engine-only drain test above is the
// paired "workout disabled → not started" proof (only the two engine periodics
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
	// proof the config gate started it beside the engine. Both nodes must be up
	// before the cancel: the two start concurrently, and the workout node reaches
	// its (single, smaller) store first often enough that canceling on its
	// periodic alone would routinely interrupt the engine mid-startup and surface a
	// context-canceled error where this test asserts a clean drain.
	require.Eventually(t, func() bool {
		return len(slugsIn(workoutDB)) == 1 && len(slugsIn(dbPath)) == 3
	}, 15*time.Second, 25*time.Millisecond, "the daemon starts the workout slot node beside the engine")
	assert.Equal(t, []string{"lucid-workout-daily"}, slugsIn(workoutDB), "the slot node reconciles its own daily periodic")

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err, "a canceled daemon with the workout node drains cleanly")
	case <-time.After(10 * time.Second):
		t.Fatal("scheduler run did not return after cancellation")
	}
}

// ── scheduler reconcile ──────────────────────────────────────────────────────

// reconcileJSON mirrors the documented `lucid scheduler reconcile --json` shape:
// what was re-armed, and how many definitions were inspected.
//
// It is deliberately the *pre-two-store* shape, with no knowledge of the `store`
// field or the `stores` array added beside it. Decoding the current output through
// it is what proves automation written against the older shape still parses.
type reconcileJSON struct {
	Reconciled []struct {
		Slug        string `json:"slug"`
		WasActive   bool   `json:"was_active"`
		NextRun     string `json:"next_run"`
		FiresMissed bool   `json:"fires_missed"`
	} `json:"reconciled"`
	Scanned int `json:"scanned"`
}

// reconcileStoresJSON is the grown shape: every store the pass covered, and the
// store each re-armed row came from.
type reconcileStoresJSON struct {
	Stores []struct {
		Name string `json:"name"`
		Path string `json:"path"`
		Note string `json:"note"`
	} `json:"stores"`
	Reconciled []struct {
		Store       string `json:"store"`
		Slug        string `json:"slug"`
		WasActive   bool   `json:"was_active"`
		NextRun     string `json:"next_run"`
		FiresMissed bool   `json:"fires_missed"`
	} `json:"reconciled"`
	Scanned int `json:"scanned"`
}

// seedReconcileEnv points the command at an isolated Ledger whose companion owns
// the evening window, plus an engine job store holding the three production
// definitions with the tripwire parked — the exact shape the repair lever exists
// for: one send switched off, one deliberately suppressed, one healthy.
func seedReconcileEnv(t *testing.T) string {
	t.Helper()
	_, schedulerDB, _ := seedScheduler(t, true, "PROMPT\n")
	seedStatusJobDB(
		t, schedulerDB,
		flywheel.PeriodicSpec{Slug: schedstatus.SlugTripwire, Kind: "lucid_tripwire", Cron: "0 6 * * *", Queue: "lucid", Active: false},
		flywheel.PeriodicSpec{Slug: schedstatus.SlugBell, Kind: "lucid_bell", Cron: "0 19 * * *", Queue: "lucid", Active: false},
		flywheel.PeriodicSpec{Slug: "lucid-bell-fallback", Kind: "lucid_bell_fallback", Cron: "0 23 * * *", Queue: "lucid", Active: true},
	)
	return schedulerDB
}

// readPeriodicActive reads the active flags back out of a job store on disk.
func readPeriodicActive(t *testing.T, path string) map[string]bool {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	defer func() {
		if sqlDB, cerr := db.DB(); cerr == nil {
			_ = sqlDB.Close()
		}
	}()
	views, err := flywheel.ListPeriodics(context.Background(), db)
	require.NoError(t, err)
	active := map[string]bool{}
	for _, v := range views {
		active[v.Slug] = v.Active
	}
	return active
}

// seedReconcileBothStores seeds the Engine store exactly as [seedReconcileEnv]
// does and the companion store beside it with its three production definitions,
// the morning send parked. That is the two-store shape the repair lever exists
// for: one parked send in each store, and healthy siblings around them that must
// be left alone.
func seedReconcileBothStores(t *testing.T) (schedulerDB, companionDB string) {
	t.Helper()
	_, schedulerDB, companionDB = seedScheduler(t, true, "PROMPT\n")
	seedStatusJobDB(
		t, schedulerDB,
		flywheel.PeriodicSpec{Slug: schedstatus.SlugTripwire, Kind: "lucid_tripwire", Cron: "0 6 * * *", Queue: "lucid", Active: false},
		flywheel.PeriodicSpec{Slug: schedstatus.SlugBell, Kind: "lucid_bell", Cron: "0 19 * * *", Queue: "lucid", Active: false},
		flywheel.PeriodicSpec{Slug: schedstatus.SlugBellFallback, Kind: "lucid_bell_fallback", Cron: "0 23 * * *", Queue: "lucid", Active: true},
	)
	seedStatusJobDB(
		t, companionDB,
		flywheel.PeriodicSpec{Slug: schedstatus.SlugCompanionMorning, Kind: "lucid_companion_morning", Cron: "0 6 * * *", Queue: "lucid-companion", Active: false},
		flywheel.PeriodicSpec{Slug: schedstatus.SlugCompanionNight, Kind: "lucid_companion_night", Cron: "0 19 * * *", Queue: "lucid-companion", Active: true},
		flywheel.PeriodicSpec{Slug: schedstatus.SlugCompanionMorningBackstop, Kind: "lucid_companion_morning_backstop", Cron: "0 7 * * *", Queue: "lucid-companion", Active: true},
	)
	return schedulerDB, companionDB
}

// TestSchedulerReconcile_TreeExposesReconcile proves the `reconcile` child is
// registered under the `scheduler` parent and carries its four flags — including
// the companion-store override that mirrors the one `status` already carries.
func TestSchedulerReconcile_TreeExposesReconcile(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})

	cmd, _, err := root.Find([]string{"scheduler", "reconcile"})
	require.NoError(t, err)
	assert.Equal(t, "reconcile", cmd.Name())
	assert.NotNil(t, cmd.Flags().Lookup(reconcileFlagSlug), "reconcile exposes --slug")
	assert.NotNil(t, cmd.Flags().Lookup(reconcileFlagNoFire), "reconcile exposes --no-fire")
	assert.NotNil(t, cmd.Flags().Lookup(schedulerFlagDB), "reconcile exposes --db")
	assert.NotNil(t, cmd.Flags().Lookup(statusFlagCompanionDB), "reconcile exposes --companion-db")
}

// TestSchedulerReconcile_SpansBothStores is the whole point of the two-store pass:
// one invocation repairs a parked Engine send and a parked companion send, each
// row naming the store it was repaired in, while the healthy siblings in both
// stores are left untouched.
func TestSchedulerReconcile_SpansBothStores(t *testing.T) {
	schedulerDB, companionDB := seedReconcileBothStores(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "reconcile")
	require.NoError(t, err)
	assert.Contains(t, out, schedulerDB, "the report names the Engine store")
	assert.Contains(t, out, companionDB, "and the companion store beside it")
	assert.Contains(t, out, "[engine] "+schedstatus.SlugTripwire, "each row names the store it was repaired in")
	assert.Contains(t, out, "[companion] "+schedstatus.SlugCompanionMorning)

	assert.True(t, readPeriodicActive(t, schedulerDB)[schedstatus.SlugTripwire], "the parked Engine send is armed again")

	companionActive := readPeriodicActive(t, companionDB)
	assert.True(t, companionActive[schedstatus.SlugCompanionMorning], "the parked companion send is armed again")
	assert.True(t, companionActive[schedstatus.SlugCompanionNight], "its healthy sibling is untouched")
	assert.True(t, companionActive[schedstatus.SlugCompanionMorningBackstop], "and so is the backstop")
}

// TestSchedulerReconcile_CompanionDisabledSkipsThatStore: with the companion off
// there is no companion store to repair. The line says so rather than being
// omitted — a store silently skipped reads exactly like a store that was clean.
func TestSchedulerReconcile_CompanionDisabledSkipsThatStore(t *testing.T) {
	_, schedulerDB, _ := seedScheduler(t, false, "PROMPT\n")
	seedStatusJobDB(t, schedulerDB, flywheel.PeriodicSpec{
		Slug: schedstatus.SlugTripwire, Kind: "lucid_tripwire", Cron: "0 6 * * *", Queue: "lucid", Active: false,
	})

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "reconcile")
	require.NoError(t, err)
	assert.Contains(t, out, "not enabled — skipped", "the skipped store is named with its reason")
	assert.Contains(t, out, "[engine] "+schedstatus.SlugTripwire, "and the Engine repair still happens")
}

// TestSchedulerReconcile_MissingCompanionStoreIsNamedNotFatal: an enabled
// companion whose store does not exist has simply never run here. That is worth
// naming, but discarding a successful Engine repair over it would make the lever
// fail exactly when a half-installed host needs it most — so the line reports it
// and the command still succeeds.
func TestSchedulerReconcile_MissingCompanionStoreIsNamedNotFatal(t *testing.T) {
	schedulerDB := seedReconcileEnv(t) // seeds the Engine store only

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "reconcile")
	require.NoError(t, err, "a companion store that was never created is not a failure")
	assert.Contains(t, out, "not found — start the daemon once to create it")
	assert.Contains(t, out, "[engine] "+schedstatus.SlugTripwire, "the Engine repair stands")
	assert.True(t, readPeriodicActive(t, schedulerDB)[schedstatus.SlugTripwire])
}

// TestSchedulerReconcile_ExplicitCompanionDBMissingIsFatal is the other half of
// that rule: an operator who named the target explicitly gets a missing file as an
// error, exactly as --db has always behaved. Softening it would silently repair
// something other than what was asked for.
func TestSchedulerReconcile_ExplicitCompanionDBMissingIsFatal(t *testing.T) {
	seedReconcileEnv(t)
	absent := filepath.Join(t.TempDir(), "absent", "companion.db")

	_, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "reconcile", "--companion-db", absent)
	require.Error(t, err)
	assert.Contains(t, err.Error(), absent)
	assert.Contains(t, stderr, "lucid: scheduler:", "the failure is named, not left to a bare exit code")
	assert.Contains(t, stderr, absent, "and it names the store it looked for")
}

// TestSchedulerReconcile_HealthyBothStoresReportsNothingParked: with both stores
// healthy the command still says so in one sentence. A repair lever that printed
// nothing would be indistinguishable from one that failed silently.
func TestSchedulerReconcile_HealthyBothStoresReportsNothingParked(t *testing.T) {
	schedulerDB, companionDB := seedReconcileBothStores(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "reconcile")
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "reconcile")
	require.NoError(t, err)
	assert.Contains(t, out, "Nothing parked")
	assert.Contains(t, out, schedulerDB, "both stores are still named on a clean pass")
	assert.Contains(t, out, companionDB)
}

// TestSchedulerReconcile_JSONKeepsExistingKeys pins the compatibility promise: the
// grown document still decodes through the pre-two-store struct with every key
// meaning what it did, and the additions — a `store` per row and the top-level
// `stores` array — sit beside them rather than replacing anything.
func TestSchedulerReconcile_JSONKeepsExistingKeys(t *testing.T) {
	seedReconcileBothStores(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "reconcile", "--json")
	require.NoError(t, err)

	var legacy reconcileJSON
	require.NoError(t, json.Unmarshal([]byte(out), &legacy), "the older shape still parses")
	assert.Equal(t, 6, legacy.Scanned, "scanned is the total across both stores")
	require.Len(t, legacy.Reconciled, 2)
	for _, row := range legacy.Reconciled {
		assert.NotEmpty(t, row.Slug)
		assert.False(t, row.WasActive)
		assert.NotEmpty(t, row.NextRun)
	}

	var full reconcileStoresJSON
	require.NoError(t, json.Unmarshal([]byte(out), &full))
	require.Len(t, full.Stores, 2)
	assert.Equal(t, "engine", full.Stores[0].Name)
	assert.Equal(t, "companion", full.Stores[1].Name)
	assert.NotEmpty(t, full.Stores[1].Path, "a scanned store names its resolved path")

	stores := map[string]string{}
	for _, row := range full.Reconciled {
		stores[row.Slug] = row.Store
	}
	assert.Equal(t, "engine", stores[schedstatus.SlugTripwire])
	assert.Equal(t, "companion", stores[schedstatus.SlugCompanionMorning])
}

// TestSchedulerReconcile_RejectsArgs: `scheduler reconcile` is a no-args verb —
// the periodic is named with --slug, not positionally.
func TestSchedulerReconcile_RejectsArgs(t *testing.T) {
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "reconcile", "extra")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
}

// TestSchedulerReconcile_ReArmsParkedSendAndSpareTheSuppressedBell is the
// command's whole point, end to end through the tree: the parked send comes back,
// and the bell the companion deliberately owns is left suppressed rather than
// "repaired" into a second evening message.
func TestSchedulerReconcile_ReArmsParkedSendAndSparesTheSuppressedBell(t *testing.T) {
	schedulerDB := seedReconcileEnv(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "reconcile")
	require.NoError(t, err)
	assert.Contains(t, out, schedstatus.SlugTripwire, "the report names what it repaired")
	assert.Contains(t, out, schedulerDB, "and the store it repaired it in")
	assert.NotContains(t, out, schedstatus.SlugBell+":", "the suppressed bell is not touched")

	active := readPeriodicActive(t, schedulerDB)
	assert.True(t, active[schedstatus.SlugTripwire], "the parked send is armed again")
	assert.False(t, active[schedstatus.SlugBell], "the companion still owns the evening")
	assert.True(t, active["lucid-bell-fallback"], "the healthy backstop is untouched")
}

// TestSchedulerReconcile_HealthyStoreSaysSo: a repair lever that printed nothing
// on a healthy store would be indistinguishable from one that failed silently.
func TestSchedulerReconcile_HealthyStoreSaysSo(t *testing.T) {
	schedulerDB := seedReconcileEnv(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "reconcile")
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "reconcile")
	require.NoError(t, err)
	assert.Contains(t, out, "Nothing parked")

	active := readPeriodicActive(t, schedulerDB)
	assert.False(t, active[schedstatus.SlugBell], "a second pass still leaves the suppressed bell alone")
}

// TestSchedulerReconcile_JSON pins the machine-readable shape automation gates
// on: what was re-armed, whether its missed send is still coming, and how many
// definitions were inspected.
func TestSchedulerReconcile_JSON(t *testing.T) {
	seedReconcileEnv(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "reconcile", "--json")
	require.NoError(t, err)

	var doc reconcileJSON
	require.NoError(t, json.Unmarshal([]byte(out), &doc))
	assert.Equal(t, 3, doc.Scanned)
	require.Len(t, doc.Reconciled, 1)
	assert.Equal(t, schedstatus.SlugTripwire, doc.Reconciled[0].Slug)
	assert.False(t, doc.Reconciled[0].WasActive)
	assert.NotEmpty(t, doc.Reconciled[0].NextRun)
}

// TestSchedulerReconcile_SlugNarrowsTheRepair: an operator acting on a status
// line repairs the send it named and nothing else.
func TestSchedulerReconcile_SlugNarrowsTheRepair(t *testing.T) {
	seedReconcileEnv(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "reconcile",
		"--slug", schedstatus.SlugTripwire, "--json")
	require.NoError(t, err)

	var doc reconcileJSON
	require.NoError(t, json.Unmarshal([]byte(out), &doc))
	assert.Equal(t, 1, doc.Scanned, "only the named definition is inspected")
	require.Len(t, doc.Reconciled, 1)
	assert.Equal(t, schedstatus.SlugTripwire, doc.Reconciled[0].Slug)
}

// TestSchedulerReconcile_NoFireSkipsTheMissedSend covers the opt-out end to end:
// the send is armed again, but the occurrence it missed is not delivered late.
func TestSchedulerReconcile_NoFireSkipsTheMissedSend(t *testing.T) {
	schedulerDB := seedReconcileEnv(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "reconcile", "--no-fire", "--json")
	require.NoError(t, err)

	var doc reconcileJSON
	require.NoError(t, json.Unmarshal([]byte(out), &doc))
	require.Len(t, doc.Reconciled, 1)
	assert.False(t, doc.Reconciled[0].FiresMissed, "the missed occurrence is skipped")

	assert.True(t, readPeriodicActive(t, schedulerDB)[schedstatus.SlugTripwire], "it is still armed")
}

// TestSchedulerReconcile_ReportsAStuckCursorAndTheLateSend covers the other
// parked shape in the human report: a definition left a full day behind, whose
// missed send is still coming. The report has to say both — what was wrong, and
// that a late send is about to arrive — or the operator is surprised by it.
func TestSchedulerReconcile_ReportsAStuckCursorAndTheLateSend(t *testing.T) {
	_, schedulerDB, _ := seedScheduler(t, true, "PROMPT\n")
	stale := models.WithClock(context.Background(), models.NewFixedClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)))
	db, err := gorm.Open(sqlite.Open(schedulerDB), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	require.NoError(t, flywheel.Migrate(db))
	require.NoError(t, flywheel.UpsertPeriodic(stale, db, flywheel.PeriodicSpec{
		Slug: schedstatus.SlugTripwire, Kind: "lucid_tripwire", Cron: "0 6 * * *", Queue: "lucid", Active: true,
	}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "reconcile")
	require.NoError(t, err)
	assert.Contains(t, out, "was active with a stuck next run", "the report names what was wrong")
	assert.Contains(t, out, "the missed occurrence fires on the next tick", "and warns that a late send is coming")
}

// TestSchedulerReconcile_MissingStoreIsNamed: pointed at a store that does not
// exist, the command says so instead of reporting a clean scan over nothing —
// a missing store means the daemon has never run here.
func TestSchedulerReconcile_MissingStoreIsNamed(t *testing.T) {
	seedScheduler(t, true, "PROMPT\n")
	absent := filepath.Join(t.TempDir(), "absent", "flywheel.db")

	_, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "reconcile", "--db", absent)
	require.Error(t, err)
	assert.Contains(t, err.Error(), absent)
	assert.Contains(t, stderr, "lucid: scheduler:", "the failure is named, not left to a bare exit code")
	assert.Contains(t, stderr, absent, "and it names the store it looked for")
}
