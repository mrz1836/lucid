# Lucid MVP — Steel Thread

This page specifies the **single end-to-end user loop** the MVP proves. It
is the implementation contract for the steel-thread overview in
[`README.md`](README.md), constrained by the principles in
[`product-principles.md`](product-principles.md).

There is one loop. Other capture modes, reflection cadences, and agent
roles in [`vision.md`](../vision.md) are deferred until this loop works
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
                              (inside /checkin only — see Stage 3)
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
   sits oddly next to a previous one, framed as a question. Used only
   when the contradiction is close to the surface; if it is not, the
   honest output is `no_pattern`. The message must reference exactly
   two supporting entry ids and must end in a question mark
   ([`agent-contracts.md`](agent-contracts.md) §3 validation rules).

**Hard rules:**

* Reflection proposes **at most one** pattern per session. Multiple
  competing patterns are explicitly out of scope for the MVP — that path
  becomes a menu, not a steel thread.
* Reflection proposes **only inside `/checkin` sessions**. Entries
  captured via `/log` or the `/closeout` journal line are structured
  asynchronously and enter the recent window at the next `/checkin` —
  the proposal arrives there, never inline
  ([`agent-contracts.md`](agent-contracts.md) §3).
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

**The rule prompt** (accepted and nuanced paths only). After the
insight is saved, the router asks the fixed prompt, once per insight,
ever: *"Want to attach a rule — one line, what you'll try? Skipping is
fine."* An answer is recorded verbatim as the insight's `rule`
([`data-model.md`](data-model.md) §"Validated insights"); a skip or
silence leaves `rule: null` and the prompt never returns for that
insight. A rule is testimony, not a goal: nothing tracks it, nothing
scores it, nothing reminds about it — it resurfaces only at recall,
where *kept* and *lapsed* are both first-class, judgment-free answers.
A rule that deserves teeth becomes an Engine commitment through a
Gate; the Mirror never grows any.

**Rejected** — the user says the pattern does not fit.

* No insight record is created.
* The processed artifact gains a `rejected_proposals` array entry: the
  proposal text, the user's reason if given, the prompt/version. This
  is so future Reflection runs can avoid re-proposing the same shape.

**Hard rules:**

* The user can always exit without answering. No insight is created
  and no rejection is recorded — instead the processed artifact gains
  an `unanswered_proposals[]` entry (`{shape_tag, proposed_at}`), kept
  separate from `rejected_proposals[]` because silence is not
  rejection. The shape is suppressed for the near window with the same
  mechanics as a rejected one, and it ages out identically
  ([`agent-contracts.md`](agent-contracts.md) §3).
* After three consecutive unanswered proposals (counted across
  sessions), the router pauses proposals for 14 days: `/checkin` still
  captures and structures, but `reflection.propose` is not invoked.
  The pause is silent — no copy ever mentions it. Any answered
  proposal (accepted, nuanced, or rejected) resets the counter.
* The system never re-prompts inside the same session. If the user goes
  quiet, Reflection holds.

### Stage 5 — Weekly recall

**Trigger:** the user runs `/reflect` (initially manual; a local cron
hook is documented in [`local-runtime.md`](local-runtime.md) but not
required for the MVP).

**Behavior:**

* Lists validated insights from the past week, in the voice from
  `product-principles.md`.
* For each insight, asks: *"Still resonating? Anything change?"* For
  insights carrying a rule, the question includes it: *"You attached:
  '<rule>'. Still standing — kept, lapsed, or retire it?"* Kept and
  lapsed are both first-class; a lapse is data, never a scolding.
* User responses can: confirm, soften, retire, or split an insight —
  and, for ruled insights, mark the rule kept, lapsed, or retired. Each
  is a separate insight record update with provenance, never a silent
  rewrite.
* If there are no validated insights from the past week, `/reflect`
  surfaces the most recent two regardless of age, then offers a `/log`
  prompt. If entries have also accumulated since the last `/checkin`
  without a Reflection pass, the router appends one fixed pointer
  line: *"There are entries since your last check-in — /checkin when
  you want to look for a pattern together."* This is recall-side
  router copy, not a proposal.

**The gate variant.** `/reflect gate`, run by the user at gate or
quarterly cadence, is the same read-and-ask pass over **every**
accepted insight (capped at 50) instead of the past week — the place
accepted insights get consumed instead of merely stored. After the
recall pass the router appends the deterministic **panel numbers** as
fixed copy: insights accepted this window, rules stated, rules
standing — plus, when one person's share of window entries crosses the
dominance threshold ([`data-model.md`](data-model.md)
`person_dominance_threshold`), a single hypothesis-framed line:
*"<name> appears in N% of entries this window, up from M% — worth a
look, or expected?"* These numbers exist at gate cadence only: never
on `/status`, never daily, never witness-visible
([`agent-contracts.md`](agent-contracts.md) §3 "Gate recall").

**Hard rules:**

* `/reflect` is read-and-ask, not generate-new. It does not propose new
  patterns. New proposals come from Reflection on fresh entries, not
  from the recall command. The gate variant included.
* `/reflect` never sends a notification, only responds when invoked.
* Panel numbers and dominance lines are router-computed, deterministic,
  and hypothesis-framed — data the user pulled, never a verdict.

### Stage 6 — Ask (grounded Q&A)

**Trigger:** the user runs `/ask <question>` in any thread.

**Behavior:** Reflection's `answer_grounded` sub-mode answers from
**only** the validated insights and weekly reflection records the
router supplies — never raw entries, never new patterns, never
general knowledge. The answer cites the records it draws from, and
when the slice cannot answer the question, the system says so.

* The router reads `~/.lucid/insights/` (status `accepted`, capped
  at the 50 most recent by last accept/confirm) and
  `~/.lucid/reflections/` (capped at the 12 most recent ISO-week
  records). These two slices are the only material the agent sees.
* The agent returns one of two outcomes:
  * **`answer`** — a short, grounded response with a `citations[]`
    list of insight and reflection ids. Every cited id is in the
    supplied slice.
  * **`insufficient`** — an honest "I don't have enough validated
    material to answer that yet — want to capture one?" response.
* `/ask` writes nothing. It does not create insights, does not update
  status, does not append to reflections.
* Safety/Consent gates the answer the same way it gates every other
  outbound message; advice/recommendation phrasing is blocked
  (see [`agent-contracts.md`](agent-contracts.md) §4).

**Hard rules:**

* `/ask` is **read-only**. It never writes any record under
  `~/.lucid/`.
* `/ask` never proposes new patterns. New patterns come from
  `/checkin`, period.
* `/ask` never reads raw entries or processed artifacts. The
  validated insights and weekly reflections are the user's curated,
  signed-off material — those are the only sources it speaks from.
* `/ask` never gives advice, recommendations, or therapeutic framing.
  If the user wants action, they ask `/checkin`; if they want a
  pattern check, they validate one. `/ask` is a mirror, not a coach.

**Synthetic transcript:**

```
user:    /ask what have I learned about how I act in groups?

Lucid:   From what you've validated:

         - You've saved one insight (i_2026_05_05_a) noting that when
           M. is in the room and a group decision is in motion, you
           tend to test an idea once and back off if it isn't picked
           up immediately.
         - In the most recent weekly reflection (reflection_2026_w18)
           you confirmed that insight still fits.

         I don't have anything else validated about groups yet — that
         is the only one. Want to capture more?
```

Notes: the answer cites two ids only, both from the supplied slice
(one insight, one weekly reflection). The agent does not generalize,
does not advise, does not propose a new pattern, and is honest about
the bounds of what it knows. If the slice had been empty, the
response would have been: *"I don't have anything validated yet —
try `/checkin` or `/log` first."*

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
4. At the user's next `/checkin`, the artifact sits in Reflection's
   recent window. Reflection proposes: "I noticed family conversations
   have come up three times this month with hurt nearby. One possible
   pattern: defaulting to defensiveness when family is the topic. Does
   this resonate?"
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
4. At the next `/checkin`, Reflection looks at recent entries, finds
   nothing strong enough to propose. It returns: *"I don't have enough
   yet to say anything useful — want to keep going?"*
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

## Voice across paths

The canonical transcript above shows the happy path. The snippets
below give prompt designers concrete voice anchors for the other
paths — rejected proposal, soft contradiction,
`max_questions_reached`, Safety/Consent block fallback, and the
empty-`/reflect` case. All synthetic.

### Rejected proposal

```
Lucid:   I noticed family conversations have come up three times this
         month with hurt nearby. One possible pattern: defaulting to
         defensiveness when family is the topic. Does this resonate,
         or is it off?

user:    No — I'm not defensive in general. This one was different
         because I was tired.

Lucid:   Got it — I'll set this one aside, not file it as fact. I'll
         remember the reason ("tired, not defensive") so I don't
         re-propose the same shape.
```

The system does not re-argue the proposal, does not apologize
performatively, and explicitly names the rejection rationale so the
user knows it landed. The `shape_tag` for this proposal is appended
to `rejected_proposals[]` on the processed artifact (see
[`data-model.md`](data-model.md) §"Insight provenance and rejected
proposals").

### Soft contradiction

```
Lucid:   Earlier this week (raw_2026_05_03_21_10) you wrote that
         pushing back felt energizing. Today (raw_2026_05_05_19_42)
         it reads more like folding left you annoyed at yourself.
         I don't have a pattern, just a gap — want to look at it?
```

Two supporting entry ids cited explicitly. The message ends in a
question; the system does not pick a side. If the user does not
engage, no insight is written and the gap is not re-surfaced unless
fresh evidence reopens it.

### `max_questions_reached`

```
user:    /checkin

Lucid:   Hi. Anything in particular on your mind, or want me to start
         with the usual?

user:    Just a hard day.

Lucid:   1) What part of it stuck with you?

user:    Tired.

Lucid:   2) And how did that land for you, after?

user:    Numb.

Lucid:   3) Anything specific that pulled at you, or more diffuse?

user:    Diffuse.

Lucid:   4) One last — anything you want to mark for later, or is the
         tiredness the headline?

user:    Headline.

Lucid:   I've got what I need — saved as raw_2026_05_05_22_18.
```

Four short answers, then the cap. The router's ack copy is fixed
("I've got what I need — saved as `raw_<id>`") per
[`agent-contracts.md`](agent-contracts.md) §1. Reflection still
runs against the bundle in the next stage; this snippet only covers
the Intake portion.

### Safety/Consent block (router fallback)

If Reflection produced an output that violated the phrase blocklist
or attempted an external action, Safety/Consent blocks it and the
router substitutes a short fallback. The user sees this rather than
the unsafe content:

```
Lucid:   I held that response — let me ask differently.

         Looking at the last few entries, I'm not sure I have a clean
         pattern yet. Want to keep going, or leave it here?
```

The fallback is honest about what happened ("I held that response"),
gives the user a way out, and never explains the underlying rule.
Safety/Consent's `notes` field carries the justification but is not
surfaced to the user (see
[`agent-contracts.md`](agent-contracts.md) §4).

### `/reflect` with no validated insights from the past week

```
user:    /reflect

Lucid:   Quiet week — nothing landed as a validated insight in the
         last seven days. Want me to surface the two most recent ones
         from before that, just to check in on them? Or would you
         rather drop a /log first and come back to /reflect later?

user:    Show me the older two.

Lucid:   Earlier you saved: "When M. is in the room and a group
         decision is in motion, I tend to test an idea once and back
         off if it isn't picked up immediately." Still resonating,
         anything change?
```

The empty-window fallback is a router decision (the agent contract
says so explicitly): if `insights_window` is empty, surface the two
most recent insights regardless of age and offer a `/log` path. When
entries have accumulated since the last `/checkin` without a
Reflection pass, the router also appends the fixed pointer line
*"There are entries since your last check-in — /checkin when you want
to look for a pattern together."* — recall-side copy, not a proposal.
No new patterns are proposed — `/reflect` is read-and-ask only.

## What this loop intentionally is not

* **Not a daily/weekly/monthly/yearly cascade.** The MVP has one
  reflection cadence: weekly recall via `/reflect`. The full multi-scale
  reflection in `vision.md` is a follow-on.
* **Not a multi-pattern proposal engine.** One pattern per session is a
  hard cap.
* **Not an autonomous notifier.** `/reflect` is invoked by the user. No
  push, no scheduled DM, no proactive outreach in the Mirror thread.
  (The Engine's bell and tripwire are the sole, pre-committed
  exceptions, and they never carry reflective content — see
  [`engine-module.md`](engine-module.md).)
* **Not a goals or coaching surface.** The Coach role from `vision.md`
  is deferred entirely. The Engine module records committed acts but
  has no voice and proposes nothing.
* **Not a draft-and-send surface.** The Agent-Self role is deferred.
  Lucid never composes external messages in the MVP.
* **Not a profile editor.** Validated insights accumulate as files; the
  MVP does not expose a "psychological profile" view. That is a
  follow-on once enough validated insights exist.
