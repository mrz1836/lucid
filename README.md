Nobody handed you an instruction manual. You got a body that hurts in ways you can't explain in a fifteen-minute appointment. Patterns you've sworn off every January since you can remember. Insights that arrive in the shower and dissolve by breakfast. A story about yourself that quietly rewrites its own history every time you fall short. And everyone you know is carrying some version of the same thing — we all hurt, we all repeat, we all forget. We just don't keep records.

Imagine waking up and knowing exactly what's been weighing on you—not vaguely, but precisely. You've mentioned feeling "not enough" four times this week. The system noticed. It connects this to a wound you identified months ago and asks: *Does this resonate?* Never *you are* — always *does this fit?* You're the only authority on you; Lucid just never loses the evidence.

Imagine your body finally having a biographer. One-line logs — pain, meals, sleep, a mood, a memory that surfaced — and the world's half filled in around them: the weather that day, where you were. Eighteen months later you walk into an appointment carrying the record no doctor has time to take: every flare, everything you tried, what actually helped. Your suspicion that the pressure drop wrecks your knee? Finally checkable, against your own data. The hard days stop being merely endured — they become the most informative entries in the set.

Your friend's birthday is approaching. A message is already drafted—it mentions the trip you took together last year, the inside joke about coffee. It sounds like you, because it learned how you express care. You review it, change a word, send it. The intention was always yours; the system just cleared the friction. This is what it looks like when the people you love get the best of you — instead of whatever was left of you.

And the past isn't as gone as it feels. Most of your life was never lost — it's unindexed. Give the system one fragment a week — the house you grew up in, the year everything changed — and watch what comes back within reach: places you haven't thought about in twenty years, the exact moment a pattern was born. You will remember more than you believe you can. And this time it stays remembered, in your own words, no longer at the mercy of the story your mind keeps revising. Not a therapist you see once a week. Not a journal you forget to write in. Something that holds your whole story across time — and asks the questions you didn't know you needed to hear.

Now the honest part. You've tried systems before. There's a graveyard of them behind you, and every headstone reads the same: day twelve, the first miss, the shame about the miss, the quiet uninstall. **Lucid was designed from that autopsy.** The worst day costs one sentence — that is the entire floor. A miss is followed by the smallest possible night, never by makeup work. Nothing you *say* is ever scored, and a return after silence is welcomed, not audited. And still — at 9:30 tonight, a bell rings, because insight you never apply is just a more articulate way of staying stuck. There are real teeth here: a witness, a stake, a record that cannot be rewritten by a bad week's narrator. But the teeth only ever touch whether you showed up. Never what you said when you did. The half that understands you never coerces; the half that pushes you never reads your journal. That boundary is the whole design — and it's why this is the one that survives you.

Ten years from now, there are two versions of you. One has a decade of evidence: every pattern named and tested against reality, a body mapped against weather and sleep and everything it was fed, the recovered memories, the receipts of who they became. Every year the tools get smarter, and they amplify whatever you hand them — and only you can hand them *you*. The other version is still making the January promise, from memory. Same person. Same pain. The only difference is that one of them kept the record.

<br>

---

<br>

We were all born without the manual.

This is how you write yours. This is [**Lucid**](docs/vision.md).

The first entry is one line. The bell rings at 9:30.

<br>

---

<br>

## Where to start

Three doors, depending on who you are:

- **You want to start living this — tonight, no software.** Copy [docs/calibration.md](docs/calibration.md) to `personal/calibration.md` and answer its questions. Set the bell, stage the environment — five minutes, and Phase 0 is running. The rest of Day 0 is one weekend, and the checklist is in the guide.
- **You want to build it.** Read [docs/mvp/scope.md](docs/mvp/scope.md) (one page, build-ready), then [docs/mvp/README.md](docs/mvp/README.md), then follow the phase order — the acceptance criteria tell you when each phase is done.
- **You want to understand it.** Read [docs/architecture.md](docs/architecture.md) first (the whole design in ~150 lines), then [vision.md](docs/vision.md) for the felt version, then [docs/engine.md](docs/engine.md) for the mechanics.

## Read more

- [Vision](docs/vision.md) — the long-form product vision and the roles Lucid plays.
- [System architecture](docs/architecture.md) — the canonical merge: the Mirror (understanding) and the Engine (behavior) over one user-owned Ledger, and the ten principles that govern both.
- [Engine specification](docs/engine.md) — chains, bells, floors, operating modes, telemetry, and the accountability ladder.
- [Observation layer](docs/observations.md) — body signals, intake, mood, context, and memory fragments on one frozen event envelope; registries, enrichers, and the projections that turn a life into a queryable record. Inventory, never obligation.
- [Calibration](docs/calibration.md) — aim the system at your own life; specs are shared, calibration is yours. (Private data lives in `personal/`, which never enters shared history.)
- [Technical specification](docs/technical-spec.md) — the reference architecture for the full system.
- [MVP docs](docs/mvp/README.md) — the first buildable slice: the unified nightly loop (close-out → capture → structure → one possible pattern → validation → weekly recall).
- [MVP scope spec](docs/mvp/scope.md) — the build-ready contract for that slice.
- [Decision records](docs/adr/README.md) — why the build goes the way it does: Go core with a CLI-first surface, plain files with SQLite as a later index, chat harness as thin sugar. Nothing locks before the loop earns it.
