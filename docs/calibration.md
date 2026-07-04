# Lucid — Calibration

**What this is.** Lucid ships generalized: the specs
([`architecture.md`](architecture.md), [`engine.md`](engine.md),
[`observations.md`](observations.md)) describe mechanics that work for
anyone and contain no facts about any person. Calibration is the step
that aims those mechanics at *your* measured life — your failure
topology, your energy map, your body, your stakes. This document is
the guided form; in the packaged app it becomes `lucid init`, an
interactive first-run wizard that asks these same questions and writes
the same file.

**How to use it today.** Copy it to `personal/calibration.md` in your
own Lucid home or fork, fill it in, and keep it out of any shared
history (`personal/` is gitignored in this repo; verify that before
your first commit). Nothing in the system's mechanics assumes any
particular life — this file is the only place calibration happens.

Everything in [engine §8](engine.md) has a default. Override anything here, with a one-line reason (architecture P8). Sections marked **(required for Day 0)** gate the program start; the rest can accrete.

---

## Sensitivity header **(required for Day 0)**

> This file contains health and relationship context. It must never enter a shared or published history. If collaborators, mirrors, or repo-scoped agents are ever added, exclude `personal/` and audit history before any visibility change.

## Calibration — measure before you build **(required for Day 0)**

The engine is aimed at *your* measured profile, not the average participant in a study. Answer from evidence (your last five attempts at anything), not aspiration.

**Failure topology, ranked.** Order these by how your past attempts actually died: (a) never actually started, (b) crowded out by work/urgent, (c) forgotten entirely, (d) strong start that faded. Your #1 determines which mechanisms are primary: ignition failure → bells, floors, staged environment; crowding → slot defense and the Crux; forgetting → ambient state (L0); fading → error budgets and the tripwire. Design for your #1; the rest come nearly free.

**Success predicates.** Look at anything you've sustained for years without trying. What made it survive? Common predicates: immediately fun rather than eventually rewarding; tied to identity; outcomes arriving obliquely as byproducts. Whatever your survivors share is your engine — aim it rather than fighting it. List your Anchors (things that already run at zero cost) here; they are load-bearing and must stay unmanaged.

**Energy and time map.** Your peak window (and whether it's currently sold to something); your discretionary window and its quality. Placement rules follow: the Crux slot goes in the peak; nothing requiring a launch sequence is ever scheduled in a trough.

**Load.** Chronic conditions, pain, sleep debt, caregiving, anything that varies your available capacity. Resolution of detail is yours — opaque one-word limiter tags are sufficient for correlation (engine §2). Register any **clinical gates** here: professional assessment that must precede specific kinds of Links (e.g., loading work before an injury is assessed). Care providers stay upstream of training decisions; the capacity/mode series is exportable for appointments.

**List relationship.** What does your backlog of someday-projects do to you when you look at it? Portfolio statuses exist to make "not chosen now" stop compiling as "failed again."

## Active configuration **(required for Day 0)**

**The first Chain.** One chain only until Gate 1. Design guidance: anchor it to a Bell at a natural transition (bedtime is the proven pattern — the chain doubles as a shutdown sequence); 3–5 Links, each with an explicit Floor; the journal Link *is* the Mirror's nightly capture (floor: one spoken or typed line); put the phone's overnight home outside the bedroom if attention is one of your problems. Write it as: Bell time, label, Links in order with floors.

**Chain start date:** record on the first completed night — all gate dates compute from this field.

**Config items** (live immediately, no slot): staged objects, removed apps, fixed anchors.

**Opportunity habits** (slotless, tallied, never quota'd): trigger → act. One or two at most.

**Schedule profiles** (only if your schedule rotates — engine §2): one named clock set per recurring schedule, each moving Bell time, tripwire hour, and rollover together (e.g., `default` and `nights`). Define them here and at Retros only; switch with `/profile` as the roster changes, effective the next logical day. A shift worker without profiles is lending the system a schedule they don't have.

## Portfolio snapshot

Sort your full lists — every project, hobby, aspiration, obligation — into: **Anchor** (self-sustaining, untouched), **Active** (the one Chain, for now), **Bench** (available for spontaneous play, untracked forever, guilt formally revoked), **Parked** (dormant, with a standing quarterly Hearing), plus domains marked **Steady** (stable, monitored, unmanaged). The sort itself is timeboxed: one sitting, imperfect, amendable at any Retro.

## The Charter **(required for Day 0 — at least sections 6 and 8)**

Write plainly, in your own voice, one sitting, imperfect. Version it. Date it. Keep it as `personal/charter.md` — same privacy rule as this file; amendments append with reasons at quarterly reviews only (architecture P8).

1. **Identity:** "I am becoming the kind of person who…" — five to ten lines.
2. **One year:** a specific evening, one year from today — where you are, what your body can do, what's within reach, who's around.
3. **Five years:** the same scene, five years out.
4. **The lenses, yours:** one paragraph on each framework in your stack — what it actually means to *you*, not to its author. Each paragraph is that lens's standing consent ([`frameworks.md`](frameworks.md) §3): it may frame reflections in its vocabulary once the layer ships. Your paragraph outranks the definition file wherever they differ; keep the stack small (≤ 3); leaving is never gated.
5. **Non-negotiables:** the three to five things that stay true even in the worst week.
6. **The stake:** what a breach costs — concrete, mildly painful, mechanically executable (engine §4) — written while strong. On a breach you execute it yourself within the stake execution window (default 72 hours, engine §8) and confirm to the witness, who verifies with one line. Choose a beneficiary who is not your witness — the person verifying a payment must not be its recipient.
7. **Forever unmeasured:** outcomes the system may serve with rooms and readiness but must never metricize (architecture P4). Naming them here is what protects them.
8. **Storm clauses & ambush windows** *(required for Day 0 — may be empty; must be chosen)*: the conditions under which the machine must treat non-performance as circumstance, written while strong, in your own words — the bereavement, the hospitalization, the injury that makes the chain unsafe — plus the calendar dates you already know are ambushes (the anniversary, the season that takes you under). The witness confirms storm declarations against exactly this text (engine §4); a clause you never wrote is a fine you'll eventually pay. Give each clause a short label — the label is all the Engine ever stores.

Show the witness sections 5, 6 **and 8** — they hold you to the non-negotiables, they hold the stake, and they confirm your storms against what you wrote while strong.

## Witness arrangement **(required for Day 0)**

Name, channel they actually see, briefing date, confirmation.

**Choosing one.** Someone who will actually ask once, without drama — the job is a question, not a lecture. Not the stake's beneficiary (Charter §6): the person verifying a payment must not be its recipient. Mutual witnessing is permitted, with one caution: keep the two contracts formally separate, each with its own brief and its own stake.

**The brief** is one paragraph: what they'll see (streak, mode, escalation state, storm state — never content), when they'll hear from the system (the L2 dead-man message after two consecutive misses, plus the monthly heartbeat — engine §4), and their four jobs — when L2 fires, ask about it, once; when a storm is declared, confirm or decline it with one line against the Charter clauses they've seen (§8 above); when a stake executes, confirm with one line that it happened; hold the written stake. One more line, consented now so it arrives expected rather than surveillant: if an L2 goes unanswered and total silence persists for 7 days, they are licensed — as a human, outside the system, in their own words, without Lucid framing — to reach out once more. Record that they've seen the stake.

**Keeping it live.** Re-brief quarterly, folded into the stake review: one message reconfirming the contract, recorded. A witness may resign at any time; on resignation or sustained unreachability, L2 disarms and the ladder runs L1-only until a replacement completes this full arrangement from the top — briefing, stake shown, confirmation, channel scoping (engine §4).

## The comprehension pass **(required for Day 0)**

Configuration is not comprehension: the simulation's users white-knuckled weeks they could have declared, because the relief valves were configured but never learned. Before Day 0 closes, do two things.

**Read the exact words.** Open [`mvp/engine-module.md`](mvp/engine-module.md) §"Consent amendment" and read, verbatim, the four texts that can arrive without warning: the L1, the L2, their storm variants, and the heartbeat. Consent granted in advance (architecture P5) means knowing precisely what you consented to — and the morning one of these lands, you will recognize a form letter you already read while strong, not a stranger's judgment. The escalations say so themselves: the L1, the L2, and both storm variants each end "— the form letter, pre-committed at Day 0." (The heartbeat carries no sign-off; it reports status and doesn't sting.)

**Walk the five relief valves, once, out loud or on paper:**

1. **Floors** — the minimum that still counts, for every Link. The worst day costs one sentence.
2. **Modes** — declare Yellow or Red *before the Bell* on the days that are already smaller than Green. A declared floor day scores 100%.
3. **The budget** — isolated misses are spend, not failure (default 4 per 30 days). A miss followed by a completed night is the system working.
4. **Backfill** — the chain ran but went unrecorded? `/closeout backfill` within 7 days; derived state recomputes; nothing is owed.
5. **The storm** — for the collapse that outlasts a day: declare it, your witness confirms it, the stake stays while contact continues (engine §4). Your clauses are Charter §8; you wrote them above.

Then put the **baseline-week Yellow rehearsal** on the calendar (engine §2): one deliberate Yellow declaration in week 1, on a day of your choosing — a drill, so the first real hard day finds the lever already warm.

## The household brief *(optional — recommended if you share a home)*

The witness gets a contract; the person you live with gets a bell going off at 21:30 and no explanation. The simulation's ugliest social moment was a partner discovering the system by ambush. One paragraph, in your own words, covering:

* **What they'll notice:** a bell at a fixed hour, two quiet minutes at night, occasionally a morning message on my own channel.
* **What it is:** a practice ledger — it defends a small routine and keeps an honest record of whether I did it.
* **What it is not:** it doesn't record you, listen to the room, or assign me homework about you.
* **Privacy runs both ways:** what I write is private, including from the witness. And if you'd rather not appear in it at all, there is a switch for exactly that — say the word and the system goes blind to you while my own record stays intact (the off-limits registry; architecture §5).
* **You can ask me anything.** The system's contracts bound the system, not you.

This is a conversation template, not a send — nothing here creates a message path, and the brief is yours to give, in person, before the first bell rings in a shared room.

## Off-limits registry **(required for Day 0 — may be empty, must be chosen)**

Topics excluded from inference or marked sensitive (architecture §5). A topic may be a person: name them here and the Mirror stops inferring about them entirely — no proposals, no dominance lines, redacted from every agent slice — while the record beneath still remembers (`/person` renders their raw mentions only).

## Observations configuration

Which observation kinds are on ([engine of record: `observations.md`](observations.md) §3 — pain, symptom, intake, elimination, mood, sleep, med, measurement), with a one-line reason each — enable what you'll actually use; kinds are free to add later. **Injury inventory:** every current and historical injury or chronic condition as a registry seed (name, onset — approximate is fine, status), at whatever resolution you choose. **Threads:** the handful of inner and outer things you're working on, each as an intent statement — no metrics, no deadlines; their progress is narrative, reviewed quarterly. **Enrichers:** which external sources are on (weather, daylight, calendar-frame), your sticky starting location (a city is enough), and the standing reminder that each source's `sends` field is a contract. **Curiosity budget:** how many micro-questions per day you'll tolerate (default 1; 0 is valid).

## Experiments

Optional. Whether the Scientist ([`scientist.md`](scientist.md)) is enabled — off by default, and never before Gate 1 regardless. **Forbidden domains:** name the load-bearing supports no experiment may ever touch — recovery infrastructure, grief practices, anything that is foundation rather than variable. This list is what the Scientist's "must not run" check ([`scientist.md`](scientist.md) §5) reads against, so an empty list is a choice, not an oversight. **Ambush windows:** named in the Charter (§8 above) — an open experiment freezes across them, and frozen blocks are censored, never counted.

## Scheduled

Ledger location · first weekly Retro (calendar slot) · first quarterly Hearing (date) · known travel with its Away Mode configuration (which Links compress, what the Floor becomes, which capture surface substitutes) · known ambush windows with their dates (Charter §8 — these enter the storm state automatically, engine §4) · Gate 1/2/3 candidate Links (each wrapped in a Anchor identity where possible; each fitting the footprint cap).

## Intake corpus

The first structured entries in your Ledger, answered as voice or text captures: (1) yesterday, hour by hour; (2) the three things you're proudest of; (3) your health file, at whatever resolution you choose; (4) what brought you here — the trigger; (5) the felt sense of the one-year scene. Start with 1; it's the easiest and it calibrates the rest.

## Amendment log

Append-only. One line per change: id, date, what, why. Helper access — anyone who installs, configures, repairs, or hand-edits the host or `~/.lucid/` on your behalf ([`local-runtime.md`](mvp/local-runtime.md)) — is recorded here too: who, when, scope.
