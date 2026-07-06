package engine

import "time"

// Send channels (engine-module.md §Consent amendment). The user channel is the
// person's own Lucid channel (bell, L1, storm-lapse notes, the L2-blocked
// fallback); the witness channel is the witness's confirmed channel (L2,
// heartbeat) and carries streak/mode/storm state only — never Mirror data.
const (
	ChannelUser    = "user"
	ChannelWitness = "witness"
)

// Send kinds — the fixed template a send renders (engine-module.md §Consent
// amendment). Every kind maps to a static repo string; no kind is ever
// composed by a model. L1, L2, and the storm variants carry the pinned
// sign-off; the bell and heartbeat do not.
const (
	SendBell       = "bell"
	SendL1         = "l1"
	SendL2         = "l2"
	SendL2Blocked  = "l2_blocked"
	SendHeartbeat  = "heartbeat"
	SendStormLapse = "storm_lapse"
)

// Send is one tripwire (or bell) send: the channel it targets, the fixed
// template kind, and the scalar fields that template interpolates. It carries
// no free text and no Mirror data — the L2 send in particular exposes only
// Streak, Mode, and the storm flag, so "zero bytes of journal/capacity" holds
// by construction. Rendering to the final string is the scheduler's job (the
// only place templates live), which keeps the Engine core model-free and
// import-pure.
type Send struct {
	Channel         string
	Kind            string
	Storm           bool
	Floor           string
	Streak          int
	Mode            string
	ConfirmedDate   string
	WitnessName     string
	EscalationState string
}

// TripwireInput is everything a morning tripwire run reasons over
// (engine-module.md §The tripwire). Reference is yesterday's logical day —
// the day the dead-man evaluates — resolved by the scheduler under the active
// profile's clocks. Records is the folded day set keyed by logical_date. The
// input is pure data: EvaluateTripwire performs no IO and calls no model.
type TripwireInput struct {
	Now             time.Time
	Loc             *time.Location
	Reference       time.Time
	Chain           ChainConfig
	Storm           StormHistory
	Witness         WitnessContract
	Records         map[string]DayRecord
	Streak          int
	FirstRunOfMonth bool
}

// TripwireDecision is the pure result of a tripwire run: the sends to emit,
// the resulting escalation_state, and the storm-history events to append. The
// scheduler executes it — rendering templates, posting sends, appending storm
// events, and persisting escalation_state — but every decision here is a
// deterministic function of the stored Ledger state and the reference day.
type TripwireDecision struct {
	Sends           []Send
	EscalationState string
	StormEvents     []StormEvent
}

// EvaluateTripwire is the dead-man decision (engine-module.md §The tripwire).
// It fires on the absence of a completed record for the reference day:
//
//   - completed (or a floor-counting Away night) ⇒ reset escalation, no send.
//   - one miss with the chain already begun ⇒ L1 to the user, floor named.
//   - two consecutive misses ⇒ L2 to the confirmed witness (streak/mode/storm
//     only), or, when the witness is unarmed, an L2-blocked note to the user;
//     escalation reaches l2_fired either way.
//
// While a storm stands the contact is unchanged but its consequences pause —
// the fixed storm variants fire, budget/breach are handled by the derived
// status, never here. Storm bookkeeping (lapse/expire/enter) runs first and is
// silent except for a lapse note. The monthly heartbeat rides the same run,
// suppressed when an L2 posts to the witness. Nothing here reads raw/,
// processed/, or a model — the templates are static and the math is
// arithmetic.
func EvaluateTripwire(in TripwireInput) TripwireDecision {
	loc := in.Loc
	if loc == nil {
		loc = time.UTC
	}
	dec := TripwireDecision{EscalationState: EscalationNone}

	events, lapsed := StormBookkeeping(in.Storm, in.Now, loc)
	dec.StormEvents = events
	effStorm := in.Storm.WithEvents(events...)
	if lapsed {
		dec.Sends = append(dec.Sends, Send{Channel: ChannelUser, Kind: SendStormLapse, WitnessName: in.Witness.WitnessName})
	}

	stormOn := StormInForce(effStorm, in.Reference, loc)
	yRec, yHas := in.Records[DateString(DateOf(in.Reference))]
	completed := yHas && yRec.Completed

	l2ToWitness := false
	switch {
	case completed:
		dec.EscalationState = EscalationNone
	case !chainStarted(in.Chain.ChainStart, in.Reference, loc):
		dec.EscalationState = EscalationNone
	default:
		mode := ModeGreen
		if yHas && yRec.Mode != "" {
			mode = yRec.Mode
		}
		if escalationRun(in.Records, in.Reference, in.Chain.ChainStart, loc) >= 2 {
			dec.EscalationState = EscalationL2
			if in.Witness.L2Armed() {
				dec.Sends = append(dec.Sends, Send{
					Channel: ChannelWitness, Kind: SendL2, Storm: stormOn,
					Streak: in.Streak, Mode: mode, ConfirmedDate: StandingConfirmedDate(effStorm, loc),
					WitnessName: in.Witness.WitnessName,
				})
				l2ToWitness = true
			} else {
				dec.Sends = append(dec.Sends, Send{
					Channel: ChannelUser, Kind: SendL2Blocked, Storm: stormOn, WitnessName: in.Witness.WitnessName,
				})
			}
		} else {
			dec.EscalationState = EscalationL1
			dec.Sends = append(dec.Sends, Send{
				Channel: ChannelUser, Kind: SendL1, Storm: stormOn, Floor: survivalFloor(in.Chain),
			})
		}
	}

	if in.FirstRunOfMonth && in.Witness.IsConfirmed() && !in.Witness.IsLapsed() && !l2ToWitness {
		dec.Sends = append(dec.Sends, Send{
			Channel: ChannelWitness, Kind: SendHeartbeat, Streak: in.Streak, EscalationState: dec.EscalationState,
		})
	}
	return dec
}

// chainStarted reports whether the chain had begun on or before the reference
// day — chain_start is set on the first completed close-out (engine-module.md
// §chain.json), and there is nothing to escalate before it. This is what keeps
// the very first tripwire runs on a fresh Ledger silent.
func chainStarted(chainStart *string, ref time.Time, loc *time.Location) bool {
	if chainStart == nil {
		return false
	}
	start, ok := dateInLoc(*chainStart, loc)
	if !ok {
		return false
	}
	return !DateOf(ref).Before(DateOf(start))
}

// escalationRun counts the trailing run of non-completed logical days ending at
// ref — the dead-man run for the L1/L2 boundary (engine-module.md §The
// tripwire: one miss ⇒ L1, two consecutive ⇒ L2). An absent day is a miss
// (that is the whole point of the dead-man), so both silence and an explicit
// `/closeout skip` count; a completed day stops the run, and the chain_start
// bound stops it from running into the pre-chain past. Storm misses are
// counted here — contact is unchanged under a storm — while the budget/breach
// reset at storm exit lives in the derived status.
func escalationRun(records map[string]DayRecord, ref time.Time, chainStart *string, loc *time.Location) int {
	var start time.Time
	hasStart := false
	if chainStart != nil {
		if s, ok := dateInLoc(*chainStart, loc); ok {
			start, hasStart = s, true
		}
	}
	count := 0
	for cur := DateOf(ref); ; cur = AddDays(cur, -1) {
		if hasStart && cur.Before(DateOf(start)) {
			break
		}
		if rec, ok := records[DateString(cur)]; ok && rec.Completed {
			break
		}
		count++
		if !hasStart {
			break
		}
	}
	return count
}

// survivalFloor returns the floor text the L1 template names — the survival
// link's floor, since on a Red day the survival link *is* the whole chain
// (engine-module.md §chain.json). It falls back to the chain label when the
// survival link is not found.
func survivalFloor(chain ChainConfig) string {
	for _, l := range chain.Links {
		if l.Key == chain.SurvivalLink && l.Floor != "" {
			return l.Floor
		}
	}
	return chain.Label
}
