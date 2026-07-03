# Lucid — Observation & Enrichment Layer

**Date:** 2026-07-03 · **Status:** Canonical — a living concept, evolving with the project
**Scope:** The structured half of the Mirror's capture surface defined in
[`architecture.md`](architecture.md) §3: body signals, intake, mood,
context, and memory fragments — plus the registries they reference, the
enrichers that annotate them, and the projections that make them
useful. This document contains no instance data; which kinds a user
enables lives in their calibration.

**Design notes (from adversarial review).** Corrections are reference
events rather than in-place edits (an in-place `corrections[]` field
would be unimplementable under append-only JSONL); the `refs` contract
is explicit; the micro-log grammar carries per-kind head rules, the
`@` backdating token, dictation tolerance, and defaults for bare
forms; `intervention` is a first-class kind and the pain payload
carries clinically load-bearing optional fields; enricher rules
require keyless endpoints, IP disclosure, coordinate quantization, and
as-of location semantics; the clinician packet is windowed, headed,
and notes-off-by-default; curiosity has a backoff rule.

## 0. The governing corollary

**Observations are inventory, never obligation.** This layer extends
the sanctuary rule (architecture P3) to the body: no observation kind
may ever carry a streak, a quota, a target, a score, or an escalation.
You log what happened; the system never grades it. Logging food is not
a diet program; logging pain is not a compliance chart; a day with no
observations is a day with no observations. If a practice ever deserves
teeth, it becomes an Engine commitment through a Gate — a separate,
deliberate act. The two never blur: **the Engine enforces acts;
observations are words about the body, and words are sanctuary.**

## 1. Position in the foundation

Lucid's foundation is three parts, and this layer adds instances of
each without changing any of them:

| Part | Holds | This layer adds |
|------|-------|-----------------|
| **Ledger** (events, append-only, bitemporal) | What happened | New event *kinds*: observations, context annotations, memory fragments |
| **Registries** (long-lived referents; `people/` is the precedent) | What things are | Injuries, threads, places, eras |
| **Projections** (rebuildable views) | What it means | Series, episodes, correlates, the day view, the clinician packet |

The extension rule (architecture §"Extension model") is what makes
this future-proof: capabilities are added only as new kinds, new
registries, new enrichers, or new projections. The event envelope
below is **frozen** — it is the contract every future tool builds on.

One clarification the foundation table implies but deserves stating:
**registries are primary data, not projections.** A place's
coordinates, an injury's clinical notes, a thread's intent statement
exist nowhere else — registries are backup-critical alongside `raw/`
and `observations/`, and registry records carry an append-only
`status_history[]` (the insights precedent) so status transitions
(active → managed → resolved) are recorded, never overwritten. Merge
semantics: patches add or replace fields; removing a field requires an
explicit null patch; every patch appends to `status_history[]` with a
timestamp.

## 2. The event envelope (frozen)

Every observation is one JSON object, appended to a per-logical-day
file (`~/.lucid/observations/YYYY/MM/obs_YYYY_MM_DD.jsonl`):

```json
{
  "id": "obs_2026_07_02_003",
  "schema": 1,
  "kind": "pain",
  "recorded_at": "2026-07-02T21:45:10-04:00",
  "occurred_at": "2026-07-02T18:00:00-04:00",
  "occurred_at_precision": "exact",
  "occurred_at_end": null,
  "logical_date": "2026-07-02",
  "source": "microlog",
  "payload": { "intensity": 6, "site": "knee", "note": "aching after the long run" },
  "tags": ["running"],
  "refs": { "injury": "injury_a-cedar" }
}
```

Envelope semantics, binding:

* **The envelope never changes.** New needs go in `payload` (versioned
  by `schema`, per kind), `tags`, or `refs` — never new top-level
  fields. Projections must tolerate unknown kinds and higher schema
  versions (read what you understand, skip what you don't).
* **Append-only, corrected by reference.** No line is ever rewritten.
  A correction is a *new* event whose `refs.corrects` names the target
  id; a re-remembered memory references its predecessor via
  `refs.prior`. Readers fold corrections onto their targets at read
  time; the original line stays byte-identical forever.
* **The `refs` contract.** `refs` is a flat object. Keys are either a
  registry kind (`injury`, `thread`, `place`, `era`, `person` — value
  is a registry key) or a reserved relational key: `corrects` (this
  event amends the named event), `prior` (this event supersedes or
  re-remembers the named event), `entry` (the related raw entry).
  Values are a single id or an array of ids; id type is discriminated
  by prefix (`obs_`, `raw_`, `injury_`, …). New reserved keys may be
  added (tolerate-unknown applies); the semantics of existing keys are
  frozen.
* **Bitemporal.** `recorded_at` is always now; `occurred_at` (with the
  same precision/range fields as raw entries) can be any time in the
  past — this is what makes memory excavation (§8) an ordinary write.
* **`logical_date` is the universal join key.** Engine day records,
  observations, enrichment, and raw entries all resolve to the same
  logical day. Derivation, binding: when precision is `exact`, apply
  the rollover boundary (engine §1) to `occurred_at`'s **own wall
  clock in its own recorded offset** (never re-projected into the zone
  current at write or read time); when precision is `approximate`, use
  the plain calendar date of `occurred_at` (no rollover — approximate
  times are placeholders, and a midnight-anchored "around September
  2014" must not file under August 31); when precision is `range`, use
  the calendar date of the range start. File placement follows
  `logical_date` — a memory about 1999 lives in
  `observations/1999/05/…` and `/day 1999-05-01` shows what is known
  about that day, while `recorded_at` preserves when you actually said
  it. Range events are indexed by their start day; day views
  additionally surface any range event spanning the requested day
  (§7). During travel, an Engine day record and an observation's
  `logical_date` can legitimately differ by a day; Away Mode is the
  sanctioned handling, and the discrepancy is documented, not a bug.
* **Ids.** `obs_<logical_date>_<seq>`: seq = max seq parsed from
  well-formed lines in the target day file, plus one — computed under
  the storage adapter's single-writer discipline, never derived from
  line count. Zero-padded to three digits, parsed numerically, wider
  values legal (`_1000`). Every appended line — micro-log, enricher,
  correction — consumes a seq. Note that obs ids encode the *logical
  date*, not creation time: an excavated memory gets an id dated
  decades before its `recorded_at`.
* **`source`** is `microlog`, `checkin`, `closeout`, `excavation`,
  `extraction` (a structuring-pass proposal the user accepted through
  the resonance gate — the accepting event referenced in `refs`), or
  `enricher:<name>` — provenance for every event, so machine-added
  context is always distinguishable from human testimony.

## 3. Observation kinds (initial vocabulary)

Kinds are enabled per-instance; disabled kinds simply never get
commands. Scales follow clinical standards where one exists, so the
record is legible to professionals without translation. Every kind's
payload accepts `note?` (the grammar routes trailing text there).

| Kind | Payload (schema 1) | Scale / convention |
|------|--------------------|--------------------|
| `pain` | `intensity`, `site?`, `side?`, `quality?`, `radiates_to?`, `interference?`, `note?` | **0–10 NRS** (the clinical standard). `site` free text, matched to the injury registry when unambiguous; `side` left/right/bilateral; `quality` one word (burning, aching, electric…); `interference` 0–10, PEG-style "how much did it interfere today". All optional — `/pain 6` alone is a valid event. |
| `symptom` | `name`, `severity?`, `note?` | severity 0–10 when given; name free text. |
| `intake` | `class` (food/liquid/supplement), `what`, `amount?`, `note?` | Free text first — "what you ate," never a nutrient database. `/ate` → class food; `/drank` → class liquid; supplements via `/obs intake supplement …`. **All medication goes through the `med` kind**, never intake — one home, so the med record is never fragmented. Normalization is a projection's job, years later if ever. |
| `elimination` | `class` (bm/urine), `bristol?`, `note?` | **Bristol 1–7** for BMs. Bare `/bm` is a valid event ("it happened"), digit optional. |
| `mood` | `level`, `word?`, `note?` | 1–5 digit plus one word (hyphenate multiword: `burnt-out`) — the same idiom as the Engine's capacity signal, deliberately. |
| `sleep` | `bed?`, `wake?`, `quality?`, `note?` | quality 1–5. A sleep event always describes **the night ending on the morning it is logged**: bed → `occurred_at`, wake → `occurred_at_end`, precision `range` — so `logical_date` is the night's *start* day, regardless of input form (a time-less `/slept q2` at 09:00 anchors approximately to the prior logical day). Correlates pair sleep on night D with day D+1. Wearable import is a future enricher, never a nightly duty. |
| `med` | `what`, `dose?`, `taken` (boolean, default true; false = deliberately logged skip), `effect?` (helped/partial/none), `note?` | Adherence *record*, not adherence *enforcement* (§0). |
| `intervention` | `what`, `provider?`, `body_site?`, `note?` | PT session, injection, procedure, appointment — the "what have you tried since last visit" record; renders as timeline markers in the clinician packet (§7). |
| `measurement` | `metric`, `value`, `unit`, `note?` | Weight, BP, HRV, anything numeric. Reviewed at gate cadence only (P4). |
| `context.location` | `place_ref`, `note?` | **Sticky**: "I'm in Lisbon" holds until the next one. Stated by the user, never harvested from a device. **Sensitive by default**: excluded from every export and packet unless explicitly included per export; the off-limits registry applies to it as a kind; and stated plainly — sticky locations form a permanent trail on a non-wipeable ledger, so choose your coarseness accordingly (a city is plenty). |
| `context.day` | per-enricher, one event per enricher per day | Written by enrichers only (§5). `occurred_at` = the logical date at local noon, precision `approximate`. |
| `memory` | `text`, `certainty` (vivid/hazy/reconstructed), `era_ref?`, `note?` | See §8. |

Adding a kind is a one-row diff to this table plus a payload schema —
no envelope change, no new subsystem.

## 4. Micro-logs — the capture grammar

Observations are captured by one-line, deterministic, agent-free
commands (sub-five-seconds, offline-capable, no LLM — the same design
stance as the Engine module):

```
/pain 6 knee aching after the long run
/pain 8 shoulder @09:00-12:30 after loading the van
/ate eggs, toast, coffee @yesterday 19:30
/drank 500ml water
/bm 4
/mood 2 wired
/slept 2340 0710 q3
/obs med ibuprofen 400
/obs intervention physio session left-knee
/obs where Lisbon
```

Grammar, binding:

* **Head rules, per kind.** Each head consumes **at most one bare
  numeric slot** (the kind's scale digit); any later bare number
  belongs to the note (`/pain 6 shoulder 2 weeks` → note "2 weeks").
  For `intake`, a leading token is `amount` only when unit-suffixed
  (`500ml`, `40g`, `x2`); a bare integer belongs to `what` (`/ate 3
  eggs` → what "3 eggs"). For `mood`, `word` is the single token after
  the digit (hyphenate multiword); the rest is note. For `pain`,
  `site` is the token(s) after the digit up to the longest unambiguous
  injury-registry match, else one token; `left`/`right`/`bilateral`
  anywhere in the head sets `side`.
* **Backdating.** An `@` token anywhere in the line overrides
  `occurred_at`: `@18:30` (today, that time), `@yesterday`,
  `@yesterday 19:30`, `@2026-07-01`, `@09:00-12:30` (a range →
  `occurred_at_end`). Backdated times are precision `approximate`
  unless a full timestamp is given. This is how the bitemporal
  envelope is reachable from the primary capture surface — yesterday's
  dinner is `/ate … @yesterday`.
* **Defaults for bare forms.** `/pain 6` → valid event, no site
  (curiosity may ask one clarifier). `/bm` → valid elimination event,
  no digit. A shorthand missing its *required* scale digit (`/mood
  wired`) → stored with the invoked kind, `payload = {note: <full
  text>, parse: "partial"}` — the kind is preserved because the
  command name is unambiguous testimony about what was being logged,
  and projections include such events as unknown-valued points rather
  than dropping them. Out-of-range scale values (`/pain 15`) take the
  same partial path — never silently clamped.
* **Tags and note.** `#tags` are copied into `tags[]` **and remain in
  the note unchanged** — the note is verbatim, always.
* **Dictation tolerance.** The parser also accepts keyword forms —
  `quality 3` for `q3`, `bristol 4`, colon-less times (`2340`, `7 10`),
  spelled digits zero–ten for the scale slot — all deterministic table
  lookups, no LLM.
* **Registry matching** only when unambiguous; the parser never
  guesses, unmatched text stays as text.
* `/obs <kind>` is the generic form; `/pain`, `/ate`, `/drank`, `/bm`,
  `/mood`, `/slept` are aliases into the same single router intent.

This is still capture-first (P1): one line, no form, no follow-up
required — structure here is *shorthand the user chose*, not ceremony
the system imposed. Prose still works: "Knee screamed all afternoon"
in a `/log` or journal line is a first-class capture, and the
structuring pass may propose an extraction *through the resonance
gate* (landing as `source: extraction`). Micro-logs exist because a
digit you'll actually type nightly beats a paragraph you won't.

## 5. Enrichers — the world's half of the record

An **enricher** is a deterministic, scheduled source that appends
`context.day` events — never mutates, never blocks, never asks. This
generalizes the Engine's Tier-2 rule (engine §3) to the whole Ledger:
passive context is unbounded and free; **no enricher may ever require
daily user effort.**

Reference set:

| Enricher | Sends outbound | Writes |
|----------|----------------|--------|
| `weather` (e.g., Open-Meteo) | quantized lat/lon + date, nothing else | temp, precipitation, pressure, humidity for the as-of location |
| `daylight` | quantized lat/lon + date | sunrise/sunset, day length |
| `calendar-frame` | nothing (local computation) | weekday, ISO week, holiday flag |

Consent and mechanics, binding (extends architecture §5):

* **Opt-in per source**, with the `sends` field declared exactly as
  above in the instance configuration. **Fetches are not sends**: an
  enricher performs read-only queries and posts no messages anywhere;
  the message-send ceiling (three Engine templates plus the witness
  heartbeat) is untouched by this layer.
* **Outbound minimalism, verified at the adapter.** All enricher
  network traffic goes through one adapter op which validates the URL
  against the per-enricher allowlist (pinned host, parameter names
  restricted to coordinate/date variants) and writes the outbound
  audit log itself — the code that opens the socket is not the code
  that self-reports. Coordinates are **quantized before any send**
  (≤ 2 decimal places, ~1 km). Failed fetches are recorded in the
  same log (no silent state) and retried next run.
* **Keyless endpoints only** in the reference set. An API key is a
  stable identifier; any keyed source requires its own consent line
  naming that. The consent text also names the unavoidable residue:
  every fetch reveals the host's IP address to the endpoint.
* **Idempotent by construction:** exactly **one** `context.day` event
  per enricher per logical day, all fields in one payload (one line,
  one atomic append); before writing, the job checks for an existing
  event with the same (`logical_date`, `source`) and skips. Revised
  upstream data appends a correction event (`refs.corrects`), never a
  sibling duplicate.
* **As-of location:** the location for a target day is the most recent
  `context.location` event whose `logical_date` ≤ that day — never the
  location current at run time. A backfill running three days after a
  move fetches weather for where you *were*.
* Location comes only from the user's sticky statements. Device
  integration (GPS, phone, email, calendar content) is a future,
  separately-consented chapter; nothing in this layer depends on it.

Barometric pressure next to a pain series is the canonical payoff —
the correlation chronic pain patients always suspect, finally
checkable against *your* data.

## 6. Curiosity — how the system asks for more

The system may ask small contextual questions, under rules that keep
asking from becoming friction:

* **Budgeted.** At most `curiosity_budget` micro-questions per day
  (default 1), attached to the ack of a capture that already happened
  — never a standalone ping.
* **Deterministic.** Chosen by what's missing for the logical day (no
  sticky location? pain with no site?). A template table, not a model.
* **Skippable, always — with backoff.** Silence is a complete answer.
  An ignored template is suppressed for 7 days, and after 3 total
  ignores it is retired until its underlying condition changes. The
  ask itself (template id + date) lives in ephemeral scheduler state,
  not the Ledger; only the non-answer is never recorded.
* **Sanctuary.** Curiosity is Mirror-side; ignoring it can never touch
  Engine state.

Deeper asking — "tell me more about that" across an era, a thread, or
a memory — is the excavation surface (§8), invited and session-shaped,
not ambient.

## 7. Projections — where the data becomes power

All views are rebuildable artifacts under `~/.lucid/projections/`,
computed by deterministic scripts (LLM analysis happens *on top of*
projections at review cadence, per P9). Named projection kinds:

* **Day view** (`/day [date]`) — one logical day joined across
  everything: engine day record, observations, enrichment, entry list,
  plus any range event spanning the day (found via a rebuildable range
  index under `projections/`). The "what was Tuesday actually like"
  answer.
* **Series** — any observation field over time as CSV/JSON: pain by
  site, Bristol, mood, capacity (see cross-tree rule below), weather.
* **Episodes** — auto-segmented runs (a flare = consecutive days above
  a user-set threshold), segmented **per site/injury ref** (falling
  back to all-pain only when site is absent), with a gap tolerance —
  up to N consecutive *unlogged* days (default 1) do not break a run,
  because the worst flare days are often the unlogged ones, and
  unlogged is explicitly distinct from below-threshold. The episode
  table carries a "days logged / days spanned" column so gaps stay
  visible.
* **Correlates** — candidate associations with lags (pain vs. sleep,
  vs. pressure, vs. intake classes, vs. weekday), computed with honest
  statistics and presented **through the resonance gate** as
  observations-about-data ("pain runs ~1.3 points higher the day
  after quality-2 sleep — 41 paired days of 88 in range") — never as
  causes, never as advice. Every surfaced correlate reports coverage
  alongside effect size; correlates below a coverage floor are
  computed but never surfaced, because logging density is itself
  correlated with symptom state. Accepted correlates become insights
  with provenance like any other.
* **Clinician packet** — an export for a care appointment, windowed
  (default: since the previous packet export). One-page header,
  derived deterministically: active injuries (registry status
  active/managed), current regimen (most recent dose per distinct
  med), episode count and durations in range. Then: capacity/mode
  series (already promised by engine §2), pain and symptom series with
  med and intervention events as markers on the same timeline, and the
  user-selected subset of everything else. **Note fields are excluded
  by default** (per-export opt-in); location and weather series
  likewise. The packet is written to `projections/` and only its
  *path* is ever posted to a chat surface. The user chooses
  resolution; opaque tags stay opaque unless they decide otherwise.
  The packet is the first shipped **aperture** — the per-recipient
  sharing contracts of [`vision.md`](vision.md) §7 (depth rings,
  registers, the disclosure log); later aperture formats (therapy
  packet, counsel brief) are new projections under the same
  render → review → release → record discipline.
* **Thread views** — see registries, below.

**Cross-tree rule, binding:** agent context slices never include the
engine tree, observation files, registries, **or any projection
derived from them** ([`mvp/agent-contracts.md`](mvp/agent-contracts.md),
cross-cutting rules — enforced by path prefix at slice-build time,
fail closed). Widening that — wiring an observation projection into an
agent slice — requires *both* a contract diff *and* a recorded
per-instance opt-in in the observations configuration, per projection
kind, default off. Projections invoked *by the user* (a `/day` call,
an export script) may join across trees — that is the user reading
their own ledger, not an agent reading it. The witness layer remains
computed from Engine events only; **no observation, projection, or
enrichment is ever witness-visible.**

## 8. Registries and the long game

Registries generalize the `people/` pattern: long-lived referents with
low-signal keys, merge-updated via the storage adapter (merge and
history semantics in §1), referenced from events via `refs`.

* **Injuries** (`injury_<slug>`): name, onset (bitemporal — old
  injuries backfill), status with history (active/managed/resolved),
  linked events, clinical notes at owner-chosen resolution. The injury
  inventory the doctor never has time to take.
* **Threads** (`thread_<slug>`): a named thing being worked on — inner
  or outer. Intent statement, domains, status, linked events and
  insights. **The obliquity guard is structural: threads have no
  progress number, no percent, no streak.** A thread's "progress" is
  its narrative — what the linked events say happened — reviewed at
  gate or quarterly cadence (P4). Threads are how "get better at X"
  lives in the system without becoming a metric that kills X.
* **Places** (`place_<slug>`): stated locations; optionally lat/lon
  (added once, by the user) so enrichers can work.
* **Eras** (`era_<slug>`): named life chapters with date ranges
  ("the first apartment," "the year of the move"). Memory fragments
  attach to eras, making the past browsable by chapter rather than by
  date you can't remember.

**Memory excavation.** A `memory` event is an ordinary observation
whose `occurred_at` is years old (precision: approximate or range) and
whose `certainty` field says how it was recalled — *vivid*, *hazy*, or
*reconstructed*. That honesty field matters: future analysis can
weight testimony accordingly, and re-remembering later appends a new
fragment with `refs.prior` naming the old one — recall improves,
history never rewrites (P2). The weekly Retro's excavation block
(engine §5) is one fragment per week; `/bootstrap` sessions are bulk
intake; era-guided excavation sessions ("walk me through that
kitchen") are a future Mirror surface, off-limits-registry-aware, that
needs no new foundation — the envelope above already carries
everything they will produce, including the event-to-event references
(`refs.prior`, `refs.corrects`) that progressive recall requires.

**The corpus bet.** All of this compounds toward one asset: a
lifetime, owner-held, plain-text corpus of what actually happened —
body, mind, places, people, weather, memory — with provenance and
consent built into every record. Tools will keep getting smarter;
the corpus is the part only you can accumulate, and only if the
formats hold. That is why the envelope is frozen, the schemas are
versioned and documented here, everything is exportable as a
directory of plain files, and nothing proprietary sits between you
and your own record (P6).

## 9. Boundaries

* **Never diagnosis, never treatment advice.** Projections and any
  analyst output frame findings as data for the user and their care
  team. The Safety gate's clinical-language rules apply to every
  surface this layer grows.
* **Sanctuary and witness-blindness** as stated in §0 and §7 —
  structural, not configurable. Partial-parse catch-up is performed
  only by the same deterministic parser (appending correction events
  on a version bump), never by an agent; observation events are never
  eligible for the prose structuring pass.
* **Off-limits registry** (architecture §5) applies: any kind, tag,
  registry entry, or era can be excluded from inference entirely.
  `context.location` is sensitive by default (§3).
* **Intake logging is never a diet program** — restated because the
  failure mode is real: no calorie targets, no daily compliance, no
  "you didn't log lunch." If an eating practice ever earns teeth
  (e.g., a protein floor), it does so as an Engine link through a
  Gate, and the observation layer still just takes inventory.
* **Wearables and device data**: only if a device is already owned,
  only as an enricher, never as a nightly duty (engine §3 Tier 2).

## 10. Defaults

Pain scale 0–10 (NRS) · Bristol 1–7 · mood/sleep-quality 1–5 ·
interference 0–10 · curiosity budget 1/day, backoff 7 days, retire at
3 ignores · episode gap tolerance 1 day · coordinate quantization 2
decimals · clinician packet window = since last export, notes and
location excluded · enrichers off until opted in · all kinds off until
enabled · sticky location coarse (city) unless the user chooses finer.
All instance-overridable with reasons (P8).
