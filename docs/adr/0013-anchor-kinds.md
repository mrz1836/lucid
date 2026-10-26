# ADR-0013 — Anchor kinds: milestone, quit, limit, habit — each kind its own honest number, kinds and events in a separate append-only store

**Status:** Proposed.

## Context

An anchor has exactly one shape today
([`../mvp/engine-module.md`](../mvp/engine-module.md) §"`anchors.json`"): a
labeled civil date that a running "days since" counts from. The store is
append-only and `AnchorVersion = 3`. A correction (a mistyped date) and a reset
(the count restarts) are the same operation: a new dated append, latest record
per identity wins. That shape is right for a milestone (a start, a gate, a
countdown) and for a cessation. It is wrong for most of what people actually
want to make stick, and the record shows it three ways:

1. **A moderation goal has no honest form.** "Rarely, and on purpose" — free on
   nearly every day, with a deliberate exception at a genuinely special moment —
   is not "never again". With one shape there are only two moves after a planned
   exception, and both break the promise that Lucid's numbers are honest:
   * reset the count to 0, which punishes an intended, sanctioned choice and
     invites the all-or-nothing "what-the-hell" effect, where one lapse becomes
     permission for many; or
   * leave it alone, which makes the record say something untrue.
2. **A daily habit is counted like a quit-streak.** "Days since morning walk:
   112" cannot tell a day that was kept from a day that was not. It only says
   when the habit started. Nothing ever resets it, so it only ever goes up, and
   a number that cannot go down is not a measurement.
3. **The witness watch-out is noise.** `internal/witnessreport` flags any anchor
   whose days-since count passes a fixed mark (45) as "worth a check-in". For a
   cessation a long run is a *win*, not a worry. For a habit the count means
   nothing at all. Much of the weekly report's "N days since" reads as a stale
   dump for exactly this reason.

**The fatigue constraint.** Tracking every small daily thing wears people down,
and the overhead itself becomes the reason a practice is dropped. The design has
to make a normal day cost **nothing** and an exception cost **one sentence**,
while keeping the record honest.

**The downgrade hazard.** `storage.AppendAnchors` decodes `anchors.json` into
`engine.AnchorLog` with a plain `json.Unmarshal`, appends, and re-marshals the
whole file. So on an older binary's next `anchor add|rename|sunset`, every
record field it does not know, and any sibling array it does not know, is
silently discarded. The same latent hazard already exists for the v2 `state`
and v3 `adopts` fields; the `AnchorVersion` comment documents only the read
side. Anything that must survive a downgrade cannot live inside `anchors.json`
as a field or array an older binary does not know.

**The witness firewall.** The fail-closed witness-safe scan guards only model
prose. A trip throws away the week's narrative and falls back to the
metrics-only report. Today the model sees only the witness-safe report data,
and anchors reach witnesses only as watch-out lines.

Binding constraints shape every choice below:

* **P3 — sanctuary.** `internal/storage` is the only package that touches
  `~/.lucid/`.
* **P9 — the runtime never depends on AI.** The Engine stays pure,
  deterministic and model-free. Every number here is a function of the records.
* **Observations are inventory, never obligation**
  ([`../observations.md`](../observations.md) §0). An allowance or a habit is a
  target, so it belongs to the Engine, never to observations.
* **Append-only, a receipt on every write** (P8). No verb edits or removes a
  stored record.
* **No new send paths.** Anything that reaches a witness goes through the
  existing delivery path.

All examples here are synthetic.

## Decision

Give every anchor a **kind**: a plain, everyday noun that answers "what is
this?". Each kind gets its own honest number. The kind, its parameters, and
every slip, use, miss, confirm, void and graduation live in a **new append-only
anchor event store** beside `anchors.json`. `anchors.json` keeps its v3 record
shape byte-for-byte and `AnchorVersion` stays 3.

### 1. Four kinds, each with its own honest number

| Kind | The plain question | The honest number | What gets logged |
|------|--------------------|-------------------|------------------|
| `milestone` | "Is it a date that matters?" | Days since (or toward) the date | Nothing — there is nothing to slip |
| `quit` | "Is it something I stopped?" | Days since the last slip, the longest clean run, the slip count | A `slip` when it happens |
| `limit` | "Is it something I do rarely, and on purpose?" | Use-days spent of an allowance of N per rolling W days, planned vs slip | Each `use`, with its intent |
| `habit` | "Is it something I do often?" | Kept days out of *accounted* days in a rolling window; never miss twice | Only a `miss`; a weekly `confirm` |

* **`milestone`** is today's behavior under an honest name, and it is the
  default: an anchor with no `kind` event is a milestone. Its math is exactly
  today's days-since math, and nothing about it changes.
* **`quit`** is something stopped. A `slip` records a relapse *separately* from
  a date correction, so a typo fix and a relapse are no longer the same append.
  The record keeps the longest clean run and the slip count. With no slips, a
  quit's day count is identical to a milestone's: the split is naming plus
  `slip`, not new day-count math.
* **`limit`** is "rarely, and on purpose": an **allowance** of N use-days per
  rolling W-day window (`--allowance 4 --window 90` reads as "4 days in 90",
  so `limit` cannot be misread as a quantity cap). Each use is logged with a
  **required intent**, `planned` or `slip`; the binary never guesses it. Using
  the allowance is not failure. This is the Engine's own error-budget idea
  ([`../calibration.md`](../calibration.md): "isolated misses are spend, not
  failure") applied to a goal: a planned use is spend. **A planned use never
  resets or zeroes anything.** Limits count use-days, not units: the goal is
  frequency and intent, not measuring quantity.
* **`habit`** is "often, without daily logging". **Only misses are logged.**
  Honesty comes from a periodic **confirmation** ("confirmed through
  `<date>`"), asked once a week. Confirmed days with no logged miss count as
  kept; **unconfirmed days count as unaccounted, never as kept**, which mirrors
  the Engine's decided/accounted honesty. The signal is **never miss twice**:
  one isolated miss (traveling, staying at a friend's place) means nothing, and
  only a run of consecutive misses raises an alarm. A habit that has held long
  enough is surfaced as **eligible to graduate** (§8): retiring it as
  *graduated* says "this became who I am", which is different from an abandoned
  sunset.

### 2. Storage: a separate append-only event store; `anchors.json` stays v3

**The file.** The store is `engine/anchor_events.json`, owned by
`internal/storage` (P3), with a versioned JSON envelope:

```json
{
  "version": 1,
  "events": [
    {"id": "anchorev_2026_03_02_a", "anchor_id": "anchor_2026_01_05_a",
     "type": "kind", "day": "2026-03-02",
     "recorded_at": "2026-03-02T21:10:00-05:00",
     "kind": "limit", "allowance": 4, "window_days": 90},
    {"id": "anchorev_2026_03_14_a", "anchor_id": "anchor_2026_01_05_a",
     "type": "use", "day": "2026-03-14",
     "recorded_at": "2026-03-15T00:40:00-04:00",
     "intent": "planned", "note": "a slice of cake at a birthday"}
  ]
}
```

* **Version.** `const AnchorEventsVersion = 1`, a constant of its own, separate
  from `AnchorVersion`. It is stamped on every write and is **a signal, not a
  gate**: reads never check it, on the same terms as `anchors.json`.
* **Writes.** Each write reads the whole file, appends, and writes once through
  the same atomic temp-file-and-rename discipline `anchors.json` uses, with
  owner-only permissions. No record is ever rewritten or removed.
* **A missing file is the empty log**, the same tolerance the self store has.
  The file is **never scaffolded**: `init` does not create it, and the first
  event write does. A Ledger with no kinds is therefore untouched, and reads
  exactly as it does today.
* **Bad data never panics.** A record that fails validation is skipped by the
  fold and reported by `lucid validate` (§10). An unparseable file is a loud
  read error that names the file.
* **Why a JSON envelope and not JSONL.** Every write already needs the whole
  store (to mint a slot and to check the anchor's current kind), so appending a
  single line saves nothing, and the atomic rename rules out a torn record.

**The event record.** Every event carries:

| Field | Meaning |
|-------|---------|
| `id` | `anchorev_YYYY_MM_DD_<slot>` — the receipt |
| `anchor_id` | The target anchor's minted id |
| `type` | `kind`, `slip`, `use`, `miss`, `confirm`, `void` or `graduate` |
| `day` | The civil logical day the event is about (for `confirm`, the through-day) |
| `recorded_at` | RFC3339 append time |
| `note` | Optional free text |

Each type adds its own fields: `kind` carries `kind`, `allowance` and
`window_days`; `use` carries `intent` (`planned` or `slip`); `confirm` carries
`from` (the first day of the attested span); `void` carries `target` (the voided
event's id). `slip`, `miss` and `graduate` add nothing. Unknown fields are
tolerated on read, so a later version can add a field without breaking this
one.

**Event ids.** `anchorev_YYYY_MM_DD_<slot>` uses the shared per-day slot grammar
(`SlotLabel`), minted from the write's logical day exactly as `NextAnchorID`
mints anchor ids. The mint scans every event *and* every id reserved by a pair
intent (§3), so an id is never reissued. The `anchorev_` prefix can never
collide with an `anchor_` id or an `anchor:<id>` link subject. Every write
returns its id as the receipt, in the ack and in `--json`.

**The kind lives here, as dated `kind` events.** The latest non-voided `kind`
event wins; since `void` never targets a `kind` event (§5), that is simply the
latest one. With no `kind` event the anchor is a `milestone`. A `limit` kind
event carries `allowance` and `window_days`, both positive integers. A `habit`
kind event may carry `window_days` for its rolling rate (default 30). Changing a
limit's parameters is a new dated `kind` event, so the parameter history is
kept. The v1 math uses the latest parameters across the whole window (§4).

**Events need a durable anchor id.** An anchor written before ids existed folds
under a read-time `legacy:<label>` identity that is never written, which is not
a durable target. An event or reclassify on such an anchor is rejected with the
remedy "run `lucid anchor backfill-ids` first". There is no implicit adoption
write.

**What reaches `anchors.json`, and why the kind does not.** A `kind` field on
the definition records would survive an older binary's *read* and be erased by
its next *write* (the re-marshal above), silently reverting every quit, limit
and habit to a milestone and orphaning its events. So `anchors.json` gains **no
new field**, its record shape is unchanged, and `AnchorVersion` stays 3. The
only records this design appends there are **ordinary v3 records** an older
binary already understands:

* the **compat re-date** — a re-dated definition record appended by a `slip`, a
  `void` of a slip, or a reclassify to `quit` (§3, §4), so an older binary never
  shows a quit run longer than the truth;
* the **definition** an `add --kind` writes, which is the same record a plain
  `add` writes;
* the **plain `sunset`** a graduation writes (§8).

### 3. Two-file verbs: one documented, conservative write order

Five verbs write both files: `anchor add --kind`, `anchor slip`, `anchor void`
of a slip, `anchor kind` to `quit` when a re-date is needed, and `anchor sunset
--graduated`. Each file write is atomic on its own, but two files cannot be
written atomically together. A crash between them must never flatter either
binary's reading of a quit, and the incomplete pair must be detectable.

**Pair intents.** Before either domain half is written, the verb appends an
**`AnchorPairIntent`** to an optional, append-only `pair_intents` array in the
store's envelope (absent when unused). An intent reserves the event id and holds
the **exact planned event**, the **exact planned v3 record**, and the `order`
(`compat_first` or `event_first`). It is transaction metadata, not an applied
fact: the fold never reads its nested event as an event. Intents are never
edited or removed; completion is inferred from the presence of the matching
event and the exact matching record. The ordinary event and its compat record
also share the anchor id and `recorded_at`, but a timestamp alone is never
treated as proof of a pair.

**The write order, per verb:**

| Verb | Order | A crash after the first half leaves… |
|------|-------|----------------------------------------|
| `slip` | Intent → **v3 re-date** → slip event | Both binaries counting from the re-date: honest, never longer. The slip count catches up on retry. |
| `kind` to `quit` (later use-day) | Intent → **v3 re-date** → kind event | The older binary counting from the re-date; the new binary still reads the old kind, with the pending re-date as a floor. |
| `void` of a slip | Intent → **void event** → restoring v3 record | The new binary reading the corrected history; the older binary still counting from the slip — shorter, never longer. |
| `add --kind` | Intent → **kind event** → v3 definition | A dangling kind event the fold ignores; no anchor appears in either binary. |
| `sunset --graduated` | Intent → **graduate event** → plain v3 sunset | An active anchor in both binaries; the new binary recognizes a graduation only once its sunset half exists. |

The re-date a slip writes uses the effective date `max(start, every accepted
use-day including the proposed slip)`, so a backdated slip can never move a
quit's effective date backwards.

**Pending reads are conservative.** An unpaired compat re-date stays an
effective-date **floor** for the new binary: the fold does not ignore it merely
because an intent identifies it as compat. Only a *completed* pair is excluded
from the base-start fold (§4). An event-first slip void may let the new binary
show the corrected, longer run while the older binary stays conservatively
shorter.

**Recovery is explicit, never automatic.** `lucid validate` reports an
intent-only, compat-only or event-only operation as a **half-written pair**,
naming the reserved event id and the missing half. It never repairs anything.
The remedy is to retry the original verb: a matching explicit retry reuses the
intent and its event id, appends only the missing halves in the recorded order,
and returns the original receipt. An older binary may write between the interruption and the retry — a
correction, a rename, a sunset — and the retry never lets the planned halves turn
stale into a flattering reading. It still appends them exactly, because that is
what completes the pair; then, when the anchor's newest record would carry an
older date than its honest effective date, or a label and note an older-binary
rename replaced, it appends **one more plain v3 record** carrying the current
label and note and the later of the two dates. That record never moves a quit
start: it only ever changes the date when an older-binary correction moved the
start past the planned date, and then it carries exactly that start. A retry on
an anchor an older binary has since sunset is rejected (An-6), because a
restoring record would quietly bring it back. An `add --kind` whose reserved
anchor id an older binary has since minted for another anchor can't be
finished: the fold ignores the event half of an unfinished `add --kind` on
whichever anchor holds that id, it blocks no change to that anchor,
`lucid validate` names it, and the label is free to record again. A *different* mutation of that anchor is
rejected while its pair is incomplete, so a provisional floor can never turn
into an ordinary correction by accident.

**The crash proof.** Tests fault-inject before and after every atomic boundary
(the intent, the first half, the second half) of every two-file verb. At each
point both the current fold and an **anchors-only v3 fold** (today's
`LatestAnchors` path, standing in for an older binary) must read a quit run no
longer than the accepted history permits. The cases include completed and
pending operations, backdated and duplicate-day slips, and an older-binary
write landing between an interrupted operation and its recovery.

### 4. The fold and the math

The fold and every number are pure Engine functions of the two files, the
current logical day and two thresholds: no clock, no disk, no model (P9).
Events join `anchors.json` by `anchor_id` against the folded identity; an event
whose anchor is unknown is ignored by the fold and reported by `validate`. Every
day difference uses the UTC-civil discipline `DaysSince` already uses, so DST
transitions never shift a count, and "today" is the current logical day at the
chain's rollover boundary.

**Event days.** An event defaults to the current logical day of the active
profile — the day `metrics` counts against — so a write after midnight and
before the rollover lands on the evening's day. `--day` reads the shared day
grammar; a future day or a partial date is rejected, and so is a day before the
anchor's start (§5).

**`milestone`:** identical to today's days-since math. It reads no events.

**`quit`:**

* **Start.** The date of the latest definition record that *changes the date*
  and is *not* part of a completed compat pair. So an `add` correction still
  moves the start, exactly as it always has, while a slip's own compat re-date
  does not. A record that keeps the date it follows — a rename, a plain
  sunset, an id adoption, whichever binary wrote it — never moves the start:
  after a slip such a record carries the re-dated date forward, and reading it
  as a correction would quietly drop the slip and shorten the longest run. A
  pending conservative re-date still floors the effective date (§3).
* **Use-days.** The distinct days of non-voided `slip` events, plus the distinct
  days of non-voided `use` events of either intent (which can only have been
  logged while the anchor was a `limit`).
* **Current run.** `DaysSince(max(start, latest use-day), today)`.
* **Longest run.** The maximum over the runs between successive use-days,
  counted from the start, the current run included.
* **Slips.** The number of distinct use-days on or after the start — a slip
  logged on the start day itself is still a slip.

A synthetic example: `sobriety` starts 2026-01-01 and slips on 2026-03-01; on
2026-04-10 it reads `Days since sobriety: 40 (longest 59, 1 slip).`

**`limit`:**

* **Window.** The W logical days ending today, inclusive, clipped at the anchor
  date. Quit slips logged before a conversion count as `slip`-intent uses.
* **Used.** The distinct use-days in the window; several uses on one day are one
  use-day. Planned and slip uses both spend allowance.
* **Remaining.** `max(0, allowance − used)`.
* **Planned and slip counts.** Use-days in the window by intent; a day logged
  with both intents counts as a slip day, the honest direction.
* **Days since the last use**, and **`over_allowance`** when used > allowance.
  Using exactly the allowance is fine.
* **`slips_7d`**, a note-free count of slip use-days in the last seven logical
  days — the same window the witness report's "this week" uses — which the
  witness summary line reads (§7).

The current allowance and window apply to the whole window. A synthetic example:
`Late-night snacks: 1 of 4 in the last 90 days (1 planned, 0 slips); 7 days
since last.` The quit object carries the same `slips_7d` count.

**`habit`:**

* **Window.** `window_days` (default 30) ending today.
* **Accounted days.** The union of non-voided confirm spans plus logged miss
  days, inside the window and never before the anchor date.
* **A miss always wins over a confirm** on the same day, because the record
  moves toward honesty. A confirm never erases a miss.
* **Kept days.** Accounted days minus miss days. **An unconfirmed day is never
  kept**, and a habit with no accounted days reads "no accounted days yet",
  never a hollow 100%.
* **Miss runs.** `current_miss_run` is the number of consecutive *calendar*
  miss days ending at the latest accounted day, and `alarm` is true when it
  reaches `miss_alarm`. A single isolated miss never alarms.
* **`last_confirmed`.** The through-day of the latest non-voided confirm.
* **`graduation_eligible`.** True once the accounted days since the last alarm
  run ended — or since the anchor became a habit, if it never alarmed — reach
  `graduation_days`. This reads the anchor's full history, so an old rough
  patch does not disqualify a habit forever.

A synthetic example: `Morning walk: kept 29 of 30 accounted days, 1 miss;
confirmed through 2026-01-24.`

**The `confirm` span.** One confirm attests a span of days per habit:

* **The through-day** defaults to yesterday's logical day, because today is not
  over.
* **The span starts** at the later of the day after the habit's last non-voided
  confirm, and the day of its latest `habit` `kind` event — the day it *became*
  a habit, never the anchor date. So by default nothing before the cutover is
  claimed, and after a kind change away and back the span starts at the later of
  the two.
* **`--from <day>`** widens the span to an earlier stretch the person actually
  vouches for. It is rejected before the anchor date or after the through-day.
* **A skipped week self-heals:** the next confirm covers the gap.
* **An empty span writes nothing** for that habit, and the ack says why:
  "already confirmed through `<date>`", or — for a habit whose through-day falls
  before it became a habit, which has nothing to confirm yet — the day its
  confirmations start. With no active habits the ack is an honest no-op.
* **One event per habit**, and the ack names the exact span confirmed for each
  (`confirmed <from>–<through>`), so whoever asked can say it back.

**Kind conversion.** Events are facts that outlive a kind change. Quit slips
and limit uses are both **use-days**, and a quit slip reads as a `slip`-intent
use under `limit`. Each kind's math reads only the facts it understands:
`milestone` reads none, `quit` and `limit` read use-days, and `habit` reads
misses and confirms. Every other event stays in the store and in the detail view
but drops out of the math, and converting back brings it back in. **No
conversion makes a quit run longer than the truth:** converting to `quit` counts
from the latest use-day when it is later than the folded date, and appends a
compat re-date (§3) so an older binary agrees. A recent planned use can never
hide inside a long quit run.

**Thresholds.** `miss_alarm` (default 2, at least 2) and `graduation_days`
(default 90, at least 1) are defaults in a new `lucid.json` `anchors` block,
passed into the pure Engine as parameters. A `miss_alarm` of 1 is rejected
because it would let a single isolated miss alarm. The graduation default
reflects habit-formation research, which puts automaticity at around two months
on average. There is no per-anchor override in v1. An older binary ignores the
block, because `lucid.json` decoding is non-strict.

### 5. Verbs, capture tiers and corrections

| Verb | Kinds | Writes | Tier |
|------|-------|--------|------|
| `anchor add <label> <date> [note] --kind <kind> [--allowance N --window D]` | any | v3 definition + first `kind` event (§3); without `--kind`, exactly today's behavior and no event | echo-and-confirm |
| `anchor kind <label> <kind> [--allowance N --window D]` | any | one `kind` event; plus a compat re-date only for a reclassify to `quit` past a later use-day | echo-and-confirm |
| `anchor slip <label>` | `quit` | compat re-date + `slip` event | capture |
| `anchor use <label> (--planned \| --slip)` | `limit` | `use` event | capture |
| `anchor miss <label>` | `habit` | `miss` event | capture |
| `anchor confirm [<label>...] [--through] [--from]` | `habit` | one `confirm` event per non-empty span | echo-and-confirm |
| `anchor void <event-id>` | — | `void` event; plus a restoring re-date for a slip | echo-and-confirm |
| `anchor sunset <label> --graduated` | any | `graduate` event + plain v3 sunset | echo-and-confirm |

`anchor kind` reclassifies in place: the id and the date are unchanged, and
changing a limit's parameters is the same verb. Re-dating with `anchor add`
stays the *correction* path for every kind. `rename`, plain `sunset`,
`backfill-ids`, adoption and `anchor:<id>` media links work unchanged for every
kind.

**Capture tier.** `use`, `miss` and `slip` are cheap, append-only, and point
toward honesty: recording a lapse never flatters the record. A harness runs them
immediately and leads with the receipt, as it does with an observation, and
`void` fixes a mishearing. **Echo-and-confirm.** Reclassifying, confirming a
span (which claims days as kept) and graduating change what the numbers mean,
so a harness echoes them back and waits for a yes. `void` sits here too:
voiding a lapse makes the record kinder, so it runs only on an explicit,
echoed correction, never on inference.

**`void` corrects without rewriting.** `anchor void <event-id>` appends a `void`
naming the target; voided events drop out of all math, and the history keeps
both records. Its scope is `slip`, `use`, `miss`, `confirm` and `graduate`.
**A `kind` event is never voidable:** the dated kind events *are* the parameter
history, and voiding one would make "what kind was this on that day" ambiguous.
A wrong kind is corrected by reclassifying. A `void` of an unknown id, an
already voided event, a `void`, or a `kind` event is rejected. Voiding a
`graduate` leaves the plain sunset in place, so the anchor reads as an ordinary
retirement.

**Rejections name their remedy and write nothing:**

* a wrong-kind event — `use` on a habit, `slip` on a milestone ("reclassify it
  as a quit first");
* an event or reclassify on a pre-id anchor ("run `lucid anchor backfill-ids`
  first");
* an event on a sunset anchor;
* `add --kind` on a label an active anchor already holds ("use `anchor kind`");
* `--allowance` or `--window` on a kind that does not take them (both belong to
  `limit`; `--window` alone also to `habit`), or a non-positive value;
* `anchor use` with neither or both of `--planned` and `--slip`;
* a `confirm --from` before the anchor date or after the through-day;
* an event day in the future, or before the anchor's start.

Every verb supports `--json`, the shared `--day` grammar wherever a day applies,
and a shell-safe `--note-file` / `--reason-file` for every free-text field
([ADR-0007](0007-cli-conventions.md)), so an unquoted `&` or `;` in a note can
never cut it short.

**No automatic writes.** The binary surfaces graduation eligibility and
over-allowance; it never graduates, reclassifies or confirms on its own. Those
stay the person's call.

### 6. The metrics surface

`lucid metrics` prose renders one line per anchor in its kind's language
(synthetic examples):

```text
Days since first gate: 40.
Days since sobriety: 102 (longest 140, 1 slip).
Late-night snacks: 1 of 4 in the last 90 days (1 planned, 0 slips); 7 days since last.
Morning walk: kept 29 of 30 accounted days, 1 miss; confirmed through 2026-01-24.
```

`lucid metrics --json` keeps every existing field and its meaning. `date` and
`days_since` stay "the folded definition date, and the days since it" for every
kind; for a quit after a slip that is the re-dated effective date, exactly what
an older binary shows. Each anchor row adds `kind` and one kind object (`quit`,
`limit` or `habit`) holding the numbers in §4. **Both are omitted for a
milestone**, so absence means milestone, the same convention `state` uses.
Graduated anchors appear in a new `anchors_graduated[]`, separate from
`anchors_sunset[]`, which is omitted when empty. Together these keep a Ledger
with no kind events byte-identical to today, `--json` included.

### 7. Witness visibility: kind-aware watch-outs, summary by default, full detail by opt-in

**The blanket aging rule goes.** In its place, kind-aware watch-outs that hold
in both modes:

* **`limit`:** over allowance (`<label>: over allowance — N of A in the last W
  days.`).
* **`habit`:** a miss run of at least `miss_alarm` that overlaps the report week
  (`<label>: missed N days in a row.`), so a mid-week slide is not hidden by a
  recovery before Sunday.
* **Slips this week:** one count-only line per anchor with any non-voided quit
  `slip` or limit `use --slip` dated in the report week (`1 slip on <label> this
  week`, `N slips on <label> this week`). A relapse is never hidden from a
  witness, and the line carries no date and no note. Planned uses never produce
  a line.
* **`milestone`, and a long `quit` run:** nothing. A long run is a win, not a
  worry.

These lines read only the metrics projection's note-free numbers.

**Summary is the default** (`witness_report.anchor_detail: summary`), and the
safe one for the open-source default where witnesses may be strangers. The
report carries only the watch-out lines above: no note, no event date. No
note-reaching reader is even wired in this mode, so the package's
by-construction privacy holds exactly as it does today.

**Full is a global, explicit opt-in** (`witness_report.anchor_detail: full`)
for someone whose witnesses are close enough that sharing the raw record is the
point. Code renders a deterministic, verbatim **Anchor detail** section holding
each anchor's events for the report week: dates, intent, slips, misses,
confirms, and notes.

* **It is never sent to the model.** The section is not part of the compose
  body or the report digest, so the model input is byte-identical with the
  detail on and off.
* **It is never scanned.** It is the person's own words and they opted in, so a
  note is never withheld or dropped, and a note that would trip the
  witness-safe scan can never cost the week's narrative.
* **It survives the fallback.** A provider outage, an unusable reply or a
  tripped scan keeps the section in the metrics-only report.

This is the witness package's **one deliberate, opt-in exception** to its
by-construction privacy, and it is recorded here as such. Any `anchor_detail`
value other than `summary` or `full` is a config error naming the two; an older
binary ignores the key and reads as summary.

**Full detail is lossless.** The section is rendered into an ordered batch of
embeds. It is chunked at event boundaries where possible, and an oversized note
is split across numbered continuation fields without changing its text. Every
field stays within 1024 characters and every embed within the 6000-character,
25-field budgets. Overflow continues in additional numbered messages, one embed
per message, through the existing `SendEmbedReturningID` and `VerifyPresent`
delivery path to the same channel: no new transport, destination, credential or
schedule. There is no "and N more" omission: joining the ordered parts
reproduces every event and note in the markdown rendering. Every part is
read back. The scheduled runner freezes the batch before its first send and
persists each part's payload, message id and read-back status in the existing
witness receipt; a retry resumes that frozen batch without re-sending verified
parts, and the week is marked delivered only once every part has verified. A
partial delivery is loud and stays incomplete. A legacy single-message receipt
still reads.

### 8. Graduation: a plain sunset plus a `graduate` event, never a new state

`anchor sunset <label> --graduated` writes a `graduate` event, then a **plain
v3 `sunset` record** (§3). It never writes a new `state` value, because
`IsSunset` treats any `state` other than `"sunset"` as active: an older binary
would read a `"graduated"` record as a live anchor and start counting it again.
The new binary shows a graduated anchor in `metrics --json` →
`anchors_graduated[]`; an older binary shows an ordinary sunset. Eligibility is
only ever surfaced (§4); the retirement itself is always the person's call.

### 9. Older binaries: what they read, and why their writes lose nothing

**Read.** An older binary never opens `engine/anchor_events.json` — nothing in
Lucid enumerates `engine/` and rejects unknown files — and it ignores the new
`lucid.json` keys. It degrades per kind, and nothing is corrupted:

| Kind | What an older binary shows |
|------|----------------------------|
| `milestone` | Exactly the same |
| `quit` | Days since the last slip, through the compat re-date |
| `limit` | Plain days since the anchor date |
| `habit` | Plain days since the anchor date |
| graduated | An ordinary sunset |

**Write.** An older binary's `anchor add|rename|sunset` decodes `anchors.json`
into its v3 `AnchorLog`, appends, and re-marshals `anchors.json` only. Every
record this design puts there is a pure v3 record, so that re-marshal is
lossless; and the store is never touched, so every kind, event and pair intent
survives. A record an older binary appends is an ordinary definition record,
which the new fold reads as a correction when it changes the date — the
meaning it has always had — and as no change to a quit's start when it keeps
the date (a rename or a sunset). A
unit test proves this with a v3-only decode, append and re-marshal followed by a
new-binary fold, and a smoke test proves it with a real run of the previous
release against a throwaway Ledger.

**Backup.** `lucid backup` already archives `engine/` whole, so the store is in
every archive. A downgrade leaves the store intact, and a later re-upgrade reads
it again.

### 10. `lucid validate` covers anchors for the first time

The read-only sweep gains, through storage reads and never by opening the
Ledger itself:

* an `anchors.json` schema check: a non-empty label, a civil date, `state`
  empty or `sunset`, a version of at least 1;
* the event-store schema: the id grammar, a known type, a civil `day`, an
  RFC3339 `recorded_at`, and the per-type fields;
* cross-file checks: every event targets a known minted anchor id, kind
  parameters are valid, `void` targets exist and are legal, and every pair
  intent is whole (§3).

A hand-corrupted event is reported and never panics a read path. A missing
store is clean, and a Ledger with only today's v1, v2 and v3 shapes validates
with no new findings. It runs as its own `anchors` check. A half-written pair is
reported with its reserved id, the missing half and the command to retry. Every
finding is an error except an unfinished `add --kind` whose reserved anchor id
is already recorded: no retry can finish it and nothing reads it, so it is a
warning rather than a gate that fails forever.

## Consequences

* **Every goal shape gets a number that is true.** A planned exception is one
  planned use of an allowance, not a reset. A daily habit costs zero words on a
  normal day and one sentence on an off day, and its kept count can go down. A
  habit that has fully stuck graduates instead of piling up as a counter.
* **Less tracking, more honesty.** One weekly question replaces any daily
  checklist. If confirmations stop, the metric shows unaccounted days rather
  than inflating.
* **The witness report means something.** Watch-outs fire on over-allowance,
  alarm runs and slips; a long quit is never flagged as a worry again; and a
  relapse always reaches a witness as at least a count.
* **Downgrade-safe by construction.** `anchors.json` keeps its shape and
  version, nothing is migrated, a Ledger with no kinds reads byte-identically,
  and an older binary can read and write anchors without losing a kind or an
  event.
* **The fold joins two files**, and the five two-file verbs take three atomic
  writes under a pair-intent protocol. An interrupted pair needs an explicit
  retry, which `validate` names; the run a reader sees is never flattering in
  the meantime.
* **The store rewrites the whole file on every write.** At the scale of one
  person's anchors this is negligible, and it buys an untearable record.
* **The v1 limit and habit math use the latest parameters across the whole
  window.** A parameter change re-reads the window at the new allowance. The
  dated `kind` events keep the history for a later per-day reading.
* **Full detail is a real privacy choice.** With `anchor_detail: full`, notes
  reach witnesses verbatim and unscanned. The open-source default stays
  summary, and the model's input and the witness-safe scan are unchanged in
  both modes.

## Alternatives considered

* **Tracking allowances and habits as observations.** Rejected: observations
  are inventory, never obligation. An allowance or a habit is a target, and a
  target is the Engine's business.
* **Daily check-ins.** Rejected: the fatigue constraint. A normal day must cost
  nothing; a weekly confirm with exceptions logged as they happen is enough.
* **Assume-kept** (an unlogged day counts as kept). Rejected as dishonest: a
  week nobody looked at would read as perfect.
* **The kind as a field on the `anchors.json` records** (with `AnchorVersion`
  4). Rejected: it survives an older binary's read but is erased by its next
  write, silently reverting every quit, limit and habit to a milestone and
  orphaning the events. It is not rollback-safe.
* **Events in `history[]`, or in a sibling array inside `anchors.json`.**
  Rejected: in `history[]` an older binary would fold an event as the latest
  *definition* of its anchor and show the wrong label or date; a sibling array
  is dropped by the re-marshal.
* **A `graduated` state value.** Rejected: an older binary's `IsSunset` reads
  it as active and resurrects the anchor as a live counter (§8).
* **Writing the event first for every two-file verb.** Rejected: a crash after
  a slip event and before its re-date would leave an older binary showing the
  pre-slip run, longer than the truth. The order is chosen per verb (§3).
* **Feeding anchor detail to the narrative model.** Rejected: it widens what
  the model sees, and a note carrying a scan-tripping marker would cost that
  week's narrative.
* **Scanning the detail section and withholding a note that trips it.**
  Rejected: it overrides the explicit decision to share the raw record, and it
  drops a note without the person choosing that.
* **Counting omitted detail** ("+N more anchor events") when a report runs
  long. Rejected: the omitted notes would never reach the witnesses who were
  promised the full record. Overflow continues instead (§7).
* **Other kind names.** `since` / `limit` / `keep` was the first draft; plain
  filler words get misread ("a keep anchor"; "keep" also reads as "keep this
  anchor"). `abstain` / `moderate` / `maintain` and `never` / `rarely` / `often`
  were weighed too, but neither set answers "what is this?" as an everyday
  noun. **`streak` is avoided** because it collides with the chain streak, which
  is kept separate on purpose.

## Future work

* **Per-anchor witness privacy.** The v1 `anchor_detail` opt-in is global; a
  later version could keep particular anchors in summary mode while others share
  full detail.
* **Away Mode accounting for habits.** Travel days could be accounted
  automatically. In v1 a trip is simply a run of noted misses.
* **Per-day parameter history in the limit and habit math.** The dated `kind`
  events already keep every parameter change; the v1 math reads only the latest
  parameters across the window.
