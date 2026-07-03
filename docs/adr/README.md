# Architecture Decision Records

One file per decision that shapes the implementation, in the classic
ADR format (status, context, decision, consequences). Specs say what
the system *is*; ADRs record why the build went one way when it could
have gone another — so future contributors inherit the reasoning, not
just the ruling. Statuses: **Proposed** → **Accepted** → possibly
**Superseded by ADR-NNNN**. Amending an accepted ADR means writing a
new one; records are append-only, like everything else here
(architecture P8).

| ADR | Title | Status |
|-----|-------|--------|
| [0001](0001-implementation-language.md) | Implementation language: Go | Accepted for MVP; lock-in reviewed at post-MVP retro |
| [0002](0002-storage.md) | Storage: plain files first, SQLite as a projection later | Accepted |
| [0003](0003-runtime-surface.md) | Runtime surface: one core binary, thin surfaces | Accepted |
| [0004](0004-core-dependencies.md) | Core dependencies: `go-flywheel` (job runtime) and `go-foundation` (base layer) | Accepted |
