package workout

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	flywheel "github.com/mrz1836/go-flywheel"
	"github.com/mrz1836/go-foundation/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/lucidtest"
)

// skipIfRoot skips a chmod-based permission test when running as root, where the
// permission bits are a no-op (matching internal/router/engine_errors_test.go).
func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
}

// companionReceiptDir returns the engine/companion receipt directory under a
// nested Ledger home, where the workout window's delivery receipt lands.
func companionReceiptDir(home string) string {
	return filepath.Join(home, "engine", "companion")
}

// TestFireGuardReceiptReadErrorIsLoud proves the idempotency guard surfaces a
// corrupt receipt as a loud error rather than silently re-sending or skipping: a
// receipt whose body will not parse fails the fire before any compose or send.
func TestFireGuardReceiptReadErrorIsLoud(t *testing.T) {
	t.Parallel()

	home, store := lucidtest.Ledger(t, lucidtest.NestedHome(), lucidtest.WithEngine())
	dir := companionReceiptDir(home)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "receipt_workout.json"), []byte("{bad"), 0o600))

	comp := &fakeComposer{res: Result{Text: "TODAY: LEGS"}}
	del := &fakeDeliverer{}
	r := &Runner{compose: comp, deliver: del, store: store, slot: "12:00"}

	_, err := r.Fire(context.Background(), at(2026, 7, 20, 12, 0))
	require.Error(t, err, "a corrupt receipt fails the guard loudly")
	assert.Equal(t, 0, comp.calls, "the guard fails before any compose")
	assert.Empty(t, del.sends, "the guard fails before any send")
}

// TestFireWriteReceiptErrorIsLoud proves a receipt that cannot be persisted after
// a verified send is a loud error: the send goes out and reads back, but an
// unwritable receipt directory fails the fire so a supervised retry re-sends
// rather than the day being recorded as delivered on a receipt that never landed.
func TestFireWriteReceiptErrorIsLoud(t *testing.T) {
	skipIfRoot(t)
	t.Parallel()

	home, store := lucidtest.Ledger(t, lucidtest.NestedHome(), lucidtest.WithEngine())
	dir := companionReceiptDir(home)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.Chmod(dir, 0o500)) // read+exec, no write — the receipt write fails
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	comp := &fakeComposer{res: Result{Text: "TODAY: LEGS"}}
	del := &fakeDeliverer{}
	r := &Runner{compose: comp, deliver: del, store: store, slot: "12:00"}

	_, err := r.Fire(context.Background(), at(2026, 7, 20, 12, 0))
	require.Error(t, err, "an unwritable receipt directory fails the fire")
	assert.Len(t, del.sends, 1, "the send still went out before the receipt failed")
	assert.Empty(t, del.alerts, "a receipt-write failure is a returned error, not an alert")
}

// TestWorkPropagatesFireError proves the daily worker surfaces a fire failure as a
// job error so the flywheel node retries it, rather than swallowing a missed send.
func TestWorkPropagatesFireError(t *testing.T) {
	t.Parallel()

	comp := &fakeComposer{res: Result{Text: "TODAY: LEGS"}}
	del := &fakeDeliverer{sendErr: errors.New("discord 503")}
	r, _ := newRunner(t, comp, del)

	w := dailyWorker{r: r, clock: models.NewFixedClock(at(2026, 7, 20, 12, 0))}
	_, err := w.Work(context.Background(), &flywheel.Job[workoutArgs]{})
	require.Error(t, err, "a fire failure propagates out of the worker")
}

// TestRunRejectsUnresolvableDBPath proves Run surfaces a DB-path resolution
// failure loudly: with no explicit path, no env override, and no home to fall
// back on, the disposable job DB cannot be located and Run refuses to start.
func TestRunRejectsUnresolvableDBPath(t *testing.T) {
	store := newStore(t)
	cfg := writeWorkoutConfig(t)

	// Clear every source ResolveDBPath consults so os.UserConfigDir has no home.
	t.Setenv(envWorkoutDB, "")
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")

	err := Run(context.Background(), Options{
		Store:    store,
		Notifier: &fakeDeliverer{},
		Metrics:  fakeMetrics{},
		Config:   cfg,
		DBPath:   "",
	})
	require.Error(t, err, "an unresolvable job-DB path fails Run rather than defaulting silently")
}

// TestUpsertPeriodicRejectsClosedDB proves the reconcile step surfaces a DB
// failure loudly: a valid slot whose upsert hits a closed job DB is wrapped and
// returned rather than a periodic silently going unregistered.
func TestUpsertPeriodicRejectsClosedDB(t *testing.T) {
	t.Parallel()

	db := newJobDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	err = upsertPeriodic(ctxAt(at(2026, 7, 19, 12, 0)), db, "12:00")
	require.Error(t, err, "a valid slot on a closed DB fails the upsert loudly")
}
