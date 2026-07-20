# Lucid — agent orientation

<!-- AGENTS.md and CLAUDE.md are kept byte-identical — edit one, copy to the other. -->

Lucid is a local-first personal operating system with two cooperating
subsystems: the **Mirror** (capture, patterns, self-understanding — never
enforcement) and the **Engine** (committed daily practices with real
accountability — never content). Both write one user-owned, append-only
Ledger. The voice is trusted advisor: warm, honest, humble about certainty —
hypothesis language only, never diagnosis. Local-first, built to outlive its
tools (P6): plain user-owned files; the AI is a stateless analyst, never the
system of record.

This file is **product orientation**. Engineering standards — Go style,
testing, commits, PRs, releases — live in
[`.github/tech-conventions/`](.github/tech-conventions/README.md);
`.github/**` re-syncs weekly from an upstream framework and is **not authored
here**, so never edit it to change Lucid.

## Read before acting

In order:

1. [`docs/mvp/scope.md`](docs/mvp/scope.md) — the canonical build scope.
2. [`docs/mvp/README.md`](docs/mvp/README.md) — the MVP doc set entrypoint.
3. [`docs/architecture.md`](docs/architecture.md) — the system design and its
   ten principles (P1–P10).
4. The doc in [`docs/mvp/`](docs/mvp/) relevant to your change.

**Docs-first:** every behavior change lands as a doc diff before a code diff
([`claude-code-workflow.md`](docs/mvp/claude-code-workflow.md)).

**Precedence when sources conflict:** on *scope*, `architecture.md` and
`engine-module.md` win ([`scope.md`](docs/mvp/scope.md) source table); on
*conventions*, `docs/mvp/` wins; a green guard test or `lucid validate` is the
floor — if a doc and a passing test disagree, stop and surface it rather than
"fixing" code to match a stale doc.

## If you're about to…

| …do this | Read first | Touch |
|----------|------------|-------|
| add/change a CLI verb | [`agent-contracts.md`](docs/mvp/agent-contracts.md), [ADR-0007](docs/adr/0007-cli-conventions.md) | `internal/cli` + `internal/router` |
| change what leaves the machine (a send) | [`engine-module.md`](docs/mvp/engine-module.md) | `internal/scheduler` + `internal/engine/templates` — and confirm it's a pre-committed template |
| change `~/.lucid/` layout or a record | [`data-model.md`](docs/mvp/data-model.md) | `internal/storage` **only** |
| add/adjust enrichment or any network call | [`observations.md`](docs/observations.md) §5, [ADR-0006](docs/adr/0006-model-access.md) | `internal/storage/fetch_enrichment.go` + `internal/observations` |
| change model/agent behavior | [`agent-contracts.md`](docs/mvp/agent-contracts.md) | `internal/agents/*` + `internal/provider` (never elsewhere) |
| add a failure/error path | [`error-states.md`](docs/mvp/error-states.md) **first** | the owning package |

## Invariants — never violate (most are enforced by tests)

- **Docs-first.** A behavior change is a doc diff before a code diff.
- **`personal/` is private.** Gitignored; never in any commit, example,
  fixture, or shared context. Do not read it unless the user explicitly
  directs you to.
- **Public-safe boundary.** No private project names, identities, or paths
  beyond `~/projects/lucid/` and `~/.lucid/`; synthetic fixtures only.
  *Enforced by `lucid validate` (S-7).*
- **Sanctuary (P3).** `internal/storage` is the **only** package that touches
  `~/.lucid/`; `internal/engine`, `internal/observations`, and
  `internal/scheduler` are agent-free / no-LLM. *Enforced by
  [`engine/purity_test.go`](internal/engine/purity_test.go),
  [`observations/purity_test.go`](internal/observations/purity_test.go),
  [`scheduler/scheduler_test.go`](internal/scheduler/scheduler_test.go).*
  Observations are **inventory, never obligation** — no streaks, scores, or
  targets on them, ever.
- **Model access only through `internal/provider`.** Every agent reaches a
  model through the `Provider` seam; stub it with `provider.Fake`
  ([ADR-0006](docs/adr/0006-model-access.md)) — never wire an SDK elsewhere.
- **No autonomous messages** beyond the pre-committed Engine templates (bell,
  L1 nudge, L2 witness + monthly heartbeat —
  [`engine-module.md`](docs/mvp/engine-module.md)); **no outbound fetch**
  beyond opted-in enrichers through the single audited `fetch_enrichment` op
  ([`observations.md`](docs/observations.md) §5: quantized coordinates + dates
  to pinned keyless endpoints, nothing else). Fetches are not sends; neither
  ceiling is a precedent.
- **The runtime never depends on AI (P9).** Prefer a deterministic script over
  an LLM call for parsing, schema validation, dates, fixtures, and greps; the
  daily surface completes with no model.
- **Don't invent** commands, agents, or record fields; **don't add** cloud
  sync, telemetry, analytics, or new send paths; **don't read** the user's
  full history — route through the storage adapter for only the slice a
  contract names; **don't add** hooks/CI before the code they would gate exists.

## Where the code lives

One static `lucid` binary; source under `internal/` only, no `pkg/`.

- [`cmd/lucid/main.go`](cmd/lucid/main.go) → [`internal/cli`](internal/cli) —
  Cobra entry; verbs: `init log closeout mode status obs day validate export
  version upgrade` ([ADR-0007](docs/adr/0007-cli-conventions.md)).
- [`internal/router`](internal/router) — dispatch spine; verb→intent 1:1;
  holds the storage adapter + resolved config; orders agent calls.
- [`internal/engine`](internal/engine) — pure, deterministic, agent-free Engine
  (chain, day, closeout, tripwire, storm, witness, mode, rollover);
  `templates/` = the fixed send templates.
- [`internal/scheduler`](internal/scheduler) — the **only** send path
  off-machine (evening bell + morning tripwire); takes an explicit `now`.
- [`internal/observations`](internal/observations) — micro-logs, salted
  registries, day-view join, curiosity, enrichment; `exports/` = CSV +
  clinician packet.
- [`internal/storage`](internal/storage) — the **only** reader/writer of
  `~/.lucid/` (`LUCID_HOME` override); frontmatter + one file per record
  family; `fetch_enrichment.go` = the single audited network op.
- [`internal/agents`](internal/agents)`/{intake,structuring,reflection,safety}`
  — the LLM-backed agents; `agentutil/` holds the shared call-the-model-and-
  decode-JSON helper (`CompleteJSON`), reachable only through `provider`.
- [`internal/provider`](internal/provider) — the single model seam
  (`iface.go`, `fake.go`).
- [`internal/validate`](internal/validate) — `lucid validate`, the read-only
  architecture-gate sweep (boundary, diagnostic-language, links, schema).
- `internal/{config,upgrade,isoweek,deps,keyderive}` + `data/` — config,
  self-update, ISO-week math, pinned core deps (`go-flywheel`, `go-foundation`),
  the pure salted slug-derivation core shared by person_keys and registry keys,
  and the embedded person-key wordlist.

## Build, verify, commit

Build, test, and lint run through **MAGE-X**, not Make:

```
magex build          # build the lucid binary
magex test           # race + coverage (≥90%), 30m timeout
magex lint           # golangci-lint (60+ linters)
magex format:fix     # gofumpt + imports
```

Tests are co-located `_test.go` — no `testdata/`, no golden files. Isolate a
Ledger with `t.TempDir()` + `LUCID_HOME`; stub the model with `provider.Fake`;
drive the scheduler with an injected `now`.

**Before you commit:** doc diff → `magex format:fix` → `magex lint` →
`magex test` → `lucid validate` → conventional commit
`<type>(<scope>): <imperative>` (PRs: `[Subsystem] …` + What / Why / Testing /
Impact). Full Go, testing, security, dependency, and release standards:
[`.github/tech-conventions/`](.github/tech-conventions/README.md).
