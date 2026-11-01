package witnessreport

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
	"github.com/mrz1836/lucid/internal/composekit"
	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/flynode"
	"github.com/mrz1836/lucid/internal/isoweek"
	"github.com/mrz1836/lucid/internal/notify"
	"github.com/mrz1836/lucid/internal/storage"
)

// Job runtime identifiers. The weekly report runs its own disposable flywheel
// node beside the Engine's and the companion's, so its queue, kind, and slug are
// distinct: the single weekly periodic never collides with the daily pairs, and
// a restart reconciles it by slug without duplicating.
const (
	queueName  = "lucid-witness-report"
	kindWeekly = "lucid_witness_report_weekly"
	slugWeekly = "lucid-witness-report-weekly"

	// backfillCap is the bounded missed-fire catch-up the flynode boot spine
	// applies (a single most-recent bucket). It is mirrored here only so the
	// in-process test rig builds its scheduler with the same cap the daemon runs.
	backfillCap = 1

	// envWitnessReportDB overrides the job-DB path when --db is not passed. Like
	// the Engine's flywheel.db and the companion's companion.db it is disposable
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
	SendEmbedReturningID(ctx context.Context, channel string, e notify.Embed) (string, error)
	VerifyPresent(ctx context.Context, channel, messageID string) error
	Send(ctx context.Context, channel, text string) error
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
	// log receives the runner's own warn lines (a failed alert). It is nil in
	// production — [Runner.logger] then uses slog.Default(), which lands in the
	// supervised daemon log — and injected by tests to capture the lines.
	log *slog.Logger
}

// NewRunner wires a Runner over the compose dependencies (from the witness_report
// config block + provider), the delivery transport, and the Ledger store that
// holds the per-week receipt. The store's home is the Lucid home the week-notes
// read refuses a path inside; with no store it is empty, and a configured
// notes file then fails closed rather than being read.
func NewRunner(o Options) *Runner {
	home := ""
	if o.Store != nil {
		home = o.Store.Home()
	}
	deps := Deps{
		SystemPrompt: o.Config.SystemPrompt,
		Template:     o.Config.Template,
		AsksFile:     o.Config.AsksFile,
		NotesFile:    o.Config.NotesFile,
		Home:         home,
		Model:        o.Config.Model,
		Provider:     o.Provider,
		Numbers:      o.Numbers,
		Records:      o.Records,
		AnchorDetail: o.AnchorDetail,
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
// alerted to review). FallbackReason, FallbackError, and FallbackDetail carry
// the composer's record of why a fallback fired (see [Report]) up to the
// scheduled worker, which reports it. Parts is how many messages the delivered
// report spanned: 1 for the card alone, more when full anchor detail continued
// past it. A resumed multi-message batch carries no compose metadata (it was
// composed by the earlier attempt), so its fallback fields read empty.
type Outcome struct {
	Week           string
	Delivered      bool
	MessageID      string
	Channel        string
	Parts          int
	Fallback       bool
	SafetyTripped  bool
	Skipped        bool
	SkipReason     string
	FallbackReason string
	FallbackError  string
	FallbackDetail string
}

// Fire composes and delivers this week's report at `now`, honoring the whole
// reliability contract in order: refuse a stale send past the cut-off (alert
// instead), skip an already-delivered week idempotently (a verified receipt that
// still reads back), compose (the deterministic report always lands — only the
// warmth can be lost), deliver the embed with read-back, and persist the
// per-week receipt. A tripped witness-safe scan still delivers the metrics-only
// report but alerts the operator to review. Any total send failure fires a
// best-effort alert and returns a loud error — silence is the one outcome it
// never produces.
//
// A report that spans several messages (full anchor detail past one embed) is
// delivered as a frozen batch: the rendered parts are persisted in the week's
// receipt before the first send, each part is sent and read back in order with
// its progress recorded, and the week is marked delivered only once every part
// has verified. A failure partway is loud — one alert naming how many parts
// verified — and leaves the week incomplete; the next fire in the same week
// resumes that frozen batch instead of re-composing, without re-sending a
// verified part. A single-message report takes exactly the original path.
func (r *Runner) Fire(ctx context.Context, now time.Time) (Outcome, error) {
	channel, err := ChannelForMode(r.mode)
	if err != nil {
		return Outcome{}, err
	}
	week := isoweek.Label(now)

	markAt, err := r.markFor(now)
	if err != nil {
		return Outcome{}, err
	}

	f := &weeklyFire{r: r, week: week, channel: channel}
	fired, err := flynode.Fire(ctx, now, flynode.Delivery[weeklyPayload]{
		Channel:      channel,
		CutoffAt:     markAt.Add(staleAfter),
		Guard:        f.guard,
		Compose:      f.compose,
		Send:         f.send,
		Verify:       f.verify,
		WriteReceipt: f.writeReceipt,
		Alert:        func(text string) { f.alert(ctx, text) },
		PostSend:     func(p weeklyPayload) { f.postSend(ctx, p) },
		CutoffAlert:  fmt.Sprintf("Lucid weekly witness report skipped for %s — it is past the missed-fire cut-off (the host was asleep at the scheduled mark). No stale report was posted.", week),
		ComposeAlert: "Lucid weekly witness report could not be composed — the scheduled report did not go out.",
		SendAlert:    weeklySendAlert,
		VerifyAlert:  "Lucid weekly witness report sent a report that could not be verified in the channel.",
		GuardAlert:   "Lucid weekly witness report could not confirm the earlier delivery is still posted — not re-sending to avoid a duplicate; it will retry.",
		ReceiptAlert: "Lucid weekly witness report delivered but could not record the receipt — a retry may re-post; check the channel.",
	})
	if err != nil {
		return Outcome{}, err
	}

	report := fired.Payload.report
	return Outcome{
		Week:           week,
		Delivered:      fired.Delivered,
		MessageID:      fired.MessageID,
		Channel:        fired.Channel,
		Parts:          len(fired.Payload.parts),
		Fallback:       report.Fallback,
		SafetyTripped:  report.SafetyTripped,
		Skipped:        fired.Skipped,
		SkipReason:     fired.SkipReason,
		FallbackReason: report.FallbackReason,
		FallbackError:  report.FallbackError,
		FallbackDetail: report.FallbackDetail,
	}, nil
}

// weeklySendAlert is the generic send-failure alert; a frozen batch that stops
// partway replaces it with one naming how many parts verified.
const weeklySendAlert = "Lucid weekly witness report failed to deliver — the scheduled report did not go out."

// weeklyFire is one fire's delivery policy for [flynode.Fire]: the closures
// over the week and channel, plus the multi-message batch state they share.
type weeklyFire struct {
	r       *Runner
	week    string
	channel string
	// resume is this week's frozen, unfinished batch, found by the guard.
	resume *storage.WitnessReportReceipt
	// batch is the multi-message batch this fire is delivering.
	batch *storage.WitnessReportReceipt
	// batchAlert replaces the generic send alert once a batch stopped
	// partway, so a partial delivery is reported as exactly that.
	batchAlert string
}

// guard reads the week's receipt: a delivered week is an idempotent skip
// (once its message reads back). An unfinished batch for this week is kept
// for compose to resume — unless the mode changed since, in which case the
// report goes whole to the new audience rather than splitting across two
// channels.
func (f *weeklyFire) guard(_ context.Context, _ time.Time) (flynode.Receipt, error) {
	rec, ok, err := f.r.store.ReadWitnessReportReceipt()
	if err != nil {
		return flynode.Receipt{}, fmt.Errorf("witnessreport: read receipt: %w", err)
	}
	if !ok || rec.Week != f.week {
		return flynode.Receipt{}, nil
	}
	if rec.MessageID != "" {
		return flynode.Receipt{MessageID: rec.MessageID, Channel: rec.ChannelID, Found: true}, nil
	}
	if rec.Pending() && rec.ChannelID == f.channel {
		f.resume = &rec
	}
	return flynode.Receipt{}, nil
}

// compose resumes the frozen batch when there is one, and otherwise composes
// the week's report and renders its ordered parts.
func (f *weeklyFire) compose(ctx context.Context, now time.Time) (weeklyPayload, error) {
	if f.resume != nil {
		return weeklyPayload{parts: thawParts(f.resume.Parts), resumed: true}, nil
	}
	report, err := f.r.compose.Compose(ctx, now)
	if err != nil {
		return weeklyPayload{}, fmt.Errorf("witnessreport: compose: %w", err)
	}
	return weeklyPayload{report: report, parts: RenderEmbeds(report)}, nil
}

// send delivers a single-message report exactly as it always was, and a
// multi-message one as a frozen batch: persisted whole before the first send,
// so a retry resumes exactly what this attempt rendered.
func (f *weeklyFire) send(ctx context.Context, ch string, p weeklyPayload) (string, error) {
	if len(p.parts) == 1 && !p.resumed {
		id, err := f.r.deliver.SendEmbedReturningID(ctx, ch, p.parts[0])
		if err != nil {
			return "", fmt.Errorf("witnessreport: deliver: %w", err)
		}
		return id, nil
	}
	b := f.resume
	if b == nil {
		b = &storage.WitnessReportReceipt{Week: f.week, ChannelID: ch, Parts: freezeParts(p.parts)}
		if err := f.r.store.WriteWitnessReportReceipt(*b); err != nil {
			return "", fmt.Errorf("witnessreport: freeze multi-part report: %w", err)
		}
	}
	f.batch = b
	id, err := f.r.deliverBatch(ctx, ch, b)
	if err != nil {
		f.batchAlert = fmt.Sprintf(
			"Lucid weekly witness report for %s is incomplete — %d of %d messages verified in the channel. It is not marked delivered; a retry resumes the remaining parts without re-sending a verified one.",
			f.week, b.PartsVerified(), len(b.Parts),
		)
		return "", err
	}
	return id, nil
}

// verify reads a message back — except a batch's part 1, which deliverBatch
// read back moments ago, part by part.
func (f *weeklyFire) verify(ctx context.Context, ch, id string) error {
	if f.batch != nil && f.batch.Parts[0].Verified && f.batch.Parts[0].MessageID == id {
		return nil
	}
	if err := f.r.deliver.VerifyPresent(ctx, ch, id); err != nil {
		return fmt.Errorf("witnessreport: verify delivery: %w", err)
	}
	return nil
}

// writeReceipt marks the week delivered. A batch keeps its frozen parts, every
// one now verified, with part 1 as the week's message.
func (f *weeklyFire) writeReceipt(now time.Time, ch, id string) error {
	rec := storage.WitnessReportReceipt{Week: f.week, ChannelID: ch}
	if f.batch != nil {
		rec = *f.batch
	}
	rec.MessageID, rec.Verified, rec.DeliveredAt = id, true, now.Format(time.RFC3339)
	if err := f.r.store.WriteWitnessReportReceipt(rec); err != nil {
		return fmt.Errorf("witnessreport: write receipt: %w", err)
	}
	return nil
}

// alert sends the loud ping, naming a partial batch delivery as exactly that.
func (f *weeklyFire) alert(ctx context.Context, text string) {
	if text == weeklySendAlert && f.batchAlert != "" {
		text = f.batchAlert
	}
	f.r.alert(ctx, text)
}

// postSend alerts the operator when the witness-safe scan tripped: the model
// produced flagged prose that was discarded. It runs only after the receipt is
// persisted and never unwinds the send.
func (f *weeklyFire) postSend(ctx context.Context, p weeklyPayload) {
	if p.report.SafetyTripped {
		f.r.alert(ctx, fmt.Sprintf(
			"Lucid weekly witness report for %s tripped the witness-safe scan — the model prose was discarded and the metrics-only report was delivered. Worth a review.",
			f.week,
		))
	}
}

// weeklyPayload is one fire's composed delivery: the report and the ordered
// embeds it renders to (one per message). A resumed payload is a frozen batch
// read back from the receipt — its report was composed by the earlier attempt,
// so only the parts travel.
type weeklyPayload struct {
	report  Report
	parts   []notify.Embed
	resumed bool
}

// deliverBatch sends a frozen multi-message batch to ch in order, skipping any
// part already verified, and records each part's message id and read-back in
// the receipt as it goes. A part a prior attempt sent but could not verify is
// probed first: present counts as verified, provably absent is sent again, and
// an indeterminate probe stops the run without re-sending (it could
// double-post). It returns part 1's id once every part has verified; any
// failure is a loud partial-delivery error naming how far the batch got.
func (r *Runner) deliverBatch(ctx context.Context, ch string, b *storage.WitnessReportReceipt) (string, error) {
	for i := range b.Parts {
		if err := r.deliverPart(ctx, ch, b, i); err != nil {
			return "", fmt.Errorf("witnessreport: partial delivery — %d of %d parts verified: %w",
				b.PartsVerified(), len(b.Parts), err)
		}
	}
	return b.Parts[0].MessageID, nil
}

// deliverPart brings part i of a frozen batch to verified, recording each step
// in the receipt. A verified part is left alone; a part sent before but never
// confirmed is re-probed, and sent again only when provably gone.
func (r *Runner) deliverPart(ctx context.Context, ch string, b *storage.WitnessReportReceipt, i int) error {
	p := &b.Parts[i]
	step := func(what string, err error) error {
		return fmt.Errorf("%s part %d of %d: %w", what, i+1, len(b.Parts), err)
	}
	if p.Verified {
		return nil
	}
	if p.MessageID != "" {
		err := r.deliver.VerifyPresent(ctx, ch, p.MessageID)
		switch {
		case err == nil:
			p.Verified = true
			return r.recordPart(b, step)
		case !errors.Is(err, flynode.ErrMessageAbsent):
			return step("verify", err) // presence unknown: re-sending could double-post
		}
		p.MessageID = "" // provably gone: send it again
	}
	id, err := r.deliver.SendEmbedReturningID(ctx, ch, thawEmbed(p.Embed))
	if err != nil {
		return step("deliver", err)
	}
	p.MessageID = id
	if err := r.recordPart(b, step); err != nil {
		return err
	}
	if err := r.deliver.VerifyPresent(ctx, ch, id); err != nil {
		return step("verify", err)
	}
	p.Verified = true
	return r.recordPart(b, step)
}

// recordPart persists a batch's progress after a part changed.
func (r *Runner) recordPart(b *storage.WitnessReportReceipt, step func(string, error) error) error {
	if err := r.store.WriteWitnessReportReceipt(*b); err != nil {
		return step("record", err)
	}
	return nil
}

// freezeParts converts rendered embeds into the receipt's frozen parts, none
// yet sent.
func freezeParts(embeds []notify.Embed) []storage.WitnessReportPart {
	parts := make([]storage.WitnessReportPart, 0, len(embeds))
	for _, e := range embeds {
		fields := make([]storage.WitnessReportField, 0, len(e.Fields))
		for _, f := range e.Fields {
			fields = append(fields, storage.WitnessReportField{Name: f.Name, Value: f.Value, Inline: f.Inline})
		}
		parts = append(parts, storage.WitnessReportPart{Embed: storage.WitnessReportEmbed{
			Title: e.Title, Description: e.Description, Color: e.Color, Fields: fields, Footer: e.Footer,
		}})
	}
	return parts
}

// thawParts reads a frozen batch back into embeds, in order.
func thawParts(parts []storage.WitnessReportPart) []notify.Embed {
	embeds := make([]notify.Embed, 0, len(parts))
	for _, p := range parts {
		embeds = append(embeds, thawEmbed(p.Embed))
	}
	return embeds
}

// thawEmbed reads one frozen embed back, byte for byte.
func thawEmbed(e storage.WitnessReportEmbed) notify.Embed {
	fields := make([]notify.EmbedField, 0, len(e.Fields))
	for _, f := range e.Fields {
		fields = append(fields, notify.EmbedField{Name: f.Name, Value: f.Value, Inline: f.Inline})
	}
	return notify.Embed{Title: e.Title, Description: e.Description, Color: e.Color, Fields: fields, Footer: e.Footer}
}

// fallbackDaemon names the weekly report in a fallback report's log line,
// job-run marker, and once-a-day ping receipt; fallbackKind names the message
// in the user-channel ping.
const (
	fallbackDaemon = "witness"
	fallbackKind   = "weekly witness report"
)

// reportFallback makes a delivered scheduled fallback loud. It runs only in the
// scheduled worker, after Fire returned delivered with its receipt persisted —
// never inside the shared fire path — so nothing here can delay, fail, or
// unwind the send.
//
// A model-composed send or a skipped fire returns the empty result (no
// compose_fallback marker). A delivered fallback is handed to
// [flynode.ReportFallback] as daemon witness: it always logs the warn line and
// returns the marker. An outage or unusable-reply fallback also pings once per
// local day across every daemon, claimed through the store's ping receipt —
// always through [Runner.alert], so the ping lands in the user channel even in
// auto mode, never the witness channel. A safety-tripped fallback carries no
// ping: the PostSend safety-trip alert already fired and stays the only alert.
func (r *Runner) reportFallback(ctx context.Context, logger *slog.Logger, out Outcome, now time.Time) flywheel.Result {
	if !out.Delivered || !out.Fallback {
		return flywheel.Result{}
	}
	return flynode.ReportFallback(ctx, logger, flynode.FallbackReport{
		Daemon: fallbackDaemon,
		Reason: out.FallbackReason,
		Error:  out.FallbackError,
		Day:    engine.DateString(engine.DateOf(now)),
		Ping:   composekit.FallbackPing(fallbackKind, out.FallbackReason, out.FallbackDetail),
		Claim: func(fr flynode.FallbackReport) (bool, error) {
			return r.store.ClaimComposeFallbackPing(storage.ComposeFallbackPing{
				Date:     fr.Day,
				Daemon:   fr.Daemon,
				Reason:   fr.Reason,
				PingedAt: now.Format(time.RFC3339),
			})
		},
		Alert: func(text string) { r.alert(ctx, text) },
	})
}

// loggerOf returns the attempt's job logger (flywheel's, carrying job_id / kind
// / run_id), or nil when the job or its logger is unset — which
// [flynode.ReportFallback] resolves to slog.Default(), so a bare job never
// panics.
func loggerOf(job *flywheel.Job[weeklyArgs]) *slog.Logger {
	if job == nil {
		return nil
	}
	return job.Logger
}

// alertTimeout bounds a detached best-effort alert: it sits above the notifier's
// own ~20s HTTP budget so a legitimately slow (cold-start) alert is never cut
// short, while still bounding how long a wedged channel can delay a shutdown.
const alertTimeout = 30 * time.Second

// alert posts a best-effort loud ping to the user channel when a weekly report
// cannot deliver its real report (or tripped the witness-safe scan) — the "never
// silent" floor. It is deliberately best-effort: if the channel itself is
// unreachable the returned error from [Runner.Fire] is the loud signal (it fails
// the job and lands in the supervised daemon log), and a failed alert must not
// mask that original error. A failed alert is itself logged at warn ("alert
// send failed") rather than silently dropped, and still never replaces the
// error the caller returns.
//
// The alert runs on a context DETACHED from the run's cancellation
// (context.WithoutCancel): a stop signal mid-send (error-states Sc-6) cancels
// the Fire ctx, and reusing it here would abort the alert POST at the transport
// exactly when a run is torn down — the loud floor going silent at the one
// moment it matters most. A bounded deadline still keeps a wedged channel from
// hanging shutdown.
func (r *Runner) alert(ctx context.Context, text string) {
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), alertTimeout)
	defer cancel()
	if err := r.deliver.Send(actx, engine.ChannelUser, text); err != nil {
		r.logger().WarnContext(ctx, "alert send failed", "daemon", "witness", "error", err)
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

// ChannelForMode resolves the delivery mode to a logical channel: preview posts
// to the operator's own user channel (the safe default during the trust period),
// auto posts to the friend-facing witness channel. An unknown mode is a hard
// error — config validation already rejects one on an enabled report, so this
// only guards a hand-built Runner — rather than a mis-send to the wrong audience.
// Exported so the CLI's `witness report` command shares this exact routing
// rather than keeping a second copy.
func ChannelForMode(mode string) (string, error) {
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
// AnchorDetail is the opt-in full-detail reader: the composition root sets it
// only when witness_report.anchor_detail is full, and leaves it nil otherwise.
type Options struct {
	Store        *storage.Adapter
	Config       config.WitnessReportConfig
	Provider     config.ProviderConfig
	Numbers      NumbersReader
	Records      RecordsReader
	AnchorDetail AnchorDetailReader
	Notifier     Deliverer
	DBPath       string
	Clock        models.Clock
	Build        composekit.ProviderBuilder
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

// Work composes and delivers the weekly report for `now`, then reports a
// delivered fallback (log, job-run marker, and — for an outage or unusable
// reply — the deduped ping).
func (w weeklyWorker) Work(ctx context.Context, job *flywheel.Job[weeklyArgs]) (flywheel.Result, error) {
	now := w.clock.Now(ctx)
	out, err := w.r.Fire(ctx, now)
	if err != nil {
		return flywheel.Result{}, err
	}
	return w.r.reportFallback(ctx, loggerOf(job), out, now), nil
}

// Run opens the disposable job DB, migrates it, registers the weekly worker,
// reconciles the single Monday-morning periodic from the config Time/Weekday, and
// runs the flywheel node until ctx is canceled (SIGINT/SIGTERM upstream). It
// returns nil on a clean drain. It runs beside the Engine and companion daemons in
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

	clock := flynode.ResolveClock(ctx, o.Clock)

	dbPath, err := DefaultDBPath(o.DBPath)
	if err != nil {
		return err
	}

	runner := NewRunner(o)
	reg := buildRegistry(runner, clock)

	return flynode.Boot(ctx, flynode.BootConfig{
		Pkg:      "witnessreport",
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
	mark, err := clockmark.Parse(hm)
	if err != nil {
		return 0, 0, fmt.Errorf("witnessreport: %w", err)
	}
	return mark.Hour(), mark.Minute(), nil
}

// DefaultDBPath resolves the disposable weekly-report job-DB path: an explicit
// override wins, then the LUCID_WITNESS_REPORT_DB env override, then the default
// witness-report.db under the OS user-config dir (outside the ~/.lucid Ledger,
// and distinct from the Engine's flywheel.db and the companion's companion.db). It
// is exported so a read-only inspector resolves the exact same path the daemon
// writes to.
func DefaultDBPath(dbPath string) (string, error) {
	return flynode.ResolveDBPath(dbPath, envWitnessReportDB, "witness-report.db")
}
