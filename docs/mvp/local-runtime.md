# Lucid MVP — Local Runtime

This page explains **how Lucid runs locally before it has a standalone
app**. The MVP rides on top of an existing local chat/agent runtime
("harness") so the team can prove the steel thread in
[`steel-thread.md`](steel-thread.md) without first building UI, accounts,
or deployment infrastructure.

The harness is not the product. Lucid is the product. The harness is a
substrate that gives Lucid:

* a chat surface where the user can run commands,
* a way to invoke local agents and tools,
* a place to keep per-channel/per-thread context,
* and an opinionated process model for spawning sessions.

When the harness disappears (or is replaced by a bespoke Lucid app),
nothing in [`architecture.md`](architecture.md) or
[`data-model.md`](data-model.md) should need to change. The harness is
swappable; the contracts are not.

> **Integration note.** The Engine module
> ([`engine-module.md`](engine-module.md)) adds five commands
> (`/closeout`, `/closeout skip`, `/closeout backfill`, `/mode`,
> `/status`), one runtime tree
> (`~/.lucid/engine/`), and a scheduler job (the bell prompt and the
> morning tripwire). The tripwire is the single sanctioned exception to
> "nothing is ever pushed": pre-committed template sends (bell, L1, L2,
> monthly witness heartbeat), each behind a recorded consent flag. The
> observations module ([`observations-module.md`](observations-module.md))
> adds the micro-log command family plus the `/packet clinician`
> export, three runtime trees
> (`observations/`, `registries/`, `projections/`), and a second
> scheduler job — the **enrichment job**, which pushes nothing but is
> the runtime's only network client: outbound fetches solely to the
> endpoints declared per opted-in enricher in
> `observations/config.json`, through one audited adapter op.
> Everything else on this page stands.

### The privacy boundary of a chat surface (read this before choosing)

Local-first means the **system of record** is `~/.lucid/` on the
user's host — but with a chat harness, every message *in transit*
(journal lines, check-in answers) passes through the chat provider's
servers, subject to their retention and policies. For an inner-life
journal this is a real tradeoff, accepted deliberately in the MVP to
avoid building UI before the loop is proven. Mitigations, in order:
use a private, single-user server; prefer terse capture in-channel and
voice memos recorded on-device when depth is needed (transcribe
locally, `/bootstrap` the text in); keep the off-limits registry in
mind — the most sensitive material can wait for the standalone app,
which removes this boundary entirely. **Observation micro-logs are
structured health data** (`/bm 4`, `/pain 6 knee`, `/obs med …`) with
the same transit exposure — arguably worse, being machine-parseable —
so enabling the `pain`, `elimination`, or `med` kinds on a chat
harness shows the transport caveat once at enable time
([`observations-module.md`](observations-module.md), binding rule 5).
The Engine's L2 witness message never contains content, so escalation
adds no exposure. Name this tradeoff to any second user before they
start.

## Supported harnesses

Two local harnesses are supported by the MVP docs.

| Harness | Role for the MVP | Why it works |
|---------|------------------|--------------|
| **OpenClaw** | Recommended first path. Pairs cleanly with Discord-as-UI, agent identity, channel memory, and slash-command-style invocation. | Fast getting-started loop on a single host; Lucid behavior fits naturally into a per-channel agent with per-thread sessions. |
| **Hermes** | Supported alternate path. Used the same way: chat in, command routed to Lucid, agent runs locally, files written under `~/.lucid/`. | Same surface area as OpenClaw from Lucid's point of view; integration details differ but the contracts do not. |

The MVP recommends **OpenClaw + Discord** as the clearest first
implementation path. Hermes is documented as a peer option so the
architecture stays harness-agnostic and a future implementer can switch
without rewriting Lucid.

The choice between them is operational, not architectural. Lucid
modules see the harness only through a thin command/router interface
(see [`architecture.md`](architecture.md) §"Command/router layer").
Implementation-wise, everything below that interface is the `lucid`
core — one Go binary exposing the same router to the CLI and to
harness agents alike ([`../adr/0001-implementation-language.md`](../adr/0001-implementation-language.md),
[`../adr/0003-runtime-surface.md`](../adr/0003-runtime-surface.md));
the harness invokes the binary, it never reimplements Lucid logic.

## Local workspace shape

The MVP uses two cleanly separated trees:

### Repo tree — `~/projects/lucid/`

Source, docs, templates, and contracts. Public-safe. Versioned in git.
No private user data lives here.

```
~/projects/lucid/
├── README.md                    # emotional landing page
├── CLAUDE.md / AGENTS.md        # coding-agent orientation (kept identical)
├── docs/
│   ├── vision.md                # long-form vision
│   ├── architecture.md          # the canonical system design
│   ├── engine.md                # behavioral engine spec
│   ├── observations.md          # observation & enrichment layer spec
│   ├── calibration.md           # per-user setup guide (lucid init)
│   ├── technical-spec.md        # reference architecture
│   ├── adr/                     # architecture decision records
│   └── mvp/                     # this MVP doc set, incl. scope.md
├── cmd/lucid/                   # (future) Go binary entrypoint (ADR-0001/0003)
├── internal/                    # (future) router, storage adapter, engine,
│                                #          observations, gates, schedulers
├── agents/                      # (future) Intake, Structuring, Reflection,
│                                #          Safety/Consent prompt definitions
└── scripts/                     # (future) deterministic helpers:
                                 #   schema validation, fixture gen, link checks
```

`cmd/`, `internal/`, `agents/`, and `scripts/` are listed as
**future implementation directories** following standard Go project
layout ([`../adr/0001-implementation-language.md`](../adr/0001-implementation-language.md)).
They are not created in the docs phase; they exist in this layout so a
coding agent has a known home for the first build sequence in
[`claude-code-workflow.md`](claude-code-workflow.md).

The repo never holds private runtime data. The split between source
and runtime is a hard line, not a convention.

### Runtime tree — `~/.lucid/`

A host-global Lucid home. Owned by the user. Not in the repo, not in
the cloud, not in any backup the user has not deliberately set up.

```
~/.lucid/
├── raw/                         # immutable raw entries (Markdown)
├── processed/                   # JSON extraction artifacts
├── insights/                    # validated insights (Markdown)
├── people/                      # lightweight people references (JSON)
├── sessions/                    # thread/session metadata (JSON)
├── reflections/                 # weekly reflection records (Markdown)
├── engine/                      # chain config + profiles, day records, witness, storm, status
├── observations/                # frozen-envelope events (JSONL) + config
├── registries/                  # injuries, threads, places, eras
├── projections/                 # rebuildable views/exports
└── lucid.json                   # tiny config (paths, agent versions)
```

Schemas and example records for each directory live in
[`data-model.md`](data-model.md).

Two reasons this lives outside the repo:

1. **Privacy.** Runtime files contain raw inner-life content. They must
   never be committed, pushed, or synced to anything the user did not
   explicitly choose.
2. **Rebuildability.** `processed/`, `insights/`, `reflections/`,
   `engine/status.json`, and `projections/` (minus
   `projections/exports.log`, the append-only export record) can be
   rebuilt if the agents or scripts improve. The trees that must
   survive forever — the backup set — are `raw/`, `observations/`,
   `registries/`, `engine/` (minus `status.json`), and
   `projections/exports.log`: they are primary data that exists
   nowhere else.

The standalone scheduler's durable job store (`flywheel.db`) is a
third category — **disposable operational machinery, not testimony.**
Per [ADR-0004](../adr/0004-core-dependencies.md) it holds only queue
and run state, "disposable and reconstructable from config," so losing
it loses no truth: the bell and tripwire jobs re-enqueue from
`chain.json`. It is therefore no part of the record set or the backup
set above, and lives **outside** the `~/.lucid/` Ledger (default under
the OS user-config dir, overridable via `--db` / `LUCID_SCHEDULER_DB`).
`~/.lucid/` stays the exclusive home of the record.

## The helper role

Hand-editable Markdown and JSON under `~/.lucid/` are deliberate
design — and they structurally induce a human role this page must
name. **The helper** is anyone who installs, configures, repairs, or
hand-edits the host or `~/.lucid/` on the user's behalf.

Binding rules:

* **A helper holds de facto access above every aperture ring.** Root
  on the box outranks every disclosure contract in
  [`vision.md`](../vision.md) §7. Accepting help is therefore a
  **disclosure**, not a technicality — the user makes it consciously,
  like any other release.
* **Helper access is recorded** — who, when, what scope — as a line
  in the calibration amendment log
  ([`calibration.md`](../calibration.md) §"Amendment log").
* **Prefer session-shaped help.** The user present and the screen
  shared beats unattended access; unattended access is the exception
  to record, not the default to assume.
* **The witness and the helper should be different people.** A
  helper-witness voids Ring 0 as a structural boundary — presence-only
  accountability ([`engine.md`](../engine.md) §4) means nothing to
  someone who can open the files. If the two roles cannot be
  separated, that acceptance is recorded in the witness contract.

## Discord as the first UI surface

The MVP recommends Discord as the user-facing surface, routed through
OpenClaw (or Hermes). Discord is chosen because it is fast to set up,
handles threads and channels natively, and does not require building a
UI before the loop is proven.

### Channel and thread model

The MVP uses a small, deterministic mapping:

| Discord concept | Lucid use |
|-----------------|-----------|
| **Server (guild)** | The user's personal Lucid space. One server per user is enough. |
| **`#lucid` channel** | Default home for `/log`, `/checkin`, and `/reflect`. Top-level commands run here. |
| **Thread inside `#lucid`** | One Lucid **session**. A `/checkin` opens a thread; the Intake agent's follow-up questions, the user's answers, the Reflection proposal, and the user's accept/reject/nuance response all live in that thread. |
| **Pinned message in `#lucid`** | Optional. May hold a short reference card: command list, "what Lucid is and is not", and a link to the channel memory file. |

A session in Discord ≈ a thread. A session in Lucid ≈ one row in
`~/.lucid/sessions/` with the thread id, the raw entry id(s), and the
processed/insight artifacts produced. The mapping is defined in
[`data-model.md`](data-model.md).

### Channel memory concept

Each Lucid Discord channel has an associated **channel memory file** in
the user's runtime tree, e.g. `~/.lucid/sessions/<channel-id>.md`. It is
**not** a transcript and **not** a profile. It is a small, agent-readable
note containing only what the harness needs to keep the channel coherent
across sessions:

* the channel's purpose ("This channel is the Lucid `#lucid` home"),
* a one-line voice cue (links to
  [`product-principles.md`](product-principles.md) §6),
* pointers to the most recent session ids (so Reflection can pull a small
  recent window),
* the path to the user's `~/.lucid/` runtime home.

What the channel memory file is **not**:

* not a private psychological profile (those live as `~/.lucid/insights/`),
* not the LLM's full context window (that is built per-call from a
  narrow slice — see [`architecture.md`](architecture.md)),
* not synced anywhere outside `~/.lucid/`.

Treating the channel memory as a small pointer file (not a context dump)
is what keeps the harness honest with [`product-principles.md`](product-principles.md)
§3 (local-first) and §10 (verifiable scope guardrails).

### Local gateway / session routing

The harness owns the gateway:

1. The user types `/log`, `/checkin`, or `/reflect` in `#lucid` (or in
   an existing thread under `#lucid`).
2. The harness maps that to a Lucid command via the command/router
   layer in [`architecture.md`](architecture.md).
3. The router invokes the right Lucid agent (Intake, Structuring,
   Reflection, Safety/Consent) inside a local process.
4. Agent reads/writes only files under `~/.lucid/` and only the slice
   of context it is allowed to see (see
   [`agent-contracts.md`](agent-contracts.md)).
5. The harness posts the agent's response back into the same channel
   or thread.

Critical rule: **the harness never bypasses the router.** Even when a
power user wants to run an agent directly, the path goes
harness → router → agent. This is what keeps Lucid swappable across
OpenClaw and Hermes without rewriting agent code.

### Agent identity

Inside the harness, Lucid presents as a single named agent (e.g.
"Lucid"). The user sees one personality, one voice, one consistent
turn-taking pattern, regardless of which internal agent is actually
running.

* The harness is responsible for the on-screen identity (avatar,
  display name, voice constraint reminder).
* The Lucid command/router is responsible for picking which internal
  agent (Intake / Structuring / Reflection / Safety/Consent) handles
  the current turn.
* The user does **not** address internal agents by name. There is no
  "@Intake, please …" surface. The internal agent set is an
  implementation detail, not a UI concept.

This separation is what lets future versions add or rearrange internal
agents without changing the user's mental model.

## Lucid command surface

The MVP exposes three command families through one router: the five
Mirror commands below; the five Engine commands in
[`engine-module.md`](engine-module.md) (`/closeout`, `/closeout skip`,
`/closeout backfill`, `/mode`, `/status`); and the observation
commands in [`observations-module.md`](observations-module.md)
(`/pain`, `/ate`, `/drank`, `/bm`, `/mood`, `/obs`, `/day` —
deterministic one-liners, all aliasing one router intent plus one
read-only view — plus the `/packet clinician` export). The Mirror
five are listed here in priority order; the first three are the steel
thread and the last two are quality-of-life affordances.

| Command | Purpose | Stage in steel thread |
|---------|---------|-----------------------|
| `/log <text>` | Friction-free single-shot capture. Writes one immutable raw entry. | Capture (no follow-ups). |
| `/checkin` | Guided capture. Intake asks 2–4 follow-up questions in a thread, then writes one bundled raw entry. | Capture + Structure + Reflection cycle. |
| `/reflect` | Weekly recall. Lists validated insights from the past week and asks "still resonating?". Read-and-ask only. | Recall (Stage 5). |
| `/ask <question>` | Read-only query against `~/.lucid/insights/` and `~/.lucid/reflections/`. Returns surfaces, not new patterns. | Diagnostic / navigation. |
| `/bootstrap` | Enter a temporary mode for capturing past, formative entries with explicit historical timestamps. Pattern proposals are suppressed during bootstrap. Exit with `/bootstrap done`. | Optional. Mirrors the bootstrap mode in `technical-spec.md` at MVP fidelity. |

Anything not on this list is post-MVP. New commands belong in
[`claude-code-workflow.md`](claude-code-workflow.md) as "candidates,"
not in the harness as silent additions. This protects
[`product-principles.md`](product-principles.md) §2 ("one steel thread,
not a menu").

### Command shape rules

* Slash-prefixed.
* Arguments are positional and short (or omitted, in which case the
  command opens a thread for follow-up).
* Every command writes some artifact under `~/.lucid/` before
  acknowledging the user. Acknowledge after persistence, not before.
* Every command's output ends with what was written and where (e.g.
  "saved as `raw_2026_05_05_19_42`"). Provenance over magic.
* Commands never send anything outside the harness — see
  [`product-principles.md`](product-principles.md) §7. (The Engine's
  three pre-committed template sends are scheduler jobs, not commands;
  see [`engine-module.md`](engine-module.md).)

## Local cron / consolidation

The MVP does **not** require a background scheduler. It is happy with:

1. **Manual `/reflect`** as the primary recall mechanism.
2. **Optional local cron** that runs a small script once a week to:
   * regenerate `~/.lucid/processed/<id>.json` for any new raw entries
     missed by the live structuring step (idempotent),
   * write a weekly reflection record to `~/.lucid/reflections/` so
     the next `/reflect` has a fresh anchor,
   * verify directory layout and `lucid.json` integrity.

Local cron is optional because:

* The user is one person on one host. They can run `/reflect`
  themselves.
* Running structuring/reflection live keeps the loop honest. If the
  user never types `/reflect`, no insights silently accrue in the
  background.
* Adding a scheduler before there is a working loop is exactly the
  kind of premature platform-engineering [`product-principles.md`](product-principles.md)
  §8 forbids.

When cron is added, it lives in `scripts/` (in the repo) and reads/writes
only `~/.lucid/`. It never opens network sockets, never DMs the user,
never speaks for Lucid. If it has anything to say, the next `/reflect`
surfaces it.

Two other scheduler jobs have their own contracts, specified in their
module pages, sharing nothing with the optional Mirror cron above
except the scheduler itself: the **Engine tripwire**
([`engine-module.md`](engine-module.md)) exists precisely to speak
unprompted, within the pre-committed template ceiling; the
**enrichment job** ([`observations-module.md`](observations-module.md))
never speaks at all but performs the runtime's only network fetches —
outbound, read-only, allowlisted, audited.

## When the harness goes away

A standalone Lucid app is **not** an MVP requirement. It is a future
target. When that future arrives, the seam is well-defined:

| Today (harness) | Future (standalone Lucid app) |
|-----------------|-------------------------------|
| Discord channel + thread | Native chat surface (desktop or mobile, per [`vision.md`](../vision.md)). |
| OpenClaw / Hermes process model | Lucid-owned runtime with its own session model. |
| Command/router invoked by the harness | Same router, invoked directly by the app's UI. |
| Channel memory file | Same idea, owned by the app's session manager. |
| `~/.lucid/` files | Same files. The richer SQLite path in [`data-model.md`](data-model.md) becomes available; Markdown/JSON remains exportable. |

The contracts (commands, agent inputs/outputs, file layout) survive
intact. Replacing the harness should be a UI/operational change, not a
product redesign. That is the point of building the MVP this way.

## Recommendation

For the first implementation pass:

1. **Use OpenClaw with a Discord `#lucid` channel.** It is the
   shortest path to a working steel thread and it gives the team the
   threading model the loop needs.
2. **Keep the Hermes path documented as an alternate.** Anything the
   OpenClaw path cannot do generically should be flagged as a harness
   coupling and revisited.
3. **Defer the standalone app.** Building the app before the loop
   works locally is the failure mode this project is explicitly trying
   to avoid.
4. **Build for the seam.** Treat the harness as replaceable from day
   one. If a feature only works on OpenClaw, it is technical debt, not
   a feature.

The next two pages turn this runtime into a concrete module layout
([`architecture.md`](architecture.md)) and a concrete file layout
([`data-model.md`](data-model.md)).
