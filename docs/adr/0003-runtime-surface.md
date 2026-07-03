# ADR-0003 — Runtime surface: one core binary, thin surfaces

**Status:** Accepted.

## Context

The MVP runs on a chat harness (Discord via OpenClaw or Hermes,
[`../mvp/local-runtime.md`](../mvp/local-runtime.md)) because capture
must work on a phone at the bell and friends must not need a dev
environment. But the harness is explicitly not the product, and a CLI
is the natural surface for the owner's desk-bound work (retros,
exports, validation) and for scripting.

## Decision

All product logic lives in the `lucid` core (ADR-0001): router,
modules, adapter, gates. Surfaces are thin clients of the same router
contract, in this order:

1. **CLI** — `lucid init` (the calibration wizard —
   [`../calibration.md`](../calibration.md)), `lucid log|closeout|mode|
   status|day|validate|export`, plus the schedulers (backed by
   `go-flywheel` when running standalone —
   [ADR-0004](0004-core-dependencies.md)). The CLI is the reference
   surface: if a behavior can't be reached from it, the behavior
   doesn't exist yet.
2. **Chat harness** — OpenClaw/Hermes agents translate channel
   messages to the same router intents and relay responses. This is
   the phone-and-friends surface, and the bell/tripwire delivery
   channel, per the MVP docs.
3. **Standalone app** — Phase 2b (architecture §8), replacing the
   harness only.

## Consequences

"Install it and it does everything for you" becomes literal: one
binary, `lucid init`, done — the harness is optional sugar for phone
access rather than a hard dependency. No feature may be
harness-coupled (local-runtime.md already binds this); the harness
never bypasses the router; and the Phase-0 rule keeps its teeth — the
chain must survive every surface being down (engine P10 / S-14).
