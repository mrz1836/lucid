# The daily companion

The **companion** is Lucid's optional pair of daily messages — one in the
morning, one at night — composed through your model provider from your own prompt
files and the chain's honest live numbers, and delivered to your user channel. It
is the Mirror-side, model-allowed counterpart to the Engine's accountability
*teeth*: the bell and tripwire stay modeless and deterministic (a model can never
soften "you missed"), while the companion wraps that same window in a warm,
in-voice message.

It ships **off**. A fresh Ledger runs the pure Engine loop exactly as before until
you opt in; nothing below happens until you set `companion.enabled: true`.

- **What it is** — a Mirror-side job that reaches the model through
  [`../adr/0006-model-access.md`](../adr/0006-model-access.md), never the Engine.
  See [`../architecture.md`](../architecture.md) for the Mirror/Engine split and
  why the teeth stay pure.
- **Where it runs** — inside `lucid scheduler run`, in the same supervised
  process as the teeth (config-gated), so it is up whenever the scheduler is. It
  is not an external cron.

## The two windows

The companion composes for two windows a day:

- **Morning** — fires on the chain's **tripwire** mark (default `06:00`).
- **Night** — fires on the chain's **bell** mark (default `19:00`).

Both are in your host's local time and follow the same DST-correct clock the
Engine uses. **The fire times are not companion settings.** The companion inherits
the `chain.json` `bell_time` / `escalation.tripwire_time` marks so it can never
drift from the deterministic pair — the companion and the teeth fire at the same
instant, and you get exactly **one message per window**. To move a window, change
the chain mark (see [`../engine.md`](../engine.md)); the companion follows.

## Turning it on

Add a `companion` block to `lucid.json` and point the three path keys at your own
prompt files:

```json
"companion": {
  "enabled": true,
  "morning_template": "/home/you/lucid-prompts/morning.md",
  "night_template": "/home/you/lucid-prompts/night.md",
  "system_prompt": "/home/you/lucid-prompts/system.md",
  "morning_routine": "/home/you/lucid-prompts/morning-routine.md",
  "night_routine": "/home/you/lucid-prompts/night-routine.md",
  "model": ""
}
```

| Key | Type | Meaning |
|-----|------|---------|
| `enabled` | bool | Gates the whole feature. Default `false` → the scheduler runs only the pure Engine teeth. |
| `morning_template` | path | The morning-window template — an opaque prompt file, passed to the model as the user message. |
| `night_template` | path | The night-window template — same, for the night window. |
| `system_prompt` | path | The system prompt, passed to the model as the `System` role on every compose. |
| `morning_routine` | path | **Optional.** An opaque file holding your intended morning routine. When set, its contents are read and injected as **context** for the morning message so the companion never invents your routine. Absent/empty → the routine grounding is simply omitted. |
| `night_routine` | path | **Optional.** Same, for the night window's intended routine. |
| `model` | string | Optional. Overrides `provider.model` for the companion's compose call; empty inherits the provider default. |

When `enabled` is `true`, the three **prompt** paths (`morning_template`,
`night_template`, `system_prompt`) must be non-empty or the config is rejected at
load. The two **routine** paths are optional and never gate the feature: an unset
or empty routine key omits that window's routine grounding, and a set-but-
unreadable one is a non-fatal degrade (the message still sends — see
[When things go wrong](#when-things-go-wrong)).

**The configured paths are the whole personal seam.** Lucid opens exactly the
files you name — the three prompt files and, when set, the two routine files —
and never walks the directory holding them (no dir-walk, no filename convention),
so you can keep them anywhere, inside or outside the repo, and Lucid only ever
reads the files you named. Each file is opaque, self-contained text: Lucid does
not parse it, follow links in it, or require any structure. Neutral, copy-me
prompt examples live in [`companion-examples/`](companion-examples): `system.md`,
`morning.md`, and `night.md`.

**Credential-dumb, like the rest of `lucid.json`.** No channel id and no token
live in the config — the target channel and the harness token stay env-only
(`LUCID_USER_CHANNEL_ID` / `LUCID_HARNESS_TOKEN`), and no model API key lives here
or anywhere in `lucid.json` (auth is the vendor CLI's or the local daemon's). Full
schema notes: [`../mvp/data-model.md`](../mvp/data-model.md) §"lucid.json".

## How a message is composed

Lucid owns the message **layout**; the model fills only the prose. That split is
what makes the briefing scannable and testable: the sectioned scaffold — the
status panel, the section order, the whitespace, and the freshness labels — is
rendered by deterministic Lucid code, and the model writes only two short slots
dropped into fixed places. A model can never produce a wall of numbers or
restate a raw metric, because it never owns the layout.

For each window the compose worker:

1. Reads the two configured opaque prompt files for the window — the
   `system_prompt` and the per-mode template — on their explicit paths.
2. Reads the chain's **honest live numbers** in-process, from the exact
   projection `lucid metrics --json` and `lucid status --json` expose, and lays
   them out as a compact **status panel** (streak + adherence and, only when they
   hold, the ambient signals). Numbers are copied straight from the chain — never
   rounded or invented — and this panel replaces the old wall-of-numbers dump.
3. Reads a **bounded, recent slice** of the Ledger's render-relevant observations
   (`mood`, `pain`, `sleep`, `symptom`, `habit_change`, `withdrawal`,
   `commitment`) and, when configured, the routine file for the window, and lays
   them out as **context sections**, each stamped with an "as logged &lt;date&gt;"
   freshness label.
4. Sends the system prompt, the per-mode template, and the deterministic context
   (panel summary + observation digest + routine) to the model (default backend
   `claude_cli`, model `opus`; see the provider config) and asks it to return
   exactly **two slots** — an interpretation and one or two next actions.
5. Parses the two slots and renders the final message from the deterministic
   scaffold. Every enrichment read is best-effort — a missing slice or an
   unreadable routine omits its section, never fails the send — while the prompt,
   verdict, and live-number reads stay loud.

On a **missed day** the scaffold is rendered as usual and the Engine's own
deterministic verdict line is appended **byte-for-byte** below it. The model
writes the warmth; it never touches the verdict, so a model can never reword
"you missed". The witness-channel escalation still fires modeless and separate —
the companion only presents the user-channel line.

### The message scaffold

Every message is one deterministic scaffold, rendered in a fixed order so status,
interpretation, and action are always in distinct, scannable regions:

- **Header** — `{emoji} **Morning** · {Weekday, Mon D}` (Night in the night
  window).
- **Status panel** (hero) — a compact block, a handful of lines at most: the
  chain's live numbers — streak + adherence, the error budget, and, only when
  they hold, days-to-gate, consecutive misses, a standing storm. This is the raw
  streak/adherence/gate data, kept as a small meaningful panel rather than the
  overwhelming metric list it replaces.
- **Context sections** — `{emoji} **{Label}** · {meta}` headers with `•` bullet
  lines, one section per available signal group (body & state, change &
  withdrawal, commitments, routine anchor). A section with no data is omitted
  entirely.
- **Interpretation** — what matters, what changed, what needs attention — a few
  sentences. Morning displays this as **The read**; night suppresses the
  separate interpretation slot so close-out stays compact.
- **Action cue** — morning renders this as **Morning routine**, grounded in the
  configured routine; night renders it as the single **Close-out** cue. The
  scaffold never renders a generic **Next** section or invents an action when no
  routine/action is available.
- **Footer** — an optional closing line, and the appended verdict on a missed day.

Major groups are separated by blank lines only. Both morning and night omit
horizontal divider lines, so the card reads like one compact phone message rather
than a status memo with separators. There are **no markdown tables** — a chat
surface renders them as raw text, so the scaffold uses bullets and key/value
lines only.

**Night is a close-out, not a second morning.** The night scaffold reorders the
regions into a close-out ritual — the day's read-back first, then the compact
status panel, then a single close-out next action, then the verdict — distinct
from the morning's forward-looking order. The model may still produce an
interpretation slot for contract compatibility, but the night renderer does not
display a separate examen/read section; the action is the point.

### The model slots

The model may return two labeled slots, each introduced by a delimiter line on
its own line:

```text
%%INTERPRETATION%%
<a few sentences — what matters, what changed, what to watch>
%%ACTIONS%%
- <one concrete next action>
- <an optional second action>
```

- `%%INTERPRETATION%%` — the interpretation slot: free prose in your own voice
  (owned by your personal template), a few sentences. Morning renders it as
  **The read**; night currently does not display it.
- `%%ACTIONS%%` — one or two lines, each a single concrete action. Morning
  renders these as **Morning routine** when grounded in the configured routine;
  night renders them as **Close-out**. The header is never the generic **Next**.

The parser is deliberately tolerant: if the `%%INTERPRETATION%%` delimiter is
absent, the whole trimmed model reply becomes the interpretation and there are no
actions — still a valid scaffold. Everything outside the slots (the panel, the
sections, the headers, the freshness labels, the ordering) is Lucid's, not the
model's.

### Freshness — "as logged" and stale

Every line derived from the Ledger carries an explicit **"as logged
&lt;YYYY-MM-DD&gt;"** stamp — the logical date of the event it came from — so
nothing unverified is silently mixed into today's message. When the most-recent
event feeding a section is older than the staleness threshold (default: 2 days),
a `stale` flag is appended to that section's header meta. The chain's live
numbers need no stamp: they are always current. This build reads **no external
data**, so there is no live-refresh path — the freshness rule governs logged
Ledger context (commitments especially, which can sit for days).

### Routine context

`morning_routine` and `night_routine` (see [Turning it on](#turning-it-on)) point
at your own routine docs. When set, the compose worker reads the file for the
window and injects it as **context** for the model — so the interpretation and
the next action are grounded in the routine you actually intend, instead of one
invented each day. The routine text is context **only**: it is not dumped
verbatim into the delivered message. An unset routine key is skipped; a set-but-
unreadable one omits the routine grounding and records the miss (never a failed
send — see [When things go wrong](#when-things-go-wrong)).

### The morning-brief layout it mirrors

The scaffold deliberately mirrors a proven **morning-brief** structure — the
shape a good daily briefing uses to stay scannable — and adds the two regions a
pure status brief lacks:

| Morning-brief element | Companion scaffold |
|---|---|
| Header: emoji + bold label · day | `{emoji} **Morning** · {Weekday, Mon D}` |
| Hero status panel | The compact live-numbers panel |
| `{emoji} **{Label}** · {meta}` section headers, `•` bullets | The context sections |
| Appended freshness flag on stale data | The `stale` flag on a section's meta |
| Footer | Closing line / appended verdict |
| *(none — a status brief stops here)* | **Interpretation slot** — what it means |
| Action row | **Morning routine** / **Close-out** slot — the concrete cue |

Those last two regions are the difference between a status dump and an operating
surface: the "what it means" and the "what to do next" a briefing alone never
carries.

## Testing a fire on demand

```sh
# Compose and print the morning message — a dry run, zero side effect.
lucid companion fire --mode morning

# Same for the night window.
lucid companion fire --mode night

# Actually deliver one idempotent, read-back-verified message now.
lucid companion fire --mode night --deliver
```

`fire` defaults to a **dry run**: it composes and prints the message with no
delivery and no receipt written — use it to preview the voice or prove the
pipeline end to end. `--deliver` sends exactly one message through the full path a
scheduled fire takes (idempotency, read-back, and the missed-fire window all
apply), so a delivered test fire behaves exactly as a real fire would. A dry run
labels the miss-day and deterministic-fallback paths when they fire, so a preview
is never mistaken for the model's warm output when it was not.

## Pausing it

Set `companion.enabled: false` (or remove the block) and restart
`lucid scheduler run`. The scheduler reverts to the pure Engine teeth — the bell
and tripwire send on their own, exactly as before the companion existed. Pausing
the companion never disables accountability.

## When things go wrong

The companion degrades in layers and is designed to **never fall silent** — a dead
06:00 or 19:00 is visible, not a quiet gap.

- **Provider unreachable** (timeout / unavailable): the worker renders the same
  deterministic scaffold — the status panel, the available context sections, a
  deterministic interpretation, and a deterministic next action (the evening Bell
  on a normal night) — so a well-formed, useful message still lands. Only the
  model's warmth is lost; the send is never skipped, and on a missed day the
  verdict is still appended byte-for-byte.
- **Malformed model reply** (missing slot delimiters, no usable text): the same
  deterministic-scaffold fallback fires. A reply that has prose but no
  `%%INTERPRETATION%%` delimiter is **not** a failure — the whole reply becomes
  the interpretation slot (see [the two model slots](#the-two-model-slots)), and
  the scaffold renders normally.
- **Routine file unreadable** (a `morning_routine` / `night_routine` path set but
  missing or unreadable): non-fatal — the routine grounding is dropped from that
  message and the miss is recorded, never a failed send. Enrichment never kills
  the life-critical daily message; the prompt, verdict, and live-number reads stay
  loud.
- **Host asleep or off at the fire time** (bounded catch-up): on wake the missed
  window fires late, prefixed with a `(late — host was asleep)` note — but only
  within a window. A morning rides until `10:00`, a night until `22:00` (local).
  Past the cut-off the send is **skipped and you are alerted** instead of posting a
  confusing hours-stale "morning" at noon. A fire within a short grace of its mark
  is treated as on-time and carries no late note.
- **Double-fire on a retry** (idempotency): each delivery writes a per-day,
  per-window receipt through the binary. If a retry finds a receipt whose message
  still reads back in the channel, it skips rather than double-posts.
- **Read-back verify**: a delivery is not considered done until the message id
  returned by the send is confirmed present in the channel — a real message that
  actually reappears, not a fire-and-forget POST.
- **Total miss** (loud alert): a compose failure, a delivery failure, a failed
  read-back, or a past-cut-off skip fires a best-effort alert to the user channel.
  If even that channel is unreachable, the job returns a loud error that fails the
  scheduled job and lands in the supervised daemon log. Silence is the one outcome
  the companion never produces.

## How it coexists with the Engine teeth

When the companion is enabled it becomes the single user-facing sender for its
window. The Engine still runs its **full modeless decision** every day — it
evaluates the tripwire, persists escalation state, and fires the witness-channel
escalation, all deterministic and untouched. Only
its *user-channel* send is suppressed, because the companion presents that line
(appending the verdict verbatim on a missed day). The dead-man decision stays
modeless; the companion only dresses its output. Disable the companion and the
teeth send for themselves again, byte-for-byte as before.

## Operational notes

- The companion runs its own small, disposable job database, separate from the
  teeth's and from the `~/.lucid` Ledger. It defaults to a path under your user
  config directory; set `LUCID_COMPANION_DB` to override it. It holds only
  scheduling machinery — no Ledger truth — so it is safe to delete; it is rebuilt
  on the next run.
- The delivery receipts that guard idempotency are written **only through the
  binary**. Never hand-edit them.

## Checking readiness (`lucid scheduler status`)

Everything on this page — whether the companion is enabled, which provider and
prompt files it will compose from, the two fire marks it inherits, the periodics
that drive each window, and the last receipt per window — is reported in one
read-only command. Run it before the morning (`06:00`) and night (`19:00`) windows
to confirm the next send will fire, and after them to confirm it did:

```sh
lucid scheduler status          # calm human summary + a one-word verdict
lucid scheduler status --json   # the same, machine-readable, for a cron or agent
```

It **sends nothing, renews no secret, and reads no prompt body** (existence only) —
so it is safe to run any time. It rolls every check into one verdict with a 3-tier
exit code, identical in text and `--json`, so a health cron can gate on the exit
alone:

- **`0` ok** — healthy; the next send will fire.
- **`1` warn** — benign but worth a glance: the companion is disabled, or the last
  receipt is unverified.
- **`2` error** — a real problem the routine depends on: a missing job store, a
  missing prompt file, an inactive required periodic, a **missed** already-elapsed
  window (no receipt or only a stale one), or the daemon down / not supervised.

**Best-effort host checks.** The daemon-process and supervisor checks run by default
but never claim false confidence: on a platform where they cannot be inspected they
report `unknown` (never `ok`), and an `unknown` never lowers the verdict — only a
positively detected problem does. So the command is useful on any host and only goes
red when something is actually wrong.

The full flag, exit-code, and verdict-threshold tables live in the
[command reference](commands.md#scheduler-status) under `scheduler status`.

## See also

- [`commands.md`](commands.md) — the full `lucid` command reference, including
  `companion fire`, `scheduler run`, and `scheduler status`.
- [`../engine.md`](../engine.md) — the accountability teeth (bell, tripwire,
  escalation) and the chain marks the companion inherits.
- [`../architecture.md`](../architecture.md) — the Mirror/Engine split and the
  purity boundary the companion sits beside.
- [`../adr/0006-model-access.md`](../adr/0006-model-access.md) — the provider
  seam the companion composes through.
- [`../mvp/data-model.md`](../mvp/data-model.md) — the `lucid.json` schema,
  including the `companion` block.
