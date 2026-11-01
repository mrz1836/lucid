---
name: lucid
description: >
  Translate chat messages into Lucid's documented router intents and relay the
  binary's replies. Use for the Mirror (capture → structure → one
  resonance-gated pattern → recall), the Engine (one committed daily practice
  with honest accountability), and the observation micro-logs — all writing one
  user-owned, append-only Ledger under ~/.lucid/. This skill is a translator,
  not a brain: it shells out to the same `lucid` commands any surface uses and
  composes no messages of its own.
min_lucid_version: "0.43.0"
---

# Lucid

Lucid is a local-first personal operating system with two cooperating
subsystems — the **Mirror** and the **Engine** — both writing one
user-owned, append-only Ledger under `~/.lucid/`. This skill is Lucid's
harness integration (ADR-0008): the single, versioned artifact that maps a
chat message to a documented router intent and relays the reply.

## What this skill is (and is not)

* **A translator, not a brain.** It maps messages to the documented router
  intents and shells out to the same `lucid` commands as any surface. It
  implements no product logic, composes no messages of its own, and adds no
  command the docs do not name.
* **Versioned with the binary it drives.** It declares the minimum `lucid`
  version it speaks (`min_lucid_version`) and is released in lockstep with the
  binary. A registry installs it from this repo; a second user installs the
  same skill against their own `~/.lucid/`.
* **Sanctuary-preserving.** The agent hosting this skill never reads
  `~/.lucid/engine/`, `~/.lucid/observations/`, or `~/.lucid/registries/`. All
  access goes through the router, which enforces the slice contracts. No model
  call sits in any Engine or observation write path.
* **Public-safe.** This definition names commands and templates only — never
  channels, people, or paths outside `~/projects/lucid/` and `~/.lucid/`.
  Instance wiring (channel ids, schedules, witness contact) lives in instance
  configuration, never here.

## Command map

Every documented command routes through one binary. Run the command; relay
its output. Acknowledge *after* the binary persists, never before.

| Message | Router intent | How the skill drives it |
|---------|---------------|-------------------------|
| `/log <text>` | capture | `lucid log <text> [--day <date>]` |
| media/file attachment | media capture | `lucid attach <path> [--caption <text>] [--day <date>] [--to <kind>:<key>]...` |
| `/checkin` | guided Intake → structure → ≤1 proposal | router check-in (thread-driven, provider-backed) |
| `/closeout …` | Engine close-out | `lucid closeout …` — **verbatim passthrough** |
| `/closeout skip` | honest miss | `lucid closeout skip` — **verbatim passthrough** |
| `/closeout backfill [yesterday\|<date>] [<compact>]` | correct a recent day | `lucid closeout backfill …` — **verbatim passthrough** |
| `/mode <green\|yellow\|red>` | declare today's mode | `lucid mode <…> [--day <date>]` — **verbatim passthrough** |
| `/storm <label\|unwritten>` / `/storm end` | declare/end a storm | `lucid storm <label\|unwritten\|end> [--day <date>]` |
| `/profile <name>` | switch clock profile | `lucid profile <name>` |
| `/status` | read-only L0 surface | `lucid status` — **verbatim passthrough** |
| `/reflect [gate]` | weekly recall (never proposes) | router recall intent (provider-backed) |
| `/ask <question>` | grounded, cited Q&A | router grounded-answer intent (provider-backed) |
| `/person <name>` | deterministic person join | `lucid person <name>` (no model) |
| `/pain` `/ate` `/drank` `/bm` `/mood` `/slept` `/obs <kind> …` | observation micro-log | `lucid obs <kind> … [--day <date>]` (also an inline `@yesterday` token) |
| `/obs where <place>` | sticky stated location | `lucid obs where <place>` |
| a measurement ("weighed 151.4 this morning", "BP 118 over 76, pulse 62") | measurement capture | `lucid obs measurement <metric> <value> <unit> [--day <date>]` — runs immediately, receipt first, like every `obs`; one run per metric (a blood-pressure reading with a pulse is three runs) |
| `/day [date]` | read-only day view | `lucid day [date]` |
| `/packet clinician [@<date>\|all]` | clinician packet export | `lucid export packet clinician …` (post only the path) |
| "export my measurements" | measurement export | `lucid export measurements [@date\|all] [--metric <slug>]` — post only the path, never the rows |
| `/stats [--last N \| --from --to]` | Ledger volume (counts only) | `lucid stats [--last N \| --from --to]` — read-only, counts only, no journal content |
| `/metrics` | derived practice metrics | `lucid metrics` — streak / adherence (trailing 30d) / one line per anchor in its kind's language, read-only |
| a slip on a `quit` anchor ("slipped last night") | anchor event capture | `lucid anchor slip <label> [--day <date>] [--note-file <path>]` — **capture tier**: run immediately, lead with the `anchorev_…` receipt |
| a use of a `limit` anchor ("had a planned slice at the party") | anchor event capture | `lucid anchor use <label> (--planned \| --slip) [--day <date>] [--note-file <path>]` — **capture tier**; the intent is required and **never guessed** — ask "planned, or a slip?" when it was not said |
| a missed day on a `habit` anchor ("skipped the walk, I'm traveling") | anchor event capture | `lucid anchor miss <label> [--day <date>] [--note-file <path>]` — **capture tier** |
| `/contact <name>` / being in touch with someone ("talked to Sam yesterday") | contact capture | `lucid contact <name> [--via <call\|text\|video\|in-person>] [--note-file <path>] [--day <date>]` — **capture tier**: run immediately, lead with the `contact_…` receipt; either direction counts, and it never creates a person |
| `/contact snooze <name>` ("skip Alex Kim this week") | skip this week's check-in | `lucid contact snooze <name>` — **capture tier**; covers the current Monday–Sunday week only and leaves the cadence untouched |
| `/contact due [--wrap]` ("who do I need to catch up with") | who's-due read view | `lucid contact due [--wrap]` — read-only, runs immediately; relay the lists as printed (`--wrap` is the Sunday view: "Due this week" and "Still open from before" only) |
| `/reflect week` | Sunday weekly recall deep-dive | `lucid reflect week` — read-only weekly deep-dive, never writes |
| `/excavate` | select the next memory cluster to excavate | `lucid excavate` — read-only, never writes |
| `/companion [morning\|night]` | compose one companion message on demand | `lucid companion fire --mode <morning\|night>` — dry-run by default; scheduled sends stay scheduler-owned |
| `/witness` | compose the weekly witness report on demand | `lucid witness report` — dry-run by default; scheduled send scheduler-owned |

### Non-chat CLI verbs

Every other current `lucid` verb is **excluded from the chat command map** on
purpose, each for a documented reason — the MVP translator surfaces only the
conversational verbs; the rest are reached by their documented CLI forms:

| Verb | Reason it is not a chat command |
|------|---------------------------------|
| `lucid anchor add` | Record an anchor — `<label> <date> [note…]`, with `--kind <milestone\|quit\|limit\|habit>`, `--allowance N --window D` (a limit: N use-days in any D days; `--window` alone for a habit), and `--note-file`. A consequential lifecycle verb: **echo-and-confirm**. Without `--kind` it is a plain milestone, exactly as before. |
| `lucid anchor kind` | Reclassify an anchor in place — `<label> <kind> [--allowance N --window D] [--note-file]`; also how a limit's allowance changes. Changes what the numbers mean: **echo-and-confirm**. |
| `lucid anchor confirm` | Attest each habit's days since its last confirm — `[<label>…] [--through <date>] [--from <date>]`, through yesterday by default; the ack names the span per habit, which the skill says back. Claims days as kept: **echo-and-confirm**, asked once a week naming the real span (`habit.next_confirm_from` in `metrics --json`). |
| `lucid anchor void` | Correct a misheard `slip`, `use`, `miss`, `confirm` or `graduate` by receipt — `<event-id> [--reason-file <path>]`; append-only, both records kept. Makes the record kinder, so it runs only on an explicit correction: **echo-and-confirm**. A wrong kind is fixed with `anchor kind`, never a void. |
| `lucid anchor sunset` | Retire an anchor (the record is kept) — `<label> [reason…] [--reason-file <path>]`; `--graduated` retires it as graduated ("this became who I am"). **Echo-and-confirm**; eligibility is surfaced by `metrics`, never acted on without the person. |
| `lucid anchor rename` | Change an anchor's display label; its id, kind, events and day count carry forward. **Echo-and-confirm**. |
| `lucid anchor backfill-ids` | Give every pre-id anchor a written id (`--dry-run` previews) — the remedy the event verbs name for a pre-id anchor. An explicit maintenance write, reached by its CLI form. |
| `lucid recall` | Read-only: browse the archive by era, thread, injury, or pet — each dimension flag takes a **name or key** (a case-insensitive, alias-aware human name or the opaque key) (never writes). |
| `lucid gallery` | Read-only: browse stored media as a date-ordered timeline (before/after), filterable by an inclusive date window and/or one linked subject (never writes). |
| `lucid memory` | Record a story from your past — backdated, linked, kept — through explicit subcommands: bare `memory <text>` creates; `memory amend <obs_id>` appends an **append-only** correction (change `--era`/`--certainty`/`--followup` or the body via `--body-file`; `--clear-followup` clears while a bare `--followup ""` is rejected as ambiguous; the `--era` **key** is validated to exist; no field flags is a no-op error; the stable `obs_id` is never re-minted and the original line never rewritten); `memory show <obs_id> [--history]` reads one story folded, with its amendment trail (`original → amended @ recorded_at`). `--caption` is **not** an amend flag (a caption lives on the linked media entry). Amended values fold on every read surface (`recall`/`excavate`/`day`). |
| `lucid era` | Manage life chapters (eras) through explicit subcommands: read-only `era list`, `era create <name>` (the only path that mints a chapter), and `era amend <name>` (append-only). Bare `era <name>` is an amend-only alias that hard-errors on a non-matching name, and reserved subcommand-shaped words (`list`, `ls`, `show`, `help`, names starting with `-`) can never become a chapter — so a fat-fingered subcommand cannot silently create an era. |
| `lucid injury` | Record or amend an injury in your body history. |
| `lucid thread` | Record or amend a thread you're working on. |
| `lucid reframe` | Keep and rotate your self-talk reframes (a *catch → flip* pair) — an append-only inner-work record family reached by its CLI form: `add`/`list`/`surface`, where `surface` returns one reframe per logical day (least-recently-surfaced, idempotent within the day). Deterministic, no model in any path. |
| `lucid focus` | Keep and rotate your daily focus work-ons (a work-on plus an optional success criterion) — an append-only inner-work record family reached by its CLI form: `add`/`list`/`surface`/`retire`, where `surface` returns one active item per logical day (least-recently-surfaced, idempotent within the day) and `retire` deactivates without deleting (`list --all` is the audit view). Deterministic, no model in any path. |
| `lucid rep` | Keep and rotate your daily practice reps (one concrete thing to try, an optional solo variant that works with nobody around, and an optional `level` — your own label for the step size, never a grade) — an append-only inner-work record family reached by its CLI form: `add`/`list`/`surface`/`retire`. `add` takes `--solo`/`--solo-file`, `--level N` (a positive integer) and `--body-file`; `surface` returns one active rep per logical day **with its solo variant** (least-recently-surfaced, idempotent within the day, never level-aware, never guessing whether the day is solo); `retire` deactivates without deleting (`list --all` is the audit view). It never asks whether a rep was done and keeps no streak. Deterministic, no model in any path. |
| `lucid contact` (cadence and correction) | Set and keep per-person check-in cadences — `every <name> <Nd\|Nw>` sets or changes the window (`28d` and `4w` are the same; on a paused cadence it changes the window and stays paused), `pause` / `resume` / `retire <name>` move the cadence, and `void <contact_…>` corrects a logged contact or snooze by its receipt (append-only, both records kept; a cadence is corrected with the cadence verbs, never voided). These change what the due lists mean: **echo-and-confirm**. The capture (`lucid contact <name>`), `snooze` and `due` forms are in the command map above. The only status word is "due"; there is no streak, score or percentage, and a person not reached simply rolls into next Monday's "Still open from before". Lucid never contacts anyone you track; it only reminds you. Deterministic, no model in any path. |
| `lucid gratitude` | Keep a running gratitude tally (the count of things you return to gratitude for) — an append-only **registry** kind reached by its CLI form: `add`/`list`/`merge`/`import`/`reconcile`/`thank`. `add "<thing>"` matches the wording to an existing entry in up to three tiers — canonical key, a deterministic word match, then an optional, opt-in by-meaning model judge — and bumps a clear match automatically, creates a new entry for a new thing, or **suggests** when it is unsure. A non-interactive or `--json` caller in that ambiguous band gets exit `1`, nothing written, and the suggestion (candidate ids, scores); relay it to the person and re-run with their answer — `add --into <id>` (bump that entry) or `add --new` (start a new one) — never guess. `add --person <subject>` also links a person (link-only); `merge <src> <dst>` folds a duplicate; `reconcile` proposes folds (dry-run unless `--apply`); `import` seeds an existing count via explicit `--count`/`--first`/`--last`; `thank <id> --person <subject>` links a person and privately notes you told them — tally-neutral, and Lucid sends nothing. `list` may offer a gentle "You might tell …" line, never counted and never an off-limits person. Every write returns a receipt id, distinct from the stable entry id `list` shows. The verbatim nightly gratitude stays a separate, unchanged `lucid log` (`#gratitude`). Deterministic except the optional tier-3 judge; with no model it runs on the deterministic tiers alone. |
| `lucid retro` | Keep the retro parking lot — the `R-NNN` queue of things parked to revisit at the weekly Retro or a Gate — an append-only inner-work record family reached by its CLI form: `park`/`list`/`show`/`resolve`/`defer`. `park "<item>"` mints the next global `R-NNN` and returns it plus a per-write receipt (the id an agent echoes back — `parked as R-012`); `list` defaults to open **and** deferred (`--all` adds resolved, `--resolved` shows only it), `show <R-NNN>` renders one item, `resolve <R-NNN> "<resolution>"` moves it to `resolved` **without deleting** (the append-only audit trail), and `defer <R-NNN> "<reason>"` sets the first-class `deferred` status while keeping it in the default `list`. Transitions reference the affected `R-NNN` without consuming a new id. Deterministic, no model in any path. |
| `lucid wheel` | Keep the monthly Wheel of Life — your own integer **1–10** rating of eight pillars (health, relationships, career/work, finances, personal growth, fun/recreation, environment, contribution) — an append-only **registry** kind, one file per calendar month, reached by its CLI form: `add`/`show`/`list`. It is a monthly state write assembled from a guided conversation, so it is never chat-mapped: gather all eight ratings **from the person** (never propose, infer, or default a number — any evidence shown before they rate is facts only, with no number), echo the assembled wheel, and run `add` only on their confirmation. `add` requires all eight ratings (a missing, out-of-range, or non-integer one writes nothing); `add --input <path\|->` takes the whole wheel as strict JSON, keeping notes and the reflection off the command line. `--suggested <pillar>=<1-10>` stores an optional calibration value beside a rating — accepted only with all eight ratings, never the score, never in the trend, and never shown back before the person rates; any gap between it and their number is raised only **after** they rate, as an optional talking point, never a correction. `--vision-reviewed` / `--vision-reflection` record the monthly vision re-read (no link to any document is stored). A second `add` for the same month appends a new snapshot that wins on read — nothing is mutated, and every write returns its own receipt id. `show` renders the trend (rating, signed delta vs the prior stored month, sparkline, lowest and biggest-drop callouts) as `key: value` lines with no table; `list` shows months, and both say `no prior month` rather than invent a delta. No balance score, streak, or target. Deterministic, no model in any path. |
| `lucid pet` | Record or amend a companion in your pet registry — a first-class named referent with its own `active`/`rehomed`/`passed` lifecycle. |
| `lucid link` | Point a stored media attachment at a subject it is about — a person, injury, day, anchor, or thread — through the append-only link ledger; a retroactive curation verb reached by its CLI form. |
| `lucid unlink` | Retire a media↔subject association by appending an unlink event — nothing is destroyed, the pair just stops being live. |
| `lucid annotate` | Attach a free-text note to a media↔subject association without changing whether it is linked. |
| `lucid workout` | Recommend, log, and review your training (config-gated). `workout amend <obs_id>` corrects or fills in a logged session after the fact (`--rpe`/`--duration`/`--type`/`--movements`/`--parts`/`--notes`/`--notes-file`, or `--day` to re-date it) by appending one append-only correction — the original line is never rewritten, and reads fold the correction in. Use it instead of re-logging, which double-counts the session. Only the passed fields change; list flags replace; anchors and soreness/pain aren't amendable. |
| `lucid obs void` | Retract a mistaken **measurement** reading by its `obs_…` receipt — `<obs-id>`; append-only, both records kept. The reading drops out of `lucid export measurements` and the packet's "Measurements in range", and `lucid day` shows both. Then re-log the right value with `lucid obs measurement <metric> <value> <unit> --day @YYYY-MM-DD`. It takes no `--day` and no reason text, and a void can't be voided. `workout` and `memory` observations use their own `amend`; other kinds aren't voidable yet. It makes a reading stop counting, so it runs only on an explicit correction: **echo-and-confirm**. |
| `lucid structure` | Structure a raw entry you did not just capture, or a window of them. |
| `lucid self` | Read and record durable facts about yourself. |
| `lucid person create` | Deliberately record a known person with **no** model call — the deterministic import/backfill primitive (people otherwise appear only as `lucid structure` extracts a mention). Idempotent on an already-recorded name (an "already recorded" no-op, never a fork); accepts the `person set` durable-field flags (`--dob`/`--relationship`/`--note`, plus `-file` siblings) to create-and-enrich in one call. Reached by its CLI form, not chat-mapped — `/person <name>` above stays a read. |
| `lucid backup` | Write the must-keep Ledger trees to a single `.tar.gz` archive; a data-safety operation rather than a conversation. |
| `lucid restore` | Rebuild a Ledger from a backup archive; destructive-adjacent and deliberately CLI-only. |
| `lucid secret` | Maintain a names-only catalog of hush secret handles (`add`/`list`/`note`/`remove`) — a handle and an optional note, never a value; `note` amends a live handle's note in place (or removes it with `--clear`), keeping its creation time, and the catalog has no reveal, fetch, or storage path. An operational/config verb reached by its CLI form. |
| `lucid init` | Scaffold the `~/.lucid/` Ledger tree. |
| `lucid update` | Self-update lucid in place from a GitHub release (alias: `upgrade`). |
| `lucid version` | Print lucid build metadata. |
| `lucid completion` | Generate the autocompletion script for the specified shell. |
| `lucid serve` | Drive the interactive `/checkin` flow over a stdin/JSON protocol. |
| `lucid bootstrap` | Toggle historical-entry (bootstrap) mode. |
| `lucid validate` | Validate the Ledger and boundary invariants (read-only). |

The skill never invents a command, an agent, or a field.

### Backdating with `--day`

Every verb that stamps a logical day reads **one** shared date grammar, so a
capture or a state record can be attributed to a prior day. The grammar and the
per-verb precision tiers are specified once in the command reference —
[Backdating with `--day`](../../docs/usage/commands.md#backdating-with---day) —
never re-decided here. In brief:

* **Which verbs carry it.** `log`, `obs`, `attach`, `mode`, `storm`, `memory`,
  `workout log`, `workout amend` (a re-date of the logged session),
  `anchor slip` / `use` / `miss`, `rep add`, `contact` (the bare capture form),
  and `closeout` (where `--day` is an alias onto `closeout backfill`);
  `anchor confirm` reads the same grammar through `--through` / `--from`. `obs`
  also accepts an inline `@yesterday` token in its value stream. The anchor
  event verbs default to the current logical day and reject a partial date or a
  day before the anchor's start. The other `contact` writes (`every`, `pause`,
  `resume`, `retire`, `snooze`, `void`) take no `--day`: they stamp the current
  logical day. `obs void` takes no `--day` either: a void files under the
  current logical day, and the re-log carries the reading's own day.
* **The grammar.** `@yesterday` / `yesterday` (the logical day before this one,
  04:00-rollover aware), `@YYYY-MM-DD` / `YYYY-MM-DD` (a civil day, taken
  literally), a partial `2014` or `2014-09` (snaps to the period's first day),
  and an optional time — `--day "@yesterday 19:30"`. A future day is always
  rejected; the real capture time is always kept as `recorded_at`.
* **Additive vs. gap-fill.** A backdated `log` / `obs` / `attach` is still an
  additive capture — it files under a prior day and overwrites nothing — so it
  runs immediately, no confirmation. `mode --day` is **gap-fill only** (never
  overwrites a mode that was declared) and `closeout --day` routes onto `closeout
  backfill` (`backfill_window_days` window, both today and the future rejected);
  both are state writes, so they follow the echo-and-confirm rule below.

## Natural-language translation (voice-first)

The slash/CLI verbs above are the **canonical baseline** — precise,
deterministic, and the contract every surface shares. This skill **may** also
accept plain, spoken-style phrasing (the shape a voice-to-text message takes),
recognize the intended verb, and **assemble the documented command** from it. It
invents **no** command, field, or flag of its own: it can only assemble a verb
that already exists, and if a message maps to nothing real it says so rather than
guessing. The CLI is the contract; natural phrasing is the human interface. The
full mechanism, per-verb phrasings, and synthetic examples live in
[`../../docs/usage/natural-language.md`](../../docs/usage/natural-language.md).

**Execution posture — reads run, writes confirm.** Voice-to-text is lossy and an
Engine write lands an immutable day record, so the two verb classes are handled
differently:

* **Read verbs run immediately.** `status`, `day`, `metrics`, `log`, and `obs`
  change no day record (a capture is additive, never an overwrite — which holds
  for a backdated `--day` capture too), so the skill assembles and runs them as
  soon as it understands the message — no confirmation step.
* **Anchor captures run immediately too.** `anchor slip`, `anchor use` and
  `anchor miss` are the **capture tier**: append-only, and logging a lapse can
  only make the record more honest. The skill runs them as soon as it
  understands the message, **leads its reply with the receipt** (the
  `anchorev_…` id), and fixes a mishearing with `anchor void` (echoed, below).
* **Contact check-ins split the same way.** `contact` and `snooze` are the
  **capture tier**: logging that you were in touch, or skipping one week, can
  only make the record more truthful. The skill runs them as soon as it
  understands the message, **leads its reply with the receipt** (the
  `contact_…` id), and fixes a mishearing with `contact void`. The verbs that
  change what the due lists mean — `every`, `pause`, `resume`, `retire` and
  `void` — are **echoed and confirmed**. `contact due` is a read and runs
  immediately. The mappings, with synthetic names:
  * "talked to Sam yesterday" → `lucid contact Sam --day @yesterday`
  * "who do I need to catch up with" → `lucid contact due`
  * "skip Alex Kim this week" → `lucid contact snooze Alex Kim`
  * "check in with Jordan Lee every 3 weeks" → `lucid contact every Jordan Lee 3w` (echo + confirm)

  Either direction counts, so "Sam called me" logs the same contact. A how
  (`call`, `text`, `video`, `in-person`) goes in `--via` only when it was said,
  and a note goes through `--note-file`. A name that matches no one is refused
  by the binary with a pointer to `lucid person create`; relay it and never
  create the person yourself. Relay the due lists as printed: "due" is the only
  status word — never say "overdue", never count days, never ask why someone was
  not reached.
* **Measurements are `obs` captures.** A weight, a blood-pressure number or a
  lab value is `lucid obs measurement <metric> <value> <unit>`, one run per
  metric. Like every `obs`, it runs as soon as the skill understands the
  message and **leads its reply with the receipt** (the `obs_…` id). The
  mappings:
  * "Weighed 151.4 this morning" →
    `lucid obs measurement weight 151.4 lb`, shown *for a user whose weight
    unit is lb*. With no known weight unit, ask once: "lb or kg?"
  * "BP 118 over 76, pulse 62" → three runs:
    * `lucid obs measurement bp-systolic 118 mmHg`
    * `lucid obs measurement bp-diastolic 76 mmHg`
    * `lucid obs measurement resting-hr 62 bpm`

  "This morning" on the current logical day needs no `@` token; a lab result
  from another day uses `--day @YYYY-MM-DD`. The unit is always the third
  token, before any note or `#tag`.
  A unit is implied only where it is universal for the metric (blood
  pressure → `mmHg`, pulse or heart rate → `bpm`); any other missing unit, and
  any missing value, gets one question — never a guess. A deployment's own
  notes may fix a default unit (for example, weight in lb). Keep slugs stable —
  `weight`, `bp-systolic`, `bp-diastolic`, `resting-hr`, plus lab slugs like
  `ldl-c` and `a1c` — so a series stays one series.
  Lucid does not enforce a metric vocabulary, so reuse the slug the person
  already uses. Never interpret a value or call it high, low or abnormal; relay
  the receipt only.

  To fix a reading, `lucid obs void <obs-id>` is echo-and-confirm, like
  `anchor void` and `contact void`:
  name the reading (date, metric, value, unit), show the command, and wait for
  a yes. Find the id from the capture receipt or `lucid day <date>`. After the
  void, re-log the right value with `--day`
  (`lucid obs measurement <metric> <value> <unit> --day @YYYY-MM-DD`). Only
  measurement readings can be voided; a workout or a memory is corrected with
  its own `amend`.
* **State-writing verbs are echoed and confirmed.** For `closeout`,
  `closeout backfill`, `mode`, and `closeout skip`, the skill **assembles the
  compact command, shows it back, and waits for a one-word confirmation** before
  running it. The consequential lifecycle verbs `storm` and `profile`, and the
  anchor verbs that change what a number means — `anchor add`, `kind`,
  `confirm`, `void`, `sunset` (including `--graduated`) and `rename` — are
  echoed for the same one-word confirm, as are the contact cadence verbs above
  and `obs void`, which makes a measurement reading stop counting.

**Ask, don't guess.** When a required piece — a chain-link state, a capacity, an
observation field (including a measurement's value, or its unit when the unit
isn't universal for the metric), a limit use's intent (planned or slip), which
anchor a lapse belongs to, or which person a contact names when the binary
lists several candidates — is missing or ambiguous, the skill asks **one**
concise question and waits. It never fabricates a link state, a capacity, an
intent, a unit, or any value the user did not give. It never reclassifies, confirms or graduates an
anchor on its own: `metrics` only surfaces eligibility and over-allowance.

**The boundaries do not change.** This is a phrasing layer over the same core;
every guarantee holds exactly as it does on the command line:

* **Engine verbs stay deterministic and are relayed verbatim** — never scored,
  embellished, or celebrated (the verbatim-passthrough rule below applies
  unchanged).
* **Every write goes through `lucid`.** The skill only assembles and runs the
  documented command; the agent-free core performs the write and acknowledges
  after it lands. The skill never writes state itself.
* **Keep free text off the command line.** When invoking `lucid` through a
  shell, never let a user-authored free-text argument (journal lines, captions,
  limiters with punctuation, observation notes) reach the command line raw —
  punctuation such as `;`, `&`, `|`, `!`, parentheses, and quotes is otherwise
  interpreted by the shell and can silently split or truncate the write.
  **Preferred:** pass the text off the command line via the field's `--…-file
  <path>` flag (write it to a temp file first, or `-` for stdin) —
  `--journal-file` (closeout), `--body-file` (log/obs/memory/thread/gratitude/
  focus/rep), `--catch-file`/`--flip-file` (reframe), `--tone-file`/`--why-file`/
  `--followup-file`/`--caption-file` (memory), `--intent-file`/`--domain-file`/
  `--note-file` (thread/era/self, anchor add/kind/slip/use/miss, and contact),
  `--value-file` (self), `--reason-file` (anchor sunset/void),
  `--success-file` (focus), `--solo-file` (rep), `--input` /
  `--vision-reflection-file` (wheel). **Fallback** for any shell-only
  path: wrap the whole free-text argument in single quotes (or a quoted
  heredoc). Then verify the saved raw id/day when the write is consequential. A
  sealed closeout tagline that still ends up wrong is corrected with `lucid
  closeout amend --day <grammar> --journal-file <path>` (supersedes the
  displayed line; Engine-critical fields stay immutable).
* **Mirror content is never scored.** A journal line or a capture is held, not
  graded; the voice-first layer adds no judgement to what is written.
* **The Ledger is never hand-edited.** No agent touches the files under
  `~/.lucid/` directly — the store is append-only and schema'd, and the only way
  in is a `lucid` verb.

**Coverage.** The voice-first layer maps the everyday verbs plus the live
practice-lifecycle verbs `storm`, `anchor` (all four kinds — `milestone`,
`quit`, `limit`, `habit` — and every subverb), `profile`, `metrics`, and
`contact` (each a documented verb in the
[command reference](../../docs/usage/commands.md)). `contact` is the contact
check-ins — capture, `snooze`, `due` and the cadence verbs — and is unrelated
to the `/checkin` guided capture. The
three **provider-backed** Mirror verbs are **shipped** and driven by their
documented forms: `/ask` runs `lucid ask <question…>` (grounded, cited Q&A over
your validated insights); `/reflect` runs `lucid reflect [gate]` (recall that
never proposes a new pattern); `/checkin` runs through the interactive `serve`
flow (`lucid serve`, the stdin/JSON check-in protocol). The Sunday weekly
reflection deep-dive is `lucid reflect week` — a read-only pass over the past
week that never writes (its nested `apply`, the only write path, goes through the
resonance gate and is not a chat-mapped verb).

**Companion, on demand.** The morning and night companion messages are composed
by Lucid's model provider from your own opaque prompt files and the chain's
honest live numbers — never fabricated. They fire automatically inside `lucid
scheduler run` on the bell and tripwire marks; `/companion [morning|night]`
composes one on demand via `lucid companion fire --mode <morning|night>` (dry-run
by default, so a preview sends nothing). Delivery stays scheduler-owned.

## Media attachments

When the user asks to save or log an image/file, first persist the inbound file
to a safe temporary local path if the harness only exposes it as transient media,
then run `lucid attach <path>` with an optional caption. Do not store user media
under the agent workspace as the final archive. The canonical media store is
`~/.lucid/media/`, and `lucid attach` emits the stored path, sha256, logical day,
linked raw id, and caption. Relay those fields back to the user.

When the user names what the file is *about* — a person, injury, day, anchor, or
thread — pass one repeatable `--to <kind>:<key>` per subject and `attach` links it
in the same flow (`attach --json` then also reports the `linked` subjects). Every
`--to` is validated **before** the media is written, so a malformed or
unresolvable subject saves no media, no raw entry, and no link. Those associations
are corrected afterward from the CLI, never conversationally: `lucid link` points
an existing media at more subjects, `lucid unlink` retires a pair, and `lucid
annotate` notes one — all append-only, and the stored binary is never touched.

Under `--json` the write verbs emit a receipt, not prose: `log`, `closeout`
(and its `amend` / `backfill`), `obs` (and `obs void`), `reframe add`, `focus add` / `retire`, and
`rep add` / `retire` emit a bare `{receipt_id, logical_date}` JSON object on
stdout, while `mode` emits the minimal `{mode}` shape. The anchor event verbs
(`anchor kind`, `slip`, `use`, `miss`, `void`) extend that receipt to
`{receipt_id, logical_date, label, event}`, plus `compat` when a definition
record was appended alongside; `anchor confirm` emits `{through, confirmed,
already_confirmed}`. The contact writes (`contact`, `contact every`, `pause`,
`resume`, `retire`, `snooze`, `void`) extend it the same way to
`{receipt_id, logical_date, person_key, event}`, `receipt_id` being the
`contact_…` id. Diagnostics stay on stderr, so the `--json` stdout is always
parseable.

## Verbatim passthrough on Engine verbs

`closeout`, `mode`, and `status` are deterministic and agent-free. Relay their
output **verbatim** — never interpret, score, embellish, or add a celebration.
The Engine is the honest number; the skill is a wire, not a coach. Because the
Engine runs no model, a close-out completes with the agent, the model, or the
whole harness down — a plain `lucid closeout` at a terminal (or a line
backfilled later) still finishes the night.

## Scheduled sends stay scheduler-owned

The autonomous messages are the scheduler's, never composed or initiated by this
skill: the bell and the morning tripwire (pre-committed templates), plus the
morning/night companion messages and the weekly witness report (composed by the
provider from the user's own opaque prompt files and the chain's honest live
numbers, never fabricated; when the user opts in, the witness report is also
grounded in one operator-approved week-notes file, which it renders verbatim).
All are posted by the harness's native scheduler (or the standalone
`go-flywheel` path). The `companion fire` and `witness report`
verbs above only ever preview these on demand; delivery stays scheduler-owned.
The Monday contact check-in post is scheduler-owned the same way: when the user
enables `contact_weekly`, the scheduler posts a fixed, model-free template of
the week's "Due this week" and "Still open from before" lists and birthdays to
the user channel only, and never to anyone the user tracks. This skill never
posts or re-posts it; `lucid contact due` shows the same lists on demand, and a
harness's own Sunday pass may fold in `lucid contact due --wrap --json`. The
enrichment job likewise runs on its own schedule and posts nothing. This skill
only handles user-initiated commands.

## Scheduler health (read-only diagnostics)

Before answering "is the scheduler running?", "why did morning/night not send?",
or "what fires next?", run the read-only health surface and relay its verdict —
never guess from process state:

```sh
lucid scheduler status          # calm human summary + a one-word verdict
lucid scheduler status --json   # machine-readable, with a top-level verdict field
```

It aggregates local state only — the companion enabled/disabled state, the chain
fire marks, the Engine and companion periodics (what fires next), the last delivery
receipt per window (what happened last), and a best-effort host/supervisor probe —
and rolls them into one verdict with a 3-tier exit code: `0` ok, `1` warn, `2`
error, identical in text and `--json`. It **sends nothing, renews no secret, and
reads no prompt body**, so it is safe to run any time. Relay the verdict plus the
relevant failing check; never paraphrase it into false confidence.

The report also lists **recent compose fallbacks**: scheduled sends from the last
7 days that were delivered as the deterministic fallback because the model's reply
could not be used (a *Recent compose fallbacks* section in text, a top-level
`compose_fallbacks` object in `--json`, each entry naming the kind, time, and
reason). They are informational and never change the verdict or exit code. Relay
them when asked why a message felt generic or whether sends were degraded.

## Witness wiring (Ring 0)

By default the witness sees exactly one thing: the L2 escalation template,
which carries streak, mode, and storm state and **zero** journal or capacity
content. The optional weekly witness report, once the user turns it on, adds
one weekly card, and carries more only where the user deliberately opts in —
full anchor detail, and operator-approved week notes from a file the user keeps
outside the Ledger ([`../../docs/usage/witness-report.md`](../../docs/usage/witness-report.md)).
Lucid reads no journal content for either. The `#lucid` channel and its session
threads are invisible to the witness role; the witness never gains a route into
the Ledger. This boundary is instance configuration — the witness contact is
never named in this skill.

## Read-only verification

`lucid validate` runs the schema, anchors, public-boundary (S-7),
diagnostic-language (S-8), sanctuary, and doc-link checks read-only. The anchors
check reports an interrupted two-file anchor write as a half-written pair, with
the command to retry; it never repairs anything. A harness or a contributor
can run it any time to confirm the boundary holds; it writes nothing and never
scaffolds the Ledger.
