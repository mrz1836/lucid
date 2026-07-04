# Lucid MVP — Engine Module

This page specifies the MVP slice of the **Engine** subsystem defined in
[`../engine.md`](../engine.md) and [`../architecture.md`](../architecture.md) §3.
It extends the Mirror steel thread in [`steel-thread.md`](steel-thread.md)
with the unified nightly loop: **the Engine's two-minute close-out is a
capture surface** — one act feeds both subsystems.

It follows the same conventions as the rest of this doc set: module
charters, named storage ops, binding schemas, per-phase acceptance
criteria. Where this page and an older MVP doc disagree about *scope*
(the old docs predate the Engine), this page and
[`scope.md`](scope.md) win; where they
disagree about *conventions* (naming, mutability, adapter discipline),
the older docs win.

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
2. **Teeth attach to acts, never to content** (architecture **P3**).
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
| `/mode <green\|yellow\|red>` | Declare today's mode. Rejected after today's bell time (engine §2: fixed at the Bell; no retroactive amendment). | `engine/days/<day-id>.json` (mode field, append-style: first declaration wins after bell) |
| `/status` | Read-only: current streak, rolling adherence vs declared mode — always co-presented with the floor-day ratio and raw days-accounted (the **honest-number pairing**, engine §3) — error-budget burn, days to next gate; plus `stake_owed` when a breach has outlived the stake execution window (engine §4), and "witness lapsed — L2 disarmed" while the witness contract is lapsed (see §`witness.json`). This is the MVP's **L0 ambient surface**. | None |
| `/closeout skip` | Records an explicit miss for the logical day (honest zero, distinct from silence — silence is what the dead-man tripwire detects). | `engine/days/<day-id>.json` with `missed: true` |
| `/closeout backfill [yesterday\|<YYYY-MM-DD>] [<compact form>]` | Creates or corrects a record for a recent past day — the chain **ran** but went unrecorded (P10's text-to-self path, now with a command). Deterministic; accepts the same compact form as `/closeout`. See sequence below. | `engine/days/<day-id>.json` (created with `"backfilled": true`, or a `corrections[]` append); one raw entry via `storage.write_raw` (`command: /closeout backfill`); rebuilt `engine/status.json` |
| `/storm <clause-label\|unwritten>` | Declare a storm citing a Charter clause label (registered in `storm.json`) or `unwritten` (engine §4). Stands only on witness confirmation within the confirmation window (72 hours); ambush windows enter automatically from their `storm.json` dates, no per-event confirmation. Ack while pending: `storm declared (<label>) — pending witness confirmation (72h).` Ack once standing: `storm standing through <date> (<label>) — undeclared days default to red; the stake is stayed; contact continues.` Renewal is the same command re-issued before expiry, once. | `engine/storm.json` (append-only `history[]`); rebuilt `engine/status.json` |
| `/storm end` | End a standing storm early; expiry at the duration bound is otherwise automatic. Breach math resets at exit (engine §4). | `engine/storm.json` (append-only `history[]`); rebuilt `engine/status.json` |
| `/profile <name>` | Switch to a named clock profile defined in `chain.json` (definitions change only at a Retro — engine §2). Sticky, recorded, **effective from the next logical day, never the current one**; the outgoing day completes under the clocks that started it. | `engine/profile.json` (append-only `history[]`) |

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
hand edit. Default target: the most recent logical day without a
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

## Storage additions — `~/.lucid/engine/`

```
~/.lucid/engine/
├── chain.json               # chain config — the only hand-edited file
├── witness.json             # witness contract + consent record
├── storm.json               # storm clauses, ambush windows, append-only history
├── profile.json             # active clock profile + append-only switch history
├── days/                    # one record per logical day (append-only per day)
│   └── 2026/07/day_2026_07_02.json
└── status.json              # derived projection — rebuildable, never hand-edited
```

New storage-adapter named ops: `write_engine_day`, `read_engine_day`,
`read_engine_days(window)`, `rebuild_engine_status`,
`read_engine_status`, `read_chain_config`, `read_witness_contract`,
`read_storm_state`, `append_storm_event`, `read_profile_state`,
`append_profile_event`.
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
  "bell_time": "21:30",
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
  "escalation": {"l1_enabled": true, "l2_enabled": false, "tripwire_time": "09:00"},
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
day in question.
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
  "clauses": ["clause-1", "clause-2"],
  "windows": [
    {"label": "window-1", "start": "2026-11-02", "end": "2026-11-09"}
  ],
  "duration_days": 14,
  "history": [
    {"at": "2026-07-14T07:05:00-04:00", "event": "declared", "label": "clause-1"},
    {"at": "2026-07-14T09:40:00-04:00", "event": "confirmed", "by": "J.",
     "text": "confirmed — that's real. stake stayed.", "through": "2026-07-28"}
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
  rejected (see §"Error states").
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
Floors count as full completions for all teeth math; the co-numbers,
never a score, are what keep the record honest without shame math.
`rebuild_engine_status` computes over **folded** day records
(`corrections[]` applied in array order) plus the storm and profile
histories. Deleting this file and rerunning `rebuild_engine_status`
must reproduce it byte-for-byte from the same inputs — corrections,
storms, and profile switches included.

## The tripwire (scheduled job)

One scheduled job, morning-run at the active profile's
`tripwire_time` (default 09:00), wired through the harness's native
scheduler (cron) — the bell prompt, its evening sibling, likewise
posts at the active profile's `bell_time`:

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
   No new sends exist for any of this.

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

## Error states (extends [`error-states.md`](error-states.md))

| Trigger | Behavior | User message | Disk effect | Recovery |
|---------|----------|--------------|-------------|----------|
| `/closeout` twice same logical day | Idempotent no-op | "Already closed out — streak N." | None | — |
| `/mode` after bell time | Reject | "Mode is fixed at the bell (21:30). Tonight runs as declared; the budget absorbs hard days." | None | Retro annotation |
| Close-out interrupted mid-flow | Write partial | "Saved what we got — floor still counts if the survival link ran." | Day record `partial: true` | `/closeout` again appends corrections |
| Tripwire can't reach witness channel | Fall back | L1-style message to user: "L2 fired but couldn't reach <witness> — you owe the message." | `escalation_state: l2_fired` | Manual |
| Clock/rollover ambiguity (e.g., travel TZ) | Trust host clock | — | Local-TZ timestamps per [`data-model.md`](data-model.md) | Away Mode pre-specs the trip |
| `/closeout backfill` beyond `backfill_window_days` | Reject | "That's outside the backfill window (7 days). The record stands — the budget absorbs it; the Retro can annotate context." | None | Retro annotation |
| `/closeout backfill` targeting a day with `completed: true` | Idempotent no-op | "Already closed out — streak N." | None | — |
| Correction attempts an immutable field | Reject | "Mode and day identity are fixed at the bell — no retroactive amendment (engine §2)." | None | — |
| `/storm` citing an unknown label | Reject | "No clause or window by that name — clauses live in the Charter and are registered in storm.json. `/storm unwritten` if life exceeded the list." | None | Quarterly writes the missing clause |
| `/storm` unconfirmed for 72 hours | Lapse | L1-style note: "storm declaration lapsed — no confirmation within 72h. Declare again, or talk to <witness>." | `storm.json` history: declaration stands unconfirmed; no storm state | Re-declare |
| `/storm` renewal beyond the one allowed | Reject | "A storm renews once (engine §4). Past that, this is a season, not a storm — it goes to the quarterly." | None | Quarterly review |
| `/profile` naming an undefined profile | Reject | "No profile by that name — profiles are defined in chain.json, at a Retro (engine §2)." | None | Retro defines it |
| `status.json` corrupt/missing | Rebuild silently | — | Regenerated from `days/` | By design (P2) |
| Harness down at bell time | Nothing | — | None | **The chain runs anyway** — phone alarm is the Phase-0 fallback; a text-to-self is a valid record, backfilled via `/closeout backfill` (P10) |

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
