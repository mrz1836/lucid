package flynode

import (
	flywheel "github.com/mrz1836/go-flywheel"
	"gorm.io/gorm"
)

// MigrateJobStore migrates a disposable Lucid job store with index
// reconciliation ON: a drifted index is dropped and recreated at the runtime's
// definition rather than failing the migration.
//
// It exists because flywheel defaults Reconcile off — deliberately, since the
// rebuild takes a table-wide exclusive lock and an uninvited lock on every start
// is a stall under load. Lucid's job stores are the opposite case: single-writer
// local SQLite files holding tens of rows, where the rebuild is microseconds and
// a refused migration is a crash-looping daemon. On 2026-07-30 a flywheel bump
// drifted jobs_ready and jobs_unique_active_key in all four stores and every
// scheduler node crash-looped on boot until they were corrected by hand.
//
// Every production migrate path routes through here so a scheduler node added
// later inherits reconciliation by construction; TestNoDirectFlywheelMigrate
// fails the build if one does not.
func MigrateJobStore(db *gorm.DB) error {
	return flywheel.MigrateWithOptions(db, flywheel.MigrateOpts{Reconcile: true})
}
