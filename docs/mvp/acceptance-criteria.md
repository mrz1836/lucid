# Lucid MVP — Acceptance Criteria

This page is the **per-phase pass/fail checklist** for the build
sequence in [`README.md`](README.md) §"Recommended first
implementation path" and
[`claude-code-workflow.md`](claude-code-workflow.md) §"First build
sequence". Every phase below has:

* **Setup** — the prerequisite state the phase assumes.
* **Test cases** — concrete inputs and expected outputs (3–6 per
  phase).
* **Verification commands** — exact shell or `jq` invocations a
  reviewer can run to confirm.
* **Definition of done** — the smallest set of checks that, if all
  pass, the phase is shippable.

This is a contract for "is Phase N done?". A phase whose verification
step is "looks good" is not done
([`claude-code-workflow.md`](claude-code-workflow.md) §"Verification
expectations per phase").

All test inputs are synthetic per
[`product-principles.md`](product-principles.md) §9.

## Phase 1 — Scaffold `~/.lucid/`

### Setup

* Repo cloned at `~/projects/lucid/`.
* Wordlist file present at
  `~/projects/lucid/data/person_keys_wordlist.txt`.
* `~/.lucid/` may or may not exist before the phase.

### Test cases

| # | Input | Expected output |
|---|-------|-----------------|
| 1.1 | First run of the scaffold script on a host where `~/.lucid/` does not exist | All seven directories created; `lucid.json` written with the schema in [`data-model.md`](data-model.md) §"`lucid.json`"; one `.keep` file under each directory proving the path is writable. |
| 1.2 | Second run on the same host | Idempotent — no errors, no overwrites, `lucid.json` unchanged. |
| 1.3 | Run on a host where `~/.lucid/raw/2026/05/` already has one existing entry | The existing entry is **not** modified; the scaffold only fills missing pieces. |
| 1.4 | `lucid.json` is hand-edited to set `recent_window: 999` (above the cap) | Next router boot clips to `recent_window_max: 14` and warns; `lucid.json` is rewritten with the clipped value. |

### Verification commands

```bash
# Layout exists
test -d ~/.lucid/raw       && test -d ~/.lucid/processed && \
test -d ~/.lucid/insights  && test -d ~/.lucid/people    && \
test -d ~/.lucid/sessions  && test -d ~/.lucid/reflections

# lucid.json validates
jq -e '.version == 1 and .recent_window_max == 14 and .ask_insights_cap == 50' \
   ~/.lucid/lucid.json

# Wordlist exists and has the expected size
test "$(wc -l < ~/projects/lucid/data/person_keys_wordlist.txt)" -eq 256
```

### Definition of done

All four test cases pass; the verification commands return success;
running the scaffold script twice in a row produces no diff.

## Phase 2 — `/log`

### Setup

* Phase 1 complete.
* Storage adapter `write_raw` and `read_raw` implemented.
* Router wired for `/log <text>`.
* Safety/Consent in `pass`-only mode (rewrite/block paths land in
  Phase 5 — the insight validation flow).

### Test cases

| # | Input | Expected output |
|---|-------|-----------------|
| 2.1 | `/log "Quiet day. Read for an hour."` | A new file under `~/.lucid/raw/2026/MM/raw_<id>.md` with valid frontmatter; ack message returns within 1 second; no `processed/` or `insights/` write. |
| 2.2 | `/log ""` (empty body) | Raw entry written with empty body; frontmatter still valid; per [`error-states.md`](error-states.md) §S-3 the response notes the empty body; no Structuring runs. |
| 2.3 | Two `/log` invocations in the same minute | Both files exist; the second has the `_SS` collision suffix per [`data-model.md`](data-model.md) §"Same-minute id collision rule". |
| 2.4 | Disk full (simulated) | Per [`error-states.md`](error-states.md) §St-1: an explicit error message; nothing written; user can retry after freeing space. |

### Verification commands

```bash
# Frontmatter validates
for f in ~/.lucid/raw/2026/*/*.md; do
  python -c "
import sys, yaml
with open('$f') as h: lines = h.read().split('---', 2)
front = yaml.safe_load(lines[1])
required = ['id','recorded_at','occurred_at','occurred_at_precision',
            'source','session_id','command','agent_versions','bootstrap']
missing = [k for k in required if k not in front]
sys.exit(0 if not missing else 1)
"
done

# Capture latency under one second (sample)
time printf '/log "synthetic ack-latency test"\n' | <test-harness>
```

### Definition of done

Test cases 2.1–2.4 pass; every raw frontmatter field listed in
[`data-model.md`](data-model.md) §"Field semantics" is present;
`/log` writes nothing under `processed/` or `insights/`.

## Phase 3 — `/checkin`

### Setup

* Phase 2 complete.
* Intake agent implemented per
  [`agent-contracts.md`](agent-contracts.md) §1.
* Bundled-text validator (≥ 90% user-authored tokens) implemented per
  [`product-principles.md`](product-principles.md) §6
  "Bundling rules".
* Structuring is **not** wired yet (lands in Phase 4).

### Test cases

| # | Input | Expected output |
|---|-------|-----------------|
| 3.1 | `/checkin` with a thin opening; user answers two questions | One raw entry written with `intake_questions[]` of length 2; `stop_reason: "satisfied"`; ack: "Saved as `raw_<id>`." |
| 3.2 | `/checkin` where Intake hits the 4-question cap | One raw entry written; `intake_questions[]` length 4; `stop_reason: "max_questions_reached"`; ack: "I've got what I need — saved as `raw_<id>`." |
| 3.3 | `/checkin` then user types `/cancel` after one question | No raw entry written; `stop_reason: "user_exit"`; ack: "Stopped — nothing saved." |
| 3.4 | `/checkin` then user types `/done` after two answers | Raw entry written with the partial bundle; ack: "Saved what we had as `raw_<id>`." |
| 3.5 | LLM returns malformed JSON twice | Per [`error-states.md`](error-states.md) §I-2: `stop_reason: "user_exit"`, no raw write, brief apology. |
| 3.6 | Bundle fails the 90% user-authored check | Validator rejects; Intake retries with stricter prompt; if still failing, downgrade per §I-2. |

### Verification commands

```bash
# Bundled-text check on the most recent /checkin entry
python scripts/check_bundle_authorship.py ~/.lucid/raw/<latest>.md
# Exit 0 iff token-overlap with frontmatter `intake_questions` answers ≥ 90%.

# Intake never asked > 4 questions
jq -e '.intake_questions | length <= 4' \
   <(yq --front-matter=extract eval ~/.lucid/raw/<latest>.md)
```

### Definition of done

Test cases 3.1–3.6 pass; no raw entry exists with `intake_questions`
length > 4 or length 1; ack copy matches the fixed strings in
[`agent-contracts.md`](agent-contracts.md) §1.

## Phase 4 — Structuring pass

### Setup

* Phase 3 complete.
* Storage adapter has `write_processed`, `read_processed`,
  `update_person`.
* Wordlist file at
  `~/projects/lucid/data/person_keys_wordlist.txt`.
* Structuring agent implemented per
  [`agent-contracts.md`](agent-contracts.md) §2.

### Test cases

| # | Input | Expected output |
|---|-------|-----------------|
| 4.1 | A raw entry mentioning "M." for the first time | A processed artifact with `produced_at` set, `agent_version` stamped; `people[]` contains `{display_name: "M.", person_key: "person_a-river" (or whatever the algorithm produces), first_mention: true}`; matching `~/.lucid/people/person_<...>.json` written by `storage.update_person`. |
| 4.2 | A raw entry mentioning "M." again | `first_mention: false`; `aka[]` on the people record updated if the spelling differs; `entry_refs[]` appended. |
| 4.3 | Run Structuring twice on the same raw entry | Idempotent — `processed/<id>.json` is overwritten with the same content (or content that differs only in `produced_at`); raw entry untouched. |
| 4.4 | Raw entry with empty body | Empty arrays + `notes: "raw body empty"`; no people record changes. |
| 4.5 | LLM returns malformed JSON twice | Empty arrays + `notes: "structuring failed (parse)"`. |
| 4.6 | LLM emits "anxious attachment" in `notes` | Validation rejects; retry; if persistent, downgrade to 4.5 path. |

### Verification commands

```bash
# Schema check on processed artifacts
for f in ~/.lucid/processed/*.json; do
  jq -e '
    .id == .entry_id and
    (.produced_at | test("^\\d{4}-\\d{2}-\\d{2}T")) and
    (.agent_version | test("^structuring-")) and
    (.people | all(has("display_name") and has("person_key") and has("first_mention"))) and
    ([.emotions, .themes, .people] | any(length > 0)
     or (.notes | length) > 0)
  ' "$f" > /dev/null || { echo "FAIL: $f"; exit 1; }
done

# No null person_key on disk
jq -e '.people | all(.person_key != null)' ~/.lucid/processed/*.json > /dev/null
```

### Definition of done

All test cases pass; `jq` schema check succeeds on every processed
artifact; running Structuring twice on the same raw entry produces a
diff only in `produced_at`; no `person_key` is `null` on disk.

## Phase 5 — Insight validation flow

### Setup

* Phase 4 complete.
* Reflection agent implemented per
  [`agent-contracts.md`](agent-contracts.md) §3 (`propose` and
  `surface_for_recall` sub-modes; `answer_grounded` lands in Phase 7).
* Safety/Consent agent implemented per
  [`agent-contracts.md`](agent-contracts.md) §4 (full
  pass/rewrite/block decisions).
* Storage adapter has `write_insight` (with provenance),
  `append_rejected_proposal`, `update_insight_status`.

### Test cases

| # | Input | Expected output |
|---|-------|-----------------|
| 5.1 | A `/checkin` whose recent window contains a recurring theme | Reflection returns `outcome: "proposal"` with valid `shape_tag` and `supporting_entry_ids`; user accepts; one insight written under `~/.lucid/insights/i_<...>.md` with full provenance and `status: accepted`. |
| 5.2 | Same as 5.1 but user says "Mostly yes, except…" | Insight written with `nuanced_from_proposal: true`; canonical statement is the user's refinement; `provenance.user_response_kind: "nuanced"`. |
| 5.3 | Same as 5.1 but user says "No — that doesn't fit." | No insight written; `processed/<id>.json` `rejected_proposals[]` appended with `shape_tag` and `user_response_text`. |
| 5.4 | A subsequent `/checkin` whose recent window includes the rejected `shape_tag` | Reflection proposes a different `shape_tag` (or `no_pattern`); validation prevents reuse of rejected tags. |
| 5.5 | Reflection emits a phrase-blocklist hit ("you always …") | Safety rewrites to "I noticed a possible pattern: …"; the user sees only the rewritten message; rewrite preserves `supporting_entry_ids`. |
| 5.6 | Reflection emits an external-action verb ("I'll send M. a follow-up") | Safety blocks; router fallback "I held that response — let me ask differently." surfaces; nothing stored. |
| 5.7 | A `/checkin` with `bootstrap_mode: true` | Reflection.propose is **not** invoked; capture-only ack. |

### Verification commands

```bash
# Every insight has full provenance
for f in ~/.lucid/insights/*.md; do
  python scripts/check_insight_provenance.py "$f" || exit 1
done

# Rejected shape_tags are honored
python scripts/check_no_rejected_shape_reuse.py \
  ~/.lucid/processed/ ~/.lucid/insights/

# Phrase blocklist regression — agent prompt files
grep -niE -f scripts/phrase_blocklist.regex ~/projects/lucid/prompts/*.md \
  && { echo "FAIL: blocklist hit in prompts"; exit 1; } || true
```

### Definition of done

All test cases pass; no insight is missing provenance; no rejected
`shape_tag` is re-proposed in the same window; Safety blocks every
external-action verb; phrase-blocklist regex returns no hits in any
prompt file.

## Phase 6 — Weekly recall (`/reflect`)

### Setup

* Phase 5 complete.
* Storage adapter has `read_insights(window=...)`,
  `update_insight_status`, `write_reflection`.
* Reflection's `surface_for_recall` sub-mode wired.
* ISO-week boundary helper implemented (no LLM does timezone math —
  [`claude-code-workflow.md`](claude-code-workflow.md) §"Deterministic
  scripts before clever agents").

### Test cases

| # | Input | Expected output |
|---|-------|-----------------|
| 6.1 | `/reflect` with three accepted insights from the past 7 days | Each surface_text is generated; user response per insight (confirm / soften / retire) updates `status_history[]`; `~/.lucid/reflections/reflection_<YYYY>_w<WW>.md` appended. |
| 6.2 | `/reflect` with zero insights from the past 7 days | Per [`error-states.md`](error-states.md) §R-7: surface most-recent-two regardless of age + `/log` prompt; no proposal generated. |
| 6.3 | `/reflect` with zero insights anywhere | Per §E-3: "Nothing validated yet — try `/checkin` first." No reflection record written. |
| 6.4 | `/reflect` invoked twice in the same ISO week | The second invocation appends to the same `reflection_<YYYY>_w<WW>.md` change log; does not duplicate the body summary. |
| 6.5 | LLM produces malformed `ordered_insights` | Per §R-8: verbatim insights surfaced with no novel framing. |

### Verification commands

```bash
# ISO-week id matches the timestamp window
python scripts/check_reflection_iso_week.py ~/.lucid/reflections/

# Status updates appear with provenance
for f in ~/.lucid/insights/*.md; do
  python scripts/check_status_history_monotonic.py "$f" || exit 1
done

# /reflect never invents a new insight id
python scripts/check_reflection_no_new_ids.py \
  ~/.lucid/insights/ ~/.lucid/reflections/
```

### Definition of done

All test cases pass; no `/reflect` invocation creates a new insight
file; status_history transitions are monotonic and provenance-stamped;
ISO-week ids match the window timestamps.

## Phase 7 — Grounded `/ask`

### Setup

* Phase 6 complete.
* Reflection's `answer_grounded` sub-mode wired per
  [`agent-contracts.md`](agent-contracts.md) §3.
* Router builds the `insights_slice` (cap 50) and `reflections_slice`
  (cap 12) deterministically.

### Test cases

| # | Input | Expected output |
|---|-------|-----------------|
| 7.1 | `/ask "what have I learned about how I act in groups?"` over a populated store | Response with `outcome: "answer"`; `citations[]` non-empty; every cited id is in the supplied slice. |
| 7.2 | `/ask` over an empty `insights/` and `reflections/` | `outcome: "insufficient"` without an LLM call; message points to `/checkin` or `/log`. |
| 7.3 | LLM returns an answer citing `i_2026_99_99_z` (not in slice) | Validation fires; Safety blocks per §Sf-7; user sees the fallback. |
| 7.4 | LLM returns advice ("you should journal more") | Safety blocks per §Sf-8; user sees the fallback. |
| 7.5 | A `/ask` invocation while another command is in flight in the same session | Reads serialize per §St-6; `~/.lucid/` is byte-identical before and after `/ask`. |
| 7.6 | LLM transport timeout | Per §N-2: one retry; if persists, §N-3 message; no partial output. |

### Verification commands

```bash
# /ask never writes
HASH_BEFORE=$(find ~/.lucid -type f | sort | xargs sha256sum | sha256sum)
<run /ask invocation>
HASH_AFTER=$(find ~/.lucid -type f | sort | xargs sha256sum | sha256sum)
test "$HASH_BEFORE" = "$HASH_AFTER"

# Citations subset of supplied slice (recorded in the session log)
python scripts/check_ask_citations_in_slice.py ~/.lucid/sessions/<latest>.json
```

### Definition of done

All test cases pass; `~/.lucid/` is byte-identical before and after
every `/ask`; every cited id in every `answer` outcome is in the
supplied slice; advice/recommendation phrasing is blocked.

## Cross-phase verification

Run after each phase lands.

| Check | Command |
|-------|---------|
| Public-boundary grep | Forbidden-term sweep for private integration names and private repo paths returns nothing. Use an escaped pattern that does not match its own documentation, e.g. `grep -R "z[a]i\|Z[a]i\|~/projects/z[a]i" ~/projects/lucid`. |
| Diagnostic-language grep | `grep -niE -f scripts/phrase_blocklist.regex ~/projects/lucid/docs ~/projects/lucid/specs ~/projects/lucid/README.md` — every hit is inside a "phrase to avoid" or "non-goal" block. |
| Link integrity | `python scripts/check_links.py docs/mvp/*.md specs/*.md` — every relative link resolves. |
| Synthetic-only fixtures | Manual review of any new transcript / fixture; no real names, dates, or identifiers. |
| Schema validators | Every record kind under `~/.lucid/` validates against its schema in [`data-model.md`](data-model.md). |

## Cross-references

* Phase ordering: [`README.md`](README.md) §"Recommended first
  implementation path" and
  [`claude-code-workflow.md`](claude-code-workflow.md) §"First build
  sequence" — these three pages must enumerate the same phases 1–7 in
  the same order. Phases 8–10 (the Engine module) are specified with
  their own acceptance criteria in
  [`engine-module.md`](engine-module.md); they depend only on phases
  1–2 and may run right after them
  ([`../../specs/mvp-scope.md`](../../specs/mvp-scope.md) §9).
* Failure modes referenced as `§I-1`, `§S-3`, etc. live in
  [`error-states.md`](error-states.md).
* Schemas referenced in `jq` checks live in
  [`data-model.md`](data-model.md).
* The phrase-blocklist regex lives in
  [`product-principles.md`](product-principles.md) §"Phrase blocklist
  (compiled regex)".
