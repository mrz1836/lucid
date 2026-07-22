package witnessreport

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
	"github.com/mrz1836/lucid/internal/isoweek"
	"github.com/mrz1836/lucid/internal/notify"
	"github.com/mrz1836/lucid/internal/storage"
)

// Job runtime identifiers. The weekly report runs its own disposable flywheel
// node beside the teeth's and the companion's, so its queue, kind, and slug are
// distinct: the single weekly periodic never collides with the daily pairs, and
// a restart reconciles it by slug without duplicating.
const (
	queueName  = "lucid-witness-report"
	kindWeekly = "lucid_witness_report_weekly"
	slugWeekly = "lucid-witness-report-weekly"

	// backfillCap fires only the single most-recent missed bucket on a supervised
	// restart — the same bounded catch-up the teeth and companion use — so a host
	// asleep across the Monday fire wakes to one late attempt, not a replay of
	// every missed week.
	backfillCap = 1

	// dbDirPerm is the mode for the disposable job-DB parent directory.
	dbDirPerm = 0o755

	// envWitnessReportDB overrides the job-DB path when --db is not passed. Like
	// the teeth's flywheel.db and the companion's companion.db it is disposable
	// machinery, not Ledger truth, so it lives outside ~/.lucid by default.
	envWitnessReportDB = "LUCID_WITNESS_REPORT_DB"

	// staleAfter is the missed-fire cut-off: a weekly report not delivered within
	// this long after its scheduled mark is stale — the host was down across the
	// fire long enough that a report now would describe a week already days gone.
	// Past it the node skips the send and alerts instead. With the default Monday
	// 09:00 mark, a report still un-fired by Wednesday morning (mark + 48h) is
	// stale. It is a worker constant, not a config key, exactly like the
	// companion's per-window cut-offs.
	staleAfter = 48 * time.Hour
)

var (
	errNoStore    = errors.New("witnessreport: store is required")
	errNoNotifier = errors.New("witnessreport: notifier is required")
	errNoNumbers  = errors.New("witnessreport: numbers reader is required")
	errNoRecords  = errors.New("witnessreport: records reader is required")
)

// ReportComposer composes one week's witness Report. *Composer satisfies it; the
// node tests inject a scripted fake so the missed-fire, idempotency, and alert
// logic is exercised without a live provider.
type ReportComposer interface {
	Compose(ctx context.Context, now time.Time) (Report, error)
}

// Deliverer is the notifier surface the weekly report delivers through. The rich
// embed send returns the created id, the read-back verify gives both the "a real
// Discord id reappears in the channel" proof and the idempotency the receipt keys
// on, and the plain Send carries the loud alert when a fire cannot deliver its
// real report. *notify.Discord satisfies all three.
type Deliverer interface {
	SendEmbedReturningID(channel string, e notify.Embed) (string, error)
	VerifyPresent(channel, messageID string) error
	Send(channel, text string) error
}

// Runner delivers one composed weekly report with the reliability guarantees
// layered on top of [Composer]: a bounded missed-fire cut-off, delivery
// idempotency + read-back through a per-ISO-week persisted receipt, and a loud
// alert on any total miss — never a silent empty send. The weekly node and the
// `lucid witness report --deliver` verb take the same delivery shape (the CLI
// keeps the minimal path; the node adds idempotency and the cut-off).
type Runner struct {
	compose ReportComposer
	deliver Deliverer
	store   *storage.Adapter
	mode    string // preview → user channel, auto → witness channel
	weekday int    // cron weekday of the scheduled mark (0=Sunday..6=Saturday)
	markHM  string // "HH:MM" local scheduled mark
}

// NewRunner wires a Runner over the compose dependencies (from the witness_report
// config block + provider), the delivery transport, and the Ledger store that
// holds the per-week receipt.
func NewRunner(o Options) *Runner {
	deps := Deps{
		SystemPrompt: o.Config.SystemPrompt,
		Template:     o.Config.Template,
		AsksFile:     o.Config.AsksFile,
		Model:        o.Config.Model,
		Provider:     o.Provider,
		Numbers:      o.Numbers,
		Records:      o.Records,
		Build:        o.Build,
	}
	return &Runner{
		compose: New(deps),
		deliver: o.Notifier,
		store:   o.Store,
		mode:    o.Config.Mode,
		weekday: o.Config.Weekday,
		markHM:  o.Config.Time,
	}
}

// Outcome records how one fire resolved so the node and the tests can report it.
// A fire is exactly one of: Delivered (with a MessageID and Channel), or Skipped
// (SkipReason "already-delivered" for the idempotent path, "past-cutoff" for the
// stale-week refusal). Fallback records the deterministic path fired (only the
// warmth was lost); SafetyTripped records the witness-safe scan discarded the
// model prose (the metrics-only report still delivered, and the operator was
// alerted to review).
type Outcome struct {
	Week          string
	Delivered     bool
	MessageID     string
	Channel       string
	Fallback      bool
	SafetyTripped bool
	Skipped       bool
	SkipReason    string
}

// The two skip reasons a fire can report.
const (
	skipPastCutoff       = "past-cutoff"
	skipAlreadyDelivered = "already-delivered"
)

// Fire composes and delivers this week's report at `now`, honoring the whole
// reliability contract in order: refuse a stale send past the cut-off (alert
// instead), skip an already-delivered week idempotently (a verified receipt that
// still reads back), compose (the deterministic report always lands — only the
// warmth can be lost), deliver the embed with read-back, and persist the
// per-week receipt. A tripped witness-safe scan still delivers the metrics-only
// report but alerts the operator to review. Any total send failure fires a
// best-effort alert and returns a loud error — silence is the one outcome it
// never produces.
func (r *Runner) Fire(ctx context.Context, now time.Time) (Outcome, error) {
	channel, err := channelForMode(r.mode)
	if err != nil {
		return Outcome{}, err
	}
	week := isoweek.Label(now)

	markAt, err := r.markFor(now)
	if err != nil {
		return Outcome{}, err
	}

	// Past the stale cut-off: the host was down across the fire long enough that a
	// report now would describe a week already gone. Refuse it and alert instead
	// of posting a stale card — but never fall silent.
	if !now.Before(markAt.Add(staleAfter)) {
		r.alert(fmt.Sprintf(
			"Lucid weekly witness report skipped for %s — it is past the missed-fire cut-off (the host was asleep at the scheduled mark). No stale report was posted.",
			week,
		))
		return Outcome{Week: week, Skipped: true, SkipReason: skipPastCutoff}, nil
	}

	// Idempotency: a receipt for this ISO week whose message still reads back means
	// a prior fire already delivered — skip rather than double-post on a supervisor
	// retry. A receipt whose message is gone falls through to a fresh delivery so
	// the week is never left silently empty.
	rec, ok, err := r.store.ReadWitnessReportReceipt()
	if err != nil {
		return Outcome{}, fmt.Errorf("witnessreport: read receipt: %w", err)
	}
	if ok && rec.Week == week && rec.MessageID != "" {
		if verr := r.deliver.VerifyPresent(rec.ChannelID, rec.MessageID); verr == nil {
			return Outcome{Week: week, Skipped: true, SkipReason: skipAlreadyDelivered, MessageID: rec.MessageID, Channel: rec.ChannelID}, nil
		}
	}

	// Compose the report. The deterministic scaffold always lands — a provider
	// timeout or a tripped witness-safe scan degrades to the metrics-only report
	// (Fallback/SafetyTripped), never an error. A loud compose failure (a missing
	// prompt file, an unreadable projection) is never a silent empty send.
	report, err := r.compose.Compose(ctx, now)
	if err != nil {
		r.alert("Lucid weekly witness report could not be composed — the scheduled report did not go out.")
		return Outcome{}, fmt.Errorf("witnessreport: compose: %w", err)
	}

	id, err := r.deliver.SendEmbedReturningID(channel, RenderEmbed(report))
	if err != nil {
		r.alert("Lucid weekly witness report failed to deliver — the scheduled report did not go out.")
		return Outcome{}, fmt.Errorf("witnessreport: deliver: %w", err)
	}
	if err := r.deliver.VerifyPresent(channel, id); err != nil {
		r.alert("Lucid weekly witness report sent a report that could not be verified in the channel.")
		return Outcome{}, fmt.Errorf("witnessreport: verify delivery: %w", err)
	}

	if werr := r.store.WriteWitnessReportReceipt(storage.WitnessReportReceipt{
		Week:        week,
		MessageID:   id,
		ChannelID:   channel,
		Verified:    true,
		DeliveredAt: now.Format(time.RFC3339),
	}); werr != nil {
		return Outcome{}, fmt.Errorf("witnessreport: write receipt: %w", werr)
	}

	// The report delivered whole, but a tripped witness-safe scan means the model
	// produced flagged prose that was discarded — alert so the operator can review
	// why. This never blocks or unwinds the successful send.
	if report.SafetyTripped {
		r.alert(fmt.Sprintf(
			"Lucid weekly witness report for %s tripped the witness-safe scan — the model prose was discarded and the metrics-only report was delivered. Worth a review.",
			week,
		))
	}

	return Outcome{
		Week: week, Delivered: true, MessageID: id, Channel: channel,
		Fallback: report.Fallback, SafetyTripped: report.SafetyTripped,
	}, nil
}

// alert posts a best-effort loud ping to the user channel when a weekly report
// cannot deliver its real report (or tripped the witness-safe scan) — the "never
// silent" floor. It is deliberately best-effort: if the channel itself is
// unreachable the returned error from [Runner.Fire] is the loud signal (it fails
// the job and lands in the supervised daemon log), and a failed alert must not
// mask that original error.
func (r *Runner) alert(text string) {
	_ = r.deliver.Send(engine.ChannelUser, text)
}

// markFor builds the scheduled-mark instant for the ISO week containing `now`:
// the configured weekday of that week at the configured "HH:MM", in now's
// location — the reference the missed-fire cut-off compares against. It anchors
// on the week's Monday (isoweek.Bounds) and offsets to the cron weekday, so the
// mark is deterministic regardless of which day a (possibly backfilled) fire
// actually runs. A malformed mark is rejected rather than silently
// mis-scheduled.
func (r *Runner) markFor(now time.Time) (time.Time, error) {
	h, m, err := parseHM(r.markHM)
	if err != nil {
		return time.Time{}, err
	}
	loc := now.Location()
	if loc == nil {
		loc = time.UTC
	}
	start, _ := isoweek.Bounds(now) // Monday 00:00 of now's ISO week, in now's loc
	// cron weekday runs 0=Sunday..6=Saturday; the ISO week starts on Monday, so
	// offset days from Monday to the configured weekday (Mon→0 … Sun→6).
	offset := (r.weekday + 6) % 7
	markDay := start.AddDate(0, 0, offset)
	return time.Date(markDay.Year(), markDay.Month(), markDay.Day(), h, m, 0, 0, loc), nil
}

// channelForMode resolves the delivery mode to a logical channel: preview posts
// to the operator's own user channel (the safe default during the trust period),
// auto posts to the friend-facing witness channel. An unknown mode is a hard
// error — config validation already rejects one on an enabled report, so this
// only guards a hand-built Runner — rather than a mis-send to the wrong audience.
func channelForMode(mode string) (string, error) {
	switch mode {
	case config.WitnessReportModePreview:
		return engine.ChannelUser, nil
	case config.WitnessReportModeAuto:
		return engine.ChannelWitness, nil
	default:
		return "", fmt.Errorf("witnessreport: unknown mode %q — use %q|%q",
			mode, config.WitnessReportModePreview, config.WitnessReportModeAuto)
	}
}

// Options configures a weekly witness-report daemon run. Store, Notifier,
// Numbers, and Records are required; DBPath and Clock default when zero (the
// disposable job DB outside ~/.lucid, and the real clock). Config selects the
// mode, the weekly mark, and the operator's opaque prompt files; Provider selects
// the compose backend; Build overrides the provider builder for tests.
type Options struct {
	Store    *storage.Adapter
	Config   config.WitnessReportConfig
	Provider config.ProviderConfig
	Numbers  NumbersReader
	Records  RecordsReader
	Notifier Deliverer
	DBPath   string
	Clock    models.Clock
	Build    ProviderBuilder
}

// weeklyArgs is the empty typed payload for the periodic: it carries no input —
// the report is a pure function of the Ledger + prompt files at fire time. Its
// JSON form is the empty object the scheduler enqueues.
type weeklyArgs struct{}

// weeklyWorker fires the weekly witness report.
type weeklyWorker struct {
	r     *Runner
	clock models.Clock
}

// Kind names the worker dispatched for kindWeekly.
func (weeklyWorker) Kind() string { return kindWeekly }

// Work composes and delivers the weekly report for `now`.
func (w weeklyWorker) Work(ctx context.Context, _ *flywheel.Job[weeklyArgs]) (flywheel.Result, error) {
	if _, err := w.r.Fire(ctx, w.clock.Now(ctx)); err != nil {
		return flywheel.Result{}, err
	}
	return flywheel.Result{}, nil
}

// Run opens the disposable job DB, migrates it, registers the weekly worker,
// reconciles the single Monday-morning periodic from the config Time/Weekday, and
// runs the flywheel node until ctx is canceled (SIGINT/SIGTERM upstream). It
// returns nil on a clean drain. It runs beside the teeth and companion daemons in
// the same process (config-gated), so it is up whenever the scheduler is.
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
	if o.Records == nil {
		return errNoRecords
	}

	// One clock drives both the flywheel machinery (via the ctx) and the worker's
	// reference instant, so a test's fixed clock moves them together and production
	// inherits the real wall clock.
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
		return fmt.Errorf("witnessreport: create job db dir: %w", mkErr)
	}

	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		return fmt.Errorf("witnessreport: open job db %q: %w", dbPath, err)
	}
	if err = flywheel.Migrate(db); err != nil {
		return fmt.Errorf("witnessreport: migrate job db: %w", err)
	}
	if err = o.Store.ScaffoldEngine(); err != nil {
		return fmt.Errorf("witnessreport: scaffold engine: %w", err)
	}
	if err = upsertWeeklyPeriodic(ctx, db, o.Config); err != nil {
		return err
	}

	runner := NewRunner(o)
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
		return fmt.Errorf("witnessreport: build node: %w", err)
	}
	return node.Run(ctx)
}

// buildRegistry registers the single weekly worker over one runner.
func buildRegistry(r *Runner, clock models.Clock) *flywheel.Registry {
	reg := flywheel.NewRegistry()
	flywheel.Register(reg, weeklyWorker{r: r, clock: clock})
	return reg
}

// upsertWeeklyPeriodic reconciles the one weekly periodic from the config
// Time/Weekday — the Monday-morning mark landing after Sunday's reflection
// window. It is idempotent by slug, safe to call on every boot.
func upsertWeeklyPeriodic(ctx context.Context, db *gorm.DB, cfg config.WitnessReportConfig) error {
	cron, err := weeklyCron(cfg.Time, cfg.Weekday)
	if err != nil {
		return err
	}
	if err := flywheel.UpsertPeriodic(ctx, db, flywheel.PeriodicSpec{
		Slug: slugWeekly, Kind: kindWeekly, Cron: cron, Queue: queueName, Active: true,
	}); err != nil {
		return fmt.Errorf("witnessreport: upsert weekly periodic: %w", err)
	}
	return nil
}

// weeklyCron turns an "HH:MM" mark and a cron weekday (0=Sunday..6=Saturday) into
// a weekly 5-field cron expression: ("09:00", 1) -> "0 9 * * 1". A malformed mark
// or an out-of-range weekday is rejected rather than silently mis-scheduled.
func weeklyCron(hm string, weekday int) (string, error) {
	h, m, err := parseHM(hm)
	if err != nil {
		return "", err
	}
	if weekday < 0 || weekday > 6 {
		return "", fmt.Errorf("witnessreport: invalid cron weekday %d: want 0..6", weekday)
	}
	return fmt.Sprintf("%d %d * * %d", m, h, weekday), nil
}

// parseHM parses an "HH:MM" clock mark into its hour and minute, rejecting a
// wrong shape, an out-of-range field, or a non-numeric field.
func parseHM(hm string) (hour, minute int, err error) {
	parts := strings.Split(hm, ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("witnessreport: malformed clock mark %q: want HH:MM", hm)
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return 0, 0, fmt.Errorf("witnessreport: invalid hour in clock mark %q", hm)
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("witnessreport: invalid minute in clock mark %q", hm)
	}
	return h, m, nil
}

// DefaultDBPath resolves the disposable weekly-report job-DB path: an explicit
// override wins, then the LUCID_WITNESS_REPORT_DB env override, then the default
// witness-report.db under the OS user-config dir (outside the ~/.lucid Ledger,
// and distinct from the teeth's flywheel.db and the companion's companion.db). It
// is exported so a read-only inspector resolves the exact same path the daemon
// writes to.
func DefaultDBPath(dbPath string) (string, error) {
	if dbPath != "" {
		return dbPath, nil
	}
	if env := os.Getenv(envWitnessReportDB); env != "" {
		return env, nil
	}
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("witnessreport: resolve user config dir: %w", err)
	}
	return filepath.Join(cfgDir, "lucid", "witness-report.db"), nil
}
