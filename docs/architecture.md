# Lucid — System Architecture

**Date:** 2026-07-03 · **Status:** Canonical — a living concept, evolving with the project

**Provenance.** This document merges two halves conceived independently for the same idea: Lucid (2025 — the inner-life system) and a behavioral engine (July 2026, formerly codenamed WAKE), unified over a single event substrate and consent model. Along the way the design has kept its vocabulary collision-free — the self-sustaining portfolio status was renamed twice ("Engine" → "Flywheel" when the Engine subsystem arrived, then → **Anchor** when the `go-flywheel` dependency entered the stack, ADR-0004), the "Mirror" voice mode became **Echo**, and the "Green" domain marker became **Steady** — generalized every spec for any user (personal data lives only in per-user calibration, never in specs), restored mechanisms dropped between design drafts (the runtime priority order, the AI-dependency boundary, the excavation practice, the dispatch definition), and stays aligned with the buildable MVP in [`mvp/scope.md`](mvp/scope.md).

**Related documents:** [`vision.md`](vision.md) (product vision), [`technical-spec.md`](technical-spec.md) (reference implementation architecture), [`docs/engine.md`](engine.md) (behavioral engine specification), [`docs/observations.md`](observations.md) (observation & enrichment layer), [`docs/scientist.md`](scientist.md) (the self-experimentation & learning layer), [`docs/frameworks.md`](frameworks.md) (the interpretation-lens layer), [`docs/calibration.md`](calibration.md) (the calibration guide — per-user setup, `lucid init` in the packaged app), [`docs/mvp/`](mvp/README.md) and [`mvp/scope.md`](mvp/scope.md) (the first buildable slice). A user's own calibration lives in `personal/calibration.md`, which is excluded from any shared history (§5).

## 1. Overview

Lucid is a personal operating system composed of two cooperating subsystems over one shared substrate. The **Mirror** produces understanding: it captures the life stream, maintains a longitudinal self-model, and interprets events through user-selected frameworks. The **Engine** produces behavior: it initiates, sequences, and defends the small set of committed daily practices, and enforces accountability for them. Both read and write one **Ledger** — an append-only event store the user owns. Around them sit the **Agent-Self** (outbound action under draft-and-approve), the **Charter** (versioned statement of direction), and the **Witness** layer (human accountability with strictly scoped visibility).

The central architectural claim: reflection tools fail without a behavior layer, and behavior tools fail without a reflection layer. A mirror without an engine yields insight that is never applied; an engine without a mirror yields compliance without understanding. Lucid is deliberately both, with a hard boundary between what each is allowed to do.

## 2. Design principles

**P1 — Capture first, structure later.** No capture pathway may impose structure at the moment of capture. Voice, one-liners, taps, and photos are accepted raw; extraction, tagging, and linking happen downstream.

**P2 — Events are immutable; interpretations are drafts.** The Ledger is append-only. Everything derived from it — the Profile, relational patterns, balance views, statistics — is a projection: rebuildable, versioned, editable. No inference merges into the self-model without explicit user acceptance (the resonance gate, §5). Rejected inferences are retained as events.

**P3 — Teeth attach to acts, never to content.** The Engine may enforce, escalate, and apply stakes to whether a committed practice occurred. Nothing in the Mirror — no entry, emotion, admission, or silence about content — may ever be scored, escalated, shared, or penalized. A return after silence is welcomed, not audited. Rationale: behavior responds to stakes; honesty dies under them.

**P4 — Obliquity.** Outcome variables (body composition, relationship status, skill level) may be computed and reviewed at gate cadence but are never surfaced as daily targets. Daily surfaces show process only. The Charter may name outcomes as **forever unmeasured**: things the system contributes rooms and readiness toward, but never metrics — these are recorded explicitly so they cannot drift into being targets later.

**P5 — Consent is symmetric.** Draft-and-approve governs both directions: outbound actions (messages, sends) and inbound self-model changes (inferences, labels) are proposals until the user approves, edits, or rejects them. Pre-committed actions (the Engine's escalation ladder, the stake) are not an exception — they are consent granted in advance, in writing, while strong, and revocable only at governed review.

**P6 — Local-first sovereignty, built to outlive its tools.** All data lives in user-owned, plain, exportable formats. AI functions as a stateless analyst over relevant excerpts; it is never the system of record. No analytics, no monetization, optional local models. The corpus is a decades-scale asset: the event envelope is frozen, payload schemas are versioned and documented, and the whole Ledger is always exportable as a directory of plain files — so the record you keep this year stays fully legible to whatever far-better tools exist in ten.

**P7 — Ignition-first runtime.** The Engine assumes starting — not persisting — is the scarce resource. External initiation (bells), minimum-viable floors, and slot defense are primary mechanisms; motivation is never a dependency.

**P8 — Governed change.** The Charter is amended only at scheduled reviews, with recorded reasons. Runtime parameters change at most once per weekly review. Anything may change; nothing changes silently.

**P9 — The runtime never depends on AI.** The Engine's daily surface (bell, chain, close-out) is self-serve and completable with no model in the loop; AI's sanctioned roles are analysis at review cadence and automation of the escalation tripwire — never daily check-ins, never motivation, never being where the data lives. The Mirror's conversational reflection is offered and slotless, never load-bearing for the chain. If every model is unavailable, the system still runs; only its insight cadence degrades. (This is the reconciliation between the concept document's guided daily check-in and the engine's independence requirement: capture is a two-minute act, conversation is optional.)

**P10 — Priority order: practice > record > analysis.** Fixed and non-negotiable. If tooling breaks, a plain text message to oneself counts as the record and is backfilled later. Losing a night of data is recoverable; losing the practice is the actual incident. Analysis exists to serve the record; the record exists to serve the practice; never the reverse.

## 3. Subsystems

**Mirror.** Responsibilities: capture ingestion — both prose (journal, one-liners, voice) and the structured **observation layer** (body signals, intake, mood, context, memory fragments; full specification in [`docs/observations.md`](observations.md)) — multi-timescale reflection (daily/weekly/monthly/yearly), pattern inference, the Personal Profile, the relational map, framework-based interpretation, and voice modes (§6). Writes capture and inference events; reads the full Ledger. Prohibited: enforcement of any kind — including over observations, which are inventory, never obligation.

**Engine.** Responsibilities: bells, chains, floors, the Crux protocol, the dispatch, operating modes, capacity signal, telemetry tiers, gates, retros, hearings, portfolio statuses, and the accountability ladder. Full specification in [`docs/engine.md`](engine.md). Writes completion, mode, and governance events; reads runtime projections and the avoided-task queue only — the Engine never reads Mirror content.

**Ledger.** Append-only event store; the single source of truth. Conceptual event shape: identifier, `recorded_at` (immutable), `occurred_at` (user-suppliable, enabling backfilled historical entries — the store is bitemporal), type, payload, and tags (domains, emotion annotations, capacity, mode, person references). Logical-day assignment uses a configurable rollover boundary (default 04:00) so post-midnight completions attribute to the intended day.

**Agent-Self.** Outbound extension: message drafting in the user's learned voice, relationship follow-through, commitment support, consented pre-briefs. Every action is a draft requiring explicit approval; edits feed voice learning. Activates in Phase 3 only, once the Profile and relational map have sufficient depth.

**Charter.** The versioned constitution: identity statement, one- and five-year images, framework stack, non-negotiables, the forever-unmeasured list (P4), stake definitions. Amended quarterly with reasons; readable by all subsystems as the statement of intent. The six authoring prompts are in [`docs/calibration.md`](calibration.md).

**Witness.** Named humans in the accountability path. Visibility contract: topline Engine status only — current streak, declared mode, escalation state. Witnesses can never access Mirror content, capacity payloads, or the Profile. Escalation uses dead-man semantics: absence of expected Engine events triggers it; user confession is never the mechanism. The witness holds the written stake (they cannot execute a stake they have not seen) and is briefed with exactly one job: when escalation fires, ask about it, once.

## 4. Domains and portfolio

Default domain set: **Body, Play, Mind, People, Craft, Inner, Health** — user-customizable. Health is operations (appointments, medication and supplement regimens, provider follow-through, labs); its administrative tasks are first-class Crux candidates. Domains covering areas that are currently stable may be marked **Steady**: monitored in balance views, unmanaged by the runtime. **Emotions are an annotation, not a domain** — emotional texture tags any event in any domain, letting the Mirror track affect across the whole system rather than filing it in a silo.

Every tracked item carries exactly one portfolio status: **Anchor** (self-sustaining, zero maintenance cost, load-bearing for oblique outcomes), **Active** (holds a runtime slot; deliberately scarce), **Bench** (available for spontaneous engagement, permanently untracked, guilt formally revoked), or **Parked** (dormant, with a standing quarterly Hearing where it may be Activated, Benched, or retired with a written reason — Parked is a repo with no open issues, not a graveyard). Activation is zero-sum: a new Active item must displace or be gated in. New Active items are wrapped in an existing Anchor identity wherever possible — a strength practice belongs to the sport it serves, not to "fitness" — because identity-wrapped practices route around the direct-pursuit failure mode (P4).

## 4b. Extension model — how Lucid grows without changing

The foundation is exactly three parts: the **Ledger** holds events
(append-only, bitemporal, one frozen envelope), **Registries** hold
referents (people, injuries, threads, places, eras — long-lived things
events point at), and **Projections** hold views (rebuildable,
versioned, disposable). Every future capability is one of four
extension axes, and only these:

1. **A new event kind** — a typed payload inside the frozen envelope
   (e.g., a pain observation, a meal, a memory fragment).
2. **A new registry** — a new class of referent, following the
   `people/` pattern.
3. **A new enricher** — a deterministic, opt-in, outbound-minimal
   source that appends context annotations ([`observations.md`](observations.md) §5).
4. **A new projection** — a new way to view, join, question, or export
   what is already recorded.

The Scientist ([`scientist.md`](scientist.md)) is the worked example:
an entire self-experimentation layer assembled from one registry
(`experiments/`), two event kinds (`hypothesis`, `verdict`), and
deterministic projections, riding cadences that already exist — no
new subsystem.

What is *never* an extension: changing the envelope, mutating past
events, adding a subsystem beyond those already named in §3
(Agent-Self, Charter, and Witness are base design activated by
roadmap phase, not extensions — the prohibition covers *new*
subsystems), or letting a new capability grow teeth outside the
Engine's Gate process. Registries themselves are not a new subsystem
— they generalize the existing people store. Payload
schemas version forward; projections read what they understand and
skip what they don't. The **logical day is the universal join key**
across engine records, observations, enrichment, and entries. This is
the contract that lets views multiply for decades — dashboards,
correlations, exports, future-AI analysis — while the record beneath
them never has to be migrated, re-consented, or re-understood.

## 5. Consent and privacy model

The **resonance gate** is the lifecycle for all inferences: proposed → presented with supporting evidence and the question "does this resonate?" → accepted, edited, or rejected → merged into the Profile only on acceptance, with rejections retained as events. The system never asserts a pattern; it offers one. An accepted insight may carry a **rule** — one line of user-stated intent derived from it, recorded as testimony. Rules take no streaks, scores, or reminders; they are revisited at gate cadence ("still standing?" — kept and lapsed are both first-class answers), and they earn teeth only by becoming an Engine commitment through a Gate. Insight-to-action is the Mirror's honest measure, and it is measured at review cadence, never surfaced daily (P4).

An **off-limits registry** lets the user mark topics as sensitive or excluded from inference entirely — including people: an off-limits person is redacted from every inference input at slice-build time, fail closed, while the record beneath persists (facts persist, labels belong to the user). The Profile is exportable and wipeable; the Ledger beneath it is not — facts persist, labels belong to the user. The Witness visibility contract (§3) is enforced at the projection layer: witness-facing views are computed from Engine events only — no observation, enrichment, or Mirror content is ever witness-visible.

**Enricher consent.** Sources that fetch external context (weather, daylight — [`observations.md`](observations.md) §5) are opt-in per source, with what each transmits declared in the instance configuration. Outbound minimalism is binding: coordinates and dates may leave; content and identifiers never do. Location is stated by the user, never harvested from a device; device integrations are a future, separately-consented chapter.

**Disclosure (apertures).** Sharing the record with people — doctors, therapists, friends, partners — follows one model ([`vision.md`](vision.md) §7): a per-recipient **aperture** (depth ring 0–4, topic scope, register, cadence) renders a projection; the user reviews the exact artifact, edits or redacts, releases it by hand, and the release is logged (what, to whom, when, at what ring). Recipients get artifacts, never live access; the off-limits registry excludes at every ring; the witness remains fixed at ring 0 structurally. Ingested material — session notes, professional plans, outside perspectives — enters as attributed events and passes the resonance gate like any inference; another person's view of the user is testimony, never fact.

**Instance isolation.** Specifications are generalized and shareable; calibration is personal and private. Each user's configuration — health context, relationships, stakes, portfolio — lives in `personal/calibration.md` (from the template in [`docs/calibration.md`](calibration.md)) and is excluded from any shared or published history (`personal/` is gitignored). If collaborators, mirrors, or repo-scoped agents are ever added, audit history before any visibility change. This same split is what makes Lucid usable by more than one person: friends fork the specs and write their own instance; nothing about the system's mechanics assumes any particular user's body, schedule, or biography.

## 6. Voice modes

Four interaction modes: **Coach** (goals, accountability, next actions), **Mentor** (decisions, craft, perspective), **Reflect** (emotions, patterns, relationships — validates before probing), and **Echo** (pure reflection: echoes, surfaces contradictions, withholds advice; formerly "Mirror," renamed to avoid collision with the Mirror subsystem). Mode selection is automatic from context with explicit user override always available. Boundary: Reflect mode is pattern cartography, not clinical treatment; for wound-level work the system's role is to support professional care by supplying longitudinal context, exactly as capacity data supports medical providers.

## 7. Frameworks

Interpretation is pluggable. The user defines a default stack in their Charter and may layer additional lenses per-situation or per-season: IFS, NVC, Stoicism, Gottman, CBT, ACT, Taoism, attachment theory, and others. Frameworks shape how the Mirror asks and interprets; no framework overrides the resonance gate or the sanctuary rule (P3). The full contract — definition files as shareable specs, stack consent, one labeled lens per output, the vocabulary-licensing mechanism, and the lens-neutral record — is [`docs/frameworks.md`](frameworks.md); the layer ships post-MVP, before the aperture translation layer.

## 8. Phased roadmap

**Phase 0 — Manual protocol (reference implementation, no software).** The Engine runs on a phone alarm and physical environment design; capture runs on voice memos or dictation into any recording surface; any capable AI serves as interim analyst at retro cadence; the escalation tripwire is wired with whatever automation exists to hand, under a hard one-day tooling timebox. Phase 0 is fully operational and is the standard against which all later phases are judged: software that adds friction relative to Phase 0 is a regression.

**Phase 1 — Structure pass.** Tooling to project the accumulated corpus into the first Profile, the first relational map entries, and balance views — all through the resonance gate. Analysis over existing data; still no required application.

**Phase 2 — Chat-runtime MVP.** The unified nightly loop per [`mvp/scope.md`](mvp/scope.md): the Engine's two-minute close-out is the capture surface, feeding both subsystems from one act, on an existing local chat/agent harness. The Mirror thread (capture → structure → one resonance-gated pattern → weekly recall) and the Engine module (close-out, streak projections, escalation tripwire) ship together, because the central claim (§1) is only testable with both halves live. Build effort is itself governed — entered as a work project with an explicit weekly budget and gate reviews. Binding constraint (the cathedral clause): build hours never displace runtime execution; a period in which the software advanced while the practice missed is recorded as a failed period regardless of code shipped.

**Phase 2b — Standalone app.** The desktop/mobile application per [`technical-spec.md`](technical-spec.md), replacing the chat harness only. Everything below the harness (router, agents, storage, gates) survives per the extension points in [`docs/mvp/architecture.md`](mvp/architecture.md).

**Phase 3 — Agent-Self.** Outbound drafting and follow-through, activated once Profile and relational-map depth support drafting in the user's true voice.

**Phase 4 — Shared Profiles.** Relational bridges per the concept document. Far horizon; requires Phase 3 maturity and a counterparty.
