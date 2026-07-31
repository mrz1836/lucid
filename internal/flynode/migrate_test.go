package flynode

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	flywheel "github.com/mrz1836/go-flywheel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// preDriftDDL is the pair of index definitions an older go-flywheel installed,
// captured verbatim from the job stores that crash-looped every scheduler node
// on 2026-07-30. jobs_ready still carries executor_class and lacks the
// deleted_at guard; jobs_unique_active_key predates the 'paused' state. Seeding
// exactly this is what makes the test below a regression test rather than a
// hypothetical: it is the state a real upgraded database is in.
func preDriftDDL() []string {
	return []string{
		`DROP INDEX jobs_unique_active_key`,
		`DROP INDEX jobs_ready`,
		`CREATE UNIQUE INDEX jobs_unique_active_key ON jobs (unique_active_key) ` +
			`WHERE unique_active_key IS NOT NULL ` +
			`AND state IN ('available', 'running', 'retryable', 'scheduled')`,
		`CREATE INDEX jobs_ready ON jobs (queue, executor_class, priority, scheduled_at) ` +
			`WHERE state IN ('available', 'retryable', 'scheduled')`,
	}
}

// newDriftedStore opens a fresh disposable job store, brings it up at the
// runtime's current schema, then rewinds the two indexes to their pre-drift
// definitions — the exact shape a store upgraded across the flywheel bump has on
// disk. It mirrors Boot's own open (gormlogger.Discard on a disposable machinery
// DB) so the test exercises the same handle production does.
func newDriftedStore(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(
		sqlite.Open(filepath.Join(t.TempDir(), "job.db")),
		&gorm.Config{Logger: gormlogger.Discard},
	)
	require.NoError(t, err, "open the temp job store")
	require.NoError(t, flywheel.Migrate(db), "a fresh store migrates cleanly at the current schema")

	for _, stmt := range preDriftDDL() {
		require.NoError(t, db.Exec(stmt).Error, "seed the pre-drift index definition: %s", stmt)
	}
	return db
}

// indexDDL reads back the stored definitions of the two indexes this test cares
// about. SQLite normalizes what it stores — whitespace collapses and IF NOT
// EXISTS is dropped — so callers must assert on semantics, never on exact text.
func indexDDL(t *testing.T, db *gorm.DB) map[string]string {
	t.Helper()

	var rows []struct {
		Name string
		SQL  string
	}
	require.NoError(t, db.Raw(
		`SELECT name, sql FROM sqlite_master WHERE type='index' AND name IN ('jobs_ready','jobs_unique_active_key')`,
	).Scan(&rows).Error)

	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.Name] = r.SQL
	}
	return out
}

// TestMigrateJobStoreHealsDriftedIndexes is the regression guard for the
// 2026-07-30 outage: a job store carrying an older flywheel's index definitions
// must migrate clean under MigrateJobStore, with both drifted indexes rebuilt at
// the runtime's current definition.
//
// If someone turns MigrateJobStore's Reconcile back off, this fails — that is
// the whole point. TestPlainMigrateStillFailsOnDrift below is its negative
// control, proving the seeded state really is drift and not a no-op.
func TestMigrateJobStoreHealsDriftedIndexes(t *testing.T) {
	db := newDriftedStore(t)

	require.NoError(t, MigrateJobStore(db),
		"a drifted job store migrates clean with reconciliation on — the daemon boots instead of crash-looping")

	ddl := indexDDL(t, db)
	require.Contains(t, ddl, "jobs_unique_active_key", "the unique active-key index survives the rebuild")
	require.Contains(t, ddl, "jobs_ready", "the ready-claim index survives the rebuild")

	assert.Contains(t, ddl["jobs_unique_active_key"], "paused",
		"jobs_unique_active_key is rebuilt with the paused state in its partial predicate")
	assert.Contains(t, ddl["jobs_ready"], "deleted_at",
		"jobs_ready is rebuilt with the deleted_at guard")
	assert.NotContains(t, ddl["jobs_ready"], "executor_class",
		"jobs_ready is rebuilt on the current key — a surviving executor_class column means the stale index was kept")
}

// TestPlainMigrateStillFailsOnDrift is the negative control. It proves the state
// newDriftedStore seeds is genuinely drift that flywheel refuses by default, so
// the positive test above cannot pass for the wrong reason — e.g. because a
// future flywheel stopped treating this pair as drifted, which would leave the
// guard passing while protecting nothing.
//
// This is a deliberate direct caller of flywheel.Migrate. It is production code
// that must route through MigrateJobStore; a test store is created fresh and
// exercises the refusal on purpose, which is why TestNoDirectFlywheelMigrate
// scopes itself to non-test files.
func TestPlainMigrateStillFailsOnDrift(t *testing.T) {
	db := newDriftedStore(t)

	err := flywheel.Migrate(db)
	require.Error(t, err,
		"plain Migrate must still refuse a drifted store — otherwise the reconcile opt-in guards nothing")
	assert.Contains(t, err.Error(), "jobs_ready",
		"the refusal names the drifted index, which is how the outage was diagnosed")
}
