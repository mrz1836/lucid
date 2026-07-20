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
before any code change. **Scope note:** this page owns the
Mirror-thread failure modes; the deterministic modules own theirs in
the same table format —
[`engine-module.md`](engine-module.md) §"Error states" and
[`observations-module.md`](observations-module.md) §"Error states".
The cross-cutting principles below bind all three tables.

## Cross-cutting principles

* **Capture is honored before structure.** A failure downstream of
  `storage.write_raw` (Structuring, Reflection, Safety) never deletes
  the raw entry. The user's ground truth stays.
* **No silent state.** Every failure either surfaces a user-visible
  message or writes a record that names what went wrong (e.g.
  `notes: "structuring failed (parse)"`). There is no quietly
  swallowed error. (Enricher fetch failures satisfy this by writing
  to the outbound audit log rather than messaging the user —
  enrichment is never load-bearing, and the log is the record.)
* **Hypothesis framing survives errors.** Fallback messages are calm
  and honest. They do not perform empathy and do not invent a status
  the system did not earn ("I held that response — let me ask
  differently.", not "Sorry, my brain is broken!").
* **No external send is ever a recovery option.** Failure recovery
  never escalates to a webhook, email, or DM. Lucid is local-first
  per [`product-principles.md`](product-principles.md) §3 and §7.
  (The Engine's three pre-committed template sends are *designed*
  behavior with their own error table in
  [`engine-module.md`](engine-module.md) — they are never used as a
  failure-recovery path for anything on this page, and no failure
  here may trigger one.)

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
| R-15 | A rule question gets an answer that maps to none of kept / lapsed / retire | Record nothing on the rule; the insight-status part of the response is processed independently; no re-prompt. | (none — the conversation simply moves on) | No `rule_history[]` append. | The rule resurfaces at the next recall as usual. |

### Reflection — `answer_grounded` (`/ask`)

| # | Trigger | System behavior | User-visible message | Disk side effect | Recovery |
|---|---------|-----------------|----------------------|------------------|----------|
| R-10 | Both `insights_slice` and `reflections_slice` empty | Return `outcome: "insufficient"` without an LLM call. | "I don't have anything validated yet — try `/checkin` or `/log` first." | None (`/ask` never writes). | User can `/log` or `/checkin`. |
| R-11 | LLM returns malformed output (1st attempt) | Retry once. | (none) | (none) | If retry succeeds, continue. |
| R-12 | LLM returns malformed output (2nd attempt) | Return `outcome: "insufficient"` with a short fallback. | "I had trouble pulling that together — want to ask it differently?" | None. | User retries `/ask`. |
| R-13 | Output cites an id not in the supplied slice | Validation fires; retry once with the slice ids restated in the prompt. If still invalid, Safety/Consent blocks the answer (see Safety section below). | "I held that response — let me ask differently." | None. | User retries `/ask`. |
| R-14 | Output contains advice / recommendation / therapeutic framing | Safety/Consent blocks. | Same as R-13. | None. | Same. |

### `/person` (deterministic view — no agent, no LLM)

| # | Trigger | System behavior | User-visible message | Disk side effect | Recovery |
|---|---------|-----------------|----------------------|------------------|----------|
| P-1 | `/person <name>` matches no `people/` record (by `display_name` or `aka[]`) | Return the empty state; never guess. | "No one by that name yet — people appear here as you mention them." | None (read-only). | User checks spelling or mentions the person in an entry. |
| P-2 | `<name>` matches more than one person record | List the candidates by display name and first-seen date; render nothing else. | "That matches more than one person — which did you mean: <list>?" | None. | User re-runs with a disambiguated name. |
| P-3 | The matched person is named in the off-limits registry | Render the view with a standing header note; inference-derived material is absent by construction (the person was redacted from every agent slice, so no insight or proposal references them). | "<name> is off-limits to inference — what follows is your raw record only: mentions and dates, nothing derived." | None. | (none — this is designed behavior, not a failure) |



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

### Companion (daily companion — Mirror-side, model-allowed)

The companion's full degrade layering — provider-unreachable fallback,
host-asleep catch-up, idempotency, read-back, and the loud total-miss alert —
lives in [`../usage/companion.md`](../usage/companion.md) §"When things go wrong".
The rows below record the degrade paths this page owns: the ones that keep an
enrichment or a slot from failing the life-critical daily send. The dividing
principle is deterministic — **enrichment reads degrade quietly; the prompt,
verdict, and live-number reads stay loud** — so a message is never silently
half-built.

| # | Trigger | System behavior | User-visible message | Disk side effect | Recovery |
|---|---------|-----------------|----------------------|------------------|----------|
| C-1 | A routine path (`morning_routine` / `night_routine`) is set but missing or unreadable | Non-fatal: omit the routine grounding from the compose context and record the miss; the message still composes and sends. Enrichment never kills the daily send. | (none — the message sends without the routine grounding) | None. | Fix or clear the routine path; the next window picks it up. |
| C-2 | The recent-observation read fails | Same non-fatal degrade: omit the affected context sections and compose from the status panel + slots. A dry run surfaces the omission so it is never invisible. | (none) | None. | Transient reads recover next window; a persistent failure surfaces in `scheduler status`. |
| C-3 | Model reply is missing the slot delimiters, or returns no usable text | Tolerant parse: a reply with prose but no `%%INTERPRETATION%%` delimiter becomes the whole interpretation slot with no actions; a reply with no usable text takes the deterministic-scaffold fallback (panel + sections + deterministic interpretation + deterministic action). Still a valid, well-formed scaffold. | The rendered scaffold. | None (or the per-window receipt on delivery). | (none — the fallback is the recovery) |
| C-4 | Ledger context older than the staleness threshold | Never silently mixed in: every Ledger-derived line carries an "as logged &lt;date&gt;" stamp, and a section whose newest event is older than the threshold (default 2 days) gets a `stale` flag on its meta. Live Engine numbers are always current and carry no stamp. | The scaffold with the freshness label. | None. | (none — labeling is the designed behavior) |
| C-5 | A prompt file, the tripwire verdict, or a live-numbers read fails | Loud, unlike the enrichment reads above: the compose returns an error rather than an empty or half-built send, so the total-miss alert path (companion.md) fires. | The loud total-miss alert. | None. | Restore the file / projection; rerun the window or wait for the next fire. |

### Workout module (config-gated — Mirror-side, model-allowed)

The workout module's full degrade table lives in
[`workout-module.md`](workout-module.md) §"Error states"; the rows below
record it in this page's unified format. The dividing principle is the
companion's — **enrichment reads degrade quietly; the pick, the safety
line, and the live-number reads stay loud** — so a recommendation is never
silently half-built and the daily slot never falls silent. Capture always
honors the drop first (§0, P10).

| # | Trigger | System behavior | User-visible message | Disk side effect | Recovery |
|---|---------|-----------------|----------------------|------------------|----------|
| W-1 | `workout.program` path missing/unreadable | Degrade to "no program": render an honest empty recommendation with the safety line, never a crash. | The empty-recommendation scaffold. | None. | Fix the program path; the next fire picks it up. |
| W-2 | Recent-observation read fails | Non-fatal: fall to the plain-calendar path (the recommender's missing-data rule); the message still composes. | (none) | None. | Transient reads recover next window. |
| W-3 | Workout extraction returns malformed fields | Retry once stricter, then degrade: store the drop as a `workout` event with `payload.parse: "partial"` and the verbatim note. | "Logged." (with the id) | Event written (partial). | The structured `lucid workout log` flag form is the precise path. |
| W-4 | Out-of-range scale (`rpe` / `pain` / `soreness` beyond bounds) | Partial path — stored with the invoked kind, never silently clamped. | (ack, id) | Event written (partial). | (none — automatic) |
| W-5 | Disabled kind used (`workout` / `body_state` not in `kinds_enabled`) | Reject with the enable hint, exactly like every observation kind. | "`workout` isn't enabled — add it to observations/config.json." | None. | Enable the kind. |
| W-6 | Provider unreachable at the slot / on-demand | Deterministic `Render` of the already-decided recommendation; only the model's warmth is lost — the pick and the safety line stand. | The deterministic scaffold. | None (or the slot receipt on delivery). | (none — the fallback is the recovery) |
| W-7 | Slot double-fire on a retry | Receipt idempotency (`ReadCompanionReceipt("workout")`): a retry whose message still reads back is skipped. | (none) | Per-window receipt. | (none — automatic) |
| W-8 | Host asleep past the slot cutoff | Bounded catch-up with a `(late)` note within the window; past the cutoff the send is skipped and the miss is alerted — never a stale midday message hours late. | The late-noted message, or the miss alert. | None. | (none — designed) |
| W-9 | Total miss (compose / deliver / read-back fails) | Loud best-effort alert to the user channel, then a loud job error into the supervised log. Silence is the one outcome the slot never produces. | The loud miss alert. | None. | Restore the input; rerun the window or wait for the next fire. |

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
