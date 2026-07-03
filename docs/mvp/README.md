# Lucid MVP Docs

This directory is the canonical entrypoint for the **first buildable steel
thread** of Lucid. It is scoped to a local, single-user companion that runs on
top of an existing chat/agent runtime (no standalone app yet).

The full Lucid product vision in [`vision.md`](../../vision.md) is much broader
than this MVP. These docs narrow the surface area to one end-to-end loop a
future implementer can build and run locally without making the harder
long-term product decisions first.

> **v2 note (unified nightly loop).** Since this set was written, the
> system-level architecture in [`../architecture.md`](../architecture.md)
> merged a behavioral **Engine** into Lucid as a first-class subsystem.
> The MVP now ships two cooperating halves: the Mirror steel thread
> documented across this set (unchanged), plus the agent-free Engine
> module specified in [`engine-module.md`](engine-module.md). The
> synthesis lives in [`../../specs/mvp-scope.md`](../../specs/mvp-scope.md)
> (v2). Where an older page here disagrees with those two documents on
> *scope*, they win; on *conventions* (naming, storage discipline,
> gates), this set remains authoritative.

## Purpose of the MVP

The MVP exists to answer one question with a working system, not a deck:

> Can a local-first companion **defend one committed daily practice**
> (initiate it, record it, escalate honestly when it slips) **and**
> capture the reflection that rides on it, structure it, suggest one
> possible pattern, get user validation, and recall it later — in a way
> that feels like the Lucid described in `vision.md` and
> [`../architecture.md`](../architecture.md)?

If that loop works for one person on one host, every other Lucid ambition
(mobile, shared profiles, agent-self drafting, framework layering, graph
memory, SQLite consolidation, the Crux and portfolio machinery) becomes a
follow-on instead of a guess. If it does not work, no amount of platform
engineering will save it.

The MVP therefore proves the product, not the platform. The central
architectural claim it tests: reflection tools fail without a behavior
layer, and behavior tools fail without a reflection layer — so the MVP
deliberately ships the smallest honest version of **both**, joined at one
act: the Engine's two-minute nightly close-out writes the Engine's day
record *and* the Mirror's raw journal entry in a single command.

## The steel thread, in one view

```
            user
              │
              ▼
   ┌───────────────────────┐
   │ /checkin or /log      │   captured in a Discord thread (or any chat
   │ in a chat thread      │   surface routed through OpenClaw or Hermes)
   └──────────┬────────────┘
              │
              ▼
   ┌───────────────────────┐
   │ Intake agent          │   asks 2–4 follow-up questions only if needed
   │ (clarifying questions)│
   └──────────┬────────────┘
              │
              ▼
   ┌───────────────────────┐
   │ Raw entry             │   written to ~/.lucid/raw/ as immutable
   │ (Markdown + frontmtr) │   Markdown — ground truth, never edited
   └──────────┬────────────┘
              │
              ▼
   ┌───────────────────────┐
   │ Structuring agent     │   extracts emotions, themes, people mentions,
   │ (extract artifacts)   │   writes ~/.lucid/processed/<entry-id>.json
   └──────────┬────────────┘
              │
              ▼
   ┌───────────────────────┐
   │ Reflection agent      │   proposes exactly one possible pattern,
   │ ("does this resonate?")│   tagged as a hypothesis, never as fact
   └──────────┬────────────┘
              │
              ▼
   ┌───────────────────────┐
   │ User validation       │   accept / reject / nuance — written to
   │                       │   ~/.lucid/insights/ with provenance
   └──────────┬────────────┘
              │
              ▼
   ┌───────────────────────┐
   │ Weekly recall         │   /reflect surfaces validated insights and
   │ (manual command)      │   asks if any still resonate this week
   └───────────────────────┘
```

The full flow with happy/rejected/no-pattern variants and a synthetic
transcript lives in [`steel-thread.md`](steel-thread.md).

**The Engine loop wraps around this thread nightly** (full spec in
[`engine-module.md`](engine-module.md)):

```
 21:30 bell prompt ─► chain runs in the world ─► /closeout (≤2 min, no LLM):
 link flags · declared-mode scoring · capacity digit · one journal line
      │                                   │
      ▼                                   ▼
 ~/.lucid/engine/ (day record,       ~/.lucid/raw/ (journal line enters
 streaks, budget burn)               the Mirror thread above)

 09:00 tripwire: completed → reset · one miss → L1 nudge naming the floor
 · two consecutive → L2 to the witness (dead-man: fires on ABSENCE of data;
 topline status only, never content)
```

## Recommended first implementation path

After these docs are approved, the very first build sequence is:

1. **Scaffold `~/.lucid/`** — create the layout described in
   [`data-model.md`](data-model.md) (raw, processed, insights, sessions,
   reflections) with a single seed file proving the path is writable.
2. **Implement `/log`** — the simplest capture: take a one-line message,
   write an immutable raw entry, return an entry id. No structuring yet.
3. **Implement `/checkin`** — the guided variant: ask 2–4 clarifying
   questions inside a thread, then write a single raw entry that bundles
   the answers.
4. **Add the structuring pass** — a script-or-agent that reads a raw entry
   and writes a processed artifact next to it. Idempotent and rebuildable.
5. **Add one insight validation flow** — propose exactly one possible
   pattern, store the user's accept/reject/nuance response with the
   processed artifact id.
6. **Add weekly reflection stub** — a `/reflect` command that lists
   validated insights from the past week and asks "still resonating?".
7. **Add grounded `/ask`** — a read-only command that answers free-form
   questions by quoting validated insights and weekly reflections only.
   No new patterns, no advice, no raw-entry access.
8. **Engine scaffold + `/closeout`** — the `~/.lucid/engine/` tree,
   chain config, per-logical-day records with 04:00 rollover math, and
   the journal line written into `raw/`. Deterministic; no agents.
9. **Derived status + `/mode` + `/status`** — rebuildable streak and
   mode-relative adherence projection, error-budget burn.
10. **The tripwire** — scheduled bell prompt and L1/L2 escalation with
    dead-man semantics and the witness consent flow.

Phases 8–10 depend only on phases 1–2 and may be built immediately
after them; [`../../specs/mvp-scope.md`](../../specs/mvp-scope.md) §9
recommends exactly that order for ignition-limited users. Acceptance
criteria for 8–10 live in [`engine-module.md`](engine-module.md).

Anything beyond this list is post-MVP. See
[`claude-code-workflow.md`](claude-code-workflow.md) for how a coding agent
should execute this sequence.

## Documents in this set

| File | What it covers |
|------|----------------|
| [`README.md`](README.md) | This page — canonical MVP entrypoint and steel-thread overview. |
| [`product-principles.md`](product-principles.md) | Implementation principles derived from `vision.md`; voice; the literal phrase blocklist and bundling rules. |
| [`steel-thread.md`](steel-thread.md) | The exact end-to-end user loop, with happy / rejected / no-pattern / soft-contradiction paths, the `/ask` stage, and synthetic transcripts. |
| [`engine-module.md`](engine-module.md) | The Engine MVP slice: `/closeout`, `/mode`, `/status`, the `~/.lucid/engine/` tree, streak/adherence math, the escalation tripwire, the pre-committed-send consent amendment, and acceptance criteria for phases 8–10. |
| [`local-runtime.md`](local-runtime.md) | How Lucid runs locally on OpenClaw or Hermes via Discord before a standalone app exists. |
| [`architecture.md`](architecture.md) | Module boundaries: harness, router, agents, storage adapter, structuring/reflection pipeline, safety gates. |
| [`data-model.md`](data-model.md) | `~/.lucid/` layout, naming conventions (incl. `person_key` algorithm and TZ rule), record schemas, and SQLite migration. |
| [`agent-contracts.md`](agent-contracts.md) | Input/output/access/failure rules for Intake, Structuring, Reflection (`propose` / `surface_for_recall` / `answer_grounded`), and Safety/Consent. |
| [`error-states.md`](error-states.md) | Unified table of every failure mode: per-agent, storage, transport, empty-state. Each row names trigger, behavior, user message, disk side effect, recovery. |
| [`acceptance-criteria.md`](acceptance-criteria.md) | Per-phase pass/fail checklist mapped to the seven build phases. Concrete test cases, verification commands, definition of done. |
| [`claude-code-workflow.md`](claude-code-workflow.md) | How a coding agent should build Lucid: docs-first planning, small commits, deterministic scripts, bounded subagents. |
| [`../../specs/mvp-scope.md`](../../specs/mvp-scope.md) | Final concise scope spec synthesized from the docs. |

Several files in this list are added in later phases. Until then, broken
relative links signal "documented next" rather than missing intent.

## Source documents this MVP reflects

The MVP set is anchored to the existing Lucid repository docs and does not
contradict them.

| Source file | What it provides | How the MVP uses it |
|-------------|------------------|---------------------|
| [`README.md`](../../README.md) | Emotional landing page describing the felt experience of Lucid. | Preserved as-is in voice; MVP docs link out from it without diluting the landing-page role. |
| [`vision.md`](../../vision.md) | Long-form vision: five roles (Journal, Therapist, Coach, Engine, Agent-Self), pillars, profile, frameworks, capture/understand/connect/grow loop, agent-self, shared profiles, future possibilities. | MVP docs translate the vision into implementation principles and select a single steel-thread loop plus the Engine module, deferring most platform-scale features. |
| [`technical-spec.md`](../../technical-spec.md) | Reference architecture: agent set, consolidation cycles, historical reprocessing, adaptive evolution, commands, skills, three-layer data model, SQLite schema, memory graph, bootstrapping. | MVP docs reduce this to a smaller set of agents and a Markdown/JSON data model, with explicit migration paths back to the full spec. |
| [`.gitignore`](../../.gitignore) | Repo hygiene baseline. | MVP docs do not require new ignored paths; runtime data lives outside the repo at `~/.lucid/`. |

## Relationship to the broader vision

* The full vision keeps five roles and a rich agent set. The MVP keeps the
  Journal and Mirror/Reflection facets plus the Engine's
  committed-practice core, and intentionally defers most of the
  Therapist, Coach, and Agent-Self surface area.
* The technical spec assumes SQLite, a memory graph, and consolidation across
  daily/weekly/monthly/yearly scales. The MVP uses Markdown/JSON files in a
  global local Lucid home, with a single weekly recall loop, and treats SQLite
  and the graph as later upgrades.
* Lucid's voice — trusted advisor, warm, honest, non-judgmental, humble about
  certainty — is preserved verbatim from the vision.

## What this MVP is not

* **Not a desktop, mobile, or web app.** The MVP runs through an existing
  chat/agent harness; a bespoke UI is a follow-on, not a precondition.
* **Not a cloud service.** Runtime data lives under `~/.lucid/` on a single
  host. Only narrow, relevant slices are sent to an LLM for processing —
  never the whole history.
* **Not an autonomous sender.** Drafts are surfaced for explicit approval.
  No external messages, calendar invites, or notifications go out without
  the user's deliberate confirmation — with exactly three pre-committed
  template exceptions (bell prompt, L1 nudge, L2 witness escalation), each
  behind a recorded consent flag and none carrying reflective content; see
  [`engine-module.md`](engine-module.md) §"Consent amendment". The
  Agent-Self surface is mostly deferred for the MVP.
* **Not a habit gamification app, and not the Coach.** The Engine module
  records days, defends one chain, and escalates honestly. It has no
  voice, no badges, no goal trees; conversational accountability remains
  a deferred surface.
* **Not a shared-profile network.** Shared profiles, multi-user dynamics,
  and relational bridges remain in `vision.md` as future possibilities.
* **Not a graph database or full SQLite consolidation.** The MVP uses
  flat Markdown/JSON. The richer schema in `technical-spec.md` is a
  documented migration path, not an MVP requirement.
* **Not a replacement** for the long-term vision in `vision.md` or the
  reference architecture in `technical-spec.md` — it is the smallest
  honest first proof of them.
