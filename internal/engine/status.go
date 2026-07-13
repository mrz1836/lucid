package engine

import (
	"slices"
	"time"
)

// Escalation states carried in status.json (engine-module.md §status.json).
// Phase 9 always reports EscalationNone; the tripwire (Phase 10) is the code
// that drives l1_fired / l2_fired.
const (
	EscalationNone = "none"
	EscalationL1   = "l1_fired"
	EscalationL2   = "l2_fired"
)

// Storm states carried in status.json. StormNone is the default; a standing
// storm reports StormStandingState with its through date (engine-module.md
// §status.json: "storm_state (none, or standing with its through date)").
const (
	StormNone          = "none"
	StormStandingState = "standing"
)

// Storm history event kinds (engine-module.md §storm.json `history[]`). A
// storm is standing when the latest event is one of the standing kinds and
// today is on or before its through date.
const (
	StormDeclared  = "declared"
	StormConfirmed = "confirmed"
	StormEntered   = "entered"
	StormRenewed   = "renewed"
	StormExpired   = "expired"
	StormEnded     = "ended"
	StormLapsed    = "lapsed"
)

// StormWindow is one ambush window (engine-module.md §storm.json `windows`):
// a pre-registered date range that enters automatically.
type StormWindow struct {
	Label string `json:"label"`
	Start string `json:"start"`
	End   string `json:"end"`
}

// StormEvent is one append-only entry in storm.json's history. Only the
// fields Phase 9 reads are typed with omitempty so an unmarshaled stub
// round-trips; the tripwire phase (Phase 10) owns writing these.
type StormEvent struct {
	At      string `json:"at"`
	Event   string `json:"event"`
	Label   string `json:"label,omitempty"`
	By      string `json:"by,omitempty"`
	Text    string `json:"text,omitempty"`
	Through string `json:"through,omitempty"`
}

// StormHistory is the parsed storm.json (engine-module.md §storm.json). The
// derived status reads it to decide whether a storm stands and, if so,
// through what date; the day records carry the per-day storm stamp used for
// budget and breach math.
type StormHistory struct {
	Clauses      []string      `json:"clauses"`
	Windows      []StormWindow `json:"windows"`
	DurationDays int           `json:"duration_days"`
	History      []StormEvent  `json:"history"`
}

// StormStanding reports whether a storm stands as of asOf and, if so, the
// through date it stands to (engine-module.md §storm.json: "A storm is
// standing when the latest history state is confirmed/entered/renewed and
// today ≤ its through date"). A bare ambush window that contains asOf stands
// automatically when no history event has superseded it. The check is a pure
// function of the storm history and the reference date — no clock, no LLM.
func StormStanding(h StormHistory, asOf time.Time, loc *time.Location) (standing bool, through string) {
	if loc == nil {
		loc = time.UTC
	}
	asOf = DateOf(asOf.In(loc))

	// The latest history event decides: a standing kind whose through date
	// has not passed keeps the storm up; anything else (declared-but-
	// unconfirmed, expired, ended, lapsed) means no storm stands.
	if n := len(h.History); n > 0 {
		last := h.History[n-1]
		switch last.Event {
		case StormConfirmed, StormEntered, StormRenewed:
			if d, ok := dateInLoc(last.Through, loc); ok && !asOf.After(DateOf(d)) {
				return true, last.Through
			}
		}
		return false, ""
	}

	// No history yet: an ambush window containing asOf stands on its own.
	for _, w := range h.Windows {
		s, sok := dateInLoc(w.Start, loc)
		e, eok := dateInLoc(w.End, loc)
		if sok && eok && !asOf.Before(DateOf(s)) && !asOf.After(DateOf(e)) {
			return true, w.End
		}
	}
	return false, ""
}

// Window is one rolling adherence window (7- or 30-day). Adherence never
// travels alone: it is always co-presented with the floor-day ratio and the
// days-accounted co-number (engine-module.md §3 "honest-number pairing"), so
// a 1.0 over two accounted days can never masquerade as an unbroken month.
type Window struct {
	Length        int     `json:"length"`
	Adherence     float64 `json:"adherence"`
	Completed     int     `json:"completed"`
	Decided       int     `json:"decided"`
	FloorDays     int     `json:"floor_days"`
	FloorDayRatio float64 `json:"floor_day_ratio"`
	DaysAccounted int     `json:"days_accounted"`
}

// ErrorBudget is the isolated-miss allowance and its burn over the trailing
// 30 days (engine-module.md §status.json: "error-budget burn (isolated
// misses in the trailing 30 days vs budget — storm misses spend nothing)").
type ErrorBudget struct {
	Budget    int  `json:"budget"`
	Burn      int  `json:"burn"`
	Remaining int  `json:"remaining"`
	Exceeded  bool `json:"exceeded"`
}

// Status is the derived engine/status.json projection (engine-module.md
// §status.json). It is rebuilt from folded day records plus chain, storm, and
// profile state; deleting it and rebuilding reproduces it byte-for-byte from
// the same inputs (the determinism criterion). Field order is fixed for a
// stable on-disk form.
type Status struct {
	CurrentStreak     int         `json:"current_streak"`
	LongestStreak     int         `json:"longest_streak"`
	ChainStart        *string     `json:"chain_start"`
	RawDaysAccounted  int         `json:"raw_days_accounted"`
	Adherence7d       Window      `json:"adherence_7d"`
	Adherence30d      Window      `json:"adherence_30d"`
	ErrorBudget       ErrorBudget `json:"error_budget"`
	ConsecutiveMisses int         `json:"consecutive_misses"`
	EscalationState   string      `json:"escalation_state"`
	StormState        string      `json:"storm_state"`
	StormThrough      *string     `json:"storm_through"`
	StakeOwed         bool        `json:"stake_owed"`
	WitnessLapsed     bool        `json:"witness_lapsed"`
	ActiveProfile     string      `json:"active_profile"`
	DaysToNextGate    *int        `json:"days_to_next_gate"`
}

// StatusInput is everything BuildStatus folds into the derived status: the
// already-folded day records, the chain config (for the SLO budget and gate
// marks), the storm history, the stamped chain_start, the active profile,
// and the location the logical dates are interpreted in.
//
// Escalation, StakeOwed, and WitnessLapsed are the fields the derived status
// carries but does not compute from the day records alone. Escalation and
// StakeOwed are tripwire-owned (engine-module.md §tripwire: the morning job
// sets escalation_state / stake_owed) and are threaded in so a rebuild
// preserves them rather than resetting the ladder; WitnessLapsed derives from
// witness.json (a stored input), surfaced so /status can say "witness lapsed —
// L2 disarmed". They default to "no open escalation" so every existing caller
// that omits them keeps the pre-tripwire behavior.
type StatusInput struct {
	Records       []DayRecord
	Chain         ChainConfig
	Storm         StormHistory
	ChainStart    *string
	Profile       string
	Escalation    string
	StakeOwed     bool
	WitnessLapsed bool
	Loc           *time.Location
}

// BuildStatus computes the derived status (engine-module.md §status.json). It
// is pure: the same inputs always yield the same Status, which is what makes
// delete-and-rebuild byte-reproducible. All rolling windows and the storm /
// gate reference resolve to the most recent recorded logical day — a stored
// anchor, not the wall clock — so the projection is a function of the Ledger
// alone. Adherence is mode-relative only through completion (a Yellow floor
// day is completed, so it scores 1.0); the MVP scores links, not a Crux.
func BuildStatus(in StatusInput) Status {
	loc := in.Loc
	if loc == nil {
		loc = time.UTC
	}
	records := in.Records
	streaks := ComputeStreaks(records, loc)

	budget := in.Chain.SLO.IsolatedMissBudgetPer30d
	escalation := in.Escalation
	if escalation == "" {
		escalation = EscalationNone
	}
	st := Status{
		CurrentStreak:    streaks.Current,
		LongestStreak:    streaks.Longest,
		ChainStart:       in.ChainStart,
		RawDaysAccounted: len(records),
		Adherence7d:      Window{Length: 7},
		Adherence30d:     Window{Length: 30},
		ErrorBudget:      ErrorBudget{Budget: budget, Remaining: budget},
		EscalationState:  escalation,
		StormState:       StormNone,
		StakeOwed:        in.StakeOwed,
		WitnessLapsed:    in.WitnessLapsed,
		ActiveProfile:    orDefaultProfile(in.Profile),
	}

	ref, hasRef := latestRecordDate(records, loc)
	if !hasRef {
		return st
	}

	st.Adherence7d = windowStats(records, ref, 7, loc)
	st.Adherence30d = windowStats(records, ref, 30, loc)
	st.ErrorBudget = errorBudget(records, ref, budget, loc)
	st.ConsecutiveMisses = consecutiveMisses(records, ref, loc)

	if standing, through := StormStanding(in.Storm, ref, loc); standing {
		st.StormState = StormStandingState
		t := through
		st.StormThrough = &t
	}

	st.DaysToNextGate = daysToNextGate(in.ChainStart, in.Chain.SLO.Gates, ref, loc)
	return st
}

// dateInLoc parses a YYYY-MM-DD logical date in loc, reporting ok=false on a
// malformed or empty value so callers skip it rather than panic.
func dateInLoc(s string, loc *time.Location) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	d, err := time.ParseInLocation(dateLayout, s, loc)
	if err != nil {
		return time.Time{}, false
	}
	return d, true
}

// latestRecordDate returns the most recent parseable logical date among the
// records — the deterministic anchor for every rolling window and the storm /
// gate reference. found is false when no record carries a valid date.
func latestRecordDate(records []DayRecord, loc *time.Location) (latest time.Time, found bool) {
	for _, r := range records {
		d, ok := dateInLoc(r.LogicalDate, loc)
		if !ok {
			continue
		}
		if !found || d.After(latest) {
			latest, found = d, true
		}
	}
	return latest, found
}

// windowStats folds the records whose logical date falls in the length-day
// window ending at ref (inclusive) into an adherence Window. A day is
// "decided" once it is completed or missed; adherence is completed/decided
// (floors count as completions — teeth math has no shame score). Accounted
// counts any record, so an in-progress mode-only day inflates the co-number,
// never the adherence.
func windowStats(records []DayRecord, ref time.Time, length int, loc *time.Location) Window {
	start := AddDays(ref, -(length - 1))
	w := Window{Length: length}
	for _, r := range records {
		d, ok := dateInLoc(r.LogicalDate, loc)
		if !ok || d.Before(start) || d.After(ref) {
			continue
		}
		w.DaysAccounted++
		if r.FloorDay {
			w.FloorDays++
		}
		switch {
		case r.Completed:
			w.Completed++
			w.Decided++
		case r.Missed:
			w.Decided++
		}
	}
	if w.Decided > 0 {
		w.Adherence = float64(w.Completed) / float64(w.Decided)
		w.FloorDayRatio = float64(w.FloorDays) / float64(w.Decided)
	}
	return w
}

// errorBudget computes the isolated-miss burn over the trailing 30 days
// ending at ref. An isolated miss is a missed day with no calendar-adjacent
// missed neighbor — a run of two or more adjacent misses is a breach the
// witness ladder handles, not a budget spend. Storm misses spend nothing
// (engine-module.md §status.json / §tripwire "storm misses spend no error
// budget"), so they never add to the burn even when isolated.
func errorBudget(records []DayRecord, ref time.Time, budget int, loc *time.Location) ErrorBudget {
	windowStart := AddDays(ref, -29)
	missed := missedDateSet(records, loc)

	burn := 0
	for _, r := range records {
		if !r.Missed || r.Storm {
			continue
		}
		d, ok := dateInLoc(r.LogicalDate, loc)
		if !ok || d.Before(windowStart) || d.After(ref) {
			continue
		}
		prev := DateString(AddDays(d, -1))
		next := DateString(AddDays(d, 1))
		if !missed[prev] && !missed[next] {
			burn++
		}
	}

	remaining := budget - burn
	if remaining < 0 {
		remaining = 0
	}
	return ErrorBudget{Budget: budget, Burn: burn, Remaining: remaining, Exceeded: burn > budget}
}

// missedDateSet is the set of civil date strings that carry a missed record —
// storm or not — used to test calendar adjacency for the isolated-miss rule.
func missedDateSet(records []DayRecord, loc *time.Location) map[string]bool {
	set := map[string]bool{}
	for _, r := range records {
		if !r.Missed {
			continue
		}
		if d, ok := dateInLoc(r.LogicalDate, loc); ok {
			set[DateString(DateOf(d))] = true
		}
	}
	return set
}

// consecutiveMisses is the trailing run of calendar-adjacent non-storm missed
// days ending at ref. It stops at the first completed day, a gap in the
// record set, or a storm day — so storm misses stay unaccrued for breach math
// and the counter resets at storm exit (the first post-storm miss is run 1),
// exactly as engine-module.md §tripwire requires.
func consecutiveMisses(records []DayRecord, ref time.Time, loc *time.Location) int {
	byDate := map[string]DayRecord{}
	for _, r := range records {
		if d, ok := dateInLoc(r.LogicalDate, loc); ok {
			byDate[DateString(DateOf(d))] = r
		}
	}

	count := 0
	for cur := DateOf(ref); ; cur = AddDays(cur, -1) {
		r, ok := byDate[DateString(cur)]
		if !ok || r.Completed || r.Storm || !r.Missed {
			break
		}
		count++
	}
	return count
}

// daysToNextGate returns the days from ref to the next unreached gate mark,
// counted from chain_start (engine-module.md §status.json "days to next
// gate"). It is nil before the chain starts and after the final gate — gate
// decisions themselves are human (§"What the Engine intentionally is not").
func daysToNextGate(chainStart *string, gates []int, ref time.Time, loc *time.Location) *int {
	if chainStart == nil {
		return nil
	}
	start, ok := dateInLoc(*chainStart, loc)
	if !ok {
		return nil
	}
	elapsed := DaysBetween(start, ref)

	sorted := append([]int(nil), gates...)
	slices.Sort(sorted)
	for _, g := range sorted {
		if g > elapsed {
			d := g - elapsed
			return &d
		}
	}
	return nil
}
