<!--
Status: build plan — sequencing only. Behavior lives in the specs
this file links; if this plan and a spec disagree, the spec wins and
this file gets the diff. Phase numbers are the canonical MVP phases
(1–7 acceptance-criteria.md, 8–10 engine-module.md,
11–12 observations-module.md) — this plan orders them, it never
renames or renumbers them.
-->

# Lucid MVP — Build Plan

What gets built, in what order, and what "done" means per stage.
The ordering leans on one sentence in [`scope.md`](scope.md) §9:
**phases 8–12 depend only on phases 1–2** — so the Engine ships
before the Mirror thread, deliberately.

## Standing constraints (before Stage 0 and forever)

* **The practice never waits on the plan.** The manual loop — a
  dedicated channel, a scheduled pre-committed bell template, a
  one-line close-out — runs from before Stage 0 and keeps running
  through every stage. The cathedral clause
  ([`../architecture.md`](../architecture.md) §8, Phase 2) is in
  force: a period in which the software advanced while the practice
  missed is a failed period regardless of code shipped.
* **Priority order is fixed:** practice > record > analysis (P10).
  Build effort is a governed work project, not a license to skip a
  night.
* **Sanctuary holds at every stage** (P3): no agent reads
  `~/.lucid/engine/`, `observations/`, or `registries/`; the Engine
  invokes no agent; no model call ever sits in an Engine write path.

## Stages

| Stage | Ships | Spec / criteria |
|-------|-------|-----------------|
| 0 | Repo bootstrap + `upgrade` | [ADR-0007](../adr/0007-cli-conventions.md) |
| 1 | Scaffold + `/log` (phases 1–2) | [`acceptance-criteria.md`](acceptance-criteria.md) |
| 2 | Engine (phases 8–10) | [`engine-module.md`](engine-module.md) |
| 3 | Mirror thread (phases 3–7) | [`acceptance-criteria.md`](acceptance-criteria.md), [`steel-thread.md`](steel-thread.md) |
| 4 | Observations (phases 11–12) | [`observations-module.md`](observations-module.md) |
| 5 | Skill + agent surface | [ADR-0008](../adr/0008-harness-skill.md), [`local-runtime.md`](local-runtime.md) |
| 6 | Supervised ops on the host | [ADR-0005](../adr/0005-secrets-management.md) |

### Stage 0 — Repo bootstrap

Copy the scaffolding wholesale from a reference binary repo (`hush`
or `atlas`) per [ADR-0007](../adr/0007-cli-conventions.md): `.github/`
CI, `.goreleaser.yml`, magex task surface, lint/codecov/editorconfig,
LICENSE, `cmd/lucid/` + `internal/` layout. Wire `lucid version` and
`lucid upgrade` (the house self-upgrade: release via `gh` with REST
fallback, SHA-256 verify, atomic swap) before any feature exists, and
cut `v0.0.x` releases from day one.

**Done when:** fresh clone builds green through magex; a tagged
release installs itself over a previous one via
`lucid upgrade` on the target host. Every later stage now ships in
place.

### Stage 1 — Scaffold + capture (phases 1–2)

The cobra spine ([ADR-0003](../adr/0003-runtime-surface.md)), config
load/validation, the `~/.lucid/` tree
([`data-model.md`](data-model.md)), and `/log` writing raw captures.

**Done when:** acceptance-criteria phases 1–2 pass (layout exists,
`lucid.json` validates, capture latency under a second).

### Stage 2 — Engine first (phases 8–10)

The reason this plan exists: the binary's first real job is taking
over the practice that is already running manually.

* Phase 8: `engine/` tree, `chain.json` incl. clock profiles, day
  records, rollover math, `/closeout` (journal line lands in `raw/`).
* Phase 9: deterministic `status.json`, `/mode`, `/status`,
  mode-relative adherence, storm-aware scoring.
* Phase 10: the tripwire and bell as `go-flywheel` jobs
  ([ADR-0004](../adr/0004-core-dependencies.md)) — **shipped** as the
  `lucid scheduler run` daemon plus a concrete Discord-bot
  `scheduler.Notifier` — L1/L2 + storm template variants, witness
  confirmation flow, dead-man semantics. The harness bot token enters
  the `hush` vault ([ADR-0005](../adr/0005-secrets-management.md)),
  injected as the generic `LUCID_HARNESS_TOKEN`; templates post to the
  dedicated channel (`"user"`) and the witness channel (`"witness"`).

**Done when:** engine-module phase 8–10 criteria pass; the interim
reminder cron is retired; a synthetic silent day fires the tripwire
end-to-end; a real close-out lands nightly through the binary.

### Stage 3 — Mirror thread (phases 3–7)

`/checkin`, the structuring pass, insight validation through the
resonance gate, `/reflect` weekly recall, `/ask` — per
[`steel-thread.md`](steel-thread.md) and
[`agent-contracts.md`](agent-contracts.md). Model access lands here,
behind the provider boundary of
[ADR-0006](../adr/0006-model-access.md): OAuth'd vendor CLIs or a
local model, fakes in tests, no API keys anywhere.

**Done when:** acceptance-criteria phases 3–7 pass, including the
phrase-blocklist regression and provenance checks.

### Stage 4 — Observations (phases 11–12)

Micro-logs and deterministic parsers, registries, the joined `/day`
view; then sticky location, the one opt-in enricher, series CSV, and
the first clinician packet.

**Done when:** observations-module phase 11–12 criteria pass; one
real export renders and is releasable by hand.

### Stage 5 — Skill and agent surface

Formalize what the interim glue proved: `skills/lucid/SKILL.md`
canonical in this repo ([ADR-0008](../adr/0008-harness-skill.md)),
the dedicated agent's identity files, verbatim passthrough on Engine
verbs, session-memory excluded from any pushed brain, witness wiring
fixed at Ring 0.

**Done when:** a close-out via the channel completes with every
model down; the sanctuary boundary survives a grep of the agent's
reachable surface; a second instance could install the same skill
against its own `~/.lucid/`.

### Stage 6 — Supervised operations

The service life: `hush supervise` runs the `lucid scheduler run`
scheduler under launchd as a sibling of the gateway (fate-sharing is
the failure mode the tripwire exists to avoid); upgrades flow through
the managed pattern
with the drain window (never between bell and close-out;
post-upgrade health check = tripwire self-check); backups cover the
ADR-0002 set.

**Done when:** kill the scheduler mid-evening and the tripwire still
fires next morning after supervised restart; an upgrade and a
rollback both complete without costing a night.

## Order rationale, in three lines

Engine before Mirror because the practice is the product's first
user and P10 makes it the priority; `scope.md` §9's dependency note
makes the reordering legal without touching any spec; `upgrade`
ships in Stage 0 because a binary that will be replaced weekly must
know how to replace itself before it earns responsibilities.
