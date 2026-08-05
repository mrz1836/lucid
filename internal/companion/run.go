package companion

import (
	"context"
	"errors"
	"fmt"
	"time"

	flywheel "github.com/mrz1836/go-flywheel"
	"github.com/mrz1836/go-foundation/models"
	"gorm.io/gorm"

	"github.com/mrz1836/lucid/internal/clockmark"
	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/flynode"
	"github.com/mrz1836/lucid/internal/scheduler"
	"github.com/mrz1836/lucid/internal/storage"
)

// Job runtime identifiers. The companion runs its own disposable flywheel node
// beside the Engine's, so its queue, kinds, and slugs are distinct: the three
// periodics never collide with the bell/tripwire pair, and a restart reconciles
// them by slug without duplicating.
const (
	queueName           = "lucid-companion"
	kindMorning         = "lucid_companion_morning"
	kindNight           = "lucid_companion_night"
	kindMorningBackstop = "lucid_companion_morning_backstop"
	slugMorning         = "lucid-companion-morning"
	slugNight           = "lucid-companion-night"
	slugMorningBackstop = "lucid-companion-morning-backstop"

	// backfillCap is the bounded missed-fire catch-up the flynode boot spine
	// applies (a single most-recent bucket). It is mirrored here only so the
	// in-process test rig builds its scheduler with the same cap the daemon runs.
	backfillCap = 1

	// envCompanionDB overrides the job-DB path when --db is not passed. Like the
	// Engine's flywheel.db it is disposable machinery, not Ledger truth, so it
	// lives outside ~/.lucid by default.
	envCompanionDB = "LUCID_COMPANION_DB"

	// Missed-fire cut-offs (Q5→A). They are worker constants, not config: past
	// them the host was down long enough that a "morning" at noon would read as
	// stale, so the companion skips the send and alerts instead. The morning
	// rides until 10:00, the night until 22:00, local.
	morningCutoff = "10:00"
	nightCutoff   = "22:00"

	// lateGrace is how long after the scheduled mark a fire still counts as
	// on-time. Beyond it the fire was almost certainly a backfill after the host
	// was asleep, so the message carries the honest late note; a fire within the
	// grace (a healthy on-time tick) carries none.
	lateGrace = 30 * time.Minute

	// lateNote prefixes a message delivered materially after its scheduled mark
	// — an honest "this is late, the machine was down" rather than a silent slip.
	lateNote = "(late — host was asleep)"

	// morningBackstopGrace is how long after the morning mark the backstop waits
	// before it speaks. The primary send's retry ladder is spent well inside it,
	// so an hour is clear of the send it backs up rather than racing it — and it
	// is still honestly "morning", far inside the 10:00 cut-off. Deriving the
	// backstop from the morning mark rather than the cut-off is the mirror image
	// of the evening backstop's reasoning: the morning window re-runs the *same*
	// send behind the same receipt, so it cannot double-post and needs no
	// cut-off-relative separation.
	morningBackstopGrace = 60 * time.Minute

	// minutesPerDay is the wrap modulus for clock-mark arithmetic.
	minutesPerDay = 24 * 60

	// backstopNote prefixes a message the morning backstop delivers. It is
	// deliberately not lateNote: every backstop fire lands past lateGrace, so
	// reusing the late note would stamp "host was asleep" on every one of them —
	// false for the case the backstop actually exists to cover, a send that
	// failed on a healthy, awake host. The message says which event happened
	// rather than guessing.
	backstopNote = "(backstop — the scheduled morning send did not go out on time)"
)

var (
	errNoStore    = errors.New("companion: store is required")
	errNoNotifier = errors.New("companion: notifier is required")
	errNoNumbers  = errors.New("companion: numbers reader is required")
	errNoVerdict  = errors.New("companion: verdict reader is required")
)

// MessageComposer composes one window's message. *Composer satisfies it; the
// node tests inject a scripted fake so the missed-fire, idempotency, and
// alerting logic is exercised without a live provider.
type MessageComposer interface {
	Compose(ctx context.Context, mode Mode, now time.Time) (Result, error)
}

// Deliverer is the notifier surface the companion delivers through. The
// message-id-returning send plus the read-back verify give the "a real Discord
// id reappears in the channel" guarantee and the idempotency the receipt keys
// on; the plain Send carries the loud alert when a fire cannot deliver its real
// message. *notify.Discord satisfies all three.
type Deliverer interface {
	SendReturningID(ctx context.Context, channel, text string) (string, error)
	VerifyPresent(ctx context.Context, channel, messageID string) error
	Send(ctx context.Context, channel, text string) error
}

// Runner delivers one composed companion message for a window with the
// life-critical guarantees layered on top of [Composer]: a bounded missed-fire
// catch-up, delivery idempotency + read-back through a persisted receipt, and a
// loud alert on any total miss — never a silent empty send. The node workers and
// the `lucid companion fire` verb share it so an on-demand fire and a scheduled
// fire take the exact same path.
type Runner struct {
	compose MessageComposer
	deliver Deliverer
	store   *storage.Adapter
}

// NewRunner wires a Runner over the compose dependencies, the delivery
// transport, and the Ledger store that holds the delivery receipts.
func NewRunner(d Deps, deliver Deliverer, store *storage.Adapter) *Runner {
	return &Runner{compose: New(d), deliver: deliver, store: store}
}

// Outcome records how one fire resolved so the CLI and the tests can report it.
// A fire is exactly one of: Delivered (with a MessageID and, on a late/backfill
// fire, Late), or Skipped (SkipReason "already-delivered" for the idempotent
// path, "past-cutoff" for the stale-window refusal). Fallback records the
// deterministic path fired (the provider was unreachable — only warmth was
// lost); Text carries the delivered body for logging.
type Outcome struct {
	Mode       Mode
	Delivered  bool
	MessageID  string
	Late       bool
	Fallback   bool
	Skipped    bool
	SkipReason string
	Text       string
}

// Fire composes and delivers one window's message at `now`, honoring the whole
// reliability contract in order: refuse a stale send past the cut-off (alert
// instead), skip an already-delivered window idempotently (a verified receipt
// that still reads back), compose (loud on any failure), prefix the late note on
// a backfilled fire, deliver with read-back, and persist the receipt. Any total
// send failure fires a best-effort alert and returns a loud error — silence is
// the one outcome it never produces.
func (r *Runner) Fire(ctx context.Context, mode Mode, now time.Time) (Outcome, error) {
	return r.fire(ctx, mode, now, lateNote)
}

// FireBackstop re-runs the *real* morning card on the morning backstop's behalf
// — the same compose-and-deliver path [Runner.Fire] takes, so the backstop
// inherits every guarantee rather than reimplementing any of them. In
// particular it reads the same delivery receipt through the same guard, so a
// morning the primary send already delivered is an idempotent skip and a
// double-post is impossible by construction.
//
// It differs from Fire in exactly one respect: the note a late fire carries.
// Every backstop fire is late by definition (it sits an hour past the mark), so
// reusing the late note would misreport a delivery failure as a sleeping host.
func (r *Runner) FireBackstop(ctx context.Context, now time.Time) (Outcome, error) {
	return r.fire(ctx, ModeMorning, now, backstopNote)
}

// fire is the shared body behind [Runner.Fire] and [Runner.FireBackstop]. The
// prefix a late fire carries is the only axis the two differ on; everything
// else — the cut-off, the receipt guard, the compose, the read-back, the alerts
// — is identical for both callers, which is what keeps the backstop honest
// about exactly-once.
func (r *Runner) fire(ctx context.Context, mode Mode, now time.Time, note string) (Outcome, error) {
	win, err := windowFor(mode)
	if err != nil {
		return Outcome{}, err
	}

	chain, err := r.store.ReadChainConfig()
	if err != nil {
		return Outcome{}, fmt.Errorf("companion: read chain: %w", err)
	}
	markHM, err := win.mark(chain)
	if err != nil {
		return Outcome{}, err
	}
	markAt, err := atClock(now, markHM)
	if err != nil {
		return Outcome{}, err
	}
	cutoffAt, err := atClock(now, win.cutoff)
	if err != nil {
		return Outcome{}, err
	}

	fired, err := flynode.Fire(ctx, now, flynode.Delivery[Result]{
		Channel:  engine.ChannelUser,
		CutoffAt: cutoffAt,
		LateAt:   markAt.Add(lateGrace),
		Guard: func(_ context.Context, gnow time.Time) (flynode.Receipt, error) {
			rec, ok, rerr := r.store.ReadCompanionReceipt(string(mode))
			if rerr != nil {
				return flynode.Receipt{}, fmt.Errorf("companion: read receipt: %w", rerr)
			}
			if ok && rec.Date == engine.DateString(engine.DateOf(gnow)) && rec.MessageID != "" {
				return flynode.Receipt{MessageID: rec.MessageID, Channel: engine.ChannelUser, Found: true}, nil
			}
			return flynode.Receipt{}, nil
		},
		Compose: func(cctx context.Context, cnow time.Time) (Result, error) {
			return r.compose.Compose(cctx, mode, cnow)
		},
		LatePrefix: func(res Result) Result {
			res.Text = note + "\n\n" + res.Text
			return res
		},
		Send: func(sctx context.Context, channel string, res Result) (string, error) {
			id, serr := r.deliver.SendReturningID(sctx, channel, res.Text)
			if serr != nil {
				return "", fmt.Errorf("companion: deliver %s: %w", mode, serr)
			}
			return id, nil
		},
		Verify: func(vctx context.Context, channel, id string) error {
			if verr := r.deliver.VerifyPresent(vctx, channel, id); verr != nil {
				return fmt.Errorf("companion: verify %s delivery: %w", mode, verr)
			}
			return nil
		},
		WriteReceipt: func(wnow time.Time, channel, id string) error {
			if werr := r.store.WriteCompanionReceipt(storage.CompanionReceipt{
				Date:        engine.DateString(engine.DateOf(wnow)),
				Window:      string(mode),
				MessageID:   id,
				ChannelID:   channel,
				Verified:    true,
				DeliveredAt: wnow.Format(time.RFC3339),
			}); werr != nil {
				return fmt.Errorf("companion: write receipt: %w", werr)
			}
			return nil
		},
		Alert:        func(text string) { r.alert(ctx, text) },
		CutoffAlert:  fmt.Sprintf("Lucid %s companion skipped — it is past the %s cut-off (the host was asleep at the fire time). No stale message was posted.", mode, win.cutoff),
		ComposeAlert: fmt.Sprintf("Lucid %s companion could not compose a message — the scheduled send did not go out.", mode),
		SendAlert:    fmt.Sprintf("Lucid %s companion failed to deliver — the scheduled send did not go out.", mode),
		VerifyAlert:  fmt.Sprintf("Lucid %s companion sent a message that could not be verified in the channel.", mode),
	})
	if err != nil {
		return Outcome{}, err
	}

	return Outcome{
		Mode:       mode,
		Delivered:  fired.Delivered,
		MessageID:  fired.MessageID,
		Late:       fired.Late,
		Skipped:    fired.Skipped,
		SkipReason: fired.SkipReason,
		Fallback:   fired.Payload.Fallback,
		Text:       fired.Payload.Text,
	}, nil
}

// The two skip reasons a fire can report.
const (
	skipPastCutoff       = "past-cutoff"
	skipAlreadyDelivered = "already-delivered"
)

// alert posts a best-effort loud ping to the user channel when a companion fire
// cannot deliver its real message — the "never silent" floor. It is
// deliberately best-effort: if the channel itself is unreachable the returned
// error from [Runner.Fire] is the loud signal (it fails the job and lands in the
// supervised daemon log), and a failed alert must not mask that original error.
func (r *Runner) alert(ctx context.Context, text string) {
	_ = r.deliver.Send(ctx, engine.ChannelUser, text)
}

// window is the per-mode scheduling metadata: the periodic identity, the
// stale-message cut-off, and how to resolve the window's scheduled mark from the
// chain. The morning rides the tripwire mark and the night rides the bell mark,
// so the companion can never drift from the deterministic pair.
type window struct {
	slug   string
	kind   string
	cutoff string
	mark   func(engine.ChainConfig) (string, error)
}

// windowFor resolves the scheduling metadata for a window, rejecting a mode that
// is neither.
func windowFor(mode Mode) (window, error) {
	switch mode {
	case ModeMorning:
		return window{
			slug:   slugMorning,
			kind:   kindMorning,
			cutoff: morningCutoff,
			mark: func(c engine.ChainConfig) (string, error) {
				_, tripwire, err := scheduler.Marks(c, engine.DefaultProfile)
				return tripwire, err
			},
		}, nil
	case ModeNight:
		return window{
			slug:   slugNight,
			kind:   kindNight,
			cutoff: nightCutoff,
			mark: func(c engine.ChainConfig) (string, error) {
				bell, _, err := scheduler.Marks(c, engine.DefaultProfile)
				return bell, err
			},
		}, nil
	default:
		return window{}, fmt.Errorf("companion: unknown mode %q", mode)
	}
}

// Options configures a companion daemon run. Store, Notifier, Numbers, and
// Verdict are required; DBPath and Clock default when zero (the disposable job
// DB outside ~/.lucid, and the real clock). Config/Provider select the compose
// backend and the operator's opaque prompt files; Build overrides the provider
// builder for tests.
type Options struct {
	Store    *storage.Adapter
	Config   config.CompanionConfig
	Provider config.ProviderConfig
	Numbers  NumbersReader
	Verdict  VerdictReader
	Notifier Deliverer
	DBPath   string
	Clock    models.Clock
	Build    ProviderBuilder
}

// companionArgs is the empty typed payload for both periodics: neither carries
// input — the message is a pure function of the Ledger + prompt files at fire
// time. Its JSON form is the empty object the scheduler enqueues.
type companionArgs struct{}

// morningWorker fires the morning companion.
type morningWorker struct {
	r     *Runner
	clock models.Clock
}

// Kind names the worker dispatched for kindMorning.
func (morningWorker) Kind() string { return kindMorning }

// Work composes and delivers the morning companion for `now`.
func (w morningWorker) Work(ctx context.Context, _ *flywheel.Job[companionArgs]) (flywheel.Result, error) {
	if _, err := w.r.Fire(ctx, ModeMorning, w.clock.Now(ctx)); err != nil {
		return flywheel.Result{}, err
	}
	return flywheel.Result{}, nil
}

// nightWorker fires the night companion.
type nightWorker struct {
	r     *Runner
	clock models.Clock
}

// Kind names the worker dispatched for kindNight.
func (nightWorker) Kind() string { return kindNight }

// Work composes and delivers the night companion for `now`.
func (w nightWorker) Work(ctx context.Context, _ *flywheel.Job[companionArgs]) (flywheel.Result, error) {
	if _, err := w.r.Fire(ctx, ModeNight, w.clock.Now(ctx)); err != nil {
		return flywheel.Result{}, err
	}
	return flywheel.Result{}, nil
}

// morningBackstopWorker re-fires the morning companion an hour after its mark,
// so a morning whose primary send failed outright is not a silent one. It is
// the morning counterpart of the Engine's evening bell fallback.
type morningBackstopWorker struct {
	r     *Runner
	clock models.Clock
}

// Kind names the worker dispatched for kindMorningBackstop.
func (morningBackstopWorker) Kind() string { return kindMorningBackstop }

// Work re-runs the morning card unless the window has gone stale, in which case
// it stands down without a word.
func (w morningBackstopWorker) Work(ctx context.Context, _ *flywheel.Job[companionArgs]) (flywheel.Result, error) {
	now := w.clock.Now(ctx)
	cutoffAt, err := atClock(now, morningCutoff)
	if err != nil {
		return flywheel.Result{}, err
	}
	// Past the cut-off the backstop says nothing at all — not even the stale-window
	// alert. The primary morning periodic reaches that cut-off first and already
	// alerted for this window, so a second copy would only be noise about a fault
	// that has been reported once. Returning cleanly (rather than erroring) keeps
	// a backstop that had nothing to do off the job-failure ladder.
	if !now.Before(cutoffAt) {
		return flywheel.Result{}, nil
	}
	if _, err := w.r.FireBackstop(ctx, now); err != nil {
		return flywheel.Result{}, err
	}
	return flywheel.Result{}, nil
}

// Run opens the disposable job DB, migrates it, registers the morning, night,
// and morning-backstop workers, seeds the three periodics from the chain's
// bell/tripwire marks and repairs any that were left parked, and runs the
// flywheel node until ctx is canceled (SIGINT/SIGTERM upstream). It returns nil
// on a clean drain. It runs beside the Engine daemon in the same process
// (config-gated), so it is up whenever the scheduler is.
func Run(ctx context.Context, o Options) error {
	if o.Store == nil {
		return errNoStore
	}
	if o.Notifier == nil {
		return errNoNotifier
	}
	if o.Numbers == nil {
		return errNoNumbers
	}
	if o.Verdict == nil {
		return errNoVerdict
	}

	clock := flynode.ResolveClock(ctx, o.Clock)

	dbPath, err := DefaultDBPath(o.DBPath)
	if err != nil {
		return err
	}

	deps := Deps{Companion: o.Config, Provider: o.Provider, Numbers: o.Numbers, Verdict: o.Verdict, Chain: o.Store, Build: o.Build}
	runner := NewRunner(deps, o.Notifier, o.Store)
	reg := buildRegistry(runner, clock)

	return flynode.Boot(ctx, flynode.BootConfig{
		Pkg:      "companion",
		Queue:    queueName,
		DBPath:   dbPath,
		Clock:    clock,
		Store:    o.Store,
		Registry: reg,
		Reconcile: func(ctx context.Context, db *gorm.DB) error {
			if err := upsertPeriodics(ctx, db, o.Store); err != nil {
				return flynode.StartupErr(ctx, err)
			}
			// Seeding declares the schedule; this pass repairs it. They are not the
			// same job: an upsert restores each periodic's active flag but
			// deliberately preserves its cursor, so a periodic whose next run was
			// left frozen in the past would come back up still unreachable by the
			// due scan. Running the same pass `lucid scheduler reconcile` exposes —
			// rather than a second, boot-only notion of "parked" — is what keeps the
			// node and the command agreeing on what a healthy schedule looks like.
			// It is a no-op on a healthy store, and the intended-active guard means
			// it never reaches a slug that is not this node's.
			if _, err := Reconcile(ctx, db, ReconcileOptions{}); err != nil {
				return flynode.StartupErr(ctx, err)
			}
			return nil
		},
	})
}

// buildRegistry registers the morning, night, and morning-backstop workers over
// one runner.
func buildRegistry(r *Runner, clock models.Clock) *flywheel.Registry {
	reg := flywheel.NewRegistry()
	flywheel.Register(reg, morningWorker{r: r, clock: clock})
	flywheel.Register(reg, nightWorker{r: r, clock: clock})
	flywheel.Register(reg, morningBackstopWorker{r: r, clock: clock})
	return reg
}

// upsertPeriodics reconciles the three periodics from the default profile's
// clock marks (chain.json): the morning fires on the tripwire mark and the night
// on the bell mark, so the companion and the deterministic pair fire at the same
// instant, and the morning backstop fires a grace period after the morning. It
// is idempotent by slug, safe to call on every boot.
//
// No SetPeriodicActive call follows the upserts, unlike the Engine's evening
// pair: nothing here toggles by configuration, because the companion node only
// runs at all when the companion is enabled — so all three of its periodics are
// intended active whenever this code is reachable.
func upsertPeriodics(ctx context.Context, db *gorm.DB, store *storage.Adapter) error {
	chain, err := store.ReadChainConfig()
	if err != nil {
		return fmt.Errorf("companion: read chain config: %w", err)
	}
	bellMark, tripwireMark, err := scheduler.Marks(chain, engine.DefaultProfile)
	if err != nil {
		return fmt.Errorf("companion: resolve clock marks: %w", err)
	}
	// The tripwire mark is parsed once and both morning-side crons are derived
	// from the resulting minutes, so the backstop can never disagree with the
	// morning send about a mark they both read.
	tripwireMin, err := markMinutes(tripwireMark)
	if err != nil {
		return err
	}
	morningCron := cronFromMinutes(tripwireMin)
	backstopCron := cronFromMinutes(morningBackstopMinutes(tripwireMin))
	nightCron, err := cronFromHM(bellMark)
	if err != nil {
		return err
	}
	if err := flywheel.UpsertPeriodic(ctx, db, flywheel.PeriodicSpec{
		Slug: slugMorning, Kind: kindMorning, Cron: morningCron, Queue: queueName, Active: true,
	}); err != nil {
		return fmt.Errorf("companion: upsert morning periodic: %w", err)
	}
	if err := flywheel.UpsertPeriodic(ctx, db, flywheel.PeriodicSpec{
		Slug: slugNight, Kind: kindNight, Cron: nightCron, Queue: queueName, Active: true,
	}); err != nil {
		return fmt.Errorf("companion: upsert night periodic: %w", err)
	}
	if err := flywheel.UpsertPeriodic(ctx, db, flywheel.PeriodicSpec{
		Slug: slugMorningBackstop, Kind: kindMorningBackstop, Cron: backstopCron, Queue: queueName, Active: true,
	}); err != nil {
		return fmt.Errorf("companion: upsert morning backstop periodic: %w", err)
	}
	return nil
}

// morningBackstopMinutes derives the morning backstop's mark from the morning
// send it backs up, both in minutes since local midnight (usage/commands.md
// §"The morning backstop"): the tripwire mark plus a grace, wrapped at midnight.
//
// It is derived rather than configured so the two can never drift — move
// tripwire_time and the backstop moves with it. There is no clamp against the
// morning cut-off, because the backstop is self-limiting: past the cut-off its
// worker stands down without a word, so an exotic mark makes it inert rather
// than noisy.
func morningBackstopMinutes(tripwireMin int) int {
	return (tripwireMin + int(morningBackstopGrace/time.Minute)) % minutesPerDay
}

// atClock builds the instant at the "HH:MM" wall-clock mark on now's calendar
// date, in now's location — the reference the missed-fire window compares
// against. Both companion windows fire within the calendar day they belong to
// (morning until 10:00, night until 22:00), so a calendar-date anchor never
// crosses a boundary. A malformed mark is rejected rather than silently
// mis-scheduled.
func atClock(now time.Time, hm string) (time.Time, error) {
	h, m, err := parseHM(hm)
	if err != nil {
		return time.Time{}, err
	}
	return time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location()), nil
}

// cronFromHM turns an "HH:MM" clock mark into a daily 5-field cron expression:
// "19:00" -> "0 19 * * *". A malformed mark is rejected rather than silently
// mis-scheduled.
func cronFromHM(hm string) (string, error) {
	total, err := markMinutes(hm)
	if err != nil {
		return "", err
	}
	return cronFromMinutes(total), nil
}

// markMinutes parses an "HH:MM" clock mark into minutes since local midnight —
// the form the morning-side mark arithmetic works in. A malformed mark (wrong
// shape, out-of-range hour or minute, non-numeric field) is rejected rather than
// silently mis-scheduled.
func markMinutes(hm string) (int, error) {
	mark, err := clockmark.Parse(hm)
	if err != nil {
		return 0, fmt.Errorf("companion: %w", err)
	}
	return mark.Minutes(), nil
}

// cronFromMinutes renders minutes since local midnight as a daily 5-field cron
// expression: 1290 -> "30 21 * * *".
func cronFromMinutes(total int) string {
	return fmt.Sprintf("%d %d * * *", total%60, total/60)
}

// parseHM parses an "HH:MM" clock mark into its hour and minute, rejecting a
// wrong shape, an out-of-range field, or a non-numeric field.
func parseHM(hm string) (hour, minute int, err error) {
	mark, err := clockmark.Parse(hm)
	if err != nil {
		return 0, 0, fmt.Errorf("companion: %w", err)
	}
	return mark.Hour(), mark.Minute(), nil
}

// DefaultDBPath resolves the disposable companion job-DB path: an explicit
// override wins, then the LUCID_COMPANION_DB env override, then the default
// companion.db under the OS user-config dir (outside the ~/.lucid Ledger, and
// distinct from the Engine's flywheel.db). It is exported so a read-only inspector
// resolves the exact same path the daemon writes to.
func DefaultDBPath(dbPath string) (string, error) {
	return flynode.ResolveDBPath(dbPath, envCompanionDB, "companion.db")
}
