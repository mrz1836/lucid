# agents/

Harness-facing agent definitions for Lucid. This tree holds the **on-screen
identity** the chat harness presents and the shareable prompt definitions the
router's internal agents grow into — the shareable-spec half of the
spec-vs-calibration split (architecture.md §5). Instance wiring (channel ids,
schedules, witness contact) never lives here; it stays in instance
configuration.

## Contents

* [`lucid/identity.md`](lucid/identity.md) — the single named **Lucid** agent
  the user sees: one voice, one turn-taking pattern, one boundary contract. The
  user addresses Lucid, never the internal agents.

## What this is not

* **Not the internal agent code.** The four required-now agents (Intake,
  Structuring, Reflection, Safety/Consent) are implemented under
  `internal/agents/` and reached only through the router. They are an
  implementation detail; there is no `@Intake` surface.
* **Not instance calibration.** Nothing here names a channel, a person, a
  witness, or a path outside `~/projects/lucid/` and `~/.lucid/`. Those are
  per-instance and live in instance configuration.
* **Not a place for product logic.** These are identity and voice definitions.
  Behavior lives in the binary; the harness relays, it does not reimplement
  (local-runtime.md).
