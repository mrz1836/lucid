# Lucid MVP Scope

> **Status:** build-ready. This is the canonical scope contract for the
> first implementable Lucid steel thread. It synthesizes the doc set in
> [`../docs/mvp/`](../docs/mvp/) into a single page a coding agent or
> reviewer can read in one sitting.

The longer surfaces — vision, principles, runtime, architecture, data
model, agent contracts, and Claude Code workflow — remain authoritative
in their own files. This spec restates only what is required to start
building, with explicit links back so nothing here drifts from the
underlying docs.

## 1. Problem statement

Most people have no system for understanding themselves. They forget
their own insights, repeat the same patterns, and lose the thread of
who they are becoming. Tools exist for tasks, money, and health — none
exist for the inner life.

The full Lucid product answer to that gap lives in [`../vision.md`](../vision.md):
a secure, AI-powered personal operating system that plays four roles
(Journal, Therapist, Coach, Agent-Self), adapts to the user's chosen
philosophical frameworks, and helps the user act, not just reflect.

The MVP narrows that gap to a single, falsifiable question:

> Can a local-first AI companion capture an inner-life reflection,
> structure it, suggest one possible pattern, get user validation, and
> recall it later in a way that feels like the Lucid described in
> [`../vision.md`](../vision.md)?

If that loop works for one person on one host, every other Lucid
ambition becomes a follow-on. If it does not work, no amount of
platform engineering will save it. **The MVP proves the product, not
the platform.**

## 2. MVP decision summary

The MVP is one buildable steel thread, run locally, on top of an
existing chat/agent harness, with files instead of databases and one
reflection cadence instead of four.

| Decision | Choice | Why | Source |
|----------|--------|-----|--------|
| Surface | Local chat thread (Discord today) via OpenClaw or Hermes — recommended path is OpenClaw + Discord. | Avoids building UI, accounts, or deployment before the loop is proven. | [`../docs/mvp/local-runtime.md`](../docs/mvp/local-runtime.md) |
| Roles | Journal + Mirror/Reflection only. Therapist, Coach, and Agent-Self are named seams, not implemented. | Picks the smallest honest first proof of `vision.md`. | [`../docs/mvp/product-principles.md`](../docs/mvp/product-principles.md) §1 |
| Capture | `/log` (free-form) and `/checkin` (2–4 follow-ups) — nothing else. | Capture-first, structure-later. | [`../docs/mvp/product-principles.md`](../docs/mvp/product-principles.md) §4 |
| Pattern proposal | Exactly one possible pattern per session, framed as a hypothesis. | Stops the system from collapsing into a confident diagnostic engine. | [`../docs/mvp/product-principles.md`](../docs/mvp/product-principles.md) §5 |
| Storage | Markdown + JSON files under `~/.lucid/`. No SQLite, no graph, no cloud. | Boring, replaceable, lossless subset of the spec's SQLite schema. | [`../docs/mvp/data-model.md`](../docs/mvp/data-model.md) |
| Reflection cadence | Manual `/reflect`, weekly. | One cadence before four. | [`../docs/mvp/steel-thread.md`](../docs/mvp/steel-thread.md) Stage 5 |
| External action | None. The MVP does not send, schedule, or post anything externally. | Agent-Self surface stays behind an explicit approval gate that does not exist yet. | [`../docs/mvp/product-principles.md`](../docs/mvp/product-principles.md) §7 |
| Voice | Trusted advisor — warm, honest, non-judgmental, humble about certainty. Hypothesis language only. | Encodes the only voice constraint a future agent prompt needs. | [`../docs/mvp/product-principles.md`](../docs/mvp/product-principles.md) §6 |

The MVP keeps every long-term role from `vision.md` named, so future
work has a clear seam to slot into without redesign.

## 3. Steel-thread flow

One end-to-end loop. The full version with happy / rejected /
no-pattern paths and a synthetic transcript lives in
[`../docs/mvp/steel-thread.md`](../docs/mvp/steel-thread.md).

```
  user
   │
   ▼
/checkin or /log         capture in a Discord thread (or any chat
   │                     surface routed through OpenClaw or Hermes)
   ▼
Intake (2–4 questions    only when /checkin and the opening message
 if needed)              is too thin; /log is friction-free
   │
   ▼
raw entry written        ~/.lucid/raw/<id>.md, immutable Markdown
   │
   ▼
Structuring extracts     ~/.lucid/processed/<id>.json — emotions,
emotions / themes /      themes, people; idempotent and rebuildable
people
   │
   ▼
Reflection proposes      one of: (a) one possible pattern, (b) "no
ONE possible pattern     pattern yet", (c) soft contradiction
   │
   ▼
"Does this resonate?"
   │
   ├── accepted  → ~/.lucid/insights/<iid>.md with provenance
   ├── nuanced   → user's refinement is canonical, same provenance
   └── rejected  → no insight; rejection appended to processed
   │
   ▼
/reflect (weekly)        lists validated insights from the past
                         week; asks "still resonating?". Read-only —
                         never proposes new patterns.
```

**Hard caps the loop enforces** (mirrored in
[`../docs/mvp/architecture.md`](../docs/mvp/architecture.md) §6 as gates):

* At most one pattern proposal per session.
* Hypothesis language only — no diagnostic phrasing.
* Each agent sees only the slice the router authorized.
* No external send, schedule, or post — no path exists.

## 4. Required commands

Five commands, one router, no menus. Defined in
[`../docs/mvp/architecture.md`](../docs/mvp/architecture.md) §2.

| Command | Behavior | Writes | Notes |
|---------|----------|--------|-------|
| `/log <text>` | Single-shot capture. No follow-ups. Acknowledges with the raw id. | `raw/<id>.md`, `sessions/<sid>.json` | Friction-free path. |
| `/checkin` | Guided capture. Intake asks 2–4 follow-ups, then bundles answers into one raw entry, then structures and proposes (at most) one pattern. | `raw/<id>.md`, `sessions/<sid>.json`, `processed/<id>.json`, optional `insights/<iid>.md` or appended `rejected_proposals[]`. | Default path for daily use. |
| `/reflect` | Weekly recall. Lists validated insights from the last 7 days, asks "still resonating?". | `reflections/<rid>.md` (append). Insight status updates via the storage adapter. | Read-and-ask only. Never proposes new patterns. |
| `/ask <q>` | Read-only Q&A grounded in validated insights and weekly reflections. Cites the records it draws from; returns `insufficient` when the slice cannot answer. | None. | Strict context-slice gate. Full contract in [`../docs/mvp/agent-contracts.md`](../docs/mvp/agent-contracts.md) §3 (`reflection.answer_grounded`). |
| `/bootstrap` | Toggles a mode where `/log` and `/checkin` write entries with explicit `occurred_at` and skip Reflection.propose until `/bootstrap done`. | Same as `/log` / `/checkin` with `bootstrap: true` frontmatter. | Used to load historical context without firing pattern proposals. |

Commands beyond this list are out of scope for the MVP.

## 5. Required storage layout

Defined in full in [`../docs/mvp/data-model.md`](../docs/mvp/data-model.md).
The MVP layout is a deliberate, lossless subset of the SQLite schema in
[`../technical-spec.md`](../technical-spec.md); the migration path is
documented in `data-model.md` §"SQLite migration path".

```
~/.lucid/
├── lucid.json              # tiny config (paths, defaults, agent versions)
├── raw/                    # immutable raw entries (.md)
│   └── 2026/05/raw_2026_05_05_19_42.md
├── processed/              # JSON extraction artifacts, one per raw entry
│   └── raw_2026_05_05_19_42.json
├── insights/               # validated insights (.md), one per accepted/nuanced
│   └── i_2026_05_05_a.md
├── people/                 # lightweight person references (.json)
│   └── person_a-river.json
├── sessions/               # thread/session metadata + channel memory
│   ├── session_2026_05_05_19_42.json
│   └── channel_lucid.md
└── reflections/            # weekly reflection records (.md)
    └── reflection_2026_w18.md
```

**Mutability rules** (binding):

| Tree | Mutability |
|------|------------|
| `raw/` | **Immutable.** No `update_raw` op. New detail = new entry referencing the original. |
| `processed/` | Rebuildable. Overwrite is allowed; each write stamps `agent_version`. |
| `insights/` | Body set on write. Status transitions append to `status_history[]` and the change log. No silent rewrites. |
| `people/` | Mutable via `storage.update_person` only. Merges new mentions. |
| `sessions/` | Append-only audit trail. |
| `reflections/` | Append-only within an ISO week. |

**Naming conventions** (binding):

| Kind | Convention |
|------|------------|
| Raw entry id | `raw_YYYY_MM_DD_HH_MM` |
| Processed artifact id | Same as raw entry id (different extension). |
| Insight id | `i_YYYY_MM_DD_<slot>` |
| Session id | `session_YYYY_MM_DD_HH_MM` |
| Person key | `person_<short-slug>` — low-signal, not the real name. |
| Reflection id | `reflection_YYYY_wWW` |

## 6. Agent / module boundaries

Six modules. Each has one charter and one input/output contract. Full
text in [`../docs/mvp/architecture.md`](../docs/mvp/architecture.md);
per-agent contracts in
[`../docs/mvp/agent-contracts.md`](../docs/mvp/agent-contracts.md).

| Module | Charter | Replaceable when |
|--------|---------|------------------|
| **Runtime harness layer** | Provide chat surface, identity, process model. OpenClaw or Hermes. | A standalone Lucid app exists. |
| **Command / router layer** | Translate slash commands into named Lucid intents. Only place that orders agents. | Never (the spine). |
| **Agent contracts** | Run individual reasoning steps. Required-now: Intake, Structuring, Reflection, Safety/Consent. Minimal-now: People (extractive). | New agents added behind contracts only. |
| **Storage adapter** | Only code that touches `~/.lucid/`. Named ops. | SQLite migration; named ops survive. |
| **Structuring / reflection pipeline** | Per-session and per-week ordering of agent + storage calls. | Never (the loop). |
| **Verification / safety gates** | Make principles enforceable in code: hypothesis-language, one-pattern-per-session, approval-before-action, context-slice, synthetic-only fixtures, public-boundary. | Never (the guardrails). |

**Required-now agents:**

| Agent | Reads | Writes |
|-------|-------|--------|
| Intake | The current thread only. | One raw entry (via storage adapter). |
| Structuring | One raw entry. | One processed artifact (via `storage.update_person` + `storage.write_processed`; the router back-fills `person_key` between the two). |
| Reflection | One processed artifact + last N processed (default 7). | A proposal in-thread; on accept, one insight. |
| Safety/Consent | The output of any other agent. | Passes, rewrites, or blocks with a flagged reason. |

**Cross-cutting agent rules** (verbatim from
[`../docs/mvp/agent-contracts.md`](../docs/mvp/agent-contracts.md) §"Cross-cutting rules"):

* No autonomous external action.
* No direct disk I/O.
* No agent-to-agent calls.
* No reading the full history.
* No hidden memory mutation.
* No diagnostic, certain, or prescriptive language.
* Stamp the agent version on every record produced.
* Synthetic-only fixtures.

## 7. Non-goals

These are explicitly out of scope for the MVP. They remain documented
in [`../vision.md`](../vision.md) and
[`../technical-spec.md`](../technical-spec.md) as the future product;
they are not in the steel thread.

* No mobile, web, or desktop Lucid app. The MVP runs through an existing
  chat/agent harness.
* No SQLite, vector search, graph memory, or memory consolidation across
  daily/weekly/monthly/yearly scales.
* No Therapist surface — the MVP is not a diagnostic tool, medical
  device, or therapy replacement, and uses hypothesis framing only.
* No Coach surface — no goals, progress, accountability, or habit
  tracking in the MVP.
* No Agent-Self surface — Lucid does not draft, send, or schedule
  external messages, calendar invites, or notifications. There is no
  "send" code path at all.
* No frameworks layer — Stoicism / NVC / IFS / etc. are deferred.
* No shared profiles or relational bridges. Single user, single host.
* No cloud sync, telemetry, or external analytics.
* No autonomous notifications. `/reflect` is invoked, not pushed.
* No multi-pattern proposals per session. The cap is one.
* No production runtime data inside the Lucid repo. `~/.lucid/` is
  outside the repo.
* No `zai` references, no private biographical detail, no real-user
  examples. Public-boundary gate enforces this.

## 8. Success metrics

The MVP succeeds when each of the following is true on a single user's
host. These are evaluable, not aspirational.

| # | Metric | How it is checked |
|---|--------|-------------------|
| S-1 | The user can run `/log` and `/checkin` and find an immutable raw entry under `~/.lucid/raw/` within the same session. | File present; frontmatter valid; body matches what was sent. |
| S-2 | Structuring produces a processed artifact for every raw entry, idempotently. | `~/.lucid/processed/<raw-id>.json` exists; rerunning the structuring pass overwrites only the artifact, never the raw entry. |
| S-3 | Reflection proposes at most one possible pattern per session, in hypothesis language. | Per-session log shows ≤1 proposal; Safety/Consent gate logs zero rewrites for diagnostic phrasing on accepted outputs. |
| S-4 | Accepted, nuanced, and rejected validation paths each produce the right artifacts (insight with provenance, insight with `nuanced_from_proposal: true`, or appended `rejected_proposals[]`). | Spot-check three sessions, one per path. |
| S-5 | `/reflect` lists validated insights from the past 7 days and updates `last_confirmed_at` / `last_softened_at` / `retired_at` on user response without creating new proposals. | Weekly reflection record present; insight frontmatter updated; no new proposal IDs. |
| S-6 | `/ask` answers a free-form question only with `citations[]` whose ids appear in the supplied slice; never proposes a new pattern; never writes any record under `~/.lucid/`. | Spot-check three `/ask` invocations over a populated store; verify `citations[]` ⊆ slice; verify `~/.lucid/` is byte-identical before and after each `/ask`. |
| S-7 | Public-boundary check passes: `grep -R "zai\|Zai\|~/projects/zai" ~/projects/lucid` returns no matches. | CI or manual grep. |
| S-8 | Diagnostic-language check passes: `grep -R "diagnos\|therapist replacement\|guarantee\|send automatically" ~/projects/lucid/docs ~/projects/lucid/specs ~/projects/lucid/README.md` shows no hits outside non-goal call-outs. | Manual grep. |
| S-9 | The user reports the loop "felt like Lucid" — voice was trusted-advisor, hypothesis framing held, no nudges arrived without invitation. | Subjective — captured in the first weekly `/reflect` after one week of real use. |

S-9 is the only subjective metric. It is the final test: the platform
can pass S-1..S-8 and still fail the product. Failing S-9 means the
loop works mechanically but does not feel like Lucid; that is a
voice/principles regression, not a code regression.

## 9. First implementation phases after docs

Mirrors the recommended path in
[`../docs/mvp/README.md`](../docs/mvp/README.md) and
[`../docs/mvp/claude-code-workflow.md`](../docs/mvp/claude-code-workflow.md).
Each phase lands as a doc diff first, then a code diff, then
verification.

1. **Scaffold `~/.lucid/`.** Create the layout in
   [`../docs/mvp/data-model.md`](../docs/mvp/data-model.md) with one
   seed file proving the tree is writable. Wire `lucid.json`. No agents
   yet.
2. **Implement `/log`.** Storage adapter + router + harness wiring.
   Returns a raw id. No structuring. No Reflection.
3. **Implement `/checkin`.** Add Intake. Bundle 2–4 follow-up answers
   into one raw entry. Still no structuring at this point.
4. **Add the structuring pass.** Implement Structuring against
   [`../docs/mvp/agent-contracts.md`](../docs/mvp/agent-contracts.md);
   write processed artifacts; integrate the People extractor.
5. **Add one insight validation flow.** Implement Reflection.propose
   plus Safety/Consent. Accepted / nuanced / rejected each produce the
   correct artifact. One pattern per session, hypothesis language only.
6. **Add the weekly reflection stub.** Implement `/reflect` as
   read-and-ask over `~/.lucid/insights/`. Append to
   `~/.lucid/reflections/`. No new proposals.
7. **Add grounded `/ask`.** Implement `Reflection.answer_grounded`
   over the validated insights and weekly reflections. Read-only,
   cites the records it draws from, returns `insufficient` when the
   slice cannot answer, and never writes.

`/bootstrap` is not a separate phase: it is a mode flag on
`lucid.json` and a frontmatter field on raw entries, wired in during
Phase 5 (the insight validation flow) because Reflection.propose has
to know to suppress proposals while `bootstrap: true`. The
post-`/bootstrap done` consolidation surface remains deferred per
[`../docs/mvp/agent-contracts.md`](../docs/mvp/agent-contracts.md)
§"Consolidation (deferred / replaced)".

Anything beyond this list — additional skills, SQLite migration,
framework selection, the Agent-Self approval gate, the consolidation
cascade — follows the same pattern: contract first, code second.

## 10. How to use this spec

* **For a coding agent:** read this page, then
  [`../docs/mvp/README.md`](../docs/mvp/README.md), then the
  doc-set link relevant to the change you are making. Follow the
  docs-first rule in
  [`../docs/mvp/claude-code-workflow.md`](../docs/mvp/claude-code-workflow.md).
  When implementing a phase, work against the test cases and
  verification commands in
  [`../docs/mvp/acceptance-criteria.md`](../docs/mvp/acceptance-criteria.md).
  When a failure path is unclear, consult
  [`../docs/mvp/error-states.md`](../docs/mvp/error-states.md) before
  inventing one.
* **For a reviewer:** every PR must trace its change back to a section
  in this spec or to a deliberate, documented change of this spec.
  "Done" means the relevant section of
  [`../docs/mvp/acceptance-criteria.md`](../docs/mvp/acceptance-criteria.md)
  passes. Anything else is scope creep.
* **For the user:** this is the contract that says what the first
  Lucid you can run will and will not do. It will feel narrow. That is
  the point — the broader product in [`../vision.md`](../vision.md)
  becomes safe to build once this loop has earned its keep.

## Source documents this scope reflects

The MVP set is anchored to the existing Lucid repo and does not
contradict it.

| Source file | What it provides |
|-------------|------------------|
| [`../README.md`](../README.md) | Emotional landing page describing the felt experience of Lucid. Preserved as-is. |
| [`../vision.md`](../vision.md) | Long-form vision: four roles, pillars, profile, frameworks, capture/understand/connect/grow loop, agent-self, future possibilities. |
| [`../technical-spec.md`](../technical-spec.md) | Reference architecture: full agent set, consolidation cycles, three-layer data model, SQLite schema, memory graph, bootstrapping. |
| [`../docs/mvp/`](../docs/mvp/) | The MVP doc set. This scope is the synthesis; the doc set is authoritative when the two disagree. |
