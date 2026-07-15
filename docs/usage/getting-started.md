# Getting started

You've [installed Lucid](install.md). This page gets you running the daily loop
and reading your own record back. Every example here is synthetic — swap in your
own words and numbers.

## The mental model in four sentences

Lucid is two halves over one append-only Ledger at `~/.lucid/`.

- **The Mirror** captures what you write, structures it, and — only with your
  say-so — proposes at most one tentative pattern per session. It *understands*;
  it never pushes, never diagnoses, and speaks in hypotheses ("does this fit?"),
  never verdicts.
- **The Engine** defends one committed daily practice: it rings a bell, records
  whether you showed up, and escalates honestly when you don't. It has real
  accountability but *never reads a word of your content*.

The line between them is the whole design (the "Sanctuary," architecture P3):
the half that understands you never pushes; the half that pushes you never
reads. Both write the same user-owned Ledger, and nothing you write is ever
scored.

## The daily rhythm

The loop is built around a **morning** touch and an **evening** close. You can
run every step by hand; a scheduler (see [install.md](install.md#optional-integrations))
just automates the two prompts.

### Evening — the bell and the close-out

1. **Declare the day's mode** (it locks at the bell, so do it before):

   ```sh
   lucid mode green      # green = full · yellow = reduced · red = floor-only
   ```

2. **The bell rings** (default ~19:00) — a fixed prompt, no sign-off. It's the
   external trigger; you never have to start from memory or motivation.

3. **Close out** — one deterministic, agent-free command that records the
   Engine's day *and* writes your journal line into the Mirror's `raw/` in a
   single act (this is the join at the heart of Lucid):

   ```sh
   lucid closeout dfx 3/wrist steady session, wrist held me back a little
   ```

   The compact form is `<status-chars> <capacity>[/<limiter>] <journal line>`:
   - `dfx` — one character per link in your chain, in order: **d**one, at
     **f**loor, or **x** skipped. (`dfx` describes a three-link chain: link 1
     done, link 2 at its floor, link 3 skipped.)
   - `3/wrist` — a capacity digit **1–5** (1 depleted → 5 resourced) with an
     optional one-word limiter tag.
   - the rest is your one journal line.

   Missed the whole night? Record it honestly — no makeup work is ever owed:

   ```sh
   lucid closeout skip
   ```

   Forgot to record a night the chain actually ran? Backfill within the 7-day
   window:

   ```sh
   lucid closeout backfill yesterday dfx 4 got it done, just late to log it
   ```

   Your chain lives in `~/.lucid/engine/chain.json`. A fresh Ledger ships a
   sensible default — a three-link night chain (a journal line, phone on the
   charger, a page in bed) with the bell at 19:00 and the tripwire at 06:00 — so
   `closeout` works immediately. Edit that file to make it yours: your links,
   their floors, and the clock profile. See
   [`../mvp/engine-module.md`](../mvp/engine-module.md). On your worst day the
   floor is the ask, and there is nothing beneath it.

### Overnight — the rollover

Each logical day ends at a rollover boundary (default **04:00**), so a
close-out just after midnight still belongs to the night that earned it. Events
before the boundary attribute to the previous day.

### Morning — the tripwire

The morning job (default ~06:00) reads what the Engine recorded and does exactly
one of:

- **Completed last night** → nothing but a quiet reset.
- **One miss** → an **L1 nudge**, private, naming tonight's floor explicitly
  ("never miss twice — tonight is a must").
- **Two consecutive misses** → an **L2** message to your witness — carrying
  *only* streak/mode/storm state, never a word of what you wrote. It fires on
  the *absence* of a completion (a dead-man switch), so silence can't hide a
  miss.

Check the ambient state any time:

```sh
lucid status              # streak, adherence vs mode, budget burn, days to gate
lucid day yesterday       # the joined record for one day
```

### Throughout the day — capture

Capture is friction-free and always available:

```sh
lucid log "shower thought: the pressure drop might be what wrecks my knee"
lucid obs pain 6 knee aching after the run
lucid obs mood 4 restless
lucid obs where Lisbon    # sticky location — enable context.location first
```

`log` writes one immutable raw entry. `obs` writes a structured health/context
micro-log (a digit you'll actually type nightly beats a paragraph you won't).
Both are inventory — never a streak, a score, or a target. Only `pain`, `intake`
(`ate`/`drank`), `elimination` (`bm`), and `mood` are enabled out of the box;
other kinds — `where` (location), `symptom`, `med`, … — are opt-in, added to
`observations/config.json` first.

### Weekly — recall (chat/harness surface)

Through a chat harness with the Lucid skill installed, weekly recall closes the
loop:

- `/reflect` surfaces validated insights from the past week and asks whether any
  still resonate — it never proposes anything new.
- `/ask <question>` answers from your validated insights and reflections only,
  with citations — surfaces, not new patterns.
- `/checkin` runs a short guided capture (2–4 follow-up questions) and may offer
  one tentative pattern through the resonance gate.

These agentic verbs have no bare-CLI equivalent; see
[`commands.md`](commands.md#chatharness-slash-commands).

## A synthetic first week

```sh
# Day 0 — set up
lucid init
# (optional) make the chain yours in ~/.lucid/engine/chain.json —
# a default 3-link night chain (bell 19:00, tripwire 06:00) ships ready to run

# Each evening
lucid mode green
lucid closeout dfx 3/wrist did the session, felt strong

# A hard night
lucid mode red
lucid closeout ffx 2 low energy, floored it but showed up

# A missed night — honest zero
lucid closeout skip

# Any morning
lucid status
lucid day yesterday

# Anytime capture
lucid log "kept the promise even though I didn't want to"
lucid obs pain 5 lower back after sitting all day

# Weekly (through the harness)
/reflect
/ask what have I noticed about my knee and the weather?
```

## Configuration

Lucid keeps its settings in small, hand-editable files inside the Ledger. Edit
them with any text editor; out-of-range values are clipped on the next run with
a one-line warning rather than failing.

| File | Governs |
|------|---------|
| `~/.lucid/lucid.json` | Mirror-thread config: directory names, `recent_window` (default 7, max 14), the intake/ask caps, `proposal_pause` (default: 3 unanswered → 14-day pause), `person_dominance_threshold`, agent versions, `bootstrap_mode`. |
| `~/.lucid/engine/chain.json` | The Engine chain — links, floors, modes — and the clock **profiles** (bell time, tripwire hour, rollover boundary). Definitions change at a weekly Retro, never mid-day. |
| `~/.lucid/observations/config.json` | Which observation kinds are enabled (default: `pain`, `intake`, `elimination`, `mood` — the rest are off until you add them), the curiosity budget, opt-in enrichers, and the clinician-packet context lines. |

Full schemas: [`../mvp/data-model.md`](../mvp/data-model.md),
[`../mvp/engine-module.md`](../mvp/engine-module.md),
[`../observations.md`](../observations.md).

## Data & privacy

Everything lives under `~/.lucid/`, owner-only, outside any repo and outside any
cloud. Two kinds of data live there:

- **Primary data — the backup set** (exists nowhere else, keep it forever):
  `raw/`, `observations/`, `registries/`, `engine/` (except `status.json`), and
  `projections/exports.log`.
- **Rebuildable** (can be regenerated if the agents or scripts improve, so it's
  optional to back up): `processed/`, `insights/`, `reflections/`,
  `engine/status.json`, and the rest of `projections/`.

A helper script lives at [`../../scripts/backup.sh`](../../scripts/backup.sh).

Two privacy notes worth internalizing:

- **The Sanctuary holds by construction.** No agent ever reads `engine/`,
  `observations/`, or `registries/`; the Engine and observation writes invoke no
  model at all. Your witness sees one thing — the L2 escalation template — with
  zero journal or capacity content.
- **A chat harness adds transit exposure.** If you drive Lucid through a chat
  provider, messages in transit (journal lines, check-in answers, health
  micro-logs) pass through that provider's servers even though the system of
  record stays local. Read the tradeoff and mitigations in
  [`../mvp/local-runtime.md`](../mvp/local-runtime.md#the-privacy-boundary-of-a-chat-surface-read-this-before-choosing)
  before enabling sensitive observation kinds. The CLI has no such exposure.

Only narrow, named slices of your Ledger are ever sent to an LLM for the agentic
Mirror verbs — never your whole history.

## Verify the boundary any time

```sh
lucid validate            # read-only sweep: public-boundary, diagnostic-language,
                          # sanctuary, doc-link, and Ledger-schema checks
```

`validate` writes nothing (it won't even scaffold the Ledger) and exits non-zero
if any error-severity check fails — safe to run in CI or on a whim.

## What's specified but not yet built

Lucid's docs describe more than the current build ships. These are **post-MVP by
design** (see [`../mvp/scope.md`](../mvp/scope.md) §7), not missing pieces:

- **Frameworks** — interpretation lenses (Stoicism, NVC, IFS, …). The spec and
  definition files exist; the loader, the `/lens` surface, and framework
  licensing do not ship yet. The record stays lens-neutral.
- **The Scientist / protocols** — pre-registered self-experiments. Specified;
  no runtime tooling yet (a manual practice at Retro/Gate cadence for now).
- **The interactive calibration wizard** — today `lucid init` scaffolds the
  Ledger; the guided first-run Charter/calibration flow in
  [`../calibration.md`](../calibration.md) is a human-filled template, not yet a
  wizard.

## Next

The full command reference — every CLI verb, sub-form, flag, and the chat
slash commands — is in [`commands.md`](commands.md).
