package companion

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	flywheel "github.com/mrz1836/go-flywheel"
	"github.com/mrz1836/go-foundation/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/lucidtest"
	"github.com/mrz1836/lucid/internal/storage"
)

// --- test doubles -----------------------------------------------------------

// fakeComposer is a scripted MessageComposer: it returns a fixed Result (or an
// error) so the node's missed-fire, idempotency, and alert logic is exercised
// without a live provider. It counts calls so a test can assert an idempotent
// or past-cut-off path never composed.
type fakeComposer struct {
	res   Result
	err   error
	calls int
}

func (f *fakeComposer) Compose(_ context.Context, _ Mode, _ time.Time) (Result, error) {
	f.calls++
	if f.err != nil {
		return Result{}, f.err
	}
	return f.res, nil
}

// sendRecord is one delivered send or alert the fake deliverer captured.
type sendRecord struct{ channel, text string }

// fakeDeliverer captures every SendReturningID (real delivery), Send (alert),
// and VerifyPresent (read-back / idempotency probe), and lets a test drive the
// failure branches: sendErr fails delivery, verifyErr fails every read-back, and
// verifyErrFor fails one specific message id (the "receipt's message is gone"
// case). It needs no token or socket.
type fakeDeliverer struct {
	sends        []sendRecord
	alerts       []sendRecord
	verifies     []string
	sendErr      error
	verifyErr    error
	verifyErrFor map[string]error
	idSeq        int
}

func (f *fakeDeliverer) SendReturningID(_ context.Context, channel, text string) (string, error) {
	f.sends = append(f.sends, sendRecord{channel, text})
	if f.sendErr != nil {
		return "", f.sendErr
	}
	f.idSeq++
	return fmt.Sprintf("msg-%d", f.idSeq), nil
}

func (f *fakeDeliverer) VerifyPresent(_ context.Context, _, messageID string) error {
	f.verifies = append(f.verifies, messageID)
	if f.verifyErrFor != nil {
		if e, ok := f.verifyErrFor[messageID]; ok {
			return e
		}
	}
	return f.verifyErr
}

func (f *fakeDeliverer) Send(_ context.Context, channel, text string) error {
	f.alerts = append(f.alerts, sendRecord{channel, text})
	return nil
}

// --- fixtures ---------------------------------------------------------------

// newStore builds a scaffolded Ledger over a temp dir, carrying the default
// chain (bell 19:00, tripwire 06:00) so the morning mark resolves to 06:00 and
// the night mark to 19:00.
func newStore(t *testing.T) *storage.Adapter {
	t.Helper()
	_, store := lucidtest.Ledger(t, lucidtest.NestedHome(), lucidtest.WithEngine())
	return store
}

// newRunner wires a Runner over a scripted composer + deliverer and a real
// scaffolded store, and returns the store so a test can read back the receipt.
func newRunner(t *testing.T, comp MessageComposer, del Deliverer) (*Runner, *storage.Adapter) {
	t.Helper()
	store := newStore(t)
	return &Runner{compose: comp, deliver: del, store: store}, store
}

// at builds a UTC instant (marks compare within one location, so UTC is fine).
//
//nolint:unparam // y is kept explicit for fixture readability even though the cases share 2026
func at(y int, mo time.Month, d, hh, mm int) time.Time {
	return time.Date(y, mo, d, hh, mm, 0, 0, time.UTC)
}

// ctxAt returns a context carrying a fixed clock at t.
func ctxAt(t time.Time) context.Context {
	return models.WithClock(context.Background(), models.NewFixedClock(t))
}

// newJobDB opens and migrates a throwaway companion job DB.
func newJobDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "companion.db")), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	require.NoError(t, flywheel.Migrate(db))
	return db
}

// periodicCrons reads the reconciled periodics back as a slug -> cron map, so a
// mark assertion names the slug it is about.
func periodicCrons(t *testing.T, db *gorm.DB, now time.Time) map[string]string {
	t.Helper()
	views, err := flywheel.ListPeriodics(ctxAt(now), db)
	require.NoError(t, err)
	crons := make(map[string]string, len(views))
	for _, v := range views {
		crons[v.Slug] = v.Cron
	}
	return crons
}

// withTripwireMark rewrites the scaffolded chain's tripwire mark, so a test can
// prove the backstop's mark is derived from it rather than hardcoded.
func withTripwireMark(t *testing.T, store *storage.Adapter, hm string) {
	t.Helper()
	chain, err := store.ReadChainConfig()
	require.NoError(t, err)
	chain.Escalation.TripwireTime = hm
	require.NoError(t, store.WriteChainConfig(chain))
}

// --- Fire: on-time delivery + receipt (AC-1, AC-4) --------------------------

// TestFire_OnTimeMorning_DeliversAndWritesReceipt: a fire at the mark delivers
// the composed message unmodified (no late note), reads it back, and persists a
// verified receipt keyed on the day + window.
func TestFire_OnTimeMorning_DeliversAndWritesReceipt(t *testing.T) {
	comp := &fakeComposer{res: Result{Text: "GOOD MORNING"}}
	del := &fakeDeliverer{}
	r, store := newRunner(t, comp, del)

	now := at(2026, 7, 6, 6, 0)
	out, err := r.Fire(context.Background(), ModeMorning, now)
	require.NoError(t, err)

	assert.True(t, out.Delivered)
	assert.False(t, out.Late)
	require.Len(t, del.sends, 1)
	assert.Equal(t, engine.ChannelUser, del.sends[0].channel)
	assert.Equal(t, "GOOD MORNING", del.sends[0].text, "an on-time fire carries no late note")
	assert.Empty(t, del.alerts, "a clean delivery raises no alert")
	assert.Equal(t, []string{out.MessageID}, del.verifies, "the delivered id is read back")

	rec, ok, rerr := store.ReadCompanionReceipt("morning")
	require.NoError(t, rerr)
	require.True(t, ok)
	assert.Equal(t, "2026-07-06", rec.Date)
	assert.Equal(t, "morning", rec.Window)
	assert.Equal(t, out.MessageID, rec.MessageID)
	assert.True(t, rec.Verified)
}

// TestFire_NightUsesBellMark: the night window rides the 19:00 bell mark — an
// on-time 19:00 fire carries no late note and delivers.
func TestFire_NightUsesBellMark(t *testing.T) {
	comp := &fakeComposer{res: Result{Text: "GOOD NIGHT"}}
	del := &fakeDeliverer{}
	r, _ := newRunner(t, comp, del)

	out, err := r.Fire(context.Background(), ModeNight, at(2026, 7, 6, 19, 0))
	require.NoError(t, err)
	assert.True(t, out.Delivered)
	assert.False(t, out.Late)
	require.Len(t, del.sends, 1)
	assert.Equal(t, "GOOD NIGHT", del.sends[0].text)
}

// --- Fire: idempotency (AC-4) -----------------------------------------------

// TestFire_RetrySameBucket_IsIdempotentSkip: a second fire in the same
// day/window with a receipt whose message still reads back is an idempotent skip
// — no second send, no re-compose.
func TestFire_RetrySameBucket_IsIdempotentSkip(t *testing.T) {
	comp := &fakeComposer{res: Result{Text: "GOOD MORNING"}}
	del := &fakeDeliverer{}
	r, _ := newRunner(t, comp, del)
	now := at(2026, 7, 6, 6, 0)

	_, err := r.Fire(context.Background(), ModeMorning, now)
	require.NoError(t, err)

	out2, err := r.Fire(context.Background(), ModeMorning, now.Add(2*time.Minute))
	require.NoError(t, err)
	assert.True(t, out2.Skipped)
	assert.Equal(t, skipAlreadyDelivered, out2.SkipReason)
	assert.Len(t, del.sends, 1, "the retry re-uses the receipt; no second send")
	assert.Equal(t, 1, comp.calls, "the retry does not re-compose")
}

// TestFire_ReceiptMessageGone_ReDelivers: a receipt whose message no longer
// reads back (deleted, or never really landed) falls through to a fresh delivery
// so the window is never left silently empty.
func TestFire_ReceiptMessageGone_ReDelivers(t *testing.T) {
	comp := &fakeComposer{res: Result{Text: "GM"}}
	del := &fakeDeliverer{verifyErrFor: map[string]error{}}
	r, _ := newRunner(t, comp, del)
	now := at(2026, 7, 6, 6, 0)

	out1, err := r.Fire(context.Background(), ModeMorning, now)
	require.NoError(t, err)

	// The first message is now gone: the idempotency read-back on the next fire
	// fails, so the window re-delivers rather than skipping into silence.
	del.verifyErrFor[out1.MessageID] = errors.New("404 unknown message")
	out2, err := r.Fire(context.Background(), ModeMorning, now.Add(time.Minute))
	require.NoError(t, err)

	assert.True(t, out2.Delivered)
	assert.NotEqual(t, out1.MessageID, out2.MessageID)
	assert.Len(t, del.sends, 2, "a receipt whose message is gone re-delivers")
}

// --- Fire: bounded missed-fire catch-up (AC-6) ------------------------------

// TestFire_LateMorning_PrependsLateNote: a fire after the mark but within the
// window (a backfill on a host that overslept) prefixes the honest late note.
func TestFire_LateMorning_PrependsLateNote(t *testing.T) {
	comp := &fakeComposer{res: Result{Text: "GOOD MORNING"}}
	del := &fakeDeliverer{}
	r, _ := newRunner(t, comp, del)

	out, err := r.Fire(context.Background(), ModeMorning, at(2026, 7, 6, 9, 30))
	require.NoError(t, err)
	assert.True(t, out.Delivered)
	assert.True(t, out.Late)
	require.Len(t, del.sends, 1)
	assert.Equal(t, lateNote+"\n\nGOOD MORNING", del.sends[0].text)
	assert.True(t, strings.HasPrefix(del.sends[0].text, "(late — host was asleep)"))
}

// TestFire_LateNight_PrependsLateNote: the late note is not morning-only — a
// backfilled 21:00 night fire (within the 22:00 window) carries it too.
func TestFire_LateNight_PrependsLateNote(t *testing.T) {
	comp := &fakeComposer{res: Result{Text: "GOOD NIGHT"}}
	del := &fakeDeliverer{}
	r, _ := newRunner(t, comp, del)

	out, err := r.Fire(context.Background(), ModeNight, at(2026, 7, 6, 21, 0))
	require.NoError(t, err)
	assert.True(t, out.Late)
	assert.Equal(t, lateNote+"\n\nGOOD NIGHT", del.sends[0].text)
}

// TestFire_PastMorningCutoff_SkipsAndAlerts: past the 10:00 morning cut-off the
// fire refuses to post a stale message — it skips, alerts Z, and never composes.
func TestFire_PastMorningCutoff_SkipsAndAlerts(t *testing.T) {
	comp := &fakeComposer{res: Result{Text: "GOOD MORNING"}}
	del := &fakeDeliverer{}
	r, store := newRunner(t, comp, del)

	out, err := r.Fire(context.Background(), ModeMorning, at(2026, 7, 6, 10, 30))
	require.NoError(t, err)
	assert.True(t, out.Skipped)
	assert.Equal(t, skipPastCutoff, out.SkipReason)
	assert.Empty(t, del.sends, "no stale message is posted past the cut-off")
	require.Len(t, del.alerts, 1, "past the cut-off Z is alerted instead of left silent")
	assert.Contains(t, del.alerts[0].text, "cut-off")
	assert.Equal(t, 0, comp.calls, "past the cut-off there is no compose")

	_, ok, _ := store.ReadCompanionReceipt("morning")
	assert.False(t, ok, "a skipped window writes no receipt")
}

// TestFire_PastNightCutoff_SkipsAndAlerts: the night cut-off is 22:00 — a 22:30
// fire skips and alerts rather than posting a stale night message near midnight.
func TestFire_PastNightCutoff_SkipsAndAlerts(t *testing.T) {
	comp := &fakeComposer{res: Result{Text: "GOOD NIGHT"}}
	del := &fakeDeliverer{}
	r, _ := newRunner(t, comp, del)

	out, err := r.Fire(context.Background(), ModeNight, at(2026, 7, 6, 22, 30))
	require.NoError(t, err)
	assert.True(t, out.Skipped)
	assert.Equal(t, skipPastCutoff, out.SkipReason)
	assert.Empty(t, del.sends)
	require.Len(t, del.alerts, 1)
}

// --- Fire: never silent (AC-4, AC-5) ----------------------------------------

// TestFire_DeliveryError_AlertsAndErrors: a total send failure fires a loud
// alert and returns a loud error — and writes no receipt, so a retry re-sends.
func TestFire_DeliveryError_AlertsAndErrors(t *testing.T) {
	comp := &fakeComposer{res: Result{Text: "GOOD MORNING"}}
	del := &fakeDeliverer{sendErr: errors.New("discord 503")}
	r, store := newRunner(t, comp, del)

	_, err := r.Fire(context.Background(), ModeMorning, at(2026, 7, 6, 6, 0))
	require.Error(t, err)
	require.Len(t, del.alerts, 1, "a total send failure fires a loud alert")

	_, ok, _ := store.ReadCompanionReceipt("morning")
	assert.False(t, ok, "a failed delivery writes no receipt")
}

// TestFire_VerifyError_AlertsAndErrors: a send that will not read back is a loud
// failure — the alert fires and the error surfaces, and no receipt is written.
func TestFire_VerifyError_AlertsAndErrors(t *testing.T) {
	comp := &fakeComposer{res: Result{Text: "GOOD MORNING"}}
	del := &fakeDeliverer{verifyErr: errors.New("404 after send")}
	r, store := newRunner(t, comp, del)

	_, err := r.Fire(context.Background(), ModeMorning, at(2026, 7, 6, 6, 0))
	require.Error(t, err)
	require.Len(t, del.alerts, 1)

	_, ok, _ := store.ReadCompanionReceipt("morning")
	assert.False(t, ok, "an unverified send writes no receipt")
}

// TestFire_ComposeError_AlertsAndErrors: a loud compose failure (a missing
// prompt file, an unreadable verdict) alerts and errors rather than sending an
// empty message.
func TestFire_ComposeError_AlertsAndErrors(t *testing.T) {
	comp := &fakeComposer{err: errors.New("prompt file missing")}
	del := &fakeDeliverer{}
	r, _ := newRunner(t, comp, del)

	_, err := r.Fire(context.Background(), ModeMorning, at(2026, 7, 6, 6, 0))
	require.Error(t, err)
	require.Len(t, del.alerts, 1)
	assert.Empty(t, del.sends, "no message goes out on a compose failure")
}

// TestFire_FallbackFlagPropagates: a deterministic-fallback compose still
// delivers, and the Outcome records the fallback so the caller can log it.
func TestFire_FallbackFlagPropagates(t *testing.T) {
	comp := &fakeComposer{res: Result{Text: "DETERMINISTIC", Fallback: true}}
	del := &fakeDeliverer{}
	r, _ := newRunner(t, comp, del)

	out, err := r.Fire(context.Background(), ModeMorning, at(2026, 7, 6, 6, 0))
	require.NoError(t, err)
	assert.True(t, out.Delivered)
	assert.True(t, out.Fallback)
	assert.Equal(t, "DETERMINISTIC", del.sends[0].text)
}

// TestFire_UnknownMode_Errors rejects a mode that is neither window.
func TestFire_UnknownMode_Errors(t *testing.T) {
	r, _ := newRunner(t, &fakeComposer{}, &fakeDeliverer{})
	_, err := r.Fire(context.Background(), Mode("noon"), at(2026, 7, 6, 6, 0))
	require.Error(t, err)
}

// --- FireBackstop: the morning safety net (AC-3, AC-4) ----------------------

// TestFireBackstop_PrependsBackstopNoteNotTheLateNote: a backstop fire sits an
// hour past the mark, so it is always past the late grace and always carries a
// prefix. That prefix names the event that actually happened — a send that did
// not go out — rather than the one that did not, a host that was asleep.
func TestFireBackstop_PrependsBackstopNoteNotTheLateNote(t *testing.T) {
	comp := &fakeComposer{res: Result{Text: "GOOD MORNING"}}
	del := &fakeDeliverer{}
	r, _ := newRunner(t, comp, del)

	out, err := r.FireBackstop(context.Background(), at(2026, 7, 6, 7, 0))
	require.NoError(t, err)
	assert.True(t, out.Delivered, "with no receipt the backstop delivers the real card")

	require.Len(t, del.sends, 1)
	assert.Equal(t, backstopNote+"\n\nGOOD MORNING", del.sends[0].text)
	assert.NotContains(t, del.sends[0].text, lateNote, "a delivery failure is not a sleeping host")
}

// TestFireBackstop_AlreadyDeliveredIsAnIdempotentSkip is the exactly-once test:
// a morning the primary send already delivered left a verified receipt whose
// message still reads back, so the backstop skips rather than posting a second
// copy. The guarantee is structural — the backstop consults the same receipt
// through the same guard the primary send uses, so at most one morning message
// exists by construction rather than by timing.
func TestFireBackstop_AlreadyDeliveredIsAnIdempotentSkip(t *testing.T) {
	comp := &fakeComposer{res: Result{Text: "GOOD MORNING"}}
	// The deliverer verifies successfully, which is the case that matters: the
	// receipt's message is still in the channel, so the window is genuinely spoken
	// for. (A receipt whose message is gone deliberately re-delivers instead —
	// TestFire_ReceiptMessageGone_ReDelivers covers that branch.)
	del := &fakeDeliverer{}
	r, store := newRunner(t, comp, del)

	primary, err := r.Fire(context.Background(), ModeMorning, at(2026, 7, 6, 6, 0))
	require.NoError(t, err)
	require.True(t, primary.Delivered)

	out, err := r.FireBackstop(context.Background(), at(2026, 7, 6, 7, 0))
	require.NoError(t, err)
	assert.True(t, out.Skipped)
	assert.Equal(t, skipAlreadyDelivered, out.SkipReason)
	assert.Len(t, del.sends, 1, "the backstop re-uses the receipt; no second send")
	assert.Equal(t, 1, comp.calls, "a no-op backstop does not re-compose")

	rec, ok, rerr := store.ReadCompanionReceipt("morning")
	require.NoError(t, rerr)
	require.True(t, ok)
	assert.Equal(t, primary.MessageID, rec.MessageID, "the primary delivery stays authoritative")
}

// TestFireBackstop_MissedMorningStillDelivers: the whole point — when the 06:00
// send failed outright it left no receipt, so the backstop composes and delivers
// the real card and the morning is not silent.
func TestFireBackstop_MissedMorningStillDelivers(t *testing.T) {
	comp := &fakeComposer{res: Result{Text: "GOOD MORNING"}}
	del := &fakeDeliverer{sendErr: errors.New("discord 503")}
	r, store := newRunner(t, comp, del)

	_, err := r.Fire(context.Background(), ModeMorning, at(2026, 7, 6, 6, 0))
	require.Error(t, err, "the primary send fails outright")
	_, ok, _ := store.ReadCompanionReceipt("morning")
	require.False(t, ok, "a failed delivery leaves no receipt to skip on")

	del.sendErr = nil // the blip has passed by the backstop mark
	out, err := r.FireBackstop(context.Background(), at(2026, 7, 6, 7, 0))
	require.NoError(t, err)
	assert.True(t, out.Delivered)
	assert.Contains(t, out.Text, "GOOD MORNING")
}

// --- The backstop worker: delegation and the silent stand-down (AC-4) -------

// TestBackstopWorker_DelegatesToFireBackstop: inside the window the worker
// re-runs the real morning card, exactly once.
func TestBackstopWorker_DelegatesToFireBackstop(t *testing.T) {
	comp := &fakeComposer{res: Result{Text: "COMPOSED"}}
	del := &fakeDeliverer{}
	r, _ := newRunner(t, comp, del)
	now := at(2026, 7, 6, 7, 0)
	w := morningBackstopWorker{r: r, clock: models.NewFixedClock(now)}

	_, err := w.Work(ctxAt(now), &flywheel.Job[companionArgs]{})
	require.NoError(t, err)
	require.Len(t, del.sends, 1, "the backstop delivers the real morning card")
	assert.Contains(t, del.sends[0].text, "COMPOSED")
}

// TestBackstopWorker_PastMorningCutoff_StandsDownSilently: past the 10:00
// cut-off the backstop does nothing and says nothing — no send and, crucially,
// no alert. The primary morning periodic reaches that cut-off first and already
// alerted for the window, so a second copy would be noise about a fault that has
// been reported once.
func TestBackstopWorker_PastMorningCutoff_StandsDownSilently(t *testing.T) {
	comp := &fakeComposer{res: Result{Text: "COMPOSED"}}
	del := &fakeDeliverer{}
	r, _ := newRunner(t, comp, del)
	now := at(2026, 7, 6, 10, 30)
	w := morningBackstopWorker{r: r, clock: models.NewFixedClock(now)}

	_, err := w.Work(ctxAt(now), &flywheel.Job[companionArgs]{})
	require.NoError(t, err, "a backstop with nothing to do is not a job failure")
	assert.Empty(t, del.sends, "no stale message is posted past the cut-off")
	assert.Empty(t, del.alerts, "the primary periodic owns the past-cut-off alert")
	assert.Equal(t, 0, comp.calls, "a stood-down backstop does not compose")
}

// --- Node wiring: periodics from chain marks (AC-1, AC-7) -------------------

// TestUpsertPeriodics_RegistersTheThreePeriodicsFromChainMarks: reconciling from
// the default chain marks registers the morning periodic on the 06:00 tripwire
// mark, the night on the 19:00 bell mark, and the morning backstop an hour after
// the morning — all three active on the companion queue.
func TestUpsertPeriodics_RegistersTheThreePeriodicsFromChainMarks(t *testing.T) {
	store := newStore(t)
	db := newJobDB(t)
	ctx := ctxAt(at(2026, 7, 5, 12, 0))

	require.NoError(t, upsertPeriodics(ctx, db, store))

	views, err := flywheel.ListPeriodics(ctx, db)
	require.NoError(t, err)
	crons := map[string]string{}
	for _, v := range views {
		crons[v.Slug] = v.Cron
		assert.True(t, v.Active, "periodic %s is active", v.Slug)
		assert.Equal(t, queueName, v.Queue)
	}
	assert.Equal(t, "0 6 * * *", crons[slugMorning], "the morning rides the 06:00 tripwire mark")
	assert.Equal(t, "0 19 * * *", crons[slugNight], "the night rides the 19:00 bell mark")
	assert.Equal(t, "0 7 * * *", crons[slugMorningBackstop], "the backstop rides the tripwire mark plus its grace")
}

// TestUpsertPeriodics_RegistersMorningBackstopAtTripwirePlusGrace: the backstop
// arms exactly one grace period after the morning mark it backs up — late
// enough that the primary send has had its full run at delivery, early enough to
// still be morning and to sit well inside the 10:00 cut-off.
func TestUpsertPeriodics_RegistersMorningBackstopAtTripwirePlusGrace(t *testing.T) {
	store := newStore(t)
	db := newJobDB(t)
	now := at(2026, 7, 5, 12, 0)

	require.NoError(t, upsertPeriodics(ctxAt(now), db, store))

	crons := periodicCrons(t, db, now)
	assert.Equal(t, "0 7 * * *", crons[slugMorningBackstop])
	assert.Equal(t, "0 6 * * *", crons[slugMorning], "arming the backstop leaves the morning mark alone")
}

// TestUpsertPeriodics_BackstopMarkFollowsAMovedTripwire: the backstop's mark is
// derived from the same chain mark the morning rides, never hardcoded — move the
// tripwire and the backstop moves with it, so the two can never drift.
func TestUpsertPeriodics_BackstopMarkFollowsAMovedTripwire(t *testing.T) {
	store := newStore(t)
	db := newJobDB(t)
	now := at(2026, 7, 5, 12, 0)
	withTripwireMark(t, store, "05:15")

	require.NoError(t, upsertPeriodics(ctxAt(now), db, store))

	crons := periodicCrons(t, db, now)
	assert.Equal(t, "15 5 * * *", crons[slugMorning], "the morning follows the moved mark")
	assert.Equal(t, "15 6 * * *", crons[slugMorningBackstop], "and the backstop follows it too")
}

// TestUpsertPeriodics_BackstopMarkWrapsPastMidnight: a tripwire late enough that
// the grace crosses midnight wraps to the next day's clock rather than
// overflowing into an invalid hour. Such a mark puts the backstop past the
// morning cut-off, where its worker stands down — inert, not misfiring.
func TestUpsertPeriodics_BackstopMarkWrapsPastMidnight(t *testing.T) {
	store := newStore(t)
	db := newJobDB(t)
	now := at(2026, 7, 5, 12, 0)
	withTripwireMark(t, store, "23:30")

	require.NoError(t, upsertPeriodics(ctxAt(now), db, store))

	assert.Equal(t, "30 0 * * *", periodicCrons(t, db, now)[slugMorningBackstop])
}

// TestUpsertPeriodics_IsIdempotentAcrossRestart: re-reconciling the same marks
// neither duplicates a definition nor changes its cadence.
func TestUpsertPeriodics_IsIdempotentAcrossRestart(t *testing.T) {
	store := newStore(t)
	db := newJobDB(t)

	require.NoError(t, upsertPeriodics(ctxAt(at(2026, 7, 5, 12, 0)), db, store))
	require.NoError(t, upsertPeriodics(ctxAt(at(2026, 7, 6, 12, 0)), db, store))

	views, err := flywheel.ListPeriodics(ctxAt(at(2026, 7, 6, 12, 0)), db)
	require.NoError(t, err)
	assert.Len(t, views, 3, "no duplicate definitions after a restart reconcile")
}

// TestUpsertPeriodics_RejectsMalformedClockMark: an out-of-range mark is rejected
// at reconcile rather than scheduled.
func TestUpsertPeriodics_RejectsMalformedClockMark(t *testing.T) {
	store := newStore(t)
	db := newJobDB(t)
	chain, err := store.ReadChainConfig()
	require.NoError(t, err)
	chain.Escalation.TripwireTime = "24:61"
	require.NoError(t, store.WriteChainConfig(chain))

	require.Error(t, upsertPeriodics(ctxAt(at(2026, 7, 5, 12, 0)), db, store))
}

// --- Node wiring: end-to-end fire through the flywheel machinery -------------

// rig is a companion flywheel runtime over an isolated Ledger and throwaway job
// DB: the scheduler (BackfillCap 1) and a deterministic runner share one
// registry, and the fake deliverer captures the sends.
type rig struct {
	db     *gorm.DB
	store  *storage.Adapter
	del    *fakeDeliverer
	comp   *fakeComposer
	sched  *flywheel.Scheduler
	runner *flywheel.Runner
}

func newRig(t *testing.T, clock models.Clock) *rig {
	t.Helper()
	store := newStore(t)
	db := newJobDB(t)
	del := &fakeDeliverer{}
	comp := &fakeComposer{res: Result{Text: "COMPOSED"}}
	r := &Runner{compose: comp, deliver: del, store: store}
	reg := buildRegistry(r, clock)

	driver := flywheel.NewSQLiteDriver(db)
	sched, err := flywheel.NewSchedulerWithConfig(flywheel.SchedulerConfig{
		DB: db, Client: flywheel.NewClient(db), Driver: driver, BackfillCap: backfillCap,
	})
	require.NoError(t, err)
	runner, err := flywheel.NewRunner(flywheel.RunnerConfig{
		DB: db, Driver: driver, Registry: reg,
		Queues: []string{queueName}, ClaimAnyClass: true, Concurrency: 1,
	})
	require.NoError(t, err)
	return &rig{db: db, store: store, del: del, comp: comp, sched: sched, runner: runner}
}

// TestRun_MorningPeriodicFiresAndDelivers drives a due morning periodic all the
// way through the machinery: the scheduler enqueues it and the runner fires the
// worker, which composes and delivers to the user channel.
func TestRun_MorningPeriodicFiresAndDelivers(t *testing.T) {
	workerNow := at(2026, 7, 6, 6, 5)
	r := newRig(t, models.NewFixedClock(workerNow))
	require.NoError(t, flywheel.UpsertPeriodic(ctxAt(at(2026, 7, 5, 12, 0)), r.db, flywheel.PeriodicSpec{
		Slug: slugMorning, Kind: kindMorning, Cron: "0 6 * * *", Queue: queueName, Active: true,
	}))

	tickCtx := ctxAt(workerNow)
	n, err := r.sched.Tick(tickCtx)
	require.NoError(t, err)
	require.Equal(t, 1, n, "the due morning periodic enqueues once")

	require.NoError(t, r.runner.RunUntilIdle(tickCtx))

	require.Len(t, r.del.sends, 1, "the morning companion delivers")
	assert.Equal(t, engine.ChannelUser, r.del.sends[0].channel)
	assert.Equal(t, "COMPOSED", r.del.sends[0].text)
}

// TestRun_NightPeriodicFiresAndDelivers proves the night periodic fires the
// night worker end to end.
func TestRun_NightPeriodicFiresAndDelivers(t *testing.T) {
	workerNow := at(2026, 7, 6, 19, 5)
	r := newRig(t, models.NewFixedClock(workerNow))
	require.NoError(t, flywheel.UpsertPeriodic(ctxAt(at(2026, 7, 5, 12, 0)), r.db, flywheel.PeriodicSpec{
		Slug: slugNight, Kind: kindNight, Cron: "0 19 * * *", Queue: queueName, Active: true,
	}))

	tickCtx := ctxAt(workerNow)
	n, err := r.sched.Tick(tickCtx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.NoError(t, r.runner.RunUntilIdle(tickCtx))

	require.Len(t, r.del.sends, 1, "the night companion delivers")
}

// TestRun_MorningBackstopPeriodicFiresAndDelivers drives a due morning backstop
// through the same machinery: the scheduler enqueues it and the runner fires the
// backstop worker, which re-composes the real card and delivers it to the user
// channel under the backstop note.
func TestRun_MorningBackstopPeriodicFiresAndDelivers(t *testing.T) {
	workerNow := at(2026, 7, 6, 7, 5)
	r := newRig(t, models.NewFixedClock(workerNow))
	require.NoError(t, flywheel.UpsertPeriodic(ctxAt(at(2026, 7, 5, 12, 0)), r.db, flywheel.PeriodicSpec{
		Slug: slugMorningBackstop, Kind: kindMorningBackstop, Cron: "0 7 * * *", Queue: queueName, Active: true,
	}))

	tickCtx := ctxAt(workerNow)
	n, err := r.sched.Tick(tickCtx)
	require.NoError(t, err)
	require.Equal(t, 1, n, "the due morning backstop enqueues once")
	require.NoError(t, r.runner.RunUntilIdle(tickCtx))

	require.Len(t, r.del.sends, 1, "the morning backstop delivers the real card")
	assert.Equal(t, engine.ChannelUser, r.del.sends[0].channel)
	assert.Equal(t, backstopNote+"\n\nCOMPOSED", r.del.sends[0].text)
}

// --- Run: production wiring --------------------------------------------------

// TestRun_ReconcilesThenDrainsCleanly exercises the production Run wiring end to
// end: DB open/migrate, engine scaffold, periodic reconcile, node build/run. It
// waits until both periodics are reconciled (setup ran), then cancels and
// asserts the node drains to a clean nil return.
func TestRun_ReconcilesThenDrainsCleanly(t *testing.T) {
	store := newStore(t)
	dbPath := filepath.Join(t.TempDir(), "companion.db")
	// Build the companion config on the test goroutine (writePrompts asserts), so
	// only the assertion-free Run drives the spawned goroutine.
	companionCfg := writePrompts(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Store:    store,
			Notifier: &fakeDeliverer{},
			Numbers:  fakeNumbers{},
			Verdict:  fakeVerdict{},
			Config:   companionCfg,
			Provider: defaultProvider(),
			DBPath:   dbPath,
		})
	}()

	require.Eventually(t, func() bool {
		db, oerr := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: gormlogger.Discard})
		if oerr != nil {
			return false
		}
		defer closeGorm(db)
		views, lerr := flywheel.ListPeriodics(context.Background(), db)
		return lerr == nil && len(views) == 3
	}, 5*time.Second, 25*time.Millisecond, "Run reconciles the morning, night, and morning-backstop periodics")

	cancel()
	select {
	case rerr := <-done:
		require.NoError(t, rerr, "a canceled node drains cleanly")
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}

// TestRun_StartupSelfHealArmsTheCompanionPeriodics is the boot-time guarantee,
// driven through the production Run wiring: a node started against a store whose
// every definition was switched off comes up with all three sends armed. Seeding
// and the reconcile pass both defend this invariant — the test asserts the
// invariant rather than which of them repaired it, because the point of running
// both is that neither is load-bearing alone.
func TestRun_StartupSelfHealArmsTheCompanionPeriodics(t *testing.T) {
	store := newStore(t)
	companionCfg := writePrompts(t)
	dbPath := filepath.Join(t.TempDir(), "companion.db")

	// A fully parked store, seeded with the production marks so the boot
	// reconcile preserves each cursor rather than re-declaring the schedule.
	seedAt := at(2026, 7, 5, 12, 0)
	seed, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	require.NoError(t, flywheel.Migrate(seed))
	require.NoError(t, upsertPeriodics(ctxAt(seedAt), seed, store))
	for _, slug := range []string{slugMorning, slugNight, slugMorningBackstop} {
		require.NoError(t, flywheel.SetPeriodicActive(ctxAt(seedAt), seed, slug, false))
	}
	closeGorm(seed)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Store:    store,
			Notifier: &fakeDeliverer{},
			Numbers:  fakeNumbers{},
			Verdict:  fakeVerdict{},
			Config:   companionCfg,
			Provider: defaultProvider(),
			DBPath:   dbPath,
		})
	}()

	require.Eventually(t, func() bool {
		db, oerr := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: gormlogger.Discard})
		if oerr != nil {
			return false
		}
		defer closeGorm(db)
		views, lerr := flywheel.ListPeriodics(context.Background(), db)
		if lerr != nil || len(views) != 3 {
			return false
		}
		for _, v := range views {
			if !v.Active {
				return false
			}
		}
		return true
	}, 5*time.Second, 25*time.Millisecond, "a parked store boots back to armed sends")

	cancel()
	select {
	case rerr := <-done:
		require.NoError(t, rerr, "a canceled node drains cleanly")
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}

// TestRun_StartupSequenceReconcilesAfterSeeding pins the boot order at the unit
// level: seed the schedule, then repair it. Reconciling before the seed would
// inspect definitions that do not exist yet on a first boot; reconciling after
// means the pass runs against exactly the schedule the node is about to serve.
func TestRun_StartupSequenceReconcilesAfterSeeding(t *testing.T) {
	seedAt := at(2026, 7, 5, 12, 0)
	now := at(2026, 7, 5, 13, 0)
	store := newStore(t)
	db := newJobDB(t)

	require.NoError(t, upsertPeriodics(ctxAt(seedAt), db, store))
	// Drift the seed cannot have caused, but a boot must still not leave behind.
	require.NoError(t, flywheel.SetPeriodicActive(ctxAt(seedAt), db, slugMorningBackstop, false))

	report, err := Reconcile(ctxAt(now), db, ReconcileOptions{})
	require.NoError(t, err)
	require.Len(t, report.Reconciled, 1)
	assert.Equal(t, slugMorningBackstop, report.Reconciled[0].Slug)
}

// TestRun_StopDuringStartupIsACleanDrain: a stop signal that lands while the
// node is still seeding and repairing its schedule is an ordinary shutdown — the
// supervisor stops and restarts this daemon constantly, and a canceled boot
// reported as a failure would put a spurious error in the supervised log every
// time.
func TestRun_StopDuringStartupIsACleanDrain(t *testing.T) {
	store := newStore(t)
	companionCfg := writePrompts(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // stopped before the boot sequence can finish

	require.NoError(t, Run(ctx, Options{
		Store:    store,
		Notifier: &fakeDeliverer{},
		Numbers:  fakeNumbers{},
		Verdict:  fakeVerdict{},
		Config:   companionCfg,
		Provider: defaultProvider(),
		DBPath:   filepath.Join(t.TempDir(), "companion.db"),
	}))
}

// closeGorm closes a gorm handle's underlying pool so the poll loop leaks no FDs.
func closeGorm(db *gorm.DB) {
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

// TestRun_RejectsMissingCollaborators guards the required-field errors.
func TestRun_RejectsMissingCollaborators(t *testing.T) {
	require.ErrorIs(t, Run(context.Background(), Options{
		Notifier: &fakeDeliverer{}, Numbers: fakeNumbers{}, Verdict: fakeVerdict{},
	}), errNoStore)
	require.ErrorIs(t, Run(context.Background(), Options{
		Store: newStore(t), Numbers: fakeNumbers{}, Verdict: fakeVerdict{},
	}), errNoNotifier)
	require.ErrorIs(t, Run(context.Background(), Options{
		Store: newStore(t), Notifier: &fakeDeliverer{}, Verdict: fakeVerdict{},
	}), errNoNumbers)
	require.ErrorIs(t, Run(context.Background(), Options{
		Store: newStore(t), Notifier: &fakeDeliverer{}, Numbers: fakeNumbers{},
	}), errNoVerdict)
}

// --- helpers -----------------------------------------------------------------

// TestCronFromHM tables the clock-mark to cron translation and its rejections.
func TestCronFromHM(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "06:00", want: "0 6 * * *"},
		{in: "19:00", want: "0 19 * * *"},
		{in: "00:00", want: "0 0 * * *"},
		{in: "23:59", want: "59 23 * * *"},
		{in: "", wantErr: true},
		{in: "0600", wantErr: true},
		{in: "06:00:00", wantErr: true},
		{in: "24:00", wantErr: true},
		{in: "06:60", wantErr: true},
		{in: "aa:bb", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := cronFromHM(tc.in)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestAtClock builds the mark instant on now's date and rejects a malformed mark.
func TestAtClock(t *testing.T) {
	now := at(2026, 7, 6, 15, 42)
	got, err := atClock(now, "06:00")
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 7, 6, 6, 0, 0, 0, time.UTC), got)

	_, err = atClock(now, "nope")
	require.Error(t, err)
}

// TestDefaultDBPath covers the explicit > env > default precedence and that the
// default lands outside the ~/.lucid Ledger, distinct from the Engine's job DB.
func TestDefaultDBPath(t *testing.T) {
	explicit := filepath.Join(t.TempDir(), "custom.db")
	got, err := DefaultDBPath(explicit)
	require.NoError(t, err)
	assert.Equal(t, explicit, got, "an explicit path wins")

	envPath := filepath.Join(t.TempDir(), "env.db")
	t.Setenv(envCompanionDB, envPath)
	got, err = DefaultDBPath("")
	require.NoError(t, err)
	assert.Equal(t, envPath, got, "the env override is next")

	t.Setenv(envCompanionDB, "")
	got, err = DefaultDBPath("")
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(got, filepath.Join("lucid", "companion.db")), "the default lands under the config dir, got %q", got)
	assert.NotContains(t, got, filepath.Join(".lucid"), "the job DB is outside the Ledger")
}
