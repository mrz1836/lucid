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
  "model": ""
}
```

| Key | Type | Meaning |
|-----|------|---------|
| `enabled` | bool | Gates the whole feature. Default `false` → the scheduler runs only the pure Engine teeth. |
| `morning_template` | path | The morning-window template — an opaque prompt file, passed to the model as the user message. |
| `night_template` | path | The night-window template — same, for the night window. |
| `system_prompt` | path | The system prompt, passed to the model as the `System` role on every compose. |
| `model` | string | Optional. Overrides `provider.model` for the companion's compose call; empty inherits the provider default. |

When `enabled` is `true`, all three paths must be non-empty or the config is
rejected at load.

**The three paths are the whole personal seam.** Lucid opens exactly those files
and never walks the directory holding them (no dir-walk, no filename convention),
so you can keep your prompt files anywhere — inside or outside the repo — and
Lucid only ever reads the three files you named. Each file is opaque, self-
contained prompt text: Lucid does not parse it, follow links in it, or require any
structure. Neutral, copy-me examples live in
[`companion-examples/`](companion-examples): `system.md`, `morning.md`, and
`night.md`.

**Credential-dumb, like the rest of `lucid.json`.** No channel id and no token
live in the config — the target channel and the harness token stay env-only
(`LUCID_USER_CHANNEL_ID` / `LUCID_HARNESS_TOKEN`), and no model API key lives here
or anywhere in `lucid.json` (auth is the vendor CLI's or the local daemon's). Full
schema notes: [`../mvp/data-model.md`](../mvp/data-model.md) §"lucid.json".

## How a message is composed

For each window the compose worker:

1. Reads the two configured opaque files for the window — the `system_prompt` and
   the per-mode template — on their explicit paths.
2. Reads the chain's **honest live numbers** in-process, from the exact projection
   `lucid metrics --json` and `lucid status --json` expose. Numbers are copied
   straight from the chain and ramp-framed — a young chain reads as "building", a
   streak reads as its real tally — never rounded or invented.
3. Sends the system prompt, your template, and the live-numbers block to the model
   (default backend `claude_cli`, model `opus`; see the provider config) and takes
   the reply as the message.

On a **missed day** the companion composes the warm message and then appends the
Engine's own deterministic verdict line **byte-for-byte** below it. The model
writes the warmth; it never touches the verdict, so a model can never reword
"you missed". The witness-channel escalation and the monthly heartbeat still fire
modeless and separate — the companion only presents the user-channel line.

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

- **Provider unreachable** (timeout / unavailable): the worker falls back to the
  Engine's own deterministic template so a message still lands — the evening bell
  plus the honest numbers on a normal night, the honest numbers alone on a normal
  morning, or the verdict line on a missed day. Only the model's warmth is lost;
  the send is never skipped, and there is no separate fallback template to
  maintain.
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
evaluates the tripwire, persists escalation state, fires the witness-channel
escalation, and sends the monthly heartbeat, all deterministic and untouched. Only
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

## See also

- [`commands.md`](commands.md) — the full `lucid` command reference, including
  `companion fire` and `scheduler run`.
- [`../engine.md`](../engine.md) — the accountability teeth (bell, tripwire,
  escalation) and the chain marks the companion inherits.
- [`../architecture.md`](../architecture.md) — the Mirror/Engine split and the
  purity boundary the companion sits beside.
- [`../adr/0006-model-access.md`](../adr/0006-model-access.md) — the provider
  seam the companion composes through.
- [`../mvp/data-model.md`](../mvp/data-model.md) — the `lucid.json` schema,
  including the `companion` block.
