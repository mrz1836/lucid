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
`obs` (+ enabled kinds) · `day` · `export` / `packet` · `validate` · `version` ·
`upgrade`

That is the full nightly close-out + morning status loop — the core of a
morning/evening routine. Full syntax: [`usage/commands.md`](usage/commands.md).

## The backlog (B, C, D)

Each item is already designed in the docs; none fights the architecture.

### B — Autonomous sends (bell, L1/L2 tripwire, monthly heartbeat) → chat

- **State:** the scheduler logic is complete and correct (`internal/scheduler`),
  but it has **no production driver** (called only from tests and the
  managed-upgrade self-check) and **no concrete `Notifier`** (only the no-op
  `selfCheckNotifier` in `internal/cli/upgrade.go`). `deploy/hush/supervise.tmpl`
  already anticipates a standalone scheduler reading the harness token from a
  vault-injected environment.
- **Build:** a scheduler daemon entry point (a `lucid` scheduler subcommand, or a
  `go-flywheel` job per ADR-0004) **+** a concrete `scheduler.Notifier` that
  posts to the chat channel using the harness token (vaulted per ADR-0005). Run
  it under `hush supervise` (Stage 6).
- **Why it matters:** the accountability half — the bell and tripwire that make
  the practice real rather than self-service. This is the only backlog item that
  needs a secret inside Lucid (the channel-post token).

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

1. **A — today.** Wire the harness to the deterministic verbs → full nightly
   close-out + morning status over chat, immediately.
2. **B.** Scheduler daemon + chat notifier → autonomous bell / tripwire /
   heartbeat.
3. **C.** CLI verbs for `storm` / `profile` / `person` / `bootstrap` (cheap,
   deterministic).
4. **D.** Provider adapter + serve/CLI surface for `checkin` / `reflect` / `ask`.

After **B + D**, chat is purely transport.
