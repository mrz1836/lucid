package router

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/workout"
)

// bootedWorkoutSurface returns a booted router whose lucid.json carries an enabled
// workout block pointing at a synthetic program and the two opaque prompt files,
// so the on-demand `Workout` intent composes against a real (empty) Ledger.
func bootedWorkoutSurface(t *testing.T) *Router {
	t.Helper()
	a := newScaffolded(t)
	require.NoError(t, a.ScaffoldObservations())
	ocfg, err := a.ReadObservationsConfig()
	require.NoError(t, err)
	ocfg.KindsEnabled = []observations.Kind{observations.KindWorkout, observations.KindBodyState}
	require.NoError(t, a.SaveObservationsConfig(ocfg))

	dir := t.TempDir()
	prog := filepath.Join(dir, "program.json")
	b, err := json.Marshal(workout.ExampleProgram())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(prog, b, 0o600))
	sys := filepath.Join(dir, "system_prompt.md")
	tmpl := filepath.Join(dir, "daily_template.md")
	require.NoError(t, os.WriteFile(sys, []byte("SYSTEM VOICE\n"), 0o600))
	require.NoError(t, os.WriteFile(tmpl, []byte("TEMPLATE BODY\n"), 0o600))

	cfg, err := a.LoadConfig()
	require.NoError(t, err)
	cfg.Workout = config.WorkoutConfig{
		Enabled: true, Program: prog, SlotTime: "12:00", SystemPrompt: sys, Template: tmpl,
	}
	require.NoError(t, a.SaveConfig(cfg))

	r := New(a)
	_, err = r.Boot()
	require.NoError(t, err)
	return r
}

// TestRouterWorkout_ComposesPhrasedMessage: the surface folds the deterministic
// pick, the honest engine numbers, and the model's phrasing into one message and
// projects the decided Recommendation/Trend for --json.
func TestRouterWorkout_ComposesPhrasedMessage(t *testing.T) {
	r := bootedWorkoutSurface(t)
	p := &provider.Fake{Script: []provider.Exchange{{Content: "You showed up — that's the whole game today."}}}

	res, err := r.Workout(context.Background(), nowEDT(), p)
	require.NoError(t, err)
	assert.True(t, res.UsedLLM)
	assert.False(t, res.Fallback)
	assert.Contains(t, res.Text, "You showed up")
	assert.NotContains(t, res.Text, "not medical advice")
	assert.NotEmpty(t, res.Recommendation.Primary.Name, "the decided pick is projected for --json")
	assert.Equal(t, 1, p.Calls())
	assert.Equal(t, "workout.daily", p.Requests[0].Intent)
}

// TestRouterWorkout_FallbackOnProviderDown: an unreachable provider renders the
// deterministic scaffold — the pick stands, only warmth is lost.
func TestRouterWorkout_FallbackOnProviderDown(t *testing.T) {
	r := bootedWorkoutSurface(t)
	p := &provider.Fake{Script: []provider.Exchange{{Err: provider.ErrUnavailable}}}

	res, err := r.Workout(context.Background(), nowEDT(), p)
	require.NoError(t, err)
	assert.True(t, res.Fallback)
	assert.False(t, res.UsedLLM)
	assert.NotContains(t, res.Text, "not medical advice")
}

// TestRouterWorkout_MissingProgramIsLoud: an enabled surface pointed at an absent
// program fails loudly rather than composing a synthetic program.
func TestRouterWorkout_MissingProgramIsLoud(t *testing.T) {
	r := bootedWorkoutSurface(t)
	cfg := r.cfg
	cfg.Workout.Program = filepath.Join(t.TempDir(), "gone.json")
	r.cfg = cfg

	_, err := r.Workout(context.Background(), nowEDT(), &provider.Fake{})
	require.Error(t, err)
}
