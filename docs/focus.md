# Lucid — Focus Layer (rotating focus reminders)

**Date:** 2026-08-23 · **Status:** Canonical — a living concept, evolving with the project
**Scope:** The daily-intention half of the Mirror's practice surface: the slow
**focus** work-ons a person keeps — a thing to work on, with an optional
success criterion for what "did it" looks like. This document defines the
focus record family, its append-only store under `~/.lucid/focus/`, the
active-vs-retired state carried by an append-only retirement event, the
verb-owned surface-state projection that rotates one focus per day, and the
`lucid focus` CLI surface. It contains no instance data; every example here
is synthetic.

## 0. The governing corollary

**Focus items are practice, never a scorecard.** This layer carries the same
sanctuary stance the observation layer states for the body
([`observations.md`](observations.md) §0) and the reframe layer states for
self-talk ([`reframes.md`](reframes.md) §0): a focus item is a work-on you
chose to keep, not a task the system grades. Surfacing a focus for the day is
a gentle reminder of your own intention — it never asks whether you did it,
never counts a streak of "days you focused," never scores follow-through. The
`success_criterion` is *your* description of what doing-it feels like, kept
verbatim beside the work-on; it is a note to yourself, not a metric the system
checks. The rotation bookkeeping in §4 tracks only *which focus was shown when*
so the daily surface stays fair; it is not adherence. If a practice ever
deserves real accountability, it becomes an Engine commitment through a Gate —
a separate, deliberate act. Focus items stay inventory of your own intentions,
and intentions are sanctuary.

## 1. Position in the foundation

Lucid's foundation is three parts ([`architecture.md`](architecture.md)),
and this layer adds instances of two without changing any of them:

| Part | Holds | This layer adds |
|------|-------|-----------------|
| **Ledger** (append-only) | What happened / what was said | A new record family: `focus` entries (including retirement events) |
| **Projections** (rebuildable views) | What it means | The surface-state rotation projection |

Focus items are a **new record family, not an observation kind** — the same
reasoning that made reframes their own family ([`reframes.md`](reframes.md) §1).
The observation envelope is frozen and observations are inventory that carries
**no** last-surfaced, streak, selection, or lifecycle state
([`observations.md`](observations.md) §0, architecture P3). A rotating
`surface` needs "which one did we show today" state, and an active-vs-retired
lifecycle needs "is this one still in the pool" state, so wiring either onto an
observation would break the sanctuary rule. Focus items therefore get their own
append-only entry stream and a **separate** projection that holds the rotation
state — the entry history stays pure, the retirement lifecycle is itself an
append (§5), and the mutable rotation bookkeeping lives outside the entries
(§4). Like every record family, focus is added only as a new kind, a new store,
and a new projection — never by changing the frozen observation envelope.

`internal/storage` is the **only** package that touches `~/.lucid/`,
including the surface-state projection (architecture P3); the focus record
family, its state fold, and its rotation logic are deterministic, agent-free,
and have **no LLM in any path** (architecture P9) — the daily surface completes
with no model.

## 2. The entry schema

Every focus entry is one JSON object, appended verbatim as one line to a
per-logical-day file (`~/.lucid/focus/YYYY/MM/focus_YYYY_MM_DD.jsonl`):

```json
{
  "id": "focus_2026_08_23_001",
  "schema": 1,
  "text": "Pause before reacting",
  "success_criterion": "I noticed the urge and let it pass",
  "state": "active",
  "recorded_at": "2026-08-23T21:45:10-04:00",
  "logical_date": "2026-08-23",
  "source": "focus",
  "tags": ["intention"],
  "refs": {}
}
```

Fields, binding:

| Field | Required | Meaning |
|-------|----------|---------|
| `id` | assigned | `focus_<logical_date>_<seq>` — assigned by the storage adapter under the single-writer discipline (§ Ids). |
| `schema` | yes | The entry schema version. `Schema = 1` today. |
| `text` | yes | The work-on — the thing you want to focus on. Free text, verbatim. An empty `text` is a usage error and nothing is written. |
| `success_criterion` | no | Your own description of what "did it" looks like, kept verbatim beside the work-on. Optional — an item may have none. It is a note to yourself (§0), never a metric. Always **emitted** in JSON (as `""` when unset) so the on-disk and machine shapes stay stable. |
| `state` | yes | `active` or `retired`. Assigned `active` on `add`; moved to `retired` by a later retirement event (§5). It is the **folded** state a reader computes, never edited in place. |
| `recorded_at` | yes | When the entry was written — always "now," an ISO-8601 local-TZ timestamp. |
| `logical_date` | yes | The day the focus is attributed to (`YYYY-MM-DD`), the universal join key. Defaults to today's logical day; `--day` backdates it (§3). |
| `source` | yes | Provenance: `focus` for a normal `lucid focus add`, `migration` for a one-time bulk import. Distinguishes machine-relayed from hand-typed, on the same footing as an observation's `source`. |
| `tags` | no | Optional `#tags` copied into `tags[]` — obs-parity, for future theme filtering. No current surface requires them; an entry with none marshals as `[]`. |
| `refs` | no | A flat object of reserved relational keys. `retires` names the focus id a retirement event moves to `retired` (§5). An entry with none marshals as `{}`. |

**Versioning.** `schema` is versioned per record family, exactly as the
observation envelope is ([`observations.md`](observations.md) §2) and the
reframe family is ([`reframes.md`](reframes.md) §2). New needs go in `tags`,
`refs`, or a new optional field under a bumped `schema` — readers tolerate an
unknown field and a higher schema version (read what you understand, skip what
you don't). The focus schema is documented and versioned here alongside the
other Ledger record schemas.

**Ids.** `focus_<logical_date>_<seq>` (concretely `focus_YYYY_MM_DD_<seq>`):
the logical date in underscores, `seq` = the max seq parsed from well-formed
lines in the target day file, plus one — computed under the storage adapter's
single-writer discipline, never from line count. Zero-padded to three digits,
parsed numerically, wider values legal (`_1000`). Every appended line — a fresh
focus **or a retirement event** — consumes a seq. A backdated focus's id encodes
the *logical* date, not the recording time (`focus_2026_08_01_001` written on
August 23rd).

**Append-only, retired by reference.** No line is ever rewritten. Retiring a
focus is a *new* entry whose `refs.retires` names the target id; readers fold
the retirement onto its target at read time, and a retired focus drops out of
`surface` selection and out of the default `list` (§5). The original line stays
byte-identical forever — a focus you retired is history you can still read via
`list --all`. Editing a focus in place, or deleting one, is never an operation;
you retire the old work-on and add a fresh one, and the old one steps aside
while its full record remains.

**Byte-stability.** `encoding/json` sorts map keys and preserves struct field
order, so the same entry always marshals to the same bytes — the property the
single-writer append discipline relies on. `success_criterion` is always
emitted (as `""` when unset), and an entry with no tags or refs marshals as
`[]` / `{}`, a stable on-disk shape across every entry.

## 3. The capture grammar

Focus items are captured by a one-line, deterministic, agent-free verb — the
same design stance as the observation micro-log, the reframe verb, and the
Engine ([`observations.md`](observations.md) §4, [`reframes.md`](reframes.md)
§3, architecture P9): sub-second, offline-capable, no LLM.

```
lucid focus add "Pause before reacting"
lucid focus add "Pause before reacting" --success "I noticed the urge and let it pass"
lucid focus add "Ask one clarifying question before starting" --day @yesterday
```

Grammar, binding:

* **One positional argument:** `text`, required, stored verbatim. An empty
  `text` is a clean usage error — nothing is written.
* **`--success <criterion>`.** Optional. Sets `success_criterion` verbatim.
  Omit it and the field is stored empty — the criterion is never synthesized.
* **`--day` backdating.** `--day` sets `logical_date` for the entry, reading the
  one shared date grammar every logical-day verb uses
  ([`usage/commands.md`](usage/commands.md#backdating-with---day)):
  `@yesterday` (the logical day before this one, 04:00-rollover aware),
  `@YYYY-MM-DD` (a civil day, taken literally), and the partial/`<day> <time>`
  forms. It is the **strict** tier — a token the grammar cannot read, or a day
  in the future, is a clean error naming the accepted forms, and **nothing is
  written.** With no `--day`, the entry files under today's logical day (the
  04:00 rollover applied to now). `recorded_at` is always the real write time
  regardless of backdating.
* **`#tags`.** A `#tag` token is copied into `tags[]` for future theme
  filtering; obs-parity and optional.

Capture is total (P1): one line, no form, no follow-up. `lucid focus add`
acknowledges only *after* the write lands and prints the receipt id
(§ Receipts), the same provenance-over-magic ack the observation micro-log and
the reframe verb give.

## 4. The surface — one focus per day

`lucid focus surface` returns **exactly one active** focus for the current
logical day and records that it was shown. It is the deterministic, agent-free
daily rotation the morning surface reads.

**The logical day.** The day key is the current logical day —
`DateString(LogicalBaseDate(now, DefaultRolloverMin))`, the 04:00 rollover the
observation, reframe, and day surfaces already use, so "today" means the same
boundary everywhere in the Ledger.

**Within-day idempotence.** If the surface-state already records a pick for
today's day key, `surface` returns that same focus **unchanged** and does
**not** advance. Repeated calls within the same logical day are idempotent —
the morning surface can call it more than once and always shows the same focus
for the day.

**Least-recently-surfaced rotation over active items.** On the first `surface`
of a new logical day, selection walks the **active** entries (retired items are
filtered out of the pool *before* selection) and picks the one surfaced **least
recently**, so the pool rotates fairly:

* An entry **never surfaced** sorts before any entry that has been surfaced —
  so a freshly added focus enters the rotation, and a cold-start Ledger
  (nothing surfaced yet) is well-defined.
* Among entries with the same last-surfaced state — including the cold-start
  case where all are unsurfaced, and any later tie of equal last-surfaced dates
  — the tie is broken by **focus id ascending**, making the pick fully
  deterministic.
* The chosen entry's last-surfaced date is updated to today's day key and
  recorded as today's pick; the next logical day then rotates to the next
  least-recently-surfaced active entry.

Surfacing an empty pool — no focus items yet, or every item retired — is a
clean "nothing to surface," never an error.

**The surface-state projection.** The rotation bookkeeping lives in a single,
verb-owned projection file — `~/.lucid/focus/surface_state.json` — **separate**
from the append-only entry stream:

```json
{
  "schema": 1,
  "last_surfaced": {
    "focus_2026_08_20_001": "2026-08-22",
    "focus_2026_08_20_002": "2026-08-23"
  },
  "today": { "day": "2026-08-23", "id": "focus_2026_08_20_002" }
}
```

* It holds the **per-id last-surfaced logical-date** and the **current day's
  pick** (the day key plus the chosen focus id) — and nothing that grades the
  user (§0).
* It is **rebuildable and disposable.** It is a projection, not testimony: the
  entry JSONL is the source of truth, and deleting `surface_state.json` only
  resets rotation memory — the next `surface` re-cold-starts and no focus is
  ever lost. Because rotation memory is disposable, it lives in a projection,
  not on the frozen entries.
* It is written and read **only** through `internal/storage` (architecture P3),
  exactly like every other file under `~/.lucid/`. Keeping it out of the entry
  stream is what lets the entry history stay pure append-only while `surface`
  still tracks what it has shown — the design reason focus is a new record
  family rather than an observation kind (§1). A last-surfaced entry for an id
  that later retires simply never gets picked again; the stale key is harmless.

This surface primitive is the same one `reframe surface` uses
([`reframes.md`](reframes.md) §4), reused here filtered to active items; this
document defines only `focus`.

## 5. Retire — active vs. retired

A focus item has a lifecycle of exactly two states, `active` and `retired`, and
it moves between them **only by appending** — never by an in-place edit or a
delete. This is what the weekly focus pass needs: it rarely deletes a work-on,
it **retires** one in favor of a fresher one (a KEEP/SWAP/ADD ritual), and the
audit trail of what you used to work on is the point.

```
lucid focus retire focus_2026_08_20_001
```

**Retire is an append.** `lucid focus retire <id>` appends one retirement event
— a focus entry whose `refs.retires` names the target id — modeled on the
anchor `sunset` pattern ([`usage/commands.md`](usage/commands.md#anchor)). It
writes nothing on the target line; the target stays byte-identical.

**State is a fold, never a mutation.** A reader folds the entry stream: an item
is `retired` when a later retirement event references its id, and `active`
otherwise. Nothing is dropped in the fold — every real work-on is returned with
its state resolved, and the retirement events themselves are bookkeeping, not
separate list items. `state` on disk is the value at write time; the folded
state is authoritative on read.

**What changes when an item retires:**

* It leaves the `surface` rotation pool (§4) — a retired focus is never picked.
* It leaves the **default** `list` — `lucid focus list` shows active items only.
* It **stays** in `lucid focus list --all` (the audit view), with `state:
  "retired"`, so a retired work-on is never invisible — it just stops
  surfacing.

**Errors.** Retiring an unknown id, or an id that is already retired, is a clean
error (prints a fixed reason, exits non-zero) and appends nothing — a retired
item is history, not an editable record. To bring a work-on back, `add` it again
as a fresh active item; the retired original stays in the audit view. This
active-vs-retired lifecycle is what lets a SWAP be a retire-old + add-new pair,
never a hard delete.

## 6. Reading the record

* **`lucid focus list`** reads the stored focus items (retirement events folded,
  retired entries omitted) and prints the **active** items human-first. `--all`
  (alias `--include-retired`) includes retired items, each marked with its
  state — the audit view. `--json` emits the structured list, exposing each
  item's `id` (so the weekly pass can `retire <id>` deterministically) — the
  machine-readable shape, consistent with the other read verbs' JSON, so
  automation never scrapes prose ([ADR-0007](adr/0007-cli-conventions.md),
  [`observations.md`](observations.md) §7). `--json` is the persistent root flag
  every command carries.
* **`lucid focus surface`** prints the one active focus for the day (§4);
  `--json` emits that single pick as a structured object.

Both reads are pure — `list` writes nothing, and `surface` touches only the
surface-state projection (never an entry).

## 7. Boundaries

* **Sanctuary and inventory, not obligation** (§0). No streak, no score, no
  "you didn't do today's focus." The surface reminds; it never grades. The
  `success_criterion` is your own note, not a checkbox the system reads.
* **`internal/storage` is the sole `~/.lucid/` writer** (architecture P3),
  including `surface_state.json`. The focus record family, its state fold, and
  its rotation are agent-free and carry no LLM in any path (P9).
* **Append-only** (§2, §5). History is never rewritten; retirement is a new
  entry via `refs.retires`, and there is no delete. The store is built to
  outlive its tools (P6): plain user-owned JSONL, exportable as a directory of
  files.
* **Public-safe** ([`CLAUDE.md`](../CLAUDE.md) invariants). Every example in
  this document and in the tests is synthetic; real focus items live only in the
  private Ledger under `~/.lucid/`, never in the repo.

## 8. Defaults

Logical-day rollover 04:00 (shared with observations, reframes, and the Engine)
· `--day` strict tier, future dates rejected, real write time kept as
`recorded_at` · `--success` optional, never synthesized, always emitted as `""`
when unset · new items land `active`; retire is an append-only event, no delete
· `list` active-only by default, `--all` for the audit view · rotation =
least-recently-surfaced over **active** items, unsurfaced-first, ties broken by
focus id ascending · within-day `surface` idempotent · `schema` = 1 · `source`
default `focus` (`migration` for a bulk import). All timestamps are ISO-8601
with the host's local-TZ offset, the same rule as every record under
`~/.lucid/` ([`mvp/data-model.md`](mvp/data-model.md) §"Time zone rule").
