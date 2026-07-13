// Package templates holds the Engine's fixed send copy (engine-module.md
// §Consent amendment). Every string here is static and pre-committed — the
// bell, the L1 nudge, the L2 witness escalation, the monthly heartbeat, and
// the storm variants. No template is ever composed by a model; this package
// imports only the standard library and the Engine's pure types, so the "no
// LLM in the tripwire path" guarantee holds by construction (a guard test
// asserts it). The scheduler renders an [engine.Send] through [Render]; the
// Engine core stays free of any rendering so it never touches this text.
package templates

import (
	"fmt"

	"github.com/mrz1836/lucid/internal/engine"
)

// SignOff is the fixed final line on L1, L2, and both storm variants
// (engine-module.md §Consent amendment "Self-identification"): the escalation
// points at something the user wrote into force at Day 0. The bell prompt and
// the heartbeat carry no sign-off — one names a chain, the other reports
// status; neither stings.
const SignOff = "— the form letter, pre-committed at Day 0."

// Bell renders the evening bell prompt naming the chain (engine-module.md
// §The tripwire: "post the chain label at the active profile's bell_time").
// No sign-off.
func Bell(label string) string {
	return fmt.Sprintf("Bell: %s. Two minutes — /closeout when the chain has run.", label)
}

// L1 renders the morning nudge after a single miss (engine-module.md §Consent
// amendment): last night was a miss, never miss twice, tonight is a must, the
// floor named.
func L1(floor string) string {
	return withSignOff(fmt.Sprintf(
		"last night was a miss. Never miss twice — tonight is a must. The floor: %s.", floor,
	))
}

// L1Storm renders the storm variant of L1 (engine-module.md §Consent
// amendment, verbatim): contact continues, coercion pauses.
func L1Storm(floor string) string {
	return withSignOff(fmt.Sprintf(
		"last night was a miss — storm standing, nothing is owed. If tonight allows it, the floor: %s.", floor,
	))
}

// L2 renders the witness escalation after two consecutive misses
// (engine-module.md §Consent amendment): streak, declared mode, and storm
// state only — never journal content, capacity values, or any Mirror data.
func L2(streak int, mode engine.Mode) string {
	return withSignOff(fmt.Sprintf(
		"two missed nights. Streak: %d. Declared mode: %s. Storm: none.", streak, mode,
	))
}

// L2Storm renders the storm variant of L2 (engine-module.md §Consent
// amendment, verbatim): the stake is stayed; the ask-once still applies.
func L2Storm(confirmedDate string) string {
	return withSignOff(fmt.Sprintf(
		"two missed nights — storm standing (confirmed %s). The stake is stayed; the ask-once still applies.", confirmedDate,
	))
}

// L2Blocked renders the user-channel note when the L2 stage is reached but the
// witness contract is not armed (unconfirmed or lapsed) — the ladder degrades
// to L1-only, and the user is told the witness escalation was withheld
// (engine-module.md §The tripwire, §witness.json "Lifecycle").
func L2Blocked(storm bool) string {
	if storm {
		return "two missed nights — storm standing. L2 is disarmed (witness not confirmed); the stake is stayed."
	}
	return "two missed nights — L2 is disarmed (witness not confirmed). You owe the conversation."
}

// L2Unreachable renders the fallback note when the witness escalation fired but
// its channel could not be reached (engine-module.md §Error states, verbatim
// user message).
func L2Unreachable(witness string) string {
	return fmt.Sprintf("L2 fired but couldn't reach %s — you owe the message.", orWitness(witness))
}

// Heartbeat renders the monthly present-state snapshot (engine-module.md
// §Consent amendment) — one of two fixed templates selected by
// escalation_state at send time. Never a month summary; no sign-off.
func Heartbeat(escalationState engine.EscalationState, streak int) string {
	if escalationState == engine.EscalationNone || escalationState == "" {
		return fmt.Sprintf("monthly status: all clear — %d-day streak, no open escalation.", streak)
	}
	return fmt.Sprintf(
		"monthly status: %d-day streak; an escalation fired this month and remains open — you have already seen it.", streak,
	)
}

// StormLapse renders the user-channel note when a storm declaration goes
// unconfirmed past the 72-hour window (engine-module.md §Error states,
// verbatim user message). No new send class — same channel, same consent as
// L1.
func StormLapse(witness string) string {
	return fmt.Sprintf("storm declaration lapsed — no confirmation within 72h. Declare again, or talk to %s.", orWitness(witness))
}

// Render maps a decided [engine.Send] to its fixed template string — the one
// place the tripwire's structured decision becomes user- or witness-facing
// text. The switch is total over the kinds EvaluateTripwire produces; an
// unknown kind renders empty rather than inventing copy.
func Render(s engine.Send) string {
	switch s.Kind {
	case engine.SendL1:
		if s.Storm {
			return L1Storm(s.Floor)
		}
		return L1(s.Floor)
	case engine.SendL2:
		if s.Storm {
			return L2Storm(s.ConfirmedDate)
		}
		return L2(s.Streak, s.Mode)
	case engine.SendL2Blocked:
		return L2Blocked(s.Storm)
	case engine.SendHeartbeat:
		return Heartbeat(s.EscalationState, s.Streak)
	case engine.SendStormLapse:
		return StormLapse(s.WitnessName)
	default:
		return ""
	}
}

// withSignOff appends the pinned sign-off on its own line.
func withSignOff(body string) string { return body + "\n" + SignOff }

// orWitness returns the witness name, or a neutral placeholder when the
// contract has no name yet.
func orWitness(name string) string {
	if name == "" {
		return "your witness"
	}
	return name
}
