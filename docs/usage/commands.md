# Command reference

Every way to drive Lucid, in one place. Two surfaces:

- [**CLI commands**](#cli-commands) — the `lucid` binary's verbs, run in a
  terminal. Deterministic and agent-free unless noted.
- [**Chat/harness slash commands**](#chatharness-slash-commands) — extra verbs
  available only through a chat harness with the Lucid skill installed.

See [`install.md`](install.md) to get the binary and [`getting-started.md`](getting-started.md)
for the daily flow.

## Global conventions

- **`--json`** is a persistent flag on every command: *"Emit machine-readable
  JSON output where supported."* Commands that have a JSON shape emit it; the
  purely human-first ones (`mode`, `log`, `closeout`, `obs`) ignore it and print
  prose.
- **Exit codes** (stable, so scripts and supervised ops can branch on them):

  | Code | Meaning |
  |------|---------|
  | `0` | success |
  | `1` | runtime error, or a breached gate (`validate` found errors, `mode` was rejected) |
  | `2` | usage / flag-parse error (unknown command, bad flag) |

- **Ledger location** is `~/.lucid/`, overridable with the `LUCID_HOME`
  environment variable (see [Environment variables](#environment-variables)).
- **Provenance over magic:** stateful commands acknowledge only *after* the
  write lands, and say what was written.

## CLI commands

### init

```
lucid init [--json]
```

Scaffold the `~/.lucid/` Ledger tree (directories `0700`, files `0600`) and
write a default `lucid.json`. Idempotent — a second run makes no changes.
`--json` emits `{home, created_dirs, wrote_config, warnings}`. Most other
commands self-scaffold, so `init` is optional.

```sh
lucid init
LUCID_HOME=/tmp/scratch lucid init --json
```

### log

```
lucid log [text]
```

Capture `text` as one immutable raw entry under `~/.lucid/raw/`, with a
sub-second acknowledgement. Capture-only: nothing is written under `processed/`
or `insights/`. Scaffolds on first use.

```sh
lucid log "shower thought about the knee-and-weather thing"
```

### closeout

```
lucid closeout [today|skip|backfill] [compact form...]
```

Record the day's committed practice. Deterministic, agent-free, prose output.
This one command writes both the Engine day record (`engine/days/…`) and the
Mirror journal line (`raw/…`), then rebuilds `engine/status.json`. Sub-forms:

| Form | Meaning |
|------|---------|
| `lucid closeout <chars> <cap>[/<tag>] <journal>` | Compact close-out for the current logical day. |
| `lucid closeout today <chars> <cap> <journal>` | Force attribution to the *current* logical day (overrides the previous-day rule near the rollover). |
| `lucid closeout skip` | Record an honest miss (a real zero, distinct from silence). No makeup work is ever owed. |
| `lucid closeout backfill [yesterday\|YYYY-MM-DD] <chars> <cap>[/<tag>] <journal>` | Create or correct a recent day (7-day window) that ran but went unrecorded. Never unsends an already-fired L1/L2. |

**Compact grammar** — `<status-chars> <capacity>[/<limiter>] <journal line>`:
- `<status-chars>`: one character per link in the chain, in order — **d**one,
  **f**loor, **x** skipped (`dfx` = a three-link chain: done, floored, skipped).
- `<capacity>`: a single digit **1–5** (1 depleted → 5 resourced).
- `/<limiter>`: optional one-word tag for what capped you.
- `<journal line>`: your one line, written verbatim into `raw/`.

```sh
lucid closeout dfx 3/wrist steady session, wrist held me back
lucid closeout today ddd 4 all links done, logging late
lucid closeout skip
lucid closeout backfill yesterday dfd 4 ran it, forgot to record
```

These examples assume a three-link chain like the shipped default; the number of
status characters must always match your chain's link count. The full close-out
sequence and the chain schema are in
[`../mvp/engine-module.md`](../mvp/engine-module.md).

### mode

```
lucid mode <green|yellow|red>
```

Declare today's Engine mode: `green` (full), `yellow` (reduced), `red`
(floor-only). Fixed at the bell — a declaration *after* today's bell time, or an
invalid name, is rejected (prints the fixed copy, exits `1`). First declaration
of the day wins. Human-first prose; ignores `--json`.

```sh
lucid mode green
lucid mode yellow
```

### status

```
lucid status [--json]
```

Read-only ambient state (the Engine's "L0" surface): current streak, adherence
vs. declared mode — always co-presented with the floor-day ratio and raw
days-accounted (the honest-number pairing) — error-budget burn, and days to the
next gate. Surfaces "witness lapsed — L2 disarmed" when the witness contract is
lapsed. Writes nothing (beyond a silent `status.json` rebuild if it's missing).
`--json` emits the `status.json` projection verbatim.

```sh
lucid status
lucid status --json
```

### metrics

```
lucid metrics [--json]
```

Read-only **practice-quality** rollup — the honest numbers of the committed
chain, computed by the Engine and never recomputed downstream: current and
longest **streak**, **adherence** over a trailing 30-day window (co-presented
with its honest co-numbers, never a bare percent), **misses** in that window
against the isolated-miss budget, **error-budget** burn, and a **days-since**
count for each recorded anchor (see [`anchor`](#anchor)). It writes nothing
beyond the silent engine scaffold. `--json` emits the metrics projection:

| Field | Meaning |
|-------|---------|
| `current_streak` / `longest_streak` | Chain streak, from the same fold `status` reports. |
| `adherence` | The default 30-day window — `{length, adherence, completed, decided, floor_days, floor_day_ratio, days_accounted}`, the honest-number pairing, never a lone score. |
| `misses_in_window` | Decided-but-not-completed days in the 30-day window. |
| `error_budget` | `{budget, burn, remaining, exceeded}` against the isolated-miss budget. |
| `gates[]` | One `{length, adherence}` per gate window (30, 60, 90) — every gate number, so the harness recomputes nothing. |
| `anchors[]` | One `{label, date, days_since, note}` per recorded anchor, sorted by label. |
| `ref` | The latest recorded logical day the windows anchor to, or `null` when no day is decided yet. |

Days-since counts whole logical days from the anchor date, **anchor day = 0**
(recorded today reads `0`, tomorrow `1`), incrementing at the chain's rollover
boundary — not naive midnight. A read never breaches a gate, so `metrics` exits
`0` on success.

```sh
lucid metrics
lucid metrics --json
```

**`metrics` vs `stats`.** `metrics` reports **practice quality** — how the
committed chain is going (streak, adherence, misses, days-since). Its sibling
read-only `stats` command reports **Ledger volume** — how much has been recorded
(raw-entry and observation counts per logical day). The two share **no** output
field; both read the same rollover / logical-day basis, so their day boundaries
can never disagree.

### anchor

```
lucid anchor add <label> <date> [note...]
```

Record a dated **milestone anchor** — a "days since X" marker (a cessation, a
gate cleared, any date you want a running day count from). The record is appended
to a dedicated, append-only anchors store in the engine tree
(`engine/anchors.json`); it is never hand-edited, deterministic, no model in the
path. `<date>` is a civil `YYYY-MM-DD` and is freely **backdatable** — any past or
future date is accepted. Re-recording the same `<label>` appends again and **the
latest record wins**: a typo fix and a genuine reset are the same append-only
operation, and days-since then counts from the newest. `[note...]` is optional
trailing free text, joined with spaces. Human-first prose ack by default; `--json`
emits the recorded anchor `{label, date, note, recorded_at}` (`recorded_at` is the
append timestamp, local TZ). An empty label or an unparseable date is rejected
(prints the fixed copy, exits `1`); missing arguments are a usage error (exit `2`).

```sh
lucid anchor add sobriety 2026-01-15
lucid anchor add gate-30 2026-02-01 cleared the first gate
lucid anchor add sobriety 2026-01-16    # correction — latest record wins
```

Read the running counts with [`metrics`](#metrics) (`anchors[]` → `days_since`).

### obs

```
lucid obs [kind] [value...]
```

Log a health/context observation micro-log. Deterministic, no LLM. The first
token is the kind (or a shorthand alias); the rest is the value text. Capture
never blocks — an unparseable head is kept verbatim on a partial path, and the
ack is inventory only (never a streak or score). A kind must be enabled in
`observations/config.json` first (default enabled: `pain`, `intake` (`ate`/
`drank`), `elimination` (`bm`), `mood`).

Common kinds and aliases: `pain`, `symptom`, `ate`/`drank` (intake), `bm`
(elimination), `mood`, `slept` (sleep), `med`, `where` (sticky location).

```sh
lucid obs pain 6 knee aching after the run
lucid obs bm 4
lucid obs ate eggs, toast, coffee @yesterday 19:30
lucid obs mood 4 restless
lucid obs where Lisbon    # enable context.location first
```

`@`-backdating, `#tags`, and the full micro-log grammar are in
[`../observations.md`](../observations.md).

### day

```
lucid day [date|yesterday] [--json]
```

Read-only joined view of one logical day: the Engine day record, the day's
observations (plus any spanning range event), and the raw entry ids. Defaults to
today; accepts `yesterday` or a `YYYY-MM-DD` date. Writes nothing. `--json`
emits the assembled view.

```sh
lucid day
lucid day yesterday
lucid day 2026-06-01 --json
```

### validate

```
lucid validate [--json]
```

Read-only verification sweep: the public-boundary (S-7), diagnostic-language
(S-8), sanctuary, doc-link, and Ledger-schema checks. Writes nothing — it won't
even scaffold the Ledger. Exits non-zero if any error-severity check fails
(warnings alone don't). Repo checks are skipped when run outside a checkout; the
schema check is skipped when no Ledger exists. `--json` emits
`{ok, ran, skipped, errors, warnings, findings, …}`.

```sh
lucid validate
lucid validate --json
```

### export

```
lucid export [series | packet clinician [@date|all]]
```

Write a projection to disk and print **only the path** (never the body). Two
forms:

| Form | Writes |
|------|--------|
| `lucid export series` | A pain/mood/capacity CSV series. |
| `lucid export packet clinician [@date\|all]` | The clinician packet. Optional `@<date>` anchors the window; `all` exports the full history. Notes, location, and weather are excluded from the packet body by default. |

`--json` emits `{command, what, path, window_start, window_end}`. The packet body
never rides the chat surface — only the path is posted.

```sh
lucid export series
lucid export packet clinician all
lucid export packet clinician @2026-06-30 --json
```

### version

```
lucid version [--json]
```

Print build metadata: version, commit, build date, Go toolchain, and platform.
`--json` emits the same as an object.

```sh
lucid version
lucid version --json
```

### upgrade

```
lucid upgrade [--check] [--force] [--channel <stable|beta|edge>] [--managed] [--json]
```

Upgrade the running binary in place from a GitHub release: download the matching
platform archive, verify it against the published SHA-256, and swap it into
place atomically (so a running scheduler is never corrupted mid-run). The target
is the resolved path of the running binary; if that directory isn't writable,
`upgrade` exits with a clear error naming it.

| Flag | Effect |
|------|--------|
| `--check` | Report whether a newer release is available; install nothing. `--json` emits the check info. |
| `--force` | Reinstall the latest release even when already current. |
| `--channel <stable\|beta\|edge>` | Release channel; overrides `UPDATE_CHANNEL`. |
| `--managed` | Supervised upgrade: honor the drain window (never between the evening bell and the morning close-out) and run a post-upgrade tripwire self-check. |

```sh
lucid upgrade --check
lucid upgrade
lucid upgrade --channel beta
UPDATE_CHANNEL=edge lucid upgrade
```

### scheduler

```
lucid scheduler run [--db <path>]
```

Run the autonomous accountability daemon: a durable **go-flywheel**
scheduler ([ADR-0004](../adr/0004-core-dependencies.md)) that fires the
evening bell, the morning tripwire (the L1/L2 escalation ladder), and the
monthly witness heartbeat on the active chain profile's clocks
(`bell_time`, `tripwire_time`) — the only autonomous messages Lucid
sends, each a pre-committed Engine template, never model-authored. The
jobs are durable: a daemon killed mid-evening still fires the missed
tripwire on its next supervised start (bounded missed-fire catch-up). The
path is deterministic and agent-free — no LLM, ever. It is meant to run
under `hush supervise` as a launchd sibling of the harness gateway,
posting via a Discord bot whose token and target channel IDs are read
from the injected environment (see
[Environment variables](#environment-variables)).

| Flag | Effect |
|------|--------|
| `--db <path>` | Path to the durable job store. Overrides `LUCID_SCHEDULER_DB`; defaults to a `flywheel.db` under the OS user-config dir, **outside** the `~/.lucid/` Ledger — disposable machinery, never the record (ADR-0004). |

```sh
lucid scheduler run
lucid scheduler run --db /var/lib/lucid/scheduler.db
```

### storm

```
lucid storm <clause-label|unwritten|end> [--json]
```

Declare or end a **storm** — the pre-committed incapacity state
([`../mvp/engine-module.md`](../mvp/engine-module.md) §Commands): witness-confirmed
within 72h, bounded (14 days, one renewal), never retroactive. `lucid storm <label>`
declares a fresh storm (pending witness confirmation) or renews a standing one
(allowed once); `lucid storm end` ends a standing storm early. While a storm stands,
misses spend no budget and the stake is stayed. Clause labels are opaque tokens
registered in `storm.json` (the words live in the Charter) and may contain spaces —
trailing arguments are joined, so `lucid storm wrist flare` is one label. Every
accepted command appends to `storm.json` and rebuilds `status.json`. Deterministic,
no model.

A rejection — an unknown label, a second renewal, or `end` with no standing storm —
is a no-op: it prints the fixed copy to stderr and exits `1`, writing nothing.
`--json` emits `{event, label, through, rejected}` (`event` is `declared`, `renewed`,
or `ended` on success; a rejection carries `rejected: true`).

```sh
lucid storm wrist-flare
lucid storm unwritten
lucid storm end
lucid storm wrist-flare --json
```

### profile

```
lucid profile <name> [--json]
```

Switch to a named clock profile defined in `chain.json`
([`../mvp/engine-module.md`](../mvp/engine-module.md) §Commands): `bell`, `tripwire`,
and `rollover` move together. The switch is sticky, recorded in `profile.json`'s
append-only history, and effective from the **next** logical day — never the current
one, so a switch after tonight's bell cannot move tonight's clocks. Deterministic, no
model. An undefined profile name is rejected with no disk effect: it prints the fixed
copy to stderr and exits `1`. `--json` emits `{from, to, effective, rejected}`
(`effective` is the logical day the switch takes hold).

```sh
lucid profile travel
lucid profile default --json
```

### person

```
lucid person <name> [--json]
```

Deterministic person join ([`../mvp/data-model.md`](../mvp/data-model.md);
[`../mvp/scope.md`](../mvp/scope.md) §4) over the people record, its mention counts,
the accepted insights citing entries that mention them, and a dominance line. Pure
**read** — it never calls a model and never writes, and the output is byte-stable
across repeated runs on the same store (S-22). Names may contain spaces (trailing
arguments are joined). It **always exits `0`**: a no-match (§P-1), several matches
(§P-2, candidates listed), a single match, and an off-limits person (§P-3, raw record
only — mentions and dates, nothing derived) are all read outcomes carried in the
result, never errors. `--json` emits `{query, matched, multiple_matches,
candidates:[{person_key, display_name, first_seen_at}], off_limits, person_key,
text}`.

```sh
lucid person "Sam Rivera"
lucid person Alex --json
```

### bootstrap

```
lucid bootstrap [done] [--json]
```

Toggle historical-entry mode ([`../mvp/scope.md`](../mvp/scope.md) §4): `lucid
bootstrap` turns it **on** — while on, captures stamp `bootstrap:true` and pattern
proposals are suppressed; `lucid bootstrap done` turns it **off** (no consolidation
pass runs on exit). The persisted `lucid.json` `bootstrap_mode` is updated and the
router's effective config follows it, so the next command reads the new mode without
a reboot. Deterministic, no model. `done` is the only valid positional argument — any
other (`lucid bootstrap foo`) is a usage error (exit `2`). `--json` emits
`{bootstrap_mode}` (the resulting boolean).

```sh
lucid bootstrap
lucid bootstrap done
lucid bootstrap --json
```

> Cobra also provides two built-ins that aren't specific to Lucid: `lucid help
> [command]` for help on any command, and `lucid completion <bash|zsh|fish|powershell>`
> to generate a shell-completion script.

## Chat/harness slash commands

These run **only through a chat harness** with the Lucid skill installed
([`../../skills/lucid/SKILL.md`](../../skills/lucid/SKILL.md)); a bare `lucid`
binary does not expose them. The harness maps a message to a router intent and
shells out to the same core — it invents no command of its own. The agentic
verbs (marked *provider-backed*) additionally need an LLM provider configured.

### Verbs with no CLI equivalent

Only the three *provider-backed* Mirror verbs still have no CLI surface — they
need an LLM provider and land behind a serve/CLI surface later (see
[`../harness-integration.md`](../harness-integration.md) §D). The deterministic
`/storm`, `/profile`, `/person`, and `/bootstrap` now shell to the CLI verbs above
(listed under [Verbatim passthroughs](#verbatim-passthroughs)).

| Command | Does |
|---------|------|
| `/checkin` | Guided capture — Intake asks 2–4 follow-up questions in a thread, bundles your (≥90%-authored) answers into one raw entry, then structures it and may offer **one** tentative pattern through the resonance gate. *Provider-backed.* |
| `/reflect [gate]` | Weekly recall of validated insights ("still resonating?" — including whether attached rules still stand). The `gate` variant, at gate/quarterly cadence, recalls every accepted insight. Never proposes new patterns. *Provider-backed.* |
| `/ask <question>` | Grounded, cited Q&A over your validated insights + reflections only — surfaces, not new patterns, never advice. *Provider-backed.* |

Observation shorthands `/pain`, `/ate`, `/drank`, `/bm`, `/mood`, `/slept` are
aliases into the same intent as `lucid obs …`.

### Verbatim passthroughs

These slash commands map one-to-one onto the CLI verbs above and relay their
output unchanged (the Engine verbs are relayed **verbatim** — never scored,
embellished, or celebrated):

| Slash command | Runs |
|---------------|------|
| `/log <text>` | `lucid log <text>` |
| `/closeout …` · `/closeout skip` · `/closeout backfill …` | `lucid closeout …` |
| `/mode <…>` | `lucid mode <…>` |
| `/status` | `lucid status` |
| `/day [date]` | `lucid day [date]` |
| `/packet clinician [@date\|all]` | `lucid export packet clinician …` (posts only the path) |
| `/storm <label\|unwritten>` · `/storm end` | `lucid storm <label\|unwritten\|end>` |
| `/profile <name>` | `lucid profile <name>` |
| `/person <name>` | `lucid person <name>` |
| `/bootstrap` · `/bootstrap done` | `lucid bootstrap [done]` |

The scheduled sends — the bell, the morning tripwire (L1/L2), and the monthly
witness heartbeat — are the scheduler's, not a command's: pre-committed
templates, the only autonomous messages Lucid sends. See
[`../mvp/engine-module.md`](../mvp/engine-module.md).

## Environment variables

| Variable | Effect |
|----------|--------|
| `LUCID_HOME` | Override the Ledger location (default `~/.lucid/`). |
| `UPDATE_CHANNEL` | Default release channel for `lucid upgrade` (`stable` \| `beta` \| `edge`); `--channel` overrides it. |
| `LUCID_HARNESS_TOKEN` | The chat-bot token `lucid scheduler run` posts with (a Discord bot token). Injected at spawn — vaulted in `hush` and never committed (ADR-0005); the binary reads it only from the environment. |
| `LUCID_USER_CHANNEL_ID` | Real channel ID the scheduler's logical `"user"` sends resolve to — the primary Lucid channel (bell, L1). Injected, never committed. |
| `LUCID_WITNESS_CHANNEL_ID` | Real channel ID the logical `"witness"` sends resolve to — the dedicated witness channel (L2, monthly heartbeat). Injected, never committed. |
| `LUCID_SCHEDULER_DB` | Optional override for the scheduler's durable job-store path; `--db` overrides it. Defaults outside `~/.lucid/` (disposable machinery, ADR-0004). |
