// Package scheduler runs the Engine's two scheduled jobs — the evening bell
// and the morning tripwire (engine-module.md §The tripwire) — wiring the pure
// [engine] decision to the storage adapter and a [Notifier]. It is the only
// place the Engine's fixed templates are rendered to text and the only place a
// send leaves the machine; the decision itself stays a deterministic function
// of the Ledger.
//
// The scheduler holds no clock of its own: every entry point takes an explicit
// `now`, so a simulated clock drives the acceptance tests and the host layer
// (launchd / hush supervise, Phase 15/16) drives production. No model is
// reachable from here — a guard test asserts the package imports no
// provider/agent/model package, satisfying "no LLM call in the tripwire path".
package scheduler

import (
	"fmt"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/engine/templates"
	"github.com/mrz1836/lucid/internal/storage"
)

// Notifier delivers a rendered send to a logical channel ("user" or
// "witness", per [engine.ChannelUser] / [engine.ChannelWitness]). The concrete
// implementation resolves the logical channel to a real one and injects the
// harness token from its environment (ADR-0005: the binary stays
// credential-dumb) — that lands host-side in Phase 16. Tests use a capturing
// fake; no real token is ever needed here.
type Notifier interface {
	Send(channel, text string) error
}

// Scheduler owns the bell and tripwire jobs over one Ledger.
type Scheduler struct {
	store  *storage.Adapter
	notify Notifier
}

// New constructs a scheduler over a storage adapter and a notifier.
func New(store *storage.Adapter, notify Notifier) *Scheduler {
	return &Scheduler{store: store, notify: notify}
}

// SentMessage records one delivered send — the channel, the fixed-template
// kind, and the exact rendered text — so tests can assert what fired and grep
// the witness payload for zero Mirror bytes.
type SentMessage struct {
	Channel string
	Kind    string
	Text    string
}

// Report is the outcome of one tripwire run: the reference day it evaluated,
// every send delivered, the resulting escalation_state, and the storm-history
// events it appended. It is what the supervisor and the tests read to confirm
// exactly one L1/L2 fired (and nothing more).
type Report struct {
	Reference   string
	Sends       []SentMessage
	Escalation  string
	StormEvents []engine.StormEvent
}

// RunBell posts the evening bell to the user's channel (engine-module.md §The
// tripwire: "the bell prompt is the same job's evening sibling"). The host
// scheduler owns the timing — it fires this at the active profile's bell_time —
// so RunBell takes no clock: it simply names the chain, and carries no sign-off
// (it names a chain, it does not sting). When the bell is disabled in
// chain.json nothing is sent; it returns the delivered message (Text empty when
// the bell is off).
func (sc *Scheduler) RunBell() (SentMessage, error) {
	if err := sc.store.ScaffoldEngine(); err != nil {
		return SentMessage{}, fmt.Errorf("scheduler: prepare engine tree: %w", err)
	}
	chain, err := sc.store.ReadChainConfig()
	if err != nil {
		return SentMessage{}, err
	}
	if !chain.Bell.Enabled {
		return SentMessage{}, nil
	}
	text := templates.Bell(chain.Label)
	if err := sc.notify.Send(engine.ChannelUser, text); err != nil {
		return SentMessage{}, fmt.Errorf("scheduler: send bell: %w", err)
	}
	return SentMessage{Channel: engine.ChannelUser, Kind: engine.SendBell, Text: text}, nil
}

// RunTripwire runs the morning dead-man for yesterday's logical day
// (engine-module.md §The tripwire). It reads the Ledger, evaluates the pure
// [engine.EvaluateTripwire] decision, appends any storm bookkeeping, delivers
// the sends (falling back to a user-channel note when the witness channel is
// unreachable), persists the escalation_state, and records the run so the
// monthly heartbeat fires once per calendar month.
func (sc *Scheduler) RunTripwire(now time.Time) (Report, error) {
	loc := now.Location()
	if err := sc.store.ScaffoldEngine(); err != nil {
		return Report{}, fmt.Errorf("scheduler: prepare engine tree: %w", err)
	}

	chain, err := sc.store.ReadChainConfig()
	if err != nil {
		return Report{}, err
	}
	profileState, err := sc.store.ReadProfileState()
	if err != nil {
		return Report{}, err
	}
	storm, err := sc.store.ReadStormState()
	if err != nil {
		return Report{}, err
	}
	witness, err := sc.store.ReadWitnessContract()
	if err != nil {
		return Report{}, err
	}
	tw, err := sc.store.ReadTripwireState()
	if err != nil {
		return Report{}, err
	}
	records, err := sc.store.ReadEngineDays()
	if err != nil {
		return Report{}, err
	}

	// Yesterday's logical day, under the profile governing now's wall date.
	profileName := engine.GoverningProfile(now, profileState.History, loc)
	clocks, err := chain.ClocksFor(profileName)
	if err != nil {
		return Report{}, err
	}
	reference := engine.AddDays(clocks.BaseLogicalDate(now), -1)

	curMonth := now.Format("2006-01")
	firstRunOfMonth := tw.LastHeartbeatMonth != curMonth

	dec := engine.EvaluateTripwire(engine.TripwireInput{
		Now:             now,
		Loc:             loc,
		Reference:       reference,
		Chain:           chain,
		Storm:           storm,
		Witness:         witness,
		Records:         recordsByDate(records),
		Streak:          engine.ComputeStreaks(records, loc).Current,
		FirstRunOfMonth: firstRunOfMonth,
	})

	if err := sc.store.AppendStormEvents(dec.StormEvents...); err != nil {
		return Report{}, err
	}

	rep := Report{Reference: engine.DateString(reference), Escalation: dec.EscalationState, StormEvents: dec.StormEvents}
	for _, s := range dec.Sends {
		msg, sendErr := sc.emit(s)
		if sendErr != nil {
			return rep, sendErr
		}
		rep.Sends = append(rep.Sends, msg)
	}

	// stake_owed is not accrued in the MVP tripwire (a breach's stake-window
	// bookkeeping is a /status surface); under a storm it is never owed. Pass
	// false so a storm L2 never produces stake_owed.
	if _, err := sc.store.SetEngineEscalation(loc, dec.EscalationState, false); err != nil {
		return rep, err
	}

	tw.LastRunDate = engine.DateString(engine.DateOf(now.In(loc)))
	if firstRunOfMonth {
		tw.LastHeartbeatMonth = curMonth
	}
	if err := sc.store.WriteTripwireState(tw); err != nil {
		return rep, err
	}
	return rep, nil
}

// emit renders and delivers one send. An L2 whose witness channel is
// unreachable falls back to the user-channel "you owe the message" note — the
// escalation still fired (engine-module.md §Error states); every other send's
// delivery failure is surfaced to the caller.
func (sc *Scheduler) emit(s engine.Send) (SentMessage, error) {
	text := templates.Render(s)
	if err := sc.notify.Send(s.Channel, text); err != nil {
		if s.Kind == engine.SendL2 {
			fb := templates.L2Unreachable(s.WitnessName)
			if ferr := sc.notify.Send(engine.ChannelUser, fb); ferr != nil {
				return SentMessage{}, fmt.Errorf("scheduler: L2 fallback send: %w", ferr)
			}
			return SentMessage{Channel: engine.ChannelUser, Kind: "l2_unreachable", Text: fb}, nil
		}
		return SentMessage{}, fmt.Errorf("scheduler: send %s: %w", s.Kind, err)
	}
	return SentMessage{Channel: s.Channel, Kind: s.Kind, Text: text}, nil
}

// recordsByDate keys folded day records by their logical_date for the
// tripwire's O(1) lookups of the reference day and its neighbors.
func recordsByDate(records []engine.DayRecord) map[string]engine.DayRecord {
	m := make(map[string]engine.DayRecord, len(records))
	for _, r := range records {
		m[r.LogicalDate] = r
	}
	return m
}

// Marks returns the bell and tripwire clock marks (HH:MM) for the active clock
// profile — the schedule metadata the host layer (launchd / hush supervise,
// Phase 15/16) installs the two cron entries from. It is a pure read of
// chain.json, no scheduling side effect of its own.
func Marks(chain engine.ChainConfig, profile string) (bell, tripwire string, err error) {
	if profile != engine.DefaultProfile {
		if p, ok := chain.Profiles[profile]; ok {
			return p.BellTime, p.TripwireTime, nil
		}
		return "", "", fmt.Errorf("scheduler: no profile named %q", profile)
	}
	return chain.BellTime, chain.Escalation.TripwireTime, nil
}
