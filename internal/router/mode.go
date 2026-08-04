package router

import (
	"fmt"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
)

// Fixed /mode user copy (engine-module.md §Error states), matched verbatim by
// tests.
const (
	// modeInvalidMsg is returned when `/mode` names something that is not one
	// of the three declarable modes.
	modeInvalidMsg = "Mode must be one of green, yellow, or red."

	// modeAlreadyDeclaredMsg rejects a gap-fill onto a day that already names a
	// mode — the bell fixed it, and a gap-fill fills a gap.
	modeAlreadyDeclaredMsg = "A mode was already declared for that day — " +
		"the bell fixed it, and there's no retroactive amendment (engine §2)."

	// modeDayEndedMsg rejects a gap-fill onto today (or later). A retroactive
	// target is always a day that has ended; today is still live, and its mode
	// is the same-day declaration's business until the bell.
	modeDayEndedMsg = "A mode gap-fill names a day that has already ended."
)

// ModeResult reports a `/mode` declaration. Rejected marks a declaration made
// after the bell (no disk effect); Invalid marks an unknown mode name; when
// neither is set the mode was declared (or already stood) for LogicalDate.
type ModeResult struct {
	Mode        engine.Mode
	LogicalDate string
	Rejected    bool
	Invalid     bool
	Idempotent  bool
	Ack         string
}

// ModeRequest carries a `/mode` declaration: the mode itself, the instant the
// command ran, and an optional DayArg. An empty DayArg is the ordinary
// same-day declaration; a non-empty one is a gap-fill onto a past logical day
// (engine-module.md §"`/mode --day` gap-fill").
type ModeRequest struct {
	Mode   engine.Mode
	DayArg string
	Now    time.Time
}

// Mode executes a same-day `/mode <green|yellow|red>` — the shape every caller
// that never backdates wants. It is a thin wrapper over [Router.DeclareMode].
func (r *Router) Mode(mode engine.Mode, now time.Time) (ModeResult, error) {
	return r.DeclareMode(ModeRequest{Mode: mode, Now: now})
}

// DeclareMode executes `/mode <green|yellow|red> [--day <date>]`
// (engine-module.md §Commands).
//
// Without `--day` it declares today's mode, fixed at the bell: the declaration
// is rejected once the governing day's bell has rung — the mode is fixed at the
// bell with no retroactive amendment (engine §2) — and the first declaration
// wins, so a second `/mode` before the bell is an idempotent no-op. The mode is
// written as the day record's immutable base; the evening close-out folds the
// practice result in via corrections, never touching the declared mode.
//
// With `--day` it gap-fills a past day (see [Router.modeGapFill]). The two
// paths share only their validation: a gap-fill is not bell-gated, because the
// day it names has already ended, and the bell it would answer to rang long ago.
func (r *Router) DeclareMode(req ModeRequest) (ModeResult, error) {
	now := whenOr(req.Now)
	loc := now.Location()

	if !engine.ValidMode(req.Mode) {
		return ModeResult{Invalid: true, Ack: modeInvalidMsg}, nil
	}
	if err := r.prepareEngine(); err != nil {
		return ModeResult{}, err
	}
	chain, err := r.store.ReadChainConfig()
	if err != nil {
		return ModeResult{}, err
	}

	profile, clocks, err := r.governingClocks(chain, now)
	if err != nil {
		return ModeResult{}, err
	}

	if strings.TrimSpace(req.DayArg) != "" {
		return r.modeGapFill(req, chain, clocks, now, loc)
	}
	return r.modeToday(req.Mode, chain, profile, clocks, now, loc)
}

// modeToday declares the current logical day's mode — the bell-gated,
// first-declaration-wins path, unchanged by backdating.
func (r *Router) modeToday(
	mode engine.Mode, chain engine.ChainConfig, profile string,
	clocks engine.Clocks, now time.Time, loc *time.Location,
) (ModeResult, error) {
	if clocks.ModeRejected(now) {
		return ModeResult{
			Rejected: true,
			Ack:      fmt.Sprintf("Mode is fixed at the bell (%s). Tonight runs as declared; the budget absorbs hard days.", bellTimeFor(chain, profile)),
		}, nil
	}

	day := clocks.ModeDay(now)
	dayID := engine.DayID(day)
	existing, found, err := r.store.ReadEngineDay(dayID)
	if err != nil {
		return ModeResult{}, err
	}
	if found {
		// First declaration wins: a record already stands for today, so this
		// re-declaration is an idempotent no-op that reports the fixed mode.
		return ModeResult{
			Mode: existing.Mode, LogicalDate: existing.LogicalDate, Idempotent: true,
			Ack: fmt.Sprintf("Mode already set to %s for %s.", existing.Mode, existing.LogicalDate),
		}, nil
	}

	rec := engine.BuildModeRecord(day, mode, now.Format(time.RFC3339), profile)
	if err := r.store.WriteEngineDay(rec); err != nil {
		return ModeResult{}, err
	}
	if _, err := r.store.RebuildEngineStatus(loc); err != nil {
		return ModeResult{}, err
	}
	return ModeResult{
		Mode: mode, LogicalDate: rec.LogicalDate,
		Ack: fmt.Sprintf("Mode set to %s for %s.", mode, rec.LogicalDate),
	}, nil
}

// modeGapFill fills the mode on a past logical day whose record carries none
// (engine-module.md §"`/mode --day` gap-fill"). It is deliberately narrower
// than `/closeout backfill`, because mode is the one field the bell fixes:
//
//   - Gap-fill only, never overwrite. A day whose mode was *declared* is
//     rejected; the bell still binds every day you showed up for. The gap is
//     read from `mode_declared_at` rather than from `mode`, because a close-out
//     builds its record with the chain default already in place — a day closed
//     out but never declared carries `mode: green` and no declaration
//     timestamp, and that default is a placeholder rather than testimony. The
//     guard is restated inside the storage op, so no call site can route around
//     it, and `mode` therefore stays off the foldable-field whitelist — the
//     correction path cannot reach it either.
//   - Bounded by backfill_window_days, the same chain.json knob `/closeout
//     backfill` uses, so the two retroactive paths cannot drift apart. Because
//     that rule requires a span of at least one day, today and any future day
//     are rejected for free.
//   - Two target shapes: a day with no record at all gets one carrying the
//     mode, with the profile governing the *target* day rather than the state
//     at write time (the rule a backfilled close-out follows); a day whose
//     record exists but declared no mode — the ordinary shape of a day closed
//     out and never declared — has only its two mode fields filled.
func (r *Router) modeGapFill(
	req ModeRequest, chain engine.ChainConfig,
	clocks engine.Clocks, now time.Time, loc *time.Location,
) (ModeResult, error) {
	_, target, err := resolveEngineDay(req.DayArg, now, clocks)
	if err != nil {
		return ModeResult{}, err
	}
	today := clocks.BaseLogicalDate(now)
	if !engine.BackfillInWindow(target, today, chain.BackfillWindowDays) {
		return ModeResult{Rejected: true, Ack: modeWindowMsg(target, today, chain.BackfillWindowDays)}, nil
	}

	dayID := engine.DayID(target)
	existing, found, err := r.store.ReadEngineDay(dayID)
	if err != nil {
		return ModeResult{}, err
	}
	declaredAt := now.Format(time.RFC3339)
	switch {
	case found && existing.ModeDeclaredAt != "":
		return ModeResult{Rejected: true, Ack: modeAlreadyDeclaredMsg}, nil
	case found:
		if err = r.store.FillEngineDayMode(dayID, req.Mode, declaredAt); err != nil {
			return ModeResult{}, err
		}
	default:
		if err = r.writeModeRecord(target, req.Mode, declaredAt, loc); err != nil {
			return ModeResult{}, err
		}
	}

	if _, err = r.store.RebuildEngineStatus(loc); err != nil {
		return ModeResult{}, err
	}
	date := engine.DateString(target)
	return ModeResult{
		Mode: req.Mode, LogicalDate: date,
		Ack: fmt.Sprintf("Mode set to %s for %s — a gap filled, not a change.", req.Mode, date),
	}, nil
}

// writeModeRecord creates a mode-only record for a past day that has none,
// stamping the profile that governed the *target* day rather than the one
// active now — the same rule a backfilled close-out follows, so a profile
// switch since then cannot rewrite how that day is read.
func (r *Router) writeModeRecord(target time.Time, mode engine.Mode, declaredAt string, loc *time.Location) error {
	state, err := r.store.ReadProfileState()
	if err != nil {
		return err
	}
	profile := engine.GoverningProfile(target, state.History, loc)
	return r.store.WriteEngineDay(engine.BuildModeRecord(target, mode, declaredAt, profile))
}

// modeWindowMsg names why a gap-fill target is out of bounds: too far back, or
// not yet over. Both are the same window rule (a span of at least one day, at
// most the configured window), but they are different mistakes and a single
// message would explain neither.
func modeWindowMsg(target, today time.Time, window int) string {
	if engine.DaysBetween(target, today) < 1 {
		return modeDayEndedMsg
	}
	return fmt.Sprintf("That's outside the mode gap-fill window (%d days).", window)
}

// governingClocks resolves the clock profile governing now's wall date and
// its parsed clock marks — shared by `/mode` and any command that needs the
// active bell/rollover for now.
func (r *Router) governingClocks(chain engine.ChainConfig, now time.Time) (profile string, clocks engine.Clocks, err error) {
	state, err := r.store.ReadProfileState()
	if err != nil {
		return "", engine.Clocks{}, err
	}
	profile = engine.GoverningProfile(now, state.History, now.Location())
	clocks, err = chain.ClocksFor(profile)
	if err != nil {
		return "", engine.Clocks{}, err
	}
	return profile, clocks, nil
}

// bellTimeFor returns the bell_time string for the governing profile — the
// default profile uses the top-level bell_time, a named profile its own.
func bellTimeFor(chain engine.ChainConfig, profile string) string {
	if profile != engine.DefaultProfile {
		if p, ok := chain.Profiles[profile]; ok {
			return p.BellTime
		}
	}
	return chain.BellTime
}
