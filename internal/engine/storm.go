package engine

import (
	"fmt"
	"slices"
	"time"
)

// Storm parameters (engine-module.md §storm.json, §The tripwire). A storm has
// a bounded duration, a 72-hour witness-confirmation window, and may be
// renewed exactly once — past that it is a season, not a storm.
const (
	// StormDurationDefault is the fallback duration when storm.json carries no
	// duration_days (engine-module.md §storm.json example: 14).
	StormDurationDefault = 14
	// StormConfirmWindow is the witness-confirmation window for a pending
	// declaration; past it the declaration lapses (engine-module.md §Commands
	// / §Error states: "unconfirmed for 72 hours").
	StormConfirmWindow = 72 * time.Hour
	// StormMaxRenewals is the one renewal a storm allows (engine-module.md
	// §Error states: "A storm renews once").
	StormMaxRenewals = 1
	// StormUnwritten is the reserved label for "life exceeded the clause list"
	// (engine-module.md §Commands: `/storm unwritten`).
	StormUnwritten = "unwritten"
)

// ValidStormLabel reports whether label may be declared: the reserved
// `unwritten`, or a clause registered in storm.json's clauses (engine-module.md
// §Commands). Ambush windows are not declared by hand — they enter
// automatically — so window labels are not valid `/storm` arguments.
func ValidStormLabel(h StormHistory, label string) bool {
	if label == StormUnwritten {
		return true
	}
	return slices.Contains(h.Clauses, label)
}

// DeclareStorm builds the `declared` event for `/storm <label>` (engine-module.md
// §Commands). It rejects an unknown label (§Error states: "citing an unknown
// label"). The declaration stands only on witness confirmation within the
// 72-hour window; until then no storm state exists (StormStanding stays false).
func DeclareStorm(h StormHistory, label string, at time.Time) (StormEvent, error) {
	if !ValidStormLabel(h, label) {
		return StormEvent{}, fmt.Errorf("engine: no clause or window named %q", label)
	}
	return StormEvent{At: at.Format(time.RFC3339), Event: StormDeclared, Label: label}, nil
}

// ConfirmStorm builds the `confirmed` event a witness confirmation records
// (engine-module.md §storm.json). It requires a pending declaration and stamps
// the through date at durationDays out from the confirmation. The confirmation
// text is recorded verbatim, exactly like witness.json's.
func ConfirmStorm(h StormHistory, by, text string, at time.Time, durationDays int) (StormEvent, error) {
	if _, pending := pendingDeclaredAt(h); !pending {
		return StormEvent{}, fmt.Errorf("engine: no pending storm declaration to confirm")
	}
	through := DateString(AddDays(DateOf(at), stormDuration(durationDays)))
	return StormEvent{At: at.Format(time.RFC3339), Event: StormConfirmed, By: by, Text: text, Through: through}, nil
}

// RenewStorm builds the `renewed` event for a `/storm <label>` re-issued before
// expiry (engine-module.md §Commands: "Renewal is the same command re-issued
// before expiry, once"). It rejects a second renewal (§Error states) and a
// renewal when no storm stands.
func RenewStorm(h StormHistory, at time.Time, durationDays int) (StormEvent, error) {
	if standing, _ := StormStanding(h, at, at.Location()); !standing {
		return StormEvent{}, fmt.Errorf("engine: no standing storm to renew")
	}
	if RenewalCount(h) >= StormMaxRenewals {
		return StormEvent{}, fmt.Errorf("engine: a storm renews once — past that it is a season, not a storm")
	}
	through := DateString(AddDays(DateOf(at), stormDuration(durationDays)))
	return StormEvent{At: at.Format(time.RFC3339), Event: StormRenewed, Through: through}, nil
}

// EndStorm builds the `ended` event for `/storm end` (engine-module.md
// §Commands). It requires a standing storm; breach math resets at exit
// (handled by the derived status's consecutive-miss reset).
func EndStorm(h StormHistory, at time.Time) (StormEvent, error) {
	if standing, _ := StormStanding(h, at, at.Location()); !standing {
		return StormEvent{}, fmt.Errorf("engine: no standing storm to end")
	}
	return StormEvent{At: at.Format(time.RFC3339), Event: StormEnded}, nil
}

// RenewalCount counts the renewals already recorded in a storm's history.
func RenewalCount(h StormHistory) int {
	n := 0
	for _, e := range h.History {
		if e.Event == StormRenewed {
			n++
		}
	}
	return n
}

// StandingConfirmedDate returns the civil date the standing storm was
// confirmed (or entered/renewed) — the `(confirmed <date>)` the L2 storm
// variant carries (engine-module.md §Consent amendment). It reads the latest
// standing event's timestamp; "" when no standing event carries a date.
func StandingConfirmedDate(h StormHistory, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	for i := len(h.History) - 1; i >= 0; i-- {
		e := h.History[i]
		switch e.Event {
		case StormConfirmed, StormEntered, StormRenewed:
			if at, err := time.Parse(time.RFC3339, e.At); err == nil {
				return DateString(DateOf(at.In(loc)))
			}
			return ""
		}
	}
	return ""
}

// StormInForce reports whether a storm was in force on the civil day `day`
// (engine-module.md §storm.json: entry applies from the declaration/window
// start forward, never backward). Unlike [StormStanding] — which answers "does
// a storm stand as of now?" for the /status surface — this respects the lower
// bound, so the tripwire never treats a day before a storm's start as a storm
// day ("entry never annotates a day before its declaration").
func StormInForce(h StormHistory, day time.Time, loc *time.Location) bool {
	if loc == nil {
		loc = time.UTC
	}
	d := DateOf(day.In(loc))
	standing, _ := StormStanding(h, day, loc)
	if !standing {
		return false
	}
	start, ok := stormStart(h, loc)
	if !ok {
		// A bare ambush window (no history) already lower-bounds itself inside
		// StormStanding, so trust the standing answer.
		return true
	}
	return !d.Before(DateOf(start))
}

// StormBookkeeping computes the deterministic storm state changes a tripwire
// run performs (engine-module.md §The tripwire "Storm behavior"): a pending
// declaration past the 72-hour window lapses; a standing storm past its
// through date expires; an ambush window whose start date has arrived enters.
// It returns the events to append (in that order) and whether a declaration
// lapsed (the caller posts the user-channel lapse note). No sends here — entry
// and expiry are silent bookkeeping.
func StormBookkeeping(h StormHistory, now time.Time, loc *time.Location) (events []StormEvent, lapsed bool) {
	if loc == nil {
		loc = time.UTC
	}
	today := DateOf(now.In(loc))
	nowStr := now.Format(time.RFC3339)

	if at, pending := pendingDeclaredAt(h); pending && now.Sub(at) >= StormConfirmWindow {
		events = append(events, StormEvent{At: nowStr, Event: StormLapsed})
		lapsed = true
	}

	if through, ok := standingThrough(h); ok {
		if td, tok := dateInLoc(through, loc); tok && today.After(DateOf(td)) {
			events = append(events, StormEvent{At: nowStr, Event: StormExpired})
		}
	}

	events = append(events, ambushEntries(h, today, nowStr, loc)...)
	return events, lapsed
}

// ambushEntries materializes any ambush window that contains today into a
// StormEntered event. Only a *history-based* standing storm
// (declared/confirmed/renewed) suppresses an entry — a bare window reads as
// standing in StormStanding for the /status surface, but the whole point of the
// entry is to materialize that window into history, so it must not block
// itself; an already-entered window is likewise skipped.
func ambushEntries(h StormHistory, today time.Time, nowStr string, loc *time.Location) []StormEvent {
	histStanding := false
	if through, ok := standingThrough(h); ok {
		if td, tok := dateInLoc(through, loc); tok && !today.After(DateOf(td)) {
			histStanding = true
		}
	}
	var events []StormEvent
	for _, w := range h.Windows {
		s, sok := dateInLoc(w.Start, loc)
		e, eok := dateInLoc(w.End, loc)
		if !sok || !eok || today.Before(DateOf(s)) || today.After(DateOf(e)) {
			continue
		}
		if histStanding || windowEntered(h, w) {
			continue
		}
		events = append(events, StormEvent{At: nowStr, Event: StormEntered, Label: w.Label, Through: w.End})
	}
	return events
}

// WithEvents returns a copy of the history with events appended — the folded
// view a tripwire run reasons over before the adapter persists the appends.
func (h StormHistory) WithEvents(events ...StormEvent) StormHistory {
	out := h
	out.History = append(slices.Clone(h.History), events...)
	return out
}

// pendingDeclaredAt returns the timestamp of a pending declaration — the
// latest history event is a `declared` with nothing (confirm/lapse/expire)
// after it — and whether one is pending.
func pendingDeclaredAt(h StormHistory) (time.Time, bool) {
	n := len(h.History)
	if n == 0 || h.History[n-1].Event != StormDeclared {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339, h.History[n-1].At)
	if err != nil {
		return time.Time{}, false
	}
	return at, true
}

// standingThrough returns the through date of the latest standing event and
// whether the latest event is a standing kind carrying one.
func standingThrough(h StormHistory) (string, bool) {
	n := len(h.History)
	if n == 0 {
		return "", false
	}
	last := h.History[n-1]
	switch last.Event {
	case StormConfirmed, StormEntered, StormRenewed:
		if last.Through != "" {
			return last.Through, true
		}
	}
	return "", false
}

// windowEntered reports whether an `entered` event already exists for window w.
func windowEntered(h StormHistory, w StormWindow) bool {
	return slices.ContainsFunc(h.History, func(e StormEvent) bool {
		return e.Event == StormEntered && e.Through == w.End && e.Label == w.Label
	})
}

// stormStart resolves the lower-bound date the latest standing storm applies
// from: a window's start for an entered ambush, the earliest declaration for a
// confirmed/renewed storm.
func stormStart(h StormHistory, loc *time.Location) (time.Time, bool) {
	n := len(h.History)
	if n == 0 {
		return time.Time{}, false
	}
	last := h.History[n-1]
	switch last.Event {
	case StormEntered:
		for _, w := range h.Windows {
			if w.End == last.Through && w.Label == last.Label {
				if s, ok := dateInLoc(w.Start, loc); ok {
					return s, true
				}
			}
		}
	case StormConfirmed, StormRenewed:
		if d, ok := earliestDeclaredDate(h, loc); ok {
			return d, true
		}
	}
	if at, err := time.Parse(time.RFC3339, last.At); err == nil {
		return DateOf(at.In(loc)), true
	}
	return time.Time{}, false
}

// earliestDeclaredDate returns the civil date of the first `declared` event.
func earliestDeclaredDate(h StormHistory, loc *time.Location) (time.Time, bool) {
	for _, e := range h.History {
		if e.Event == StormDeclared {
			if at, err := time.Parse(time.RFC3339, e.At); err == nil {
				return DateOf(at.In(loc)), true
			}
		}
	}
	return time.Time{}, false
}

// stormDuration returns d, or the default when d is non-positive.
func stormDuration(d int) int {
	if d <= 0 {
		return StormDurationDefault
	}
	return d
}
