package config

import (
	"encoding/json"
	"strings"
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
	assert.Equal(t, 40, c.SelfFactsCap)
	assert.Equal(t, 35, c.ReflectWeekMaxDays)
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

// TestDefault_ReflectWeekCap pins the weekly deep-dive's catch-up ceiling: five
// weeks, and it reaches the on-disk lucid.json under the documented key so an
// operator can raise or lower it by hand. Kept separate from the jq-contract
// test above, which mirrors a fixed acceptance-criteria predicate.
func TestDefault_ReflectWeekCap(t *testing.T) {
	b, err := Default().Marshal()
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	assert.EqualValues(t, 35, m["reflect_week_max_days"], "the catch-up ceiling is five weeks")
}

// TestValidate_ReflectWeekCapAccepted proves the >= 1 check is a floor, not a
// pin: a hand-edited ceiling of one day is unusual but legal, so a user who
// wants an aggressively short window is not blocked by the validator.
func TestValidate_ReflectWeekCapAccepted(t *testing.T) {
	c := Default()
	c.ReflectWeekMaxDays = 1
	assert.NoError(t, c.Validate())
}

func TestMirrorDirs_SixInOrder(t *testing.T) {
	assert.Equal(t,
		[]string{"raw", "processed", "insights", "people", "sessions", "reflections"},
		Default().MirrorDirs())
}

// TestClip covers the recent_window clip rule end to end: an in-range value is
// returned untouched with no warning; an over-ceiling value clips to the max
// (acceptance case 1.4); a below-minimum value floors to one; and a
// hand-zeroed ceiling falls back to the default max rather than clipping to
// zero. Every row also proves Clip never mutates its receiver.
func TestClip(t *testing.T) {
	tests := []struct {
		name        string
		window      int  // RecentWindow override
		zeroCeiling bool // also zero RecentWindowMax (hand-edited config)
		wantWindow  int
		wantWarning string // substring of the single warning; "" ⇒ expect none
	}{
		{"in-range value is unchanged", Default().RecentWindow, false, Default().RecentWindow, ""},
		{"above the ceiling clips to the max", 999, false, 14, "clipped to 14"},
		{"below the minimum floors to one", 0, false, 1, "below minimum"},
		{"a zeroed ceiling falls back to the default max", 999, true, 14, "clipped to 14"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			c.RecentWindow = tc.window
			if tc.zeroCeiling {
				c.RecentWindowMax = 0
			}
			before := c.RecentWindow

			out, warnings := c.Clip()

			assert.Equal(t, tc.wantWindow, out.RecentWindow)
			assert.Equal(t, before, c.RecentWindow, "Clip must not mutate the receiver")
			if tc.wantWarning == "" {
				assert.Empty(t, warnings)
				assert.Equal(t, c, out, "an in-range config is returned unchanged")
			} else {
				require.Len(t, warnings, 1)
				assert.Contains(t, warnings[0], tc.wantWarning)
			}
		})
	}
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
// runs the pure Engine until an operator opts in (data-model.md
// §"lucid.json").
func TestDefault_CompanionBlock(t *testing.T) {
	c := Default().Companion
	assert.False(t, c.Enabled, "companion ships disabled")
	assert.Empty(t, c.MorningTemplate)
	assert.Empty(t, c.NightTemplate)
	assert.Empty(t, c.SystemPrompt)
	assert.Empty(t, c.MorningRoutine, "routine paths ship empty → feature off")
	assert.Empty(t, c.NightRoutine, "routine paths ship empty → feature off")
	assert.Empty(t, c.Birthdate, "birthdate ships empty → life-weeks line off")
	assert.Zero(t, c.LifeHorizonAge, "horizon age ships 0 → defaults to 90 at render")
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
	// The optional life-weeks keys render too, so the seam is discoverable.
	assert.Contains(t, s, `"birthdate":`)
	assert.Contains(t, s, `"life_horizon_age":`)
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

// TestValidate_CompanionBirthdate proves the one companion check that runs
// regardless of Enabled: an empty birthdate is fine, a well-formed YYYY-MM-DD
// validates, and a malformed one is a hard error even on a disabled block — a
// typo shouldn't silently drop the panel's life-weeks line. The horizon age is
// unconstrained (0 defaults to 90 at render), so it never fails validation.
func TestValidate_CompanionBirthdate(t *testing.T) {
	t.Run("empty birthdate validates", func(t *testing.T) {
		c := Default()
		assert.NoError(t, c.Validate())
	})
	t.Run("valid date validates while disabled", func(t *testing.T) {
		c := Default()
		c.Companion.Birthdate = "1990-06-15"
		c.Companion.LifeHorizonAge = 0 // 0 is legal → defaults to 90 at render
		assert.NoError(t, c.Validate())
	})
	t.Run("malformed date errors even while disabled", func(t *testing.T) {
		c := Default()
		c.Companion.Birthdate = "06/15/1990"
		assert.Error(t, c.Validate())
	})
	t.Run("a bare month is not a full date", func(t *testing.T) {
		c := Default()
		c.Companion.Birthdate = "1990-06"
		assert.Error(t, c.Validate())
	})
}

// TestDefault_WorkoutBlock pins the shipped workout default: the feature is off
// and every opaque path and the slot time are empty, so a fresh Ledger runs only
// the existing Engine and companion until an operator opts in (data-model.md
// §"lucid.json"; workout-module.md).
func TestDefault_WorkoutBlock(t *testing.T) {
	w := Default().Workout
	assert.False(t, w.Enabled, "workout ships disabled")
	assert.Empty(t, w.Program)
	assert.Empty(t, w.SlotTime)
	assert.Empty(t, w.SystemPrompt)
	assert.Empty(t, w.Template)
	assert.Empty(t, w.Model, "model override empty → inherits provider.model")
}

// TestWorkout_MarshalsDocumentedShape asserts a marshaled default config carries
// the workout block with the documented keys, off by default, and — like the
// companion and provider blocks — never leaks a token or channel id into
// lucid.json (those stay env-only).
func TestWorkout_MarshalsDocumentedShape(t *testing.T) {
	b, err := Default().Marshal()
	require.NoError(t, err)

	var m struct {
		Workout struct {
			Enabled      bool   `json:"enabled"`
			Program      string `json:"program"`
			SlotTime     string `json:"slot_time"`
			SystemPrompt string `json:"system_prompt"`
			Template     string `json:"template"`
			Model        string `json:"model"`
		} `json:"workout"`
	}
	require.NoError(t, json.Unmarshal(b, &m))
	assert.False(t, m.Workout.Enabled)
	assert.Empty(t, m.Workout.Program)
	assert.Empty(t, m.Workout.SlotTime)

	s := string(b)
	assert.Contains(t, s, `"workout":`)
	assert.Contains(t, s, `"program":`)
	assert.Contains(t, s, `"slot_time":`)
	assert.Contains(t, s, `"template":`)
	// No token or channel id ever lands in the config.
	assert.NotContains(t, s, "harness_token")
	assert.NotContains(t, s, "channel_id")
}

// TestWorkout_RoundTripEnabled proves a fully-configured workout block survives
// a marshal/unmarshal cycle byte-identically in value — the opaque paths, the
// slot time, and the optional model override.
func TestWorkout_RoundTripEnabled(t *testing.T) {
	c := Default()
	c.Workout = WorkoutConfig{
		Enabled:      true,
		Program:      "/opt/lucid/workout/program.json",
		SlotTime:     "12:00",
		SystemPrompt: "/opt/lucid/workout/system_prompt.md",
		Template:     "/opt/lucid/workout/daily_template.md",
		Model:        "sonnet",
	}
	b, err := c.Marshal()
	require.NoError(t, err)

	got, err := Unmarshal(b)
	require.NoError(t, err)
	assert.Equal(t, c, got)
	assert.Equal(t, "sonnet", got.Workout.Model)
	assert.Equal(t, "12:00", got.Workout.SlotTime)
	assert.Equal(t, "/opt/lucid/workout/program.json", got.Workout.Program)
}

// TestValidate_WorkoutEnabledRequiresPaths is the workout validate rule: an
// enabled workout missing any one of the three opaque paths, or carrying a bad
// slot_time, is a hard error, while all three paths set with a valid HH:MM
// slot_time (with or without a model override) validates.
func TestValidate_WorkoutEnabledRequiresPaths(t *testing.T) {
	full := WorkoutConfig{
		Enabled:      true,
		Program:      "p.json",
		SlotTime:     "12:00",
		SystemPrompt: "s.md",
		Template:     "t.md",
	}
	failures := map[string]func(*WorkoutConfig){
		"missing program":       func(w *WorkoutConfig) { w.Program = "" },
		"missing system_prompt": func(w *WorkoutConfig) { w.SystemPrompt = "" },
		"missing template":      func(w *WorkoutConfig) { w.Template = "" },
		"empty slot_time":       func(w *WorkoutConfig) { w.SlotTime = "" },
		"non-clock slot_time":   func(w *WorkoutConfig) { w.SlotTime = "noon" },
		"hour out of range":     func(w *WorkoutConfig) { w.SlotTime = "25:00" },
		"minute out of range":   func(w *WorkoutConfig) { w.SlotTime = "12:60" },
		"missing colon":         func(w *WorkoutConfig) { w.SlotTime = "1200" },
		// The shared clockmark rule rejects inner whitespace, so a slot_time that
		// passes here is guaranteed to build a valid cron in the workout node —
		// the lenient inner-trim this replaced once accepted "12 : 30".
		"inner whitespace": func(w *WorkoutConfig) { w.SlotTime = "12 : 30" },
	}
	for name, mutate := range failures {
		t.Run(name, func(t *testing.T) {
			c := Default()
			c.Workout = full
			mutate(&c.Workout)
			assert.Error(t, c.Validate())
		})
	}

	t.Run("all paths set with valid slot_time validates", func(t *testing.T) {
		c := Default()
		c.Workout = full
		assert.NoError(t, c.Validate())
	})
	t.Run("model override does not require a known name", func(t *testing.T) {
		c := Default()
		c.Workout = full
		c.Workout.Model = "some-future-model"
		assert.NoError(t, c.Validate())
	})
	t.Run("single-digit hour slot_time validates", func(t *testing.T) {
		c := Default()
		c.Workout = full
		c.Workout.SlotTime = "9:30"
		assert.NoError(t, c.Validate())
	})
}

// TestValidate_WorkoutDisabledIgnoresPaths confirms that while disabled (the
// default), empty paths and slot time are tolerated — the block is inert, so it
// never blocks a load.
func TestValidate_WorkoutDisabledIgnoresPaths(t *testing.T) {
	c := Default()
	c.Workout = WorkoutConfig{Enabled: false} // all fields empty
	assert.NoError(t, c.Validate())
}

// TestDefault_WitnessReportBlock pins the shipped witness-report default: the
// feature is off (enabled false) with its safe behavioral defaults pre-filled —
// preview mode, a Monday (weekday 1) 09:00 fire mark, and every opaque path empty
// — so a fresh Ledger runs only the existing Engine and companion until an operator
// opts in (data-model.md §"lucid.json"; witness-report.md).
func TestDefault_WitnessReportBlock(t *testing.T) {
	w := Default().WitnessReport
	assert.False(t, w.Enabled, "witness report ships disabled")
	assert.Equal(t, WitnessReportModePreview, w.Mode, "the safe default is preview (own channel, never friends)")
	assert.Equal(t, "09:00", w.Time)
	assert.Equal(t, 1, w.Weekday, "Monday")
	assert.Empty(t, w.SystemPrompt)
	assert.Empty(t, w.Template)
	assert.Empty(t, w.AsksFile, "curated-asks override empty → auto-drafted asks stand")
	assert.Empty(t, w.Model, "model override empty → inherits provider.model")
}

// TestWitnessReport_MarshalsDocumentedShape asserts a marshaled default config
// carries the witness_report block with the documented keys, off by default, and —
// like the companion, workout, and provider blocks — never leaks a token or
// channel id into lucid.json (those stay env-only).
func TestWitnessReport_MarshalsDocumentedShape(t *testing.T) {
	b, err := Default().Marshal()
	require.NoError(t, err)

	var m struct {
		WitnessReport struct {
			Enabled      bool   `json:"enabled"`
			Mode         string `json:"mode"`
			Time         string `json:"time"`
			Weekday      int    `json:"weekday"`
			SystemPrompt string `json:"system_prompt"`
			Template     string `json:"template"`
			AsksFile     string `json:"asks_file"`
			Model        string `json:"model"`
		} `json:"witness_report"`
	}
	require.NoError(t, json.Unmarshal(b, &m))
	assert.False(t, m.WitnessReport.Enabled)
	assert.Equal(t, WitnessReportModePreview, m.WitnessReport.Mode)
	assert.Equal(t, "09:00", m.WitnessReport.Time)
	assert.Equal(t, 1, m.WitnessReport.Weekday)

	s := string(b)
	assert.Contains(t, s, `"witness_report":`)
	assert.Contains(t, s, `"mode":`)
	assert.Contains(t, s, `"weekday":`)
	assert.Contains(t, s, `"asks_file":`)
	// No token or channel id ever lands in the config.
	assert.NotContains(t, s, "harness_token")
	assert.NotContains(t, s, "channel_id")
}

// TestWitnessReport_RoundTripEnabled proves a fully-configured witness_report
// block survives a marshal/unmarshal cycle byte-identically in value — the mode,
// the fire mark, the opaque paths, the curated-asks override, and the optional
// model override.
func TestWitnessReport_RoundTripEnabled(t *testing.T) {
	c := Default()
	c.WitnessReport = WitnessReportConfig{
		Enabled:      true,
		Mode:         WitnessReportModeAuto,
		Time:         "08:30",
		Weekday:      1,
		SystemPrompt: "/opt/lucid/witness/system_prompt.md",
		Template:     "/opt/lucid/witness/template.md",
		AsksFile:     "/opt/lucid/witness/asks.md",
		Model:        "sonnet",
	}
	b, err := c.Marshal()
	require.NoError(t, err)

	got, err := Unmarshal(b)
	require.NoError(t, err)
	assert.Equal(t, c, got)
	assert.Equal(t, "sonnet", got.WitnessReport.Model)
	assert.Equal(t, WitnessReportModeAuto, got.WitnessReport.Mode)
	assert.Equal(t, "/opt/lucid/witness/asks.md", got.WitnessReport.AsksFile)
}

// TestValidate_WitnessReportEnabledRequiresPaths is the witness-report validate
// rule: an enabled report missing either required prompt path, carrying an unknown
// mode, a bad time, or an out-of-range weekday is a hard error, while the full set
// (with or without a model override or curated asks file) validates.
func TestValidate_WitnessReportEnabledRequiresPaths(t *testing.T) {
	full := WitnessReportConfig{
		Enabled:      true,
		Mode:         WitnessReportModePreview,
		Time:         "09:00",
		Weekday:      1,
		SystemPrompt: "s.md",
		Template:     "t.md",
	}
	failures := map[string]func(*WitnessReportConfig){
		"missing system_prompt": func(w *WitnessReportConfig) { w.SystemPrompt = "" },
		"missing template":      func(w *WitnessReportConfig) { w.Template = "" },
		"empty mode":            func(w *WitnessReportConfig) { w.Mode = "" },
		"unknown mode":          func(w *WitnessReportConfig) { w.Mode = "broadcast" },
		"empty time":            func(w *WitnessReportConfig) { w.Time = "" },
		"non-clock time":        func(w *WitnessReportConfig) { w.Time = "morning" },
		"hour out of range":     func(w *WitnessReportConfig) { w.Time = "24:00" },
		"minute out of range":   func(w *WitnessReportConfig) { w.Time = "09:75" },
		"weekday below range":   func(w *WitnessReportConfig) { w.Weekday = -1 },
		"weekday above range":   func(w *WitnessReportConfig) { w.Weekday = 7 },
	}
	for name, mutate := range failures {
		t.Run(name, func(t *testing.T) {
			c := Default()
			c.WitnessReport = full
			mutate(&c.WitnessReport)
			assert.Error(t, c.Validate())
		})
	}

	t.Run("full block validates", func(t *testing.T) {
		c := Default()
		c.WitnessReport = full
		assert.NoError(t, c.Validate())
	})
	t.Run("auto mode validates", func(t *testing.T) {
		c := Default()
		c.WitnessReport = full
		c.WitnessReport.Mode = WitnessReportModeAuto
		assert.NoError(t, c.Validate())
	})
	t.Run("model override does not require a known name", func(t *testing.T) {
		c := Default()
		c.WitnessReport = full
		c.WitnessReport.Model = "some-future-model"
		assert.NoError(t, c.Validate())
	})
	t.Run("asks_file is optional", func(t *testing.T) {
		c := Default()
		c.WitnessReport = full // asks_file empty
		assert.NoError(t, c.Validate())
	})
	t.Run("sunday and saturday weekdays validate", func(t *testing.T) {
		for _, day := range []int{0, 6} {
			c := Default()
			c.WitnessReport = full
			c.WitnessReport.Weekday = day
			assert.NoError(t, c.Validate(), "weekday %d should validate", day)
		}
	})
}

// TestValidate_WitnessReportDisabledIgnoresConfig confirms that while disabled
// (the default), empty paths and even a bogus mode/time are tolerated — the block
// is inert, so it never blocks a load.
func TestValidate_WitnessReportDisabledIgnoresConfig(t *testing.T) {
	c := Default()
	c.WitnessReport = WitnessReportConfig{Enabled: false, Mode: "nonsense", Time: "bad"}
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
		"bad version":                    func(c *Config) { c.Version = 2 },
		"empty raw_dir":                  func(c *Config) { c.RawDir = "" },
		"empty processed_dir":            func(c *Config) { c.ProcessedDir = "" },
		"empty insights_dir":             func(c *Config) { c.InsightsDir = "" },
		"empty people_dir":               func(c *Config) { c.PeopleDir = "" },
		"empty sessions_dir":             func(c *Config) { c.SessionsDir = "" },
		"empty reflections_dir":          func(c *Config) { c.ReflectionsDir = "" },
		"bad recent_window_max":          func(c *Config) { c.RecentWindowMax = 0 },
		"bad ask_insights_cap":           func(c *Config) { c.AskInsightsCap = 0 },
		"bad ask_reflectionscap":         func(c *Config) { c.AskReflectionsCap = 0 },
		"zero self_facts_cap":            func(c *Config) { c.SelfFactsCap = 0 },
		"negative self_facts_cap":        func(c *Config) { c.SelfFactsCap = -1 },
		"zero reflect_week_max_days":     func(c *Config) { c.ReflectWeekMaxDays = 0 },
		"negative reflect_week_max_days": func(c *Config) { c.ReflectWeekMaxDays = -1 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			c := Default()
			mutate(&c)
			assert.Error(t, c.Validate())
		})
	}
}

// TestSelfFactsCap_ValidationAndRoundTrip covers the self-facts grounding cap
// end to end: the documented default, the >= 1 rejection message for both a
// zero and a negative value, and an explicit value surviving a write/read cycle.
func TestSelfFactsCap_ValidationAndRoundTrip(t *testing.T) {
	assert.Equal(t, 40, Default().SelfFactsCap, "documented default")

	for _, bad := range []int{0, -1} {
		c := Default()
		c.SelfFactsCap = bad
		err := c.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "self_facts_cap must be >= 1")
	}

	c := Default()
	c.SelfFactsCap = 7
	require.NoError(t, c.Validate())
	b, err := c.Marshal()
	require.NoError(t, err)
	assert.Contains(t, string(b), `"self_facts_cap": 7`)

	got, err := Unmarshal(b)
	require.NoError(t, err)
	assert.Equal(t, 7, got.SelfFactsCap)
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

// TestDefault_GratitudeMatchBlock pins the documented gratitude.match defaults
// (gratitude.md §9; ADR-0012 §2, §6): tier 2 at 0.85 / 0.15, the shared 0.50
// floor, a stricter tier 3 at 0.90 / 0.20, the judge that cleared the trust gate
// (claude_cli, sonnet) switched off until opted in, a 30s judge bound, and a
// 200-entry cap.
func TestDefault_GratitudeMatchBlock(t *testing.T) {
	m := Default().Gratitude.Match
	assert.InDelta(t, 0.85, m.Tier2High, 1e-9)
	assert.InDelta(t, 0.15, m.Tier2Margin, 1e-9)
	assert.InDelta(t, 0.50, m.AmbiguousFloor, 1e-9)
	assert.False(t, m.Tier3Enabled, "tier 3 ships off — its default judge is hosted, so enabling it is the opt-in")
	assert.Equal(t, "claude_cli", m.Tier3Backend, "the backend that cleared the trust gate")
	assert.Equal(t, "sonnet", m.Tier3Model, "the model that cleared the trust gate")
	assert.InDelta(t, 0.90, m.Tier3High, 1e-9)
	assert.InDelta(t, 0.20, m.Tier3Margin, 1e-9)
	assert.Equal(t, 30, m.Tier3TimeoutSeconds)
	assert.Equal(t, 200, m.Tier3MaxCandidates)
	assert.Equal(t, DefaultGratitudeMatch(), m)
	assert.GreaterOrEqual(t, m.Tier2High, m.AmbiguousFloor, "the floor never sits above a high cutoff")
	assert.GreaterOrEqual(t, m.Tier3High, m.AmbiguousFloor)
}

// TestGratitudeMatch_MarshalsDocumentedShape asserts a marshaled default config
// carries gratitude.match under the documented keys (data-model.md §"lucid.json")
// and, like every other block, no credential.
func TestGratitudeMatch_MarshalsDocumentedShape(t *testing.T) {
	b, err := Default().Marshal()
	require.NoError(t, err)

	var m struct {
		Gratitude struct {
			Match map[string]any `json:"match"`
		} `json:"gratitude"`
	}
	require.NoError(t, json.Unmarshal(b, &m))
	match := m.Gratitude.Match
	require.NotNil(t, match, "the gratitude.match block is written")
	assert.Len(t, match, 10, "exactly the ten documented knobs")
	assert.InDelta(t, 0.85, match["tier2_high"], 1e-9)
	assert.InDelta(t, 0.15, match["tier2_margin"], 1e-9)
	assert.InDelta(t, 0.5, match["ambiguous_floor"], 1e-9)
	assert.Equal(t, false, match["tier3_enabled"])
	assert.Equal(t, "claude_cli", match["tier3_backend"])
	assert.Equal(t, "sonnet", match["tier3_model"])
	assert.InDelta(t, 0.9, match["tier3_high"], 1e-9)
	assert.InDelta(t, 0.2, match["tier3_margin"], 1e-9)
	assert.EqualValues(t, 30, match["tier3_timeout_seconds"])
	assert.EqualValues(t, 200, match["tier3_max_candidates"])

	s := string(b)
	assert.NotContains(t, s, "api_key")
	assert.NotContains(t, s, "token")
}

// TestGratitudeMatch_AbsentOrPartialBlockReadsDefaults: a lucid.json written
// before the gratitude block existed reads every documented default, and one that
// sets only some gratitude.match keys keeps the defaults for the rest — neither
// raises a clip warning (a missing key is not an out-of-range value), so boot
// never rewrites an older file just to add the block.
func TestGratitudeMatch_AbsentOrPartialBlockReadsDefaults(t *testing.T) {
	absent, err := Unmarshal([]byte(`{"version": 1, "recent_window": 7, "recent_window_max": 14}`))
	require.NoError(t, err)
	assert.Equal(t, DefaultGratitudeMatch(), absent.Gratitude.Match, "an absent block reads the defaults")
	_, warnings := absent.Clip()
	assert.Empty(t, warnings, "an absent block is not a clip")

	partial, err := Unmarshal([]byte(`{"version": 1, "recent_window": 7, "recent_window_max": 14,
		"gratitude": {"match": {"tier3_enabled": false, "tier2_high": 0.9}}}`))
	require.NoError(t, err)
	want := DefaultGratitudeMatch()
	want.Tier3Enabled = false
	want.Tier2High = 0.9
	assert.Equal(t, want, partial.Gratitude.Match, "set keys win, missing keys keep their defaults")
	_, warnings = partial.Clip()
	assert.Empty(t, warnings)

	// A config literal with no block (never loaded, e.g. a router before Boot)
	// clips to the defaults silently too, and OrDefault reads it the same way.
	zero := Default()
	zero.Gratitude = GratitudeConfig{}
	clipped, warnings := zero.Clip()
	assert.Empty(t, warnings)
	assert.Equal(t, DefaultGratitudeMatch(), clipped.Gratitude.Match)
	assert.Equal(t, DefaultGratitudeMatch(), GratitudeMatchConfig{}.OrDefault())
	assert.NoError(t, zero.Validate(), "an absent block validates as the defaults")
}

// TestGratitudeMatch_ClipFailSafe covers every gratitude.match clip rule
// (data-model.md §"lucid.json"): an out-of-range score, an ambiguous floor above a
// high cutoff, an unknown tier-3 backend, and a sub-1 timeout or candidate cap are
// each pulled back to the documented default with a warning naming the key — fail
// safe, never a crash — and the clipped config validates. In-range values
// (including the empty inherit-the-provider backend) pass untouched.
func TestGratitudeMatch_ClipFailSafe(t *testing.T) {
	def := DefaultGratitudeMatch()
	tests := []struct {
		name        string
		mutate      func(*GratitudeMatchConfig)
		check       func(t *testing.T, m GratitudeMatchConfig)
		wantWarning string // substring of a warning; "" ⇒ expect none
	}{
		{
			name:   "in-range values are untouched",
			mutate: func(m *GratitudeMatchConfig) { m.Tier2High, m.Tier2Margin, m.Tier3Model = 0.8, 0.1, "llama3" },
			check: func(t *testing.T, m GratitudeMatchConfig) {
				assert.InDelta(t, 0.8, m.Tier2High, 1e-9)
				assert.Equal(t, "llama3", m.Tier3Model)
			},
		},
		{
			name:   "an empty backend inherits provider.backend and is valid",
			mutate: func(m *GratitudeMatchConfig) { m.Tier3Backend = "" },
			check:  func(t *testing.T, m GratitudeMatchConfig) { assert.Empty(t, m.Tier3Backend) },
		},
		{
			name:        "a high cutoff above one is clipped",
			mutate:      func(m *GratitudeMatchConfig) { m.Tier2High = 1.7 },
			check:       func(t *testing.T, m GratitudeMatchConfig) { assert.InDelta(t, def.Tier2High, m.Tier2High, 1e-9) },
			wantWarning: "gratitude.match.tier2_high 1.7 is outside [0, 1]",
		},
		{
			name:        "a negative margin is clipped",
			mutate:      func(m *GratitudeMatchConfig) { m.Tier3Margin = -0.2 },
			check:       func(t *testing.T, m GratitudeMatchConfig) { assert.InDelta(t, def.Tier3Margin, m.Tier3Margin, 1e-9) },
			wantWarning: "gratitude.match.tier3_margin -0.2 is outside [0, 1]",
		},
		{
			name:   "a floor above a high cutoff is reset to its default",
			mutate: func(m *GratitudeMatchConfig) { m.AmbiguousFloor = 0.95 },
			check: func(t *testing.T, m GratitudeMatchConfig) {
				assert.InDelta(t, def.AmbiguousFloor, m.AmbiguousFloor, 1e-9)
			},
			wantWarning: "ambiguous_floor 0.95 is above a high cutoff",
		},
		{
			name:   "a high cutoff still below the reset floor is reset too",
			mutate: func(m *GratitudeMatchConfig) { m.Tier2High, m.AmbiguousFloor = 0.4, 0.45 },
			check: func(t *testing.T, m GratitudeMatchConfig) {
				assert.InDelta(t, def.AmbiguousFloor, m.AmbiguousFloor, 1e-9)
				assert.InDelta(t, def.Tier2High, m.Tier2High, 1e-9)
			},
			wantWarning: "gratitude.match.tier2_high 0.4 is below ambiguous_floor",
		},
		{
			name:        "an unknown backend fails safe toward local",
			mutate:      func(m *GratitudeMatchConfig) { m.Tier3Backend = "gpt5_cli" },
			check:       func(t *testing.T, m GratitudeMatchConfig) { assert.Equal(t, "ollama", m.Tier3Backend) },
			wantWarning: `gratitude.match.tier3_backend "gpt5_cli" is not a known backend`,
		},
		{
			name:        "a zero timeout is clipped",
			mutate:      func(m *GratitudeMatchConfig) { m.Tier3TimeoutSeconds = 0 },
			check:       func(t *testing.T, m GratitudeMatchConfig) { assert.Equal(t, 30, m.Tier3TimeoutSeconds) },
			wantWarning: "gratitude.match.tier3_timeout_seconds 0 is below 1",
		},
		{
			name:        "a negative candidate cap is clipped",
			mutate:      func(m *GratitudeMatchConfig) { m.Tier3MaxCandidates = -3 },
			check:       func(t *testing.T, m GratitudeMatchConfig) { assert.Equal(t, 200, m.Tier3MaxCandidates) },
			wantWarning: "gratitude.match.tier3_max_candidates -3 is below 1",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			tc.mutate(&c.Gratitude.Match)
			before := c.Gratitude.Match

			out, warnings := c.Clip()

			assert.Equal(t, before, c.Gratitude.Match, "Clip must not mutate the receiver")
			tc.check(t, out.Gratitude.Match)
			if tc.wantWarning == "" {
				assert.Empty(t, warnings)
				require.NoError(t, c.Validate(), "an in-range block validates as written")
			} else {
				require.NotEmpty(t, warnings)
				assert.Contains(t, strings.Join(warnings, "\n"), tc.wantWarning)
				assert.Contains(t, strings.Join(warnings, "\n"), "clipped to")
				require.Error(t, c.Validate(), "Validate reports what Clip would coerce")
			}
			require.NoError(t, out.Validate(), "a clipped config always validates")
			_, again := out.Clip()
			assert.Empty(t, again, "clipping is idempotent")
		})
	}
}

// TestGratitudeMatch_RoundTrip proves a tuned gratitude.match block survives a
// write/read cycle exactly — here an opt-in to a local judge, every knob moved
// off its default, which the defaults pre-seed must not pull back.
func TestGratitudeMatch_RoundTrip(t *testing.T) {
	c := Default()
	c.Gratitude.Match = GratitudeMatchConfig{
		Tier2High:           0.8,
		Tier2Margin:         0.1,
		AmbiguousFloor:      0.4,
		Tier3Enabled:        true,
		Tier3Backend:        "ollama",
		Tier3Model:          "qwen3:8b",
		Tier3High:           0.95,
		Tier3Margin:         0.25,
		Tier3TimeoutSeconds: 12,
		Tier3MaxCandidates:  50,
	}
	b, err := c.Marshal()
	require.NoError(t, err)

	got, err := Unmarshal(b)
	require.NoError(t, err)
	assert.Equal(t, c, got)
	assert.True(t, got.Gratitude.Match.Tier3Enabled)
	require.NoError(t, got.Validate())
}

// TestGratitudeMatch_Tier3ProviderConfig: the tier-3 judge is built from the
// provider block with tier3_backend / tier3_model overriding backend / model when
// set — empty inherits, the companion/workout model rule — and
// tier3_timeout_seconds as the per-call bound, while the endpoint and every other
// provider key are inherited unchanged (gratitude.md §7.5). A zero match block
// reads as the defaults.
func TestGratitudeMatch_Tier3ProviderConfig(t *testing.T) {
	base := Default().Provider
	base.Backend, base.Model, base.Endpoint, base.TimeoutSeconds = "ollama", "llama3", "http://127.0.0.1:9", 120

	got := DefaultGratitudeMatch().Tier3ProviderConfig(base)
	assert.Equal(t, "claude_cli", got.Backend, "the default tier-3 backend overrides provider.backend")
	assert.Equal(t, "sonnet", got.Model, "the default tier-3 model overrides provider.model")
	assert.Equal(t, 30, got.TimeoutSeconds, "the per-call bound is tier3_timeout_seconds")
	assert.Equal(t, "http://127.0.0.1:9", got.Endpoint, "the endpoint is inherited")

	inherit := DefaultGratitudeMatch()
	inherit.Tier3Backend, inherit.Tier3Model, inherit.Tier3TimeoutSeconds = "", "", 7
	got = inherit.Tier3ProviderConfig(base)
	assert.Equal(t, "ollama", got.Backend, "an empty tier3_backend inherits provider.backend")
	assert.Equal(t, "llama3", got.Model, "an empty tier3_model inherits provider.model")
	assert.Equal(t, 7, got.TimeoutSeconds)

	assert.Equal(t, DefaultGratitudeMatch().Tier3ProviderConfig(base), GratitudeMatchConfig{}.Tier3ProviderConfig(base),
		"a zero block reads as the defaults")
	assert.Equal(t, 120, base.TimeoutSeconds, "the base block is not mutated")
}
