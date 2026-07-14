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
| media/file attachment | media capture | `lucid attach <path> [--caption <text>] [--day @yesterday|@YYYY-MM-DD]` |
| `/checkin` | guided Intake → structure → ≤1 proposal | router check-in (thread-driven, provider-backed) |
| `/closeout …` | Engine close-out | `lucid closeout …` — **verbatim passthrough** |
| `/closeout skip` | honest miss | `lucid closeout skip` — **verbatim passthrough** |
| `/closeout backfill [yesterday\|<date>] [<compact>]` | correct a recent day | `lucid closeout backfill …` — **verbatim passthrough** |
| `/mode <green\|yellow\|red>` | declare today's mode | `lucid mode <…>` — **verbatim passthrough** |
| `/storm <label\|unwritten>` / `/storm end` | declare/end a storm | `lucid storm <label\|unwritten\|end>` |
| `/profile <name>` | switch clock profile | `lucid profile <name>` |
| `/status` | read-only L0 surface | `lucid status` — **verbatim passthrough** |
| `/reflect [gate]` | weekly recall (never proposes) | router recall intent (provider-backed) |
| `/ask <question>` | grounded, cited Q&A | router grounded-answer intent (provider-backed) |
| `/person <name>` | deterministic person join | `lucid person <name>` (no model) |
| `/bootstrap` / `/bootstrap done` | historical-entry mode | `lucid bootstrap [done]` |
| `/pain` `/ate` `/drank` `/bm` `/mood` `/slept` `/obs <kind> …` | observation micro-log | `lucid obs <kind> …` |
| `/obs where <place>` | sticky stated location | `lucid obs where <place>` |
| `/day [date]` | read-only day view | `lucid day [date]` |
| `/packet clinician [@<date>\|all]` | clinician packet export | `lucid export packet clinician …` (post only the path) |

Commands beyond this list are out of scope for the MVP. The skill never
invents a command, an agent, or a field.

## Natural-language translation (voice-first)

The slash/CLI verbs above are the **canonical baseline** — precise,
deterministic, and the contract every surface shares. This skill **may** also
accept plain, spoken-style phrasing (the shape a voice-to-text message takes),
recognize the intended verb, and **assemble the documented command** from it. It
invents **no** command, field, or flag of its own: it can only assemble a verb
that already exists, and if a message maps to nothing real it says so rather than
guessing. The CLI is the contract; natural phrasing is the human interface. The
full mechanism, per-verb phrasings, and synthetic examples live in
[`../../docs/usage/natural-language.md`](../../docs/usage/natural-language.md).

**Execution posture — reads run, writes confirm.** Voice-to-text is lossy and an
Engine write lands an immutable day record, so the two verb classes are handled
differently:

* **Read verbs run immediately.** `status`, `day`, `metrics`, `log`, and `obs`
  change no day record (a capture is additive, never an overwrite), so the skill
  assembles and runs them as soon as it understands the message — no
  confirmation step.
* **State-writing verbs are echoed and confirmed.** For `closeout`,
  `closeout backfill`, `mode`, and `closeout skip`, the skill **assembles the
  compact command, shows it back, and waits for a one-word confirmation** before
  running it. The consequential lifecycle verbs `storm`, `anchor`, and `profile`
  are echoed for the same one-word confirm.

**Ask, don't guess.** When a required piece — a chain-link state, a capacity, or
an observation field — is missing or ambiguous, the skill asks **one** concise
question and waits. It never fabricates a link state, a capacity, or any value
the user did not give.

**The boundaries do not change.** This is a phrasing layer over the same core;
every guarantee holds exactly as it does on the command line:

* **Engine verbs stay deterministic and are relayed verbatim** — never scored,
  embellished, or celebrated (the verbatim-passthrough rule below applies
  unchanged).
* **Every write goes through `lucid`.** The skill only assembles and runs the
  documented command; the agent-free core performs the write and acknowledges
  after it lands. The skill never writes state itself.
* **Mirror content is never scored.** A journal line or a capture is held, not
  graded; the voice-first layer adds no judgement to what is written.
* **The Ledger is never hand-edited.** No agent touches the files under
  `~/.lucid/` directly — the store is append-only and schema'd, and the only way
  in is a `lucid` verb.

**Coverage.** The voice-first layer maps the everyday verbs plus the live
practice-lifecycle verbs `storm`, `anchor`, `profile`, and `metrics` (each a
documented verb in the [command reference](../../docs/usage/commands.md)). The
three **provider-backed** Mirror verbs — `/checkin`, `/reflect`, and `/ask` — are
**not yet wired** into this layer; their natural-language phrasing is
**deliberately deferred** until they ship, so drive them by their documented
forms until then.

## Media attachments

When the user asks to save or log an image/file, first persist the inbound file
to a safe temporary local path if the harness only exposes it as transient media,
then run `lucid attach <path>` with an optional caption. Do not store user media
under the agent workspace as the final archive. The canonical media store is
`~/.lucid/media/`, and `lucid attach` emits the stored path, sha256, logical day,
linked raw id, and caption. Relay those fields back to the user.

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
