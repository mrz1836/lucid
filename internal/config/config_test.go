package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDefault_MatchesDocumentedSchema pins the documented default
// values (data-model.md §"lucid.json"). These back the acceptance-
// criteria jq check: version==1 && recent_window_max==14 &&
// ask_insights_cap==50.
func TestDefault_MatchesDocumentedSchema(t *testing.T) {
	c := Default()
	assert.Equal(t, 1, c.Version)
	assert.Equal(t, "~/.lucid/", c.Home)
	assert.Equal(t, "data/person_keys_wordlist.txt", c.WordlistPath)
	assert.Equal(t, 7, c.RecentWindow)
	assert.Equal(t, 14, c.RecentWindowMax)
	assert.Equal(t, 4, c.IntakeMaxQuestions)
	assert.Equal(t, 50, c.AskInsightsCap)
	assert.Equal(t, 12, c.AskReflectionsCap)
	assert.Equal(t, 3, c.ProposalPause.UnansweredThreshold)
	assert.Equal(t, 14, c.ProposalPause.PauseDays)
	assert.InDelta(t, 0.5, c.PersonDominanceThreshold, 1e-9)
	assert.Equal(t, "intake-2026.05.0", c.AgentVersions.Intake)
	assert.Equal(t, "safety-2026.05.0", c.AgentVersions.SafetyConsent)
	assert.False(t, c.BootstrapMode)
}

// TestDefault_JQContract asserts the exact predicate the acceptance
// criteria run with jq against a freshly written lucid.json.
func TestDefault_JQContract(t *testing.T) {
	b, err := Default().Marshal()
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	assert.EqualValues(t, 1, m["version"])
	assert.EqualValues(t, 14, m["recent_window_max"])
	assert.EqualValues(t, 50, m["ask_insights_cap"])
}

func TestMirrorDirs_SixInOrder(t *testing.T) {
	assert.Equal(t,
		[]string{"raw", "processed", "insights", "people", "sessions", "reflections"},
		Default().MirrorDirs())
}

func TestClip_InRangeUnchanged(t *testing.T) {
	c := Default()
	out, warnings := c.Clip()
	assert.Empty(t, warnings)
	assert.Equal(t, c, out)
}

// TestClip_AboveCeiling is acceptance test case 1.4: recent_window: 999
// clips to recent_window_max (14) with a warning; the receiver is not
// mutated.
func TestClip_AboveCeiling(t *testing.T) {
	c := Default()
	c.RecentWindow = 999
	out, warnings := c.Clip()
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "recent_window 999")
	assert.Contains(t, warnings[0], "clipped to 14")
	assert.Equal(t, 14, out.RecentWindow)
	assert.Equal(t, 999, c.RecentWindow, "Clip must not mutate the receiver")
}

func TestClip_BelowMinimum(t *testing.T) {
	c := Default()
	c.RecentWindow = 0
	out, warnings := c.Clip()
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "below minimum")
	assert.Equal(t, 1, out.RecentWindow)
}

// TestClip_ZeroCeilingFallsBackToDefaultMax covers a hand-edited config
// that also zeroed recent_window_max: the clip uses the default ceiling
// rather than clipping everything to zero.
func TestClip_ZeroCeilingFallsBackToDefaultMax(t *testing.T) {
	c := Default()
	c.RecentWindowMax = 0
	c.RecentWindow = 999
	out, warnings := c.Clip()
	require.Len(t, warnings, 1)
	assert.Equal(t, 14, out.RecentWindow)
}

func TestValidate_Good(t *testing.T) {
	require.NoError(t, Default().Validate())
}

func TestValidate_Failures(t *testing.T) {
	tests := map[string]func(*Config){
		"bad version":            func(c *Config) { c.Version = 2 },
		"empty raw_dir":          func(c *Config) { c.RawDir = "" },
		"empty processed_dir":    func(c *Config) { c.ProcessedDir = "" },
		"empty insights_dir":     func(c *Config) { c.InsightsDir = "" },
		"empty people_dir":       func(c *Config) { c.PeopleDir = "" },
		"empty sessions_dir":     func(c *Config) { c.SessionsDir = "" },
		"empty reflections_dir":  func(c *Config) { c.ReflectionsDir = "" },
		"bad recent_window_max":  func(c *Config) { c.RecentWindowMax = 0 },
		"bad ask_insights_cap":   func(c *Config) { c.AskInsightsCap = 0 },
		"bad ask_reflectionscap": func(c *Config) { c.AskReflectionsCap = 0 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			c := Default()
			mutate(&c)
			assert.Error(t, c.Validate())
		})
	}
}

// TestMarshalUnmarshal_RoundTrip proves a default config survives a
// write/read cycle byte-identically in value.
func TestMarshalUnmarshal_RoundTrip(t *testing.T) {
	c := Default()
	b, err := c.Marshal()
	require.NoError(t, err)
	assert.Equal(t, byte('\n'), b[len(b)-1], "marshaled config ends with a newline")

	got, err := Unmarshal(b)
	require.NoError(t, err)
	assert.Equal(t, c, got)
}

func TestUnmarshal_BadJSON(t *testing.T) {
	_, err := Unmarshal([]byte("{not json"))
	assert.Error(t, err)
}
