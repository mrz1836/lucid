# Lucid — Reframe Layer (catch → flip)

**Date:** 2026-08-23 · **Status:** Canonical — a living concept, evolving with the project
**Scope:** The inner-work half of the Mirror's practice surface: the
self-talk **reframes** a person keeps — "when I catch myself saying X, I
say instead Y" (a *catch → flip* pair). This document defines the reframe
record family, its append-only store under `~/.lucid/reframes/`, the
verb-owned surface-state projection that rotates one reframe per day, and
the `lucid reframe` CLI surface. It contains no instance data; every
example here is synthetic.

## 0. The governing corollary

**Reframes are practice, never a scorecard.** This layer carries the same
sanctuary stance the observation layer states for the body
([`observations.md`](observations.md) §0): a reframe is a phrase you chose
to keep, not a task the system grades. Surfacing a reframe for the day is a
gentle reminder of your own words — it never asks whether you used it,
never counts a streak of "days you reframed," never scores follow-through.
The rotation bookkeeping in §4 tracks only *which reframe was shown when*
so the daily surface stays fair; it is not adherence. If a practice ever
deserves real accountability, it becomes an Engine commitment through a
Gate — a separate, deliberate act. Reframes stay inventory of your own
narration, and words are sanctuary.

## 1. Position in the foundation

Lucid's foundation is three parts ([`architecture.md`](architecture.md)),
and this layer adds instances of two without changing any of them:

| Part | Holds | This layer adds |
|------|-------|-----------------|
| **Ledger** (append-only) | What happened / what was said | A new record family: `reframe` entries |
| **Projections** (rebuildable views) | What it means | The surface-state rotation projection |

Reframes are a **new record family, not an observation kind.** The
observation envelope is frozen and observations are inventory that carries
**no** last-surfaced, streak, or selection state ([`observations.md`](observations.md)
§0, architecture P3). A rotating `surface` needs exactly that kind of
"which one did we show today" state, so wiring it onto an observation would
break the sanctuary rule. Reframes therefore get their own append-only
entry stream and a **separate** projection that holds the rotation state —
the entry history stays pure, and the mutable bookkeeping lives outside it
(§4). Like every record family, reframes are added only as a new kind, a
new store, and a new projection — never by changing the frozen observation
envelope.

`internal/storage` is the **only** package that touches `~/.lucid/`,
including the surface-state projection (architecture P3); the reframe
record family and its rotation logic are deterministic, agent-free, and
have **no LLM in any path** (architecture P9) — the daily surface completes
with no model.

## 2. The entry schema

Every reframe is one JSON object, appended verbatim as one line to a
per-logical-day file (`~/.lucid/reframes/YYYY/MM/reframe_YYYY_MM_DD.jsonl`):

```json
{
  "id": "reframe_2026_08_23_001",
  "schema": 1,
  "catch": "I can't do this",
  "flip": "I can learn this",
  "recorded_at": "2026-08-23T21:45:10-04:00",
  "logical_date": "2026-08-23",
  "source": "reframe",
  "tags": ["growth"],
  "refs": {}
}
```

Fields, binding:

| Field | Required | Meaning |
|-------|----------|---------|
| `id` | assigned | `reframe_<logical_date>_<seq>` — assigned by the storage adapter under the single-writer discipline (§ Ids). |
| `schema` | yes | The entry schema version. `Schema = 1` today. |
| `catch` | yes | The self-talk to notice — the line you want to catch yourself saying. Free text, verbatim. |
| `flip` | yes | The reframe to say instead — the line you flip to. Free text, verbatim. |
| `recorded_at` | yes | When the entry was written — always "now," an ISO-8601 local-TZ timestamp. |
| `logical_date` | yes | The day the reframe is attributed to (`YYYY-MM-DD`), the universal join key. Defaults to today's logical day; `--day` backdates it (§3). |
| `source` | yes | Provenance: `reframe` for a normal `lucid reframe add`, `migration` for a one-time bulk import. Distinguishes machine-relayed from hand-typed, on the same footing as an observation's `source`. |
| `tags` | no | Optional `#tags` copied into `tags[]` — obs-parity, for future theme filtering. No current surface requires them; an entry with none marshals as `[]`. |
| `refs` | no | A flat object of reserved relational keys. `corrects` names an entry this one supersedes (§ Append-only). An entry with none marshals as `{}`. |

**Versioning.** `schema` is versioned per record family, exactly as the
observation envelope is ([`observations.md`](observations.md) §2). New
needs go in `tags`, `refs`, or a new optional field under a bumped
`schema` — readers tolerate an unknown field and a higher schema version
(read what you understand, skip what you don't). The reframe schema is
documented and versioned here alongside the other Ledger record schemas.

**Ids.** `reframe_<logical_date>_<seq>` (concretely `reframe_YYYY_MM_DD_<seq>`):
the logical date in underscores,
`seq` = the max seq parsed from well-formed lines in the target day file,
plus one — computed under the storage adapter's single-writer discipline,
never from line count. Zero-padded to three digits, parsed numerically,
wider values legal (`_1000`). Every appended line — a fresh reframe or a
correction — consumes a seq. A backdated reframe's id encodes the
*logical* date, not the recording time (`reframe_2026_08_01_001` written
on August 23rd).

**Append-only, corrected by reference.** No line is ever rewritten. A
correction is a *new* entry whose `refs.corrects` names the target id;
readers fold corrections onto their targets at read time, and a superseded
entry drops out of `list` and out of `surface` selection. The original
line stays byte-identical forever — a reframe you refined is history you
can still read, exactly as the observation layer folds `refs.corrects`
([`observations.md`](observations.md) §2). Editing a reframe in place is
never an operation; you append the improved pair and the old one steps
aside.

**Byte-stability.** `encoding/json` sorts map keys and preserves struct
field order, so the same entry always marshals to the same bytes — the
property the single-writer append discipline relies on. An entry with no
tags or refs marshals as `[]` / `{}`, a stable on-disk shape across every
entry.

## 3. The capture grammar

Reframes are captured by a one-line, deterministic, agent-free verb — the
same design stance as the observation micro-log and the Engine
([`observations.md`](observations.md) §4, architecture P9): sub-second,
offline-capable, no LLM.

```
lucid reframe add "I can't do this" "I can learn this"
lucid reframe add "I always mess up" "I'm still practicing"
lucid reframe add "This is too hard" "This is worth the effort" --day @yesterday
```

Grammar, binding:

* **Two positional arguments:** `catch` then `flip`, both required, both
  stored verbatim. An empty `catch` or `flip` is a clean usage error —
  nothing is written.
* **`--day` backdating.** `--day` sets `logical_date` for the entry,
  reading the one shared date grammar every logical-day verb uses
  ([`usage/commands.md`](usage/commands.md#backdating-with---day)):
  `@yesterday` (the logical day before this one, 04:00-rollover aware),
  `@YYYY-MM-DD` (a civil day, taken literally), and the partial/`<day>
  <time>` forms. It is the **strict** tier — a token the grammar cannot
  read, or a day in the future, is a clean error naming the accepted forms,
  and **nothing is written.** With no `--day`, the entry files under
  today's logical day (the 04:00 rollover applied to now). `recorded_at`
  is always the real write time regardless of backdating.
* **`#tags`.** A `#tag` token is copied into `tags[]` for future theme
  filtering; obs-parity and optional.

Capture is total (P1): one line, no form, no follow-up. `lucid reframe
add` acknowledges only *after* the write lands and prints the receipt id
(§ Receipts), the same provenance-over-magic ack the observation micro-log
gives.

## 4. The surface — one reframe per day

`lucid reframe surface` returns **exactly one** reframe for the current
logical day and records that it was shown. It is the deterministic,
agent-free daily rotation the morning surface reads.

**The logical day.** The day key is the current logical day —
`DateString(LogicalBaseDate(now, DefaultRolloverMin))`, the 04:00 rollover
the observation and day surfaces already use, so "today" means the same
boundary everywhere in the Ledger.

**Within-day idempotence.** If the surface-state already records a pick for
today's day key, `surface` returns that same reframe **unchanged** and does
**not** advance. Repeated calls within the same logical day are idempotent
— the morning surface can call it more than once and always shows the same
reframe for the day.

**Least-recently-surfaced rotation.** On the first `surface` of a new
logical day, selection walks the non-superseded entries and picks the one
surfaced **least recently**, so the pool rotates fairly:

* An entry **never surfaced** sorts before any entry that has been
  surfaced — so a freshly added reframe enters the rotation, and a
  cold-start Ledger (nothing surfaced yet) is well-defined.
* Among entries with the same last-surfaced state — including the
  cold-start case where all are unsurfaced, and any later tie of equal
  last-surfaced dates — the tie is broken by **reframe id ascending**,
  making the pick fully deterministic.
* The chosen entry's last-surfaced date is updated to today's day key and
  recorded as today's pick; the next logical day then rotates to the next
  least-recently-surfaced entry.

Surfacing an empty pool (no reframes yet) is a clean "nothing to surface,"
never an error.

**The surface-state projection.** The rotation bookkeeping lives in a
single, verb-owned projection file — `~/.lucid/reframes/surface_state.json`
— **separate** from the append-only entry stream:

```json
{
  "schema": 1,
  "last_surfaced": {
    "reframe_2026_08_20_001": "2026-08-22",
    "reframe_2026_08_20_002": "2026-08-23"
  },
  "today": { "day": "2026-08-23", "id": "reframe_2026_08_20_002" }
}
```

* It holds the **per-id last-surfaced logical-date** and the **current
  day's pick** (the day key plus the chosen reframe id) — and nothing that
  grades the user (§0).
* It is **rebuildable and disposable.** It is a projection, not testimony:
  the entry JSONL is the source of truth, and deleting `surface_state.json`
  only resets rotation memory — the next `surface` re-cold-starts and no
  reframe is ever lost. Because rotation memory is disposable, it lives in
  a projection, not on the frozen entries.
* It is written and read **only** through `internal/storage` (architecture
  P3), exactly like every other file under `~/.lucid/`. Keeping it out of
  the entry stream is what lets the entry history stay pure append-only
  while `surface` still tracks what it has shown — the design reason
  reframes are a new record family rather than an observation kind (§1).

This same surface primitive is shaped so a future rotating inner-work verb
can reuse it; this document defines only `reframe`.

## 5. Reading the record

* **`lucid reframe list`** reads the stored reframes (corrections folded,
  superseded entries omitted) and prints them human-first. `--json` emits
  the structured list — the machine-readable shape, consistent with the
  other read verbs' JSON, so automation never scrapes prose
  ([ADR-0007](adr/0007-cli-conventions.md), [`observations.md`](observations.md)
  §7). `--json` is the persistent root flag every command carries.
* **`lucid reframe surface`** prints the one reframe for the day (§4);
  `--json` emits that single pick as a structured object.

Both reads are pure — `list` writes nothing, and `surface` touches only the
surface-state projection (never an entry).

## 6. Boundaries

* **Sanctuary and inventory, not obligation** (§0). No streak, no score, no
  "you didn't reframe today." The surface reminds; it never grades.
* **`internal/storage` is the sole `~/.lucid/` writer** (architecture P3),
  including `surface_state.json`. The reframe record family and rotation
  are agent-free and carry no LLM in any path (P9).
* **Append-only** (§2). History is never rewritten; a correction is a new
  entry via `refs.corrects`. The store is built to outlive its tools (P6):
  plain user-owned JSONL, exportable as a directory of files.
* **Public-safe** ([`CLAUDE.md`](../CLAUDE.md) invariants). Every example
  in this document and in the tests is synthetic; real reframes live only
  in the private Ledger under `~/.lucid/`, never in the repo.

## 7. Defaults

Logical-day rollover 04:00 (shared with observations and the Engine) ·
`--day` strict tier, future dates rejected, real write time kept as
`recorded_at` · rotation = least-recently-surfaced, unsurfaced-first,
ties broken by reframe id ascending · within-day `surface` idempotent ·
`schema` = 1 · `source` default `reframe` (`migration` for a bulk import).
All timestamps are ISO-8601 with the host's local-TZ offset, the same rule
as every record under `~/.lucid/` ([`mvp/data-model.md`](mvp/data-model.md)
§"Time zone rule").
