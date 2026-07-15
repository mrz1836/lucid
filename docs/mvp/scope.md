# Lucid MVP Scope

> **Status:** build-ready (the unified nightly loop). This is the
> canonical scope contract for the first implementable Lucid steel
> thread. It synthesizes the surrounding doc set (entrypoint:
> [`README.md`](README.md)) into a single page a coding agent or
> reviewer can read in one sitting — the Mirror thread plus the
> Engine and observation modules, per
> [`../architecture.md`](../architecture.md) (the system-level
> design), [`engine-module.md`](engine-module.md), and
> [`observations-module.md`](observations-module.md).

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
([`../architecture.md`](../architecture.md) §1).

The full Lucid answer lives in [`../vision.md`](../vision.md) and
[`../architecture.md`](../architecture.md): two subsystems —
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
| Surface | Local chat thread (Discord today) via OpenClaw or Hermes — recommended path is OpenClaw + Discord. | Avoids building UI before the loop is proven; works on the phone at the bell; zero install for a second user. | [`local-runtime.md`](local-runtime.md) |
| Subsystems | Mirror thread (capture → structure → one pattern → recall) **plus** the Engine module (close-out, streaks, escalation). | The central claim is only testable with both halves live. | [`../architecture.md`](../architecture.md) §1, §8 Phase 2 |
| Engine intelligence | **None.** The Engine module is agent-free: deterministic close-out, arithmetic streaks, template escalation. | Architecture P9 (runtime never depends on AI); deterministic-scripts-first. | [`engine-module.md`](engine-module.md) |
| Observations | Agent-free micro-logs (`/pain`, `/ate`, `/drank`, `/bm`, `/mood`, `/obs`), registries, `/day` view, one opt-in enricher, first exports — phases 11–12. **Inventory, never obligation**: no observation carries a streak, target, or score. | Bakes the body/health/context record into the frozen event envelope now, so decades of views can be added later without touching the foundation. | [`observations-module.md`](observations-module.md), [`../observations.md`](../observations.md) |
| Roles | Journal + Mirror/Reflection + committed-practice accountability. Therapist-style mapping, Coach voice, and Agent-Self remain named seams. | Smallest honest proof of both halves. | [`product-principles.md`](product-principles.md) §1 |
| Capture | `/log` (free-form), `/checkin` (2–4 follow-ups), `/closeout` (nightly, deterministic) — nothing else on the prose side; observation micro-logs are the separate structured family in the row above. | Capture-first, structure-later; the close-out journal line is an ordinary raw entry. | [`product-principles.md`](product-principles.md) §4 |
| Pattern proposal | Exactly one possible pattern per session, framed as a hypothesis. | Stops the system from collapsing into a confident diagnostic engine. | [`product-principles.md`](product-principles.md) §5 |
| Storage | Markdown + JSON files under `~/.lucid/`. No SQLite, no graph, no cloud. | Boring, replaceable, lossless subset of the spec's SQLite schema. | [`data-model.md`](data-model.md) |
| Reflection cadence | Manual `/reflect`, weekly. | One cadence before four. | [`steel-thread.md`](steel-thread.md) Stage 5 |
| External action | **Messages:** none, except the pre-committed templates — bell prompt, L1 nudge (user's own channel), L2 witness escalation + monthly heartbeat (topline status only, dead-man semantics, witness-confirmed). **Fetches:** none, except opted-in enrichers — read-only, quantized coordinates + dates to pinned keyless endpoints, through one audited adapter op (S-17). | Pre-commitment is consent granted in advance (architecture P5); a system with an unwired escalation path is a suggestion; fetches are not sends. | [`engine-module.md`](engine-module.md) §"Consent amendment", [`observations-module.md`](observations-module.md) §"The enrichment job" |
| Sanctuary boundary | The Engine module reads/writes `~/.lucid/engine/` only; no read path to raw/processed/insights/people. Witness view computed from Engine data only. | Teeth attach to acts, never to content (architecture P3). | [`../engine.md`](../engine.md) §4 |
| Voice | Trusted advisor — warm, honest, non-judgmental, humble about certainty. Hypothesis language only. The Engine has no voice: fixed templates. | Encodes the only voice constraints a future agent prompt needs. | [`product-principles.md`](product-principles.md) §6 |
| Implementation | Contracts are language-agnostic; the build is **Go** — one static `lucid` binary (core + CLI), with the chat harness as a thin surface over the same router. Core dependencies: `go-flywheel` (durable job runtime for bell/tripwire/heartbeat/enrichment when installed) and `go-foundation` (base layer). Language and database lock-in are reviewed at the post-MVP retro. | Single-binary durability suits a tool that must outlive its tooling; the owner's toolchain is Go and first-party; nothing locks before the loop earns it. | [`../adr/0001-implementation-language.md`](../adr/0001-implementation-language.md), [`0003`](../adr/0003-runtime-surface.md), [`0004`](../adr/0004-core-dependencies.md) |

## 3. Steel-thread flow

Two threads, one substrate. The Mirror thread is the original steel thread
(full version with happy / rejected / no-pattern paths in
[`steel-thread.md`](steel-thread.md)); the
Engine loop wraps around it nightly.

```
        19:00  bell prompt (template)  ──►  user runs the chain in the world
                                                    │
                                                    ▼
        /closeout   link flags · mode-relative scoring · capacity digit
                    · one journal line            (≤ 2 minutes, no LLM)
                    │                     │
                    ▼                     ▼
        engine/days/<day>.json      raw/<id>.md  ──►  Structuring (async
        engine/status.json                             downstream pass) ──►
        (streaks, adherence,                           next /checkin: Reflection
         budget burn)                                  proposes ≤1 pattern →
                    │                                  "does this resonate?"
                    ▼                                  → insight or rejection
        06:00  tripwire (cron): completed → reset · 1 miss → L1 nudge
               naming the floor · 2 consecutive → L2 to witness
               (dead-man: fires on ABSENCE of a day record)

        anytime:  /log  /checkin  /mode  /status  /storm  /profile
                  /ask  /person  +  micro-logs
                  (/pain /ate /drank /bm /mood /slept /obs /day)
        weekly:   /reflect        gates/quarterly:  /reflect gate
```

**Hard caps the loop enforces** (mirrored as gates in
[`architecture.md`](architecture.md) §6 and
[`engine-module.md`](engine-module.md)):

* At most one pattern proposal per session; hypothesis language only.
* Structuring is a downstream pass and the proposal happens at the
  next `/checkin` — never inside the nightly close-out, which stays
  deterministic and agent-free end to end
  ([`agent-contracts.md`](agent-contracts.md) §3).
* Each agent sees only the slice the router authorized.
* No autonomous message beyond the pre-committed templates; no
  outbound fetch beyond opted-in enrichers through the audited adapter
  op; no LLM in any Engine or observation path.
* L2 payload contains zero bytes of journal, capacity, or Profile data.
* Mode declarations are fixed at the bell; no retroactive amendment.
* Storm entry requires witness confirmation (or a pre-named ambush
  window), is never retroactive, and a storm miss is never a stake
  event ([`../engine.md`](../engine.md) §4).

## 4. Required commands

Three command families, one router, no menus. Mirror commands are
defined in [`architecture.md`](architecture.md) §2;
Engine commands in
[`engine-module.md`](engine-module.md);
observation commands in
[`observations-module.md`](observations-module.md).

| Command | Behavior | Writes |
|---------|----------|--------|
| `/log <text>` | Single-shot capture. No follow-ups. | `raw/`, `sessions/` |
| `/checkin` | Guided capture: 2–4 follow-ups → one raw entry → structuring → ≤1 pattern proposal. | `raw/`, `sessions/`, `processed/`, optional `insights/` |
| `/closeout` | Nightly deterministic close-out: link flags, capacity, journal line. Idempotent per logical day. | `engine/days/`, `raw/` (journal line), `sessions/`, rebuilt `engine/status.json` |
| `/closeout skip` | Record an honest miss (does not suppress escalation). | `engine/days/` |
| `/closeout backfill [yesterday\|<YYYY-MM-DD>] [<compact form>]` | Create or correct the record for a recent past day (default: the most recent logical day without a completed record; window 7 days) — the chain ran but went unrecorded. Same compact form as `/closeout`; derived state recomputes over folded records; never unsends an already-fired L1/L2. | `engine/days/` (`backfilled: true` record or appended `corrections[]`), `raw/` (journal line), rebuilt `engine/status.json` |
| `/mode <green\|yellow\|red>` | Declare today's mode; rejected after bell time. | `engine/days/` |
| `/storm <clause-label\|unwritten>` / `/storm end` | Declare (or end) a storm — the pre-committed incapacity state: witness-confirmed within 72h (ambush windows enter automatically), bounded (14 days, one renewal), never retroactive. While standing: undeclared days default to Red, L1/L2 use fixed storm variants, misses spend no budget, and the stake is stayed ([`engine-module.md`](engine-module.md)). | `engine/storm.json` (append-only history), rebuilt `engine/status.json` |
| `/profile <name>` | Switch to a named clock profile (Bell, tripwire hour, rollover move together); defined at a Retro, effective from the next logical day — never the current one. | `engine/profile.json` (append-only history) |
| `/status` | Read-only ambient state: streak, mode-relative adherence co-presented with the floor-day ratio and raw days-accounted (the honest-number pairing), budget burn, days to next gate; surfaces `stake_owed` (plumbed but reserved in the MVP — no automated breach event sets it, so it never fires until L3 lands) and "witness lapsed — L2 disarmed" state ([`engine-module.md`](engine-module.md)). | None |
| `/reflect [gate]` | Weekly recall of validated insights; "still resonating?" — including whether attached rules still stand (kept/lapsed, judgment-free). The gate variant, at gate/quarterly cadence, recalls every accepted insight (cap 50) and appends the deterministic panel numbers + any person-dominance line ([`agent-contracts.md`](agent-contracts.md) §3). Never proposes new patterns. | `reflections/`, `rule_history[]` appends on `insights/` |
| `/ask <q>` | Read-only grounded Q&A over validated insights + reflections, with citations. | None |
| `/person <name>` | Read-only deterministic join, no LLM: the person record, mention counts over time, accepted insights that cite entries mentioning them, dominance share. Off-limits persons render raw record only ([`error-states.md`](error-states.md) §P-3). | None |
| `/bootstrap` | Historical-entry mode: explicit `occurred_at`, proposals suppressed until `/bootstrap done`. | as `/log`/`/checkin` |
| `/pain` `/ate` `/drank` `/bm` `/mood` `/obs <kind>` | Observation micro-logs — one line, deterministic, sub-second, no LLM. All alias one router intent. | `observations/` (+ `registries/` on match) |
| `/obs where <place>` | Sticky stated location (feeds enrichers; never device-derived). | `observations/`, `registries/places/` |
| `/day [date]` | Read-only joined day view: engine record + observations + enrichment + entry list. | None |
| `/packet clinician [@<date>\|all]` | Render the clinician packet projection; post only its path. Window: since the last packet export (first-ever: trailing 90 days); `@<date>` overrides the window start, `all` exports everything. | `projections/` (packet + one line appended to `projections/exports.log`) |

Commands beyond this list are out of scope for the MVP. Three
families, one router: the Mirror six, the Engine seven, the
observation micro-logs plus the packet export (phases 11–12).

## 5. Required storage layout

Defined in [`data-model.md`](data-model.md)
(Mirror trees, naming conventions, TZ and collision rules — all
unchanged), [`engine-module.md`](engine-module.md)
(Engine tree and schemas), and
[`observations-module.md`](observations-module.md)
(observations, registries, and projections trees and schemas).

```
~/.lucid/
├── lucid.json               # tiny config
├── raw/                     # immutable raw entries (.md) — incl. /closeout journal lines
├── processed/               # JSON extraction artifacts
├── insights/                # validated insights (.md)
├── people/                  # lightweight person references (.json)
├── sessions/                # session metadata + channel memory
├── reflections/             # weekly reflection records (.md)
├── engine/                  # ENGINE TREE — the only tree the Engine module touches
│   ├── chain.json           # chain config incl. clock profiles (hand-edited at Retros only)
│   ├── witness.json         # witness contract + consent record
│   ├── storm.json           # storm clauses, ambush windows, append-only history
│   ├── profile.json         # active clock profile + append-only switch history
│   ├── days/                # one record per logical day (append-only per day)
│   └── status.json          # derived streak/adherence projection (rebuildable)
├── observations/            # frozen-envelope events, JSONL per logical day
├── registries/              # long-lived referents: injuries, threads, places, eras
└── projections/             # rebuildable views/exports — deletable wholesale
                             # (exception: exports.log, the append-only export record)
```

All the data-model mutability and naming rules stand. New binding rules:
`engine/days/` is append-only per day-id (`corrections[]`, never
rewrite); `storm.json` and `profile.json` histories are append-only,
and storm clause labels are opaque — the clause text lives only in the
Charter; `status.json` must be byte-reproducible from `days/` +
`chain.json` + the storm and profile histories; `capacity` and
`limiter_tag` exist only in the engine
tree; `chain_start` is stamped once, on the first completed close-out.
Observation rules: the event envelope is frozen; JSONL lines are never
rewritten (corrections are new events via `refs.corrects`); registries
are primary, backup-critical data with append-only `status_history[]`;
`projections/` is deletable wholesale — except `projections/exports.log`,
the append-only export record, which records what has left the
instance and is not recomputable. **The backup set is `raw/`,
`observations/`, `registries/`, `engine/` (minus `status.json`), plus
`projections/exports.log`.**

## 6. Agent / module boundaries

The six Mirror-thread modules stand as specced in
[`architecture.md`](architecture.md). Two
modules are added:

| Module | Charter | Replaceable when |
|--------|---------|------------------|
| **Engine module** | Defend one committed daily chain: bell, close-out capture, logical-day and streak arithmetic, mode-relative adherence, escalation tripwire. Deterministic; agent-free; reads/writes `engine/` only. | The standalone app absorbs it; the schemas and dead-man semantics survive. |
| **Observations module** | Take inventory: micro-log parsing on the frozen event envelope, registries, the day view, the enrichment job, series/packet exports. Deterministic; agent-free; owns `observations/`, `registries/`, `projections/`. | Same — the envelope and consent rules survive any UI. |

Both sit beside the storage adapter and router — neither is an agent
contract, because neither contains a reasoning step. Two components
run on the harness scheduler rather than the router: the Engine
tripwire and the enrichment job; both use the same storage adapter
ops (the enrichment job additionally uses the single audited
`fetch_enrichment` network op).

Cross-cutting agent rules from
[`agent-contracts.md`](agent-contracts.md)
stand unchanged, with one addition: **no agent may read the `engine/`,
`observations/`, or `registries/` trees, nor any projection derived
from them** (path-prefix denylist, fail closed), and the modules may
invoke no agent. The sanctuary boundary is enforced by construction in
both directions (the same `AgentContext<T>` mechanism as the
context-slice gate).

## 7. Non-goals

Every Mirror-thread non-goal stands — no mobile/web/desktop
app, no SQLite/graph/consolidation, no Therapist surface, no
Agent-Self or send path beyond the three pre-committed templates, no
frameworks layer (designed in [`../frameworks.md`](../frameworks.md);
ships post-MVP — in the MVP the blocklist stands whole and Reflection
never frames in named-framework terms), no shared profiles, no cloud,
no multi-pattern proposals, no production data in the repo — with
these amendments:

* **Amended:** "No Coach surface" becomes: no goal trees, no progress
  celebration, no conversational accountability voice. The Engine's
  committed-practice record and escalation **are** in scope; they are
  not Coach — they have no voice at all. Insight rules are not Coach
  either: recorded testimony revisited at recall, never tracked,
  celebrated, or reminded ([`agent-contracts.md`](agent-contracts.md)
  §3).
* **Amended:** "No autonomous notifications" becomes: none except the
  three pre-committed template sends (bell, L1, L2), each behind a
  recorded consent flag, none containing Mirror content.
* **Added:** No Crux dispatch, portfolio management, gate automation,
  or Retro tooling — those remain manual practices per
  [`../engine.md`](../engine.md) in this phase. `/status`
  reports days-to-gate; humans decide gates.
* **Added:** No multi-chain support; one chain until the first gate.
* **Added:** No Tier 2 passive telemetry (screen metrics, wearables).
* **Added:** No nutrition database, calorie counting, or diet scoring —
  intake logging is inventory, permanently
  ([`../observations.md`](../observations.md) §0, §9).
* **Added:** No device-derived location, health-kit, or wearable sync;
  enrichers are opt-in and outbound-minimal (coordinates + dates only).
* **Added:** No medical advice or diagnosis anywhere; health
  projections are data for the user and their care team.
* **Added:** No agent reads observations or registries in the MVP;
  correlation and excavation surfaces are post-MVP contract diffs.
* **Added:** No Scientist tooling — the experiment lifecycle
  ([`../scientist.md`](../scientist.md)) is a manual practice at
  Retro/Gate cadence in this phase; its registry and event kinds ride
  the frozen envelope; no new commands.

## 8. Success metrics

The MVP succeeds when each of the following is true on a single
user's host. These are evaluable, not aspirational.

| # | Metric | How it is checked |
|---|--------|-------------------|
| S-1 | The user can run `/log` and `/checkin` and find an immutable raw entry under `~/.lucid/raw/` within the same session. | File present; frontmatter valid; body matches what was sent. |
| S-2 | Structuring produces a processed artifact for every raw entry, idempotently. | `~/.lucid/processed/<raw-id>.json` exists; rerunning the structuring pass overwrites only the artifact, never the raw entry. |
| S-3 | Reflection proposes at most one possible pattern per session, in hypothesis language. | Per-session log shows ≤1 proposal; Safety/Consent gate logs zero rewrites for diagnostic phrasing on accepted outputs. |
| S-4 | Accepted, nuanced, and rejected validation paths each produce the right artifacts (insight with provenance, insight with `nuanced_from_proposal: true`, or appended `rejected_proposals[]`). | Spot-check three sessions, one per path. |
| S-5 | `/reflect` lists validated insights from the past 7 days and updates `last_confirmed_at` / `last_softened_at` / `retired_at` on user response without creating new proposals. | Weekly reflection record present; insight frontmatter updated; no new proposal IDs. |
| S-6 | `/ask` answers a free-form question only with `citations[]` whose ids appear in the supplied slice; never proposes a new pattern; never writes any record under `~/.lucid/`. | Spot-check three `/ask` invocations over a populated store; verify `citations[]` ⊆ slice; verify `~/.lucid/` is byte-identical before and after each `/ask`. |
| S-7 | Public-boundary check passes: forbidden-term sweep for private integration names and private repo paths returns no matches; use self-nonmatching patterns such as `z[a]i`, `Z[a]i`, and `~/projects/z[a]i`. The sweep scans authored product content only — tooling/infra trees (`.git`, `.idea`, `vendor`, `node_modules`, `.github`, `.claude`, and the validate package's own tree) are excluded, since `.github` is re-synced weekly from an upstream framework and is not authored here. | CI or manual grep. |
| S-8 | Diagnostic-language check passes: `grep -R "guarantee\|send automatically" ~/projects/lucid/docs ~/projects/lucid/README.md` shows no hits outside non-goal call-outs. | Manual grep. |
| S-9 | The user reports the loop "felt like Lucid" — voice was trusted-advisor, hypothesis framing held, no nudges arrived without invitation. | Subjective — captured in the first weekly `/reflect` after one week of real use. S-9 is the only subjective metric and the final test: the platform can pass S-1..S-8 and still fail the product. |
| S-10 | `/closeout` completes in ≤ 2 minutes of user interaction and writes both the day record and the raw journal entry. | Prompt-count cap (links + 3); both files present and cross-referenced (`raw_entry_id`). |
| S-11 | Logical-day math is correct across the rollover boundary. | Fixture: 23:50 close-out → today; 03:50 → yesterday; 04:12 with yesterday unrecorded and today's bell not yet rung → yesterday; 04:12 with yesterday already completed → today; same-day repeat is a no-op. |
| S-12 | Adherence is mode-relative and `status.json` is deterministic. | Yellow floor-day scores 1.0; delete + rebuild reproduces the file byte-for-byte, with `corrections[]` folded in array order (last write per field wins). |
| S-13 | The tripwire fires on absence, honestly and narrowly. | Simulated clock: 1 miss → exactly one L1 naming the floor; 2 consecutive → exactly one L2; L2 payload greps clean of journal/capacity content; unconfirmed witness blocks L2; a same-morning `/closeout backfill` (or attributed close-out) landing before the tripwire run produces no L1/L2. |
| S-14 | The chain survives tooling failure. | Kill the harness at bell time; the phone-alarm fallback + next-day `corrections[]` backfill path is documented and exercised once. Priority order holds: no data-loss scenario blocks the practice. |
| S-15 | After 30 days: an honest engine record (every logical day accounted: completed, floor, missed, or Away) **and** ≥ 1 validated insight exist for the same user. | The falsifiable question in §1, checked at the first gate. |
| S-16 | Micro-logs are frictionless and judgment-free: sub-second ack, valid frozen envelope, correct logical-day attribution across the 04:00 boundary, and no evaluative language in any ack template. | Latency sample; envelope validator; boundary fixture; grep ack templates for streak/score/praise terms. |
| S-17 | Enrichment is provably minimal: every logged outbound query contains only coordinates and dates; enricher events are source-attributed and idempotent. | Grep the enricher's query log; rerun fixture. |
| S-18 | The clinician packet renders capacity/mode + pain series + med record with zero journal content by default. | Generate against fixtures; grep output for body text. |
| S-19 | The storm protocol stays the stake and keeps contact. | Fixtures: standing storm + two consecutive misses ⇒ exactly one L2 storm variant, `escalation_state: l2_fired`, zero budget spend, and no `stake_owed` ever; a declaration unconfirmed at 72h lapses; entry never annotates a day before its declaration; expiry at 14 days without renewal; consecutive-miss count resets at exit. |
| S-20 | Profiles move Bell, tripwire, and rollover together, effective from the next logical day. | Fixture: switch to a `nights` profile (rollover 12:00) after today's bell ⇒ tonight runs on the old clocks; next day an 11:00 close-out attributes to the new profile's previous logical day; the `/mode` deadline follows the active profile's bell. |
| S-21 | Rules are sanctuary: attached once, revisited only at recall, never tracked. | Fixtures: the rule prompt appears exactly once per accepted insight and never after a skip; grep every rule/recall/panel template for streak, score, praise, and shame terms — zero hits; `kept` and `lapsed` acks are byte-identical in tone (no celebratory or corrective variant exists); nothing rule-related appears on `/status` or any daily surface. |
| S-22 | `/person` and the gate panel are deterministic and off-limits-safe. | Fixtures: `/person` output is byte-identical across repeated runs on the same store; an off-limits person renders raw-record-only with the §P-3 header; the dominance line appears only above `person_dominance_threshold` and only in `/reflect gate` and `/person` — grep `/status` and daily-surface templates for dominance terms: zero hits. |

## 9. Build phases

Phases 1–7 are the Mirror thread (scaffold, `/log`, `/checkin`,
structuring, insight validation, `/reflect`, `/ask`) — see
[`acceptance-criteria.md`](acceptance-criteria.md).
The modules add:

8. **Engine scaffold + `/closeout`.** `engine/` tree, chain.json
   (incl. clock profiles), day records, rollover math and `/profile`
   switching, journal line into `raw/`.
9. **Derived status + `/mode` + `/status`.** Deterministic
   `status.json`, mode-relative adherence, budget burn, storm-aware
   scoring (undeclared storm days score against Red).
10. **Tripwire + `/storm`.** Scheduled job, bell prompt, L1/L2
    templates and their storm variants, storm declaration/confirmation
    flow, witness confirmation flow, dead-man semantics.
11. **Micro-logs + registries + `/day`.** The observation envelope,
    deterministic parsers, registry keys, the joined day view.
12. **Enrichment + exports.** Sticky location, one enricher
    (weather), series CSV, first clinician packet.

Acceptance criteria for 8–10 live in
[`engine-module.md`](engine-module.md); for
11–12 in
[`observations-module.md`](observations-module.md).
**Dependency note:** phases 8–12 depend only on phases 1–2 (phase 12
additionally needs the harness scheduler, which phase 10 also uses but
does not own). For a user whose primary failure mode is ignition, the
recommended build order is 1, 2, 8, 9, 10, 11, 12, then 3–7 — the
chain gets defended and the body record starts accumulating weeks
before the first pattern proposal, and the close-out journal lines
give Structuring a real corpus on day one. The cathedral clause binds throughout: build
hours never displace runtime execution
([`../architecture.md`](../architecture.md) §8).

## 10. How to use this spec

* **For a coding agent:** read this page, then
  [`README.md`](README.md), then the doc
  relevant to your change. Docs-first per
  [`claude-code-workflow.md`](claude-code-workflow.md);
  work against
  [`acceptance-criteria.md`](acceptance-criteria.md)
  and [`engine-module.md`](engine-module.md);
  consult [`error-states.md`](error-states.md)
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
| [`../README.md`](../../README.md) | Emotional landing page — the tease still leads — now wrapped in the standard project scaffolding (crown badges, navigation, install, command reference, docs index, security). |
| [`../vision.md`](../vision.md) | Long-form product vision: the five roles, the aperture sharing model (§7), shared profiles. |
| [`../architecture.md`](../architecture.md) | The system-level design: Mirror + Engine over one Ledger, ten principles, phased roadmap. **Where an older MVP page and the architecture disagree on scope, the architecture wins.** |
| [`../engine.md`](../engine.md) | The behavioral engine specification (chains, modes, telemetry, accountability, governance). |
| [`../calibration.md`](../calibration.md) | The calibration guide — per-user setup (`lucid init` in the packaged app); the repo carries no personal data. |
| [`../technical-spec.md`](../technical-spec.md) | Reference implementation architecture for the full system. |
| [`README.md`](README.md) | The MVP doc set. This scope is the synthesis; for conventions, the doc set is authoritative; for scope, this page + `engine-module.md` are. |
