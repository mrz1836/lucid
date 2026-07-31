package schedrun

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/glebarez/sqlite"
	flywheel "github.com/mrz1836/go-flywheel"
	"github.com/mrz1836/go-foundation/models"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/mrz1836/lucid/internal/flynode"
)

// staleCursorGrace is how far behind an *active* periodic's next run may fall
// before its cursor is read as stuck rather than merely due.
//
// A live scheduler advances a fired periodic's cursor past now in a single tick,
// so an active daily periodic sits legitimately "in the past" between its mark
// and the next poll — and for as long as a stopped host stays off. A full day
// behind is different: an entire occurrence came and went with no tick at all.
// The grace is what keeps a merely-due (or merely-asleep) periodic from being
// reported as parked, so the pass stays a true no-op on a healthy store.
const staleCursorGrace = 24 * time.Hour

// errJobStoreMissing reports a durable job store that does not exist yet, so the
// repair lever says "nothing has been scheduled here" rather than silently
// creating an empty store and reporting a clean scan over zero periodics.
var errJobStoreMissing = errors.New("schedrun: job store not found")

// ReconcileOptions configures one reconcile pass.
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
// runs now, and whether the occurrence it missed is still coming.
type ReconciledPeriodic struct {
	Slug        string    `json:"slug"`
	WasActive   bool      `json:"was_active"`
	NextRun     time.Time `json:"next_run"`
	FiresMissed bool      `json:"fires_missed"`
}

// ReconcileReport is the outcome of a pass: every periodic re-armed, and how
// many were inspected. An empty Reconciled with a non-zero Scanned is the
// healthy answer — nothing was parked.
type ReconcileReport struct {
	Reconciled []ReconciledPeriodic `json:"reconciled"`
	Scanned    int                  `json:"scanned"`
}

// Reconcile re-arms every parked periodic and reports what it changed.
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
func Reconcile(ctx context.Context, db *gorm.DB, opts ReconcileOptions) (ReconcileReport, error) {
	now := models.ClockFrom(ctx).Now(ctx)
	views, err := flywheel.ListPeriodics(ctx, db)
	if err != nil {
		return ReconcileReport{}, fmt.Errorf("schedrun: list periodics: %w", err)
	}

	report := ReconcileReport{Reconciled: []ReconciledPeriodic{}}
	for _, v := range views {
		if opts.Slug != "" && v.Slug != opts.Slug {
			continue
		}
		report.Scanned++
		want, known := intendedActive(v.Slug, opts.CompanionEnabled)
		if !known || !want || !parked(v, now) {
			continue
		}
		entry, rerr := rearm(ctx, db, v, now, opts.NoFire)
		if rerr != nil {
			return ReconcileReport{}, rerr
		}
		report.Reconciled = append(report.Reconciled, entry)
	}
	return report, nil
}

// ReconcileStore is [Reconcile] against a job store on disk: it opens the store
// read-write, reconciles, and closes. It is the seam the CLI uses, so the
// command layer never opens a database itself and the daemon's own path
// resolution stays the single source of the store's location.
//
// A store that does not exist is an error rather than an empty scan: the
// periodics are created by the daemon, so a missing file means the daemon has
// never run here — a different problem than a parked send, and one a silent
// "scanned 0" would hide.
func ReconcileStore(ctx context.Context, dbPath string, opts ReconcileOptions) (ReconcileReport, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return ReconcileReport{}, fmt.Errorf("%w at %q: start the scheduler daemon once to create it", errJobStoreMissing, dbPath)
	}
	db, err := gorm.Open(sqlite.Open(readWriteDSN(dbPath)), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		return ReconcileReport{}, fmt.Errorf("schedrun: open job db %q: %w", dbPath, err)
	}
	defer func() {
		if sqlDB, cerr := db.DB(); cerr == nil {
			_ = sqlDB.Close()
		}
	}()
	if err = flynode.MigrateJobStore(db); err != nil {
		return ReconcileReport{}, fmt.Errorf("schedrun: migrate job db: %w", err)
	}
	return Reconcile(ctx, db, opts)
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
// no tick is moving it (see staleCursorGrace).
func parked(v flywheel.PeriodicView, now time.Time) bool {
	if !v.Active {
		return true
	}
	return v.NextRunAt.Before(now.Add(-staleCursorGrace))
}

// rearm re-arms one parked periodic and describes the result.
//
// The default path only flips the active flag: the stale cursor stays, so the
// scheduler's next tick backfills the bucket that was missed and the send goes
// out late rather than never. noFire instead resets the cursor forward, so the
// periodic resumes at its next scheduled occurrence and the missed one is
// skipped.
func rearm(ctx context.Context, db *gorm.DB, v flywheel.PeriodicView, now time.Time, noFire bool) (ReconciledPeriodic, error) {
	entry := ReconciledPeriodic{Slug: v.Slug, WasActive: v.Active}
	if noFire {
		next, err := resetCursor(ctx, db, v)
		if err != nil {
			return ReconciledPeriodic{}, err
		}
		entry.NextRun = next
		return entry, nil
	}
	if err := flywheel.SetPeriodicActive(ctx, db, v.Slug, true); err != nil {
		return ReconciledPeriodic{}, fmt.Errorf("schedrun: re-arm periodic %q: %w", v.Slug, err)
	}
	entry.NextRun = v.NextRunAt
	entry.FiresMissed = !v.NextRunAt.After(now)
	return entry, nil
}

// resetCursor re-arms a periodic at its next future occurrence, skipping the
// bucket it missed, and returns that new cursor.
//
// There is no "advance the cursor" writer upstream, so the row is deleted and
// re-declared from its own current definition — same slug, same kind, same
// queue, same schedule — which seeds a fresh next run ahead of now. The pair
// runs in one transaction so a failure can never leave the schedule deleted, and
// re-declaring from the row's own view is what keeps the cron expression
// untouched. It is safe for exactly the periodics this package owns: all three
// carry the empty args payload, which is what a re-declared spec restores.
func resetCursor(ctx context.Context, db *gorm.DB, v flywheel.PeriodicView) (time.Time, error) {
	spec := flywheel.PeriodicSpec{Slug: v.Slug, Kind: v.Kind, Queue: v.Queue, Cron: v.Cron, Active: true}
	if v.IntervalSeconds > 0 {
		spec.Cron = ""
		spec.Every = time.Duration(v.IntervalSeconds) * time.Second
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := flywheel.DeletePeriodic(ctx, tx, v.Slug); err != nil && !errors.Is(err, flywheel.ErrPeriodicNotFound) {
			return err
		}
		return flywheel.UpsertPeriodic(ctx, tx, spec)
	}); err != nil {
		return time.Time{}, fmt.Errorf("schedrun: reset periodic %q cursor: %w", v.Slug, err)
	}

	// Read the seeded cursor back rather than recomputing it, so the report
	// states what the store actually holds.
	next, err := nextRunOf(ctx, db, v.Slug)
	if err != nil {
		return time.Time{}, err
	}
	return next, nil
}

// nextRunOf reads one periodic's current cursor back out of the store.
func nextRunOf(ctx context.Context, db *gorm.DB, slug string) (time.Time, error) {
	views, err := flywheel.ListPeriodics(ctx, db)
	if err != nil {
		return time.Time{}, fmt.Errorf("schedrun: list periodics: %w", err)
	}
	for _, v := range views {
		if v.Slug == slug {
			return v.NextRunAt, nil
		}
	}
	return time.Time{}, fmt.Errorf("schedrun: periodic %q missing after re-arm", slug)
}

// readWriteDSN builds the read-write file: DSN for path, percent-encoding it so
// a space (the OS user-config dir's "Application Support") or another reserved
// character cannot corrupt the query string, and setting a short busy timeout so
// a transient lock held by the live daemon degrades to a brief retry rather than
// an instant failure.
func readWriteDSN(path string) string {
	u := url.URL{Scheme: "file", Path: path}
	q := url.Values{}
	q.Set("_pragma", "busy_timeout(2000)")
	u.RawQuery = q.Encode()
	return u.String()
}
