// Package witnessreport builds Lucid's weekly witness report — the polished,
// friend-facing accountability post that replaces the sparse monthly all-clear
// on the witness channel. It is the Mirror-side, model-allowed sibling of the
// pure accountability teeth, structured exactly like internal/companion: a
// deterministic core computes a witness-safe report from the chain's honest
// live numbers, and (in a later phase) a narrative pass reaches the model
// through internal/provider to warm that skeleton with in-voice prose.
//
// The split is the whole point and the firewall. This file's deterministic
// core — report.go and asks.go — imports no provider/agent/model package and no
// observations/journal/raw-entry reader: it folds only the metrics projection
// (engine.BuildMetrics, streak/adherence/misses/error-budget, never recomputed)
// plus a 7-day WeekWindow into the Report data model. Every streak, adherence,
// and this-week-miss number is copied straight from the projection, so a
// witness-facing number can never be fabricated. The watch-outs and the
// auto-drafted friend-asks are derived deterministically from those real
// signals; a quiet week surfaces its own thin logging as a watch-out rather
// than being suppressed or padded with invented activity.
//
// Nothing personal lives here — the mechanism ships in the OSS repo, the
// operator's witness-report voice and curated asks stay in their own files.
package witnessreport
