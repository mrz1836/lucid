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
min_lucid_version: "0.19.0"
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
| `/log <text>` | capture | `lucid log <text> [--day <date>]` |
| media/file attachment | media capture | `lucid attach <path> [--caption <text>] [--day <date>] [--to <kind>:<key>]...` |
| `/checkin` | guided Intake → structure → ≤1 proposal | router check-in (thread-driven, provider-backed) |
| `/closeout …` | Engine close-out | `lucid closeout …` — **verbatim passthrough** |
| `/closeout skip` | honest miss | `lucid closeout skip` — **verbatim passthrough** |
| `/closeout backfill [yesterday\|<date>] [<compact>]` | correct a recent day | `lucid closeout backfill …` — **verbatim passthrough** |
| `/mode <green\|yellow\|red>` | declare today's mode | `lucid mode <…> [--day <date>]` — **verbatim passthrough** |
| `/storm <label\|unwritten>` / `/storm end` | declare/end a storm | `lucid storm <label\|unwritten\|end> [--day <date>]` |
| `/profile <name>` | switch clock profile | `lucid profile <name>` |
| `/status` | read-only L0 surface | `lucid status` — **verbatim passthrough** |
| `/reflect [gate]` | weekly recall (never proposes) | router recall intent (provider-backed) |
| `/ask <question>` | grounded, cited Q&A | router grounded-answer intent (provider-backed) |
| `/person <name>` | deterministic person join | `lucid person <name>` (no model) |
| `/pain` `/ate` `/drank` `/bm` `/mood` `/slept` `/obs <kind> …` | observation micro-log | `lucid obs <kind> … [--day <date>]` (also an inline `@yesterday` token) |
| `/obs where <place>` | sticky stated location | `lucid obs where <place>` |
| `/day [date]` | read-only day view | `lucid day [date]` |
| `/packet clinician [@<date>\|all]` | clinician packet export | `lucid export packet clinician …` (post only the path) |
| `/stats [--last N \| --from --to]` | Ledger volume (counts only) | `lucid stats [--last N \| --from --to]` — read-only, counts only, no journal content |
| `/metrics` | derived practice metrics | `lucid metrics` — streak / adherence (trailing 30d) / days-since-anchor, read-only |
| `/reflect week` | Sunday weekly recall deep-dive | `lucid reflect week` — read-only weekly deep-dive, never writes |
| `/excavate` | select the next memory cluster to excavate | `lucid excavate` — read-only, never writes |
| `/companion [morning\|night]` | compose one companion message on demand | `lucid companion fire --mode <morning\|night>` — dry-run by default; scheduled sends stay scheduler-owned |
| `/witness` | compose the weekly witness report on demand | `lucid witness report` — dry-run by default; scheduled send scheduler-owned |

### Non-chat CLI verbs

Every other current `lucid` verb is **excluded from the chat command map** on
purpose, each for a documented reason — the MVP translator surfaces only the
conversational verbs; the rest are reached by their documented CLI forms:

| Verb | Reason it is not a chat command |
|------|---------------------------------|
| `lucid anchor` | Record days-since milestones (anchors); a consequential lifecycle verb that is echoed and confirmed rather than chat-mapped. |
| `lucid recall` | Read-only: browse the archive by era, thread, injury, or pet (never writes). |
| `lucid gallery` | Read-only: browse stored media as a date-ordered timeline (before/after), filterable by an inclusive date window and/or one linked subject (never writes). |
| `lucid memory` | Record a story from your past — backdated, linked, kept. |
| `lucid era` | Record or amend a life chapter (era). |
| `lucid injury` | Record or amend an injury in your body history. |
| `lucid thread` | Record or amend a thread you're working on. |
| `lucid pet` | Record or amend a companion in your pet registry — a first-class named referent with its own `active`/`rehomed`/`passed` lifecycle. |
| `lucid link` | Point a stored media attachment at a subject it is about — a person, injury, day, anchor, or thread — through the append-only link ledger; a retroactive curation verb reached by its CLI form. |
| `lucid unlink` | Retire a media↔subject association by appending an unlink event — nothing is destroyed, the pair just stops being live. |
| `lucid annotate` | Attach a free-text note to a media↔subject association without changing whether it is linked. |
| `lucid workout` | Recommend, log, and review your training (config-gated). |
| `lucid structure` | Structure a raw entry you did not just capture, or a window of them. |
| `lucid self` | Read and record durable facts about yourself. |
| `lucid backup` | Write the must-keep Ledger trees to a single `.tar.gz` archive; a data-safety operation rather than a conversation. |
| `lucid restore` | Rebuild a Ledger from a backup archive; destructive-adjacent and deliberately CLI-only. |
| `lucid secret` | Maintain a names-only catalog of hush secret handles (`add`/`list`/`note`/`remove`) — a handle and an optional note, never a value; `note` amends a live handle's note in place (or removes it with `--clear`), keeping its creation time, and the catalog has no reveal, fetch, or storage path. An operational/config verb reached by its CLI form. |
| `lucid init` | Scaffold the `~/.lucid/` Ledger tree. |
| `lucid update` | Self-update lucid in place from a GitHub release (alias: `upgrade`). |
| `lucid version` | Print lucid build metadata. |
| `lucid completion` | Generate the autocompletion script for the specified shell. |
| `lucid serve` | Drive the interactive `/checkin` flow over a stdin/JSON protocol. |
| `lucid bootstrap` | Toggle historical-entry (bootstrap) mode. |
| `lucid validate` | Validate the Ledger and boundary invariants (read-only). |

The skill never invents a command, an agent, or a field.

### Backdating with `--day`

Every verb that stamps a logical day reads **one** shared date grammar, so a
capture or a state record can be attributed to a prior day. The grammar and the
per-verb precision tiers are specified once in the command reference —
[Backdating with `--day`](../../docs/usage/commands.md#backdating-with---day) —
never re-decided here. In brief:

* **Which verbs carry it.** `log`, `obs`, `attach`, `mode`, `storm`, `memory`,
  `workout log`, and `closeout` (where `--day` is an alias onto `closeout
  backfill`). `obs` also accepts an inline `@yesterday` token in its value stream.
* **The grammar.** `@yesterday` / `yesterday` (the logical day before this one,
  04:00-rollover aware), `@YYYY-MM-DD` / `YYYY-MM-DD` (a civil day, taken
  literally), a partial `2014` or `2014-09` (snaps to the period's first day),
  and an optional time — `--day "@yesterday 19:30"`. A future day is always
  rejected; the real capture time is always kept as `recorded_at`.
* **Additive vs. gap-fill.** A backdated `log` / `obs` / `attach` is still an
  additive capture — it files under a prior day and overwrites nothing — so it
  runs immediately, no confirmation. `mode --day` is **gap-fill only** (never
  overwrites a mode that was declared) and `closeout --day` routes onto `closeout
  backfill` (`backfill_window_days` window, both today and the future rejected);
  both are state writes, so they follow the echo-and-confirm rule below.

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
  change no day record (a capture is additive, never an overwrite — which holds
  for a backdated `--day` capture too), so the skill assembles and runs them as
  soon as it understands the message — no confirmation step.
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
* **Shell-quote free text.** When invoking `lucid` through a shell, wrap every
  user-authored free-text argument (journal lines, captions, limiters with
  punctuation, observation notes) so punctuation such as `;`, `&`, `|`, `!`,
  parentheses, and quotes cannot be interpreted by the shell. Prefer a single
  fully quoted command argument for closeout/log text, then verify the saved
  raw id/day when the write is consequential.
* **Mirror content is never scored.** A journal line or a capture is held, not
  graded; the voice-first layer adds no judgement to what is written.
* **The Ledger is never hand-edited.** No agent touches the files under
  `~/.lucid/` directly — the store is append-only and schema'd, and the only way
  in is a `lucid` verb.

**Coverage.** The voice-first layer maps the everyday verbs plus the live
practice-lifecycle verbs `storm`, `anchor`, `profile`, and `metrics` (each a
documented verb in the [command reference](../../docs/usage/commands.md)). The
three **provider-backed** Mirror verbs are **shipped** and driven by their
documented forms: `/ask` runs `lucid ask <question…>` (grounded, cited Q&A over
your validated insights); `/reflect` runs `lucid reflect [gate]` (recall that
never proposes a new pattern); `/checkin` runs through the interactive `serve`
flow (`lucid serve`, the stdin/JSON check-in protocol). The Sunday weekly
reflection deep-dive is `lucid reflect week` — a read-only pass over the past
week that never writes (its nested `apply`, the only write path, goes through the
resonance gate and is not a chat-mapped verb).

**Companion, on demand.** The morning and night companion messages are composed
by Lucid's model provider from your own opaque prompt files and the chain's
honest live numbers — never fabricated. They fire automatically inside `lucid
scheduler run` on the bell and tripwire marks; `/companion [morning|night]`
composes one on demand via `lucid companion fire --mode <morning|night>` (dry-run
by default, so a preview sends nothing). Delivery stays scheduler-owned.

## Media attachments

When the user asks to save or log an image/file, first persist the inbound file
to a safe temporary local path if the harness only exposes it as transient media,
then run `lucid attach <path>` with an optional caption. Do not store user media
under the agent workspace as the final archive. The canonical media store is
`~/.lucid/media/`, and `lucid attach` emits the stored path, sha256, logical day,
linked raw id, and caption. Relay those fields back to the user.

When the user names what the file is *about* — a person, injury, day, anchor, or
thread — pass one repeatable `--to <kind>:<key>` per subject and `attach` links it
in the same flow (`attach --json` then also reports the `linked` subjects). Every
`--to` is validated **before** the media is written, so a malformed or
unresolvable subject saves no media, no raw entry, and no link. Those associations
are corrected afterward from the CLI, never conversationally: `lucid link` points
an existing media at more subjects, `lucid unlink` retires a pair, and `lucid
annotate` notes one — all append-only, and the stored binary is never touched.

## Verbatim passthrough on Engine verbs

`closeout`, `mode`, and `status` are deterministic and agent-free. Relay their
output **verbatim** — never interpret, score, embellish, or add a celebration.
The Engine is the honest number; the skill is a wire, not a coach. Because the
Engine runs no model, a close-out completes with the agent, the model, or the
whole harness down — a plain `lucid closeout` at a terminal (or a line
backfilled later) still finishes the night.

## Scheduled sends stay scheduler-owned

The autonomous messages are the scheduler's, never composed or initiated by this
skill: the bell and the morning tripwire (pre-committed templates), plus the
morning/night companion messages and the weekly witness report (composed by the
provider from the user's own opaque prompt files and the chain's honest live
numbers, never fabricated). All are posted by the harness's native scheduler (or
the standalone `go-flywheel` path). The `companion fire` and `witness report`
verbs above only ever preview these on demand; delivery stays scheduler-owned.
The enrichment job likewise runs on its own schedule and posts nothing. This
skill only handles user-initiated commands.

## Scheduler health (read-only diagnostics)

Before answering "is the scheduler running?", "why did morning/night not send?",
or "what fires next?", run the read-only health surface and relay its verdict —
never guess from process state:

```sh
lucid scheduler status          # calm human summary + a one-word verdict
lucid scheduler status --json   # machine-readable, with a top-level verdict field
```

It aggregates local state only — the companion enabled/disabled state, the chain
fire marks, the Engine and companion periodics (what fires next), the last delivery
receipt per window (what happened last), and a best-effort host/supervisor probe —
and rolls them into one verdict with a 3-tier exit code: `0` ok, `1` warn, `2`
error, identical in text and `--json`. It **sends nothing, renews no secret, and
reads no prompt body**, so it is safe to run any time. Relay the verdict plus the
relevant failing check; never paraphrase it into false confidence.

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
