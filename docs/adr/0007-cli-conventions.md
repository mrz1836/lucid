# ADR-0007 — CLI conventions: the house style (`hush`)

**Status:** Accepted.

## Context

ADR-0003 makes the CLI the reference surface: if a behavior can't be
reached from `lucid <command>`, it doesn't exist yet. The owner's
toolchain already has a settled CLI idiom, verified in `hush`:
**cobra** command trees, the
`cmd/` + `internal/` (+ `pkg/` where a public API exists) layout,
**magex** (`mage-x`) as the task surface, **goreleaser** for
multi-platform release, and codecov-tracked CI. Inventing a private
idiom for `lucid` would cost muscle memory (human and agent — the
dev agents already drive these patterns daily) and fork the tooling
this build is explicitly meant to mature.

## Decision

`lucid` follows the house CLI conventions as exemplified by `hush`:

* **cobra** for the command tree, with the grammar already named in
  ADR-0003 (`lucid init|log|closeout|mode|status|day|validate|export`)
  as the spine; subcommands map one-to-one onto router intents, never
  onto module internals.
* **Repo bootstrap by copy, not derivation:** the scaffolding is
  copied wholesale from the house binary-repo template and adapted by
  rename — `.github/` workflows and CI, `.goreleaser.yml`,
  the `.mage.yaml`/magefiles task surface, lint and codecov configs,
  `.editorconfig`/`.gitattributes`, LICENSE. Layout follows the same
  source: `cmd/lucid/` entrypoint, `internal/` for everything not
  contractually public, `docs/` and `examples/`.
* **Build/test/release:** magex targets matching the sibling repos
  (ADR-0001's "one Makefile" realizes as the house `.mage.yaml`
  task surface), goreleaser for cross-platform artifacts, coverage
  and CI conventions carried over unchanged.
* **`lucid update`, the house self-update:** the running binary
  updates itself in place from a GitHub release — the latest release
  resolved via the `gh` CLI with a REST fallback, the platform tarball
  verified against published SHA-256 checksums, then swapped in place
  atomically (staged `.new` → rename) so a running scheduler is never
  corrupted mid-execution. `--check` reports without installing,
  `--force` reinstalls the current release, and `--verbose` narrates
  each step; `upgrade` stays as an alias, and every other command
  prints a passive, cached "a new version is available" notice. This
  adds one verb to the ADR-0003 command spine, recorded here.
* **`lucid scheduler`, the daemon verb tree — `run | status |
  reconcile`:** the house pattern of a long-running process paired with
  read-only inspection, extended by one repair verb. `run` is the
  daemon, `status` its credential-dumb health read, and `reconcile` the
  sanctioned lever that re-arms a parked periodic
  ([`../usage/commands.md`](../usage/commands.md#scheduler-reconcile),
  [`../mvp/engine-module.md`](../mvp/engine-module.md) §"Durability of
  the schedule itself"). The convention `reconcile` establishes: **state
  the binary owns is repaired only through the binary.** The durable job
  store is disposable machinery ([ADR-0004](0004-core-dependencies.md)),
  never the record — but "disposable" is not "hand-editable", and a
  runbook step that says *edit the job store directly* is a missing verb.
  Every diagnostic a graded `status` reports should name the command that
  fixes it. These verbs sit beside the ADR-0003 spine, recorded here.
* **Output discipline:** human-first output by default; a
  machine-readable mode on the commands scripts need (`status`,
  `day`, `export`, `validate`), so automation never scrapes prose.
* Improvements made here flow back: shared patterns mature in the
  shared tools (`mage-x`, `go-foundation`), not in Lucid-local forks.

## Consequences

Contributor onboarding equals "any `mrz1836` repo." The scheduler,
storage, and secrets decisions (ADR-0002/0004/0005) slot into this
layout without adaptation. Deviations from the house style require a
superseding ADR, not a quiet exception. `lucid update` replaces the
running binary in place and never touches a supervised process — the
supervisor restarts the daemon on its next cycle, and the morning
tripwire self-check still guards the practice: a build that would cost
a night of the practice is caught regardless of what shipped (P10).
One guard inherited from the
hard rules: the CLI adds no commands beyond the documented set —
new verbs land in the docs (scope, module specs) before they land in
cobra.
