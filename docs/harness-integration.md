# Harness integration — current state & roadmap

Lucid's core is one Go binary. A chat harness (a Discord bot, a terminal agent,
etc.) drives it by invoking the same commands any surface uses — it never
reimplements Lucid logic. This page records what is wired across the
**process boundary**: with pillars A–D landed, the full "communicate only through
chat" deployment is now reached.

It complements [`mvp/local-runtime.md`](mvp/local-runtime.md) (the harness
model) and [`mvp/build-plan.md`](mvp/build-plan.md) (Stages 5–6). Code pointers
are current as of this writing.

## A — Works today: the deterministic loop

A harness that shells out to the `lucid` binary can drive the entire
deterministic spine right now, with **no LLM provider and no secrets inside
Lucid** (the binary writes only `~/.lucid/`):

`log` · `closeout` (+ `skip` / `backfill` / `today`) · `mode` · `status` ·
`obs` (+ enabled kinds) · `day` · `anchor add` · `metrics` · `stats` · `export` / `packet` ·
`validate` · `version` · `upgrade`

That is the full nightly close-out + morning status loop — the core of a
morning/evening routine. Full syntax: [`usage/commands.md`](usage/commands.md).

**Deterministic metrics substrate.** `lucid metrics --json` is the read surface a
harness consumes for the practice's honest numbers — current/longest streak,
30-day adherence (with the 30/60/90 gate rollups), misses in window, and a
days-since count for each recorded anchor. Every number comes from the Engine
fold, so a harness (or a morning brief) renders them **without ever recomputing**
— they can't drift or be softened downstream. The record verb that feeds it is
`lucid anchor add <label> <date> [note]`, which appends a dated milestone to the
append-only anchors store; both are deterministic and provider-free, exactly like
the rest of the loop above.

## B — Works today: autonomous sends (bell, L1/L2 tripwire, monthly heartbeat)

The accountability half is now wired across the process boundary too — the bell,
the morning tripwire (L1/L2), and the monthly witness heartbeat fire on their
own, no human in the loop.

- **Driver:** a `lucid scheduler run` daemon backed by a **go-flywheel** durable
  job runtime (ADR-0004) registers the bell and the morning tripwire as
  periodics on the chain's configured clocks (`bell_time`, `tripwire_time`; the
  monthly heartbeat rides the tripwire run). The jobs are durable — a daemon
  killed mid-evening still fires the missed tripwire next morning after a
  supervised restart (bounded missed-fire catch-up). The path is deterministic
  and **agent-free**: no model, exactly like the rest of the Engine.
- **Notifier:** a concrete Discord-bot `scheduler.Notifier` resolves the logical
  `"user"` / `"witness"` channels to real Discord channel IDs and posts the
  pre-committed Engine templates via the bot REST API, reading its bot token from
  the injected `LUCID_HARNESS_TOKEN` environment (ADR-0005 — the binary stays
  credential-dumb; no real secret name lives in the repo). `"user"` routes to the
  primary Lucid channel; `"witness"` routes to a dedicated witness channel.
- **Deploy:** runs under `hush supervise` as a launchd sibling of the harness
  gateway (Stage 6), with the managed-upgrade drain window (never between the
  evening bell and the morning close-out) and the post-upgrade tripwire
  self-check unchanged.
- **Ceiling held:** the notifier sends **only** the pre-committed templates,
  received already-rendered from the Engine — it composes nothing, imports no
  model, and L2 stays content-free (streak / mode / storm only). These remain
  the *only* autonomous messages Lucid sends.

## C — Works today: deterministic router CLI verbs

The four deterministic router intents that previously had no CLI surface now ship
as `lucid` subcommands, so a harness can drive them by shelling out — still with
**no LLM provider and no secrets inside Lucid**:

`storm` (declare / renew / end an incapacity storm) · `profile` (switch the clock
profile) · `person` (deterministic person join, a pure read) · `bootstrap` (toggle
historical-entry mode).

- **Build: done.** Each verb is a thin dispatch over the existing router method
  (`Router.Storm` / `Profile` / `Person` / `Bootstrap`) — deterministic, cheap,
  provider-free, and no new product logic. Full syntax, `--json` shapes, and the
  exit-code contract: [`usage/commands.md`](usage/commands.md).
- **Machine-drivable:** all four honor `--json` (storm
  `{event,label,through,rejected}`, profile `{from,to,effective,rejected}`,
  bootstrap `{bootstrap_mode}`, person its read view). `person` always exits `0`
  (match / no-match / ambiguous / off-limits are read outcomes, never errors);
  write-verb rejections exit `1`; usage errors exit `2`.

## D — Works today: the agentic Mirror verbs (`/checkin`, `/reflect`, `/ask`)

The Mirror's reflective half is now reachable end-to-end. The router methods
(`Checkin` / `Structure` / `Validate` / `Reflect` / `Ask`) that were previously
called **only from tests** now have both a concrete model provider behind them and
an invocation surface in front of them — every agent-authored message still routed
through the Safety/Consent gate.

- **Concrete provider (ADR-0006 — no API keys).** Two `provider.Provider` backends
  ship behind a `lucid.json` **config seam** (the `provider` block — see
  [`usage/commands.md`](usage/commands.md) and [`mvp/data-model.md`](mvp/data-model.md)):
  - **Claude Code CLI** (`backend: "claude_cli"`, the zero-setup default) — a fresh
    one-shot `claude -p --output-format json --model <model>` per call (default
    `opus`), the JSON envelope's `.result` becoming the completion. On-host
    subscription OAuth; nothing to pull.
  - **Local Ollama** (`backend: "ollama"`, the full-sovereignty path) — a
    non-streaming `POST /api/chat` to the local daemon (default `qwen2.5:14b`),
    with **every call deadline-bounded** so the known binary-skew hang maps to
    `ErrTimeout` rather than waiting forever; an unreachable daemon or an unpulled
    model maps to `ErrUnavailable`.

  A single configured backend serves all four agent roles this pillar; the config
  type reserves per-role backend/model overrides so ADR-0006's per-role mandate can
  be exercised later without a contract change. The `provider.Fake` stays for tests
  — no test requires live vendor auth.
- **Invocation surface.** `/checkin` is multi-turn (Intake asks 2–4 follow-ups via
  a `Responder`, then the resonance/rule gate needs a user yes/no), so it rides a
  small **`lucid serve`** stdin/JSON line protocol that carries both the follow-ups
  and the resonance/rule confirmation, orchestrating Checkin → Structure → Validate.
  `/reflect [gate]` and `/ask <question>` are one-shot and ship as the plain
  subcommands **`lucid reflect`** and **`lucid ask`**. Full syntax:
  [`usage/commands.md`](usage/commands.md).
- **Why it matters:** the Mirror's reflective conversation is real, not just
  specified — capture → one resonance-gated pattern → recall, all through the router
  and the Safety gate, with provenance.

## Boundary caution (non-negotiable)

The harness is a **translator/relay, never the brain**
([`../skills/lucid/SKILL.md`](../skills/lucid/SKILL.md)). Every agent-authored
message must pass Lucid's Safety/Consent gate (blocklist, hypothesis rewriting,
citation grounding) and the resonance gate — so the model call for B/D lives
**inside the binary behind `provider.Provider`**, not in the harness agent. The
harness never bypasses the router; no model call ever sits in an Engine or
observation write path (architecture P3). A harness agent that freelances a
reflection and writes files directly voids these guarantees.

## Suggested sequence

1. **A — done.** The harness drives the deterministic verbs → full nightly
   close-out + morning status over chat.
2. **B — done.** Scheduler daemon + Discord-bot notifier → autonomous bell /
   tripwire / heartbeat, under `hush supervise`.
3. **C — done.** CLI verbs for `storm` / `profile` / `person` / `bootstrap` (cheap,
   deterministic) → the harness drives every deterministic router intent by shelling
   out.
4. **D — done.** Provider adapter (two backends behind the config seam) + the
   serve/CLI surface for `checkin` / `reflect` / `ask`, every message through the
   Safety gate.

With **D** landed, chat is purely transport — B already closed the autonomous-send
half, so the harness now reimplements no Lucid logic at all.
