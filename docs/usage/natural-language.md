# Talking to Lucid — the natural-language surface

**The CLI is the contract; natural phrasing is the human interface.**

The `lucid` verbs in [`commands.md`](commands.md) are the canonical, precise
baseline — deterministic, scriptable, and unambiguous. But you should not have to
remember compact syntax like `closeout ddx 3/late-night …` while you're tired,
mobile, or speaking into voice-to-text. This page describes a **voice-first
layer**: a way to talk to Lucid in plain language and let a chat harness map what
you said onto the exact documented command.

Nothing here changes what Lucid *does*. It changes only how a request is
*phrased*. Every command the harness runs is one you could have typed yourself.

## How translation works

A chat harness with the [Lucid skill](../../skills/lucid/SKILL.md) installed reads
your message, recognizes the intended verb, and **assembles the documented
compact command** — then shells out to the same `lucid` core. Three properties
hold:

- **It invents no command, field, or flag of its own.** The harness can only
  assemble verbs that already exist in [`commands.md`](commands.md); if what you
  asked for isn't a real Lucid verb, it says so rather than guessing.
- **The binary still does the write.** The harness translates phrasing into a
  command; the deterministic, agent-free core performs the actual write and
  acknowledges only after it lands.
- **The compact form stays available.** Typing `lucid closeout ddx 3 …` (or the
  `/closeout` slash form) works exactly as before — precise, and the right tool
  when you want to be unambiguous or you're debugging.

Throughout this page the examples use a synthetic persona, **Sam**, whose chain
has three links — **stretch · water · read**. Swap in your own chain, words, and
numbers; the shapes are the same.

## Command coverage

Rule of thumb: **reads run immediately**, and the Engine's day-record writes
(`closeout`, `closeout backfill`, `mode`, `skip`) are **echoed back as the
assembled command and wait for a one-word confirmation** before they run —
voice-to-text is lossy, and a write lands an immutable day record. Lifecycle
state changes (`storm`, `anchor`, `profile`) are echoed the same way.

### Reads — run immediately

These never change the record, so the harness runs them as soon as it
understands you.

- **`status`** — *"How am I doing?"* / *"What's my Lucid status?"*
  → runs `lucid status`
- **`day`** — *"Show me yesterday."* / *"What did today look like?"*
  → runs `lucid day yesterday` (or `lucid day` for today)
- **`metrics`** — *"How's my streak?"* / *"What's my adherence and how many days
  since I quit soda?"*
  → runs `lucid metrics`
- **`log`** (capture) — *"Jot this down — the pressure drop might be what wrecks
  my knee."*
  → runs `lucid log "the pressure drop might be what wrecks my knee"`
- **`obs`** — pain / intake / elimination / mood micro-logs:
  - *"Log pain four in my knee."* → `lucid obs pain 4 knee`
  - *"I ate oatmeal and a coffee."* → `lucid obs ate oatmeal, coffee`
  - *"Log a BM, Bristol four."* → `lucid obs bm 4`
  - *"Mood's about a three, kind of restless."* → `lucid obs mood 3 restless`
- **clinician `export packet`** — *"Export my clinician packet."* / *"Put
  together the whole history for my doctor."*
  → runs `lucid export packet clinician` (or `… clinician all`) — and posts
  **only the file path**, never the packet body.

### Writes — assembled, echoed, and confirmed

The harness assembles the compact command from what you said, shows it back, and
runs it after a one-word *"yes"*.

**`closeout`** — the nightly record.

- **Instead of typing** `lucid closeout ddx 3/late-night small night but the chain held`
- **you can say** — *"Close me out. I stretched and got my water in, but I didn't
  read tonight. Capacity's a three, it was a late one. Journal line: small
  night but the chain held."*
- **the harness assembles** `lucid closeout ddx 3/late-night small night but the chain held`
  (stretch → **d**one, water → **d**one, read → **x** skipped; capacity `3`;
  limiter `late-night`) and echoes it for your *"yes"* before it runs.

**`closeout skip`** — an honest miss (a real zero; no makeup work is ever owed).

- **Instead of typing** `lucid closeout skip`
- **you can say** — *"I missed the whole thing last night — mark it honestly."*
- **the harness assembles** `lucid closeout skip` and confirms before running.

**`closeout backfill`** — record a recent night that ran but went unlogged
(7-day window).

- **Instead of typing** `lucid closeout backfill yesterday ddf 2 did it, just forgot to log`
- **you can say** — *"I did the chain yesterday but forgot to log it — stretched,
  drank my water, only got a page in on the floor. Capacity two."*
- **the harness assembles** `lucid closeout backfill yesterday ddf 2 did it, just forgot to log`
  (stretch → **d**one, water → **d**one, read → **f**loor) and confirms first.

**`mode`** — declare today's Engine mode (locks at the bell).

- **Instead of typing** `lucid mode yellow`
- **you can say** — *"Make today a yellow day."* / *"I'm running on empty — red
  mode."*
- **the harness assembles** `lucid mode yellow` (or `red`) and confirms before
  running.

### Lifecycle — declared state changes

Less frequent, but consequential — a storm stays your budget, a profile switch
moves your clocks. The harness echoes these for a confirm too.

**`storm`** — declare or end a pre-committed incapacity state.

- **Instead of typing** `lucid storm travel-week`
- **you can say** — *"Declare a storm — I'm traveling all next week and can't hold
  the chain."* / *"The storm's over, I'm back."*
- **the harness assembles** `lucid storm travel-week` (or `lucid storm end`).

**`anchor`** — mark a dated "days since X" milestone.

- **Instead of typing** `lucid anchor add quit-soda 2026-03-01`
- **you can say** — *"Set an anchor — I quit soda on March 1st."* / *"Mark the day
  I cleared the 30-day gate, April 1st."*
- **the harness assembles** `lucid anchor add quit-soda 2026-03-01` (or
  `lucid anchor add gate-30 2026-04-01 cleared the first gate`). Read the running
  counts back with `metrics`.

**`profile`** — switch to a named clock profile (effective the next logical day).

- **Instead of typing** `lucid profile travel`
- **you can say** — *"Switch me to my travel schedule."* / *"Back to my default
  clock."*
- **the harness assembles** `lucid profile travel` (or `lucid profile default`).

## The night close-out, step by step

Close-out is the one write that needs several required pieces at once — the
per-link states, your capacity, an optional limiter, and your journal line — so
it's where voice-first earns its keep. The harness collects **only what's
missing**: it keeps whatever you already said, asks for exactly the gaps, and
never re-asks something you've given.

The required pieces, for a chain of *N* links:

- **Link states** — one character per link, in chain order: **d**one, **f**loor
  (a reduced but real rep), **x** skipped. Sam's three-link chain wants three
  characters, e.g. `ddx`.
- **Capacity** — a single digit **1–5** (1 depleted → 5 resourced).
- **Limiter** *(optional)* — a one-word tag for what capped you (`/late-night`,
  `/travel`). Skipped freely; never a blocker.
- **Journal line** — your one line, written down verbatim.

**Everything at once** — Sam says: *"Close me out. I stretched and got my water
in but didn't read. Capacity three, it was a late one. Line: kept it small but
kept it."*
The harness has all four pieces (`d d x`, capacity `3`, limiter `late-night`,
the line), assembles `lucid closeout ddx 3/late-night kept it small but kept it`,
echoes it, and runs on your *"yes"*.

**Only the gaps** — Sam says: *"Close me out — stretched and drank my water,
skipped the reading."*
The states are clear (`ddx`) but capacity and the line are missing, so the
harness asks **only** for those: *"Got it — stretched and watered, reading
skipped. What was your capacity, 1 to 5, and your line for tonight?"* It does
**not** re-ask the link states you already gave.

## Reads run, writes confirm

One rule keeps voice-first both fast and honest:

- **Read verbs run immediately.** `status`, `day`, `metrics`, `log`, and `obs`
  change no day record (a capture is additive and never overwrites a decided
  day), so the harness runs them the moment it understands you — no confirmation
  step.
- **State-writing verbs are echoed and confirmed.** `closeout`,
  `closeout backfill`, `mode`, and `skip` land or move an Engine day record, so
  the harness **assembles the compact command, shows it back, and waits for a
  one-word confirmation** (*"yes"*) before running it.

The reason is voice-to-text itself: it mishears a digit, flips a link state, or
drops a word — and a close-out writes an **immutable** day record plus a journal
line. A one-word confirm is cheap insurance that the record stays true to what
you actually did. The lifecycle verbs `storm`, `anchor`, and `profile` are
consequential state changes and are echoed for the same one-word confirm.

## When the harness asks instead of guesses

The harness never fabricates a missing detail to avoid a question. When a
**required** piece is absent or ambiguous, it asks **one** concise question and
waits — it does not guess a link state, a capacity, or an observation field you
didn't give.

- **A missing link state** — *"Close me out, I mostly got through it."*
  "Mostly" isn't a state, so the harness asks which links landed rather than
  inventing a pattern: *"Which of stretch, water, read were done, and which fell
  to the floor or got skipped?"*
- **An ambiguous observation** — *"Log some pain."* No location or level, so it
  asks *"Where, and how bad?"* instead of writing a placeholder.
- **A count that doesn't fit the chain** — if the states you give don't total
  your chain's link count (three characters for a three-link chain), the harness
  flags the mismatch and asks, rather than padding or trimming to fit.

One question, then it proceeds. The goal is to remove typing, not to remove the
truth.

## What doesn't change — the boundaries

Talking to Lucid is a phrasing layer over the same core. The guarantees that
make Lucid trustworthy hold exactly as they do on the command line:

- **Engine verbs stay deterministic and are relayed verbatim.** The harness
  surfaces the Engine's own output — a streak, a close-out acknowledgement, a
  status line — **never scored, embellished, or celebrated.** No praise is
  added, no number is recomputed.
- **Every write goes through `lucid`.** The harness only assembles and runs the
  documented command; the deterministic, agent-free core performs the write and
  acknowledges after it lands. The harness never writes state itself.
- **Mirror content is never scored.** Your journal line and captures are held,
  not graded — the voice-first layer adds no judgement to what you write.
- **The Ledger is never hand-edited.** No agent or harness touches the files
  under `~/.lucid/` directly; the store is append-only and schema'd, and the
  only way in is a `lucid` verb.

## Not yet: `/checkin`, `/reflect`, `/ask`

Three Mirror verbs — [`checkin`](commands.md#serve), [`reflect`](commands.md#reflect),
and [`ask`](commands.md#ask) — are **provider-backed** (they need an LLM backend)
and are **not yet wired** into this natural-language layer. Until they ship,
drive them by their documented forms. Their voice-first phrasing is **deliberately
deferred** so the gap stays a conscious choice, not an omission — this page will
grow to cover them when they land.
