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

### Backdating with --day

Every verb that stamps a logical day reads **one** date grammar, so a token that
works on `lucid log` works identically on `lucid workout log`, `lucid mode`, and
the registry's `--start` / `--end` / `--onset`. The commands carrying the `--day`
flag are [`log`](#log), [`attach`](#attach), [`memory`](#memory), [`obs`](#obs),
[`workout log`](#workout), [`mode`](#mode), [`storm`](#storm), and
[`closeout`](#closeout).

**The grammar.** A leading `@` is optional on every form.

| Form | Resolves to | Precision |
|------|-------------|-----------|
| `@yesterday` · `yesterday` | The logical day before the current one | `approximate` |
| `@YYYY-MM-DD` · `YYYY-MM-DD` | That civil day, taken literally | `approximate` |
| `YYYY-MM-DDTHH:MM[:SS]` | That exact instant | `exact` |
| `HH:MM` | Today at that time | `exact` |
| `HH:MM-HH:MM` | A span within today — also sets `occurred_at_end` | `range` |
| `<day> <time>` — e.g. `--day "@yesterday 19:30"` | That day at that time | `exact` |
| `YYYY-MM` | The first day of that month | `approximate` |
| `YYYY` | January 1st of that year | `approximate` |

**Two tiers, one rule each.**

- **Strict — a date you typed on purpose.** `--day`, the registry's `--start`,
  `--end`, and `--onset`, and `anchor add`'s positional date. A token the
  grammar cannot read, or a day in the future, is a clean error: the command
  exits non-zero, names the accepted forms, and **nothing is written**. A
  deliberate flag is an assertion, so a typo fails loudly instead of quietly
  landing on today.
- **Permissive — an inline `@`-token in `obs` prose.** `lucid obs ate eggs
  @yesterday` parses the token out of a sentence, and capture is total (P10): an
  unrecognized token stays in the note as ordinary text and the observation is
  written anyway. An inline token never blocks a capture.

**The rollover rule.** A relative word respects the 04:00 rollover; an explicit
date is literal. At 02:00 the logical day has not rolled yet — a bare capture
still files under the previous calendar day — so `@yesterday` means the day
*before* that one. `@2026-07-01` means July 1st at any hour. Both tiers follow
this rule.

**Partial dates.** `--day 2014` records `occurred_at 2014-01-01T00:00` at
`approximate` precision under `logical_date 2014-01-01`; `--day 2014-09` files
under `2014-09-01`. A logical day is one real day, so a partial date snaps to
the period's first instant — which means that on a capture, `2014` and
`2014-01-01` are indistinguishable once written. The registry
([`era`](#era), [`injury`](#injury)) is the one place that keeps the string **as
typed**, because "sometime in 2014" is the normal input there rather than an
edge case.

**A bare four-digit token reads differently per tier.** On a `--day` flag,
`2014` is the year. In `obs` prose, `@2014` is still the clock time 20:14 —
colon-less dictation tolerance ([`../observations.md`](../observations.md) §4)
predates the flag and is kept.

### Accepted behavior changes

Unifying the grammar moved six shipped behaviors. Each is deliberate:

1. **`memory --day` is strict.** A typo or a future date is now a hard error
   with nothing written, where it previously recorded *now* in silence.
2. **`memory --day @yesterday` is rollover-aware.** Before 04:00 it resolves one
   day earlier than it used to.
3. **`obs`'s inline `@yesterday` is rollover-aware**, on the same terms.
4. **The registry date flags reject free text.** `era --start "spring 2015"` was
   stored verbatim; it is now a clean error naming the accepted forms and
   pointing at `--note`. **This is the one capability removal** — re-type it as
   `2015-03`, or keep the prose in `--note`.
5. **Registry `yesterday` is rollover-aware**, where it was calendar-literal.
6. **The registry date flags reject future dates.** `era --start`, `era --end`,
   and `injury --onset` never had a future ceiling; they do now. A planned era
   end waits until it happens, or goes in `--note`.

### Deliberate divergences

Five behaviors sit outside the strict tier on purpose. Nothing else does — the
registry now carries the future ceiling, so it is not on this list:

| Divergence | Why |
|------------|-----|
| `obs`'s inline prose token stays permissive | Capture is total (P10) — a date token parsed out of a sentence must never cost you the capture. This covers the colon-less `HHMM` reading above. |
| `anchor add` still accepts a future date | An anchor may be a forward commitment — a date you count *toward*, not only since. |
| `anchor add` rejects a partial date | An anchor renders a precise day count, and counting from a guessed January 1st would be a confident wrong number. |
| `closeout --day` carries backfill semantics | It is an alias onto `closeout backfill`, so it keeps that path's `backfill_window_days` window and rejects today as well as the future. |
| The registry stores a partial date as typed | `--onset 2014-09` stores `"2014-09"`, not the snapped day — the registry is where the *degree* of imprecision is itself the record. |

**The closed write surface.** Every verb that stamps a logical day is named
above: `log`, `attach`, `obs`, `memory`, `workout log`, `era`, `injury`,
`anchor`, `mode`, `storm`, `closeout`. Two write verbs are deliberately N/A —
[`thread`](#thread) is a lifecycle registry with no dated occurrence, and
[`structure`](#structure) distills raw entries that already exist, selected by
id or window, rather than capturing a new one.

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
lucid log [text] [--day <date>]
```

Capture `text` as one immutable raw entry under `~/.lucid/raw/`, with a
sub-second acknowledgement. Capture-only: nothing is written under `processed/`
or `insights/`. Scaffolds on first use.

| Flag | Effect |
|------|--------|
| `--day <date>` | Attribute the entry to a prior logical day. Accepts the shared grammar — a relative word, a full or partial date, and an optional time-of-day — on the strict tier: see [Backdating with --day](#backdating-with---day). The real capture time is always kept as `recorded_at`; only `occurred_at` follows the flag. Defaults to now at exact precision. |

Backdating attributes the entry — it does not move it: the day view still lists
the entry under the day it was captured. `--day` records the day it belongs to on
the entry itself.

```sh
lucid log "shower thought about the knee-and-weather thing"
lucid log "power was out all evening, wrote this up the next morning" --day @yesterday
lucid log "notes from the workshop" --day @2026-07-28
lucid log "wrapped the session late" --day "@yesterday 19:30"
lucid log "the summer I moved north" --day 2014
```

### attach

```
lucid attach <path> [--caption <text>] [--day <date>]
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
| `--day <date>` | Attribute the media to a prior logical day, on the strict tier and the shared grammar — see [Backdating with --day](#backdating-with---day). `@yesterday` steps back from the current logical day, so before 04:00 it resolves to the day *before* the one a bare capture files under. A day in the future is rejected and nothing is saved. Defaults to the current logical day. |

Provenance over magic: the ack lands only *after* the write, naming the stored
path, the sha256, the logical day, and the linked raw id. `--json` emits
`{stored_path, sha256, day, raw_id, caption}`. Scaffolds the media store on first
use.

```sh
lucid attach ~/Pictures/IMG_4823.jpg --caption "handwritten session notes, page 1"
lucid attach ./scan.pdf --caption "clinic intake form"
lucid attach ~/Pictures/whiteboard.png --day @yesterday
lucid attach ./scan-2.pdf --day "2026-07-01 19:30"
lucid attach ./box-of-negatives.jpg --day 2014-09
lucid attach ./artifact.bin --json
```

### closeout

```
lucid closeout [today|skip|backfill] [compact form...] [--day <date>]
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

| Flag | Effect |
|------|--------|
| `--day <date>` | An alias for the **backfill** target, using the shared grammar ([Backdating with --day](#backdating-with---day)). It routes onto the same path as the positional form: the record is stamped `backfilled: true`, the `backfill_window_days` window (default 7) applies, and both the future **and today** are rejected — a backfill target is always a day that has ended. Passing `--day` together with anything that names its own day — the positional `backfill <target>` form, or the `skip` / `today` sub-forms — is a usage error (two targets, one intent) rather than a silent pick between them. |

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
lucid closeout --day @yesterday dfd 4 ran it, forgot to record
```

These examples assume a three-link chain like the shipped default; the number of
status characters must always match your chain's link count. The full close-out
sequence and the chain schema are in
[`../mvp/engine-module.md`](../mvp/engine-module.md).

### mode

```
lucid mode <green|yellow|red> [--day <date>]
```

Declare today's Engine mode: `green` (full), `yellow` (reduced), `red`
(floor-only). Fixed at the bell — a declaration *after* today's bell time, or an
invalid name, is rejected (prints the fixed copy, exits `1`). First declaration
of the day wins. Human-first prose; ignores `--json`.

| Flag | Effect |
|------|--------|
| `--day <date>` | **Gap-fill only.** Declare the mode for a past logical day whose record carries **no** mode. It never overwrites a mode that was declared — the bell still binds every day you showed up for — and reaches back at most `backfill_window_days` (`chain.json`, default 7). Today and any future day are rejected, as they are for [`closeout backfill`](#closeout). The grammar is the shared one ([Backdating with --day](#backdating-with---day)), on the strict tier. |

```sh
lucid mode green
lucid mode yellow
lucid mode yellow --day @yesterday    # a Tuesday you never declared
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
| `anchors[]` | One `{id, label, date, days_since, note}` per **active** anchor, sorted by label. Sunset anchors are not here. |
| `anchors_sunset[]` | One `{id, label, date, note, sunset_at}` per retired anchor. `date` is the milestone date the anchor counted from; `note` is the sunset reason when one was given; `sunset_at` is when the retirement was recorded. A retired milestone stops counting but never leaves the ledger. |
| `ref` | The latest recorded logical day the windows anchor to, or `null` when no day is decided yet. |

A **label may appear in both arrays**: a retired run and a later run of the same
name are distinct anchors with distinct ids, so a reader that assumes label
uniqueness across the two would be wrong. The `id` is the address; the label is
a display name (see [`anchor`](#anchor)).

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
lucid anchor sunset <label> [reason...]
lucid anchor rename <label> <new-label>
```

A **milestone anchor** is a recorded date the Engine counts a running "days
since" from — a cessation, a gate cleared, any date you want a running day count
from. Every record is appended to a dedicated, append-only anchors store in the
engine tree (`engine/anchors.json`); it is never hand-edited, deterministic, no
model in the path. All three verbs *append* — nothing is ever deleted or
rewritten.

**Identity.** Each anchor carries a stable `id` (`anchor_YYYY_MM_DD_a`), minted
when the milestone is first recorded and never derived from what you called it.
The **label is a display name**, so it can be corrected or renamed without
forking the milestone. Anchors recorded before ids existed keep working exactly
as they always have and publish `id: "legacy:<label>"` — a synthesized address
so a harness can still refer to them. Nothing about those records is rewritten.

#### `anchor add`

Record a milestone. `<date>` takes the shared date grammar
([Backdating with --day](#backdating-with---day)), so `lucid anchor add sober
@yesterday` works alongside a civil `YYYY-MM-DD`. `[note...]` is optional
trailing free text, joined with spaces.

Re-recording a `<label>` an **active** anchor already holds appends again under
**that anchor's id**, and **the latest record wins**: a typo fix and a genuine
reset are the same append-only operation, and days-since then counts from the
newest. Re-recording a label whose only holder is a **sunset** anchor starts a
**new** milestone with a new id, and the ack says so — the retired run and the
new one stay distinct in the ledger rather than reading as one milestone that
paused and resumed.

Human-first prose ack by default; `--json` emits the recorded anchor
`{id, label, date, note, recorded_at}` (`recorded_at` is the append timestamp,
local TZ). An empty label or an unparseable date is rejected (prints the fixed
copy, exits `1`); missing arguments are a usage error (exit `2`).

`anchor add` is the CLI's **two documented exceptions** to the strict tier:

- **A future date is accepted** — an anchor may be a forward commitment, a date
  you count *toward* rather than only since. It is the one date surface with no
  future ceiling.
- **A partial date is rejected.** `lucid anchor add sober 2014` is a clean error
  explaining that a day count needs a real day: an anchor's whole output is a
  precise "days since" number, and deriving it from a guessed January 1st would
  report a confident wrong count. Use `YYYY-MM-DD` (or `@yesterday`) instead.

#### `anchor sunset`

Retire a milestone from the counting surfaces **without deleting anything** — a
mistyped label, a duplicate, an abandoned gate. It appends one record mirroring
the anchor plus `state: "sunset"`, with the optional `[reason...]` carried in
that record's `note`.

What changes: the milestone stops appearing in `lucid metrics` prose, leaves
`anchors[]` in `metrics --json`, and stops raising the witness aging-anchor
check. What does not: the milestone stays in `history[]` in full, and appears in
`metrics --json` → `anchors_sunset[]` with the date it counted from, so a
retired milestone is never invisible — it just stops counting.

Adding the label again later is supported and starts a new milestone under a new
id (see `anchor add` above). This is the supported way to correct the metrics
surface; hand-editing the store is not.

#### `anchor rename`

Change an active anchor's display name. Its id, date, note and running day count
all carry forward, so a rename costs you nothing — it is one more append whose
label differs. Exactly two arguments: there is no trailing reason, because
nothing is being retired.

Renaming an anchor **recorded before ids existed** *adopts* it, in a single
write: a retired stub under the old name appears in `anchors_sunset[]` carrying
`id: "legacy:<old-label>"`, and the milestone continues under the new name with
a freshly minted id. That stub is documented behavior, not a caveat — it is how
the ledger stays honest about a name that is no longer in use, and it is what
every rename of a pre-id anchor will do.

#### Errors

Each of these prints a fixed reason, exits `1`, and appends nothing:

- **A label no anchor holds** — the error names the labels that *are* recorded,
  so the remedy is in the message rather than in a separate lookup:
  `no anchor named "gate-3" — recorded anchors: gate-30, sobriety`.
- **A label that is already sunset** — a retired anchor is history, not an
  editable record. Add it again to start a new milestone under that name.
- **`rename` onto a label an active anchor already holds**, or onto the same
  label — at most one active anchor per label is what lets every verb take a
  bare label.

Renaming onto a label held only by a **sunset** anchor succeeds — that name is
free, exactly as it is for `add`.

```sh
lucid anchor add sobriety 2026-01-15
lucid anchor add gate-30 2026-02-01 cleared the first gate
lucid anchor add sobriety 2026-01-16    # correction — same anchor, latest wins
lucid anchor add streak-restart @yesterday
lucid anchor sunset gate-30 mistyped label
lucid anchor rename gate-30 gate-thirty
```

Read the running counts with [`metrics`](#metrics) (`anchors[]` → `days_since`),
and the retired ones with `metrics --json` (`anchors_sunset[]`).

### obs

```
lucid obs [kind] [value...] [--day <date>]
```

Log a health/context observation micro-log. Deterministic, no LLM. The first
token is the kind (or a shorthand alias); the rest is the value text. Capture
never blocks — an unparseable head is kept verbatim on a partial path, and the
ack is inventory only (never a streak or score). A kind must be enabled in
`observations/config.json` first (default enabled: `pain`, `intake` (`ate`/
`drank`), `elimination` (`bm`), `mood`).

Common kinds and aliases: `pain`, `symptom`, `ate`/`drank` (intake), `bm`
(elimination), `mood`, `slept` (sleep), `med`, `where` (sticky location).

`obs` is the one verb that carries **both** backdating tiers, because it accepts
a date two ways ([Backdating with --day](#backdating-with---day)):

| Flag | Effect |
|------|--------|
| `--day <date>` | The **strict** tier: an unreadable token or a future day is a clean error and nothing is captured. When `--day` and an inline `@`-token are both supplied, **the flag wins** and the inline token is still stripped from the note. |

The inline `@`-token in the prose stays **permissive** — capture is total (P10),
so an unrecognized token is left in the note as text and the observation is
written regardless. A colon-less four-digit token in prose is still a clock time
(`lucid obs ate eggs @2014` → today at 20:14), while `--day 2014` is the year.

```sh
lucid obs pain 6 knee aching after the run
lucid obs bm 4
lucid obs ate eggs, toast, coffee @yesterday 19:30
lucid obs mood 4 restless
lucid obs ate eggs, toast --day @yesterday
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

### backup

```
lucid backup [--out <file>] [--json]
```

Write the **must-keep** Ledger set to a single gzip-compressed tar archive: the
primary data that exists nowhere else and must survive forever (ADR-0002; the
same set `scripts/backup.sh` and `deploy.BackupManifest` encode) — `raw/`,
`observations/`, `registries/`, `engine/` (minus its derived `status.json`), and
`projections/exports.log`. Rebuildable trees (`processed/`, `insights/`,
`reflections/`, `engine/status.json`, the rest of `projections/`) and the
reconstructable indexes (`people/`, `sessions/`, `lucid.json`) are deliberately
omitted. It reads `~/.lucid/` and writes one archive; it makes no network call
and never mutates the Ledger.

| Flag | Effect |
|------|--------|
| `--out <file>` | Destination archive path. Default: `lucid-backup-<UTC-timestamp>.tar.gz` in the current directory. The path must be **outside** `~/.lucid/` (a backup is never written into the tree it copies), and an existing file is never clobbered — a same-named target is a clear error, not an overwrite. |

`--json` emits `{command, path, bytes, files}` — the archive path, its compressed
size in bytes, and the number of files written. The human summary ends by naming
what was written and where.

```sh
lucid backup
lucid backup --out /mnt/usb/lucid-2026-07-29.tar.gz
lucid backup --out /tmp/b.tar.gz --json
```

### restore

```
lucid restore [--in <file>] [--force] [--json]
```

Rebuild a Ledger from a `lucid backup` archive. The archive is opened from
wherever you name it (outside `~/.lucid/`); every entry is written back under
`~/.lucid/` after a path-traversal (Zip-Slip) check and a manifest-root
allowlist, so a crafted archive can never plant a file outside the record trees.
Restore is an **overlay**: it writes and overwrites the archive's files and never
deletes anything already present (a full replace is out of scope). The archive
path may be passed with `--in` or positionally.

| Flag | Effect |
|------|--------|
| `--in <file>` | Source archive path (or pass it positionally — not both). |
| `--force` | Overlay even when the target already holds data. Without it, restore **refuses** to write into an occupied home (any backup-set root holding a non-`.keep` file) and names `--force`; a scaffolded-but-empty home (only `lucid.json` + `.keep` markers) counts as empty and restores without the flag. |

`--json` emits `{command, path, bytes, files}` — the source archive, the total
bytes restored, and the number of files written.

```sh
lucid restore --in /tmp/b.tar.gz
lucid restore /mnt/usb/lucid-2026-07-29.tar.gz
lucid restore --in /tmp/b.tar.gz --force --json
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
lucid scheduler run       [--db <path>]
lucid scheduler status    [--scheduler-db <path>] [--companion-db <path>] [--json]
lucid scheduler reconcile [--slug <slug>] [--no-fire] [--db <path>] [--json]
```

The `scheduler` parent has three subcommands: **`run`** starts the autonomous
daemon, **[`status`](#scheduler-status)** is its read-only health sibling — it
inspects local state and reports a verdict, sending nothing and touching no
secret — and **[`reconcile`](#scheduler-reconcile)** is the sanctioned repair
lever that re-arms a scheduled send whose periodic got parked. `status` names
the problem; `reconcile` is the one command that fixes it. Between them, job
state is never repaired by hand: the durable job store is machinery the binary
owns, and editing it directly is unsupported.

#### scheduler run

Run the autonomous accountability daemon: a durable **go-flywheel**
scheduler ([ADR-0004](../adr/0004-core-dependencies.md)) that fires the
evening bell and the morning tripwire (the L1/L2 escalation ladder) on the
active chain profile's clocks (`bell_time`, `tripwire_time`) — pre-committed
Engine templates, never model-authored. The retired monthly heartbeat is
absorbed by the weekly [witness report](witness-report.md), which runs beside
the Engine alongside the daily [companion](companion.md) when configured. The
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

**Periodics it reconciles at boot.** Every start seeds the Engine periodics from
the default profile's clock marks, idempotently by slug, so a restart never
duplicates them:

| Slug | Fires | Active when |
|------|-------|-------------|
| `lucid-tripwire` | `tripwire_time` | always |
| `lucid-bell` | `bell_time` | the companion is **disabled** (otherwise the companion owns the evening user send and the bell is deliberately suppressed) |
| `lucid-bell-fallback` | the [evening backstop mark](#the-evening-backstop-lucid-bell-fallback) | the companion is **enabled** |

**Startup self-heal.** After seeding, the daemon runs the same reconcile pass
that [`scheduler reconcile`](#scheduler-reconcile) exposes: any periodic that is
*intended active* but found parked — inactive, or with its next run stuck in the
past — is re-armed on the spot, and the missed occurrence fires. It is a no-op on
a healthy store, and it never re-arms a periodic that is *intended inactive*
(a companion-suppressed bell stays suppressed). A transient outage can therefore
cost at most one restart, not a silently dead send.

```sh
lucid scheduler run
lucid scheduler run --db /var/lib/lucid/scheduler.db
```

#### The evening backstop (`lucid-bell-fallback`)

When the [companion](companion.md) is enabled it owns the evening user send, and
the Engine bell is suppressed so the window never double-posts. That hands one
window to one sender — and if that sender's delivery fails outright (the host was
off past its cut-off, the channel was unreachable all evening), the evening
accountability window would pass in silence. The backstop closes that gap: a
dedicated periodic that fires **after the companion can no longer send**, and
posts the ordinary pre-committed bell template only if nothing was delivered.

- **What it sends.** The **existing** bell template
  ([`../mvp/engine-module.md`](../mvp/engine-module.md) §Consent amendment),
  verbatim, on the bell's own consent record (`chain.json` `bell.enabled`). It is
  not a new message, a new voice, or a fourth consent — it is the same send under
  a new, deterministic condition. `bell.enabled: false` means it never fires.
- **When it fires.** At the **evening backstop mark**: the companion's night
  cut-off (`22:00` local — see [`companion.md`](companion.md#when-things-go-wrong))
  plus a short grace, and never earlier than `bell_time`. The mark is deliberately
  derived from that cut-off rather than offset from the bell: past the cut-off the
  companion refuses to post at all, so the two can never race. It stays on the same
  logical day as the bell it backs up (`rollover`, default `04:00`).
- **What gates it.** Today's companion `night` delivery receipt. If a receipt
  exists for today's logical day with a delivered message id, the backstop is a
  **no-op** — the evening was already spoken for. If there is no receipt, or only a
  stale one from a previous day, it sends. The receipt is exactly the record the
  companion writes on a verified delivery, read through the engine tree; no Mirror
  content is read, and no model is in the path.
- **At most one evening send per day**, by construction: the companion delivers and
  the backstop stands down, or the companion misses and the backstop speaks. A late
  send is the deliberate trade — on a night the companion missed, the alternative is
  silence, and the practice is what the window exists to defend
  ([`../architecture.md`](../architecture.md) §2, P10).
- **Only when the companion owns the window.** With the companion disabled the
  backstop periodic is inactive and the real bell fires at `bell_time`, exactly as
  before.

#### scheduler status

```
lucid scheduler status [--scheduler-db <path>] [--companion-db <path>] [--json]
```

Read-only **health surface** for the autonomous scheduler — it answers one plain
question: *is the scheduler healthy, what fires next, and what happened last?* It
aggregates local state only and is **credential-dumb and agent-free**: it sends
nothing, renews no secret, reads no prompt body, and runs no model. It reports the
companion enabled/disabled state and its provider backend/model, each configured
prompt path (existence only), the chain bell and tripwire marks, the Engine and
companion periodics (cron, active flag, next run, last enqueue, and — for one that
is deliberately off — the reason), the last companion
delivery receipt per window, a bounded recent-run failure summary, and a
best-effort host/supervisor probe — then rolls every check into one **verdict** and
exits on it. Run it before the morning (`06:00`) and night (`19:00`) windows to
confirm the next send will fire, and after them to confirm it did.

**Every fault it reports names its remedy.** A periodic that is *intended active*
but found parked — inactive, or with its next run stuck in the past — is reported
as an error line naming the slug **and the exact command that repairs it**
(`lucid scheduler reconcile --slug <slug>`), so the report is actionable without a
second lookup. The inverse reads just as plainly: a bell suppressed because the
companion owns the evening send is shown as *suppressed by companion (intended)*
and is **OK**, never a fault — an intended-inactive periodic is a healthy state,
not a parked one.

| Flag | Effect |
|------|--------|
| `--json` | Emit the full report as JSON with a top-level `verdict` (`ok` \| `warn` \| `error`) and structured per-check results. The `verdict` mirrors the exit code. |
| `--scheduler-db <path>` | Inspect an explicit Engine job-store path. Overrides `LUCID_SCHEDULER_DB`; defaults to the daemon's resolved path. |
| `--companion-db <path>` | Inspect an explicit companion job-store path. Overrides `LUCID_COMPANION_DB`; defaults to the daemon's resolved path. |

The command resolves the two disposable job-store paths exactly as the daemon does
(flag → env → OS-user-config default) and **always prints the resolved paths**, so
an environment / launchd path drift is visible rather than silently green.

**It names all four job stores, not just the two it inspects.** The scheduler runs
**four** independent disposable job stores — one each for the Engine, the companion,
the weekly witness report, and the workout companion — and only the Engine store is
pinned into the launchd plist as `LUCID_SCHEDULER_DB`. The other three resolve their
own default under the OS user-config dir, independently of it. So a schema repair,
a relocation, or a backup keyed on the pinned path alone is a fix applied to a
**quarter** of the surface, and nothing about that path reveals the other three
exist. `status` therefore prints the whole set — each store's resolved path beside
the environment variable that moves it — resolved through each daemon's own
resolver, so a printed path is the file that daemon actually opens:

```
Job stores (4; only the Engine store is pinned by the launchd plist):
  engine           /path/to/flywheel.db        (LUCID_SCHEDULER_DB)
  companion        /path/to/companion.db       (LUCID_COMPANION_DB)
  witness-report   /path/to/witness-report.db  (LUCID_WITNESS_REPORT_DB)
  workout          /path/to/workout.db         (LUCID_WORKOUT_DB)
```

The block is **informational**: it is reported in both the text and `--json`
output (as a `job_stores` array), classifies nothing, and never lowers the verdict.
A path that cannot be resolved keeps its row and shows a dash rather than failing
the command — `status` is what you reach for when something is already broken.
Only the Engine and companion stores have inspection flags (`--scheduler-db`,
`--companion-db`); the other two are named, not read.

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
| Engine or (required) companion job store missing / unreadable | `error` |
| Companion enabled but a configured prompt file is missing | `error` |
| A required periodic inactive or missing while the companion is enabled | `error` |
| An intended-active periodic parked (inactive, or next run stuck in the past) | `error` — the line names `lucid scheduler reconcile` |
| Engine bell inactive while the companion owns the night send | not a fault (*suppressed by companion (intended)*) |
| Evening backstop (`lucid-bell-fallback`) inactive while the companion owns the night send | `error` |
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

#### scheduler reconcile

```
lucid scheduler reconcile [--slug <slug>] [--no-fire] [--db <path>] [--json]
```

Re-arm a **parked** scheduled send. A periodic is parked when it is *intended
active* — the configuration says this send should be running — but the job store
says otherwise: the periodic is inactive, or its next run is stuck in the past so
the due scan never reaches it again. That is the state a transient delivery
outage used to be able to leave behind, and it is silent: nothing is broken, a
send simply never comes again. `reconcile` is the sanctioned repair, through the
binary, so the durable job store is never hand-edited
([ADR-0004](../adr/0004-core-dependencies.md) — it is disposable machinery, and
the binary owns its shape).

`lucid scheduler run` performs this same pass at startup, so a restart normally
repairs drift on its own. This command is the on-demand lever for the times you
do not want to bounce the daemon — and the one
[`scheduler status`](#scheduler-status) points at when it reports a parked
periodic.

**The intended-active guard.** Whether a periodic *should* be running is derived
from configuration, per slug — never guessed from the job store:

| Slug | Intended active when |
|------|----------------------|
| `lucid-tripwire` | always — the morning dead-man is unconditional |
| `lucid-bell` | the companion is **disabled** |
| `lucid-bell-fallback` | the companion is **enabled** |
| anything else | never — an unrecognized slug is left untouched |

The guard is what keeps the command from fighting a deliberate configuration: a
bell suppressed because the companion owns the evening send is *intended
inactive*, so `reconcile` leaves it inactive and says so. It re-arms what was
parked; it never re-enables what you turned off.

**What counts as stuck.** An active periodic whose next run has just passed is
*due*, not parked — the scheduler advances its cursor past now on the next tick.
Only a cursor a full day behind (an entire daily occurrence elapsed with nothing
advancing it) reads as stuck, so a running daemon between its mark and its next
poll is never mistaken for a broken one.

**The missed occurrence fires.** Re-arming leaves the stale cursor in place, so
the next tick delivers the send that was missed — late, but delivered. Pass
`--no-fire` to skip it and resume from the next scheduled occurrence instead.

| Flag | Effect |
|------|--------|
| `--slug <slug>` | Reconcile one periodic instead of all of them. An unknown slug is a no-op, never a silent guess. |
| `--no-fire` | Re-arm without delivering the missed occurrence: the cursor is reset forward and the next send is the next scheduled one. |
| `--db <path>` | Path to the durable job store. Same resolution as `run`: flag → `LUCID_SCHEDULER_DB` → the default under the OS user-config dir. |
| `--json` | Emit `{reconciled: [{slug, was_active, next_run, fires_missed}], scanned}`. |

**Safe to run any time.** It is idempotent — running it when nothing is parked
changes nothing and reports nothing re-armed. It never creates a duplicate
periodic, never edits a cron expression, never moves a healthy periodic's next
run, and never touches a slug outside the table above. It sends nothing itself:
it re-arms the periodic, and the daemon's next tick does the sending.
Deterministic, no model.

```sh
lucid scheduler reconcile                          # repair everything parked
lucid scheduler reconcile --slug lucid-tripwire    # just the morning dead-man
lucid scheduler reconcile --no-fire                # re-arm, skip the missed send
lucid scheduler reconcile --json
```

#### scheduler install

```
lucid scheduler install [--out <dir> | --apply] [--force] [--json]
                        [--lucid <path>] [--hush <path>] [--scheduler-db <path>]
                        [--supervise-config <path>] [--hush-server <url>] [--machine-index <n>]
```

Lay down the supervised-daemon artifacts that keep `scheduler run` alive across
reboots: a launchd job that runs **`hush supervise`**, and the hush supervise
config it reads. The launchd job never names `lucid` — hush injects the harness
token at spawn and execs the scheduler, so the binary stays credential-dumb
([ADR-0005](../adr/0005-secrets-management.md)). The rendered artifacts **name**
`LUCID_HARNESS_TOKEN` (in the supervise `scope`) and **carry no value**; the two
logical channel IDs are non-secret env (`LUCID_USER_CHANNEL_ID`,
`LUCID_WITNESS_CHANNEL_ID`), read from the environment, never from `lucid.json`.

Every render is linted by the `deploy` package before it is shown or written, so
an artifact that failed its dry-run is never emitted. The command is opt-in about
host mutation — three modes, one flag-gated command:

| Mode | Effect | Platform |
|------|--------|----------|
| `lucid scheduler install` (default) | Render + lint + **print** both artifacts to stdout. Zero mutation. | any |
| `lucid scheduler install --out <dir>` | Render + lint + **write** both files to `<dir>`. No load. | any |
| `lucid scheduler install --apply` | Write the plist to `~/Library/LaunchAgents/`, the supervise config to `~/.hush/supervisors/lucid-scheduler.toml`, `launchctl bootstrap`, and verify. | macOS |

| Flag | Effect |
|------|--------|
| `--out <dir>` | Write the two files to `<dir>` instead of printing. Mutually exclusive with `--apply`. |
| `--apply` | Perform the host install (macOS). On a non-macOS host it returns an unsupported-host error and prints the manual guidance instead. |
| `--force` | On `--apply`, replace an already-loaded job (`bootout` then `bootstrap`) instead of refusing. |
| `--lucid <path>` | The lucid binary the child command runs (default: this executable, symlinks resolved). |
| `--hush <path>` | The hush binary launchd execs (default: `hush` on `PATH`, else a placeholder + a "hush not on PATH" note). |
| `--scheduler-db <path>` | The job store the plist pins as `LUCID_SCHEDULER_DB` (default: the daemon's resolved path, so `scheduler status` never reports drift). |
| `--supervise-config <path>` | Where the supervise config is written / referenced (default: `~/.hush/supervisors/lucid-scheduler.toml`). |
| `--hush-server <url>` / `--machine-index <n>` | Fill the supervise `server_url` / `client_machine_index`; unset keeps the example defaults and notes to edit them. |

`--json` emits `{plist, supervise, hush_present, notes}` (the rendered bodies,
whether hush was found, and any provisioning notes). On `--apply` success the
command reuses the `scheduler status` host probe and points you there —
**success is the launchd job loaded**, not a momentary process, so the launchd
spawn race never reads as a false negative. When `hush` is absent the artifacts
are still laid down but the command does **not** claim success (a supervised send
needs the token hush injects).

```sh
lucid scheduler install                    # inspect the artifacts (no mutation)
lucid scheduler install --out ./deploy-out # write them for review
lucid scheduler install --apply            # install + load the launchd job (macOS)
```

#### scheduler uninstall

```
lucid scheduler uninstall [--dry-run] [--label <label>] [--json]
```

Tear the launchd job down: `launchctl bootout` then remove the plist. It is
**idempotent** — an already-absent job or plist is a clean no-op, not an error —
and never touches `~/.lucid/` or the disposable job store. On a non-macOS host it
prints manual guidance. `--dry-run` reports what it would remove and touches
nothing; `--label` overrides the job label (default `com.lucid.scheduler`).

```sh
lucid scheduler uninstall --dry-run        # preview
lucid scheduler uninstall                  # bootout + remove the plist (macOS)
```

### storm

```
lucid storm <clause-label|unwritten|end> [--day <date>] [--json]
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

| Flag | Effect |
|------|--------|
| `--day <date>` | Date the storm event itself — available on **declare, renew, and end** — using the shared grammar on the strict tier ([Backdating with --day](#backdating-with---day)). The case it exists for is the honest one: on the days a storm is worst, declaring it at the time is exactly what you could not do. The `through` date follows from the backdated instant, and a renew or end dated to a moment when no storm stood is rejected as before. Storm history is folded in order, so an event dated **before the last recorded storm event** is rejected as incoherent and nothing is written. |

```sh
lucid storm wrist-flare
lucid storm unwritten
lucid storm end
lucid storm unwritten --day @yesterday
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

#### reflect week

```
lucid reflect week [--json] [--week | --since <YYYY-MM-DD> | --days <N>]
lucid reflect week apply [--json]
lucid reflect week close [--json]
```

The **read-only weekly deep-dive** — it reads everything since your last
reflection, frames one calm hypothesis through your active lens, and creates no
record. The window is a **catch-up** window, not a fixed calendar week: a skipped
week is read on the next run rather than dropped. The three range flags are
mutually exclusive — `--week` restores the ISO-week-only behavior, `--since` runs
from an explicit day, and `--days` covers the last `N` logical days. The default
window is capped at `reflect_week_max_days` (35); when the cap bites the output
says how many days it deferred, and `--since` reads the full span. `--json` adds
`window_start`, `window_end`, `days_covered`, `capped`, and `uncovered_days`
alongside the retained `iso_week` (the ISO week of the window's end day).

**`apply`** is the write path — it routes a surfaced candidate plus your response
through the same resonance gate every proposal passes. **`close`** stamps the
reflected-through cursor so the next run starts where this one ended; reading
alone never moves it, so an abandoned sit-down re-reads instead of skipping days.
Full guide: [`weekly-reflection.md`](weekly-reflection.md).

```sh
lucid reflect week
lucid reflect week --since 2026-07-13 --json
lucid reflect week close
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
fallback, and a back-off/safety door), today's **daily anchor** with its current
program-week targets (dropped when the program defines no anchor), and a read-only
progress panel (workout streak, frequency, skipped days, recent body response). The
pick is never the model's: with the provider unreachable the message still renders
deterministically (only the phrasing warmth is lost). `--json` emits the decided
`{recommendation, trend, anchor}` projection instead of the rendered text.

**`lucid workout log`** captures a completed session two ways — a spoken drop
(extracted by the model, the voice-first default) or the structured flags
(`--type --duration --rpe --parts --soreness --pain --notes`) for guided or backfill
capture. The two forms are mutually exclusive. Each writes a `workout` observation
(plus one `body_state` reading per soreness/pain flag) to the Ledger; the readings
are what the recommender reads back for the recovery and pain guardrails.
`--anchor` (with optional repeatable `--anchor-item name:count`) logs the **daily
anchor** through the same verb — a self-report is enough, counts are inventory when
given, and an anchor writes no body parts so it opens no recovery window.

| Flag | Effect |
|------|--------|
| `--day <date>` | Record the session on a prior logical day, using the shared grammar on the strict tier ([Backdating with --day](#backdating-with---day)). It is not a content flag, so it composes with **both** capture forms — the spoken drop and the structured flags. Every derived `body_state` reading inherits the same instant, precision, and logical day as the session itself, so the recovery guardrail (which reads `occurred_at`) and the progress trend (which reads `logical_date`) can never disagree about when the session happened. |

**Provider-backed** for phrasing (the `provider` block), with a deterministic
fallback; the recommendation itself and the capture parser are model-free.

```sh
lucid workout                          # today's recommendation, phrased
lucid workout --json                   # the decided recommendation + trend
lucid workout log "did pull, shoulder felt fine, ~50 min"
lucid workout log --type legs --duration 45 --rpe 7 --soreness quads:5 --pain knee:7
lucid workout log --type push --rpe 6 --day @yesterday   # yesterday's session
lucid workout log "2 mile bike ride" --day @yesterday    # spoken, backdated
lucid workout log --anchor --anchor-item squats:55   # today's daily anchor
```

### Provider configuration (agentic verbs)

`serve`, `reflect`, `ask`, and `structure` are the **provider-backed** verbs — they
need a model backend, configured by the `provider` block in `lucid.json` (ADR-0006,
no API keys):

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
lucid injury <name> [--status active|managed|resolved] [--onset <date>]
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

`--onset` runs on the **strict** tier of the shared grammar
([Backdating with --day](#backdating-with---day)) and accepts exactly
`@yesterday`, `YYYY-MM-DD`, `YYYY-MM`, and `YYYY`:

- **Free text is rejected.** `--onset "spring 2015"` is a clean error naming the
  accepted forms, and nothing is written. Re-type it as `2015-03`, or keep the
  prose in `--note` where it reads as testimony rather than a date.
- **A partial value is stored as typed.** `--onset 2014-09` stores `"2014-09"`
  at `approximate` precision — the registry is where "I know roughly when"
  belongs, so the degree of imprecision is kept rather than snapped away.
- **A future onset is rejected.** An injury cannot have begun on a day that has
  not happened.
- **A supplied time is parsed but not stored** — registry dates are
  date-granular, so `--onset "2014-09-01 19:30"` records `"2014-09-01"`.

```sh
lucid injury "left knee"
lucid injury "left knee" --status managed --onset 2014-09 --current-limitations "no deep squats" --json
```

### era

```
lucid era <name> [--start <date>] [--end <date>] [--note <text>] [--json]
```

Record or amend a life chapter in the `era` registry ([`life-archive.md`](life-archive.md);
[`../mvp/life-archive.md`](../mvp/life-archive.md) §4). Either bound may be
approximate; omit `--end` for a still-running chapter. Stories attach to an era
via `lucid memory --era <key>`, so the past becomes browsable by chapter. Same
create-then-amend, append-only merge, and `{kind, key, display_name, status,
created, fields}` `--json` shape as `injury`.

`--start` and `--end` take the same **strict** registry date rules as
[`injury --onset`](#injury) — `@yesterday`, `YYYY-MM-DD`, `YYYY-MM`, `YYYY`
only; free text rejected; a partial value stored as typed; a future date
rejected ([Backdating with --day](#backdating-with---day)). A chapter you expect
to end on a known future date waits until it does, or records the expectation in
`--note`.

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
             [--day <date>] [--attach <path> [--caption <text>]] [--json]
```

Record a story from your past as one `memory` observation, written at a backdated
`occurred_at` and linked to the era, place, and people it belongs to
([`life-archive.md`](life-archive.md); [`../mvp/life-archive.md`](../mvp/life-archive.md) §3).
The `memory` kind is **enable-gated** — like every observation kind it ships off;
a disabled kind prints the enable hint and writes nothing (exit `0`). `--certainty`
is the honesty field; `--era`/`--place`/`--people` become the story's `refs`;
`--day` is the shared backdate grammar — `@yesterday`, a full date, an optional
time, or an approximate `YYYY-MM` / `YYYY` (see
[Backdating with --day](#backdating-with---day)).

**`--day` is strict here.** A token the grammar cannot read, or a day in the
future, is a hard error and **nothing is written** — where a bad value once
recorded the story at *now* without saying so. `@yesterday` is also
rollover-aware: before 04:00 it resolves one day earlier than it used to. Both
are deliberate — a story filed under the wrong decade is the kind of quietly
false record the archive exists to avoid. When `--attach` is present the media
inherits the same resolved day, so a story and its photo never split.

**Optional media, never a gate:** `--attach <path>` reuses
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

### structure

```
lucid structure <raw_id> [--force] [--json]
lucid structure --since <YYYY-MM-DD> [--until <YYYY-MM-DD>] [--force] [--json]
```

The **standalone Structuring pass** — distill a raw entry this run did not
capture into its processed artifact. Until now the pass ran only as a side effect
of a guided check-in, so an entry captured any other way had no shipped route to
a processed artifact; this verb is that route. It runs Structuring and nothing
else: it never proposes a pattern (proposals stay inside a check-in session) and
it never modifies the raw entry it read.

**Two modes, one verb.** A positional raw id structures that one entry. `--since`
structures every raw entry in an inclusive civil-date window, with `--until`
defaulting to today. The two forms are mutually exclusive: passing both, passing
neither, passing `--until` without `--since`, or giving a `--since` later than
`--until` is a usage error (exit `2`).

**Every entry, no filtering.** The ranged mode covers every raw entry in the
window whatever command produced it — [`log`](#log) and [`closeout`](#closeout)
journal lines included — per
[`../mvp/agent-contracts.md`](../mvp/agent-contracts.md) §"How contracts
compose". There is no per-command filter, by design: which captures are worth
distilling is a reader's judgment, not the binary's.

**Skip by default, `--force` to redo.** An entry that already has a processed
artifact is skipped with no model call. `--force` re-structures it — the pass is
idempotent, so a re-run overwrites the artifact differing only in its
produced-at stamp, and leaves the raw entry untouched. Skipping is what makes an
interrupted window run resumable: re-run the same command and it pays only for
what it has not already done, so a repeat run over a settled window is free.

**Counts.** Every run reports attempted / written / skipped / degraded / failed.
`written` and `degraded` overlap on purpose, and the overlap is the ordinary case
rather than an edge — a degraded extraction still produces an artifact (the model
call failed and its stricter retry failed too, or the raw entry's body was
empty), so such an entry counts in both. The per-entry `outcome` reports the
single most informative label instead, with a fixed precedence of `failed` over
`degraded` over `wrote` over `skipped`. A run carrying any failed entry exits
non-zero; a run that only skipped is a success.

**Progress.** In ranged mode one progress line per entry goes to **stderr** while
stdout carries only the summary, so a wide window shows forward motion and
`--json` output stays parseable. Single-entry mode prints the summary alone.

`--json` emits `{mode, since, until, force, attempted, written, skipped,
degraded, failed, entries}`, where each element of `entries` is `{raw_id,
outcome}` and `outcome` is one of `wrote`, `skipped`, `degraded`, or `failed`;
arrays are never null.

**Provider-backed** (the `provider` block, like [`ask`](#ask) and
[`reflect`](#reflect) — see
[Provider configuration](#provider-configuration-agentic-verbs)): one model call
per entry actually structured, and a second when an extraction has to retry. A
wide window is therefore a long-running command, while a window with nothing new
in it costs no model call at all.

```sh
lucid structure raw_2026_01_14_09_30
lucid structure raw_2026_01_14_09_30 --force
lucid structure --since 2026-01-01
lucid structure --since 2026-01-01 --until 2026-01-07 --json
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

The scheduled Engine sends — the bell and the morning tripwire (L1/L2) — are the
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
| `LUCID_SCHEDULER_DB` | Optional override for the scheduler's durable Engine job-store path; `--db` (on `run` and `reconcile`) or `--scheduler-db` (on `status`) overrides it. Defaults outside `~/.lucid/` (disposable machinery, ADR-0004). |
| `LUCID_COMPANION_DB` | Optional override for the companion's disposable job-store path, read by `lucid scheduler status`; `--companion-db` overrides it. Defaults under the OS user-config dir, outside `~/.lucid/`. |
| `LUCID_WITNESS_REPORT_DB` | Optional override for the weekly witness report's disposable job-store path. Defaults under the OS user-config dir, outside `~/.lucid/` (disposable machinery, never the record). |
| `LUCID_WORKOUT_DB` | Optional override for the workout companion's disposable job-store path. Defaults under the OS user-config dir, outside `~/.lucid/` (disposable machinery, never the record). |
