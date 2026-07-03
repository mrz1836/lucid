# Lucid MVP — Claude Code Workflow

This page is the **build playbook** for whatever coding agent picks
Lucid up after the docs are approved. It is opinionated on purpose:
the MVP fails the moment a coding agent starts inventing product
decisions, fan-outs across files it should not touch, or plausible-but-
unverified scaffolding. The workflow below is what keeps that from
happening.

It assumes Claude Code as the primary coding agent harness, but every
rule generalizes to any well-behaved coding agent that respects
repo-local context files.

The companion docs are:

* [`README.md`](README.md) — the MVP entrypoint and steel thread.
* [`product-principles.md`](product-principles.md) — the constraints
  every change must satisfy.
* [`architecture.md`](architecture.md) — the module map.
* [`agent-contracts.md`](agent-contracts.md) — the per-agent contracts
  the build sequence implements.
* [`data-model.md`](data-model.md) — the on-disk shape.
* [`local-runtime.md`](local-runtime.md) — the harness story.
* [`scope.md`](scope.md) — the final
  build-ready scope (added in Phase 5).

## Cardinal rule: docs-first, not docs-after

Every change to Lucid lands as a doc diff first, then a code diff.
That order is the only thing keeping the MVP from quietly turning
into "whatever the coding agent felt like building".

* New command? Edit [`local-runtime.md`](local-runtime.md) and
  [`architecture.md`](architecture.md) §2 first; then the router code.
* New agent? Add a contract in
  [`agent-contracts.md`](agent-contracts.md) first; then the agent
  code.
* New field on a record? Edit
  [`data-model.md`](data-model.md) first; then the storage adapter.
* New voice rule? Edit
  [`product-principles.md`](product-principles.md) §6 and the phrase
  blocklist first; then the Safety/Consent prompt.

If the user asks for "just the code", point them back to this rule.
The docs are not bureaucracy — they are the only place the MVP
boundaries live.

## Repo-local context strategy

Coding agents read context files at the repo root before they look at
anything else. Lucid uses two short pointer files so agents always
land in the right doc set without ingesting the whole repo.

### `AGENTS.md` and `CLAUDE.md`

* **Both are pointers, not duplicates.** They name the canonical doc
  ([`README.md`](README.md)) and the canonical scope spec
  ([`scope.md`](scope.md)) and tell
  the agent to read those before answering.
* **Both stay short** (≤ ~60 lines). They contain orientation
  ("Lucid is a local-first inner-life companion; the MVP is a single
  steel thread"), the public-safe boundary, and the docs-first rule.
  Detail belongs in the linked docs.
* **Either is acceptable.** Some agents prefer `AGENTS.md`, others
  `CLAUDE.md`. Maintaining both is fine; their content should be
  near-identical.
* **Never copy the doc set into them.** A pointer that goes stale is a
  small annoyance; an out-of-date duplicate of `architecture.md` is a
  silent bug.

### What goes in the pointer file (suggested outline)

1. One-paragraph "what Lucid is" framing in the voice from
   [`product-principles.md`](product-principles.md) §6.
2. Public-safe boundary: no references to private projects, identities,
   or paths beyond `~/projects/lucid/` and `~/.lucid/`.
3. The docs-first rule from this page.
4. A short "before you suggest a change" checklist:
   * Read [`README.md`](README.md).
   * Read the relevant doc in `docs/mvp/`.
   * Confirm the change is in scope per
     [`README.md`](README.md) §"What this MVP is not".
   * Propose a doc diff before a code diff.
5. A short "what not to do" list:
   * Don't invent commands, agents, or fields.
   * Don't add cloud sync, telemetry, or external sends.
   * Don't read the user's full history; route through the storage
     adapter and only the slice the contract names.
   * Don't add tests/CI before the code they would gate exists.

The actual `AGENTS.md` / `CLAUDE.md` files are not created in this
phase unless they materially help the next handoff. Their **shape** is
specified here so the next contributor adds them in five minutes
without re-deriving the rules.

## Phase shape: small, verified, committed

The MVP is built as a sequence of small phases. Each phase has the
same shape:

1. **Doc diff (if any).** New behavior reflected in the relevant
   `docs/mvp/*.md` page first.
2. **One small code diff.** Implement only what the doc diff names.
   Resist drive-by refactors.
3. **Deterministic checks** (see below). They run locally and pass
   before the commit.
4. **One commit per phase.** Conventional Commits style, e.g.
   `feat(commands): add /log capture path` or
   `feat(agents): wire structuring extractor`.
5. **Verification step.** Manual or scripted, named in the phase. The
   phase is not done until verification passes.

A phase that does not fit on one screen of diff is two phases.
Splitting before commit is cheaper than untangling after.

### Commit messages

Commit messages should be small enough that a person reading
`git log --oneline` can reconstruct the build order without opening
diffs. Examples:

```
docs(mvp): add agent contracts page
feat(storage): implement write_raw and read_raw
feat(commands): wire /log to storage.write_raw
test(storage): cover write_raw idempotence
chore(scripts): add fixture-gen for synthetic raw entries
```

Keep one logical change per commit. If a commit message needs "and",
it is two commits.

## Deterministic scripts before clever agents

Anywhere the work is parsing, formatting, schema validation, date
handling, fixture generation, or link checking, **prefer a small
deterministic script** over an LLM call. Scripts are testable,
diffable, and free; LLM calls for these jobs are slower, less
reproducible, and usually wrong at the margins.

### Where scripts must own the work

* **Parsing.** YAML frontmatter, slash commands, date strings.
  Anything that round-trips through a known grammar.
* **Schema validation.** JSON for `processed/`, `lucid.json`, sessions;
  YAML frontmatter for `raw/`, `insights/`, `reflections/`. Every
  record schema in [`data-model.md`](data-model.md) gets a validator
  before the agent that writes it ships.
* **Date handling.** `recorded_at`, `occurred_at`, ISO weeks for
  `/reflect`, week-window boundaries. No LLM ever computes a
  timezone.
* **Fixture generation.** Synthetic raw entries, processed artifacts,
  insights, sessions, reflections — generated by a small `scripts/`
  helper that takes seeds and emits valid records. The fixtures match
  the schemas; tests run against them.
* **Link checks.** Markdown link integrity across `docs/mvp/*.md`,
  `docs/`, and the root `README.md`. A 30-line script is enough.
* **Public-boundary grep.** A script enforces that the Lucid repo
  never references private projects, identities, or paths outside
  `~/projects/lucid/` and `~/.lucid/`. Pattern list lives in
  [`product-principles.md`](product-principles.md) §10 and the
  Phase 5 verification block.
* **Phrase-blocklist grep.** The diagnostic-language list from
  [`product-principles.md`](product-principles.md) §6 is enforced by a
  grep script run against MVP docs and, once code exists, against
  agent prompts.

### Where LLM calls own the work

Only inside the four required-now agents in
[`agent-contracts.md`](agent-contracts.md): Intake, Structuring,
Reflection, and the rewrite path of Safety/Consent. Every other LLM
call is suspect.

If a contributor reaches for an LLM to "make the script smarter",
that is a signal to write the determination explicitly. Most of the
time the rule the model would discover is one regex.

## Slash-command-style workflows (after code exists)

Once a working `/log`, `/checkin`, and `/reflect` exist, repeated
build flows benefit from short, named, slash-command-style scripts
the coding agent can invoke. They are documented here so they are
named consistently across contributors.

Recommended commands once they have something to drive:

| Command | What it runs |
|---------|--------------|
| `lucid validate` | Schema validation + link check + public-boundary grep + phrase-blocklist grep across docs and code. Read-only. |
| `lucid fixture <kind>` | Emits a synthetic record (`raw`, `processed`, `insight`, `session`, `reflection`) to stdout. Used by tests and demos. |
| `lucid replay <raw-id>` | Re-runs Structuring against an existing raw entry and writes a new processed artifact. Idempotent. |
| `lucid lint` | Markdown formatting + heading-level checks across `docs/`. |
| `lucid status` | Walks `~/.lucid/` and prints counts (raw, processed, insights, sessions, reflections) plus any schema violations. Read-only. |

These commands are **not implemented in the docs phase**. They are
listed so the first build sequence has known names to grow into.

## Hooks and checks (after code exists)

Hooks and checks land **only** when there is real code for them to
gate. Adding fixtures, schema validators, or pre-commit hooks before
the storage adapter exists creates a maintenance burden with no
target.

The intended progression:

1. **Phase A — pure docs.** No checks beyond Markdown link integrity
   and the boundary/phrase greps.
2. **Phase B — first code (`storage.write_raw` + `/log`).** Add
   schema validators for `raw/` frontmatter and a unit test for
   `write_raw`. Add a pre-commit hook that runs `lucid validate` on
   changed docs and unit tests on changed code.
3. **Phase C — Structuring + Reflection + Safety.** Add validators
   for `processed/` and `insights/`. Add Safety/Consent fixture tests
   covering the example table in
   [`agent-contracts.md`](agent-contracts.md) §4. Add a
   "no diagnostic phrases in agent prompts" check.
4. **Phase D — `/reflect` and weekly recall.** Add ISO-week boundary
   tests. Add an end-to-end fixture test that captures→structures→
   proposes→accepts→recalls a synthetic flow.

Don't skip ahead. A hook that has no code to fail against will quietly
rot until someone disables it.

## Subagents: bounded research, not core product choices

Subagents (delegated coding agent runs) are useful for:

* **Bounded research.** "Find every place X is referenced; report
  back." Cap the report length.
* **Bounded review.** "Read this diff and look for unused exports /
  obvious schema drift." Cap the scope to one file or one diff.
* **Look-ups.** "Where is the recent-window default defined?"

Subagents are **not** useful for:

* Picking which command, agent, or field to add to the MVP.
* Deciding voice or scope.
* Resolving an ambiguity in
  [`product-principles.md`](product-principles.md) or
  [`agent-contracts.md`](agent-contracts.md). That is a doc edit, not
  a delegated decision.

Rule of thumb: if the answer is a fact about the codebase, a subagent
is fine. If the answer would change a contract, the human and the
docs decide; subagents do not.

## Sensitive fixtures and context bloat

The repo's fixtures must stay synthetic. Real user content has no
business in `~/projects/lucid/`.

* **Synthetic only.** Every example, fixture, transcript, and seed
  in the repo is invented. The synthetic transcript in
  [`steel-thread.md`](steel-thread.md) is the model.
* **No real names, places, dates, or identifiers.** Person slugs in
  fixtures are low-signal (`person_a-river`, `person_b-pine`).
* **No `~/.lucid/` paste-ins.** A contributor copying a real raw entry
  into a test is a near-miss; reviewers should reject it on sight.
  The repo's `.gitignore` covers `~/.lucid/` paths but the rule is
  social first, mechanical second.
* **Coding-agent context limits.** When a coding agent asks to read
  files, prefer narrow reads
  ([`agent-contracts.md`](agent-contracts.md) — context-slice gate).
  "Open the whole repo" is rarely the right answer. Most tasks need
  one or two files.
* **Don't paste private content into a prompt.** Even for debugging.
  The synthetic fixtures exist precisely so reproductions never
  require real entries.

These rules trace to
[`product-principles.md`](product-principles.md) §3 (local-first) and
§9 (synthetic examples).

## Verification expectations per phase

Every phase, doc or code, ends with explicit verification. The minimum
bar:

| Phase kind | Verification |
|------------|--------------|
| Docs-only | `git -C ~/projects/lucid status --short`; manual link check; boundary+phrase greps from [`product-principles.md`](product-principles.md) §10. |
| New schema | Schema validator runs on every record kind it covers; at least one synthetic fixture round-trips. |
| New command | Unit test for the router plan; integration test that the command writes the named record(s); user-visible ack matches the rule in [`local-runtime.md`](local-runtime.md) §"Command shape rules". |
| New agent | Contract section in [`agent-contracts.md`](agent-contracts.md) updated; happy-path test; failure-handling test for every clause in the contract; phrase-blocklist regression test. |
| New gate | Test for each row in [`architecture.md`](architecture.md) §6; explicit `block` and `pass` cases. |

A phase whose verification step is "looks good" is not done.

## First build sequence (after docs approval)

This is the **only** sanctioned ordering of the Mirror-thread build
phases after the docs land. Future tasks add to the end of this list;
nothing short-circuits the order — with one sanctioned interleave: the
Engine phases 8–10 ([`engine-module.md`](engine-module.md)) and the
observation phases 11–12
([`observations-module.md`](observations-module.md)) depend only on
phases 1–2 and may be built immediately after them, before phases 3–7.
[`scope.md`](scope.md) §9 recommends
exactly that (1, 2, 8, 9, 10, 11, 12, 3–7) so the chain is defended and
the body record accumulating weeks before the first pattern proposal.
The module phases follow every rule on this page (docs-first,
deterministic scripts — both modules are *entirely* deterministic
scripts — one commit per phase, verification per their module pages).
"After step 7 the steel thread is end-to-end" refers to the Mirror
thread; phases 8–12 are in-scope MVP work, not post-MVP.

1. **Scaffold `~/.lucid/`.** Create the layout from
   [`data-model.md`](data-model.md): `raw/`, `processed/`,
   `insights/`, `people/`, `sessions/`, `reflections/`, plus
   `lucid.json`. Write a small script that creates missing directories
   idempotently and seeds `lucid.json` with the schema in
   [`data-model.md`](data-model.md) §"`lucid.json`". Verification:
   the script is safe to re-run and `lucid.json` validates.

2. **Implement `/log`.** The simplest capture path:
   * Storage: `write_raw`, `read_raw`. Frontmatter validator.
   * Router: `/log <text>` plan from
     [`architecture.md`](architecture.md) §2.
   * Safety: pass the ack message through (`pass` decision only at
     this stage).
   * Verification: unit test for `write_raw` immutability; integration
     test that `/log "hello"` writes a single raw entry under
     `~/.lucid/raw/<YYYY>/<MM>/` and acknowledges with the entry id.

3. **Implement `/checkin`.** The guided variant:
   * Add Intake per [`agent-contracts.md`](agent-contracts.md) §1,
     including the 2–4 question cap, the `stop_reason` enum, and the
     bundled-text validator.
   * Router: `/checkin` plan from
     [`architecture.md`](architecture.md) §2 (capture only — no
     Structuring or Reflection yet).
   * Verification: integration test for a 2-question and a 4-question
     flow; a `user_exit` test; a malformed-LLM-response test that
     returns `user_exit` cleanly.

4. **Add the structuring pass.** Per
   [`agent-contracts.md`](agent-contracts.md) §2:
   * Storage: `write_processed`, `read_processed`. Processed-artifact
     JSON validator.
   * Structuring agent with extractive-only prompt.
   * People (extractive): `storage.update_person` for each person
     mention.
   * Verification: a synthetic raw entry produces the expected
     emotions/themes/people; an empty body produces empty arrays plus
     `notes: "raw body empty"`; a malformed LLM response produces
     empty arrays plus `notes: "structuring failed (parse)"`.

5. **Implement one insight validation flow.** Per
   [`agent-contracts.md`](agent-contracts.md) §3 and §4:
   * Reflection (per-session) with the `proposal | no_pattern |
     soft_contradiction` outcomes.
   * Safety/Consent with the pass/rewrite/block decisions.
   * Storage: `write_insight` (with provenance) and
     `append_rejected_proposal`.
   * Router: extend `/checkin` to chain Structuring → Reflection →
     Safety → user response.
   * Verification: happy path, rejected path, no-pattern-yet path
     from [`steel-thread.md`](steel-thread.md); phrase-blocklist
     regression suite for Safety; "rejected `shape_tag` is not
     re-proposed" test.

6. **Implement the weekly reflection stub.** Per
   [`agent-contracts.md`](agent-contracts.md) §3
   (`reflection.surface_for_recall`):
   * Storage: `read_insights(window=7d)`, `update_insight_status`,
     `write_reflection`.
   * Router: `/reflect` plan from
     [`architecture.md`](architecture.md) §2.
   * Verification: ISO-week boundary tests; empty-week fallback to
     "most recent two" plus a `/log` prompt; one round-trip test
     where confirming an insight stamps `last_confirmed_at`.

7. **Implement grounded `/ask`.** Per
   [`agent-contracts.md`](agent-contracts.md) §3
   (`reflection.answer_grounded`):
   * Storage: `read_insights(status=accepted, cap=50)`,
     `read_reflections(cap=12)`. Both already exist from earlier
     phases; no new ops needed.
   * Reflection: third sub-mode, with the `answer | insufficient`
     outcomes and the citations-must-be-in-slice rule.
   * Safety/Consent: extend the example fixture set with the three
     `/ask` rows from [`agent-contracts.md`](agent-contracts.md) §4
     (`pass`, `block` for advice, `block` for unverified citation).
   * Router: `/ask` plan from
     [`architecture.md`](architecture.md) §2 — read-only; no writes.
   * Verification: empty-store path returns "nothing validated yet";
     happy path returns an `answer` whose `citations[]` are all in
     the supplied slice; an LLM response that cites a fabricated id
     is caught by validation and Safety blocks the message.

After step 7, the steel thread in [`README.md`](README.md) is end-to-
end. Anything beyond it is post-MVP and starts with a doc diff.

## What success looks like at handoff

A future contributor — human or agent — should be able to open Lucid
and answer all of these from the docs alone:

* What is the MVP? — [`README.md`](README.md).
* What does the MVP refuse to be? — [`README.md`](README.md)
  §"What this MVP is not"; [`product-principles.md`](product-principles.md).
* What is the user loop? — [`steel-thread.md`](steel-thread.md).
* How does it run today? — [`local-runtime.md`](local-runtime.md).
* What are the modules? — [`architecture.md`](architecture.md).
* What lives on disk? — [`data-model.md`](data-model.md).
* What does each agent promise? — [`agent-contracts.md`](agent-contracts.md).
* How do I build it? — this page.
* What is the canonical scope? —
  [`scope.md`](scope.md).

If any of those answers requires reading code, the docs are wrong and
the next phase is a doc fix, not a code change.
