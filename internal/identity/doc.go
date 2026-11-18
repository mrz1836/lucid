// Package identity is the pure core of the people identity log (ADR-0016;
// data-model.md §"Identity log"): who matters now (a person's status), who a
// bare name means (name claims), and where a single mention belongs when
// capture could not decide or decided wrong (holds, pins, and move-refs) —
// stored as one append-only, schema'd JSONL file, ~/.lucid/identity/identity.jsonl.
//
// Like the contact core it is entirely agent-free: deterministic value
// functions with no LLM in any path (architecture P9) and no filesystem access
// (architecture P3 — the storage adapter is the only code that touches
// ~/.lucid/). This package owns the event envelope, its id grammar, byte-stable
// (un)marshaling, per-kind validation, and the pure fold from events to the
// current identity facts. The storage adapter owns the append/read discipline
// and supplies the redirect canonicalization the fold applies.
//
// Nothing here ever rewrites a people record or a processed artifact: the log
// is folded on read, and a Ledger without it reads as everyone active, with no
// claims, holds, pins, or move-refs.
package identity
