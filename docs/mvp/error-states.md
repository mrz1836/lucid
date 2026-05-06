# Lucid MVP — Error States

This page enumerates every failure mode named in the doc set and
specifies, for each one, the trigger, the system behavior, the
user-visible message, the side effect on `~/.lucid/`, and the
recovery path. It is the unified table that
[`agent-contracts.md`](agent-contracts.md),
[`steel-thread.md`](steel-thread.md), and
[`local-runtime.md`](local-runtime.md) all reference but none own.

The rules below are binding. If a coding agent encounters a failure
mode not listed here, the correct response is a doc edit on this page
before any code change.

## Cross-cutting principles

* **Capture is honored before structure.** A failure downstream of
  `storage.write_raw` (Structuring, Reflection, Safety) never deletes
  the raw entry. The user's ground truth stays.
* **No silent state.** Every failure either surfaces a user-visible
  message or writes a record that names what went wrong (e.g.
  `notes: "structuring failed (parse)"`). There is no quietly
  swallowed error.
* **Hypothesis framing survives errors.** Fallback messages are calm
  and honest. They do not perform empathy and do not invent a status
  the system did not earn ("I held that response — let me ask
  differently.", not "Sorry, my brain is broken!").
* **No external send is ever a recovery option.** Failure recovery
  never escalates to a webhook, email, or DM. Lucid is local-first
  per [`product-principles.md`](product-principles.md) §3 and §7.

## Per-agent failures

### Intake (`/checkin`)

| # | Trigger | System behavior | User-visible message | Disk side effect | Recovery |
|---|---------|-----------------|----------------------|------------------|----------|
| I-1 | LLM returns malformed payload (1st attempt) | Retry once with a stricter prompt. | (none — retry is silent) | (none) | If the retry succeeds, continue. |
| I-2 | LLM returns malformed payload (2nd attempt) | Return `stop_reason: "user_exit"` with `answers: []`; surface a brief apology. | "I held that — let me try a different opening another time. Nothing saved." | None. (No `raw/` write because there is no bundle.) | User can rerun `/checkin`. |
| I-3 | User types `/done` or `/cancel` mid-question | Stop immediately; return `stop_reason: "user_exit"` with whatever `answers` exist so far. | Router writes a partial bundle if `answers` is non-empty (*"Saved what we had as `raw_<id>`."*); writes nothing if empty (*"Stopped — nothing saved."*). | Conditional `raw/<id>.md`. | User can `/log` or `/checkin` again. |
| I-4 | User goes silent for > harness timeout mid-question | Same as I-3 (`user_exit`). The router does not re-prompt. | Same as I-3. | Same as I-3. | User reopens the thread and runs the command again. |
| I-5 | `max_questions_reached` (4th answer received) | Return `stop_reason: "max_questions_reached"`, full bundle. | "I've got what I need — saved as `raw_<id>`." (fixed copy from [`agent-contracts.md`](agent-contracts.md) §1) | `raw/<id>.md` written. | Continue downstream pipeline as normal. |
| I-6 | Bundle fails the ≥ 90% user-authored token check | Reject the bundle, retry the LLM call with a stricter prompt. | (none on the first reject — the user only sees the eventual successful bundle or the I-2 fallback) | (none until success) | Treat as I-1 / I-2. |

### Structuring

| # | Trigger | System behavior | User-visible message | Disk side effect | Recovery |
|---|---------|-----------------|----------------------|------------------|----------|
| S-1 | LLM returns malformed JSON (1st attempt) | Retry once with a stricter prompt. | (none) | (none) | If retry succeeds, continue. |
| S-2 | LLM returns malformed JSON (2nd attempt) | Write a processed artifact with empty arrays and `notes: "structuring failed (parse)"`. | (none — Structuring is silent to the user; the next ack covers capture) | `processed/<id>.json` with empty arrays. | Reflection sees the empty artifact and returns `no_pattern`. User-driven `lucid replay <id>` (post-MVP) re-runs Structuring with the same raw. |
| S-3 | Raw entry body is empty (e.g. `/log ""`) | Write a processed artifact with empty arrays and `notes: "raw body empty"`. No retry. | "Saved as `raw_<id>` — looks like the body was empty, so I'll skip looking for patterns." | `raw/<id>.md` (with empty body) and `processed/<id>.json` (empty + notes). | User can run `/log` again with content. |
| S-4 | `notes` contains a phrase-blocklist hit (regression) | Validation rule fires before write; treat as S-1 (retry). | (none) | (none) | If the retry still produces a hit, downgrade to S-2 path. |
| S-5 | `storage.update_person` fails for a mention (e.g. wordlist file missing) | Abort the write_processed call; log internally; surface a fallback ack. | "Saved as `raw_<id>` — I had trouble organizing the people in this one; I'll catch up next time." | `raw/<id>.md` written; **no** `processed/<id>.json`. | Once the wordlist is restored, re-run Structuring (post-MVP `lucid replay`). |

### Reflection — `propose` (`/checkin`)

| # | Trigger | System behavior | User-visible message | Disk side effect | Recovery |
|---|---------|-----------------|----------------------|------------------|----------|
| R-1 | LLM returns malformed output (1st attempt) | Retry once. | (none) | (none) | If retry succeeds, continue. |
| R-2 | LLM returns malformed output (2nd attempt) | Return `outcome: "no_pattern"` with a generic message. | "I don't have enough yet to say anything useful — want to keep going?" | None. | User can `/log` more or wait for the next `/checkin`. |
| R-3 | `recent_window` is empty (first or near-first entry) | Return `outcome: "no_pattern"` without an LLM call. | Same as R-2. | None. | Same as R-2. |
| R-4 | Current artifact has empty `emotions`, `themes`, **and** `people` | Return `outcome: "no_pattern"` without an LLM call. | Same as R-2. | None. | Same as R-2. |
| R-5 | Proposed `shape_tag` is in `rejected_shape_tags` | Validation fires; retry with the rejected tag included in the prompt's "do not propose" list. | (none) | (none) | If the retry still proposes a rejected shape, downgrade to R-2 (`no_pattern`). |
| R-6 | Bootstrap mode active (`bootstrap: true`) | Router suppresses Reflection entirely; if Reflection is somehow invoked, it must return `no_pattern` immediately. | (none — capture ack only, no proposal turn) | None. | After `/bootstrap done`, the next `/checkin` resumes proposals. |

### Reflection — `surface_for_recall` (`/reflect`)

| # | Trigger | System behavior | User-visible message | Disk side effect | Recovery |
|---|---------|-----------------|----------------------|------------------|----------|
| R-7 | `insights_window` is empty for the past 7 days | Router substitutes the two most recent insights regardless of age and adds a `/log` prompt. | "Quiet week — nothing landed as a validated insight in the last seven days. Want me to surface the two most recent ones from before that?" | None until the user responds. | User confirms or declines. |
| R-8 | LLM returns malformed `ordered_insights` | Retry once. If still malformed, fall back to surfacing each insight verbatim ("Earlier you saved: '<canonical statement>'. Still resonating?"). | Verbatim insights, no novel framing. | Same as the happy path on response. | (none — the verbatim path is itself the recovery) |
| R-9 | All validated insights have already been confirmed/softened/retired this week | Treat the user response (if any) as idempotent — `status_history` accepts duplicate confirms within a week. | "I noticed I've already heard from you on these this week — anything you want to revisit?" | (potential append to `status_history` if the user re-confirms) | (none — graceful) |

### Reflection — `answer_grounded` (`/ask`)

| # | Trigger | System behavior | User-visible message | Disk side effect | Recovery |
|---|---------|-----------------|----------------------|------------------|----------|
| R-10 | Both `insights_slice` and `reflections_slice` empty | Return `outcome: "insufficient"` without an LLM call. | "I don't have anything validated yet — try `/checkin` or `/log` first." | None (`/ask` never writes). | User can `/log` or `/checkin`. |
| R-11 | LLM returns malformed output (1st attempt) | Retry once. | (none) | (none) | If retry succeeds, continue. |
| R-12 | LLM returns malformed output (2nd attempt) | Return `outcome: "insufficient"` with a short fallback. | "I had trouble pulling that together — want to ask it differently?" | None. | User retries `/ask`. |
| R-13 | Output cites an id not in the supplied slice | Validation fires; retry once with the slice ids restated in the prompt. If still invalid, Safety/Consent blocks the answer (see Safety section below). | "I held that response — let me ask differently." | None. | User retries `/ask`. |
| R-14 | Output contains advice / recommendation / therapeutic framing | Safety/Consent blocks. | Same as R-13. | None. | Same. |

### Safety / Consent

| # | Trigger | System behavior | User-visible message | Disk side effect | Recovery |
|---|---------|-----------------|----------------------|------------------|----------|
| Sf-1 | Phrase-blocklist regex hit on a candidate | `decision: "rewrite"` with the diff applied. One LLM call (rewrite-only) is allowed per [`agent-contracts.md`](agent-contracts.md) §4. | The rewritten message (preserves `supporting_entry_ids`). | None directly; the rewritten text is what the upstream surface emits. | (none — the rewrite is the recovery) |
| Sf-2 | External-action verb detected (`send`, `dm`, `email`, `post`, `schedule`, `call`, `notify`, `webhook`, `publish`, `tweet`) | `decision: "block"` with `reason_code: "external_action_attempt"`. Never rewrites — block is the only valid decision. | "I held that response — let me ask differently." | None. | User retries the underlying command if appropriate. |
| Sf-3 | Diagnostic vocabulary on a `propose_pattern` candidate | `decision: "block"` with `reason_code: "diagnostic_language"`. | Same as Sf-2. | The proposal is **not** stored; no insight, no rejection, no `rejected_proposals[]` append. | User can `/checkin` again later. |
| Sf-4 | LLM call for a `rewrite` decision fails | Downgrade to `block`. A failed Safety/Consent **never** falls through to `pass`. | Same as Sf-2. | None. | (none) |
| Sf-5 | Candidate message is empty | `decision: "block"` with `reason_code: "ok"` (nothing to say). | (silence — the router does not surface an empty message) | None. | (none) |
| Sf-6 | `propose_pattern` candidate with `bootstrap_mode == true` | `decision: "block"` with `reason_code: "scope_violation"`. | (none if Reflection should not have been invoked at all; otherwise Sf-2 fallback) | None. | (none) |
| Sf-7 | `answer` candidate cites an id not in the slice | `decision: "block"` with `reason_code: "unverified_claim"`. | Same as Sf-2. | None. | User retries `/ask`. |
| Sf-8 | `answer` candidate gives advice / recommendation | `decision: "block"` with `reason_code: "agent_self_attempt"`. | Same as Sf-2. | None. | (none) |

## Storage failures

| # | Trigger | System behavior | User-visible message | Disk side effect | Recovery |
|---|---------|-----------------|----------------------|------------------|----------|
| St-1 | `write_raw` fails — disk full or permission denied | Surface the failure to the user immediately; do not retry silently. | "I couldn't write to `~/.lucid/raw/` — check disk space and permissions, then try again. Nothing was saved." | None. | User clears the underlying issue and retries. |
| St-2 | Same-minute id collision (`raw_YYYY_MM_DD_HH_MM` already exists) | Append `_SS` (zero-padded seconds) and retry per [`data-model.md`](data-model.md) §"Same-minute id collision rule". If `_SS` collides too, append a small monotonic counter `_SSS`. | (none — the user sees the final id in the ack) | `raw/<id>_SS.md` (or `_SSS`). | (none — automatic) |
| St-3 | `write_processed` fails after Structuring succeeded | Treat the raw entry as captured but unprocessed. The next Structuring run (post-MVP `lucid replay`) is the recovery. | "Saved as `raw_<id>` — I'll catch up on the rest later." | `raw/<id>.md` exists; `processed/<id>.json` does not. | Manual replay or next session retry. |
| St-4 | `update_person` fails (wordlist missing, permission) | Abort `write_processed`; surface fallback per S-5. | (see S-5) | (see S-5) | Restore wordlist; replay. |
| St-5 | `write_insight` lacks required provenance | Validator raises before write. The router then treats Reflection's response as a generic acknowledgement (no insight stored). | "I held that — let me ask the validation question again." | None. | Router re-prompts the user. |
| St-6 | Concurrent writes within the same session/thread | Storage adapter serializes per-session writes; second writer waits. If the wait exceeds the harness timeout, the second write is rejected with St-1 messaging. | (typically invisible) | First write lands; second waits or rejects. | Retry. |

## Network / LLM transport failures

These are independent of payload shape — the call itself fails.

| # | Trigger | System behavior | User-visible message | Disk side effect | Recovery |
|---|---------|-----------------|----------------------|------------------|----------|
| N-1 | Rate limit (HTTP 429 or equivalent) | Wait per the provider's `Retry-After` if given, else 5 seconds; retry once. If the retry also fails, treat as N-3. | (none on the first retry) | (none) | Automatic retry. |
| N-2 | Timeout (no response within agent-specific budget) | Retry once. Each agent has its own budget — Intake: 20s, Structuring: 30s, Reflection: 30s, Safety: 10s. | (none on the first retry) | (none) | Automatic retry. |
| N-3 | Transport failure persists after one retry | Surface a transient-failure message; do not retry again automatically. | "I'm having trouble reaching my model right now — want to try again?" | (none) | User retries the command. |
| N-4 | Provider returns an empty response body | Treat as malformed payload (see I-1, S-1, R-1, R-11). | (per-agent fallback) | (per-agent) | Automatic retry once. |

## Empty-state and first-run paths

These are not failures — they are valid states the system handles
explicitly so the user never sees an empty UI.

| # | State | System behavior | User-visible message | Disk side effect |
|---|-------|-----------------|----------------------|------------------|
| E-1 | First-ever entry (no recent window) | Reflection returns `no_pattern` without an LLM call (R-3). | "Saved as `raw_<id>`. I don't have enough yet to look for patterns — keep going?" | `raw/<id>.md`, `processed/<id>.json` only. |
| E-2 | `/reflect` with 0 validated insights this week | Router falls back to most recent two regardless of age + `/log` prompt (R-7). | (see Voice across paths in [`steel-thread.md`](steel-thread.md)) | None until user responds. |
| E-3 | `/reflect` with 0 validated insights anywhere | Router returns "Nothing validated yet — try `/checkin` first." | (as quoted) | None. |
| E-4 | `/ask` over an empty store | Reflection returns `outcome: "insufficient"` without an LLM call (R-10). | "I don't have anything validated yet — try `/checkin` or `/log` first." | None. |
| E-5 | `/bootstrap` mode active | Reflection.propose suppressed; ack-only at capture. | "Saved as `raw_<id>` (bootstrap)." | `raw/<id>.md` with `bootstrap: true`. |
| E-6 | `/bootstrap done` exit | `lucid.json` flips `bootstrap_mode: false`. **No** consolidation pass runs (deferred). | "Done. Pattern proposals will resume on the next `/checkin`." | `lucid.json` updated. |

## Cross-references

* Per-agent failure handling lives in
  [`agent-contracts.md`](agent-contracts.md) §1–§4. This page is the
  user-facing companion to that contract surface.
* Voice for fallback messages is constrained by
  [`product-principles.md`](product-principles.md) §6 and the phrase
  blocklist regex therein. Any new fallback string here must pass the
  blocklist.
* Empty-state and first-run paths are also illustrated in
  [`steel-thread.md`](steel-thread.md) §"Voice across paths".
* Storage failure semantics map to the named ops in
  [`architecture.md`](architecture.md) §4.

If a failure mode appears in code that is not on this page, the next
phase is a doc edit, not a code change.
