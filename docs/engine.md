# Lucid — Behavioral Engine Specification

**Version:** 1.1 · **Date:** 2026-07-02 · **Status:** Canonical
**Scope:** The Engine subsystem defined in [`docs/architecture.md`](architecture.md) §3. This document specifies runtime mechanics, telemetry, accountability, and governance. It contains no instance data; user calibration lives in `personal/instance.md` (template: [`docs/instance-template.md`](instance-template.md)).

**Changelog.** v1.0 was the first extraction from the design dialogue. v1.1 restores mechanisms present in the source designs (`night-chain-design.md`, `wake-architecture.md`) that were dropped in the extraction — the priority order, the restart ritual, the baseline week, the runtime footprint cap, the dispatch definition, the one-day tooling timebox, the failure-mode table, the AI boundary, the excavation block, and the Day-0 gate semantics — and closes four decisions the sources left open: mode-declaration timing, capacity polarity, the Crux queue's ingestion path, and the witness's custody of the stake.

## 1. Definitions

**Chain** — an ordered sequence of Links anchored to a Bell. **Link** — one behavior in a Chain. **Floor** — the minimum version of a Link or Chain that still counts as completion; floor completion equals completion for all adherence math. **Bell** — the external trigger that initiates a Chain; the user never initiates from memory or motivation. **Dispatch** — the routine (initially just the Bell plus a fixed template; eventually an agent) that reads the Charter, the calendar, the declared mode, and the avoided-task queue, and names the day's allocation: today's Crux and the Chain at its declared level. The Dispatch never invents new obligations. **Config** — a one-time or ambient change requiring no ongoing willpower (a staged object, a removed app, a fixed supplement anchor); Config items consume no runtime slot. **Opportunity habit** — a trigger-based, slotless practice executed when its cue naturally occurs; logged as a tally, never quota'd. **Crux** — the day's single most-avoided live task. **Gate** — a scheduled checkpoint (default: days 30/60/90) where Chain additions are earned. **Retro** — the weekly review; the only place runtime parameters change. **Hearing** — a Parked item's scheduled quarterly trial. **Away Mode** — a named, pre-specified travel configuration of a Chain. **Logical day** — calendar day after applying the rollover boundary (default 04:00): events before the boundary attribute to the previous day.

## 2. Runtime

**Priority order (fixed, non-negotiable): chain > record > analysis.** If tooling breaks, a text message to oneself counts as the record and gets backfilled later. Losing a night of data is recoverable; losing the chain is the actual incident (architecture P10).

A Chain executes at its Bell. Each Link's completion cues the next; the Chain compresses to its Floor under load but never skips — floors exist precisely for the worst days. Chains are deliberately scarce: the runtime holds one Chain until the first Gate, and additions are earned, never enthusiasm-driven. **The total daily runtime footprint stays under a fixed cap (default ~30 minutes) plus whatever rides existing Flywheel slots.** Gates may add Links only within the cap; a Link that would exceed it must displace something. This cap is what structurally keeps the runtime tiny — structure at the anchor points, freedom everywhere else.

New Links are wrapped in an existing Flywheel identity wherever possible (a mobility practice belongs to the sport it serves), because identity-wrapped practices bypass the direct-pursuit failure mode.

**Baseline week.** Week 1 of any new Chain is calibration: run the chain, capture everything, change nothing. No parameter changes at the first Retro except lowering a floor. You cannot measure improvement without a baseline.

**Operating modes.** Each day carries a declared mode: **Green** (full Chain plus Crux), **Yellow** (floors and Config only), **Red** (survival floor: the single smallest countable act). Undeclared days default to Green. Adherence is always measured against the declared mode — a Yellow day executed at Yellow scores 100%. Modes make enforcement fair enough to be legitimate: a push that ignores load gets ignored; a push calibrated to declared capacity has standing.

**Mode declaration timing.** A mode may be declared or downgraded at any point up to the day's Bell; after the Bell it is fixed for that logical day. Retroactive declaration or amendment is prohibited — the mode ledger is append-only like everything else. A hard day that went undeclared is exactly what the isolated-miss budget exists for; the Retro may annotate context on a miss but never rescores it. This asymmetry (easy to declare honestly beforehand, impossible to renegotiate afterward) is what keeps modes from becoming the escape hatch.

**Capacity signal.** One digit (1–5) captured nightly — what did today's body and mind allow — with an optional one-word limiter tag. **Polarity: 1 = nothing available, 5 = fully resourced.** Anchors: 1 — survival only; 2 — floors were a reach; 3 — ordinary; 4 — resourced; 5 — surplus. Tags may be opaque labels; the analyst needs correlations, not diagnoses. The capacity series with mode declarations is exportable as longitudinal context for professional care.

**The Crux protocol.** Each day, the Dispatch names exactly one Crux and it receives ten minutes of mandatory contact in a fixed slot placed inside the user's peak-energy window — contact, not completion; the obligation is to touch the avoided thing, and finisher dynamics handle the rest more often than predicted. The avoided-task queue carries a WIP limit (default 3). **Queue ingestion:** items enter the queue only by explicit user nomination, which is an Engine event. The Mirror may notice avoidance-shaped material in reflections and offer it as a candidate through the resonance gate — but on acceptance it is the user who places the item in the queue; the Engine reads only the queue and never Mirror content (architecture P3). The weekly Retro runs the avoidance sort on every queue item: **Crux or retire** — some avoided items are growth edges deserving contact; others are misaligned obligations to be formally retired with a written reason. Avoidance is data; the sort is the skill.

## 3. Telemetry

**Tier 1 — Active capture.** The nightly close-out: link completions, mode, capacity digit and tag, and a journal body with a floor of one line (spoken or typed). Hard budget: two minutes. The Tier 1 schema is frozen between Retros; any field addition must displace something. The close-out is self-serve — completable with no AI in the loop (architecture P9); its journal body flows to the Mirror as an ordinary capture, where the sanctuary rule applies to every word of it.

**Tier 2 — Passive capture.** Unbounded and free: timestamps, entry-time series, screen and pickup metrics where the platform provides them (the objective ground truth for the phone habit, not memory of it), device charge events, wearable data if a device is already owned (never buy gear for telemetry), calendar covariates, journal length. No passive source may ever require nightly user effort.

**Tier 3 — Derived.** Streak mathematics, rolling adherence per Link, completion-time drift, floor-day ratio, miss clustering by weekday and covariate, error-budget burn. Drift, rising floor ratio, and shrinking entries are leading indicators — canaries reviewed at Retro before they become misses.

## 4. Accountability

**SLO.** Zero consecutive misses (hard), and an isolated-miss budget per 30 days (default 4). A miss followed by a completed night is budget spend, not failure. Two consecutive misses is a breach.

**Escalation ladder.** **L0** — ambient state on a surface the user cannot avoid (prompt, status line, lock screen, physical marker at the point of performance). **L1** — automated private nudge the morning after a single miss. Fixed content: last night was a miss; never miss twice; tonight is a must; the floor version, named explicitly. **L2** — automatic escalation to the Witness after two consecutive misses, via dead-man semantics: the absence of expected completion events fires it; self-report is never the trigger. **L3** — the pre-committed stake, defined in the Charter while strong, executed mechanically on breach or a blown monthly budget, without renegotiation. Each level exists so the level above rarely fires.

**Restart ritual (after any miss or breach).** The floor chain, that night, no makeup work. Nothing is added, nothing is owed, nothing is compensated — the what-the-hell effect is absorbed by making re-entry the smallest possible act. After an L3 breach the stake executes, the restart ritual still applies the same night, gate clocks continue from the original chain start, and the error budget refills on its normal 30-day window. A breach costs the stake; it does not reset the system.

**Tripwire automation.** L1 and L2 must be wired, not intended: escalation runs on schedule without the user's mood participating. Tooling for the tripwire (and everything else in Phase 0) carries a hard **one-day build timebox** — the tool becoming the project is a named failure mode (§6). A system with an unwired escalation path is a suggestion.

**Witness contract.** Witness-facing views expose streak, declared mode, and escalation state only — computed exclusively from Engine events. Journal content, capacity payloads, and Profile data are never visible at this layer (architecture P3). The witness is briefed once, at Day 0, with exactly one job: when L2 fires, ask about it, once, in person or by message. **The witness holds the written stake** — they are shown it at the briefing and confirm it, because a stake the witness has never seen cannot be executed without renegotiation. The briefing includes one backstop: *total silence — no escalation and no periodic all-clear for a full month — is itself the signal to ask once.* This is the dead-man switch for the dead-man switch: if the tripwire tooling silently dies, the system still has exactly one human check it cannot lose.

**Stake criteria.** Concrete, mildly painful, and mechanically executable: a payment to a cause the user dislikes, a specific owed obligation — decided while motivated, written into the Charter, triggered without discussion. A stake that requires deliberation at execution time is not a stake.

## 5. Governance

**Weekly Retro.** Twenty minutes, fixed slot. Inputs: the week's three telemetry tiers, plus one optional **excavation block** — a single historical entry (a formative moment, an old pattern, a piece of relationship history) captured to the Ledger with its correct `occurred_at`, feeding the Mirror's recontextualization. Output: at most one change, selected by pre-committed rules — adherence under 5/7 → simplify (lower a floor, move the Bell earlier, remove friction), never intensify; fourteen consecutive clean days with stable timing → eligible to ratchet one parameter or hold; entry-time drift beyond 45 minutes across the week → move the Bell to match reality; a canary trip without a miss → environmental fix only. One change per iteration keeps week-over-week comparison causally readable. Baseline week (§2) permits simplification only.

**Gates.** At days 30/60/90 from Chain start: rolling adherence at or above the threshold (default 85%) with stable timing earns the right to attach one new Link or activate one Parked item; below threshold, the gate's only output is simplification. Additions must fit inside the runtime footprint cap (§2). Physical-loading Links additionally require any registered clinical gates in the instance configuration to be satisfied. Day 90 is a full postmortem: what graduates to permanent, what gets dropped, what the next 90 looks like.

**Quarterly.** Charter review with reasoned amendments; Hearings for every Parked item; framework-stack changes; stake review.

**Away Mode.** Defined before travel, as configuration: which Links compress, what the Floor becomes, which capture surface substitutes. Floor nights in Away Mode count as completions; the error budget remains active. Travel is an operating mode, not an exception negotiated in the moment.

**Day 0 — definition of done.** The program starts when, and only when, all of the following exist: the Bell is set with the Chain named in its label; the environment is staged (each Link's point of performance prepared); the witness is briefed and has confirmed; the stake is written and shared with the witness; the Ledger location is chosen; the off-limits registry is drafted (it may be empty; it must be chosen); Away Mode for any known travel is acknowledged; and the first weekly Retro and first quarterly Hearing are on the calendar. Total setup is timeboxed to one sitting for ignition (the Bell and the environment — five minutes, tonight) and one weekend for the rest. None of it is optional: an unwired escalation path is a suggestion, and suggestions have a documented track record.

## 6. Failure modes and mitigations

| Failure mode | Mitigation |
|---|---|
| Never actually starts (ignition failure) | Bells, staged environment, floors — starting is the designed-for case, not the assumed one. |
| The novelty cliff (~day 12) | Floors plus the L1 tripwire; the chain compresses, it never skips. |
| The what-the-hell effect after a miss | Never-miss-twice plus the restart ritual: floor chain, that night, no makeup work. |
| Protocol thrash | Frozen Tier 1 schema; one change per Retro; baseline week. |
| Measurement fatigue | Passive-heavy telemetry; two-minute active budget; fields must displace to enter. |
| The tool becoming the project | One-day tooling timebox; chain > record > analysis; the cathedral clause (architecture §8, Phase 2). |
| Modes becoming the escape hatch | Declaration fixed at the Bell; no retroactive amendment; misses absorbed by budget, not renegotiation. |
| Travel and schedule chaos | Away Mode as pre-specced configuration; the rollover boundary for late nights. |

## 7. The AI layer

AI is the analyst, never the database (architecture P6, P9). The Ledger is a durable artifact the user owns; models read excerpts, never solely hold the corpus. Sanctioned uses: the weekly Retro copilot (miss-clustering, drift detection, covariate analysis, and a pressure-test of the one proposed change), deep analysis at gates, automation of the L1/L2 tripwire, and — on the Mirror side, at the user's invitation — reflection over the journal stream under the sanctuary rule. What AI is not for: being a dependency of the daily chain, motivation, or being the place the data lives.

## 8. Defaults

Rollover boundary 04:00 · runtime footprint cap ~30 min/day · Tier 1 budget 2 minutes · Crux contact 10 minutes · avoided-queue WIP 3 · isolated-miss budget 4 per 30 days · gate threshold 85% · gate cadence 30/60/90 days · Retro weekly (20 min), Hearings quarterly · capacity polarity 1=depleted, 5=resourced · tooling timebox 1 day. All defaults are instance-overridable; overrides are recorded with reasons per architecture P8.
