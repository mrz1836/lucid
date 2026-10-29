package contactweekly

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	flywheel "github.com/mrz1836/go-flywheel"
	"github.com/mrz1836/go-foundation/models"
	"gorm.io/gorm"

	"github.com/mrz1836/lucid/internal/clockmark"
	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/contact"
	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/flynode"
	"github.com/mrz1836/lucid/internal/isoweek"
	"github.com/mrz1836/lucid/internal/storage"
)

// Job runtime identifiers. The Monday post runs its own disposable flywheel
// node beside the Engine's and the other nodes', so its queue, kind, and slug
// are distinct: the single weekly periodic never collides with theirs, and a
// restart reconciles it by slug without duplicating.
const (
	queueName  = "lucid-contact-weekly"
	kindWeekly = "lucid_contact_weekly"
	slugWeekly = "lucid-contact-weekly"

	// backfillCap is the bounded missed-fire catch-up the flynode boot spine
	// applies (a single most-recent bucket — the one late attempt). It is
	// mirrored here only so the in-process test rig builds its scheduler with
	// the same cap the daemon runs.
	backfillCap = 1

	// envContactWeeklyDB overrides the job-DB path when no explicit path is
	// passed. Like the other nodes' job DBs it is disposable machinery, not
	// Ledger truth, so it lives outside ~/.lucid by default.
	envContactWeeklyDB = "LUCID_CONTACT_WEEKLY_DB"

	// staleAfter is the missed-fire cut-off: a post not delivered within this
	// long after its scheduled mark is skipped, with an alert, rather than
	// posting a list for a week already days gone. With the default Monday 09:00
	// mark a post still un-fired by Wednesday morning is stale. It is a worker
	// constant, not a config key, exactly like the witness report's.
	staleAfter = 48 * time.Hour
)

// SkipNothingDue is the skip reason of a week with nothing to post: both lists
// and the birthdays line are empty. It sits beside flynode's
// [flynode.SkipPastCutoff] and [flynode.SkipAlreadyDelivered].
const SkipNothingDue = "nothing-due"

var (
	errNoStore    = errors.New("contactweekly: store is required")
	errNoNotifier = errors.New("contactweekly: notifier is required")
	errNoDue      = errors.New("contactweekly: due reader is required")
)

// The alerts, all to the user channel, in plain and calm words. They name what
// happened and point at the read view; none says how long anyone has gone
// without contact.
const (
	alertCutoff  = "Lucid skipped the Monday check-in list for %s — the host was asleep past its posting window, so it was not posted days after the fact. `lucid contact due` shows this week's list any time."
	alertCompose = "Lucid could not read the check-in lists, so the Monday post did not go out. `lucid contact due` shows the list any time."
	alertSend    = "Lucid could not deliver the Monday check-in list — the post did not go out. `lucid contact due` shows the list any time."
	alertVerify  = "Lucid sent the Monday check-in list but could not confirm it is in the channel."
	alertGuard   = "Lucid could not confirm the earlier Monday check-in list is still posted — not re-sending to avoid a duplicate; it will try again."
	alertReceipt = "Lucid delivered the Monday check-in list but could not record the receipt — a retry may post it again; check the channel."
)

// DueReader reads the due view for a moment. *router.Router satisfies it — the
// same seam `lucid contact due` reads — so the post and the read view share one
// grouping, one fold, and one off-limits rule.
type DueReader interface {
	ContactDue(now time.Time, wrap bool) (contact.DueView, error)
}

// Deliverer is the notifier surface the post delivers through: the plain-text
// send returns the created id, the read-back verify gives both the "a real
// Discord id reappears in the channel" proof and the idempotency the receipt
// keys on, and the plain Send carries the alert when a fire cannot deliver.
// *notify.Discord satisfies all three.
type Deliverer interface {
	SendReturningID(ctx context.Context, channel, text string) (string, error)
	VerifyPresent(ctx context.Context, channel, messageID string) error
	Send(ctx context.Context, channel, text string) error
}

// Runner renders and delivers one week's post with the reliability guarantees
// the witness report carries: a bounded missed-fire cut-off, idempotency and
// read-back through a per-ISO-week receipt, and an alert on any total miss.
type Runner struct {
	due     DueReader
	deliver Deliverer
	store   *storage.Adapter
	weekday int    // cron weekday of the scheduled mark (0=Sunday..6=Saturday)
	markHM  string // "HH:MM" local scheduled mark
	// log receives the runner's own lines (the nothing-due note, a failed
	// alert). It is nil in production — [Runner.logger] then uses
	// slog.Default(), which lands in the supervised daemon log — and injected by
	// tests to capture the lines.
	log *slog.Logger
}

// NewRunner wires a Runner over the due reader, the delivery transport, and
// the Ledger store that holds the per-week receipt, on the configured mark.
func NewRunner(o Options) *Runner {
	return &Runner{
		due:     o.Due,
		deliver: o.Notifier,
		store:   o.Store,
		weekday: o.Config.Weekday,
		markHM:  o.Config.Time,
	}
}

// Outcome records how one fire resolved. A fire is exactly one of: Delivered
// (with a MessageID, the Channel, and the posted Text), or Skipped (SkipReason
// [SkipNothingDue] for an empty week, [flynode.SkipAlreadyDelivered] for the
// idempotent path, [flynode.SkipPastCutoff] for a stale week).
type Outcome struct {
	Week       string
	Delivered  bool
	MessageID  string
	Channel    string
	Text       string
	Skipped    bool
	SkipReason string
}

// Fire renders and delivers this week's post at now. It reads the due view
// first and renders the fixed template; a week with nothing to post returns a
// [SkipNothingDue] skip before the send path — no send, no alert. Otherwise it
// runs [flynode.Fire] to the user channel: refuse a stale send past the
// cut-off (alert instead), skip a week whose receipt still reads back, send,
// read back, and persist the receipt. A due view that cannot be read alerts and
// returns the error — never a silent miss.
func (r *Runner) Fire(ctx context.Context, now time.Time) (Outcome, error) {
	week := isoweek.Label(now)
	markAt, err := r.markFor(now)
	if err != nil {
		return Outcome{}, err
	}

	view, err := r.due.ContactDue(now, false)
	if err != nil {
		r.alert(ctx, alertCompose)
		return Outcome{}, fmt.Errorf("contactweekly: read due view: %w", err)
	}
	text, ok := contact.RenderMondayPost(view)
	if !ok {
		return Outcome{Week: week, Skipped: true, SkipReason: SkipNothingDue}, nil
	}

	fired, err := flynode.Fire(ctx, now, flynode.Delivery[string]{
		Channel:  engine.ChannelUser,
		CutoffAt: markAt.Add(staleAfter),
		Guard: func(_ context.Context, _ time.Time) (flynode.Receipt, error) {
			rec, found, rerr := r.store.ReadContactWeeklyReceipt()
			if rerr != nil {
				return flynode.Receipt{}, fmt.Errorf("contactweekly: read receipt: %w", rerr)
			}
			if found && rec.Week == week && rec.MessageID != "" {
				return flynode.Receipt{MessageID: rec.MessageID, Channel: engine.ChannelUser, Found: true}, nil
			}
			return flynode.Receipt{}, nil
		},
		Compose: func(context.Context, time.Time) (string, error) {
			return text, nil
		},
		Send: func(sctx context.Context, channel, post string) (string, error) {
			id, serr := r.deliver.SendReturningID(sctx, channel, post)
			if serr != nil {
				return "", fmt.Errorf("contactweekly: deliver: %w", serr)
			}
			return id, nil
		},
		Verify: func(vctx context.Context, channel, id string) error {
			if verr := r.deliver.VerifyPresent(vctx, channel, id); verr != nil {
				return fmt.Errorf("contactweekly: verify delivery: %w", verr)
			}
			return nil
		},
		WriteReceipt: func(wnow time.Time, channel, id string) error {
			if werr := r.store.WriteContactWeeklyReceipt(storage.ContactWeeklyReceipt{
				Week:        week,
				MessageID:   id,
				ChannelID:   channel,
				Verified:    true,
				DeliveredAt: wnow.Format(time.RFC3339),
			}); werr != nil {
				return fmt.Errorf("contactweekly: write receipt: %w", werr)
			}
			return nil
		},
		Alert:        func(msg string) { r.alert(ctx, msg) },
		CutoffAlert:  fmt.Sprintf(alertCutoff, week),
		ComposeAlert: alertCompose,
		SendAlert:    alertSend,
		VerifyAlert:  alertVerify,
		GuardAlert:   alertGuard,
		ReceiptAlert: alertReceipt,
	})
	if err != nil {
		return Outcome{}, err
	}

	out := Outcome{
		Week:       week,
		Delivered:  fired.Delivered,
		MessageID:  fired.MessageID,
		Channel:    fired.Channel,
		Skipped:    fired.Skipped,
		SkipReason: fired.SkipReason,
	}
	if fired.Delivered {
		out.Text = fired.Payload
	}
	return out, nil
}

// alertTimeout bounds a detached best-effort alert: above the notifier's own
// ~20s HTTP budget, while still bounding how long a wedged channel can delay a
// shutdown.
const alertTimeout = 30 * time.Second

// alert posts a best-effort ping to the user channel — never any other. A stop
// signal mid-send cancels the Fire ctx, so the alert runs on a context detached
// from that cancellation with its own deadline. A failed alert is logged at
// warn and never masks the error the caller returns.
func (r *Runner) alert(ctx context.Context, text string) {
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), alertTimeout)
	defer cancel()
	if err := r.deliver.Send(actx, engine.ChannelUser, text); err != nil {
		r.logger().WarnContext(ctx, "alert send failed", "daemon", "contact-weekly", "error", err)
	}
}

// logger returns the runner's injected logger, or slog.Default() when none was
// set (production).
func (r *Runner) logger() *slog.Logger {
	if r.log != nil {
		return r.log
	}
	return slog.Default()
}

// markFor builds the scheduled-mark instant for the ISO week containing now:
// the configured weekday of that week at the configured "HH:MM", in now's
// location — the reference the missed-fire cut-off compares against. It anchors
// on the week's Monday and offsets to the cron weekday, so the mark is the same
// whichever day a (possibly backfilled) fire runs. A malformed mark is
// rejected rather than silently mis-scheduled.
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

// Options configures a Monday-post daemon run. Store, Notifier, and Due are
// required; DBPath and Clock default when zero (the disposable job DB outside
// ~/.lucid, and the real clock). Config carries the weekly mark.
type Options struct {
	Store    *storage.Adapter
	Config   config.ContactWeeklyConfig
	Due      DueReader
	Notifier Deliverer
	DBPath   string
	Clock    models.Clock
}

// SkipMarker is the job-run output of a week with nothing to post, so the job
// store records why no message went out.
type SkipMarker struct {
	Skipped    bool   `json:"skipped"`
	SkipReason string `json:"skip_reason"`
	Week       string `json:"week"`
}

// weeklyArgs is the empty typed payload for the periodic: the post is a pure
// function of the Ledger at fire time. Its JSON form is the empty object the
// scheduler enqueues.
type weeklyArgs struct{}

// weeklyWorker fires the Monday post.
type weeklyWorker struct {
	r     *Runner
	clock models.Clock
}

// Kind names the worker dispatched for kindWeekly.
func (weeklyWorker) Kind() string { return kindWeekly }

// Work fires the post for now. A week with nothing to post logs one info line
// and returns the [SkipMarker] as the job run's output; every other outcome
// returns the empty result, and an error fails the job into the supervised log.
func (w weeklyWorker) Work(ctx context.Context, job *flywheel.Job[weeklyArgs]) (flywheel.Result, error) {
	out, err := w.r.Fire(ctx, w.clock.Now(ctx))
	if err != nil {
		return flywheel.Result{}, err
	}
	if out.SkipReason != SkipNothingDue {
		return flywheel.Result{}, nil
	}
	logger := w.r.logger()
	if job != nil && job.Logger != nil {
		logger = job.Logger
	}
	logger.InfoContext(ctx, "contact weekly: nothing due — no post", "week", out.Week)
	return flywheel.Result{Output: SkipMarker{Skipped: true, SkipReason: SkipNothingDue, Week: out.Week}}, nil
}

// Run opens the disposable job DB, migrates it, registers the weekly worker,
// reconciles the single weekly periodic from the config Time/Weekday, and runs
// the flywheel node until ctx is canceled. It returns nil on a clean drain. It
// runs beside the Engine and the other nodes in the same process, config-gated,
// so it is up whenever the scheduler is and contact_weekly is enabled.
func Run(ctx context.Context, o Options) error {
	if o.Store == nil {
		return errNoStore
	}
	if o.Notifier == nil {
		return errNoNotifier
	}
	if o.Due == nil {
		return errNoDue
	}

	clock := flynode.ResolveClock(ctx, o.Clock)

	dbPath, err := DefaultDBPath(o.DBPath)
	if err != nil {
		return err
	}

	runner := NewRunner(o)
	reg := buildRegistry(runner, clock)

	return flynode.Boot(ctx, flynode.BootConfig{
		Pkg:      "contactweekly",
		Queue:    queueName,
		DBPath:   dbPath,
		Clock:    clock,
		Store:    o.Store,
		Registry: reg,
		Reconcile: func(ctx context.Context, db *gorm.DB) error {
			return upsertWeeklyPeriodic(ctx, db, o.Config)
		},
	})
}

// buildRegistry registers the single weekly worker over one runner.
func buildRegistry(r *Runner, clock models.Clock) *flywheel.Registry {
	reg := flywheel.NewRegistry()
	flywheel.Register(reg, weeklyWorker{r: r, clock: clock})
	return reg
}

// upsertWeeklyPeriodic reconciles the one weekly periodic from the config
// Time/Weekday — Monday 09:00 by default. It is idempotent by slug, safe to
// call on every boot.
func upsertWeeklyPeriodic(ctx context.Context, db *gorm.DB, cfg config.ContactWeeklyConfig) error {
	cron, err := weeklyCron(cfg.Time, cfg.Weekday)
	if err != nil {
		return err
	}
	if err := flywheel.UpsertPeriodic(ctx, db, flywheel.PeriodicSpec{
		Slug: slugWeekly, Kind: kindWeekly, Cron: cron, Queue: queueName, Active: true,
	}); err != nil {
		return fmt.Errorf("contactweekly: upsert weekly periodic: %w", err)
	}
	return nil
}

// weeklyCron turns an "HH:MM" mark and a cron weekday (0=Sunday..6=Saturday)
// into a weekly 5-field cron expression: ("09:00", 1) -> "0 9 * * 1". A
// malformed mark or an out-of-range weekday is rejected rather than silently
// mis-scheduled.
func weeklyCron(hm string, weekday int) (string, error) {
	h, m, err := parseHM(hm)
	if err != nil {
		return "", err
	}
	if weekday < 0 || weekday > 6 {
		return "", fmt.Errorf("contactweekly: invalid cron weekday %d: want 0..6", weekday)
	}
	return fmt.Sprintf("%d %d * * %d", m, h, weekday), nil
}

// parseHM parses an "HH:MM" clock mark into its hour and minute, rejecting a
// wrong shape, an out-of-range field, or a non-numeric field.
func parseHM(hm string) (hour, minute int, err error) {
	mark, err := clockmark.Parse(hm)
	if err != nil {
		return 0, 0, fmt.Errorf("contactweekly: %w", err)
	}
	return mark.Hour(), mark.Minute(), nil
}

// DefaultDBPath resolves the disposable Monday-post job-DB path: an explicit
// override wins, then the LUCID_CONTACT_WEEKLY_DB env override, then the
// default contact-weekly.db under the OS user-config dir (outside the ~/.lucid
// Ledger, and distinct from every other node's job DB). It is exported so a
// read-only inspector resolves the exact same path the daemon writes to.
func DefaultDBPath(dbPath string) (string, error) {
	return flynode.ResolveDBPath(dbPath, envContactWeeklyDB, "contact-weekly.db")
}
