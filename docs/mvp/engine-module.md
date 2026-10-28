# Lucid MVP — Engine Module

This page specifies the MVP slice of the **Engine** subsystem defined in
[`../engine.md`](../engine.md) and [`../architecture.md`](../architecture.md) §3.
It extends the Mirror steel thread in [`steel-thread.md`](steel-thread.md)
with the unified nightly loop: **the Engine's two-minute close-out is a
capture surface** — one act feeds both subsystems.

It follows the same conventions as the rest of this doc set: module
charters, named storage ops, binding schemas, per-phase acceptance
criteria. Where this page and another MVP doc disagree about *scope*,
this page and [`scope.md`](scope.md) win; where they
disagree about *conventions* (naming, mutability, adapter discipline),
the Mirror-thread docs win.

## Why the Engine is in the MVP

The architecture's central claim ([`../architecture.md`](../architecture.md) §1)
is that reflection tools fail without a behavior layer and behavior
tools fail without a reflection layer. A Mirror-only MVP cannot test
that claim. The falsifiable question in [`README.md`](README.md) therefore
widens to:

> Can a local-first companion **defend one committed daily practice**
> (initiate it, record it, escalate honestly when it slips) **and**
> capture the reflection that rides on it, structure it, propose one
> resonance-gated pattern, and recall it later — such that after 30
> days the user has both an unbroken record and at least one validated
> insight?

## Design constraints inherited

Three rules from [`../engine.md`](../engine.md) and
[`../architecture.md`](../architecture.md) bind everything below:

1. **Agent-free by design.** The Engine module contains **no LLM
   agent**. Close-out is a fixed deterministic script; streak math is
   arithmetic; escalation messages are templates. This satisfies both
   "deterministic scripts before clever agents"
   ([`product-principles.md`](product-principles.md) §8) and
   architecture **P9** (the runtime never depends on AI). If every
   model is down, `/closeout` still works.
2. **Consequences attach to acts, never to content** (architecture **P3**).
   The Engine module reads and writes only `~/.lucid/engine/`. It has
   no read path to `raw/`, `processed/`, `insights/`, or `people/`.
   The close-out's journal line is written to `raw/` *by the router
   via the storage adapter*, exactly like a `/log` entry — the Engine
   sees only that a journal event occurred, never what it said.
   Witness-facing output is computed exclusively from the
   `engine/` tree.
3. **Priority order: chain > record > analysis** (architecture
   **P10**). Every failure path below degrades toward "accept a
   minimal record now, repair later" — never toward blocking the
   user's chain on tooling.

## Consent amendment: pre-committed sends

The existing approval-before-action gate
([`architecture.md`](architecture.md) §6) says no external send exists.
The Engine requires exactly one narrow, explicit exception, consistent
with architecture **P5** (pre-commitment is consent granted in
advance):

| Send | Channel | Content | Consent record |
|------|---------|---------|----------------|
| Bell prompt (L0/ignition) | The user's own Lucid channel | Fixed template naming the chain | `engine/chain.json` `bell.enabled: true`, set by the user |
| L1 nudge | The user's own Lucid channel, morning after a single miss | Fixed template: last night was a miss; never miss twice; tonight is a must; the floor, named | `engine/chain.json` `escalation.l1_enabled: true` |
| L2 witness escalation | The witness's confirmed channel, after two consecutive misses | Fixed template: streak state, declared mode, and storm state only — **never** journal content, capacity values, or any Mirror data | `engine/witness.json` with `confirmed_at` set by an explicit witness confirmation |

The witness consent additionally covers a **monthly heartbeat**: one
fixed line to the same channel, same template discipline. The
heartbeat is a **present-state snapshot, never a month summary** — it
reports the state at send time, so it can never falsely imply a quiet
month. Two templates, selected by `escalation_state` at send time:

| State at send time | Template (fixed, sent verbatim) |
|--------------------|--------------------------------|
| No open escalation | `monthly status: all clear — N-day streak, no open escalation.` |
| An escalation fired this month and remains open | `monthly status: N-day streak; an escalation fired this month and remains open — you have already seen it.` |

It exists so silence stays meaningful — the witness's briefed backstop
(engine §4) is "a full calendar month with *neither* an L2 nor a
heartbeat means the tooling died; ask once," which only works if a
healthy system is never totally silent. The heartbeat is part of the
L2 send class, not a fourth consent. In Phase 0 the user sends it by
hand, **template verbatim** — paraphrase dissolves the template
discipline and risks content leakage.

**Storm variants.** While a storm stands (engine §4), L1 and L2 fire
on their ordinary schedule using fixed storm variants of the same
templates — same send classes, same consent records, no new consent,
no LLM:

| Send | Storm variant (fixed, sent verbatim) |
|------|--------------------------------------|
| L1 | `last night was a miss — storm standing, nothing is owed. If tonight allows it, the floor: <floor>.` |
| L2 | `two missed nights — storm standing (confirmed <date>). The stake is stayed; the ask-once still applies.` |

The variants exist because the ordinary L1 ("tonight is a must") has
the wrong voice for a confirmed collapse; contact continues, coercion
pauses. Selection is deterministic: standing storm at send time ⇒
storm variant.

**Self-identification.** L1, L2, and both storm variants each end with
the same fixed final line:

```
— the form letter, pre-committed at Day 0.
```

The retention evidence is blunt: the escalation received as a form
letter gets a floor run that night; the one received as a judgment
gets an uninstall. The user read these exact texts at Day 0
(engine §5, the comprehension pass), so the line points at something
they remember writing into force. The bell prompt and heartbeat carry
no sign-off — one names a chain, the other reports status; neither
stings.

**The evening backstop is a send condition, not a send.** When the
config-gated daily companion is enabled it presents the evening user
window and the bell periodic is suppressed, so the window has exactly
one sender. If that sender's delivery fails outright, the window would
pass in silence — so a dedicated backstop periodic
(`lucid-bell-fallback`,
[`../usage/commands.md`](../usage/commands.md#the-evening-backstop-lucid-bell-fallback))
fires the **bell template above, verbatim**, once the companion can no
longer post, and only when the companion left no verified delivery
receipt for that logical day. This adds **no fourth send class and no
new consent**: it is the L0/ignition bell, on the bell's own consent
record (`chain.json` `bell.enabled`), fired under a new deterministic
condition. `bell.enabled: false` silences it exactly as it silences the
bell. The gate reads one engine-tree receipt — no Mirror content, no
model, no new template — so the ceiling below holds unchanged.

**The shared send path's guarantee is *never a silent miss*, not
exactly-once.** Every autonomous sender (companion, workout slot, weekly
witness report) runs the same reliability order, whose idempotency turns on the
delivery receipt and a read-back of the prior message: a read-back that proves
the message is gone (a clean `404`) re-sends so the window is never silently
empty; an *indeterminate* read-back (a transient `5xx`/`429`) holds and lets the
supervised retry re-probe rather than risk a duplicate; and the one residual
window — a process death between a verified send and its receipt write — costs a
bounded, at-most-one-extra post, which is announced with a loud alert rather than
passing quietly (see [`../usage/companion.md`](../usage/companion.md) §"When
things go wrong").

Binding rules: these are the **only** autonomous sends in the MVP; all
three use fixed templates with no LLM in the path; L2 cannot be
enabled until `witness.json` has `confirmed_at`; disabling any of them
is recorded as a config change with a reason (architecture P8). The
"no autonomous notifications" non-goal in the older MVP docs is
superseded *only* for these three sends.

## Commands (router additions)

| Command | Plan | Writes |
|---------|------|--------|
| `/closeout` | Deterministic guided close-out (see sequence below). No agents. | `engine/days/<day-id>.json`; one raw entry via `storage.write_raw` (`command: /closeout`); `sessions/<sid>.json`; rebuilt `engine/status.json` |
| `/mode <green\|yellow\|red> [--day <date>]` | Declare today's mode. Rejected after today's bell time (engine §2: fixed at the Bell; no retroactive amendment). `--day` is **gap-fill only** — see §"`/mode --day` gap-fill" below. | `engine/days/<day-id>.json` (mode field, append-style: first declaration wins after bell) |
| `/status` | Read-only: current streak, rolling adherence vs declared mode — always co-presented with the floor-day ratio and raw days-accounted (the **honest-number pairing**, engine §3) — error-budget burn, days to next gate; plus `stake_owed` when a breach has outlived the stake execution window (engine §4; **plumbed but reserved in the MVP** — see the tripwire's L3 note in step 5), and "witness lapsed — L2 disarmed" while the witness contract is lapsed (see §`witness.json`). This is the MVP's **L0 ambient surface**. | None |
| `/closeout skip` | Records an explicit miss for the logical day (honest zero, distinct from silence — silence is what the dead-man tripwire detects). | `engine/days/<day-id>.json` with `missed: true` |
| `/closeout backfill [yesterday\|<YYYY-MM-DD>] [<compact form>]` (also `--day <date>`, an alias onto the same path) | Creates or corrects a record for a recent past day — the chain **ran** but went unrecorded (P10's text-to-self path, now with a command). Deterministic; accepts the same compact form as `/closeout`. See sequence below. | `engine/days/<day-id>.json` (created with `"backfilled": true`, or a `corrections[]` append); one raw entry via `storage.write_raw` (`command: /closeout backfill`); rebuilt `engine/status.json` |
| `/storm <clause-label\|unwritten> [--day <date>]` | Declare a storm citing a Charter clause label (registered in `storm.json`) or `unwritten` (engine §4). Stands only on witness confirmation within the confirmation window (72 hours); ambush windows enter automatically from their `storm.json` dates, no per-event confirmation. Ack while pending: `storm declared (<label>) — pending witness confirmation (72h).` Ack once standing: `storm standing through <date> (<label>) — undeclared days default to red; the stake is stayed; contact continues.` Renewal is the same command re-issued before expiry, once. | `engine/storm.json` (append-only `history[]`); rebuilt `engine/status.json` |
| `/storm end [--day <date>]` | End a standing storm early; expiry at the duration bound is otherwise automatic. Breach math resets at exit (engine §4). | `engine/storm.json` (append-only `history[]`); rebuilt `engine/status.json` |
| `/profile <name>` | Switch to a named clock profile defined in `chain.json` (definitions change only at a Retro — engine §2). Sticky, recorded, **effective from the next logical day, never the current one**; the outgoing day completes under the clocks that started it. | `engine/profile.json` (append-only `history[]`) |
| `metrics` (read; CLI `lucid metrics [--json]`) | Read-only **practice-quality** rollup: current/longest streak, 30-day adherence (co-presented with its honest co-numbers) **plus the 30/60/90 gate rollups in `--json`**, misses in window, error-budget burn, and one line per active anchor **in its kind's language** — days since for a `milestone`, days since the last slip for a `quit`, the allowance spent for a `limit`, days running beside the windowed kept-of-accounted days for a `habit` (§"Anchor event store (`anchor_events.json`)"). Assembled from existing Engine folds (`ComputeStreaks`, the adherence/error-budget windows) — no new chain math; the only new arithmetic is the anchor math (the days-since day count and the per-kind fold), a pure function of the two anchor files that never touches the chain numbers. | None (silent scaffold only) |
| `anchor add <label> <date> [note] [--kind <kind>] [--allowance N --window D] [--note-file <path>]` (record; CLI `lucid anchor add`) | Record a dated **anchor** to the append-only anchors store. `<date>` reads the shared date grammar ([`../usage/commands.md`](../usage/commands.md#backdating-with---day)) with two documented carve-outs: a **future date is accepted** (an anchor may be a forward commitment — the CLI's only surface with no future ceiling), and a **partial date is rejected** (a "days since" count needs a real day, never a guessed January 1st). **Latest record per identity wins**: the append reuses the id of the **active** anchor holding that label (a correction), and mints a new id only when none does — a first use, or a label whose only holder was sunset. **Without `--kind` the verb is exactly what it has always been** — a `milestone` (the default kind) and no event. **`--kind <kind>`** (`milestone`, `quit`, `limit`, `habit`) records the same v3 definition plus the anchor's first `kind` event (with `--allowance`/`--window` for a `limit`, `--window` optionally for a `habit`); it is a two-file verb, written **intent → `kind` event → definition**. `--kind` on a label an active anchor already holds is rejected (reclassify with `anchor kind`). `--note-file` supplies the note off the command line. Deterministic, no model in the path. | `engine/anchors.json` (append-only `history[]`); with `--kind`, first `engine/anchor_events.json` (a pair intent, then the `kind` event) |
| `anchor sunset <label> [reason] [--reason-file <path>] [--graduated]` (record; CLI `lucid anchor sunset`) | Retire an anchor from the counting surfaces without deleting anything: appends one full mirror of the anchor plus `state: "sunset"`, carrying the anchor's own `date` and the optional reason in `note`. The anchor leaves `metrics` prose, `anchors[]`, and the kind-aware witness watch-outs, and appears in `metrics --json` → `anchors_sunset[]`. **`--graduated`** retires it as *graduated* — "this became who I am", not an abandoned sunset — and is a two-file verb, written **intent → `graduate` event → the same plain v3 sunset record**: never a new `state` value, because an older binary's `IsSunset` reads any other state as active. A graduated anchor appears in `anchors_graduated[]` instead of `anchors_sunset[]`; an older binary shows an ordinary sunset. Rejects an unrecorded label (naming the recorded ones), an already-sunset one, or an anchor whose two-file write is unfinished ([`error-states.md`](error-states.md) An-13 — retrying that write finishes it), before any write; `--graduated` also needs a durable id (An-5). Echo-and-confirm tier for `--graduated`. Deterministic, no model in the path. | `engine/anchors.json` (append-only `history[]`); with `--graduated`, first `engine/anchor_events.json` (a pair intent, then the `graduate` event) |
| `anchor rename <label> <new-label>` (record; CLI `lucid anchor rename`) | Change an active anchor's display name, keeping its id, date, note and running day count — one append under the same id. Against a **legacy (id-less) anchor** it writes a **pair** in a single write: an id-less sunset stub retiring `legacy:<old-label>`, plus a new id-bearing record carrying the original date and note under the new name. An anchor's kind and events key on its id, so they follow a rename untouched, for every kind. Rejects an unrecorded, already-sunset, identical, or actively-held target label, or an anchor whose two-file write is unfinished (An-13), before any write. Deterministic, no model in the path. | `engine/anchors.json` (append-only `history[]`) |
| `anchor kind <label> <kind> [--allowance N --window D] [--note-file <path>]` (record; CLI `lucid anchor kind`) | **Reclassify** an active anchor in place as `milestone`, `quit`, `limit` or `habit`: its id, label and date are unchanged, and one dated `kind` event is appended. Changing a limit's allowance or window is the same verb, so the parameter history is the sequence of dated `kind` events. A `limit` requires both `--allowance` and `--window` (positive integers); a `habit` takes `--window` alone (default 30); a `milestone` or `quit` takes neither. A reclassify **to `quit`** whose latest use-day is later than the folded date also appends a **compat re-date** — a v3 definition record at that use-day, so an older binary agrees — and is then a two-file verb, written **intent → compat re-date → `kind` event**. Every other reclassify leaves `anchors.json` untouched. Rejects a pre-id anchor (`lucid anchor backfill-ids` first), a sunset anchor, or invalid parameters, before any write. Echo-and-confirm tier; the receipt is the event id. Deterministic, no model in the path. | `engine/anchor_events.json` (one `kind` event); a reclassify to `quit` past a later use-day writes a pair intent, then the compat re-date to `engine/anchors.json`, then the `kind` event |
| `anchor slip <label> [--day <grammar>] [--note-file <path>]` (record; CLI `lucid anchor slip`) | **`quit` anchors only.** Record a relapse, kept separate from a date correction: the running count restarts from the slip day, while the longest clean run and the slip count are kept. When the slip moves the quit's effective date — `max(start, every accepted use-day including this one)`, so a backdated slip never moves it backwards — it is a two-file verb, written **intent → compat re-date → `slip` event**, so neither binary ever shows a run longer than the truth; a slip that does not move it (backdated, or on a day already counted) writes the event alone. Re-dating with `anchor add` stays the *correction* path. Capture tier; the receipt is the event id. Deterministic, no model in the path. | `engine/anchor_events.json` (`slip` event, plus a pair intent when it re-dates) + `engine/anchors.json` (the compat re-date, when the effective date moves) |
| `anchor use <label> (--planned \| --slip) [--day <grammar>] [--note-file <path>]` (record; CLI `lucid anchor use`) | **`limit` anchors only.** Log one use with its **required intent** — `--planned` or `--slip`; neither or both is rejected, because the binary never guesses an intent. A use spends allowance; a **planned use never resets or zeroes anything**. Several uses on one day are one use-day. Capture tier; the receipt is the event id. Deterministic, no model in the path. | `engine/anchor_events.json` (one `use` event) |
| `anchor miss <label> [--day <grammar>] [--note-file <path>]` (record; CLI `lucid anchor miss`) | **`habit` anchors only.** Log a missed day — the only thing a habit logs as it happens. A miss always wins over a confirm on the same day. Capture tier; the receipt is the event id. Deterministic, no model in the path. | `engine/anchor_events.json` (one `miss` event) |
| `anchor confirm [<label>...] [--through <grammar>] [--from <grammar>]` (record; CLI `lucid anchor confirm`) | Attest a span of days for each named **habit** (no labels: every active habit). The through-day defaults to **yesterday's logical day**; the span starts at the later of the day after the habit's last non-voided confirm and the day of its latest `habit` `kind` event — the day it became a habit, never the anchor date — and `--from` widens it to an earlier stretch the person vouches for, never before the anchor date nor after the through-day. One `confirm` event per non-empty span; an empty span writes nothing and the ack says why ("already confirmed through `<date>`", or, when the through-day falls before the habit began, the day its confirmations start), and no active habits is an honest no-op. The ack names `confirmed <from>–<through>` per habit. Echo-and-confirm tier. Deterministic, no model in the path. | `engine/anchor_events.json` (one `confirm` event per habit with a non-empty span) |
| `anchor void <event-id> [--reason-file <path>]` (record; CLI `lucid anchor void`) | Correct a misheard `slip`, `use`, `miss`, `confirm` or `graduate` without rewriting: appends a `void` naming the target, and the voided event drops out of all math while the history keeps both. A void of a slip that moves the quit's effective date also appends the **restoring** v3 definition, and is then a two-file verb, written **intent → `void` event → restoring record**. Rejects an unknown id, an already voided event, a `void`, a `kind` event (reclassify instead), or an event of a sunset anchor other than its own `graduate`, before any write. Echo-and-confirm tier — voiding a lapse makes the record kinder, so it runs only on an explicit correction. Deterministic, no model in the path. | `engine/anchor_events.json` (one `void` event, plus a pair intent when it restores) + `engine/anchors.json` (the restoring record, last) |
| `self set <key> <value> [--since <date>] [--note <text>]` (record; CLI `lucid self set`) | Record a **self fact** — a durable, atemporal attribute of the subject — to the append-only self-facts store. The `<key>` is `<namespace>.<leaf>` over a fixed five-namespace set (`identity.`, `body.`, `constraint.`, `pref.`, `misc.`) with a free-form leaf; an unrecognized namespace is rejected with the accepted set **and** `misc.` named, so no fact is ever turned away. **Latest record per identity wins**: the append reuses the id of the **active** fact holding that key (a correction), and mints a new id only when none does — a first use, or a key whose only holder was retired. `--since` is **optional** (the only write surface where a date is not required at all) and reads the shared date grammar with `AllowPartial`: a partial origin is stored **as typed**, never snapped, because nothing derives a day count from a self fact. Deterministic, no model in the path. | `engine/self.json` (append-only `history[]`) |
| `self retire <key> [reason]` (record; CLI `lucid self retire`) | Retire a fact that has stopped being true, without deleting anything: appends one full mirror of the fact plus `state: "retired"`, carrying the fact's own `since` (never the retirement day) and the optional reason in `note`. The key leaves the rendered profile, `--json`, and the `/ask` grounding slice, while staying in `history[]` in full and readable under `--history`. Rejects an unrecorded key (naming the recorded ones) or an already-retired one, before any write. Deterministic, no model in the path. | `engine/self.json` (append-only `history[]`) |
| `self move <key> <new-key>` (record; CLI `lucid self move`) | Re-categorize a fact, keeping its id, value, origin, note and whole history — **exactly one append** under the same id with the new key. There is no legacy pair-write here: every record in this store carries an id, so the `anchor rename` adoption path has no analogue. Rejects an unrecorded, already-retired, identical, or actively-held target key before any write; a key held only by a retired fact is free. Deterministic, no model in the path. | `engine/self.json` (append-only `history[]`) |

`/chain` (editing chain config in-channel) is deliberately **not** a
command: parameters change at most once per weekly Retro (engine §5),
by hand-editing `engine/chain.json` — it is tiny and human-readable,
and making edits slightly effortful is the point.

### `/closeout` sequence

1. Resolve the **logical day** — binding attribution rule: *a
   close-out belongs to the bell that initiated it.* If now <
   `rollover` (default 04:00 local), the day is yesterday's date
   (engine §1). Additionally, if now ≥ `rollover` **and** the previous
   logical day has no day record **and** the current logical day's
   bell has not yet rung, the close-out attributes to the **previous
   logical day** — a night-shift 04:12 close-out still answers last
   night's bell. `/closeout today` explicitly forces
   current-logical-day attribution.
2. If a day record already exists with `completed: true` → say so,
   stop (idempotent).
3. Prompt once per link, in chain order: `done / floor / skipped`.
   Single message with reaction-or-reply shorthand is fine; the hard
   budget is **two minutes**, enforced by design (one prompt per link,
   no free-form negotiation).
4. Prompt for capacity digit (1–5; 1 = depleted, 5 = resourced) and
   optional one-word limiter tag.
5. Prompt for the journal line (floor: one line; voice attachment
   sanctioned where the harness supports transcription later).
6. Router writes: the raw entry (journal body) via `storage.write_raw`;
   the day record via `storage.write_engine_day`; then rebuilds
   `engine/status.json` via `storage.rebuild_engine_status`.
7. Ack with the updated streak — one line, no celebration theater.

If the user answers nothing after step 3 begins, whatever was gathered
is written with `partial: true`; a partial close-out with at least the
survival floor link still counts per the declared mode. Capture is
never lost to an interrupted flow (P10).

**Compact form (the friction floor).** The whole close-out also
accepts a single message, no prompts:

```
/closeout dfx 3/wrist Long day but the chain ran.
```

— one character per link in chain order (`d` done, `f` floor,
`x` skipped), capacity digit with optional `/tag`, then the journal
line. The guided flow is the default; the compact form exists because
the two-minute budget is a ceiling, not a target, and on the worst
nights the difference between one message and five prompts is the
difference between a record and a miss. Both forms write identical
records; the parser is deterministic (a script, per
[`claude-code-workflow.md`](claude-code-workflow.md) §"Deterministic
scripts").

### `/closeout backfill` sequence

`/closeout backfill [yesterday|<YYYY-MM-DD>] [<compact form>]` records
that the chain **ran** but went unrecorded — the phone-alarm night,
the text-to-self (engine §2, P10), now with a command instead of a
hand edit. The `yesterday` keyword resolves to the day before the
current **logical** day (the rollover boundary of engine §1), so a
pre-rollover backfill targets the day that just ended — never the one
still in progress. Default target: the most recent logical day without a
completed record. The window is `backfill_window_days` (`chain.json`,
default 7); backfills beyond it are rejected (see §"Error states").
If no day record exists for the target day, one is created with
`"backfilled": true`; if one exists, a `corrections[]` entry is
appended (schema below). The journal line is written to `raw/` by the
router via `storage.write_raw` (`command: /closeout backfill`),
exactly like `/closeout`'s — the Engine still never reads it (P3).
Both paths end with `storage.rebuild_engine_status`.

Retraction semantics are arithmetic, not negotiation:

* The tripwire evaluates **folded** state at run time — a backfill
  landing before the tripwire run means no L1/L2 fires.
* An already-sent L1/L2 is **never unsent**. The record folds to
  completed; `escalation_state`, streak, and error budget recompute on
  rebuild; the witness-facing view and the next heartbeat reflect
  the corrected state; the human ask-once conversation absorbs any
  already-fired L2. There is no retraction send — the message ceiling
  holds.
* A breach recorded then corrected within the window voids the stake
  obligation **only if** the folded record shows no breach occurred.

Honesty guards: the backfill count is Tier-3 telemetry reviewed at
Retro — a rising count is a capture-friction canary, never a score.
Correcting an explicit `/closeout skip` to completed is allowed within
the window; the correction trail stays visible, and that transparency
(P8) is the guard.

**The `--day` alias.** `closeout --day <date>` routes onto this same
backfill path — it is a spelling, not a second mechanism. The record is
stamped `"backfilled": true`, `backfill_window_days` applies unchanged,
and both a future target and **today** are rejected (a backfill target
is always a day that has ended). A *relative* token resolves through the
same profile-aware `yesterday` intent the positional keyword uses, never
a calendar date computed by the CLI — a naive calendar date collides
with the in-progress day before the rollover and reads as out of window.
Passing `--day` alongside anything that names its own day — the
positional `backfill <target>` form, or the `skip` / `today` sub-forms —
is a usage error: two targets, one intent.

### `/mode --day` gap-fill

`/mode --day <date>` fills a mode on a **past** logical day whose record
carries none. It is deliberately narrower than `/closeout backfill`,
because mode is the one field the bell fixes (engine §2):

* **Gap-fill only, never overwrite.** It writes `mode` and
  `mode_declared_at` **only** when the target day never declared a mode —
  read from an empty `mode_declared_at`, not from an empty `mode`. A
  close-out builds its record with the chain's default mode already in
  place, so a day that was closed out but never declared carries
  `mode: green` with no declaration timestamp; that default is a
  placeholder the record needed in order to exist, not testimony about
  the day. A day whose mode was declared is rejected — the bell still
  binds every day you showed up for, and there is still no retroactive
  amendment of a declared mode. `mode` therefore stays **off** the
  foldable-field whitelist (§"Day record"): the correction path cannot
  reach it either, and this fill is its own guarded write rather than a
  correction.
* **Bounded by `backfill_window_days`.** The same `chain.json` knob that
  governs `/closeout backfill` (default 7), so the two retroactive paths
  cannot drift apart. Because the window rule requires a span of at
  least one day, **today and any future day are rejected for free**.
* **Two target shapes.** If the target day has no record at all, one is
  created carrying the mode, with `profile` derived from the history
  governing the *target* day (never the state at write time — the same
  rule a backfilled close-out follows). If a record exists but declared
  no mode — the ordinary shape of a day closed out and never declared —
  only the two mode fields are filled.
* **`status.json` is rebuilt** afterwards, exactly as the same-day path
  does.
* The same-day path is unchanged: still bell-gated, still
  first-declaration-wins.

### Backdated storm events

`/storm … --day <date>` dates the storm event itself, on declare, renew,
and end. The case it serves is the honest one: the days a storm is worst
are precisely the days declaring it at the time was not possible. The
resolved instant becomes the event's `at`, `through` derives from it as
usual, and standing is evaluated **at that instant** — so a renew or end
dated to a moment when no storm stood is rejected by the existing checks,
unchanged, and renew-once still holds.

One invariant is added: storm history is folded in order and the standing
computation takes the last matching event, so an event dated **before the
last recorded storm event** is rejected as incoherent input and nothing
is written. Backdating necessarily re-derives standing for the days it
now covers; that retroactive recompute is inherent to backdating and is
accepted.

## Storage additions — `~/.lucid/engine/`

```
~/.lucid/engine/
├── chain.json               # chain config — the only hand-edited file
├── witness.json             # witness contract + consent record
├── storm.json               # storm clauses, ambush windows, append-only history
├── profile.json             # active clock profile + append-only switch history
├── anchors.json             # anchor definitions (label, date, state) — append-only history, v3
├── anchor_events.json       # anchor event store — kinds, slips, uses, misses, confirms, voids, graduations (append-only; absent until the first event)
├── self.json                # self facts ("about me") — append-only history, atemporal
├── days/                    # one record per logical day (append-only per day)
│   └── 2026/07/day_2026_07_02.json
└── status.json              # derived projection — rebuildable, never hand-edited
```

New storage-adapter named ops: `write_engine_day`, `read_engine_day`,
`read_engine_days(window)`, `rebuild_engine_status`,
`read_engine_status`, `read_chain_config`, `read_witness_contract`,
`read_storm_state`, `append_storm_event`, `read_profile_state`,
`append_profile_event`, `read_anchors`, `append_anchor`,
`read_anchor_events`, `append_anchor_events`, `append_anchor_pair_intent`,
`read_self_facts`, `append_self_fact`.
Same discipline as [`architecture.md`](architecture.md) §4: only the
adapter touches disk; `days/` is append-only per day-id (corrections
append a `corrections[]` entry, never rewrite — the mode ledger is
append-only per engine §2).

### `chain.json` (binding schema, example values)

```json
{
  "version": 1,
  "chain_id": "night_chain",
  "label": "Journal. Dock. Read.",
  "bell_time": "19:00",
  "rollover": "04:00",
  "backfill_window_days": 7,
  "links": [
    {"key": "journal", "name": "One journal line", "floor": "one line, spoken or typed"},
    {"key": "dock",    "name": "Phone to charger", "floor": "phone on charger"},
    {"key": "read",    "name": "Read in bed",      "floor": "one page"}
  ],
  "survival_link": "journal",
  "chain_start": null,
  "footprint_cap_minutes": 30,
  "slo": {"isolated_miss_budget_per_30d": 4, "gate_threshold": 0.85, "gates": [30, 60, 90]},
  "bell": {"enabled": true},
  "escalation": {"l1_enabled": true, "l2_enabled": false, "tripwire_time": "06:00"},
  "away_mode": null,
  "profiles": {
    "nights": {"bell_time": "08:30", "tripwire_time": "17:00", "rollover": "12:00"}
  }
}
```

`chain_start` is set automatically on the first completed close-out
and never changed thereafter; all gate math derives from it.
`profiles` holds named clock alternates for recurring schedules
(engine §2); the top-level `bell_time`, `rollover`, and
`escalation.tripwire_time` **are** the `default` profile. Profile
*definitions* change only at a Retro, like every other field in this
file; profile *switching* is `/profile <name>` and lives in
`profile.json`. The bell prompt, the mode deadline, the tripwire run,
and logical-day math all read the clocks of the profile active for the
day in question. **One bounded exception:** raw-entry day attribution
— the logical day a bare `lucid log` reports, the day `lucid day`
groups a raw entry under, and the day a bare `lucid day` opens on —
reads only the **top-level** `rollover` (the `default` profile), with
04:00 as the documented fallback when this file is absent or that
value is invalid ([`../observations.md`](../observations.md) §2).
Resolving the active profile on those paths is deferred; while the
`default` profile is active the two agree.
`survival_link` is what a Red day requires — note the deliberate
alignment: when the survival link is the journal, the close-out's one
line *is* the Red-day floor, so the worst day costs one sentence.
`away_mode`, when set, names which links compress and what counts as
the floor (engine §5) — floor nights in Away Mode count as completions.

### `witness.json`

```json
{
  "witness_name": "J.",
  "channel": {"kind": "discord_channel", "id": "channel_witness"},
  "briefed_at": "2026-07-05T18:00:00-04:00",
  "confirmed_at": "2026-07-05T18:20:00-04:00",
  "confirmation_text": "confirmed — I'll ask about it, once",
  "sees": ["streak", "declared_mode", "escalation_state", "storm_state"],
  "stake_shared": true,
  "status_history": [
    {"at": "2026-07-05T18:00:00-04:00", "status": "briefed"},
    {"at": "2026-07-05T18:20:00-04:00", "status": "confirmed"}
  ]
}
```

Mechanics: the witness gets access to one dedicated channel (e.g., a
`#witness` channel on the user's server, or any channel the harness
can post to that the witness actually reads). **Binding permission
rule:** the witness's access MUST be scoped by explicit channel
permissions to the witness channel only — `#lucid` and every thread
under it (where journal lines and observation micro-logs are typed)
must be invisible to the witness role. Witness setup is incomplete,
and `l2_enabled` stays false, until both the confirmation message and
the channel-permission scoping are recorded. Confirmation is the
witness's own message in that channel, recorded verbatim with a
timestamp; until `confirmed_at` is set, `l2_enabled` cannot be turned
on. `stake_shared: true` records that the witness has seen the written
stake (engine §4 — a stake the witness has never seen cannot execute
without renegotiation). The witness sees *only* what the L2 template
carries; they get no access to `~/.lucid/` at all.

**Lifecycle.** A witness may resign at any time. On resignation or
sustained unreachability the contract enters **witness-lapsed state**:
`l2_enabled` goes false, the ladder degrades to L1-only, and `/status`
surfaces "witness lapsed — L2 disarmed" until a replacement completes
the full Day-0 witness flow — briefing, stake shown, confirmation,
channel scoping (engine §4). Every transition (`briefed`, `confirmed`,
`lapsed`, `resigned`, a replacement's re-`confirmed`) appends to
`status_history[]` — the registries precedent
([`../observations.md`](../observations.md) §1): transitions are
recorded, never overwritten. Re-briefing is quarterly, folded into the
stake review (engine §4).

### `storm.json`

```json
{
  "version": 1,
  "clauses": ["clause-1", "clause-2"],
  "windows": [
    {"label": "window-1", "start": "2026-11-02", "end": "2026-11-09"}
  ],
  "duration_days": 14,
  "history": [
    {"at": "2026-07-14T07:05:00-04:00", "event": "declared", "label": "clause-1"},
    {"at": "2026-07-14T09:40:00-04:00", "event": "confirmed", "by": "J.",
     "text": "confirmed — that's real. stake stayed.", "through": "2026-07-28"}
  ],
  "episodes": [
    {"id": "storm_2026_07_14_a", "opener_at": "2026-07-14T07:05:00-04:00"}
  ]
}
```

Clause labels are **opaque** — the words live in the Charter
(`personal/charter.md`, [`../calibration.md`](../calibration.md)
Charter §8); the Engine tree holds labels and dates only, the same
discipline as the capacity limiter tag. The witness confirms against
the Charter text they were shown at Day 0, in the human channel; the
confirmation is recorded verbatim with a timestamp, exactly like
`witness.json`'s. `history[]` events: `declared`, `confirmed`,
`entered` (an ambush window's start date arriving), `renewed` (once,
maximum), `expired`, `ended`. A storm is **standing** when the latest
history state is `confirmed`/`entered`/`renewed` and today ≤ its
`through` date. Entry applies from the declaration forward, never
backward (engine §4); a pending declaration lapses if unconfirmed
within the confirmation window (72 hours).

**Episodes — an addressable name for a storm run, added without touching
history.** A storm is otherwise only ever a *derived* state; an **episode** makes
one run addressable. An episode is the run from a `declared`/`entered` opener
through its terminal event (`ended`/`expired`/`lapsed`), or the still-open
trailing run at the end of the log; a standing bare ambush window that stands
with no `entered` history event of its own is its own episode. `confirmed` and
`renewed` continue a run — they never open one, so a confirmation without a
declaration is not an episode. Episode identity lives in a **new top-level
`episodes[]` index** — `{id, opener_at}` for a run opened by an event, or
`{id, window_label, window_start}` for a bare window — and the id grammar is
`storm_YYYY_MM_DD_<slot>`, dated by the opener's `at` and slotted with
`SlotLabel`: the same per-day slot grammar anchors, insights, and self facts
share.

**Why identity is a sibling index and not a history event.** `StormStanding`
decides purely from `history[len-1]`, and `pendingDeclaredAt`, `standingThrough`,
and `stormStart` all read the last element too — so appending *anything* to
`history[]`, even an id-only adoption event, would move the derived
`status.json`. `episodes[]` is therefore a sibling array that every existing
consumer ignores; adding it changes no derived value. Membership stays derived
from the run rule above, so no existing history event changes and `history[]`
gains nothing. **No stored entry is edited or removed.**

**Minting.** An episode id is minted forward the moment an episode opens (in the
same write as its opener), and `lucid storm backfill-episodes` backfills ids for
existing runs on demand — idempotent, with a `--dry-run` that writes nothing and,
like the anchor backfill, never automatic. `StormStanding` and every derived
`status.json` value are provably **unchanged** by the addition (asserted over a
multi-episode fixture, still-open trailing run included). A future ambush window
is not yet an episode; it becomes one when its start arrives.

**`version: 1` is a signal, not a gate**, on the same terms as `anchors.json`:
stamped on every append, never validated on read, so a `storm.json` written
before `episodes[]`/`version` existed reads exactly as it always did — an older
binary ignores both new fields.

### `profile.json`

```json
{
  "active": "nights",
  "history": [
    {"at": "2026-07-07T21:50:00-04:00", "from": "default", "to": "nights",
     "effective": "2026-07-08"}
  ]
}
```

Switches are sticky and take effect from the `effective` logical day —
always the *next* one, never the day of the switch, so a switch after
tonight's bell cannot move tonight's clocks. Each day record stamps
the profile that governed it; logical-day math for a given day uses
that profile's rollover. Switch frequency is Tier-3 telemetry at the
Retro.

### `anchors.json`

```json
{
  "version": 3,
  "history": [
    {"label": "sobriety", "date": "2026-01-15", "note": "",
     "recorded_at": "2026-01-15T08:04:00-05:00"},
    {"id": "anchor_2026_02_01_a", "label": "gate-30", "date": "2026-02-01",
     "note": "cleared the first gate",
     "recorded_at": "2026-02-01T21:12:00-05:00"},
    {"id": "anchor_2026_02_01_a", "label": "gate-30", "date": "2026-02-01",
     "note": "mistyped label", "recorded_at": "2026-08-04T09:30:00-04:00",
     "state": "sunset"},
    {"id": "anchor_2026_08_07_a", "label": "sobriety", "date": "2026-01-15",
     "note": "", "recorded_at": "2026-01-15T08:04:00-05:00",
     "adopts": "legacy:sobriety", "adopted_at": "2026-08-07T10:15:00-04:00"}
  ]
}
```

A **milestone anchor** is a recorded date the Engine counts a running "days
since" from — a cessation, a gate cleared, any date worth a running day count.
The store is **append-only** and never hand-edited, the same discipline as
`storm.json` and `profile.json`: every verb appends one record to `history[]` and
no record is ever rewritten or removed.

**Every anchor has a kind, and the kind is not stored here.** An anchor is a
`milestone` — the default, and everything this section describes — or a `quit`,
a `limit` or a `habit` ([ADR-0013](../adr/0013-anchor-kinds.md)). The kind, its
parameters and every slip, use, miss, confirm, void and graduation live in the
separate anchor event store (§"Anchor event store (`anchor_events.json`)"),
joined to this file by `id`. An anchor with no `kind` event is a milestone, so a
Ledger with no event store reads exactly as it always has.

**Identity.** An anchor's identity is its `id` — `anchor_YYYY_MM_DD_<slot>`, a
per-day bijective base-26 slot (the same grammar the insights store uses),
minted from the clock and never derived from the label. That makes the **label a
mutable display name**: it can be corrected or renamed without forking the
milestone, and two milestones that reuse a name stay distinct. The first record
above is an **id-less legacy record**: records written before `version: 2` carry
no `id`, are **never rewritten**, and fold under a synthetic `legacy:<label>`
identity derived at read time. That identity is published on the JSON surfaces
(as `"id": "legacy:sobriety"`) but is never written to this file, and a legacy
anchor therefore behaves exactly as it always has.

**The fold: latest record per `id` wins**, at read time by append order
(`recorded_at`), where a record's identity is its `id`, or `legacy:<label>` when
absent. A correction whose date is *earlier* than the original still supersedes
it — a typo fix and a genuine reset are the same append-only operation.
`anchor add <label> <date> [note]` appends under the id of the **active** anchor
holding that label when there is one, and mints a new id when there is not (a
first use, or a label whose only holder was sunset). `date` is a civil
`YYYY-MM-DD` and is backdatable (any past or future date). Days-since counts
whole logical days from the folded anchor date, **anchor day = 0**, at the
chain's rollover boundary; it is **surfaced by `metrics`, never stored** here.

**Adoption — a legacy anchor acquires a written id by appending, never by
rewriting.** A pre-id anchor folds under a read-time `legacy:<label>` identity
that is never written (above), which is not a durable target: a link, or any
future reference, keyed on it would dangle. `lucid anchor backfill-ids` fixes
that **without touching a single stored byte** — it *appends* one **adoption
record** per legacy identity, carrying a freshly minted `anchor_YYYY_MM_DD_<slot>`
`id`, an `adopts: "legacy:<label>"` field naming the identity it takes over, and
an `adopted_at` timestamp (the real backfill time). Every other field —
`label`, `date`, `note`, `state`, and **`recorded_at` especially** — is copied
**verbatim** from the folded winner of the identity it adopts. `recorded_at` must
be verbatim because `LatestSunsetFor` / `SunsetDate` read it: an adoption stamped
with today's `recorded_at` would silently restate when a milestone was retired,
so the real append time lives in `adopted_at` instead. (In the example above, the
last record adopts `legacy:sobriety`: it copies `sobriety`'s `date` and
`recorded_at` verbatim and carries the minted id `anchor_2026_08_07_a`.)

**The remap is a pre-pass inside the fold, and `AnchorIdentity` is unchanged.**
`LatestAnchors` first scans `history[]` for records carrying `adopts`, building a
`legacy:<label> → minted id` map (last adoption wins if an identity somehow
carries two), then folds with that remap applied — so a pre-id record and the
adoption record that took it over fold as **one** anchor rather than forking into
two. `AnchorIdentity` stays a pure per-record function and its `legacy:` branch
stays **forever**: pre-id records stay on disk by design, so nothing about them
is ever rewritten. Everything downstream (`FindActiveAnchor`, `LatestSunsetFor`,
`AnchorLabels`, `ProjectAnchor`, metrics) reads the folded set and needs no
change.

**`lucid anchor backfill-ids` is explicit, idempotent, and never automatic.** It
appends the adoption records on demand — with a `--dry-run` that prints exactly
what it would append and writes nothing — because a silent write to primary
testimony on upgrade is precisely what Lucid's no-silent-writes posture forbids.
Re-running it appends nothing (every legacy identity is already adopted), and the
mint threads the growing log so same-day mints never collide. Until it is run,
`anchor:<…>` resolution for a pre-id record is refused with this command named as
the remedy. **No stored entry is edited or removed by any of this.**

**Visible surface change (documented, not slipped in).** After the backfill,
`ProjectAnchor` and `lucid metrics --json` publish the **minted** id for an
adopted anchor instead of `legacy:<label>`. That synthetic id was read-time-only
and never durable, so the change is safe — but it is a real output change and is
called out here rather than discovered later.

**`state`** discriminates a retirement, exactly as `event` does in `storm.json`:
absent ⇒ active; `"sunset"` ⇒ retired. `anchor sunset <label> [reason]` appends a
full mirror of the anchor plus `state: "sunset"`, carrying the superseded
anchor's `date` (never the retirement day) and the optional reason in `note`. A
retired anchor is filtered out of the counting surfaces — `metrics` prose,
`anchors[]`, and the witness watch-outs — while staying in `history[]` in full
and surfacing in `metrics --json` → `anchors_sunset[]`. The witness watch-outs
are **kind-aware**: a `limit` over its allowance, a `habit` miss run of at least
`miss_alarm` that overlaps the report week, and one count-only line per anchor
with slips this week; a `milestone` and a long `quit` run produce nothing,
because a long run is a win, not a worry (§"Anchor event store" → "Witness
watch-outs and anchor detail"). A **graduation** (`anchor sunset --graduated`)
is this same plain sunset record — never a new `state` value, because an older
binary's `IsSunset` reads any other state as active; only its paired `graduate`
event in the event store moves the anchor from `anchors_sunset[]` to
`anchors_graduated[]`.
`anchor rename <label> <new-label>` appends under the **same id** with a new
label; against a legacy anchor it writes a **pair** in one write — an id-less
sunset stub retiring `legacy:<old-label>`, plus a new id-bearing record carrying
the original date and note forward.

**`version: 3` is a signal, not a gate.** It is stamped on every append (bumped
from 2 when the adoption fields shipped), so the number in a file is always true
of that file, but nothing validates it on read — a `version: 1` or `version: 2`
file still reads exactly as it always did. The honest consequence of having no
gate: an older binary reading a v3 file ignores `adopts` / `adopted_at` and would
fold a backfilled anchor as **two** identities (the legacy record and its
adoption), just as it already ignores `state` and would keep counting a sunset
anchor on its metrics surface.

**Anchor kinds change neither this record shape nor the version:
`AnchorVersion = 3` stays.** No record gains a field and no sibling array is
added. The reason
is the older binary's *write*: its `anchor add|rename|sunset` decodes this file
into its own v3 struct, appends, and re-marshals the whole file, so any field or
array it does not know is silently erased on its next write — a `kind` field
here would revert every quit, limit and habit to a milestone and orphan its
events. Kinds and events therefore live in their own file, which an older binary
never opens. The only records anchor kinds append here are **ordinary v3
records** an older binary already understands:

* the **compat re-date** — a full mirror of the anchor's folded definition with
  only `date` moved — that `anchor slip`, an `anchor void` of a slip (the
  *restoring* record), or an `anchor kind` to `quit` past a later use-day
  appends, so an older binary counting from this file never shows a quit run
  longer than the truth;
* the **definition** `anchor add --kind` writes, the same record a plain
  `anchor add` writes;
* the **plain sunset** `anchor sunset --graduated` writes (above).

Each is one half of a **pair** with an event in the store, and follows the
conservative per-verb write order (§"Anchor event store" → "Two-file verbs and
the write order"). The pair is identified by an append-only **pair intent**
holding the exact planned event and the exact planned v3 record, as well as by
the shared anchor id and `recorded_at` — a timestamp alone is never treated as
proof. In the new binary's quit fold a compat re-date in a **completed** pair is
a reading of the slip, not a correction, so it never moves the quit's start; one
whose pair is still **pending** stays a conservative floor on the effective
date. An older binary reads every one of these records as an ordinary
correction or sunset — the meaning it has always had.

No capacity, journal, or Mirror data ever rides this file — it holds ids, labels
and dates only. It no longer shares the storm clause labels' discipline: an
anchor is addressed by a stable id, while a storm clause is still identified by
its label string. Because the store lives in the engine tree (not a Mirror
tree), it carries no schema-kind entry in the `validate` schema sweep; `lucid
validate` checks it instead through its own anchors check — this file's record
schema, the event store's schema, and the cross-file pairs (§"Anchor event
store" → "Reads, older binaries, validation and backup").

### Anchor event store (`anchor_events.json`)

```json
{
  "version": 1,
  "events": [
    {"id": "anchorev_2026_01_02_a", "anchor_id": "anchor_2026_01_01_a",
     "type": "kind", "day": "2026-01-02",
     "recorded_at": "2026-01-02T08:30:00-05:00", "kind": "quit"},
    {"id": "anchorev_2026_03_02_a", "anchor_id": "anchor_2026_01_05_a",
     "type": "kind", "day": "2026-03-02",
     "recorded_at": "2026-03-02T21:10:00-05:00",
     "kind": "limit", "allowance": 4, "window_days": 90},
    {"id": "anchorev_2026_03_14_a", "anchor_id": "anchor_2026_01_05_a",
     "type": "use", "day": "2026-03-14",
     "recorded_at": "2026-03-15T00:40:00-04:00",
     "intent": "planned", "note": "a slice of cake at a birthday"},
    {"id": "anchorev_2026_04_02_a", "anchor_id": "anchor_2026_01_01_a",
     "type": "slip", "day": "2026-04-01",
     "recorded_at": "2026-04-02T08:15:00-04:00"}
  ],
  "pair_intents": [
    {"order": "compat_first",
     "event": {"id": "anchorev_2026_04_02_a",
               "anchor_id": "anchor_2026_01_01_a", "type": "slip",
               "day": "2026-04-01",
               "recorded_at": "2026-04-02T08:15:00-04:00"},
     "compat": {"id": "anchor_2026_01_01_a", "label": "sobriety",
                "date": "2026-04-01", "note": "",
                "recorded_at": "2026-04-02T08:15:00-04:00"}}
  ]
}
```

Four events and one pair intent (all synthetic): `sobriety` reclassified as a
`quit` (one event, no intent — nothing to re-date); `late-night-snacks`
reclassified as a `limit` of 4 use-days per 90; a planned use of that allowance,
logged after midnight and so landing on the evening's logical day; and a slip on
`sobriety`, written as a pair whose compat re-date (the intent's `compat`) went
to `anchors.json` first.

The anchor event store holds every anchor's **kind** and every fact logged
against it — `kind`, `slip`, `use`, `miss`, `confirm`, `void` and `graduate`
events — beside `anchors.json`, which keeps the definitions
([ADR-0013](../adr/0013-anchor-kinds.md)). The split is forced by the
older-binary write described under §"`anchors.json`": anything an older binary
must not erase cannot live in a file it re-marshals. The store is
**append-only** and never hand-edited, owned by `internal/storage` (P3) like
every other engine file, and read by the pure Engine as a value: every number
below is a function of the two files, the current logical day and two
thresholds — no clock, no disk, no model (P9).

#### Envelope, records and ids

**The envelope.** `{"version": 1, "events": [...]}`, plus an optional
append-only `pair_intents` array that is **absent when unused** (§"Two-file
verbs and the write order" below). `const AnchorEventsVersion = 1` is a constant
of its own, separate from `AnchorVersion`, stamped on every write.

**Writes.** Each write reads the whole file, appends, and writes once through
the same atomic temp-file-and-rename discipline `anchors.json` uses, with
owner-only permissions. No event or intent is ever rewritten or removed. The
file is **never scaffolded**: `init` does not create it, the first event write
does, and a Ledger with no kinds therefore has no store and reads exactly as it
always has. A JSON envelope rather than JSONL because every write already needs
the whole store (to mint a slot and to check the anchor's current kind), so a
line append would save nothing, and the atomic rename rules out a torn record.

**The event record.** Every event carries:

| Field | Meaning |
|-------|---------|
| `id` | `anchorev_YYYY_MM_DD_<slot>` — the receipt |
| `anchor_id` | The target anchor's minted `anchor_…` id |
| `type` | `kind`, `slip`, `use`, `miss`, `confirm`, `void` or `graduate` |
| `day` | The civil logical day the event is about (for `confirm`, the through-day) |
| `recorded_at` | RFC3339 append time, local offset |
| `note` | Optional free text (`--note-file`, or `--reason-file` for `void`) |

Each type adds its own fields:

| Type | Adds | Written by |
|------|------|------------|
| `kind` | `kind` (`milestone`, `quit`, `limit`, `habit`); `allowance` and `window_days` for a `limit` (both positive integers, both required); `window_days` optionally for a `habit` (default 30) | `anchor add --kind`, `anchor kind` |
| `slip` | — | `anchor slip` |
| `use` | `intent`: `planned` or `slip` (required) | `anchor use` |
| `miss` | — | `anchor miss` |
| `confirm` | `from`: the first day of the attested span (`day` is its last) | `anchor confirm` |
| `void` | `target`: the voided event's id | `anchor void` |
| `graduate` | — | `anchor sunset --graduated` |

Unknown fields are tolerated on read, so a later version can add a field
without breaking this one.

**Event ids.** `anchorev_YYYY_MM_DD_<slot>` uses the shared per-day slot grammar
(`SlotLabel`), minted from the write's logical day exactly as `NextAnchorID`
mints anchor ids. The mint scans every event **and** every id a pair intent has
reserved, so an id is never reissued — not even one whose operation never
completed. The `anchorev_` prefix can never collide with an `anchor_` id or an
`anchor:<id>` link subject. Every write returns its event id as the receipt, in
the ack and in `--json`.

**Event days.** `slip`, `use` and `miss` default to the **current logical day**
of the active profile — the day `metrics` counts against — so a write after
midnight and before the rollover lands on the evening's day; `--day` reads the
shared date grammar ([`../usage/commands.md`](../usage/commands.md#backdating-with---day)).
A future day or a partial date is rejected, and so is a day before the anchor's
start (the quit start below for a `quit`, the anchor date for every other kind).
A `confirm`'s `day` is its through-day. A `kind`, `void` or `graduate` event
carries the logical day of the write — for a `kind` event that is the day the
anchor *became* that kind, never its anchor date, which is what keeps a first
`confirm` from claiming days before the cutover.

#### The join and the fold

**The join.** Events join `anchors.json` by `anchor_id` against the **folded**
identity (adoption remap included). An event whose anchor is unknown is ignored
by the fold and reported by `lucid validate`. Events need a **durable** id: a
pre-id anchor folds under the read-time `legacy:<label>` identity, which is
never written, so every event and reclassify on one is rejected with the remedy
`lucid anchor backfill-ids` — there is no implicit adoption write. An event aimed
at a sunset anchor is rejected; the one exception is a `void` of that anchor's
`graduate` event, which leaves the plain sunset in place so the anchor reads as
an ordinary retirement.

**The kind.** The latest `kind` event wins; `void` never targets a `kind`
event, so that is simply the latest one. With **no `kind` event the anchor is a
`milestone`**. A limit's or habit's parameters are those of its latest `kind`
event, applied across the whole window (the dated `kind` events keep the
history; the v1 math does not read it per day).

**Which facts each kind reads.** Events are facts that outlive a kind change.
Quit slips and limit uses are both **use-days**, and a quit slip reads as a
`slip`-intent use under `limit`. `milestone` reads no events; `quit` and `limit`
read use-days; `habit` reads misses and confirms. Every other event stays in
the store and in the detail view but drops out of the math, and converting back
brings it back in. Voided events drop out of all math. Every day difference
uses the UTC-civil discipline `DaysSince` already uses, so a DST transition
never shifts a count, and "today" is the current logical day at the chain's
rollover boundary.

**`milestone`.** Identical to today's days-since math, from the folded date.

**`quit`.**

* **Start** — the date of the latest definition record that **changes the
  date** and is **not** the compat half of a *completed* pair. An `anchor add`
  correction therefore still moves the start exactly as it always has, while a
  slip's own re-date does not. A record that **keeps** the date it follows — a
  rename, a plain sunset, an id adoption, whichever binary wrote it — never
  moves the start: after a slip such a record carries the re-dated date
  forward, and reading it as a correction would quietly drop the slip and
  shorten the longest run. A compat re-date whose pair is still *pending* stays
  a conservative floor on the effective date (below).
* **Use-days** — the distinct days of non-voided `slip` events, plus the
  distinct days of non-voided `use` events of either intent (which can only have
  been logged while the anchor was a `limit`).
* **Current run** — `DaysSince(max(start, latest use-day), today)`. With no
  slips this is exactly a milestone's count.
* **Longest run** — the maximum over the runs between successive use-days,
  counted from the start, the current run included.
* **Slips** — the number of distinct use-days on or after the start (a slip
  logged on the start day itself is still a slip); **`slips_7d`** — the slip
  use-days in the last seven logical days (the witness report's week).

A synthetic example: `sobriety` starts 2026-01-01 and slips on 2026-03-01; on
2026-04-10 it reads `Days since sobriety: 40 (longest 59, 1 slip).`

**`limit`.**

* **Window** — the `window_days` logical days ending today, inclusive, clipped
  at the anchor date.
* **Used** — the distinct use-days in the window; several uses on one day are
  one use-day, and planned and slip uses both spend allowance. **Remaining** —
  `max(0, allowance − used)`.
* **Planned and slip counts** — use-days in the window by intent; a day logged
  with both intents counts as a slip day, the honest direction.
* **Days since the last use**, **`over_allowance`** when used > allowance
  (using exactly the allowance is fine), and **`slips_7d`**, the note-free count
  of slip use-days in the last seven logical days.

A **planned use never resets or zeroes anything**: it is spend, the Engine's
error-budget idea ([`../calibration.md`](../calibration.md)) applied to a goal.
A synthetic example: `Late-night snacks: 1 of 4 in the last 90 days (1 planned,
0 slips); 7 days since last.`

**`habit`.**

* **Window** — `window_days` (default 30) logical days ending today, clipped at
  the anchor date.
* **Accounted days** — the union of non-voided confirm spans and logged miss
  days inside the window, never before the anchor date. **A miss always wins
  over a confirm** on the same day; a confirm never erases a miss.
* **Kept days** — accounted days minus miss days. **An unconfirmed day is never
  kept**; a habit with no accounted days reads "no accounted days yet", never a
  hollow 100%.
* **`current_miss_run`** — consecutive *calendar* miss days ending at the latest
  accounted day; **`alarm`** when it reaches `miss_alarm`. A single isolated
  miss never alarms. **`miss_run_7d`** — the longest such run that touches the
  last seven logical days, at its full length — is what the witness habit
  watch-out reads, so a mid-week slide stays visible after a recovery.
* **`last_confirmed`** — the latest through-day of a non-voided confirm.
* **`graduation_eligible`** — true once the accounted days since the last run
  of at least `miss_alarm` consecutive misses ended — or since the anchor became
  a habit, if no run ever reached it — number at least `graduation_days`. This
  reads the anchor's full history, so an old rough patch does not disqualify a
  habit forever. Eligibility is only surfaced; graduating is always the
  person's call.

A synthetic example:
`Morning walk: running 83 days (since 2025-11-03); last 30 days: kept 29 of 30 accounted, 1 miss; confirmed through 2026-01-24.`
The prose line leads with **days running** — the anchor's `days_since`, elapsed
time since its date, labeled as such and never merged with the kept figure —
and names the kept figure's window; the full copy is in
[`../usage/commands.md`](../usage/commands.md#metrics) (§metrics).

**The `confirm` span.** The through-day defaults to yesterday's logical day,
because today is not over. The span starts at the **later** of the day after the
habit's last non-voided confirm and the day of its latest `habit` `kind` event,
so by default nothing before the cutover is claimed, a skipped week self-heals
(the next confirm covers the gap), and after a kind change away and back the
span starts at the later of the two. `--from` widens the start to an earlier
stretch the person vouches for; it is rejected before the anchor date or after
the through-day. Days in the span with no logged miss count as kept; a miss
inside the span stays a miss. An empty span writes nothing.

**No conversion makes a quit run longer than the truth.** A reclassify to
`quit` counts from the latest use-day when it is later than the folded date, and
appends a compat re-date so an older binary agrees. A recent planned use can
never hide inside a long quit run.

**Thresholds.** `miss_alarm` (default 2, at least 2 — a 1 would let a single
isolated miss alarm) and `graduation_days` (default 90, at least 1) come from
the `lucid.json` `anchors` block and are passed into the Engine as parameters;
there is no per-anchor override in v1. An older binary ignores the block.

**The metrics projection.** `lucid metrics --json` keeps every existing field
and its meaning: `date` and `days_since` stay the plain fold of `anchors.json`
(latest record wins, compat records included), so for a quit after a slip they
show the re-dated effective date — exactly what an older binary shows. Each
anchor row adds `kind` and one kind object (`quit`, `limit` or `habit`) holding
the numbers above (every field is listed in
[`../usage/commands.md`](../usage/commands.md#metrics) §metrics); **both are
omitted for a milestone**, so absence means
milestone, the same convention `state` uses. Graduated anchors appear in
`anchors_graduated[]`, separate from `anchors_sunset[]`, omitted when empty. A
Ledger with no kind events is byte-identical to today, `--json` included.

#### Two-file verbs and the write order

Five verbs write both files: `anchor add --kind`, `anchor slip`, `anchor void`
of a slip, `anchor kind` to `quit` past a later use-day, and `anchor sunset
--graduated`. Each file write is atomic on its own, but two files cannot be
written atomically together, so these verbs follow one documented protocol. A
crash between the halves must never flatter either binary's reading of a quit,
and the incomplete pair must be detectable.

**Pair intents.** Before either domain half, the verb appends an
**`AnchorPairIntent`** to `pair_intents`: it reserves the event id and holds the
**exact planned event**, the **exact planned v3 record**, and the `order`
(`compat_first` or `event_first`). It is transaction metadata, not an applied
fact — the fold never reads its nested event as an event. Intents are never
edited or removed; completion is inferred from the presence of the matching
event and the exact matching record in `anchors.json`. The ordinary event and its
v3 record also share the anchor id and `recorded_at`, but a timestamp alone is
never treated as proof of a pair. A slip, a slip void or a reclassify whose v3
half would not move the quit's effective date has no second half: it writes
its event alone, with no intent.

**The write order, per verb:**

| Verb | Order | A crash after the first half leaves… |
|------|-------|----------------------------------------|
| `anchor slip` | intent → **v3 re-date** → `slip` event (`compat_first`) | Both binaries counting from the re-date: honest, never longer. The slip count catches up on retry. |
| `anchor kind` to `quit` (later use-day) | intent → **v3 re-date** → `kind` event (`compat_first`) | The older binary counting from the re-date; the new binary still reading the old kind, with the pending re-date as a floor. |
| `anchor void` of a slip | intent → **`void` event** → restoring v3 record (`event_first`) | The new binary reading the corrected history; the older binary still counting from the slip — shorter, never longer. |
| `anchor add --kind` | intent → **`kind` event** → v3 definition (`event_first`) | A dangling kind event the fold ignores; no anchor appears in either binary. |
| `anchor sunset --graduated` | intent → **`graduate` event** → plain v3 sunset (`event_first`) | An active anchor in both binaries; the new binary recognizes a graduation only once its sunset half exists. |

The re-date a slip writes uses the effective date `max(start, every accepted
use-day including the proposed slip)`, so a backdated slip can never move the
effective date backwards. A compat re-date or restoring record is a full mirror
of the anchor's folded definition with only `date` changed.

**Pending reads are conservative.** An unpaired compat re-date stays an
effective-date **floor** for the new binary: the fold does not ignore it merely
because an intent identifies it as compat. Only a *completed* pair is excluded
from the quit start. An event-first slip void may let the new binary show the
corrected, longer run while the older binary stays conservatively shorter.

**Recovery is explicit, never automatic.** `lucid validate` reports an
intent-only, compat-only or event-only operation as a **half-written pair**,
naming the reserved event id and the missing half, and never repairs it. The
remedy is to retry the original verb: a matching explicit retry reuses the
intent and its event id, appends only the missing halves in the recorded order,
and returns the original receipt. An older binary may write between the interruption and the retry — a
correction, a rename, a sunset — and the retry never lets the planned halves turn
stale into a flattering reading. It still appends them exactly, because that is
what completes the pair; then, when the anchor's newest record would carry an
older date than its honest effective date, or a label and note an older-binary
rename replaced, it appends **one more plain v3 record** carrying the current
label and note and the later of the two dates. That record never moves a quit
start: it only ever changes the date when an older-binary correction moved the
start past the planned date, and then it carries exactly that start. A retry on
an anchor an older binary has since sunset is rejected (An-6), because a
restoring record would quietly bring it back. An `add --kind` whose reserved
anchor id an older binary has since minted for another anchor can't be
finished: the fold ignores the event half of an unfinished `add --kind` on
whichever anchor holds that id, it blocks no change to that anchor,
`lucid validate` names it (as a warning: nothing reads it and no retry can
finish it), and the label is free to record again. A *different* mutation of that anchor is
rejected while its pair is incomplete, so a provisional floor can never turn
into an ordinary correction by accident.

**The crash proof.** Tests fault-inject before and after every atomic boundary
(the intent, the first half, the second half) of every two-file verb. At each
point both the current fold and an **anchors-only v3 fold** (today's
`LatestAnchors` path, standing in for an older binary) must read a quit run no
longer than the accepted history permits — completed and pending operations,
backdated and duplicate-day slips, and an older-binary write landing between an
interrupted operation and its recovery included.

#### Reads, older binaries, validation and backup

**Reads are tolerant and ungated.** A missing file is the empty log — the same
tolerance `self.json` has — and is not an error. `version` is **a signal, not a
gate**: nothing checks it on read, on the same terms as `anchors.json`. A record
that fails validation (an unknown type, a malformed day, a missing per-type
field) is skipped by the fold and reported by `lucid validate`; an unparseable
file is a loud read error that names the file. Nothing on any read path panics.

**Older binaries.** An older binary never opens this file — nothing in Lucid
enumerates `engine/` and rejects unknown files — and ignores the new
`lucid.json` keys. It degrades per kind, and nothing is corrupted: a `milestone`
reads exactly the same; a `quit` reads days since its last slip, through the
compat re-date; a `limit` or `habit` reads plain days since its anchor date; a
graduated anchor reads as an ordinary sunset. Its writes touch `anchors.json`
only and append pure v3 records the new fold reads as corrections (or, for a
rename or sunset that keeps the date, as no change to a quit's start), so every
kind, event and pair intent here survives them.

**Validation.** `lucid validate` covers anchors for the first time, through
storage reads and never by opening the Ledger itself: an `anchors.json` schema
check (a non-empty label, a civil date, `state` empty or `sunset`, a version of
at least 1); this store's schema (the id grammar, a known type, a civil `day`,
an RFC3339 `recorded_at`, the per-type fields above); and cross-file checks —
every event targets a known minted anchor id, kind parameters are valid, `void`
targets exist and are legal, and every pair intent is whole. A hand-corrupted
record is reported and never panics; a missing store is clean, and a Ledger
with only today's v1, v2 and v3 shapes validates with no new findings. It runs
as its own `anchors` check, beside `schema`, and each defect is one finding
naming the file, the record or event id and the rule that fired. A file that is
not valid JSON is one finding naming the file, and the cross-file checks are
skipped until it reads. A half-written pair names its reserved event id, the
missing half and the command to retry (`anchorev_2026_04_02_a: half-written pair
— the slip event is missing; retry lucid anchor slip sobriety --day
2026-04-01`). Every finding is an error except one: an unfinished `add --kind`
whose reserved anchor id is already recorded can never be finished and nothing
reads it, so it is named as a warning rather than failing the sweep forever.
The rejections and findings are listed in [`error-states.md`](error-states.md)
§"Anchors".

**Backup.** `lucid backup` archives `engine/` whole, so the store is in every
archive; a downgrade leaves it intact and a later re-upgrade reads it again.

#### Witness watch-outs and anchor detail

**The watch-outs are kind-aware**, and the rule holds in both detail modes:

* **`limit`** — over allowance (`<label>: over allowance — N of A in the last W
  days.`);
* **`habit`** — a miss run of at least `miss_alarm` that overlaps the report
  week (`<label>: missed N days in a row.`), so a mid-week slide is not hidden by
  a recovery before Sunday;
* **slips this week** — one count-only line per anchor with any non-voided quit
  `slip` or limit `use --slip` dated in the report week (`1 slip on <label> this
  week`, `N slips on <label> this week`), with no date and no note; planned uses
  never produce a line;
* **`milestone`, and a long `quit` run** — nothing. A long run is a win, not a
  worry.

These lines read only the metrics projection's note-free numbers.

**Summary is the default** (`witness_report.anchor_detail: summary`): the report
carries only the watch-out lines, with no note and no event date, and no
note-reaching reader is wired, so the witness package's by-construction privacy
holds exactly as before. **Full is a global, explicit opt-in**
(`witness_report.anchor_detail: full`): code renders a deterministic, verbatim
**Anchor detail** section holding each anchor's events for the report week —
dates, intent, slips, misses, confirms and notes. It is **never sent to the
model** (it is not part of the compose body or the report digest, so the model
input is byte-identical with the detail on and off), **never scanned** (it is
the person's own words and they opted in), and **survives the metrics-only
fallback**. Full detail is **lossless**: it is rendered into an ordered batch of
embeds within the existing field, field-count and total-character budgets, and
overflow continues in additional numbered messages through the existing
delivery path — no new transport, destination or schedule, and no "and N more"
omission. Every part is read back; the scheduled runner records per-part
progress in the existing witness receipt, a retry resumes the frozen batch
without re-sending verified parts, and the week is marked delivered only once
every part has verified. This is the witness package's one deliberate, opt-in
exception to its by-construction privacy. Any other `anchor_detail` value is a
config error naming the two; an older binary ignores the key and reads as
summary.

### `self.json`

```json
{
  "version": 1,
  "history": [
    {"id": "self_2026_08_05_a", "key": "identity.generation",
     "value": "elder millennial",
     "recorded_at": "2026-08-05T09:12:00-04:00"},
    {"id": "self_2026_08_05_b", "key": "misc.handedness", "value": "left",
     "recorded_at": "2026-08-05T09:13:00-04:00"},
    {"id": "self_2026_08_05_b", "key": "body.handedness", "value": "left",
     "recorded_at": "2026-08-06T18:40:00-04:00"},
    {"id": "self_2026_08_05_b", "key": "body.handedness",
     "value": "ambidextrous", "note": "throws right",
     "recorded_at": "2026-09-01T07:55:00-04:00"},
    {"id": "self_2026_08_05_c", "key": "pref.editor", "value": "vim",
     "since": "2014-09", "recorded_at": "2026-08-05T09:15:00-04:00"},
    {"id": "self_2026_08_05_c", "key": "pref.editor", "value": "vim",
     "since": "2014-09", "note": "switched years ago",
     "recorded_at": "2026-09-02T11:20:00-04:00", "state": "retired"}
  ]
}
```

Six records, four shapes: a **plain fact** (`…_a`); a **move** (`…_b`'s second
record — same id, new key, `misc.handedness` re-filed as `body.handedness`); a
**correction** (`…_b`'s third — same id, same key, new value); and a
**retirement** (`…_c`'s second, carrying `state`). The whole file is those four
shapes repeated.

A **self fact** is a durable, atemporal attribute of the subject — a *semantic*
record, where every other store in the Ledger is episodic. The store is
**append-only** and never hand-edited, the same discipline as `anchors.json`,
`storm.json` and `profile.json`: every verb appends one record to `history[]` and
no record is ever rewritten or removed.

**Identity.** A fact's identity is its `id` — `self_YYYY_MM_DD_<slot>`, the same
per-day bijective base-26 slot grammar the insights and anchors stores use,
minted from the clock and never derived from the key. That makes the **key a
mutable address**: it can be corrected or re-categorized without forking the
fact, and two facts that reuse a key over time stay distinct. Slots are minted
against the **whole history**, never the folded set, so a slot held by a retired
fact is never reissued.

**Every record carries an `id`.** This store is `version: 1` from birth, so
unlike `anchors.json` there is no id-less legacy era, no synthetic
`legacy:<key>` identity, and no read-time identity derivation — a record without
an `id` is corrupt, not historical. The consequence worth naming: `self move`
is **always exactly one append**, where `anchor rename` writes a pair against a
legacy anchor. That asymmetry is deliberate and should not be "restored".

**The fold: latest record per `id` wins**, at read time by **append order** — not
by `recorded_at`. A correction whose timestamp is *earlier* than the record it
supersedes still supersedes it, exactly as in `anchors.json`. `self set <key>
<value>` appends under the id of the **active** fact holding that key when there
is one, and mints a new id when there is not (a first use, or a key whose only
holder was retired). At most one *active* fact may hold a given key, which is
what lets every verb take a bare key.

**Keys** are `<namespace>.<leaf>` over a fixed five-namespace set — `identity.`,
`constraint.`, `body.`, `pref.`, `misc.` — with a free-form leaf that may itself
contain dots (`constraint.diet.dairy`). That list is in **priority order**, and
it is one ordered list serving two consumers: the grouped render and the `/ask`
grounding slice. An unrecognized namespace is rejected with the accepted set
**and** `misc.` named, and is never auto-coerced into `misc.` — silently filing a
typo'd namespace would hide the typo permanently. `value` is a single free-text
string bounded by a 500-rune ceiling, which is a size guard on the grounding
slice rather than a judgment about content.

**`since`** is an optional origin, not a logical day: it is the one date field in
the tree with **no** required value and with partial dates stored **as typed**
(`"1985"`, `"2014-09"`), because nothing here derives a day count from it. See
[`../usage/commands.md`](../usage/commands.md#deliberate-divergences).

**`state`** discriminates a retirement, exactly as it does in `anchors.json`:
absent ⇒ active; `"retired"` ⇒ retired. `self retire <key> [reason]` appends a
full mirror of the fact plus `state: "retired"`, carrying the superseded fact's
own `since` (never the retirement day) and the optional reason in `note`. A
retired fact leaves the rendered profile, the `--json` profile, and the `/ask`
grounding slice, while staying in `history[]` in full and readable under
`--history`.

**Two divergences from the sibling stores, both deliberate.**

1. **The append is atomic** — written to a temp file in the same directory,
   synced, then `rename`d over the target. Every other engine file is written
   with a plain whole-file write, which for a few dozen anchors costs a small,
   recoverable loss. This store is expected to accumulate for years, so a torn
   write would cost the user's entire self-profile. Making the rest of the tree
   atomic is a separate change; this file does not wait for it.
2. **The read tolerates an absent file** — a missing `self.json` reads as an
   empty log, not an error, where `read_anchors` requires the file. `/ask`
   grounds on this store and is contractually read-only, so it must never
   scaffold in order to answer; a Ledger created before this store existed has to
   keep answering. A *malformed* file is still a hard error — a corrupt
   self-profile must surface, never silently reset.

**Grounding.** `/ask` reads the active profile as a router-composed slice capped
at `self_facts_cap` (default 40) and ordered by **namespace priority then key,
never recency** — recency is meaningless for an atemporal fact and would evict a
year-one blood type in favor of last week's trivia. Facts enter the slice by id,
so a self fact can be *cited* (`kind: "self_fact"`) and not merely read; see
[`agent-contracts.md`](agent-contracts.md) §3 `reflection.answer_grounded`. The
file itself is inside the Sanctuary (`engine/`), so an agent reaches these facts
only as composed values — never by path.

**`version: 1` is a signal, not a gate**, on the same terms as `anchors.json`: it
is stamped on every append, but nothing validates it on read. Because the store
lives in the engine tree (not a Mirror tree), it carries no schema-kind entry in
the `validate` schema sweep — the same as `anchors.json`, and for the same
reason.

### Day record — `engine/days/YYYY/MM/day_YYYY_MM_DD.json`

```json
{
  "day_id": "day_2026_07_02",
  "logical_date": "2026-07-02",
  "recorded_at": "2026-07-02T22:41:10-04:00",
  "mode": "green",
  "mode_declared_at": "2026-07-02T14:02:00-04:00",
  "links": {"journal": "done", "dock": "done", "read": "floor"},
  "floor_day": true,
  "completed": true,
  "missed": false,
  "partial": false,
  "capacity": 3,
  "limiter_tag": "wrist",
  "raw_entry_id": "raw_2026_07_02_22_41",
  "backfilled": false,
  "storm": false,
  "profile": "default",
  "corrections": []
}
```

`capacity` and `limiter_tag` live **only** here — they are Engine
telemetry, never surfaced to the witness view, and the raw journal
entry does not repeat them (the Mirror can be granted them later as a
consented covariate; the MVP keeps the trees separate).
`raw_entry_id` is a pointer for provenance; the Engine never
dereferences it. `backfilled: true` marks a record created after its
logical day by `/closeout backfill`. `storm` and `profile` are stamped
by the system from the standing state that governed that logical day
(`storm.json`, `profile.json`) — a backfilled record derives them from
the history for the *target* day, deterministically, never from the
state at write time.

**`corrections[]` (binding schema).** A day record is never rewritten;
it is corrected by appended entries, folded at read time:

```json
{"at": "<ISO local>", "fields": { ...subset... }, "reason": "<short text>", "source": "user"}
```

* **Foldable fields:** `links`, `floor_day`, `completed`, `missed`,
  `partial`, `capacity`, `limiter_tag`, `raw_entry_id`. **Never
  foldable:** `mode`, `mode_declared_at`, `logical_date`, `day_id`,
  `recorded_at`, `storm`, `profile` — the mode is fixed at the bell
  and there is no retroactive amendment (engine §2), and storm/profile
  stamps derive deterministically from their history files, so
  correcting them by hand would let a bad week's narrator re-file a
  miss as weather; a correction attempting an immutable field is
  rejected (see §"Error states"). `/mode --day` does **not** change
  this: it is a guarded fill that writes `mode`/`mode_declared_at` only
  when both are empty, so a *declared* mode is still unreachable from
  every path, and `mode` stays off the foldable list.
* **Fold rule:** apply corrections in array order; last write per
  field wins; the original record body stays byte-identical.
* All derived state — streak, adherence, error-budget burn,
  `escalation_state` — is computed over **folded** records, so a
  valid backfill automatically restores a streak and refunds budget.
  Sends are never unsent; records are corrected; projections
  recompute.

### `status.json` (derived — rebuildable from `days/` + `chain.json`)

Current streak · longest streak · rolling 7-day and 30-day adherence
(each day scored against its **declared mode**: a Yellow day executed
at floors scores 1.0; an undeclared storm day scores against Red —
engine §4) · floor-day ratio · raw days-accounted (days
with any record in the window) · error-budget burn (isolated misses in
the trailing 30 days vs budget — storm misses spend nothing) ·
consecutive-miss count (resets at storm exit) ·
`escalation_state` (`none | l1_fired | l2_fired`) ·
`storm_state` (`none`, or `standing` with its `through` date) ·
active profile · days to next gate.
Adherence never travels alone: every surface that shows it —
status.json and `/status` included — co-presents the floor-day ratio
and raw days-accounted (the **honest-number pairing**, engine §3).
Floors count as full completions for all consequence math; the co-numbers,
never a score, are what keep the record honest without shame math.
`rebuild_engine_status` computes over **folded** day records
(`corrections[]` applied in array order) plus the storm and profile
histories. Deleting this file and rerunning `rebuild_engine_status`
must reproduce it byte-for-byte from the same inputs — corrections,
storms, and profile switches included.

## The tripwire (scheduled job)

One scheduled job, morning-run at the active profile's
`tripwire_time` (default 06:00) — the bell prompt, its evening
sibling, likewise posts at the active profile's `bell_time`. In a
**standalone install** this job runs on the **go-flywheel** durable
scheduler ([ADR-0004](../adr/0004-core-dependencies.md)) — the `lucid
scheduler run` daemon, which catches up a fire it missed while down on
its next supervised start; under a chat harness the harness's native
cron drives it instead. The runtime is swappable; the send semantics
below are identical either way:

1. Compute yesterday's logical day. If the **folded** day record
   (`corrections[]` applied) shows `completed: true` (or a
   floor-counting Away Mode night) → reset `escalation_state`, done.
   The tripwire always evaluates folded state at run time: a
   `/closeout backfill` — or a rollover-attributed close-out — landing
   before this run means no L1/L2 fires.
2. No record, or `missed: true`: if the day before was completed →
   **L1**: post the fixed nudge template in the user's channel; set
   `escalation_state: l1_fired`.
3. Two consecutive missing/missed days → **L2**: post the fixed
   witness template to the witness channel (only if `witness.json`
   is confirmed and `l2_enabled`); set `escalation_state: l2_fired`.
   The message contains streak and declared-mode data only.
4. On the first run of each calendar month, additionally post the
   heartbeat line to the witness channel (if `witness.json` is
   confirmed and not lapsed) — see §"Consent amendment".
   **Same-run collision:** if an L2 posts on the same run, the
   heartbeat is suppressed that month — the L2 is the month's proof
   of life.
5. **L3 is not automated in the MVP.** The stake executes by the
   humans it binds; the tripwire's job ends at making the breach
   impossible to miss. A `l2_fired` state on `/status` is the record.
   On a breach, the user executes the stake within the stake
   execution window (default 72 hours) and confirms to the witness
   (engine §4); past the window, `/status` surfaces `stake_owed`, and
   gates cannot ratchet (simplify/hold only) while a stake is owed.
   No new sends exist for any of this. **Because the MVP automates no
   breach/L3 event, nothing sets `stake_owed` true in the MVP:** the
   field and its `/status` line are plumbed but reserved, surfacing only
   once L3 lands — the code passes it `false` on every tripwire run, and
   the storm rule below already asserts a storm miss can never produce
   it.

**Storm behavior.** While a storm stands (`storm.json`, engine §4)
the tripwire's *contact* is unchanged and its *consequences* pause:
steps 2–3 post the fixed storm variants (§"Consent amendment") and
still set `escalation_state`, but storm misses spend no error budget,
accrue no breach, and never produce `stake_owed` — a storm miss is
never a stake event. The consecutive-miss counter resets at storm
exit: the first post-storm miss is an isolated miss. The same run
handles storm bookkeeping deterministically: an ambush window whose
start date arrives appends `entered`; a standing storm past its
`through` date appends `expired`; a pending declaration past the
72-hour confirmation window lapses with an L1-style note to the user's
own channel (no new send class — same channel, same consent as L1).

Dead-man semantics are preserved exactly: the tripwire fires on the
**absence** of a day record. Self-report (`/closeout skip`) produces
an honest miss but never suppresses escalation. The bell prompt is the
same job's evening sibling (post the chain label at the active
profile's `bell_time`).

**After any miss, the restart ritual applies** (engine §4): the L1
template names the floor chain for tonight and nothing else — no
makeup work exists anywhere in the system.

### Durability of the schedule itself

The sends above defend the chain; this section defends the sends. A
scheduled send is driven by a **periodic** — a durable row naming its
slug, its cron, whether it is active, and when it next runs. Three rules
bind that row, because a scheduled send that quietly stops existing is
worse than one that visibly fails (P10: the record serves the practice):

1. **A failed delivery never parks its periodic.** A send job that
   exhausts its retries — a delivery timeout, an unreachable channel — is
   discarded as a *job*. Its parent periodic stays active and its cursor
   advances to the next occurrence, so tonight's transport failure costs
   tonight's send and nothing more. Locked by a regression test, because
   the failure it prevents is silent: a transient outage that disabled a
   daily accountability send would surface only as a send that never
   came again.
2. **A parked periodic self-heals on startup.** `lucid scheduler run`
   reconciles on every boot: a periodic that is *intended active* (see
   the guard table in
   [`../usage/commands.md`](../usage/commands.md#scheduler-reconcile))
   but found inactive, or with its next run stuck in the past, is
   re-armed and the missed occurrence fires. The guard is derived from
   configuration per slug, so the pass never re-arms a periodic that is
   *intended inactive* — a companion-suppressed bell stays suppressed,
   and `bell.enabled: false` stays off. On a healthy store it changes
   nothing.
3. **Repair goes through the binary.** `lucid scheduler reconcile` is
   the same pass on demand, and is the only sanctioned repair for a
   parked send. The job store is disposable machinery
   ([ADR-0004](../adr/0004-core-dependencies.md)), never the record, and
   is never hand-edited — the same discipline the Ledger's derived
   `status.json` follows.
4. **The rules cover every scheduled send, not only the Engine's.** The
   companion runs its own periodics in its own job store, so rules 2 and
   3 apply there too: its node runs the same startup self-heal, and
   `scheduler reconcile` spans both stores. A fault the report can name
   is a fault the lever can fix — otherwise a parked companion window
   would be a send that stopped existing with nothing but a hand edit to
   bring it back, which is exactly the outcome this section forbids.

Two **backstops** complete the set: rules 1–4 keep a *periodic* alive, and
the backstops keep a *window* from falling silent when the send that owns
it fails outright. The evening backstop (§"Consent amendment") covers the
night; the morning backstop
([`../usage/commands.md`](../usage/commands.md#the-morning-backstop-lucid-companion-morning-backstop))
covers the morning, which needs it most — with the companion presenting
that window the tripwire's own user-channel send is suppressed, so an
unbacked morning failure is total silence rather than a plainer message.

## Error states (extends [`error-states.md`](error-states.md))

| Trigger | Behavior | User message | Disk effect | Recovery |
|---------|----------|--------------|-------------|----------|
| `/closeout` twice same logical day | Idempotent no-op | "Already closed out — streak N." | None | — |
| `/mode` after bell time | Reject | "Mode is fixed at the bell (19:00). Tonight runs as declared; the budget absorbs hard days." | None | Retro annotation |
| `/mode --day` targeting a day that already has a declared mode | Reject — gap-fill never overwrites | "A mode was already declared for that day — the bell fixed it, and there's no retroactive amendment (engine §2)." | None | Retro annotation |
| `/mode --day` beyond `backfill_window_days`, or targeting today / a future day | Reject | "That's outside the mode gap-fill window (7 days)." / "A mode gap-fill names a day that has already ended." | None | Retro annotation |
| `--day` (any Engine verb) given a token the date grammar cannot read, or a day in the future | Reject before any write — the strict tier | The accepted forms, named; "Nothing was saved." | None | Re-type the date |
| `/storm … --day` dated before the last recorded storm event | Reject — storm history folds in order, so an out-of-order append would corrupt the fold | "That's before the last recorded storm event — storm history is append-only and in order." | None | Re-date the event |
| Close-out interrupted mid-flow | Write partial | "Saved what we got — floor still counts if the survival link ran." | Day record `partial: true` | `/closeout` again appends corrections |
| Tripwire can't reach witness channel | Fall back | L1-style message to user: "L2 fired but couldn't reach <witness> — you owe the message." | `escalation_state: l2_fired` | Manual |
| Clock/rollover ambiguity (e.g., travel TZ) | Trust host clock | — | Local-TZ timestamps per [`data-model.md`](data-model.md) | Away Mode pre-specs the trip |
| `/closeout backfill` beyond `backfill_window_days` | Reject | "That's outside the backfill window (7 days). The record stands — the budget absorbs it; the Retro can annotate context." | None | Retro annotation |
| `/closeout backfill` targeting a day with `completed: true` | Idempotent no-op | "Already closed out — streak N." | None | — |
| Correction attempts an immutable field | Reject | "Mode and day identity are fixed at the bell — no retroactive amendment (engine §2)." | None | — |
| `/storm` citing an unknown label | Reject | "No clause or window by that name — clauses live in the Charter and are registered in storm.json. `/storm unwritten` if life exceeded the list." | None | Quarterly writes the missing clause |
| `/storm` unconfirmed for 72 hours | Lapse | L1-style note: "storm declaration lapsed — no confirmation within 72h. Declare again, or talk to <witness>." | `storm.json` history: declaration stands unconfirmed; no storm state | Re-declare |
| `/storm` renewal beyond the one allowed | Reject | "A storm renews once (engine §4). Past that, this is a season, not a storm — it goes to the quarterly." | None | Quarterly review |
| An anchor verb aimed at the wrong kind, a pre-id or sunset anchor, an illegal `void` target, invalid allowance/window, a `confirm --from` outside the anchor's life, or an anchor with a half-written pair | Reject before any write — the strict tier | The fixed copy of [`error-states.md`](error-states.md) An-4–An-13, each naming its remedy; "Nothing was saved." | None | Follow the named remedy (reclassify, `lucid anchor backfill-ids`, retry the original verb) |
| `/profile` naming an undefined profile | Reject | "No profile by that name — profiles are defined in chain.json, at a Retro (engine §2)." | None | Retro defines it |
| `status.json` corrupt/missing | Rebuild silently | — | Regenerated from `days/` | By design (P2) |
| Harness down at bell time | Nothing | — | None | **The chain runs anyway** — phone alarm is the Phase-0 fallback; a text-to-self is a valid record, backfilled via `/closeout backfill` (P10) |
| A scheduled send's delivery fails (transport timeout, channel unreachable) until its retries are exhausted | Discard the **job**; the parent periodic stays active and its next run advances | (none — the send simply did not land; `scheduler status` reports the missed window) | Job store only; no Ledger effect | Automatic — the next occurrence fires normally. Tonight's record is repaired with `/closeout backfill` (P10) |
| A periodic is *intended active* but found parked (inactive, or next run stuck in the past) | Re-arm on the next `scheduler run` startup; the missed occurrence fires | `scheduler status` reports an error line naming the slug **and** the repair command | Job store only | `lucid scheduler reconcile [--slug <slug>]` — never a hand edit of the job store |
| A periodic is *intended inactive* (companion-suppressed bell, `bell.enabled: false`) | Left inactive by every reconcile pass | `scheduler status` reports "suppressed by companion (intended)" — OK, not a fault | None | (none — this is designed behavior, not a failure) |
| Companion owns the evening window and left no verified `night` receipt for the logical day by the backstop mark | Evening backstop fires the bell template verbatim | The ordinary bell prompt (late) | Job store only | (none — the backstop *is* the recovery) |
| Companion already delivered a verified `night` receipt for the logical day | Backstop is a no-op — at most one evening send per day | (none) | None | (none — designed) |
| Companion left no verified `morning` receipt for the logical day by the backstop mark | Morning backstop re-runs the companion card | The real morning card (late), carrying a backstop note | Job store only | (none — the backstop *is* the recovery) |
| Companion already delivered a verified `morning` receipt for the logical day | Backstop is a no-op — at most one morning send per day | (none) | None | (none — designed) |
| The morning backstop mark falls past the companion's `10:00` morning cut-off | Backstop stands down — no send, and no alert | (none — the primary morning send already alerted for that window) | None | (none — designed; a second alert would report the same fault twice) |

## Acceptance criteria (build phases 8–10)

These extend [`acceptance-criteria.md`](acceptance-criteria.md); same
format applies (setup, test cases, verification commands, definition
of done). Phases 8–10 depend only on Phases 1–2 (scaffold + `/log`)
and may be built immediately after them — before the Mirror's
structuring/insight phases — since the Engine module is agent-free.

**Phase 8 — Engine scaffold + `/closeout`.**
Day records write correctly for both sides of the rollover boundary
and the attribution rule (23:50 → today; 03:50 → yesterday; 04:12 with
yesterday unrecorded and today's bell not yet rung → yesterday; 04:12
with yesterday already completed → today); a same-day repeat is a
no-op; `chain_start` is stamped exactly once; the journal line lands
in `raw/` with `command: /closeout` and valid frontmatter; close-out
transcript shows ≤ (links + 3) prompts (two-minute budget by
construction); a `/closeout backfill yesterday` inside the window
creates a `backfilled: true` record (or appends a correction) while a
target beyond `backfill_window_days` is rejected. Profile fixtures: a
switch to a `nights` profile (rollover 12:00) after today's bell
leaves tonight on the old clocks; the next day, an 11:00 close-out
attributes to the new profile's previous logical day; each day record
stamps the profile that governed it.

**Phase 9 — Derived status + `/mode` + `/status`.**
`rebuild_engine_status` is deterministic (delete + rebuild ⇒
identical, including day records carrying `corrections[]` — the fold
is part of the byte-reproducibility criterion); adherence is
mode-relative (fixture: a Yellow floor-day
scores 1.0, an undeclared floor-day scores 1.0 on links but Crux-less
Green scoring only applies once the Crux ships post-MVP — MVP scores
links only); `/mode` after `bell_time` is rejected; error-budget burn
matches a hand-computed 30-day fixture including one isolated miss
(spend) and no consecutive misses. Storm fixtures: an undeclared day
under a standing storm scores against Red; a storm miss spends zero
budget and leaves consecutive-miss count unaccrued for breach math;
`status.json` carries `storm_state` with its `through` date and the
active profile; the consecutive-miss counter resets at storm exit.

**Phase 10 — Tripwire.**
Simulated clock fixtures: completed day ⇒ no send; one missing day ⇒
exactly one L1 in-channel with the floor named; two consecutive ⇒
exactly one L2 to the witness fixture containing **zero** bytes of
journal/capacity data (grep the payload); L2 with unconfirmed
`witness.json` ⇒ blocked + user notified; recovery night ⇒ state
resets and L1 does not re-fire. Storm fixtures: standing storm + one
miss ⇒ exactly one L1 storm variant; + two consecutive ⇒ exactly one
L2 storm variant with zero budget spend and no `stake_owed`, ever; a
declaration unconfirmed at 72 hours ⇒ lapses with the user-channel
note; an ambush window's start date ⇒ `entered` appended with no send;
expiry past `through` ⇒ `expired` appended; entry never annotates a
day before its declaration. All templates are static strings in
the repo — verify no LLM call in the tripwire path, and verify L1, L2,
and both storm variants end with the pinned sign-off line ("— the form
letter, pre-committed at Day 0.") while the bell prompt and heartbeat
do not.

## What the Engine module intentionally is not (MVP)

* **Not the Crux protocol, portfolio, gates-as-software, or Retro
  tooling.** Those stay manual practices per
  [`../engine.md`](../engine.md) §5 in Phase 0/2; the software records
  days and defends the chain. Gate math surfaces in `/status`
  (days-to-gate) but gate decisions are human.
* **Not multi-chain.** One chain until the first gate — in the schema
  and in the spec.
* **Not a habit-gamification surface.** No badges, no confetti, no
  streak-loss drama. The ack is one line. Streaks report; they do not
  perform.
* **Not the Coach voice.** The Engine has no voice at all — fixed
  templates only. Conversational accountability is a deferred surface
  behind [`agent-contracts.md`](agent-contracts.md) seams.
* **Not Tier 2 passive telemetry.** Screen metrics, charge events,
  and wearables are post-MVP; the schema leaves room (day-record
  `corrections[]` and future `covariates{}`).
