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

## The backlog (D)

Already designed in the docs; it does not fight the architecture.

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
3. **C — done.** CLI verbs for `storm` / `profile` / `person` / `bootstrap` (cheap,
   deterministic) → the harness drives every deterministic router intent by shelling
   out.
4. **D.** Provider adapter + serve/CLI surface for `checkin` / `reflect` / `ask`.

After **D**, chat is purely transport — B has already closed the
autonomous-send half.
