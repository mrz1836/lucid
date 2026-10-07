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
  plain "a lighter day is fine" line, so there is always a lowest-effort door). The
  Easier door is **always genuinely lighter than Recommended** — when a card has no
  distinct easier variant, the core synthesizes a lighter one by downshifting the
  recommended plan, so the two are never the same words twice.
- **Daily Anchor** — today's floor and this week's numbers on one line
  (`⚓ Daily Anchor · squats 50 · core 40 · easy push-ups 20 (accumulate) — week 1`).
  Each item shows the target for the **current program week**, counted from the
  program's `start_date`, so a ramp is visible as it happens; `(accumulate)` marks a
  movement your program says is done in small sets through the day. A hold-time or
  set-based item renders with its unit (`wall sit 5x45s`, `breathing 5 min`,
  `shoulder band 2 sets`) rather than a bare number. The line is dropped entirely if
  your program has no `daily_anchor`.
- **Progress** — an **insight** panel, not a flat dashboard: one slim streak line,
  then a per-part next-day pain-response trend (rising / stable / easing, with an
  honest "insufficient data" note until there are enough paired days), a per-part
  load-vs-pain pattern, a **post-workout check-in scaffold** (right after / ~12h /
  ~24h, timed from a logged qualifying session, when your program defines one), and
  any specific watch-outs — a compact glance at signal, never a grade. (The older
  frequency-direction, skipped-day, and flat body-response lines are still in
  `--json`, just no longer on the card.)

There is no "Why" line: a recovery veto or a pain hard stop changes *which card is
recommended* (and shows up in `--json`), it just doesn't argue its case at you.

The model contributes only the leading note; everything else is Lucid's and renders
identically with the provider down (then the note is simply absent). `--json` emits
the decided `{recommendation, trend, anchor, sessions}` projection instead of the
rendered text, so a harness reads the same pick the message shows. `sessions` echoes
the logged sessions the decision read (the four-week look-back) with any amendments
already folded in, so a corrected value is directly readable there — one entry per
session, newest first, never the corrections themselves. An anchor-only capture
isn't a session, so it isn't listed: every id in `sessions` is one
`workout amend` accepts.

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

A note with shell metacharacters or several lines can come off the command line:
`--notes-file <path>` reads it from a file, and `--notes-file -` from stdin, stored
verbatim. Give `--notes` or `--notes-file`, not both.

### Logging a session you did on a prior day

```sh
lucid workout log --type push --rpe 6 --day @yesterday
lucid workout log "2 mile bike ride, easy" --day @yesterday
```

`--day` records the session on the logical day it actually happened, using the
same date grammar every other verb reads
([`commands.md`](commands.md#backdating-with---day)) — a relative word, a full or
partial date, or a day with a time (`--day "@yesterday 19:30"`). It is **not** a
content flag, so it composes with both capture paths: a spoken drop plus `--day`
is fine, and so is the structured set.

The derived `body_state` readings inherit the same instant and the same logical
day as the session. That matters more than it looks: the recovery guardrail
reads a reading's `occurred_at` and the progress trend reads its `logical_date`,
so a half-applied backdate would let the two disagree about when you trained.
Logging yesterday's leg day today therefore opens yesterday's recovery window —
not a fresh one starting now.

A day the grammar cannot read, or a day in the future, is a clean error and
nothing is written.

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

### Amending a logged session

```
lucid workout amend <obs-id> [flags]
```

Logged a session and want to fix or fill it in afterwards — add the RPE you didn't
have at the time, correct the duration, move it to the day it actually happened?
Amend it; don't log it again. A second `workout log` writes a second session, which
double-counts the day in the streak and frequency numbers and opens a second
recovery window.

```sh
lucid workout amend obs_2026_01_15_001 --rpe 4
lucid workout amend obs_2026_01_15_001 --duration 50 --type climbing
lucid workout amend obs_2026_01_15_001 --parts fingers,forearms
lucid workout amend obs_2026_01_15_001 --notes-file ./session-notes.txt
lucid workout amend obs_2026_01_15_001 --day @yesterday
```

The id is the `obs_…` id the log acknowledgement printed. Amending is append-only,
like every correction in the Ledger
([`../observations.md`](../observations.md) §"Append-only, corrected by
reference"): it **appends one new `workout` event** whose `refs.corrects` names the
session, carrying only the fields you changed. The original line is never
rewritten — it stays byte-identical, so the values you first logged remain in the
history. Readers fold the corrections onto the session at read time, the latest
correction winning per field, so the recommendation's recovery guardrail, the
progress trend, and `lucid workout --json` all see **one** session with the
corrected values.

| Flag | Effect |
|------|--------|
| `--rpe <0-10>` | Set the session RPE. Same range as `workout log`. |
| `--duration <minutes>` | Set the duration, in whole minutes (zero or more). |
| `--type <text>` | Set the session type. |
| `--movements <a,b,…>` | **Replace** the movements list. |
| `--parts <a,b,…>` | **Replace** the body parts trained. |
| `--notes <text>` | Replace the session note. |
| `--notes-file <path\|->` | Replace the note with the contents of a file, or of stdin for `-`. |
| `--day <date>` | Re-date the session (see below). |
| `--json` | Emit the machine-readable amend view instead of the acknowledgement. |

- **Only what you pass changes.** A field you leave off keeps the value it had —
  amending the RPE alone leaves the duration and type exactly as they were.
  Amend corrects a field; it doesn't clear one, so an empty value
  (`--type ""`, `--parts ""`) is refused.
- **Lists replace, they don't merge.** `--parts fingers,forearms` makes those two
  the session's body parts; restate the full list to add one or drop one. Within
  one call the list flags comma-split and repeat exactly the way `workout log`'s
  do, so `--parts fingers --parts forearms` is the same list.
- **Amend as often as you need.** Each amend is another event: the latest value
  per field wins, and every earlier value stays in the history. Any id in the
  chain works — passing the id of an earlier correction amends the same session
  it corrected.
- **Notes come off the command line when they need to.** `--notes-file` reads the
  note through the same file reader the other free-text verbs use, so `$`,
  backticks, `;`, `&`, quotes, and newlines are stored verbatim instead of being
  parsed by your shell. Give `--notes` or `--notes-file`, not both. (`workout log`
  takes `--notes-file` too.)

**Re-dating.** `--day` moves the session to another day using the same grammar
and strict tier as `workout log --day`
([`commands.md`](commands.md#backdating-with---day)). The session's instant,
precision, and logical day move together, so the trend (which reads the logical
day) and the recovery guardrail (which reads the instant) agree about when you
trained. A re-date is a complete amend on its own — `--day` is the one flag that
needs no other. A bare day records the day at approximate precision, as it does
on `log`; give a time (`--day "@yesterday 18:30"`) to keep an exact instant. A
day the grammar can't read, or one in the future, is refused.

Two things a re-date does **not** move, both known limits of this first version:

- **Soreness and pain readings stay where they were.** The `body_state` readings
  logged with the session keep their original day; re-dating moves the session,
  not its readings.
- **`/day` reads one day at a time.** It folds the corrections filed on the
  session's own day, so a re-dated session still appears under its original day
  in `/day`. The recommendation, the trend, and `workout --json` read across days
  and always show the session where it now belongs.

**What amend doesn't do.** Soreness and pain aren't amendable yet —
`--soreness`/`--pain` are refused, and body-state correction is a planned
follow-up. Daily anchors aren't amendable either: amend corrects logged sessions,
so an anchor-only capture is refused — including one that carries a note, since
a note describes the anchor and doesn't turn it into a session. There is no way to amend through a spoken
drop — amend takes flags only, with no model call.

Every refusal — an unknown id, an id that isn't a workout session, no fields to
change, words after the id, a future `--day`, an anchor-only target, a
`--soreness`/`--pain` flag, `--notes` with `--notes-file` — exits non-zero, says
why on stderr, and writes nothing. With the `workout` kind disabled, amend behaves
exactly like `log`: it prints the enable hint and writes nothing. The full list is
in [`../mvp/error-states.md`](../mvp/error-states.md) §"Workout module".

The acknowledgement names the session and the new correction's id — inventory, as
on `log`, with no score and no grade. `--json` emits
`{event_id, target_id, logical_date, changes}`: the new correction's id, the
session it corrects (the base session, even when you passed a later correction's
id), the logical day the correction landed on, and one
`{"from": …, "to": …}` entry per changed field — the value before this amend
(`null` when it was unset) and the new one. Fields are keyed by their record
names (`rpe`, `duration_min`, `type`, `movements`, `body_parts`, `note`); a
re-date reports `occurred_at` and `logical_date`.

## The daily slot

When enabled, the recommendation also fires once a day at `slot_time` (local,
default midday), delivered inside `lucid scheduler run` beside the Engine and the
companion. It composes the same way the on-demand command does — deterministic
pick, model phrasing, deterministic fallback — and delivers one idempotent,
read-back-verified message with the same never-silent degrade layering the
companion uses. It never inherits the chain marks; its time is your choice. See
[`../mvp/workout-module.md`](../mvp/workout-module.md) §"Surfaces" for the delivery
contract.

## Operational notes

- The workout companion runs its own small, disposable job database, separate from
  the Engine's, the companion's, and the weekly witness report's, and from the
  `~/.lucid` Ledger. It defaults to a path under your user config directory; set
  `LUCID_WORKOUT_DB` to override it. It holds only scheduling machinery — no Ledger
  truth — so it is safe to delete; it is rebuilt on the next run.
- `lucid scheduler status` prints this path beside the other three job stores, so
  the full set is visible in one command. See
  [`commands.md`](commands.md#scheduler-status).
- The delivery receipts that guard idempotency are written **only through the
  binary**. Never hand-edit them.

## Boundaries

The workout copy avoids medical and clinical claims — it offers options and names
the safe one, and points to professional care for concerning pain, the same stance
as [`../observations.md`](../observations.md) §9. The deterministic core owns the
pick, so the phrasing never has to command: it never tells you what you "should" do.
