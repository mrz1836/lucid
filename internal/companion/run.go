package companion

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	flywheel "github.com/mrz1836/go-flywheel"
	"github.com/mrz1836/go-foundation/models"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/scheduler"
	"github.com/mrz1836/lucid/internal/storage"
)

// Job runtime identifiers. The companion runs its own disposable flywheel node
// beside the teeth's, so its queue, kinds, and slugs are distinct: the two
// periodics never collide with the bell/tripwire pair, and a restart reconciles
// them by slug without duplicating.
const (
	queueName   = "lucid-companion"
	kindMorning = "lucid_companion_morning"
	kindNight   = "lucid_companion_night"
	slugMorning = "lucid-companion-morning"
	slugNight   = "lucid-companion-night"

	// backfillCap fires only the single most-recent missed bucket on a
	// supervised restart — the same bounded catch-up the teeth use — so a host
	// asleep across the fire time wakes to one late attempt, not a replay.
	backfillCap = 1

	// dbDirPerm is the mode for the disposable job-DB parent directory.
	dbDirPerm = 0o755

	// envCompanionDB overrides the job-DB path when --db is not passed. Like the
	// teeth's flywheel.db it is disposable machinery, not Ledger truth, so it
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
	SendReturningID(channel, text string) (string, error)
	VerifyPresent(channel, messageID string) error
	Send(channel, text string) error
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

	// Past the cut-off: the host was down long enough that a message now would
	// read as stale. Refuse it and alert instead of posting a confusing
	// hours-late "morning" — but never fall silent.
	if !now.Before(cutoffAt) {
		r.alert(fmt.Sprintf(
			"Lucid %s companion skipped — it is past the %s cut-off (the host was asleep at the fire time). No stale message was posted.",
			mode, win.cutoff,
		))
		return Outcome{Mode: mode, Skipped: true, SkipReason: skipPastCutoff}, nil
	}

	// Idempotency: a receipt for this date + window whose message still reads
	// back means a prior fire already delivered — skip rather than double-post on
	// a supervisor retry. A receipt whose message is gone falls through to a
	// fresh delivery so the window is never left silently empty.
	date := engine.DateString(engine.DateOf(now))
	rec, ok, err := r.store.ReadCompanionReceipt(string(mode))
	if err != nil {
		return Outcome{}, fmt.Errorf("companion: read receipt: %w", err)
	}
	if ok && rec.Date == date && rec.MessageID != "" {
		if verr := r.deliver.VerifyPresent(engine.ChannelUser, rec.MessageID); verr == nil {
			return Outcome{Mode: mode, Skipped: true, SkipReason: skipAlreadyDelivered, MessageID: rec.MessageID}, nil
		}
	}

	late := now.After(markAt.Add(lateGrace))

	res, err := r.compose.Compose(ctx, mode, now)
	if err != nil {
		// A loud compose failure (a missing prompt file, an unreadable verdict, a
		// non-transport provider error) is never a silent empty send.
		r.alert(fmt.Sprintf("Lucid %s companion could not compose a message — the scheduled send did not go out.", mode))
		return Outcome{}, err
	}

	text := res.Text
	if late {
		text = lateNote + "\n\n" + text
	}

	id, err := r.deliver.SendReturningID(engine.ChannelUser, text)
	if err != nil {
		r.alert(fmt.Sprintf("Lucid %s companion failed to deliver — the scheduled send did not go out.", mode))
		return Outcome{}, fmt.Errorf("companion: deliver %s: %w", mode, err)
	}
	if err := r.deliver.VerifyPresent(engine.ChannelUser, id); err != nil {
		r.alert(fmt.Sprintf("Lucid %s companion sent a message that could not be verified in the channel.", mode))
		return Outcome{}, fmt.Errorf("companion: verify %s delivery: %w", mode, err)
	}

	if werr := r.store.WriteCompanionReceipt(storage.CompanionReceipt{
		Date:        date,
		Window:      string(mode),
		MessageID:   id,
		ChannelID:   engine.ChannelUser,
		Verified:    true,
		DeliveredAt: now.Format(time.RFC3339),
	}); werr != nil {
		return Outcome{}, fmt.Errorf("companion: write receipt: %w", werr)
	}

	return Outcome{Mode: mode, Delivered: true, MessageID: id, Late: late, Fallback: res.Fallback, Text: text}, nil
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
func (r *Runner) alert(text string) {
	_ = r.deliver.Send(engine.ChannelUser, text)
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

// Run opens the disposable job DB, migrates it, registers the morning and night
// workers, reconciles the two periodics from the chain's bell/tripwire marks,
// and runs the flywheel node until ctx is canceled (SIGINT/SIGTERM upstream). It
// returns nil on a clean drain. It runs beside the teeth daemon in the same
// process (config-gated), so it is up whenever the scheduler is.
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

	// One clock drives both the flywheel machinery (via the ctx) and each
	// worker's reference instant, so a test's fixed clock moves them together and
	// production inherits the real wall clock.
	clock := o.Clock
	if clock == nil {
		clock = models.ClockFrom(ctx)
	}
	ctx = models.WithClock(ctx, clock)

	dbPath, err := DefaultDBPath(o.DBPath)
	if err != nil {
		return err
	}
	if mkErr := os.MkdirAll(filepath.Dir(dbPath), dbDirPerm); mkErr != nil {
		return fmt.Errorf("companion: create job db dir: %w", mkErr)
	}

	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		return fmt.Errorf("companion: open job db %q: %w", dbPath, err)
	}
	if err = flywheel.Migrate(db); err != nil {
		return fmt.Errorf("companion: migrate job db: %w", err)
	}
	if err = o.Store.ScaffoldEngine(); err != nil {
		return fmt.Errorf("companion: scaffold engine: %w", err)
	}
	if err = upsertPeriodics(ctx, db, o.Store); err != nil {
		return err
	}

	deps := Deps{Companion: o.Config, Provider: o.Provider, Numbers: o.Numbers, Verdict: o.Verdict, Chain: o.Store, Build: o.Build}
	runner := NewRunner(deps, o.Notifier, o.Store)
	reg := buildRegistry(runner, clock)

	node, err := flywheel.NewNode(flywheel.NodeConfig{
		Runners: []flywheel.RunnerConfig{{
			DB:       db,
			Driver:   flywheel.NewSQLiteDriver(db),
			Registry: reg,
			Queues:   []string{queueName},
			// SQLite is single-writer: one runner claiming every class.
			ClaimAnyClass: true,
			Concurrency:   1,
		}},
		Scheduler: &flywheel.SchedulerConfig{
			DB:          db,
			Client:      flywheel.NewClient(db),
			BackfillCap: backfillCap,
		},
	})
	if err != nil {
		return fmt.Errorf("companion: build node: %w", err)
	}
	return node.Run(ctx)
}

// buildRegistry registers the morning and night workers over one runner.
func buildRegistry(r *Runner, clock models.Clock) *flywheel.Registry {
	reg := flywheel.NewRegistry()
	flywheel.Register(reg, morningWorker{r: r, clock: clock})
	flywheel.Register(reg, nightWorker{r: r, clock: clock})
	return reg
}

// upsertPeriodics reconciles the morning and night periodics from the default
// profile's clock marks (chain.json): the morning fires on the tripwire mark and
// the night on the bell mark, so the companion and the deterministic pair fire
// at the same instant. It is idempotent by slug, safe to call on every boot.
func upsertPeriodics(ctx context.Context, db *gorm.DB, store *storage.Adapter) error {
	chain, err := store.ReadChainConfig()
	if err != nil {
		return fmt.Errorf("companion: read chain config: %w", err)
	}
	bellMark, tripwireMark, err := scheduler.Marks(chain, engine.DefaultProfile)
	if err != nil {
		return fmt.Errorf("companion: resolve clock marks: %w", err)
	}
	morningCron, err := cronFromHM(tripwireMark)
	if err != nil {
		return err
	}
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
	return nil
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
	h, m, err := parseHM(hm)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d %d * * *", m, h), nil
}

// parseHM parses an "HH:MM" clock mark into its hour and minute, rejecting a
// wrong shape, an out-of-range field, or a non-numeric field.
func parseHM(hm string) (hour, minute int, err error) {
	parts := strings.Split(hm, ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("companion: malformed clock mark %q: want HH:MM", hm)
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return 0, 0, fmt.Errorf("companion: invalid hour in clock mark %q", hm)
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("companion: invalid minute in clock mark %q", hm)
	}
	return h, m, nil
}

// DefaultDBPath resolves the disposable companion job-DB path: an explicit
// override wins, then the LUCID_COMPANION_DB env override, then the default
// companion.db under the OS user-config dir (outside the ~/.lucid Ledger, and
// distinct from the teeth's flywheel.db). It is exported so a read-only inspector
// resolves the exact same path the daemon writes to.
func DefaultDBPath(dbPath string) (string, error) {
	if dbPath != "" {
		return dbPath, nil
	}
	if env := os.Getenv(envCompanionDB); env != "" {
		return env, nil
	}
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("companion: resolve user config dir: %w", err)
	}
	return filepath.Join(cfgDir, "lucid", "companion.db"), nil
}
