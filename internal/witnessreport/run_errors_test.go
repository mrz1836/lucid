package witnessreport

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/mrz1836/go-foundation/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/mrz1836/lucid/internal/config"
)

// skipIfRoot skips a chmod-driven permission test under root, where the write
// bits are a no-op and the failure branch can never be reached. It mirrors the
// guard in internal/router/engine_errors_test.go.
func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
}

// TestFire_ReceiptReadError_Errors: a corrupt receipt file surfaces a loud error
// on the idempotency read rather than silently re-delivering or skipping — the
// Guard read failure aborts the fire before any send.
func TestFire_ReceiptReadError_Errors(t *testing.T) {
	comp := &runFakeComposer{report: sampleReport()}
	del := &runFakeDeliverer{}
	r, store := newRunner(t, comp, del)

	witnessDir := filepath.Join(store.Home(), "engine", "witness")
	require.NoError(t, os.MkdirAll(witnessDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(witnessDir, "receipt.json"), []byte("{bad"), 0o600))

	_, err := r.Fire(context.Background(), reportNow())
	require.Error(t, err, "a corrupt receipt is loud, not a silent re-send")
	assert.Empty(t, del.sends, "the read failure aborts before any send")
}

// TestFire_ReceiptWriteError_Errors: a receipt that cannot be persisted after a
// verified send is a loud error — the fire never returns success on a durability
// failure that would let the next week double-post.
func TestFire_ReceiptWriteError_Errors(t *testing.T) {
	skipIfRoot(t)
	comp := &runFakeComposer{report: sampleReport()}
	del := &runFakeDeliverer{}
	r, store := newRunner(t, comp, del)

	witnessDir := filepath.Join(store.Home(), "engine", "witness")
	require.NoError(t, os.MkdirAll(witnessDir, 0o700))
	require.NoError(t, os.Chmod(witnessDir, 0o500)) // read+exec, no write
	t.Cleanup(func() { _ = os.Chmod(witnessDir, 0o700) })

	_, err := r.Fire(context.Background(), reportNow())
	require.Error(t, err, "a receipt write failure surfaces loudly")
	require.Len(t, del.sends, 1, "the send lands; only the receipt persistence fails")
}

// TestWork_FireErrorPropagates: the weekly worker returns a fire's error to the
// flywheel runner (so a supervised retry sees it) rather than swallowing it — a
// bogus mode fails the fire before any send.
func TestWork_FireErrorPropagates(t *testing.T) {
	r, _ := newRunner(t, &runFakeComposer{report: sampleReport()}, &runFakeDeliverer{})
	r.mode = "bogus"
	w := weeklyWorker{r: r, clock: models.NewFixedClock(reportNow())}

	_, err := w.Work(context.Background(), nil)
	require.Error(t, err, "the worker surfaces the fire error to the runner")
}

// TestRun_DBPathError: Run surfaces a job-DB path resolution failure rather than
// booting a node against an unresolved path. With no explicit path, no env
// override, and no user-config dir resolvable, the path cannot be built.
func TestRun_DBPathError(t *testing.T) {
	store := runStore(t)
	// Force flynode.ResolveDBPath's user-config-dir lookup to fail on both darwin
	// and linux (CI): no explicit path, no env override, no HOME/XDG.
	t.Setenv(envWitnessReportDB, "")
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")

	err := Run(context.Background(), Options{
		Store:    store,
		Notifier: &runFakeDeliverer{},
		Numbers:  fakeNumbers{},
		Records:  fakeRecords{},
		Config: config.WitnessReportConfig{
			Enabled: true, Mode: config.WitnessReportModePreview, Time: "09:00", Weekday: 1,
		},
	})
	require.Error(t, err, "an unresolvable job-DB path fails Run before it boots a node")
}

// TestUpsertWeeklyPeriodic_UpsertError: a valid mark against an un-migrated job DB
// surfaces the flywheel upsert failure wrapped, rather than a partial reconcile.
func TestUpsertWeeklyPeriodic_UpsertError(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "unmigrated.db")), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)

	cfg := config.WitnessReportConfig{Time: "09:00", Weekday: 1}
	err = upsertWeeklyPeriodic(ctxAt(reportNow()), db, cfg)
	require.Error(t, err, "an un-migrated DB fails the periodic upsert")
	assert.Contains(t, err.Error(), "upsert weekly periodic")
}
