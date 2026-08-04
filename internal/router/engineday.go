package router

import (
	"errors"
	"fmt"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/observations"
)

// DayRejectedError is a `--day` value the strict tier refused: unreadable, or a
// day that has not happened yet. It is distinct from a runtime failure because
// the two want opposite handling at the surface — a refusal is a fixed reason
// the user should read, while a runtime failure is a fault. The CLI matches it
// with errors.As and prints Reason the same way it prints any other
// deterministic rejection, since [cli.Execute] renders no returned error.
type DayRejectedError struct{ Reason string }

// Error returns the fixed reason, which is already a complete user-facing
// sentence naming what was wrong and that nothing was saved.
func (e *DayRejectedError) Error() string { return e.Reason }

// rejectDay builds a [DayRejectedError] from a formatted reason.
func rejectDay(format string, args ...any) error {
	return &DayRejectedError{Reason: fmt.Sprintf(format, args...)}
}

// resolveEngineDay resolves an Engine verb's `--day` value into the instant the
// event records and the logical day it lands on. It is the Engine-side entry
// into the strict tier — `lucid mode` and `lucid storm` resolve here — and the
// counterpart to [resolveCaptureWhen], which serves the capture verbs.
//
// The grammar itself lives in [observations.ResolveDay] (usage/commands.md
// §"Backdating with --day"): every accepted form, the literal reading of an
// explicit date, and the future ceiling. Two things are added here.
//
// First, the strict tier's contract in this package's own words: an unreadable
// token or a day that has not happened yet is a clean error naming what was
// wrong, and the caller writes nothing (error-states.md §B-1, §B-2).
//
// Second — and this is the part that cannot be delegated — the *relative* word
// resolves against the governing profile's rollover rather than the
// observations default. Both boundaries exist on purpose and they are not the
// same number: a capture files under the observations rollover, while an Engine
// day is bounded by the profile's, which a travel or night-shift profile moves.
// Reading `@yesterday` on a `/mode` gap-fill through the capture boundary would
// name the wrong Engine day for exactly the users who configured a profile to
// say so. An explicit date is taken literally on both sides, so it needs no
// such adjustment.
func resolveEngineDay(dayArg string, now time.Time, clocks engine.Clocks) (occ, day time.Time, err error) {
	res, resErr := observations.ResolveDay(dayArg, now, observations.DayOptions{AllowPartial: true})
	switch {
	case errors.Is(resErr, observations.ErrDayFuture):
		// The ceiling stays in ResolveDay; this second pass only recovers the
		// day the value resolved to, so the refusal can name it.
		future, _ := observations.ResolveDay(dayArg, now, observations.DayOptions{
			AllowFuture: true, AllowPartial: true,
		})
		return time.Time{}, time.Time{}, rejectDay(
			"cannot record against %s — that day has not happened yet; nothing was saved", future.LogicalDate,
		)
	case resErr != nil:
		// Self-contained rather than wrapped: the sentinel already carries the
		// value and the accepted forms, and repeating them reads as a stutter.
		return time.Time{}, time.Time{}, rejectDay(
			"could not read the day %q (want %s); nothing was saved", dayArg, observations.AcceptedDayForms,
		)
	}

	occ = res.OccurredAt
	if res.Relative {
		occ = onDay(engine.AddDays(clocks.BaseLogicalDate(now), -1), occ)
	}
	return occ, engine.DateOf(occ), nil
}

// onDay moves an instant onto day, keeping its time of day — how a relative
// token that carried a time (`@yesterday 19:30`) survives being re-anchored on
// the Engine's own boundary.
func onDay(day, at time.Time) time.Time {
	h, m, s := at.Clock()
	return time.Date(day.Year(), day.Month(), day.Day(), h, m, s, 0, at.Location())
}
