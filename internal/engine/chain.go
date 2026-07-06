// Package engine is the Engine module's pure, deterministic core
// (engine-module.md). It owns the Engine schemas (chain config, day
// records, corrections, derived status, profile state), the logical-day
// rollover math, close-out record construction, backfill target
// resolution, and streak arithmetic — all as pure functions with no
// filesystem access and no LLM anywhere.
//
// Two constraints from engine-module.md §"Design constraints inherited"
// bind this package by construction:
//
//   - Agent-free (P9): nothing here calls a model. Close-out is a fixed
//     script, streak math is arithmetic. A guard test asserts no
//     provider/agent import ever reaches this package.
//   - Teeth attach to acts, never content (P3): this package reads and
//     writes only the engine/ tree via the storage adapter; it never
//     touches raw/, processed/, insights/, or people/. The close-out's
//     journal line is written to raw/ by the router, not here.
package engine

import (
	"fmt"
)

// ChainVersion is the only chain.json schema version the MVP understands.
const ChainVersion = 1

// Link is one committed act in the chain (engine-module.md §chain.json).
// key is the stable identifier used in a day record's links map; name and
// floor are human-readable.
type Link struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	Floor string `json:"floor"`
}

// SLO carries the service-level objective for the chain: the isolated
// miss budget, the gate adherence threshold, and the gate day marks.
type SLO struct {
	IsolatedMissBudgetPer30d int     `json:"isolated_miss_budget_per_30d"`
	GateThreshold            float64 `json:"gate_threshold"`
	Gates                    []int   `json:"gates"`
}

// Bell records whether the evening bell prompt is enabled.
type Bell struct {
	Enabled bool `json:"enabled"`
}

// Escalation configures the morning tripwire's ladder. l2_enabled cannot
// be true until witness.json carries a confirmation (engine-module.md
// §witness.json); the tripwire itself lands in Phase 10.
type Escalation struct {
	L1Enabled    bool   `json:"l1_enabled"`
	L2Enabled    bool   `json:"l2_enabled"`
	TripwireTime string `json:"tripwire_time"`
}

// AwayMode names which links compress and what counts as the floor while
// away (engine-module.md §chain.json), and is set only when away. The MVP
// leaves it null; the type exists so the schema round-trips.
type AwayMode struct {
	Label string   `json:"label"`
	Links []string `json:"links"`
	Floor string   `json:"floor"`
}

// ProfileClocks is a named clock alternate (engine-module.md §chain.json
// `profiles`). The top-level bell_time/rollover/escalation.tripwire_time
// are the implicit "default" profile.
type ProfileClocks struct {
	BellTime     string `json:"bell_time"`
	TripwireTime string `json:"tripwire_time"`
	Rollover     string `json:"rollover"`
}

// ChainConfig is chain.json — the one hand-edited Engine file
// (engine-module.md §chain.json). Field order matches the documented
// schema so a marshaled default reads like the spec.
type ChainConfig struct {
	Version             int                      `json:"version"`
	ChainID             string                   `json:"chain_id"`
	Label               string                   `json:"label"`
	BellTime            string                   `json:"bell_time"`
	Rollover            string                   `json:"rollover"`
	BackfillWindowDays  int                      `json:"backfill_window_days"`
	Links               []Link                   `json:"links"`
	SurvivalLink        string                   `json:"survival_link"`
	ChainStart          *string                  `json:"chain_start"`
	FootprintCapMinutes int                      `json:"footprint_cap_minutes"`
	SLO                 SLO                      `json:"slo"`
	Bell                Bell                     `json:"bell"`
	Escalation          Escalation               `json:"escalation"`
	AwayMode            *AwayMode                `json:"away_mode"`
	Profiles            map[string]ProfileClocks `json:"profiles"`
}

// DefaultProfile is the implicit profile name for the top-level clocks.
const DefaultProfile = "default"

// DefaultChain returns the documented default chain.json
// (engine-module.md §chain.json, example values). A fresh Engine tree is
// scaffolded with exactly this; chain_start is null until the first
// completed close-out stamps it.
func DefaultChain() ChainConfig {
	return ChainConfig{
		Version:            ChainVersion,
		ChainID:            "night_chain",
		Label:              "Journal. Dock. Read.",
		BellTime:           "21:30",
		Rollover:           "04:00",
		BackfillWindowDays: 7,
		Links: []Link{
			{Key: "journal", Name: "One journal line", Floor: "one line, spoken or typed"},
			{Key: "dock", Name: "Phone to charger", Floor: "phone on charger"},
			{Key: "read", Name: "Read in bed", Floor: "one page"},
		},
		SurvivalLink:        "journal",
		ChainStart:          nil,
		FootprintCapMinutes: 30,
		SLO:                 SLO{IsolatedMissBudgetPer30d: 4, GateThreshold: 0.85, Gates: []int{30, 60, 90}},
		Bell:                Bell{Enabled: true},
		Escalation:          Escalation{L1Enabled: true, L2Enabled: false, TripwireTime: "09:00"},
		AwayMode:            nil,
		Profiles: map[string]ProfileClocks{
			"nights": {BellTime: "08:30", TripwireTime: "17:00", Rollover: "12:00"},
		},
	}
}

// Clocks holds the three schedule marks for a profile as minutes-since-
// midnight, parsed once for the logical-day and bell math.
type Clocks struct {
	BellMin     int
	RolloverMin int
	TripwireMin int
}

// ClocksFor resolves the clock marks for a named profile. DefaultProfile
// uses the top-level marks; any other name must be defined in Profiles or
// the call is rejected (engine-module.md §Error states: "/profile naming
// an undefined profile"). It is the single place a profile name is
// validated against the chain config.
func (c ChainConfig) ClocksFor(profile string) (Clocks, error) {
	bell, rollover, tripwire := c.BellTime, c.Rollover, c.Escalation.TripwireTime
	if profile != DefaultProfile {
		p, ok := c.Profiles[profile]
		if !ok {
			return Clocks{}, fmt.Errorf("engine: no profile named %q", profile)
		}
		bell, rollover, tripwire = p.BellTime, p.Rollover, p.TripwireTime
	}
	bm, err := parseHM(bell)
	if err != nil {
		return Clocks{}, fmt.Errorf("engine: bell_time: %w", err)
	}
	rm, err := parseHM(rollover)
	if err != nil {
		return Clocks{}, fmt.Errorf("engine: rollover: %w", err)
	}
	tm, err := parseHM(tripwire)
	if err != nil {
		return Clocks{}, fmt.Errorf("engine: tripwire_time: %w", err)
	}
	return Clocks{BellMin: bm, RolloverMin: rm, TripwireMin: tm}, nil
}

// HasProfile reports whether name is a switchable profile: DefaultProfile
// is always valid, any other name must be defined in Profiles.
func (c ChainConfig) HasProfile(name string) bool {
	if name == DefaultProfile {
		return true
	}
	_, ok := c.Profiles[name]
	return ok
}

// LinkKeys returns the chain's link keys in chain order — the order the
// compact close-out form and the guided prompts both follow.
func (c ChainConfig) LinkKeys() []string {
	keys := make([]string, len(c.Links))
	for i, l := range c.Links {
		keys[i] = l.Key
	}
	return keys
}
