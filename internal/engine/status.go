package engine

import "time"

// Escalation and storm states carried in status.json. Phase 8 always
// reports "none"; the tripwire (Phase 10) drives the escalation state and
// the storm reader (Phase 10) drives the storm state.
const (
	EscalationNone = "none"
	StormNone      = "none"
)

// Status is the derived engine/status.json projection (engine-module.md
// §status.json). It is rebuilt from folded day records plus chain, storm,
// and profile state; deleting it and rebuilding reproduces it byte-for-
// byte. Phase 8 fills the streak, chain-start, accounting, and profile
// fields; the adherence, error-budget, and storm-scoring fields are added
// by Phase 9. Field order is fixed for deterministic marshaling.
type Status struct {
	CurrentStreak    int     `json:"current_streak"`
	LongestStreak    int     `json:"longest_streak"`
	ChainStart       *string `json:"chain_start"`
	RawDaysAccounted int     `json:"raw_days_accounted"`
	EscalationState  string  `json:"escalation_state"`
	StormState       string  `json:"storm_state"`
	ActiveProfile    string  `json:"active_profile"`
}

// BuildStatus computes the derived status from folded day records. It is
// pure: the same inputs always yield the same Status (the deterministic-
// rebuild criterion). chainStart is echoed from chain.json after the
// caller has stamped it; activeProfile comes from profile.json.
func BuildStatus(records []DayRecord, chainStart *string, activeProfile string, loc *time.Location) Status {
	streaks := ComputeStreaks(records, loc)
	return Status{
		CurrentStreak:    streaks.Current,
		LongestStreak:    streaks.Longest,
		ChainStart:       chainStart,
		RawDaysAccounted: len(records),
		EscalationState:  EscalationNone,
		StormState:       StormNone,
		ActiveProfile:    orDefaultProfile(activeProfile),
	}
}
