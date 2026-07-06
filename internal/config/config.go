// Package config models lucid.json — the single, tiny, hand-editable
// global config at the root of the ~/.lucid/ Ledger (data-model.md
// §"lucid.json"). It is pure: it owns the schema, the documented
// defaults, marshaling to/from the exact on-disk JSON shape, clipping
// of out-of-range values, and validation. It performs no filesystem
// access — the storage adapter is the only code that reads or writes
// the home tree (architecture.md §4).
package config

import (
	"encoding/json"
	"fmt"
)

// SchemaVersion is the only lucid.json schema version the MVP
// understands. A file carrying any other version is rejected by
// [Config.Validate] rather than silently coerced.
const SchemaVersion = 1

// ProposalPause configures the router-level proposal pause: after
// UnansweredThreshold consecutive unanswered proposals the router stops
// invoking reflection.propose for PauseDays days (data-model.md,
// agent-contracts.md §3).
type ProposalPause struct {
	UnansweredThreshold int `json:"unanswered_threshold"`
	PauseDays           int `json:"pause_days"`
}

// AgentVersions stamps which prompt/version of each agent is current.
// Every processed artifact and insight records the versions that
// touched it so the system can later identify work produced by a prompt
// it no longer uses (data-model.md §"lucid.json").
type AgentVersions struct {
	Intake        string `json:"intake"`
	Structuring   string `json:"structuring"`
	Reflection    string `json:"reflection"`
	SafetyConsent string `json:"safety_consent"`
}

// Config is the in-memory representation of lucid.json. Field order
// matches the documented schema so a marshaled default file reads
// identically to data-model.md §"lucid.json".
type Config struct {
	Version                  int           `json:"version"`
	Home                     string        `json:"home"`
	RawDir                   string        `json:"raw_dir"`
	ProcessedDir             string        `json:"processed_dir"`
	InsightsDir              string        `json:"insights_dir"`
	PeopleDir                string        `json:"people_dir"`
	SessionsDir              string        `json:"sessions_dir"`
	ReflectionsDir           string        `json:"reflections_dir"`
	WordlistPath             string        `json:"wordlist_path"`
	RecentWindow             int           `json:"recent_window"`
	RecentWindowMax          int           `json:"recent_window_max"`
	IntakeMaxQuestions       int           `json:"intake_max_questions"`
	AskInsightsCap           int           `json:"ask_insights_cap"`
	AskReflectionsCap        int           `json:"ask_reflections_cap"`
	ProposalPause            ProposalPause `json:"proposal_pause"`
	PersonDominanceThreshold float64       `json:"person_dominance_threshold"`
	AgentVersions            AgentVersions `json:"agent_versions"`
	BootstrapMode            bool          `json:"bootstrap_mode"`
}

// Default returns a fresh config carrying the documented default values
// (data-model.md §"lucid.json"). A freshly scaffolded Ledger writes
// exactly this.
func Default() Config {
	return Config{
		Version:            SchemaVersion,
		Home:               "~/.lucid/",
		RawDir:             "raw",
		ProcessedDir:       "processed",
		InsightsDir:        "insights",
		PeopleDir:          "people",
		SessionsDir:        "sessions",
		ReflectionsDir:     "reflections",
		WordlistPath:       "data/person_keys_wordlist.txt",
		RecentWindow:       7,
		RecentWindowMax:    14,
		IntakeMaxQuestions: 4,
		AskInsightsCap:     50,
		AskReflectionsCap:  12,
		ProposalPause: ProposalPause{
			UnansweredThreshold: 3,
			PauseDays:           14,
		},
		PersonDominanceThreshold: 0.5,
		AgentVersions: AgentVersions{
			Intake:        "intake-2026.05.0",
			Structuring:   "structuring-2026.05.0",
			Reflection:    "reflection-2026.05.0",
			SafetyConsent: "safety-2026.05.0",
		},
		BootstrapMode: false,
	}
}

// MirrorDirs returns the six Mirror directory names the scaffold must
// create, in a stable order. The Engine and observation trees are
// created by their own phases (acceptance-criteria.md §"Cross-phase"),
// not here.
func (c Config) MirrorDirs() []string {
	return []string{
		c.RawDir,
		c.ProcessedDir,
		c.InsightsDir,
		c.PeopleDir,
		c.SessionsDir,
		c.ReflectionsDir,
	}
}

// Clip returns a copy of the config with out-of-range values pulled
// back into their allowed bounds, plus a human-readable warning for
// each field it changed. recent_window is the only field with a
// documented runtime ceiling: the router refuses any value above
// recent_window_max and clips it (data-model.md §"lucid.json"; test
// case 1.4). Clip never mutates the receiver.
func (c Config) Clip() (Config, []string) {
	out := c
	var warnings []string

	ceiling := out.RecentWindowMax
	if ceiling <= 0 {
		ceiling = Default().RecentWindowMax
	}
	if out.RecentWindow > ceiling {
		warnings = append(warnings, fmt.Sprintf(
			"recent_window %d exceeds recent_window_max %d — clipped to %d",
			out.RecentWindow, ceiling, ceiling,
		))
		out.RecentWindow = ceiling
	}
	if out.RecentWindow < 1 {
		warnings = append(warnings, fmt.Sprintf(
			"recent_window %d below minimum — clipped to 1", out.RecentWindow,
		))
		out.RecentWindow = 1
	}

	return out, warnings
}

// Validate reports whether the config is structurally usable. It checks
// the schema version, that every directory name is set, and that the
// caps and windows are positive. It does not clip — call [Clip] for
// range coercion.
func (c Config) Validate() error {
	if c.Version != SchemaVersion {
		return fmt.Errorf("config: unsupported version %d (want %d)", c.Version, SchemaVersion)
	}
	dirs := map[string]string{
		"raw_dir":         c.RawDir,
		"processed_dir":   c.ProcessedDir,
		"insights_dir":    c.InsightsDir,
		"people_dir":      c.PeopleDir,
		"sessions_dir":    c.SessionsDir,
		"reflections_dir": c.ReflectionsDir,
	}
	for name, v := range dirs {
		if v == "" {
			return fmt.Errorf("config: %s must not be empty", name)
		}
	}
	if c.RecentWindowMax < 1 {
		return fmt.Errorf("config: recent_window_max must be >= 1, got %d", c.RecentWindowMax)
	}
	if c.AskInsightsCap < 1 {
		return fmt.Errorf("config: ask_insights_cap must be >= 1, got %d", c.AskInsightsCap)
	}
	if c.AskReflectionsCap < 1 {
		return fmt.Errorf("config: ask_reflections_cap must be >= 1, got %d", c.AskReflectionsCap)
	}
	return nil
}

// Marshal renders the config as the exact indented JSON written to
// lucid.json, with a trailing newline so the file is POSIX-clean and
// diffs stay minimal.
func (c Config) Marshal() ([]byte, error) {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("config: marshal: %w", err)
	}
	return append(b, '\n'), nil
}

// Unmarshal parses lucid.json bytes into a Config. It does not clip or
// validate — callers decide when to apply [Clip] and [Validate].
func Unmarshal(b []byte) (Config, error) {
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("config: unmarshal: %w", err)
	}
	return c, nil
}
