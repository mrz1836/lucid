# Lucid — Technical Specification

*The reference implementation architecture for the full system. The
canonical design is [architecture.md](architecture.md) — the Mirror
and the Engine over one user-owned Ledger, under ten principles — and
the first buildable slice is [mvp/scope.md](mvp/scope.md). This
document describes the destination they point at: the agent
architecture, the deterministic modules beside it, the memory model,
and the data layer. See [vision.md](vision.md) for the vision.*

---

## Agent Architecture

The Mirror is powered by a modular set of **framework-agnostic
agents**:

| Agent | Purpose |
|-------|---------|
| **Intake Agent** | Captures daily check-ins, quick thoughts, any user input |
| **Structuring Agent** | Extracts entities, themes, connections from raw entries |
| **People Agent** | Detects new people, prompts for profiles, maintains the relational map |
| **Therapist Agent** | Identifies emotional patterns, fears, wounds, growth edges — cartography, never treatment |
| **Coach Agent** | Tracks goals, suggests actions, celebrates progress — voice and encouragement, never teeth: accountability for committed practices belongs to the Engine ([engine.md](engine.md)) |
| **Framework Agent** | Applies the user's consented interpretation lenses ([frameworks.md](frameworks.md)) to any entry or insight |
| **Reflection Agent** | Generates daily/weekly/monthly/yearly summaries |
| **Consolidation Agent** | Strengthens memory connections, surfaces patterns, maintains the memory graph |
| **Safety/Consent Agent** | The last gate on every agent-authored outbound message: enforces hypothesis language, the phrase blocklist, and the no-external-action rule |

> **MVP subset** (see [`docs/mvp/architecture.md`](mvp/architecture.md)
> §"Mapping `technical-spec.md` agents to MVP modules"):
> the MVP ships **Intake**, **Structuring**, **Reflection**, and
> **Safety/Consent**. **People** is reduced to an extractive
> deterministic routine (no profile prompts, no relational map).
> **Therapist**, **Coach**, **Framework**, and **Consolidation** are
> deferred; their seams are named in
> [`docs/mvp/agent-contracts.md`](mvp/agent-contracts.md)
> §"Optional / deferred contracts".

**Framework-agnostic architecture:**
* The **Framework Agent** is a translation layer applying the user's
  consented lenses per [frameworks.md](frameworks.md): definitions
  are shareable specs, so adding a framework means adding a
  definition file, never changing agent code
* One labeled lens per message; multi-lens walks and composite
  lenses per frameworks.md §4
* Every other agent produces lens-neutral material — the record
  itself is lens-neutral forever

Each agent is replaceable, extensible, and customizable. This makes Lucid **a platform**, not a static tool.

---

## The Deterministic Modules

Beside the agents sit modules that are deliberately **agent-free**
(architecture P9: the runtime never depends on AI). They share the
agents' storage adapter and router but contain no reasoning step —
if every model is down, they still run.

* **The Engine** ([engine.md](engine.md)) — close-out capture, streak
  arithmetic, template escalation, the storm protocol, clock
  profiles. Its records are readable by witness-facing projections in
  topline form only, and no agent reads them (architecture P3):
  enforcement and reflective content never touch.
* **The observation layer** ([observations.md](observations.md)) —
  structured body/context events (pain, intake, elimination, mood,
  sleep, measurements, memory fragments) on a frozen event envelope;
  registries for long-lived referents (injuries, threads, places,
  eras — generalizing the `people` pattern); deterministic enrichers
  (weather, daylight) appending the world's half of each day.
  Projections — series, episodes, correlates, the clinician packet —
  are computed views, and correlation findings route through the
  resonance gate like any inference.
* **The Scientist** ([scientist.md](scientist.md)) — pre-registered
  self-experiments as a lifecycle over existing parts: one registry,
  two event kinds, deterministic verdict scripts at Retro/Gate
  cadence. Not a subsystem; a practice with a notebook.

---

## The Consolidation Process

The Consolidation Agent runs periodically in the background—a kind of "dream state" where the system processes, connects, and strengthens what it knows about you.

| Frequency | What Happens |
|-----------|--------------|
| **Nightly** | Light processing: increase activation scores for today's memories, find immediate connections, flag potential patterns |
| **Weekly** | Deeper analysis: strengthen memories that appeared multiple times, connect this week's themes to historical patterns, surface insights for review |
| **Monthly** | Comprehensive review: evaluate salience scores, promote repeated patterns to higher salience, archive low-activation ephemeral data, generate a "state of mind" summary |

This mimics how human memory works—consolidation during rest strengthens important memories and lets unimportant ones fade. The difference is Lucid can surface what it finds: "I noticed this week's frustration with your boss echoes a pattern from three months ago. Worth exploring?"

The consolidation cascade and the adaptive-evolution loop (below) sit
beyond the MVP ([mvp/scope.md](mvp/scope.md)); until they land, the
weekly `/reflect` and the Engine's Retro are their human-run form.

---

## Historical Reprocessing

When you add a historical entry—something from your past that wasn't in the system before—it can change how everything afterward is understood. A core wound from childhood, once added, might recontextualize patterns the system identified months ago.

**How it works:**
1. Historical entry is stored with correct temporal placement
2. System assesses the entry's salience (how foundational is it?)
3. Higher-salience entries trigger wider reprocessing scope
4. Affected downstream data (patterns, insights, connections) is queued for re-evaluation
5. Reprocessing runs during the next consolidation cycle

**What gets re-evaluated:**
* Patterns identified after the historical event's date
* Insights that might now have deeper context
* Memory connections that could be strengthened or revised
* Relational dynamics that may trace back to the new information

**Notification settings (configurable):**

| Setting | Behavior |
|---------|----------|
| **Surface for review** (default) | "Based on what you shared about [X], I've recontextualized 3 insights. Want to see them?" |
| **Silent** | Updates automatically, surfaces in next reflection |
| **Ask first** | "This may affect existing insights. Proceed with reprocessing?" |

---

## Adaptive Evolution

Lucid doesn't just learn *about* you—it learns *how to work with* you.

Over time, the system adapts:
* **Prompts** — Questions evolve based on what resonates. If you respond deeply to IFS-style parts work, the system uses more of that.
* **Commands** — Your `/checkin` flow becomes personalized. New commands emerge based on your patterns.
* **Agent behaviors** — The Coach learns your motivation style. The Therapist learns how deep you want to go.
* **Timing** — The system learns when you're most reflective, when you need gentle versus direct.

**How it learns:**
* Response depth (did you engage or dismiss?)
* Explicit feedback ("this resonated" / "not quite")
* Edits to agent-generated drafts
* Time spent on reflections

**Configurable autonomy:**

The system starts conservative—every proposed adaptation requires your explicit approval. As trust builds, you can grant more autonomy:

| Level | Behavior |
|-------|----------|
| **Conservative** | All adaptations require explicit approval (default) |
| **Moderate** | Minor tweaks auto-apply; major changes need approval |
| **Autonomous** | System adapts freely; surfaces changes for review |

Whatever the level, every adaptation is recorded with a reason and is
reversible — nothing changes silently (architecture P8). You can see
exactly how your experience has evolved over time, and revert any
change that doesn't work.

---

## User Commands

One router, three command families
([mvp/scope.md](mvp/scope.md) §4 is the shipped subset; this is the
full surface):

| Family | Command | What it triggers | Status |
|--------|---------|------------------|--------|
| Mirror | `/log` | Quick thought capture | MVP |
| Mirror | `/checkin` | Guided check-in flow → one possible pattern | MVP |
| Mirror | `/reflect [gate]` | Weekly recall of validated insights; the gate variant recalls everything and appends the panel numbers | MVP |
| Mirror | `/ask` | Grounded Q&A over validated insights, with citations | MVP |
| Mirror | `/person <name>` | Deterministic person view: mentions over time, cited insights, dominance share — grows into the relational-map explorer | MVP |
| Mirror | `/bootstrap` | Teach Lucid your life history; proposals suppressed until `/bootstrap done` | MVP |
| Mirror | `/lens <id>[,…]` | Read an entry through one or several consented lenses ([frameworks.md](frameworks.md) §5) | Post-MVP |
| Mirror | `/me` | The psychological Profile view: fears, loops, values, growth edges — every element resonance-gated | Post-MVP |
| Mirror | `/goals` · `/progress` | Coach surfaces: goal trees, progress across pillars | Post-MVP |
| Engine | `/closeout` (`skip`, `backfill`) | The two-minute nightly close-out; honest misses; corrections | MVP |
| Engine | `/mode` | Declare today's Green/Yellow/Red before the bell | MVP |
| Engine | `/status` | Ambient state: streak, honest-number pairing, budget, gates | MVP |
| Engine | `/storm` | The pre-committed incapacity state, witness-confirmed | MVP |
| Engine | `/profile <name>` | Switch clock profiles (Bell, tripwire, rollover move together) | MVP |
| Observations | `/pain` `/ate` `/drank` `/bm` `/mood` `/slept` `/obs` | One-line micro-logs on the frozen envelope | MVP |
| Observations | `/day [date]` | One logical day, joined across everything | MVP |
| Observations | `/packet clinician` | Render the clinician packet; review before release | MVP |

---

## Skills (Reusable Capabilities)

Agents invoke shared skills for common operations:

| Skill | What it does |
|-------|--------------|
| `extract_emotions` | Identify emotions in text |
| `extract_people` | Identify people mentioned, detect new vs known |
| `extract_themes` | Identify recurring themes |
| `match_patterns` | Find similar past entries |
| `calculate_progress` | Determine goal progress (supports nested hierarchy) |
| `generate_insight` | Create an insight from patterns |
| `apply_framework` | Interpret through a consented lens ([frameworks.md](frameworks.md)) |
| `query_history` | Search past entries with filters |
| `prompt_person_profile` | Generate prompts to enrich a newly detected person |
| `link_to_goals` | Connect entries/insights to relevant goals |
| `consolidate_memories` | Strengthen connections, adjust salience, archive dormant memories |
| `traverse_graph` | Follow memory connections to surface related context |
| `adapt_approach` | Modify agent behavior based on accumulated feedback |
| `process_historical_cascade` | Re-evaluate downstream patterns and insights after historical entry is added |

---

## Data Layer

### Three-Layer Model

| Layer | What It Contains |
|-------|------------------|
| **Raw — the Ledger** | Everything captured, exactly as given: Mirror entries, Engine day records, observations — peers in one append-only, bitemporal store. Timestamped, unmodified, immutable. This is the ground truth—never altered. |
| **Processed** | Entities extracted by agents: people, places, events, emotions, themes, connections. Can be re-processed as agents improve. |
| **Knowledge** | Your psychological profile, relational map, goal state, framework preferences. This layer grows and changes over time. |

**Design principles:**
* Raw is permanent — never lose the original input
* Processed is rebuildable — can re-extract if agents improve
* Knowledge is mutable — your understanding of yourself changes
* Every derived layer — Processed, Knowledge, streaks, balance views —
  is a rebuildable projection over the Ledger (architecture P2)
* User can see all layers — transparency about what the system "thinks"
* User can correct any layer — the system learns from corrections

### Temporal Architecture

Every entry carries two timestamps:

| Timestamp | Meaning |
|-----------|---------|
| **Recorded** | When you wrote this (always "now") |
| **Occurred** | When it happened (can be past, can be a range) |

For entries about the present, these are the same. For historical entries, they differ—and the system uses both intelligently.

Temporal precision varies:

| Precision | Example | How It's Stored |
|-----------|---------|-----------------|
| **Exact** | "March 15, 2019" | Single date |
| **Approximate** | "Spring 2019" | Date with precision flag |
| **Range** | "2015-2019" | Start and end dates |

This temporal architecture enables:
* Accurate timeline reconstruction
* Historical entries placed in correct context
* Queries like "How did I feel about work in 2020 vs now?"
* Reprocessing of downstream data when historical context is added

### Multi-Dimensional Memory

Not all memories are equal. Some define who you are; others are passing observations. Lucid treats memories as multi-dimensional rather than forcing them into rigid categories.

Every memory has four dimensions:

| Dimension | What It Measures |
|-----------|------------------|
| **Salience** | How foundational is this? (ephemeral → significant → core) |
| **Type** | What kind of memory? (factual, emotional, relational, insight, pattern) |
| **Confidence** | How certain are we? (inferred → validated → user-stated) |
| **Activation** | How recently accessed? (dormant → latent → active) |

A single memory can be many things at once. "My father never said he was proud of me" is simultaneously:
* **Core** (shapes your identity)
* **Emotional** (carries deep feeling)
* **Relational** (about a person)
* **User-stated** (high confidence—you told the system directly)

This multi-dimensional approach allows the system to:
* Strengthen memories that keep appearing (activation increases salience)
* Connect related memories across time and context
* Let ephemeral observations fade unless they prove significant
* Give more weight to things you've validated vs. things it inferred

### Memory Graph

Memories don't exist in isolation—they connect. Lucid maintains a graph of relationships between memories, people, patterns, and goals.

Connection types include:
* **Supports** — one memory reinforces another
* **Contradicts** — memories that conflict (worth exploring)
* **Relates-to** — thematic connection
* **Caused-by** — causal relationship
* **Example-of** — specific instance of a broader pattern

This enables powerful traversal: "Show me everything connected to my fear of rejection" returns not just the fear itself, but the memories that formed it, the people involved, the patterns it creates, and the goals it blocks.

---

## Database Schema (SQLite)

**Conceptual schema.** The MVP ships this as plain files
([mvp/data-model.md](mvp/data-model.md)); the tables below are the
migration target — the storage adapter's named ops stay the same,
their implementation moves.

Mirror:

* `entries` — raw stream (immutable), includes: recorded_at (when entered), occurred_at (when happened), occurred_at_end (for ranges), temporal_precision (exact/approximate/range)
* `sessions` — one row per chat thread / capture session: the audit trail for capture and the source of Reflection's `recent_window`
* `entities` — extracted people, places, events
* `emotions` — emotional data points
* `themes` — recurring patterns
* `goals` — goal definitions (supports hierarchy: goal → sub-goal → task)
* `goal_links` — connections between goals (parent-child, related)
* `goal_progress` — progress events
* `profile` — psychological profile elements
* `people` — first-class person entities
* `person_entries` — links between people and entries mentioning them
* `relationships` — your relationship to each person, dynamic state
* `frameworks` — lens definitions ([frameworks.md](frameworks.md) — shipped as spec files, indexed here)
* `user_frameworks` — the consented stack: which lenses are active, consent timestamps
* `insights` — validated insights, with rules and lens provenance
* `reflections` — generated reflection documents
* `memories` — multi-dimensional memory store with salience, confidence, activation scores
* `memory_connections` — graph edges between memories (type, strength)
* `adaptations` — learned system behaviors and their evolution history
* `reprocessing_queue` — historical entries awaiting cascade processing, tracks scope and status

Engine (deterministic; no agent reads these — architecture P3):

* `chains` — chain config including clock profiles, versioned at Retros
* `day_records` — per logical day: link states, declared mode, capacity, floor/miss/storm flags, corrections
* `escalations` — L1/L2 events with dead-man provenance
* `witnesses` — contract, consent record, lifecycle history
* `storms` — clauses, ambush windows, declaration/confirmation history

Observations:

* `observations` — the frozen event envelope + per-kind JSON payload
* `registries` — long-lived referents: injuries, threads, places, eras, experiments
* `enrichment_log` — the outbound audit trail for enricher fetches

**Principles:**
* SQLite for the packaged app (simple, local, portable); plain files first
* Schema designed to evolve (migrations supported)
* Full export capability (your data is yours)
* Encryption at rest for sensitive data

---

## Query Capabilities

The system supports questions like:
* "How did I feel about work 6 months ago vs now?"
* "Show me all entries where I mentioned [person]"
* "What patterns repeat in my romantic relationships?"
* "What was I struggling with this time last year?"

---

## Progressive Unlocking

Features unlock as you participate:

| Milestone | Unlocks |
|-----------|---------|
| First 3 check-ins | Weekly reflection summaries |
| A first week of entries | Pattern detection activated |
| First month of entries | Monthly deep-dive report |
| 10 validated insights | "Shadow work" suggestions |
| 3 months of data | Yearly narrative begins compiling |

Features aren't hidden to frustrate—they're gated because **they
require data to be meaningful.** Milestones count volume, never
consecutiveness: streaks belong to the Engine, and only ever for
committed acts (architecture P3).

---

## Bootstrapping Mode

When you first start Lucid, you need to teach it about your life. Bootstrapping mode is designed for this:

* Enter with `/bootstrap` — "I'm teaching you about my life"
* Add historical entries, key people, formative events, past wounds—as much as you want
* The system batches reprocessing (doesn't analyze after every entry)
* Pattern notifications are suppressed during teaching
* Exit with `/bootstrap done` — triggers comprehensive consolidation
* The system surfaces a summary: *"I've learned about X people, Y major events, Z patterns. Ready to begin."*

You can re-enter bootstrapping mode anytime—when you remember something important, start therapy, or want to add context you didn't share before.
