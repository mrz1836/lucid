# Lucid MVP — Data Model

This page defines the **on-disk shape of a working Lucid MVP**. It
turns the storage adapter's named ops in
[`architecture.md`](architecture.md) §4 into concrete files, formats,
and schemas. The reference layout in
[`technical-spec.md`](../technical-spec.md) (SQLite, multi-layer
memory, graph edges, salience scores) is preserved as a future target;
the MVP gets there by upgrading this layout, not by replacing it.

The two design rules that drive everything below:

1. **Primary data is permanent.** The trees the user absolutely must
   never lose are `raw/`, `observations/`,
   `registries/`, and `engine/` (minus the derived `status.json`):
   each holds testimony or configuration that exists nowhere else.
   `processed/`, `insights/`, `reflections/`, `engine/status.json`,
   and `projections/` are rebuildable.
2. **Processed is rebuildable.** If the agents improve,
   `~/.lucid/processed/` and `~/.lucid/insights/` can be regenerated
   from `~/.lucid/raw/` plus the user's accept/reject responses.

These rules trace directly to
[`product-principles.md`](product-principles.md) §3 (local-first) and
§4 (capture first, structure later) and to the three-layer model in
[`technical-spec.md`](../technical-spec.md).

## Top-level layout

```
~/.lucid/
├── lucid.json              # tiny config (paths, defaults, agent versions)
├── raw/                    # immutable raw entries (.md)
│   └── 2026/05/raw_2026_05_05_19_42.md
├── processed/              # JSON extraction artifacts, one per raw entry
│   └── raw_2026_05_05_19_42.json
├── insights/               # validated insights (.md), one per accepted/nuanced
│   └── i_2026_05_05_a.md
├── people/                 # lightweight person references (.json)
│   └── person_a-river.json
├── sessions/               # thread/session metadata (.json) + channel memory
│   ├── session_2026_05_05_19_42.json
│   └── channel_lucid.md
├── reflections/            # weekly reflection records (.md)
│   └── reflection_2026_w18.md
├── engine/                 # Engine tree — schemas owned by engine-module.md
│   ├── chain.json
│   ├── witness.json
│   ├── storm.json
│   ├── profile.json
│   ├── days/2026/07/day_2026_07_02.json
│   └── status.json
├── observations/           # frozen-envelope events — owned by observations-module.md
├── registries/             # injuries, threads, places, eras — same key derivation as people/
└── projections/            # rebuildable views/exports — deletable wholesale
```

Subdirectories under `raw/` use a `YYYY/MM/` shard so a single
directory does not grow unbounded (and `engine/days/` shards the same
way). All other directories are flat in the MVP; sharding can be added
later without breaking ids.

The `engine/` tree's record schemas, mutability rules, and derived-file
semantics are owned by [`engine-module.md`](engine-module.md); the
`observations/`, `registries/`, and `projections/` trees by
[`observations-module.md`](observations-module.md). All follow every
convention on this page (local-TZ timestamps, append-only discipline,
adapter-only access, the `people/` key derivation for registry slugs)
and add two naming kinds: `day_YYYY_MM_DD` for logical-day records and
`obs_YYYY_MM_DD_<seq>` for observation events.

### Naming conventions

| Kind | Convention | Example |
|------|------------|---------|
| Raw entry id | `raw_YYYY_MM_DD_HH_MM` (creation time, local TZ). Append `_SS` if a same-minute collision is detected. | `raw_2026_05_05_19_42`, or `raw_2026_05_05_19_42_07` on collision |
| Processed artifact id | Same id as the raw entry it describes. | `raw_2026_05_05_19_42.json` |
| Insight id | `i_YYYY_MM_DD_<slot>` where `<slot>` is `a`, `b`, ... per day. | `i_2026_05_05_a` |
| Session id | `session_YYYY_MM_DD_HH_MM` (thread open time, same `_SS` rule). | `session_2026_05_05_19_42` |
| Person key | `person_<initial>-<word>` derived deterministically from the display name. See "person_key derivation" below. | `person_a-river` |
| Reflection id | `reflection_YYYY_wWW` (ISO week). | `reflection_2026_w18` |

People keys deliberately do not encode real names; the storage adapter
maintains a `display_name` field separately so the on-disk filenames
remain low-signal.

#### Time zone rule

All timestamps under `~/.lucid/` are ISO-8601 with the host's local TZ
offset (for example `2026-05-05T19:42:11-04:00`). The MVP does not
normalize to UTC, does not store a separate timezone field, and does
not assume DST behavior beyond what the host's clock reports. SQLite
migration preserves the offset on read by storing the literal string.

#### Same-minute id collision rule

Raw entry, processed artifact, and session ids all encode minute
precision. If `storage.write_raw` (or `write_session`) is called and
the candidate id already exists on disk, the writer appends `_SS`
(zero-padded seconds) and tries again. If `_SS` also collides, the
writer appends `_SSS` (a small monotonic counter, starting at 1)
until the id is unique. Writers never overwrite an existing id under
`raw/` or `sessions/`.

#### `person_key` derivation

Given a `display_name` from Structuring, the deterministic People
routine produces a low-signal slug as follows:

```text
normalized = lowercase(strip_punct_and_whitespace(display_name))
hash       = sha256(normalized)              # 32 bytes
b0, b1     = hash[0], hash[1]                # first two bytes
b2, b3     = hash[2], hash[3]                # next two bytes
N          = len(WORDLIST)                   # 256, fixed
word1      = WORDLIST[(b0 * 256 + b1) % N]
word2      = WORDLIST[(b2 * 256 + b3) % N]
key        = "person_" + word1[0] + "-" + word2
```

* `WORDLIST` is the fixed file at
  `~/projects/lucid/data/person_keys_wordlist.txt`, one one-syllable
  low-signal English word per line (river, pine, meadow, stone,
  wren, ...). The list is committed with the repo and is read-only;
  changing it is a breaking schema change.
* If the resulting `key` already exists in `~/.lucid/people/` for a
  **different** normalized name, the writer appends `-2`, `-3`, ...
  until unique (`person_a-river`, `person_a-river-2`, ...). This is
  the only reason a slug ever drifts from the deterministic output.
* `aka[]` on the people record carries every display variant ever
  seen so a future search can resolve "M." back to `person_a-river`.

Structuring never derives this slug itself; the People routine
inside the storage adapter does, immediately before
`storage.write_processed` runs (see
[`agent-contracts.md`](agent-contracts.md) §"How contracts compose").

### `lucid.json`

The single global config file. Tiny, hand-editable, agent-readable.

```json
{
  "version": 1,
  "home": "~/.lucid/",
  "raw_dir": "raw",
  "processed_dir": "processed",
  "insights_dir": "insights",
  "people_dir": "people",
  "sessions_dir": "sessions",
  "reflections_dir": "reflections",
  "wordlist_path": "data/person_keys_wordlist.txt",
  "recent_window": 7,
  "recent_window_max": 14,
  "intake_max_questions": 4,
  "ask_insights_cap": 50,
  "ask_reflections_cap": 12,
  "proposal_pause": {"unanswered_threshold": 3, "pause_days": 14},
  "person_dominance_threshold": 0.5,
  "agent_versions": {
    "intake": "intake-2026.05.0",
    "structuring": "structuring-2026.05.0",
    "reflection": "reflection-2026.05.0",
    "safety_consent": "safety-2026.05.0"
  },
  "bootstrap_mode": false
}
```

* `wordlist_path` is resolved relative to the repo root
  (`~/projects/lucid/`); the file is read-only and shipped with the
  repo, not under `~/.lucid/`.
* `recent_window` is the per-session Reflection window default;
  `recent_window_max` is the hard ceiling — the router refuses any
  configured value above this and clips to 14.
* `ask_insights_cap` and `ask_reflections_cap` are the slice caps the
  router applies to `/ask` (see
  [`agent-contracts.md`](agent-contracts.md) §3
  `reflection.answer_grounded`).
* `proposal_pause` configures the router-level proposal pause: after
  `unanswered_threshold` consecutive unanswered proposals, the router
  stops invoking `reflection.propose` for `pause_days` days (see
  [`agent-contracts.md`](agent-contracts.md) §3 "Proposal pause").
* `person_dominance_threshold` is the share of window entries
  mentioning one person above which `/reflect gate` and `/person`
  surface a dominance line — deterministic router copy, hypothesis
  language, gate cadence only (see
  [`agent-contracts.md`](agent-contracts.md) §3 "Gate recall").

Agent versions are stamped into every processed artifact and insight
so the system can later identify "this insight was produced by a prompt
we no longer use".

## Raw entries — `~/.lucid/raw/`

**Format:** Markdown with YAML frontmatter. One file per entry.

**Mutability:** **Immutable.** The storage adapter has no
`update_raw`. If the user wants to add detail, they create a new entry
that references the original by id.

### Schema

```markdown
---
id: raw_2026_05_05_19_42
recorded_at: 2026-05-05T19:42:11-04:00
occurred_at: 2026-05-05T18:00:00-04:00
occurred_at_precision: exact          # exact | approximate | range
occurred_at_end: null                  # set when precision: range
source: discord
session_id: session_2026_05_05_19_42
command: /checkin                      # /log | /checkin | /bootstrap | /closeout
intake_questions:
  - "What part of it stuck with you afterward?"
  - "How did that land for you, after?"
agent_versions:
  intake: intake-2026.05.0
bootstrap: false
---

# Entry

The dinner with M. and J. went sideways again. The bit where I tried
to push back on the plan and then dropped it — I just kind of agreed.

Annoyed. A little embarrassed. Not at them, more at myself for
folding.
```

### Field semantics

| Field | Required | Meaning |
|-------|----------|---------|
| `id` | yes | Stable, sortable identifier. |
| `recorded_at` | yes | When the user wrote this (always "now" at write time). |
| `occurred_at` | yes | When it happened. Equal to `recorded_at` for "now" entries. |
| `occurred_at_precision` | yes | `exact`, `approximate`, or `range`. Mirrors `technical-spec.md`. |
| `occurred_at_end` | no | Only set when `precision: range`. |
| `source` | yes | Harness identifier (`discord`, `cli`, future surfaces). |
| `session_id` | yes | The session/thread this entry belongs to. |
| `command` | yes | Which command produced it: `/log`, `/checkin`, `/bootstrap`, or `/closeout` (the nightly journal line — see [`engine-module.md`](engine-module.md)). |
| `intake_questions` | no | Present for `/checkin`; the questions Intake actually asked. |
| `agent_versions` | yes | Which agent versions touched the entry at write time. |
| `bootstrap` | yes | `true` when written during a `/bootstrap` session; Reflection.propose is suppressed for these. |

### Example: `/log` entry (no Intake)

```markdown
---
id: raw_2026_05_06_07_15
recorded_at: 2026-05-06T07:15:02-04:00
occurred_at: 2026-05-06T07:15:02-04:00
occurred_at_precision: exact
source: discord
session_id: session_2026_05_06_07_15
command: /log
agent_versions:
  intake: null
bootstrap: false
---

# Entry

Quiet day. Read for an hour. Felt fine.
```

### Example: bootstrap entry with approximate timestamp

```markdown
---
id: raw_2026_05_07_22_03
recorded_at: 2026-05-07T22:03:44-04:00
occurred_at: 2014-09-01T00:00:00-04:00
occurred_at_precision: approximate
source: discord
session_id: session_2026_05_07_22_03
command: /bootstrap
agent_versions:
  intake: intake-2026.05.0
bootstrap: true
---

# Entry

Around the time I started my second job. I remember a stretch of
months where I felt invisible in meetings.
```

## Processed artifacts — `~/.lucid/processed/`

**Format:** JSON. One file per raw entry, named by the raw id.

**Mutability:** Rebuildable. The storage adapter may overwrite, but
each write must record the agent version that produced it.

### Schema

```json
{
  "id": "raw_2026_05_05_19_42",
  "entry_id": "raw_2026_05_05_19_42",
  "produced_at": "2026-05-05T19:42:14-04:00",
  "agent_version": "structuring-2026.05.0",
  "emotions": [
    {"name": "annoyed", "rationale": "user said 'annoyed' directly"},
    {"name": "embarrassed", "rationale": "user said 'a little embarrassed'"}
  ],
  "themes": [
    {"name": "voice-not-heard", "rationale": "tried to push back, dropped it"},
    {"name": "self-criticism", "rationale": "frustration aimed at self for folding"}
  ],
  "people": [
    {"display_name": "M.", "person_key": "person_a-river", "first_mention": false},
    {"display_name": "J.", "person_key": "person_b-pine", "first_mention": true}
  ],
  "notes": "Pattern of testing-then-folding may be worth flagging for Reflection.",
  "rejected_proposals": [],
  "unanswered_proposals": []
}
```

### Field semantics

| Field | Required | Meaning |
|-------|----------|---------|
| `id` / `entry_id` | yes | Match the raw entry id. Two fields for legibility; values are equal. |
| `produced_at` | yes | When the artifact was written. |
| `agent_version` | yes | Stamps the prompt/version that produced this artifact. |
| `emotions[]` | yes | Short list, each with a one-line rationale. May be empty. |
| `themes[]` | yes | Short list of recurring or newly noticed themes. May be empty. |
| `people[]` | yes | Each entry pairs a `display_name` (as written) with a stable `person_key` (see below) and a `first_mention` flag. The on-disk artifact never has `person_key: null`; the Structuring agent emits `null` and the router runs `storage.update_person` (the deterministic People routine) before `write_processed`, which back-fills the slug per [`agent-contracts.md`](agent-contracts.md) §"How contracts compose". |
| `notes` | no | Optional free-text the agent wants to preserve. Not surfaced to the user. |
| `rejected_proposals[]` | yes | Appended to when Reflection's proposal is rejected. See "Insight provenance" below. |
| `unanswered_proposals[]` | yes | Appended to when Reflection's proposal goes unanswered. Each entry is `{shape_tag, proposed_at}` — an exact parallel to `rejected_proposals[]`, kept as a separate array because silence is not rejection (see [`agent-contracts.md`](agent-contracts.md) §2). |

### Empty / "no useful structure" case

When extraction fails or returns nothing useful, the artifact still
exists, with empty arrays and a `notes` value explaining why. This
keeps the loop honest:

```json
{
  "id": "raw_2026_05_06_07_15",
  "entry_id": "raw_2026_05_06_07_15",
  "produced_at": "2026-05-06T07:15:04-04:00",
  "agent_version": "structuring-2026.05.0",
  "emotions": [{"name": "calm", "rationale": "'felt fine'"}],
  "themes": [{"name": "rest", "rationale": "'read for an hour', 'quiet day'"}],
  "people": [],
  "notes": "Single short entry; not enough signal for a pattern proposal.",
  "rejected_proposals": [],
  "unanswered_proposals": []
}
```

## Validated insights — `~/.lucid/insights/`

**Format:** Markdown with YAML frontmatter.

**Mutability:** Mostly append-only. The body of an insight is set on
write; status updates (e.g. `last_confirmed_at`, `softened_at`,
`retired_at`) are appended in the frontmatter and in a small change
log section.

### Schema

```markdown
---
id: i_2026_05_05_a
created_at: 2026-05-05T19:43:50-04:00
status: accepted                      # accepted | retired
nuanced_from_proposal: true
provenance:
  raw_entry_ids: [raw_2026_05_05_19_42, raw_2026_05_03_21_10]
  processed_artifact_id: raw_2026_05_05_19_42
  reflection_prompt_version: reflection-2026.05.0
  framework: null                     # lens id + version once the frameworks layer ships; null = baseline voice
  user_response_kind: nuanced         # accepted | nuanced
  user_response_text: |
    Mostly yes. I'd say it's not always groups — it's more when
    M. is in the room.
status_history:
  - at: 2026-05-05T19:43:50-04:00
    kind: accepted
  - at: 2026-05-09T20:11:02-04:00
    kind: confirmed
last_confirmed_at: 2026-05-09T20:11:02-04:00
last_softened_at: null
retired_at: null
rule: "When I catch myself folding mid-sentence, finish the sentence."
rule_history:
  - at: 2026-05-05T19:44:20-04:00
    kind: stated
---

# Insight

When M. is in the room and a group decision is in motion, I tend to
test an idea once and back off if it isn't picked up immediately.

## Change log

- 2026-05-05: Accepted with the user's refinement above.
- 2026-05-09: Surfaced in `/reflect`; user confirmed it still fits.
```

### Field semantics

| Field | Required | Meaning |
|-------|----------|---------|
| `id` | yes | See the naming conventions table. |
| `created_at` | yes | When the insight was first written. |
| `status` | yes | `accepted` or `retired`. There is no `pending` — unanswered proposals never produce an insight. |
| `nuanced_from_proposal` | yes | `true` if the canonical statement is the user's refinement of Reflection's proposal. |
| `provenance.raw_entry_ids[]` | yes | Every raw entry the proposal referenced. |
| `provenance.processed_artifact_id` | yes | The processed artifact whose Reflection produced the proposal. |
| `provenance.reflection_prompt_version` | yes | Which Reflection prompt produced the proposal. |
| `provenance.framework` | yes (nullable) | Which interpretation lens framed the proposal (`<id> v<version>`, e.g. `stoicism v1`), or `null` for the baseline voice. Always `null` in the MVP — the field exists now so lens-framed insights are attributable the day the frameworks layer ships ([`../frameworks.md`](../frameworks.md) §5). |
| `provenance.user_response_kind` | yes | `accepted` or `nuanced`. |
| `provenance.user_response_text` | yes | Verbatim user response. |
| `status_history[]` | yes | Append-only log of state transitions. |
| `last_confirmed_at` | no | Updated when `/reflect` confirms the insight still fits. |
| `last_softened_at` | no | Updated when `/reflect` softens the insight. |
| `retired_at` | no | Set when the user retires the insight via `/reflect`. |
| `rule` | no | One line of user-stated intent, verbatim, attached at validation via the fixed rule prompt (asked once per insight, ever — skipping leaves `null` forever unless the user volunteers one later at recall). Testimony, not obligation: no streaks, no scores, no reminders (architecture §5). |
| `rule_history[]` | with `rule` | Append-only: `stated`, then `kept` / `lapsed` (both first-class, judgment-free) / `retired`, each stamped, from `/reflect` responses. Insight-to-action conversion and rule survival are computed from these at gate cadence only — never on a daily surface (P4). |

### Insight provenance and rejected proposals

A **rejected** proposal does not produce an insight. The processed
artifact records the rejection in its `rejected_proposals[]` array:

```json
{
  "rejected_proposals": [
    {
      "at": "2026-05-06T08:21:18-04:00",
      "reflection_prompt_version": "reflection-2026.05.0",
      "proposal_text": "One possible pattern: defaulting to defensiveness when family is the topic.",
      "user_response_text": "No — I'm not defensive in general. This one was different because I was tired.",
      "shape_tag": "family-defensiveness-default"
    }
  ]
}
```

The `shape_tag` is a short normalized label future Reflection runs can
check against to avoid re-proposing the same shape. It is intentionally
small so it can be regenerated from the proposal text alone.

## People references — `~/.lucid/people/`

**Format:** JSON. One file per person key.

**Mutability:** Mutable, but only via `storage.update_person`. The
adapter merges new mentions into the existing record.

### Schema

```json
{
  "person_key": "person_a-river",
  "display_name": "M.",
  "aka": ["M", "M."],
  "first_seen_at": "2026-04-12T19:01:55-04:00",
  "last_seen_at": "2026-05-05T19:42:11-04:00",
  "entry_refs": [
    "raw_2026_04_12_19_01",
    "raw_2026_04_28_07_03",
    "raw_2026_05_03_21_10",
    "raw_2026_05_05_19_42"
  ],
  "notes": null
}
```

### Field semantics

| Field | Required | Meaning |
|-------|----------|---------|
| `person_key` | yes | Stable, low-signal slug. The filename matches. |
| `display_name` | yes | Whatever the user actually wrote (or the latest version). |
| `aka[]` | yes | Other forms the same person has been written as. |
| `first_seen_at` / `last_seen_at` | yes | Lifecycle window. |
| `entry_refs[]` | yes | Raw entry ids in which this person was mentioned. |
| `notes` | no | Free-text, optional, off by default. The MVP does not prompt for or store relational profiles — that is a follow-on per [`product-principles.md`](product-principles.md) §1. |

The MVP does **not** record relationships, dynamics, or affect per
person. People records are extractive only; they exist so future
relational features have somewhere to grow into.

## Sessions and channel memory — `~/.lucid/sessions/`

**Format:** JSON for sessions, Markdown for the per-channel memory file.

### Session record

```json
{
  "id": "session_2026_05_05_19_42",
  "started_at": "2026-05-05T19:42:00-04:00",
  "ended_at": "2026-05-05T19:48:30-04:00",
  "harness": "openclaw",
  "channel_id": "channel_lucid",
  "thread_id": "thread_2026_05_05_19_42",
  "command": "/checkin",
  "raw_entry_ids": ["raw_2026_05_05_19_42"],
  "processed_artifact_ids": ["raw_2026_05_05_19_42"],
  "insight_ids": ["i_2026_05_05_a"],
  "rejected_proposal_count": 0,
  "agent_versions": {
    "intake": "intake-2026.05.0",
    "structuring": "structuring-2026.05.0",
    "reflection": "reflection-2026.05.0",
    "safety_consent": "safety-2026.05.0"
  }
}
```

Session records are the audit trail. Reading the most recent N
sessions gives Reflection its `recent_window` slice without having to
walk the whole `processed/` tree.

### Channel memory file

`~/.lucid/sessions/channel_lucid.md` is the small, agent-readable
pointer file described in [`local-runtime.md`](local-runtime.md). It
is **not** a transcript and **not** a profile.

```markdown
---
channel_id: channel_lucid
purpose: "Default home for /log, /checkin, /reflect."
voice_cue: "See product-principles.md §6 — trusted advisor, hypothesis language."
home: "~/.lucid/"
---

# Channel: #lucid

Recent sessions (most recent first):

- session_2026_05_06_07_15 — /log
- session_2026_05_05_19_42 — /checkin → insight i_2026_05_05_a (nuanced)
- session_2026_05_03_21_10 — /checkin → no pattern yet

The full session log lives under `~/.lucid/sessions/`.
```

The recent-sessions list is regenerated on each session close. It is
deliberately short — long enough to anchor the harness, short enough
that Reflection still pulls from `processed/` for real signal.

## Weekly reflections — `~/.lucid/reflections/`

**Format:** Markdown with YAML frontmatter. One file per ISO week.

**Mutability:** Append-only within a single week. New entries can be
added to the change log; the body summary is set once on first
`/reflect` of the week.

### Schema

```markdown
---
id: reflection_2026_w18
iso_week: 2026-W18
window_start: 2026-05-04T00:00:00-04:00
window_end: 2026-05-10T23:59:59-04:00
created_at: 2026-05-09T20:10:14-04:00
agent_version: reflection-2026.05.0
insights_surfaced:
  - id: i_2026_05_05_a
    response_kind: confirmed       # confirmed | softened | retired | unanswered
new_insight_ids: []
notes: null
---

# Weekly recall — week 18, 2026

This week I surfaced one validated insight from earlier in the week.
You confirmed it still fits.

## Change log

- 2026-05-09: Surfaced i_2026_05_05_a — confirmed.
```

`/reflect` only **reads** insights; it does not propose new ones. Any
new insights in a given week come from per-session Reflection on fresh
entries. `new_insight_ids` is therefore expected to be empty for most
weekly records.

## How the data model supports the steel thread

Each stage in [`steel-thread.md`](steel-thread.md) maps to a small
number of files:

| Stage | Reads | Writes |
|-------|-------|--------|
| Capture (`/log`) | — | `raw/<id>.md`, `sessions/<sid>.json` |
| Capture (`/checkin`) | — | `raw/<id>.md` (with `intake_questions`), `sessions/<sid>.json` |
| Structure | `raw/<id>.md` | `processed/<id>.json`, `people/<key>.json` |
| Reflection (propose) | `processed/<id>.json` + last N processed | (only writes on user response) |
| Validation — accepted/nuanced | `processed/<id>.json` | `insights/<iid>.md` |
| Validation — rejected | — | append `rejected_proposals[]` to `processed/<id>.json` |
| Validation — unanswered | — | append `unanswered_proposals[]` to `processed/<id>.json` |
| Validation — rule stated | — | set `rule` + append `rule_history[]` on `insights/<iid>.md` |
| Recall (`/reflect`) | `insights/*.md` filtered by week | `reflections/<rid>.md` (append), `rule_history[]` appends on response |
| Gate recall (`/reflect gate`) | `insights/*.md` (all accepted, cap 50) + `processed/*.json` people counts (deterministic, router-side) | `reflections/<rid>.md` (append), `rule_history[]` appends |
| Person view (`/person <name>`) | `people/<key>.json` + `processed/*.json` + `insights/*.md` (deterministic join, no LLM) | — (read-only) |

No SQL. No graph queries. Everything is a small named read or write
against the storage adapter.

## SQLite migration path

The MVP layout is a deliberate, lossless subset of the SQLite schema
in [`technical-spec.md`](../technical-spec.md). When SQLite is
introduced, the storage adapter's named ops stay the same; their
implementation moves from filesystem reads/writes to SQL.

The mapping below is the contract.

| File path | SQLite table(s) | Notes |
|-----------|-----------------|-------|
| `raw/**/*.md` | `entries` (immutable). One row per file. Frontmatter fields → columns. Body → `entries.body`. | `entries.recorded_at`, `entries.occurred_at`, `entries.occurred_at_end`, `entries.temporal_precision` map directly. |
| `processed/<id>.json` `emotions[]` | `emotions` | One row per emotion, joined to `entries` via `entry_id`. |
| `processed/<id>.json` `themes[]` | `themes` | One row per theme, joined to `entries` via `entry_id`. |
| `processed/<id>.json` `people[]` | `entities` + `person_entries` | `entities` for the canonical person; `person_entries` for the join. |
| `processed/<id>.json` `rejected_proposals[]` and `unanswered_proposals[]` | `reprocessing_queue` (with a "rejected proposal" status) **and** a small per-artifact log table that survives migration. | Rejection rationale and unanswered-shape entries stay addressable so Reflection can avoid re-proposing the same shape; the two arrays remain distinct because silence is not rejection. |
| `insights/<iid>.md` | `insights` + `memories` | `memories` carries the four dimensions from `technical-spec.md` (salience, type, confidence, activation). MVP-migrated rows default to `confidence='validated'`, `activation='active'`, `salience='unscored'`, `type='insight'` until the adaptive-evolution loop lands and starts assigning real salience. `rule` and `rule_history[]` migrate as a column plus a small history table, preserving the kept/lapsed record. |
| `people/<key>.json` | `people` (+ `relationships` once relational profile lands) | `aka[]` becomes a related table or JSON column. |
| `sessions/<sid>.json` | `sessions` | Direct map — the audit trail for capture and the source of Reflection's `recent_window` ([`../technical-spec.md`](../technical-spec.md) §"Database Schema"). |
| `sessions/channel_*.md` | A small `channels` table or kept as a file even after migration. | Channel memory is small enough that staying as a file post-migration is fine. |
| `reflections/*.md` | `reflections` | Direct map. |

Migration order (when it happens):

1. **Stand up SQLite alongside the file tree.** Both are written; the
   file tree remains canonical.
2. **Backfill from files.** A one-shot script ingests every existing
   `raw/`, `processed/`, `insights/`, `people/`, `sessions/`, and
   `reflections/` record into the corresponding tables.
3. **Switch reads.** Storage adapter named ops start reading from
   SQLite for performance-sensitive ops; writes still mirror to files
   for auditability.
4. **Promote SQLite to canonical.** Once equivalence is verified,
   files become an export format (the user can always
   `lucid export` and get the same Markdown/JSON layout back).

The file tree never goes away — it remains the export format and the
backup format. This keeps
[`product-principles.md`](product-principles.md) §3 (local-first) and
§9 (synthetic examples) intact across the transition.

## What the data model intentionally is not

* **Not a memory graph.** No edge tables, no traversal, no salience
  scores. Memory graph is an extension point in
  [`architecture.md`](architecture.md), not an MVP requirement.
* **Not a profile store.** There is no `profile.json` summarizing the
  user. Validated insights are the closest the MVP gets to a profile,
  and they are individual files, not a single document.
* **Not a goals or progress tracker.** No `goals/`, `progress/`, or
  framework-state directories. Coach surface is deferred per
  [`product-principles.md`](product-principles.md) §1. Insight rules
  are not goals: recorded testimony revisited at gate cadence, never
  tracked daily, never celebrated, never reminded — a rule that
  deserves teeth becomes an Engine commitment through a Gate.
* **Not a backup spec.** The user is responsible for backing up
  `~/.lucid/` (especially `~/.lucid/raw/`). The docs name this as a
  responsibility; the MVP does not implement automated backup.
* **Not synced.** Nothing under `~/.lucid/` is mirrored to a cloud
  service in the MVP. Shared profiles (per
  [`vision.md`](../vision.md)) remain a future possibility behind
  explicit per-relationship export, not a sync.

The next docs in the set turn this storage layout into agent contracts
([`agent-contracts.md`](agent-contracts.md)) and a build sequence
([`claude-code-workflow.md`](claude-code-workflow.md)).
