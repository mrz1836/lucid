package router

// This file owns the on-demand workout surface — the `lucid workout` intent. It
// is deliberately separate from workout.go (the capture surface): that file
// imports the extraction agent (internal/agents/workout) as `workout`, while this
// one imports the recommender/compose package (internal/workout) as `workout`,
// and Go's file-scoped imports let both names live in one package without a
// collision. The deterministic core owns the pick; the passed provider only
// phrases it.

import (
	"context"
	"time"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/workout"
)

// WorkoutResult is the on-demand `lucid workout` surface: the rendered
// recommendation + trend message a person reads (Text), the deterministic
// Recommendation / Trend projection a script reads (--json), and how the compose
// resolved — model-phrased (UsedLLM), deterministic fallback (Fallback), or the
// recent-slice read degraded (EnrichmentDegraded).
type WorkoutResult struct {
	Text               string
	UsedLLM            bool
	Fallback           bool
	EnrichmentDegraded bool
	Recommendation     workout.Recommendation
	Trend              workout.Trend
}

// Workout composes the on-demand workout recommendation at now. The deterministic
// core owns the pick (rotation, recovery windows, pain hard stops); the passed
// provider only phrases the already-decided plan, and a provider outage renders
// the deterministic scaffold instead — the model never changes the pick. It reads
// the configured program on its opaque path, the bounded recent workout/body-state
// slice, the active injuries, and the honest engine numbers. A missing program or
// prompt file is a loud error; the recent-slice / injury reads degrade quietly to
// the plain-calendar path.
func (r *Router) Workout(ctx context.Context, now time.Time, p provider.Provider) (WorkoutResult, error) {
	if err := r.prepareObservations(); err != nil {
		return WorkoutResult{}, err
	}
	res, err := workout.New(workout.Deps{
		Workout:      r.cfg.Workout,
		Provider:     r.cfg.Provider,
		Metrics:      workoutMetrics{r},
		Observations: r.store,
		Injuries:     r.store,
		Build:        func(config.ProviderConfig) (provider.Provider, error) { return p, nil },
	}).Compose(ctx, now)
	if err != nil {
		return WorkoutResult{}, err
	}
	return WorkoutResult{
		Text:               res.Text,
		UsedLLM:            res.UsedLLM,
		Fallback:           res.Fallback,
		EnrichmentDegraded: res.EnrichmentDegraded,
		Recommendation:     res.Recommendation,
		Trend:              res.Trend,
	}, nil
}

// workoutMetrics adapts the router's own Metrics result to the [engine.Metrics]
// the workout composer folds the streak/adherence from, so internal/workout never
// imports the router (the two would otherwise form an import cycle).
type workoutMetrics struct{ r *Router }

// Metrics returns the engine metrics projection, dropping the rendered lines the
// composer does not need.
func (m workoutMetrics) Metrics(now time.Time) (engine.Metrics, error) {
	res, err := m.r.Metrics(now)
	if err != nil {
		return engine.Metrics{}, err
	}
	return res.Metrics, nil
}
