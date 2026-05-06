# Lucid MVP Docs

This directory is the canonical entrypoint for the **first buildable steel
thread** of Lucid. It is scoped to a local, single-user companion that runs on
top of an existing chat/agent runtime (no standalone app yet).

The full Lucid product vision in [`vision.md`](../../vision.md) is much broader
than this MVP. These docs narrow the surface area to one end-to-end loop a
future implementer can build and run locally without making the harder
long-term product decisions first.

## Purpose of the MVP

The MVP exists to answer one question with a working system, not a deck:

> Can a local-first AI companion capture an inner-life reflection, structure
> it, suggest one possible pattern, get user validation, and recall it later
> in a way that feels like the Lucid described in `vision.md`?

If that loop works for one person on one host, every other Lucid ambition
(mobile, shared profiles, agent-self drafting, framework layering, graph
memory, SQLite consolidation) becomes a follow-on instead of a guess. If it
does not work, no amount of platform engineering will save it.

The MVP therefore proves the product, not the platform.

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

Anything beyond this list is post-MVP. See
[`claude-code-workflow.md`](claude-code-workflow.md) for how a coding agent
should execute this sequence.

## Documents in this set

| File | What it covers |
|------|----------------|
| [`README.md`](README.md) | This page — canonical MVP entrypoint and steel-thread overview. |
| [`product-principles.md`](product-principles.md) | Implementation principles derived from `vision.md`; voice; long-term roles vs. MVP focus. |
| [`steel-thread.md`](steel-thread.md) | The exact end-to-end user loop, with happy / rejected / no-pattern paths and a synthetic transcript. |
| [`local-runtime.md`](local-runtime.md) | How Lucid runs locally on OpenClaw or Hermes via Discord before a standalone app exists. |
| [`architecture.md`](architecture.md) | Module boundaries: harness, router, agents, storage adapter, structuring/reflection pipeline, safety gates. |
| [`data-model.md`](data-model.md) | `~/.lucid/` layout and Markdown/JSON record schemas, with a migration path to the SQLite schema in `technical-spec.md`. |
| [`agent-contracts.md`](agent-contracts.md) | Input/output/access/failure rules for Intake, Structuring, Reflection, and Safety/Consent agents. |
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
| [`vision.md`](../../vision.md) | Long-form vision: four roles (Journal, Therapist, Coach, Agent-Self), pillars, profile, frameworks, capture/understand/connect/grow loop, agent-self, shared profiles, future possibilities. | MVP docs translate the vision into implementation principles and select a single steel-thread loop, deferring most platform-scale features. |
| [`technical-spec.md`](../../technical-spec.md) | Reference architecture: agent set, consolidation cycles, historical reprocessing, adaptive evolution, commands, skills, three-layer data model, SQLite schema, memory graph, bootstrapping. | MVP docs reduce this to a smaller set of agents and a Markdown/JSON data model, with explicit migration paths back to the full spec. |
| [`.gitignore`](../../.gitignore) | Repo hygiene baseline. | MVP docs do not require new ignored paths; runtime data lives outside the repo at `~/.lucid/`. |

## Relationship to the broader vision

* The full vision keeps four roles and a rich agent set. The MVP keeps the
  Journal and Mirror/Reflection facets and intentionally defers most of the
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
* **Not a therapist, diagnostic tool, or medical device.** Inferred patterns
  are hypotheses framed as "I noticed a possible pattern — does this
  resonate?", never as findings.
* **Not a cloud service.** Runtime data lives under `~/.lucid/` on a single
  host. Only narrow, relevant slices are sent to an LLM for processing —
  never the whole history.
* **Not an autonomous sender.** Drafts are surfaced for explicit approval.
  No external messages, calendar invites, or notifications go out without
  the user's deliberate confirmation. The Agent-Self surface is mostly
  deferred for the MVP.
* **Not a shared-profile network.** Shared profiles, multi-user dynamics,
  and relational bridges remain in `vision.md` as future possibilities.
* **Not a graph database or full SQLite consolidation.** The MVP uses
  flat Markdown/JSON. The richer schema in `technical-spec.md` is a
  documented migration path, not an MVP requirement.
* **Not a replacement** for the long-term vision in `vision.md` or the
  reference architecture in `technical-spec.md` — it is the smallest
  honest first proof of them.
