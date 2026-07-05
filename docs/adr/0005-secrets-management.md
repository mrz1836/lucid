# ADR-0005 — Secrets: `hush` vault with spawn-time injection, never plaintext

**Status:** Accepted.

## Context

Lucid's operational surface needs very few secrets, but the ones it
needs are load-bearing: the chat-harness bot/webhook token that
delivers the bell, tripwire, and witness sends (the only send path in
the system), and potentially — though not today — a keyed enricher
(current enrichers are keyless by design,
[`../observations.md`](../observations.md) §5). Where those secrets
live is a posture decision. Dotfiles, shell rc, and plaintext config
all fail the same way: they leak into backups, fixtures, and shared
context, and they rot silently — exactly what the public-safe boundary
and the outlive-its-tools bet (architecture P6) forbid. The owner
already runs a first-party answer in production: **`hush`** (same
`mrz1836` toolchain as ADR-0004's dependencies) provides a local
secrets vault (`hush serve`) and a process supervisor
(`hush supervise`) that injects secrets into a child's environment at
spawn time — the pattern the reference OpenClaw deployment has run for
months.

## Decision

`hush` is the sanctioned secret store for every Lucid deployment
shape. Binding rules:

* **No secret material at rest in Lucid's world.** Nothing secret in
  Lucid config files, `personal/` calibration, the Ledger, fixtures,
  or this repo — ever. Config and calibration may *name* a secret
  (an env-var name or vault reference); they never carry a value.
* **Injection at spawn.** When the standalone `lucid` scheduler
  (ADR-0004) runs as a service, it runs under `hush supervise`, which
  injects the named secrets into the process environment at child
  start. The chat-harness deployment inherits the same property from
  the gateway's existing supervision.
* **The binary stays credential-dumb.** `lucid` reads secrets from its
  environment and holds them in memory only; it implements no
  keychain, no crypto store, no token refresh of its own.

## Consequences

* A dead or locked vault degrades *sends*, never the chain: bell and
  tripwire delivery failures follow the existing error-state paths,
  and the practice survives on the Phase-0 rule (P10, ADR-0003) — a
  missing secret can cost a notification, never the record.
* ADR-0004's dependency policy stands untouched: `hush` is a
  deployment-time **companion process**, not a linked library. If a
  hush client library is ever linked into the core, this ADR is the
  recorded reason it may be considered.
* Friends' installs inherit a two-binary bootstrap (`lucid` + `hush`)
  — acceptable, since both ship through the same release tooling
  (ADR-0007), and instance isolation (architecture §5) already
  assumes each instance provisions its own credentials.
* The MVP vault inventory is deliberately tiny — likely one entry
  (the harness token). ADR-0006 keeps it that way: model access is
  designed to require no stored secret at all.
