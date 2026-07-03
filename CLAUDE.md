# Lucid — agent orientation

Lucid is a local-first personal operating system with two cooperating
subsystems: the **Mirror** (capture, patterns, self-understanding — never
enforcement) and the **Engine** (committed daily practices with real
accountability — never content). Both write one user-owned, append-only
Ledger. The voice is trusted advisor: warm, honest, humble about
certainty — hypothesis language only, never diagnosis.

## Read before acting

1. [`specs/mvp-scope.md`](specs/mvp-scope.md) — the canonical build scope.
2. [`docs/mvp/README.md`](docs/mvp/README.md) — the MVP doc set entrypoint.
3. [`docs/architecture.md`](docs/architecture.md) — the system design and
   its ten principles (P1–P10).
4. The doc in `docs/mvp/` relevant to your change.

## Hard rules

- **Docs-first.** Every behavior change lands as a doc diff before a code
  diff ([`docs/mvp/claude-code-workflow.md`](docs/mvp/claude-code-workflow.md)).
- **`personal/` is private.** It is gitignored and must never enter any
  commit, example, fixture, or shared context. Do not read it unless the
  user explicitly directs you to.
- **Public-safe boundary.** No private project names, identities, or
  paths beyond `~/projects/lucid/` and `~/.lucid/`. Synthetic fixtures
  only.
- **Sanctuary (architecture P3).** No agent reads `~/.lucid/engine/`;
  the Engine module invokes no agent and never touches reflective
  content. Do not weaken this in either direction.
- **No external sends** beyond the three pre-committed Engine templates
  (bell, L1 nudge, L2 witness) specified in
  [`docs/mvp/engine-module.md`](docs/mvp/engine-module.md).

## Before you suggest a change

- Confirm it is in scope per [`docs/mvp/README.md`](docs/mvp/README.md)
  §"What this MVP is not" and `specs/mvp-scope.md` §7.
- Prefer a deterministic script over an LLM call for parsing, schema
  validation, dates, fixtures, and greps.
- Work against the acceptance criteria
  ([`docs/mvp/acceptance-criteria.md`](docs/mvp/acceptance-criteria.md),
  [`docs/mvp/engine-module.md`](docs/mvp/engine-module.md)); consult
  [`docs/mvp/error-states.md`](docs/mvp/error-states.md) before inventing
  a failure path.

## Do not

- Invent commands, agents, or record fields.
- Add cloud sync, telemetry, analytics, or new send paths.
- Read the user's full history; route through the storage adapter with
  only the slice a contract names.
- Add hooks/CI before the code they would gate exists.
