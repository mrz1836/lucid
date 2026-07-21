# ADR-0009 — Workout companion: a config-gated, deterministic-core Mirror surface

**Status:** Accepted.

## Context

Two standing constraints in this doc set pull against a daily workout
surface. [`../mvp/product-principles.md`](../mvp/product-principles.md) §1
**defers the Coach** — goals, progress narrative, and conversational
"next actions" are out of scope for the MVP. And
[`../observations.md`](../observations.md) §0 holds the body layer as
**inventory, never obligation** — no observation kind may carry a streak,
target, or score. A naive "workout coach" would violate both: it would
grow a voice that pushes actions, and it would grade the body.

But there is a real, in-scope need: a person building a daily training
habit wants the right workout handed to them safely — one that respects
recovery, does not repeat a high-load body part without a rest window,
and backs off on a pain signal — without a second daily decision to make.

The [daily companion](../usage/companion.md) already resolved the same
tension for the morning/night briefing. It is a **config-gated,
off-by-default, model-allowed** Mirror surface with a **deterministic
core** and **LLM-phrased delivery**, sitting beside the Engine teeth
without touching them: the deterministic verdict is the Engine's, the
model only writes warmth, and nothing it does scores content (P3). That
precedent is the shape a workout surface needs.

## Decision

Lucid adds **one config-gated companion-class workout surface**, of
exactly the same class as the daily companion. Full spec:
[`../mvp/workout-module.md`](../mvp/workout-module.md).

* **The decision is deterministic; the model only phrases it.** A pure
  `Recommend` core picks and vetoes today's workout (rotation table,
  per-body-part recovery windows, pain-flag hard stops, injury and
  guardrail constraints) from a program plus the recent Ledger. The model
  is handed an *already-decided* plan and writes only the prose; it never
  changes the pick. The surface completes with every model down (P9).
* **Off by default.** A `workout` block in `lucid.json` gates the whole
  feature. A fresh Ledger runs the existing teeth and companion unchanged
  until an operator opts in, exactly like the companion block.
* **Observations stay inventory.** Two new capturable kinds — `workout`
  and `body_state` — carry no streak, target, or score; they are
  enable-gated and off by default. The Engine never reads them.
* **The streak is the Engine chain's, not a score on an event.** The
  adherence/streak the surface shows is read from the Engine fold
  ([`../mvp/engine-module.md`](../mvp/engine-module.md) `metrics` /
  `status.json`); the progress view is a **read-only projection**, never
  written back onto an event.
* **The LLM reads only a bounded slice.** Extraction sees the spoken drop
  only (no Ledger read); phrasing sees a bounded recent workout/body-state
  slice plus the program — the companion's own sanctuary reach, never the
  full history.
* **Generic schema in the repo, personal program on an opaque path.** The
  `Program` schema, the recommender, the projection, and a **synthetic**
  example program are OSS; the real program — injuries, recovery windows,
  rehab guardrails, week-by-week numbers — is operator config on an opaque
  path named in `lucid.json`, read directly (no dir-walk), never committed.

This **revises the "Coach deferred" note** in
[`../mvp/product-principles.md`](../mvp/product-principles.md) §1 narrowly:
a config-gated companion-class workout surface exists — with a
deterministic core, off by default, and no voice of its own beyond the
model phrasing the companion already sanctioned. It is not the Coach: it
has no goal trees, no progress celebration, no accountability voice, and
no teeth. A workout practice that earns teeth still becomes an Engine link
through a Gate.

## Consequences

* The Coach seam stays deferred *as a voice*; what ships is a deterministic
  recommender the model dresses — the same class boundary the companion
  drew, now reused rather than reinvented.
* Observations remain sanctuary: the two new kinds are inventory, the
  streak lives on the Engine chain, and the trend is a disposable
  projection. The purity boundary (P3) is untouched in both directions —
  no agent reads the new kinds; the recommender reads no reflective
  content.
* The runtime never depends on AI (P9): the deterministic core owns the
  pick and the render, so a provider outage costs only warmth, never the
  recommendation or the safety line.
* The public-safe boundary gains an enforcement point: the generic schema
  is shareable spec (it may name the schema, the kinds, and synthetic
  examples), while the personal program is operator config on an opaque
  path — the repo stays synthetic-only, enforced by `lucid validate`
  ([`../mvp/scope.md`](../mvp/scope.md) §8 S-7).
* If a second personalized surface ever needs the same shape (a nutrition
  companion, a reading companion), it wraps the same class — config-gated,
  deterministic core, bounded model phrasing, opaque personal config — and
  lands as its own module beside this one. The core stays surface-agnostic.
