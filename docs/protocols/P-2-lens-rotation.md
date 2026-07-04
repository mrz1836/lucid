# P-2 — Lens rotation (v1)

**Status:** Blocked — on the frameworks layer (see Prerequisites).
The acted-on instrumentation — insight rules and their kept/lapsed
record — is now specced
([`../mvp/data-model.md`](../mvp/data-model.md) §"Validated
insights").

## Question

Which interpretive lens produces proposals the user accepts *and acts
on*? The framework stack ([`../architecture.md`](../architecture.md)
§7) is chosen at calibration on taste; this protocol would let it be
kept on evidence. **Honest framing, binding: at current insight rates
this is preference exploration, not effect estimation.** The
simulation observed roughly 1.4 acted-on insights per user-quarter —
no plausible block structure separates two lenses on that base rate
in a single run. The protocol exists so the exploration is
pre-registered and honest, and so acceptance is never mistaken for
effect: it is **measured on acted-on, never on acceptance** — the
politeness confound is observed, not hypothetical.

## Arms

Two lenses from the user's Charter framework stack, fixed at
registration:

* `lens-a` — the incumbent lens framing Reflection's proposals.
* `lens-b` — the challenger lens.

Each arm is a legal stack configuration the Retro could adopt on its
own (equipoise — [`../scientist.md`](../scientist.md) §5). Which
lenses, as always, is instance data: named in the `hypothesis`
event's `arms[]` (scientist.md §3), never here. No framework
overrides the resonance gate or the sanctuary rule (architecture §7);
an experiment over lenses changes what frames a proposal, never
whether consent governs it.

## Design

Alternating blocks; block = Retro-to-Retro (default), minimum four
counted (scientist.md §9). Insight cadence is slow, so registration
may pre-commit longer blocks — two Retro-to-Retro weeks per block
doubles the per-block count at the cost of calendar time — but the
choice is frozen at registration and never adjusted mid-run. Switch
lenses at a Retro and only there. Interim counts are computed and
never surfaced.

## Primary measure (and disqualified measures)

**Acted-on count per block** — accepted insights that carry a rule
whose `rule_history[]` later records `kept`
([`../mvp/data-model.md`](../mvp/data-model.md) §"Validated
insights"), attributed to the block whose lens produced the proposal.
This is the nearest thing to exhaust the Mirror can offer: a recorded,
revisited intent — not a felt agreement.

**Disqualified:** acceptance rate — users accept proposals to be kind
to the mirror, and a lens that flatters will out-poll a lens that
helps. Also disqualified: proposal volume (it measures the corpus,
not the lens) and mode-relative adherence percentage (disqualified in
every protocol — scientist.md §9).

## Decision rule

Deterministic script, pre-registered (architecture P9). Let a(X) =
acted-on count in a counted block of lens X. With counted blocks
A1 B1 A2 B2:

* **`adopt`** a lens iff its count is strictly higher in *both*
  within-pair comparisons **and** total acted-on across counted
  blocks ≥ 4.
* Total acted-on < 4 → **`underpowered`** — the expected verdict at
  current rates, and recording it is the point: underpowered nulls
  are kept, and they aggregate (scientist.md §7).
* Ties or splits above the minimum → **`null`**.
* A lens changed outside the design, or the rule mechanics'
  semantics changed mid-run → **`invalidated`**.

The verdict is delivered at a Gate, enters the resonance gate as a
proposal, and any stack change lands at the next Retro as an ordinary
recorded change (architecture P8).

## Stop rules

Stopping costs one word and records only the `stopped` verdict
(scientist.md §5). Pre-registered automatic stop: a lens proves
actively wrong for the season it lands in — a framing that grates
during grief is not data to push through. The user may name further
stops at registration.

## Storm handling

The pinned freeze triggers apply (scientist.md §9): capacity ≤ 2 for
three consecutive days · any open escalation (`l2_fired` or
`stake_owed`) · a standing storm ([`../engine.md`](../engine.md)
§4), which subsumes Charter-named ambush windows; frozen blocks are
censored, never counted, and the run extends. One Mirror-specific
censor in addition: a block in which the proposal pause engaged
([`../mvp/agent-contracts.md`](../mvp/agent-contracts.md) §3 — three
consecutive unanswered proposals → 14-day pause) is censored — no
proposals means no measure, and the pause exists for a reason senior
to any experiment.

## Reactivity note

Measurement itself may be the mechanism — in N-of-1, placebo is a
treatment you may keep. Specifically: attending to whether insights
get acted on may make insights get acted on, under either lens. A
rotation that raises acted-on across both arms is a win the verdict
cannot attribute — and does not need to.

## Prerequisites

* **The frameworks layer** — deferred per agent-contracts.md
  §Framework (deferred): per-framework definition files, a router
  seam for selecting one, and the reframing-consent prompt. Until it
  exists there are no lenses to rotate.
* **Acted-on instrumentation** — **shipped**: insight rules with
  their kept/lapsed `rule_history[]`
  ([`../mvp/data-model.md`](../mvp/data-model.md) §"Validated
  insights") — added after the simulation found accepted insights
  near-write-only
  ([`../research/2026-07-03-ninety-day-simulation.md`](../research/2026-07-03-ninety-day-simulation.md),
  finding 15). Acceptance alone remains disqualified above; a run
  needs enough rule activity for the underpowered threshold to be
  beatable at all.
* The universal preconditions (scientist.md §5, §9), including the
  recorded forbidden-domain check (scientist.md §4).

## Verdict template

The card a closed run may release (default: never; each release
appends to `projections/exports.log` — scientist.md §7). Zero raw
data, zero dates, zero free text:

```
protocol:    P-2 v1
arms:        lens-a · lens-b
blocks:      counted <n> · censored <m>
coverage:    <quartile band, e.g. "50–75% of days logged">
effect:      improved | no-change | worsened | mixed
confounds:   subset of [capture-drought, proposal-pause, emotionally-loaded-period, life-event]
verdict:     adopt | null | underpowered | invalidated | stopped
```

On an `adopt` verdict the adopted arm lives in the private
`verdict` event's `adopted_arm?` field (scientist.md §3), not on the
card. The confound-tag list is fixed by this protocol; free-text
confounds belong in the private `verdict` event's `note?`, never on
the card.
