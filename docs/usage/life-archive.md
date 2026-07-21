# The life archive — excavating injuries and stories

Lucid's day-to-day capture is about *now*. The **life archive** is about
*then* — the long-lived history a life accumulates and slowly loses. It has two
tracks, kept separate so a session opens exactly one and never blurs them:

- **Injury / body history** — the inventory a doctor never has time to take:
  when an injury began, how it happened, what it left behind, what it still
  limits, and how sure the record is.
- **Fun / wild story history** — the stories worth keeping: roughly when and
  where they happened, who was there, how they felt, why they still matter, and
  what to ask about next time.

Everything here rides the **frozen bitemporal envelope**: a record's
`occurred_at` can be any time in the past (with a recorded precision), so an old
injury's onset backfills to when it happened and a story about a decade ago
files under *that* decade — never overwriting a current-day log. The exact
field vocabulary each track uses is the **field convention** in
[`../mvp/life-archive.md`](../mvp/life-archive.md); this page is the run-it
guide for the six commands that read and write it.

The archive is **inventory, never obligation**: nothing here carries a score, a
streak, or a target, and an unanswered prompt is never a debt (see
[`../mvp/product-principles.md`](../mvp/product-principles.md) §0).

## The six verbs at a glance

| Verb | Kind | What it does |
|------|------|--------------|
| [`lucid injury`](#lucid-injury) | write | Record or amend an injury in your body history. |
| [`lucid era`](#lucid-era) | write | Record or amend a life chapter (an era). |
| [`lucid thread`](#lucid-thread) | write | Record or amend a thread you're working on. |
| [`lucid memory`](#lucid-memory) | write | Record a story from your past — backdated, linked, optionally with a photo. |
| [`lucid excavate`](#lucid-excavate) | read-only | Select the next memory cluster to excavate and emit its prompts. |
| [`lucid recall`](#lucid-recall) | read-only | Browse the archive by era, thread, or injury, with source context. |

All six are **deterministic and agent-free** — no model runs in any of them. The
read-only pair (`excavate`, `recall`) writes nothing; the write verbs
acknowledge only *after* the record lands and say what was written. Each is also
in the full [command reference](commands.md).

> **The excavation ritual lives in a harness, not the binary.** `lucid excavate`
> only does the deterministic half — pick the next cluster, emit generic prompts.
> The gentle, one-cluster-at-a-time *conversation* (and any monthly cadence) is
> driven by a chat harness that reads `lucid excavate --json`, holds the sit-down
> on its own surface, and calls the write verbs to persist your answers. This
> mirrors the split the [weekly reflection](weekly-reflection.md) already uses:
> the binary does the read-only analysis; the harness drives the conversation. No
> conversational loop, no scheduler, and no model call enters the binary.

## Enabling the story (`memory`) kind

Injuries, eras, and threads are registries and work out of the box. Stories are
`memory` **observations**, and — like every observation kind — the `memory` kind
ships **disabled**. A `lucid memory` capture against a disabled kind writes
nothing and prints an enable hint rather than failing silently:

```
`memory` isn't enabled — add it to observations/config.json
```

Enable it once by adding `memory` to the `kinds_enabled` array in
`~/.lucid/observations/config.json` (see
[`../observations.md`](../observations.md) §10 for the kind-enablement model):

```jsonc
{
  "kinds_enabled": ["memory"]
}
```

`lucid injury`, `lucid era`, `lucid thread`, `lucid excavate`, and `lucid recall`
need no enablement.

## Backdating — placing a record in the past

Injury onsets, era bounds, and stories all accept the same backdating grammar,
so the archive can hold history without pretending it happened today:

| Form | Meaning |
|------|---------|
| *(omitted)* | Files under the current logical day. |
| `@yesterday` | The previous logical day. |
| `YYYY-MM-DD` | An exact past day. |
| `YYYY-MM` or `YYYY` | An **approximate** date — the precision is recorded alongside, so a later read can weight how sure the placement is. |

A story dated to an approximate year files under that year's logical day and
never touches a current-day log.

## `lucid injury`

```
lucid injury <name> [--status active|managed|resolved] [--onset @yesterday|YYYY-MM-DD]
             [--body-area <text>] [--cause <text>] [--severity <text>]
             [--lasting-effects <text>] [--current-limitations <text>]
             [--treatments <text>] [--uncertainty <text>] [--note <text>] [--json]
```

Record or amend an **injury** in the `injury` registry. The first mention of a
name creates the record; a later call with the same name amends the same record,
merging the fields you supply and appending any status transition to the record's
append-only `status_history` (an injury's history is recorded, never
overwritten). Every flag is optional — a bare `lucid injury "left knee"` is a
valid first mention that you flesh out over later sessions.

The flags map one-to-one onto the injury field convention
([`../mvp/life-archive.md`](../mvp/life-archive.md) §2). `--onset` is
backdate-aware and records its precision; `--severity` is felt severity *in your
own words*, not a clinical scale. None of these is a score or a target.

`--json` emits `{kind, key, display_name, status, created, fields}` — the
resolved (salted) key, whether this call created the record, and the merged
convention fields.

```sh
# First mention — thin is fine.
lucid injury "left knee"

# Flesh it out later; the status transition is recorded, the fields merge.
lucid injury "left knee" --status managed \
  --onset 2014-09 --body-area "left knee, medial" \
  --cause "landed wrong off a ledge" --current-limitations "no deep squats" \
  --treatments "6 weeks PT, daily mobility since" \
  --uncertainty "not sure it was the same knee in 2017"

lucid injury "old ankle" --onset 2011 --json
```

## `lucid era`

```
lucid era <name> [--start @yesterday|YYYY-MM-DD] [--end @yesterday|YYYY-MM-DD] [--note <text>] [--json]
```

Record or amend a **life chapter** in the `era` registry — a named span of time
you can hang stories on. Either bound may be approximate; omit `--end` for a
still-running chapter. Stories attach to an era via their `--era` reference, so
the past becomes browsable by chapter rather than by a date no one remembers.
Same create-then-amend and append-only merge as `lucid injury`.

`--json` emits the same `{kind, key, display_name, status, created, fields}`
shape.

```sh
lucid era "the coast years" --start 2010 --end 2014
lucid era "wild summer" --start 2010-06-01 --json
```

## `lucid thread`

```
lucid thread <name> [--intent <text>] [--domain <text>]... [--status active|managed|resolved] [--note <text>] [--json]
```

Record or amend a **thread** in the `thread` registry — a named thing you're
working on, inner or outer. `--intent` is the one-line statement of what it is;
`--domain` names a domain it touches and may be repeated.

A thread deliberately has **no progress number, no percent, and no streak** — the
obliquity guard is structural. A thread's "progress" is the narrative its linked
events tell, not a metric; there is no flag to set one, and the write path omits
any that slip through. Same create-then-amend and append-only merge as the other
registry verbs.

```sh
lucid thread "learning to sail" --intent "get comfortable single-handing" --domain skill --domain outdoors
lucid thread "the memoir" --intent "write the messy years down" --status active --json
```

## `lucid memory`

```
lucid memory <text> [--certainty vivid|hazy|reconstructed] [--era <key>] [--place <name>]
             [--people <name>,<name>]... [--tone <text>] [--why <text>] [--followup <text>]
             [--day @yesterday|YYYY-MM-DD] [--attach <path> [--caption <text>]] [--json]
```

Record a **story** from your past as one `memory` observation, written at a
backdated `occurred_at` and linked to the era, place, and people it belongs to.
Requires the `memory` kind to be [enabled](#enabling-the-story-memory-kind); a
disabled kind prints the enable hint and writes nothing (exit `0`, nothing lost).

The flags follow the story field convention
([`../mvp/life-archive.md`](../mvp/life-archive.md) §3): `--certainty` is the
honesty field (how you recall it); `--era`/`--place`/`--people` become the
story's relational `refs`; `--tone`, `--why`, and `--followup` carry the
emotional colour, why it matters, and the thread to pull next time. `--day` is
the backdating grammar above — text is captured whether or not you place it in
time.

**Optional media, never a gate.** When `--attach <path>` is present, the story
reuses [`lucid attach`](commands.md#attach) unchanged — it copies the bytes,
hashes them, and emits an immutable raw entry — then links that entry from the
story's `refs.entry`. A **text-only story simply omits it and is never blocked**
by the lack of a photo.

`--json` emits `{event_id, logical_date, partial, rejected, refs}` — the appended
event id, the day it filed under, and the resolved refs (with `entry` present
when a photo was attached).

```sh
# Text-only story, placed roughly in time.
lucid memory "the night we drove to the coast" \
  --era wild-summer --certainty vivid --tone "reckless and free" \
  --why "first time I felt like an adult" --day 2010-07

# Same story with a photo — attached and linked, not required.
lucid memory "the pier at 2am" --era wild-summer \
  --attach ~/Pictures/pier.jpg --caption "the old boardwalk" --json
```

## `lucid excavate`

```
lucid excavate [--json]
```

**Read-only.** Select the next memory cluster to excavate and emit its generic
prompt templates. Nothing under `~/.lucid/` changes and no model runs — this is
the deterministic half of the excavation ritual (§5 of the field convention); a
harness reads it and drives the conversation.

Selection is a pure function with a stable tiebreak, over two separate tracks:

- **Injury track** — prefer the *thinnest* injury (the most missing convention
  fields), so the record most worth fleshing out comes up first.
- **Story track** — prefer the era with the fewest linked stories (or the oldest
  un-excavated chapter), so a chapter with room for more comes up.

`gaps` names which convention fields are missing, and `prompts` are the generic
questions to ask about them (your specifics come from the data, never from
hard-coded copy). An empty or fully-excavated store degrades to an honest empty
result — the calm fallback line, no model spent over it.

`--json` emits `{found, track, key, display_name, reason, gaps, prompts}`; the
human form prints the cluster and its prompts as bullets (no tables — the output
is chat-friendly).

```sh
lucid excavate
lucid excavate --json
```

## `lucid recall`

```
lucid recall [--era <key> | --thread <key> | --injury <key>] [--json]
```

**Read-only.** Browse the archive. With one of the mutually-exclusive dimension
flags it opens that referent and the stories filed under it; with no flag it
prints the **archive index** over every era, thread, and injury. Nothing is
written and no model runs, mirroring `lucid excavate`.

Every surfaced item carries its **source context** — the supporting raw /
observation ids behind it and its provenance — so nothing in a recall is uncited:
a story cites its own observation id, an injury or era referent cites its registry
record. The same projection-only reads back this archive, so the
[weekly reflection](weekly-reflection.md) can consume it without a second data
path.

`--json` emits `{dimension, key, found, referent, items}`, each item carrying
`{kind, key, title, detail, source, supporting_entry_ids}`; the human form prints
bullets with a `Cites:` line per item (no tables). A dimension key that does not
resolve, and an empty archive, each print an honest fallback rather than erroring.

```sh
lucid recall                          # the whole index
lucid recall --era wild-summer        # a chapter and its stories
lucid recall --injury left-knee --json
```

## Where the pieces live

- **Field convention** (the exact keys and shapes each track writes):
  [`../mvp/life-archive.md`](../mvp/life-archive.md).
- **The frozen bitemporal envelope** (how a past record is placed and never
  overwrites today): [`../observations.md`](../observations.md) §2, §8.
- **Media attachments** (`lucid attach`, reused by `lucid memory`):
  [`../mvp/data-model.md`](../mvp/data-model.md) §"Media attachments".
- **Every command, in one place**: [`commands.md`](commands.md).

The archive reads and writes only your own local Ledger under `~/.lucid/`;
nothing here syncs anywhere.
