# ADR-0001 — Implementation language: Go

**Status:** Accepted for the MVP build. Final lock-in is a named
agenda item at the post-MVP retro (with ADR-0002), after the steel
thread has run for one gate cycle.

## Context

The MVP contracts are deliberately language-agnostic: named router
plans, named storage ops, JSON/Markdown records, per-agent
input/output schemas. Any language could implement them. But a
recommendation matters, because the two module families that ship
first (Engine, Observations) are pure deterministic code — parsers,
date math, streak arithmetic, template rendering, one audited HTTP
fetch — exactly the profile where language choice is about operational
character, not expressiveness. The owner's existing toolchain, CI, and
release tooling are Go.

## Decision

Implement the Lucid core in **Go**: the router, storage adapter,
Engine module, Observations module, schedulers, and validators — one
statically-linked `lucid` binary per platform.

Reasons, in order: a single binary with no runtime dependencies is the
right shape for a tool that must outlive its tooling (architecture P6
— the practice depends on the tripwire firing on a machine nobody has
maintained in months); cross-compilation covers Mac/Linux/friends'
machines from one Makefile; the deterministic-scripts-first rule
(claude-code-workflow) becomes table-driven Go functions with real
tests instead of shell; the owner's existing Go toolchain removes a
whole class of yak-shaving. LLM agent calls (Intake, Structuring,
Reflection, Safety) are HTTP calls behind interfaces — no
language-level ML dependency exists anywhere in the design.

## Consequences

The MVP doc set stays contract-first — nothing in `docs/mvp/` needs
Go-specific rewording; `AgentContext<T>` realizes naturally as typed
structs per slice. Contributors who don't write Go can still author
agents' prompts, docs, and fixtures. If the post-MVP retro overturns
this (it shouldn't — but the gate exists on principle: nothing locks
before the loop has earned it), the contracts are the migration
boundary and this ADR is superseded, not edited.
