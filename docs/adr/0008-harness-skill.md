# ADR-0008 — Harness integration: one managed `lucid` skill

**Status:** Accepted.

## Context

ADR-0003 names the chat harness (Discord via OpenClaw or Hermes) as
surface #2: agents translate channel messages into router intents and
relay responses. That translation layer needs a concrete, versioned
artifact. The owner's orchestration layer manages agent capabilities
as **skills** (a `SKILL.md` definition per capability in a shared
registry), and the reference deployment will interact with Lucid
primarily through a dedicated channel plus such a skill. Left
unmanaged, harness integration would accrete as ad-hoc prompt glue —
unversioned, unreviewable, and impossible to keep aligned with the
binary it drives.

## Decision

Lucid ships **one first-class skill** as the harness integration:

* **Canonical home in this repo** (`skills/lucid/SKILL.md`),
  versioned with the binary it drives and declaring the minimum
  `lucid` version it speaks; harness registries install it from
  here. Instance wiring — channel IDs, schedules, witness contact —
  stays in instance configuration, never in the skill (the
  shareable-spec vs. personal-calibration split, architecture §5).
* **The skill is a translator, not a brain.** It maps messages to the
  documented router intents and shells out to the same `lucid`
  commands as any surface (ADR-0003/0007). It implements no product
  logic, composes no messages of its own (the pre-committed template
  ceiling stands), and adds no commands the docs don't name.
* **Sanctuary holds through the skill.** The agent hosting the skill
  never reads `~/.lucid/engine/`, `~/.lucid/observations/`, or
  `~/.lucid/registries/` — all access goes through the router, which
  enforces the slice contracts. Engine-side verbs (`closeout`,
  `mode`, `status`) are deterministic passthrough: an agent may relay
  them verbatim but never interprets, scores, or embellishes them,
  and no model call sits in the Engine write path (P3, P9).
* **Scheduled sends stay scheduler-owned.** Bell, tripwire, and
  heartbeat posts are driven by the harness's native scheduler (or
  `go-flywheel` standalone, ADR-0004) posting pre-committed
  templates — never agent-initiated (the no-autonomous-messages
  ceiling).

## Consequences

* The Phase-0 rule keeps its teeth end-to-end: with the agent, the
  model, or the whole harness down, `lucid closeout` at a terminal —
  or a plain line backfilled later — still completes the night (P9,
  P10, ADR-0003).
* Skill updates are doc-reviewable diffs in this repo, released in
  lockstep with the binary rather than drifting inside a private
  orchestration repo; friends' instances install the same skill
  against their own `~/.lucid/` (instance isolation).
* The public-safe boundary gains a new enforcement point: the skill
  definition is shareable spec, so it may name commands and
  templates, never channels, people, or paths outside
  `~/projects/lucid/` and `~/.lucid/`.
* If a second harness ever needs a differently-shaped artifact, it
  wraps the same router contract and lands as its own thin skill —
  the core stays surface-agnostic (ADR-0003).
