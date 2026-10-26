# The weekly witness report

The **witness report** is Lucid's polished, friend-facing weekly accountability
post. Once a week it composes one report from the chain's honest live numbers and
your own opaque prompt files, renders it as a Discord **rich embed**, and delivers
it to the witness channel — the streaks, the faults, the watch-outs, and one to
three concrete asks a friend can act on. It replaces the sparse monthly all-clear
line the witness channel used to carry (see
[What it retires](#what-it-retires)).

It is the Mirror-side, model-allowed sibling of the Engine's accountability.
Like the [companion](companion.md), the numbers stay modeless and
deterministic — a model can never soften "you missed" or invent a streak — while
the model wraps that same honest skeleton in warm, in-voice prose. See
[`../architecture.md`](../architecture.md) for the Mirror/Engine split and
[`../adr/0006-model-access.md`](../adr/0006-model-access.md) for the provider seam
it composes through.

It ships **off**. A fresh Ledger runs the pure Engine loop and any companion
exactly as before until you opt in; nothing below happens until you set
`witness_report.enabled: true`.

- **What it is** — a Mirror-side weekly job that reaches the model through the
  provider seam, never the Engine. Every streak, adherence, and miss figure is
  copied from the same projection `lucid metrics --json` exposes.
- **Where it runs** — inside `lucid scheduler run`, in the same supervised process
  as the Engine and the companion (config-gated), so it is up whenever the
  scheduler is. It is not an external cron.

## The weekly window

The report fires **once a week, Monday morning** by default (weekday `1`,
`09:00`, your host's local time). The Monday mark is deliberate: it lands *after*
Sunday's [weekly reflection](weekly-reflection.md) window, so the week is fully
closed before the witness sees it, and it front-loads the friend-facing asks for
the week ahead rather than reading as a Sunday-night retrospective.

Both the day and the time are config keys (`weekday`, `time`) — change them and
the periodic reconciles on the next daemon start. A host asleep across the Monday
mark wakes to one bounded late attempt; a report still un-fired two days past its
mark is treated as stale and skipped-with-an-alert rather than posted days late
(see [When things go wrong](#when-things-go-wrong)).

## What it retires

The witness channel used to carry a **monthly heartbeat** — a fixed
`monthly status: all clear — N-day streak, no open escalation` line whose only job
was to prove the tooling was still alive. The weekly report **absorbs that
all-clear signal**: a report that lands every Monday with the honest live numbers
is a far stronger dead-man-of-the-dead-man than a monthly one-liner, so the
monthly heartbeat is retired outright.

**The L2 dead-man escalation is unchanged.** Retiring the heartbeat touches only
the monthly all-clear; the two-consecutive-misses escalation to the witness fires
exactly as before, byte-for-byte. See [`../engine.md`](../engine.md).

## Turning it on

Add a `witness_report` block to `lucid.json` and point the two required path keys
at your own prompt files:

```json
"witness_report": {
  "enabled": true,
  "mode": "preview",
  "time": "09:00",
  "weekday": 1,
  "system_prompt": "/home/you/lucid-prompts/witness-system.md",
  "template": "/home/you/lucid-prompts/witness-template.md",
  "asks_file": "/home/you/lucid-prompts/witness-asks.md",
  "model": "",
  "anchor_detail": "summary"
}
```

| Key | Type | Meaning |
|-----|------|---------|
| `enabled` | bool | Gates the whole feature. Default `false` → the scheduler runs only the pure Engine (and any companion). |
| `mode` | string | Delivery target: `preview` posts to your **own** user channel (the safe default during the trust-building period), `auto` posts to the friend-facing **witness** channel. Flipping `preview` → `auto` is a one-key change, never code. |
| `time` | string | The `HH:MM` local fire mark. Default `"09:00"`. |
| `weekday` | int | The cron weekday of the fire mark (`0`=Sunday … `1`=Monday … `6`=Saturday). Default `1` (Monday). |
| `system_prompt` | path | **Required when enabled.** The system prompt — an opaque file passed to the model as the `System` role on every compose. |
| `template` | path | **Required when enabled.** The report template — an opaque file passed to the model as the user message. Your witness-report voice lives here. |
| `asks_file` | path | **Optional.** A curated friend-asks file. When set and non-empty its asks **override** the auto-drafted ones; unset/empty leaves the auto-drafted asks in place. Never gates the feature. |
| `model` | string | Optional. Overrides `provider.model` for the compose call; empty inherits the provider default. |
| `anchor_detail` | string | `summary` (the default) or `full`. Summary shows anchors only as watch-out lines, with no note and no date; full adds a verbatim **Anchor detail** section — the week's anchor events, notes included — that is never sent to the model. See [Anchors in the report](#anchors-in-the-report). Any other value is a config error naming the two. |

When `enabled` is `true`, `system_prompt` and `template` must be non-empty or the
config is rejected at load, and `mode` must be `preview` or `auto`. `asks_file` is
optional and never gates the feature. `anchor_detail` is optional; an older
`lucid` ignores it and behaves as `summary`.

**The configured paths are the whole personal seam.** Lucid opens exactly the
files you name — the system prompt, the template, and, when set, the curated-asks
file — and never walks the directory holding them (no dir-walk, no filename
convention), so you can keep them anywhere, inside or outside the repo. Each file
is opaque, self-contained text: Lucid does not parse it, follow links in it, or
require any structure.

**Credential-dumb, like the rest of `lucid.json`.** No channel id and no token
live in the config — the witness channel, your user channel, and the harness token
stay env-only (`LUCID_WITNESS_CHANNEL_ID` / `LUCID_USER_CHANNEL_ID` /
`LUCID_HARNESS_TOKEN`), and no model API key lives here or anywhere in `lucid.json`
(auth is the vendor CLI's or the local daemon's).

## The privacy boundary

The witness report is friend-facing, so its firewall is stricter than the
companion's — and it is **structural first, not a filter bolted on afterward.**

1. **Structural input restriction (the primary guarantee).** The report builder
   reads exactly two data sources: the metrics **projection**
   (streak / adherence / this-week misses / error budget / each anchor's kind
   numbers — counts only, never a note or an event date) and the engine **day
   records** for the 7-day window. It has **no reader for observations, journal
   entries, or raw log content at all** — that seam does not exist in the package.
   Private detail is not filtered out; it is structurally unreachable. The model,
   in turn, is handed **only** the witness-safe report data model — the honest
   numbers, the derived watch-outs, and the drafted asks. It never sees a journal
   line, a mood log, or a raw entry, because nothing in the compose path can load
   one.

   **The one deliberate, opt-in exception** is full anchor detail
   (`anchor_detail: full`, [below](#anchors-in-the-report)). Only then is a third,
   narrow reader wired — the week's anchor events, notes included — and the
   section it feeds is rendered by code, never handed to the model, and never
   scanned. In the default summary mode that reader does not exist, so the
   guarantee above holds exactly as written.

2. **Fail-closed output scan (defense in depth).** After the model composes its
   prose, a witness-specific scan checks it for private-detail markers. If it
   trips, the prose is **discarded** and the report degrades to the deterministic,
   metrics-only card — the numbers, the asks, and the watch-outs still land, the
   flagged prose never does — and you are alerted to review why.

3. **The preview gate (human review during the trust period).** In `preview` mode
   every report posts to your own channel first, so you read each one before any
   friend does. You graduate to `auto` only once the boundary has proven itself —
   and that graduation is a config change, not a rebuild.

The result: a witness-facing number can never be fabricated, and a raw journal
line can never leak — the first by construction (the numbers are copied, never
recomputed by a model), the second by construction *and* by two more layers behind
it. An anchor note reaches a witness only if you opted in to full detail, and then
verbatim, in your own words, never through the model.

## How a report is composed

Lucid owns the report **layout**; the model fills only the prose. That split is
what keeps the friend-facing surface honest and scannable: the sectioned embed —
the title, the colored risk sidebar, every number, the watch-outs, the asks, and
the footer — is rendered by deterministic Lucid code, and the model writes only a
few short slots dropped into fixed fields. A model can never produce a wall of
numbers or restate a metric, because it never owns the structure.

For each weekly fire the compose worker:

1. Reads the chain's **honest live numbers** in-process, from the exact projection
   `lucid metrics --json` exposes — streak, longest streak, 30-day adherence,
   error budget, and each anchor's kind numbers — and computes a 7-day window for
   "this week" (completed / decided / logged days, ending at the current logical
   day). The counts are copied straight from
   the chain, never invented. The 30-day adherence is shown as a whole percent, its
   0–1 ratio rounded to the **nearest** (half-up at the .5 midpoint) — the identical
   rounding `lucid status`, `lucid metrics`, and the companion apply, so a witness
   report and the daily surfaces never disagree by a point.
2. Derives the **watch-outs** deterministically from those real signals:
   this-week misses at or above the threshold, a 30-day adherence dip, a spent
   error budget, the **kind-aware anchor signals** (a limit over its allowance, a
   habit miss run, a slip this week — see
   [Anchors in the report](#anchors-in-the-report)), and — first of all — **thin
   logging** (a quiet week is its own accountability risk). A strong week surfaces
   no watch-outs and the section is omitted entirely.
3. Auto-drafts one to three **friend-asks** from those same signals (consecutive
   misses → "check in on me midweek"; over-budget → "ask me how the chain held";
   thin logging → "nudge me to log daily"). When a curated `asks_file` is set, its
   asks override the auto-drafted ones. With genuinely no signal and no curated
   file, it yields exactly one honest, generic ask — never a fabricated specific.
4. Reads the two opaque prompt files (`system_prompt`, `template`) on their
   explicit paths and sends them, plus the **witness-safe report data model only**,
   to the model, asking it to fill labeled prose slots — a warm read, the faults
   framing, an optional progress note, and refined asks. The numbers never enter a
   model slot.
5. Runs the composed prose through the [witness-safe scan](#the-privacy-boundary)
   and renders the final rich embed from the deterministic scaffold. On any
   failure the deterministic metrics-only report still renders — only the warmth is
   lost, never the report. With full anchor detail on, code renders that section
   last, outside the model and the scan, so it rides along unchanged either way.

## The report layout

Every report is one deterministic embed, rendered in a fixed order so status,
faults, asks, and risk are always in distinct, scannable fields:

- **Title** — `Weekly witness report · Week <ISO>` (e.g. `Week 2026-W05`).
- **Colored risk sidebar** — green when the week surfaced no watch-out, amber for
  a soft signal (a quiet week, or an anchor signal: a limit over its allowance, a
  habit miss run, a slip this week), red for a hard one (this-week misses, a spent
  error budget, or a 30-day adherence dip). The color always agrees with the
  rendered watch-outs.
- **Description** — the model's warm read (the one free-prose paragraph). Omitted
  cleanly on a deterministic fallback.
- **⛓️ Streak & adherence** — the streak, 30-day adherence, and longest streak.
- **📅 This week** — the 7-day window: completed / decided and logged days.
- **🪞 Faults** — the model's constructive framing of the week's misses; on a
  fallback it degrades to an honest line straight from the numbers.
- **📈 Progress** — an optional model note; omitted when empty.
- **🤝 How friends can help** — the one to three concrete asks.
- **⚠️ Watch-outs** — the deterministic risk signals; the whole field is omitted
  when the week supports none.
- **⚓ Anchor detail** — **full mode only**: the week's anchor events, verbatim,
  rendered by code. When it outgrows one embed it continues in numbered
  follow-up messages (see [Anchors in the report](#anchors-in-the-report)).
  Omitted in summary mode and on a week with no anchor events.
- **Footer** — the fixed cadence + honesty line:
  `Weekly · Monday after Sunday's reflection · numbers are exact, never softened`.

There are **no markdown tables** — a chat surface renders them as raw text — so
the layout uses fields and `•` bullets only. A structured-markdown render is
retained as a fallback for any surface where the rich embed cannot be used; it
mirrors the embed's sections one-for-one so the two never drift.

### A synthetic example

The numbers below are **synthetic**, for shape only. A normal week with one miss,
rendered as the markdown fallback:

```text
**Weekly witness report · Week 2026-W05**

A steady week. The chain held six of seven, and the one gap was a travel
Wednesday rather than a slide — worth a nudge, not an alarm.

**⛓️ Streak & adherence**
23-day streak · 92% adherence (33/36 decided) · longest 41

**📅 This week**
6/7 completed · 7 logged of 7

**🪞 Faults**
Missed one Wednesday — travel threw the evening off; the chain held either side.

**🤝 How friends can help**
• Ask me how the chain held this week

_Weekly · Monday after Sunday's reflection · numbers are exact, never softened_
```

A **quiet / low-signal week** is never suppressed and never padded — the thin
logging is surfaced as its own watch-out:

```text
**Weekly witness report · Week 2026-W06**

A quiet one — barely logged this week. The streak is intact, but a week this thin
is itself the thing to watch.

**⛓️ Streak & adherence**
24-day streak · 90% adherence (36/40 decided) · longest 41

**📅 This week**
2/2 completed · 2 logged of 7

**🤝 How friends can help**
• Nudge me to log daily

**⚠️ Watch-outs**
• Logged only 2 of 7 days this week — accountability risk.

_Weekly · Monday after Sunday's reflection · numbers are exact, never softened_
```

## Anchors in the report

Anchors reach a witness by their **kind** ([`commands.md`](commands.md#anchor)),
so a watch-out means something: a long quit is a win, not a worry, and a relapse
is never hidden.

**Kind-aware watch-outs** — the same in both detail modes, read only from the
note-free numbers in the metrics projection:

| Kind | Watch-out | When |
|------|-----------|------|
| `limit` | `<label>: over allowance — <used> of <allowance> in the last <window> days.` | More use-days in the rolling window than the allowance. Spending exactly the allowance is fine. |
| `habit` | `<label>: missed <n> days in a row.` | A run of consecutive misses reaching `anchors.miss_alarm` (default 2) that overlaps the report week (the habit's `miss_run_7d`) — so a mid-week slide is not hidden by a recovery before Sunday. One isolated miss never raises a line. |
| `quit` and `limit` | `1 slip on <label> this week.` / `<n> slips on <label> this week.` | Any slip in the report week — a quit `slip` or a limit `use --slip`, voided ones excluded (the anchor's `slips_7d`). **Count only**: no date, no note. A planned use never produces a line. |
| `milestone`, and a long `quit` run | — | Nothing. A long run is a win, not a worry. |

An anchor signal colors the sidebar amber. A retired or graduated anchor raises
nothing.

**Summary (the default).** With `anchor_detail: summary` — or no key at all —
anchors appear **only** as the watch-out lines above: no note and no event date,
ever. No reader that can reach a note is even wired in this mode, which makes it
the safe default when your witnesses could be anyone.

**Full (an explicit opt-in).** `anchor_detail: full` is for someone whose
witnesses are close enough that sharing the raw record is the point. It adds an
**⚓ Anchor detail** section holding every anchor event dated in the report week
— the same seven days as "this week", ending at the current logical day: each
anchor's slips, uses with their intent (planned or slip), misses, confirmed
spans, kind changes and graduations, with dates and your notes verbatim. A voided
event, and its void, are left out. Lines are ordered by label, then day (a
confirm's through-day), then receipt. The opt-in is global: every anchor with an
event that week appears, including one retired during it.

What full detail promises:

- **It is never sent to the model.** The section is not part of what the model
  is asked to write from, so the model's input is byte-for-byte the same with the
  detail on and off.
- **It is never scanned.** These are your own words and you opted in, so a note
  is never withheld or reworded — and a note that *would* trip the witness-safe
  scan can never cost the week's narrative, because it never reaches the scan.
- **It survives the fallback.** A provider outage, an unusable reply, or a
  tripped scan drops only the prose; the metrics-only report keeps the section.
- **Nothing is dropped for length.** The section is chunked at event boundaries
  where it can be, and a long note is split across numbered continuation fields
  without changing a character. Every field stays within 1024 characters and every
  embed within 6000 characters and 25 fields; whatever does not fit continues in
  additional numbered messages (`⚓ Anchor detail · part 2 of 3`), one embed per
  message, through the same delivery path to the same channel — no new channel,
  credential, transport or schedule. There is never an "and N more": reading the
  parts in order reproduces every event and note.
- **Every part is read back.** A delivery counts only when every part is
  confirmed present in the channel. The scheduled run freezes the whole batch
  before its first send and records each part's message and read-back in the
  week's delivery receipt, so a retry resumes exactly where it stopped without
  re-sending a verified part. A partial delivery is loud and stays incomplete;
  the week is marked delivered only once every part has verified.

Full detail is the witness report's **one deliberate exception** to its
by-construction privacy ([ADR-0013](../adr/0013-anchor-kinds.md)). Preview it
before a friend sees it: turn it on while `mode` is still `preview`, so reports
land in your own channel first, or render a dry run (`lucid witness report`) with
`full` set — it prints the section exactly as a witness would read it and sends
nothing.

A synthetic week in full mode, as the markdown fallback renders it (summary mode
prints the same report without the last section):

```text
**⚠️ Watch-outs**
• morning-walk: missed 2 days in a row.
• 1 slip on sobriety this week.

**⚓ Anchor detail**
• late-night-snacks (limit) — 2026-02-06 · use, planned · a slice of cake at a friend's birthday
• morning-walk (habit) — 2026-02-04 · miss · flight day
• morning-walk (habit) — 2026-02-05 · miss · still traveling
• morning-walk (habit) — 2026-02-01–2026-02-07 · confirmed
• sobriety (quit) — 2026-02-03 · slip · after a long shift
```

## Friend-facing expectations

The witness channel is an **accountability surface, not a journal mirror**. A
friend reading the report sees the honest shape of the week — the streak, the
adherence, this week's misses, one to three concrete asks, and any risk signals —
and nothing else. They do **not** see raw journal entries, mood or pain logs,
medical or relationship detail, or observation content; none of it is reachable by
the composer (see [The privacy boundary](#the-privacy-boundary)). Anchors reach
them as watch-out lines — a count, never a note or a date — unless you opted in
to full anchor detail, in which case they also read the week's anchor events in
your own words. What the report asks of a friend is small and specific: a midweek check-in, a single question, a
nudge — the kind of thing a real accountability partner can actually do.

## Testing a fire on demand

```sh
# Compose and print this week's report — a dry run, zero side effect.
lucid witness report

# Print the deterministic report data model instead of the rendered markdown.
lucid witness report --dry-run --json

# Actually deliver one report now to the channel witness_report.mode selects.
lucid witness report --deliver
```

`report` defaults to a **dry run**: it composes and prints the report with no
delivery — use it to preview the voice or prove the pipeline end to end. `--json`
prints the report data model (the deterministic scaffold plus any composed prose)
so you can inspect the honest numbers directly (with full anchor detail on, it
carries an `anchor_detail` list as well; in summary mode the key is absent).
`--deliver` posts one read-back-verified rich embed — plus its numbered
continuation messages, each read back too, when full anchor detail outgrows it —
to the channel `witness_report.mode` selects: preview to your own user channel,
auto to the friend-facing witness channel. A
dry run labels the deterministic-fallback and witness-safe-trip paths when they
fire, so a preview is never mistaken for the model's warm output when it was not.

A `--deliver` on a disabled feature is a clean no-op — the scheduler would not post
it and a manual fire should not either.

## A quiet / low-signal week

A quiet week is the single most useful thing for a witness to see, so the report
**always posts** and treats the low signal itself as a watch-out
(`Logged only N of 7 days this week — accountability risk.`). It never suppresses
the report to avoid an empty-feeling week, and it never fabricates activity to fill
one. This is deliberately different from the weekly reflection, which
short-circuits an empty week privately: the witness cadence is "at least weekly,"
and a thin week is exactly when a friend most needs the signal.

## When things go wrong

The report degrades in layers and is designed to **never fall silent** — a dead
Monday is visible, not a quiet gap.

- **Provider unreachable** (timeout / unavailable) or a **malformed model reply**:
  the worker renders the deterministic metrics-only report — the honest numbers,
  the watch-outs, and the auto-drafted asks — marked as a fallback. Only the
  narrative warmth is lost; the report still lands. The fallback is never quiet:
  the scheduler logs a `compose fell back` warning (reason, provider error),
  marks the run's job record `compose_fallback` (reason `provider_outage` or
  `unusable_reply`) while still counting it delivered, and sends the same
  once-a-day note to your user channel the companion uses (see
  [`companion.md`](companion.md#when-things-go-wrong)).
- **Witness-safe scan tripped**: the model's prose is discarded, the
  metrics-only report is delivered instead, and you are alerted to review why the
  scan fired. The flagged prose never reaches the channel. The run is also logged
  and marked `compose_fallback` with reason `safety_tripped`; the review alert
  stays the only alert — there is no second ping. Fallback pings, like every
  alert on this page, go to your **user** channel, never the witness channel.
- **Host asleep at the Monday mark** (bounded catch-up): on wake the missed week
  fires one late attempt. Past the missed-fire cut-off (the scheduled mark plus 48
  hours — a report still un-fired by Wednesday morning), the send is **skipped and
  you are alerted** instead of posting a card describing a week already days gone.
- **Double-fire on a retry** (idempotency): each delivery writes a per-ISO-week
  receipt through the binary. If a retry finds a receipt whose message still reads
  back in the channel, it skips rather than double-posts. A multi-part report
  (full anchor detail) records each part in that receipt: a retry re-sends only
  the parts not yet verified, from the batch frozen before the first send.
- **Read-back verify**: a delivery is not considered done until the message id
  returned by the send is confirmed present in the channel — a real message that
  actually reappears, not a fire-and-forget POST. For a multi-part report, every
  part; a delivery that stops partway is loud and stays incomplete.
- **Total miss** (loud alert): a compose failure, a delivery failure, a failed
  read-back, or a past-cut-off skip fires a best-effort alert to your user channel.
  If even that channel is unreachable, the job returns a loud error that fails the
  scheduled job and lands in the supervised daemon log. Silence is the one outcome
  the report never produces.

## How it coexists with the Engine

The report is a third node beside the Engine and the companion, and it changes
nothing about the accountability decision. The Engine still runs its **full
modeless decision** every day — it evaluates the tripwire, persists escalation
state, and fires the L2 witness escalation, all deterministic and untouched. The
witness report only adds the weekly accountability post; it never gates, softens,
or delays an escalation. Disable it (`witness_report.enabled: false`) and restart
`lucid scheduler run`, and the Engine and companion run exactly as before.

## Operational notes

- The report runs its own small, disposable job database, separate from the
  Engine's and the companion's and from the `~/.lucid` Ledger. It defaults to a path
  under your user config directory; set `LUCID_WITNESS_REPORT_DB` to override it.
  It holds only scheduling machinery and the per-week delivery receipt — no Ledger
  truth — so it is safe to delete; it is rebuilt on the next run.
- The delivery receipts that guard idempotency are written **only through the
  binary**. Never hand-edit them.

## See also

- [`companion.md`](companion.md) — the daily companion, the near-identical
  Mirror-side precedent this report is built from.
- [`commands.md`](commands.md) — the full `lucid` command reference, including
  `witness report`, `scheduler run`, and `scheduler status`.
- [`weekly-reflection.md`](weekly-reflection.md) — the Sunday weekly deep-dive the
  Monday report lands after.
- [`../engine.md`](../engine.md) — the accountability node (bell, tripwire,
  escalation) and the retired monthly heartbeat the weekly report absorbs.
- [`../architecture.md`](../architecture.md) — the Mirror/Engine split and the
  purity boundary the report sits beside.
- [`../adr/0006-model-access.md`](../adr/0006-model-access.md) — the provider seam
  the report composes through.
