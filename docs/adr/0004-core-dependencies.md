# ADR-0004 — Core dependencies: go-flywheel and go-foundation

**Status:** Accepted. Follows from ADR-0001 (Go); reviewed alongside
it at the post-MVP retro.

## Context

The installed `lucid` binary (ADR-0003) carries real scheduled
responsibilities: the bell prompt, the morning tripwire, the monthly
witness heartbeat, and the enrichment job. These are not nice-to-have
crons — the tripwire is the dead-man switch, and the whole
accountability design collapses if it can silently die. Hand-rolling a
scheduler with retries, recovery, and an audit trail is exactly the
wheel the owner's existing toolchain (the `mrz1836` org, same owner)
already ships.

## Decision

Two first-party libraries are the sanctioned core dependencies:

* **[`github.com/mrz1836/go-flywheel`](https://github.com/mrz1836/go-flywheel)**
  — the job runtime for everything scheduled or backgrounded when
  Lucid runs installed: bell, tripwire, heartbeat, enrichment, and
  (later) Mirror agent calls, which are precisely the "slow, flaky, or
  costly operations" it targets. What it buys, mapped to Lucid's own
  requirements: durable jobs over **SQLite** with no external broker
  (single-binary ethos intact); cron-style scheduling with
  **stuck-lease recovery** (the tripwire that cannot silently die);
  retries with backoff (enricher fetch failures); an **append-only
  `job_runs` audit trail** (the execution record behind the dead-man
  backstop and the enricher outbound log — the same append-only ethos
  as the Ledger, in the machinery layer); idempotent enqueue (the
  one-event-per-enricher-per-day rule, enforced twice).
* **[`github.com/mrz1836/go-foundation`](https://github.com/mrz1836/go-foundation)**
  — the domain-agnostic base layer wherever one is needed: config
  types, structured logging, health checks, middleware if a local HTTP
  surface ever exists, and its test utilities — **fixed clocks** are
  exactly what the logical-day/rollover fixtures require, and its
  **DST-aware recurrence** is the correct engine for a 21:30 bell that
  must survive daylight-saving transitions.

Dependency policy: these two are the sanctioned first-party base.
Third-party additions stay minimal and boring, per the single-binary,
outlive-its-tools ethos (architecture P6); anything beyond the
standard library and these two needs a reason in a future ADR.

## Consequences

* **Naming collision, resolved by rename:** "Flywheel" was a Lucid
  portfolio status (a self-sustaining practice — architecture §4).
  Rather than carry a permanent library/status homonym, the status was
  renamed to **Anchor** — the plain-language "anchor habit" term for
  exactly that mechanic, already used verbatim in the corpus
  ("anchored to morning coffee"). Lineage: Engine → Flywheel → Anchor,
  each rename a collision fix. Convention going forward: capitalized
  **Anchor** is the portfolio status; lowercase "anchor/anchored" in
  prose is the ordinary word; `flywheel` appears only as the
  backticked library name `go-flywheel` (or its database file).
* **The job database is machinery, not record.** `go-flywheel`'s
  SQLite store (e.g., `~/.lucid/flywheel.db`) holds queue state and
  run history — operational infrastructure, disposable and
  reconstructable from config, never testimony. ADR-0002 stands
  untouched: the Ledger remains plain files; no observation, entry, or
  registry data ever lives only in the job database. Losing
  `flywheel.db` loses no truth; jobs re-enqueue from configuration.
* **Scheduler mapping across surfaces (ADR-0003):** riding a chat
  harness, the harness's native scheduler drives the jobs, as the MVP
  docs specify; installed and standalone, `go-flywheel` is the
  scheduler and daemon. The job *definitions* (what fires, when, with
  what template and consent flag) live in Lucid's own config either
  way — the runtime is swappable, the contract is not.
* Being first-party (same owner as the project) keeps the
  dependency risk profile aligned with P6: the tools most likely to
  need a fix decades from now are maintained by the same hands that
  need them fixed.
