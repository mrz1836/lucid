<!--
Status: research artifact, not spec. Nothing here is binding; every
recommendation lands, if at all, as a doc diff to the canonical files
per docs/mvp/claude-code-workflow.md.

Method: multi-agent simulation study, 2026-07-03. Five readers digested
the full doc set into a behavioral spec; six synthetic personas
(rotating-shift nurse, founder, widow, ADHD grad student, manager in a
strained marriage, contractor in recovery) each lived 90 simulated days
on the documented Phase 0-2 system with mandated life shocks; three
friendship dyads simulated the witness/social layer; an empiricist and
a methodologist analyzed across lives; 111 raw findings were triaged to
22 and each adversarially verified against the actual docs (8 confirmed
gaps, 13 partially addressed, 1 invalid — the invalid one is retained
in the findings table with its verdict). All personas are synthetic; no personal data was read or used.
-->

# Ninety Days of Lucid: A Six-Life Simulation Study

## 1. Executive summary

**The verdict on the owner's success criterion.** The criterion was: transform the human in many measurable ways, give deeper context for the human's benefit, and make sure the system works, learns, adapts, grows — and is always integrated and never abandoned. On this evidence, the criterion is *partially achieved, by an unexpected mechanism*. Lucid did not directly improve health metrics — sleep, mood, capacity, and pain moved with life (a cortisone needle, a grief group, a closed funding round), not with the software. What Lucid provably did in six hard-case lives (all twelve-system quitters by design) was three things: (1) it produced unprecedented **accountability density** — 77–90 of 90 days honestly accounted per user, including honest misses, a record none of the seventy-two dead prior systems ever produced; (2) it collapsed the **shame latency** that killed those systems — Sam's miss-to-restart interval went from "permanent uninstall" to "next night"; and (3) it made its users **legible to their existing human infrastructure** — a doctor, a sponsor, a daughter, a spouse, and finally themselves. Maya's clinician packet plausibly accelerated an MRI and diagnosis by one to two visit cycles; Ruth's packet turned "I'm managing" into a PHQ-9 and a grief group; the Mirror's single best insight per user produced more life change than the entire Engine. That is a real transformation engine — but it is *legibility, not optimization*, and the docs should own that.

Integration is the fragile half. All six personas are probably retained at day 180 (stated confidence 75–95%), all six are genuinely uncertain at day 365 (~50–60%), and the uncertainty is the same everywhere: either one more spec-correct punishment of circumstance, or quiet hollowing into a two-minute ritual the system cannot distinguish from success.

**Top five insights.**
1. **The floor is the product.** The one-line/one-circle/one-goodnight floor, plus the restart ritual's "nothing added, nothing owed," is what all six survivors independently named as the difference from twelve dead systems. Every survival at a kill-moment traces to those two mechanisms.
2. **The teeth bite inverted.** Across six lives the stake executed exactly twice — both against circumstance (grief collapse, back injury), never against avoidance — and was socially waived every time it was owed for avoidance-shaped behavior. The escalation apparatus punishes the collapse it exists to catch and is ceremony against its actual target.
3. **The value concentrates at the boundary with humans.** The clinician packet, the sponsor's disambiguating call, the witness's off-script dinner — Lucid's measurable wins were cases of giving a human relationship better information at the right moment.
4. **The Mirror runs at idle for exactly the users who need it most.** Depleted users write one-line entries; thin corpora yield banal or wrong proposals; proposal fatigue suppresses the capture that would fix it. Roughly one load-bearing insight per user per quarter — enough to justify the subsystem, not enough to sustain it.
5. **Every life required an undocumented human role.** Setup, config changes, corrections, and exports all ran through a technical helper (Elena for Maya, Marcus for Ruth) who holds root and voids Ring 0 — a load-bearing role no doc names.

**Top five risks.**
1. A fourth spec-correct escalation into circumstance (Maya's roster, Ruth's day-300 death anniversary, Marcus's next injury) ends a subscription the design cannot afford to lose (finding 1).
2. Hollowing: mode-relative adherence is already Goodharted (eleven gamed Yellows at 91%+; a 97% gate over a floors-only month), and the floor-day canary has no threshold or voice (finding 9).
3. The ladder ends at L2; the next long silence has no specced reach, and the last one was ended only by a witness breaking his brief (finding 6).
4. Witness-channel semantics — contentless alarms forcing worst-case inference, quiet floors hiding real crisis — spend relationship capital on every activation (findings 8, 11, 14).
5. The asymmetric household: the most-documented person in Elena's Ledger had no existence, consent, or reply channel, and the costs landed in month one (finding 22).

## 2. The six lives

**Maya** (rotating-shift ER nurse, single mother). Ran a tiny bedtime chain against a roster the clock-fixed mechanics could not see: mid-shift bells, a 04:25 close-out misfiled as a miss, an L1 waking her mid-day-sleep, two L2s fired over nights she had substantially performed. Kept by floors, "nothing owed," and the Day-75 clinician packet (41 ibuprofen doses in 70 days → same-visit cortisone, MRI, named supraspinatus tear). 90/90 days accounted, ~92% mode-relative, two breaches, zero stakes paid. Day 180: probably yes, at floor equilibrium. Day 365: uncertain — decided by whether the system "learns what a night shift is" before a fourth technicality-L2 spends the last of her patience.

**Devon** (founder, mid-bridge-round). Nine-hour cathedral Day 0; two of three Charter commitments spent the quarter structurally outside the one-chain Engine. Discovered the Yellow loophole (eleven consecutive gamed days at 91%+), breached on an unplanned trip, paid the only executed-against-will stake of the study ($250, clarifying fury). One Mirror insight — kids appear in plans, never in things that happened — produced the study's largest measured life change (a standing Saturday outing). Day 180: yes (~80%) — the chain is household infrastructure. Day 365: ~50%, and the threat is hollowing, not abandonment.

**Ruth** (67, widowed, retired teacher). Consented to Lucid rather than chose it; Marcus wired her box. Red mode carried the 45th anniversary with dignity; the same machinery then executed her $100 stake into a grief collapse ("fined for grieving"). The rejected depression hypothesis plus a hand-exported packet walked her into a grief group anyway; the excavation practice re-remembered her husband's last hospital week and is the reason she'll stay. 85/90 days. Day 180: ~75%. Day 365: ~55% — the calendar promises ambushes the machinery currently cannot see.

**Sam** (PhD student, ADHD, 3am chronotype, thirteen-time quitter). The 04:00 rollover fit his life until 04:12 filed a completed night as a false miss — his angriest moment. Conference all-nighters produced a breach, an L2, a paid stake, and nine days of silence the system met with a swiped bell; return came only via Devon's off-script dinner. Red-mode floors carried a breakup ("she left. logged."). Meds adherence roughly doubled — mostly the pill bottle staged on the keyboard. 77/90 days, longest streak 17. Day 180: ~75%. Day 365: hinges entirely on the next silence and whether anything specced reaches into it.

**Elena** (ops director; marriage under strain). Her Ledger became a complaint archive about Tomas (peaking ~74% of entries) with no dominance threshold to warn her; his Day-17 discovery-by-ambush nearly ended everything, and the resulting L2 leaked exactly what he'd accused the system of leaking. Two uncanny insights — transactional evenings; "the Ledger is where the talking is happening" — produced the three-day rule and Sunday walks, dropping the Tomas-share to 47%. The stake was known-dead from Day 19. Day 180: yes, with high confidence — but only the Engine. Day 365: uncertain; what survives may be Lucid-as-alarm-clock beside an unread Mirror.

**Marcus** (contractor, three years sober). The Engine mapped cleanly onto AA virtues; amnesty on craving logs held absolutely (eight raw urge one-liners, zero reaction, exactly right and worrying at once). The Day-29 insight — cravings cluster within hours of Darnell contact — outworked everything else the Mirror produced. The system executed his $200 stake against a wrenched back and a muscle relaxant; Big Ed's "you don't renegotiate with your disease either" kept him in. The packet documented a refused Percocet; his sobriety traveled by voice because no field carries it. Day 180: near-certain for the Engine. Day 365: hinges on whether it fines him again for being human.

## 3. What the design already gets right

The simulations were adversarial by construction, and much of the doc set survived contact.

**The floor + restart pair is the retention thesis, vindicated.** At every one of ~20 near-abandonment events, the two things that fired and helped were the floor (small enough to clear at capacity 2) and the restart ritual's no-penance re-entry (engine.md §4). Sam's Day-46 return — one line, "Streak 1.", no audit — is the single cleanest product moment in six lives.

**Mode-relative scoring is the most humane rule in the system.** Maya's 93% Day-30 gate over floor-heavy night weeks, Ruth's anniversary Red day counting as complete, Sam's grief week alive on one-sentence floors — floors-count-as-completion (engine.md §1) repeatedly converted survival into success rather than shame.

**Payload minimalism is real, test-enforced, and load-bearing.** The verifier confirmed the L2's poverty of content is a binding Phase-10 grep, not an accident (finding 8, partially-addressed): Elena de-escalated the Tomas fight precisely by showing him how little the payload carried. Ring 0 as presence-only is designed, briefed, and correct in intent (vision.md §7).

**The Engine/Mirror decoupling works as designed.** Sam avoided /checkin for ten days after a mistimed proposal while his Engine floor kept running untouched — exactly the two-subsystem independence architecture.md promises.

**Sanctuary held, and it is why honesty happened.** Observations-as-inventory (P3; observations.md §0) is what made an addict willing to log cravings and a grieving man willing to log /mood 1 for a week. Ruth's forever-unmeasured grief was never scored, targeted, or trended — the docs honored it completely, and she noticed.

**The adversarial verification shows the docs ahead of naive critique in several places.** Finding 5 ("clinical patterns silenced forever") was ruled **invalid**: rejection memory is window-scoped by design (agent-contracts.md §3, acceptance-criteria Phase 5), Safety blocks consume nothing (error-states Sf-3), and the designed clinical bridge — the clinician packet — is exactly the path the simulation observed working. Finding 14's headline was contradicted: an unconfirmed witness blocks program start and produces explicit user notification, not silent disarmament (engine.md §5; engine-module Phase 10). Finding 12's "missed heartbeat means nothing" was wrong — the month-of-silence backstop is precisely defined over specced sends. Finding 6's proposed fifth send class is not a gap but an explicitly decided refusal (product-principles.md §7; vision.md line 229). Finding 18 undercounts existing homes for non-chain commitments (portfolio Hearings, Config staging — Sam's pill bottle *is* the documented mechanism, engine.md §1). The doc set's deliberateness is a strength; the remaining gaps below are the ones that survived that scrutiny.

**The clinician packet is the strongest expression of the whole value proposition** — three exports, three changed clinical conversations — even though it currently has no command, which is Section 5's problem.

## 4. Insights gained

Ninety simulated days taught things the docs alone could not.

**1. Legibility, not optimization, is the product.** Health outcomes belonged to life. What Lucid changed was who could *see*: Maya's "I'm not just saying it hurts, I have 68 data points"; Ruth's "the graph said what I wouldn't"; Elena seeing her own complaint archive. Every durable win is a legibility win, and the design should measure and market itself accordingly (Section 8).

**2. The teeth are theater against their target and live ammunition against the innocent.** Zero stake executions for avoidance; two for circumstance; three affectionate waivers with no record. The pre-commitment wager is sound; the execution layer is unbuilt (findings 1, 2).

**3. One insight per user per quarter carries the Mirror — and the economics are a starvation loop.** 49 proposals, ~60% accepted, ~1.4 acted-on per user-quarter, exactly one uncanny hit per life. The tax (banal proposals, verbatim "Does this resonate?" fatigue — parodied at the kettle by three of six users) suppresses the capture that would raise the hit rate, and silence records nothing, so the system cannot learn it is being ignored (findings 10, 13, 15).

**4. The social layer already exists; the docs just don't govern it.** Fellow-userhood was the de facto comprehension mechanism (Marcus learning modes from Ruth over the fence; the form-letter rescue), the de facto retention mechanism (Devon's dinner), and the de facto admin layer (Elena, Marcus as root). The specced witness channel carried, in one dyad, exactly one L2 and one heartbeat in 90 days while the real relationship ran on screenshots and meals. Meanwhile Ring-0 dashboards *displaced* real contact — Maya's green view of Elena's worst fortnight quietly licensed not calling ("Your app told me your streak every month. It never once told me you were sad.").

**5. The dyad thought-experiments cut against the vision's lyricism.** Sharing artifacts are safest flowing toward professionals and most dangerous between intimates who already owe each other conversation; a beautiful quarterly counsel brief is Elena's Day-52 pathology industrialized. High rings hand witnesses exactly the content that makes ask-once surveillance; Ring 1 destroys the amnesty ("no audit" is a property of what the other person can see). The pair that needed sharing least — Ruth and Marcus — had built their own Ring-2-lite out of spoken Engine arithmetic with deniability no aperture contract can match. Design implication: invest at the low rings (cadence, disclosure logs, perspective ingest) before the high ones.

**6. Configuration is not comprehension.** Day 0 produced valid configs and zero operational models: Marcus white-knuckled three weeks of undeclared Green out of ignorance; Ruth's survival features arrived entirely by social transmission (finding 20). The relief valves must be taught as prominently as the commitments.

**7. Users route around every boundary they don't understand or can't reach** — Devon metricizing his forever-unmeasured item in Sheets, Marcus laundering pain into journal lines so the pattern engine could see it, Devon's /obs pseudo-tracker. Obliquity governs surfaces, not the existence of evidence; ungoverned science happens wherever governed science is impossible (Section 9).

## 5. Obstacles and issues

The verified confirmed/partial gaps, by theme. Severity and full citations in Section 11.

**Theme A — Circumstance vs. avoidance (the existential cluster).**
- **No incapacity, storm, or crisis state exists** (finding 1, confirmed-gap; engine.md §2/§4, error-states.md, calibration.md §6). Mode locks at the bell, undeclared defaults to Green, Away Mode is travel-only, and L2/L3 fire on absence of records. Ruth fined for grieving; Marcus fined for a back injury; Devon's L1 into a 39.2° fever. Roughly two-thirds of all near-abandonments were this one event in different clothes.
- **L3 has no executor, verifier, waiver record, or re-arm rule** (finding 2, partially-addressed; engine-module tripwire step 5). Five breaches, zero executions against will except Devon's; no Day-0 executor consent; the witness can be the payment's beneficiary; "written while strong" is unvalidated (Maya authored hers at 23:40 post-shift).
- **corrections[] cannot retract a fired escalation, and the rollover edge files completed nights as misses** (finding 3, confirmed-gap; engine-module §Storage additions, §Commands). All six sims flagged backfill-vs-escalation as a [GAP]; four users backfilled and watched nothing change; no user-facing correction command exists; status.json's byte-for-byte-rebuild claim is internally inconsistent with tripwire-set escalation state.
- **Clock-fixed mechanics cannot serve rotating rosters** (finding 7, confirmed-gap; engine-module chain.json; observations.md §3/§7). Four independent scalars, one-change-per-Retro, "stable timing" unmeetable by construction, /slept bindingly diurnal. (Stable extreme chronotypes are largely servable via instance overrides; the rotating-roster prong is the real hole.)

**Theme B — Measurement integrity.**
- **Mode-relative adherence is Goodharted** (finding 9, partially-addressed; engine.md §3/§5). No Yellow cap, no capacity-digit linkage, canary with no number, floor-day ratio absent from /status and gates; the Green-vs-floor scoring tension is unreconciled even post-MVP. No surfaced metric distinguishes integration from a compliance ritual.

**Theme C — Mirror economics.**
- **Resonance-gate fatigue is invisible** (finding 10, partially-addressed; steel-thread Stage 4 vs. observations.md §6). Unanswered proposals write nothing; curiosity has a backoff ladder, Reflection has none; no timing sensitivity after emotionally loaded entries (Sam's Maya proposal cost ten days).
- **Capture skews and starves the Mirror** (finding 13, partially-addressed; agent-contracts §3 vs. observations.md §7). Count-based 7-artifact windows, no missingness model (Maya's "work cures pain" artifact), no dominance/register detection (Elena's archive), audio ingestion a named-but-unbuilt seam.
- **Accepted insights are near-write-only** (finding 15, partially-addressed; agent-contracts §3). /reflect and /ask consume them, but /reflect dies under load with nothing noticing; no acted-on field; soft contradictions evaporate; warning-shaped insights (Sam's shrinking-entries) have no MVP consumer.

**Theme D — The social layer.**
- **Witness signal semantics fail twice over** (finding 8, partially-addressed): alarms without information ("you hurt, or you drinking?"), and quiet floors hiding crisis; the "all quiet" heartbeat wording actively invites the false negative.
- **Ask-once breaks under intimacy** (finding 11, partially-addressed; engine.md §4, calibration.md): theater when the witness already knows (Amara, puke bucket), collapse when affection overwhelms (Naomi, ninety seconds), a cage when honored (Maya's week of unresolved worry); every retention-critical act exceeded the brief.
- **Heartbeat is false-by-design after a loud month** (finding 12, partially-addressed): failed as written in 100% of observed sends; the tripwire can post an L2 and "all quiet" the same morning.
- **Witness lifecycle post-confirmation is unspecified** (finding 14, partially-addressed): no disengagement detection, no revocation/replacement/re-briefing, no capacity check on the receiving human (Sam's L2 landed on Devon at his own worst week), zero selection guidance.
- **The asymmetric household has no story** (finding 22, confirmed-gap; engine.md Day-0 definition of done): elaborate witness briefing, not one word about the cohabitant who lives with the bell and dominates the record.

**Theme E — Operability and onboarding.**
- **The undocumented root-holding helper** (finding 4, confirmed-gap; local-runtime.md, vision.md §7): hand-edited JSON is deliberate design, but the role it structurally induces is ungoverned, and "structural" Ring 0 is meaningless against the box's administrator.
- **Onboarding transfers configuration, not comprehension** (finding 20, partially-addressed; engine.md §5, calibration.md): no walkthrough, rehearsal, or modes mention at Day 0.
- **The clinician packet — highest-value artifact — has no command, first-window rule, or discovery path, and no clinical-disclosure field** (finding 17, confirmed-gap; observations-module §Commands, observations.md §7/§10). Marcus's sobriety traveled by voice at an opioid decision.
- **Governance has no floor and no failure path** (finding 19, confirmed-gap; engine.md §5/§6): the 20-minute Retro — the only simplification mechanism — is the first casualty of load, in a product otherwise obsessive about floors; Crux contact has no record surface.
- **Fixed templates are context-blind, and confession costs what hiding costs** (finding 16, partially-addressed): no mode-aware L1 variant, no skip-acknowledging variant, no annotation mechanism despite two doc references to it. (Skip's identical escalation is explicit design — the fix must be a deliberate amendment.)
- **Non-daily and multi-time-of-day commitments have no accountability home** (finding 18, partially-addressed): Marcus's weekly program call fits nothing; second-chain mechanics post-Gate-1 are never specified.
- **Sustained distress logging receives undesigned silence** (finding 21, confirmed-gap; observations.md §0/§9): seven nights of /mood 1 structurally reach nothing, and Marcus's requested self-directed threshold ("tell *me*, not Big Ed") is forbidden without ever being deliberated. The finding demands an explicit documented decision, not automatic weakening — sanctuary is a hard rule for good reason.

## 6. Pitfalls

Failure modes that end in abandonment or harm. Each: mechanism → early-warning sign → design counter.

**The circumstance fine.** *Mechanism:* dead-man semantics read two absent records as avoidance; injury, grief, or medication produce absent records; the apparatus escalates and executes, spec-correctly, into the collapse. *Early warning:* capacity ≤2 runs, an active injury/med event in the Ledger, a Charter-named ambush date approaching. *Counter:* the Day-0 storm clause + witness-confirmable incapacity state (annotate without rescoring, stay the stake, keep L1/L2 contact) + corrections retroactivity. Pre-committed while strong, so the no-renegotiation wager survives.

**The shame spiral (the graveyard-builder).** *Mechanism:* miss → shame → avoidance → the system goes mute after L2 → the bell trains notification blindness → uninstall. *Early warning:* Sam's own validated insight — shrinking entries precede disappearing; also an unanswered L2 ask. *Counter:* the floor + restart ritual already break the spiral for anyone who returns; the open decision is whether one pre-committed, self-authored, Day-0-consented return template on day N of silence is admissible — the docs currently answer no explicitly (product-principles §7), and that refusal should be re-argued in writing against Sam's Days 41–45. Cheaper honesty helps too: a distinct L1 variant for honest /closeout skip. And define what /status emphasizes post-lapse — Sam got amnesty in the copy and "the receipt for the thing it said it forgave" in the numbers.

**The complaint archive.** *Mechanism:* a 21:30 close-out samples end-of-day depletion; one person dominates the corpus in grievance register; the Ledger becomes where the talking happens instead of with the person; discovery-by-ambush detonates it. *Early warning:* one person_key above ~50–60% of entries in a complaint register — computable deterministically today. *Counter:* a dominance threshold surfaced at gate cadence; Elena's per-person recall surface; Day-0 household-disclosure guidance ("what to tell a partner") mirroring the witness briefing.

**The cathedral clause inversion.** *Mechanism:* setup is the most engaged the user will ever be (Devon's nine hours); engagement then decays toward the two-minute floor while headline metrics improve; the system cannot distinguish mummified from integrated. *Early warning:* floor-day ratio high and rising, Retros skipped, Crux dark, /reflect dead. *Counter:* give the canary a number and a mandated, floored (5-minute) Retro question — "the floors have been the whole practice for N weeks: keep, shrink, or resize?"

**Resonance fatigue.** *Mechanism:* constant proposal quality-tax at exactly the hour of least capacity; silence records nothing; user abandons /checkin, starving the corpus that would improve proposals. *Early warning:* unanswered-proposal runs (currently unrecordable — that's the bug). *Counter:* port the curiosity backoff ladder (7-day suppression, retire after 3 ignores) to Reflection proposals; a swipe-away event; a timing-sensitivity rule after emotionally loaded entries.

**Witness fatigue and the smoke-alarm decay.** *Mechanism:* every activation is contentless and (so far) unjust; the witness's model degrades from concern to "the app being wrong about Maya sometimes"; the channel is half-ignored exactly when a true positive finally comes. *Early warning:* waived stakes, "??" replies to heartbeats, ask-once breaches in either direction. *Counter:* a user-precommitted one-line context (written while strong, like the stake) in the L2; a non-quiet heartbeat variant; witness re-briefing at gate cadence; selection guidance (distance, capacity, conflicts — no recruiter-creditor-judge stacking).

**The sanctuary's passive-watching dilemma (the addict case).** *Mechanism:* the amnesty that makes craving-logging possible also forbids any response; the intersection of individually correct refusals is a silence indistinguishable from neglect for a user without Marcus's AA scaffolding. *Early warning:* self-initiated distress logging rising in frequency (visible in the data, invisible to every surface). *Counter:* decide it explicitly. Either a narrowly scoped carve-out for user-authored, self-only, opt-in thresholds ("if I log ten urges in a week, tap *my* shoulder") — arguably the fulfillment of "inventory, never obligation," since the user is informing themselves — or a reasoned, documented refusal. The current silence is undesigned, which is the one state a principles-first project should not tolerate.

**The clinical boundary (the widow case).** *Mechanism:* a grieving user's first reflex is rejection; the window-scoped rejection memory (correctly designed — finding 5 invalid) still leaves the *bridge* to care resting on an undocumented export script and a lucky friend. *Early warning:* sustained capacity ≤2 with a rejected care-boundary shape in history. *Counter:* ship the clinician packet as a first-class command with a defined first window and a discovery mention; add a user-elected standing clinical-context field (Marcus's "three years sober" must not travel by voice); specify a respectful re-open rule for care-boundary shapes under accumulating evidence at gate cadence — the verifier confirmed window aging already permits this; make it deliberate rather than incidental.

## 7. What information we are missing

**Only real humans can settle:** whether floor-equilibrium at day 365 is integration or mummification — synthetic personas cannot feel the difference between a kept promise and a dead habit; the true base rate of the uncanny insight (one per user-quarter here may be an artifact of authored lives); whether witnesses fatigue over years rather than quarters; whether the household asymmetry (Tomas) resolves or corrodes over a real marriage's timescale; placebo and reactivity effects (Marcus's craving-naming was arguably the treatment); and everything about N=6 synthetic hard cases generalizing — all six were twelve-system quitters, which sharpens retention findings and biases everything else.

**Only real time can settle:** the day-300 death anniversary; a second breach in the same quarter; a witness's own crisis year; whether "the first one that survived my life" survives year two.

**The docs must decide (the load-bearing [GAP]s, consolidated from all six lives and three dyads):**
- Does a corrections[] backfill retract a fired L1/L2, refund budget, restore streak, un-record a breach? (Asked by every single simulation.)
- Streak formula: Red survival nights, partials, honest skips — increment or reset? Breach misses vs. the isolated-miss budget; stake re-arm within a quarter.
- Green-floors scoring vs. "floor = completion for all adherence math": is Green strictly dominated, and is chronic Yellow legitimate for gate eligibility?
- Is the nightly close-out line a proposal-eligible session? (scope.md §3 and agent-contracts §"How contracts compose" currently conflict — this single answer determines whether the Mirror starves or spams.)
- L3 mechanics: who executes, verifies, records waiver; executor consent and conflict-of-interest at Day 0; what happens when the user simply doesn't pay.
- Ask-once scope (per-event is derivable; ceiling-vs-floor is not); witness conduct after the ask; independent-knowledge exemption; out-of-band contact vs. the silence backstop; Ring 0 as pollable view vs. push-only; mutual witnessing; witness resignation/replacement.
- Heartbeat semantics in a non-quiet month; Phase-0 paraphrase legality; the same-morning L2+"all quiet" collision.
- The technical-helper role: contract, scope, audit — or an explicit statement that the helper is a conscious Ring-1+ disclosure.
- Whether a person can be an off-limits topic; where journaling-about-a-person norms live.
- Self-directed observation thresholds: carve-out or reasoned refusal.
- taken:false meds and the packet's regimen header; the packet's first-ever window.
- Retro skipped: failure path, floor variant, Crux record surface.
- Post-Gate-1 second-chain mechanics; any home for weekly-cadence commitments.

## 8. Measuring a better human

The tension — obliquity (P4) versus the owner's "many measurable ways" — resolves on a fact the data already demonstrated: **obliquity governs surfaces and cadence, not the existence of evidence.** Devon metricized his forever-unmeasured item in Google Sheets within two weeks; the Ledger is user-owned and the evidence exists regardless. What P4 actually forbids is daily targets and scores on outcomes. Therefore: **gate cadence is the instrument aperture.** The proposed panel, every element surfaced only at Gates, none ever on /status:

1. **Charter-derived behavioral proxies** (3–5, declared at calibration, computed by deterministic script from ledger mentions, reviewed quarterly only): dinners cooked, days with live human contact, deep-work blocks. *Failure modes:* Goodhart if any proxy leaks to a daily surface (enforce via storage-adapter contract — proxy queries answerable only by the gate reviewer); journal performativity (partially self-correcting; audit against instrument 3).
2. **Honest-number pairing, mandatory:** any display of mode-relative adherence co-presents floor-day ratio and raw days-accounted, and the canary gets a threshold that trips a floored Retro question, not a score. *Failure mode:* Green-declaring to dodge the ratio — mitigated because undeclared-Green misses cost budget.
3. **Third-party noticing** — the only anti-Goodhart instrument observed working (Priya's dock confirmation; Tomas noticing morning bids). At gate cadence the *user* asks one templated open question of witness or named partner ("noticed anything different?") and records the verbatim answer as testimony, not metric. User-relayed — no new send path; the message ceiling holds. *Failure modes:* observer expectancy, performing improvement for a spouse, skipping in bad quarters (record the skip).
4. **A rule registry** — insight-to-action as the Mirror's own KPI. When a user accepts an insight and states a rule ("after Darnell contact, call a program guy"), record it; at each gate ask once whether it still stands. Conversion rate (~1.4/user-quarter observed, uninstrumented) and rule survival are the honest measure of the Mirror. *Failure mode:* rules creeping toward Engine semantics — mitigate with P3 language: no streaks, no scores, inventory only.
5. **Infrastructure events:** count clinician-packet exports plus one user-recorded bit — did the conversation change? Three of six lives; three changed conversations. Cheap, oblique, and it measures the value cluster the data says is real.
6. **The counterfactual honesty line:** every gate panel carries a user-tagged storm record beside outcomes and compares gate-over-gate medians, with the panel template stating plainly that attribution is a hypothesis the panel exists to make arguable, not prove. This is the only available hedge against regression to the mean (Maya's D60 dip, Marcus's pain curve).

Forever-unmeasured items get exactly one instrument: qualitative hypothesis proposals through the resonance gate. Maya's bedtime insight and Devon's kids insight served precisely these items without a number, twice, and produced the study's largest life changes. That *is* the oblique instrument, proven.

## 9. The Scientist

The owner asked: "how do we figure things out without us figuring them out?" The answer the study converged on: **the Scientist is a stance and a lifecycle, not a subsystem** — architecture §4b's prohibition on new modules stands. It assembles entirely from the four sanctioned extension axes: one registry (`experiments/`, following `threads/`), two event kinds (`hypothesis`, `verdict`) inside the frozen envelope, and deterministic projections (protocol reports, verdict cards). Its cadence already exists: **pre-register at a Retro, switch arms only at a Retro (the one-change rule gives block designs for free), deliver verdicts at a Gate, adopt only through the resonance gate.** The weekly Retro is already a lab meeting; this gives it a lab notebook.

The need is empirical: every persona ran feral experiments anyway — Devon's 1am Sheets paired-comparison, Marcus laundering pain into journal lines, Maya's whole quarter an uncontrolled trial of fixed clocks against a roster. Tooled science can be governed; feral science cannot.

**The lifecycle, binding rules:** pre-registration is consent (P5) — hypothesis, arms, primary measure, decision rule, and stop rules written while strong, before data, append-only; verdicts are computed by deterministic script and enter the resonance gate as proposals — the system never quietly becomes the winning arm; every arm must be a configuration the Retro could legally adopt on its own (equipoise by construction); underpowered and invalidated are first-class verdicts, and nulls are kept.

**Four protocols fall straight out of the six lives:** P-1 bell-timing/roster ABAB (Maya — primary measure: misfire count, which requires finding 3's corrections fix first, since today misfires aren't even countable); P-2 Mirror lens rotation (measured on acted-on, not acceptance — Devon and Sam both accepted out of politeness; honestly framed as preference exploration at current insight rates); P-3 enricher-correlation confirmation (exploratory correlate → registered hypothesis → evaluate only on post-registration days, with pre-registered missingness handling — the fix for Maya's Day-87 artifact); P-4 floor-size titration (Sam — staircase per Gate with a pre-committed reversal rule; null hypothesis: smaller is right).

**Guardrails:** no teeth ever (P3 — declining an experiment is never a miss, never reaches the witness); no daily readouts (blinding-by-boredom: interim results computed and never shown, killing both peeking and outcome-metric leakage); Goodhart defenses (measures frozen at registration and *retired at verdict*; prefer exhaust over testimony — adherence percentage is disqualified as a primary measure because the spec itself makes it unfalsifiable; pre-registered gaming checks so discovering gaming is a finding, not an accusation); reactivity honesty (a mandatory "measurement itself may be the mechanism" line — in N-of-1, placebo is a treatment to keep if the user wants it).

**When the Scientist must not run:** never during collapse (a machine-readable storm state — capacity ≤2 for N days, active escalation, Charter-named ambush windows — freezes arm switches and verdict deliveries; frozen blocks are censored, not counted); never on load-bearing supports (recovery infrastructure, forever-unmeasured outcomes, off-limits topics — the difference between science and vivisection, and the sims contain the negative proof); consent while strong, revocable while weak, stopping costs one word and records nothing; no experiments in the first 30 days.

**Cross-user learning without telemetry** — the direct answer to the owner's question. Local-first means the data never leaves; it does not mean the design cannot learn. Layer 0: protocols are specs (`docs/protocols/`), containing no instance data, versioned like any spec. Layer 1: pattern-library and protocol improvements ship *to* instances as spec releases the user git-pulls; changed defaults are proposed at the next Retro per P8, never silently applied. Layer 2: the **verdict card** — a projection from a closed experiment carrying protocol id, arm ids, block count, coverage band, quantized effect direction, confounds, verdict; zero raw data, zero dates, zero free text — released only by deliberate human hand under the same render→review→redact→release→log discipline as the clinician packet. Default is never-release. Layer 3: the project aggregates voluntarily released cards ("P-1 ran on 9 instances; 7 adopted profiled bells; both nulls were fixed-schedule users") into sharpened defaults and preconditions in the next spec release. And the cheapest channel of all, modeled by this study itself: **gap reports** — pure spec questions containing no personal data ("do backfills retract a fired L2?") filed as first-class, template-supported bug reports against the shared genome. Lucid-the-project gets smarter; Lucid-the-instance stays sovereign, because every boundary crossing is a human act on a reviewed artifact.

## 10. Never abandoned

The retention evidence is unusually consistent. What kept six twelve-system quitters through 90 days: a floor cheap enough that shame couldn't outbid it; a restart that owed nothing; at least one consequence that left the user's own head; a human who exceeded spec at the kill moment; and one early concrete external payoff (usually the packet). The design owns the first three-and-a-half; it currently free-rides on the off-script human and hides the payoff behind an undocumented script. What threatens day 365 is two scenarios, named identically by all six verdicts: one more punishment of circumstance, and hollowing the system cannot see.

**Ranked design deltas by day-365 probability raised across all six personas:**

1. **The pre-committed circumstance valve** (touches all six; addresses ~two-thirds of observed near-abandonments). One unified mechanism four sims independently invented: Day-0 storm clauses and ambush dates written while strong; a witness-confirmable incapacity state that annotates without rescoring and stays the stake while leaving L1/L2 contact intact; schedule/roster profiles that move bell, mode deadline, tripwire hour, and rollover together; corrections[] retroactivity so a same-morning backfill of a done-but-unrecorded floor retracts pending escalation. **This is the single highest-leverage change.** It removes every misfire observed while leaving the teeth fully intact for avoidance — the only thing the teeth were ever for — and it preserves the core wager because every element is pre-committed, not negotiated in the moment.
2. **Governance that cannot go dark + a canary with a number** (Devon, Elena, and the hollowing scenario everywhere): a floored 5-minute Retro variant, mandatory when the floor-day-ratio threshold trips; honest-number pairing at gates.
3. **A specced answer for the silence** (Sam, and eventually everyone): decide the return-template question explicitly against product-principles §7 — either a fifth pre-committed, self-authored, Day-0-consented send class, or a written refusal with a compensating design (e.g., the witness brief formally licensing one non-Lucid-framed contact); plus the cheaper-honesty L1 variant for /closeout skip and defined post-lapse /status emphasis.
4. **Ship the packet and the helper role** (the value proposition and the operability floor): a first-class export command with first-window rule, discovery mention, and clinical-context field; a named, scoped, logged helper role; /enable and a correction command so the role is less mandatory.
5. **Mirror consumption and fatigue mechanics** (all six, slower burn): proposal backoff ladder, acted-on field, gate-cadence insight re-surfacing — Elena's per-person recall surface being the strongest single version, and also the relational map she was sold.
6. **Witness lifecycle v1.1**: pre-committed L2 context line, non-quiet heartbeat variant, selection guidance, re-briefing cadence, executor consent at Day 0.

A note the numbers force: the two subsystems are diverging. The Engine has earned seatbelt status in all six lives; the Mirror is at subsistence in five. If only deltas 1–4 land, day-365 Lucid is a superb accountability loop with a vestigial understanding layer — retained, but not the product the docs describe. Delta 5 is what keeps the Mirror alive next to an Engine that no longer needs help.

## 11. Verified findings table

| # | Finding | Severity | Verdict | Citation |
|---|---------|----------|---------|----------|
| 1 | Escalation/stakes fire on circumstance; no incapacity/storm state | Critical | Confirmed-gap | engine.md §2/§4; error-states.md; calibration.md §6 |
| 2 | L3 has no executor, verifier, waiver record, or re-arm rule | Critical | Partially-addressed | engine-module.md §Tripwire step 5; engine.md §4 |
| 3 | corrections[] can't retract fired escalation; rollover edge files false misses | Critical | Confirmed-gap | engine-module.md §Storage/§Commands/§Tripwire; scope.md S-14 |
| 4 | Undocumented technical-helper role holds root, voids Ring 0 | Critical | Confirmed-gap | local-runtime.md §Runtime tree; vision.md §7 |
| 5 | Clinical patterns "silenced forever" | Critical | **Invalid** | acceptance-criteria.md Phase 5; agent-contracts.md §3 |
| 6 | Ladder ends at L2; system mute through silences | Critical | Partially-addressed | engine-module.md §Tripwire; product-principles.md §7 |
| 7 | Clock-fixed mechanics vs. rotating shifts | Critical | Confirmed-gap | engine-module.md §chain.json; engine.md §5; observations.md §3/§7 |
| 8 | Witness signal semantics: alarm without info, quiet floors hide crisis | High | Partially-addressed | engine.md §4; vision.md §7; engine-module.md Phase 10 |
| 9 | Adherence Goodharted; canary has no threshold | High | Partially-addressed | engine.md §3/§5; engine-module.md Phase 9 |
| 10 | Resonance fatigue invisible; no proposal backoff | High | Partially-addressed | steel-thread.md Stage 4; contrast observations.md §6 |
| 11 | Ask-once breaks both ways under intimacy | High | Partially-addressed | engine.md §4; calibration.md §Witness; scope.md S-13 |
| 12 | Heartbeat false-by-design after loud months | High | Partially-addressed | engine.md §4; engine-module.md §Consent amendment |
| 13 | Capture skew starves the Mirror; no missingness model | High | Partially-addressed | agent-contracts.md §3 vs. observations.md §7 |
| 14 | Witness lifecycle post-confirmation unspecified | High | Partially-addressed | engine.md §4/§5; engine-module.md Phase 10 |
| 15 | Accepted insights near-write-only; /reflect dies under load | High | Partially-addressed | agent-contracts.md §3; data-model.md §Insights |
| 16 | Templates context-blind; confession costs same as hiding | High | Partially-addressed | engine-module.md §Consent amendment/§Tripwire; engine.md §2 |
| 17 | Clinician packet: no command, window, discovery, disclosure field | High | Confirmed-gap | observations-module.md §Commands; observations.md §7/§10 |
| 18 | Morning/weekly/non-daily commitments homeless | High | Partially-addressed | engine.md §1–2/§5; calibration.md §Portfolio |
| 19 | Governance has no floor/failure path; Crux invisible | High | Confirmed-gap | engine.md §5/§6; engine-module.md §Day record |
| 20 | Onboarding: configuration without comprehension | High | Partially-addressed | engine.md §5 Day-0; calibration.md |
| 21 | Sustained distress logging gets undesigned silence | High | Confirmed-gap | observations.md §0/§9; architecture.md §3 |
| 22 | Asymmetric-adoption household has no story | High | Confirmed-gap | engine.md §Day-0 done; calibration.md §Witness/§Off-limits |

## 12. Recommended doc diffs

Prioritized; each is a doc change first, per claude-code-workflow.md.

1. **engine.md §2/§4 + engine-module.md + calibration.md §6** — Add the storm clause / incapacity state / roster profile / non-travel load windows (the circumstance valve); define the incapacity state as annotate-without-rescoring, stake-staying, L1/L2-preserving. *Removes the study's dominant kill-shot.*
2. **engine-module.md §Storage + error-states.md** — Specify corrections[] semantics end-to-end: schema, a user-facing command, fold rules, and explicit retraction/refund/restore behavior against fired escalations; resolve the status.json rebuild inconsistency. *Six-of-six [GAP]; also P-1's prerequisite.*
3. **engine.md §4 + calibration.md §Witness** — L3 execution procedure: executor assignment, Day-0 executor/beneficiary consent, execution-vs-waiver record, re-arm rule, witness script for attempted renegotiation. *Zero executions against avoidance in five breaches means the teeth are currently prose.*
4. **engine.md §3/§5** — Give the floor-day-ratio canary a numeric threshold, a mandated floored (5-minute) Retro response, and mandatory honest-number pairing at gates; reconcile Green-floor scoring; define the missed-Retro failure path and a Crux/memo field in the day record. *The anti-hollowing package.*
5. **observations-module.md §Commands + observations.md §7/§10** — First-class clinician-packet command: name, first-window rule, discovery mention, user-elected standing clinical-context field, taken:false regimen handling. *Ships the highest-value artifact observed.*
6. **product-principles.md §7 + engine-module.md §Consent amendment** — Explicitly re-decide the silence question (self-authored return template: adopt or refuse with reasons); add deterministic L1 variants (Red-declared, skip-acknowledging); define heartbeat semantics for non-quiet months and the L2+heartbeat same-morning collision; give the verbatim template strings a doc home.
7. **local-runtime.md + vision.md §7** — Name the technical-helper role: scope, briefing, audit expectation — or state explicitly that a helper is a conscious Ring-1+ disclosure the user makes; add /enable to close the hand-edit dependency where cheap.
8. **agent-contracts.md §3 + steel-thread.md** — Record unanswered proposals; port the curiosity backoff ladder to Reflection; add an acted-on/behavior-link field to insights; a timing-sensitivity rule after emotionally loaded entries; resolve the scope.md-vs-agent-contracts.md conflict on close-out proposal eligibility.
9. **calibration.md + engine.md §5** — Day-0 comprehension step: rehearsed Yellow declaration in the baseline week, modes/budget taught in-band, actual L1/L2 text shown to the user, templates self-identifying as pre-committed form letters; witness selection guidance and re-briefing cadence; a "what to tell a cohabitant" template paralleling the witness briefing.
10. **observations.md §0/§9 + architecture.md P3** — Explicitly decide the self-directed distress threshold: a narrowly scoped user-authored self-only carve-out, or a reasoned refusal in writing. *The silence must become designed either way.*
11. **docs/scientist.md (new) + architecture.md §4b + observations.md §3/§8 + docs/protocols/ (new)** — The Scientist lifecycle, `experiments/` registry, `hypothesis`/`verdict` event kinds, storm-state freeze, verdict-card aperture, gap-report template; one architecture paragraph stating it is a practice built from the extension axes, not a subsystem.
12. **data-model.md §People + vision.md §7** — Person-dominance threshold surfaced at gate cadence; the per-person recall surface as the first Mirror consumption feature; note that a person may be named in the off-limits registry.

The through-line of all twelve: keep the floors and the amnesty exactly as they are — they are the proven core — and spend the next quarter of doc work making sure the system never again fines a human for grieving, getting hurt, or working the night shift. Everything else compounds from there.
