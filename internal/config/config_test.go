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

// TestDefault_ProviderBlock pins the shipped provider defaults
// (data-model.md §"lucid.json"; ADR-0006): the zero-setup Claude CLI
// backend on model opus, a 120s call bound, the Ollama base URL, and an
// empty reserved per-role map.
func TestDefault_ProviderBlock(t *testing.T) {
	p := Default().Provider
	assert.Equal(t, "claude_cli", p.Backend)
	assert.Equal(t, "opus", p.Model)
	assert.Equal(t, 120, p.TimeoutSeconds)
	assert.Equal(t, "http://localhost:11434", p.Endpoint)
	assert.NotNil(t, p.Roles)
	assert.Empty(t, p.Roles, "roles map is reserved but empty this pillar")
}

// TestProvider_MarshalsDocumentedShape asserts a marshaled default
// config carries the provider block exactly as documented, including the
// reserved roles map rendering as an empty object (not null).
func TestProvider_MarshalsDocumentedShape(t *testing.T) {
	b, err := Default().Marshal()
	require.NoError(t, err)

	var m struct {
		Provider struct {
			Backend        string         `json:"backend"`
			Model          string         `json:"model"`
			TimeoutSeconds int            `json:"timeout_seconds"`
			Endpoint       string         `json:"endpoint"`
			Roles          map[string]any `json:"roles"`
		} `json:"provider"`
	}
	require.NoError(t, json.Unmarshal(b, &m))
	assert.Equal(t, "claude_cli", m.Provider.Backend)
	assert.Equal(t, "opus", m.Provider.Model)
	assert.Equal(t, 120, m.Provider.TimeoutSeconds)
	assert.Equal(t, "http://localhost:11434", m.Provider.Endpoint)
	assert.NotNil(t, m.Provider.Roles, "roles marshals as {} not null")
	assert.Empty(t, m.Provider.Roles)

	// The reserved roles map serializes as an empty JSON object.
	assert.Contains(t, string(b), `"roles": {}`)
	// No API key leaks into the config, ever.
	assert.NotContains(t, string(b), "api_key")
	assert.NotContains(t, string(b), "apikey")
}

// TestProvider_RoundTripWithRoleOverride proves a hand-edited per-role
// override survives a marshal/unmarshal cycle even though the router
// does not consume it this pillar — the schema reserves it.
func TestProvider_RoundTripWithRoleOverride(t *testing.T) {
	c := Default()
	c.Provider.Roles = map[string]ProviderRole{
		"reflection": {Backend: "ollama", Model: "qwen2.5:14b"},
	}
	b, err := c.Marshal()
	require.NoError(t, err)

	got, err := Unmarshal(b)
	require.NoError(t, err)
	assert.Equal(t, c, got)
	assert.Equal(t, "ollama", got.Provider.Roles["reflection"].Backend)
	assert.Equal(t, "qwen2.5:14b", got.Provider.Roles["reflection"].Model)
}

// TestDefault_CompanionBlock pins the shipped companion default: the
// feature is off and every prompt-file path is empty so a fresh Ledger
// runs the pure Engine teeth until an operator opts in (data-model.md
// §"lucid.json").
func TestDefault_CompanionBlock(t *testing.T) {
	c := Default().Companion
	assert.False(t, c.Enabled, "companion ships disabled")
	assert.Empty(t, c.MorningTemplate)
	assert.Empty(t, c.NightTemplate)
	assert.Empty(t, c.SystemPrompt)
	assert.Empty(t, c.MorningRoutine, "routine paths ship empty → feature off")
	assert.Empty(t, c.NightRoutine, "routine paths ship empty → feature off")
	assert.Empty(t, c.Model, "model override empty → inherits provider.model")
}

// TestCompanion_MarshalsDocumentedShape asserts a marshaled default config
// carries the companion block with the explicit per-file path keys, off by
// default, and — like the provider block — never leaks a token or channel
// id into lucid.json (those stay env-only).
func TestCompanion_MarshalsDocumentedShape(t *testing.T) {
	b, err := Default().Marshal()
	require.NoError(t, err)

	var m struct {
		Companion struct {
			Enabled         bool   `json:"enabled"`
			MorningTemplate string `json:"morning_template"`
			NightTemplate   string `json:"night_template"`
			SystemPrompt    string `json:"system_prompt"`
			MorningRoutine  string `json:"morning_routine"`
			NightRoutine    string `json:"night_routine"`
			Model           string `json:"model"`
		} `json:"companion"`
	}
	require.NoError(t, json.Unmarshal(b, &m))
	assert.False(t, m.Companion.Enabled)
	assert.Empty(t, m.Companion.MorningTemplate)
	assert.Empty(t, m.Companion.NightTemplate)
	assert.Empty(t, m.Companion.SystemPrompt)
	assert.Empty(t, m.Companion.MorningRoutine)
	assert.Empty(t, m.Companion.NightRoutine)

	s := string(b)
	assert.Contains(t, s, `"companion":`)
	assert.Contains(t, s, `"morning_template":`)
	assert.Contains(t, s, `"system_prompt":`)
	// The optional routine path keys always render, even when empty, so an
	// operator can see the seam to point at their own routine docs.
	assert.Contains(t, s, `"morning_routine":`)
	assert.Contains(t, s, `"night_routine":`)
	// No token or channel id ever lands in the config.
	assert.NotContains(t, s, "harness_token")
	assert.NotContains(t, s, "channel_id")
}

// TestCompanion_RoundTripEnabled proves a fully-configured companion block
// survives a marshal/unmarshal cycle byte-identically in value — the
// explicit per-file paths and the optional model override.
func TestCompanion_RoundTripEnabled(t *testing.T) {
	c := Default()
	c.Companion = CompanionConfig{
		Enabled:         true,
		MorningTemplate: "/opt/lucid/companion/morning_template.md",
		NightTemplate:   "/opt/lucid/companion/night_template.md",
		SystemPrompt:    "/opt/lucid/companion/system_prompt.md",
		MorningRoutine:  "/opt/lucid/companion/morning_routine.md",
		NightRoutine:    "/opt/lucid/companion/night_routine.md",
		Model:           "sonnet",
	}
	b, err := c.Marshal()
	require.NoError(t, err)

	got, err := Unmarshal(b)
	require.NoError(t, err)
	assert.Equal(t, c, got)
	assert.Equal(t, "sonnet", got.Companion.Model)
	assert.Equal(t, "/opt/lucid/companion/morning_routine.md", got.Companion.MorningRoutine)
	assert.Equal(t, "/opt/lucid/companion/night_routine.md", got.Companion.NightRoutine)
}

// TestValidate_CompanionEnabledRequiresPaths is the companion validate
// rule: an enabled companion missing any one of the three prompt-file
// paths is a hard error, while all three set (with or without a model
// override) validates.
func TestValidate_CompanionEnabledRequiresPaths(t *testing.T) {
	full := CompanionConfig{
		Enabled:         true,
		MorningTemplate: "m.md",
		NightTemplate:   "n.md",
		SystemPrompt:    "s.md",
	}
	failures := map[string]func(*CompanionConfig){
		"missing morning": func(c *CompanionConfig) { c.MorningTemplate = "" },
		"missing night":   func(c *CompanionConfig) { c.NightTemplate = "" },
		"missing system":  func(c *CompanionConfig) { c.SystemPrompt = "" },
	}
	for name, mutate := range failures {
		t.Run(name, func(t *testing.T) {
			c := Default()
			c.Companion = full
			mutate(&c.Companion)
			assert.Error(t, c.Validate())
		})
	}

	t.Run("all paths set validates", func(t *testing.T) {
		c := Default()
		c.Companion = full
		assert.NoError(t, c.Validate())
	})
	t.Run("model override does not require a known name", func(t *testing.T) {
		c := Default()
		c.Companion = full
		c.Companion.Model = "some-future-model"
		assert.NoError(t, c.Validate())
	})
}

// TestValidate_CompanionRoutinePathsOptional proves the routine paths are
// enrichment-only: an enabled companion with all three prompt paths set
// validates whether the morning_routine/night_routine keys are empty or set,
// and adding routine paths never rescues a companion that is still missing a
// required prompt path.
func TestValidate_CompanionRoutinePathsOptional(t *testing.T) {
	base := CompanionConfig{
		Enabled:         true,
		MorningTemplate: "m.md",
		NightTemplate:   "n.md",
		SystemPrompt:    "s.md",
	}

	t.Run("routine paths empty validates", func(t *testing.T) {
		c := Default()
		c.Companion = base
		assert.NoError(t, c.Validate())
	})
	t.Run("routine paths set validates", func(t *testing.T) {
		c := Default()
		c.Companion = base
		c.Companion.MorningRoutine = "/r/morning.md"
		c.Companion.NightRoutine = "/r/night.md"
		assert.NoError(t, c.Validate())
	})
	t.Run("routine paths do not rescue a missing prompt path", func(t *testing.T) {
		c := Default()
		c.Companion = base
		c.Companion.SystemPrompt = ""
		c.Companion.MorningRoutine = "/r/morning.md"
		c.Companion.NightRoutine = "/r/night.md"
		assert.Error(t, c.Validate())
	})
}

// TestValidate_CompanionDisabledIgnoresPaths confirms that while disabled
// (the default), empty template paths are tolerated — the block is inert,
// so it never blocks a load.
func TestValidate_CompanionDisabledIgnoresPaths(t *testing.T) {
	c := Default()
	c.Companion = CompanionConfig{Enabled: false} // all paths empty
	assert.NoError(t, c.Validate())
}

func TestValidate_Good(t *testing.T) {
	require.NoError(t, Default().Validate())
}

// TestValidate_ProviderFailures covers the provider block rules Validate
// enforces: an unknown backend name (top-level or in a reserved role
// override) and a non-positive per-call timeout are hard errors.
func TestValidate_ProviderFailures(t *testing.T) {
	tests := map[string]func(*Config){
		"unknown backend":      func(c *Config) { c.Provider.Backend = "gpt5_cli" },
		"zero timeout":         func(c *Config) { c.Provider.TimeoutSeconds = 0 },
		"negative timeout":     func(c *Config) { c.Provider.TimeoutSeconds = -1 },
		"unknown role backend": func(c *Config) { c.Provider.Roles = map[string]ProviderRole{"intake": {Backend: "nope"}} },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			c := Default()
			mutate(&c)
			assert.Error(t, c.Validate())
		})
	}
}

// TestValidate_ProviderKnownBackends confirms both shipped backends pass
// validation, and that an empty backend is tolerated (the caller falls
// back to the documented default rather than erroring at load).
func TestValidate_ProviderKnownBackends(t *testing.T) {
	for _, backend := range []string{"claude_cli", "ollama", ""} {
		c := Default()
		c.Provider.Backend = backend
		assert.NoError(t, c.Validate(), "backend %q should validate", backend)
	}
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

// TestDefault_FrameworkLayerOff pins the shipped framework default: no lens is
// stacked or consented, so the reflection voice stays baseline until an
// operator amends the Charter stack (docs/frameworks.md §3). The collections
// are non-nil so the file renders [] / {} rather than null.
func TestDefault_FrameworkLayerOff(t *testing.T) {
	c := Default()
	assert.NotNil(t, c.FrameworkStack)
	assert.Empty(t, c.FrameworkStack)
	assert.NotNil(t, c.FrameworkConsents)
	assert.Empty(t, c.FrameworkConsents)

	for _, id := range []string{
		"attachment-theory", "eight-dates", "four-agreements", "ifs", "nvc", "stoicism",
	} {
		assert.Falsef(t, c.LensConsented(id), "no lens is consented by default: %q", id)
	}
	if _, ok := c.ActiveFramework(); ok {
		t.Error("ActiveFramework over the default stack: want none")
	}
}

// TestLensConsented_FailsClosed proves a lens frames proposals only when it is
// both in the standing stack AND carries a recorded consent timestamp — a
// stacked-but-unconsented entry, a dangling consent for an unstacked lens, an
// unknown id, and the empty id all read as not consented.
func TestLensConsented_FailsClosed(t *testing.T) {
	c := Default()
	c.FrameworkStack = []string{"stoicism", "ifs"}
	c.FrameworkConsents = map[string]string{
		"stoicism": "2026-07-05T18:00:00-04:00",
		// ifs is stacked but never consented; nvc is consented but not stacked.
		"nvc": "2026-07-05T18:02:00-04:00",
	}
	assert.True(t, c.LensConsented("stoicism"), "stacked + consented ⇒ consented")
	assert.False(t, c.LensConsented("ifs"), "stacked but not consented ⇒ fails closed")
	assert.False(t, c.LensConsented("nvc"), "consented but not stacked ⇒ fails closed")
	assert.False(t, c.LensConsented("stoicism-typo"), "unknown id ⇒ not consented")
	assert.False(t, c.LensConsented(""), "empty id ⇒ not consented")

	// An empty-string consent value is treated as no consent (fails closed).
	c.FrameworkStack = append(c.FrameworkStack, "nvc")
	c.FrameworkConsents["nvc"] = ""
	assert.False(t, c.LensConsented("nvc"), "empty consent timestamp ⇒ fails closed")
}

// TestActiveFramework_FirstConsentedInStackOrder proves the active lens is
// selected deterministically — the first consented lens in stack order — with
// no rotation: a leading unconsented entry is skipped, and re-running never
// changes the pick.
func TestActiveFramework_FirstConsentedInStackOrder(t *testing.T) {
	c := Default()
	// ifs leads the stack but is unconsented, so stoicism (next, consented) wins.
	c.FrameworkStack = []string{"ifs", "stoicism", "nvc"}
	c.FrameworkConsents = map[string]string{
		"stoicism": "2026-07-05T18:00:00-04:00",
		"nvc":      "2026-07-05T18:02:00-04:00",
	}
	got, ok := c.ActiveFramework()
	require.True(t, ok)
	assert.Equal(t, "stoicism", got)
	// Deterministic: a second call yields the same pick (no rotation).
	again, _ := c.ActiveFramework()
	assert.Equal(t, got, again)

	// No stacked lens consented ⇒ the baseline voice.
	c.FrameworkConsents = map[string]string{}
	_, ok = c.ActiveFramework()
	assert.False(t, ok)
}

// TestFramework_MarshalsEmptyCollections asserts the default framework block
// renders [] / {} (never null) and that a populated block survives a
// marshal/unmarshal round-trip byte-identically in value.
func TestFramework_MarshalsEmptyCollections(t *testing.T) {
	b, err := Default().Marshal()
	require.NoError(t, err)
	s := string(b)
	assert.Contains(t, s, `"framework_stack": []`)
	assert.Contains(t, s, `"framework_consents": {}`)

	c := Default()
	c.FrameworkStack = []string{"stoicism"}
	c.FrameworkConsents = map[string]string{"stoicism": "2026-07-05T18:00:00-04:00"}
	rb, err := c.Marshal()
	require.NoError(t, err)
	got, err := Unmarshal(rb)
	require.NoError(t, err)
	assert.Equal(t, c, got)
}

// TestValidate_FrameworkBlockOptional confirms the framework fields are
// additive and optional — a config carrying a stack + consents still validates,
// and so does one that omits them entirely (nil collections).
func TestValidate_FrameworkBlockOptional(t *testing.T) {
	c := Default()
	c.FrameworkStack = []string{"stoicism"}
	c.FrameworkConsents = map[string]string{"stoicism": "2026-07-05T18:00:00-04:00"}
	assert.NoError(t, c.Validate())

	c.FrameworkStack = nil
	c.FrameworkConsents = nil
	assert.NoError(t, c.Validate(), "nil framework collections validate (layer off)")
}
