# Lucid — Gratitude Tally (accumulating count)

**Date:** 2026-08-25 · **Revised:** 2026-09-29 (by-meaning matching + outward expression, [ADR-0012](adr/0012-gratitude-semantic-matching.md)) · **Status:** Canonical — a living concept, evolving with the project
**Scope:** The inner-work **gratitude tally**: the running count of the things
a person keeps returning to gratitude for — "grateful for my morning coffee
×15." This document defines the gratitude record family, its append-only store
under `~/.lucid/registries/gratitude/`, the typed history the count/first/last
are folded from, the three-tier match that decides which entry a nightly `add`
lands on, the outward-expression record (linking an entry to a person and
noting that you told them), and the `lucid gratitude` CLI surface (`add` /
`list` / `merge` / `import` / `reconcile` / `thank`). It contains no instance
data; every example here is synthetic.

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

The same holds for **saying it out loud** (§8). Lucid can remember that a
gratitude involves someone, offer a quiet "you might tell them," and keep a
private note that you did — but telling someone is never a task, never counted,
never a completion bar, and never escalated. The note that you expressed it
does not move the tally.

## 1. Position in the foundation

Lucid's foundation is three parts ([`architecture.md`](architecture.md)), and
this layer adds instances of one without changing any of them:

| Part | Holds | This layer adds |
|------|-------|-----------------|
| **Ledger** (append-only) | What happened / what was said | A new registry kind: `gratitude` entries with a typed occurrence history |
| **Projections** (rebuildable views) | What it means | The derived Count/First/Last (and each linked person's last-expressed date) folded from that history at read time |

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
(architecture P3). The gratitude record family, the count fold, and the first
two match tiers — the canonical key and the normalized token match — are
deterministic, agent-free, and carry **no LLM** (architecture P9): a nightly
`add` and the whole of `list` complete with no model. Matching **by meaning** —
connecting tonight's "my bike" to a stored "the two wheels that carry me to
work", which share no words — is the one step that needs a model. It is tier 3
of the match (§7): **optional** and **off until you opt in** (§7.5), reached
only through the single `internal/provider` seam, and run only when the
deterministic tiers are not confident. With tier 3 off, or no model reachable,
`add` behaves exactly as tiers 1–2 decide and still completes (§7.6).
The human keeps the last word through `--into`, `--new`, and `merge`.

## 2. The entry schema

Every gratitude entry is one JSON object — one file per entry — under
`~/.lucid/registries/gratitude/`, keyed by the salted, kind-prefixed registry
key (`gratitude_<slug>`), the same low-signal key derivation people, injuries,
and places use ([`observations.md`](observations.md) §8):

```json
{
  "key": "gratitude_a-river",
  "kind": "gratitude",
  "schema": 2,
  "display_name": "coffee with Sam on the porch",
  "aka": ["coffee with Sam on the porch", "slow coffee outside"],
  "people": ["person_a-river"],
  "history": [
    { "id": "grat_2026_08_23_001", "at": "2026-08-23T21:45:10-04:00", "type": "occurrence", "date": "2026-08-23", "source": "gratitude", "person": "person_a-river" },
    { "id": "grat_2026_08_24_002", "at": "2026-08-24T21:50:02-04:00", "type": "occurrence", "date": "2026-08-24", "source": "gratitude", "match_tier": 3, "match_score": 0.94 },
    { "id": "grat_2026_08_25_003", "at": "2026-08-25T19:12:40-04:00", "type": "expressed", "date": "2026-08-25", "person": "person_a-river" }
  ],
  "redirect_to": "",
  "created_at": "2026-08-23T21:45:10-04:00",
  "updated_at": "2026-08-25T19:12:40-04:00"
}
```

Fields, binding:

| Field | Required | Meaning |
|-------|----------|---------|
| `key` | assigned | The stable entry id: `gratitude_<slug>`, the salted registry key derived from the normalized `display_name` under the storage adapter's single-writer discipline (§ Ids). This is what `list` shows and what `--into` / `merge` / `thank` target. |
| `kind` | yes | Always `gratitude`. |
| `schema` | yes | The record schema version. `Schema = 2` today (§ Versioning). |
| `display_name` | yes | The phrase you named, verbatim — the latest wording that landed by canonical key. |
| `aka` | yes | Other forms the same thing has been written as, including every wording absorbed by `--into`, a confirmed suggestion, an automatic tier-2/3 match, or a `merge`. Key resolution (tier 1) never scans `aka[]` — resolution is by canonical key and tombstone only (the people precedent, [`data-model.md`](mvp/data-model.md) §"Merge & redirect"). The separate tier-2/3 match pass (§7) **reads** `aka[]` as match evidence; that is discovery, not resolution — the same split [ADR-0011](adr/0011-person-alias-resolution.md) draws for people. Marshals as `[]` when empty. |
| `people` | no | The person keys (`person_<slug>`) this gratitude is linked to (§8), each the canonical live key at link time. Grows only — a link is added by `add --person` or `thank`, absorbed by `merge`, never removed. Omitted or `[]` when unlinked. |
| `history` | yes | The **append-only** typed event log the tally folds. Each event carries its own unique receipt id (§ Ids), a real write timestamp `at`, a `type`, and a logical `date`. The count, the first/last span, and each person's last-expressed date are **derived** from it (§ Deriving the tally); no `count` / `first` / `last` field is stored. |
| `redirect_to` | no | Present only on a **merge tombstone**: the `key` of the canonical entry a merged-away duplicate now resolves to (§4). Omitted / empty on a live entry. Single-hop by invariant — a tombstone never points at another tombstone. |
| `created_at` | yes | When the entry was first created — an ISO-8601 local-TZ timestamp. |
| `updated_at` | yes | When the most recent event was appended. |

**The typed history.** Every mutation appends exactly one event; no event is
ever rewritten. Four event `type`s exist:

| `type` | Written by | Contributes to the fold |
|--------|-----------|-------------------------|
| `occurrence` | a nightly `add` (or `add --into`, or a confirmed suggestion) | **+1** to the count at its `date`; widens the first/last span. |
| `seed` | a one-time `import` (or `add --count …`) | an explicit `count` (**+N**) with explicit `first` / `last` dates carried on the event — **no** per-occurrence dates are fabricated (§5). |
| `merge` | a `merge <src> <dst>` on the destination (by hand or by `reconcile --apply`) | folds the source's whole history in (its count and its first/last span), and names the source key it absorbed — an auditable record of the fold (§4). |
| `expressed` | `thank <id> --person <subject>` | **nothing** — tally-neutral. It records that you told the named person, on its `date`, and feeds only that person's last-expressed date (§8). |

Per-type fields, all optional and omitted when unset:

* An `occurrence` carries a single `date` and a `source`. When a tier-2 or
  tier-3 match chose the entry **automatically** (§7.4 High band), it also
  carries `match_tier` (`2` or `3`) and `match_score` (the winning score, 0–1),
  so an automatic landing is always distinguishable and attributable. An
  occurrence that landed by canonical key (tier 1), by `--into`, by a
  human-confirmed suggestion, or that created its entry carries neither —
  those are exactly the v1 shapes, unchanged. When the same write linked a
  person (`add --person`), the occurrence carries that `person` key.
* A `seed` carries `count`, `first`, and `last` inline.
* A `merge` carries the absorbed `source_key` and that source's derived
  `source_count` / `source_first` / `source_last`, plus — when the source had
  any `expressed` events — `source_expressed`, a map of person key → that
  source's last-expressed date, so the destination's fold stays local.
* An `expressed` event carries the `person` key and its logical `date`.

All four carry their own receipt `id` and real `at`.

**Deriving the tally.** Count/First/Last are a pure fold over `history`,
computed at read time and never stored:

* **Count** = the sum of every event's contribution (`occurrence` → 1, `seed` →
  its `count`, `merge` → the absorbed source's derived count, `expressed` → 0).
* **First** = the earliest date across all contributing events (an
  `occurrence`'s `date`, a `seed`'s `first`, a `merge`'s absorbed `first`).
* **Last** = the latest such date. An `expressed` event never moves First or
  Last.
* **Last expressed** (per linked person) = the latest `date` across this
  entry's `expressed` events naming that person and any `merge` event's
  `source_expressed` entry for them. Empty when never expressed.

Because the tally is a fold, it is rebuildable from the append-only history
alone — the source of truth is the events, and no derived number is ever
written back onto the record.

**Ids — two, kept distinct.** The entry has a **stable id** and each write has
its own **receipt id**; they are never interchangeable:

* The **stable entry id** is the registry `key` (`gratitude_<slug>`, e.g.
  `gratitude_a-river`) — one per referent, stable across its whole life, shown
  by `list`, and the only thing `--into`, `merge`, and `thank` accept.
* The **receipt id** is `grat_<logical_date>_<seq>` (e.g. `grat_2026_08_24_002`)
  — one per appended event, minted under the storage adapter's single-writer
  discipline (`seq` = max seq parsed from the entry's history plus one, never a
  count). **Every** mutating verb — `add` (however it matched), `add --into`,
  `merge`, `import`, `reconcile --apply` (one per fold), and `thank` — appends
  an event and returns *that event's* receipt id, so two writes to the same
  entry return two different receipts. A backdated event's receipt encodes the
  *logical* date, not the recording time.

The two id shapes never collide: the entry key is a word-slug
(`gratitude_a-river`), the receipt is date-and-sequence (`grat_2026_08_24_002`).

**Versioning.** `schema` is versioned per record family, exactly as the
observation envelope and the other registries are ([`observations.md`](observations.md)
§2). New needs go in a new optional field or a new event `type` under a bumped
`schema` — readers tolerate an unknown field and a higher schema version (read
what you understand, skip what you don't). Schema 2 is that move: it adds the
optional `people` field, the `expressed` event type, and the optional
`match_tier` / `match_score` / `person` / `source_expressed` event fields.
Every schema-2 addition is optional, so a **schema-1 entry reads unchanged** and
is written back as schema 2 on its next write — there is no migration pass.
Readers (and `lucid validate`) accept both 1 and 2.

**Append-only, corrected by reference.** No line is ever rewritten in place.
The tally only ever grows an event; a duplicate is not deleted but folded and
redirected (§4). The store is built to outlive its tools (P6): plain
user-owned JSON, exportable as a directory of files.

## 3. The capture grammar — nightly `add`

Gratitude is tallied by a one-line verb — the same design stance as the
observation micro-log and the reframe capture ([`observations.md`](observations.md)
§4, [`reframes.md`](reframes.md) §3, architecture P9): sub-second on the
deterministic path, offline-capable, and never dependent on a model.

```
lucid gratitude add "my morning coffee"
lucid gratitude add "clean drinking water"
lucid gratitude add "a walk outside" --day @yesterday
lucid gratitude add "coffee with Sam on the porch" --person person_a-river
lucid gratitude add "the walk home" --new
```

Grammar, binding:

* **One positional argument:** the thing you're grateful for, stored verbatim
  (as a new entry's `display_name`, or into `aka[]` when it lands on an existing
  entry by a different wording). An empty phrase is a clean usage error and
  nothing is written.
* **Matching (§7).** `add "<phrase>"` decides which entry the occurrence lands
  on through the three match tiers, cheapest and most certain first: the
  **canonical key** (tier 1 — the v1 behavior, unchanged: two phrasings that
  normalize equal are the same entry), then a deterministic **normalized token
  match** against every live entry's wordings (tier 2), then — only if neither
  is confident and a model is configured — the optional **by-meaning judge**
  (tier 3). The outcome is one of three bands: a confident single winner is
  **bumped automatically**; a genuinely new thing **creates** an entry; anything
  in between is **suggested**, never guessed (§7.4).
* **`--into <id>`.** Bump a **specific** existing entry by its stable id,
  regardless of tonight's wording — the manual override, and the way a
  non-interactive caller resolves a suggestion (§4, §7.4). No matching runs.
* **`--new`.** Start a **new** entry for this phrase — the way a caller answers
  "no, this is something else" to a suggestion. Tiers 2 and 3 are skipped; tier
  1 still applies, because a phrase that normalizes equal to a live entry's
  canonical key *is* that entry by definition (it bumps, exactly as v1 did).
  `--new` cannot be combined with `--into` or `--count`.
* **`--person <subject>`.** Link the entry this `add` lands on to a person in
  the people registry (§8) — a **link-only** convenience: it records no
  "expressed" event and has no tally effect beyond the ordinary occurrence. The
  subject is resolved and validated **before** anything is written; an
  unresolvable or ambiguous subject writes nothing.
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

Capture is total (P1): one line, no form. The only follow-up `add` ever asks is
the §7.4 "did you mean …?" — and only on a terminal, only in the ambiguous
band. `lucid gratitude add` acknowledges only *after* the write lands and prints
the receipt id (§2 Ids), the same provenance-over-magic ack the observation
micro-log and the reframe capture give; an automatic by-meaning landing says so
in the ack (§7.4).

## 4. Targeting and dedup correction — `--into` and `merge`

Automatic matching (§7) handles the common case, but the human keeps the last
word. Two verbs carry that judgment directly — and every write stays
deterministic and receipted.

**`lucid gratitude add "<phrase>" --into <id>`** bumps the entry named by
`<id>` (its stable `gratitude_<slug>` key) **regardless of the phrasing**. It
appends an `occurrence` event to that entry (+1, last-date refreshed) and
returns a receipt; the new wording is recorded into the entry's `aka[]` for
readability — and, from then on, as tier-2 match evidence (§7.1) — but never
changes the canonical key. It is the manual override when matching would get it
wrong, and it is how a non-interactive caller resolves an ambiguous-band
suggestion (§7.4). `--into` naming a key that does not resolve to a live entry
is a clean error and appends nothing.

**`lucid gratitude merge <src> <dst>`** repairs an accidental duplicate after
the fact — the same append-and-redirect identity model `person merge` uses
([`data-model.md`](mvp/data-model.md) §"Merge & redirect"), with no delete:

* The **whole of `<src>`'s history folds into `<dst>`** — its count, its
  first/last span, and any last-expressed dates — via a `merge` event appended
  to `<dst>` that names the absorbed source key. `<dst>`'s `aka[]` absorbs
  `<src>`'s wordings and its `people[]` absorbs `<src>`'s links.
* **`<src>` is rewritten as a redirect tombstone** (`redirect_to: <dst>`). It
  is **omitted from the active `list`**, from the tally, and from every match
  tier as a target, but the record is kept — an auditable redirect, never a hard
  delete.
* **Single hop, no cycles.** A tombstone never points at another tombstone; a
  self-merge and a cycle are structurally refused. Merging onto or from a
  missing entry is a clean error that changes nothing.
* The merge appends one event and returns its receipt, like every other
  mutation. `reconcile --apply` (§7.9) folds through exactly this path.

Together, `--into` (prevent a split at capture time) and `merge` (fold a split
that already happened) give a complete, receipted, storage-only way to keep the
tally deduped without ever hand-editing a file — and both are the seam the
automatic tiers (§7) drive.

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
  pre-counted span. Seeding resolves by **canonical key only** — tiers 2 and 3
  never run on `import` or `add --count`, so a migration is exactly as
  deterministic and model-free as it was in v1. Fold any duplicates it leaves
  with `reconcile` (§7.9) or `merge`.
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
shows its **stable id** (`gratitude_<slug>`) so `--into`, `merge`, and `thank`
can target the right entry, alongside the count, the first/last span, and — for
an entry linked to people (§8) — each person and when you last told them:

```
lucid gratitude list
lucid gratitude list --json
```

* Human-first output leads with the count and the phrase and shows the stable
  id for targeting. A linked person is named on the row with their
  last-expressed date when there is one. Below the tally, `list` may add the
  gentle "you might tell …" lines (§8) — offered, never required, and never a
  count.
* **`--json`** emits the structured tally — the machine-readable shape,
  consistent with the other read verbs' JSON so automation never scrapes prose
  ([ADR-0007](adr/0007-cli-conventions.md), [`observations.md`](observations.md)
  §7): each entry's `id`, `thing`, `aka`, `count`, `first`, `last`, and a
  `people` array of `{person_key, display_name, off_limits, last_expressed}`
  (`[]` when unlinked; `last_expressed` is `""` when never expressed), plus a
  top-level `reminders` array (§8; `[]`, never null). `--json` is the persistent
  root flag every command carries; on `add`, `import`, `merge`, `reconcile`, and
  `thank` it emits the receipt(s) and the resulting entry where that fits the
  verb's ergonomics.

`list` is a pure read — it writes nothing, calls no model, and folds the tally
fresh from the append-only history each time.

## 7. Matching by meaning — the three tiers (R-011)

v1 shipped a **single, documented canonical-key function** — normalize the
phrase, derive the salted key, match a live entry by that key — and named it
the **seam** for R-011, the work item that owns by-meaning matching. This
section is that upgrade. The nightly `add` stays one line; behind the same seam
the match step now consults up to **three tiers**, cheapest and most certain
first, and stops at the first confident answer. Nothing about the schema's
identity model or the verb surface is replaced: `--into` and `merge` remain the
manual override and the correction verb, and the canonical key is still tier 1.

**When matching runs.** On a plain nightly `add` — one without `--into`,
`--count`, or `--new` — and in the `reconcile` pass (§7.9). `import`,
`add --count`, `add --into`, `merge`, and `thank` never run tiers 2 or 3.

### 7.1 The tiers

**Tier 1 — canonical key (unchanged).** Normalize the phrase (the shared
`keyderive.Normalize`: lowercase, trim, strip punctuation, collapse whitespace)
and derive the salted canonical key. A **live** entry at that key is bumped
immediately — no tier 2, no model call, and the same record, ack, and `--json`
output as v1 (a tier-1 bump carries no `match_tier`). One refinement: when the
key names a **merge tombstone**, the occurrence forwards the single hop to the
tombstone's live destination (the wording joins that entry's `aka[]`) instead of
refusing as v1 did — a merged-away wording now resolves to the entry it was
folded into, the same forward resolution person tombstones give. When no live
entry or tombstone holds the key, matching continues to tier 2.

**Tier 2 — normalized token match (deterministic, no model).** Each wording is
reduced to a **token set**: the tier-1 normalization, then split on whitespace,
then a small fixed English stopword list removed (`a`, `an`, `the`, `my`,
`our`, `your`, `his`, `her`, `their`, `its`, `of`, `for`, `to`, `and`, `with`,
`in`, `on`, `at`, `that`, `this`, `these`, `those`, `some`), then **light
stemming** (a possessive `'s` dropped; a plural `-ies` → `-y`, with a word
ending `-ie` folded to `-y` the same way so "cookie" and "cookies" agree; `-es`
dropped after `ss`/`x`/`z`/`ch`/`sh` — not after a single `s`, so "houses" meets
"house"; otherwise a trailing `-s` dropped except after `s`/`u`/`i`; a rule that
would cut a stem below three letters is skipped). The phrase's set is
scored against **every live entry** — each of its wordings (`display_name` and every `aka[]` form) — with
the **Dice coefficient**, `2·|A∩B| / (|A|+|B|)`; an entry's score is its best
wording's score. A phrase whose set is empty (all stopwords) scores 0
everywhere. Tier 2 is a pure function of the phrase and the live entries: it
reads no disk itself (the router hands it the live set), calls no model, and
never scores a tombstone — a merged-away entry is not a match target, so every
tier-2 candidate is already the canonical live entry.

Two properties are deliberate. **No edit distance:** "my dog" and "my dad"
score 0 (`{dog}` vs `{dad}` share nothing), where a character-distance fuzz
would pair them — exactly the false-positive class
[ADR-0011](adr/0011-person-alias-resolution.md) removed from person reconcile.
**Dice, not containment:** a one-word phrase is not a confident match for every
long entry that contains that word — `{bike}` against a five-token description
scores 0.33 — so a short nightly phrase against an elaborated stored entry falls
to the ambiguous or low band and is handed to tier 3, the tier built for
exactly that.

**`aka[]` is how tier 2 learns.** Every wording that lands on an entry —
through `--into`, a confirmed suggestion, or an automatic match — joins that
entry's `aka[]`. The next night the same wording is an exact tier-2 hit (score
1.0) with no model involved, so the model is consulted only for genuinely new
phrasings. Tier-1 key resolution still never scans `aka[]` (§2).

**Tier 3 — the by-meaning judge (optional, model-backed).** Runs only when tiers
1–2 did not produce a confident automatic match (tier 2 landed in the ambiguous
or low band) **and** tier 3 is enabled. It is one bounded call through the
`internal/provider` seam that asks a model to judge the new phrase against the
**live list** — not against tier 2's lexical shortlist, so a match that shares
zero words ("my bike" → "the two wheels that carry me to work") is reachable —
and returns a score per candidate it judges to be the same thing. The model only
scores; the band rule (§7.2) decides. Its contract, privacy, and default
provider are §7.5; its failure behavior is §7.6.

### 7.2 Scores, confidence bands, and the margin

Tiers 2 and 3 each yield a ranked list of candidates with scores in [0, 1].
Each list is classified into one of three **confidence bands** by the same
rule, with each tier's own cutoffs (§9 Defaults):

| Band | Rule (top-1 / top-2 = best / second-best score; top-2 = 0 when there is one candidate) | `add` does |
|------|------|-----------|
| **High** | top-1 ≥ the tier's **high cutoff** **and** top-1 − top-2 ≥ the tier's **margin** | auto-bump the top-1 entry |
| **Ambiguous** | top-1 ≥ `ambiguous_floor`, but not High | suggest — never silently merge, never silently create |
| **Low** | top-1 < `ambiguous_floor`, or no candidates | create a new entry |

The **margin** is what makes "High" mean *a clear single winner*. A strong
top-1 with a close runner-up is Ambiguous, not High: "the walk home" scoring
0.5 against both "the walk to work" and "a quiet home" is an exact tie, and a
tier-3 pair like 0.91 / 0.86 is a near-tie — both are suggestions, never an
automatic fold. A plausible-but-unconfident single candidate (top-1 between the
floor and the high cutoff) is Ambiguous too, so a near-duplicate is surfaced
rather than silently created. Tier-3 matches are eligible for the automatic
High band under the **same** margin rule, with their own, stricter default
cutoffs ([ADR-0012](adr/0012-gratitude-semantic-matching.md) §2).

All cutoffs and both margins are configuration (`gratitude.match` in
`lucid.json`, [`mvp/data-model.md`](mvp/data-model.md) §"`lucid.json`"). The
defaults in §9 are conservative placeholders tuned against the synthetic
fixtures; an out-of-range value is clipped to its default with a load-time
warning — fail-safe, never a crash.

### 7.3 Deciding across tiers

1. **Tier 1 hit** (a live entry, or a tombstone forwarding to one) → bump.
   Stop.
2. **Tier 2 High** → auto-bump the tier-2 winner. Stop — no model call.
3. **Tier 3**, when enabled and tier 2 was Ambiguous or Low:
   * tier-3 **High** → auto-bump the tier-3 winner;
   * tier-3 **Ambiguous** → suggest the tier-3 candidates;
   * tier-3 **Low** → fall back to the tier-2 band (Ambiguous → suggest the
     tier-2 candidates; Low → create).
4. **Tier 3 disabled or unavailable** (§7.6) → the tier-2 band stands
   (Ambiguous → suggest; Low → create).

The combining rule is the safer-outcome rule: **tier 3 can promote an add to a
match, but it can never turn a tier-2 ambiguity into a silent create.**

### 7.4 What `add` does in each band — the suggest-vs-auto boundary

**High — auto-bump.** The occurrence is appended to the matched entry with
`match_tier` and `match_score` (§2), the new wording joins `aka[]` (the
canonical `display_name` is unchanged), and the receipt is returned. The ack
says it matched and how, so the automatic step is legible and correctable:

```
Tallied "the two wheels that carry me to work" (×9) as `grat_2026_09_28_014` — matched "my bike" by meaning (tier 3).
```

Under `--json` the ordinary add view carries `match_tier` and `match_score`.

**Low — create.** A new entry with its first occurrence, exactly as v1 created
one.

**Ambiguous — suggest.** The add never guesses. What happens depends on whether
someone is there to answer:

* **Interactive** (stdin is a terminal, `--json` is not set, and the phrase
  was not itself read from stdin by `--body-file -`): `add` asks on stderr —
  so stdout still carries only the ack — and writes only after the answer —

  ```
  Did you mean to bump gratitude_a-river: "the walk to work"?
    [y] bump it   [n] start a new entry   [2] gratitude_b-stone: "a quiet home"   [q] cancel
  ```

  `y` bumps the named entry, a number picks another listed candidate, `n`
  creates a new entry (exactly as `--new` would), and `q` (or end of input)
  cancels with nothing written, exiting `1` like any other deferred choice.
  Any other answer — a blank line included — names the choices and asks
  again; nothing is ever chosen by default. A confirmed suggestion is the
  human's call, so it lands exactly as `--into <id>` would — no `match_tier` is
  stamped.
* **Non-interactive or `--json`: refuse and defer.** There is no one to ask,
  and neither "merge" nor "create" may be silent, so `add` **writes nothing**,
  **exits 1**, and returns the suggestion. The caller — a script, or a harness
  agent relaying the question to the human in conversation — resolves it by
  re-running with an explicit **`--into <id>`** (bump that entry) or
  **`--new`** (create). Under `--json` the suggestion is the stdout payload;
  otherwise the same facts are printed as a sentence on stderr:

  ```json
  {
    "status": "suggestion",
    "thing": "the walk home",
    "band": "ambiguous",
    "match_tier": 2,
    "candidates": [
      { "id": "gratitude_a-river", "thing": "the walk to work", "score": 0.5 },
      { "id": "gratitude_b-stone", "thing": "a quiet home", "score": 0.5 }
    ],
    "resolve": ["--into <id>", "--new"],
    "saved": false
  }
  ```

  `candidates` lists every candidate at or above `ambiguous_floor`, best first,
  at most three; `match_tier` names the tier whose band produced the
  suggestion; a `tier3` field (§7.6) says whether the model was consulted.

### 7.5 The tier-3 judge — contract, privacy, and default provider

**What is sent — and nothing else.** One request, intent label
`gratitude.match`, carrying **only** the new phrase and the candidate entries'
wordings (each live entry's `display_name` and `aka[]` forms), numbered
positionally. No entry ids or keys, no counts or dates, no people links, no
journal text, no other Ledger data leave the process. The reply names
candidates by their position; the router maps positions back to entries. Tests
assert the exact request the fake provider received.

**Which candidates.** Every live entry — semantic retrieval over the whole
tally, never tier 2's lexical shortlist — **except** any entry linked to a
person marked off-limits (§8), which is withheld from the judge, fail closed;
tiers 1–2 still match it deterministically. An `add --person` naming an
off-limits person skips tier 3 for that add. At a tally of a hundred-odd short
wordings the whole list is a few hundred tokens, so there is no embedding
index and no persisted vector store ([ADR-0012](adr/0012-gratitude-semantic-matching.md)
§1). Above `tier3_max_candidates` entries, the request carries the entries with
any tier-2 score first (best first), then the most recently tallied, up to the
cap.

**What comes back.** A JSON object `{"matches": [{"n": <position>, "score":
<0–1>}]}` listing only the candidates the model judges to name the same thing
(an empty list means "none of these"). The reply must be that object alone —
or that object inside a single enclosing markdown code fence, which some hosted
models add by habit. A reply that does not parse (prose or a thinking preamble
around the object included), names an unknown or repeated position, omits a
position or score, or carries a score outside [0, 1] is treated as
**unavailable** (§7.6) — never partially trusted. The judge decides nothing:
its scores go through the §7.2 band rule like tier 2's.

**Which model.** The judge is built through `internal/provider/factory` from
the `provider` block with two overrides in `gratitude.match`: `tier3_backend`
and `tier3_model` (each empty → inherit `provider.backend` / `provider.model`,
the same override rule the companion's and workout's `model` keys use). The
endpoint is `provider.endpoint`; the per-call bound is `tier3_timeout_seconds`,
short enough that a stalled model never holds up a nightly `add`.

* **Off until you opt in.** `tier3_enabled` defaults to `false`: out of the
  box `add` matches on tiers 1–2 alone and **nothing leaves the machine**.
  Gratitude phrasings are intimate, so sending them anywhere is your call.
* **Default judge: `claude_cli` with `sonnet`** — the configuration that
  cleared the trust gate. Enabling tier 3 with it sends the minimal payload
  above — the phrase and the candidate wordings, nothing else — to the vendor's
  hosted model through the on-host CLI: an explicit, documented egress.
* **Local: `ollama`.** Setting `tier3_backend` to `ollama` with a local
  `tier3_model` keeps the phrase and wordings on the machine. It is supported,
  but no local model evaluated cleared the trust gate at the default bound —
  one answered too slowly, another confidently merged unrelated phrases — and a
  "thinking" model needs a longer `tier3_timeout_seconds`. An unrecognized
  `tier3_backend` is coerced to `ollama`, so a typo fails safe toward local.
* **The trust gate.** A backend and model become the default only after their
  match quality is checked against the synthetic fixture set (zero-overlap true
  matches, near-ties, different-meaning look-alikes, genuinely new phrases).
  Local-first was the intent; the local models underperformed, so `claude_cli`
  is documented and configured as the default instead, and tier 3 ships opt-in.
  The evaluation, its per-model results, and the resulting default are recorded
  in [ADR-0012](adr/0012-gratitude-semantic-matching.md) §6. Either way tier 3
  stays optional.

**Sanctuary reach.** Gratitude entries live under `~/.lucid/registries/`,
which the cross-cutting sanctuary denylist keeps away from agent inference
([`mvp/agent-contracts.md`](mvp/agent-contracts.md) §"Cross-cutting rules").
The judge is not an inference agent mining that tree: it is this layer's own
**match step**, handed a bounded slice (wordings only) by the router for a
user-invoked capture, returning scores the deterministic band rule acts on,
writing nothing, and introducing no hypothesis — the same module-side reach the
workout compose documents. The contract note lives in
[`mvp/agent-contracts.md`](mvp/agent-contracts.md) §"Note — the gratitude match
reach"; the denylist for Reflection-class inference stands unchanged.

### 7.6 No model, no problem — P9 degradation

Tier 3 is never load-bearing. When it is **disabled** (`tier3_enabled: false`,
the default),
**unavailable** (no provider reachable, a timeout, or a provider error — the
`provider.ErrUnavailable` / `ErrTimeout` outage class), or **untrustworthy** (a
malformed reply), the add proceeds on the tier-1/tier-2 decision alone (§7.3
step 4) and completes:

* **Exit status** is the add's own — `0` when it writes (a bump or a create);
  `1` only for the ordinary refusals (an empty phrase, a rejected `--day`, an
  ambiguous-band suggestion). A model outage is **never** an add failure.
* **Output says so.** When tier 3 was needed but unavailable, a one-line note
  goes to stderr ("meaning match unavailable — matched by wording only"), and
  the `--json` view carries `"tier3": "unavailable"`. The `tier3` field reads
  `used`, `disabled`, `unavailable`, or `off_limits`, and is omitted when tier 3
  was not needed (a tier-1 or tier-2-High landing, `--into`, `--new`,
  `--count`), so a v1-shaped add stays v1-shaped.
* **`list` never calls a model**, and `import`, `merge`, `thank`, and
  `reconcile --apply` never need one.

Every tier-3 test uses `provider.Fake` — no test opens a socket or needs a live
model ([ADR-0006](adr/0006-model-access.md)).

### 7.7 Audit and receipts

Every automatic action is a receipted, attributable event, and its receipt is
the id of the one event it appended:

* An **automatic bump** appends an `occurrence` carrying `match_tier` and
  `match_score` to the matched entry, and returns that occurrence's receipt
  (the ack prints it; `--json` carries it as `receipt`).
* A **`reconcile --apply` fold** is an ordinary `merge` event on the target
  plus a `redirect_to` tombstone on the source, with the single-hop invariant
  intact — exactly what `lucid gratitude merge` writes — and returns one
  receipt per fold: the id of that fold's `merge` event, which names the
  absorbed `source_key`.

A receipt is unique within the entry that minted it (§2 Ids), so a receipt
plus the entry it names always finds exactly one event. Nothing automatic is
ever unrecorded, and nothing is ever deleted.

### 7.8 Undo — correcting a wrong automatic match

Append-only means a correction is a new event, not an erasure:

* **A wrong automatic bump.** Find it in the entry's history — it is the
  occurrence with a `match_tier` and the receipt the ack printed. Tally the
  night's phrase where it belongs with `add "<phrase>" --into <right id>` (or
  `--new`). The mis-landed occurrence **stays** in the wrong entry's history,
  visibly attributed to its tier and score, and still counts there — the tally
  is inventory, not a ledger of debts, and an honest +1 of residue is preferred
  over rewriting history. If that stray count matters, restore the `lucid
  backup` taken before it.
* **A wrong wording in `aka[]`.** An automatic bump adds the new wording to the
  matched entry's `aka[]`, where tier 2 will keep matching it. Re-tally that
  wording with `--into` the right entry once — it then scores 1.0 on both
  entries, which is a tie, so the next time it comes up it is a **suggestion**
  rather than an automatic bump, and your answer decides.
* **A wrong `reconcile` fold.** A fold is a merge — the absorbed entry becomes
  a tombstone and there is no unmerge verb. The fold's receipt names its
  `merge` event on the target, and the source's `redirect_to` names where it
  went, so a wrong fold is always identifiable. Take a `lucid backup` before
  `reconcile --apply` (the dry run and the report both say so); to undo a wrong
  fold, restore that pre-reconcile snapshot with `lucid restore --in <file>
  --force` — the same escape hatch §5 prescribes for a partial `import`. A
  restore rewinds every write since the backup, so take it right before the
  apply.

### 7.9 Reconcile — folding duplicates that already exist

**`lucid gratitude reconcile [--apply] [--json]`** scans the live tally for
likely duplicates — pairs of entries that name the same thing — and **proposes**
folds. It is **dry-run by default**: it prints each proposal and writes
nothing.

* **Tier 2 pairs.** Every live entry is scored against every other with the
  tier-2 scorer (the best pair of wordings, one from each entry's
  `display_name` and `aka[]`, scored by Dice) and banded with the tier-2
  cutoffs by the §7.2 rule read from **both** sides: a pair is **High** only
  when each entry is the other's top match, clearing `tier2_high` and beating
  that entry's runner-up by `tier2_margin` — clearly each other's best match.
  A High pair is a proposal that `--apply` will fold, marked **fold**. A pair
  at or above `ambiguous_floor` that is not High is **Ambiguous**, marked
  **look** — *worth a look* — with the exact `lucid gratitude merge` command,
  and is never applied. A pair below the floor is not proposed.
* **Tier 3 pairs (dry run only, when available).** One `gratitude.reconcile`
  call sends only the live entries' wordings, numbered (the §7.5 slice rules
  and off-limits exclusion apply; an entry's tier-2 evidence for the cap is its
  best pair score), and asks for pairs that name the same thing. The reply is
  `{"pairs": [{"a": <position>, "b": <position>, "score": <0–1>}]}` alone (or
  inside one enclosing code fence); a reply that does not parse, names an
  unknown position, pairs an item with itself, repeats a pair, omits a position
  or score, or scores outside [0, 1] is treated as **unavailable** — never
  partially trusted. The pairs are banded with the tier-3 cutoffs by the same
  both-sides rule. Tier-3 pairs are **always advisory** — marked **look** and
  listed with their `merge` command, never applied, because a model's answer
  can differ between the dry run you read and the run that applies. When tier 3
  is disabled or unavailable, reconcile says so and proposes tier-2 pairs only.
* **Direction and overlap.** In each pair, the entry with the lower count folds
  into the higher; a tie folds the later-created into the earlier, then the
  greater key into the lesser. Each entry appears in at most one proposal per
  run, so an applied set never chains through a fresh tombstone and every
  printed `merge` command still runs after the others; re-run to continue. The
  **fold** pairs are placed first, so no advisory pair can crowd one out; the
  advisory pairs then take the entries left, highest score first (tier 2 before
  tier 3 on a tie).
* **`--apply`** is the explicit confirmation: it folds exactly the tier-2 High
  proposals the dry run lists — deterministic, so the same store yields the same
  set — each through the ordinary `merge` path (§4), returning one receipt per
  fold. It never applies an Ambiguous or tier-3 proposal, and it never consults
  the model at all (its report lists the tier-2 proposals only). If a fold
  cannot land, the apply stops with a clean error naming how many folds landed
  before it, each with its receipt. Take a `lucid backup` first (§7.8).
* **`--json`** emits `{proposals: [{source, source_thing, target,
  target_thing, score, match_tier, band, will_apply, command}], tier3,
  applied: [{receipt, source, target}]}` — `proposals` and `applied` are arrays,
  never null; `applied` is `[]` on a dry run. `band` is `high` or `ambiguous`;
  `tier3` is `used`, `disabled`, or `unavailable`, and is omitted when tier 3
  was not consulted (an `--apply`, or fewer than two live entries).

## 8. Outward expression — linking a person and saying thank you

Some gratitude involves a particular person. This section gives that a light,
optional path: link the entry to someone in the people registry, get a quiet
reminder that you might tell them, and keep a private note once you have. Lucid
**never sends anything** — it prompts and records; you do the telling, by
whatever means you choose.

**`lucid gratitude thank <id> --person <subject> [--day <date>]`** is the home
for both the person link and the record of having expressed it:

* `<id>` is a live entry's stable id (a tombstone or unknown id is a clean
  error that writes nothing, like `--into`).
* `--person <subject>` (required, one per call) is resolved exactly as the
  person write verbs resolve a subject
  ([`usage/commands.md`](usage/commands.md#resolving-a-subject)): an exact
  `person_key` wins, otherwise the name is matched against live people
  `display_name`/`aka[]`, redirect tombstones followed to the canonical record.
  No match, or more than one, is a clean error naming the remedy or the
  candidate keys — **validated before anything is written**; the verb never
  guesses.
* On success it adds the canonical person key to the entry's `people[]` (if not
  already there) and appends one **`expressed`** event (`person`, logical
  `date`), returning its receipt. `--day` is the shared strict-tier backdating
  grammar (§3) for "I told them yesterday"; the event's `at` is the real write
  time.
* It is **tally-neutral**: Count, First, and Last are unchanged (§2).

**`lucid gratitude add "<phrase>" --person <subject>`** is the link-only
convenience (§3): the occurrence lands wherever matching decides, the person is
linked onto that entry, and no `expressed` event is written.

**Off-limits people.** Linking and recording are allowed for a person marked
off-limits (`lucid person off-limits`): the link and the note are your own
private record, and off-limits means *off-limits to inference*, not erased —
consistent with `lucid person <name>` still rendering the raw record. So the
link shows on the entry in `list` (flagged `off_limits`), but a reminder
**never** names an off-limits person, and an entry linked to one is withheld
from the tier-3 judge (§7.5).

**The reminder — gentle and optional.** `list` (and `list --json`'s
`reminders` array) may offer a few quiet lines:

```
You might tell:
  Sam Rivera — "coffee with Sam on the porch" (gratitude_a-river)
```

* A linked, not-off-limits person is offered for an entry when you have never
  recorded telling them, or when the entry has been tallied again since you
  last did.
* At most **three** lines, most recently tallied entries first — a fixed,
  quiet ceiling. There is **no** count of how many others there are, no
  completion percentage, no streak, no quota, no "unthanked" backlog, no
  overdue language, and the wording never escalates however long a line has
  been offered. Tests assert that none of those render.
* It surfaces **only** alongside `list`. It is never pushed, never scheduled,
  and never part of the check-in or reflection flow.
* `--json` shape: `reminders: [{person_key, display_name, entry_id, thing}]`.

Linked people are resolved forward through person redirects at read time, so a
later `person merge` never strands a link.

## 9. Defaults

Logical-day rollover 04:00 (shared with observations, reframes, and the Engine)
· matching runs on a plain nightly `add` and in `reconcile`; tier 1 = canonical
key (a tombstone key forwards one hop), tier 2 = stopword-and-stem token sets
scored by Dice over `display_name` + `aka[]`, live entries only, no edit
distance; tier 3 = the optional by-meaning judge over the live list, run only
when tier 2 is Ambiguous or Low · `gratitude.match` knobs (all configurable,
out-of-range clipped to the default with a warning): `tier2_high` **0.85**,
`tier2_margin` **0.15**, `ambiguous_floor` **0.50** (shared), `tier3_high`
**0.90**, `tier3_margin` **0.20**, `tier3_enabled` **false** (tier 3 is
opt-in), `tier3_backend` **`claude_cli`** and `tier3_model` **`sonnet`** (the
configuration that cleared the trust gate,
[ADR-0012](adr/0012-gratitude-semantic-matching.md) §6; `ollama` keeps the judge
local; empty inherits `provider.backend` / `provider.model`; an unrecognized
backend is coerced to `ollama`), `tier3_timeout_seconds`
**30**, `tier3_max_candidates` **200** · ambiguous band: interactive asks,
non-interactive / `--json` refuses-and-defers (writes nothing, exit 1, resolve
with `--into` or `--new`), at most three candidates shown · `--into` targets a
stable id regardless of wording; `merge` folds + redirects (single-hop, no
cycles) · `reconcile` dry-run by default; `--apply` folds tier-2 High pairs
only and never consults the model · `import` / `add --count` writes one `seed`
event with explicit Count/First/Last, canonical key only, fabricates no dates;
it is **not** idempotent · `add --day` / `thank --day` strict tier, future dates rejected,
real write time kept as the event's `at` · `list` sorted by count then recency,
shows the stable id, linked people, and last-expressed dates; at most three
reminder lines, never a count · `schema` = 2 (schema 1 read unchanged) ·
occurrence `source` default `gratitude` (`migration` for a seed). All
timestamps are ISO-8601 with the host's local-TZ offset, the same rule as every
record under `~/.lucid/` ([`mvp/data-model.md`](mvp/data-model.md) §"Time zone
rule").

## 10. Boundaries

* **Sanctuary and inventory, not obligation** (§0). No quota, no streak, no "you
  weren't grateful today," and no score for telling people. The tally counts;
  it never grades.
* **The raw `#gratitude` capture is untouched.** The verbatim nightly gratitude
  is an ordinary `lucid log` entry and remains the source of truth; the tally is
  a derived count *over* that practice, added beside it, never in place of it.
* **`internal/storage` is the sole `~/.lucid/` writer** (architecture P3). The
  record family, the count fold, and match tiers 1–2 are agent-free and carry no
  LLM (P9); `internal/observations` holds the pure tier-2 scorer and imports no
  provider. Tier 3 reaches a model only through `internal/provider`, is
  optional, and degrades to tiers 1–2 (§7.6).
* **Minimal egress.** The tier-3 judge sees the new phrase and the candidate
  wordings — nothing else — and nothing leaves the machine unless you enable
  tier 3 (§7.5).
* **No sends.** Outward expression prompts and records; Lucid never messages a
  person on your behalf.
* **Append-only** (§2). History is never rewritten; a duplicate is folded and
  redirected (§4), never deleted; a wrong automatic match is corrected by a new
  event (§7.8). The store is built to outlive its tools (P6): plain user-owned
  JSON, exportable as a directory of files.
* **Public-safe** ([`CLAUDE.md`](../CLAUDE.md) invariants). Every example in
  this document and in the tests is synthetic; real gratitude entries and
  people live only in the private Ledger under `~/.lucid/`, never in the repo.
