# Harness integration — current state & roadmap

Lucid's core is one Go binary. A chat harness (a Discord bot, a terminal agent,
etc.) drives it by invoking the same commands any surface uses — it never
reimplements Lucid logic. This page records what is wired across the
**process boundary** today and the backlog to reach a full "communicate only
through chat" deployment.

It complements [`mvp/local-runtime.md`](mvp/local-runtime.md) (the harness
model) and [`mvp/build-plan.md`](mvp/build-plan.md) (Stages 5–6). Code pointers
are current as of this writing.

## A — Works today: the deterministic loop

A harness that shells out to the `lucid` binary can drive the entire
deterministic spine right now, with **no LLM provider and no secrets inside
Lucid** (the binary writes only `~/.lucid/`):

`log` · `closeout` (+ `skip` / `backfill` / `today`) · `mode` · `status` ·
`obs` (+ enabled kinds) · `day` · `anchor add` · `metrics` · `export` / `packet` ·
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

## The backlog (C, D)

Each item is already designed in the docs; none fights the architecture.

### C — Deterministic router intents with no CLI surface

- **State:** `/storm`, `/profile`, `/person`, `/bootstrap` are implemented as
  router methods but have **no CLI verb**, so a harness can't drive them by
  shelling out. No model needed. `/storm` (stays the stake during incapacity)
  and `/profile` (switch clocks) are operationally important.
- **Build:** add `lucid storm | profile | person | bootstrap` subcommands that
  call the existing router methods. Deterministic, cheap, no provider.

### D — Agentic Mirror verbs (`/checkin`, `/reflect`, `/ask`)

- **State:** the agent code is implemented and tested, but the router methods
  (`Checkin` / `Reflect` / `Ask` / `Structure` / …) are called **only from
  tests**; there is no CLI/serve/RPC surface exposing them, and **no concrete LLM
  provider** (`internal/provider` is the interface + a test fake only).
- **Build:** (a) a concrete `provider.Provider` — per ADR-0006, an OAuth'd vendor
  CLI or a local model, **no API keys**; (b) an invocation surface. `/checkin` is
  multi-turn (Intake asks 2–4 follow-ups via a `Responder`), so a small
  `lucid serve` stdin/JSON protocol fits it better than a one-shot verb;
  `/reflect` and `/ask` are one-shot and can be plain subcommands.
- **Why it matters:** the Mirror's reflective conversation (capture → one
  resonance-gated pattern → recall).

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
3. **C.** CLI verbs for `storm` / `profile` / `person` / `bootstrap` (cheap,
   deterministic).
4. **D.** Provider adapter + serve/CLI surface for `checkin` / `reflect` / `ask`.

After **D**, chat is purely transport — B has already closed the
autonomous-send half.
