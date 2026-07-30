package workout

// This file owns the workout module's **daily-slot daemon** — the configurable-
// time periodic that delivers the workout recommendation once a day. It mirrors
// internal/companion/run.go exactly: a disposable flywheel node beside the Engine
// and the companion, delivery idempotency + read-back through a persisted
// receipt, a bounded missed-fire catch-up, a deterministic fallback on a
// provider outage, and a loud alert on any total miss — never a silent empty
// send. The only shape difference is that the workout has a single window (not a
// morning/night pair) and its fire time is the operator's configurable
// WorkoutConfig.SlotTime (default midday) rather than a chain mark. See
// docs/mvp/workout-module.md §"Surfaces" and §"The daily slot".

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	flywheel "github.com/mrz1836/go-flywheel"
	"github.com/mrz1836/go-foundation/models"
	"gorm.io/gorm"

	"github.com/mrz1836/lucid/internal/clockmark"
	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/flynode"
	"github.com/mrz1836/lucid/internal/storage"
)

// Job runtime identifiers. The workout slot runs its own disposable flywheel
// node beside the Engine's and the companion's, so its queue, kind, and slug are
// distinct: the three periodic sets never collide, and a restart reconciles the
// workout slot by slug without duplicating.
const (
	queueName = "lucid-workout"
	kindDaily = "lucid_workout_daily"
	slugDaily = "lucid-workout-daily"

	// backfillCap is the bounded missed-fire catch-up the flynode boot spine
	// applies (a single most-recent bucket). It is mirrored here only so the
	// in-process test rig builds its scheduler with the same cap the daemon runs.
	backfillCap = 1

	// envWorkoutDB overrides the job-DB path when --db is not passed. Like the
	// Engine's flywheel.db and the companion's companion.db it is disposable
	// machinery, not Ledger truth, so it lives outside ~/.lucid by default.
	envWorkoutDB = "LUCID_WORKOUT_DB"

	// receiptWindow keys the workout slot's delivery receipt in the shared
	// companion receipt tree (engine/companion/receipt_workout.json). The single
	// daily slot reuses the companion receipt machinery, so its idempotency guard
	// is byte-identical to the morning/night windows'.
	receiptWindow = "workout"

	// defaultSlot is the slot's fallback fire time when WorkoutConfig.SlotTime is
	// empty — midday, aligned to the default midday workout. Config validation
	// requires a slot_time whenever the feature is enabled, so this only guards a
	// direct programmatic Run started with an unset slot.
	defaultSlot = "12:00"

	// lateGrace is how long after the slot mark a fire still counts as on-time.
	// Beyond it the fire was almost certainly a backfill after the host was
	// asleep, so the message carries the honest late note; a fire within the grace
	// (a healthy on-time tick) carries none.
	lateGrace = 30 * time.Minute

	// cutoffWindow is how long after the slot mark a fire still delivers. Past it
	// the host was down long enough that "today's session" would read as stale, so
	// the slot skips the send and alerts instead. A midday (12:00) slot therefore
	// rides until 18:00 local. It is a worker constant, not config: the staleness
	// horizon is fixed by the contract, not tuned per instance.
	cutoffWindow = 6 * time.Hour

	// lateNote prefixes a message delivered materially after its slot mark — an
	// honest "this is late, the machine was down" rather than a silent slip.
	lateNote = "(late — host was asleep)"
)

var (
	errNoStore    = errors.New("workout: store is required")
	errNoNotifier = errors.New("workout: notifier is required")
	errNoMetrics  = errors.New("workout: metrics reader is required")
)

// MessageComposer composes the day's workout message. *Composer satisfies it;
// the node tests inject a scripted fake so the missed-fire, idempotency, and
// alerting logic is exercised without a live provider.
type MessageComposer interface {
	Compose(ctx context.Context, now time.Time) (Result, error)
}

// Deliverer is the notifier surface the slot delivers through. The
// message-id-returning send plus the read-back verify give the "a real Discord
// id reappears in the channel" guarantee and the idempotency the receipt keys
// on; the plain Send carries the loud alert when a fire cannot deliver its real
// message. *notify.Discord satisfies all three.
type Deliverer interface {
	SendReturningID(ctx context.Context, channel, text string) (string, error)
	VerifyPresent(ctx context.Context, channel, messageID string) error
	Send(ctx context.Context, channel, text string) error
}

// Runner delivers one composed workout recommendation for the day with the
// life-critical guarantees layered on top of [Composer]: a bounded missed-fire
// catch-up, delivery idempotency + read-back through a persisted receipt, and a
// loud alert on any total miss — never a silent empty send. The node worker and
// the `lucid workout fire` verb share it so an on-demand fire and a scheduled
// fire take the exact same path.
type Runner struct {
	compose MessageComposer
	deliver Deliverer
	store   *storage.Adapter
	slot    string
}

// NewRunner wires a Runner over the compose dependencies, the delivery
// transport, and the Ledger store that holds the delivery receipts. The slot
// mark is read from the workout config so an on-demand `fire` uses the same
// configured fire time the scheduled node does.
func NewRunner(d Deps, deliver Deliverer, store *storage.Adapter) *Runner {
	return &Runner{compose: New(d), deliver: deliver, store: store, slot: d.Workout.SlotTime}
}

// Outcome records how one fire resolved so the CLI and the tests can report it.
// A fire is exactly one of: Delivered (with a MessageID and, on a late/backfill
// fire, Late), or Skipped (SkipReason "already-delivered" for the idempotent
// path, "past-cutoff" for the stale-window refusal). Fallback records the
// deterministic path fired (the provider was unreachable — only warmth was
// lost); Text carries the delivered body for logging.
type Outcome struct {
	Delivered  bool
	MessageID  string
	Late       bool
	Fallback   bool
	Skipped    bool
	SkipReason string
	Text       string
}

// The two skip reasons a fire can report.
const (
	skipPastCutoff       = "past-cutoff"
	skipAlreadyDelivered = "already-delivered"
)

// Fire composes and delivers the day's workout message at `now`, honoring the
// whole reliability contract in order: refuse a stale send past the cut-off
// (alert instead), skip an already-delivered day idempotently (a verified
// receipt that still reads back), compose (deterministic fallback on a provider
// outage, loud on any other failure), prefix the late note on a backfilled fire,
// deliver with read-back, and persist the receipt. Any total send failure fires
// a best-effort alert and returns a loud error — silence is the one outcome it
// never produces.
func (r *Runner) Fire(ctx context.Context, now time.Time) (Outcome, error) {
	markAt, err := atClock(now, slotOrDefault(r.slot))
	if err != nil {
		return Outcome{}, err
	}
	cutoffAt := markAt.Add(cutoffWindow)

	fired, err := flynode.Fire(ctx, now, flynode.Delivery[Result]{
		Channel:  engine.ChannelUser,
		CutoffAt: cutoffAt,
		LateAt:   markAt.Add(lateGrace),
		Guard: func(_ context.Context, gnow time.Time) (flynode.Receipt, error) {
			rec, ok, rerr := r.store.ReadCompanionReceipt(receiptWindow)
			if rerr != nil {
				return flynode.Receipt{}, fmt.Errorf("workout: read receipt: %w", rerr)
			}
			if ok && rec.Date == engine.DateString(engine.DateOf(gnow)) && rec.MessageID != "" {
				return flynode.Receipt{MessageID: rec.MessageID, Channel: engine.ChannelUser, Found: true}, nil
			}
			return flynode.Receipt{}, nil
		},
		Compose: func(cctx context.Context, cnow time.Time) (Result, error) {
			return r.compose.Compose(cctx, cnow)
		},
		LatePrefix: func(res Result) Result {
			res.Text = lateNote + "\n\n" + res.Text
			return res
		},
		Send: func(sctx context.Context, channel string, res Result) (string, error) {
			id, serr := r.deliver.SendReturningID(sctx, channel, res.Text)
			if serr != nil {
				return "", fmt.Errorf("workout: deliver: %w", serr)
			}
			return id, nil
		},
		Verify: func(vctx context.Context, channel, id string) error {
			if verr := r.deliver.VerifyPresent(vctx, channel, id); verr != nil {
				return fmt.Errorf("workout: verify delivery: %w", verr)
			}
			return nil
		},
		WriteReceipt: func(wnow time.Time, channel, id string) error {
			if werr := r.store.WriteCompanionReceipt(storage.CompanionReceipt{
				Date:        engine.DateString(engine.DateOf(wnow)),
				Window:      receiptWindow,
				MessageID:   id,
				ChannelID:   channel,
				Verified:    true,
				DeliveredAt: wnow.Format(time.RFC3339),
			}); werr != nil {
				return fmt.Errorf("workout: write receipt: %w", werr)
			}
			return nil
		},
		Alert:        func(text string) { r.alert(ctx, text) },
		CutoffAlert:  fmt.Sprintf("Lucid workout slot skipped — it is past the %s delivery window (the host was asleep at the slot time). No stale recommendation was posted.", cutoffAt.Format("15:04")),
		ComposeAlert: "Lucid workout slot could not compose a recommendation — the scheduled send did not go out.",
		SendAlert:    "Lucid workout slot failed to deliver — the scheduled send did not go out.",
		VerifyAlert:  "Lucid workout slot sent a message that could not be verified in the channel.",
	})
	if err != nil {
		return Outcome{}, err
	}

	return Outcome{
		Delivered:  fired.Delivered,
		MessageID:  fired.MessageID,
		Late:       fired.Late,
		Skipped:    fired.Skipped,
		SkipReason: fired.SkipReason,
		Fallback:   fired.Payload.Fallback,
		Text:       fired.Payload.Text,
	}, nil
}

// alert posts a best-effort loud ping to the user channel when a workout fire
// cannot deliver its real message — the "never silent" floor. It is deliberately
// best-effort: if the channel itself is unreachable the returned error from
// [Runner.Fire] is the loud signal (it fails the job and lands in the supervised
// daemon log), and a failed alert must not mask that original error.
func (r *Runner) alert(ctx context.Context, text string) {
	_ = r.deliver.Send(ctx, engine.ChannelUser, text)
}

// Options configures a workout daemon run. Store, Notifier, and Metrics are
// required; DBPath and Clock default when zero (the disposable job DB outside
// ~/.lucid, and the real clock). Config selects the slot time + the operator's
// opaque program/prompt files, Provider selects the compose backend, and
// Observations/Injuries are the bounded enrichment readers (a nil reader leaves
// the recommendation on its plain-calendar path rather than failing it). Build
// overrides the provider builder for tests.
type Options struct {
	Store        *storage.Adapter
	Config       config.WorkoutConfig
	Provider     config.ProviderConfig
	Metrics      MetricsReader
	Observations ObservationsReader
	Injuries     InjuryReader
	Notifier     Deliverer
	DBPath       string
	Clock        models.Clock
	Build        ProviderBuilder
}

// workoutArgs is the empty typed payload for the daily periodic: it carries no
// input — the message is a pure function of the Ledger + program/prompt files at
// fire time. Its JSON form is the empty object the scheduler enqueues.
type workoutArgs struct{}

// dailyWorker fires the workout slot.
type dailyWorker struct {
	r     *Runner
	clock models.Clock
}

// Kind names the worker dispatched for kindDaily.
func (dailyWorker) Kind() string { return kindDaily }

// Work composes and delivers the workout recommendation for `now`.
func (w dailyWorker) Work(ctx context.Context, _ *flywheel.Job[workoutArgs]) (flywheel.Result, error) {
	if _, err := w.r.Fire(ctx, w.clock.Now(ctx)); err != nil {
		return flywheel.Result{}, err
	}
	return flywheel.Result{}, nil
}

// Run opens the disposable job DB, migrates it, registers the daily worker,
// reconciles the daily periodic from the configured slot time, and runs the
// flywheel node until ctx is canceled (SIGINT/SIGTERM upstream). It returns nil
// on a clean drain. It runs beside the Engine and the companion in the same
// process (config-gated), so it is up whenever the scheduler is and the feature
// is enabled.
func Run(ctx context.Context, o Options) error {
	if o.Store == nil {
		return errNoStore
	}
	if o.Notifier == nil {
		return errNoNotifier
	}
	if o.Metrics == nil {
		return errNoMetrics
	}

	clock := flynode.ResolveClock(ctx, o.Clock)

	dbPath, err := DefaultDBPath(o.DBPath)
	if err != nil {
		return err
	}

	deps := Deps{
		Workout:      o.Config,
		Provider:     o.Provider,
		Metrics:      o.Metrics,
		Observations: o.Observations,
		Injuries:     o.Injuries,
		Build:        o.Build,
	}
	runner := NewRunner(deps, o.Notifier, o.Store)
	reg := buildRegistry(runner, clock)

	return flynode.Boot(ctx, flynode.BootConfig{
		Pkg:      "workout",
		Queue:    queueName,
		DBPath:   dbPath,
		Clock:    clock,
		Store:    o.Store,
		Registry: reg,
		Reconcile: func(ctx context.Context, db *gorm.DB) error {
			return upsertPeriodic(ctx, db, o.Config.SlotTime)
		},
	})
}

// buildRegistry registers the daily worker over one runner.
func buildRegistry(r *Runner, clock models.Clock) *flywheel.Registry {
	reg := flywheel.NewRegistry()
	flywheel.Register(reg, dailyWorker{r: r, clock: clock})
	return reg
}

// upsertPeriodic reconciles the daily periodic from the configured slot time
// (WorkoutConfig.SlotTime, default midday). It is idempotent by slug, safe to
// call on every boot; a malformed slot is rejected here rather than silently
// mis-scheduled.
func upsertPeriodic(ctx context.Context, db *gorm.DB, slot string) error {
	cron, err := cronFromHM(slotOrDefault(slot))
	if err != nil {
		return err
	}
	if err := flywheel.UpsertPeriodic(ctx, db, flywheel.PeriodicSpec{
		Slug: slugDaily, Kind: kindDaily, Cron: cron, Queue: queueName, Active: true,
	}); err != nil {
		return fmt.Errorf("workout: upsert daily periodic: %w", err)
	}
	return nil
}

// slotOrDefault returns the configured slot time, or the midday default when it
// is empty — the single place the default midday fire time is applied.
func slotOrDefault(slot string) string {
	if strings.TrimSpace(slot) == "" {
		return defaultSlot
	}
	return slot
}

// atClock builds the instant at the "HH:MM" wall-clock mark on now's calendar
// date, in now's location — the reference the missed-fire window compares
// against. A malformed mark is rejected rather than silently mis-scheduled.
func atClock(now time.Time, hm string) (time.Time, error) {
	h, m, err := parseHM(hm)
	if err != nil {
		return time.Time{}, err
	}
	return time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location()), nil
}

// cronFromHM turns an "HH:MM" slot mark into a daily 5-field cron expression:
// "12:00" -> "0 12 * * *". A malformed mark is rejected rather than silently
// mis-scheduled.
func cronFromHM(hm string) (string, error) {
	mark, err := clockmark.Parse(hm)
	if err != nil {
		return "", fmt.Errorf("workout: invalid slot time: %w", err)
	}
	return mark.Cron(), nil
}

// parseHM parses an "HH:MM" clock mark into its hour and minute, rejecting a
// wrong shape, an out-of-range field, or a non-numeric field.
func parseHM(hm string) (hour, minute int, err error) {
	mark, err := clockmark.Parse(hm)
	if err != nil {
		return 0, 0, fmt.Errorf("workout: invalid slot time: %w", err)
	}
	return mark.Hour(), mark.Minute(), nil
}

// DefaultDBPath resolves the disposable workout job-DB path: an explicit
// override wins, then the LUCID_WORKOUT_DB env override, then the default
// workout.db under the OS user-config dir (outside the ~/.lucid Ledger, and
// distinct from the Engine's flywheel.db and the companion's companion.db). It is
// exported so a read-only inspector resolves the exact same path the daemon
// writes to.
func DefaultDBPath(dbPath string) (string, error) {
	return flynode.ResolveDBPath(dbPath, envWorkoutDB, "workout.db")
}
