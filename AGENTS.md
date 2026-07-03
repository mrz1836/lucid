# Lucid — agent orientation (mirror of CLAUDE.md — keep identical)

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
- **Sanctuary (architecture P3).** No agent reads `~/.lucid/engine/`,
  `~/.lucid/observations/`, or `~/.lucid/registries/`; the Engine and
  observation modules invoke no agent. Observations are inventory,
  never obligation — no streaks, scores, or targets on them, ever.
  Do not weaken any of this in either direction.
- **No autonomous messages** beyond the pre-committed Engine templates
  (bell, L1 nudge, L2 witness + monthly heartbeat —
  [`docs/mvp/engine-module.md`](docs/mvp/engine-module.md)), and **no
  outbound fetches** beyond opted-in enrichers through the single
  audited `fetch_enrichment` op ([`docs/observations.md`](docs/observations.md) §5:
  quantized coordinates + dates to pinned keyless endpoints, nothing
  else). Fetches are not sends; neither ceiling is a precedent.

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
