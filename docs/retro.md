# Lucid — Retro Parking Lot (the R-NNN queue)

**Date:** 2026-08-27 · **Status:** Canonical — a living concept, evolving with the project
**Scope:** The **parking lot** for the weekly Retro and the Gate: the single
auditable home for anything parked to revisit later — "park this for Sunday." This
document defines the retro record family, its append-only store under
`~/.lucid/retro/`, the open/resolved/deferred lifecycle carried by append-only
transition events, the split between a parked item's stable **`R-NNN`** identity
and a per-write internal receipt, and the `lucid retro` CLI surface
(`park` / `list` / `show` / `resolve` / `defer`, plus a hidden one-time
`import`). It contains no instance data; every example here is synthetic.

## 0. The governing corollary

**The parking lot is an audit trail, and the receipt is the whole of it.** Where
the focus and reframe layers state a *sanctuary* stance for practice
([`focus.md`](focus.md) §0, [`reframes.md`](reframes.md) §0), the retro parking
lot states an *audit* stance for a queue: the reason it exists is that nothing
parked may ever be silently lost. Two contracts carry that, and the tool — not
discipline — enforces both:

* **The echo is the signal.** `park` returns a real `R-NNN` id, or it fails
  loudly and writes nothing. There is no third outcome. A relaying agent echoes
  that id back the moment it parks (`"parked as R-012"`), and the absence of an
  id is therefore proof the item did **not** land — an audit signal that is now
  structural, not a habit someone can forget.
* **Never delete.** A parked item is never removed. It is resolved (moved to the
  audit trail with a record of *what we did*) or deferred (kept visible with a
  one-line reason), each an **appended** transition — never an in-place edit and
  never a delete. Resolved is not "gone"; it is history you can still read.

Everything below is these two contracts made mechanical. The parking lot does not
grade the queue, does not score how fast things resolve, and does not nudge the
count down — it only guarantees that what was parked stays findable and that
every write left a receipt.

## 1. Position in the foundation

Lucid's foundation is three parts ([`architecture.md`](architecture.md)), and
this layer adds instances of two without changing any of them:

| Part | Holds | This layer adds |
|------|-------|-----------------|
| **Ledger** (append-only) | What happened / what was said | A new record family: `retro` events (a `park`, plus `resolve` / `defer` transitions) |
| **Projections** (rebuildable views) | What it means | The folded parked-item view (open/resolved/deferred), computed at read time |

The retro parking lot is a **new record family, not an observation kind** — the
same net-new-family decision reframes and focus made
([`reframes.md`](reframes.md) §1, [`focus.md`](focus.md) §1). A parked item
carries an open→resolved→deferred lifecycle and a stable `R-NNN` identity, and
wiring either onto the frozen observation envelope would break the sanctuary rule
([`observations.md`](observations.md) §0, architecture P3). So retro gets its own
append-only entry stream, modeled directly on `focus`: the lifecycle is itself an
append (§5), and a reader **folds** the events into current items at read time
(§4) — the entry history stays pure and no line is ever rewritten.

`internal/storage` is the **only** package that touches `~/.lucid/`
(architecture P3); the retro record family, its id minters, and the state fold
are deterministic, agent-free, and have **no LLM in any path** (architecture P9)
— `park`, `resolve`, `defer`, `list`, and `show` all complete with no model.

**Two identities, kept distinct.** Unlike `focus` — where one per-day id serves
as both the item id and the write receipt — retro deliberately **splits** them,
because the `R-NNN` is a durable, human-quoted identity that a lifecycle event
must be able to *reference* without *consuming*:

* The **parked-item id** is `R-NNN` (`R-012`) — one per parked item, minted
  **globally monotonic** across the whole store, stable for the item's whole
  life, shown by `list`, and the only thing `show` / `resolve` / `defer` accept.
  This is the identity the echo contract (§0) rests on.
* The **receipt id** is `retro_event_<logical_date>_<seq>`
  (`retro_event_2026_08_23_001`) — one per **appended event**, minted per
  logical-day. `park`, `resolve`, and `defer` each append one event and return
  *that event's* receipt, so two writes touching the same `R-NNN` return two
  different receipts.

A `resolve` or `defer` therefore names the `R-NNN` it acts on, returns that
`R-NNN` **alongside** its own fresh receipt, and does **not** advance the
parked-item sequence — so a transition can never open a gap in the `R-NNN`
numbering or be mistaken for a new parked item.

## 2. The entry schema

Every retro event is one JSON object, appended verbatim as one line to a
per-logical-day file
(`~/.lucid/retro/YYYY/MM/retro_YYYY_MM_DD.jsonl`):

```json
{
  "event_id": "retro_event_2026_08_23_001",
  "schema": 1,
  "event_type": "park",
  "id": "R-001",
  "item": "Revisit whether the Sunday walk should open with the deferred items",
  "status": "open",
  "source": "chat",
  "resolution": "",
  "resolved_date": "",
  "defer_reason": "",
  "recorded_at": "2026-08-23T21:45:10-04:00",
  "logical_date": "2026-08-23",
  "tags": ["process"],
  "refs": {}
}
```

Fields, binding:

| Field | Required | Meaning |
|-------|----------|---------|
| `event_id` | assigned | The **write receipt**: `retro_event_<logical_date>_<seq>`, assigned per logical-day by the storage adapter under the single-writer discipline (§ Ids). Unique per appended event; every mutating verb returns it. |
| `schema` | yes | The entry schema version. `Schema = 1` today. |
| `event_type` | yes | `park`, `resolve`, or `defer` — which lifecycle event this line is. A `park` creates an item; a `resolve` / `defer` transitions an existing one (§5). |
| `id` | assigned / supplied | The **parked-item id**: `R-NNN`. On a `park` it is the minted (or, for `import`, the supplied) new id; on a `resolve` / `defer` it names the **affected** item and mints nothing (§1). |
| `item` | on `park` | The parked thing, stored **verbatim** — the full text, kept exactly. Set on the `park` event; empty on transitions (which carry no new item text). An empty `item` on a `park` is a usage error and nothing is written. |
| `status` | yes | `open`, `resolved`, or `deferred` — the status this event sets: `park` → `open`, `resolve` → `resolved`, `defer` → `deferred`. It is the value at write time; the **folded** status a reader computes (§4) is authoritative. |
| `source` | yes | Provenance of the park, stored **verbatim**: `retro` for a normal `lucid retro park`, a raw entry id (`raw_2026_07_10_22_55`) or `chat` when `--source` names where it came from, `migration` for the one-time import. Carried on the `park`; a transition records the actor that resolved/deferred (`retro`). |
| `resolution` | on `resolve` | The record of *what we did*, stored verbatim. Set on a `resolve` event; always **emitted** (as `""` when unset) so the on-disk shape stays stable. |
| `resolved_date` | on `resolve` | The logical day the item was resolved (`YYYY-MM-DD`) — the `resolve` event's `logical_date`, surfaced by role. Emitted `""` on non-resolve events. |
| `defer_reason` | on `defer` | The one-line reason an item was consciously deferred, stored verbatim. Set on a `defer` event; emitted `""` when unset. |
| `recorded_at` | yes | When the line was written — always "now," an ISO-8601 local-TZ timestamp, regardless of any backdating. |
| `logical_date` | yes | The day the event is attributed to (`YYYY-MM-DD`), the universal join key. On a `park` this is the **parked date**; on a `resolve` it is the **resolved date**. Defaults to today's logical day; `--day` backdates it on `park` (§3). |
| `tags` | no | Optional `#tags` copied into `tags[]` for future theme filtering — obs-parity, optional; an event with none marshals as `[]`. |
| `refs` | no | A flat object of reserved relational keys; an event with none marshals as `{}`. |

**The folded item.** `list` and `show` do not return raw events — they return the
**folded item**, grouped by `id`: the `park` supplies `id`, `item`, `parked-date`
(its `logical_date`), and `source`; a later `resolve` folds in `status: resolved`
+ `resolution` + `resolved-date`; a later `defer` folds in `status: deferred` +
`defer-reason`. The folded shape carries exactly `id`, `parked-date`, `source`,
`item`, `status`, `resolution`, `resolved-date`, and (optional) `defer-reason` —
the audit-facing view of one parked item, with every field always present (empty
when unset) so the machine shape stays stable.

**Ids — two, kept distinct (§1).** The two shapes never collide: the item id is a
short dashed counter (`R-012`), the receipt is date-and-sequence
(`retro_event_2026_08_23_001`).

* The **`R-NNN` item id** is **global-monotonic**: `seq` = the maximum `R-NNN`
  parsed from every well-formed `park` line across the whole store, plus one,
  formatted `R-%03d` (zero-padded to three digits, parsed numerically, wider
  values legal — `R-1000`). Only `park` events feed the max; `resolve` / `defer`
  events reference an existing id and are ignored by the minter, so a transition
  never advances the counter. `park` auto-mints the next id; `import` **supplies**
  explicit ids, so after a bulk import the next `park` continues at
  imported-max + 1 with no separate seed step.
* The **`retro_event_` receipt** is per logical-day: `seq` = the max event seq
  parsed from that day's file, plus one — computed single-writer, never from line
  count. Every appended line consumes a receipt seq; a backdated park's receipt
  encodes the *logical* date, not the recording time.

**Versioning.** `schema` is versioned per record family, exactly as the
observation envelope and the other inner-work families are
([`observations.md`](observations.md) §2, [`focus.md`](focus.md) §2). New needs go
in `tags`, `refs`, a new optional field, or a new `event_type` under a bumped
`schema` — readers tolerate an unknown field and a higher schema version (read
what you understand, skip what you don't).

**Append-only, transitioned by event.** No line is ever rewritten. Resolving or
deferring an item is a **new** event whose `id` names the target; readers fold it
onto the item at read time. The original `park` line stays byte-identical forever
— a resolved item is history you can still read via `list --all` / `list
--resolved`. Editing an item in place, or deleting one, is never an operation.

**Byte-stability.** `encoding/json` sorts map keys and preserves struct field
order, so the same event always marshals to the same bytes — the property the
single-writer append discipline relies on. `resolution`, `resolved_date`, and
`defer_reason` are always emitted (as `""` when unset), and an event with no tags
or refs marshals as `[]` / `{}`, a stable on-disk shape across every event.

## 3. The capture grammar — `park`

Items are parked by a one-line, deterministic, agent-free verb — the same design
stance as the observation micro-log, the reframe verb, and focus
([`observations.md`](observations.md) §4, [`reframes.md`](reframes.md) §3,
[`focus.md`](focus.md) §3, architecture P9): sub-second, offline-capable, no LLM.

```
lucid retro park "Revisit whether the Sunday walk should open with the deferred items"
lucid retro park "Try a shorter Gate cadence next quarter" --source chat
lucid retro park "Experiment with a two-column layout for the weekly notes" --day @yesterday
```

Grammar, binding:

* **One positional argument:** the `item`, required, stored **verbatim** — the
  full text is kept exactly, so a multi-line parked note loses nothing. An empty
  `item` is a clean usage error and nothing is written.
* **`--source <value>`.** Optional. Sets `source` verbatim — where the item came
  from (`chat`, a `raw_…` entry id, a person's name-free handle). Omit it and the
  source is `retro`. It is never synthesized.
* **`--day` backdating.** `--day` sets `logical_date` (the item's **parked
  date**) for the event, reading the one shared date grammar every logical-day
  verb uses
  ([`usage/commands.md`](usage/commands.md#backdating-with---day)): `@yesterday`
  (the logical day before this one, 04:00-rollover aware), `@YYYY-MM-DD` (a civil
  day, taken literally), and the partial / `<day> <time>` forms. It is the
  **strict** tier — a token the grammar cannot read, or a day in the future, is a
  clean error naming the accepted forms, and **nothing is written.** With no
  `--day`, the item files under today's logical day. `recorded_at` is always the
  real write time regardless of backdating. This flag exists specifically so a
  migration can reproduce each item's original parked date (§6).
* **`--json`.** Emit the write result — the minted `R-NNN`, its `event_id`
  receipt, and the folded item — as a structured object instead of prose, the
  persistent root flag every command carries.
* **`#tags`.** A `#tag` token is copied into `tags[]` for future theme filtering;
  obs-parity and optional.

Capture is total (P1): one line, no form, no follow-up. `lucid retro park`
acknowledges only *after* the write lands and prints **both** identities — the
minted `R-NNN` and the `event_id` receipt (`parked as R-012 (receipt
retro_event_2026_08_23_001)`) — the provenance-over-magic ack that makes the echo
contract (§0) structural: the id in the ack is the id that landed.

## 4. Reading the record — `list` and `show`

**`lucid retro list`** reads the stored events, folds them into current items, and
prints them **human-first**, ordered by **ascending `R-NNN`** so the Sunday
top-to-bottom walk is stable. Its **default view shows both `open` and
`deferred`** items together — a deferred item is a conscious *not-now*, not a
disappearance, so it must stay in the walk with its reason visible. Two filters
change the view:

```
lucid retro list                 # default: open + deferred, ascending R-NNN
lucid retro list --all           # every item — open, deferred, and resolved
lucid retro list --resolved      # only the resolved audit trail
lucid retro list --json          # the structured list
```

* **default** — `open` ∪ `deferred`, ascending `R-NNN`. The Sunday-walk view.
* **`--all`** — adds the resolved items after the open/deferred section, each
  marked with its `status`, so a resolved item is never invisible — it just stops
  showing in the default walk. This is the full audit view.
* **`--resolved`** — the resolved items alone, the "what we did" trail.
* **`--json`** — emits the structured list, exposing each item's `id` (so an
  agent can `resolve`/`defer <id>` deterministically) and every folded field —
  the machine-readable shape, consistent with the other read verbs' JSON so
  automation never scrapes prose ([ADR-0007](adr/0007-cli-conventions.md),
  [`observations.md`](observations.md) §7).

**`lucid retro show R-NNN`** displays a **single** item with **all** its folded
fields — `id`, `parked-date`, `source`, `item` (verbatim), `status`,
`resolution`, `resolved-date`, and `defer-reason` — so the full record of one
parked item is one command away. `--json` emits it as a structured object.
Showing an unknown id is a clean error.

Both reads are pure — they write nothing and fold the items fresh from the
append-only stream each time.

## 5. Resolve and defer — the lifecycle transitions

A parked item has three states — `open`, `resolved`, `deferred` — and it moves
between them **only by appending** a transition event, never by an in-place edit
or a delete. This is the mechanical form of the never-delete contract (§0): the
audit trail of what was parked, and what became of it, is the point.

```
lucid retro resolve R-001 "Adopted it — the Sunday walk now opens with deferred items"
lucid retro defer   R-014 "Someday — revisit after the quarter closes"
```

**`resolve` is an append that never deletes.** `lucid retro resolve <R-NNN>
<resolution>` appends one `resolve` event whose `id` names the target and which
carries the `resolution` text and a `resolved-date` (the event's `logical_date`).
It writes **nothing** on the original `park` line — that line stays
byte-identical. The item folds to `status: resolved`, leaves the default `list`,
and appears under `list --resolved` / `list --all` as the audit trail. Both
arguments are required; an empty resolution is a usage error and nothing is
written.

**`defer` sets the first-class `deferred` status.** `lucid retro defer <R-NNN>
<reason>` appends one `defer` event whose `id` names the target and which carries
the `defer-reason`. The item folds to `status: deferred` — a **distinct** status,
not "open with a note" — **and stays visible in the default `list`** so the
Sunday walk never loses it. This is the mid-life defer a Retro actually does:
consciously setting something aside *with a reason*, while keeping it in view.

**Both transitions reference, never consume (§1).** A `resolve` / `defer`:

* names the affected `R-NNN` and mints **no** new item id — the parked-item
  counter does not advance, so the next `park` is unaffected;
* appends one event, minting its **own** fresh `retro_event_` receipt;
* acknowledges the affected `R-NNN` **alongside** that receipt (`resolved R-001
  (receipt retro_event_2026_08_30_004)`), so the ack always names both the item
  audited and the write that recorded it.

**State is a fold, never a mutation.** A reader folds the stream: an item is
`resolved` when a later `resolve` references its id, `deferred` when a later
`defer` does, and `open` otherwise; the most recent transition wins, so a deferred
item can later be resolved. Nothing is dropped in the fold — every parked item is
returned with its state resolved, and the transition events are bookkeeping, not
separate list rows.

**Errors.** Resolving or deferring an unknown id is a clean error (prints a fixed
reason, exits non-zero) and appends nothing. Resolve and defer never delete, so
there is no "undo by delete" — to reopen a resolved item you `park` it afresh (a
new `R-NNN`), and the resolved original stays in the audit trail.

## 6. The migration path — the hidden `import`

The one-time cutover from a hand-maintained queue into this Ledger is a
**distinct, hidden verb**, not one of the five everyday subcommands — modeled on
the hidden `pet migrate-self`, it carries `Hidden: true` and never appears in
`lucid retro --help`. It exists to reproduce an existing queue **exactly**, ids
and dates and all, which the everyday `park` cannot do (it mints a fresh id and
files under today):

```
lucid retro import --file items.json
```

* **It reads a JSON array of items**, each carrying an explicit `R-NNN` id, its
  original parked date, its verbatim source, the verbatim item text, its final
  status, and — where they apply — a `defer-reason` or a `resolution` +
  `resolved-date`.
* **For each item it appends a `park` event** with the **supplied** id, the
  backdated parked date, and the verbatim source and text — then, if the item's
  final status is `resolved` or `deferred`, **appends the matching transition**
  (`resolve` with the original resolution + resolved-date, or `defer` with the
  original reason) so the folded status and reason reproduce the source exactly.
* **Every generated event gets its own fresh `retro_event_` receipt**, and only
  the **supplied `R-NNN` ids** seed the global minter — so after an import of
  `R-001..R-028` the next everyday `park` is `R-029`, regardless of how many
  transition events the import wrote.
* **`import` is not idempotent.** Re-running it appends the items a *second* time
  and duplicates ids. A migration is therefore a single pass; if it fails partway
  and must be retried, restore the pre-migration `lucid backup` snapshot first,
  then replay — never re-run `import` on top of a partially-migrated store.

After import, ordinary `park` / `resolve` / `defer` operate on the migrated items
normally; the imported items are indistinguishable from natively-parked ones
except for their `source` provenance.

## 7. Boundaries

* **Audit, not obligation** (§0). The parking lot guarantees that what was parked
  is never lost and that every write left a receipt — it does not grade the queue,
  score resolution speed, or push the count down. The echo is a proof of landing,
  not a metric.
* **`internal/storage` is the sole `~/.lucid/` writer** (architecture P3). The
  retro record family, its `R-NNN` and receipt minters, and the state fold are
  agent-free and carry no LLM in any path (P9).
* **Append-only, never delete** (§2, §5). History is never rewritten; resolve and
  defer are new events, and there is no delete. The store is built to outlive its
  tools (P6): plain user-owned JSONL, exportable as a directory of files.
* **Two identities stay distinct** (§1). The `R-NNN` is the durable, quotable item
  id the echo contract rests on; the `retro_event_` receipt is the per-write
  proof. A transition references the item without consuming a new id, so the
  parked-item numbering never gaps.
* **Public-safe** ([`CLAUDE.md`](../CLAUDE.md) invariants). Every example in this
  document and in the tests is synthetic; real parked items live only in the
  private Ledger under `~/.lucid/`, never in the repo.

## 8. Defaults

Logical-day rollover 04:00 (shared with observations, reframes, focus, and the
Engine) · `park` mints the next global `R-NNN` and returns it plus a per-day
`retro_event_` receipt; an empty item is a usage error · `--source` optional,
verbatim, default `retro`, never synthesized · `--day` strict tier on `park`,
future dates rejected, real write time kept as `recorded_at` · `resolve` records
`resolution` + `resolved-date` and never deletes; `defer` sets the first-class
`deferred` status + `defer-reason` and stays in the default `list` · `resolve` /
`defer` reference the affected `R-NNN`, mint their own receipt, and never advance
the item counter · `list` default = open ∪ deferred, ascending `R-NNN`; `--all`
adds resolved, `--resolved` shows only resolved · `import` is the hidden one-time
migration verb, supplies explicit ids, and is **not** idempotent · `schema` = 1 ·
`source` default `retro` (`migration` for an import). All timestamps are ISO-8601
with the host's local-TZ offset, the same rule as every record under `~/.lucid/`
([`mvp/data-model.md`](mvp/data-model.md) §"Time zone rule").
