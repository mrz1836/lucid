# ADR-0016 — Person status and name claims: an append-only identity log consulted before slug resolution, folded on read

**Status:** Accepted.

## Context

A `person_key` is an unsalted hash of the normalized display name
([`../mvp/data-model.md`](../mvp/data-model.md) §"`person_key` derivation"), so
the first record to hold a bare name owns it forever. The structuring agent
emits display names only and never reads `people/`; the deterministic People
routine (`update_person`) then maps a bare mention to its derived slug, follows
any redirect, and folds the mention into the live record there. The nickname
discovery pass (ADR-0011) runs only when that slug is free, so an existing live
holder always wins.

People also have no lifecycle. A person mentioned once, years ago, carries the
same weight as someone central today. All names below are synthetic.

**The failure.** A first name, "Sam", was recorded years ago for an old contact
from one passing mention. Later the user meets someone important who is also
"Sam" and records them deliberately — necessarily as "Sam Rivera", because
`person create "Sam"` returns the existing record. From then on every bare
"Sam" folds into the old record, which the user no longer cares about. No verb
fixes this:

* `merge` fuses two different people into one.
* `off-limits` only redacts inference; it does not affect resolution.
* Renaming the old record makes the next bare "Sam" mint a third record under a
  collision suffix instead of reaching "Sam Rivera".
* `alias "Sam Rivera" Sam` is refused (§P-7): the form already resolves to a
  different live record.
* Writing full names in every entry is a workaround in the user's memory, not a
  fix in the product.

**The constraint.** Resolution routes only through derived keys and redirect
tombstones and never scans `aka[]`: a record's `display_name` is the single
collision-oracle witness for its slug, and scanning `aka[]` would let one slug
claim two witnesses (ADR-0011). Any fix must keep the collision oracle sound,
keep `DerivePersonKey` and every existing key unchanged, keep resolution
deterministic with no model, keep structuring blind to `people/`, and leave an
untouched Ledger byte-identical.

## Decision

Give people a first-class, deterministic, append-only answer to two questions —
**who matters now** (a status of `active` or `dormant`) and **who a bare name
means** (name claims) — plus a way to repair one misfiled mention, all recorded
in a separate registry that is **consulted before slug resolution** and **folded
on read**.

### 1. One append-only identity log

A new tree, `~/.lucid/identity/identity.jsonl`, holds six event kinds —
`status`, `claim`, `release`, `hold`, `pin`, and `move_ref` — with receipt ids
`identity_YYYY_MM_DD_<slot>` (the contact-event slot rule;
[`../mvp/data-model.md`](../mvp/data-model.md) §"Identity log"). Like the link
ledger it is one file, read only through `internal/storage`, and every
correction is a new event. `people/*.json` keeps its exact shape: there is no
status field, and a Ledger with no log reads as everyone active with no claims.
Reads never create the file. A new pure package, `internal/identity`, owns the
event schema, the ids, and the fold, with no IO and no model — mirroring
`internal/contact`.

### 2. Five curation verbs on the strict tier

`lucid person dormant <subject> [--reason-file <path>]` and its `--restore`,
`lucid person claim <subject> <form> [--force]`, `lucid person release <subject>
<form>`, and `lucid person move-ref <raw_id> [--from <subject>] --to <subject>`
each append to the log and nothing else. Every rejection is decided before any
write, every write returns its receipt, and every repeat is a no-op ack. Three
new codes: §P-10 (a claim conflicts with an active holder), §P-11 (a mention is
held, not assigned), and §P-12 (a `move-ref` is refused)
([`../mvp/error-states.md`](../mvp/error-states.md)).

### 3. One resolver, two modes, consulted before slug resolution

A single resolver in `internal/storage` serves every surface
([`../mvp/data-model.md`](../mvp/data-model.md) §"Resolving a bare name"):

* **Lookup** — who the name means now — for `lucid person <name>`, the `contact`
  subject, gratitude, and memory linking. An exact key wins, then an active
  claimant, then the single active exact holder; when every exact holder is
  dormant, a single-word form goes to the one active person with that first
  name. It never mints.
* **Capture** — where a new mention goes — for `update_person`. A recorded pin
  or the raw's prior resolution wins (re-runs are sticky), then an active
  claimant, then the slug path. When only dormant people hold the slug path, a
  single-word form routes to the one active first-name match (recorded as a
  pin, never as an `aka`), several matches **hold** the mention, and otherwise a
  fresh active record is minted.
* **Record targeting** — the curation verbs name a record, not a meaning: an
  exact key or exact form across active and dormant records, refused with §P-5
  when that disagrees with the lookup, so a write never guesses.

The claim and status layers sit **in front of** the slug path. The slug path
itself is unchanged: `aka[]` is never scanned on the slug-resolution path. It
reads the derived slug, its redirect, and each same-name suffix slot from that
slot's **own** record, so every slug keeps exactly one witness and the collision
oracle is untouched.

### 4. The discovery read can only hold

Two records can share an exact form only through `aka[]` (an explicit "X, aka Y"
whose tombstone could not be planted). Detecting that requires reading `aka[]`.
Like the ADR-0011 pre-mint pass, that read is **discovery**: it may **hold** a
mention — two or more active holders, or a sole active `aka` holder behind
dormant slug-path holders — but it never **assigns** one. A lookup of the second
case still resolves to the active holder, as `lucid person` always has. Held
mentions go into a new optional `held_people[]` array beside `people[]` in the
processed artifact, so every `people[]` entry keeps its non-null `person_key`,
and `person reconcile` lists each one with its one-command fix.

### 5. The suffix rule extends to a dormant same-name holder

When every holder of a name is dormant and a new person of that name is minted
(capture, or `person create`), a dormant same-name slot counts as taken and the
new record takes the next free collision suffix (`person_s-oak-2`).
`DerivePersonKey` and every existing key are unchanged; later resolution walks
the same-name suffix chain by reading each slot's own record.

### 6. Corrections fold on read

Pins and move-refs never rewrite `people/` or `processed/`. Capture keeps
writing `entry_refs` as it always has; every read that attributes a raw entry to
a person — the `/person` view, the dominance line and gate panel, the
agent-bound Reflection slice (off-limits checked on both the stored and the
folded key, failing closed), and reconcile counts — goes through one storage
fold. A `claim`, `release`, `dormant`, or `restore` write pins the held mentions
it settles under capture rules that never mint; claims and status never move a
mention that is already recorded.

### 7. Merge keeps the log valid

`person merge` leaves the target's status alone, clears a dormant source with a
`status: active` event, and re-points the source's claims to the target (or
releases one another active person holds). `lucid validate` gains a
`people-identity` check for dangling claims, claims on tombstones, two active
claims on one form, and status on a tombstone.

## Consequences

* **The motivating case is one command.** Making the old "Sam" dormant sends
  every future bare "Sam" to "Sam Rivera"; a claim makes that explicit and keeps
  it robust if the old record is restored. Nothing is deleted.
* **Two signed-off output changes on existing Ledgers.** `person reconcile` adds
  a first-name-overlap line where an overlap exists (advisory only), and a new
  capture that hits a pre-existing `aka` collision is held instead of assigned
  first-come. Every other surface of an untouched Ledger is byte-identical,
  proven by characterization goldens taken on the unchanged code.
* **The collision oracle, `DerivePersonKey`, and every key are unchanged**, so
  ADR-0011's guarantees and the redirect-graph checks still hold.
* **Ambiguity is honest.** Two active people who hold a form exactly no longer
  race for a mention; the mention waits, counted for no one, until one command
  settles it. A shared first name alone never holds a mention back.
* **One more tree outside the backup set.** Like `people/`, `identity/` is not in
  the ADR-0002 manifest; losing it returns everyone to active with no claims,
  which is valid data.
* **An older binary ignores the tree.** It reads a Ledger with an identity log
  correctly but does not honor dormant status or claims.

## Alternatives considered

* **`merge` the two records.** Rejected: they are different people, and a merge
  fuses their histories.
* **`off-limits` the old record.** Rejected: off-limits redacts inference only;
  resolution would still fold every bare "Sam" into the old record.
* **Rename the old record.** Rejected: the next bare "Sam" derives the old slug,
  finds it renamed, and mints a third record under a collision suffix instead of
  reaching "Sam Rivera".
* **Alias the form onto the new person.** Rejected: the form already resolves
  to a different live record (§P-7), and forcing it would hijack another
  person's slug and break the collision oracle.
* **A status field on `people/*.json`.** Rejected: it changes every record's
  bytes and the oracle witness's record, and status history would be lost to an
  in-place overwrite.
* **`person_key: null` in `people[]` for an unassigned mention.** Rejected: it
  breaks the processed-artifact contract and every reader of it; a sibling
  `held_people[]` array is additive.
* **A per-mention resolution log written on every capture.** Rejected: it writes
  on every untouched capture and breaks the byte-identical guarantee; the prior
  processed artifact already records each resolution.
* **Treating a shared first name as ambiguous.** Rejected: every Ledger with a
  "Sam" and a "Sam Rivera" would start holding mentions back. Reconcile surfaces
  the overlap with its fix instead.
* **Folding a mention into the dormant holder.** Rejected: dormant says "not who
  I mean now"; folding into them would undercut that.
