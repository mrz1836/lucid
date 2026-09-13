# ADR-0011 — Person alias resolution: extend the redirect-tombstone mechanism, tighten reconcile precision

**Status:** Accepted.

## Context

Person identity is keyed on the *exact* normalized `display_name`
([`../mvp/data-model.md`](../mvp/data-model.md) §"`person_key` derivation"):
`NormalizeName` → `DerivePersonKey` hashes the normalized surface form, so the
`person_key` *is* that form. There is no alias-aware step that consults existing
records before minting, so every distinct way one person is written — a full
name, a nickname, a diminutive — derives a different slug and becomes a separate
record. Over time these "alias shards" accumulate: the real person's record stays
thin while empty shards pad the person list. All name examples here are synthetic.

The intended cleanup path, `lucid person reconcile`, was itself unreliable. Its
proximity heuristic paired two people whenever their normalized forms fell within
a bounded Levenshtein edit distance, which conflated unrelated short given names
("Braden" vs "Brandon", "Dana" vs "Dania"). The false positives trained the user
to distrust and hand-filter the output — defeating the feature.

A hard constraint bounds any fix. Capture-time resolution deliberately routes
**only** through the derived-key + redirect-tombstone graph and **never scans
`aka[]`** ([`../mvp/data-model.md`](../mvp/data-model.md) §"Merge & redirect").
That is not an accident: an `aka` normalizes differently from its record's
`display_name`, and the record's `display_name` is the single collision-oracle
witness for its slug. Scanning `aka[]` in the resolution path would let one slug
claim two witnesses and break the oracle. So "consult `display_name` + `aka`
before minting" cannot mean "scan records inside resolution".

## Decision

Add alias-aware routing by **extending the existing redirect-tombstone/resolver
mechanism**, not by scanning records in the resolution path — and drop reconcile's
bare edit-distance path in favor of a corroborating-signal rule.

* **Discovery is a separate pre-mint pass, not the resolution path.** A new pass
  runs *only* when a mention would otherwise mint a fresh record. It may read live
  records' `display_name`/`aka` and a curated nickname map — exactly the reads
  reconcile and `/person` name-matching already perform. When, and only when, it
  finds **exactly one** live candidate, it plants/reuses a redirect tombstone at
  the mention's derived slug via the same append-and-redirect mechanism
  `person merge` uses. From then on the mention folds in through the tombstone,
  identically to an explicit `"X, aka Y"` alias. Zero or ≥2 candidates → mint a
  new record and let reconcile suggest the merge (the conservative bias: at capture
  it is safer to leave a possible duplicate separate than to fuse two distinct
  people).

* **The "aka is never scanned" collision-soundness invariant stays intact.** The
  collision oracle and resolution path (`ResolvePersonKey`, `personKeyOwner`,
  `ResolvePersonRedirect`) are untouched — they still route through derived keys
  and tombstones alone and never scan `aka[]`. The new pass is *discovery* that
  reads records to decide whether to plant a tombstone; the *resolution* it
  produces still flows through the tombstone graph. The planted tombstone's frozen
  `display_name` is the mention form — the one witness for that slug — so each slug
  keeps exactly one witness and the oracle stays sound by construction.

* **A curated nickname↔canonical map, shipped as an embedded data asset.** A
  committed, reviewable `data/person_nicknames.txt` (mirroring the wordlist,
  [`../mvp/data-model.md`](../mvp/data-model.md) §"`person_key` derivation")
  supplies the bidirectional links a prefix rule cannot know ("mike" is not a
  prefix of "michael"). Both the capture pass and reconcile consume it through one
  shared rule: reduce a raw `display_name`/`aka` to its normalized **first
  whitespace-delimited component** before lookup, so "Mike" links to "Michael
  Torres" without inspecting the surname. Only unambiguous clusters ship; ambiguous
  nicknames (e.g. "alex" → Alexander *or* Alexandra) are excluded as safe
  false-negatives, as are titles and family-name-first forms.

* **Reconcile requires a corroborating signal.** The bare edit-distance path is
  removed. A suggestion fires only on a shared normalized form, a nickname-map
  link, or an explicit diminutive link. **Entry co-occurrence is deliberately
  excluded** — spouses and coworkers co-occur constantly and would reintroduce
  false positives. `reconcile` stays read-only and byte-stable; merges remain
  user-driven through `lucid person merge`.

* **Deterministic and local-first — no model in the loop.** Both the capture pass
  and reconcile are pure functions of the records and the committed map, with **no
  LLM** (P9). A re-run of the same mention sequence yields byte-identical records:
  the second pass sees the planted tombstone and takes the ordinary resolve path.

## Consequences

* Canonical records accrue detail instead of fragmenting: a bare nickname or
  diminutive of a known person folds into the existing record at capture instead
  of spawning a shard — but only on an unambiguous single-candidate match, so two
  distinct people are never fused automatically.
* `lucid person reconcile` becomes trustworthy: near-zero false positives on
  unrelated short names means its suggestions can be acted on without
  hand-filtering, while real duplicates (full-name/aka overlap, nickname-map hit,
  diminutive link) are still caught.
* The collision oracle is unchanged, so the existing redirect-graph guarantees
  (single-hop, no cycles, the `lucid validate` redirect checks) and every prior
  person test continue to hold; the new behavior is additive.
* The map is a small ongoing curation asset. Its bias is toward false-negatives:
  an unmapped or ambiguous nickname simply does not link, which is safe. Growing it
  is a data edit, never a code or model change.
* No data is silently fused: capture routes only on an unambiguous match, and all
  cross-record merges still require a deliberate `lucid person merge`. Backfilling
  the existing shard population is out of scope — the new rules apply going forward
  and can be proven before any retroactive fuse is considered.
