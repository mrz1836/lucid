package engine

import (
	"fmt"
	"strings"
	"time"
)

// CloseoutInput is the gathered result of one close-out — from the guided
// flow or the compact form, they build identical inputs (engine-module.md
// §"Compact form": both forms write identical records). It is pure data;
// BuildDayRecord turns it into a DayRecord.
type CloseoutInput struct {
	// LogicalDay is the resolved logical day this close-out answers.
	LogicalDay time.Time
	// RecordedAt is the wall-clock write instant.
	RecordedAt time.Time
	// Links maps each chain link key to done/floor/skipped. Empty for a
	// skip or a fully-interrupted partial.
	Links map[string]string
	// Capacity is the 1–5 capacity digit (0 = not gathered).
	Capacity int
	// LimiterTag is the optional one-word limiter.
	LimiterTag string
	// RawEntryID is the id of the journal raw entry the router wrote.
	RawEntryID string
	// Mode is the declared mode; empty defaults to green.
	Mode Mode
	// ModeDeclaredAt is when /mode declared it (empty when undeclared).
	ModeDeclaredAt string
	// Profile is the profile that governed the logical day.
	Profile string
	// Storm marks the record as taken under a standing storm (Phase 10
	// wires the storm reader; Phase 8 always stamps false).
	Storm bool
	// Backfilled marks a record created after its logical day.
	Backfilled bool
	// Partial marks an interrupted close-out (engine-module.md step 7).
	Partial bool
	// Skip records an explicit miss (`/closeout skip`).
	Skip bool
}

// SurvivalRan reports whether the survival link ran (done or floor) for
// this input — the test for whether a partial close-out still counts
// (engine-module.md: "a partial close-out with at least the survival floor
// link still counts per the declared mode").
func (in CloseoutInput) SurvivalRan(survivalLink string) bool {
	s := in.Links[survivalLink]
	return s == StatusDone || s == StatusFloor
}

// BuildDayRecord constructs the day record for a close-out input. It is
// deterministic and side-effect-free: the same input and chain always
// yield the same record (the compact/guided identical-record criterion).
//
// completed/missed/partial/floor_day are derived here:
//   - skip ⇒ missed, not completed, empty links (an honest zero).
//   - partial ⇒ completed only if the survival link ran; partial flag set.
//   - otherwise ⇒ completed when the survival link ran.
//   - floor_day ⇒ at least one present link ran at its floor.
func BuildDayRecord(chain ChainConfig, in CloseoutInput) DayRecord {
	mode := in.Mode
	if mode == "" {
		mode = ModeGreen
	}
	rec := DayRecord{
		DayID:          DayID(in.LogicalDay),
		LogicalDate:    DateString(in.LogicalDay),
		RecordedAt:     in.RecordedAt.Format(time.RFC3339),
		Mode:           mode,
		ModeDeclaredAt: in.ModeDeclaredAt,
		Links:          map[string]string{},
		Capacity:       in.Capacity,
		LimiterTag:     in.LimiterTag,
		RawEntryID:     in.RawEntryID,
		Backfilled:     in.Backfilled,
		Storm:          in.Storm,
		Profile:        orDefaultProfile(in.Profile),
		Corrections:    []Correction{},
	}

	if in.Skip {
		rec.Missed = true
		return rec
	}

	for k, v := range in.Links {
		rec.Links[k] = v
	}
	rec.FloorDay = anyFloor(in.Links)
	survival := in.SurvivalRan(chain.SurvivalLink)
	if in.Partial {
		rec.Partial = true
		rec.Completed = survival
		rec.Missed = !survival
		return rec
	}
	rec.Completed = survival
	rec.Missed = !survival
	return rec
}

// anyFloor reports whether at least one link ran at its floor.
func anyFloor(links map[string]string) bool {
	for _, v := range links {
		if v == StatusFloor {
			return true
		}
	}
	return false
}

// orDefaultProfile returns p, or DefaultProfile when p is empty.
func orDefaultProfile(p string) string {
	if p == "" {
		return DefaultProfile
	}
	return p
}

// GuidedPrompts is the ordered prompt plan for the guided close-out: one
// prompt per link, then capacity, then the journal line — proving the
// two-minute budget by construction (engine-module.md: close-out shows ≤
// (links + 3) prompts). The limiter tag rides on the capacity prompt, so
// the plan is exactly links + 2, comfortably within the ceiling.
func GuidedPrompts(chain ChainConfig) []string {
	prompts := make([]string, 0, len(chain.Links)+2)
	for _, l := range chain.Links {
		prompts = append(prompts, fmt.Sprintf("%s — done / floor / skipped?", l.Name))
	}
	prompts = append(
		prompts,
		"Capacity (1–5) and an optional one-word limiter?",
		"One journal line?",
	)
	return prompts
}

// PromptBudget returns the ceiling for a chain's close-out prompt count:
// links + 3 (engine-module.md §Phase 8 acceptance).
func PromptBudget(chain ChainConfig) int { return len(chain.Links) + 3 }

// compactStatus maps a compact-form character to a link status (d done,
// f floor, x skipped), reporting whether the character is recognized.
func compactStatus(r rune) (string, bool) {
	switch r {
	case 'd':
		return StatusDone, true
	case 'f':
		return StatusFloor, true
	case 'x':
		return StatusSkipped, true
	default:
		return "", false
	}
}

// ParseCompact parses the compact close-out form (engine-module.md
// §"Compact form"):
//
//	dfx 3/wrist Long day but the chain ran.
//
// one character per link in chain order (d done, f floor, x skipped), a
// capacity digit with optional /tag, then the journal line. It is a
// deterministic parser; the same string always yields the same input,
// identical to what the guided flow would build. It fills Links, Capacity,
// LimiterTag, and returns the journal line; timing/profile fields are set
// by the caller.
func ParseCompact(chain ChainConfig, s string) (links map[string]string, capacity int, limiterTag, journal string, err error) {
	fields := strings.SplitN(strings.TrimSpace(s), " ", 3)
	if len(fields) < 2 {
		return nil, 0, "", "", fmt.Errorf("engine: compact form needs at least link chars and capacity")
	}

	chars := fields[0]
	keys := chain.LinkKeys()
	if len([]rune(chars)) != len(keys) {
		return nil, 0, "", "", fmt.Errorf("engine: compact form has %d link chars, chain has %d links", len([]rune(chars)), len(keys))
	}
	links = make(map[string]string, len(keys))
	for i, r := range chars {
		st, ok := compactStatus(r)
		if !ok {
			return nil, 0, "", "", fmt.Errorf("engine: unknown link char %q (want d/f/x)", string(r))
		}
		links[keys[i]] = st
	}

	capacity, limiterTag, err = parseCapacityTag(fields[1])
	if err != nil {
		return nil, 0, "", "", err
	}

	if len(fields) == 3 {
		journal = strings.TrimSpace(fields[2])
	}
	return links, capacity, limiterTag, journal, nil
}

// parseCapacityTag parses the "3" or "3/wrist" capacity field.
func parseCapacityTag(f string) (capacity int, tag string, err error) {
	capPart, tag, _ := strings.Cut(f, "/")
	if len(capPart) != 1 || capPart[0] < '1' || capPart[0] > '5' {
		return 0, "", fmt.Errorf("engine: capacity must be a single digit 1–5, got %q", capPart)
	}
	return int(capPart[0] - '0'), tag, nil
}
