# The workout companion

The **workout companion** is Lucid's optional daily training loop: it recommends
today's session, records what actually happened, and reviews progress over time.
Like the [daily companion](companion.md) it is a Mirror-side, model-allowed
surface — but the decision is never the model's. A **deterministic core** picks and
vetoes today's workout (rotation, per-body-part recovery windows, a pain-flag hard
stop, the injury registry); the model only **phrases** the already-decided plan. So
the guardrails are testable, and the message still renders with the model down.

It ships **off**. A fresh Ledger runs the pure Engine loop and the existing
companion exactly as before until you opt in; nothing below happens until you set
`workout.enabled: true` and enable the two observation kinds.

The full behavior spec — the program schema, the recommender contract, the trend
projection, and the message scaffold — lives in
[`../mvp/workout-module.md`](../mvp/workout-module.md); this page is the run-it
guide.

## What it is

- A **generic engine** (rotation, recovery windows, guardrails, safety copy) that
  reads a **program** — the generic body of what to do. The engine is shipped;
  the program is your own file on an opaque path, so the personal specifics of a
  real body never live in the public repo.
- A **deterministic recommender** that owns the pick: it resolves today's card from
  the program calendar, then vetoes it if the focus is still inside its recovery
  window (no leg day two days running), if a recent `body_state` pain reading or an
  active injury targets a loaded part (a **hard-stop / back-off** door), or if the
  session needs equipment/time the program does not allow.
- A **model phrasing** step (reached through
  [`../adr/0006-model-access.md`](../adr/0006-model-access.md), never the Engine)
  that writes a short warm note over the decided plan — and a **deterministic
  fallback** when the provider is unreachable.

## Turning it on

Add a `workout` block to `lucid.json` and enable the two kinds in
`observations/config.json`:

```json
"workout": {
  "enabled": true,
  "program": "/home/you/lucid-workout/program.json",
  "slot_time": "12:00",
  "system_prompt": "/home/you/lucid-workout/system.md",
  "template": "/home/you/lucid-workout/daily.md",
  "model": ""
}
```

| Key | Type | Meaning |
|-----|------|---------|
| `enabled` | bool | Gates the whole feature. Default `false`. |
| `program` | path | The generic-schema program JSON, read directly on this opaque path (never dir-walked) — a synthetic example in the repo's tests, your own program at runtime. |
| `slot_time` | `HH:MM` | The daily slot's local fire time. **Configurable, default midday** — unlike the companion this surface is not tied to the chain's bell/tripwire, because a workout window is a personal choice. |
| `system_prompt` | path | The system prompt for the phrasing call — an opaque file, same seam as `program`. |
| `template` | path | The per-message template for the phrasing call. |
| `model` | string | Optional. Overrides `provider.model` for the phrasing call; empty inherits the provider default. |

When `enabled` is true, `program`, `system_prompt`, and `template` must be
non-empty and `slot_time` must be a valid `HH:MM`, or the config is rejected at
load rather than silently leaving the surface dead.

The captured record needs the two observation kinds enabled — add `workout` and
`body_state` to `kinds_enabled` in `observations/config.json`. Both are off by
default (the same enable-gated posture as the other opt-in kinds).

## The on-demand recommendation

```
lucid workout [--json]
```

`lucid workout` composes today's recommendation now: the deterministic pick, the
model's phrasing, and the read-only progress panel. Every message is a
byte-stable, mobile-friendly scaffold (bullets, no markdown tables):

- **Header** — `🏋️ Workout · {Weekday, Mon D}`.
- **Three offerings** — exactly a **Recommended** plan, an **Easier** fallback, and
  a **Back off** door (the pain-signal safety option when one is warranted, else a
  plain "a lighter day is fine" line, so there is always a lowest-effort door).
- **Daily Anchor** — today's floor and this week's numbers on one line
  (`⚓ Daily Anchor · squats 50 · core 40 · easy push-ups 20 (accumulate) — week 1`).
  Each item shows the target for the **current program week**, counted from the
  program's `start_date`, so a ramp is visible as it happens; `(accumulate)` marks a
  movement your program says is done in small sets through the day. The line is
  dropped entirely if your program has no `daily_anchor`.
- **Progress** — the workout streak, frequency direction, skipped-day count, and
  recent body response — a compact glance, never a grade.

There is no "Why" line: a recovery veto or a pain hard stop changes *which card is
recommended* (and shows up in `--json`), it just doesn't argue its case at you.

The model contributes only the leading note; everything else is Lucid's and renders
identically with the provider down (then the note is simply absent). `--json` emits
the decided `{recommendation, trend, anchor}` projection instead of the rendered
text, so a harness reads the same pick the message shows.

```sh
lucid workout          # today's recommendation, phrased
lucid workout --json   # the decided recommendation + trend as JSON
```

### The streak is your workout record, not the chain's

The streak on this panel counts **your logged workout days** — it is not the night
chain's streak borrowed from the Engine. A day closes when you log a completed daily
anchor **or** a session; either one is "did real work today". Today still in progress
never breaks it, a gap of two or more days ends it, and with nothing logged it reads
**"Building — no active streak yet"** rather than showing a number you haven't
earned. Nothing is written back onto an event: the count is recomputed on read, and
the anchor targets are shown, never scored.

## Logging a completed session

```
lucid workout log [drop...] [flags]
```

Two capture paths, mutually exclusive:

- **Spoken drop** (the voice-first default) — just say how it went; the model
  extracts the session type, duration, RPE, body parts, and any soreness/pain:

  ```sh
  lucid workout log "did pull + scapular work, shoulder felt fine, ~50 min"
  ```

- **Structured flags** (guided or backfill) — precise fields, range-checked:

  ```sh
  lucid workout log --type legs --duration 45 --rpe 7 --soreness quads:5 --pain knee:7
  ```

Each writes one `workout` observation, plus one `body_state` reading per
soreness/pain flag (a bare `--pain knee` records an unquantified flag so the
recommender can still protect it). Those readings are exactly what the recovery and
pain guardrails read back on the next recommendation. Capture is inventory only —
the acknowledgement names what was written and nothing more (no score, no grade).

### Logging the daily anchor

The daily anchor goes through the same verb — `lucid anchor` is the *milestone*
anchor (a sobriety or gate date), a different thing entirely:

```sh
lucid workout log --anchor                                  # did the anchor today
lucid workout log --anchor --anchor-item squats:55 --anchor-item core:50
lucid workout log "did my daily anchor, 55 squats and 50 core"   # spoken, same result
```

Saying you did it is enough — that closes the day for the streak. `--anchor-item
name:count` (repeatable) records the counts when you have them, so you can watch the
ramp later; nothing compares them to the week's target and nothing marks a day short.
A bare `--anchor-item squats` records the item with no count. Like every other content
flag, `--anchor` can't be combined with a spoken drop — it's spoken *or* structured.

An anchor is not a session: it writes no body parts, so it opens no recovery window
and never changes tomorrow's card.

## The daily slot

When enabled, the recommendation also fires once a day at `slot_time` (local,
default midday), delivered inside `lucid scheduler run` beside the Engine and the
companion. It composes the same way the on-demand command does — deterministic
pick, model phrasing, deterministic fallback — and delivers one idempotent,
read-back-verified message with the same never-silent degrade layering the
companion uses. It never inherits the chain marks; its time is your choice. See
[`../mvp/workout-module.md`](../mvp/workout-module.md) §"Surfaces" for the delivery
contract.

## Boundaries

The workout copy avoids medical and clinical claims — it offers options and names
the safe one, and points to professional care for concerning pain, the same stance
as [`../observations.md`](../observations.md) §9. The deterministic core owns the
pick, so the phrasing never has to command: it never tells you what you "should" do.
