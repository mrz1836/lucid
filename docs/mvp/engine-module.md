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
| L2 witness escalation | The witness's confirmed channel, after two consecutive misses | Fixed template: streak state and declared mode only — **never** journal content, capacity values, or any Mirror data | `engine/witness.json` with `confirmed_at` set by an explicit witness confirmation |

The witness consent additionally covers a **monthly heartbeat**: one
fixed line ("all quiet — N-day streak") to the same channel, same
template discipline. It exists so silence stays meaningful — the
witness's briefed backstop (engine §4) is "a full month of *total*
silence means ask once," which only works if a healthy system is never
totally silent. The heartbeat is part of the L2 send class, not a
fourth consent.

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
| `/status` | Read-only: current streak, rolling adherence vs declared mode, error-budget burn, days to next gate. This is the MVP's **L0 ambient surface**. | None |
| `/closeout skip` | Records an explicit miss for the logical day (honest zero, distinct from silence — silence is what the dead-man tripwire detects). | `engine/days/<day-id>.json` with `missed: true` |

`/chain` (editing chain config in-channel) is deliberately **not** a
command: parameters change at most once per weekly Retro (engine §5),
by hand-editing `engine/chain.json` — it is tiny and human-readable,
and making edits slightly effortful is the point.

### `/closeout` sequence

1. Resolve the **logical day**: if now < `rollover` (default 04:00
   local), the day is yesterday's date (engine §1).
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

## Storage additions — `~/.lucid/engine/`

```
~/.lucid/engine/
├── chain.json               # chain config — the only hand-edited file
├── witness.json             # witness contract + consent record
├── days/                    # one record per logical day (append-only per day)
│   └── 2026/07/day_2026_07_02.json
└── status.json              # derived projection — rebuildable, never hand-edited
```

New storage-adapter named ops: `write_engine_day`, `read_engine_day`,
`read_engine_days(window)`, `rebuild_engine_status`,
`read_engine_status`, `read_chain_config`, `read_witness_contract`.
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
  "away_mode": null
}
```

`chain_start` is set automatically on the first completed close-out
and never changed thereafter; all gate math derives from it.
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
  "sees": ["streak", "declared_mode", "escalation_state"],
  "stake_shared": true
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
  "corrections": []
}
```

`capacity` and `limiter_tag` live **only** here — they are Engine
telemetry, never surfaced to the witness view, and the raw journal
entry does not repeat them (the Mirror can be granted them later as a
consented covariate; the MVP keeps the trees separate).
`raw_entry_id` is a pointer for provenance; the Engine never
dereferences it.

### `status.json` (derived — rebuildable from `days/` + `chain.json`)

Current streak · longest streak · rolling 7-day and 30-day adherence
(each day scored against its **declared mode**: a Yellow day executed
at floors scores 1.0) · floor-day ratio · error-budget burn (isolated
misses in the trailing 30 days vs budget) · consecutive-miss count ·
`escalation_state` (`none | l1_fired | l2_fired`) · days to next gate.
Deleting this file and rerunning `rebuild_engine_status` must
reproduce it byte-for-byte from the same inputs.

## The tripwire (scheduled job)

One scheduled job, morning-run (default 09:00), wired through the
harness's native scheduler (cron):

1. Compute yesterday's logical day. If a day record exists with
   `completed: true` (or a floor-counting Away Mode night) → reset
   `escalation_state`, done.
2. No record, or `missed: true`: if the day before was completed →
   **L1**: post the fixed nudge template in the user's channel; set
   `escalation_state: l1_fired`.
3. Two consecutive missing/missed days → **L2**: post the fixed
   witness template to the witness channel (only if `witness.json`
   is confirmed and `l2_enabled`); set `escalation_state: l2_fired`.
   The message contains streak and declared-mode data only.
4. On the first run of each calendar month, additionally post the
   heartbeat line to the witness channel (if `witness.json` is
   confirmed) — see §"Consent amendment".
5. **L3 is not automated in the MVP.** The stake executes by the
   humans it binds; the tripwire's job ends at making the breach
   impossible to miss. A `l2_fired` state on `/status` is the record.

Dead-man semantics are preserved exactly: the tripwire fires on the
**absence** of a day record. Self-report (`/closeout skip`) produces
an honest miss but never suppresses escalation. The bell prompt is the
same job's evening sibling (post the chain label at `bell_time`).

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
| `status.json` corrupt/missing | Rebuild silently | — | Regenerated from `days/` | By design (P2) |
| Harness down at bell time | Nothing | — | None | **The chain runs anyway** — phone alarm is the Phase-0 fallback; a text-to-self is a valid record, backfilled via `corrections[]` (P10) |

## Acceptance criteria (build phases 8–10)

These extend [`acceptance-criteria.md`](acceptance-criteria.md); same
format applies (setup, test cases, verification commands, definition
of done). Phases 8–10 depend only on Phases 1–2 (scaffold + `/log`)
and may be built immediately after them — before the Mirror's
structuring/insight phases — since the Engine module is agent-free.

**Phase 8 — Engine scaffold + `/closeout`.**
Day records write correctly for both sides of the rollover boundary
(23:50 → today; 03:50 → yesterday); a same-day repeat is a no-op;
`chain_start` is stamped exactly once; the journal line lands in
`raw/` with `command: /closeout` and valid frontmatter; close-out
transcript shows ≤ (links + 3) prompts (two-minute budget by
construction).

**Phase 9 — Derived status + `/mode` + `/status`.**
`rebuild_engine_status` is deterministic (delete + rebuild ⇒
identical); adherence is mode-relative (fixture: a Yellow floor-day
scores 1.0, an undeclared floor-day scores 1.0 on links but Crux-less
Green scoring only applies once the Crux ships post-MVP — MVP scores
links only); `/mode` after `bell_time` is rejected; error-budget burn
matches a hand-computed 30-day fixture including one isolated miss
(spend) and no consecutive misses.

**Phase 10 — Tripwire.**
Simulated clock fixtures: completed day ⇒ no send; one missing day ⇒
exactly one L1 in-channel with the floor named; two consecutive ⇒
exactly one L2 to the witness fixture containing **zero** bytes of
journal/capacity data (grep the payload); L2 with unconfirmed
`witness.json` ⇒ blocked + user notified; recovery night ⇒ state
resets and L1 does not re-fire. All templates are static strings in
the repo — verify no LLM call in the tripwire path.

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
