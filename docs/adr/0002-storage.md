# ADR-0002 — Storage: plain files first, SQLite as a projection later

**Status:** Accepted. Reviewed alongside ADR-0001 at the post-MVP
retro.

## Context

The reference spec ([`../../technical-spec.md`](../technical-spec.md))
assumes SQLite; the corpus bet (architecture P6) demands the record
stay legible for decades; the MVP needs zero-dependency durability on
day one.

## Decision

The system of record is plain files — Markdown with frontmatter and
JSON/JSONL under `~/.lucid/` — exactly as specified in
[`../mvp/data-model.md`](../mvp/data-model.md) and
[`../observations.md`](../observations.md). SQLite arrives later as a
**derived index**, never the canonical store: the migration path in
data-model.md §"SQLite migration path" stands, with the file tree
remaining the export and backup format permanently. The backup set is
`raw/`, `observations/`, `registries/`, `engine/` (minus
`status.json`).

## Consequences

Every future query engine, dashboard, or model reads either the files
or an index rebuilt from them; nothing proprietary ever sits between
the user and their record. Query performance is bounded until the
index lands — acceptable, because the MVP's heaviest read is a
seven-item window. Database lock-in is a decision the post-MVP retro
can make with real usage data instead of forecasts.
