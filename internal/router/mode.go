package router

import (
	"fmt"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
)

// modeInvalidMsg is returned when `/mode` names something that is not one of
// the three declarable modes.
const modeInvalidMsg = "Mode must be one of green, yellow, or red."

// ModeResult reports a `/mode` declaration. Rejected marks a declaration made
// after the bell (no disk effect); Invalid marks an unknown mode name; when
// neither is set the mode was declared (or already stood) for LogicalDate.
type ModeResult struct {
	Mode        string
	LogicalDate string
	Rejected    bool
	Invalid     bool
	Idempotent  bool
	Ack         string
}

// Mode executes `/mode <green|yellow|red>` (engine-module.md §Commands):
// declare today's mode, fixed at the bell. The declaration is rejected once
// the governing day's bell has rung — the mode is fixed at the bell with no
// retroactive amendment (engine §2) — and the first declaration wins, so a
// second `/mode` before the bell is an idempotent no-op. The mode is written
// as the day record's immutable base; the evening close-out folds the
// practice result in via corrections, never touching the declared mode.
func (r *Router) Mode(mode string, now time.Time) (ModeResult, error) {
	now = whenOr(now)
	loc := now.Location()

	if !engine.ValidMode(mode) {
		return ModeResult{Invalid: true, Ack: modeInvalidMsg}, nil
	}
	if err := r.store.ScaffoldEngine(); err != nil {
		return ModeResult{}, fmt.Errorf("could not prepare the engine tree: %w", err)
	}
	chain, err := r.store.ReadChainConfig()
	if err != nil {
		return ModeResult{}, err
	}

	profile, clocks, err := r.governingClocks(chain, now)
	if err != nil {
		return ModeResult{}, err
	}

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
