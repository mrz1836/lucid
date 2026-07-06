package router

import (
	"fmt"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
)

// Fixed /storm user copy (engine-module.md §Commands, §Error states), matched
// verbatim by tests.
const (
	stormUnknownLabelMsg = "No clause or window by that name — clauses live in the Charter and are registered in storm.json. `/storm unwritten` if life exceeded the list."
	stormRenewTwiceMsg   = "A storm renews once (engine §4). Past that, this is a season, not a storm — it goes to the quarterly."
	stormNoStandingMsg   = "No standing storm to end — expiry at the duration bound is automatic."
	stormUsageMsg        = "Usage: /storm <clause-label|unwritten> or /storm end."
)

// StormResult reports a `/storm` command. Event is the appended history event
// kind on success (declared|renewed|ended); Rejected marks a no-op rejection
// with the fixed copy in Ack.
type StormResult struct {
	Event    string
	Label    string
	Through  string
	Rejected bool
	Ack      string
}

// Storm executes `/storm <label|unwritten>` and `/storm end` (engine-module.md
// §Commands). A `/storm <label>` re-issued while a storm stands is a renewal
// (allowed once); otherwise it is a fresh declaration that stands only on
// witness confirmation within 72 hours. Clause labels are opaque — the words
// live in the Charter; storm.json holds labels and dates only — so an unknown
// label is rejected. Every accepted command appends to storm.json's history
// and rebuilds status.json.
func (r *Router) Storm(arg string, now time.Time) (StormResult, error) {
	now = whenOr(now)
	loc := now.Location()
	if err := r.store.ScaffoldEngine(); err != nil {
		return StormResult{}, fmt.Errorf("could not prepare the engine tree: %w", err)
	}
	h, err := r.store.ReadStormState()
	if err != nil {
		return StormResult{}, err
	}

	switch strings.TrimSpace(arg) {
	case "":
		return StormResult{Rejected: true, Ack: stormUsageMsg}, nil
	case "end":
		return r.stormEnd(h, now, loc)
	default:
		if standing, _ := engine.StormStanding(h, now, loc); standing {
			return r.stormRenew(h, strings.TrimSpace(arg), now, loc)
		}
		return r.stormDeclare(h, strings.TrimSpace(arg), now, loc)
	}
}

// stormDeclare records a fresh declaration, pending witness confirmation. An
// unknown label is rejected before any write (the validity check is up front,
// so a DeclareStorm error here is a genuine, unexpected failure).
func (r *Router) stormDeclare(h engine.StormHistory, label string, now time.Time, loc *time.Location) (StormResult, error) {
	if !engine.ValidStormLabel(h, label) {
		return StormResult{Rejected: true, Ack: stormUnknownLabelMsg}, nil
	}
	ev, err := engine.DeclareStorm(h, label, now)
	if err != nil {
		return StormResult{}, err
	}
	if err := r.persistStorm(ev, loc); err != nil {
		return StormResult{}, err
	}
	return StormResult{
		Event: engine.StormDeclared, Label: label,
		Ack: fmt.Sprintf("storm declared (%s) — pending witness confirmation (72h).", label),
	}, nil
}

// stormRenew records a renewal, rejected past the one allowed (the renewal
// count is checked up front).
func (r *Router) stormRenew(h engine.StormHistory, label string, now time.Time, loc *time.Location) (StormResult, error) {
	if engine.RenewalCount(h) >= engine.StormMaxRenewals {
		return StormResult{Rejected: true, Ack: stormRenewTwiceMsg}, nil
	}
	ev, err := engine.RenewStorm(h, now, h.DurationDays)
	if err != nil {
		return StormResult{}, err
	}
	if err := r.persistStorm(ev, loc); err != nil {
		return StormResult{}, err
	}
	return StormResult{
		Event: engine.StormRenewed, Label: label, Through: ev.Through,
		Ack: fmt.Sprintf("storm renewed — standing through %s.", ev.Through),
	}, nil
}

// stormEnd ends a standing storm early, rejected when none stands (the standing
// check is up front).
func (r *Router) stormEnd(h engine.StormHistory, now time.Time, loc *time.Location) (StormResult, error) {
	if standing, _ := engine.StormStanding(h, now, loc); !standing {
		return StormResult{Rejected: true, Ack: stormNoStandingMsg}, nil
	}
	ev, err := engine.EndStorm(h, now)
	if err != nil {
		return StormResult{}, err
	}
	if err := r.persistStorm(ev, loc); err != nil {
		return StormResult{}, err
	}
	return StormResult{Event: engine.StormEnded, Ack: "storm ended — breach math resets from here."}, nil
}

// persistStorm appends a storm event and rebuilds status.json — the shared
// tail of every accepted `/storm` command.
func (r *Router) persistStorm(ev engine.StormEvent, loc *time.Location) error {
	if err := r.store.AppendStormEvent(ev); err != nil {
		return err
	}
	_, err := r.store.RebuildEngineStatus(loc)
	return err
}
