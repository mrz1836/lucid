# P-3 — Correlate confirmation (v1)

**Status:** Blocked — on MVP phase 12: enrichers plus the correlates
projection (see Prerequisites).

## Question

Does an exploratory correlate survive pre-registration? The
correlates projection ([`../observations.md`](../observations.md) §7)
surfaces candidate associations with honest statistics — but honest
statistics over the data that suggested the hypothesis are still
exploratory. This protocol is the promotion path: a surfaced
correlate becomes a registered hypothesis, then earns or loses belief
only on days it could not have cherry-picked.

## Arms

None in the interventional sense — this protocol changes no
configuration, so equipoise is trivially satisfied: there is nothing
to adopt but a belief, and the verdict proposes an insight, never a
parameter change. What the `hypothesis` event's `arms[]` records
instead are the pre-registered **exposure strata**: exposed days
versus unexposed days, defined exactly and in words ("nights with
sleep quality ≤ 2" versus all other logged nights), frozen at
registration ([`../scientist.md`](../scientist.md) §3). Nothing about
the user's day is asked to change; the lifecycle is
**exploratory correlate → registered hypothesis → evaluation on
post-registration days only.**

## Design

Register at a Retro by copying the correlate exactly as the
projection stated it: the measure pair, the lag, the stratum
definitions, the effect direction and magnitude. Pre-register the
evaluation window (a minimum paired-day count) and the **missingness
handling**: unlogged days are excluded from the pairing and counted
against coverage — never imputed — honoring the coverage floor
(observations.md §7), because logging density is itself correlated
with symptom state, and a correlate computed only over logged days is
biased by exactly the thing under study. Evaluation uses
**post-registration days only**; the exploratory window is
motivation, never evidence. There is no arm switching; blocks exist
only as the censoring unit, and the storm freeze applies day-wise.
Interim results are computed and never surfaced (architecture P4).

## Primary measure (and disqualified measures)

The pre-registered paired measure: one observation series against one
exposure series, both drawn from exhaust and enrichment — a pain
series against prior-night sleep quality, against barometric
pressure, against an intake class (observations.md §7). Frozen at
registration, retired at verdict.

**Disqualified:** any measure introduced after registration (that is
a new exploratory question, not this experiment); testimony where
exhaust exists; and mode-relative adherence percentage (disqualified
in every protocol — scientist.md §9).

## Decision rule

Deterministic recomputation of the same statistic the projection
used, over post-registration paired days only (architecture P9):

* Same direction, magnitude at or above the pre-registered threshold
  (default: half the exploratory estimate), paired days at or above
  the minimum, coverage above the floor → **`adopt`** — meaning the
  correlate is confirmed and enters the resonance gate as a proposed
  insight, in observations-about-data language, never as cause, never
  as advice (observations.md §7). On acceptance it becomes an insight
  with provenance like any other.
* Direction fails, or holds below the magnitude threshold →
  **`null`** — and the correlate is retired, not re-rolled:
  re-registering the same pair requires a new exploratory signal.
* Paired days below the minimum, or coverage below the floor, at
  window end → **`underpowered`**.
* A stratum or measure definition broken mid-window — a regimen
  change that redefines the exposure, an enricher change that
  redefines the series → **`invalidated`**.

## Stop rules

Stopping costs one word and records only the `stopped` verdict
(scientist.md §5). Pre-registered automatic stop: the exposure
becomes something the user is deliberately steering — avoiding the
suspected food, restructuring sleep around the hypothesis. At that
point the run has become an intervention it never registered, and it
stops rather than pretends. The user may name further stops at
registration.

## Storm handling

The pinned freeze triggers apply (scientist.md §9): capacity ≤ 2 for
three consecutive days · any open escalation (`l2_fired` or
`stake_owed`) · a standing storm ([`../engine.md`](../engine.md)
§4), which subsumes Charter-named ambush windows. Frozen days are
censored from the pairing, never counted, and the window extends;
verdict delivery halts while frozen.

## Reactivity note

Measurement itself may be the mechanism — in N-of-1, placebo is a
treatment you may keep. It cuts sharper here than anywhere: naming a
suspected trigger changes exposure to it. A confirmation window in
which exposure quietly collapses is not evidence about the correlate
— it is behavior change, and it may be the better outcome; the stop
rule above exists to record that honestly instead of scoring it.

## Prerequisites

* **MVP phase 12** — enrichers and exports
  ([`../mvp/observations-module.md`](../mvp/observations-module.md)
  §Acceptance criteria, Phase 12), and the correlates projection with
  its coverage floor (observations.md §7). Until the projection
  exists there is nothing to promote.
* **A surfaced exploratory correlate.** Correlates below the coverage
  floor are computed but never surfaced — so they can never be
  registered; the floor gates the pipeline at its mouth.
* The universal preconditions (scientist.md §5, §9), including the
  recorded forbidden-domain check (scientist.md §4) — a correlate
  touching an off-limits kind, tag, or era is not a candidate.

## Verdict template

The card a closed run may release (default: never; each release
appends to `projections/exports.log` — scientist.md §7). Zero raw
data, zero dates, zero free text:

```
protocol:    P-3 v1
arms:        exposed-days · unexposed-days
blocks:      counted <n> · censored <m>
coverage:    <quartile band, e.g. "50–75% of days logged">
effect:      improved | no-change | worsened | mixed
confounds:   subset of [season, coverage-shift, regimen-change, location-change]
verdict:     adopt | null | underpowered | invalidated | stopped
```

On an `adopt` verdict the adopted stratum lives in the private
`verdict` event's `adopted_arm?` field (scientist.md §3), not on the
card. The confound-tag list is fixed by this protocol; free-text
confounds belong in the private `verdict` event's `note?`, never on
the card.
