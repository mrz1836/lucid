# Lucid — The Frameworks Layer (interpretation lenses)

**Date:** 2026-07-03 · **Status:** Canonical — a living concept, evolving with the project
**Scope:** The pluggable-interpretation layer named in
[`architecture.md`](architecture.md) §7 and the Mirror's
"framework-based interpretation" responsibility (§3): what a framework
definition is, how the user's stack is consented and stored, exactly
where lenses act, and the safety mechanics that let a lens speak its
own vocabulary without ever becoming a diagnostic engine. This
document contains no instance data; which lenses a user runs lives in
their Charter and calibration.

**Position.** This layer is the vision's core promise made
mechanical — "the system adapts to your worldview, not the other way
around" ([`vision.md`](vision.md) §4). It ships post-MVP, behind a
hardened Reflection/Safety pipeline
([`mvp/agent-contracts.md`](mvp/agent-contracts.md) §Framework), and
it is the prerequisite for protocol
[P-2](protocols/P-2-lens-rotation.md) and the future aperture
translation layer ([`vision.md`](vision.md) §7 — the therapy packet
rendered in the therapist's own modality).

## 0. The governing rule

**A lens colors interpretation; it never touches the record, and it
never hardens into diagnosis.** Raw entries, observations, and
processed artifacts are lens-neutral forever — capture and extraction
run identically whatever the user believes. A framework acts only
where interpretation was already happening, and everything it produces
remains a hypothesis under the resonance gate: no framework overrides
the gate, the sanctuary rule (P3), or the off-limits registry
(architecture §7). The user's worldview is sovereign — the system
adapts to it, never the reverse — and sovereignty cuts both ways: no
lens the user did not choose ever frames a single sentence.

## 1. Position in the design

The frameworks layer is **base design, not an extension**: architecture
§3 names framework-based interpretation as a Mirror responsibility and
§7 declares interpretation pluggable, so no extension-axis or
new-subsystem question arises. What this document adds is the
contract: definition files, consent mechanics, seams, and safety.

**Definitions are specs** — the shared-genome pattern
([`scientist.md`](scientist.md) §7, Layer 0). A framework definition
lives in [`frameworks/`](frameworks/stoicism.md) as a versioned file
containing zero instance data: anyone can fork it, improve it, or
write their own tradition's lens as a doc diff. The user's *stack* —
which lenses, in what role, meaning what to them — is instance data
and lives in the Charter (§3 below).

| Shared (repo) | Personal (instance) |
|---------------|---------------------|
| `docs/frameworks/<id>.md` — vocabulary, stance, question templates, licenses, boundaries | Charter §4 — what each lens means *to you*; `lucid.json` — the machine-readable stack and consent record |

## 2. The definition file

One file per framework, `docs/frameworks/<id>.md`, with YAML
frontmatter and fixed sections. Binding schema:

```markdown
---
id: stoicism
version: 1
name: Stoicism
lineage: "Epictetus, Marcus Aurelius, Seneca; modern: Hadot, Irvine"
licenses: []          # blocklist patterns this lens unlocks — §6
composes: []          # parent lens ids, for composite lenses — §4
---
```

* **The lens, in one paragraph** — what this way of seeing *is*, in
  plain language a non-adherent can check.
* **Vocabulary** — the lens's terms with one-line meanings, so agent
  and user share definitions (dichotomy of control; parts, protectors,
  exiles; feelings and needs).
* **How this lens asks** — 3–5 question templates in Lucid's voice,
  hypothesis-framed. These shape Reflection's framing, never Intake's
  (§5).
* **How this lens reads** — the interpretation stance: what it
  foregrounds, what it deliberately ignores.
* **Reframe shapes** — 2–3 worked examples of a lens-framed proposal,
  each labeled and ending in the resonance question.
* **Boundaries** — what this lens must never do, binding (§7). Every
  definition carries at minimum the clinical line and the doctrine
  line.
* **Sources** — where the tradition's actual content comes from, so a
  definition can be checked against its lineage rather than against
  vibes.

`licenses:` is almost always empty; §6 defines the rare exception.
Adding a framework is a doc diff following this schema — proposed,
reviewed, versioned like any spec
([`mvp/claude-code-workflow.md`](mvp/claude-code-workflow.md)). A
definition change never retro-colors anything: insights keep the
provenance of the version that framed them.

## 3. The stack — consent mechanics

**The stack is standing consent, one line per lens.** Charter §4
("the lenses, yours") holds the human side — one paragraph per
framework on what it means to the user. The machine side lives in
`lucid.json`:

```json
"framework_stack": ["stoicism", "nvc"],
"framework_consents": {
  "stoicism": "2026-07-05T18:00:00-04:00",
  "nvc": "2026-07-05T18:02:00-04:00"
}
```

Binding rules:

* **Joining the stack is a Charter act** — written at calibration or
  amended at a quarterly review with reasons (P8). A lens in the stack
  may frame proposals without further asking; that is what the consent
  bought.
* **A trial is not the stack.** Any defined framework may be invoked
  once, per situation, via the interpret surface (§5) — with a
  first-use consent ask (*"This reads your entry through <name> —
  want that?"*), recorded. Trials never join the stack by
  accumulation; joining is always the deliberate Charter act.
* **Leaving is never gated.** Removing a lens takes effect immediately
  at any time — narrowing consent is never held to review cadence
  (the apertures precedent: depth can always narrow). The removal is
  recorded with a reason like any config change.
* **Guidance: keep the stack small** (≤ 3 active is the default
  posture). A stack is a worldview, not a menu; the interpret surface
  exists for the occasional other lens.

**Modes are orthogonal to lenses.** Coach/Mentor/Reflect/Echo
(architecture §6) set *how* the system engages; the lens sets *which
interpretive vocabulary* it thinks in. "I don't need comfort, I need
a plan" changes mode; "give me the Stoic read" changes lens.

## 4. One lens per message; any number per invocation

* **Exactly one lens frames any single message.** This keeps
  attribution clean (P-2 depends on it), keeps traditions honest, and
  prevents mush. It does not mean one lens at a time in any wider
  sense — the next two mechanisms are how "more than one" works:
* **Multi-lens invocation.** One interpret call may walk several
  lenses — the vision's Interpret table
  ([`vision.md`](vision.md) §6) made invocable: `/lens nvc,ifs,stoicism
  <entry>` (or `/lens stack` for the whole stack) returns one labeled
  message per lens, in order, over the same material. Layering —
  NVC to find the need, IFS to find the part, Stoicism to find the
  cut, Watts to hold it lightly — is exactly this: a walk, not a
  blend.
* **Composite lenses — "your own combination" as a first-class
  definition.** The vision promises users can combine frameworks into
  their own lens; the sanctioned form is a definition file like any
  other, with `composes:` naming its parents (e.g. `composes: [nvc,
  ifs]`) and its own vocabulary, questions, and reframe shapes doing
  the actual blending — authored, versioned, and consented **as
  itself**. A composite counts as exactly one lens everywhere: one
  Charter paragraph, one label, one `provenance.framework` entry, one
  arm in P-2. Binding rule: **licenses never inherit** — a composite
  unlocks nothing its own `licenses:` list does not explicitly
  re-declare, because consent must see what it buys.
* **Proposals stay single-lens.** `reflection.propose` frames under
  exactly one lens (composite included) per proposal; the multi-lens
  walk is the interpret surface's job, where the user asked for
  perspectives rather than a pattern.
* **Labeling is mandatory.** Every lens-framed output names its lens
  inline (*"Through the Stoic lens: …"*; a composite by its own
  name) so the user always knows which glasses are on. The unlabeled
  trusted-advisor voice
  ([`mvp/product-principles.md`](mvp/product-principles.md) §6)
  remains the baseline and the default.
* **The resonance question survives every lens.** A lens-framed
  proposal still ends "does this resonate?" — a framework changes the
  vocabulary of the hypothesis, never its epistemic status.

## 5. The seams — where lenses act

* **`reflection.propose`** — the router passes the active lens
  (`framework: <id> | null`) with the slice; the agent applies the
  definition's question templates and reframe shapes. `null` means
  the baseline voice; the stack's first lens is the default once the
  layer ships, rotated only by explicit choice (or by protocol P-2
  under its pre-registration). Structuring and Intake are **never**
  lens-aware — the scribe and the extractor stay neutral (§0).
* **The interpret surface** — a user-invoked, post-MVP command
  (working name `/lens <id>[,<id>…] [<entry>|last]`, plus `/lens
  stack`): read one entry (or the current situation) through one or
  several named lenses — one labeled message per lens (§4). This is
  the vision's Interpret table ([`vision.md`](vision.md) §6) made
  invocable — and the home of trials (§3). Read-only; writes nothing;
  proposals stay `/checkin`'s job.
* **Recall framing** — `/reflect` may surface an insight in the lens
  that produced it (provenance, below); it never re-frames an insight
  into a lens the user did not accept it under.
* **Provenance.** Every lens-framed accepted insight records
  `provenance.framework` ([`mvp/data-model.md`](mvp/data-model.md)) —
  which lens, which definition version. This is what makes P-2's
  verdicts checkable and future translation honest.
* **The translation layer (future).** Apertures
  ([`vision.md`](vision.md) §7) will render the same record in a
  recipient's modality — a CBT therapist receives thought-record
  shapes, an IFS therapist parts language. That is this layer pointed
  outward, and it inherits everything here: definitions as specs,
  one lens per artifact, provenance, and review-before-release. It
  ships with apertures, not before.

## 6. Safety — the licensing mechanism

The phrase blocklist
([`mvp/product-principles.md`](mvp/product-principles.md) §6) bans
clinical-label vocabulary outright — which collides with a lens the
user explicitly chose (attachment theory cannot speak without saying
"attachment"). The resolution is narrow, explicit, and one-directional:

* A definition file may **license** specific blocklist patterns in its
  `licenses:` frontmatter — and Safety/Consent honors a license only
  when **all three** hold: the framework is in the consented stack
  (or a recorded trial is in flight), the candidate output carries
  that lens's label, and the output is hypothesis-framed.
* **Certainty is unlicensable.** The patterns `you always/never`,
  `clearly/obviously`, `I diagnose / you're suffering from`, and the
  `you're / you have <label>` forms can never appear in any
  `licenses:` list — no lens, however consented, gets to tell the
  user who they are. *"Through the attachment lens, this reads like
  an anxious pattern — does that fit?"* is licensable;
  *"you're an anxious attacher"* is not, under any lens, ever.
* Licenses are per-pattern and visible in the definition file the user
  consented to — the consent line covers the vocabulary it unlocks.
  Most definitions license nothing;
  [`frameworks/attachment-theory.md`](frameworks/attachment-theory.md)
  is the worked example of one that must.
* Everything else in the safety stack is untouched: Safety/Consent
  remains the last gate on every agent-authored message, the
  resonance gate governs every proposal, and the off-limits registry
  excludes at every layer — a lens cannot see what inference cannot
  see ([`mvp/agent-contracts.md`](mvp/agent-contracts.md),
  cross-cutting rules).

## 7. Boundaries

* **Lens ≠ therapy.** IFS-informed language is not IFS therapy; CBT
  shapes are not treatment. The clinical boundary (architecture §6)
  binds every lens: pattern cartography, never diagnosis or treatment,
  and wound-level work belongs with a human professional. Every
  therapy-derived definition carries this line in its Boundaries
  section, verbatim in spirit.
* **Lens ≠ doctrine enforcement.** A spiritual framework serves the
  user's stated belief; it never evangelizes, never adjudicates
  orthodoxy, never appears unchosen. The user's paragraph in Charter
  §4 — what this tradition means *to them* — outranks the definition
  file's rendering of the tradition wherever they differ.
* **The record is lens-neutral** (§0). If every framework were deleted
  tomorrow, the Ledger would be untouched and every accepted insight
  would still stand in the user's own accepted words.
* **No lens in the Engine, ever.** Bells, escalations, and templates
  have no voice and no worldview; the frameworks layer is Mirror-side
  only.
* **Ships post-MVP.** The MVP keeps its single implicit voice; this
  layer lands as the contract diff
  [`mvp/agent-contracts.md`](mvp/agent-contracts.md) §Framework names,
  once Reflection/Safety are hardened in live use. It must ship
  before the aperture translation layer, and P-2 stays blocked until
  it does.

## 8. Initial definitions

Shipped as reference implementations of the schema —
[`frameworks/stoicism.md`](frameworks/stoicism.md),
[`frameworks/nvc.md`](frameworks/nvc.md),
[`frameworks/ifs.md`](frameworks/ifs.md),
[`frameworks/attachment-theory.md`](frameworks/attachment-theory.md)
(the licensing exemplar),
[`frameworks/eight-dates.md`](frameworks/eight-dates.md), and
[`frameworks/four-agreements.md`](frameworks/four-agreements.md).

**A lens does not have to be a whole tradition.** Eight Dates and the
Four Agreements are **book-scoped lenses** — one book's practice,
honestly bounded: the schema doesn't care how wide a lens is, only
that the lineage line says how far it reaches and the Boundaries
section says what it must never do. A single essay, a single practice,
or a single sentence a user lives by is authorable the same way.

The wider set the vision names — the full Gottman method, CBT, ACT,
Taoism, Kabbalah, Christianity, Watts — are authorable by the same
schema, each a doc diff; so are **composite lenses** (§4), which is
how a user's personal blend becomes a citable, consentable thing. A
tradition Lucid has never heard of is authorable the same way; that
is the point of definitions being specs.

## 9. Defaults

One lens per message, labeled; any number per invocation (§4) ·
proposals single-lens · composites count as one lens everywhere;
licenses never inherit · baseline voice is lens-null ·
stack guidance ≤ 3 active lenses (a composite is one) · joining the
stack at calibration or
quarterly review only (P8); leaving at any time · trials via the
interpret surface with a recorded first-use consent · certainty
patterns unlicensable, always · `provenance.framework` recorded on
every lens-framed accepted insight · Intake and Structuring
lens-free, permanently. All instance-overridable with reasons (P8),
except the unlicensable certainty patterns and the lens-neutral
record, which are structural.
