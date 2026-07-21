package observations

import (
	"encoding/json"
	"fmt"
	"slices"
)

// ConfigVersion is the only observations/config.json schema version the MVP
// understands.
const ConfigVersion = 1

// Enricher declares one scheduled context source (observations.md §5). The
// enrichment job that reads these ships in Phase 12; the MVP capture phase
// only models and scaffolds the block so the config template is complete.
// Endpoint is omitted for pure-local enrichers (calendar-frame sends
// nothing).
type Enricher struct {
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Sends    string `json:"sends"`
	Endpoint string `json:"endpoint,omitempty"`
	Cadence  string `json:"cadence"`
}

// PacketConfig carries the standing clinical-context lines a user authors for
// the clinician packet header (observations.md §7), rendered verbatim. The
// packet renderer itself is Phase 12; the lines are configured here.
type PacketConfig struct {
	ClinicalContext []string `json:"clinical_context"`
}

// Config models observations/config.json (observations-module.md §"Storage
// additions"). Field order matches the documented example so a written file
// reads like the spec. key_salt is generated once at first run and salts
// every registry key derivation (§"Registry keys").
type Config struct {
	Version            int            `json:"version"`
	KeySalt            string         `json:"key_salt"`
	KindsEnabled       []Kind         `json:"kinds_enabled"`
	CuriosityBudgetDay int            `json:"curiosity_budget_per_day"`
	AgentSliceOptins   map[string]any `json:"agent_slice_optins"`
	Packet             PacketConfig   `json:"packet"`
	Enrichers          []Enricher     `json:"enrichers"`
}

// DefaultConfig returns the observations config a fresh Ledger writes, with
// the documented example's enabled kinds and enricher set (observations-
// module.md §"observations/config.json"). key_salt is filled by the storage
// adapter at scaffold time (a per-instance random secret); it is empty here
// because this package performs no I/O and has no randomness — determinism is
// a binding property of the module.
func DefaultConfig() Config {
	return Config{
		Version: ConfigVersion,
		KeySalt: "",
		// The companion-context kinds (KindWithdrawal, KindHabitChange,
		// KindCommitment) are deliberately absent here — they are enable-gated
		// and off by default (observations.md §3), added per-instance only.
		// KindMemory (the life-archive story kind, mvp/life-archive.md §3) is
		// likewise off by default: the excavation surface is enabled in the
		// operator's own runtime, never defaulted-on in the OSS Ledger, so a
		// disabled memory capture returns the enable hint rather than writing.
		KindsEnabled:       []Kind{KindPain, KindIntake, KindElimination, KindMood},
		CuriosityBudgetDay: 1,
		AgentSliceOptins:   map[string]any{},
		Packet:             PacketConfig{ClinicalContext: []string{}},
		Enrichers: []Enricher{
			{Name: "weather", Enabled: false, Sends: "quantized lat/lon + date", Endpoint: "open-meteo", Cadence: "daily"},
			{Name: "calendar-frame", Enabled: true, Sends: "nothing (local)", Cadence: "daily"},
		},
	}
}

// KindEnabled reports whether kind may be captured under this config
// (observations.md §10: all kinds off until enabled). A capture of a disabled
// kind is rejected with the enable hint, never silently stored.
func (c Config) KindEnabled(kind Kind) bool {
	return slices.Contains(c.KindsEnabled, kind)
}

// EnableHint is the fixed rejection copy for a disabled kind (error-states:
// "`pain` isn't enabled — add it to observations/config.json").
func EnableHint(kind Kind) string {
	return fmt.Sprintf("`%s` isn't enabled — add it to observations/config.json", kind)
}

// Validate reports whether the config is structurally usable.
func (c Config) Validate() error {
	if c.Version != ConfigVersion {
		return fmt.Errorf("observations: unsupported config version %d (want %d)", c.Version, ConfigVersion)
	}
	if c.KeySalt == "" {
		return fmt.Errorf("observations: key_salt must be set")
	}
	return nil
}

// Marshal renders the config as the exact indented JSON written to
// observations/config.json, with a trailing newline.
func (c Config) Marshal() ([]byte, error) {
	b, err := json.MarshalIndent(c.normalized(), "", "  ")
	if err != nil {
		return nil, fmt.Errorf("observations: marshal config: %w", err)
	}
	return append(b, '\n'), nil
}

// UnmarshalConfig parses observations/config.json bytes.
func UnmarshalConfig(b []byte) (Config, error) {
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("observations: unmarshal config: %w", err)
	}
	return c, nil
}

// normalized guarantees the collection fields are non-nil so the written
// file always carries [] / {} rather than null.
func (c Config) normalized() Config {
	if c.KindsEnabled == nil {
		c.KindsEnabled = []Kind{}
	}
	if c.AgentSliceOptins == nil {
		c.AgentSliceOptins = map[string]any{}
	}
	if c.Packet.ClinicalContext == nil {
		c.Packet.ClinicalContext = []string{}
	}
	if c.Enrichers == nil {
		c.Enrichers = []Enricher{}
	}
	return c
}
