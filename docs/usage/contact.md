# Staying in touch — contact check-ins

**Contact check-ins** help you keep in touch with the people who matter to you,
inside a time frame you choose — "every four weeks or so" — without turning
friendship into a tracking project. You give a person a **cadence**, you say
"we talked" when you were in touch, and Lucid keeps one short list of who is
due. On Monday it can hand you that list for the week; on Sunday you can look at
who is still open. That is the whole feature.

The design record is [`../adr/0014-contact-check-ins.md`](../adr/0014-contact-check-ins.md);
the full verb reference is [`commands.md`](commands.md#contact). All names on
this page are synthetic.

## What it is, and what it is not

- **A reminder for you, never a message to a friend.** Lucid never contacts,
  messages or schedules anything with anyone you track here. The only message it
  can send is the optional Monday post, to **your own** channel.
- **No scores.** There are no streaks, no percentages, no "days late", and the
  only status word is "due". Someone you did not get to this week is simply
  still open, and rolls into next Monday's list.
- **No content.** Checking someone off needs only their name. An optional
  one-line note and how you were in touch (`--via`) are there if you want them.
- **Deterministic and model-free.** Every list, date and message is computed
  from your own records. No model is called anywhere in this feature.
- **Append-only.** Every write is a new event with a receipt id; a mistake is
  corrected with `void`, never by editing.

## Setting a cadence

```sh
lucid contact every Sam Rivera 4w
lucid contact every Alex Kim 10d
```

A window is `Nd` or `Nw` — a whole number ≥ 1 of days or weeks. `28d` and `4w`
are the same 28-day window; Lucid keeps the form you typed for display ("every 4
weeks", "every 28 days"). Zero, negative, fractional, unit-less or unreadable
windows are refused, the message shows the grammar, and nothing is saved.

Setting a cadence prints a receipt and when the window closes:

```text
Sam Rivera: every 4 weeks — next due by Nov 26 (contact_2026_10_29_b).
```

**When it first comes due.** The window is anchored on the most recent contact
you have logged with that person, or on the day you set it when nothing is
logged yet. So a new `4w` with nothing logged first comes due four weeks later —
setting up ten people at once never puts all ten on one Monday.

**Recording a real last contact.** If you last talked two weeks ago, log that
contact with its day, before or after setting the cadence; it becomes the
anchor:

```sh
lucid contact Sam Rivera --day 2026-10-12
```

**Why someone can land straight in "Still open from before".** If the last
contact you logged was long ago, the window may already have closed the moment
you set it. That is honest — it is how long it has been — and the ack says so:

```text
Riley Moss: every 2 weeks — open now; they'll be on Monday's list (contact_2026_11_04_a).
```

Running `every` again changes the window and keeps the anchor.

## Logging contact

```sh
lucid contact Sam Rivera
lucid contact Sam Rivera --via call
lucid contact Alex Kim --via text --note "sent the photos from the hike"
lucid contact Jordan Lee --day @yesterday
lucid contact Riley Moss --note-file /tmp/note.txt
```

```text
Logged contact with Sam Rivera on 2026-10-29 (contact_2026_10_29_a).
```

- **Either direction counts.** They called you, you texted them, you ran into
  each other — it is all contact, and Lucid never asks who reached out.
- **`--via`** is one of `call`, `text`, `video` or `in-person`, and optional.
  Anything else is refused with the four values listed.
- **The note is one line**, given as `--note <text>` or `--note-file <path>`
  (`-` reads stdin, which keeps shell characters like `&` off the command line).
  The two are mutually exclusive, and a note with more than one line is refused.
- **`--day`** backdates with the shared date grammar
  ([Backdating with --day](commands.md#backdating-with---day)), including
  `@yesterday` and `YYYY-MM-DD`. A future or unreadable day is refused and
  nothing is saved.
- **It runs immediately.** Logging contact is a capture, like `lucid obs`: the
  receipt comes first, and `void` fixes a mistake.
- **People without a cadence.** You can log contact with anyone you have a
  record for. It shows in `lucid person <name>` as the last time you caught up,
  and has no effect on any list.
- **It never creates a person.** A name that matches nobody is refused, and the
  message points at `lucid person create <name>`.

## Snoozing a week

```sh
lucid contact snooze Alex Kim
```

A snooze keeps the person off every list for the rest of **this** week — the
Monday–Sunday week of the day you write it — without touching their window. The
following Monday they appear exactly where their window puts them. A snooze
needs an active cadence, takes no `--day` (so it can never reach into next
week), and works once per week per person.

## Correcting with `void`

```sh
lucid contact void contact_2026_10_29_a
```

`void` corrects a contact or a snooze by its receipt id. A voided contact stops
counting toward the window; a voided snooze stops hiding the person. Both records
stay in the history. An unknown id, an already-voided event, a void, or a cadence
event is refused — a cadence is corrected with `every`, `pause`, `resume` or
`retire` instead.

## Pausing, resuming and retiring

```sh
lucid contact pause Jordan Lee     # off the lists for now; the window is kept
lucid contact resume Jordan Lee    # back on, from the later of the last contact and today
lucid contact retire Jordan Lee    # stop the cadence; history stays
```

- **Pause** works on an active cadence. Running `every` while paused changes the
  window and stays paused.
- **Resume** works on a paused cadence. The window restarts from the later of
  the last contact and the resume day, so coming back from a pause never puts
  someone straight past their window.
- **Retire** ends a cadence (active or paused). A later `every` starts fresh,
  anchored like a first set.

Each is a new event with a receipt; nothing is rewritten.

## The due view

```sh
lucid contact due
lucid contact due --json
```

The due view groups everyone with an active cadence against **this week,
Monday to Sunday**, by the day their window closes:

- **Due this week** — the window closes this Monday through this Sunday.
- **Still open from before** — the window closed before this Monday.
- **Coming up next week** — the window closes next Monday through next Sunday.

Later windows are not shown. Paused, retired and snoozed people stay off the
lists. Within each list people are ordered by the day their window closes, then
by name.

A synthetic week — today is Wednesday, Nov 4, 2026:

| Person | Cadence | Last caught up | Window closes | Shows under |
|--------|---------|----------------|---------------|-------------|
| Alex Kim | every 2 weeks | not logged yet (set Oct 14) | Wed Oct 28 | Still open from before |
| Sam Rivera | every 4 weeks | Oct 8 | Thu Nov 5 | Due this week |
| Jordan Lee | every 3 weeks | Oct 20 | Tue Nov 10 | Coming up next week |
| Casey Park | every 4 weeks | Oct 30 | Fri Nov 27 | — (later) |
| Riley Moss | every 2 weeks | Oct 16 | Fri Oct 30 | — (snoozed this week) |

```text
Week of Nov 2 – Nov 8

Due this week
- Sam Rivera (every 4 weeks), last caught up Oct 8

Still open from before
- Alex Kim (every 2 weeks), not logged yet

Coming up next week
- Jordan Lee (every 3 weeks), last caught up Oct 20

Birthdays this week
- Jordan Lee — Wed Nov 4
```

Each line shows the cadence and the last contact — or "not logged yet" — and
never how late anyone is. The year appears only when the last contact was in
another year (`last caught up Dec 30, 2025`). With no cadences set yet the view
reads `No check-in cadences yet — set one with lucid contact every <name> 4w.`;
with nobody in the three lists it reads `Nobody's due this week.` (birthdays are
still listed).

**`--json`** is deterministic for a given Ledger and day:

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
  "still_open": [
    {"person_key": "person_a-cedar", "display_name": "Alex Kim",
     "cadence": "2w", "cadence_days": 14,
     "last_contact": null, "window_closes": "2026-10-28"}
  ],
  "coming_up": [
    {"person_key": "person_j-lantern", "display_name": "Jordan Lee",
     "cadence": "3w", "cadence_days": 21,
     "last_contact": "2026-10-20", "window_closes": "2026-11-10"}
  ],
  "birthdays": [
    {"person_key": "person_j-lantern", "display_name": "Jordan Lee",
     "date": "2026-11-04"}
  ]
}
```

Arrays are never `null`; an empty list is `[]`. `window_closes` is a date, used
for ordering — never a count.

`lucid person <name>` shows the same facts for one person:

```text
Check-ins: every 4 weeks — last caught up 2026-10-08
```

(or `Check-ins: paused (every 4 weeks) — …`, `Check-ins: retired — …`, or
`Last caught up 2026-10-08 (no check-in cadence)` for someone with contacts and
no cadence). A person with no contact events looks exactly as before.

## Birthdays

Anyone with a date of birth (`lucid person set <name> --dob YYYY-MM-DD`) shows in
a **Birthdays this week** block when their birthday falls this Monday–Sunday —
whether or not they have a cadence, and even if they are snoozed or paused. A
Feb 29 birthday is marked on Feb 28 in other years, and a week that spans New
Year works as you would expect. Birthdays show in the due view and the Monday
post, not in the Sunday wrap, and they have no separate switch: they ride the
Monday post's.

## The weekly cycle

1. **Monday** — once you turn it on (below), Lucid posts the week's list to your
   own channel: "Due this week", "Still open from before", and any birthdays.
2. **The week** — the whole week is yours to find a moment. When you are in
   touch, `lucid contact <name>` checks them off.
3. **Sunday** — `lucid contact due --wrap` shows who is still open, in the same
   two lists. If you use a chat harness, its Sunday pass can read
   `lucid contact due --wrap --json` and fold it into a message it already sends;
   Lucid itself adds no Sunday post.
4. **Next Monday** — anyone not reached rolls into "Still open from before" on
   their own. There is nothing to reset and nothing to catch up on.

The wrap is computed fresh each time — there is no stored Monday snapshot — and
leaves out "Coming up next week" and birthdays:

```text
Still open this week — no pressure; anyone here rolls into Monday's list.

Due this week
- Sam Rivera (every 4 weeks), last caught up Oct 8

Still open from before
- Alex Kim (every 2 weeks), not logged yet
```

When both lists are empty it reads `Nobody's still open this week.`

## Turning the Monday post on

The Monday post ships **off**. Add a `contact_weekly` block to `lucid.json`:

```json
"contact_weekly": {
  "enabled": true,
  "weekday": 1,
  "time": "09:30"
}
```

| Key | Type | Meaning |
|-----|------|---------|
| `enabled` | bool | Gates the post. Default `false`. |
| `weekday` | int | The cron weekday of the fire mark (`0`=Sunday … `1`=Monday … `6`=Saturday). Default `1`. |
| `time` | string | The `HH:MM` local fire mark. Default `"09:00"`. |

An absent block, or an absent key, reads the defaults. When enabled, a `time`
that is not `HH:MM` or a `weekday` outside `0`–`6` is refused at load, naming the
key. Pick a time that does not land on top of another scheduled message.

The post runs inside `lucid scheduler run`, beside the Engine and the other
nodes; restart the scheduler after changing the block. It goes only to your user
channel (`LUCID_USER_CHANNEL_ID`) — the block holds no channel id and no token.
It looks like this:

```text
Here's your week for staying in touch — no rush, the whole week is yours.

**Due this week**
- Sam Rivera (every 4 weeks), last caught up Oct 8

**Still open from before**
- Alex Kim (every 2 weeks), not logged yet

🎂 Birthdays this week: Jordan Lee (Wed Nov 4)
```

- **Empty sections are left out.** A week with nothing due and no birthdays
  posts nothing at all; the scheduler records a quiet `nothing-due` skip.
- **A long list is capped** at 1900 characters on a line boundary, ending
  `…plus more in lucid contact due.`
- **A host asleep at the mark** gets one late attempt on wake. Past 48 hours the
  post is skipped and you get a short alert instead of a list days late.
- **No double posts.** Each delivery writes a per-week receipt; a retry that
  finds the message already in the channel skips.
- **Its job store** is a small, disposable database separate from the Ledger
  (default under your user config directory; override with
  `LUCID_CONTACT_WEEKLY_DB`). `lucid scheduler status` lists it.

To see what Monday would post without waiting, run `lucid contact due` — the post
is built from the same two lists.

## Merges, aliases and off-limits people

- **Names resolve like every person verb**
  ([Resolving a subject](commands.md#resolving-a-subject)): an exact
  `person_key` wins, then a display name or alias. A name that matches more than
  one person is refused with the candidate keys, so you can retry with the key.
- **Merges carry history.** After `lucid person merge`, cadences, contacts and
  snoozes recorded under either record belong to the merged person. Merging
  duplicates before setting cadences keeps each list clean from the start.
- **Off-limits people** (`lucid person off-limits`) can still be logged — it is
  your own record — but they never appear in the due view, the birthdays block,
  the Monday post or the wrap.
- **The witness report never reads contact data.** Who you keep in touch with is
  never part of the friend-facing report.

## A subcommand shadows a literal name

`every`, `pause`, `resume`, `retire`, `snooze`, `void` and `due` are
subcommands, so `lucid contact due` always runs the due view. The subcommand
names shadow a person with the same literal name — reach such a person by their
`person_key`, the same rule `lucid person` follows.

## See also

- [`commands.md`](commands.md) — the full command reference, including
  `contact`, `person` and `scheduler`.
- [`../adr/0014-contact-check-ins.md`](../adr/0014-contact-check-ins.md) — the
  design decisions and their reasons.
- [`../mvp/error-states.md`](../mvp/error-states.md) — every refusal (the `Ct-`
  rows) and its exact copy.
- [`witness-report.md`](witness-report.md) — the weekly node this one is shaped
  like.
