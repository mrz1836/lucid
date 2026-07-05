# ADR-0007 — CLI conventions: the house style (`hush`, `go-broadcast`, `atlas`)

**Status:** Accepted.

## Context

ADR-0003 makes the CLI the reference surface: if a behavior can't be
reached from `lucid <command>`, it doesn't exist yet. The owner's
toolchain already has a settled CLI idiom, verified across `hush`,
`go-broadcast`, and `atlas`: **cobra** command trees, the
`cmd/` + `internal/` (+ `pkg/` where a public API exists) layout,
**magex** (`mage-x`) as the task surface, **goreleaser** for
multi-platform release, and codecov-tracked CI. Inventing a private
idiom for `lucid` would cost muscle memory (human and agent — the
dev agents already drive these patterns daily) and fork the tooling
this build is explicitly meant to mature.

## Decision

`lucid` follows the house CLI conventions as exemplified by `hush`,
`go-broadcast`, and `atlas`:

* **cobra** for the command tree, with the grammar already named in
  ADR-0003 (`lucid init|log|closeout|mode|status|day|validate|export`)
  as the spine; subcommands map one-to-one onto router intents, never
  onto module internals.
* **Repo bootstrap by copy, not derivation:** the scaffolding is
  copied wholesale from a reference binary repo (`hush` or `atlas`)
  and adapted by rename — `.github/` workflows and CI, `.goreleaser.yml`,
  the `.mage.yaml`/magefiles task surface, lint and codecov configs,
  `.editorconfig`/`.gitattributes`, LICENSE. Layout follows the same
  source: `cmd/lucid/` entrypoint, `internal/` for everything not
  contractually public, `docs/` and `examples/`.
* **Build/test/release:** magex targets matching the sibling repos
  (ADR-0001's "one Makefile" realizes as the house `.mage.yaml`
  task surface), goreleaser for cross-platform artifacts, coverage
  and CI conventions carried over unchanged.
* **`lucid upgrade`, cloned from the house self-upgrade:** the exact
  pattern `hush upgrade` and `atlas upgrade` implement — latest
  GitHub release resolved via the `gh` CLI with REST fallback, the
  platform tarball verified against published SHA-256 checksums,
  then swapped in place atomically (copy → `.new` → rename) so a
  running scheduler is never corrupted mid-execution; `--check`,
  `--force`, `--channel`, and the `UPDATE_CHANNEL` convention
  (stable | beta | edge) carry over. This adds one verb to the
  ADR-0003 command spine, recorded here.
* **Output discipline:** human-first output by default; a
  machine-readable mode on the commands scripts need (`status`,
  `day`, `export`, `validate`), so automation never scrapes prose.
* Improvements made here flow back: shared patterns mature in the
  shared tools (`mage-x`, `go-foundation`), not in Lucid-local forks.

## Consequences

Contributor onboarding equals "any `mrz1836` repo." The scheduler,
storage, and secrets decisions (ADR-0002/0004/0005) slot into this
layout without adaptation. Deviations from the house style require a
superseding ADR, not a quiet exception. On a supervised host,
`upgrade` is invoked through the managed-upgrade flow and honors the
drain window — never between bell and close-out — and the
post-upgrade health check is a tripwire self-check: an upgrade that
costs a night of the practice is a failed upgrade regardless of what
shipped (P10). One guard inherited from the
hard rules: the CLI adds no commands beyond the documented set —
new verbs land in the docs (scope, module specs) before they land in
cobra.
