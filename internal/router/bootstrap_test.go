package router

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBootstrap_Enter toggles historical-entry mode on: the persisted config and
// the router's effective config both flip, and the enter ack names the paused
// surface.
func TestBootstrap_Enter(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	res, err := r.Bootstrap(BootstrapRequest{Done: false})
	require.NoError(t, err)

	assert.True(t, res.BootstrapMode)
	assert.Equal(t, bootstrapOnAck, res.Ack)
	assert.True(t, r.Config().BootstrapMode, "the router's effective config follows the toggle")

	persisted, err := a.LoadConfig()
	require.NoError(t, err)
	assert.True(t, persisted.BootstrapMode, "lucid.json records the mode")
}

// TestBootstrap_E6_Done is §E-6: /bootstrap done flips the mode off, runs no
// consolidation pass, and surfaces the resume ack.
func TestBootstrap_E6_Done(t *testing.T) {
	r, a, home := newBootedRouter(t)

	_, err := r.Bootstrap(BootstrapRequest{Done: false})
	require.NoError(t, err)

	insightsBefore := countFiles(t, home, "insights")
	processedBefore := countFiles(t, home, "processed")
	reflectionsBefore := countFiles(t, home, "reflections")

	res, err := r.Bootstrap(BootstrapRequest{Done: true})
	require.NoError(t, err)

	assert.False(t, res.BootstrapMode)
	assert.Equal(t, bootstrapDoneAck, res.Ack)
	assert.Equal(t, "Done. Pattern proposals will resume on the next `/checkin`.", res.Ack)
	assert.False(t, r.Config().BootstrapMode)

	persisted, err := a.LoadConfig()
	require.NoError(t, err)
	assert.False(t, persisted.BootstrapMode, "lucid.json records bootstrap_mode: false")

	// No consolidation pass: exiting bootstrap creates no derived records.
	assert.Equal(t, insightsBefore, countFiles(t, home, "insights"))
	assert.Equal(t, processedBefore, countFiles(t, home, "processed"))
	assert.Equal(t, reflectionsBefore, countFiles(t, home, "reflections"))
}

// TestBootstrap_RoundTrip confirms on → off leaves the config back at the
// documented default (false).
func TestBootstrap_RoundTrip(t *testing.T) {
	r, _, _ := newBootedRouter(t)

	on, err := r.Bootstrap(BootstrapRequest{Done: false})
	require.NoError(t, err)
	require.True(t, on.BootstrapMode)

	off, err := r.Bootstrap(BootstrapRequest{Done: true})
	require.NoError(t, err)
	assert.False(t, off.BootstrapMode)
}
