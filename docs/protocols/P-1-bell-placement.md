# P-1 — Bell placement (v1, v2)

**Status:** Both runnable now. v1 for fixed schedules; v2 (the
roster-profile variant, below) for rotating ones — it requires
schedule profiles configured ([`../engine.md`](../engine.md) §2).

## Question

Does moving the Bell reduce misses and backfills? The Bell is the
chain's ignition ([`../engine.md`](../engine.md) §1), and the weekly
Retro already moves it when entry-time drift says the clock disagrees
with reality (engine.md §5) — but that adjustment is a judgment call
made after the fact. This protocol turns it into a pre-registered
comparison: two candidate bell times, run blind by boredom, decided
by exhaust.

## Arms

Two pre-chosen bell times, fixed at registration:

* `bell-a` — the incumbent bell time.
* `bell-b` — the candidate bell time.

Each arm is a complete, legal Retro configuration on its own — moving
the Bell is a sanctioned Retro output (engine.md §5) — so equipoise
holds by construction ([`../scientist.md`](../scientist.md) §5). The
actual times are instance data: they enter the `hypothesis` event's
`arms[]` at registration (scientist.md §3), never this file.

## Design

ABAB. Block = Retro-to-Retro (default one week); minimum four counted
blocks (scientist.md §9). Register at a Retro by appending the
`hypothesis` event; switch arms at a Retro and only there — the
switch is that week's one change, which is what makes the block
design free (engine.md §5). Interim counts are computed and never
surfaced; results appear only in the Gate verdict (architecture P4).

## Primary measure (and disqualified measures)

**Miss count plus backfill count per block** — pure exhaust: misses
from the day records, backfills from `corrections[]` appends
([`../mvp/engine-module.md`](../mvp/engine-module.md) §Storage
additions). Both are countable since the corrections semantics
landed; no self-rating touches the verdict. Backfills count because
they are the honest trace of a bell that fired at the wrong moment —
the chain ran, the record limped.

**Disqualified:** mode-relative adherence percentage (disqualified in
every protocol — scientist.md §9: floors and modes exist to absorb
variance, so the number cannot separate arms), and any felt-sense
rating of the evening ("smoother," "easier") — testimony where
exhaust exists. Such notes may ride along in `note?`; they never
decide.

## Decision rule

Deterministic script, pre-registered (architecture P9). Let m(X) =
misses + backfills in a counted block of arm X. With counted blocks
A1 B1 A2 B2:

* **`adopt`** an arm iff its count is strictly lower in *both*
  within-pair comparisons — m(B1) < m(A1) and m(B2) < m(A2) adopts
  `bell-b`; the mirror case adopts `bell-a`, keeping the incumbent on
  evidence rather than habit.
* Any tie or split → **`null`**. Nulls are kept.
* Fewer than four counted blocks by the pre-registered end →
  **`underpowered`**.
* A bell change outside the design, or a break in the exhaust
  (corrections unavailable mid-run) → **`invalidated`**.

The verdict is delivered at a Gate and enters the resonance gate as a
proposal; on acceptance the user makes the bell change at the next
Retro as an ordinary recorded parameter change (architecture P8). The
system never quietly becomes the winning arm.

## Stop rules

Stopping costs one word and records only the `stopped` verdict — no
reason owed, nothing escalates, nothing reaches the witness
(scientist.md §5). Pre-registered automatic stop: a schedule change
that makes either arm unliveable (a new fixed obligation on the bell
hour) — the arms are no longer in equipoise, so the run ends rather
than limps. The user may name further stops at registration.

## Storm handling

The pinned freeze triggers apply (scientist.md §9): capacity ≤ 2 for
three consecutive days · any open escalation (`l2_fired` or
`stake_owed`) · a standing storm
([`../engine.md`](../engine.md) §4), which subsumes Charter-named
ambush windows. A frozen block is
**censored, never counted**; the four-block minimum is met by
extending the schedule, never by counting a frozen block. Arm
switches and verdict delivery halt while frozen.

## Reactivity note

Measurement itself may be the mechanism — in N-of-1, placebo is a
treatment you may keep. Specifically: knowing the Bell is on trial
may sharpen response to both arms. If misses drop under both, that is
a finding about attention, not a failure of the design — and it may
be worth keeping.

## Prerequisites

* Countable misses and backfills — the `corrections[]` semantics
  (engine-module.md §Storage additions). **Shipped.**
* A chain past Gate 1 — no experiments in the first 30 days
  (scientist.md §5) — with the weekly Retro cadence live.
* The universal preconditions (scientist.md §5, §9): Scientist
  enabled in calibration, one experiment at a time, the
  forbidden-domain check recorded in the registry record
  (scientist.md §4).

**The roster-profile variant is not v1.** For rotating schedules, a
bell time is not a clock time — v1 assumes fixed weeks. The honest
comparison for a rotating life is between two candidate *definitions*
of the same schedule profile; that is v2, below.

## Verdict template

The card a closed run may release (default: never; each release
appends to `projections/exports.log` — scientist.md §7). Zero raw
data, zero dates, zero free text:

```
protocol:    P-1 v1
arms:        bell-a · bell-b
blocks:      counted <n> · censored <m>
coverage:    <quartile band, e.g. "75–100% of days logged">
effect:      improved | no-change | worsened | mixed
confounds:   subset of [schedule-change, travel, illness-adjacent, season-daylight]
verdict:     adopt | null | underpowered | invalidated | stopped
```

On an `adopt` verdict the adopted arm lives in the private `verdict`
event's `adopted_arm?` field (scientist.md §3), not on the card. The
confound-tag list is fixed by this protocol; free-text confounds
belong in the private `verdict` event's `note?`, never on the card.

## v2 — the roster-profile variant

Everything not restated below inherits v1. Register as `P-1 v2`
(the registry pins protocol id *and* version — scientist.md §4).

* **Question.** For a rotating schedule, do this profile's clocks sit
  in the right place? The comparison is between two candidate
  **definitions of one named profile** (e.g., `nights` with Bell 08:30
  · tripwire 17:00 · rollover 12:00, versus Bell 09:15 · tripwire
  18:00 · rollover 12:30 — any or all three clocks may differ).
* **The roster is never an arm.** Reality is not assignable: the
  experiment never dictates *which* profile is active — `/profile`
  keeps tracking the actual roster — it varies the clocks *inside*
  the named profile. Equipoise holds because a profile definition is
  an ordinary Retro-editable configuration (engine.md §2).
* **Arm switching.** At the block-boundary Retro, the profile's
  definition in `chain.json` is edited to the block's assigned arm —
  the week's one change, as v1.
* **Design.** ABAB by Retro-to-Retro blocks, minimum four counted, as
  v1 — but only days *governed by the named profile* count. A block
  with fewer than **3 governed days** is censored as underexposed and
  the schedule extends, exactly like a storm-censored block.
* **Primary measure.** (misses + backfills) **per governed day** — a
  rate, because exposure varies with the roster. Same exhaust sources
  and the same disqualifications as v1.
* **Decision rule.** v1's within-pair dominance, computed on the rate:
  strictly lower in both within-pair comparisons → `adopt`; tie or
  split → `null`; fewer than four counted blocks → `underpowered`; a
  profile definition changed outside the design → `invalidated`.
* **Pre-registered automatic stop.** Roster collapse: the named
  profile stops occurring (the job change, the rotation ends) — the
  arms have no exposure left to compare.
* **Verdict card.** As v1, with `protocol: P-1 v2` and the same
  confound-tag list plus `roster-change`.
