---
name: lucid
description: >
  Translate chat messages into Lucid's documented router intents and relay the
  binary's replies. Use for the Mirror (capture → structure → one
  resonance-gated pattern → recall), the Engine (one committed daily practice
  with honest accountability), and the observation micro-logs — all writing one
  user-owned, append-only Ledger under ~/.lucid/. This skill is a translator,
  not a brain: it shells out to the same `lucid` commands any surface uses and
  composes no messages of its own.
min_lucid_version: "0.1.0"
---

# Lucid

Lucid is a local-first personal operating system with two cooperating
subsystems — the **Mirror** and the **Engine** — both writing one
user-owned, append-only Ledger under `~/.lucid/`. This skill is Lucid's
harness integration (ADR-0008): the single, versioned artifact that maps a
chat message to a documented router intent and relays the reply.

## What this skill is (and is not)

* **A translator, not a brain.** It maps messages to the documented router
  intents and shells out to the same `lucid` commands as any surface. It
  implements no product logic, composes no messages of its own, and adds no
  command the docs do not name.
* **Versioned with the binary it drives.** It declares the minimum `lucid`
  version it speaks (`min_lucid_version`) and is released in lockstep with the
  binary. A registry installs it from this repo; a second user installs the
  same skill against their own `~/.lucid/`.
* **Sanctuary-preserving.** The agent hosting this skill never reads
  `~/.lucid/engine/`, `~/.lucid/observations/`, or `~/.lucid/registries/`. All
  access goes through the router, which enforces the slice contracts. No model
  call sits in any Engine or observation write path.
* **Public-safe.** This definition names commands and templates only — never
  channels, people, or paths outside `~/projects/lucid/` and `~/.lucid/`.
  Instance wiring (channel ids, schedules, witness contact) lives in instance
  configuration, never here.

## Command map

Every documented command routes through one binary. Run the command; relay
its output. Acknowledge *after* the binary persists, never before.

| Message | Router intent | How the skill drives it |
|---------|---------------|-------------------------|
| `/log <text>` | capture | `lucid log <text>` |
| `/checkin` | guided Intake → structure → ≤1 proposal | router check-in (thread-driven, provider-backed) |
| `/closeout …` | Engine close-out | `lucid closeout …` — **verbatim passthrough** |
| `/closeout skip` | honest miss | `lucid closeout skip` — **verbatim passthrough** |
| `/closeout backfill [yesterday\|<date>] [<compact>]` | correct a recent day | `lucid closeout backfill …` — **verbatim passthrough** |
| `/mode <green\|yellow\|red>` | declare today's mode | `lucid mode <…>` — **verbatim passthrough** |
| `/storm <label\|unwritten>` / `/storm end` | declare/end a storm | router storm intent |
| `/profile <name>` | switch clock profile | router profile intent |
| `/status` | read-only L0 surface | `lucid status` — **verbatim passthrough** |
| `/reflect [gate]` | weekly recall (never proposes) | router recall intent (provider-backed) |
| `/ask <question>` | grounded, cited Q&A | router grounded-answer intent (provider-backed) |
| `/person <name>` | deterministic person join | router person intent (no model) |
| `/bootstrap` / `/bootstrap done` | historical-entry mode | router bootstrap intent |
| `/pain` `/ate` `/drank` `/bm` `/mood` `/slept` `/obs <kind> …` | observation micro-log | `lucid obs <kind> …` |
| `/obs where <place>` | sticky stated location | `lucid obs where <place>` |
| `/day [date]` | read-only day view | `lucid day [date]` |
| `/packet clinician [@<date>\|all]` | clinician packet export | `lucid export packet clinician …` (post only the path) |

Commands beyond this list are out of scope for the MVP. The skill never
invents a command, an agent, or a field.

## Verbatim passthrough on Engine verbs

`closeout`, `mode`, and `status` are deterministic and agent-free. Relay their
output **verbatim** — never interpret, score, embellish, or add a celebration.
The Engine is the honest number; the skill is a wire, not a coach. Because the
Engine runs no model, a close-out completes with the agent, the model, or the
whole harness down — a plain `lucid closeout` at a terminal (or a line
backfilled later) still finishes the night.

## Scheduled sends stay scheduler-owned

The bell, the morning tripwire, and the monthly heartbeat are the only
autonomous messages, and they are the scheduler's — pre-committed templates
posted by the harness's native scheduler (or the standalone `go-flywheel`
path), never composed or initiated by this skill. The enrichment job likewise
runs on its own schedule and posts nothing. This skill only handles
user-initiated commands.

## Witness wiring (Ring 0)

The witness sees exactly one thing: the L2 escalation template, which carries
streak, mode, and storm state and **zero** journal or capacity content. The
`#lucid` channel and its session threads are invisible to the witness role;
the witness never gains a route into the Ledger. This boundary is instance
configuration — the witness contact is never named in this skill.

## Read-only verification

`lucid validate` runs the schema, public-boundary (S-7), diagnostic-language
(S-8), sanctuary, and doc-link checks read-only. A harness or a contributor
can run it any time to confirm the boundary holds; it writes nothing and never
scaffolds the Ledger.
