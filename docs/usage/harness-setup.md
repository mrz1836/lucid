# Driving Lucid from a chat harness (Path A)

This is the operator guide for **Path A** in
[`../harness-integration.md`](../harness-integration.md): driving Lucid's
**deterministic loop** from a chat surface by having a harness agent shell out
to the `lucid` binary. It needs **no LLM provider and no secrets inside Lucid**.

- Harness model and privacy tradeoffs: [`../mvp/local-runtime.md`](../mvp/local-runtime.md).
- Integration contract (one managed skill): [ADR-0008](../adr/0008-harness-skill.md).

**What you get:** the full nightly close-out + morning status loop over chat —
`log`, `closeout` (+`skip`/`backfill`), `mode`, `status`, `obs`, `day`,
`export`/`packet`, `validate`. The autonomous bell/tripwire (pillar B) and the
agentic verbs `/checkin` · `/reflect` · `/ask` (pillar D) are **separate builds**
— see [`../harness-integration.md`](../harness-integration.md).

## Prerequisites

- `lucid` installed and on the harness process's `PATH` ([`install.md`](install.md)).
- `~/.lucid/` initialized (`lucid init`) — or rely on self-scaffolding.
- A chat harness (e.g. OpenClaw or Hermes) whose agent can (a) run shell
  commands and (b) load a skill definition.

## 1. Install and initialize on the host

```sh
go install github.com/mrz1836/lucid/cmd/lucid@latest   # or build from source
lucid init
lucid version
```

## 2. Make `lucid` reachable to the harness process

The harness runs the agent's shell in some process environment. Two things must
hold in **that** environment:

- **`lucid` is on `PATH`** — install it into a directory the harness already
  exposes, or add its directory to the harness's exec `PATH`.
- **`HOME` is passed through** so `~/.lucid/` resolves — or set **`LUCID_HOME`**
  explicitly for the agent.

> Gotcha: a supervised gateway often runs with a minimal environment. Confirm
> `PATH` and `HOME` (or `LUCID_HOME`) actually reach the child process, not just
> your login shell.

## 3. Load the skill and identity into an agent

- Point the harness at the canonical skill
  [`../../skills/lucid/SKILL.md`](../../skills/lucid/SKILL.md) — add its parent
  directory to the harness's skill search path, or install it into the harness's
  skill registry. It declares the minimum `lucid` version it speaks and maps
  chat messages to `lucid` commands.
- Use [`../../agents/lucid/identity.md`](../../agents/lucid/identity.md) as the
  agent's on-screen identity / system prompt — one named "Lucid" voice.
- Keep **instance wiring** — channel IDs, bot tokens, witness contact — **out of
  the repo**; it lives in your harness configuration (ADR-0008, architecture §5).

## 4. Route a channel to the agent

Dedicate a chat channel to Lucid and route it to the agent carrying the skill.
The channel → agent mapping is harness-specific.

The skill is a **translator, not a brain**: the agent runs the mapped `lucid`
command and relays the output. On the Engine verbs (`closeout`, `mode`,
`status`) it relays **verbatim** — never scores, embellishes, or celebrates.

| Chat | Runs |
|------|------|
| `/log <text>` | `lucid log <text>` |
| `/closeout …` · `skip` · `backfill …` | `lucid closeout …` |
| `/mode <green\|yellow\|red>` | `lucid mode …` |
| `/status` | `lucid status` |
| `/pain` `/ate` `/bm` `/mood` `/obs …` | `lucid obs …` |
| `/day [date]` | `lucid day …` |
| `/packet clinician …` | `lucid export packet clinician …` |

Full map: [`../../skills/lucid/SKILL.md`](../../skills/lucid/SKILL.md); full
syntax: [`commands.md`](commands.md).

## 5. Secrets posture

- **Lucid is credential-dumb.** For Path A it needs and stores **no secret** — it
  only writes `~/.lucid/`.
- **The chat/bot token belongs to the harness, not Lucid.** Store it in your
  secrets manager (e.g. `hush`) and inject it into the harness process at spawn;
  Lucid never sees it.
- Lucid needs a vaulted secret **only** when you add the **autonomous scheduler**
  (pillar B): a standalone `lucid` scheduler posting the bell/tripwire needs a
  channel-post token, vaulted and injected per
  [`../../deploy/hush/supervise.tmpl`](../../deploy/hush/supervise.tmpl) and
  [ADR-0005](../adr/0005-secrets-management.md). Not required for Path A.

## 6. Boundary rules (non-negotiable)

- **Translator, not brain** — the agent composes no messages of its own and adds
  no command the docs don't name.
- **Sanctuary (P3)** — the agent never reads `~/.lucid/engine/`,
  `~/.lucid/observations/`, or `~/.lucid/registries/`; all access is through the
  `lucid` commands.
- **Persist, then acknowledge** — never ack before the binary has written.
- **Scheduled sends stay scheduler-owned** — the bell, tripwire, and heartbeat
  are the only autonomous messages, and they're the scheduler's, never the
  agent's.

## 7. Verify end-to-end

In the dedicated channel, message the agent:

1. `/status` → the agent runs `lucid status` and relays the ambient state.
2. `/log kept the promise even though I didn't want to` → a raw entry lands.
3. `/mode green`, then `/closeout dfx 3/wrist steady session` → the day records.
4. `/obs pain 6 knee aching after the run` → a micro-log lands.

Confirm the writes appear under `~/.lucid/` (`raw/`, `engine/days/`,
`observations/`). If a message returns "command not found," revisit step 2
(`PATH`/`HOME`).

## Live provider smoke (pillar D)

> **Stub — the full runbook lands with Task 10 of the provider-adapter build
> ([`../harness-integration.md`](../harness-integration.md) §D).** This section will
> hold the **one-time manual live-smoke** that proves the two real model backends
> complete the Mirror end-to-end. It is a **manual** check: **CI never runs it** —
> ADR-0006 forbids any test requiring live vendor auth, so the automated end-to-end
> runs entirely on the `provider.Fake`.

Once filled, the smoke covers **both** backends by flipping the `lucid.json`
`provider` block:

- **Claude Code CLI** (`backend: "claude_cli"`, default) — precondition: `claude`
  OAuth live.
- **Local Ollama** (`backend: "ollama"`, `model: qwen2.5:14b`) — preconditions:
  `ollama serve` running and the model pulled.

For each backend it drives one `lucid serve` `/checkin` to a validated,
resonance-gated insight, one `lucid ask`, and one `lucid reflect`, recording the
session output (or an honest `ErrTimeout` / `ErrUnavailable` if a backend is down).
Provider block + per-verb reference: [`commands.md`](commands.md); per-backend
invocation contract: [`../adr/0006-model-access.md`](../adr/0006-model-access.md).

## Not covered here

- **Autonomous bell/tripwire → chat** (pillar B) — a build, not wiring. See
  [`../harness-integration.md`](../harness-integration.md).
- **The agentic verbs' build** (pillar D — the provider adapter and the
  `serve` / `reflect` / `ask` surface) lives in the core, not the harness wiring; its
  one-time live-smoke is the [Live provider smoke](#live-provider-smoke-pillar-d)
  section above. See [`../harness-integration.md`](../harness-integration.md) §D.
