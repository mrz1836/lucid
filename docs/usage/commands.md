# Command reference

Every way to drive Lucid, in one place. Two surfaces:

- [**CLI commands**](#cli-commands) — the `lucid` binary's verbs, run in a
  terminal. Deterministic and agent-free unless noted.
- [**Chat/harness slash commands**](#chatharness-slash-commands) — extra verbs
  available only through a chat harness with the Lucid skill installed.

See [`install.md`](install.md) to get the binary and [`getting-started.md`](getting-started.md)
for the daily flow.

Prefer to talk instead of type? [`natural-language.md`](natural-language.md)
describes the voice-first surface that maps plain language onto these commands —
this reference stays the precise baseline.

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

### attach

```
lucid attach <path> [--caption <text>] [--day @yesterday|@YYYY-MM-DD]
```

Attach a file to the Ledger: copy **any binary** — a photo, a scanned PDF, a
handwritten page, an artifact — into the `~/.lucid/media/` store, hash it, and
record it against a logical day. Deterministic and **agent-free**: no model runs
in the write path — the copy, the sha256, and the metadata sidecar are mechanical
(P3). Content is stored **opaquely** (no type gate, original extension
preserved); video and audio stay out of scope by convention. The stored file is
named `YYYY-MM-DD-<slug>.<ext>` and paired with a `<stored-filename>.json`
metadata sidecar (see [`../mvp/data-model.md`](../mvp/data-model.md) §"Media
attachments"). Attach also emits **one immutable `raw/` entry** referencing the
media, so the attachment is discoverable by the day view and the Mirror.

| Flag | Effect |
|------|--------|
| `--caption <text>` | Optional description, stored verbatim and used to derive the filename slug. Absent on the frictionless "drop it" path. |
| `--day @yesterday` (or `@YYYY-MM-DD`) | Attribute the media to a prior logical day, reusing the same 04:00-rollover backdating as `obs`. Defaults to the current logical day. |

Provenance over magic: the ack lands only *after* the write, naming the stored
path, the sha256, the logical day, and the linked raw id. `--json` emits
`{stored_path, sha256, day, raw_id, caption}`. Scaffolds the media store on first
use.

```sh
lucid attach ~/Pictures/IMG_4823.jpg --caption "handwritten session notes, page 1"
lucid attach ./scan.pdf --caption "clinic intake form"
lucid attach ~/Pictures/whiteboard.png --day @yesterday
lucid attach ./artifact.bin --json
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
observations (plus any spanning range event), the raw entry ids, and any media
attached to the day — surfaced as an inventory `Media:` line (stored path and
caption only, never the body or a score). Defaults to today; accepts `yesterday`
or a `YYYY-MM-DD` date. Writes nothing. `--json` emits the assembled view,
including a `media` array.

```sh
lucid day
lucid day yesterday
lucid day 2026-06-01 --json
```

### stats

```
lucid stats [--last N | --from YYYY-MM-DD --to YYYY-MM-DD] [--json]
```

Read-only **Ledger volume** rollup — *how much* has been recorded, counted per
logical day: raw-entry count, observation count, observation counts by kind,
total events, and a per-logical-day breakdown over a date range. A pure
projection over the Ledger — deterministic, read-only, and **agent-free** (P9):
no model in the path, and it never reads, prints, or returns journal / entry /
observation-payload / Mirror **content**. Counts, kinds, and dates only. Writes
nothing beyond the silent Ledger scaffold `day`/`status` already perform.

**Range selection.** The window is a run of logical days, resolved on the same
rollover basis `lucid day` uses:

| Invocation | Window |
|------------|--------|
| `lucid stats` | The current logical day only (mirrors bare `lucid day`). |
| `lucid stats --last N` | The `N` logical days ending at **and including** today (e.g. `--last 2` on `2026-07-11` → `2026-07-10..2026-07-11`). |
| `lucid stats --from A --to B` | The inclusive explicit range `A..B`. |
| `lucid stats --from A` | `A` through today (`--to` defaults to today). |
| `lucid stats --to B` | The single day `B` (`--from` defaults to `--to`). |

`--last` and `--from`/`--to` are **mutually exclusive** — passing both is a usage
error. A malformed `--from`/`--to`, a `from` later than `to`, or `--last < 1` is
likewise a usage error (exit `2`). A read never breaches a gate, so a resolved
query exits `0`.

**Output fields.** Both surfaces report the resolved date range, the logical-day
basis, `raw_entries`, `observations`, `observations_by_kind`, `total_events`
(`= raw_entries + observations`), and the per-day breakdown.
`observations_by_kind` differs by surface: **dense** under `--json` — every
enabled kind (`pain`, `intake`, `elimination`, `mood`, in config order) is
emitted with zeros included, so the key set and order stay stable across every
run (a harness parses one fixed shape); **sparse** in human output — only
nonzero kinds are listed, for readability. `observations` counts *every*
observation event on the day; `observations_by_kind` breaks down the enabled
kinds only, so on a day carrying a context/enricher event (e.g. `context.day`,
`context.location`) the by-kind counts can sum to fewer than `observations`.

Human output (sparse by-kind):

```text
Stats 2026-07-10..2026-07-11 (logical days)
Raw entries: 36
Observations: 2
  intake: 1
  elimination: 1
Total events: 38

By day:
  2026-07-10: 34 entries, 2 observations, 36 total
  2026-07-11: 2 entries, 0 observations, 2 total
```

`--json` (field order fixed; `observations_by_kind` dense):

```json
{
  "from": "2026-07-10",
  "to": "2026-07-11",
  "logical_day": true,
  "raw_entries": 36,
  "observations": 2,
  "observations_by_kind": {"pain": 0, "intake": 1, "elimination": 1, "mood": 0},
  "total_events": 38,
  "days": [
    {"date": "2026-07-10", "raw_entries": 34, "observations": 2, "total_events": 36},
    {"date": "2026-07-11", "raw_entries": 2, "observations": 0, "total_events": 2}
  ]
}
```

**Two deliberate divergences from `lucid day`.** `stats` reuses the exact
`lucid day` join per day, so a given day's counts match `lucid day` — with two
documented exceptions:

- **Raw entries follow recorded-civil-date bucketing; observations are
  rollover-correct.** A raw entry is counted on the civil date it was recorded
  (the same bucketing `lucid day` uses), while an observation is placed on its
  rollover-correct `logical_date`. Near a rollover / DST boundary a same-moment
  raw entry and observation can therefore fall on different days — matching
  `lucid day` exactly.
- **A spanning observation is counted once, on its start day.** A range
  observation that covers several logical days is counted a single time, on its
  start (`logical_date`) day, so the per-day columns sum exactly to the top-line
  totals. (`lucid day` re-surfaces such a spanning event on every day it covers,
  so for a mid-range spanned day `stats` can report fewer observations than
  `lucid day`.)

**`stats` vs `metrics`.** `stats` reports **Ledger volume** — how much has been
recorded (raw-entry and observation counts per logical day). Its sibling
read-only [`metrics`](#metrics) command reports **practice quality** — how the
committed chain is going (streak, adherence, misses, days-since). The two share
**no** output field; both read the same rollover / logical-day basis, so their
day boundaries can never disagree.

```sh
lucid stats
lucid stats --last 2
lucid stats --from 2026-07-10 --to 2026-07-11
lucid stats --last 7 --json
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
lucid scheduler run    [--db <path>]
lucid scheduler status [--scheduler-db <path>] [--companion-db <path>] [--json]
```

The `scheduler` parent has two subcommands: **`run`** starts the autonomous
daemon, and **[`status`](#scheduler-status)** is its read-only health sibling —
it inspects local state and reports a verdict, sending nothing and touching no
secret.

#### scheduler run

Run the autonomous accountability daemon: a durable **go-flywheel**
scheduler ([ADR-0004](../adr/0004-core-dependencies.md)) that fires the
evening bell and the morning tripwire (the L1/L2 escalation ladder) on the
active chain profile's clocks (`bell_time`, `tripwire_time`) — pre-committed
Engine templates, never model-authored. The retired monthly heartbeat is
absorbed by the weekly [witness report](witness-report.md), which runs beside
the teeth alongside the daily [companion](companion.md) when configured. The
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

#### scheduler status

```
lucid scheduler status [--scheduler-db <path>] [--companion-db <path>] [--json]
```

Read-only **health surface** for the autonomous scheduler — it answers one plain
question: *is the scheduler healthy, what fires next, and what happened last?* It
aggregates local state only and is **credential-dumb and agent-free**: it sends
nothing, renews no secret, reads no prompt body, and runs no model. It reports the
companion enabled/disabled state and its provider backend/model, each configured
prompt path (existence only), the chain bell and tripwire marks, the teeth and
companion periodics (cron, active flag, next run, last enqueue), the last companion
delivery receipt per window, a bounded recent-run failure summary, and a
best-effort host/supervisor probe — then rolls every check into one **verdict** and
exits on it. Run it before the morning (`06:00`) and night (`19:00`) windows to
confirm the next send will fire, and after them to confirm it did.

| Flag | Effect |
|------|--------|
| `--json` | Emit the full report as JSON with a top-level `verdict` (`ok` \| `warn` \| `error`) and structured per-check results. The `verdict` mirrors the exit code. |
| `--scheduler-db <path>` | Inspect an explicit teeth job-store path. Overrides `LUCID_SCHEDULER_DB`; defaults to the daemon's resolved path. |
| `--companion-db <path>` | Inspect an explicit companion job-store path. Overrides `LUCID_COMPANION_DB`; defaults to the daemon's resolved path. |

The command resolves the two disposable job-store paths exactly as the daemon does
(flag → env → OS-user-config default) and **always prints the resolved paths**, so
an environment / launchd path drift is visible rather than silently green.

**Exit-code contract (a deliberate override of the [global table](#global-conventions)).**
`scheduler status` is a *graded* command: its exit code is the health verdict,
identical in text and `--json` output, so a health cron or agent can gate on the
code alone without parsing JSON.

| Code | Verdict | Meaning |
|------|---------|---------|
| `0` | `ok` | Healthy — every check passed (or is a benign `unknown`). |
| `1` | `warn` | A benign-but-noteworthy condition (companion disabled, an unverified receipt). |
| `2` | `error` | A real problem (a missing job store, a missed send, the daemon down). |

Warnings are always printed (never swallowed) in both modes. A hard runtime failure
— an unreadable Ledger — still surfaces as a normal error exit.

**Verdict thresholds.** Each check classifies against this table; the report's
verdict is the most severe check — `error` beats `warn` beats `ok`, and an
`unknown` never lowers it:

| Condition | Verdict |
|-----------|---------|
| Companion disabled | `warn` |
| Teeth or (required) companion job store missing / unreadable | `error` |
| Companion enabled but a configured prompt file is missing | `error` |
| A required periodic inactive or missing while the companion is enabled | `error` |
| Teeth bell inactive while the companion owns the night send | not a fault (suppressed) |
| Latest companion receipt present but unverified | `warn` |
| Most-recent already-elapsed window has no receipt, or only a stale one | `error` |
| On-disk build newer than the running supervised daemon (stale daemon) | `warn` |
| Daemon not running / not supervised | `error` |
| Supervisor uninspectable on this platform | `unknown` (never lowers the verdict) |

**Best-effort host checks.** The host/supervisor checks (the daemon process, its
supervisor, and a stale supervised binary) run by default but are best-effort: on a
platform where they cannot be inspected — a non-macOS host, or an unreadable
supervisor — each reports `unknown`, never `ok`. An `unknown` check never lowers
the verdict; only a positively detected problem (daemon down, stale binary) does.
So the command is useful on any host and only goes red when something is actually
wrong.

```sh
lucid scheduler status
lucid scheduler status --json
lucid scheduler status --scheduler-db /var/lib/lucid/flywheel.db
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

### serve

```
lucid serve [--json]
```

Run the **interactive `/checkin` flow** over a line-oriented stdin/JSON protocol —
the one Mirror verb that is multi-turn (Intake asks 2–4 follow-ups, then the
resonance gate needs a yes/no and any attached rule confirmed). A harness drives one
session per connection: the server emits `{"type":"question"|"proposal"|"rule"|"ack"|
"error", …}` frames and reads `{"type":"answer"|"resonance"|"rule_answer"|"control",
…}` back, orchestrating Checkin → Structure → Validate to a resonance-gated insight
with provenance. **Provider-backed** — it builds the model backend from the
`lucid.json` `provider` block (see [Provider configuration](#provider-configuration-agentic-verbs))
and routes every agent-authored message through the Safety/Consent gate. `/done` or
`/cancel` control frames end a session.

```sh
lucid serve
```

### reflect

```
lucid reflect [gate] [--json]
```

Weekly **recall** of your validated insights ("still resonating?", and whether any
attached rule still stands). The `gate` variant, at gate/quarterly cadence, recalls
every accepted insight and appends the deterministic panel numbers. One-shot and
read-mostly: it writes the ISO-week reflection record and appends rule-status
responses, but **never proposes a new pattern**. Surfaces default to `unanswered`; an
optional stdin/JSON batch of per-insight answers (confirm / soften / retire, plus
kept / lapsed for ruled insights) is applied in one shot when supplied.
**Provider-backed** (the `provider` block); every surfaced line passes the
Safety/Consent gate.

```sh
lucid reflect
lucid reflect gate --json
```

### ask

```
lucid ask <question...>
```

Grounded, cited **Q&A** over your validated insights and weekly reflections only —
surfaces, never new patterns, never advice. Read-only: it writes nothing. Prints the
answer with in-slice citations, the fixed calm fallback when the Safety gate holds an
out-of-slice citation, or an "not enough validated material yet" message when the
slice is empty. Trailing arguments are joined, so quoting the question is optional.
**Provider-backed** (the `provider` block); the answer passes the Safety/Consent
gate.

```sh
lucid ask "what tends to trip me up in group settings?"
lucid ask what did I decide about mornings --json
```

### workout

```
lucid workout [--json]
lucid workout log [drop...] [flags]
```

The optional, **config-gated** workout companion — it recommends today's session,
records what actually happened, and reviews progress. Off by default; enable it by
adding a `workout` block to `lucid.json` (an opaque `program` path plus
`slot_time`, `system_prompt`, `template`) and the `workout`/`body_state` kinds to
`observations/config.json`. Full guide: [`workout.md`](workout.md).

Bare **`lucid workout`** composes today's recommendation on demand. A deterministic
core owns the decision — it picks today's card from the program rotation and vetoes
it against per-body-part recovery windows (no leg day two days running), a pain-flag
hard stop, and the injury registry — and the model only phrases the already-decided
plan. Every message carries exactly three offerings (a recommended plan, an easier
fallback, and a back-off/safety door), a read-only progress panel (streak, frequency,
skipped days, recent body response), and a fixed *not-medical-advice* line. The pick
is never the model's: with the provider unreachable the message still renders
deterministically (only the phrasing warmth is lost). `--json` emits the decided
`{recommendation, trend}` projection instead of the rendered text.

**`lucid workout log`** captures a completed session two ways — a spoken drop
(extracted by the model, the voice-first default) or the structured flags
(`--type --duration --rpe --parts --soreness --pain --notes`) for guided or backfill
capture. The two forms are mutually exclusive. Each writes a `workout` observation
(plus one `body_state` reading per soreness/pain flag) to the Ledger; the readings
are what the recommender reads back for the recovery and pain guardrails.

**Provider-backed** for phrasing (the `provider` block), with a deterministic
fallback; the recommendation itself and the capture parser are model-free.

```sh
lucid workout                          # today's recommendation, phrased
lucid workout --json                   # the decided recommendation + trend
lucid workout log "did pull, shoulder felt fine, ~50 min"
lucid workout log --type legs --duration 45 --rpe 7 --soreness quads:5 --pain knee:7
```

### Provider configuration (agentic verbs)

`serve`, `reflect`, and `ask` are the only **provider-backed** verbs — they need a
model backend, configured by the `provider` block in `lucid.json` (ADR-0006, no API
keys):

| Field | Meaning |
|-------|---------|
| `backend` | `claude_cli` (default) — a fresh one-shot `claude -p --output-format json --model <model>`; on-host subscription OAuth, zero setup. Or `ollama` — a non-streaming `POST <endpoint>/api/chat` to the local daemon; needs `ollama serve` up and the model pulled. |
| `model` | The backend's model. Default `opus` (Claude CLI); e.g. `qwen2.5:14b` (Ollama). |
| `timeout_seconds` | Bounds **every** call, so a hung backend degrades to a timeout instead of waiting forever. Default `120`. |
| `endpoint` | Ollama base URL (default `http://localhost:11434`); ignored by the Claude CLI backend. |
| `roles` | Reserved per-role `{backend, model}` overrides. Empty by default — one configured backend serves all four agent roles for now. |

An unreachable backend or a missing model surfaces as "no model reachable" rather
than blocking the loop (the Engine and deterministic verbs never need a model). Full
field table: [`../mvp/data-model.md`](../mvp/data-model.md) §`lucid.json`; per-backend
invocation contract: [`../adr/0006-model-access.md`](../adr/0006-model-access.md).

> Cobra also provides two built-ins that aren't specific to Lucid: `lucid help
> [command]` for help on any command, and `lucid completion <bash|zsh|fish|powershell>`
> to generate a shell-completion script.

### injury

```
lucid injury <name> [--status active|managed|resolved] [--onset @yesterday|YYYY-MM-DD]
             [--body-area <text>] [--cause <text>] [--severity <text>]
             [--lasting-effects <text>] [--current-limitations <text>]
             [--treatments <text>] [--uncertainty <text>] [--note <text>] [--json]
```

Record or amend an injury in the `injury` registry — the first of the
**life-archive** verbs (full guide: [`life-archive.md`](life-archive.md); field
convention: [`../mvp/life-archive.md`](../mvp/life-archive.md) §2). The first
mention of a name creates the record; a later call with the same name amends it,
merging the supplied fields and appending any status transition to the
append-only `status_history` (recorded, never overwritten). Backdate-aware onset
(`--onset`) records its precision. Every flag is optional — a bare `lucid injury
"left knee"` is a valid first mention. Deterministic, agent-free; scaffolds on
first use. No field is a score, streak, or target (inventory, not obligation).
`--json` emits `{kind, key, display_name, status, created, fields}`.

```sh
lucid injury "left knee"
lucid injury "left knee" --status managed --onset 2014-09 --current-limitations "no deep squats" --json
```

### era

```
lucid era <name> [--start @yesterday|YYYY-MM-DD] [--end @yesterday|YYYY-MM-DD] [--note <text>] [--json]
```

Record or amend a life chapter in the `era` registry ([`life-archive.md`](life-archive.md);
[`../mvp/life-archive.md`](../mvp/life-archive.md) §4). Either bound may be
approximate; omit `--end` for a still-running chapter. Stories attach to an era
via `lucid memory --era <key>`, so the past becomes browsable by chapter. Same
create-then-amend, append-only merge, and `{kind, key, display_name, status,
created, fields}` `--json` shape as `injury`.

```sh
lucid era "wild summer" --start 2010-06-01
lucid era "the coast years" --start 2010 --end 2014 --json
```

### thread

```
lucid thread <name> [--intent <text>] [--domain <text>]... [--status active|managed|resolved] [--note <text>] [--json]
```

Record or amend a thread you're working on in the `thread` registry
([`life-archive.md`](life-archive.md); [`../mvp/life-archive.md`](../mvp/life-archive.md) §4).
`--intent` is the one-line statement of what it is; `--domain` is repeatable. A
thread has **no progress number, percent, or streak** — the obliquity guard is
structural, there is no flag to set one, and the write path omits any that slip
through. Same append-only merge and `--json` shape as the other registry verbs.

```sh
lucid thread "learning to sail" --intent "get comfortable single-handing" --domain skill --domain outdoors
lucid thread "the memoir" --intent "write the messy years down" --status active --json
```

### memory

```
lucid memory <text> [--certainty vivid|hazy|reconstructed] [--era <key>] [--place <name>]
             [--people <name>,<name>]... [--tone <text>] [--why <text>] [--followup <text>]
             [--day @yesterday|YYYY-MM-DD] [--attach <path> [--caption <text>]] [--json]
```

Record a story from your past as one `memory` observation, written at a backdated
`occurred_at` and linked to the era, place, and people it belongs to
([`life-archive.md`](life-archive.md); [`../mvp/life-archive.md`](../mvp/life-archive.md) §3).
The `memory` kind is **enable-gated** — like every observation kind it ships off;
a disabled kind prints the enable hint and writes nothing (exit `0`). `--certainty`
is the honesty field; `--era`/`--place`/`--people` become the story's `refs`;
`--day` is the backdate grammar (`@yesterday`, `YYYY-MM-DD`, or an approximate
year). **Optional media, never a gate:** `--attach <path>` reuses
[`lucid attach`](#attach) and links the returned raw id from `refs.entry`; a
text-only story omits it and is never blocked. Deterministic, agent-free;
scaffolds on first use. `--json` emits `{event_id, logical_date, partial,
rejected, refs}`.

```sh
lucid memory "the night we drove to the coast" --era wild-summer --certainty vivid --day 2010-07
lucid memory "the pier at 2am" --era wild-summer --attach ~/Pictures/pier.jpg --caption "the old boardwalk" --json
```

### excavate

```
lucid excavate [--json]
```

**Read-only.** Select the next memory cluster to excavate — the thinnest injury
or the least-excavated era, over two separate tracks — and emit its generic
prompt templates ([`life-archive.md`](life-archive.md);
[`../mvp/life-archive.md`](../mvp/life-archive.md) §5–§6). Nothing under
`~/.lucid/` changes and **no model runs**: this is the deterministic half of the
excavation ritual; a chat harness reads `--json` and drives the one-cluster-at-a-time
conversation on its own surface. An empty or fully-excavated store degrades to an
honest empty result (the calm fallback, no model spent). `--json` emits `{found,
track, key, display_name, reason, gaps, prompts}`; the human form prints the
cluster and prompts as bullets (no tables).

```sh
lucid excavate
lucid excavate --json
```

### recall

```
lucid recall [--era <key> | --thread <key> | --injury <key>] [--json]
```

**Read-only.** Browse the archive by era, thread, or injury (mutually-exclusive
dimension flags); with no flag, print the archive index over all three
([`life-archive.md`](life-archive.md); [`../mvp/life-archive.md`](../mvp/life-archive.md) §7).
Every surfaced item carries its **source context** — the supporting
raw/observation ids and its provenance — so nothing is uncited (a story cites its
observation id; a referent cites its registry record). Nothing is written and no
model runs, mirroring `excavate`; the same projection-only reads back the
[weekly reflection](weekly-reflection.md). A key that does not resolve, and an
empty archive, each print an honest fallback. `--json` emits `{dimension, key,
found, referent, items}`; the human form prints bullets with a `Cites:` line per
item (no tables).

```sh
lucid recall
lucid recall --era wild-summer
lucid recall --injury left-knee --json
```

## Chat/harness slash commands

These run **only through a chat harness** with the Lucid skill installed
([`../../skills/lucid/SKILL.md`](../../skills/lucid/SKILL.md)); a bare `lucid`
binary does not expose them. The harness maps a message to a router intent and
shells out to the same core — it invents no command of its own. The agentic
verbs (marked *provider-backed*) additionally need an LLM provider configured.

### The provider-backed Mirror verbs

The three *provider-backed* Mirror verbs now shell to a CLI/serve surface like every
other verb (see [`../harness-integration.md`](../harness-integration.md) §D):
`/checkin` drives [`lucid serve`](#serve) — its multi-turn thread rides the
stdin/JSON protocol — while `/reflect [gate]` and `/ask` map one-shot to
[`lucid reflect`](#reflect) and [`lucid ask`](#ask). They differ from the
deterministic verbs only in needing a configured `provider` block (an LLM backend,
ADR-0006). The deterministic `/storm`, `/profile`, `/person`, and `/bootstrap` shell
to their CLI verbs above (listed under [Verbatim passthroughs](#verbatim-passthroughs)).

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

The scheduled teeth sends — the bell and the morning tripwire (L1/L2) — are the
scheduler's, not a command's: pre-committed templates. The config-gated
Mirror-side sends (the daily companion and the weekly witness report, which
replaced the retired monthly heartbeat) run beside them. See
[`../mvp/engine-module.md`](../mvp/engine-module.md).

## Environment variables

| Variable | Effect |
|----------|--------|
| `LUCID_HOME` | Override the Ledger location (default `~/.lucid/`). |
| `UPDATE_CHANNEL` | Default release channel for `lucid upgrade` (`stable` \| `beta` \| `edge`); `--channel` overrides it. |
| `LUCID_HARNESS_TOKEN` | The chat-bot token `lucid scheduler run` posts with (a Discord bot token). Injected at spawn — vaulted in `hush` and never committed (ADR-0005); the binary reads it only from the environment. |
| `LUCID_USER_CHANNEL_ID` | Real channel ID the scheduler's logical `"user"` sends resolve to — the primary Lucid channel (bell, L1). Injected, never committed. |
| `LUCID_WITNESS_CHANNEL_ID` | Real channel ID the logical `"witness"` sends resolve to — the dedicated witness channel (L2 escalation, weekly witness report). Injected, never committed. |
| `LUCID_SCHEDULER_DB` | Optional override for the scheduler's durable teeth job-store path; `--db` (on `run`) or `--scheduler-db` (on `status`) overrides it. Defaults outside `~/.lucid/` (disposable machinery, ADR-0004). |
| `LUCID_COMPANION_DB` | Optional override for the companion's disposable job-store path, read by `lucid scheduler status`; `--companion-db` overrides it. Defaults under the OS user-config dir, outside `~/.lucid/`. |
| `LUCID_WITNESS_REPORT_DB` | Optional override for the weekly witness report's disposable job-store path. Defaults under the OS user-config dir, outside `~/.lucid/` (disposable machinery, never the record). |
