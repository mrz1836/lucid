package schedrun

import (
	"context"
	"time"

	flywheel "github.com/mrz1836/go-flywheel"
	"gorm.io/gorm"

	"github.com/mrz1836/lucid/internal/flynode"
)

// pkgName tags this daemon's errors — its boot errors ([flynode.BootConfig.Pkg])
// and its reconcile errors ([flynode.ReconcileOptions.Pkg]) alike — so a
// supervised log still says which node's store failed even though the pass
// itself is shared.
const pkgName = "schedrun"

// errJobStoreMissing reports a durable job store that does not exist yet, so the
// repair lever says "nothing has been scheduled here" rather than silently
// creating an empty store and reporting a clean scan over zero periodics. The
// sentinel is shared; this package's name is added to the message at format time
// (see [flynode.ErrJobStoreMissing]).
var errJobStoreMissing = flynode.ErrJobStoreMissing

// ReconcileOptions configures one reconcile pass over the Engine's job store.
//
// The reference instant and the clock every write stamps come from the context
// ([models.WithClock]), the same way the daemon threads one clock through the
// whole runtime — so a test's fixed clock moves the pass with it.
type ReconcileOptions struct {
	// CompanionEnabled is the evening window's ownership switch, and the only
	// input to the intended-active guard: it decides whether the bell or its
	// backstop is the periodic that should be running.
	CompanionEnabled bool

	// NoFire re-arms without delivering the missed occurrence: the cursor is
	// reset forward so the next send is the next scheduled one. The default
	// leaves the stale cursor in place, so the missed send goes out late.
	NoFire bool

	// Slug narrows the pass to one periodic. Empty scans them all; an unknown
	// slug scans nothing and re-arms nothing.
	Slug string
}

// ReconciledPeriodic records one re-armed periodic: what it was, when it next
// runs now, and whether the occurrence it missed is still coming. It is an alias
// for the shared type, so a report crossing package boundaries stays one type.
type ReconciledPeriodic = flynode.ReconciledPeriodic

// ReconcileReport is the outcome of a pass: every periodic re-armed, and how
// many were inspected. An empty Reconciled with a non-zero Scanned is the
// healthy answer — nothing was parked.
type ReconcileReport = flynode.ReconcileReport

// Reconcile re-arms every parked Engine periodic and reports what it changed.
//
// A periodic is parked when the configuration says it should be running (the
// intended-active guard below) but the job store says otherwise: it is inactive,
// or its cursor is frozen far enough in the past that no tick is advancing it.
// That is the silent failure this pass exists for — nothing is broken, a
// scheduled send simply never comes again — and it is why repair goes through
// the binary rather than a hand edit of the durable store
// (usage/commands.md §"scheduler reconcile", ADR-0007).
//
// It is idempotent: on a healthy store it changes nothing and reports nothing.
// It never creates a periodic, never edits a cron expression, never moves a
// healthy periodic's cursor, and never touches a slug outside the guard.
//
// Both callers share it — the daemon runs it at startup after seeding the
// periodics, and `lucid scheduler reconcile` runs it on demand — so the two can
// never disagree about what "parked" means.
//
// The pass itself is [flynode.Reconcile], shared with the companion's own job
// store; only the intended-active guard below is the Engine's.
func Reconcile(ctx context.Context, db *gorm.DB, opts ReconcileOptions) (ReconcileReport, error) {
	return flynode.Reconcile(ctx, db, flynode.ReconcileOptions{
		Pkg:      pkgName,
		Intended: engineGuard(opts.CompanionEnabled),
		NoFire:   opts.NoFire,
		Slug:     opts.Slug,
	})
}

// ReconcileStore is [Reconcile] against the Engine's job store on disk: it opens
// the store read-write, reconciles, and closes. It is the seam the CLI uses, so
// the command layer never opens a database itself and the daemon's own path
// resolution stays the single source of the store's location.
//
// A store that does not exist is an error rather than an empty scan: the
// periodics are created by the daemon, so a missing file means the daemon has
// never run here — a different problem than a parked send, and one a silent
// "scanned 0" would hide.
func ReconcileStore(ctx context.Context, dbPath string, opts ReconcileOptions) (ReconcileReport, error) {
	return flynode.ReconcileStore(ctx, dbPath, flynode.ReconcileOptions{
		Pkg:      pkgName,
		Intended: engineGuard(opts.CompanionEnabled),
		NoFire:   opts.NoFire,
		Slug:     opts.Slug,
	})
}

// engineGuard adapts [intendedActive] to the shared pass's injected-guard shape
// by closing over the one configuration input the Engine's guard needs.
func engineGuard(companionEnabled bool) flynode.IntendedActive {
	return func(slug string) (want, known bool) { return intendedActive(slug, companionEnabled) }
}

// intendedActive answers whether a slug *should* be running, derived from
// configuration and never guessed from the job store, plus whether the slug is
// one this package owns at all.
//
// This is the guard that keeps repair from fighting a deliberate setting: when
// the companion owns the evening window the bell is intended inactive, so a
// reconcile pass leaves it suppressed and re-arms its backstop instead
// (usage/commands.md §"The intended-active guard"). An unrecognized slug is
// never touched — the pass repairs what it understands and nothing else.
func intendedActive(slug string, companionEnabled bool) (want, known bool) {
	switch slug {
	case slugTripwire:
		// The morning dead-man is unconditional: it is the escalation ladder.
		return true, true
	case slugBell:
		return !companionEnabled, true
	case slugBellFallback:
		return companionEnabled, true
	default:
		return false, false
	}
}

// parked reports whether an intended-active periodic is in the state repair
// exists for: switched off, or with a cursor stuck far enough in the past that
// no tick is moving it (see [flynode.StaleCursorGrace]). The Engine and the
// companion share one definition of it, so their two stores can never disagree
// about what "parked" means.
func parked(v flywheel.PeriodicView, now time.Time) bool {
	return flynode.Parked(v, now)
}
