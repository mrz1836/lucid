# ADR-0014 — Contact check-ins: per-person cadences in a separate append-only store, a native Monday who's-due post, and the Sunday wrap as a read view

**Status:** Accepted.

## Context

People want to stay in touch with the people who matter to them within a time
frame they chose — "every four weeks or so" — without turning friendship into a
tracking project. The ask is small: on Monday, a short list of who to reach this
week; a one-line way to say "we talked" so that person drops off the list; and on
Sunday, a gentle look at who is still open. No conversation content, no
analytics, no scores.

People already live in the Ledger (`~/.lucid/people/`), and the person verbs
already resolve a name to one record through a shared resolver
([`../usage/commands.md`](../usage/commands.md) §"Resolving a subject"). Four
things shape how the rest is built:

* **`person set` cannot hold a cadence.** `person set` overwrites its fields in
  place. A cadence stored there would lose its history on every change, which
  breaks the append-only, receipt-on-every-write discipline (architecture P8)
  that every other record family keeps. A cadence, and every contact logged
  against it, needs its own append-only events — the way `focus`, `rep` and the
  anchor event store ([ADR-0013](0013-anchor-kinds.md)) already work.
* **"Check-in" is taken.** `/checkin` is the guided-capture flow behind
  `lucid serve` (Intake asks follow-up questions and writes a raw entry), and a
  harness may already keep a `checkin-sessions/` folder beside the Ledger. A
  verb or record family named "check-in" would collide with both, in code and in
  conversation.
* **The send ceiling.** Lucid does not send messages on the user's behalf beyond
  a short, pre-committed list
  ([`../mvp/product-principles.md`](../mvp/product-principles.md) §7). Any new
  scheduled message must sit behind an explicit, user-visible gate.
  [ADR-0009](0009-workout-companion.md) set the approved pattern for an extra
  surface — config-gated, off by default, posting only to the user's own
  channel — and the weekly witness report reused it. A friend is never a
  recipient: Lucid reminds the user and nobody else.
* **The fatigue constraint.** Keeping a list current is exactly the overhead
  that makes people drop a practice. A normal week must cost one Monday read and
  one short capture per person reached, and nothing else. The record must never
  ask for more than that, and it must never read as a judgment.

Binding constraints shape every choice below:

* **P3 — sanctuary.** `internal/storage` is the only package that touches
  `~/.lucid/`.
* **P9 — the runtime never depends on AI.** Every list, date and message here is
  a deterministic function of the records and the current logical day. No model
  is called anywhere in this feature.
* **Append-only, a receipt on every write** (P8). No verb edits or removes a
  stored record; corrections are new events.
* **Inventory, never obligation.** A cadence is the user's own reminder
  interval, not a target to grade them against. No streak, score or percentage
  exists anywhere.

All examples here are synthetic.

## Decision

Lucid adds **contact check-ins**: a per-person cadence (a window of N days or
weeks), a capture verb for "we were in touch", a deterministic **due view** over
ISO Monday–Sunday weeks, a config-gated **Monday post** that delivers the due
lists to the user's own channel, and a **Sunday wrap** that is simply the due
view read on Sunday. Cadences, contacts, snoozes and voids live in a new
append-only store, `contacts/`, owned by `internal/storage`.

### 1. The `lucid contact` verb group

A new top-level group, shaped like `rep`, `focus` and `anchor`:

```text
lucid contact <subject...> [--via <call|text|video|in-person>] [--note <text> | --note-file <path>] [--day <date>] [--json]
lucid contact every <subject...> <window> [--json]
lucid contact pause <subject...> [--json]
lucid contact resume <subject...> [--json]
lucid contact retire <subject...> [--json]
lucid contact snooze <subject...> [--json]
lucid contact void <id> [--json]
lucid contact due [--wrap] [--json]
```

* The bare form logs a contact. A multi-word subject joins into one name, as it
  does for `person rename`; `every` takes its window from the last argument.
* Every subject resolves through the shared person resolver: an exact
  `person_key` wins, then a `display_name`/`aka[]` match, with redirect
  tombstones followed forward. No match is refused (§P-4), more than one is
  refused with the candidate keys (§P-5), and the verb never guesses.
* Every write returns a receipt id (§2), as prose and under `--json` as
  `{receipt_id, logical_date, person_key, event}`.
* **The subcommand names shadow a person with the same literal name**, the same
  documented rule as `person` (`every`, `pause`, `resume`, `retire`, `snooze`,
  `void` and `due` always run the subcommand). Reach such a person by their
  `person_key`.

"Contact" says nothing about who reached out or how, which fits the rule that
contact in either direction counts. A top-level group also matches every other
Lucid feature family. Putting the verbs under `person` would bury the everyday
capture inside a group that already has seven subcommands; a `friend` group
would label family and everyone else as friends, and "talked" reads oddly for a
text.

### 2. Storage: a separate append-only record family

**The tree.** Events are JSONL day shards at
`~/.lucid/contacts/YYYY/MM/contact_YYYY_MM_DD.jsonl`, sharded by the event's
**logical day**, so a backdated contact files under its own day. Each write is
one appended, fsync'd line through `internal/storage`, which is the only package
that reads or writes the tree (P3). This is the `reps/` layout, file for file.

**The event record.** Fields, in this order:

| Field | Meaning |
|-------|---------|
| `id` | `contact_YYYY_MM_DD_<slot>` — the receipt |
| `schema` | `1` |
| `type` | `cadence`, `contact`, `snooze` or `void` |
| `person_key` | The subject's canonical key at write time |
| `logical_date` | The logical day the event is about (`YYYY-MM-DD`) |
| `recorded_at` | RFC3339 append time, local offset |
| `source` | `contact` |

Each type adds its own fields:

* `cadence` carries `action` (`set`, `pause`, `resume` or `retire`); a `set`
  also carries `window` (as typed, e.g. `4w`) and `window_days` (e.g. `28`).
* `contact` always carries `via` and `note`, each `""` when unset.
* `snooze` adds nothing.
* `void` carries `refs.voids`, the id of the event it corrects.

There is **no direction, initiator, content, duration, score or streak field**,
and none may be added: the record says only that the user and this person were
in touch on this day.

**Event ids.** `contact_YYYY_MM_DD_<slot>` uses the anchor-event slot grammar
(`SlotLabel`: `a`, `b`, … `z`, `aa`, …), minted as the first slot not used in
that day's shard — never the line count, so a malformed line never causes a slot
to be reissued. The first contact on 2026-10-29 is `contact_2026_10_29_a`.

**Folding.** State is a pure fold at read time, never a stored projection. Each
event's `person_key` is mapped through the redirect tombstones to its canonical
key before folding, so a cadence, a contact or a snooze recorded before a
`person merge` stays with the merged person, with no rewrite. A line that fails
validation is skipped by the fold, never a panic.

**Weekly delivery receipt.** The Monday post's per-ISO-week receipt is separate
operational state at `contacts/weekly_receipt.json`, written atomically through
`internal/storage` (§8). It holds no Ledger truth.

No model reads or writes any of this.

### 3. Cadence lifecycle and the window grammar

**The window grammar.** `Nd` or `Nw`, a whole number N ≥ 1. `28d` and `4w` are
the same 28-day window; the form as typed is kept for display ("every 4 weeks",
"every 28 days", "every week", "every day"). Zero, negative, fractional,
unit-less or unparseable input is refused before any write, with the grammar in
the message: `a window is Nd or Nw — a whole number ≥ 1 of days or weeks, e.g.
28d or 4w`.

**The lifecycle.** A person's cadence is `none`, `active`, `paused` or
`retired`.

| Verb | From | To | Notes |
|------|------|----|-------|
| `every <subject> <window>` | `none` or `retired` | `active` | A first set (the window-start rule below applies) |
| `every <subject> <window>` | `active` | `active` | Changes the window; keeps the anchor |
| `every <subject> <window>` | `paused` | `paused` | Changes the window; an explicit `resume` restarts it |
| `pause <subject>` | `active` | `paused` | Refused from any other state |
| `resume <subject>` | `paused` | `active` | Refused from any other state |
| `retire <subject>` | `active` or `paused` | `retired` | Refused with no live cadence (`none` or `retired`) |

Every row is one appended `cadence` event with its own receipt; history is never
rewritten. A refused transition writes nothing. Cadence events order by
`recorded_at`, then id.

**The window-start rules.** The window closes N days after its **anchor**:

* On a first set, or a set after retire, the window is anchored on the most
  recent non-voided contact, or on the day it was set when no contact exists.
  So a new `4w` with nothing logged first comes due four weeks later.
* A backdated contact logged after the set moves the anchor to that day.
* Resume anchors on the later of the last contact and the resume day, so a pause
  never puts someone straight past their window.
* Changing the window keeps the anchor.
* An old contact before a first set can put someone straight into "Still open
  from before", and that is honest: it is how long it has been.

Any later non-voided contact moves the anchor forward. A voided contact drops
out, and the anchor falls back to the next rule that applies. The close date is
the anchor plus `window_days` in civil-date arithmetic (no instants), so a DST
change can never shift it.

### 4. Contact capture

`lucid contact <subject>` records the person and a logical day:

* **The write is capture tier**, like `obs`: it runs immediately and leads with
  its receipt. Logging contact only ever makes the record more truthful, and
  `void` fixes a mishearing.
* **`--via`** takes `call`, `text`, `video` or `in-person`. Anything else is
  refused with the four values listed. Leaving it out is fine.
* **The note is one line**, given as `--note <text>` or `--note-file <path>`
  (`-` reads stdin), trimmed. The two forms are mutually exclusive, and a note
  with more than one line is refused. No content is ever required.
* **`--day`** reads the shared date grammar on the strict tier
  ([`../usage/commands.md`](../usage/commands.md) §"Backdating with --day"),
  including `@yesterday`. A future or unreadable day is refused and nothing is
  written. `recorded_at` is always the real write time.
* **Either direction counts.** The interface never asks who initiated.
* **It never creates a person.** An unknown subject is refused (§P-4) with a
  pointer to `lucid person create <name>`.
* **Contact without a cadence records history only.** It shows in
  `lucid person <name>` as the last time you caught up, and has no due effect.

The other contact writes take no `--day`: they stamp the current logical day by
design.

### 5. Snooze and void

**Snooze.** `lucid contact snooze <subject>` appends one `snooze` event that
keeps the person off both due lists and the wrap for the **ISO week of the
logical day it is written**. It takes no `--day`, so it can never target next
week. It leaves the window and the anchor untouched: the following Monday the
person appears exactly where their window puts them. A snooze needs an active
cadence and is refused a second time in the same ISO week. It is capture tier.

**Void.** `lucid contact void <id>` appends one `void` event naming a `contact`
or a `snooze`. A voided contact stops counting toward the window; a voided
snooze stops hiding the person. The history keeps both records. A void of an
unknown id, an already-voided event, a void, or a `cadence` event is refused and
writes nothing. **Cadence events are never voided**: the dated cadence events
*are* the history, so a wrong window or state is corrected with `every`,
`pause`, `resume` or `retire` — the precedent ADR-0013 set for `kind` events.

### 6. The due view

`lucid contact due` groups people against the ISO Monday–Sunday week of the
current logical day (the 04:00 rollover applies). Only people with an
**active** cadence, not snoozed this week, and not off-limits are grouped:

| Group | `--json` key | The window closes… |
|-------|--------------|--------------------|
| Due this week | `due_this_week` | This Monday through this Sunday |
| Still open from before | `still_open` | Before this Monday |
| Coming up next week | `coming_up` | Next Monday through next Sunday |

A window that closes later than next Sunday is not shown. Each group is ordered
by close date, then case-folded display name, then person key, and every surface
that shows these lists reads this one grouping.

**The line.** Each person line shows the cadence and the last contact, and
**never a count of days late**:

```text
Sam Rivera (every 4 weeks), last caught up Sep 28
Alex Kim (every 10 days), not logged yet
Jordan Lee (every 2 weeks), last caught up Dec 30, 2025
```

The year is added only when the last contact falls in another year.

**The human form** opens with the week (`Week of Nov 2 – Nov 8`), then the
sections `Due this week`, `Still open from before`, `Coming up next week` and
`Birthdays this week` (§7), each line prefixed `- `, with empty sections left
out. With no active cadences at all it reads
`No check-in cadences yet — set one with lucid contact every <name> 4w.`; with
cadences but nobody in the three groups it reads `Nobody's due this week.`, and
birthdays are still listed.

**The `--json` contract** is byte-deterministic for a given Ledger and day:

```json
{
  "day": "2026-11-04",
  "week": "2026-W45",
  "week_start": "2026-11-02",
  "week_end": "2026-11-08",
  "wrap": false,
  "due_this_week": [
    {"person_key": "person_s-river", "display_name": "Sam Rivera",
     "cadence": "4w", "cadence_days": 28,
     "last_contact": "2026-10-08", "window_closes": "2026-11-05"}
  ],
  "still_open": [],
  "coming_up": [],
  "birthdays": [
    {"person_key": "person_j-lantern", "display_name": "Jordan Lee",
     "date": "2026-11-04"}
  ]
}
```

* A person row is `{person_key, display_name, cadence, cadence_days,
  last_contact, window_closes}`; `last_contact` is `null` when nothing is
  logged. `window_closes` is a date, the ordering key — never a count.
* A birthday row is `{person_key, display_name, date}`, `date` being the day it
  is observed this week.
* Arrays are never `null`; an empty group is `[]`.
* `--wrap` drops `coming_up` and `birthdays` (§9).

`lucid person <name>` gains one line when contact events exist — the cadence
(or paused / retired) and the last contact — and an omitempty `contact` object
under `--json`. A person with no contact events renders byte-identically to
before (S-22).

### 7. Birthdays

Anyone with a `dob` who is not off-limits, cadence or not, appears in a
birthdays block when their birthday falls in this Monday–Sunday week. A snooze
or a pause does not hide a birthday.

* A Feb 29 `dob` is observed on Feb 28 in non-leap years.
* A week that crosses New Year works: Dec 31 and Jan 1 birthdays both land in
  the week of 2029-12-31.
* Birthdays are ordered by date, then name.
* They show in the due view and the Monday post, not in the wrap.
* There is no separate toggle: birthdays ride the Monday post's gate.

### 8. The Monday post

A new scheduler node, `internal/contactweekly`, delivers the due lists once a
week. It reuses the witness report's weekly-job shape and the shared
`flynode.Fire` reliability order (past the cut-off, idempotency, compose, send,
read-back verify, receipt), with **no model call** anywhere in the package.

**The fixed template.** Sections are joined by a blank line, and an empty
section is left out:

```text
Here's your week for staying in touch — no rush, the whole week is yours.

**Due this week**
- Sam Rivera (every 4 weeks), last caught up Sep 28

**Still open from before**
- Alex Kim (every 2 weeks), not logged yet

🎂 Birthdays this week: Jordan Lee (Wed Nov 4)
```

Several birthdays join with `, ` on the one line. "Coming up next week" shows
only in the read view. The post is capped at 1900 characters, cut at a line
boundary, and a cut post ends `…plus more in lucid contact due.`

**An empty week posts nothing.** If both lists and the birthdays line are empty,
the node returns before `flynode.Fire`: no send, no alert, a `nothing-due` skip
marker on the job run (`{skipped: true, skip_reason: "nothing-due", week}`) and
the log line `contact weekly: nothing due — no post`.

**Reliability**, inherited from the witness report:

* one bounded late attempt when the host was asleep at the mark;
* a 48-hour stale cut-off past the mark, after which the post is skipped and the
  user is alerted instead of posting a list days late;
* idempotency per ISO week through `contacts/weekly_receipt.json`: a retry whose
  receipt still reads back in the channel skips, and one whose message is gone
  re-delivers;
* send, verify and receipt failures alert as `flynode.Fire` already does.

**The `contact_weekly` config block** ships off and is validated like
`witness_report`:

```json
"contact_weekly": {
  "enabled": false,
  "weekday": 1,
  "time": "09:00"
}
```

An absent block, or an absent key, reads these defaults. When enabled, `time`
must be a valid `HH:MM` and `weekday` a cron weekday `0`–`6`, or the config is
refused naming the key. Like every block in `lucid.json` it is credential-dumb:
no channel id, no token.

**User channel only.** The post and every alert go to the user's own channel
(`LUCID_USER_CHANNEL_ID`). No code path addresses a friend. Because the template
is fixed and model-free, friend names never reach a model provider (P9). The
node keeps its own small, disposable job database (`LUCID_CONTACT_WEEKLY_DB`),
separate from the Ledger and the other nodes, and registers exactly one weekly
periodic (`lucid-contact-weekly`, cron `0 9 * * 1` by default).

### 9. The Sunday wrap is a read view

`lucid contact due --wrap [--json]` is the due view with only the two lists:
"Due this week" and "Still open from before". It is computed fresh on every
call, so there is no stored Monday snapshot to keep in sync, and it leaves out
"Coming up next week" and birthdays. Its human form opens
`Still open this week — no pressure; anyone here rolls into Monday's list.`
and, when both lists are empty, reads `Nobody's still open this week.`

A harness's existing Sunday pass reads it and folds it into a message it
already sends. **Lucid adds no Sunday post.** Unchecked people need no special
handling: next Monday their window has closed before that Monday, so they appear
under "Still open from before" on their own.

### 10. Boundaries

* **Off-limits people** can still be written about — a contact, a cadence and a
  snooze for them are your own record — but they never appear in the due view,
  the birthdays block, the Monday post or the wrap. This is the rule `gratitude`
  already set.
* **The witness report never reads contact data**, and v1 has no setting to
  include it.
* **Lucid never messages, schedules or contacts a friend.** It reminds the user,
  in the user's own channel, and nothing else.
* **Ethos.** The only status word is "due", never "overdue". There are no
  streaks, scores, adherence percentages or day counts of lateness anywhere,
  in output or in data. "Still open from before" is a gentle carry-over, not a
  late list.
* **Capture tiers for a harness.** `contact` and `snooze` are capture tier: run
  immediately, receipt first. `every`, `pause`, `resume`, `retire` and `void`
  change what the lists mean, so a harness echoes them back and waits for a yes.
* **Only an explicit contact write counts.** `gratitude thank`, a mention in an
  entry, or anything else never counts as contact.

### 11. Nice-to-haves: three shipped, two deferred

**Shipped in v1:**

* **Birthdays** (§7), because they reuse the existing `dob` field and the Monday
  gate.
* **The one-week snooze** (§5), because it is one more append-only event type
  and leaves the window untouched.
* **The skill's natural-language mapping** — "talked to Sam yesterday" becomes
  `lucid contact Sam --day @yesterday`, "who do I need to catch up with" becomes
  `lucid contact due` — because it maps onto verbs that already exist.

**Deferred:**

* The **group defaults** (one cadence for everyone in a group) wait, because
  the people model has no group concept, and they would need a new record type.
* The **rep solo-day fallback** (offering a contact as the day's rep when
  nobody is around) waits, because it would tie the rep and companion surfaces
  to contact data.

## Consequences

* **A new record family and a new scheduler node.** `contacts/` joins the
  Ledger's append-only families, and `internal/contactweekly` joins the
  scheduler's nodes, off until a user enables it.
* **The Monday post sits inside the §7 ceiling** because it is gated by an
  explicit, user-visible config switch, renders a fixed template, calls no
  model, and goes only to the user's own channel — the ADR-0009 pattern.
* **A normal week costs one read and one capture per person.** Nothing asks the
  user to account for anyone; an unchecked person simply carries over.
* **Merges are safe.** Because folds canonicalize through redirect tombstones, a
  later `person merge` reunites a split history with no rewrite.
* **Not yet backed up or validated.** Like `focus/` and `reps/`, `contacts/` is
  not yet in the ADR-0002 backup manifest, and `lucid validate` does not yet
  check the store. Both are listed under Future work.

## Alternatives considered

* **The verbs under `person`** (`person cadence`, `person contacted`, `person
  due`). Rejected: the everyday capture verb would sit inside a group that
  already has seven subcommands.
* **A `friend` group** (`friend talked`, `friend due`). Rejected: it labels
  family and everyone else as friends, and "talked" reads oddly for a text.
* **A native Sunday wrap post as well.** Rejected: it adds a Sunday-evening
  message where a harness's Sunday pass already exists; the read view covers it.
* **Harness first** (Lucid ships only the read views; a harness cron posts on
  Monday). Rejected: the weekly cycle would only work with a harness, and most
  of the feature would live outside the open-source repo.
* **Riding the daily companion** (a due block in Monday's morning message, the
  wrap in Sunday's night message). Rejected: it would depend on the companion
  being on, and friend names would reach the model provider.
* **Due right away when nothing is logged.** Rejected: setting up ten people at
  once would put all ten on one Monday.
* **Asking for a last-contact date at set time** (`every --last <date>`).
  Rejected: a backdated contact already records the real last contact, with its
  own receipt.
* **One list with everyone due by Sunday.** Rejected in favor of two lists, so
  carried-over people are visible as such; the copy keeps that second list
  gentle.
* **One list, names only.** Rejected: it drops the cadence and the last contact,
  the context that makes the list useful.
* **The cadence as a `person set` field.** Rejected: `person set` overwrites in
  place, so history would be lost.
* **A single JSON event file** (the anchor event store's envelope). Rejected:
  no write here needs the whole store to mint an id, so append-only day shards —
  the `reps/` layout — are simpler and never rewrite earlier lines.

## Future work

* **Group defaults**, once the people model has a group concept.
* **The rep solo-day fallback**, if tying rep to contact data ever earns its
  keep.
* **Backup-manifest coverage** for `contacts/`, together with `focus/` and
  `reps/`.
* **`lucid validate` coverage** for the contact store (schema, ids, void
  targets).
* **An optional native wrap post**, behind its own gate, for users with no
  harness.
