# Lucid MVP — Observations Module

This page specifies the MVP slice of the observation & enrichment
layer in [`../observations.md`](../observations.md): micro-log
capture, the day view, one reference enricher, and the first exports.
Like the Engine module, it is **entirely agent-free** — deterministic
parsers, arithmetic, and template output; no LLM in any path
(architecture P9, "deterministic scripts before clever agents").

Precedence follows the same rule as [`engine-module.md`](engine-module.md):
this page and [`scope.md`](scope.md)
own *scope*; the older MVP docs own *conventions*.

## Binding rules inherited

1. **Inventory, never obligation** ([`../observations.md`](../observations.md) §0):
   no observation carries a streak, quota, target, or escalation — in
   the schema, in `/status`, in any ack. An ack is "logged" plus the
   id, nothing else.
2. **Sanctuary boundaries:** no agent context slice includes
   `~/.lucid/observations/`, `~/.lucid/registries/`, or any projection
   derived from them — enforced by path prefix at slice-build time,
   fail closed ([`agent-contracts.md`](agent-contracts.md),
   cross-cutting rules). Widening that later requires both a contract
   diff and a recorded per-instance opt-in in
   `observations/config.json`, default off. The witness view never
   touches these trees; the Engine never reads them. User-invoked
   projections (`/day`, exports) may join across trees — that is the
   user reading their own ledger.
3. **Capture never blocks** (P1/P10): a malformed or out-of-range
   micro-log is stored as an event with the **invoked kind** and
   `payload = {note: <full text verbatim>, parse: "partial"}` rather
   than rejected — the kind is preserved (the command name is
   unambiguous testimony), projections count it as an unknown-valued
   point, and catch-up happens only via the same deterministic parser
   appending a correction event on a version bump — never via an
   agent.
4. **Corrections are new events.** JSONL lines are never rewritten; a
   correction appends a fresh event with `refs.corrects` naming the
   target ([`../observations.md`](../observations.md) §2). Correcting
   an event leaves the original line byte-identical.
5. **Transport honesty:** micro-logs are structured health data
   transiting the chat surface like all capture
   ([`local-runtime.md`](local-runtime.md) §"privacy boundary"). When
   a user enables the `pain`, `elimination`, or `med` kinds on a chat
   harness, the enable step shows the transport caveat once.

## Commands (router additions)

| Command | Behavior | Writes |
|---------|----------|--------|
| `/pain`, `/ate`, `/drank`, `/bm`, `/mood`, `/slept` | Named shorthands, one line each, parsed per the grammar in [`../observations.md`](../observations.md) §4 — including the `@` backdating token (`@yesterday 19:30`), per-kind head rules, bare-form defaults, and dictation tolerance. All alias one router intent (`observation.capture`). | One event appended to `observations/YYYY/MM/obs_<logical_date>.jsonl` |
| `/obs <kind> ...` | Generic form for every other enabled kind (`symptom`, `sleep`, `med`, `intervention`, `measurement`, `memory`, `where`). | Same |
| `/obs where <place>` | Sticky location: writes a `context.location` event; creates/merges the place registry entry. | Event + `registries/places/<key>.json` |
| `/day [date\|yesterday]` | Read-only day view: engine day record + observations + enrichment + entry ids for one logical day, plus range events spanning it. No date resolves to the current **logical** day (and `yesterday` to the one before it), on the same rollover boundary that files events (§2) — so a 02:00 `/day` shows the day just lived, not an empty new one. | None |
| `/packet clinician [@<date>\|all]` | Renders the clinician packet projection per [`../observations.md`](../observations.md) §7 and posts only its *path*. Window: since the last packet export; **first-ever export: trailing 90 days**; `@<date>` overrides the window start, `all` exports everything. Header includes any standing `packet.clinical_context` lines from `observations/config.json`, verbatim; regimen derives from the most recent `taken: true` event per distinct med, and a med whose latest event is `taken: false` renders `(last logged: skipped <date>)` rather than disappearing. | Packet under `projections/` + one line appended to `projections/exports.log` (what, window, when, path) |

Acks return within one second; no LLM call exists in the path, so
offline capture works. `logical_date` derivation (rollover for exact
times only, calendar date for approximate/range, occurred_at's own
offset) is specified in [`../observations.md`](../observations.md) §2
and is the file-placement rule — a backdated event appends to the file
for its logical date, not today's.

Two `/packet clinician` rules beyond the table: **every** render
appends its `projections/exports.log` line — the MVP seed of the
disclosure log ([`../vision.md`](../vision.md) §7, "Record") — and
discovery is a deterministic template, not curiosity: the ack for
`/obs intervention` may append, at most once per 30 days, the line
`A clinician packet for appointments is available: /packet clinician.`
(last-pointer date in ephemeral scheduler state, like curiosity
ask-state — never the Ledger, never a standalone ping, never a
question).

## Storage additions

```
~/.lucid/
├── observations/
│   ├── config.json          # enabled kinds, curiosity budget, enrichers, key salt (hand-edited)
│   └── 2026/07/obs_2026_07_02.jsonl   # append-only, one file per logical day
├── registries/
│   ├── injuries/injury_<slug>.json    # filename = the full kind-prefixed key
│   ├── threads/thread_<slug>.json
│   ├── places/place_<slug>.json
│   └── eras/era_<slug>.json
└── projections/             # rebuildable — deletable wholesale, incl. the range index
                             # (exception: exports.log, the append-only export record)
```

New storage-adapter ops: `append_observation`, `read_observations(day|range|kind)`,
`update_registry(kind, key, patch)`, `read_registry(kind, key?)`,
`write_projection`, `read_day_view`, and **`fetch_enrichment(enricher, url)`**
— the *only* network path in the module (see below). Disk ops follow
the established discipline: adapter-only access; JSONL append-only
with whole-line fsync writes; event ids per the seq rule in
[`../observations.md`](../observations.md) §2 (max-seq + 1 from
well-formed lines, single-writer, never line count).

**Backup invariant (binding — supersedes the older Mirror-only rule):** `raw/`,
`observations/`, `registries/`, and `engine/` (minus `status.json`)
are the permanent, backup-critical trees; `processed/`, `insights/`,
`reflections/`, `engine/status.json`, and `projections/` (minus
`projections/exports.log`, which records what has left the instance
and is not recomputable) are
rebuildable. Registries are primary data — a place's coordinates and
an injury's notes exist nowhere else — with append-only
`status_history[]` per [`../observations.md`](../observations.md) §1.

**Registry keys:** derived with the `people/` wordlist algorithm
([`data-model.md`](data-model.md)) but **salted with a per-instance
random secret** (`key_salt` in `observations/config.json`, generated
at first run) — injury and place names come from tiny public
dictionaries, and unsalted hashes would make the "low-signal"
filenames trivially reversible. The filename is the full
kind-prefixed key: `registries/injuries/injury_a-cedar.json` where
the key *is* `injury_a-cedar`. (`people/` keeps its legacy unsalted
derivation in the MVP.)

### `observations/config.json` (example)

```json
{
  "version": 1,
  "key_salt": "<random, generated at first run>",
  "kinds_enabled": ["pain", "intake", "elimination", "mood"],
  "curiosity_budget_per_day": 1,
  "agent_slice_optins": {},
  "packet": {"clinical_context": ["in recovery — flag anything habit-forming"]},
  "enrichers": [
    {"name": "weather", "enabled": false, "sends": "quantized lat/lon + date",
     "endpoint": "open-meteo", "cadence": "daily"},
    {"name": "calendar-frame", "enabled": true, "sends": "nothing (local)",
     "cadence": "daily"}
  ]
}
```

`kinds_enabled` is the per-instance allowlist: a kind receives commands only
when it is listed here (a capture of an unlisted kind is rejected with the enable
hint, never silently stored). The default set stays small
(`pain`, `intake`, `elimination`, `mood`); every other kind in
[`../observations.md`](../observations.md) §3 is opt-in. The three
**companion-context kinds** — `withdrawal`, `habit_change`, and `commitment`
— are available to add here so the daily companion can render them, but they are
**off by default**: a fresh Ledger never lists them, and an operator adds them
only when they want to log and surface those signals. Enabling them changes
nothing about the sanctuary or witness boundaries — like every kind, they are
inventory the Engine never reads.

## The enrichment job

A scheduled job, distinct from the Mirror's optional cron and from the
Engine tripwire: when the Engine phases are built it co-schedules
after the tripwire; when they are not, it stands up its own scheduler
entry — **phase 12 does not depend on phase 10**, only on the
harness's scheduler existing. For yesterday's logical day (and any
still-missing prior days), for each enabled enricher:

1. **Idempotency check first:** if an event with the same
   (`logical_date`, `source: enricher:<name>`) already exists, skip.
   This is the mechanism behind "exactly one event per enricher per
   day."
2. **As-of location:** resolve the most recent `context.location`
   whose `logical_date` ≤ the target day — never the location current
   at run time. A backfill running after a move fetches weather for
   where the user *was*.
3. **Fetch through the adapter:** `fetch_enrichment` validates the URL
   against the per-enricher allowlist (pinned host from `endpoint`;
   parameter names restricted to latitude/longitude/date variants;
   coordinates pre-quantized to ≤ 2 decimals) and **the adapter — not
   the enricher — writes the outbound audit log from the exact URL it
   transmits**. "Coordinates and dates only" is enforced and logged by
   code the enricher doesn't control, not self-reported.
4. **Write atomically:** one `context.day` event per enricher, all
   fields in one payload — one line, one fsync, so a crash never
   leaves a partial set.
5. **Failures are logged, not silent:** a failed fetch is recorded in
   the same audit log (satisfying error-states' no-silent-state
   principle) and retried next run; revised upstream data appends a
   correction event (`refs.corrects`), never a sibling duplicate.

MVP reference enricher: Open-Meteo (free, **keyless** — keyed sources
are out of MVP scope; an API key is an identifier requiring its own
consent line). `calendar-frame` is pure local computation.

## Curiosity (MVP slice)

One optional micro-question per day, max, appended to a capture ack,
chosen by a deterministic template table (missing sticky location →
ask; pain event with no site → one clarifier). Backoff per
[`../observations.md`](../observations.md) §6: an ignored template is
suppressed 7 days, retired after 3 ignores until its condition
changes; ask-state lives in ephemeral scheduler state, never the
Ledger. No LLM; the table ships in the repo.

## Error states (extends [`error-states.md`](error-states.md))

| Trigger | Behavior | Disk effect |
|---------|----------|-------------|
| Unparseable micro-log | Store with invoked kind, `payload.parse: "partial"`, note verbatim; ack normally | Event written |
| Out-of-range scale (`/pain 15`, `/bm 9`) | Same partial path — never silently clamped | Event written |
| Disabled kind used | Reject with the enable hint ("`pain` isn't enabled — add it to observations/config.json") | None |
| Registry match ambiguous | Store text unmatched; no ref | Event written, `refs` empty |
| Enricher fetch fails | Log the failure in the outbound audit log; retry next run | Audit log line (backfill later) |
| Enricher config lacks coordinates | Skip that enricher; `/day` shows "no location on file" once | None |
| `/day` for a day with nothing | Honest empty: "No record for 2026-07-01." | None |
| JSONL corruption (truncated line) | Reader skips bad lines, reports count; writer always appends whole lines with fsync; seq derivation ignores malformed lines | Unaffected lines readable |

## Acceptance criteria (build phases 11–12)

Phase 11 depends only on phases 1–2 (scaffold + `/log`); phase 12
additionally needs a harness scheduler (shared with, but not dependent
on, Engine phase 10). Same format as
[`acceptance-criteria.md`](acceptance-criteria.md).

**Phase 11 — Micro-logs + registries + `/day`.**
Each shorthand writes a valid envelope (frozen field set); logical-day
attribution fixtures: 23:50 exact → today, 03:50 exact → yesterday,
`@yesterday 19:30` → yesterday's file, an excavated memory with
`occurred_at` 2014-09-01T00:00 approximate → the file for 2014-09-01
(not 08-31); `/bm 4` round-trips in under a second with an ack
containing no evaluative language (grep the ack templates: no
"streak", "good", "keep it up"); bare `/pain 6` and bare `/bm` are
valid events; `/mood wired` and `/pain 15` take the partial path with
kind preserved; a correction event leaves the original line
byte-identical; registry keys are salted and stable within the
instance; `/day` joins engine + observations + entries for a fixture
day, includes a spanning range event, and is byte-stable across
reruns; disabled kinds reject with the hint; grep
[`agent-contracts.md`](agent-contracts.md): the denylist names
`observations/` and `registries/` and no contract's inputs reference
them.

**Phase 12 — Enrichment + exports.**
With a place that has coordinates and `weather` enabled: exactly one
`context.day` event per enricher per logical day, source-attributed,
idempotent on rerun (fixture: run twice, count once); **as-of location
fixture:** location changes on day D, a delayed backfill for day D−1
runs on D+3 and carries D−1's `place_ref`; the adapter's outbound
audit log contains only pinned-host URLs with coordinate/date
parameters, coordinates quantized to ≤ 2 decimals (grep the log
against content words and full-precision coordinates); fetch failure
appends an audit-log line and no event; series export produces valid
CSV for pain/mood/capacity joined on `logical_date`; the first
`/packet clinician` render uses the trailing-90-day window and
renders the header (`packet.clinical_context` lines verbatim, active
injuries, current regimen — most recent `taken: true` per distinct
med; fixture: a med whose latest event is `taken: false` renders
`(last logged: skipped <date>)`, never dropped — episode count) +
capacity/mode + pain series with med/intervention markers,
**excludes note fields, location, and weather by default**,
is written under `projections/`, and only its path is posted to the
chat surface (grep the posted message for body content); every render
appends one `projections/exports.log` line (what, window, when,
path); two `/obs intervention` events a week apart append the
discovery line exactly once.

## What this module intentionally is not (MVP)

* **Not a nutrition database, calorie counter, or diet score.** Ever,
  for the coaching sense of "diet" ([`../observations.md`](../observations.md) §0, §9).
* **Not device integration.** No GPS, no health-kit, no wearable sync;
  location is typed by a human.
* **Not the correlation engine.** Episodes and correlates
  ([`../observations.md`](../observations.md) §7) are post-MVP
  projections; the MVP ships the series they'll be computed from.
* **Not excavation sessions or thread views.** The events and
  registries they need exist now; the surfaces come after the steel
  thread proves out.
* **Not agent-visible.** No Reflection over observations in the MVP;
  patterns still come only from the prose thread. Wiring observation
  projections into agent slices requires a future contract diff
  **plus** a per-instance `agent_slice_optins` entry, default off.
