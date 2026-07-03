# Lucid MVP — Architecture

This page is the module-level view of the MVP. It is constrained by the
principles in [`product-principles.md`](product-principles.md), driven
by the loop in [`steel-thread.md`](steel-thread.md), and grounded in
the runtime in [`local-runtime.md`](local-runtime.md).

The goal: make the architecture **boring and buildable** before it is
ambitious. Every module here can be deleted and replaced when something
better arrives, because each one has a small, explicit contract.

> **v2 note.** Two further modules exist beside the six below, both
> deterministic and agent-free, both routing their commands through
> the same router, both invisible to every agent contract on this
> page (no agent slice may include their trees; they invoke no
> agents). The **Engine module** ([`engine-module.md`](engine-module.md))
> reads/writes `~/.lucid/engine/` only. The **observations module**
> ([`observations-module.md`](observations-module.md)) owns
> `~/.lucid/observations/`, `registries/`, and `projections/`, with
> commands `/pain` `/ate` `/drank` `/bm` `/mood` `/slept` `/obs`
> `/day`. The harness scheduler runs two jobs: the Engine tripwire
> (bell + escalation) and the enrichment job (opt-in, fetch-only —
> see the §4 network exception). The module map below is unchanged
> for the Mirror thread; the six-module count and "fewer than ten
> ops" line refer to the Mirror thread only.

## Module map

```
              ┌──────────────────────────────────────────────┐
              │           Runtime harness layer              │
              │  (OpenClaw or Hermes; see local-runtime.md)  │
              └──────────────────────────────────────────────┘
                                  │
                                  ▼
              ┌──────────────────────────────────────────────┐
              │            Command / router layer            │
              │  /log /checkin /reflect /ask /bootstrap      │
              └──────────────────────────────────────────────┘
                                  │
              ┌───────────────────┼────────────────────┐
              ▼                   ▼                    ▼
       ┌────────────┐     ┌────────────────┐   ┌────────────────┐
       │   Agent    │     │   Storage      │   │  Verification/ │
       │  contracts │◄───►│   adapter      │◄──┤  safety gates  │
       │ (Intake,   │     │ (~/.lucid/)    │   │                │
       │ Structuring│     └────────────────┘   └────────────────┘
       │ Reflection,│              ▲                    ▲
       │ Safety)    │              │                    │
       └────────────┘              │                    │
              │                    │                    │
              ▼                    │                    │
   ┌────────────────────────┐      │                    │
   │ Structuring/Reflection │──────┘                    │
   │ pipeline (per session) │                           │
   └────────────────────────┘                           │
              │                                         │
              └─────────────────────────────────────────┘
```

Six modules. Each has a one-paragraph charter, a one-sentence input
contract, and a one-sentence output contract. Everything else is
implementation detail.

## 1. Runtime harness layer

**Charter.** Provide the chat surface, identity, and process model.
Receive user commands and return Lucid's responses.

**Input.** A user message in a channel or thread (Discord today,
something else later).

**Output.** A function call against the command/router layer, with the
user message and minimal channel/session context.

**MVP rules.**
* Two harnesses are supported equally at the contract level: OpenClaw
  and Hermes. See [`local-runtime.md`](local-runtime.md).
* The harness never reads or writes `~/.lucid/` directly. Only the
  storage adapter does that.
* The harness never invokes an agent directly. Only the router does
  that.
* The harness never sends anything outside the host. No DMs, no
  webhooks, no email. See [`product-principles.md`](product-principles.md)
  §7.

**Replace when.** A standalone Lucid app exists. The router and below
do not change.

## 2. Command / router layer

**Charter.** Translate a slash command into an explicit, named Lucid
intent. Pick which agent(s) handle the turn, in what order, against
which storage scope.

**Input.** `(command, args, session_context)`.

**Output.** An ordered plan: agent invocations, storage reads/writes,
and the response to surface back through the harness.

**MVP commands routed:**

| Command | Plan |
|---------|------|
| `/log <text>` | storage.write_raw(text) → ack with raw id. No agents. |
| `/checkin` | Intake.gather(session) → storage.write_raw(bundled_text) → Structuring.extract(raw_id) → Reflection.propose(processed_id, recent_window) → ack. |
| `/reflect` | storage.read_recent_insights(7d) → Reflection.surface_for_recall(insights) → ack. (No new pattern proposals.) |
| `/ask <q>` | storage.read_insights() ∪ storage.read_reflections() → Reflection.answer_grounded(q, slice) → ack. (Read-only.) |
| `/bootstrap` | Mode flag toggled in `~/.lucid/lucid.json` (`bootstrap_mode: true`); subsequent `/log` and `/checkin` write raw entries with explicit `occurred_at`, set `bootstrap: true` on the frontmatter, and skip Reflection.propose. `/bootstrap done` flips the flag back (`bootstrap_mode: false`); the MVP runs **no** consolidation pass on exit (the technical-spec consolidation cascade is deferred). The next `/checkin` after `/bootstrap done` resumes Reflection.propose normally. |
| `/closeout`, `/closeout skip`, `/mode`, `/status` | Engine module plans — deterministic, no agents. Full router plans in [`engine-module.md`](engine-module.md). `/closeout` is the one command that writes to two trees: `engine/days/` (via the engine ops) and `raw/` (the journal line, via `storage.write_raw`). |

**MVP rules.**
* The router is the only place that decides agent ordering. Agents do
  not call each other directly.
* The router is the only place that grows when new commands ship. New
  commands are added in [`claude-code-workflow.md`](claude-code-workflow.md)
  before they are added to the router.
* Every router plan has a deterministic, scriptable form: a small
  number of named ops on named storage paths. This keeps the router
  unit-testable without a live LLM.

**Replace when.** Never, in MVP. The router is the spine.

## 3. Agent contracts

**Charter.** Run individual reasoning steps. Each agent has a single,
narrow job and only the data access required for that job.

**Required-now agents.**

| Agent | Job | Reads | Writes |
|-------|-----|-------|--------|
| **Intake** | If `/checkin`, ask 2–4 follow-up questions and bundle the user's answers into one raw entry. | The current thread only. | One raw entry. |
| **Structuring** | Extract emotions, themes, people mentions from a raw entry. | One raw entry. | One processed artifact, written via `storage.update_person` (which back-fills `person_key` for each mention) followed by `storage.write_processed`. |
| **Reflection** | Three sub-modes: on `/checkin`, propose at most one possible pattern from a processed artifact + small recent window; on `/reflect`, surface validated insights for the past week (no new patterns); on `/ask`, answer a free-form question by quoting validated insights and weekly reflections only. | Per sub-mode: one processed artifact + last N processed artifacts (default 7); validated insights for the week; or `insights_slice` (cap 50) + `reflections_slice` (cap 12). | A proposal in the thread; on user accept, one insight record. On `/reflect`, status updates + append to weekly reflection. On `/ask`, a grounded answer with citations — no writes. |
| **Safety/Consent** | Gate any output that looks like diagnosis, autonomous action, or unbounded context use. | The output of any other agent. | Either passes the output through, or rewrites/blocks it with a flagged reason. |

**Minimal-now agents.**

| Agent | Job | Notes |
|-------|-----|-------|
| **People (extractive)** | When Structuring finds a person mention, normalize the name and update `~/.lucid/people/<slug>.json` with first/last seen timestamps and entry references. | No profile prompts, no relational map yet. |

**Deferred / reduced agents.**

These are named so future work has a clear seam, not implemented now.

| Agent | MVP treatment | Why |
|-------|---------------|-----|
| **Therapist** | Not implemented. | Diagnostic surface is forbidden in the MVP. |
| **Coach** | Not implemented. | Goals/progress are out of scope. |
| **Framework** | Not implemented. | One implicit voice ([`product-principles.md`](product-principles.md) §6) is enough for the MVP; framework selection is a follow-on. |
| **Consolidation** | Replaced by manual `/reflect` and an optional weekly cron. | A "dream-state" background loop is too much surface area before the live loop is proven. |
| **Agent-Self** | Not implemented. | Drafting external messages is the highest-stakes feature; it is built last, behind an explicit approval gate. |

The full contracts (purpose, inputs, outputs, allowed tools, forbidden
behavior, failure handling, validation) live in
[`agent-contracts.md`](agent-contracts.md).

**MVP rules.**
* Agents only see what the router hands them. They do not query
  storage on their own.
* Agents never write directly to disk. They return data; the storage
  adapter writes.
* Agents never call other agents. They return; the router decides
  what runs next.
* Agents never call external services. The only LLM call is the one
  the router authorizes for that step, with the slice the router
  passed in.

## 4. Storage adapter

**Charter.** Be the only code that reads or writes `~/.lucid/`. Hide
the file layout from agents and the router.

**Input.** Named ops: `write_raw`, `read_raw`, `write_processed`,
`read_processed`, `write_insight`, `read_insights`, `update_person`,
`read_session`, `write_session`, `write_reflection`, `read_reflections`.

**Output.** Records (typed) and ids; never raw filesystem handles.

**MVP rules.**
* The exact layout under `~/.lucid/` is documented in
  [`data-model.md`](data-model.md). Anything that wants to reach into
  that layout goes through this adapter.
* Raw entries are immutable: `write_raw` is append-only; there is no
  `update_raw`.
* Processed artifacts are rebuildable: `write_processed` may overwrite,
  but never silently. Each write records the agent version.
* Insights have provenance: `write_insight` requires raw entry ids,
  processed artifact id, agent prompt/version, and the user's response
  text. Without those, it raises.
* No network — with exactly one narrow exception:
  `fetch_enrichment(enricher, url)`, the single audited op through
  which opt-in enrichers query their pinned, keyless endpoints
  ([`observations-module.md`](observations-module.md) §"The enrichment
  job"). The op validates every URL against a per-enricher allowlist
  and writes the outbound audit log itself; no other op, agent, or
  module opens a socket.

**Replace when.** SQLite migration. The adapter's named ops survive;
their implementation moves from files to tables. See
[`data-model.md`](data-model.md) §"SQLite migration path".

## 5. Structuring / reflection pipeline

**Charter.** Define the per-session ordering of Structuring → Reflection
→ user validation, and the per-week ordering of recall surfaces.

This is not a separate process — it is the named sequence the router
plans for `/checkin` and `/reflect`. It is called out as its own module
because the ordering is the part that matters, and the ordering should
be obvious in code, not implicit in agent prompts.

**Per-session sequence (`/checkin`):**

1. Intake.gather → bundled text.
2. storage.write_raw(text) → raw_id.
3. Structuring.extract(raw_id) → processed payload (every `people[]`
   entry has `person_key: null`; Structuring has no read access to
   `~/.lucid/people/`).
4. For each `people[]` mention: storage.update_person(...) — the
   deterministic People routine resolves an existing slug or creates a
   new one per [`data-model.md`](data-model.md) §"Naming conventions",
   and back-fills `person_key` on the payload.
5. storage.write_processed(processed_payload) → processed_id (no
   `person_key` is `null` on disk).
6. Reflection.propose(processed_id, last_7_processed_ids) → either
   (a) one proposal, (b) "no pattern yet", or (c) soft contradiction.
7. If proposal: present in thread, await user response.
8. On accepted/nuanced: storage.write_insight(...) with provenance.
   On rejected: storage.append_rejected_proposal(processed_id, ...).
   On no answer: nothing is written.

**Per-week sequence (`/reflect`):**

1. storage.read_insights(window=7d) → list.
2. If empty: storage.read_insights(latest=2) and prompt `/log`.
3. Reflection.surface_for_recall(list) → ordered messages.
4. For each, await user "still resonating?" response.
5. On any response, storage.update_insight_status(...).

**Per-question sequence (`/ask`):**

1. storage.read_insights(status=accepted, cap=50) → insights_slice.
2. storage.read_reflections(cap=12) → reflections_slice.
3. If both slices empty: return a grounded "nothing validated yet"
   message and stop (no LLM call, no writes).
4. Reflection.answer_grounded(question, insights_slice,
   reflections_slice) → `answer` (with citations) or `insufficient`.
5. Safety.evaluate(answer_text) → outbound message.

`/ask` never writes. Citations must be a subset of the supplied
slices.

**MVP rules.**
* Reflection.propose is the only place a hypothesis is ever
  introduced. `/reflect` does not propose new patterns.
* The recent-window default (7 entries) is configurable via
  `lucid.json`, but cannot exceed a small constant in the MVP. See
  [`product-principles.md`](product-principles.md) §3.

## 6. Verification / safety gates

**Charter.** Make the safety constraints in
[`product-principles.md`](product-principles.md) §5–§7 enforceable in
code, not just in prose.

**Gates the MVP requires:**

| Gate | What it checks | Where it runs |
|------|----------------|---------------|
| **Hypothesis-language gate** | Reflection output uses hypothesis phrasing ("I noticed", "one possible pattern", "does this resonate?"). Diagnostic phrases are blocked or rewritten. | After Reflection.propose, before the harness posts. |
| **One-pattern-per-session gate** | At most one proposal per session is presented to the user. Multiple proposals collapse to the highest-confidence one or "no pattern yet". | Inside the structuring/reflection pipeline. |
| **Approval-before-action gate** | No agent-initiated external send, schedule, or post is permitted. Any draft surfaces in-thread for explicit user approval; the only autonomous *messages* are the Engine's pre-committed templates, and the only autonomous *fetches* are consented enricher queries through `fetch_enrichment` — both module-owned, neither reachable by an agent. | At the router boundary. |
| **Context-slice gate** | Agents may receive only the data the router authorized for that step (e.g. one raw entry, last N processed artifacts). The full history is never passed. | Around every LLM call. See "Mechanism" below. |
| **Synthetic-only fixtures gate** | Tests, examples, and docs reference only synthetic content. | In `scripts/` and CI checks; see [`claude-code-workflow.md`](claude-code-workflow.md). |
| **Public-boundary gate** | The Lucid repo never references private personal projects, identities, or operational paths beyond `~/projects/lucid/` and `~/.lucid/`. | A `grep` check in the verification phase. |

These gates are not optional polish. They are how the architecture
keeps the loop honest under change.

**Mechanism — how the context-slice gate is enforced.** The router
constructs a typed `AgentContext<T>` for every agent invocation,
where `T` is the explicit slice schema for that sub-mode (for
example, `AgentContext<IntakeInput>`,
`AgentContext<StructuringInput>`,
`AgentContext<ReflectionProposeInput>`,
`AgentContext<ReflectionAnswerGroundedInput>`). The agent function
signature accepts only `AgentContext<T>` — there is no global
`storage` handle, no shared session map, and no module-level
filesystem reader available to the agent code. The harness layer
also has no read access to `~/.lucid/` (it routes through the
storage adapter only). Because the only data an agent can reach is
the data the router placed in its `AgentContext<T>`, "context-slice"
is enforced by construction, not by convention. New agents must
declare a new `T` and a new router plan; that is the only way to
extend what an agent sees.

## Mapping `technical-spec.md` agents to MVP modules

The reference architecture in
[`technical-spec.md`](../../technical-spec.md) lists eight agents and a
rich skill set. The MVP keeps the names, reduces the surface, and
records the seams.

| Spec agent | MVP module | Status |
|------------|------------|--------|
| Intake Agent | Intake (agent contracts §3) | Required now. |
| Structuring Agent | Structuring (agent contracts §3) | Required now. |
| People Agent | People — extractive only (agent contracts §3) | Minimal now. Profile prompts and relational map deferred. |
| Therapist Agent | (none) | Deferred. The Safety/Consent gate enforces non-diagnostic framing in its place. |
| Coach Agent | (none) | Deferred. No goals or progress in MVP. |
| Framework Agent | (none) | Deferred. Implicit voice instead of selectable frameworks. |
| Reflection Agent | Reflection (agent contracts §3) | Required now, with a hard one-pattern-per-session cap. |
| Consolidation Agent | Manual `/reflect` + optional weekly cron | Reduced. The "nightly/weekly/monthly" cascade in the spec collapses to a single weekly recall in the MVP. |
| (added) Safety/Consent | Safety/Consent gate (agent contracts §3) | Required now even though it is not in the original spec, because it is the only thing standing between the system and a confident diagnostic engine. |

The reference skill list in `technical-spec.md` (`extract_emotions`,
`extract_people`, `extract_themes`, `match_patterns`, etc.) is also
preserved as a future seam. Skills become a directory under
`~/projects/lucid/skills/` once code exists; they are not implemented
in the docs phase.

## Extension points

These are the places future work will plug in. Naming them now keeps
each future addition a localized change, not a redesign.

| Extension | Where it slots in | What stays the same |
|-----------|-------------------|---------------------|
| **SQLite** | Storage adapter implementation. | Named ops, agent contracts, router plans. |
| **Standalone UI (desktop / mobile)** | Replace the runtime harness layer. | Everything below the harness. |
| **Voice memo capture** | New router command (`/voice`) that calls a transcription skill before `Intake.gather`. | Raw/processed/insight layout. |
| **Shared profiles (relational bridges)** | A new agent + a new storage namespace under `~/.lucid/shared/`, gated behind explicit per-relationship consent. | The local-first invariant: shared profiles are exported, not synced. |
| **Memory graph** | A new storage namespace and a new `traverse_graph` skill, fed by existing processed artifacts. | Raw layer; insight provenance. |
| **Richer frameworks (Stoicism, NVC, IFS, ...)** | New Framework agent + framework definitions under `~/projects/lucid/agents/frameworks/`. | All other agent contracts. |
| **New enrichers (air quality, wearable import, ...)** | A new entry in `observations/config.json` + an allowlist row for `fetch_enrichment`. Opt-in, outbound-minimal, keyless (a keyed source needs its own consent line). | The frozen event envelope; the fetch-audit discipline. |
| **New observation kinds / registries / projections** | One row in [`../observations.md`](../observations.md) §3, a payload schema, optionally a shorthand — per the extension model in [`../architecture.md`](../architecture.md) §4b. | The envelope, the sanctuary denylist, every agent contract. |
| **Therapist / Coach surfaces** | New named agents behind the Safety/Consent gate. Each requires its own contract in [`agent-contracts.md`](agent-contracts.md) before it ships. | One-pattern-per-session and approval-before-action gates. |

If a proposed feature does not fit one of these extension points, the
default answer for the MVP is "not yet". That is not pessimism — it is
the only way the architecture stays small enough to delete and replace.

## Why this architecture is boring on purpose

* It chooses **files over databases**, **scripts over agents**, and
  **one path over a menu**.
* Every module has fewer than ten ops.
* Every module is replaceable without redesigning anything else.
* Every safety constraint in the principles has at least one gate that
  can be checked by `grep`, by a unit test, or by a code review of a
  single function.

The next document, [`data-model.md`](data-model.md), turns the storage
adapter's named ops into a concrete file layout and record schemas. The
documents after that ([`agent-contracts.md`](agent-contracts.md),
[`claude-code-workflow.md`](claude-code-workflow.md)) turn this module
map into something a coding agent can build.
