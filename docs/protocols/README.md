# Lucid — Protocols

**Date:** 2026-07-03 · **Status:** Canonical — a living concept, evolving with the project
**Scope:** The protocol library for the Scientist
([`../scientist.md`](../scientist.md)) — Layer 0 of learning without
telemetry (scientist.md §7). Protocols are shared, versioned
experiment specs containing zero instance data; which protocols a
user runs, and with what actual values, lives in their `hypothesis`
events (scientist.md §3) and their calibration
([`../calibration.md`](../calibration.md) §Experiments).

## What a protocol is

A protocol is a spec, not data: the reusable design for one
pre-registered self-experiment — the question, the shape of the arms,
the block structure, the measure, the decision rule, the stop rules,
and how storms are handled. It contains no bell times, no dates, no
results, no names. Arms in a protocol file are placeholders; the real
configuration enters the `hypothesis` event at registration, and the
experiment's registry record pins the protocol id and version it
registered under (scientist.md §4). Because protocols carry no
instance data, they are versioned, shared, and improved exactly like
any other spec in this repository — that is the whole point of
Layer 0: the design travels; the data never does.

Every protocol inherits the guardrails of scientist.md §5 and the
defaults of §9 without restating them: the Scientist enabled in
calibration, one experiment at a time, no experiments before Gate 1,
the forbidden-domain check recorded at registration, interim results
computed and never surfaced, verdicts computed by deterministic
script (architecture P9) and delivered at a Gate into the resonance
gate. Three of those rules are load-bearing enough that every
protocol file states them in its own terms anyway: **equipoise by
construction** (every arm must be a configuration the Retro could
legally adopt on its own), **the pinned storm freeze** (frozen blocks
are censored, never counted), and **the reactivity line** (below).

## The template

Every protocol file is named `P-<n>-<slug>.md` and uses exactly these
sections:

* `# P-<n> — <Name> (v1)` — the title carries the id and version. A
  version that adds a *variant* while the prior version stays valid
  may live as a marked section in the same file (P-1 v2 is the
  precedent); the title then carries both (`(v1, v2)`).
* **Status** — a header line under the title: runnable now, or
  blocked on a named prerequisite.
* **Question** — one falsifiable question, in plain words, with the
  honest framing of what an answer would and would not mean.
* **Arms** — the shape of the configurations compared. Every arm must
  be a legal Retro configuration on its own (equipoise by
  construction, scientist.md §5); actual values are instance data and
  never appear in the file.
* **Design** — block structure, switching cadence, minimums. The
  defaults (scientist.md §9): block = Retro-to-Retro, minimum four
  counted blocks, switches at a Retro and only there.
* **Primary measure (and disqualified measures)** — frozen at
  registration, retired at verdict (anti-Goodhart); prefer exhaust
  over testimony wherever an exhaust measure exists. Mode-relative
  adherence percentage is disqualified as a primary measure in every
  protocol — floors and modes exist to absorb variance, so the spec
  itself makes it unfalsifiable.
* **Decision rule** — deterministic, computable by script from the
  named exhaust, mapping outcomes onto the five verdicts
  (`adopt | null | underpowered | invalidated | stopped`,
  scientist.md §3). Nulls are kept.
* **Stop rules** — pre-registered automatic stops, plus the standing
  rule: stopping costs one word, records only the `stopped` verdict,
  and triggers nothing (scientist.md §5).
* **Storm handling** — the pinned freeze triggers (scientist.md §9,
  including a standing storm — [`../engine.md`](../engine.md) §4) and
  what censoring means for this design.
* **Reactivity note** — every protocol carries the line:
  *measurement itself may be the mechanism — in N-of-1, placebo is a
  treatment you may keep* — plus what reactivity looks like for this
  design specifically.
* **Prerequisites** — what must exist before the protocol is runnable
  as written, honest about blockers, with the universal preconditions
  (scientist.md §5, §9) assumed rather than repeated.
* **Verdict template** — the card a closed run may release (default:
  never — scientist.md §7), including this protocol's fixed
  confound-tag list. Zero raw data, zero dates, zero free text.

A protocol marked **runnable now** must be concrete enough that a
user at a Retro can pre-register from it as written — copy the
question, fill in the arms, freeze the measure, append the
`hypothesis` event — with no further design work.

## Authoring and versioning

A protocol change is a doc diff, like every behavior change in this
project. New protocols are authored against the template above and
land through review like any spec (scientist.md §6). A version bump
notes, in the file, what sharpened and which Layer-3 evidence
motivated it — voluntarily released verdict cards, gap reports
(scientist.md §7) — so the protocol's own provenance is legible.
Versions bind forward only: a registered experiment pins protocol id
and version in its registry record (scientist.md §4) and finishes on
the version it registered under; a bump never retro-applies to a
running experiment. Protocols contain zero instance data at every
version.

## Index

The initial four. (Grounding:
[`../research/2026-07-03-ninety-day-simulation.md`](../research/2026-07-03-ninety-day-simulation.md) §9.)

| Id | Protocol | Question | Status |
|----|----------|----------|--------|
| P-1 | [Bell placement](P-1-bell-placement.md) | Does moving the Bell reduce misses and backfills? | **Runnable now** — v1 for fixed schedules; v2 (the roster-profile variant, same file) for rotating ones, requiring schedule profiles ([`../engine.md`](../engine.md) §2). |
| P-2 | [Lens rotation](P-2-lens-rotation.md) | Which interpretive lens produces proposals the user accepts *and acts on*? | **Blocked** — on the frameworks layer *shipping*; its design is canonical ([`../frameworks.md`](../frameworks.md)) and the acted-on instrumentation — insight rules ([`../mvp/data-model.md`](../mvp/data-model.md)) — is specced. |
| P-3 | [Correlate confirmation](P-3-correlate-confirmation.md) | Does an exploratory correlate survive pre-registration? | **Blocked** — on MVP phase 12 (enrichers + exports) and the post-MVP correlates projection with its coverage floor ([`../observations.md`](../observations.md) §7). |
| P-4 | [Floor titration](P-4-floor-titration.md) | Is a smaller floor better? | **Runnable now.** |
