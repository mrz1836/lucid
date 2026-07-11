// Package deps once pinned the sanctioned first-party core dependencies
// (ADR-0004) into the module graph with blank imports, before the Engine and
// its scheduler wired them for real.
//
// Those real call sites now exist, so the reservation has shrunk to this note:
//
//   - github.com/mrz1836/go-flywheel — the durable job runtime that drives the
//     bell and morning tripwire, wired in internal/schedrun (ADR-0004).
//   - github.com/mrz1836/go-foundation — the domain-agnostic base layer
//     (fixed/real clocks) used by internal/schedrun and the runtime it hosts.
//
// Both modules are now required through ordinary imports, so `go mod tidy`
// keeps them without a blank-import placeholder. The package is retained as the
// documented home of the dependency contract; it deliberately imports nothing.
package deps
