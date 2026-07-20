# The weekly reflection (`lucid reflect week`)

`lucid reflect week` is the **read-only weekly deep-dive**: once a week it reads
the past week's projections, frames a calm, hypothesis-first reflection through
your active interpretive lens, and surfaces at most one tentative pattern for you
to accept, refine, or set aside. It is a mirror for the whole week, distinct from
the daily rhythm and from `lucid reflect`, which only recalls insights you have
already validated.

This page covers what the deep-dive reads, the frameworks/lens layer it can be
framed through, the two commands (`reflect week` and `reflect week apply`), and
how a pattern you confirm becomes a tracked insight through the same resonance
gate every proposal passes.

> Read [`commands.md`](commands.md) for the full CLI reference, [`companion.md`](companion.md)
> for the daily model-composed messages, and [`../frameworks.md`](../frameworks.md)
> for the design of the interpretation layer.

## What it is

- **Read-only.** The deep-dive **writes nothing** under `~/.lucid/` — no insight,
  no reflection record, no raw entry. It reads the week, composes the reflection,
  and returns it. Persisting a pattern is a separate, explicit step
  ([`reflect week apply`](#applying-a-pattern-reflect-week-apply)).
- **Projection-only.** It never reaches the sanctuary trees
  (`engine` / `observations` / `registries`) directly. Every number and signal
  comes through the same read-only projections the CLI and the daily companion
  already expose (see [The week bundle](#the-week-bundle)).
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
lucid reflect week            # Discord-friendly text
lucid reflect week --json     # machine-readable projection
```

The command takes no positional arguments. It is provider-backed (it composes the
narrative through the `provider` block in `lucid.json`), but an **empty or thin
week never spends a model call** — with nothing to read it prints a calm fallback
and returns.

### Text output

The text form is Discord-friendly — bulleted sections and a header line, never a
markdown table. Empty sections are omitted. A week with material renders:

```
Week 2026-W19
A steadier week overall, with a few quieter evenings.
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

An empty or thin week prints only:

```
Nothing to reflect on yet this week — capture a few entries and come back.
```

### JSON output

`--json` emits a stable, snake_case projection so a harness can branch on fields
rather than parse prose. The read-only surface writes nothing, so there is no
record id or `wrote` flag. `pattern` is `null` when no candidate surfaced;
`applied_lens` is present only when a lens framed the run.

```json
{
  "iso_week": "2026-W19",
  "summary": "A steadier week overall, with a few quieter evenings.",
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

The deep-dive reads a **projection-only** bundle for the ISO week containing
today. Every field is assembled through a sanctuary-safe read — the numbers are
copied verbatim from the projections, never recomputed:

- **Honest numbers** — current and longest streak, this week's raw-entry and
  body-signal totals, and the count of accepted insights in the recall window,
  from the same `metrics` / `status` projections `lucid metrics --json` and
  `lucid status --json` expose.
- **Per-day volume** — the raw-entry and observation counts per logical day of
  the week, from the sanctioned `/day` join (the same read `lucid stats` uses),
  so a day's counts match `lucid day`.
- **Raw-entry digest** — your own words for each entry in the week, resolved by
  id through the `/day` projection so the deep-dive can cite an entry it grounds a
  hypothesis in.
- **Body signals** — the kind and logical day of each observation in the week
  (which kind was recorded when — never the value payload).
- **Accepted insights** — the insights validated in the rolling recall window,
  carried for continuity so the deep-dive relates the week to what you have
  already confirmed rather than re-proposing it.

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

The apply path honors the existing guardrails end to end: **one hypothesis at a
time** (the deep-dive surfaces at most one candidate), and the
**three-unanswered → 14-day proposal pause**. While a pause is in effect the
read pass suppresses the candidate entirely — the narrative still surfaces — so
no unprompted proposal arrives during the quiet window. Applying an insight
requires at least one processed artifact to anchor it to; a week that never
reached a check-in has no processed context and the apply surfaces that honestly
rather than writing an orphan insight.

## Safety and privacy

- **Read-only by contract.** `reflect week` creates no insight, reflection, or
  raw file. The only write path is the explicit `apply` command.
- **Sanctuary boundary.** The deep-dive reads only through projections; it never
  touches `~/.lucid/engine`, `~/.lucid/observations`, or `~/.lucid/registries`
  directly.
- **Local-first.** Everything runs on your own host; nothing syncs to a cloud.
- **Hypothesis, never diagnosis.** Every surfaced line is Safety-gated and
  framed as a question, grounded in citations to your own words.
