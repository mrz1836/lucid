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

| Command | Does |
|---------|------|
| `/checkin` | Guided capture — Intake asks 2–4 follow-up questions in a thread, bundles your (≥90%-authored) answers into one raw entry, then structures it and may offer **one** tentative pattern through the resonance gate. *Provider-backed.* |
| `/reflect [gate]` | Weekly recall of validated insights ("still resonating?" — including whether attached rules still stand). The `gate` variant, at gate/quarterly cadence, recalls every accepted insight. Never proposes new patterns. *Provider-backed.* |
| `/ask <question>` | Grounded, cited Q&A over your validated insights + reflections only — surfaces, not new patterns, never advice. *Provider-backed.* |
| `/person <name>` | Deterministic person join (mentions, dominance line). No model. |
| `/storm <clause-label\|unwritten>` · `/storm end` | Declare (or end) a storm — the pre-committed incapacity state: witness-confirmed within 72h, bounded (14 days, one renewal), never retroactive. While standing, misses spend no budget and the stake is stayed. |
| `/profile <name>` | Switch to a named clock profile (bell, tripwire, rollover move together), effective from the next logical day. |
| `/bootstrap` · `/bootstrap done` | Enter/exit historical-entry mode for capturing past, formative entries with explicit timestamps; pattern proposals are suppressed while it's on. |

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

The scheduled sends — the bell, the morning tripwire (L1/L2), and the monthly
witness heartbeat — are the scheduler's, not a command's: pre-committed
templates, the only autonomous messages Lucid sends. See
[`../mvp/engine-module.md`](../mvp/engine-module.md).

## Environment variables

| Variable | Effect |
|----------|--------|
| `LUCID_HOME` | Override the Ledger location (default `~/.lucid/`). |
| `UPDATE_CHANNEL` | Default release channel for `lucid upgrade` (`stable` \| `beta` \| `edge`); `--channel` overrides it. |
