// Package witnessreport builds Lucid's weekly witness report — the polished,
// friend-facing accountability post that replaces the sparse monthly all-clear
// on the witness channel. It is the Mirror-side, model-allowed sibling of the
// pure Engine, structured exactly like internal/companion: a
// deterministic core computes a witness-safe report from the chain's honest
// live numbers, and a narrative pass reaches the model through
// internal/provider to warm that skeleton with in-voice prose.
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
// Anchors reach the report by kind: the watch-outs read only the numbers in
// the projection, never a note (a limit over its allowance, a habit miss run, a count of
// slips this week), so a long run is never flagged and a relapse is never
// hidden. There are exactly two deliberate, opt-in exceptions to the
// by-construction privacy, and neither adds a reader for observations, journal
// entries, or raw log content.
//
// The first is full anchor detail (witness_report.anchor_detail: full,
// detail.go): only then is a third, narrow reader wired — the week's anchor
// events, notes included — and its section is rendered by code after the model
// pass, never part of the model's input and never scanned, and delivered
// losslessly across as many messages as it needs.
//
// The second is the week notes (witness_report.notes_file, notes.go,
// ADR-0015): one operator-approved file, opened on exactly its configured path
// — never a directory walk, glob, or naming convention — and refused unread
// when it resolves, lexically or through a symlink, to or under the Lucid
// home, so the package itself still opens no Ledger file (internal/storage
// stays the only package that does). Notes are included only
// when the file is valid, dated inside the report's own week, and non-empty.
// Included notes are rendered verbatim by code in their own section (verbatim.go
// packs it losslessly, like anchor detail), never scanned and never truncated,
// and — unlike anchor detail — they are also handed to the model, as one
// labeled block of content, not instructions, so the read and the asks can be
// specific to the week. Every other notes status leaves the model input and
// the card byte-identical to a report without notes, and an invalid or
// unreadable file alerts the operator privately after delivery; a notes
// problem never blocks or delays the report.
//
// Nothing personal lives here — the mechanism ships in the OSS repo, the
// operator's witness-report voice and curated asks stay in their own files.
package witnessreport
