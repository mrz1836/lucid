# Lucid — Wheel of Life (monthly balance review)

**Date:** 2026-09-29 · **Status:** Canonical — a living concept, evolving with the project
**Scope:** The monthly **Wheel of Life**: a few-minute pulse-check where you
rate each of eight life pillars on your own 1–10 scale, see which spokes are
strong and which are sagging, and watch each pillar move month to month. This
document defines the wheel record family, its append-only store under
`~/.lucid/registries/wheel/`, the month-keyed snapshot history the trend is
folded from, the separate `suggested` calibration field, the vision-reflection
fields, and the `lucid wheel` CLI surface (`add` / `show` / `list`). It
contains no instance data; every example here is synthetic.

## 0. The governing corollary

**A wheel is an inventory of balance, never a scorecard.** This layer carries
the same sanctuary stance the observation layer states for the body
([`observations.md`](observations.md) §0) and the gratitude tally states for
thanks ([`gratitude.md`](gratitude.md) §0): a pillar rating is a number *you*
chose to name about your own month, not a target the system grades. The wheel
shows where things stand and how they moved so the balance is visible — it
never computes a balance score, an average, or a total; never scores a streak
of months reviewed; never sets a target for a pillar; and never nudges you
toward bigger numbers. A low spoke is information, not a failure.

**Your rating is the record.** The number stored for a pillar is the one you
gave, exactly as you gave it. Lucid never fabricates, infers, rounds, softens,
or defaults a pillar rating, and nothing a model or a companion reads from the
month can set one (§5). Evidence may help you remember the month before you
rate it; it never decides the number.

## 1. Position in the foundation

Lucid's foundation is three parts ([`architecture.md`](architecture.md)), and
this layer adds instances of two without changing any of them:

| Part | Holds | This layer adds |
|------|-------|-----------------|
| **Ledger** (append-only) | What happened / what was said | A new registry kind: `wheel` entries, one per calendar month, each with an append-only history of `snapshot` events |
| **Projections** (rebuildable views) | What it means | The month-over-month trend — each pillar's current rating, its delta vs the prior month, a sparkline, and the low/drop callouts — folded from those snapshots at read time |

The wheel is a **new registry kind, not an observation kind** — the same
net-new-kind decision the pet registry and the gratitude tally made
([`mvp/life-archive.md`](mvp/life-archive.md) §"Pet registry kind"). A month's
wheel is a long-lived referent with its own history: you record it, you may correct it,
and it is read back for as long as the practice runs. It diverges from the
other registries in two ways. Its key is the **month itself**, not a salted
phrase slug (§4), because a calendar month is not sensitive and must stay
enumerable in order for `list` and the delta to work. And it carries a typed
**snapshot history** in place of `status_history`, because a wheel needs to
fold *the latest rating per month*, not a status transition.

`internal/storage` is the **only** package that touches `~/.lucid/`
(architecture P3). The wheel record family, the validation, the fold, and
every read and write are deterministic, agent-free, and carry **no LLM**
(architecture P9): `wheel add`, `wheel show`, and `wheel list` complete with
no model configured, no provider reachable, and no companion present. The
structured `wheel add` is a complete terminal path on its own.

The wheel lives under `registries/`, so it is primary, backup-critical data:
`lucid backup` already carries it, and the sanctuary denylist that keeps
`~/.lucid/registries/` away from agent inference
([`mvp/agent-contracts.md`](mvp/agent-contracts.md) §"Cross-cutting rules")
covers it unchanged. A harness reaches it only through the `lucid wheel` verbs.

## 2. The pillars

A wheel has exactly **eight pillars**, fixed for v1. Each has a short **key**
(used by the flags and the JSON) and a **label** (used in human output):

| Key | Label | What you are rating |
|-----|-------|---------------------|
| `health` | health | Your body and energy — sleep, movement, food, and how you feel physically and mentally, day to day. |
| `relationships` | relationships | The people closest to you — partner, family, and friends — and how connected, honest, and cared-for those bonds feel. |
| `career` | career/work | The work you do — whether it is meaningful, how it is going, and whether it is headed where you want it to go. |
| `finances` | finances | Your money — security, direction, and how at ease you are with where it stands. |
| `growth` | personal growth | Learning and becoming — the skills, habits, and inner work that make you more of who you want to be. |
| `fun` | fun/recreation | Play and rest — the things you do purely because you enjoy them, and whether there is room for them. |
| `environment` | environment | Your surroundings — home, workspace, and the places you spend your days — and whether they support you. |
| `contribution` | contribution | Giving beyond yourself — service, generosity, and the difference you make for others. |

The order above is the **canonical order**: human output, `--json` arrays, and
tie-breaks all follow it. Every snapshot rates all eight (§3); there is no
partial wheel. Adding, removing, or renaming a pillar is a schema change (§4
Versioning), never a silent edit.

## 3. The scale — its own 1–10

Each pillar is rated on an integer **1–10** scale: 1 is as bad as that part of
life gets for you, 10 is as good as you can imagine it. It is the wheel's
**own scale** and is named and validated as such everywhere:

* It is **not** the Engine's **1–5 capacity** scale (the close-out capacity
  digit, [`mvp/engine-module.md`](mvp/engine-module.md)), and it is **not** the
  **1–7 Bristol** scale the observation micro-log uses for elimination
  ([`observations.md`](observations.md) §3). The three never mix: a wheel rating
  is never read as a capacity, a capacity is never read as a wheel rating, and
  no conversion between them exists.
* **Integers only.** `6` is a rating; `6.5`, `6.0` written as a decimal, `"6"`
  as a JSON string, `0`, `11`, and a negative number are not.
* **Every pillar is required.** A snapshot with any of the eight ratings
  missing or blank is refused — the missing rating is never filled in from a
  prior month, a `suggested` value, a default, or anything else.
* A missing, out-of-range, or non-integer rating is a **clean error**: the
  command exits non-zero, names the pillar and the accepted range, and
  **nothing is written**.

## 4. The entry schema

Every month's wheel is one JSON object — one file per month — under
`~/.lucid/registries/wheel/`, keyed by the calendar month:
`registries/wheel/wheel_2026-09.json`. The month key is deterministic and
non-sensitive; the ratings, notes, and reflection (the sensitive content) live
**inside** the file, so filenames stay low-signal — a directory listing shows
only which months have a wheel.

```json
{
  "key": "wheel_2026-09",
  "kind": "wheel",
  "schema": 1,
  "month": "2026-09",
  "history": [
    {
      "id": "wheel_2026_09_001",
      "at": "2026-09-06T17:12:40-04:00",
      "type": "snapshot",
      "pillars": {
        "health":        { "score": 6, "note": "moving most days", "suggested": 5 },
        "relationships": { "score": 7 },
        "career":        { "score": 5, "suggested": 6 },
        "finances":      { "score": 4, "note": "one large repair" },
        "growth":        { "score": 7 },
        "fun":           { "score": 4 },
        "environment":   { "score": 6 },
        "contribution":  { "score": 5 }
      },
      "vision_reviewed": true,
      "vision_reflection": "Still true. The pace shifted, not the direction."
    }
  ],
  "created_at": "2026-09-06T17:12:40-04:00",
  "updated_at": "2026-09-06T17:12:40-04:00"
}
```

Fields, binding:

| Field | Required | Meaning |
|-------|----------|---------|
| `key` | assigned | The stable entry id: `wheel_YYYY-MM`, derived from the month (§ Ids). |
| `kind` | yes | Always `wheel`. |
| `schema` | yes | The record schema version. `Schema = 1` today (§ Versioning). |
| `month` | yes | The calendar month this wheel covers, `YYYY-MM`. |
| `history` | yes | The **append-only** list of `snapshot` events. Each carries its own receipt id, a real write timestamp `at`, and the complete wheel as recorded by that write. The current wheel for the month is **derived** from it (§ Deriving the month); no "current" field is stored. |
| `created_at` | yes | When the month's first snapshot was written — an ISO-8601 local-TZ timestamp. |
| `updated_at` | yes | When the most recent snapshot was appended. |

**The snapshot event.** Every `wheel add` appends exactly one `snapshot`; no
event is ever rewritten. A snapshot is a whole wheel, never a partial patch:

| Snapshot field | Required | Meaning |
|----------------|----------|---------|
| `id` | yes | The write's receipt id (§ Ids). |
| `at` | yes | The real write time, ISO-8601 with the host's local-TZ offset. |
| `type` | yes | Always `snapshot` in schema 1. |
| `pillars` | yes | A map of all eight pillar keys (§2) to a pillar object. |
| `pillars.<key>.score` | yes | **Your** rating for the pillar: an integer 1–10 (§3). The only number the trend reads. |
| `pillars.<key>.note` | no | An optional one-line "why", in your words, stored verbatim. Omitted when unset. |
| `pillars.<key>.suggested` | no | An optional calibration value, integer 1–10, kept **separate** from `score` (§5). Omitted when unset. |
| `vision_reviewed` | yes | Whether you re-read your vision this month (§6). `false` when not. |
| `vision_reflection` | no | A short free-text reflection on that re-read, stored verbatim (§6). Omitted when unset. |

**Deriving the month — amend by append, latest wins.** A month's current wheel
is a pure fold over its `history`, computed at read time and never stored: it
is the **latest snapshot** (the last one appended). A second `wheel add` for a
month that already has a wheel is not refused and does not mutate anything — it
appends a new snapshot to the same file, which then wins the fold. The earlier
snapshot stays on disk with its own receipt, so a correction is frictionless
and fully auditable: every attempt keeps its receipt, nothing is deleted, and
`show` reads the latest. There is no merge, tombstone, or partial overlay — a
snapshot replaces the month's wheel *as read*, never the record *as written*.

**Ids — two, kept distinct.** The month has a **stable id** and each write has
its own **receipt id**; they are never interchangeable:

* The **stable entry id** is the registry `key`, `wheel_YYYY-MM` (e.g.
  `wheel_2026-09`) — one per month, stable forever, shown by `list`.
* The **receipt id** is `wheel_YYYY_MM_<seq>` (e.g. `wheel_2026_09_002`) — one
  per appended snapshot, minted under the storage adapter's single-writer
  discipline: `seq` is the max seq parsed from that month's history plus one,
  never a count, zero-padded to three digits (wider values legal). Every
  `wheel add` returns *its* snapshot's receipt, so two writes to the same month
  return two different receipts.

The two shapes never collide: the entry key separates year and month with a
hyphen and has no sequence (`wheel_2026-09`); the receipt uses underscores and
always ends in a sequence (`wheel_2026_09_001`).

**Versioning.** `schema` is versioned per record family, exactly as the
observation envelope and the other registries are
([`observations.md`](observations.md) §2). New needs go in a new optional field
or a new event `type` under a bumped `schema` — readers tolerate an unknown
field and a higher schema version (read what you understand, skip what you
don't).

**Append-only.** No snapshot is ever rewritten in place. The store is built to
outlive its tools (P6): plain user-owned JSON, one file per month, exportable
as a directory of files.

## 5. The `suggested` field — calibration, never the score

A companion that walks you through the monthly review may read the month's
evidence — your raw entries, observations, gratitude, reframes, retro notes —
and form its own guess at each pillar. The wheel can keep that guess **beside**
your rating, in the per-pillar `suggested` field, so later months can compare
how the evidence read against how the month felt. It is calibration data, and
it is fenced off from everything that matters:

* **Never the score.** `suggested` is a separate field. It is never written
  into `score`, never used when a `score` is missing (a missing score is
  refused, §3), and never rendered as a pillar's rating.
* **Never before you rate.** The binary accepts a `suggested` value **only**
  in the same write that carries all eight of your own ratings — there is no
  way to store a suggestion for a month you have not rated, and nothing reads
  a suggestion back before its month is recorded. The conversational half of
  this rule — show evidence as *facts only, with no number*, before you rate —
  belongs to the companion, and is the reason the field exists at all: a gap
  between your number and the evidence's is worth talking about **after** you
  rate, as an optional prompt, never as a correction to your number.
* **Never in the trend.** The current rating, the delta, the sparkline, the
  lowest and biggest-drop callouts (§7) are computed from `score` alone.
  Changing or removing every `suggested` value changes none of them.
* **Never computed by Lucid.** No wheel path calls a model (§1). Lucid stores a
  `suggested` value a caller supplies; it never derives one, and an absent
  `suggested` is simply absent.

`suggested` uses the same 1–10 integer scale as `score` and the same
validation (§3): a non-integer, out-of-range, or unknown-pillar suggestion is a
clean error and nothing is written. It is surfaced in exactly one place — the
`calibration` block of `wheel show --json` (§7) — and never in any human
output.

## 6. The vision reflection

The monthly review is also the natural home for a **vision re-read**: once a
month, after rating the pillars and looking at the trend, you re-read whatever
states your direction and ask two questions — *is it still true? what
shifted?* The wheel records only the outcome of that step:

* `vision_reviewed` — a plain yes/no: did you do the re-read this month.
  Supplying a non-empty reflection implies `true`.
* `vision_reflection` — an optional short free-text reflection, stored
  verbatim.

That is all the binary stores. **There is no source link:** no path, URL, or
pointer to a vision document is part of the schema. Where your vision lives,
and whether a companion points you at it each month, is private instance
configuration outside this repository — the wheel keeps the reflection, never
the document.

## 7. The CLI surface — `lucid wheel`

```
lucid wheel add --health <1-10> --relationships <1-10> --career <1-10> --finances <1-10>
                --growth <1-10> --fun <1-10> --environment <1-10> --contribution <1-10>
                [--note <pillar>=<text>]... [--suggested <pillar>=<1-10>]...
                [--vision-reviewed] [--vision-reflection <text> | --vision-reflection-file <path>]
                [--month <YYYY-MM>] [--json]
lucid wheel add --input <path> [--json]
lucid wheel show [--month <YYYY-MM>] [--json]
lucid wheel list [--json]
```

### 7.1 `wheel add` — record a month

`add` records one month's whole wheel as a single `snapshot` and prints the
write's receipt id. It is the one write, and it is the same write whether you
type it at a terminal or a companion runs it at the end of a guided
conversation.

* **The eight ratings are required flags** — `--health`, `--relationships`,
  `--career`, `--finances`, `--growth`, `--fun`, `--environment`,
  `--contribution` — each an integer 1–10 (§3). With any missing, `add` is a
  usage error naming the missing pillars, and nothing is written. v1 has no
  interactive pillar-by-pillar prompt.
* **`--note <pillar>=<text>`** (repeatable) stores that pillar's optional
  one-line "why" verbatim. An unknown pillar key or an empty text is a clean
  error.
* **`--suggested <pillar>=<1-10>`** (repeatable) stores that pillar's optional
  calibration value (§5). An unknown pillar key or an invalid value is a clean
  error.
* **`--vision-reviewed`** records that you did the vision re-read (§6).
  **`--vision-reflection <text>`** stores the reflection verbatim;
  **`--vision-reflection-file <path>`** reads it from a file, or from `-`
  (stdin), keeping punctuation and multiline text off the command line — the
  inline flag and its file sibling are mutually exclusive, and an empty file
  is rejected. A non-empty reflection implies `--vision-reviewed`.
* **`--month <YYYY-MM>`** names the month the wheel covers (a leading `@` is
  tolerated). The default is the month of the current **logical day** (04:00
  rollover), so a review finished just after midnight on the 1st still files
  under the month it reviewed. A malformed month, or one after the current
  logical month, is a clean error and nothing is written; any earlier month
  may be recorded, so a missed month can be filled in late.
* **`--input <path>`** reads the whole wheel from a JSON document instead — a
  file, or `-` for stdin. It is the structured path a companion uses, keeping
  eight notes and a reflection off the command line. It is mutually exclusive
  with every other `add` flag except `--json`. The document is strict — an
  unknown key, an unknown pillar, a missing pillar, or a non-integer value is
  a clean error and nothing is written:

  ```json
  {
    "month": "2026-09",
    "pillars": {
      "health":        { "score": 6, "note": "moving most days", "suggested": 5 },
      "relationships": { "score": 7 },
      "career":        { "score": 5, "suggested": 6 },
      "finances":      { "score": 4, "note": "one large repair" },
      "growth":        { "score": 7 },
      "fun":           { "score": 4 },
      "environment":   { "score": 6 },
      "contribution":  { "score": 5 }
    },
    "vision_reviewed": true,
    "vision_reflection": "Still true. The pace shifted, not the direction."
  }
  ```

  `month` is optional (the same default as `--month`); `note`, `suggested`,
  `vision_reviewed`, and `vision_reflection` are optional.

**Same month again — amend by append.** Recording a month that already has a
wheel appends a second snapshot, which becomes the month's wheel on the next
read; the first stays on disk with its receipt (§4). `add` says so in its ack.

**The ack.** `add` acknowledges only *after* the write lands — the same
provenance-over-magic ack every capture gives — naming the month and the
receipt, then echoing the eight stored ratings verbatim, one `label: N` line
each (never the `suggested` values). On an amend it adds that the new snapshot
replaces the earlier one when read, and that both are kept:

```
Recorded the 2026-09 wheel as wheel_2026_09_002 (replaces wheel_2026_09_001 when read; both kept).
health: 6
relationships: 7
career/work: 5
finances: 4
personal growth: 7
fun/recreation: 4
environment: 6
contribution: 5
```

### 7.2 `wheel show` — the trend

`show` folds the latest snapshot for each stored month and renders the wheel
for one month — by default the most recent — against the **most recent prior
stored month**. It is a pure read: it writes nothing and calls no model.

* **One line per pillar**, in canonical order: `label: N (±d) <sparkline>`.
  `N` is your stored rating, printed exactly as stored — never rounded,
  averaged, smoothed, or softened. `(±d)` is the signed delta against the prior
  stored month (`(+1)`, `(-2)`, `(0)`, with an ASCII sign). The sparkline is
  that pillar's rating over up to the **last six stored months**, oldest first.
* **The comparison month is named** in the header (`vs 2026-08`). A month with
  no wheel is skipped, not counted as zero — if the prior stored month is not
  the calendar month before, the header says which month the delta is against.
* **Callouts.** `lowest:` names every pillar tied at the month's lowest rating,
  with the rating. `biggest drop:` names every pillar tied at the most negative
  delta, with the delta — or `none` when no pillar dropped.
* **No prior month.** When the shown month is the first one recorded, the
  header says `no prior month`, the pillar lines carry no delta (never a
  fabricated `(+0)`), and `biggest drop:` reads `no prior month`. With no wheel
  recorded at all, `show` prints `no prior month — no wheel recorded yet` and
  exits `0`.
* **Notes and vision.** Pillars with a note are listed under `notes:`, one
  `- label: note` line each; then `vision reviewed: yes|no` and, when present,
  `vision reflection:` with the text verbatim.
* **`--month <YYYY-MM>`** shows that month against its own prior stored month,
  with the sparkline ending at that month. A month with no wheel is a clean
  error.
* **Discord-safe.** Human output is `key: value` lines and `- ` bullets only —
  **never a markdown table** — so it renders cleanly when relayed into a chat.
  There is no average, total, or balance score anywhere in the output (§0).

Synthetic example:

```
wheel: 2026-09 (vs 2026-08)
health: 6 (+1) ▃▄▄▅
relationships: 7 (0) ▆▆▆▆
career/work: 5 (-1) ▅▅▅▄
finances: 4 (-2) ▅▅▅▃
personal growth: 7 (0) ▄▅▆▆
fun/recreation: 4 (0) ▄▃▃▃
environment: 6 (0) ▅▅▅▅
contribution: 5 (0) ▃▃▄▄
lowest: finances (4), fun/recreation (4)
biggest drop: finances (-2)
notes:
- health: moving most days
- finances: one large repair
vision reviewed: yes
vision reflection: Still true. The pace shifted, not the direction.
```

**The sparkline** maps each rating onto eight fixed block characters — a fixed
scale, never auto-scaled to the pillar's own range, so the same block always
means the same rating band on every pillar and every month:

| Rating | 1 | 2 | 3–4 | 5 | 6 | 7–8 | 9 | 10 |
|--------|---|---|-----|---|---|-----|---|----|
| Block | `▁` | `▂` | `▃` | `▄` | `▅` | `▆` | `▇` | `█` |

(Equivalently, block index = `((rating − 1) × 7 + 4) ÷ 9` in integer
arithmetic.) The sparkline is shape only; the rating and the delta carry the
exact numbers.

**`--json`** emits the same view structured, with every array present and never
null:

```json
{
  "month": "2026-09",
  "prior_month": "2026-08",
  "receipt_id": "wheel_2026_09_002",
  "months": ["2026-06", "2026-07", "2026-08", "2026-09"],
  "pillars": [
    { "pillar": "health", "label": "health", "score": 6, "delta": 1, "trend": [4, 5, 5, 6], "sparkline": "▃▄▄▅", "note": "moving most days" }
  ],
  "lowest": [ { "pillar": "finances", "score": 4 }, { "pillar": "fun", "score": 4 } ],
  "biggest_drop": [ { "pillar": "finances", "delta": -2 } ],
  "vision_reviewed": true,
  "vision_reflection": "Still true. The pace shifted, not the direction.",
  "calibration": { "suggested": { "health": 5, "career": 6 } }
}
```

`pillars` holds all eight in canonical order (one shown above). `trend[i]` is
the rating in `months[i]`. `delta` is `null` and `prior_month` is `""` when
there is no prior month; `biggest_drop` is `[]` then, and when nothing dropped.
`note` and `vision_reflection` are `""` when unset. `calibration.suggested`
carries the shown month's stored `suggested` values (§5) — `{}` when none — and
is the only place they appear; nothing else in the payload is derived from
them. With no wheel recorded, `month` is `""` and every array is `[]`.

### 7.3 `wheel list` — the months

`list` prints one line per recorded month, **most recent first**: the month,
its latest snapshot's receipt id, the snapshot count when the month was
amended, and whether the vision was reviewed.

```
2026-09: wheel_2026_09_002 (2 snapshots) · vision reviewed
2026-08: wheel_2026_08_001 · vision reviewed
2026-07: wheel_2026_07_001
```

With no wheel recorded, `list` prints `no prior month — no wheel recorded yet`
and exits `0` — never an invented row or delta. `--json` emits `{months:
[{month, entry_id, receipt_id, snapshots, recorded_at, vision_reviewed}]}`,
most recent first, where `recorded_at` is the latest snapshot's `at` and
`months` is `[]`, never null, when empty. `list` is a pure read and calls no
model.

### 7.4 `--json` on `add`

`add --json` emits `{receipt_id, entry_id, month, amended, scores}` — the
snapshot's receipt, the month's stable id, the month, whether the month already
had a wheel before this write, and the eight stored ratings as a
`pillar key → integer` map. Like every `--json` stream, stdout carries JSON
only; diagnostics stay on stderr.

## 8. Boundaries and defaults

* **Sanctuary and inventory, not obligation** (§0). No balance score, average,
  or total; no streak of months reviewed; no targets; no "you haven't done your
  wheel" nudge from Lucid. Skipping a month leaves a gap, not a debt.
* **Self-report is sovereign** (§3, §5). The stored rating is yours, verbatim;
  `suggested` never becomes, replaces, defaults, or moves it.
* **Deterministic, agent-free, no model** (§1). `add`, `show`, and `list`
  complete with no provider configured. `internal/storage` is the sole
  `~/.lucid/` writer; `internal/observations` holds the pure schema, validation,
  and fold, and imports no provider.
* **No sends.** The wheel posts nothing anywhere. A monthly reminder, if you
  want one, is a companion or harness schedule outside Lucid: the Engine
  scheduler's notifier sends only pre-committed one-shot templates
  ([`harness-integration.md`](harness-integration.md)), and the review itself is
  a multi-turn conversation, so it is companion-driven. A scheduler-native
  monthly opener is a possible later addition, not part of v1.
* **Append-only** (§4). Snapshots are never rewritten; a correction is a new
  snapshot that wins the fold; every write keeps its receipt.
* **Public-safe** ([`CLAUDE.md`](../CLAUDE.md) invariants). Every example in
  this document and in the tests is synthetic; real ratings, notes, and
  reflections live only in the private Ledger under `~/.lucid/`, never in the
  repo. The schema holds no pointer to any private document (§6).

**Defaults.** Eight fixed pillars in canonical order (§2) · integer 1–10 per
pillar, every pillar required (§3) · month key calendar `YYYY-MM`, defaulting
to the month of the current logical day (04:00 rollover), future months
rejected · one file per month under `registries/wheel/`, key `wheel_YYYY-MM`,
receipt `wheel_YYYY_MM_<seq>` · same-month re-entry appends a snapshot,
latest-wins on read · delta against the most recent prior **stored** month ·
sparkline over up to the last six stored months on a fixed 1–10 block scale ·
`suggested` stored only alongside a full rating, surfaced only in
`show --json`'s `calibration` block · `vision_reviewed` defaults to `false`; a
reflection implies `true` · `schema` = 1. All timestamps are ISO-8601 with the
host's local-TZ offset, the same rule as every record under `~/.lucid/`
([`mvp/data-model.md`](mvp/data-model.md) §"Time zone rule").
