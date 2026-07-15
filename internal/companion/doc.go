// Package companion composes and (in later phases) delivers Lucid's two daily
// messages — the morning and night companion. It is the Mirror-side,
// model-allowed sibling of the pure accountability teeth: unlike
// internal/{engine,scheduler,schedrun}, which carry a purity guard forbidding
// any provider/agent/model import so the dead-man decision can never be softened
// by a model (architecture P9), this package deliberately reaches the model
// through internal/provider to compose a warm, in-voice message from the
// operator's own opaque prompt files and the chain's honest live numbers.
//
// The split is the whole point. The teeth keep their modeless floor; the
// companion enriches it. On a miss-day the companion composes the warm message
// and appends the Engine's deterministic user verdict byte-for-byte below it
// (read send-free via the scheduler), so the model writes the warmth but never
// the verdict — a model cannot reword "you missed". When the model is
// unreachable the companion falls back to the Engine's own deterministic copy so
// a message always lands; only the warmth is lost, never the send.
//
// The firewall seam is the config block: the companion opens exactly the three
// explicit prompt-file paths lucid.json names and never walks a directory, so no
// personal template tree is ever traversed. Nothing personal lives in this
// package — the mechanism ships in the OSS repo, the content stays in the
// operator's own files.
package companion
