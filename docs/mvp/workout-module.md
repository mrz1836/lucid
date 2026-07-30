# Lucid MVP — Workout Module

This page specifies the **workout companion** — an optional, config-gated
daily surface that recommends today's workout, records what actually
happened, and reviews progress over time. It is the Mirror-side,
model-allowed counterpart the [`../usage/companion.md`](../usage/companion.md)
established for the morning/night briefing, narrowed to one job: *hand
the user the right workout on a silver platter, safely, in under a
minute.*

Like the companion, it ships **off**. A fresh Ledger runs the pure
Engine and the existing surfaces exactly as before until an
operator sets `workout.enabled: true`; nothing below happens until then.

Precedence follows the same rule as [`engine-module.md`](engine-module.md)
and [`observations-module.md`](observations-module.md): this page and
[`scope.md`](scope.md) own *scope*; the older MVP docs own *conventions*
(naming, mutability, adapter discipline, the sanctuary rule).

## Why this is a companion-class surface, not a Coach

[`product-principles.md`](product-principles.md) §1 defers the **Coach**
role (goals, progress narrative, "next actions") and
[`../observations.md`](../observations.md) §0 holds observations as
**inventory, never obligation**. The workout module sits inside both
constraints, exactly as the daily companion does, by keeping the same
three lines the companion draws:

1. **The decision is deterministic; the model only phrases it.** A pure
   `Recommend` core picks and vetoes today's workout from a program plus
   the recent Ledger; the model is handed an *already-decided* plan and
   writes only the warmth around it (the [`../adr/0009-workout-companion.md`](../adr/0009-workout-companion.md)
   decision). A model can never talk the user into leg day twice in a
   row, because it never owns the pick.
2. **The streak is a projection, not a score on an event.** The workout
   and body-state observations carry no streak, quota, target, or grade
   (§0) — not in the schema, not in an ack. The progress panel's workout
   streak is **counted on read** from the logical days that carry a logged
   workout (a completed daily anchor *or* a session), and adherence is read
   from the Engine chain ([`engine-module.md`](engine-module.md)
   `status.json` / `metrics`). Both are **read-only projections**, computed
   on demand and never written back onto an event. Counting days is not
   consequences: there is no quota, no makeup obligation, and no escalation —
   a workout practice that earns consequences becomes an Engine link through a
   Gate, a separate deliberate act.
3. **It surfaces, it never nags.** The recommendation rides one
   configurable daily slot and an on-demand command. A skipped day is a
   skipped day: no "you didn't work out", no makeup obligation, no
   push into silence (the companion's own ceiling, and §0's).

This is not a new subsystem. It is one config-gated Mirror-side surface
plus two new observation kinds — the extension model in
[`../architecture.md`](../architecture.md) §4b (a new event kind, a new
projection), riding cadences that already exist. The "Coach deferred"
note in [`product-principles.md`](product-principles.md) §1 is revised to
say: a **config-gated companion-class** workout surface exists — with a
deterministic core, off by default, and no voice of its own beyond the
model phrasing the companion already sanctioned.

## Binding rules inherited

1. **Inventory, never obligation** ([`../observations.md`](../observations.md) §0).
   The `workout` and `body_state` kinds carry no streak, quota, target,
   score, or escalation — not in the schema, not in any ack, not in the
   daily message. An ack is "logged" plus the id.
2. **The trend stores nothing back.** The projection counts the workout
   streak from the logged workout days it is handed, and reads adherence
   from the Engine fold ([`engine-module.md`](engine-module.md) `metrics` /
   `status.json`); it stores nothing back, and no count ever becomes a
   field on an event. If a workout practice ever deserves consequences it becomes
   an Engine link through a Gate (§0), a separate deliberate act — the
   recommender never acquires consequences of its own.
3. **Sanctuary boundaries hold, in both directions.** The two new kinds
   live under `~/.lucid/observations/`; no agent context slice includes
   the observation, registry, or engine trees, nor any projection over
   them (path-prefix denylist at slice-build time, fail closed —
   [`agent-contracts.md`](agent-contracts.md), cross-cutting rules). The
   recommender and trend projection are **user-invoked** reads that may
   join across trees — the user reading their own ledger — exactly like
   `/day`. The witness never sees any of it.
4. **Capture never blocks** (P1/P10). A malformed or partial workout
   drop is stored as an event with the invoked kind and
   `payload = {note: <verbatim>, parse: "partial"}` rather than
   rejected, the same discipline as every micro-log
   ([`observations-module.md`](observations-module.md)).
5. **Deterministic core owns the pick; the model is bounded.** Model
   calls are limited to two jobs — extracting fields from a spoken drop,
   and phrasing an already-decided recommendation — each over a bounded
   slice, each with a deterministic fallback. Neither ever changes the
   decision (architecture P9: the runtime never depends on AI).
6. **Never diagnosis, never treatment advice** ([`../observations.md`](../observations.md) §9).
   The surface avoids clinical or prescriptive framing while keeping the card
   compact; the same Safety boundary that governs every Lucid surface applies.
7. **Synthetic examples only** ([`product-principles.md`](product-principles.md) §9).
   Every program, card, and worked example in this repo is synthetic.
   The personal program — with a real body's injuries, recovery windows,
   and rehab guardrails — is operator config on an opaque path, never in
   the public repo (§"The OSS/personal split").

## The OSS/personal split

The generic **schema** is OSS; the personal **values** are operator
config. This is the same firewall the companion draws for its routine
and template files ([`../usage/companion.md`](../usage/companion.md)):

* **In the repo (generic, synthetic):** the `Program` schema (rotation,
  session cards, per-body-part recovery windows, daily anchor,
  guardrails, goals, equipment, safety copy), the two kinds, the
  deterministic recommender, the trend projection, the render, and a
  **synthetic** example program used only by tests and docs.
* **Operator config (personal, private):** the actual program — a JSON
  file on an **opaque path** named in `lucid.json` (`workout.program`),
  read directly by the loader (no dir-walk, no filename convention), plus
  a personal system prompt and daily template. A real rehab program's
  injury specifics, avoid-movements, and week-by-week numbers live here,
  outside the public repo, exactly as the companion's routine files do.

The public-safe boundary (`lucid validate`, [`scope.md`](scope.md) §8
S-7) is the enforced firewall: the repo stays synthetic-only, and no
personal detail is ever committed.

## Configuration — the `workout` block in `lucid.json`

The whole feature is one block, off by default (full schema notes in
[`data-model.md`](data-model.md) §"lucid.json"):

```json
"workout": {
  "enabled": false,
  "program": "",
  "slot_time": "",
  "system_prompt": "",
  "template": "",
  "model": ""
}
```

| Key | Type | Meaning |
|-----|------|---------|
| `enabled` | bool | Gates the whole feature. Default `false` → the scheduler runs only the existing Engine and companion. |
| `program` | path | The generic-schema program JSON, on an **opaque** operator path — the personal program at runtime, a synthetic one in tests. |
| `slot_time` | `HH:MM` | The daily slot's local fire time. **Configurable, default midday** — this surface is not tied to the chain's bell/tripwire the way the companion is, because a workout window is a personal choice (some train in the morning, some at midday). |
| `system_prompt` | path | The system prompt for the phrasing call — an opaque file, same seam as `program`. |
| `template` | path | The per-message template for the phrasing call. |
| `model` | string | Optional. Overrides `provider.model` for the phrasing call; empty inherits the provider default. |

When `enabled` is `true`, `program`, `system_prompt`, `template` must be
non-empty and `slot_time` must be a valid `HH:MM`, or the config is
rejected at load (mirrors `companion`'s validate). **Credential-dumb like
the rest of `lucid.json`:** no channel id, no token, no model API key
lives here — the channel and harness token stay env-only
(`LUCID_USER_CHANNEL_ID` / `LUCID_HARNESS_TOKEN`), auth is the vendor
CLI's or the local daemon's.

Unlike the companion, `slot_time` **is** a workout key: the workout slot
does not inherit the chain marks (they defend the night chain, a
different thing). A single configurable local time is the whole
scheduling seam.

## The generic program schema

A **program** is the generic, versioned body of what to do — the rotation,
the session cards, the recovery windows, the daily anchor, the guardrails,
and the safety copy. It is loaded from the opaque `program` path directly
(no dir-walk) and validated on load; a bad or missing program degrades to
"no program" rather than crashing the surface.

The schema, with a **synthetic** example (no personal content):

```json
{
  "version": 1,
  "program_id": "example_foundation",
  "label": "Foundation rotation",
  "start_date": "2026-01-05",
  "goals": ["build a base", "protect recovering joints", "stay consistent"],
  "equipment": ["bodyweight", "light dumbbells", "band"],
  "session_minutes": 50,
  "rotation": [
    {"weekday": "mon", "card": "legs"},
    {"weekday": "tue", "card": "push"},
    {"weekday": "wed", "card": "pull"},
    {"weekday": "thu", "card": "cardio"},
    {"weekday": "fri", "card": "full_body"},
    {"weekday": "sat", "card": "power_skill"},
    {"weekday": "sun", "card": "recovery"}
  ],
  "cards": [
    {
      "id": "pull",
      "name": "Pull + posture",
      "focus": ["back", "rear_shoulders"],
      "load": "moderate",
      "movements": ["supported row", "band pull-apart", "face pull"],
      "easier": {"name": "Easy pull", "load": "light",
                 "movements": ["band pull-apart", "face pull"]}
    }
  ],
  "recovery_hours": {"back": 48, "chest": 48, "legs": 48, "core": 24},
  "daily_anchor": {
    "items": [
      {"name": "squats", "target": 50},
      {"name": "core", "target": 40},
      {"name": "easy push-ups", "target": 20, "mode": "accumulate"}
    ],
    "targets_by_week": {"2": {"squats": 55, "core": 50, "easy push-ups": 25}}
  },
  "guardrails": {
    "avoid_movements": ["loaded end-range overhead reaches"],
    "provocative_positions": ["any loaded position that reproduces a specific joint pain"],
    "no_strengthen": ["already-overworked stabilizers named by the operator"]
  },
  "pain_flag_threshold": 5
}
```

Field semantics, binding:

| Field | Meaning |
|-------|---------|
| `program_id`, `label` | Stable id + human label. |
| `start_date` | Civil `YYYY-MM-DD` the program is anchored to. The week index (`daily_anchor.targets_by_week`) and any dated calendar override count from here at the chain's rollover boundary. Absent or unparseable → week 1 always; a `now` on or before `start_date` is week 1 (never week 0 or negative). |
| `goals`, `equipment`, `session_minutes` | Free-text context the phrasing call may read; the recommender uses `equipment`/`session_minutes` only to veto a card the operator cannot run. |
| `rotation[]` | The weekly rotation: one card id per weekday. This is the default calendar; a program may also carry an optional dated `calendar[]` (`{date, card}`) that overrides the weekday rotation for specific days. |
| `cards[]` | The session library. Each card has an `id`, `name`, `focus[]` (the body parts it loads — the key the recovery guardrail reads), a `load` (`light`/`moderate`/`hard`), `movements[]`, and an optional `easier` variant used as the message's fallback offering. A `recovery`/`mobility` card carries `load: "none"`. |
| `recovery_hours` | Per-body-part minimum hours before that part may take a **non-light** load again. The recovery guardrail (below) reads this. |
| `daily_anchor` | The "something every day" floor — **surfaced on the card, never graded**. `items[]` are the anchor movements; each `target` is the count the card *shows* for today, not a bar the system scores the user against; `mode: "accumulate"` marks a movement done in small sets through the day; `targets_by_week` overrides item targets for a given 1-indexed program week (§"The daily-anchor projection"). Completion is self-reported inventory: the surface records that the anchor was done, and any counts given with it, and compares them to nothing. |
| `guardrails` | `avoid_movements[]` and `provocative_positions[]` are never recommended; `no_strengthen[]` names parts the program deliberately does not load. All three are generic slots the operator fills with their real specifics. |
| `pain_flag_threshold` | The `body_state.pain` value (0–10) at or above which the recommender emits a hard-stop and downshifts (default 5). |

Payload schemas version forward per the frozen-envelope rule; a loader
reads what it understands and skips what it doesn't.

## Two new observation kinds

Two new Ledger kinds capture what happened. Both extend the initial
vocabulary in [`../observations.md`](../observations.md) §3 and are
**enable-gated, off by default** (like `withdrawal` / `habit_change` /
`commitment`): a fresh Ledger never carries them; an operator adds them
to `observations/config.json` `kinds_enabled` to log and surface them.
Both preserve the frozen envelope — new needs go in `payload`, never a
new top-level field.

| Kind | Payload (schema 1) | Convention |
|------|--------------------|------------|
| `workout` | `type`, `movements?`, `duration_min?`, `rpe?`, `body_parts?`, `anchor?`, `anchor_items?`, `note?` | `type` free text (the card name or a free description); `rpe` 0–10 (session RPE, the standard effort scale); `body_parts[]` free text matched to the program's `focus` vocabulary when unambiguous. `anchor: true` marks the event as a completed daily anchor and `anchor_items` carries the per-item counts when the user gave any (§"Capture"). All optional — a bare `type` is a valid event ("I trained"), and so is a bare `anchor` ("did the anchor"). |
| `body_state` | `body_part`, `soreness?`, `pain?`, `note?` | `body_part` free text, matched to the injury registry when unambiguous; `soreness` 0–10 (ordinary training soreness); `pain` 0–10 (the clinical NRS, the same scale as `pain`) — the signal the hard-stop reads. A `pain` at or above the program threshold is a back-off signal, never a grade. |

These are **capturable** kinds: both aliases route the one
`observation.capture` router intent, sub-second, no LLM, offline-capable.
`body_state` is a distinct kind from the existing `pain` micro-log
deliberately — `pain` is the general body-signal micro-log; `body_state`
pairs soreness *and* pain against a named part specifically for workout
recovery reasoning, and carries a `soreness` slot `pain` does not.
Adding them changes nothing about the sanctuary or witness boundaries:
like every kind they are inventory the Engine never reads.

## The deterministic recommender contract

`Recommend` is a **pure** function — zero model calls, zero disk I/O — so
the guardrails the success criteria mandate are unit-testable and the
daily surface completes even with no model available.

**Inputs** (`RecommendInput`): the loaded `Program`; the recent
`workout` events; the recent `body_state` events; the injury-registry
records (`refs`-linked context, read-only); the Engine `metrics`
(streak/adherence, from the chain fold); `now` and the local
`*time.Location`.

**Output** (`Recommendation`):

```text
Recommendation{
  Primary  Card          // today's recommended plan
  Fallback Card          // the easier variant — always present
  HardStop *SafetyOption // set only when a pain signal warrants backing off
  Reason   string        // one deterministic line: why this card today (not rendered)
  Vetoes   []string      // cards/parts vetoed this run and why (for tests + --json)
}
```

`Reason` and `Vetoes` are the decision's **audit trail**, not message copy: they
are asserted by the recommender's table tests and emitted by `--json` so a
harness can read *why* the pick landed where it did. Neither is rendered — the
message carries no prose "Why" region (§"The message scaffold").

**Rules** (deterministic, in order):

1. **Pick today's card.** Resolve today's logical day, then read the
   program's dated `calendar[]` for that day if present, else the
   `rotation[]` entry for the weekday. That card is the Primary
   candidate; its `easier` variant is the Fallback.
2. **Recovery-window veto** (the no-repeat-without-recovery guardrail).
   For each body part in the candidate card's `focus`, find the most
   recent **non-light** `workout` event that loaded it. If `now` is
   inside that part's `recovery_hours` window, the card is **vetoed**:
   the recommender rotates to the next rotation card whose focus is
   clear, or — if none is clear — downshifts the Primary to a
   recovery/mobility card and records the veto. This is what stops
   "leg day every day": a part loaded hard yesterday cannot be the hard
   focus again until its window clears.
3. **Pain-flag hard stop.** If any recent `body_state.pain` for a part
   the candidate targets is at or above `pain_flag_threshold`, or an
   active injury-registry constraint names a targeted part, the
   recommender emits a `HardStop` (a named safety back-off option) and
   downshifts the Primary to the easier or a recovery card. A specific
   joint pain always wins over the calendar.
4. **Guardrail filter.** A card whose movements intersect
   `guardrails.avoid_movements` or `provocative_positions`, or whose
   focus is a `no_strengthen` part, is never Primary or Fallback — it is
   filtered before the veto pass.
5. **Equipment / time veto.** A card the operator cannot run given
   `program.equipment` / `session_minutes` is vetoed to its `easier`
   variant.
6. **Missing-data behavior.** With no recent `workout` or `body_state`
   events (a fresh Ledger, or the surface just enabled), no window can be
   computed, so the recommender falls back to the **plain program
   calendar**: today's rotation card, its easier fallback, no hard stop,
   `Reason` naming the absence. Missing data never blocks a
   recommendation.

Every path yields a Primary, a Fallback, and a Reason; `HardStop` is the
only optional field. The core makes zero model calls on every path.

## The daily-anchor projection

`BuildAnchor` is the second **pure** projection — zero model calls, zero
disk I/O — turning the loaded program plus `now` into the anchor the card
shows today:

```text
Anchor{
  Week  int          // 1-indexed program week, derived from start_date
  Items []AnchorLine // {Name, Target, Mode} — one per daily_anchor item
}
```

**Week derivation.** The program week is counted from `start_date` at the
Ledger's rollover boundary (`LogicalBaseDate`, the same civil-day resolution
every other surface uses), so the week advances at the boundary rather than
at midnight-local:

1. No `start_date`, or one that does not parse → **week 1**.
2. `now` on or before `start_date` → **week 1**; the index never goes below 1.
3. Otherwise `week = ⌊days since start ÷ 7⌋ + 1`, so days 0–6 are week 1,
   days 7–13 are week 2, and so on.

**Target selection.** For each item the effective target is the one named by
the **highest** `targets_by_week` entry whose week index is ≤ the current
week, falling back to the item's own `target` when no entry names it. Two
consequences, both intended: an undefined intermediate week **inherits** the
last defined one (a program may specify weeks 1 and 3 and skip 2), and a
program that runs past its last defined week **holds** at that last target
instead of falling back to the base or off a cliff. A ramp that ends is a
plateau, not a reset.

The projection carries no completion state and no comparison. It answers
only "what does today's floor look like" — the card shows it, and §"Capture"
records what the user says they did, side by side, ungraded.

## The trend / progress projection

`BuildTrend` is a **read-only projection** over the Ledger plus the
Engine fold — nothing is written back onto any event (P3 sanctuary):

* **Workout streak** — the consecutive run of recent logical days that
  carry a logged workout, counted on read from the `workout` events the
  caller passes in. It is the surface's own honest number, **not** the
  Engine chain's `CurrentStreak`: the chain defends the night close-out, a
  different practice, and borrowing its count printed a number the workout
  record had not earned. Three rules make it honest:
  * **A day closes on a completed daily anchor *or* a logged session.**
    Both are a `workout` event, so both land in the same day set; the
    low-friction anchor is the ordinary way a day closes, and a session
    day counts on its own even when the anchor was never logged separately.
  * **Today in progress is not a break.** The count anchors on today when
    today already carries a workout, and otherwise on yesterday — a day is
    not scored against the user before it is over. A gap of two or more
    days ends the run.
  * **Zero is zero.** No logged workout day yields `0`, which the panel
    renders as "Building — no active streak yet". The surface never shows
    a borrowed or fabricated number in its place.
* **Adherence** — still read from the Engine `metrics` (the chain fold),
  never recomputed as a score on workout events.
* **Volume / frequency trend** — sessions per week and a simple
  direction (up / flat / down) from the recent `workout` events.
* **Skipped days** — logical days in range with no `workout` event,
  surfaced as inventory (a count, not a shame line).
* **Body response** — recent `body_state` soreness/pain by part, so the
  user can see how the body answered the load.

The projection computes with zero data (an honest empty trend) and with
sparse data. It is surfaced by `lucid workout` and inside the daily slot;
it is never pushed, never witness-visible, and never stored.

## Capture — two paths, both writing events

A completed workout becomes structured data two ways
([`../usage/natural-language.md`](../usage/natural-language.md) is the
voice-first stance this follows):

* **Spoken free-text drop → LLM extraction (default).** The user says how
  it went in their own words; a small **Workout Extraction** agent
  ([`agent-contracts.md`](agent-contracts.md)) turns the drop into the
  `workout` fields (and any `body_state`). One model call, one bounded
  slice (the drop only, no Ledger read), retry-once-stricter-then-degrade,
  no storage handle. The router then writes the durable events.
* **Structured `lucid workout log` (guided / precise backfill).** A
  deterministic command with flags (`--type --duration --rpe --parts
  --soreness --pain --notes --anchor --anchor-item`) and a
  positional/`--text` free-text form that routes to extraction. No LLM in
  the flag path.

Both paths write `KindWorkout` (and, when soreness/pain are present,
`KindBodyState`) events through the router's deterministic capture, which
writes the durable record **first** — a failure leaves nothing
half-written on disk.

**The daily anchor is captured through the same verb.** `lucid anchor`
already means the *milestone* anchor (a sobriety or gate date); the daily
floor is a different concept and stays under `workout`, so there is exactly
one workout capture verb and no collision:

* **Structured** — `lucid workout log --anchor` records that today's anchor
  was done. Repeatable `--anchor-item name:count` adds the counts when the
  user has them (`--anchor-item squats:55 --anchor-item core:50`); a bare
  `--anchor-item squats` records the item with no count. `--anchor` is
  **content**, so an anchor-only log is a complete event, not the empty
  partial path — and, like every other content flag, it may not be combined
  with a spoken drop (spoken *or* structured, never both).
* **Spoken** — the same free-text drop, the voice-first default: "did my
  daily anchor, 55 squats and 50 core" extracts the anchor marker and any
  per-item counts. The extraction stays generic — it recognizes that a
  daily anchor was named, never a hardcoded list of item names, which live
  only in the operator's program.

Both write **one** `KindWorkout` event carrying `payload.anchor: true`, plus
`payload.anchor_items` when counts were given (frozen envelope: a marker in
`payload`, never a new top-level field or a third kind). Two properties
follow, and both are deliberate:

* **A self-report closes the day.** Saying the anchor was done is enough for
  the day to count toward the streak; per-item counts are recorded as
  inventory when offered and required for nothing. Grading an off-by-five-reps
  day as a total miss would be a score on an event, which §0 forbids — and a
  ramp is watchable without being graded.
* **An anchor opens no recovery window.** An anchor event carries no
  `body_parts`, so the recovery guardrail sees nothing to protect and the
  next day's card is unaffected. The daily floor is not a session.

## Surfaces — a configurable slot and an on-demand command

* **The daily slot** — a config-gated periodic that fires once at
  `workout.slot_time` (local, **default midday**), delivered by a
  flywheel node beside the Engine and companion, gated on
  `workout.enabled`. It composes the deterministic recommendation, asks
  the model to phrase it, and delivers one idempotent, read-back-verified
  message with the same degrade layering the companion uses (deterministic
  fallback on provider outage, receipt idempotency, a bounded late-note
  catch-up, a loud alert on a total miss). It never inherits the chain
  marks — its time is the operator's choice.
* **`lucid workout`** — the on-demand command: compose and print (or
  `--deliver`) the recommendation + trend now. A dry run is the default
  (zero side effect), the same shape as `companion fire`. `lucid workout
  fire` drives a real or dry-run slot fire.
* **`lucid workout log`** — the capture command above.

The on-demand path composes the same way the slot does: deterministic
pick, model phrasing over a bounded slice, deterministic render on
provider outage — the model never changes the pick.

## The message scaffold

Lucid owns the **layout**; the model fills only the prose — the same
split the companion draws. Every message is a deterministic, byte-stable
scaffold, mobile-friendly (bullets, blank-line spacing, **no markdown
tables** — a chat surface renders them as raw text):

* **Header** — `{emoji} **Workout** · {Weekday, Mon D}`.
* **Three offerings** (the heart of the message) — exactly:
  1. **Recommended** — the Primary card, its focus and movements.
  2. **Easier** — the Fallback variant, for a low-capacity day.
  3. **Back off** — the `HardStop`/safety option: rendered as a real
     third choice **only** when a pain signal warrants it; on an ordinary
     day this line is the deterministic recovery/mobility option so there
     is always a "less is fine" door.
* **Daily Anchor** — today's floor and this week's numbers, one line:
  `{emoji} **Daily Anchor** · squats 50 · core 40 · easy push-ups 20
  (accumulate) — week 1`. Each item carries its current-week target and
  an `(accumulate)` marker when the program says the movement is done in
  small sets through the day; the trailing `— week N` is the derived
  program week, so a ramp is visible as it happens rather than guessed at.
  The region is **dropped entirely** when the program carries no
  `daily_anchor.items` — a program without a floor shows no empty label.
* **Trend panel** — the read-only projection: the workout streak, frequency
  direction, skipped-day count, recent body response — a compact panel,
  never a grade.

There is **no "Why" region.** A guardrail veto, a recovery downshift, or a
pain hard stop still changes *which card is recommended* and still shows up
in the back-off door and in `--json`; what it no longer does is print a
prose line explaining itself. The three doors are the message: the pick is
already deterministic, and a sentence justifying it to the user each day was
chrome, not signal.

The model's phrasing rides in a bounded slot; everything else — the panel,
the anchor line, the ordering, and the three-offering structure — is
Lucid's, and renders identically with the model down.

The render is held to the voice constraints every Lucid surface obeys:
hypothesis/offer framing, and **no** coaching-imperative or
phrase-blocklist token ([`product-principles.md`](product-principles.md)
§6). The deterministic core owns the pick, so the phrasing never has to
command — it offers three doors and names the back-off option.

## Error states (extends [`error-states.md`](error-states.md))

The dividing principle is the companion's: **enrichment reads degrade
quietly; the pick and the live-number reads stay loud** —
a message is never silently half-built, and the daily send never falls
silent.

| # | Trigger | Behavior | Disk effect |
|---|---------|----------|-------------|
| W-1 | Program path missing/unreadable | Degrade to "no program": the surface renders an honest empty recommendation, not a crash | None |
| W-2 | Recent-observation read fails | Non-fatal: the recommender falls to the plain-calendar path (missing-data rule); the message still composes | None |
| W-3 | Workout extraction returns malformed fields | Retry once stricter, then **degrade**: store the drop as a `workout` event with `payload.parse: "partial"` and the verbatim note (capture never blocks) | Event written |
| W-4 | Out-of-range scale (`rpe`/`pain`/`soreness` beyond bounds) | Partial path — stored with the invoked kind, never silently clamped | Event written |
| W-5 | Disabled kind used (`workout`/`body_state` not in `kinds_enabled`) | Reject with the enable hint, exactly like every observation kind | None |
| W-6 | Provider unreachable at the slot / on-demand | Deterministic `Render` of the already-decided recommendation; only the model's warmth is lost, the pick stands | None (or the slot receipt on delivery) |
| W-7 | Slot double-fire on a retry | Receipt idempotency (`ReadCompanionReceipt("workout")`): a retry whose message still reads back in the channel skips | Per-window receipt |
| W-8 | Host asleep past the slot cutoff | Bounded catch-up with a `(late)` note within the window; past the cutoff the send is skipped and the miss is alerted, never a stale midday message hours late | None |
| W-9 | Total miss (compose/deliver/read-back fails) | Loud best-effort alert to the user channel, then a loud job error into the supervised log — silence is the one outcome the slot never produces | None |

## Acceptance criteria (build phases 13–17)

These extend [`acceptance-criteria.md`](acceptance-criteria.md); same
format (setup, test cases, verification commands, definition of done).
The workout phases depend on phases 1–2 (scaffold + `/log`) and the
observation envelope (phase 11); the slot additionally needs the harness
scheduler (shared with, not dependent on, the companion). The generic
build is synthetic-only throughout — the personal program is operator
config, never a fixture.

**Phase 13 — Kinds + config + program.**
`workout` and `body_state` write valid frozen envelopes, are off by
default, and take the partial path on malformed/out-of-range input; the
`workout` config block validates only with the three paths present and a
valid `HH:MM` `slot_time`, and is a disabled zero-value otherwise; the
synthetic example program loads and validates, and the loader never
dir-walks (opaque path only).

**Phase 14 — Deterministic recommender.**
Table fixtures cover the four mandated classes: **rotation** (today's
card resolves from the calendar/rotation), **recovery constraint** (a
part loaded non-light inside its `recovery_hours` window is vetoed and
the Primary rotates/downshifts — no leg day twice), **pain-flag hard
stop** (a `body_state.pain` ≥ threshold or an active injury constraint
emits a `HardStop` and downshifts), and **missing data** (no recent
events → plain-calendar fallback). `Recommend` makes zero model calls and
touches no disk on every path.

**Phase 15 — Trend + anchor + render.**
`BuildTrend` computes with zero and sparse data, stores nothing back, and
counts the workout streak from logged workout days — zero with no data,
alive on a yesterday-only log, reset across a two-day gap, closed by an
anchor-only day exactly like a session day. `BuildAnchor` derives the
program week from `start_date` (pre-start and absent → week 1, the ramp
advances, a week past the last defined entry holds) and selects each item's
effective target. `Render` is byte-stable, shows exactly three offerings,
renders the anchor region (and drops it when the program has no anchor
items), carries **no** `**Why**` region on any path, uses no markdown
tables, fits under a minute of reading, and the guard test asserts zero
blocklist/coaching-imperative tokens.

**Phase 16 — Capture (structured + spoken).**
`lucid workout log` writes a round-tripping `workout` (+ optional
`body_state`) event from both the flag form and the free-text form; the
extraction agent validates via `provider.Fake` (clean extract,
malformed → degrade, empty text → zero calls) and imports no Ledger
package. `--anchor` writes a `workout` event carrying `payload.anchor`
(with `payload.anchor_items` when counts are given), a spoken anchor drop
extracts the same marker, an anchor-only request never takes the empty
partial path, an anchor combined with a spoken drop is rejected, and an
anchor event opens no recovery window.

**Phase 17 — Surfaces (command + slot).**
`lucid workout` prints a rendered recommendation + trend and still renders
deterministically with the provider down; the slot is off unless
`workout.enabled`, fires once at `slot_time`, is idempotent and
read-back-verified, catches up late within the window, refuses past the
cutoff, and alerts loudly on a total miss.

## What this module intentionally is not (MVP)

* **Not a Coach.** No goal trees, no progress celebration, no "you should"
  voice. The recommendation is deterministic and offered; the only voice
  is the model phrasing the companion already sanctioned. A workout
  practice that earns consequences becomes an Engine link through a Gate.
* **Not a score on the body.** `workout` and `body_state` are inventory;
  the trend — streak included — is a read-only projection counted on
  demand and stored nowhere. Nothing grades a session, an anchor count, or
  the body (§0, P3).
* **Not device integration.** No wearables, no heart-rate import, no rep
  counting from a sensor — body parts, soreness, and effort are typed by
  a human, exactly like every observation.
* **Not medical or physical-therapy advice.** No diagnosis, no treatment
  prescription, no rehab protocol authored by Lucid. The program is the
  operator's (or their care team's); the surface hands it back safely and
  points at professional care.
* **Not nutrition or body-composition tracking.** Intake stays inventory
  ([`../observations.md`](../observations.md) §0); outcome variables are
  gate-cadence only (P4), never a daily workout target.
* **Not agent-visible.** No Reflection over workout or body-state events;
  the sanctuary denylist covers the new kinds. Wiring them into an agent
  slice would require a contract diff **plus** a per-instance opt-in,
  default off.
