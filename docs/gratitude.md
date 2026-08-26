# Lucid — Gratitude Tally (accumulating count)

**Date:** 2026-08-25 · **Status:** Canonical — a living concept, evolving with the project
**Scope:** The inner-work **gratitude tally**: the running count of the things
a person keeps returning to gratitude for — "grateful for my morning coffee
×15." This document defines the gratitude record family, its append-only store
under `~/.lucid/registries/gratitude/`, the typed occurrence history the
count/first/last are folded from, and the `lucid gratitude` CLI surface
(`add` / `list` / `merge` / `import`). It contains no instance data; every
example here is synthetic.

## 0. The governing corollary

**A tally is a count you keep, never a scorecard.** This layer carries the same
sanctuary stance the observation layer states for the body
([`observations.md`](observations.md) §0): a gratitude entry is a thing you
chose to name, not a target the system grades. The tally counts *how often a
thing has come up* so a long-running practice is visible ("grateful for clean
water ×22") — it never sets a quota, never scores a streak of "days you were
grateful," and never nudges you toward a bigger number. The raw nightly
gratitude itself — the verbatim words — is captured separately and unchanged by
the ordinary `lucid log` (a `#gratitude`-tagged entry); the tally is a derived
*count over* that practice, not a replacement for it. Counting your own thanks
is inventory of what you keep noticing, and words are sanctuary.

## 1. Position in the foundation

Lucid's foundation is three parts ([`architecture.md`](architecture.md)), and
this layer adds instances of one without changing any of them:

| Part | Holds | This layer adds |
|------|-------|-----------------|
| **Ledger** (append-only) | What happened / what was said | A new registry kind: `gratitude` entries with a typed occurrence history |
| **Projections** (rebuildable views) | What it means | The derived Count/First/Last folded from that history at read time |

Gratitude is a **new registry kind, not an observation kind** — the same
net-new-kind decision the pet registry made ([`mvp/life-archive.md`](mvp/life-archive.md)
§"Pet registry kind"). A gratitude entry is a long-lived referent with its own
identity, alternate wordings, and lifecycle, exactly what the registry family
(`internal/observations/registry.go`) already models for injuries, places,
eras, and pets — so it reuses that machinery (the salted low-signal key
derivation, `aka[]`, the append-and-redirect tombstone) rather than inventing a
store. What it adds on top is a **typed occurrence history** in place of the
plain `status_history`, because a tally needs to fold a *count* and a
*first/last span*, not a status transition (§2).

`internal/storage` is the **only** package that touches `~/.lucid/`
(architecture P3); the gratitude record family, its canonical-key match, and
the count fold are deterministic, agent-free, and have **no LLM in any path**
(architecture P9) — a nightly `add` and the whole `list` complete with no model.
Automatic *by-meaning* matching (matching "my house" to a stored "a roof over
my head") is the one thing this layer deliberately does **not** attempt in v1;
it is the future work item **R-011** (§7), and until it lands the human agent
supplies that judgment through `--into` and `merge`.

## 2. The entry schema

Every gratitude entry is one JSON object — one file per entry — under
`~/.lucid/registries/gratitude/`, keyed by the salted, kind-prefixed registry
key (`gratitude_<slug>`), the same low-signal key derivation people, injuries,
and places use ([`observations.md`](observations.md) §8):

```json
{
  "key": "gratitude_a-river",
  "kind": "gratitude",
  "schema": 1,
  "display_name": "my morning coffee",
  "aka": ["my morning coffee", "the first cup of the day"],
  "history": [
    { "id": "grat_2026_08_23_001", "at": "2026-08-23T21:45:10-04:00", "type": "occurrence", "date": "2026-08-23", "source": "gratitude" },
    { "id": "grat_2026_08_24_001", "at": "2026-08-24T21:50:02-04:00", "type": "occurrence", "date": "2026-08-24", "source": "gratitude" }
  ],
  "redirect_to": "",
  "created_at": "2026-08-23T21:45:10-04:00",
  "updated_at": "2026-08-24T21:50:02-04:00"
}
```

Fields, binding:

| Field | Required | Meaning |
|-------|----------|---------|
| `key` | assigned | The stable entry id: `gratitude_<slug>`, the salted registry key derived from the normalized `display_name` under the storage adapter's single-writer discipline (§ Ids). This is what `list` shows and what `--into` / `merge` target. |
| `kind` | yes | Always `gratitude`. |
| `schema` | yes | The record schema version. `Schema = 1` today. |
| `display_name` | yes | The phrase you named, verbatim — the latest wording. |
| `aka` | yes | Other forms the same thing has been written as, including every wording absorbed by `--into` or a `merge`. Human readability only — resolution is by canonical key and tombstone, never by scanning `aka[]` (the people precedent, [`data-model.md`](mvp/data-model.md) §"Merge & redirect"). Marshals as `[]` when empty. |
| `history` | yes | The **append-only** typed event log the tally folds. Each event carries its own unique receipt id (§ Ids), a real write timestamp `at`, a `type`, and a logical `date`. The count and the first/last span are **derived** from it (§ Deriving the tally); no `count` / `first` / `last` field is stored. |
| `redirect_to` | no | Present only on a **merge tombstone**: the `key` of the canonical entry a merged-away duplicate now resolves to (§4). Omitted / empty on a live entry. Single-hop by invariant — a tombstone never points at another tombstone. |
| `created_at` | yes | When the entry was first created — an ISO-8601 local-TZ timestamp. |
| `updated_at` | yes | When the most recent event was appended. |

**The typed history.** Every mutation appends exactly one event; no event is
ever rewritten. Three event `type`s exist:

| `type` | Written by | Contributes to the fold |
|--------|-----------|-------------------------|
| `occurrence` | a nightly `add` (or `add --into`) | **+1** to the count at its `date`; widens the first/last span. |
| `seed` | a one-time `import` (or `add --count …`) | an explicit `count` (**+N**) with explicit `first` / `last` dates carried on the event — **no** per-occurrence dates are fabricated (§5). |
| `merge` | a `merge <src> <dst>` on the destination | folds the source's whole history in (its count and its first/last span), and names the source key it absorbed — an auditable record of the fold (§4). |

A `seed` event therefore carries `count`, `first`, and `last` inline; an
`occurrence` carries a single `date`; a `merge` carries the absorbed `source`
key. All three carry their own receipt `id` and real `at`.

**Deriving the tally.** Count/First/Last are a pure fold over `history`,
computed at read time and never stored:

* **Count** = the sum of every event's contribution (`occurrence` → 1, `seed` →
  its `count`, `merge` → the absorbed source's derived count).
* **First** = the earliest date across all contributing events (an
  `occurrence`'s `date`, a `seed`'s `first`, a `merge`'s absorbed `first`).
* **Last** = the latest such date.

Because the tally is a fold, it is rebuildable from the append-only history
alone — the source of truth is the events, and no derived number is ever
written back onto the record.

**Ids — two, kept distinct.** The entry has a **stable id** and each write has
its own **receipt id**; they are never interchangeable:

* The **stable entry id** is the registry `key` (`gratitude_<slug>`, e.g.
  `gratitude_a-river`) — one per referent, stable across its whole life, shown
  by `list`, and the only thing `--into` and `merge` accept.
* The **receipt id** is `grat_<logical_date>_<seq>` (e.g. `grat_2026_08_24_001`)
  — one per appended event, minted under the storage adapter's single-writer
  discipline (`seq` = max seq parsed from the entry's history plus one, never a
  count). **Every** mutating verb — `add`, `add --into`, `merge`, and `import`
  — appends an event and returns *that event's* receipt id, so two writes to the
  same entry return two different receipts. A backdated occurrence's receipt
  encodes the *logical* date, not the recording time.

The two id shapes never collide: the entry key is a word-slug
(`gratitude_a-river`), the receipt is date-and-sequence (`grat_2026_08_24_001`).

**Versioning.** `schema` is versioned per record family, exactly as the
observation envelope and the other registries are ([`observations.md`](observations.md)
§2). New needs go in a new optional field or a new event `type` under a bumped
`schema` — readers tolerate an unknown field and a higher schema version (read
what you understand, skip what you don't).

**Append-only, corrected by reference.** No line is ever rewritten in place.
The tally only ever grows an event; a duplicate is not deleted but folded and
redirected (§4). The store is built to outlive its tools (P6): plain
user-owned JSON, exportable as a directory of files.

## 3. The capture grammar — nightly `add`

Gratitude is tallied by a one-line, deterministic, agent-free verb — the same
design stance as the observation micro-log and the reframe capture
([`observations.md`](observations.md) §4, [`reframes.md`](reframes.md) §3,
architecture P9): sub-second, offline-capable, no LLM.

```
lucid gratitude add "my morning coffee"
lucid gratitude add "clean drinking water"
lucid gratitude add "a walk outside" --day @yesterday
```

Grammar, binding:

* **One positional argument:** the thing you're grateful for, stored verbatim
  as the entry's `display_name`. An empty phrase is a clean usage error and
  nothing is written.
* **Canonical-key auto-match (v1).** `add "<phrase>"` normalizes the phrase
  (lowercase, trim, strip punctuation, collapse whitespace — the shared
  `keyderive.Normalize`) and derives the salted canonical key. If a **live**
  entry already holds that key, `add` **bumps it**: it appends an `occurrence`
  event (+1, last-date refreshed) and returns a receipt. If no live entry
  matches, `add` **creates** a new entry with a first `occurrence` event. This
  is the whole of v1 matching: two phrasings that *normalize equal* land on the
  same entry; two phrasings that normalize differently land on **different**
  entries. Matching a short wording to a long stored phrase ("my house" to "a
  roof over my head") is **not** a normalized-string match and is deliberately
  **not** attempted here — that is R-011 (§7); the interim path is `--into`
  (§4).
* **`--into <id>`.** Bump a **specific** existing entry by its stable id,
  regardless of tonight's wording — the interim by-meaning path (§4). Covered
  in full there.
* **`--day` backdating.** `--day` sets the `occurrence` event's logical `date`
  (and therefore the entry's derived last-date), reading the one shared date
  grammar every logical-day verb uses
  ([`usage/commands.md`](usage/commands.md#backdating-with---day)): `@yesterday`
  (the logical day before this one, 04:00-rollover aware) and `@YYYY-MM-DD` (a
  civil day, taken literally). It is the **strict** tier — an unreadable token,
  or a day in the future, is a clean error naming the accepted forms, and
  **nothing is written.** With no `--day`, the occurrence files under today's
  logical day. The event's `at` is always the real write time regardless of
  backdating.

Capture is total (P1): one line, no form, no follow-up. `lucid gratitude add`
acknowledges only *after* the write lands and prints the receipt id (§2 Ids),
the same provenance-over-magic ack the observation micro-log and the reframe
capture give.

## 4. Targeting and dedup correction — `--into` and `merge`

Because v1 auto-match is canonical-key only, a night phrased differently from
the stored wording would create a *second* entry and re-fragment the very tally
this layer exists to keep deduped. Two verbs keep the by-meaning judgment with
the human agent (who is already making exactly that call) until R-011 automates
it — and every write stays deterministic and receipted.

**`lucid gratitude add "<phrase>" --into <id>`** bumps the entry named by
`<id>` (its stable `gratitude_<slug>` key) **regardless of the phrasing**. It
appends an `occurrence` event to that entry (+1, last-date refreshed) and
returns a receipt; the new wording is recorded into the entry's `aka[]` for
readability but never changes the canonical key. This is how the agent, seeing
tonight's "my house" and knowing entry `gratitude_a-river` already holds "a roof
over my head," tallies the right one instead of splitting it. `--into` naming a
key that does not resolve to a live entry is a clean error and appends nothing.

**`lucid gratitude merge <src> <dst>`** repairs an accidental duplicate after
the fact — the same append-and-redirect identity model `person merge` uses
([`data-model.md`](mvp/data-model.md) §"Merge & redirect"), with no delete:

* The **whole of `<src>`'s history folds into `<dst>`** — its count and its
  first/last span — via a `merge` event appended to `<dst>` that names the
  absorbed source key. `<dst>`'s `aka[]` absorbs `<src>`'s wordings.
* **`<src>` is rewritten as a redirect tombstone** (`redirect_to: <dst>`). It
  is **omitted from the active `list`** and from the tally, but the record is
  kept — an auditable redirect, never a hard delete.
* **Single hop, no cycles.** A tombstone never points at another tombstone; a
  self-merge and a cycle are structurally refused. Merging onto or from a
  missing entry is a clean error that changes nothing.
* The merge appends one event and returns its receipt, like every other
  mutation.

Together, `--into` (prevent a split at capture time) and `merge` (fold a split
that already happened) give a complete, receipted, storage-only way to keep the
tally deduped without ever hand-editing a file — and both are exactly the seam
R-011 will later drive automatically.

## 5. The seed / import migration path

A tally row carries a **Count** and a **First**/**Last** span, but history
rarely remembers every individual night a thing came up — a count of 4 may know
only its first and last date. Replaying four nightly `add`s cannot reproduce
that honestly: it would have to invent two dates that never happened. So seeding
an existing tally is a **distinct, one-time path**, never nightly `add`:

```
lucid gratitude import "clean drinking water" --count 22 --first 2025-11-02 --last 2026-08-20
lucid gratitude add   "clean drinking water" --count 22 --first 2025-11-02 --last 2026-08-20
```

* `import` (and the equivalent `add --count N --first <date> --last <date>`)
  writes **one** entry carrying a **single `seed` event** with the explicit
  `count`, `first`, and `last`. It fabricates **no** per-occurrence dates — the
  raw per-night truth already lives forever in the separate `#gratitude` logs,
  so the tally seed does not need to invent it.
* It is documented and behaves as a **migration affordance**, distinct from the
  nightly `add`: `add` records one dated occurrence; `import` records a
  pre-counted span.
* **`import` is not idempotent.** Re-running it appends a *second* `seed` event
  and **double-counts**. A migration is therefore a single pass; if it fails
  partway and must be retried, restore the pre-migration `lucid backup` snapshot
  first, then replay — never re-run imports on top of a partially-seeded
  Ledger.

After seeding, ordinary nightly `add`s accumulate on top of the seeded count
normally (the seed contributes its N; each later occurrence adds one).

## 6. Reading the record — `list`

**`lucid gratitude list`** reads the live entries (tombstones omitted), folds
each one's Count/First/Last, and prints the tally **sorted by count then
recency** — the things you return to most, most-recently, at the top. Each row
shows its **stable id** (`gratitude_<slug>`) so `--into` and `merge` can target
the right entry, alongside the count and the first/last span:

```
lucid gratitude list
lucid gratitude list --json
```

* Human-first output leads with the count and the phrase and shows the stable
  id for targeting.
* **`--json`** emits the structured tally — the machine-readable shape,
  consistent with the other read verbs' JSON so automation never scrapes prose
  ([ADR-0007](adr/0007-cli-conventions.md), [`observations.md`](observations.md)
  §7). `--json` is the persistent root flag every command carries; on `add`,
  `import`, and `merge` it emits the receipt and the resulting entry where that
  fits the verb's ergonomics.

`list` is a pure read — it writes nothing and folds the tally fresh from the
append-only history each time.

## 7. The canonical-key seam → R-011

v1 matching is a **single, documented canonical-key function**: normalize the
phrase, derive the salted key, match a live entry by that key. That one function
is the **seam**. It intentionally stops at string canonicalization — it will
never connect "my house" to a stored "a roof over my head," because those
normalize to different keys.

**By-meaning (semantic) matching is the future work item R-011.** R-011 owns
replacing the human `--into`/`merge` judgment with an automatic semantic match:
when it lands, it slots in behind this same seam — the nightly `add` stays one
line, but the match step consults meaning, not just the normalized string, and
proposes the entry a human would have chosen with `--into`. Nothing else in the
schema or the verb surface changes; `--into` and `merge` remain as the manual
override and the correction verb. This layer builds **only** the seam and the
interim human path; it does not attempt semantic matching, and it does not
depend on any model to run (P9). R-011 is the single owner of that upgrade, and
both this document and the match function name it as such.

## 8. Boundaries

* **Sanctuary and inventory, not obligation** (§0). No quota, no streak, no "you
  weren't grateful today." The tally counts; it never grades.
* **The raw `#gratitude` capture is untouched.** The verbatim nightly gratitude
  is an ordinary `lucid log` entry and remains the source of truth; the tally is
  a derived count *over* that practice, added beside it, never in place of it.
* **`internal/storage` is the sole `~/.lucid/` writer** (architecture P3). The
  gratitude record family, the canonical-key match, and the count fold are
  agent-free and carry no LLM in any path (P9).
* **Append-only** (§2). History is never rewritten; a duplicate is folded and
  redirected (§4), never deleted. The store is built to outlive its tools (P6):
  plain user-owned JSON, exportable as a directory of files.
* **Public-safe** ([`CLAUDE.md`](../CLAUDE.md) invariants). Every example in
  this document and in the tests is synthetic; real gratitude entries live only
  in the private Ledger under `~/.lucid/`, never in the repo.

## 9. Defaults

Logical-day rollover 04:00 (shared with observations, reframes, and the Engine)
· v1 auto-match = canonical key (normalized phrase) only; by-meaning is R-011 ·
`--into` targets a stable id regardless of wording; `merge` folds + redirects
(single-hop, no cycles) · `import` / `add --count` writes one `seed` event with
explicit Count/First/Last and fabricates no dates; it is **not** idempotent ·
`add --day` strict tier, future dates rejected, real write time kept as the
event's `at` · `list` sorted by count then recency, shows the stable id ·
`schema` = 1 · occurrence `source` default `gratitude` (`migration` for a seed).
All timestamps are ISO-8601 with the host's local-TZ offset, the same rule as
every record under `~/.lucid/` ([`mvp/data-model.md`](mvp/data-model.md)
§"Time zone rule").
