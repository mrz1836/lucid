package companion

import (
	"context"

	"gorm.io/gorm"

	"github.com/mrz1836/lucid/internal/flynode"
)

// ReconcileOptions configures one reconcile pass over the companion's job store.
//
// The reference instant and the clock every write stamps come from the context
// ([models.WithClock]), the same way the daemon threads one clock through the
// whole runtime — so a test's fixed clock moves the pass with it.
type ReconcileOptions struct {
	// NoFire re-arms without delivering the missed occurrence: the cursor is
	// reset forward so the next send is the next scheduled one. The default
	// leaves the stale cursor in place, so the missed send goes out late.
	NoFire bool

	// Slug narrows the pass to one periodic. Empty scans them all; an unknown
	// slug scans nothing and re-arms nothing.
	Slug string
}

// Reconcile re-arms every parked companion periodic and reports what it changed.
//
// A periodic is parked when it should be running (the intended-active guard
// below) but the job store says otherwise: it is inactive, or its cursor is
// frozen far enough in the past that no tick is advancing it. That is the silent
// failure this pass exists for — nothing is broken, a scheduled send simply
// never comes again — and it is why repair goes through the binary rather than a
// hand edit of the durable store (usage/commands.md §"scheduler reconcile",
// ADR-0007).
//
// It is idempotent: on a healthy store it changes nothing and reports nothing.
// It never creates a periodic, never edits a cron expression, never moves a
// healthy periodic's cursor, and never touches a slug outside the guard.
//
// Both callers share it — the node runs it at startup after seeding its
// periodics, and `lucid scheduler reconcile` runs it on demand — so the two can
// never disagree about what "parked" means.
//
// The pass itself is [flynode.Reconcile], shared with the Engine's own job
// store; only the intended-active guard below is the companion's.
func Reconcile(ctx context.Context, db *gorm.DB, opts ReconcileOptions) (flynode.ReconcileReport, error) {
	return flynode.Reconcile(ctx, db, sharedOptions(opts))
}

// ReconcileStore is [Reconcile] against the companion's job store on disk: it
// opens the store read-write, reconciles, and closes. It is the seam the CLI
// uses, so the command layer never opens a database itself and the node's own
// path resolution ([DefaultDBPath]) stays the single source of the store's
// location.
//
// A store that does not exist is an error rather than an empty scan: the
// periodics are created by the node, so a missing file means the companion has
// never run here — a different problem than a parked send, and one a silent
// "scanned 0" would hide.
func ReconcileStore(ctx context.Context, dbPath string, opts ReconcileOptions) (flynode.ReconcileReport, error) {
	return flynode.ReconcileStore(ctx, dbPath, sharedOptions(opts))
}

// sharedOptions translates this node's options into the shared pass's, naming
// the package once so both entry points tag their errors identically.
func sharedOptions(opts ReconcileOptions) flynode.ReconcileOptions {
	return flynode.ReconcileOptions{Pkg: "companion", Intended: intendedActive, NoFire: opts.NoFire, Slug: opts.Slug}
}

// intendedActive answers whether a slug *should* be running, plus whether the
// slug is one this package owns at all. An unrecognized slug is never touched —
// the pass repairs what it understands and nothing else, so a foreign periodic
// sharing the store is safe from it.
//
// Unlike the Engine's guard it takes no configuration input, because there is
// none to take: the Engine's evening window is traded between the bell and its
// backstop, so which of the two is intended active depends on a setting. Nothing
// here trades. The companion node only runs at all when the companion is
// enabled, so all three of its periodics are unconditionally intended active
// whenever this code is reachable.
func intendedActive(slug string) (want, known bool) {
	switch slug {
	case slugMorning, slugNight, slugMorningBackstop:
		return true, true
	default:
		return false, false
	}
}
