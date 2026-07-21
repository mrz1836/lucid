# Lucid MVP — Life-Archive Module (memory excavation)

This page specifies the **memory-excavation surface** the observation
foundation in [`../observations.md`](../observations.md) was frozen to
carry. [`../observations.md`](../observations.md) §8 names "era-guided
excavation sessions ('walk me through that kitchen')" as a *future
Mirror surface* that "needs no new foundation — the envelope above
already carries everything they will produce"; the observations module
([`observations-module.md`](observations-module.md)) likewise defers
"excavation sessions or thread views" with the note that "the events
and registries they need exist now." This module turns those deferred
surfaces on.

It builds two things the foundation left as gaps: the **user-facing
write verbs** for the `injury`, `era`, and `thread` registries and for
story `memory` events (today only a sticky location auto-creates a
registry — there is no `injury`/`era`/`thread` write command), and the
**read surfaces** that select the next cluster to excavate and browse
what has been captured. Like the Engine and observation modules, the
whole surface is **deterministic and agent-free** — parsers,
registry merges, arithmetic, and template output; **no model runs in
any capture, selection, or browse path** (architecture P9). The one
place a model is allowed is the *personal* review conversation an
operator's own harness drives on top of these verbs — outside this
repo, outside the binary.

Precedence follows the same rule as
[`observations-module.md`](observations-module.md): this page and
[`scope.md`](scope.md) own *scope*; the older MVP docs own
*conventions*. Where this page fixes a field vocabulary, it fixes it on
the **frozen envelope** — no new top-level field, no new subsystem.

## 0. The governing corollary (inherited)

**Memory is inventory, never obligation** — the same rule the
observation layer opens with ([`../observations.md`](../observations.md)
§0). An injury record, a story, an era, or a thread carries no streak,
quota, target, score, or escalation. The archive takes testimony about
a body and a life; it never grades either. A month with no excavation
is a month with no excavation. The excavation ritual **pauses, it never
nags** — an unanswered prompt is a complete answer, and the surface
never initiates a second contact on any content threshold
([`../observations.md`](../observations.md) §9, "sustained-distress
silence is a decision").

## 1. Two tracks

The archive excavates two long-lived kinds of history, kept as separate
tracks so a session opens exactly one and never blurs them:

| Track | Registry / kind | What it captures |
|-------|-----------------|------------------|
| **Injury / body history** | `injury` registry | The injury inventory a doctor never has time to take: onset, cause, severity, lasting effects, current limitations, treatments tried, and how sure the record is. |
| **Fun / wild story history** | `memory` events attached to `era` (and optionally `place`/`person`/`thread`) | The stories a life accumulates and loses: where and roughly when, who was there, how it felt, why it still matters, and what to ask next. |

Both ride the frozen bitemporal envelope
([`../observations.md`](../observations.md) §2): `occurred_at` can be
any time in the past with `occurred_at_precision`
(`exact`/`approximate`/`range`), and a `certainty` field records how the
memory was recalled. An injury onset backfills to when it happened; a
story about a decade ago files under that decade's `logical_date`,
never overwriting a current-day log.

## 2. The injury `Fields` convention

The `injury` registry (`internal/observations/registry.go`,
`RegistryInjury`) already exists: a display name, an append-only
`status_history` (`active` → `managed` → `resolved`, recorded, never
overwritten), and a free-form `Fields map[string]any`. Every field the
Success Criteria list fits in `Fields` **with no schema change** — the
registry envelope is frozen exactly like the event envelope. This
section fixes the key vocabulary so capture stays consistent and the
projection (§6) and browse (§7) surfaces read a stable contract.

| `Fields` key | Type | Meaning | Synthetic example |
|--------------|------|---------|-------------------|
| `onset` | string (bitemporal) | When the injury began, backdate-aware (`@yesterday`, `YYYY-MM-DD`, or an approximate year). Precision is recorded alongside. | `"2014-09"` (approximate) |
| `timeline` | string | The free-text arc since onset — flares, surgeries, plateaus, the "what happened over the years" line. | `"sprained on a trail run; re-tweaked twice; mostly quiet since 2019"` |
| `body_area` | string | The region, in the owner's words — matched to nothing, guessed at by nothing. | `"left knee"` |
| `cause` | string | How it happened, as testimony. | `"landed wrong off a boulder"` |
| `severity` | string | Felt severity in the owner's framing (free text, not a clinical scale — the daily body-signal scales belong to the observation kinds, not the registry). | `"bad at first, moderate now"` |
| `lasting_effects` | string | What it left behind. | `"clicks on deep bends"` |
| `current_limitations` | string | What it still stops or shapes today — the line a body-guidance consumer reads. | `"no deep squats under load"` |
| `treatments` | string | What has been tried — PT, injections, rest, rehab. | `"6 weeks PT in 2015; daily mobility since"` |
| `uncertainty` | string | What the record is *not* sure of — an honesty field, so future reads can weight it. | `"not sure if the 2017 flare was the same knee"` |

Every key is optional; a bare `lucid injury "left knee"` is a valid
first mention. `status` transitions ride the existing
`active`/`managed`/`resolved` history. None of these keys is a score,
target, or streak (§0).

## 3. The story `memory` convention

A story is one `memory` observation (`internal/observations/parse.go`,
`KindMemory`) written at a **backdated** `occurred_at` with
`source: excavation` (`SourceExcavation`, already defined in the
envelope). The kind's payload already carries `text` and `certainty`;
this convention adds three payload keys and fixes the `refs` shape —
all **additive on the frozen envelope** (`schema` stays `1`, no new
top-level field; new needs go in `payload`/`refs`, per
[`../observations.md`](../observations.md) §2).

**Payload (schema 1):**

| Payload key | Type | Meaning |
|-------------|------|---------|
| `text` | string | The story in the owner's words, verbatim. |
| `certainty` | `vivid` \| `hazy` \| `reconstructed` | How it was recalled — the honesty field ([`../observations.md`](../observations.md) §8). |
| `tone` | string (optional) | The emotional colour, one phrase. |
| `why_it_matters` | string (optional) | Why it was worth keeping. |
| `follow_up` | string (optional) | The thread to pull next time — what the review should ask about later. |
| `people` | string[] (optional) | Names as testimony; resolved to `refs.person` when a `person` key resolves (§ [`data-model.md`](data-model.md) person derivation), else kept as a plain list. |
| `note` | string (optional) | The grammar's trailing-text home, verbatim. |

**`refs`** (the frozen relational contract,
[`../observations.md`](../observations.md) §2 — a flat object of
registry-kind or reserved keys):

| `refs` key | Points at | Notes |
|------------|-----------|-------|
| `era` | an `era` registry key | The life chapter the story sits in. |
| `place` | a `place` registry key | Resolved exactly as `context.location` resolves a place. |
| `person` | one or more `person` keys | Optional, when a name resolves. |
| `entry` | a `raw_` entry id | The **media link**: when a photo is attached via `lucid attach`, the returned raw entry id is referenced here. Optional — a text-only story omits it and is never gated (§5). |

Media reuses `lucid attach` unchanged
([`data-model.md`](data-model.md) §"Media attachments"): the verb
copies the bytes, hashes them, writes the sidecar, and emits the
immutable `raw/` entry whose id the `memory` references via `refs.entry`.
No new media build.

## 4. The `era` and `thread` registry conventions

Both are existing registries (`RegistryEra`, `RegistryThread`) written
through the same append-only merge path as `injury`.

* **`era`** — a named life chapter with a date range. `Fields`:
  `start` and `end` (either may be approximate; a range with an open
  `end` is a still-running chapter). Stories attach to an era via
  `refs.era`, so the past becomes browsable by chapter rather than by a
  date no one remembers.
* **`thread`** — a named thing being worked on, inner or outer.
  `Fields`: `intent` (the one-line statement of what it is) and
  optional `domains`. **The obliquity guard is structural
  ([`../observations.md`](../observations.md) §8): a thread has no
  progress number, no percent, no streak.** A thread's "progress" is
  the narrative its linked events tell. The write verb rejects (omits)
  any progress/percent/streak field so the guard cannot be bypassed by
  a stray `--field`.

## 5. The excavation ritual (selection + prompts; the conversation lives elsewhere)

The review is gentle, opt-in, and opens **one cluster at a time**. The
binary owns only the **deterministic** half of it, split cleanly from
the conversation:

* **In the binary (read-only, agent-free):** a selection engine picks
  the next cluster to excavate and emits **generic** prompt templates
  for it (§6, the cluster-selection contract). It holds no session
  state and speaks to no one.
* **Outside the binary (the personal driver):** an operator's own
  harness reads the selection, holds the gentle one-cluster-at-a-time
  conversation on whatever surface they choose, and calls the write
  verbs (§2–§4) to persist the answers. This mirrors the split the
  weekly reflection deep-dive already uses
  ([`../usage/weekly-reflection.md`](../usage/weekly-reflection.md)):
  the binary does the read-only analysis; the harness drives the
  sit-down. **No conversational loop, no scheduler change, and no model
  call enters the binary** — the monthly cadence and the live
  conversation are the harness's, exactly as the daily companion's fire
  times are the scheduler's, not a new binary feature
  ([`agent-contracts.md`](agent-contracts.md), cross-cutting rules; the
  pre-committed-template send ceiling is untouched).

The ritual is a *review*, so its prompts are never a standalone ping
and its silence is never recorded (§0). A cluster the operator does not
answer simply stays thin and is offered again a later month.

## 6. The read contracts (selection + injury projection)

Both contracts are **deterministic, read-only, and sanctuary-safe**:
they read `injury`/`era`/`thread` registries and `memory` events **only
through router projection seams** — never the raw `engine/`,
`observations/`, or `registries/` trees (the same bidirectional
sanctuary boundary the week bundle reads through,
[`agent-contracts.md`](agent-contracts.md); enforced by the path-prefix
denylist, fail closed). They write nothing.

### Cluster-selection contract

**Input:** the enabled `injury` and `era` registries, the `memory`
events, and the current time. **Output:** the next `Cluster` —
`{track: "injury" | "story", key, display_name, reason, gaps[]}` — plus
the generic prompt templates for it.

Selection is a pure function with a stable tiebreak:

* **Injury track:** prefer an injury whose convention `Fields` (§2) are
  thinnest — the record with the most missing keys, tiebroken by key so
  the choice is deterministic.
* **Story track:** prefer an `era` with few or no linked `memory`
  events (or the oldest un-excavated era).

`gaps[]` names which convention fields are missing, and the prompt
templates are keyed by `(track, gap)`. The templates are **generic** —
personal specifics come from the `Cluster` data, never from hard-coded
copy in the repo (synthetic examples only,
[`product-principles.md`](product-principles.md) §9). An empty or thin
store degrades to an honest empty result; there is no model to spend
over an empty store.

### Injury-context projection contract

A read-only projection extends the clinician packet's existing
`activeInjuryLines()` (`internal/storage/exports.go`) into a
**structured** seam a body-guidance consumer reads:
`InjuryContext{Key, DisplayName, Status, BodyArea, CurrentLimitations,
Timeline, Severity}`, one per active/managed injury (resolved excluded),
byte-stable in sort order. It shares the exact `active`/`managed` filter
`activeInjuryLines` uses (one helper, two renders) so the packet and the
projection can never diverge. It **renders registry facts only — never a
diagnosis or treatment recommendation** ([`../observations.md`](../observations.md)
§9, "never diagnosis, never treatment advice"), and it writes nothing.
This is the stable seam a workout/body-guidance consumer reads instead
of touching raw registry state (the sanctuary rule: consumers read
projections, not state).

## 7. The recall / browse contract

A read-only browse over the archive **by era / thread / injury**, each
surfaced item carrying its **source context**:
`RecallRequest{dimension: "era" | "thread" | "injury" | "", key}` →
items that each carry `SupportingEntryIDs` (the raw/observation ids
behind them) and `source` provenance. A bare request (no dimension)
returns an index of eras, threads, and injuries. Like §6 it reads only
through router projection seams and writes nothing, and it degrades to
an honest empty result on a thin or missing store. **No surfaced item is
uncited** — the source-context ids are the browse's contract, so the
same projection-only reads are consumable by the weekly reflection
surface without a second data path.

## 8. Data-contract decision — no net-new observation kind in v1

This module was scoped to *default to the frozen model and reach past it
only when the model genuinely cannot hold a shape.* The decision, for
the record:

**v1 introduces no net-new observation kind.** The frozen `injury`
registry (`Fields` free-form), the `memory` payload/`refs`, and the
`era`/`thread` registries express **every** field the Success Criteria
list, with the envelope frozen (`schema: 1`, no new top-level field):

* injury timeline/body_area/cause/severity/lasting_effects/
  current_limitations/treatments/uncertainty → `injury.Fields` (§2);
* story era/date/people/place/tone/why_it_matters/follow_up →
  `memory` payload + `refs` (§3);
* temporal placement + provenance → the bitemporal envelope
  (`occurred_at`/precision/`certainty`/`source: excavation`), already
  built and tested;
* media → `lucid attach` + `refs.entry`, already built.

Adding a kind would therefore buy nothing but drift.

### The escape hatch (the criteria for a future net-new kind)

A future need may genuinely exceed the frozen model. When — and only
when — that is true, a net-new kind is added under these conditions,
**with a written justification recorded here or in an ADR** before any
code:

1. **The need cannot be expressed** as an `injury.Fields` key, a
   `memory` payload key, a `refs` link, a `tag`, or a new registry
   `Fields` key on an existing registry — the free-form maps are the
   first resort, and a documented convention key is nearly always
   enough.
2. **The need is a distinct kind of event**, not a field on an existing
   one — it has its own scale/convention and its own capture verb, the
   way `pain` and `memory` are distinct.
3. **It follows the `parse.go` kind checklist** exactly as
   [`../observations.md`](../observations.md) §3 states it: "Adding a
   kind is a one-row diff to [the kinds] table plus a payload schema —
   no envelope change, no new subsystem." The row lands in
   [`../observations.md`](../observations.md) §3, the parser gains one
   `parseX` function, and the kind is enable-gated off by default.
4. **Projections tolerate it**: unknown kinds and higher schema
   versions are read-what-you-understand, skip-what-you-don't
   ([`../observations.md`](../observations.md) §2), so an older reader
   never breaks on a newer kind.

The bias is explicit: reach for a documented `Fields`/payload key first,
a new registry `Fields` key second, and a net-new kind only when a real
need survives all four tests with its justification written down. v1
needs none.

## 9. Boundaries (inherited, restated)

* **Never diagnosis, never treatment advice.** The injury projection
  and every surface here render the owner's record as data for the
  owner and their care team — the clinical-language gate applies
  ([`../observations.md`](../observations.md) §9).
* **Sanctuary and inventory are structural.** Registries and
  observations stay sanctuary trees; the selection, projection, and
  browse surfaces read them only through router projection seams; no
  captured field is a score or obligation (§0). The off-limits registry
  ([`../architecture.md`](../architecture.md) §5) still excludes any
  named injury, era, thread, place, or person from inference.
* **Public-safe.** This module ships **synthetic examples only**; the
  binary carries no personal injuries, stories, eras, or threads, and
  no personal review conversation. The live archive and the personal
  driver live in the owner's own runtime, never in this repo
  ([`product-principles.md`](product-principles.md) §3, §9).

## 10. Defaults

Injury `Fields` keys per §2 · story `memory` payload/`refs` per §3 ·
`era` = name + date range · `thread` = name + intent, **no progress
number** · selection prefers the thinnest injury / least-linked era,
deterministic tiebreak · one cluster per review session · unanswered →
pause, never nag · `excavate` and `recall` read-only and model-free ·
injury projection = active/managed only, no diagnosis · **no net-new
observation kind in v1** (§8). All instance-overridable with reasons
(architecture P8).
