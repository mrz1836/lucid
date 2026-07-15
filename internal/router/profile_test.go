package router

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
)

// TestProfile_EffectiveNextDay: a `/profile` switch after tonight's bell is
// effective the next logical day, never the current one.
func TestProfile_EffectiveNextDay(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	res, err := r.Profile("nights", atUTC(2026, 7, 7, 21, 50)) // after default bell 19:00
	require.NoError(t, err)
	assert.False(t, res.Rejected)
	assert.Equal(t, "2026-07-08", res.Effective)
	assert.Equal(t, "Profile switches to nights — effective 2026-07-08.", res.Ack)

	state, err := a.ReadProfileState()
	require.NoError(t, err)
	assert.Equal(t, "nights", state.Active)
	require.Len(t, state.History, 1)
	assert.Equal(t, engine.DefaultProfile, state.History[0].From)
}

// TestProfile_UndefinedRejected is the undefined-profile rejection copy
// (engine-module.md §Error states) with no disk effect.
func TestProfile_UndefinedRejected(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	res, err := r.Profile("weekends", atUTC(2026, 7, 7, 21, 50))
	require.NoError(t, err)
	assert.True(t, res.Rejected)
	assert.Equal(t, profileRejectMsg, res.Ack)

	state, err := a.ReadProfileState()
	require.NoError(t, err)
	assert.Equal(t, engine.DefaultProfile, state.Active)
	assert.Empty(t, state.History)
}

// TestProfile_NightsFixture is the engine-module.md §Phase 8 profile
// fixture: switching to nights (rollover 12:00) after tonight's bell leaves
// that night on the old clocks, while the next day's 11:00 close-out
// attributes to the new profile's previous logical day and stamps nights.
func TestProfile_NightsFixture(t *testing.T) {
	t.Run("switch night stays on old clocks", func(t *testing.T) {
		r, a, _ := newBootedRouter(t)
		_, err := r.Profile("nights", atUTC(2026, 7, 7, 21, 50))
		require.NoError(t, err)

		// A close-out later that same night (07-07): the switch is not yet
		// effective, so default clocks (rollover 04:00) attribute it to 07-07
		// and stamp the default profile.
		res, err := r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 7, 22, 10), Links: compactLinks(), Journal: "old clocks"})
		require.NoError(t, err)
		assert.Equal(t, "2026-07-07", res.LogicalDate)
		rec := readDay(t, a, "day_2026_07_07")
		assert.Equal(t, engine.DefaultProfile, rec.Profile)
	})

	t.Run("next-day 11:00 attributes to previous logical day under nights", func(t *testing.T) {
		r, a, _ := newBootedRouter(t)
		_, err := r.Profile("nights", atUTC(2026, 7, 7, 21, 50)) // effective 07-08
		require.NoError(t, err)

		// 11:00 on 07-08: nights governs (wall date ≥ effective); its rollover
		// is 12:00, so 11:00 attributes to 07-07 — the new profile's previous
		// logical day — and the record stamps nights.
		res, err := r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 8, 11, 0), Links: compactLinks(), Journal: "night shift"})
		require.NoError(t, err)
		assert.Equal(t, "2026-07-07", res.LogicalDate)
		rec := readDay(t, a, "day_2026_07_07")
		assert.Equal(t, "nights", rec.Profile)
	})
}

// TestChain exercises the router's chain accessor used by the CLI parser.
func TestChain(t *testing.T) {
	r, _, _ := newBootedRouter(t)
	chain, err := r.Chain()
	require.NoError(t, err)
	assert.Equal(t, []string{"journal", "dock", "read"}, chain.LinkKeys())
}
