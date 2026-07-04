# Lucid — The Scientist (self-experimentation & learning layer)

**Date:** 2026-07-03 · **Status:** Canonical — a living concept, evolving with the project
**Scope:** The experiment lifecycle over the existing foundation:
pre-registered self-experiments, the `experiments/` registry, the
`hypothesis` and `verdict` event kinds, verdict cards, and the
cross-user learning channels. This document contains no instance data;
which protocols a user enables lives in their calibration
([`calibration.md`](calibration.md)).

**Provenance.** Designed from the ninety-day simulation study's
methodologist analysis
([`research/2026-07-03-ninety-day-simulation.md`](research/2026-07-03-ninety-day-simulation.md)
§9). The motivation is empirical: every simulated persona ran feral
experiments anyway — untooled self-experimentation happens wherever
tooled science is impossible. Tooled science can be governed; feral
science cannot.

## 0. The governing rule

**Experiments never grow teeth. Results never appear on daily
surfaces.** The first sentence extends the sanctuary rule
(architecture P3): declining an experiment, deviating from one, or
stopping one is never a miss, never escalates, never reaches the
witness. The second extends obliquity (architecture P4): interim
results are computed and never surfaced; the numbers appear exactly
once, in the Gate verdict. Everything below is these two sentences
applied.

## 1. Position in the foundation

**The Scientist is a stance and a lifecycle, not a subsystem.**
[`architecture.md`](architecture.md) §4b's prohibition on new
subsystems stands untouched. The
entire layer assembles from the four sanctioned extension axes: **one
registry** (`experiments/`, following the `threads/` pattern), **two
event kinds** (`hypothesis`, `verdict`) inside the frozen envelope,
and **deterministic projections** (protocol reports, verdict cards).
Its cadence already exists: **pre-register at a Retro, switch arms
only at a Retro (the one-change rule gives block designs for free),
deliver verdicts at a Gate, adopt only through the resonance gate.**
The weekly Retro is already a lab meeting; this gives it a lab
notebook.

| Part | Holds | This layer adds |
|------|-------|-----------------|
| **Ledger** (events, append-only, bitemporal) | What happened | Two event *kinds*: `hypothesis`, `verdict` |
| **Registries** (long-lived referents; `people/` is the precedent) | What things are | Experiments (`experiments/`, following `threads/`) |
| **Projections** (rebuildable views) | What it means | Protocol reports, verdict cards |

Nothing else is added: no new agent, no new command in the MVP, no new
send path, and zero autonomous messages. Verdict cards are
hand-released projections under render → review → release → record,
exactly like the clinician packet
([`observations.md`](observations.md) §7); the message ceiling is
untouched.

## 2. The lifecycle

An experiment is registered at a Retro, runs in Retro-to-Retro blocks,
freezes during storms, and delivers its verdict at a Gate — where the
verdict enters the resonance gate as a proposal like any other
inference:

```
  Retro          register: hypothesis event + experiments/ record —
                 statement, arms, primary measure, decision rule,
                 stop rules, written while strong, before data
      │
      ▼
  blocks run     Retro-to-Retro (default 1 week); arm switches happen
  (ABAB, ≥ 4)    at a Retro and only there — the one-change rule,
      │          reused as the block boundary
      │
      ├── storm state ──►  freeze: no arm switch, no verdict
      │                    delivery; frozen blocks are censored
      ▼
  Gate           deterministic script computes the verdict per the
                 pre-registered decision rule → verdict event
      │
      ▼
  resonance      the verdict is a proposal — accepted, edited, or
  gate           rejected; rejections retained as events
      │
      ▼
  next Retro     on acceptance, the user adopts the winning arm as an
                 ordinary recorded parameter change — or retires the
                 experiment; the primary measure retires either way
```

Lifecycle semantics, binding:

* **Registration is at a Retro, before data.** The hypothesis event
  (§3) and the registry record (§4) are written in the same sitting;
  block one starts at that Retro. Pre-registration is consent (§5).
* **Equipoise by construction.** Every arm must be a configuration the
  Retro could legally adopt on its own — a bell time, a floor size, a
  lens choice. If an arm would need a Gate, a waiver, or new teeth to
  adopt, it cannot be an arm. This is what makes an experiment safe by
  default: whichever arm is running, the user is living under an
  ordinary, legal configuration.
* **Blocks are Retro-to-Retro.** The weekly one-change rule
  ([`engine.md`](engine.md) §5) is the switching mechanism, not a
  parallel process: the one
  change at a Retro inside an open experiment is the pre-registered
  arm switch. Minimum four blocks (ABAB), so no single week's weather
  decides anything.
* **Storms freeze, never fail.** When a storm trigger holds (§9), arm
  switches and verdict deliveries halt; the block is **censored, never
  counted**. The experiment resumes at the next clean Retro.
* **The deviation rule.** A block in which the user deviated from the
  assigned arm — declared or discovered — is censored or annotated,
  never a failure. There is no adherence math over experiments; a
  deviated block is missing data, and missing data is recorded as
  missing (P3).
* **Verdicts are computed, then proposed.** The verdict is computed by
  deterministic script per the pre-registered decision rule (P9) and
  delivered at a Gate. It enters the resonance gate as a proposal; on
  acceptance the user makes the change at the next Retro as an
  ordinary recorded parameter change (P8). **The system never quietly
  becomes the winning arm.** Underpowered and invalidated are
  first-class verdicts, and nulls are kept.

## 3. Event kinds

Both kinds live inside the frozen envelope
([`observations.md`](observations.md) §2) — no new top-level fields,
appended to the per-logical-day observation files like any other
event. Both carry `source: "experiment"` — a one-word extension of the
envelope's provenance vocabulary (mirrored in
[`observations.md`](observations.md) §2's source list) — and
`refs.experiment` naming the registry record (`experiment_<slug>`).

| Kind | Payload (schema 1) | Notes |
|------|--------------------|-------|
| `hypothesis` | `statement`, `arms[]` (each `{id, config}`), `primary_measure`, `decision_rule`, `stop_rules[]`, `planned_blocks`, `note?` | Each arm's `config` is a legal Retro configuration, in words (equipoise, §2). Written at registration, before data; append-only like everything else — a revised design is a *new* experiment. |
| `verdict` | `verdict` (one of `adopt \| null \| underpowered \| invalidated \| stopped`), `adopted_arm?`, `effect_summary`, `blocks_counted`, `blocks_censored`, `coverage`, `note?` | Computed by deterministic script per the pre-registered decision rule; delivered at a Gate. A stop records *only* the `stopped` verdict (§5). |

One worked example (P-1, bell placement):

```json
{
  "id": "obs_2026_08_14_006",
  "schema": 1,
  "kind": "hypothesis",
  "recorded_at": "2026-08-14T20:10:00-04:00",
  "occurred_at": "2026-08-14T20:10:00-04:00",
  "occurred_at_precision": "exact",
  "occurred_at_end": null,
  "logical_date": "2026-08-14",
  "source": "experiment",
  "payload": {
    "statement": "Moving the Bell from 21:30 to 21:00 reduces misses and backfills.",
    "arms": [
      { "id": "bell-a", "config": "Bell at 21:30 (current)" },
      { "id": "bell-b", "config": "Bell at 21:00" }
    ],
    "primary_measure": "miss count + backfill count per block",
    "decision_rule": "with m(X) = misses + backfills in a counted block of arm X, adopt bell-b iff m(B1) < m(A1) and m(B2) < m(A2), mirror case adopts bell-a; any tie or split → null",
    "stop_rules": ["a schedule change that makes either arm unliveable", "the user says stop"],
    "planned_blocks": 4
  },
  "tags": [],
  "refs": { "experiment": "experiment_bell-2100" }
}
```

Experiment events live in the observations tree and the registries —
already agent-invisible and witness-invisible under the existing
cross-tree denylist (§8). No new enforcement is needed; the walls were
built before this layer arrived.

## 4. The experiments registry

`experiments/` generalizes the registry pattern
([`observations.md`](observations.md) §8) exactly as `threads/` does:
long-lived referents with low-signal keys, merge-updated via the
storage adapter, referenced from events via `refs`.

An `experiment_<slug>` record carries:

* **name** — human-readable, owner-chosen.
* **protocol id + version** — which protocol (§6) this experiment
  instantiates, pinned at registration (e.g., `P-1 v1`).
* **arms** — the registered arm list, mirroring the hypothesis event.
* **status** — `registered | running | frozen | closed`, with an
  append-only `status_history[]` (the insights precedent —
  transitions are recorded, never overwritten). `frozen` is the storm
  state (§2); `closed` is reached only through a verdict event,
  including `stopped`.
* **linked event ids** — the hypothesis event and, once delivered, the
  verdict event.
* **forbidden-domain check record** — one line: what was checked
  against the calibration's forbidden-domains list and the off-limits
  registry, and when. Registration without this line is not
  registration.

The registry is primary data, not a projection — backup-critical
alongside `raw/` and `observations/`, like every registry.

## 5. Guardrails

* **Pre-registration is consent (P5).** Hypothesis, arms, primary
  measure, decision rule, and stop rules are written while strong,
  before data, append-only. Consent granted while strong is revocable
  while weak: **stopping costs one word** — it records only the
  `stopped` verdict and triggers nothing. No exit interview, no
  penalty, no residue beyond the record that it happened.
* **Equipoise** as defined in §2: every arm is a configuration the
  Retro could legally adopt on its own.
* **Blinding by boredom (P4).** Interim results are computed and never
  surfaced — it kills both peeking and outcome-metric leakage in one
  move. Results appear only in the Gate verdict. There is no dashboard
  to watch, which is the point.
* **Goodhart defenses.** The primary measure is **frozen at
  registration and retired at verdict** — a measure that has decided
  something never becomes a standing target. **Prefer exhaust over
  testimony**: timestamps, miss counts, backfill counts — not
  self-ratings — wherever an exhaust measure exists. **Mode-relative
  adherence percentage is disqualified** as a primary measure: the
  spec itself makes it unfalsifiable — floors and modes exist to
  absorb variance, so it cannot separate arms. And every protocol
  carries **pre-registered gaming checks**, so discovering gaming is a
  finding, not an accusation.
* **Reactivity honesty.** Every protocol carries the reactivity line:
  *measurement itself may be the mechanism — in N-of-1, placebo is a
  treatment you may keep.* A result that evaporates when you stop
  measuring was still a result while you measured; the user decides
  whether that is a bug or the treatment.

**When the Scientist must not run** — binding, checked at registration
and standing thereafter:

* **Never during collapse.** The storm freeze (§2, triggers in §9)
  halts arm switches and verdict deliveries; frozen blocks are
  censored, never counted.
* **Never on load-bearing supports.** Recovery infrastructure, grief
  practices, anything the user names in calibration as foundation —
  the difference between science and vivisection. This is what the
  forbidden-domain check (§4) checks against.
* **Never on forever-unmeasured outcomes (P4).** What the Charter has
  named forever unmeasured cannot become a primary measure by the
  side door of an experiment.
* **The off-limits registry applies to experiment subjects entirely**
  (architecture §5).
* **No experiments in the first 30 days.** The Scientist waits for
  Gate 1; a chain that has not survived its first month has no
  baseline to experiment against.
* **Consent granted while strong is revocable while weak** — restated
  here because it is the clause the others depend on.

## 6. Protocols

A protocol is a **spec, not data**: a shared, versioned experiment
design in [`protocols/`](protocols/README.md), containing zero
instance data, versioned like any spec (Layer 0, §7). Running an
experiment means instantiating a protocol at a Retro — the protocol
supplies the design; the instance supplies the arms' concrete values
and the record.

The initial four:

* **[P-1 — Bell placement](protocols/P-1-bell-placement.md)** —
  *runnable now.* Does moving the Bell reduce misses and backfills?
  ABAB over two pre-chosen bell times, measured on pure exhaust. The
  v2 variant runs the same design over two definitions of a schedule
  profile, for rotating rosters ([`engine.md`](engine.md) §2).
* **[P-2 — Lens rotation](protocols/P-2-lens-rotation.md)** —
  *blocked* on the frameworks layer shipping (the design is canonical:
  [`frameworks.md`](frameworks.md), four reference definitions
  shipped); its acted-on measure — insight rules and their kept/lapsed
  record ([`mvp/data-model.md`](mvp/data-model.md) §"Validated
  insights") — is specced. Which interpretive lens produces proposals the user
  accepts *and acts on* — honestly framed as preference exploration
  at current insight rates.
* **[P-3 — Correlate confirmation](protocols/P-3-correlate-confirmation.md)**
  — *blocked* on MVP phase 12 (enrichers + exports) and the post-MVP
  correlates projection with its coverage floor
  ([`observations.md`](observations.md) §7). Does an exploratory
  correlate (observations.md §7) survive pre-registration?
* **[P-4 — Floor titration](protocols/P-4-floor-titration.md)** —
  *runnable now.* Is a smaller floor better? A staircase design with a
  pre-committed reversal rule; the null hypothesis is that smaller is
  right.

**Authoring rule:** new protocols are doc diffs following the template
in [`protocols/README.md`](protocols/README.md) — proposed, reviewed,
and versioned like any other spec change
([`mvp/claude-code-workflow.md`](mvp/claude-code-workflow.md)). A
protocol change never touches an experiment already registered: the
registry record pins protocol id *and version* (§4).

## 7. Learning without telemetry

Local-first means the data never leaves; it does not mean the design
cannot learn. Four layers, each crossing the boundary only by a human
act on a reviewed artifact:

* **Layer 0 — protocols are specs.** They live in
  [`protocols/`](protocols/README.md), contain no instance data, and
  are versioned like any spec.
* **Layer 1 — improvements ship *to* instances.** Protocol and default
  improvements arrive as spec releases the user pulls; changed
  defaults are proposed at the next Retro per P8, never silently
  applied.
* **Layer 2 — the verdict card**, released by hand, default never
  (below).
* **Layer 3 — aggregation.** The project aggregates voluntarily
  released cards into sharpened defaults and preconditions in the
  next protocol version.

**The verdict card** is a projection from a closed experiment
containing exactly: protocol id + version, arm ids, blocks
counted/censored, coverage band (quartile bands, e.g. "50–75% of days
logged"), effect direction from a fixed vocabulary (`improved |
no-change | worsened | mixed`), confound tags from a fixed list
defined by the protocol, and the verdict. **Zero raw data, zero dates,
zero free text.** It is released only by deliberate hand under
render → review → release → record — the clinician-packet discipline
([`observations.md`](observations.md) §7) — and the release is logged:
each release appends one line to `projections/exports.log`. The
default is **never-release**; an instance that shares nothing is a
fully functioning instance.

**Gap reports** are the cheapest cross-user channel — a pure spec
question containing no personal data, filed against the shared genome:
`{doc + section, the question, why the spec is ambiguous or silent,
(optional, flagged) what this instance chose meanwhile}`. A gap report
carries no measurements, no dates, no arms — only the discovery that
the spec has a hole. The ninety-day simulation study itself is the
founding example of the genre.

Lucid-the-project gets smarter; Lucid-the-instance stays sovereign,
because every boundary crossing is a human act on a reviewed artifact.

## 8. Boundaries

* **Sanctuary, witness-blindness, agent-blindness — already
  enforced.** Experiment events live in the observations tree and the
  registries, which the existing cross-tree denylist
  ([`mvp/agent-contracts.md`](mvp/agent-contracts.md), cross-cutting
  rules; [`observations.md`](observations.md) §7) already excludes
  from every agent context slice and every witness-facing view. This
  layer adds no new enforcement because it needs none; it inherits
  walls that were load-bearing before it existed.
* **Never diagnosis, never treatment advice.** A verdict is a
  statement about a decision rule over a pre-registered measure —
  data for the user and their care team, never a clinical claim. The
  Safety gate's clinical-language rules apply to every surface this
  layer grows.
* **The off-limits registry** (architecture §5) excludes at every
  layer: subjects (§5), verdict cards, gap reports.
* **No MVP commands.** The Scientist is a manual practice at
  Retro/Gate cadence from Phase 0: the hypothesis is written at a
  Retro, the blocks are lived, the verdict script is run by hand at a
  Gate. Tooling is post-MVP and rides the extension axes when it
  comes ([`mvp/scope.md`](mvp/scope.md) §7) — no new commands, no new
  subsystem, the same envelope.

## 9. Defaults

One experiment at a time · no experiments before Gate 1 (day 30 of
the chain) · block = Retro-to-Retro (default 1 week) · minimum 4
blocks (ABAB) · storm freeze triggers: capacity ≤ 2 for 3 consecutive
days · any open escalation (`l2_fired` or `stake_owed`) · a standing
storm ([`engine.md`](engine.md) §4 — the Engine's witness-confirmed
incapacity state, which subsumes Charter-named ambush windows) —
frozen blocks are censored, never counted · primary measure frozen at registration and
retired at verdict (anti-Goodhart) · mode-relative adherence
percentage disqualified as a primary measure · prefer exhaust over
testimony (timestamps, miss counts, backfill counts — not
self-ratings — wherever an exhaust measure exists) · interim results
computed, never surfaced · stopping costs one word, records only the
`stopped` verdict, triggers nothing · verdict cards default
never-release; each release appends to `projections/exports.log` ·
every protocol carries the reactivity line: *measurement itself may
be the mechanism — in N-of-1, placebo is a treatment you may keep.*
All instance-overridable with reasons (P8).
