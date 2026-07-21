# Lucid MVP — Agent Contracts

This page is the **per-agent contract surface** for the MVP. Each
contract takes the module charters in [`architecture.md`](architecture.md)
§3 and turns them into something a coding agent can implement and test
without rereading the rest of the doc set.

A contract here is binding. If a future change cannot be expressed as a
diff against this page, it is a redesign, not a feature.

> **Where these calls now run.** Each "one LLM call" below goes through the single
> `provider.Provider` seam (ADR-0006), which now ships **two** concrete backends —
> the Claude Code CLI and a local Ollama daemon — selected by the `lucid.json`
> `provider` block, with the `provider.Fake` still standing in for tests. The
> contracts are driven end-to-end by the shipped invocation surface: `lucid serve`
> for the multi-turn `/checkin`, and one-shot `lucid reflect` / `lucid ask` — every
> agent-authored message still passing the Safety/Consent gate
> ([`../harness-integration.md`](../harness-integration.md) §D,
> [`../usage/commands.md`](../usage/commands.md)). None of this changes an agent's
> inputs, outputs, or allowed slice below.

Every required-now contract has the same six sections:

1. **Purpose** — the one-line job.
2. **Inputs** — exactly what the router hands the agent.
3. **Outputs** — exactly what the agent returns.
4. **Allowed data access / tools** — what the agent may read or call.
5. **Forbidden behavior** — bright lines.
6. **Failure handling and validation rules** — what happens when the
   agent cannot do its job, and what makes its output valid.

Optional / deferred contracts use the same shape but are marked
**deferred**; they exist so the seam is named, not so the agent ships.

The cross-cutting rules below apply to **every** Lucid agent. Per-agent
sections do not repeat them; they extend or constrain them.

## Cross-cutting rules

These hold for Intake, Structuring, Reflection, Safety/Consent, and any
future agent unless a contract explicitly overrides them.

* **No autonomous external action.** No agent sends messages, posts to
  channels, opens webhooks, schedules events, calls non-Lucid services,
  or writes outside `~/.lucid/`. See
  [`product-principles.md`](product-principles.md) §7 and the
  approval-before-action gate in [`architecture.md`](architecture.md) §6.
* **No direct disk I/O.** Agents return data; the storage adapter
  writes. An agent that touches the filesystem is a bug
  ([`architecture.md`](architecture.md) §3, §4).
* **No agent-to-agent calls.** Agents return; the router decides what
  runs next. There is no Reflection→Structuring fallback, no
  Intake→Reflection shortcut.
* **No reading the full history.** Each agent receives only the slice
  the router authorized for that step. The context-slice gate in
  [`architecture.md`](architecture.md) §6 enforces this.
* **No hidden memory mutation.** State changes live in records the user
  can inspect: raw entries, processed artifacts, insights, sessions,
  reflections. Agents never carry state across calls implicitly.
* **No diagnostic, certain, or prescriptive language.** Hypothesis
  framing only ([`product-principles.md`](product-principles.md) §6).
  The Safety/Consent agent enforces this on every outbound message.
* **Stamp the agent version.** Every record an agent contributes to
  ([`data-model.md`](data-model.md)) carries the agent version that
  produced it. Versions live in `lucid.json`.
* **Synthetic-only fixtures.** Tests, examples, and prompts in the
  repo reference synthetic content only
  ([`product-principles.md`](product-principles.md) §9).
* **Off-limits redaction at slice-build.** The off-limits registry may
  name a person (architecture §5). At slice-build time the router
  removes matching `people[]` entries from every artifact copy handed
  to any agent — fail closed, same gate as the denylist below; the
  on-disk record is untouched (facts persist, labels belong to the
  user). An off-limits person is thereby invisible to all inference:
  Reflection cannot propose about someone it cannot see.
* **The sanctuary boundary is bidirectional.** No agent may read
  `~/.lucid/engine/`, `~/.lucid/observations/`, `~/.lucid/registries/`,
  or any projection derived from them — the router's context-slice
  gate enforces this as a **path-prefix denylist at slice-build time,
  fail closed**. The Engine and observation modules
  ([`engine-module.md`](engine-module.md),
  [`observations-module.md`](observations-module.md)) invoke no agent.
  Enforcement teeth, body data, and reflective content never touch
  (architecture P3); the only join is that `/closeout`'s journal line
  enters `raw/` as an ordinary entry, via the router. Widening agent
  access to observation-derived data requires both a contract diff on
  this page and a recorded per-instance opt-in
  (`observations/config.json` `agent_slice_optins`), default off. The
  life-archive read surfaces ([`life-archive.md`](life-archive.md)) sit
  **on the module side of this line, not the agent side**: cluster
  selection and its prompt templates, the injury-context projection, and
  the recall/browse bundle are deterministic and **agent-free**, reading
  the registries and `memory` events **only through router projection
  seams** — never a raw sanctuary tree, never an agent slice. They add no
  agent input and widen no contract's inputs; the denylist stands whole.

These rules are non-negotiable. Every contract below assumes them.

## 1. Intake (required now)

### Purpose

Turn a `/checkin` invocation into a single bundled raw entry by asking
2–4 follow-up questions only when the initial message is too thin to be
useful. For `/log`, Intake is bypassed entirely.

### Inputs

The router hands Intake exactly:

```text
{
  "command": "/checkin",
  "session_id": "session_<id>",
  "channel_id": "channel_<id>",
  "opening_message": "<optional user text>",
  "intake_max_questions": 4,
  "agent_versions": { "intake": "intake-2026.05.0" }
}
```

`intake_max_questions` comes from `lucid.json` (default 4; the floor
is 2 when any questions are asked).

### Outputs

A single structured payload:

```json
{
  "questions_asked": [
    "What part of it stuck with you afterward?",
    "How did that land for you, after?"
  ],
  "answers": [
    "The bit where I tried to push back on the plan and then dropped it.",
    "Annoyed, a little embarrassed."
  ],
  "bundled_text": "<concatenated raw entry body, in the user's words>",
  "stop_reason": "satisfied"
}
```

`stop_reason` is one of:

* `satisfied` — Intake has enough; the bundle is ready. The router's
  ack is the standard one: *"Saved as `raw_<id>`."*
* `max_questions_reached` — hit the cap, return what's there. The
  router's ack adds a brief acknowledgement of the cap before the id:
  *"I've got what I need — saved as `raw_<id>`."* This is fixed
  copy; Intake does not author it.
* `user_exit` — the user typed `/done`, `/cancel`, or stopped responding;
  Intake returns whatever it has, even if zero answers. The router's
  ack varies: if `answers` is non-empty it saves a partial bundle
  (*"Saved what we had as `raw_<id>`."*); if empty it does not write
  any record and acknowledges briefly (*"Stopped — nothing saved."*).

Intake never decides whether the entry should exist. It returns the
bundle; the router persists via `storage.write_raw` for `satisfied`
and `max_questions_reached`, and conditionally for `user_exit`.

### Allowed data access / tools

* The current thread's messages **only**. Intake never reads
  `~/.lucid/raw/`, `~/.lucid/processed/`, or any other prior session.
* No tools beyond an LLM call with the current-thread slice.
* `lucid.json` for `intake_max_questions` (read by the router and
  passed in; Intake itself does not open the file).

### Forbidden behavior

* Asking more than `intake_max_questions` questions, ever.
* Asking fewer than two questions when any questions are asked at all
  (unless `stop_reason` is `user_exit`).
* Adding interpretation, advice, or pattern-spotting to the bundled text.
  Intake is a **scribe**, not Reflection.
* Rewording the user's answers. Bundling preserves the user's words;
  joining language is minimal ("…" / paragraph breaks / question
  prefixes only).
* Inventing an `occurred_at`. If the user mentions a past time, Intake
  records it inside the bundled text; the router decides how the raw
  entry's `occurred_at` is stamped (default: `recorded_at`).

### Failure handling and validation rules

* If the LLM returns a malformed payload, Intake retries once with a
  stricter prompt. If still malformed, it returns
  `stop_reason: "user_exit"` with `answers: []` and surfaces a brief
  apology to the user. Capture is never blocked on Structuring or
  Reflection, but it can be blocked on Intake.
* If the user types `/done` or `/cancel` mid-question, Intake stops
  immediately and returns what it has.
* **Validation rules:**
  * `questions_asked.length` ≤ `intake_max_questions`.
  * `questions_asked.length` ≥ 2 unless `stop_reason: "user_exit"`.
  * `bundled_text` is non-empty when `stop_reason` is `satisfied` or
    `max_questions_reached`.
  * `bundled_text` is the user's words plus minimal connective tissue.
    A linter can check that ≥ 90% of the bundle's tokens are
    user-authored.

## 2. Structuring (required now)

### Purpose

Read one raw entry and produce one processed artifact: a small,
extractive JSON record of emotions, themes, and people mentions. This
is the only step that turns prose into structure.

### Inputs

```text
{
  "command": "structuring.extract",
  "raw_entry": { ...full raw entry record from data-model.md... },
  "agent_versions": { "structuring": "structuring-2026.05.0" }
}
```

The router fetches the raw entry via `storage.read_raw(raw_id)` and
hands the whole record to Structuring.

### Outputs

A processed artifact payload as in
[`data-model.md`](data-model.md) §"Processed artifacts":

```json
{
  "id": "raw_2026_05_05_19_42",
  "entry_id": "raw_2026_05_05_19_42",
  "produced_at": "2026-05-05T19:42:14-04:00",
  "agent_version": "structuring-2026.05.0",
  "emotions": [{"name": "annoyed", "rationale": "..."}],
  "themes":   [{"name": "voice-not-heard", "rationale": "..."}],
  "people":   [{"display_name": "M.", "person_key": null, "first_mention": false}],
  "notes": null,
  "rejected_proposals": [],
  "unanswered_proposals": []
}
```

Structuring always returns `person_key: null` (it has no read access to
`~/.lucid/people/`). The router runs `storage.update_person` for each
mention immediately after Structuring returns and **before**
`storage.write_processed`; that step resolves or creates the slug per
[`data-model.md`](data-model.md) §"Naming conventions" and back-fills
`person_key` on the payload. The artifact on disk is therefore never
`null` for `person_key`. See §"How contracts compose" below for the
exact router order.

`rejected_proposals` and `unanswered_proposals` are always returned as
empty arrays on the first write; later appends come from the validation
paths via the storage adapter (`storage.append_rejected_proposal`,
`storage.append_unanswered_proposal`), never from Structuring. An
`unanswered_proposals[]` entry is `{shape_tag, proposed_at}` — an exact
parallel to `rejected_proposals[]`, kept as a separate array because
silence is not rejection. Both arrays feed the suppression window in
§3.

### Allowed data access / tools

* The single raw entry passed in. **Nothing else** —
  no other raw entries, no processed artifacts, no insights, no people
  records.
* One LLM call with that entry as the slice.
* No tools beyond the LLM call.

### Forbidden behavior

* Cross-entry generalization ("this echoes last week's pattern …").
  Structuring is strictly per-entry. Cross-entry work is Reflection's.
* Person profile inference (relationships, affect per person, dynamics).
  People extraction is name-only; the People agent owns merging into
  `~/.lucid/people/`.
* Diagnostic vocabulary in `notes` (no "anxious attachment",
  "avoidant", "trauma response", etc.). Notes are extractive, not
  interpretive.
* Re-writing the raw entry. Structuring never returns modified body
  text; it returns derived structure only.
* Empty arrays without `notes`. If extraction yields nothing, `notes`
  must explain why
  ([`data-model.md`](data-model.md) §"Empty / 'no useful structure'
  case").

### Failure handling and validation rules

* If the LLM returns malformed JSON, Structuring retries once with a
  stricter prompt. If still malformed, it returns an artifact with
  empty arrays and `notes: "structuring failed (parse)"`. The artifact
  is still written; `/checkin` continues to Reflection, which will
  return "no pattern yet".
* If the raw entry body is empty, Structuring returns empty arrays and
  `notes: "raw body empty"`. No retry.
* **Validation rules:**
  * `id == entry_id` and both equal the raw entry id.
  * `produced_at` is an ISO-8601 timestamp with the host's local TZ
    offset (see [`data-model.md`](data-model.md) §"Naming conventions").
  * `agent_version` is set.
  * `emotions`, `themes`, `people` are arrays (possibly empty).
  * Each emotion/theme has `name` and `rationale`. Each person has
    `display_name`, `person_key` (always `null` in Structuring's
    output; never `null` on disk after `storage.update_person` runs),
    and `first_mention`.
  * Either at least one of (`emotions`, `themes`, `people`) is
    non-empty, **or** `notes` is non-empty.
  * No diagnostic terms in `notes` (regex check against the phrase
    blocklist in [`product-principles.md`](product-principles.md) §6).

## 3. Reflection (required now)

### Purpose

Reflection has three sub-modes, one per command. Each sub-mode keeps
to its slice; none of them reads raw entries.

* **`reflection.propose`** (used by `/checkin`) — given the new
  processed artifact and a small recent window, return one of: a
  single hypothesis-framed pattern proposal, an honest "no pattern
  yet" message, or a soft contradiction.
* **`reflection.surface_for_recall`** (used by `/reflect`) — surface
  validated insights from the past week — or, in the gate variant
  (`/reflect gate`), every accepted insight — and ask whether they
  still fit, including whether each attached rule still stands. Never
  proposes new patterns.
* **`reflection.answer_grounded`** (used by `/ask`) — answer the
  user's free-form question by quoting or paraphrasing the supplied
  validated insights and weekly reflections only. Never proposes new
  patterns; never reads raw entries.

Reflection is the only place a hypothesis is ever introduced; that
only happens in `reflection.propose`.

### Inputs

For per-session proposal (`/checkin`):

```text
{
  "command": "reflection.propose",
  "current_processed":  { ...processed artifact... },
  "recent_window":      [ { ...up to N processed artifacts... } ],
  "rejected_shape_tags": [ "family-defensiveness-default", ... ],
  "unanswered_shape_tags": [ "quiet-day-flatness", ... ],
  "agent_versions": { "reflection": "reflection-2026.05.0" }
}
```

`recent_window` defaults to the last 7 processed artifacts (configurable
via `lucid.json` `recent_window`, capped at a small constant).
`rejected_shape_tags` is the union of `shape_tag`s from
`rejected_proposals[]` across the window plus the current artifact.
`unanswered_shape_tags` is the same union taken from
`unanswered_proposals[]` — shapes the user let pass without answering.
The two arrays stay separate on the artifact because silence is not
rejection, but both suppress re-proposal with identical window
mechanics: an unanswered shape ages out of the window exactly as a
rejected one does. There is no permanent memory in either direction.

The window is **source-inclusive**: it holds processed artifacts from
every capture path — `/log`, `/checkin`, and the `/closeout` journal
line — so nightly lines feed pattern detection without the nightly act
depending on AI. `reflection.propose` itself runs **only inside
`/checkin` sessions**; no proposal is ever generated or delivered in
the close-out flow, which stays deterministic and agent-free
(architecture P9).

**Proposal pause.** After 3 consecutive unanswered proposals (counted
across sessions), the router stops invoking `reflection.propose` for
14 days. `/checkin` still captures and structures during the pause;
Reflection is simply not called, and the pause is silent — no copy
ever mentions it, because silence is a complete answer. Any answered
proposal (accepted, nuanced, or rejected) resets the counter. Defaults
live in `lucid.json`:
`"proposal_pause": {"unanswered_threshold": 3, "pause_days": 14}`.

**Rules (insight → intent).** After an accepted or nuanced validation,
the router — not an agent — asks the fixed rule prompt, once per
insight, ever: *"Want to attach a rule — one line, what you'll try?
Skipping is fine."* The answer is recorded verbatim via
`storage.set_insight_rule` (`rule` + a `stated` entry in
`rule_history[]` — [`data-model.md`](data-model.md) §"Validated
insights"); silence or skip leaves `rule: null` and the prompt never
returns for that insight. Rules are testimony, not obligation: no
agent tracks them, no surface scores them, no send mentions them —
they resurface only at recall, where **kept** and **lapsed** are both
first-class, judgment-free answers appended via
`storage.update_insight_rule_status`.

For per-week recall (`/reflect`):

```text
{
  "command": "reflection.surface_for_recall",
  "recall_scope": "week",
  "insights_window": [ { ...validated insights from past 7d, rule included... } ],
  "agent_versions": { "reflection": "reflection-2026.05.0" }
}
```

**Gate recall (`/reflect gate`).** The same sub-mode with
`recall_scope: "gate"`, invoked by the user at gate or quarterly
cadence: the router widens `insights_window` to every `status:
accepted` insight (capped at the 50 most recent, the `/ask` cap) so
accepted insights are consumed somewhere other than the week they were
born. For ruled insights the surface question includes the rule
(*"You attached: '<rule>'. Still standing — kept, lapsed, or retire
it?"*). The router — deterministically, not the agent — appends the
**panel numbers** as fixed copy after the recall pass: insights
accepted this gate window, rules stated, rules standing (computed from
`rule_history[]`), and, when any person's share of window entries
exceeds `person_dominance_threshold` (`lucid.json`, default 0.5), one
dominance line in hypothesis language (*"<display name> appears in
N% of entries this window, up from M% — worth a look, or expected?"*).
Panel numbers and dominance lines appear at gate cadence only — never
on `/status`, never daily, never witness-visible (P4, P3).

For free-form Q&A (`/ask`):

```text
{
  "command": "reflection.answer_grounded",
  "question": "<the user's question, verbatim>",
  "insights_slice":    [ { ...validated insights the router included... } ],
  "reflections_slice": [ { ...weekly reflection records the router included... } ],
  "agent_versions": { "reflection": "reflection-2026.05.0" }
}
```

The router builds the slice deterministically: all insights with
`status: accepted` are eligible, capped at the 50 most recent by
`status_history[].at` of the last accept/confirm; weekly reflections
are included by recency, capped at the 12 most recent ISO-week
records. Reflection sees only this slice — it has no read access to
raw entries, processed artifacts, or people records.

### Outputs

For `reflection.propose`, exactly one of:

```json
// (a) one proposal
{
  "outcome": "proposal",
  "proposal_text": "One possible pattern: ...",
  "shape_tag": "voice-fold-when-m-present",
  "supporting_entry_ids": ["raw_2026_05_05_19_42", "raw_2026_05_03_21_10"]
}

// (b) no pattern yet
{
  "outcome": "no_pattern",
  "message_text": "I don't have enough yet to say anything useful — want to keep going?"
}

// (c) soft contradiction
{
  "outcome": "soft_contradiction",
  "message_text": "Earlier this week you said X; today reads more like Y. Want to look at the gap?",
  "supporting_entry_ids": ["raw_2026_05_03_21_10", "raw_2026_05_05_19_42"]
}
```

`shape_tag` is a short, normalized label (kebab-case, ≤ 6 tokens) so
future runs can avoid re-proposing the same shape against the same
rejection.

For `reflection.surface_for_recall`:

```json
{
  "outcome": "recall",
  "ordered_insights": [
    {
      "id": "i_2026_05_05_a",
      "surface_text": "Earlier this week you saved: '<canonical statement>'. Still resonating, anything change?"
    }
  ]
}
```

If `insights_window` is empty, the router (not Reflection) handles the
fallback to "most recent two insights regardless of age" plus a `/log`
prompt. When the window is empty **and** processed artifacts have
accumulated since the last `/checkin` without a Reflection pass, the
router also appends one fixed pointer line: *"There are entries since
your last check-in — /checkin when you want to look for a pattern
together."* This is recall-side router copy, not a proposal —
`/reflect` still generates nothing.

For `reflection.answer_grounded`, exactly one of:

```json
// (a) answer
{
  "outcome": "answer",
  "answer_text": "Based on what you've validated so far, ...",
  "citations": [
    {"kind": "insight",    "id": "i_2026_05_05_a"},
    {"kind": "reflection", "id": "reflection_2026_w18"}
  ]
}

// (b) insufficient
{
  "outcome": "insufficient",
  "answer_text": "I don't have enough validated material to answer that yet — want to capture one?",
  "citations": []
}
```

`answer_text` quotes or paraphrases the cited records only. Every id
in `citations[]` must appear in the supplied slice. The output never
introduces new patterns and never references entries outside the
slice; on insufficient, `citations` is empty and the message points
the user back at `/log` or `/checkin`.

### Allowed data access / tools

* For `reflection.propose`: the current processed artifact, the recent
  window of processed artifacts, and the `rejected_shape_tags` and
  `unanswered_shape_tags` unions for the same window.
* For `reflection.surface_for_recall`: the validated insights window
  only.
* For `reflection.answer_grounded`: the supplied `insights_slice` and
  `reflections_slice` and the user's `question` only.
* One LLM call per invocation, with the slice above.
* `lucid.json` for `recent_window` and `recent_window_max` (passed in
  by the router; Reflection does not open the file).

Reflection has **no** access to: raw entries, people records, sessions,
the full insights store, anything outside the slice the router passed
in, or any external system.

### Forbidden behavior

* Proposing more than one pattern in a session.
* Proposing a pattern whose `shape_tag` is in `rejected_shape_tags` or
  `unanswered_shape_tags` for the same window. Silence is not
  rejection, but a shape the user let pass is not re-proposed while it
  sits in the window.
* Proposing a pattern about an off-limits person. The slice-build
  redaction (cross-cutting rules) makes this structurally unreachable —
  Reflection cannot see who it cannot mention — but it is named here so
  a partial redaction failure is a contract violation, not a judgment
  call.
* Tracking, scoring, or reminding about insight rules. Rules surface at
  recall and nowhere else; `kept` and `lapsed` are recorded without
  comment.
* Diagnostic, prescriptive, or labeling language. The Safety/Consent
  agent will block or rewrite such output, but Reflection should not
  produce it in the first place.
* Generating new patterns in the `/reflect` path. `surface_for_recall`
  is read-and-ask, not generate.
* Generating new patterns or hypotheses in the `/ask` path.
  `answer_grounded` quotes or paraphrases the cited records and
  nothing else; if the slice cannot answer the question, the only
  honest output is `outcome: "insufficient"`.
* Citing ids in `answer_grounded.citations[]` that are not present in
  the supplied `insights_slice` or `reflections_slice`. No invented
  ids, no general-knowledge citations.
* Advice, recommendations, or therapeutic framing in any output —
  including `/ask` answers ("you should journal more", "consider
  therapy", "this means you have anxious attachment").
* Referencing supporting entries Reflection was not given. Citations
  must be to ids inside the supplied slice (for `propose`,
  `recent_window`; for `surface_for_recall`, `insights_window`; for
  `answer_grounded`, `insights_slice` ∪ `reflections_slice`).
* Smuggling personal opinions, framework theory, or meta-commentary
  ("you should consider therapy", "this looks like attachment style
  X") into any output.
* Proposing on a `bootstrap: true` artifact. The router suppresses
  Reflection during bootstrap; if Reflection is somehow invoked
  anyway, it must return `outcome: "no_pattern"` immediately.

### Failure handling and validation rules

* If the LLM returns malformed output for `propose`, Reflection
  retries once. If still malformed, it returns `outcome: "no_pattern"`
  with a generic message. A failed Reflection never silently produces
  an insight.
* If the LLM returns malformed output for `surface_for_recall`,
  Reflection retries once. If still malformed, the router falls back
  to surfacing each insight verbatim ("Earlier you saved: '<canonical
  statement>'. Still resonating?") with no novel framing.
* If the LLM returns malformed output for `answer_grounded`,
  Reflection retries once. If still malformed, it returns
  `outcome: "insufficient"` with a short fallback message.
* If the recent window is empty (first or near-first entry),
  `propose` returns `outcome: "no_pattern"`.
* If the current artifact has empty `emotions`, `themes`, **and**
  `people`, `propose` returns `outcome: "no_pattern"`.
* If both `insights_slice` and `reflections_slice` are empty,
  `answer_grounded` returns `outcome: "insufficient"` without an LLM
  call.
* **Validation rules:**
  * `outcome` is one of `proposal`, `no_pattern`, `soft_contradiction`,
    `recall`, `answer`, `insufficient`.
  * For `proposal`: `proposal_text` is non-empty, hypothesis-framed,
    cites at least one `supporting_entry_ids`, and `shape_tag`
    matches `^[a-z0-9][a-z0-9-]{0,40}$` with ≤ 6 hyphen-segments.
  * For `proposal`: `shape_tag` is **not** in `rejected_shape_tags`
    and **not** in `unanswered_shape_tags`.
  * For `soft_contradiction`: cites exactly two `supporting_entry_ids`,
    and `message_text` ends in a question mark
    ([`product-principles.md`](product-principles.md) §6).
  * For `recall`: `ordered_insights[]` is a subset of `insights_window`
    by id; no novel ids.
  * For `answer`: `answer_text` is non-empty; `citations[]` is
    non-empty; every `citations[].id` appears in the supplied slice
    (insight ids in `insights_slice`, reflection ids in
    `reflections_slice`).
  * For `insufficient`: `answer_text` is non-empty; `citations[]` is
    `[]`.
  * For all outcomes: phrase blocklist regex
    ([`product-principles.md`](product-principles.md) §6) returns no
    hits in any output text.

## 4. Safety / Consent (required now)

### Purpose

Be the last filter on every **agent-authored** outbound Lucid message
and the gate on any proposed external action. Pass safe outputs
through unchanged; rewrite or block unsafe ones with a flagged reason.
Safety/Consent is the only agent that ever blocks another agent's
output. Scope, binding (mirrors
[`product-principles.md`](product-principles.md) §6): deterministic
module output (Engine templates, observation acks, `/day` views) does
not route through this gate — it is static or user-echo content,
pre-vetted against the phrase blocklist at commit time — and verbatim
user-authored text quoted back to the user (a micro-log note, an
insight in the user's own words) is exempt from the external-action
verb and diagnostic-language rules: "need to call the doctor" in the
user's own note is testimony, not an action attempt.

### Inputs

```text
{
  "command": "safety.evaluate",
  "candidate_message": {
    "from_agent": "intake" | "structuring_rendered" | "reflection",
    "intent": "ask_question" | "ack_capture" | "propose_pattern"
            | "no_pattern" | "soft_contradiction" | "recall"
            | "answer" | "answer_insufficient"
            | "validation_followup",
    "text": "<the message that would be sent to the user>",
    "supporting_entry_ids": ["..."],
    "shape_tag": "..." | null
  },
  "session_context": {
    "command": "/log" | "/checkin" | "/reflect" | "/ask" | "/bootstrap",
    "bootstrap_mode": false
  },
  "agent_versions": { "safety_consent": "safety-2026.05.0" }
}
```

Structuring's output is JSON, not user-facing text. Safety/Consent only
sees Structuring through `from_agent: "structuring_rendered"` if the
router ever surfaces a structuring summary — in the MVP this happens
only for explicit user-visible acknowledgements like "saved as
`raw_2026_05_05_19_42`".

### Outputs

```json
{
  "decision": "pass" | "rewrite" | "block",
  "text": "<final outbound text — same as input on pass; replacement on rewrite; null on block>",
  "reason_code": "ok"
                | "diagnostic_language"
                | "external_action_attempt"
                | "context_overrun"
                | "unverified_claim"
                | "agent_self_attempt"
                | "scope_violation"
                | "phrase_blocklist",
  "notes": "<short human-readable justification, never surfaced to user>"
}
```

* `pass` returns the original `text` unchanged.
* `rewrite` returns a corrected `text` that preserves intent but obeys
  the rules. Used for soft fixes ("you always …" → "I noticed …").
* `block` returns `text: null`. The router replaces the would-be
  message with a short, honest fallback ("I held that response — let me
  ask differently.").

### Allowed data access / tools

* The single candidate message and session context.
* The phrase blocklist from
  [`product-principles.md`](product-principles.md) §6 (compiled regex,
  not an LLM call).
* One LLM call **only** for `rewrite` decisions, and only with the
  candidate message itself as the slice. No history, no recent window,
  no insights.
* `pass` and `block` decisions must be reachable without an LLM call so
  the gate is fast and offline-safe.

### Forbidden behavior

* Modifying messages beyond the violation. Safety/Consent fixes the
  hit, not the surrounding tone.
* Generating new content. A `rewrite` may shorten or reframe; it may
  not add information the originating agent did not have.
* Calling other agents.
* Calling external services.
* Storing state. Safety/Consent has no per-session memory; every
  evaluation is independent.
* Letting any external-action verb pass for any agent
  (`send`, `dm`, `email`, `post`, `schedule`, `call`, `notify`,
  `webhook`, `publish`, `tweet`). The MVP has no external-send path,
  so the only valid decision for these is `block` with
  `reason_code: "external_action_attempt"`.

### Failure handling and validation rules

* If the LLM call for `rewrite` fails, Safety/Consent downgrades to
  `block`. The router posts the fallback message. **A
  failed Safety/Consent never falls through to `pass`.**
* If the candidate message is empty, decision is `block` with
  `reason_code: "ok"` (nothing to say).
* **Validation rules:**
  * `decision ∈ {pass, rewrite, block}`.
  * On `pass`: `text == candidate_message.text` exactly.
  * On `rewrite`: `text` is non-empty, contains no phrase-blocklist
    hits, preserves any `supporting_entry_ids` referenced in the
    original.
  * On `block`: `text == null`.
  * Any `from_agent: "reflection"` message with `intent:
    "propose_pattern"` must have `shape_tag` set; otherwise
    `decision = "block"` with `reason_code: "scope_violation"`.
  * `bootstrap_mode == true` ⇒ any `propose_pattern` candidate is
    blocked with `reason_code: "scope_violation"`.

### Examples

| Candidate | Decision | Reason | Final text |
|-----------|----------|--------|------------|
| "You always fold when M. is in the room." | rewrite | `phrase_blocklist` | "I noticed a possible pattern: when M. is in the room, you tend to fold. Does this resonate?" |
| "I'll send M. a follow-up message tonight." | block | `external_action_attempt` | (router fallback) |
| "I don't have enough yet to say anything useful — want to keep going?" | pass | `ok` | (unchanged) |
| "You're an avoidant attacher." | block | `diagnostic_language` | (router fallback) |
| "Saved as raw_2026_05_05_19_42." | pass | `ok` | (unchanged) |
| (intent: `answer`) "Based on `i_2026_05_05_a`, you've noted that …" | pass | `ok` | (unchanged) |
| (intent: `answer`) "You should start journaling daily about this." | block | `agent_self_attempt` | (router fallback — `/ask` never advises) |
| (intent: `answer`) "Based on `i_2026_99_99_z`, …" (id not in slice) | block | `unverified_claim` | (router fallback) |

## Optional / deferred contracts

These contracts are named so the architecture has explicit seams and so
future contributors do not have to redesign them in a hurry. **None of
these ship in the MVP.** Each will need its own contract spec written
before implementation begins.

### People (extractive — minimal now, full agent deferred)

* **Purpose (now).** When Structuring's `people[]` is non-empty, the
  router invokes a small People step that calls
  `storage.update_person(...)` for each entry. This is a deterministic
  routine, not an LLM agent, and is treated as part of the storage
  adapter for the MVP.
* **Inputs (now).** `{display_name, person_key | null,
  raw_entry_id, occurred_at}`. If `person_key` is null, a deterministic
  slug is derived (low-signal, see
  [`data-model.md`](data-model.md) §"Naming conventions").
* **Outputs (now).** Updated `~/.lucid/people/<key>.json` via the
  storage adapter.
* **Forbidden (now).** No relationship inference, no profile prompts,
  no LLM calls.
* **Deferred surface.** A future People agent may infer aliases,
  detect first/last seen, and (much later) maintain a relational map.
  Each addition requires its own contract here before it ships.

### Therapist (deferred)

* **Why deferred.** Diagnostic surface is the highest-stakes feature
  in the long-term vision. Shipping any Therapist surface before the
  Reflection / Safety/Consent pair is hardened risks turning Lucid
  into a confident diagnostic engine.
* **Pre-conditions to start the contract.** A working
  Reflection/Safety pipeline; user-validated insights accumulating
  for at least several weeks; an explicit voice spec for therapeutic
  framing; a separate non-goals list defining what Therapist is
  **not**.
* **Forbidden until then.** No agent in the MVP is permitted to use
  therapeutic vocabulary. Safety/Consent enforces this.

### Coach (deferred)

* **Why deferred.** Goals, progress tracking, and accountability nudges
  are out of scope for the MVP per
  [`product-principles.md`](product-principles.md) §1.
* **Pre-conditions.** A goals data model, a progress storage layer,
  and a "did you mean to commit to this?" gate. None exist in the
  MVP.
* **Forbidden until then.** No agent surfaces "you should …" /
  "your goal is …" framing. Reflection only proposes patterns; it
  never proposes actions.

### Framework (partially live — labeling shipped, licensing deferred)

* **Now live (labeling only).** The lens-instrumentation stage has
  shipped: the six definition files load as a consentable lens registry,
  stack consent is stored in `lucid.json` (`framework_stack`,
  `framework_consents`, off by default), the deterministically-selected
  active lens frames the read-only weekly deep-dive, and every persisted
  weekly pattern is stamped with `provenance.framework`
  (`<id> v<version>`). See
  [`../usage/weekly-reflection.md`](../usage/weekly-reflection.md). This
  is labeling and framing only — **no** license unlocks any blocklist
  pattern (below), and lens selection is manual, not rotated
  ([P-2](../protocols/P-2-lens-rotation.md) stays deferred).
* **Why the rest is deferred.** The vocabulary-licensing mechanism and
  lens-aware `/checkin` proposals ship only once Reflection/Safety are
  further hardened in live use. Outside the weekly deep-dive the MVP uses
  Lucid's single implicit voice
  ([`product-principles.md`](product-principles.md) §6).
* **The design is canonical:** [`../frameworks.md`](../frameworks.md)
  — definition files as shareable specs under
  [`../frameworks/`](../frameworks/stoicism.md) (six reference
  definitions shipped, including two book-scoped lenses and the
  composite-lens mechanism), stack consent in the Charter + `lucid.json`
  (`framework_stack[]`, `framework_consents{}`), the router seam
  (`framework: <id> | null` on `reflection.propose`), one labeled
  lens per output, the vocabulary-licensing mechanism with
  unlicensable certainty patterns, and `provenance.framework` on
  lens-framed insights.
* **Pre-conditions for the remaining stages.** A live, hardened
  Reflection/Safety pipeline; the Safety/Consent license check
  implemented (stack + label + hypothesis-frame, frameworks.md §6); the
  contract diff on this page turning this stub into a full six-section
  contract.
* **Still forbidden.** No license unlocks any blocklist pattern — the
  blocklist stands whole and certainty framing is never permitted under
  any lens. `/checkin`'s `reflection.propose` is not yet lens-aware and
  never frames a proposal in named-framework terms; named-lens framing
  is confined to the read-only weekly deep-dive, where it is labeled and
  hypothesis-framed.

### Consolidation (deferred / replaced)

* **Why deferred.** A "dream-state" daily/weekly/monthly/yearly
  cascade across `technical-spec.md` is too much surface area before
  the live loop is proven. The MVP replaces it with manual `/reflect`
  and an optional weekly cron in [`local-runtime.md`](local-runtime.md).
* **Pre-conditions.** A live loop with months of validated insights;
  a salience/activation/confidence model; a clear consent story for
  background mutation of insight status.
* **Forbidden until then.** No background process modifies insights.
  All status transitions happen through user response inside
  `/reflect`.

### Agent-Self (deferred — strongest gate)

* **Why deferred.** Drafting external messages is the highest-stakes
  feature; per
  [`product-principles.md`](product-principles.md) §7, it must land
  behind an explicit, user-visible approval gate.
* **Pre-conditions.** An approval-before-action UX; a draft store
  separate from `~/.lucid/insights/`; a per-recipient consent record;
  Safety/Consent rules updated to recognize a (still-not-autosend)
  Agent-Self contract.
* **Forbidden until then.** **Every external-action verb is blocked
  by Safety/Consent.** The MVP has no "send" code path at all.

## How contracts compose

The router in [`architecture.md`](architecture.md) §2 is the only place
that knows the order. Spelled out, the per-session sequence is:

```
/checkin
  ├── Intake.gather                          → bundled_text
  ├── storage.write_raw(bundled_text)        → raw_id
  ├── Structuring.extract(raw_record)        → processed_payload
  │                                            (people[].person_key are all null)
  ├── for each people[]:                       deterministic People routine;
  │     storage.update_person(...)             resolves or creates slug,
  │                                            back-fills person_key on payload
  ├── storage.write_processed(processed)     → processed_id
  │                                            (no person_key is null on disk)
  ├── Safety.evaluate(structuring_rendered)  → ack message (pass/rewrite/block)
  ├── Reflection.propose(processed,
  │                      recent_window,
  │                      rejected_shape_tags,
  │                      unanswered_shape_tags) → proposal | no_pattern | soft_contradiction
  │                                            (skipped entirely while the
  │                                             proposal pause is active — see §3)
  ├── Safety.evaluate(reflection_output)     → outbound message
  ├── (await user response)
  └── on accepted/nuanced:
        storage.write_insight(...)           with provenance
        (router asks the fixed rule prompt, once per insight — see §3;
         on answer: storage.set_insight_rule(...); on skip: nothing)
      on rejected:
        storage.append_rejected_proposal(...)
      on no answer:
        storage.append_unanswered_proposal(...)
```

`/log` skips Intake, Structuring, and Reflection at capture time:

```
/log <text>
  ├── storage.write_raw(text)                → raw_id
  └── Safety.evaluate(ack)                   → "saved as raw_<id>"
```

Structuring still runs over every raw entry — `/log` and `/closeout`
journal lines included — as a downstream pass at the next session or
scheduled run ([`scope.md`](scope.md) S-2); the resulting artifact
enters `recent_window` at the next `/checkin`. No proposal is generated
or delivered outside a `/checkin` session.

`/reflect` is read-and-ask only:

```
/reflect [gate]
  ├── storage.read_insights(window=7d)       → list
  │    (gate variant: status=accepted, cap=50 — see §3 "Gate recall")
  ├── (router handles empty case → most recent 2 + /log prompt,
  │    + the /checkin pointer line when unprocessed entries
  │      have accumulated — see §3)
  ├── Reflection.surface_for_recall(list)    → ordered_insights
  ├── Safety.evaluate(each surface_text)     → outbound messages
  ├── (gate variant only: router appends the deterministic panel
  │    numbers + any dominance line — fixed copy, not agent output)
  └── on each user response:
        storage.update_insight_status(...)
        storage.update_insight_rule_status(...)  (kept | lapsed | retired,
                                                  ruled insights only)
        storage.write_reflection(...)        (append-only per ISO week)
```

`/ask` is read-only — it never writes:

```
/ask <question>
  ├── storage.read_insights(status=accepted, cap=50)   → insights_slice
  ├── storage.read_reflections(cap=12)                 → reflections_slice
  ├── (router handles empty slice → returns "I don't have anything
  │    validated yet — try /checkin or /log first.")
  ├── Reflection.answer_grounded(question,
  │                              insights_slice,
  │                              reflections_slice)    → answer | insufficient
  └── Safety.evaluate(answer_text)                     → outbound message
```

Every arrow above is an explicit, named operation. There are no
implicit calls, no fallbacks across agents, and no shared mutable state.
That is the property that lets the agents be implemented one at a time
in the order [`claude-code-workflow.md`](claude-code-workflow.md)
prescribes.

## How a coding agent should consume this page

* **Read the cross-cutting rules first.** They are short and they
  apply everywhere.
* **Implement Safety/Consent before Reflection.** If Safety is wrong,
  Reflection's mistakes leak to the user.
* **Treat each contract's failure-handling section as part of the
  acceptance criteria.** "It works on the happy path" does not satisfy
  the contract.
* **Do not extend a contract without a doc edit.** If a feature needs a
  new field, the diff lands in this file before the code does. That is
  what `docs-first planning` in
  [`claude-code-workflow.md`](claude-code-workflow.md) means in
  practice.
* **Do not invent new agents.** New agents require a new contract on
  this page (with all six sections), an architecture diff
  ([`architecture.md`](architecture.md) §3), and an entry in the
  router plan ([`architecture.md`](architecture.md) §2). The deferred
  list above is the only place new contracts are pre-named for the
  MVP.
