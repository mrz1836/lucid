# Lucid MVP — Steel Thread

This page specifies the **single end-to-end user loop** the MVP proves. It
is the implementation contract for the steel-thread overview in
[`README.md`](README.md), constrained by the principles in
[`product-principles.md`](product-principles.md).

There is one loop. Other capture modes, reflection cadences, and agent
roles in [`vision.md`](../../vision.md) are deferred until this loop works
end-to-end for one person on one host.

## The loop

```
  user   ──► /checkin or /log   ──► Intake (2–4 questions if needed)
                                          │
                                          ▼
                              raw entry written to ~/.lucid/raw/
                                          │
                                          ▼
                              Structuring extracts emotions / themes /
                              people; writes ~/.lucid/processed/<id>.json
                                          │
                                          ▼
                              Reflection proposes ONE possible pattern
                                          │
                                          ▼
                              "Does this resonate?"
                                          │
              ┌───────────────────────────┼───────────────────────────┐
              ▼                           ▼                           ▼
        accepted                    nuanced                      rejected
              │                           │                           │
              └───────────────┬───────────┘                           │
                              ▼                                       ▼
                  insight written to              rejection recorded against
                  ~/.lucid/insights/              the processed artifact, no
                  with provenance                 insight created
                              │
                              ▼
                  /reflect (weekly): list validated insights from
                  the past week and ask "still resonating?"
```

The diagram in [`README.md`](README.md) is the "elevator pitch" version of
this same loop; this file is the implementation-grade version with all
three branches.

## Stages in detail

### Stage 1 — Capture

**Trigger:** the user runs `/checkin` or `/log` inside a Discord thread (or
another chat surface routed through OpenClaw/Hermes — see
[`local-runtime.md`](local-runtime.md)).

**`/log`** is the single-shot capture:

* Input: one free-form message.
* Output: an immutable raw entry under `~/.lucid/raw/`, plus a short
  acknowledgement to the user with the entry id.
* Intake does **not** ask follow-up questions for `/log`. The whole point
  is friction-free capture.

**`/checkin`** is the guided capture:

* Input: an optional opening message; the command alone is also valid.
* Behavior: Intake asks **2–4 follow-up questions** to fill in obvious
  blanks (what happened, how it felt, what it touches). Two questions is
  the default; four is the cap.
* Output: a single raw entry that bundles the user's answers in order,
  with a small frontmatter block recording which questions were asked.

**Hard rules at this stage:**

* Capture never blocks on structuring. The acknowledgement returns as
  soon as the raw entry is written.
* Raw entries are immutable. If the user wants to add detail, they create
  a new entry that references the original by id.
* The raw layer is the ground truth. Processed and insight files are
  rebuildable; raw files are not.

### Stage 2 — Structure

**Trigger:** a new raw entry exists.

**Behavior:** the Structuring agent reads the raw entry and writes a
processed artifact at `~/.lucid/processed/<entry-id>.json` containing:

* `entry_id` — the raw entry id.
* `emotions` — short list of emotions named or implied (each with a
  short rationale string).
* `themes` — short list of recurring or newly noticed themes.
* `people` — names mentioned, plus a "first mention?" flag per name.
* `notes` — optional free-text notes the agent wants to preserve.
* `agent_version` — the prompt/version that produced this artifact.

**Hard rules:**

* Structuring is **idempotent**. Running it again on the same raw entry
  may overwrite the processed artifact but never the raw entry.
* Structuring is **strictly extractive** for the MVP. It does not
  generalize across entries, propose patterns, or build a profile. That
  is Reflection's job.
* If extraction fails or returns nothing useful, the artifact still
  exists with empty arrays plus a `notes` value explaining the gap.

### Stage 3 — Reflection (one possible pattern)

**Trigger:** the processed artifact for an entry exists.

**Behavior:** the Reflection agent is given the new processed artifact
and a small window of recent processed artifacts (the MVP recommends the
last 7 entries — see [`agent-contracts.md`](agent-contracts.md) for the
exact contract). It returns one of three outcomes:

1. **One proposed pattern.** A short hypothesis statement, framed in the
   voice from `product-principles.md`, with a reference to the supporting
   entry ids.
2. **No pattern yet.** An honest "I don't have enough to say anything
   useful yet" message.
3. **Soft contradiction.** A gentle observation that the latest entry
   sits oddly next to a previous one, framed as a question. (Optional in
   the MVP; only used when the contradiction is close to the surface.)

**Hard rules:**

* Reflection proposes **at most one** pattern per session. Multiple
  competing patterns are explicitly out of scope for the MVP — that path
  becomes a menu, not a steel thread.
* Reflection always asks **"Does this resonate?"** at the end of a
  proposal. There is no path that quietly stores a pattern as fact.
* Reflection never uses diagnostic language. See
  [`product-principles.md`](product-principles.md) §6 for the phrase list.

### Stage 4 — Validation

The user responds to "Does this resonate?" with one of three outcomes.
All three are first-class.

**Accepted** — the user confirms the pattern fits.

* An insight record is written to `~/.lucid/insights/<insight-id>.md` with:
  * The pattern statement, verbatim.
  * Provenance: source raw entry ids, processed artifact id, Reflection
    prompt/version, user's accept message.
  * Status: `accepted`.
* The user is told the insight has been saved and will surface in the
  next weekly recall.

**Nuanced** — the user partially agrees and offers a refinement.

* The user's refinement becomes the canonical statement.
* Provenance still references the original Reflection output for
  traceability.
* Status: `accepted` with a `nuanced_from_proposal: true` flag.

**Rejected** — the user says the pattern does not fit.

* No insight record is created.
* The processed artifact gains a `rejected_proposals` array entry: the
  proposal text, the user's reason if given, the prompt/version. This
  is so future Reflection runs can avoid re-proposing the same shape.

**Hard rules:**

* The user can always exit without answering. In that case, no insight
  is created and no rejection is recorded — the proposal is simply
  considered unanswered.
* The system never re-prompts inside the same session. If the user goes
  quiet, Reflection holds.

### Stage 5 — Weekly recall

**Trigger:** the user runs `/reflect` (initially manual; a local cron
hook is documented in [`local-runtime.md`](local-runtime.md) but not
required for the MVP).

**Behavior:**

* Lists validated insights from the past week, in the voice from
  `product-principles.md`.
* For each insight, asks: *"Still resonating? Anything change?"*
* User responses can: confirm, soften, retire, or split an insight. Each
  is a separate insight record update with provenance, never a silent
  rewrite.
* If there are no validated insights from the past week, `/reflect`
  surfaces the most recent two regardless of age, then offers a `/log`
  prompt.

**Hard rules:**

* `/reflect` is read-and-ask, not generate-new. It does not propose new
  patterns. New proposals come from Reflection on fresh entries, not
  from the recall command.
* `/reflect` never sends a notification, only responds when invoked.

## Three flow paths

### Happy path — accepted pattern

1. User runs `/checkin` after a meeting that went sideways.
2. Intake asks two questions to anchor what happened and how it felt.
3. Raw entry is written.
4. Structuring extracts `emotions: [frustrated, dismissed]`,
   `themes: [voice-not-heard, work]`, `people: [Coworker A]` (first
   mention).
5. Reflection looks at the last seven entries and notices "voice not
   heard" appears twice this week. It proposes one possible pattern and
   asks "Does this resonate?".
6. User says yes.
7. Insight `i_2026_05_05_a` is written with provenance.
8. On Friday, `/reflect` surfaces the insight and asks if it still
   fits. User confirms. Insight metadata gets a `last_confirmed_at`
   stamp.

### Rejected-insight path

1. User runs `/log` after a tough conversation with a sibling.
2. Raw entry is written.
3. Structuring extracts `emotions: [hurt, defensive]`,
   `themes: [family]`, `people: [Sibling B]`.
4. Reflection proposes: "I noticed family conversations have come up
   three times this month with hurt nearby. One possible pattern:
   defaulting to defensiveness when family is the topic. Does this
   resonate?"
5. User responds: "No — I'm not defensive in general. This one was
   different because I was tired."
6. No insight record is created. The processed artifact records the
   rejected proposal and the user's reason.
7. Future Reflection runs see the rejection and avoid the same framing.

### No-pattern-yet path

1. User runs `/log`: "Quiet day. Read for an hour. Felt fine."
2. Raw entry is written.
3. Structuring extracts `emotions: [calm]`, `themes: [rest]`,
   `people: []`.
4. Reflection looks at recent entries, finds nothing strong enough to
   propose. It returns: *"I don't have enough yet to say anything useful
   — want to keep going?"*
5. No insight is created and no rejection is recorded. The processed
   artifact stands alone.
6. Friday's `/reflect` does not include this entry; nothing to recall.

## Synthetic transcript (Lucid voice)

The transcript below is fictional. It exists to give a future agent
prompt a concrete model of Lucid's voice, the question pacing, and the
hypothesis framing. No real person, conversation, or biographical detail
is encoded.

```
user:    /checkin
Lucid:   Hi. Anything in particular on your mind, or want me to start
         with the usual?

user:    The dinner with M. and J. went sideways again.

Lucid:   Got it — I'll keep this short. Two questions.

         1) What part of it stuck with you afterward?

user:    The bit where I tried to push back on the plan and then dropped
         it. I just kind of agreed.

Lucid:   2) And how did that land for you, after?

user:    Annoyed. A little embarrassed. Not at them, more at myself for
         folding.

Lucid:   Thanks — saved as raw_2026_05_05_19_42 (just so you have it).

         I noticed something across the last week or so, and I want to
         check it with you rather than file it as fact.

         One possible pattern: when a group decision is in motion, you
         tend to test a different idea, then back off if it isn't
         immediately picked up. The annoyance afterward seems to point at
         "I had something to say and folded" rather than at the group.

         Does this resonate, or is it off?

user:    Mostly yes. I'd say it's not always groups — it's more when
         M. is in the room.

Lucid:   That's a real refinement, thank you. I'll save it as your
         version: "When M. is in the room and a group decision is in
         motion, I tend to test an idea once and back off if it isn't
         picked up immediately."

         Want me to surface it on Friday so we can see if it still
         fits?

user:    Yeah.

Lucid:   Saved. Anything else for tonight, or are we good here?
```

Notes on what the transcript is doing:

* Two follow-up questions, not four. Intake stops as soon as it has
  enough.
* Reflection proposes **one** possible pattern, not three.
* The pattern is framed as a hypothesis, not a label.
* The user's nuance becomes the canonical insight, with provenance.
* The closing line is calm and explicit, not performative.

## What this loop intentionally is not

* **Not a daily/weekly/monthly/yearly cascade.** The MVP has one
  reflection cadence: weekly recall via `/reflect`. The full multi-scale
  reflection in `vision.md` is a follow-on.
* **Not a multi-pattern proposal engine.** One pattern per session is a
  hard cap.
* **Not an autonomous notifier.** `/reflect` is invoked by the user. No
  push, no scheduled DM, no proactive outreach in the MVP.
* **Not a goals or coaching surface.** The Coach role from `vision.md`
  is deferred entirely.
* **Not a draft-and-send surface.** The Agent-Self role is deferred.
  Lucid never composes external messages in the MVP.
* **Not a profile editor.** Validated insights accumulate as files; the
  MVP does not expose a "psychological profile" view. That is a
  follow-on once enough validated insights exist.
