# Lucid MVP Scope

> **Status:** build-ready, v2 (unified nightly loop). This is the
> canonical scope contract for the first implementable Lucid steel
> thread. It synthesizes the doc set in [`../docs/mvp/`](../docs/mvp/)
> into a single page a coding agent or reviewer can read in one
> sitting. v1 of this spec was Mirror-only; v2 adds the Engine module
> per [`../docs/architecture.md`](../docs/architecture.md) (the
> system-level merge) and
> [`../docs/mvp/engine-module.md`](../docs/mvp/engine-module.md).

The longer surfaces — vision, system architecture, engine spec,
principles, runtime, MVP architecture, data model, agent contracts,
and Claude Code workflow — remain authoritative in their own files.
This spec restates only what is required to start building, with
explicit links back so nothing here drifts from the underlying docs.

## 1. Problem statement

Most people have no system for understanding themselves. They forget
their own insights, repeat the same patterns, and lose the thread of
who they are becoming. And the reflection tools that exist share a
failure mode with the habit tools that exist: reflection without a
behavior layer yields insight that is never applied; behavior tracking
without a reflection layer yields compliance without understanding
([`../docs/architecture.md`](../docs/architecture.md) §1).

The full Lucid answer lives in [`../vision.md`](../vision.md) and
[`../docs/architecture.md`](../docs/architecture.md): two subsystems —
the **Mirror** (understanding) and the **Engine** (behavior) — over
one user-owned event store, under one consent model.

The MVP narrows that to a single, falsifiable question:

> Can a local-first companion **defend one committed daily practice**
> (initiate it, record it, escalate honestly when it slips) **and**
> capture the reflection that rides on it, structure it, propose one
> resonance-gated pattern, and recall it later — such that after 30
> days one user on one host has both an unbroken (or honestly
> accounted) record and at least one validated insight?

If that loop works, every other Lucid ambition becomes a follow-on.
**The MVP proves the product, not the platform.**

## 2. MVP decision summary

The MVP is one buildable steel thread, run locally, on top of an
existing chat/agent harness, with files instead of databases and one
reflection cadence instead of four. The Engine's two-minute nightly
close-out is the unifying act: one command feeds both subsystems.

| Decision | Choice | Why | Source |
|----------|--------|-----|--------|
| Surface | Local chat thread (Discord today) via OpenClaw or Hermes — recommended path is OpenClaw + Discord. | Avoids building UI before the loop is proven; works on the phone at the bell; zero install for a second user. | [`../docs/mvp/local-runtime.md`](../docs/mvp/local-runtime.md) |
| Subsystems | Mirror thread (capture → structure → one pattern → recall) **plus** the Engine module (close-out, streaks, escalation). | The central claim is only testable with both halves live. | [`../docs/architecture.md`](../docs/architecture.md) §1, §8 Phase 2 |
| Engine intelligence | **None.** The Engine module is agent-free: deterministic close-out, arithmetic streaks, template escalation. | Architecture P9 (runtime never depends on AI); deterministic-scripts-first. | [`../docs/mvp/engine-module.md`](../docs/mvp/engine-module.md) |
| Roles | Journal + Mirror/Reflection + committed-practice accountability. Therapist-style mapping, Coach voice, and Agent-Self remain named seams. | Smallest honest proof of both halves. | [`../docs/mvp/product-principles.md`](../docs/mvp/product-principles.md) §1 |
| Capture | `/log` (free-form), `/checkin` (2–4 follow-ups), `/closeout` (nightly, deterministic) — nothing else. | Capture-first, structure-later; the close-out journal line is an ordinary raw entry. | [`../docs/mvp/product-principles.md`](../docs/mvp/product-principles.md) §4 |
| Pattern proposal | Exactly one possible pattern per session, framed as a hypothesis. | Stops the system from collapsing into a confident diagnostic engine. | [`../docs/mvp/product-principles.md`](../docs/mvp/product-principles.md) §5 |
| Storage | Markdown + JSON files under `~/.lucid/`. No SQLite, no graph, no cloud. | Boring, replaceable, lossless subset of the spec's SQLite schema. | [`../docs/mvp/data-model.md`](../docs/mvp/data-model.md) |
| Reflection cadence | Manual `/reflect`, weekly. | One cadence before four. | [`../docs/mvp/steel-thread.md`](../docs/mvp/steel-thread.md) Stage 5 |
| External action | None, **except three pre-committed template sends**: the bell prompt, the L1 nudge (both to the user's own channel), and the L2 witness escalation (topline status only, dead-man semantics, witness-confirmed). | Pre-commitment is consent granted in advance (architecture P5); a system with an unwired escalation path is a suggestion. | [`../docs/mvp/engine-module.md`](../docs/mvp/engine-module.md) §"Consent amendment" |
| Sanctuary boundary | The Engine module reads/writes `~/.lucid/engine/` only; no read path to raw/processed/insights/people. Witness view computed from Engine data only. | Teeth attach to acts, never to content (architecture P3). | [`../docs/engine.md`](../docs/engine.md) §4 |
| Voice | Trusted advisor — warm, honest, non-judgmental, humble about certainty. Hypothesis language only. The Engine has no voice: fixed templates. | Encodes the only voice constraints a future agent prompt needs. | [`../docs/mvp/product-principles.md`](../docs/mvp/product-principles.md) §6 |

## 3. Steel-thread flow

Two threads, one substrate. The Mirror thread is unchanged from v1
(full version with happy / rejected / no-pattern paths in
[`../docs/mvp/steel-thread.md`](../docs/mvp/steel-thread.md)); the
Engine loop wraps around it nightly.

```
        21:30  bell prompt (template)  ──►  user runs the chain in the world
                                                    │
                                                    ▼
        /closeout   link flags · mode-relative scoring · capacity digit
                    · one journal line            (≤ 2 minutes, no LLM)
                    │                     │
                    ▼                     ▼
        engine/days/<day>.json      raw/<id>.md  ──►  Structuring ──►
        engine/status.json                             Reflection proposes ≤1
        (streaks, adherence,                           pattern → "does this
         budget burn)                                  resonate?" → insight
                    │                                  or rejection
                    ▼
        09:00  tripwire (cron): completed → reset · 1 miss → L1 nudge
               naming the floor · 2 consecutive → L2 to witness
               (dead-man: fires on ABSENCE of a day record)

        anytime:  /log  /checkin  /mode  /status  /ask     weekly: /reflect
```

**Hard caps the loop enforces** (mirrored as gates in
[`../docs/mvp/architecture.md`](../docs/mvp/architecture.md) §6 and
[`../docs/mvp/engine-module.md`](../docs/mvp/engine-module.md)):

* At most one pattern proposal per session; hypothesis language only.
* Each agent sees only the slice the router authorized.
* No external send beyond the three pre-committed templates; no LLM in
  any Engine path.
* L2 payload contains zero bytes of journal, capacity, or Profile data.
* Mode declarations are fixed at the bell; no retroactive amendment.

## 4. Required commands

Nine commands, one router, no menus. Mirror commands are defined in
[`../docs/mvp/architecture.md`](../docs/mvp/architecture.md) §2; Engine
commands in [`../docs/mvp/engine-module.md`](../docs/mvp/engine-module.md).

| Command | Behavior | Writes |
|---------|----------|--------|
| `/log <text>` | Single-shot capture. No follow-ups. | `raw/`, `sessions/` |
| `/checkin` | Guided capture: 2–4 follow-ups → one raw entry → structuring → ≤1 pattern proposal. | `raw/`, `sessions/`, `processed/`, optional `insights/` |
| `/closeout` | Nightly deterministic close-out: link flags, capacity, journal line. Idempotent per logical day. | `engine/days/`, `raw/` (journal line), `sessions/`, rebuilt `engine/status.json` |
| `/closeout skip` | Record an honest miss (does not suppress escalation). | `engine/days/` |
| `/mode <green\|yellow\|red>` | Declare today's mode; rejected after bell time. | `engine/days/` |
| `/status` | Read-only ambient state: streak, mode-relative adherence, budget burn, days to next gate. | None |
| `/reflect` | Weekly recall of validated insights; "still resonating?". Never proposes new patterns. | `reflections/` |
| `/ask <q>` | Read-only grounded Q&A over validated insights + reflections, with citations. | None |
| `/bootstrap` | Historical-entry mode: explicit `occurred_at`, proposals suppressed until `/bootstrap done`. | as `/log`/`/checkin` |

Commands beyond this list are out of scope for the MVP.

## 5. Required storage layout

Defined in [`../docs/mvp/data-model.md`](../docs/mvp/data-model.md)
(Mirror trees, naming conventions, TZ and collision rules — all
unchanged) plus [`../docs/mvp/engine-module.md`](../docs/mvp/engine-module.md)
(Engine tree and schemas).

```
~/.lucid/
├── lucid.json               # tiny config
├── raw/                     # immutable raw entries (.md) — incl. /closeout journal lines
├── processed/               # JSON extraction artifacts
├── insights/                # validated insights (.md)
├── people/                  # lightweight person references (.json)
├── sessions/                # session metadata + channel memory
├── reflections/             # weekly reflection records (.md)
└── engine/                  # ENGINE TREE — the only tree the Engine module touches
    ├── chain.json           # chain config (hand-edited at Retros only)
    ├── witness.json         # witness contract + consent record
    ├── days/                # one record per logical day (append-only per day)
    └── status.json          # derived streak/adherence projection (rebuildable)
```

All v1 mutability and naming rules stand. New binding rules:
`engine/days/` is append-only per day-id (`corrections[]`, never
rewrite); `status.json` must be byte-reproducible from `days/` +
`chain.json`; `capacity` and `limiter_tag` exist only in the engine
tree; `chain_start` is stamped once, on the first completed close-out.

## 6. Agent / module boundaries

The six v1 modules stand as specced in
[`../docs/mvp/architecture.md`](../docs/mvp/architecture.md). One
module is added:

| Module | Charter | Replaceable when |
|--------|---------|------------------|
| **Engine module** | Defend one committed daily chain: bell, close-out capture, logical-day and streak arithmetic, mode-relative adherence, escalation tripwire. Deterministic; agent-free; reads/writes `engine/` only. | The standalone app absorbs it; the schemas and dead-man semantics survive. |

The Engine module sits beside the storage adapter and router — it is
**not** an agent contract, because it contains no reasoning step. The
scheduled tripwire is the one component the router does not order; it
runs on the harness scheduler and uses the same storage adapter ops.

Cross-cutting agent rules from
[`../docs/mvp/agent-contracts.md`](../docs/mvp/agent-contracts.md)
stand unchanged, with one addition: **no agent may read the
`engine/` tree**, and the Engine module may invoke no agent. The
sanctuary boundary is enforced by construction in both directions
(the same `AgentContext<T>` mechanism as the context-slice gate).

## 7. Non-goals

Everything in the v1 non-goals list stands — no mobile/web/desktop
app, no SQLite/graph/consolidation, no Therapist surface, no
Agent-Self or send path beyond the three pre-committed templates, no
frameworks layer, no shared profiles, no cloud, no multi-pattern
proposals, no production data in the repo — with these amendments:

* **Amended:** "No Coach surface" becomes: no goal trees, no progress
  celebration, no conversational accountability voice. The Engine's
  committed-practice record and escalation **are** in scope; they are
  not Coach — they have no voice at all.
* **Amended:** "No autonomous notifications" becomes: none except the
  three pre-committed template sends (bell, L1, L2), each behind a
  recorded consent flag, none containing Mirror content.
* **Added:** No Crux dispatch, portfolio management, gate automation,
  or Retro tooling — those remain manual practices per
  [`../docs/engine.md`](../docs/engine.md) in this phase. `/status`
  reports days-to-gate; humans decide gates.
* **Added:** No multi-chain support; one chain until the first gate.
* **Added:** No Tier 2 passive telemetry (screen metrics, wearables).

## 8. Success metrics

S-1 … S-9 from v1 stand verbatim (raw-entry integrity, idempotent
structuring, one-pattern cap, three validation paths, weekly recall,
grounded `/ask`, public-boundary and diagnostic-language greps, and
S-9 "felt like Lucid"). v2 adds:

| # | Metric | How it is checked |
|---|--------|-------------------|
| S-10 | `/closeout` completes in ≤ 2 minutes of user interaction and writes both the day record and the raw journal entry. | Prompt-count cap (links + 3); both files present and cross-referenced (`raw_entry_id`). |
| S-11 | Logical-day math is correct across the rollover boundary. | Fixture: 23:50 close-out → today; 03:50 → yesterday; same-day repeat is a no-op. |
| S-12 | Adherence is mode-relative and `status.json` is deterministic. | Yellow floor-day scores 1.0; delete + rebuild reproduces the file byte-for-byte. |
| S-13 | The tripwire fires on absence, honestly and narrowly. | Simulated clock: 1 miss → exactly one L1 naming the floor; 2 consecutive → exactly one L2; L2 payload greps clean of journal/capacity content; unconfirmed witness blocks L2. |
| S-14 | The chain survives tooling failure. | Kill the harness at bell time; the phone-alarm fallback + next-day `corrections[]` backfill path is documented and exercised once. Priority order holds: no data-loss scenario blocks the practice. |
| S-15 | After 30 days: an honest engine record (every logical day accounted: completed, floor, missed, or Away) **and** ≥ 1 validated insight exist for the same user. | The falsifiable question in §1, checked at the first gate. |

## 9. Build phases

Phases 1–7 are unchanged from v1 (scaffold, `/log`, `/checkin`,
structuring, insight validation, `/reflect`, `/ask`) — see
[`../docs/mvp/acceptance-criteria.md`](../docs/mvp/acceptance-criteria.md).
v2 adds:

8. **Engine scaffold + `/closeout`.** `engine/` tree, chain.json,
   day records, rollover math, journal line into `raw/`.
9. **Derived status + `/mode` + `/status`.** Deterministic
   `status.json`, mode-relative adherence, budget burn.
10. **Tripwire.** Scheduled job, bell prompt, L1/L2 templates,
    witness confirmation flow, dead-man semantics.

Acceptance criteria for 8–10 live in
[`../docs/mvp/engine-module.md`](../docs/mvp/engine-module.md).
**Dependency note:** phases 8–10 depend only on phases 1–2. For a
user whose primary failure mode is ignition, the recommended build
order is 1, 2, 8, 9, 10, then 3–7 — the chain gets defended weeks
before the first pattern proposal, and the accumulating close-out
journal lines give Structuring a real corpus on day one. The
cathedral clause binds throughout: build hours never displace runtime
execution ([`../docs/architecture.md`](../docs/architecture.md) §8).

## 10. How to use this spec

* **For a coding agent:** read this page, then
  [`../docs/mvp/README.md`](../docs/mvp/README.md), then the doc
  relevant to your change. Docs-first per
  [`../docs/mvp/claude-code-workflow.md`](../docs/mvp/claude-code-workflow.md);
  work against
  [`../docs/mvp/acceptance-criteria.md`](../docs/mvp/acceptance-criteria.md)
  and [`../docs/mvp/engine-module.md`](../docs/mvp/engine-module.md);
  consult [`../docs/mvp/error-states.md`](../docs/mvp/error-states.md)
  before inventing a failure path.
* **For a reviewer:** every PR traces to a section of this spec or a
  documented change to it. "Done" means the relevant acceptance
  criteria pass. Anything else is scope creep.
* **For the user:** this is the contract for the first Lucid you can
  run. It defends one practice and validates one pattern at a time.
  It will feel narrow. That is the point.

## Source documents this scope reflects

| Source file | What it provides |
|-------------|------------------|
| [`../README.md`](../README.md) | Emotional landing page. Preserved as-is. |
| [`../vision.md`](../vision.md) | Long-form product vision (the Mirror half's origin). |
| [`../docs/architecture.md`](../docs/architecture.md) | The system-level merge: Mirror + Engine over one Ledger, ten principles, phased roadmap. **Where v1 docs and this architecture disagree on scope, the architecture wins.** |
| [`../docs/engine.md`](../docs/engine.md) | The behavioral engine specification (chains, modes, telemetry, accountability, governance). |
| [`../docs/instance-template.md`](../docs/instance-template.md) | Per-user calibration template; the repo carries no instance data. |
| [`../technical-spec.md`](../technical-spec.md) | Reference implementation architecture for the full system. |
| [`../docs/mvp/`](../docs/mvp/) | The MVP doc set. This scope is the synthesis; for conventions, the doc set is authoritative; for scope, this page + `engine-module.md` are. |
