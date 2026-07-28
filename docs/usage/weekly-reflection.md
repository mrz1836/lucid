# The weekly reflection (`lucid reflect week`)

`lucid reflect week` is the **read-only weekly deep-dive**: once a week it reads
the projections for **everything since your last reflection**, frames a calm,
hypothesis-first reflection through your active interpretive lens, and surfaces
at most one tentative pattern for you to accept, refine, or set aside. It is a
mirror for the whole stretch — a skipped week is caught up on the next run rather
than dropped — distinct from the daily rhythm and from `lucid reflect`, which
only recalls insights you have already validated.

This page covers what the deep-dive reads, the window it resolves, the
frameworks/lens layer it can be framed through, the three commands
(`reflect week`, `reflect week apply`, and `reflect week close`), and how a
pattern you confirm becomes a tracked insight through the same resonance gate
every proposal passes.

> Read [`commands.md`](commands.md) for the full CLI reference, [`companion.md`](companion.md)
> for the daily model-composed messages, and [`../frameworks.md`](../frameworks.md)
> for the design of the interpretation layer.

## What it is

- **Read-only.** The deep-dive **creates no record** — no insight, no reflection
  record, no raw entry. It resolves its window, reads it, composes the
  reflection, and returns it. Persisting a pattern is a separate, explicit step
  ([`reflect week apply`](#applying-a-pattern-reflect-week-apply)), and marking
  the stretch reflected is another
  ([`reflect week close`](#closing-a-reflection)).
- **Projection-only.** It never reaches the sanctuary trees
  (`engine` / `observations` / `registries`) itself. Every number and signal
  comes through the same read-only projections the CLI and the daily companion
  already expose (see [The week bundle](#the-week-bundle)); the one piece of
  state it consults — the [reflected-through cursor](#closing-a-reflection) —
  is read through the storage adapter like any other record.
- **Hypothesis-framed and Safety-gated.** Every line is written tentatively —
  no diagnosis, no clinical label as fact, no advice — and every line passes the
  Safety/Consent gate before it is shown. A surfaced pattern always carries the
  raw-entry-id citations that ground it; a line Safety blocks is dropped, and a
  soft overclaim is softened.
- **Lens-framed (optional).** When you have consented to a framework, the
  reflection is framed through that lens and every persisted pattern is stamped
  with which lens produced it (`provenance.framework`). With no lens consented
  — the default — the reflection uses Lucid's baseline voice.

## Usage

```
lucid reflect week                      # Discord-friendly text
lucid reflect week --json               # machine-readable projection
lucid reflect week --week               # only this ISO week
lucid reflect week --since 2026-07-13   # from an explicit day forward
lucid reflect week --days 10            # the last 10 logical days
```

The command takes no positional arguments — the range overrides below are flags.
It is provider-backed (it composes the narrative through the `provider` block in
`lucid.json`), but an **empty or thin window never spends a model call** — with
nothing to read it prints a calm fallback and returns.

### The window it reads

The deep-dive reads **everything since your last reflection**, not a fixed
calendar week:

- **Default** — from the day of your last [`close`](#closing-a-reflection) (or
  your last completed `apply`) through the end of today.
- **First-ever run** — from your earliest logged entry through the end of today,
  subject to the [cap](#the-catch-up-cap).
- **A skipped week is caught up, not dropped.** Miss a Sunday and those days are
  still un-reflected, so the next run reads them. That is this page's core
  promise: no logged day silently falls outside the window.
- **The day you closed on is re-read, not half-dropped.** The window starts at
  the *start* of the day your last close landed on, so an entry logged at 22:00
  after a 20:00 close is still read. You may see a day twice; you will never
  lose one.

#### Range overrides

| Flag | The window it reads |
|------|---------------------|
| *(none)* | Everything since your last close — the catch-up default. |
| `--week` | Monday 00:00 → Sunday 23:59:59 of the ISO week containing today (the behavior before catch-up windows). |
| `--since <YYYY-MM-DD>` | From that day 00:00 through the end of today. |
| `--days N` | The last `N` logical days, ending today. |

The three flags are **mutually exclusive** — passing two is an error, never a
silent precedence. `--since` and `--days` are deliberate requests, so they
**bypass the cap**; the default window and `--week` are capped.

#### The catch-up cap

A catch-up window loads every raw entry it covers, so an unbounded gap would be
slow, expensive, and a worse read — the reflection averages over too much
material. The default window is therefore capped at **35 days (5 weeks)**,
configurable in `lucid.json`:

```json
{
  "reflect_week_max_days": 35
}
```

When the cap bites, the read covers the most recent capped span and **says so**
— the shortfall is stated, never hidden. The text form prints, under the header:

```
Covering the last 35 of 96 un-reflected days — pass --since to read the full span.
```

and `--json` carries the same fact in machine form:

```json
{
  "capped": true,
  "uncovered_days": 61
}
```

`capped` is `false` and `uncovered_days` is `0` on any window the cap did not
touch. Passing `--since` (or `--days`) reads the full span deliberately.

### Text output

The text form is Discord-friendly — bulleted sections and a header line, never a
markdown table. Empty sections are omitted.

The header states the range actually covered. A window that falls inside a single
ISO week keeps the familiar one-week line, unchanged:

```
Week 2026-W19
```

A catch-up window spanning more than one ISO week names both ends and the day
count:

```
Weeks 2026-W29 → W30 (14 days)
```

A window with material renders:

```
Weeks 2026-W29 → W30 (14 days)
A steadier stretch overall, with a few quieter evenings.
Wins:
• Logged an entry every day
Misses:
• One skipped closeout mid-week
Body & pain:
• A pain note on Wednesday
Habit changes:
• Earlier evenings toward the weekend
Next week:
• One small experiment: a fixed wind-down time

Pattern — prep-as-safety (lens: stoicism v1):
One possible pattern: preparation as a way to feel safe — does that fit?
Cites: raw_2026_05_06_20_41, raw_2026_05_08_19_02
```

An empty or thin window prints only:

```
Nothing to reflect on yet this week — capture a few entries and come back.
```

### JSON output

`--json` emits a stable, snake_case projection so a harness can branch on fields
rather than parse prose. The read-only surface creates no record, so there is no
record id or `wrote` flag. `pattern` is `null` when no candidate surfaced;
`applied_lens` is present only when a lens framed the run.

The window fields describe exactly what was read: `window_start` and `window_end`
are logical days (`YYYY-MM-DD`, inclusive), `days_covered` is the count of
logical days between them, and `capped` / `uncovered_days` state any shortfall
(see [The catch-up cap](#the-catch-up-cap)).

**`iso_week` is retained** and keeps its key and its position: on a catch-up
window it is the ISO week of the window's **end** day. Existing harnesses that
key on `iso_week` keep working unchanged — it is never renamed, removed, or
emitted as `null`.

```json
{
  "iso_week": "2026-W30",
  "window_start": "2026-07-13",
  "window_end": "2026-07-26",
  "days_covered": 14,
  "capped": false,
  "uncovered_days": 0,
  "summary": "A steadier stretch overall, with a few quieter evenings.",
  "wins": ["Logged an entry every day"],
  "misses": ["One skipped closeout mid-week"],
  "body_pain": ["A pain note on Wednesday"],
  "habit_change": ["Earlier evenings toward the weekend"],
  "next_week": ["One small experiment: a fixed wind-down time"],
  "pattern": {
    "proposal_text": "One possible pattern: preparation as a way to feel safe — does that fit?",
    "shape_tag": "prep-as-safety",
    "supporting_entry_ids": ["raw_2026_05_06_20_41", "raw_2026_05_08_19_02"]
  },
  "applied_lens": "stoicism v1"
}
```

## The week bundle

The deep-dive reads a **projection-only** bundle for the
[resolved window](#the-window-it-reads). Every field is assembled through a
sanctuary-safe read — the numbers are copied verbatim from the projections,
never recomputed:

- **Honest numbers** — current and longest streak, the window's raw-entry and
  body-signal totals, and the count of accepted insights in the recall window,
  from the same `metrics` / `status` projections `lucid metrics --json` and
  `lucid status --json` expose.
- **Per-day volume** — the raw-entry and observation counts per logical day in
  the window, from the sanctioned `/day` join (the same read `lucid stats` uses),
  so a day's counts match `lucid day`.
- **Raw-entry digest** — your own words for each entry in the window, resolved by
  id through the `/day` projection so the deep-dive can cite an entry it grounds a
  hypothesis in.
- **Body signals** — the kind and logical day of each observation in the window
  (which kind was recorded when — never the value payload).
- **Accepted insights** — the insights validated in the last
  **`max(7 days, reflection window)`**, carried for continuity so the deep-dive
  relates the window to what you have already confirmed rather than re-proposing
  it. The slice widens with a catch-up window and never narrows below the
  seven-day floor, so a three-week read sees the insights you confirmed across
  those three weeks.

Companion message bodies are **not** included: they are not persisted, and are
out of scope for the weekly read.

## Frameworks and lenses

Six framework definitions ship as reference lenses under
[`../frameworks/`](../frameworks.md): `attachment-theory`, `ifs`, `nvc`,
`stoicism`, `four-agreements`, and `eight-dates`. Each is a versioned definition
file — vocabulary, stance, question templates, boundaries — and each can be
consented as a lens the weekly deep-dive frames its reflection through.

Lenses are **off by default**. You opt in through two `lucid.json` keys:

```json
{
  "framework_stack": ["stoicism", "nvc"],
  "framework_consents": { "stoicism": "v1", "nvc": "v1" }
}
```

- **`framework_stack`** — the ordered list of lens ids you have chosen.
- **`framework_consents`** — the version of each lens you have consented to; a
  lens is active only when it appears here.

The **active lens** for a run is selected **deterministically** — the first
consented lens in the stack — and its label (`<id> v<version>`, e.g.
`stoicism v1`) is what stamps `provenance.framework` on any pattern you persist.
There is **no automatic rotation** in this release: which lens is active changes
only when you change the config. (Automatic, evidence-based rotation is protocol
[P-2](../protocols/P-2-lens-rotation.md), deferred to a follow-up.)

Labeling is the whole of the MVP: a lens colors the *framing* and records *which*
lens produced a pattern. It does **not** relax the Safety phrase blocklist — the
vocabulary-licensing mechanism ([`../frameworks.md`](../frameworks.md) §6) is not
yet live, so the blocklist still stands whole and certainty framing
(`you always/never`, `you're / you have <label>`) is never permitted, under any
lens.

## Applying a pattern (`reflect week apply`)

The deep-dive proposes; it never persists. `reflect week apply` is the write path
that routes a surfaced candidate — plus your response — back through the **same**
resonance/consent machinery every `/checkin` proposal passes. There is no
parallel writer.

It reads one JSON envelope on stdin: the candidate the read pass surfaced (echoed
verbatim, including its citations), the lens label that framed it, and your
response.

```
lucid reflect week apply --json < envelope.json
```

Envelope shape:

```json
{
  "candidate": {
    "proposal_text": "One possible pattern: preparation as a way to feel safe — does that fit?",
    "shape_tag": "prep-as-safety",
    "supporting_entry_ids": ["raw_2026_05_06_20_41"]
  },
  "framework": "stoicism v1",
  "response": { "kind": "accepted", "text": "Yes, that fits." },
  "rule": { "answered": false }
}
```

- **`response.kind`** is one of `accepted` | `nuanced` | `rejected` |
  `unanswered`. An unrecognized value is an error, never a silent downgrade.
- **`accepted` / `nuanced`** re-gate the candidate through Safety and persist a
  tracked insight, stamped with its `provenance.framework` lens label and the raw
  entry ids that cite it. A nuance's refinement text becomes the canonical
  statement.
- **`rejected`** records that the shape was not a fit and tracks nothing.
- **`unanswered`** leaves it open and advances the pause bookkeeping.

A completed apply — `accepted`, `nuanced`, or `rejected` — also advances the
[reflected-through cursor](#closing-a-reflection) as a convenience, so answering
a candidate closes the stretch without a second command. `unanswered` does not:
nothing was engaged with.

The apply path honors the existing guardrails end to end: **one hypothesis at a
time** (the deep-dive surfaces at most one candidate), and the
**three-unanswered → 14-day proposal pause**. While a pause is in effect the
read pass suppresses the candidate entirely — the narrative still surfaces — so
no unprompted proposal arrives during the quiet window. Applying an insight
requires at least one processed artifact to anchor it to; a week that never
reached a check-in has no processed context and the apply surfaces that honestly
rather than writing an orphan insight.

## Closing a reflection

The deep-dive's default window runs from your last reflection forward, so
something has to record when that was. That record is the **reflected-through
cursor**: one small receipt at `engine/reflection/receipt.json` under
`~/.lucid/`, overwritten each time — a cursor, not a history.

```
lucid reflect week close          # "Reflected through 2026-07-28."
lucid reflect week close --json   # {"reflected_through": "…", "source": "close", "wrote": true}
```

Two things move the cursor, and nothing else does:

- **`reflect week close`** — the explicit act of finishing a sit-down. It stamps
  the moment you closed.
- **A completed `reflect week apply`** — `accepted`, `nuanced`, or `rejected`.
  Answering a candidate is engagement, so it advances the same cursor as a
  convenience. `unanswered` does **not**.

**Reading is not closing.** Running `reflect week` leaves the cursor exactly
where it was, so a sit-down you open and abandon re-reads those days next time
instead of marking them done. That is deliberate: a repeated read is a cheap,
visible cost, while a silently dropped day is the failure this window exists to
prevent. The trade is stated plainly so you can rely on it — forget to close and
you will see the stretch again; you will not lose it.

## Safety and privacy

- **Read-only by contract.** `reflect week` creates no insight, reflection, or
  raw file, and never stamps the cursor. It reads one small reflected-through
  receipt to resolve its window; the two write paths are the explicit `apply` and
  `close` commands.
- **Sanctuary boundary.** The deep-dive reads only through the storage adapter —
  projections for everything it reflects on, plus the reflected-through receipt
  for its window. It touches no observation or registry record, and reaches no
  `~/.lucid/` path itself.
- **Local-first.** Everything runs on your own host; nothing syncs to a cloud.
- **Hypothesis, never diagnosis.** Every surfaced line is Safety-gated and
  framed as a question, grounded in citations to your own words.
