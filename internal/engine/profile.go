package engine

import (
	"fmt"
	"time"
)

// ProfileSwitch is one recorded clock-profile change (engine-module.md
// §profile.json `history`). effective is the logical day the switch takes
// hold — always the next one, never the day of the switch.
type ProfileSwitch struct {
	At        string `json:"at"`
	From      string `json:"from"`
	To        string `json:"to"`
	Effective string `json:"effective"`
}

// ProfileState is profile.json: the active profile plus its append-only
// switch history.
type ProfileState struct {
	Active  string          `json:"active"`
	History []ProfileSwitch `json:"history"`
}

// DefaultProfileState returns a fresh profile.json for a new Engine tree:
// the default clocks, no switches yet.
func DefaultProfileState() ProfileState {
	return ProfileState{Active: DefaultProfile, History: []ProfileSwitch{}}
}

// GoverningProfile resolves which profile governs a close-out performed on
// wallDate (engine-module.md §profile.json: switches take effect from
// their effective logical day; the outgoing day completes on the clocks
// that started it). A switch applies once wallDate ≥ its effective date;
// the latest applicable switch wins. Before any switch, the default
// governs. Comparing against the wall date — not the resolved logical day
// — is what lets a nights profile (rollover 12:00) attribute an 11:00
// close-out to the previous logical day while still stamping the new
// profile.
func GoverningProfile(wallDate time.Time, history []ProfileSwitch, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	wall := DateOf(wallDate)
	active := DefaultProfile
	for _, sw := range history {
		eff, err := time.ParseInLocation(dateLayout, sw.Effective, loc)
		if err != nil {
			continue
		}
		if !wall.Before(DateOf(eff)) {
			active = sw.To
		}
	}
	return active
}

// ComputeSwitch builds the ProfileSwitch for `/profile name` issued at now
// (engine-module.md §profile.json: effective is the next logical day under
// the clocks active at switch time, so a switch after tonight's bell
// cannot move tonight's clocks). It validates name against the chain and
// rejects an undefined profile (engine-module.md §Error states).
func ComputeSwitch(chain ChainConfig, state ProfileState, name string, now time.Time) (ProfileSwitch, error) {
	if !chain.HasProfile(name) {
		return ProfileSwitch{}, fmt.Errorf("engine: no profile named %q", name)
	}
	from := GoverningProfile(now, state.History, now.Location())
	clocks, err := chain.ClocksFor(from)
	if err != nil {
		return ProfileSwitch{}, err
	}
	current := clocks.baseLogicalDate(now)
	effective := AddDays(current, 1)
	return ProfileSwitch{
		At:        now.Format(time.RFC3339),
		From:      from,
		To:        name,
		Effective: DateString(effective),
	}, nil
}

// WithSwitch returns a copy of the state with sw appended and Active
// updated. The history is append-only (engine-module.md §profile.json).
func (s ProfileState) WithSwitch(sw ProfileSwitch) ProfileState {
	out := ProfileState{Active: sw.To}
	out.History = append(append([]ProfileSwitch{}, s.History...), sw)
	return out
}
