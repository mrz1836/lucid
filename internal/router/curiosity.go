package router

import (
	"time"

	"github.com/mrz1836/lucid/internal/observations"
)

// Curiosity returns at most one deterministic micro-question for the current
// logical day (observations.md §6), honoring the per-day budget and the 7-day /
// 3-ignore backoff, and persisting the ephemeral ask-state. It is the reachable
// seam for the capture surface (and the Phase 14 skill) to attach a small
// contextual question to a capture ack — never a standalone ping, never a
// question that touches the Ledger. painWithoutSite says whether the capture
// that just happened was a pain event lacking a site; an empty return means no
// question fires. Silence is a complete answer; only the non-answer is never
// recorded.
func (r *Router) Curiosity(now time.Time, painWithoutSite bool) (string, error) {
	now = whenOr(now)
	cfg, err := r.store.ReadObservationsConfig()
	if err != nil {
		return "", err
	}
	day := observations.DateString(observations.DateOf(now))
	onFile, err := r.store.LocationOnFile(day)
	if err != nil {
		return "", err
	}

	state, err := r.store.ReadCuriosityState()
	if err != nil {
		return "", err
	}
	ask, next, asked := observations.ChooseCuriosity(state, observations.CuriosityContext{
		Day:             day,
		HasLocation:     onFile,
		PainWithoutSite: painWithoutSite,
	}, cfg.CuriosityBudgetDay)
	if err := r.store.WriteCuriosityState(next); err != nil {
		return "", err
	}
	if !asked {
		return "", nil
	}
	return ask, nil
}
