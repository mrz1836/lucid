package flynode

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
)

// StaleCursorGrace is how far behind an *active* periodic's next run may fall
// before its cursor is read as stuck rather than merely due.
//
// A live scheduler advances a fired periodic's cursor past now in a single tick,
// so an active daily periodic sits legitimately "in the past" between its mark
// and the next poll — and for as long as a stopped host stays off. A full day
// behind is different: an entire occurrence came and went with no tick at all.
// The grace is what keeps a merely-due (or merely-asleep) periodic from being
// reported as parked, so the pass stays a true no-op on a healthy store.
const StaleCursorGrace = 24 * time.Hour

// ErrJobStoreMissing reports a durable job store that does not exist yet, so the
// repair lever says "nothing has been scheduled here" rather than silently
// creating an empty store and reporting a clean scan over zero periodics.
//
// The daemon name is deliberately *not* baked in: it is supplied at format time
// from [ReconcileOptions.Pkg], so each caller's message names its own package
// while every caller shares one sentinel to match on.
var ErrJobStoreMissing = errors.New("job store not found")

// IntendedActive answers whether a slug *should* be running, derived from
// configuration and never guessed from the job store, plus whether the slug is
// one the caller owns at all.
//
// This is the guard that keeps repair from fighting a deliberate setting: a send
// a configuration intends to leave suppressed reports want=false and stays
// suppressed, and an unrecognized slug reports known=false and is never touched
// — the pass repairs what its caller understands and nothing else. Injecting it
// is what makes one pass serve every daemon's own job store: the store is
// generic, only the guard is not.
type IntendedActive func(slug string) (want, known bool)

// ReconcileOptions configures one reconcile pass.
//
// The reference instant and the clock every write stamps come from the context
// ([models.WithClock]), the same way the daemon threads one clock through the
// whole runtime — so a test's fixed clock moves the pass with it.
type ReconcileOptions struct {
	// Pkg tags every error with the daemon name, exactly as [BootConfig.Pkg]
	// tags the boot errors, so a supervised log still says which node's store
	// failed. Empty defaults to "flynode".
	Pkg string

	// Intended is the caller's intended-active guard (see [IntendedActive]). A
	// nil guard repairs nothing — every slug reads as unknown — which is the
	// safe direction: a pass with no notion of what should be running must not
	// invent one from the store it is inspecting.
	Intended IntendedActive

	// NoFire re-arms without delivering the missed occurrence: the cursor is
	// reset forward so the next send is the next scheduled one. The default
	// leaves the stale cursor in place, so the missed send goes out late.
	NoFire bool

	// Slug narrows the pass to one periodic. Empty scans them all; an unknown
	// slug scans nothing and re-arms nothing.
	Slug string
}

// pkg is the daemon name errors are tagged with, defaulting to this package's
// own name when a caller left it unset.
func (o ReconcileOptions) pkg() string {
	if o.Pkg == "" {
		return "flynode"
	}
	return o.Pkg
}

// intended applies the caller's guard, treating a nil guard as "nothing here is
// mine", so a misconfigured pass is inert rather than destructive.
func (o ReconcileOptions) intended(slug string) (want, known bool) {
	if o.Intended == nil {
		return false, false
	}
	return o.Intended(slug)
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
// caller's intended-active guard) but the job store says otherwise: it is
// inactive, or its cursor is frozen far enough in the past that no tick is
// advancing it. That is the silent failure this pass exists for — nothing is
// broken, a scheduled send simply never comes again — and it is why repair goes
// through the binary rather than a hand edit of the durable store
// (usage/commands.md §"scheduler reconcile", ADR-0007).
//
// It is idempotent: on a healthy store it changes nothing and reports nothing.
// It never creates a periodic, never edits a cron expression, never moves a
// healthy periodic's cursor, and never touches a slug outside the guard.
//
// Every caller shares it — each daemon runs it at startup after seeding its own
// periodics, and `lucid scheduler reconcile` runs it on demand across every
// store — so no two of them can disagree about what "parked" means. The store is
// whatever *gorm.DB it is handed, which is what lets one implementation repair
// the Engine's job store and the companion's alike.
//
// Beware that [BootConfig.Reconcile] is a different thing wearing a similar
// name: that field is the caller's boot hook, and this is the shared pass such a
// hook typically calls after it seeds its periodics.
func Reconcile(ctx context.Context, db *gorm.DB, opts ReconcileOptions) (ReconcileReport, error) {
	now := models.ClockFrom(ctx).Now(ctx)
	views, err := flywheel.ListPeriodics(ctx, db)
	if err != nil {
		return ReconcileReport{}, fmt.Errorf("%s: list periodics: %w", opts.pkg(), err)
	}

	report := ReconcileReport{Reconciled: []ReconciledPeriodic{}}
	for _, v := range views {
		if opts.Slug != "" && v.Slug != opts.Slug {
			continue
		}
		report.Scanned++
		want, known := opts.intended(v.Slug)
		if !known || !want || !Parked(v, now) {
			continue
		}
		entry, rerr := rearm(ctx, db, v, now, opts)
		if rerr != nil {
			return ReconcileReport{}, rerr
		}
		report.Reconciled = append(report.Reconciled, entry)
	}
	return report, nil
}

// ReconcileStore is [Reconcile] against a job store on disk: it opens the store
// read-write, reconciles, and closes. It is the seam the CLI uses, so the
// command layer never opens a database itself and each daemon's own path
// resolution stays the single source of its store's location.
//
// A store that does not exist is an error rather than an empty scan: the
// periodics are created by the daemon, so a missing file means the daemon has
// never run here — a different problem than a parked send, and one a silent
// "scanned 0" would hide.
func ReconcileStore(ctx context.Context, dbPath string, opts ReconcileOptions) (ReconcileReport, error) {
	pkg := opts.pkg()
	if _, err := os.Stat(dbPath); err != nil {
		return ReconcileReport{}, fmt.Errorf("%s: %w at %q: start the scheduler daemon once to create it", pkg, ErrJobStoreMissing, dbPath)
	}
	db, err := gorm.Open(sqlite.Open(readWriteDSN(dbPath)), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		return ReconcileReport{}, fmt.Errorf("%s: open job db %q: %w", pkg, dbPath, err)
	}
	defer func() {
		if sqlDB, cerr := db.DB(); cerr == nil {
			_ = sqlDB.Close()
		}
	}()
	if err = MigrateJobStore(db); err != nil {
		return ReconcileReport{}, fmt.Errorf("%s: migrate job db: %w", pkg, err)
	}
	return Reconcile(ctx, db, opts)
}

// Parked reports whether an intended-active periodic is in the state repair
// exists for: switched off, or with a cursor stuck far enough in the past that
// no tick is moving it (see [StaleCursorGrace]).
//
// It is exported so a status surface can name the same fault the repair lever
// fixes, rather than growing a second, subtly different notion of "parked".
func Parked(v flywheel.PeriodicView, now time.Time) bool {
	if !v.Active {
		return true
	}
	return v.NextRunAt.Before(now.Add(-StaleCursorGrace))
}

// rearm re-arms one parked periodic and describes the result.
//
// The default path only flips the active flag: the stale cursor stays, so the
// scheduler's next tick backfills the bucket that was missed and the send goes
// out late rather than never. NoFire instead resets the cursor forward, so the
// periodic resumes at its next scheduled occurrence and the missed one is
// skipped.
func rearm(ctx context.Context, db *gorm.DB, v flywheel.PeriodicView, now time.Time, opts ReconcileOptions) (ReconciledPeriodic, error) {
	entry := ReconciledPeriodic{Slug: v.Slug, WasActive: v.Active}
	if opts.NoFire {
		next, err := resetCursor(ctx, db, v, opts.pkg())
		if err != nil {
			return ReconciledPeriodic{}, err
		}
		entry.NextRun = next
		return entry, nil
	}
	if err := flywheel.SetPeriodicActive(ctx, db, v.Slug, true); err != nil {
		return ReconciledPeriodic{}, fmt.Errorf("%s: re-arm periodic %q: %w", opts.pkg(), v.Slug, err)
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
// untouched. It is safe for exactly the periodics a caller's guard owns: every
// one of them carries the empty args payload, which is what a re-declared spec
// restores.
func resetCursor(ctx context.Context, db *gorm.DB, v flywheel.PeriodicView, pkg string) (time.Time, error) {
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
		return time.Time{}, fmt.Errorf("%s: reset periodic %q cursor: %w", pkg, v.Slug, err)
	}

	// Read the seeded cursor back rather than recomputing it, so the report
	// states what the store actually holds.
	next, err := nextRunOf(ctx, db, v.Slug, pkg)
	if err != nil {
		return time.Time{}, err
	}
	return next, nil
}

// nextRunOf reads one periodic's current cursor back out of the store.
func nextRunOf(ctx context.Context, db *gorm.DB, slug, pkg string) (time.Time, error) {
	views, err := flywheel.ListPeriodics(ctx, db)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s: list periodics: %w", pkg, err)
	}
	for _, v := range views {
		if v.Slug == slug {
			return v.NextRunAt, nil
		}
	}
	return time.Time{}, fmt.Errorf("%s: periodic %q missing after re-arm", pkg, slug)
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

// StartupErr grades a failure from a daemon's boot sequence: a stop signal that
// lands mid-startup aborts the in-flight DB work with a context error, and that
// is an ordinary shutdown, not a failure. Reporting it as one would put a
// spurious error line in the supervised log every time a daemon is stopped or
// restarted during its first moments — the drain path a process supervisor
// exercises constantly. A genuine startup failure (a malformed clock mark, an
// unusable job store) has no canceled context and still surfaces.
//
// It lives here so every daemon shares one definition of "a canceled boot is a
// stop request, not a failure".
//
//nolint:nilerr // discarding the error is the point: a canceled boot is a stop request, and node.Run reports the same clean nil for a canceled drain
func StartupErr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return nil
	}
	return err
}
