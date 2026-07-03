# **Lucid**

### *A Secure, AI-Powered System for Personal Growth, Self-Understanding & Life Management*

---

## **1. The Problem**

Most people have no system for understanding themselves.

They repeat the same patterns for decades. They lose track of what they're working toward. They forget the insights they've had. They don't see the connections between today's frustration and last year's wound. They don't notice that they keep having the same conflict with different people.

People have tools for tasks. People have tools for money. People have tools for health.

**But no one has a tool for their inner life.**

Lucid is that tool.

---

## **2. Why This Matters**

This isn't just another journaling app or habit tracker. Lucid fills a massive gap in how we live:

* **Clarity** where things feel chaotic
* **Structure** where things feel overwhelming
* **Insight** where things feel mysterious
* **Agency** where things feel stuck
* **Security** where vulnerability is needed
* **Growth** where patterns have persisted for years

It is a **foundational tool for becoming who you want to be**—built with the deepest respect for your data ownership, your worldview, and your humanity.

Lucid is built to answer one powerful question:

> **"What is actually happening in my life, how can I grow from it, and how can I show up better for the people and commitments that matter?"**

---

## **3. What Lucid Is**

Lucid is a **secure, AI-powered personal operating system** for your inner life. It plays five roles:

### **Journal**
A journal that remembers everything and finds the patterns you can't see. Daily reflections that compound into weekly insights, monthly patterns, yearly transformations.

### **Therapist**
A therapist that builds a living map of your fears, triggers, wounds, and growth edges—without judgment, without cost, available whenever you need it. It connects today's emotional spike to the wound you identified six months ago. It notices that you've mentioned feeling "not enough" in four different contexts this month. It asks: *"Does this resonate?"*

### **Coach**
A coach that tracks your goals, celebrates your progress, and gently reminds you of what you said mattered. It gives *actionable guidance*, not abstract ideas.

### **Engine**
A behavior layer with real teeth — because insight that is never applied is just a more articulate way of staying stuck. The Engine initiates and defends a small set of committed daily practices: a bell that starts the chain (never memory, never motivation), a floor version for the worst days, honest escalation when you slip — up to a human witness who sees only that you showed up, never what you said. Reflection tools fail without a behavior layer; behavior tools fail without a reflection layer. Lucid is deliberately both, with a hard boundary between them: the Engine enforces *acts*, and everything you *say* in reflection lives under absolute amnesty. Full design in [docs/architecture.md](docs/architecture.md) and [docs/engine.md](docs/engine.md).

### **Agent-Self**
An extension of you that helps you **act**. When your friend's birthday is coming up, Lucid drafts a message in your voice, suggests when to send it, and waits for your approval. When you've been meaning to reach out to someone, it proposes what you might say. When you made a commitment and forgot, it reminds you—with a draft ready to go.

And it does all of this through the **philosophical frameworks you believe in**—whether that's Stoicism, NVC, Kabbalah, IFS, or your own combination. The system adapts to your worldview, not the other way around.

Over time, it becomes the companion you wish you'd always had—one that truly knows you, holds your story across time, and helps you become who you want to be.

---

## **4. Core Concepts**

Three foundational ideas power everything Lucid does:

### **Life Pillars**

Your life is organized into customizable pillars:

* Health
* Emotions
* Relationships
* Work / Craft
* Money
* Home / Environment
* Growth / Learning
* Spirituality / Meaning
* Creativity / Play

Every entry is mapped to one or more pillars so you can see what's strong, what's neglected, what's turbulent, and what's trending. It creates a dynamic model of your life's balance.

### **Personal Profile**

This is Lucid's deepest layer—a living psychological map that evolves as you do.

Over time, the system learns and stores your:

* **Fears** — what you avoid, what triggers anxiety
* **Core wounds** — early experiences that still shape present reactions
* **Triggers** — specific situations, words, or dynamics that activate you
* **Emotional loops** — predictable sequences (e.g., criticism → shame → withdrawal → resentment)
* **Desires** — what you actually want vs. what you say you want
* **Personal values** — what you protect, what you sacrifice for
* **Attachment style** — anxious, avoidant, secure, or disorganized patterns
* **Defense mechanisms** — how you protect yourself
* **Repeated relational patterns** — the same dynamic playing out with different people

The profile isn't just a snapshot of who you are today—it's a living record of who you've been. You can look back: "What was I struggling with a year ago? How have I changed?"

This creates something unprecedented: **a longitudinal record of your inner life.**

### **Philosophical Frameworks**

Lucid doesn't impose a single philosophy. You choose which frameworks shape how the system thinks—and you can **combine multiple frameworks** to create your own lens.

Available frameworks include:

* **Stoicism** → discipline, emotional regulation, dichotomy of control
* **NVC (Nonviolent Communication)** → feelings & needs structure, empathy
* **IFS (Internal Family Systems)** → internal parts and sub-selves
* **Kabbalah** → desire, restriction, transformation
* **Christianity** → grace, forgiveness, love
* **Gottman** → relationship health markers, bids for connection
* **Alan Watts** → presence, detachment, playful acceptance
* **Attachment Theory** → anxious/avoidant/secure patterns
* **Taoism** → natural flow, wu wei
* **CBT** → cognitive distortions, thought records
* **ACT** → acceptance, defusion, values-based action

Frameworks aren't mutually exclusive. You can layer them:

* Use **NVC** to identify what you're feeling and needing
* Use **IFS** to explore which part of you is activated
* Use **Stoicism** to decide what's in your control
* Use **Watts** to zoom out and hold it all lightly

The system adapts its questions, labels, and guidance based on your active frameworks.

### **Companion Voice**

Lucid isn't a friend—it's a trusted advisor. The voice is professional but warm, honest to the point of bluntness when needed, and non-judgmental while still challenging you.

The system shifts between four modes based on context:

| Mode | When It Activates | How It Sounds |
|------|-------------------|---------------|
| **Coach** | Goals, accountability, action | Direct, motivating, focused on next steps |
| **Mentor** | Career, craft, decisions, growth | Wise, asks guiding questions, offers perspective |
| **Therapist** | Emotions, wounds, patterns, relationships | Gentle, validates first, then probes deeper |
| **Mirror** | When you need reflection, not advice | Echoes back what you said, highlights contradictions, no judgment |

Mode detection happens automatically—if you're processing grief, the system won't jump into coach mode. But you can always override: "I don't need comfort right now, I need a plan."

*(Naming note: architecture v2 renames the Therapist voice mode to **Reflect** and the Mirror voice mode to **Echo** — the old names collided with the clinical boundary and the Mirror subsystem. See [docs/architecture.md](docs/architecture.md) §6; the behaviors in the table above are unchanged.)*

The voice also adapts to your preferences over time. Some people want more warmth; others want it clinical and direct. Lucid learns which approach helps you move forward.

---

## **5. Your Agent-Self**

This is what makes Lucid different from every other reflection tool.

Lucid doesn't just help you understand yourself—it helps you **act**. Your agent-self is an AI extension that operates alongside you, helping you show up in the world.

### **What It Does**

**Message drafting:**
* Birthday wishes, thank-you notes, difficult conversations—drafted in your voice
* "Here's a draft for Sarah's birthday. It mentions the trip you took together last year and your inside joke about coffee. Sound right?"

**Relationship follow-through:**
* Not just reminding you to reach out, but proposing what you might say
* "You mentioned wanting to reconnect with David. Here's a low-pressure message based on your last conversation."

**Commitment support:**
* When you've made a promise or set an intention, the agent helps you follow through
* "You said you'd send that article to your brother. Here it is with a quick note. Ready to send?"

**Proactive intelligence:**
* "You have that presentation tomorrow. Last time you had one, you spiraled the night before. Want to do a grounding exercise?"
* "Sarah's birthday is Friday. Based on what you've shared about her, here's a message draft. Want to send it Thursday evening?"

### **The Draft-and-Approve Model**

Everything the agent proposes is a **draft**:

* You review, edit, and approve before anything happens
* Nothing is sent or acted on without your explicit consent
* You can reject, modify, or ask for alternatives
* The system learns from your edits to get better at sounding like you

### **Why This Isn't Inauthentic**

Using your agent-self to help you act doesn't diminish your humanity—it amplifies it:

* **The intention is yours.** The agent saves time; the care is real.
* **The voice is yours.** Drafts are built from *your* patterns, *your* values, *your* way of expressing yourself.
* **The approval is yours.** You decide what goes out and what doesn't.
* **Time saved is reinvested.** Less friction means more presence with the people who matter.

This isn't outsourcing your humanity. It's a **superpower**. In a world where attention is scarce and life moves fast, your agent-self helps you be *more* human, not less.

The deep profile you build becomes the foundation for AI that genuinely represents you—not as a replacement, but as an extension of your capacity to connect.

---

## **6. How It Works**

### **Capture**

Everything goes into your secure "life stream" that the system analyzes over time.

**What you can capture:**
* Daily reflections
* Quick thoughts
* Emotional spikes
* Wins or breakthroughs
* Fears and anxieties
* Conflicts or patterns
* Moments of clarity
* Goals, desires, insights

**Quick capture options:**
* **One-liner drop** — Just type a sentence: "Felt dismissed in the meeting." Done.
* **Emotion picker** — Tap an emotion wheel when words won't come.
* **Voice memo** — Speak freely. The system transcribes and extracts structure later.
* **Photo + caption** — Capture a moment visually with a short note.
* **Rating pulse** — "How are you right now?" One tap, 2 seconds. (Realized as the `/mood` observation on its 1–5 scale — see [docs/observations.md](docs/observations.md).)
* **Body micro-logs** — `/pain 6 shoulder`, `/ate eggs and toast`, `/bm 4`, `/mood 2 wired`. One line each, clinical-standard scales, building a medical-grade personal record over years — pain, injuries, meals, sleep, symptoms — plus the world's half of the day (weather, daylight, where you were) filled in automatically from sources you approve. Inventory, never obligation: nothing here is ever scored or streaked. See [docs/observations.md](docs/observations.md).

The philosophy: **capture first, structure later.** Never let the interface get in the way of the moment.

**Streaks without punishment:**
* Streaks exist only where you granted them teeth: the Engine tracks the small set of practices you formally committed to ([docs/engine.md](docs/engine.md)) — and even there a return after a miss is one floor-level night, never makeup work
* Everything you *say* is never scored: capture volume, journaling depth, observation logging, silence about content carry no streaks, no quotas, no "you were quiet" pushes — if you've been away, the welcome happens when *you* open the door, at your next check-in, not via a notification
* Teeth on acts, amnesty on words — the one clean boundary the whole system is built on

**Historical entries:**
Not everything important happened today. Lucid lets you add past events—traumas, key life moments, relationship history, formative experiences—and places them in the correct temporal context. When you add something from the past, it can recontextualize patterns the system has already noticed.

### **Understand**

**Daily check-ins:**
Each day, the system guides you through what happened, how you felt, what mattered, and which pillars were touched.

**Pattern discovery:**
The system uses three methods:

1. **Explicit input** — You directly tell the system about past experiences, known triggers, or things you're working on.
2. **Pattern inference** — As entries accumulate, the system notices recurring themes: "You've mentioned feeling 'not enough' in 4 different contexts this month."
3. **Validated insights** — When the system notices a pattern, it asks: "Does this resonate?" You can accept, reject, or nuance any interpretation.

**Multi-timescale reflection:**

| Scale | Question |
|-------|----------|
| **Daily** | "What happened today, underneath the surface?" |
| **Weekly** | "What patterns repeated? Where did you grow? Where did you avoid yourself?" |
| **Monthly** | "What pillars are rising or falling? Which themes dominated?" |
| **Yearly** | "What transformations have begun? What long-term story is unfolding?" |

It builds a **living narrative** of your life.

**User agency:**
* Every inferred pattern can be edited, rejected, or reframed
* The system never claims certainty—it offers reflections
* You can mark topics as "off-limits" or "sensitive"
* You can export, review, or wipe your profile at any time

### **Connect**

People aren't database rows. They're constellations of memories, dynamics, feelings, and shared history. Lucid learns about the people in your life *through your reflections*, not through forms.

**How people emerge:**
People appear organically in your entries. When the system detects someone new, it offers to learn more:
* "You mentioned 'Sarah' for the first time. Want to tell me about her?"

**What the system learns about each person:**
* Who they are to you
* The emotional texture of the relationship
* Patterns that repeat
* Shared history and key moments
* Important dates (learned from entries, not forms)
* What you're working on in the relationship

**Relational insights:**
* "You've mentioned feeling dismissed by [person] in 4 different entries. Is this a pattern worth exploring?"
* "Your entries about [person] shifted noticeably after that conversation in March."
* "You tend to avoid conflict with [person]. Three times you've written about unspoken frustrations."

The relational map isn't just a list—it's a web of connections that helps you see the patterns in how you relate.

**Looking ahead: Shared Profiles**
When someone you care about also uses Lucid, imagine choosing to share parts of your profile with them—and receiving theirs. Both systems suddenly understand the relationship from both sides. More on this in [Future Possibilities](#shared-profiles-relational-bridges).

### **Grow**

**Coaching layer:**
The system tracks your external goals and habits and suggests:
* Micro-goals for weak pillars
* New habits based on your behavior
* Experiments for self-growth
* Ways to break recurring cycles

**Goal structure:**
Goals can be nested—a big goal like "Improve my relationship with my body" can have sub-goals like "Exercise consistently" which have tasks like "Go to the gym Tuesday."

**Where goals come from:**
* **User-stated** — You explicitly tell the system what you're working toward
* **System-suggested** — "You've mentioned wanting more creative time in 7 entries. Want to make this a goal?"
* **Emergent** — The system notices you're already doing the work and asks if you want to make it explicit

**When a goal is completed:**
* Reflection prompt: "What did achieving this teach you?"
* Connection: "This relates to your larger goal of 'advocating for myself.' You've now completed 3 goals in that theme this year."
* Archive: The goal is stored with the story of how you got there

### **Interpret**

Every situation can be seen through multiple lenses.

**Example: You feel resentful after your partner dismissed your idea at dinner.**

| Framework | Interpretation |
|-----------|----------------|
| **NVC** | "You're feeling hurt because your need for respect wasn't met. What request could you make?" |
| **IFS** | "A young part of you feels unseen. Can you turn toward it with curiosity?" |
| **Stoicism** | "Their reaction is outside your control. What *is* in your control here?" |
| **Gottman** | "This might be a missed bid for connection. How could you try a repair attempt?" |
| **Watts** | "Who is the 'you' that feels dismissed? What if this moment is just weather passing through?" |

You can receive all of these perspectives, or choose which ones feel useful for this moment.

Lucid **adapts to your worldview**, not the other way around.

---

## **7. Your Data, Your Control**

Your innermost thoughts deserve the highest protection. Here's how Lucid handles your data:

**Local-first storage:** All your data lives on your device—your reflections, your psychological profile, your relational map. You own it completely. Export it, delete it, or migrate it anytime.

**AI processing:** By default, Lucid uses a third-party AI (like Claude) via API to power its intelligence. Only the relevant portions of your data are sent for processing—never your entire history, never stored in the cloud, never used for training. If you prefer, you can run a local AI model so nothing ever leaves your device.

**No hidden agendas:** No third-party analytics. No data monetization. Full transparency about what goes where.

This is your mind, your life, your data—you control it completely.

---

## **8. What We're Building**

### **MVP: The unified nightly loop**
* Runs on an existing local chat harness (see [specs/mvp-scope.md](specs/mvp-scope.md)) — a standalone desktop app is the follow-on, not the precondition
* The Engine's two-minute nightly close-out doubles as capture: one act writes the day's record and the journal entry
* Core agent system for the Mirror half (intake, structuring, reflection); the Engine half is deliberately agent-free
* Markdown/JSON data storage you own completely

### **Future Possibilities**
Once the core is solid, Lucid could expand to:
* **Slack/Discord bot** — DM yourself thoughts throughout the day
* **CLI tool** — Quick terminal commands for capture
* **Voice memo processing** — Speak freely, get structured entries
* **Mobile companion** — Quick capture on the go
* **Calendar integration** — Anticipate stress points, know what's coming
* **Email/text awareness** — Understand commitments and relationship dynamics
* **Web dashboard** — Visualizations of your journey over time

The architecture is designed so **input can come from anywhere**, with secure local storage at the center.

### **Shared Profiles (Relational Bridges)**
When someone you care about also uses Lucid, you can choose to share a curated version of your personal profile with them—and receive theirs in return.

**What gets shared (you choose):**
* Aspects of your psychological profile you want them to understand
* Your communication preferences and what helps you feel heard
* Patterns you're working on and how they can support you
* Your attachment style and what you need in moments of stress

**What becomes possible:**
* **Mutual understanding** — Both Lucid systems now understand the shared dynamic, not just one side
* **Sculpted interactions** — Messages drafted with awareness of both parties' needs and triggers
* **Pattern bridging** — "You both tend to withdraw when hurt. Here's how to reach toward each other instead."
* **Relationship insights** — Surfacing complementary strengths and potential friction points

This is vulnerability as a superpower—choosing to be known so you can be loved more fully.

**Use cases (in order of priority):**
1. **Close relationships** — Friends and family who want deeper mutual understanding
2. **Couples** — Partners using Lucid together, potentially with therapist integration
3. **Professional relationships** — Collaborators who want to work together more effectively

---

## **9. The Vision Realized**

Imagine waking up and knowing exactly what's been weighing on you—not vaguely, but precisely. The system noticed you've mentioned feeling "overlooked" three times this week and connects it to a wound you identified months ago.

Imagine your friend's birthday approaching and having a message already drafted—in your voice, mentioning your shared history, ready to send with one tap. Not because a robot wrote it, but because your agent-self knows how you express care.

Imagine looking back at the end of the year and seeing not just what you did, but who you became. The fears you faced. The patterns you broke. The relationships you deepened. The goals you achieved and what they taught you.

Imagine having a companion that truly knows you—not a therapist you see once a week, not a journal you forget to write in, but a living system that holds your story across time and helps you become who you want to be.

**This is what's possible when you finally have a system for your inner life.**

This is Lucid.

---

*For implementation details and technical architecture, see the [Technical Specification](technical-spec.md).*
