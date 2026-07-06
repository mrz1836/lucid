// Package deps pins the sanctioned first-party core dependencies
// (ADR-0004) into the module graph before the Stage 2 Engine and its
// scheduler wire them for real:
//
//   - github.com/mrz1836/go-flywheel — the durable job runtime that
//     will drive the bell, tripwire, heartbeat, and enrichment jobs
//     when lucid runs installed (ADR-0004).
//   - github.com/mrz1836/go-foundation — the domain-agnostic base
//     layer: fixed clocks and DST-aware recurrence for the
//     logical-day/rollover math and the 21:30 bell, plus config,
//     logging, and health helpers.
//
// Stage 0 (this phase) only bootstraps the repo, so these are reserved
// here as blank imports of the lightest representative subpackage of
// each module. Later phases replace these with real call sites; when
// that happens this reservation can shrink or disappear. The blank
// imports keep `go mod tidy` from dropping the two core requires and
// make the dependency contract explicit and greppable.
package deps

import (
	// go-flywheel: durable, recoverable job runtime (scheduler).
	_ "github.com/mrz1836/go-flywheel/config"
	// go-foundation: DST-aware recurrence for the bell/tripwire clock.
	_ "github.com/mrz1836/go-foundation/recurrence"
)
