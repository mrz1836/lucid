# ADR-0012 — Gratitude by-meaning matching and outward expression: three tiers behind the canonical-key seam, a local-first judge, a tally-neutral "expressed" record

**Status:** Accepted. The provider trust-gate *outcome* (§6) is recorded here
once its fixture evaluation has run; the decision to gate is part of this
record now.

## Context

The gratitude tally ([`../gratitude.md`](../gratitude.md)) shipped with
canonical-key matching only: `add` normalizes the phrase, derives the salted
key, and bumps a live entry with that key or creates a new one. That matching
stops at string canonicalization, and the v1 spec named the key function as the
seam the retro item **R-011** would later fill with by-meaning matching.

The need is real. Nightly phrasings are short ("my bike", "clean water") while
the stored entries they belong to are often elaborated ("the two wheels that
carry me to work", "clean drinking water straight from the tap"). Canonical keys never connect those, so
every such night either splits the tally into near-duplicates or needs a human
to pass `--into <id>` by hand. The tally's value is a true count, not a pile of
near-duplicates. The hardest cases share **no words at all**, so no lexical
method can reach them. Real near-ties also exist: one short phrase that
plausibly belongs to either of two entries. A confident wrong merge there would
be worse than asking.

The tally also counts gratitude held privately. The natural next step is saying
it to the person involved. Doing that without turning it into a score — a
streak, a quota, an "unthanked" backlog — is the Sanctuary constraint (§0 of the
spec).

Four binding constraints shape every choice below:

* **P9 — the runtime never depends on AI.** A nightly `add` and the whole of
  `list` must complete with no model.
* **P3 / Sanctuary.** `internal/storage` is the only `~/.lucid/` writer,
  `internal/observations` stays model-free, and the agent-contracts sanctuary
  denylist keeps agent inference out of `~/.lucid/registries/`, where gratitude
  entries live.
* **Model access only through `internal/provider`.** Tests stub it with
  `provider.Fake` ([ADR-0006](0006-model-access.md)).
* **Append-only.** History is never rewritten, and a duplicate is folded and
  redirected, never deleted.

All examples here are synthetic.

## Decision

### 1. Three tiers, cheapest first — and tier 3 is a judge over the live list, not an embedding index

The match step behind the v1 seam runs up to three tiers and stops at the first
confident answer. Tier 1 is the unchanged canonical key. Tier 2 is a
deterministic normalized-token match (stopwords, light stemming, Dice
coefficient over token sets) against every live entry's `display_name` and
`aka[]`. Tier 3 is an optional model judge. Tiers 1–2 carry no model, so the
pure tier-2 scorer lives in `internal/observations` and imports no provider.

**Tier 2 uses no edit distance.** "my dog" / "my dad" is the false-positive
class ADR-0011 removed from person reconcile. **It scores with Dice, not
containment**, so a one-word phrase is not a confident match for every long
entry that happens to contain that word. Such cases fall through to tier 3,
which is built for exactly that. `aka[]` is **match evidence for tier 2**: every
wording that lands on an entry joins its `aka[]`, so the next night the same
wording is a deterministic exact hit. That leaves the model consulted only for
genuinely new phrasings.

This keeps ADR-0011's split between discovery and resolution. Tier-1 key
resolution still never scans `aka[]`. The tier-2/3 pass is discovery: it reads
entries in order to choose a target for an occurrence, the way `--into` does.

**Tier 3 judges the new phrase against the whole live list in one compact
request.** It does not re-rank tier 2's lexical shortlist. A shortlist built
from shared words can never contain the zero-overlap matches that motivate
R-011. A personal tally on the order of a hundred short wordings fits in a few
hundred tokens. A judge that sees the whole list can also answer "none of these".
**Embeddings with cosine similarity are deferred.** They would need a persisted,
rebuildable projection that has to be invalidated on every new wording. Their
similarity thresholds are model-specific and poorly calibrated for two- to
four-word phrases. They earn their keep only at a scale this tally does not
have. Above a configurable candidate cap the request is bounded: entries with a
tier-2 score come first, then the most recently tallied.

### 2. Confidence bands with a top-1/top-2 margin — tier 3 may auto-bump under the same rule

Each tier's ranked scores fall into one of three bands:

* **High** needs top-1 ≥ the tier's high cutoff **and** top-1 − top-2 ≥ the
  tier's margin. It means a clear single winner, and the add auto-bumps.
* **Ambiguous** is any top-1 at or above a shared floor that is not High. That
  covers a near-tie as well as a plausible but unconfident single match. The add
  suggests.
* **Low** is everything below the floor. The add creates a new entry.

**Why a margin and not just a high top-1:** a high top-1 with a close runner-up
is precisely the near-tie case, and auto-merging it would be a confident wrong
answer.

**Tier-3 matches may auto-bump.** The alternatives were suggest-only, or a
separate, stricter tier-3-only cutoff. Suggest-only would leave a confirm step
on exactly the zero-overlap cases this work exists to automate. The margin, a
receipt, and an attributable `match_tier` on the event are a sufficient safety
net at this scale. Tier 3 therefore has its own cutoffs, stricter by default:
high 0.90 and margin 0.20, against tier 2's 0.85 and 0.15, with a shared floor
of 0.50. All are `lucid.json` settings (`gratitude.match`), clipped fail-safe.
These are conservative placeholders, tuned against the synthetic fixtures during
the build.

**Combining tiers is a safer-outcome rule.** Tier 3 runs only when tier 2 is
Ambiguous or Low. A tier-3 High result auto-bumps and a tier-3 Ambiguous result
suggests. A tier-3 Low result falls back to tier 2's band, so **tier 3 can
promote an add to a match but can never turn a tier-2 ambiguity into a silent
create.**

### 3. When matching runs: at `add`, and in a dry-run `reconcile`

At `add`, the nightly act stays one line: a clear match bumps and a new thing
creates. Duplicates that already exist are cleaned up by `lucid gratitude
reconcile`, which is dry-run by default. `--apply` is the explicit confirmation,
and it folds only **tier-2 High pairs**, through the ordinary `merge` path.
Tier-3 reconcile pairs are **advisory only**. A model's answer can differ
between the dry run the user read and the run that applies, while the tier-2
set is a pure function of the store. Nothing on the automatic path ever merges
two existing entries; reconcile under an explicit `--apply` is the only fold,
and it is always a normal merge plus tombstone.

`import` / `add --count` stay canonical-key only, so a migration remains exactly
as deterministic as it was.

### 4. The ambiguous band never guesses: ask on a terminal, refuse and defer otherwise

On a terminal, `add` asks "did you mean to bump `<id>`?". Off a terminal, or
under `--json`, nobody is there to answer, and the band may neither merge nor
create silently. So `add` **refuses and defers**: it writes nothing, exits `1`,
and returns the structured suggestion, which the caller resolves with an explicit
`--into <id>` or the new `--new`.

The two rejected alternatives each break a rule:

* **Create, but flag it** breaks "never silently create" for scripted callers.
* **Suggest and exit 0** makes a no-op look like a success.

A harness agent running the nightly capture relays the suggestion to the human
in conversation. A human-confirmed suggestion lands exactly as `--into` does,
without a `match_tier` stamp, because the human made the call.

### 5. Audit, receipts, and an honest undo

An automatic bump appends an `occurrence` carrying `match_tier` (2 or 3) and
`match_score`. Its receipt is returned and its ack says "matched … by meaning".
A tier-1 bump carries neither, so it stays byte-identical to v1.

**No new undo verb is added.** A wrong automatic bump is corrected by tallying
the phrase `--into` the right entry. The mis-landed occurrence stays where it
landed, visibly attributed, and still counts there. That is the append-only
trade: an honest +1 of residue is preferred over rewriting history. The tally is
inventory, and a backup restore remains available if the residue matters.

A wrong `reconcile` fold is undone by restoring the pre-reconcile `lucid backup`.
This is the same escape hatch a partial `import` uses.

A `retract` event is a candidate follow-on if wrong automatic bumps prove
common; it is not built speculatively.

### 6. Provider default: local-first `ollama`, `claude_cli` opt-in — behind a trust gate

The judge is built through `internal/provider/factory` from the `provider` block,
with `gratitude.match.tier3_backend` / `tier3_model` overrides. Each is empty to
inherit, the same rule the companion's and workout's `model` keys use. The
**default is `ollama`**. Gratitude phrasings are intimate, and a local model
keeps the phrase and candidate wordings on the machine. `claude_cli` is the
**opt-in** alternative, with its egress documented: the same minimal payload
goes to the vendor's hosted model. An unrecognized backend value coerces to the
default, which fails safe toward local.

**The trust gate.** The local default is trusted only after its match quality is
measured against the synthetic fixture set:

* zero-overlap true matches;
* near-ties that must be suggested;
* different-meaning look-alikes that must not merge;
* genuinely new phrases that must create.

If the local model underperforms, the docs and config name `claude_cli` as the
recommended backend instead. Either way tier 3 stays optional, and with no
backend reachable `add` runs on tiers 1–2. The model name shipped as the local
default is a placeholder until this gate records the evaluated model.

**Trust-gate outcome:** _pending_ — the fixture evaluation runs once the tier-3
judge exists. This section will record the models evaluated, the per-fixture
results, and the resulting default backend and model. That update to this ADR,
together with the matching lines in `gratitude.md` §9 and the `lucid.json`
default, is the one design refinement recorded after the docs-first diff.

### 7. The judge's sanctuary reach — module side, bounded slice

The agent-contracts sanctuary denylist keeps agent inference out of
`~/.lucid/registries/`. The judge is not an inference agent mining that tree. It
is the gratitude layer's own match step, run inside a verb the user issued:

* the router hands it a bounded slice of **only** the phrase and the candidate
  wordings, numbered, with no keys, counts, dates, people, or journal text;
* it returns scores that the deterministic band rule acts on;
* it writes nothing and introduces no hypothesis.

That is the same module-side reach the workout compose documents, and it is
recorded as a contract note in [`../mvp/agent-contracts.md`](../mvp/agent-contracts.md)
§"Note — the gratitude match reach". Entries linked to an off-limits person are
withheld from the judge, fail closed. An `add --person` naming an off-limits
person skips tier 3.

The per-instance gate is `gratitude.match.tier3_enabled`. It defaults on only
because the default backend is local, so nothing leaves the device by default.
It does **not** extend `agent_slice_optins`, because no Reflection-class agent
gains any access. Widening real agent access to registry data still needs a
contract diff plus that opt-in.

### 8. Outward-expression shape: a `thank` subverb, and `add --person` as a link-only convenience

`lucid gratitude thank <id> --person <subject> [--day]` is the single home for
both the person link and the record of having expressed it. It keeps the nightly
`add` one-liner uncluttered. `add --person <subject>` links the entry an add
lands on, without writing an "expressed" record. Subjects resolve exactly as
the person write verbs resolve them, and are validated before any write.

Reusing the generic `--to person:person_<slug>` link-ledger grammar was
rejected. It has no natural home for the "expressed" event and is less
discoverable for this use.

Linking an off-limits person is allowed, because it is the user's own private
record and off-limits means off-limits to inference. Such a person is never
named in a reminder.

### 9. How expression is recorded: schema 2 and a tally-neutral `expressed` event

Expression is a new history event type, `expressed` (with `person` and a logical
`date`), under `schema = 2`. Alongside it:

* the entry gains an optional, grow-only `people[]` list of links, absorbed on
  merge like `aka[]`;
* a merge event carries the source's last-expressed dates, so the fold stays
  local.

`Tally()` ignores `expressed`, so **telling someone never moves the count**.
Every schema-2 field is optional: schema-1 entries read unchanged and are
written back as schema 2 on their next write, with no migration pass, and
readers accept both.

The receipt id grammar is unchanged, and every `thank` returns its own receipt.

### 10. The reminder lives with `list` only, and never counts

The "you might tell them" nudge surfaces only alongside `lucid gratitude list`
(and its `--json` `reminders` array). It is offered for a linked person who is
not off-limits and whom the user has never recorded telling, or not since the
entry last came up. It is capped at three quiet lines, most recently tallied
first, with **no** "and N more", no percentage, no streak, no quota, no backlog
count, no overdue wording, and no escalation. Tests assert that none of those
render. Wiring the nudge into the check-in or reflection flow is deferred: the
nudge should be offered, never load-bearing, and the change stays contained to
the gratitude surface.

### 11. Tier 1 forwards through a merge tombstone

In v1, a plain `add` whose canonical key named a merge tombstone refused. A
wording merged away last month therefore errored the next time it was written.
Tier 1 now forwards that single hop to the live destination (the wording joins
its `aka[]`), matching the "always resolves to the canonical live entry" rule
and the forward resolution person tombstones already give. A live-key tier-1 hit
is unchanged.

## Consequences

* The nightly `add` stays one line, and repeated wordings converge on the right
  entry: first through tier 3 or a single confirmation, and thereafter
  deterministically through `aka[]` at tier 2. `--into` becomes rare.
* No silent wrong merges. Near-ties and look-alikes surface as suggestions.
  Every automatic landing is an attributable, receipted event, and the only fold
  of two existing entries is a human-confirmed, deterministic
  `reconcile --apply` through normal merge semantics.
* P9 holds by construction. Tiers 1–2, `list`, `import`, `merge`, `thank`, and
  `reconcile --apply` never need a model, and a tier-3 outage degrades an `add`
  to tiers 1–2 with exit `0` and a visible note. Every tier-3 test runs against
  `provider.Fake`.
* Egress is minimal and, by default, zero. Only phrases leave the process, only
  to the configured judge, and by default that judge is local.
* Undo is honest rather than magical. A wrong automatic bump leaves a visible +1
  of residue; a wrong fold needs a backup restore. If wrong bumps prove common,
  a `retract` event is the documented next step.
* The record schema moves to 2 additively: no migration, no reader breakage, and
  an unchanged tally for every existing entry. Outward expression adds a private
  record and a gentle nudge, and no metric of any kind.
* The ambiguous band exits `1` in non-interactive mode, so scripted callers must
  handle a suggestion as a distinct, parseable outcome
  (`"status": "suggestion"`), not as an I/O error.
