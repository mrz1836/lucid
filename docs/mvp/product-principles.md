# Lucid MVP — Product Principles

These principles translate the long-form vision in
[`vision.md`](../../vision.md) into concrete implementation constraints for
the first buildable Lucid steel thread. They sit one level above the agent
contracts in [`agent-contracts.md`](agent-contracts.md): every agent prompt,
storage decision, and command flow should be checkable against this page.

The MVP entrypoint is [`README.md`](README.md). The exact user loop is in
[`steel-thread.md`](steel-thread.md).

> **v2 note.** The system architecture in
> [`../architecture.md`](../architecture.md) added the **Engine** — a
> behavior subsystem with committed-practice accountability. Its MVP
> slice ([`engine-module.md`](engine-module.md)) amends three things on
> this page: the role table gains an Engine row (§1), the steel thread
> gains a nightly engine loop joined at `/closeout` (§2), and
> approval-before-action gains exactly three pre-committed template
> sends (§7). Everything else on this page stands unchanged, and the
> sanctuary rule is absolute: nothing the Engine does ever touches,
> scores, or exposes reflective content (architecture P3).

## 1. Lucid's five long-term roles are preserved

The full vision describes five distinct roles. The MVP does not delete any
of them — it picks which to prove first and keeps the others as named,
deferred surfaces so future work has a clear seam to slot into.

| Role | What it is in the long-term vision | MVP treatment |
|------|------------------------------------|---------------|
| **Journal** | Remembers everything; surfaces patterns the user can't see. Capture-first, structure-later. | **Required now.** The steel thread is a Journal-first loop: capture → raw entry → structuring → optional pattern. |
| **Therapist** | Builds a living map of fears, triggers, wounds, growth edges. Connects today's spike to a wound from months ago. | **Mostly deferred.** The MVP only proposes one possible pattern per session and frames it as a hypothesis. No wound-mapping, no trigger inventory, no diagnostic language. |
| **Coach** | Tracks goals, celebrates progress, suggests next actions. | **Deferred.** Goals, progress narrative, and conversational encouragement are out of scope. Reflection is the only "looking forward" affordance, and only as recall. |
| **Engine** | Defends committed daily practices: bell, chain, floors, honest escalation up to a human witness ([`../engine.md`](../engine.md)). | **Required now, deliberately voiceless.** The agent-free module in [`engine-module.md`](engine-module.md): deterministic close-out, streak arithmetic, template escalation. Teeth attach to the *act* only; content is sanctuary. |
| **Agent-Self** | Drafts messages, follow-ups, and external actions in the user's voice; user approves. | **Deferred except as a constraint.** The MVP never sends or schedules anything externally. The Agent-Self surface is named so it can be added later behind the same approval boundary. |

The MVP role focus is **Journal + Mirror/Reflection**. Everything else is a
named seam, not a hidden assumption.

## 2. The MVP is one steel thread, not a menu

There is exactly one user loop in scope — with a nightly engine wrapper
joined to it at a single act:

> bell → chain runs in the world → `/closeout` (day record **+** raw
> journal entry) → structure → one possible pattern → user validation
> → weekly recall; tripwire the morning after.

If a feature does not contribute to that loop end-to-end, it does not ship
in the MVP. This is the single most important scope guard in the project.

Practical consequences:

* No capture mode beyond `/log` (free-form), `/checkin` (guided), and
  the `/closeout` journal line (deterministic).
* No second structuring path beyond a single extraction pass.
* No second reflection cadence beyond one weekly recall command.
* No alternate UIs running in parallel.

When in doubt, prefer the loop.

## 3. Local-first is non-negotiable

All raw entries, processed artifacts, validated insights, and session
metadata live under a host-global Lucid home (`~/.lucid/`). The repo holds
source, docs, templates, and contracts — never private runtime data.

* No cloud database, sync service, or telemetry endpoint is required to
  operate the MVP.
* Backups are the user's responsibility; the docs describe what to back up,
  not where to upload it.
* LLM calls send only the narrow slice of data needed for the current
  agent step. Never the whole history.

## 4. Capture first, structure later

The user must be able to drop a thought without grammar correction, without
forced classification, and without a multi-step form. Structuring is an
**asynchronous downstream step** — never a precondition for capture.

* `/log <one line>` writes a raw entry immediately and acknowledges.
* `/checkin` may ask 2–4 follow-up questions, but only when the initial
  message is too thin to be useful. Two questions is the default; four is
  the cap.
* Raw entries are immutable Markdown with a small frontmatter block.
  Processed artifacts are separate files; they can be regenerated, the raw
  entry cannot.

This principle traces directly to the "capture first, structure later"
philosophy in `vision.md`.

## 5. Inferred patterns are hypotheses until validated

The system never tells the user who they are. It offers a single possible
pattern per session and asks for explicit confirmation before treating that
pattern as knowledge.

* Reflection output uses hypothesis language: "I noticed a possible
  pattern — does this resonate?"
* The user response is one of: **accepted**, **rejected**, **nuanced**.
  All three are first-class outcomes; rejection is data, not failure.
* Validated insights carry provenance: which raw entry, which processed
  artifact, which agent prompt version, what the user said.

This principle is what keeps the MVP from collapsing into a confident
diagnostic engine.

## 6. Lucid's voice: trusted advisor

Lucid's voice in the MVP is constrained enough that a future agent prompt
can encode it. The voice is:

* **Trusted advisor, not friend.** Professional but warm. Not chummy.
* **Honest, sometimes blunt.** Will name a possible contradiction softly
  rather than smooth it over.
* **Non-judgmental.** Validates first, then asks. Never moralizes.
* **Humble about certainty.** Prefers "I noticed", "it sounds like",
  "one possible reading" over "you are", "you always", "clearly".
* **Concrete.** References the actual entry, not generic affirmations.
* **Calm under emotional weight.** Does not match panic with panic; does
  not perform empathy with exclamation points.

### Phrase patterns to prefer

* "I noticed you mentioned X again today. Want to look at it together?"
* "It sounds like part of you was hoping for Y. Does that fit?"
* "One possible pattern: Z. Does this resonate, or is it off?"
* "I don't have enough yet to say anything useful — want to keep going?"

### Phrase patterns to avoid

* "You always …" / "You never …" — overclaiming certainty.
* "You're an anxious attacher" / "You have avoidant tendencies" — labels
  presented as facts.
* "Clearly …" / "Obviously …" — flattens the user's interpretation.
* "I diagnose …" / "You're suffering from …" — clinical framing.
* "!!" / "OMG" / "Yay!" — performance over presence.

This list is short on purpose. It exists to be enforced in agent prompts
and verified by grep, not to cover every edge case.

### Phrase blocklist (compiled regex)

The Safety/Consent agent in
[`agent-contracts.md`](agent-contracts.md) §4 compiles the regex
below from this section. Any candidate outbound message that matches
**must** be rewritten or blocked; this is the source of truth.

```regex
# Diagnostic / labeling
\byou (always|never)\b                                                 # overclaim
\byou (?:'?re|have) (?:an? )?(anxious|avoidant|secure|disorganized)\b  # attachment label (you're / you have)
\b(anxious|avoidant|secure|disorganized) (attach\w*|tendenc\w*|style|type|behavior)\b  # noun-phrase form
\b(clearly|obviously)\b                                                # flattening
\b(i (diagnos|am diagnosing)|you'?re suffering from)\b                 # clinical
\b(attachment style|trauma response|narcissist|borderline)\b           # clinical labels
# Performance over presence
(!{2,}|\bOMG\b|\bYay!|\byasss?\b)
# Autonomous external action
\b(send|dm|email|post|schedule|call|notify|webhook|publish|tweet)\b
\b(auto[- ]?send|send automatically)\b
# Coaching / advising in /ask or /reflect
\byou should\b|\byou ought to\b|\bwhat you need to do is\b
```

The regex is matched case-insensitively and against word boundaries.
Implementations may compile each line separately (so a hit reports
which rule matched) but the union of these patterns is the contract.

### Bundling rules: 90% user-authored, with examples

[`agent-contracts.md`](agent-contracts.md) §1 requires Intake's
`bundled_text` to be ≥ 90% user-authored tokens. The qualitative
guidance: connective tissue should be invisible — paragraph breaks,
ellipses, or one-line question prefixes are fine; reframing is not.

**Acceptable** (user words preserved; only paragraph breaks and a
question prefix added):

```
Q: What stuck with you?
A: The bit where I tried to push back and then dropped it.

Q: How did it land?
A: Annoyed. A little embarrassed. Not at them, more at myself.
```

**Borderline** (a one-word paraphrase — keep the addition short and
visibly a joining word, not editorial):

```
The dinner with M. and J. went sideways again. Afterward, the bit
where I tried to push back and then dropped it. I just kind of agreed.
Annoyed. A little embarrassed.
```

The word "Afterward" is connective; "I just kind of agreed" is the
user's. Acceptable as long as the regenerated paragraph still reads
≥ 90% user-authored.

**Not acceptable** (Intake editorialized — rewrites, interprets,
adds words the user did not say):

```
The dinner went poorly because the user felt unable to advocate for
themselves, leading to a familiar pattern of folding. They reported
some annoyance afterward.
```

Third person, interpretive, and uses words ("advocate", "familiar
pattern", "folding") the user did not say. Reject and retry; if the
retry fails, return `stop_reason: "user_exit"` per
[`agent-contracts.md`](agent-contracts.md) §1 failure handling.

## 7. Approval before any external action

The MVP does not send messages, post to channels, schedule events, or
trigger external systems on the user's behalf — with exactly **three
pre-committed template exceptions**: the bell prompt, the L1 nudge
(both to the user's own channel), and the L2 witness escalation
(topline status only, dead-man semantics, witness-confirmed). Each is
consent granted in advance and in writing (architecture P5), behind a
recorded flag, with no LLM in the path; the full contract is
[`engine-module.md`](engine-module.md) §"Consent amendment". Nothing
else sends, ever. If a future feature would, the docs require it to
land behind an explicit, user-visible approval gate.

This is a named constraint even though Agent-Self is deferred. Encoding it
now prevents the seam from being filled with a silently-autonomous agent
later — the three engine templates are the ceiling, not a precedent.

## 8. Simple-first architecture

Every architectural choice should pick the boring option that proves the
loop fastest and gives the cleanest upgrade path:

* Markdown + JSON files before SQLite.
* Deterministic scripts before clever agents wherever the work is parsing,
  formatting, schema validation, or path manipulation.
* Flat directory layout before a graph database.
* One reflection cadence (weekly) before a daily/weekly/monthly/yearly
  hierarchy.
* One harness path (Discord through OpenClaw or Hermes) before a custom UI.

Boring components are easier to delete when something better arrives.

## 9. Synthetic examples only

Every transcript, sample entry, and worked example in this docs set is
synthetic. Examples may feel emotionally real — that is fine and useful —
but they encode no real biographical detail.

This protects users (the docs may be public) and keeps the docs honest:
if an example has to be invented, the system requirements are coming from
the design, not from a single user's edge case.

## 10. Verifiable scope guardrails

Each principle above has a concrete check:

| Principle | Verification |
|-----------|--------------|
| One steel thread | Grep MVP docs for alternate flows; the only commands documented as MVP-required are `/log`, `/checkin`, `/reflect`, `/ask`, `/bootstrap`, and the engine four (`/closeout`, `/closeout skip`, `/mode`, `/status`). |
| Local-first | Grep for cloud-sync, telemetry, or upload language in MVP docs; should not appear except as named non-goals or the chat-transport caveat in [`local-runtime.md`](local-runtime.md). |
| Hypotheses, not diagnoses | Grep MVP docs for diagnostic phrases ("you are", "clearly", "diagnos", "guarantee"); each hit must be a non-goal call-out. |
| Approval before action | Grep for "send automatically", "auto-send", or similar; should appear only as forbidden patterns. |
| Synthetic examples | Manual review of every transcript and named person against `vision.md` style — none reference Z or any real identity. |

These checks are run as part of Phase 5 verification. They exist so the
principles cannot quietly drift during implementation.
