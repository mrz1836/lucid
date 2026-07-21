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
- **Progress** — streak (from the Engine chain), frequency direction, skipped-day
  count, and recent body response — a compact glance, never a grade.
- **Why** — the deterministic one-liner: why this card today, or why it downshifted.

The model contributes only the leading note; everything else is Lucid's and renders
identically with the provider down (then the note is simply absent). `--json` emits
the decided `{recommendation, trend}` projection instead of the rendered text, so a
harness reads the same pick the message shows.

```sh
lucid workout          # today's recommendation, phrased
lucid workout --json   # the decided recommendation + trend as JSON
```

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

## The daily slot

When enabled, the recommendation also fires once a day at `slot_time` (local,
default midday), delivered inside `lucid scheduler run` beside the teeth and the
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
