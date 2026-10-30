// Package contact is the contact check-in record family (ADR-0014): per-person
// check-in cadences (a window of N days or weeks), the contacts logged against
// them, one-week snoozes, and voids that correct a mistaken contact or snooze —
// stored as append-only, schema'd JSONL events under ~/.lucid/contacts/.
//
// Like the rep and focus cores it is entirely agent-free: deterministic value
// functions with no LLM in any path (architecture P9) and no filesystem access
// (architecture P3 — the storage adapter is the only code that touches
// ~/.lucid/). This package owns the event envelope, its id grammar, byte-stable
// (un)marshaling, the Nd/Nw window grammar, and the pure fold from events to
// per-person state, including the window-start rules. The storage adapter owns
// the append/read discipline; the router resolves subjects and canonicalizes
// person keys through merge redirects before folding.
//
// The record says only that the user and a person were in touch on a day. It
// has no direction, initiator, content, score or streak field, and none may be
// added.
package contact
