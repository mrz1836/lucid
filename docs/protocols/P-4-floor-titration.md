# P-4 — Floor titration (v1)

**Status:** Runnable now.

## Question

Is a smaller floor better? Floors are the retention thesis — small
enough to clear at capacity 2, cheap enough that shame cannot outbid
them ([`../engine.md`](../engine.md) §2). A floor is insurance
against the worst day, and like insurance, the premium — its nightly
size — should be the smallest that still pays out, where paying out
means re-entry after a miss. **The null hypothesis is pinned:
smaller is right.** The burden of proof is on the bigger floor.

## Arms

A pre-registered **notch ladder**: the current floor `F0` and at
least two strictly smaller versions `F1`, `F2`, …, each written out
in words at registration. Each notch is a complete floor the Retro
could legally set on its own — lowering a floor is always the legal
direction; even the baseline week permits it (engine.md §2, §5) — so
equipoise holds by construction
([`../scientist.md`](../scientist.md) §5). The ladder's contents are
instance data: they enter the `hypothesis` event's `arms[]`
(scientist.md §3), never this file.

## Design

Staircase, paced by Gates: shrink one notch per Gate, with the
reversal rule pre-committed at registration. Each shrink is a
simplification — a legal Gate output — but the Gate only *decides*
it; the notch changes at the next Retro, as that week's one recorded
parameter change, so arm switches still happen at a Retro and only
there (scientist.md §2, engine.md §5). Blocks remain Retro-to-Retro
for censoring math (scientist.md §9); a Gate-to-Gate span is
evaluated over its counted blocks only. The step cadence is
asymmetric on purpose: a shrink waits for a Gate to be decided; a
reversal waits for no Gate and fires at the next Retro, because a
failing floor should not wait thirty days for permission to grow
back. The run spans at least two shrink steps or one reversal,
whichever comes first, and the pinned four-counted-block minimum
applies to the whole run. Interim results are computed and never
surfaced (architecture P4).

P-4 runs on the live chain: misses inside the experiment are the same
misses the Engine's budget and ladder see. The experiment adds no
teeth and removes none — it only decides what the floor should be.

## Primary measure (and disqualified measures)

**Miss count plus restart latency per span** — restart latency being
the length of consecutive-miss runs, pure exhaust from the day
records: how many nights pass before the floor gets cleared again.
This measures what floors are *for* — not adherence but re-entry.

**Disqualified:** mode-relative adherence percentage (disqualified in
every protocol — scientist.md §9 — and doubly here: shrinking the
floor changes what completion means, so any completion-relative
number compares different rulers across notches). Also disqualified
as a primary: the floor-day ratio — it is a canary (engine.md §3),
watched as a confound, and mechanically sensitive to the very change
under test.

## Decision rule

Deterministic script, pre-registered (architecture P9). At
registration, two bounds are frozen from the pre-experiment baseline
span: a **miss ceiling** (default: baseline span misses + 1) and a
**run ceiling** (default: baseline longest consecutive-miss run, and
never below 2 — a single isolated miss must not end a titration). A
span at notch `Fk` **holds** iff its counted blocks show miss count ≤
the miss ceiling and longest run ≤ the run ceiling. Then:

* Span holds → shrink one notch, decided at the Gate and enacted at
  the next Retro.
* Span fails either bound → **reversal**: revert to `Fk−1` at the
  next Retro; the run closes with **`adopt`: `Fk−1`** — the smallest
  floor that held.
* Ladder exhausted with every span holding → **`adopt`** the smallest
  notch. The null was right.
* Run ended before two full spans were counted → **`underpowered`** ·
  a floor changed outside the staircase → **`invalidated`** · one
  word → **`stopped`**.

The verdict is delivered at a Gate and enters the resonance gate as a
proposal; the adopted notch is confirmed at the next Retro as an
ordinary recorded parameter change (architecture P8) — including the
reversal case, where the verdict ratifies a revert that safety
already made.

## Stop rules

Stopping costs one word, records only the `stopped` verdict, and
triggers nothing (scientist.md §5) — the floor in effect simply
stays, and changing it back is one Retro change away, owed to no one.
Pre-registered automatic stop: a breach (two consecutive misses) at a
shrunk notch triggers the reversal rule immediately, regardless of
the span bounds. The restart ritual runs that night exactly as always
— floor chain, nothing added, nothing owed (engine.md §4) — and the
experiment never modifies what the Engine does about the breach.

## Storm handling

The pinned freeze triggers apply (scientist.md §9): capacity ≤ 2 for
three consecutive days · any open escalation (`l2_fired` or
`stake_owed`) · a standing storm ([`../engine.md`](../engine.md)
§4), which subsumes Charter-named ambush windows. Frozen blocks are
censored, never counted; spans extend. One boundary deserves stating:
a notch that produces an open escalation freezes the run, and the
counted blocks before the freeze are what the reversal rule fires on
at re-entry. A storm is never evidence against a notch — censored
means censored, even when the timing looks suspicious; the suspicion
belongs in the confound tags, not the count.

## Reactivity note

Measurement itself may be the mechanism — in N-of-1, placebo is a
treatment you may keep. A floor under trial is a floor being watched,
and the watching may be what keeps it cleared. If the smallest notch
holds partly because the experiment is running, the honest options
are to keep the notch and keep paying attention, or to re-run after
the novelty fades — both are legitimate, and the verdict card cannot
tell them apart.

## Prerequisites

* Day records with countable misses and consecutive-miss runs
  ([`../mvp/engine-module.md`](../mvp/engine-module.md)). **Shipped.**
* A chain past Gate 1 with Gates on the calendar — the staircase
  steps at Gates, so the no-experiments-before-Gate-1 rule
  (scientist.md §5) is satisfied by construction.
* A floor that decomposes into at least two smaller notches nameable
  in words. A floor already at one line, one circle, one goodnight
  cannot titrate — that is a finding about the floor, not a blocked
  experiment.
* **Never on load-bearing supports.** A floor that is recovery
  infrastructure, a grief practice, or anything named as foundation
  in calibration is off the table entirely (scientist.md §5 — the
  difference between science and vivisection); the forbidden-domain
  check is recorded in the registry record (scientist.md §4).

## Verdict template

The card a closed run may release (default: never; each release
appends to `projections/exports.log` — scientist.md §7). Zero raw
data, zero dates, zero free text:

```
protocol:    P-4 v1
arms:        f0 · f1 · f2 …
blocks:      counted <n> · censored <m>
coverage:    <quartile band, e.g. "75–100% of days logged">
effect:      improved | no-change | worsened | mixed
confounds:   subset of [travel, schedule-change, illness-adjacent, season]
verdict:     adopt | null | underpowered | invalidated | stopped
```

On an `adopt` verdict the adopted notch lives in the private
`verdict` event's `adopted_arm?` field (scientist.md §3), not on the
card. The confound-tag list is fixed by this protocol; free-text
confounds belong in the private `verdict` event's `note?`, never on
the card.
