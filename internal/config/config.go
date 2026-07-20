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
	"slices"
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

// KnownBackends are the provider backend names lucid.json accepts. A
// single configured backend serves all four agent roles for this
// pillar; the Codex CLI and any future backend register here without a
// schema change (adr/0006-model-access.md §"Pinned invocation
// contracts").
var KnownBackends = map[string]bool{ //nolint:gochecknoglobals // a fixed, read-only set of accepted backend names (adr/0006-model-access.md)
	"claude_cli": true,
	"ollama":     true,
}

// ProviderRole reserves a per-role backend/model override in the
// lucid.json provider block. ADR-0006 mandates that "which provider
// backs which role is per-instance configuration"; the map is defined
// and marshaled now but unused this pillar — one configured default
// backend serves every role — so overrides drop in later without a
// schema change (data-model.md §"lucid.json").
type ProviderRole struct {
	Backend string `json:"backend"`
	Model   string `json:"model"`
}

// ProviderConfig selects the model backend for the agentic verbs
// (/checkin, /reflect, /ask) — the config seam ADR-0006 requires.
// Backend is a KnownBackends name (claude_cli or ollama); Model is that
// backend's model; TimeoutSeconds bounds every call so a hung backend
// degrades to a timeout rather than waiting forever; Endpoint is the
// Ollama base URL (ignored by the Claude CLI backend). Roles reserves
// per-role overrides (see ProviderRole). No model API key lives here,
// or anywhere in lucid.json — auth is the vendor CLI's or the local
// daemon's (data-model.md §"lucid.json").
type ProviderConfig struct {
	Backend        string                  `json:"backend"`
	Model          string                  `json:"model"`
	TimeoutSeconds int                     `json:"timeout_seconds"`
	Endpoint       string                  `json:"endpoint"`
	Roles          map[string]ProviderRole `json:"roles"`
}

// CompanionConfig configures the daily companion — the Mirror-side,
// model-allowed job that composes and delivers the morning and night
// messages (companion.md). It is credential-dumb like the rest of
// lucid.json: it carries no channel id and no token (those stay env-only —
// LUCID_USER_CHANNEL_ID / LUCID_HARNESS_TOKEN), only the explicit paths to
// the three opaque prompt files the compose worker reads. Enabled gates the
// whole feature: the default zero value is false, so an unconfigured Ledger
// runs the pure Engine teeth exactly as before. MorningTemplate,
// NightTemplate, and SystemPrompt are each an opaque, self-contained prompt
// file the worker opens directly — lucid never traverses into the directory
// holding them (no dir-walk, no filename convention), so the block is the
// whole firewall seam. Model optionally overrides provider.model for the
// companion's compose call; empty inherits the provider default. Fire times
// are deliberately not companion keys — they are inherited from the
// chain.json bell/tripwire marks so the companion can never drift from the
// deterministic pair (data-model.md §"lucid.json").
type CompanionConfig struct {
	Enabled         bool   `json:"enabled"`
	MorningTemplate string `json:"morning_template"`
	NightTemplate   string `json:"night_template"`
	SystemPrompt    string `json:"system_prompt"`
	Model           string `json:"model"`
}

// Config is the in-memory representation of lucid.json. Field order
// matches the documented schema so a marshaled default file reads
// identically to data-model.md §"lucid.json".
type Config struct {
	Version                  int             `json:"version"`
	Home                     string          `json:"home"`
	RawDir                   string          `json:"raw_dir"`
	ProcessedDir             string          `json:"processed_dir"`
	InsightsDir              string          `json:"insights_dir"`
	PeopleDir                string          `json:"people_dir"`
	SessionsDir              string          `json:"sessions_dir"`
	ReflectionsDir           string          `json:"reflections_dir"`
	WordlistPath             string          `json:"wordlist_path"`
	RecentWindow             int             `json:"recent_window"`
	RecentWindowMax          int             `json:"recent_window_max"`
	IntakeMaxQuestions       int             `json:"intake_max_questions"`
	AskInsightsCap           int             `json:"ask_insights_cap"`
	AskReflectionsCap        int             `json:"ask_reflections_cap"`
	ProposalPause            ProposalPause   `json:"proposal_pause"`
	PersonDominanceThreshold float64         `json:"person_dominance_threshold"`
	AgentVersions            AgentVersions   `json:"agent_versions"`
	BootstrapMode            bool            `json:"bootstrap_mode"`
	Provider                 ProviderConfig  `json:"provider"`
	Companion                CompanionConfig `json:"companion"`
	// FrameworkStack is the ordered standing-consent list — one interpretation
	// lens id per line the user has admitted to their stack at calibration or a
	// quarterly Charter amendment (docs/frameworks.md §3). A lens in the stack
	// may frame reflection proposals without re-asking; that is what the consent
	// bought. Empty by default: the frameworks layer is off until an id is
	// deliberately added, so the reflection voice stays baseline.
	FrameworkStack []string `json:"framework_stack"`
	// FrameworkConsents records when each stacked lens was consented (lens id →
	// RFC3339 timestamp) — the audit trail the lens-rotation protocol's verdicts
	// are checked against (docs/frameworks.md §3; docs/protocols/P-2-lens-
	// rotation.md). A stacked lens with no recorded consent fails closed and
	// never frames a proposal (see [Config.LensConsented]).
	FrameworkConsents map[string]string `json:"framework_consents"`
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
		Provider: ProviderConfig{
			Backend:        "claude_cli",
			Model:          "opus",
			TimeoutSeconds: 120,
			Endpoint:       "http://localhost:11434",
			Roles:          map[string]ProviderRole{},
		},
		// The companion ships off: a fresh Ledger runs the pure Engine teeth
		// until an operator points the three prompt-file paths at their own
		// template dir and flips enabled true.
		Companion: CompanionConfig{},
		// The frameworks layer ships off: no lens is stacked or consented, so
		// LensConsented is false for every id and the reflection voice stays
		// baseline until an operator amends the Charter stack. Non-nil empties
		// keep the marshaled file rendering [] / {} rather than null.
		FrameworkStack:    []string{},
		FrameworkConsents: map[string]string{},
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

// LensConsented reports whether the interpretation lens id may frame a
// reflection this run. It fails closed on two counts, mirroring the
// observation-kind enable-gate ([observations.Config.KindEnabled]): the lens
// must be in the standing FrameworkStack AND carry a non-empty consent
// timestamp in FrameworkConsents. A hand-edited stack entry with no recorded
// consent, a dangling consent for an unstacked lens, and the empty id all read
// as not consented — the layer never silently frames a proposal in a lens the
// user did not sign for (docs/frameworks.md §3; product-principles.md P9, fail
// closed).
func (c Config) LensConsented(id string) bool {
	if id == "" {
		return false
	}
	if !slices.Contains(c.FrameworkStack, id) {
		return false
	}
	return c.FrameworkConsents[id] != ""
}

// ActiveFramework returns the id of the lens that frames this run's proposals,
// selected deterministically: the first consented lens in FrameworkStack order
// (the stack's head is the operative choice), skipping any leading entry that
// is not yet consented. It returns ("", false) when no stacked lens is
// consented — the baseline, lens-neutral voice. Selection is deliberately
// static: automatic rotation is protocol P-2, deferred until the frameworks
// layer is proven, so the active lens changes only when the user re-orders or
// amends the stack (docs/frameworks.md §5; docs/protocols/P-2-lens-rotation.md).
func (c Config) ActiveFramework() (string, bool) {
	for _, id := range c.FrameworkStack {
		if c.LensConsented(id) {
			return id, true
		}
	}
	return "", false
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
	if err := c.Provider.validate(); err != nil {
		return err
	}
	if err := c.Companion.validate(); err != nil {
		return err
	}
	return nil
}

// validate reports whether the provider block is structurally usable: a
// set backend must be a known name (KnownBackends) and the per-call
// timeout must be at least one second so every model call is bounded.
// Reserved per-role overrides are checked the same way when present,
// even though they are unused this pillar. There is no clip rule — no
// provider bound is documented as coercible — so an out-of-range value
// is a hard error, not a silent pull-back.
func (p ProviderConfig) validate() error {
	if p.Backend != "" && !KnownBackends[p.Backend] {
		return fmt.Errorf("config: provider.backend %q is not a known backend", p.Backend)
	}
	if p.TimeoutSeconds < 1 {
		return fmt.Errorf("config: provider.timeout_seconds must be >= 1, got %d", p.TimeoutSeconds)
	}
	for role, override := range p.Roles {
		if override.Backend != "" && !KnownBackends[override.Backend] {
			return fmt.Errorf("config: provider.roles[%q].backend %q is not a known backend", role, override.Backend)
		}
	}
	return nil
}

// validate reports whether the companion block is structurally usable. The
// feature is off by default, so a zero-valued block is always valid; but
// once enabled, all three prompt-file paths must be set — an enabled
// companion with a missing template path is a hard error, mirroring the
// provider validate style, rather than a silent no-op that would leave a
// life-critical daily ritual quietly dead. The optional model override is
// unconstrained here: an unknown model surfaces at compose time from the
// provider, exactly as provider.model does. There is no clip rule — no
// companion bound is documented as coercible.
func (c CompanionConfig) validate() error {
	if !c.Enabled {
		return nil
	}
	paths := map[string]string{
		"companion.morning_template": c.MorningTemplate,
		"companion.night_template":   c.NightTemplate,
		"companion.system_prompt":    c.SystemPrompt,
	}
	for name, v := range paths {
		if v == "" {
			return fmt.Errorf("config: %s must not be empty when companion.enabled is true", name)
		}
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
