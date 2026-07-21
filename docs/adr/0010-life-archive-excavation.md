# ADR-0010 — Life-archive excavation: build the deferred Mirror surface on the frozen foundation

**Status:** Accepted.

## Context

[`../observations.md`](../observations.md) §8 froze an event envelope
and a registry model explicitly so that memory excavation would be "an
ordinary write" and named "era-guided excavation sessions" as a future
Mirror surface that "needs no new foundation." The observations module
([`../mvp/observations-module.md`](../mvp/observations-module.md))
deferred "excavation sessions or thread views" with the same reasoning:
"the events and registries they need exist now; the surfaces come after
the steel thread proves out."

The foundation held. Verified against the current source: the `injury`,
`era`, `thread`, and `place` registries exist with append-only
`status_history` and a free-form `Fields` map; the `memory` observation
kind exists with a `certainty` field; the bitemporal envelope
(`occurred_at`/precision, `source: excavation`) and the backdate grammar
exist; `lucid attach` persists media and emits a linked raw entry; and
the storage adapter's `UpdateRegistry`/`ReadRegistry` ops apply
append-only merges. **What is missing is not data model — it is
surface.** Today the only user-facing verb that writes a registry is the
sticky-location path that auto-creates a `place`; there is no
`lucid injury`/`era`/`thread` write command, and there is no surface
that selects the next thing to excavate or browses what has been
captured.

Two shapes were possible: build the excavation as a live, in-binary
conversational loop, or build the deterministic capture + read surfaces
in the binary and leave the conversation to a harness. The daily
companion and the weekly reflection deep-dive already established the
second shape (deterministic analysis in the binary; the sit-down driven
by a harness), and it keeps the corpus machinery public-safe while
reserving the personal conversation for the operator's own runtime.

## Decision

Ship the life-archive excavation as **capture verbs + read surfaces on
the frozen foundation**, with no envelope change and no conversational
loop in the binary. The full field convention is
[`../mvp/life-archive.md`](../mvp/life-archive.md); the decisions this
record fixes:

* **The surfaces ship on the frozen foundation.** No new top-level
  envelope field, no new registry mechanism. The injury `Fields`
  convention, the story `memory` payload/`refs` convention, and the
  `era`/`thread` `Fields` conventions are documented vocabularies on the
  existing free-form maps — testable, but not schema.
* **The registry-write verbs fill the real CLI gap.** `lucid injury`,
  `lucid era`, and `lucid thread` create and amend their registries
  through the existing append-only `UpdateRegistry` path; `lucid memory`
  captures a backdated story through the existing
  capture → build-event → append triple. Story media reuses
  `lucid attach` unchanged.
* **Determinism owns selection and capture; the model stays out of the
  binary.** The cluster-selection engine, its prompt templates, the
  injury-context projection, and the recall/browse surface are all
  deterministic, read-only where they read, and agent-free (P9). The
  only model use is the personal review conversation a harness drives on
  top of these verbs — outside this repo. The binary gains no
  scheduler change: the monthly cadence lives in the harness, exactly as
  the companion's fire times live in the scheduler.
* **Observations and registries stay inventory, and stay sanctuary.**
  No captured field is a score, streak, or target; the `thread` verb
  keeps the structural obliquity guard (no progress number). The
  selection, projection, and browse surfaces read the sanctuary trees
  only through router projection seams, never raw — the same
  bidirectional boundary every other reader obeys.
* **No net-new observation kind in v1.** The frozen model expresses
  every required field; a future kind is possible but gated behind a
  written justification and the `parse.go` kind checklist
  ([`../mvp/life-archive.md`](../mvp/life-archive.md) §8).

## Consequences

* Decades of body and story history become an ordinary, backdated write
  today — the "corpus bet" ([`../observations.md`](../observations.md)
  §8) gains its first excavation surface without touching the
  foundation the bet depends on.
* A body-guidance consumer reads injury context through one stable,
  read-only projection seam (extending `activeInjuryLines()`) rather
  than reaching into registry state — the projection is additive, so a
  consumer built in parallel adapts to it whenever either lands.
* The public-safe boundary is preserved by construction: the binary
  carries only generic engine, schema, verbs, and synthetic fixtures;
  the personal review conversation, the monthly cadence, and the live
  archive live in the operator's own runtime, never in this repo.
* Because every new package, command, and projection is **additive** (no
  existing signature changes), the surface is revertible cleanly, and a
  future in-binary conversational loop — should one ever earn its
  place — wraps these same read-only contracts rather than replacing
  them.
