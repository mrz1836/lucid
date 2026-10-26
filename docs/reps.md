# Lucid — Practice Reps (rotating daily rep)

**Date:** 2026-10-26 · **Status:** Canonical — a living concept, evolving with the project
**Scope:** The small-step half of the Mirror's practice surface: the
**practice reps** a person keeps — one concrete thing to try today, with an
optional **solo** variant that works on a day with nobody around and an
optional **level** naming how big a step it is. (A practice rep is not an
exercise repetition count; the workout module's numbers are unrelated.) This
document defines the rep record family, its append-only store under
`~/.lucid/reps/`, the active-vs-retired state carried by an append-only
retirement event, the verb-owned surface-state projection that rotates one rep
per day, and the `lucid rep` CLI surface. It contains no instance data; every
example here is synthetic.

## 0. The governing corollary

**Practice reps are practice, never a scorecard.** This layer carries the same
sanctuary stance the observation layer states for the body
([`observations.md`](observations.md) §0), the reframe layer states for
self-talk ([`reframes.md`](reframes.md) §0), and the focus layer states for
work-ons ([`focus.md`](focus.md) §0): a rep is a step you chose to keep, not a
task the system grades. Surfacing a rep for the day is a gentle prompt toward your own chosen practice — it never asks whether you did it, never counts a streak, and never scores follow-through.
The `level` is *your* label for how big a step the rep is, kept beside it so
you can curate a mix; it is never a grade, and nothing in Lucid ranks, sums, or
advances it. The rotation bookkeeping in §4 tracks only *which rep was shown
when* so the daily surface stays fair; it is not adherence. If a practice ever
deserves real accountability, it becomes an Engine commitment through a Gate —
a separate, deliberate act. Reps stay inventory of your own practice, and
practice is sanctuary.

## 1. Position in the foundation

Lucid's foundation is three parts ([`architecture.md`](architecture.md)),
and this layer adds instances of two without changing any of them:

| Part | Holds | This layer adds |
|------|-------|-----------------|
| **Ledger** (append-only) | What happened / what was said | A new record family: `rep` entries (including retirement events) |
| **Projections** (rebuildable views) | What it means | The surface-state rotation projection |

Practice reps are a **new record family, not an observation kind** — the same
reasoning that made reframes and focus items their own families
([`reframes.md`](reframes.md) §1, [`focus.md`](focus.md) §1). The observation
envelope is frozen and observations are inventory that carries **no**
last-surfaced, streak, selection, or lifecycle state
([`observations.md`](observations.md) §0, architecture P3). A rotating
`surface` needs "which one did we show today" state, and an active-vs-retired
lifecycle needs "is this one still in the pool" state, so wiring either onto an
observation would break the sanctuary rule. Reps therefore get their own
append-only entry stream and a **separate** projection that holds the rotation
state — the entry history stays pure, the retirement lifecycle is itself an
append (§5), and the mutable rotation bookkeeping lives outside the entries
(§4). Like every record family, reps are added only as a new kind, a new store,
and a new projection — never by changing the frozen observation envelope.

`internal/storage` is the **only** package that touches `~/.lucid/`,
including the surface-state projection (architecture P3); the rep record
family, its state fold, and its rotation logic are deterministic, agent-free,
and have **no LLM in any path** (architecture P9) — the daily surface completes
with no model.

## 2. The entry schema

Every rep entry is one JSON object, appended verbatim as one line to a
per-logical-day file (`~/.lucid/reps/YYYY/MM/rep_YYYY_MM_DD.jsonl`):

```json
{
  "id": "rep_2026_08_23_001",
  "schema": 1,
  "text": "Ask one question in a meeting you would normally sit through quietly",
  "solo": "Write the question down and send it to the organizer afterward",
  "level": 2,
  "state": "active",
  "recorded_at": "2026-08-23T21:45:10-04:00",
  "logical_date": "2026-08-23",
  "source": "rep",
  "tags": [],
  "refs": {}
}
```

Fields, binding:

| Field | Required | Meaning |
|-------|----------|---------|
| `id` | assigned | `rep_<logical_date>_<seq>` — assigned by the storage adapter under the single-writer discipline (§ Ids). |
| `schema` | yes | The entry schema version. `Schema = 1` today. |
| `text` | yes | The rep — the one concrete thing to try. Free text, verbatim. An empty `text` is a usage error and nothing is written. |
| `solo` | no | The **solo variant**: a version of the same rep that works on a day with nobody around, kept verbatim beside it. Optional — a rep may have none. Always **emitted** in JSON (as `""` when unset) so the on-disk and machine shapes stay stable. |
| `level` | no | Your own label for how big a step the rep is — a positive integer with **no maximum and no meaning inside Lucid**. Lucid stores and displays it; it never ranks, sums, compares, or advances it (§0). Always **emitted**; `0` means *unlevelled* (no level given). |
| `state` | yes | `active` or `retired`. Assigned `active` on `add`; moved to `retired` by a later retirement event (§5). It is the **folded** state a reader computes, never edited in place. |
| `recorded_at` | yes | When the entry was written — always "now," an ISO-8601 local-TZ timestamp. |
| `logical_date` | yes | The day the rep is attributed to (`YYYY-MM-DD`), the universal join key. Defaults to today's logical day; `--day` backdates it (§3). |
| `source` | yes | Provenance: `rep` for a normal `lucid rep add`, `migration` for a one-time bulk import. Distinguishes machine-relayed from hand-typed, on the same footing as an observation's `source`. |
| `tags` | no | Reserved for obs-parity theme filtering. `add` stores the text verbatim — a `#word` stays part of the text — so no current capture path fills it; an entry with none marshals as `[]`. |
| `refs` | no | A flat object of reserved relational keys. `retires` names the rep id a retirement event moves to `retired` (§5). An entry with none marshals as `{}`. |

**Two variants, never three.** A rep carries exactly one primary `text` and at
most one `solo` variant. There is no third variant field (no "smaller" or
"low-energy" version): how far to scale a rep down on a given day is a
consumer's judgement, never a stored field, and a different-sized step is a
different rep with its own `level`.

**Versioning.** `schema` is versioned per record family, exactly as the
observation envelope is ([`observations.md`](observations.md) §2) and the
reframe and focus families are ([`reframes.md`](reframes.md) §2,
[`focus.md`](focus.md) §2). New needs go in `tags`, `refs`, or a new optional
field under a bumped `schema` — readers tolerate an unknown field and a higher
schema version (read what you understand, skip what you don't). The rep schema
is documented and versioned here alongside the other Ledger record schemas.

**Ids.** `rep_<logical_date>_<seq>` (concretely `rep_YYYY_MM_DD_<seq>`): the
logical date in underscores, `seq` = the max seq parsed from well-formed lines
in the target day file, plus one — computed under the storage adapter's
single-writer discipline, never from line count. Zero-padded to three digits,
parsed numerically, wider values legal (`_1000`). Every appended line — a fresh
rep **or a retirement event** — consumes a seq. A backdated rep's id encodes
the *logical* date, not the recording time (`rep_2026_08_01_001` written on
August 23rd). A malformed line in a day file is skipped on read, never fatal,
and never counted toward the next seq.

**Append-only, retired by reference.** No line is ever rewritten. Retiring a
rep is a *new* entry whose `refs.retires` names the target id; readers fold the
retirement onto its target at read time, and a retired rep drops out of
`surface` selection and out of the default `list` (§5). The original line stays
byte-identical forever — a rep you retired is history you can still read via
`list --all`. Editing a rep in place, or deleting one, is never an operation;
you retire the old rep and add a fresh one, and the old one steps aside while
its full record remains.

**Byte-stability.** `encoding/json` sorts map keys and preserves struct field
order, so the same entry always marshals to the same bytes — the property the
single-writer append discipline relies on. `solo` is always emitted (as `""`
when unset), `level` is always emitted (as `0` when unlevelled), and an entry
with no tags or refs marshals as `[]` / `{}`, a stable on-disk shape across
every entry.

## 3. The capture grammar

Reps are captured by a one-line, deterministic, agent-free verb — the same
design stance as the observation micro-log, the reframe and focus verbs, and
the Engine ([`observations.md`](observations.md) §4, [`reframes.md`](reframes.md)
§3, [`focus.md`](focus.md) §3, architecture P9): sub-second, offline-capable,
no LLM.

```
lucid rep add "Ask one question in a meeting you would normally sit through quietly"
lucid rep add "Ask one question in a meeting" --solo "Write the question down and send it afterward" --level 2
lucid rep add --body-file /tmp/rep.txt --solo-file /tmp/solo.txt --level 3
lucid rep add "Try one new route home" --day @yesterday
```

Grammar, binding:

* **One positional argument:** `text`, required, stored verbatim (unquoted
  words are joined with spaces). An empty `text` is a clean usage error —
  nothing is written.
* **`--solo <text>`.** Optional. Sets `solo` verbatim. Omit it and the field is
  stored empty — the solo variant is never synthesized.
* **`--level N`.** Optional. Sets `level` to a positive integer. Omit it and the
  rep is stored unlevelled (`0`). A given level of `0` or below, or a value that
  is not an integer, is a clean usage error — nothing is written. There is no
  maximum.
* **`--body-file <path>` / `--solo-file <path>`.** The shell-safe forms of the
  two free-text fields: each reads its field from a file, or from stdin for
  `-`, through the shared free-text reader, so punctuation, quotes, and
  newlines are stored verbatim and never interpreted by a shell. `--body-file`
  supplies `text` (in place of the positional); `--solo-file` supplies `solo`
  (in place of `--solo`). The inline and file forms of the same field are
  **mutually exclusive**, an empty file is rejected, and only **one** field may
  read stdin (`-`) in a single call — a usage error writes nothing.
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

Capture is total (P1): one line, no form, no follow-up. `lucid rep add`
acknowledges only *after* the write lands and prints the receipt id
(§ Receipts), the same provenance-over-magic ack the observation micro-log and
the reframe and focus verbs give.

**Receipts.** Every write returns a receipt id. `add` prints a prose ack
carrying the new entry's `rep_…` id, and `retire` prints one carrying the
retirement event's id; under `--json`, both emit the shared
`{receipt_id, logical_date}` receipt instead
([`usage/commands.md`](usage/commands.md#global-conventions)).

## 4. The surface — one rep per day

`lucid rep surface` returns **exactly one active** rep for the current logical
day, together with its solo variant, and records that it was shown. It is the
deterministic, agent-free daily rotation a morning surface reads.

**The logical day.** The day key is the current logical day —
`DateString(LogicalBaseDate(now, DefaultRolloverMin))`, the 04:00 rollover the
observation, reframe, focus, and day surfaces already use, so "today" means the
same boundary everywhere in the Ledger.

**Within-day idempotence.** If the surface-state already records a pick for
today's day key, `surface` returns that same rep **unchanged** and does **not**
advance. Repeated calls within the same logical day are idempotent — a morning
surface can call it more than once and always shows the same rep for the day.

**Least-recently-surfaced rotation over active reps.** On the first `surface`
of a new logical day, selection walks the **active** entries (retired reps are
filtered out of the pool *before* selection) and picks the one surfaced **least
recently**, so the pool rotates fairly:

* An entry **never surfaced** sorts before any entry that has been surfaced —
  so a freshly added rep enters the rotation, and a cold-start Ledger (nothing
  surfaced yet) is well-defined.
* Among entries with the same last-surfaced state — including the cold-start
  case where all are unsurfaced, and any later tie of equal last-surfaced dates
  — the tie is broken by **rep id ascending**, making the pick fully
  deterministic.
* The chosen entry's last-surfaced date is updated to today's day key and
  recorded as today's pick; the next logical day then rotates to the next
  least-recently-surfaced active entry.
* If the pick recorded for today has since left the pool (it was retired), the
  surface re-picks from the remaining active reps by the same rule.

So consecutive days differ whenever more than one rep is active, and no rep
repeats until every active rep has had its day. A pool of one rep surfaces that
rep every day. Surfacing an empty pool — no reps yet, or every rep retired — is
a clean "nothing to surface," never an error.

**The surface carries the solo variant.** The surface returns the rep **with
its solo variant** (when one is stored), in both the prose line and the
`--json` object, so a consumer always has a version that works with nobody
around. It does not choose between them: the surface takes no "is today a solo
day" input and applies no heuristic for it, and it never guesses how much
capacity you have. Which form to act on is yours.

**Rotation is not level-aware.** The rotation reads only ids and last-surfaced
dates; it never looks at `level`. The mix of levels you see over a cycle comes
only from curation — which reps you `add` and which you `retire` (§5) — and,
because cold-start ties break by id ascending, from the order you added them.

**The surface-state projection.** The rotation bookkeeping lives in a single,
verb-owned projection file — `~/.lucid/reps/surface_state.json` — **separate**
from the append-only entry stream:

```json
{
  "schema": 1,
  "last_surfaced": {
    "rep_2026_08_20_001": "2026-08-22",
    "rep_2026_08_20_002": "2026-08-23"
  },
  "today": { "day": "2026-08-23", "id": "rep_2026_08_20_002" }
}
```

* It holds the **per-id last-surfaced logical-date** and the **current day's
  pick** (the day key plus the chosen rep id) — and nothing that grades the
  user (§0).
* It is **rebuildable and disposable.** It is a projection, not testimony: the
  entry JSONL is the source of truth, and deleting `surface_state.json` only
  resets rotation memory — the next `surface` re-cold-starts and no rep is ever
  lost. A corrupt or missing file reads as a cold start, never an error.
  Because rotation memory is disposable, it lives in a projection, not on the
  frozen entries.
* It is written and read **only** through `internal/storage` (architecture P3),
  exactly like every other file under `~/.lucid/`. `surface` touches no entry
  file — every `rep_…jsonl` stays byte-identical across any number of
  surfaces. A last-surfaced entry for an id that later retires simply never
  gets picked again; the stale key is harmless.

This surface primitive is the same one `reframe surface` and `focus surface`
use ([`reframes.md`](reframes.md) §4, [`focus.md`](focus.md) §4), reused here
filtered to active reps; this document defines only `rep`.

## 5. Retire — active vs. retired

A rep has a lifecycle of exactly two states, `active` and `retired`, and it
moves between them **only by appending** — never by an in-place edit or a
delete. This is what a weekly curation pass needs: it rarely deletes a rep, it
**retires** one in favor of a fresher one (a KEEP/SWAP/ADD ritual), and the
audit trail of what you used to practice is the point.

```
lucid rep retire rep_2026_08_20_001
```

**Retire is an append.** `lucid rep retire <id>` appends one retirement event
— a rep entry whose `refs.retires` names the target id — modeled on the focus
retirement ([`focus.md`](focus.md) §5). It writes nothing on the target line;
the target stays byte-identical.

**State is a fold, never a mutation.** A reader folds the entry stream: a rep
is `retired` when a later retirement event references its id, and `active`
otherwise. Nothing is dropped in the fold — every real rep is returned with its
state resolved, and the retirement events themselves are bookkeeping, not
separate list items. `state` on disk is the value at write time; the folded
state is authoritative on read.

**What changes when a rep retires:**

* It leaves the `surface` rotation pool (§4) — a retired rep is never picked.
* It leaves the **default** `list` — `lucid rep list` shows active reps only.
* It **stays** in `lucid rep list --all` (the audit view), with `state:
  "retired"`, so a retired rep is never invisible — it just stops surfacing.

**Errors.** Retiring an unknown id, or an id that is already retired, is a clean
error (prints a fixed reason, exits non-zero) and appends nothing — a retired
rep is history, not an editable record. To bring a rep back, `add` it again as
a fresh active rep; the retired original stays in the audit view. This
active-vs-retired lifecycle is what lets a weekly SWAP be a retire-old +
add-new pair — say, retiring a rep you have outgrown and adding one a step
larger — never a hard delete.

## 6. Reading the record

* **`lucid rep list`** reads the stored reps (retirement events folded, retired
  entries omitted) and prints the **active** reps human-first, each with its id,
  text, level when set, and solo variant when set. `--all` (alias
  `--include-retired`) includes retired reps, each marked with its state — the
  audit view. `--json` emits the structured list,
  `{"count": N, "reps": [ … ]}` (an empty pool is `"reps": []`, never `null`),
  exposing each rep's `id` (so a weekly pass can `retire <id>`
  deterministically) — the machine-readable shape, consistent with the other
  read verbs' JSON, so automation never scrapes prose
  ([ADR-0007](adr/0007-cli-conventions.md), [`observations.md`](observations.md)
  §7). `--json` is the persistent root flag every command carries.
* **`lucid rep surface`** prints the one active rep for the day (§4) as one
  line, `<text> (level N) — Solo: <solo>`, omitting the level when the rep is
  unlevelled and the `Solo:` segment when it has no solo variant. `--json` emits
  the single pick as `{"day": "<logical day>", "rep": { … }}`, where `rep` is
  the full entry (§2) — or `null` when there is nothing to surface. The object
  carries exactly those two keys: no solo-day flag, no capacity field, and no
  done/not-done field.

Both reads are pure — `list` writes nothing, and `surface` touches only the
surface-state projection (never an entry).

## 7. Boundaries

* **Sanctuary and inventory, not obligation** (§0). No streak, no score, no
  "you didn't do today's rep," and no question about whether it was done. The
  surface prompts; it never grades. The `level` is your own label, not a rank
  the system reads or moves.
* **`internal/storage` is the sole `~/.lucid/` writer** (architecture P3),
  including `surface_state.json`. The rep record family, its state fold, and
  its rotation are agent-free and carry no LLM in any path (P9).
* **Append-only** (§2, §5). History is never rewritten; retirement is a new
  entry via `refs.retires`, and there is no delete. The store is built to
  outlive its tools (P6): plain user-owned JSONL, exportable as a directory of
  files.
* **Public-safe** ([`CLAUDE.md`](../CLAUDE.md) invariants). Every example in
  this document and in the tests is synthetic; real reps live only in the
  private Ledger under `~/.lucid/`, never in the repo.

## 8. Defaults

Logical-day rollover 04:00 (shared with observations, reframes, focus, and the
Engine) · `--day` strict tier, future dates rejected, real write time kept as
`recorded_at` · `--solo` optional, never synthesized, always emitted as `""`
when unset · `--level` optional, a positive integer with no maximum, always
emitted as `0` when unlevelled · inline and file forms of a field mutually
exclusive, one stdin reader per call · new reps land `active`; retire is an
append-only event, no delete · `list` active-only by default, `--all` for the
audit view · rotation = least-recently-surfaced over **active** reps,
unsurfaced-first, ties broken by rep id ascending, not level-aware · within-day
`surface` idempotent, solo variant always included · `schema` = 1 · `source`
default `rep` (`migration` for a bulk import). All timestamps are ISO-8601 with
the host's local-TZ offset, the same rule as every record under `~/.lucid/`
([`mvp/data-model.md`](mvp/data-model.md) §"Time zone rule").
