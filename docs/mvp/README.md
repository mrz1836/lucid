# Lucid MVP Docs

This directory holds the documentation set for the **first buildable steel
thread** of Lucid. It is scoped to a local, single-user companion that runs on
top of an existing chat/agent runtime (no standalone app yet).

The full Lucid product vision is much broader than this MVP. These docs
narrow the surface area to one end-to-end loop a future implementer can build
and run locally without making the harder long-term product decisions first.

## Source documents this MVP reflects

This MVP set is anchored to the existing Lucid repository docs and does not
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
  certainty — is preserved.

## What lives here

The remaining MVP documents will be added in subsequent phases:

* `product-principles.md` — implementation principles derived from the vision.
* `steel-thread.md` — the single end-to-end user loop the MVP proves.
* `local-runtime.md` — how Lucid runs locally before a standalone app exists.
* `architecture.md` — module/contract layout for the MVP.
* `data-model.md` — Markdown/JSON record shapes under `~/.lucid/`.
* `agent-contracts.md` — input/output/access/failure contracts per agent.
* `claude-code-workflow.md` — how a coding agent should build the MVP.

The build-ready spec lives at [`../../specs/mvp-scope.md`](../../specs/mvp-scope.md).

## What this MVP is not

* Not a desktop, mobile, or web app.
* Not a therapist, diagnostic tool, or medical device.
* Not a cloud service, shared-profile network, or autonomous sender.
* Not a replacement for the long-term vision in `vision.md` or the reference
  architecture in `technical-spec.md`.
