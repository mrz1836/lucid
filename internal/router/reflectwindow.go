package router

import (
	"fmt"
	"time"

	"github.com/mrz1836/lucid/internal/isoweek"
)

// reflectWindowDateLayout is the civil-date form the earliest-raw anchor and the
// range overrides speak (YYYY-MM-DD).
const reflectWindowDateLayout = "2006-01-02"

// ReflectWindowMode selects how the weekly deep-dive resolves its window. The
// zero value is the default — everything since the last reflection — so a caller
// that sets nothing gets the catch-up read rather than a fixed calendar week.
type ReflectWindowMode string

// The four window modes. Only the default mode is capped; the three explicit
// overrides are deliberate user requests and are honored verbatim.
const (
	// ReflectWindowDefault resolves from the reflected-through cursor: the span
	// since the last close, so a skipped week is caught up rather than dropped.
	ReflectWindowDefault ReflectWindowMode = ""
	// ReflectWindowWeek reproduces the pre-catch-up behavior exactly: the ISO
	// week containing now, Monday 00:00:00 → Sunday 23:59:59.
	ReflectWindowWeek ReflectWindowMode = "week"
	// ReflectWindowSince reads from an explicit start date through end of today.
	ReflectWindowSince ReflectWindowMode = "since"
	// ReflectWindowDays reads the trailing N logical days, today inclusive.
	ReflectWindowDays ReflectWindowMode = "days"
)

// Window provenance labels. They name which input decided the window's start,
// so the surface layer (and a reader of a --json payload) can tell a catch-up
// read from a first-ever read from an explicit override without re-deriving it.
const (
	reflectSourceCursor   = "cursor"
	reflectSourceFirstRun = "first-run"
	reflectSourceWeek     = "week"
	reflectSourceSince    = "since"
	reflectSourceDays     = "days"
	reflectSourceEmpty    = "empty"
)

// ReflectWindowOptions carries the caller's range request. Mode selects the
// resolution rule; Since and Days are read only by their own mode, so an unset
// field can never leak into a window it does not belong to.
type ReflectWindowOptions struct {
	Mode  ReflectWindowMode
	Since time.Time
	Days  int
}

// ReflectWindow is the resolved span the weekly deep-dive reads — computed once
// and threaded to every downstream surface (the bundle's day loop, the narrative
// header, the JSON payload) so text and JSON can never disagree about what was
// covered.
//
// Start is midnight of the window's first logical day and End is 23:59:59 of its
// last, matching the isoweek.Bounds convention. Days counts inclusive logical
// days. Capped and UncoveredDays record a deferred shortfall: when the span
// since the last reflection exceeds the configured ceiling the read covers the
// most recent Days and states the UncoveredDays it did not reach, so a long gap
// costs an honest, visible deferral rather than a silent drop.
type ReflectWindow struct {
	Start         time.Time
	End           time.Time
	Days          int
	Capped        bool
	UncoveredDays int
	Source        string
}

// resolveReflectWindow turns (now, range flags, reflected-through cursor,
// config) into the one window every downstream surface reads. It is pure apart
// from the single cursor read: no model call, no write, and no clock of its own
// — `now` is a parameter, so a window is reproducible.
//
// Resolution order:
//
//  1. week   — isoweek.Bounds(now) verbatim, so the override is byte-identical
//     to the pre-catch-up behavior.
//  2. since  — the given date at 00:00 through end of today. Cap bypassed.
//  3. days   — the trailing N logical days, today inclusive. Cap bypassed.
//  4. default — the reflected-through cursor; failing that the earliest logged
//     entry; failing that today's ISO week. Cap applied.
//
// Every bound is calendar-date arithmetic in now's location, so a window that
// spans a DST transition still counts whole logical days rather than 23- or
// 25-hour ones.
func (r *Router) resolveReflectWindow(now time.Time, opts ReflectWindowOptions) (ReflectWindow, error) {
	now = whenOr(now)

	switch opts.Mode {
	case ReflectWindowWeek:
		start, end := isoweek.Bounds(now)
		return newReflectWindow(start, end, reflectSourceWeek), nil

	case ReflectWindowSince:
		if opts.Since.IsZero() {
			return ReflectWindow{}, fmt.Errorf("reflectwindow: since needs a %s date", reflectWindowDateLayout)
		}
		start := notAfter(civilDayIn(opts.Since, now.Location()), startOfCivilDay(now))
		return newReflectWindow(start, endOfCivilDay(now), reflectSourceSince), nil

	case ReflectWindowDays:
		if opts.Days < 1 {
			return ReflectWindow{}, fmt.Errorf("reflectwindow: days must be >= 1, got %d", opts.Days)
		}
		start := startOfCivilDay(now).AddDate(0, 0, -(opts.Days - 1))
		return newReflectWindow(start, endOfCivilDay(now), reflectSourceDays), nil

	case ReflectWindowDefault:
		// Resolved from the cursor below — the catch-up read.

	default:
		return ReflectWindow{}, fmt.Errorf("reflectwindow: unknown window mode %q", opts.Mode)
	}

	start, end, source, err := r.defaultReflectBounds(now)
	if err != nil {
		return ReflectWindow{}, err
	}
	return r.capReflectWindow(newReflectWindow(start, end, source)), nil
}

// defaultReflectBounds resolves the catch-up window's raw bounds, before the
// cap. The reflected-through cursor is the sole input when it exists; a Ledger
// that has never closed a reflection falls back to its earliest logged entry so
// a first-ever read covers the whole record; and a Ledger with nothing logged at
// all falls back to today's ISO week, which routes straight into the existing
// empty-week short-circuit with no model call.
func (r *Router) defaultReflectBounds(now time.Time) (start, end time.Time, source string, err error) {
	loc := now.Location()

	receipt, ok, err := r.store.ReadReflectionReceipt()
	if err != nil {
		return time.Time{}, time.Time{}, "", fmt.Errorf("reflectwindow: read reflected-through cursor: %w", err)
	}
	if ok {
		through, perr := time.Parse(time.RFC3339, receipt.ReflectedThrough)
		if perr != nil {
			// The cursor is written only through the binary, so an unparseable
			// value means a hand-edit or a corrupt write. Surfacing it beats
			// guessing a start date: a silently wrong cursor is exactly how a
			// day goes missing.
			return time.Time{}, time.Time{}, "", fmt.Errorf(
				"reflectwindow: parse reflected_through %q: %w", receipt.ReflectedThrough, perr,
			)
		}
		// Start at the beginning of the logical day the user closed on — read in
		// the zone they closed in — so an entry logged later that same evening is
		// re-read rather than half-dropped. A day may be seen twice; it is never
		// lost.
		return notAfter(civilDayIn(through, loc), startOfCivilDay(now)),
			endOfCivilDay(now), reflectSourceCursor, nil
	}

	earliest, ok, err := r.store.EarliestRawDate()
	if err != nil {
		return time.Time{}, time.Time{}, "", fmt.Errorf("reflectwindow: resolve earliest logged entry: %w", err)
	}
	if ok {
		day, perr := time.ParseInLocation(reflectWindowDateLayout, earliest, loc)
		if perr != nil {
			return time.Time{}, time.Time{}, "", fmt.Errorf(
				"reflectwindow: parse earliest logged date %q: %w", earliest, perr,
			)
		}
		return notAfter(day, startOfCivilDay(now)), endOfCivilDay(now), reflectSourceFirstRun, nil
	}

	start, end = isoweek.Bounds(now)
	return start, end, reflectSourceEmpty, nil
}

// capReflectWindow applies the configured ceiling to a catch-up window, moving
// the start forward so the read covers the most recent allowed span and
// recording the shortfall it deferred. The deferral is never silent: Capped and
// UncoveredDays are what the surface layer states in both text and JSON, and an
// explicit --since reads the full span.
//
// It is applied to the default mode only. The three overrides are deliberate
// requests, and --week in particular must stay byte-identical to
// isoweek.Bounds(now) whatever the ceiling is set to. A ceiling below 1 means
// the router was never booted, so no cap is applied rather than collapsing the
// window to nothing.
func (r *Router) capReflectWindow(win ReflectWindow) ReflectWindow {
	maxDays := r.cfg.ReflectWeekMaxDays
	if maxDays < 1 || win.Days <= maxDays {
		return win
	}
	win.UncoveredDays = win.Days - maxDays
	win.Start = civilDayIn(win.End, win.End.Location()).AddDate(0, 0, -(maxDays - 1))
	win.Days = maxDays
	win.Capped = true
	return win
}

// newReflectWindow assembles a window from its bounds, counting the inclusive
// logical days between them.
func newReflectWindow(start, end time.Time, source string) ReflectWindow {
	return ReflectWindow{
		Start:  start,
		End:    end,
		Days:   civilDaysBetween(start, end) + 1,
		Source: source,
	}
}

// startOfCivilDay returns midnight of t's own civil date, in t's location.
func startOfCivilDay(t time.Time) time.Time { return civilDayIn(t, t.Location()) }

// endOfCivilDay returns 23:59:59 of t's own civil date — one second before the
// next midnight, the same closing convention isoweek.Bounds uses.
func endOfCivilDay(t time.Time) time.Time {
	return startOfCivilDay(t).AddDate(0, 0, 1).Add(-time.Second)
}

// civilDayIn reads t's civil date in t's own location and re-anchors it at
// midnight in loc. Taking the date before converting is deliberate: a cursor
// stamped at 23:00 in one offset must keep naming the day its owner closed on,
// not the next day in some other zone.
func civilDayIn(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

// civilDaysBetween returns the whole-day span from a to b, counting calendar
// days rather than elapsed hours. The dates are re-anchored in UTC purely for
// the subtraction: a wall-clock difference would report six days across a
// spring-forward week, because one of those days is only 23 hours long.
func civilDaysBetween(a, b time.Time) int {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	from := time.Date(ay, am, ad, 0, 0, 0, 0, time.UTC)
	to := time.Date(by, bm, bd, 0, 0, 0, 0, time.UTC)
	return int(to.Sub(from).Hours() / 24)
}

// notAfter clamps t to ceiling when it runs past it, so a cursor or start date
// ahead of today (clock skew, a hand-edited receipt) yields a single-day window
// instead of an inverted one.
func notAfter(t, ceiling time.Time) time.Time {
	if t.After(ceiling) {
		return ceiling
	}
	return t
}

// earlierOf returns whichever of a and b comes first. time.Time is not an
// ordered type, so the builtin min does not apply.
func earlierOf(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}
