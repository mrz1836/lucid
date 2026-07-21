package observations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	c := DefaultConfig()
	assert.Equal(t, ConfigVersion, c.Version)
	assert.Equal(t, []Kind{KindPain, KindIntake, KindElimination, KindMood}, c.KindsEnabled)
	assert.Equal(t, 1, c.CuriosityBudgetDay)
	require.Len(t, c.Enrichers, 2)
	assert.Equal(t, "weather", c.Enrichers[0].Name)
	assert.False(t, c.Enrichers[0].Enabled)
	assert.Equal(t, "calendar-frame", c.Enrichers[1].Name)
	assert.True(t, c.Enrichers[1].Enabled)
}

func TestConfig_KindEnabled(t *testing.T) {
	c := DefaultConfig()
	assert.True(t, c.KindEnabled(KindPain))
	assert.False(t, c.KindEnabled(KindSleep)) // not in the default set
	assert.False(t, c.KindEnabled(KindLocation))
	// The workout-module kinds are enable-gated and off by default (like the
	// companion-context kinds): a fresh Ledger never carries them.
	assert.False(t, c.KindEnabled(KindWorkout))
	assert.False(t, c.KindEnabled(KindBodyState))
	// The generic enable hint covers the new kinds, so a disabled capture is
	// rejected with a useful pointer rather than silently stored.
	assert.Contains(t, EnableHint(KindWorkout), "workout")
	assert.Contains(t, EnableHint(KindWorkout), "observations/config.json")
}

func TestEnableHint(t *testing.T) {
	hint := EnableHint(KindPain)
	assert.Contains(t, hint, "pain")
	assert.Contains(t, hint, "observations/config.json")
	assert.Contains(t, hint, "isn't enabled")
}

func TestConfig_MarshalRoundTrip(t *testing.T) {
	c := DefaultConfig()
	c.KeySalt = "deadbeef"
	b, err := c.Marshal()
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(string(b), "\n"))

	got, err := UnmarshalConfig(b)
	require.NoError(t, err)
	assert.Equal(t, c.KeySalt, got.KeySalt)
	assert.Equal(t, c.KindsEnabled, got.KindsEnabled)
	assert.Equal(t, c.Enrichers, got.Enrichers)

	_, err = UnmarshalConfig([]byte("{not json"))
	require.Error(t, err)
}

func TestConfig_MarshalNormalizesEmptyCollections(t *testing.T) {
	c := Config{Version: ConfigVersion, KeySalt: "x"}
	b, err := c.Marshal()
	require.NoError(t, err)
	s := string(b)
	assert.Contains(t, s, `"kinds_enabled": []`)
	assert.Contains(t, s, `"agent_slice_optins": {}`)
	assert.Contains(t, s, `"clinical_context": []`)
	assert.Contains(t, s, `"enrichers": []`)
}

func TestConfig_Validate(t *testing.T) {
	c := DefaultConfig()
	c.KeySalt = "x"
	require.NoError(t, c.Validate())

	bad := c
	bad.Version = 99
	require.Error(t, bad.Validate())

	bad = c
	bad.KeySalt = ""
	require.Error(t, bad.Validate())
}
