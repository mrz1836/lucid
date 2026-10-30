package contactweekly

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
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

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/contact"
	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/flynode"
	"github.com/mrz1836/lucid/internal/lucidtest"
	"github.com/mrz1836/lucid/internal/storage"
)

// --- test doubles -----------------------------------------------------------

// fakeDue is a scripted DueReader: it returns a fixed view (or an error) and
// records every call, so a test can assert the node reads the full view (never
// the wrap) and that an idempotent or stale path still reads it at most once.
type fakeDue struct {
	view  contact.DueView
	err   error
	calls int
	wraps []bool
}

func (f *fakeDue) ContactDue(_ time.Time, wrap bool) (contact.DueView, error) {
	f.calls++
	f.wraps = append(f.wraps, wrap)
	if f.err != nil {
		return contact.DueView{}, f.err
	}
	return f.view, nil
}

// The Monday post delivers text, so its tests drive the shared
// lucidtest.FakeDeliverer (SendReturningID / VerifyPresent / Send): Sends holds
// the posts, Alerts the alerts, Verifies every read-back id.

// --- fixtures ---------------------------------------------------------------

// mondayMark is the default Monday 09:00 mark of ISO week 2026-W45.
func mondayMark() time.Time { return time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC) }

const week45 = "2026-W45"

// sampleView is a synthetic week with one person due, one still open from
// before, and a birthday.
func sampleView() contact.DueView {
	return contact.DueView{
		Day: "2026-11-02", Week: week45, WeekStart: "2026-11-02", WeekEnd: "2026-11-08",
		DueThisWeek: []contact.DueRow{{
			PersonKey: "person_s-rivera", DisplayName: "Sam Rivera", Cadence: "4w", CadenceDays: 28,
			Human: "every 4 weeks", LastContact: "2026-09-28", WindowCloses: "2026-11-05",
		}},
		StillOpen: []contact.DueRow{{
			PersonKey: "person_a-kim", DisplayName: "Alex Kim", Cadence: "2w", CadenceDays: 14,
			Human: "every 2 weeks", WindowCloses: "2026-10-28",
		}},
		ComingUp: []contact.DueRow{{
			PersonKey: "person_c-park", DisplayName: "Casey Park", Cadence: "3w", CadenceDays: 21,
			Human: "every 3 weeks", LastContact: "2026-10-20", WindowCloses: "2026-11-10",
		}},
		Birthdays: []contact.BirthdayRow{{PersonKey: "person_j-lee", DisplayName: "Jordan Lee", Date: "2026-11-04"}},
	}
}

// samplePost is sampleView's post, exactly as ADR-0014 §8 shows it.
const samplePost = `Here's your week for staying in touch — no rush, the whole week is yours.

**Due this week**
- Sam Rivera (every 4 weeks), last caught up Sep 28

**Still open from before**
- Alex Kim (every 2 weeks), not logged yet

🎂 Birthdays this week: Jordan Lee (Wed Nov 4)`

// emptyView is a week with nothing to post: someone coming up next week only.
func emptyView() contact.DueView {
	v := sampleView()
	v.DueThisWeek, v.StillOpen, v.Birthdays = []contact.DueRow{}, []contact.DueRow{}, []contact.BirthdayRow{}
	return v
}

// runStore builds a scaffolded, isolated Ledger for the weekly receipt.
func runStore(t *testing.T) *storage.Adapter {
	t.Helper()
	_, store := lucidtest.Ledger(t, lucidtest.NestedHome(), lucidtest.WithEngine())
	return store
}

// newRunner wires a Runner over a scripted due reader + deliverer and a real
// store on the default Monday 09:00 mark.
func newRunner(t *testing.T, due DueReader, del Deliverer) (*Runner, *storage.Adapter) {
	t.Helper()
	store := runStore(t)
	return &Runner{due: due, deliver: del, store: store, weekday: 1, markHM: "09:00"}, store
}

// ctxAt returns a context carrying a fixed clock at t.
func ctxAt(t time.Time) context.Context {
	return models.WithClock(context.Background(), models.NewFixedClock(t))
}

// newJobDB opens and migrates a throwaway job DB.
func newJobDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "contact-weekly.db")), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	require.NoError(t, flywheel.Migrate(db))
	return db
}

// channelsOf lists the channel of every send and alert a fake captured.
func channelsOf(del *lucidtest.FakeDeliverer) []string {
	out := make([]string, 0, len(del.Sends)+len(del.Alerts))
	for _, s := range del.Sends {
		out = append(out, s.Channel)
	}
	for _, a := range del.Alerts {
		out = append(out, a.Channel)
	}
	return out
}

// --- Fire: on-time delivery + receipt ----------------------------------------

// TestFire_OnTime_DeliversToUserChannelAndWritesReceipt: a fire at the Monday
// mark reads the full due view (never the wrap), posts the fixed template to the
// user channel, reads it back, and persists a verified receipt keyed on the ISO
// week.
func TestFire_OnTime_DeliversToUserChannelAndWritesReceipt(t *testing.T) {
	due := &fakeDue{view: sampleView()}
	del := &lucidtest.FakeDeliverer{}
	r, store := newRunner(t, due, del)

	out, err := r.Fire(context.Background(), mondayMark())
	require.NoError(t, err)

	assert.True(t, out.Delivered)
	assert.Equal(t, week45, out.Week)
	assert.Equal(t, engine.ChannelUser, out.Channel)
	assert.Equal(t, samplePost, out.Text)
	require.Len(t, del.Sends, 1)
	assert.Equal(t, engine.ChannelUser, del.Sends[0].Channel)
	assert.Equal(t, samplePost, del.Sends[0].Text, "the post is the fixed template, byte for byte")
	assert.NotContains(t, del.Sends[0].Text, "Casey Park", "coming up next week shows only in the read view")
	assert.Equal(t, []string{out.MessageID}, del.Verifies, "the delivered id is read back")
	assert.Empty(t, del.Alerts, "a clean delivery raises no alert")
	assert.Equal(t, []bool{false}, due.wraps, "the post reads the full view, not the wrap")

	rec, ok, err := store.ReadContactWeeklyReceipt()
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, storage.ContactWeeklyReceipt{
		Week: week45, MessageID: out.MessageID, ChannelID: engine.ChannelUser, Verified: true,
		DeliveredAt: mondayMark().Format(time.RFC3339),
	}, rec)
}

// TestFire_PostsOnlyToUserChannel: across every path — a delivery, a stale
// skip, a failed send, a failed read-back, an unreadable due view — the fake
// records each channel the node touched, and only the user channel ever
// appears, alerts included. No code path addresses a friend.
func TestFire_PostsOnlyToUserChannel(t *testing.T) {
	cases := map[string]struct {
		del *lucidtest.FakeDeliverer
		due *fakeDue
		at  time.Time
	}{
		"on time":          {&lucidtest.FakeDeliverer{}, &fakeDue{view: sampleView()}, mondayMark()},
		"past the cut-off": {&lucidtest.FakeDeliverer{}, &fakeDue{view: sampleView()}, mondayMark().Add(49 * time.Hour)},
		"send fails":       {&lucidtest.FakeDeliverer{SendErr: errors.New("discord 503")}, &fakeDue{view: sampleView()}, mondayMark()},
		"read-back fails":  {&lucidtest.FakeDeliverer{VerifyErr: errors.New("discord 503")}, &fakeDue{view: sampleView()}, mondayMark()},
		"due view unread":  {&lucidtest.FakeDeliverer{}, &fakeDue{err: errors.New("people tree unreadable")}, mondayMark()},
		"nothing to post":  {&lucidtest.FakeDeliverer{}, &fakeDue{view: emptyView()}, mondayMark()},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r, _ := newRunner(t, tc.due, tc.del)
			_, _ = r.Fire(context.Background(), tc.at)
			for _, ch := range channelsOf(tc.del) {
				assert.Equal(t, engine.ChannelUser, ch, "the Monday post and its alerts go only to the user channel")
			}
		})
	}
}

// --- Fire: the empty week ------------------------------------------------------

// TestFire_NothingDue_SkipsWithoutSending: with both lists and the birthdays
// line empty, the node returns before the send path — no send, no alert, no
// receipt — and the job run records the nothing-due marker and one info line.
func TestFire_NothingDue_SkipsWithoutSending(t *testing.T) {
	due := &fakeDue{view: emptyView()}
	del := &lucidtest.FakeDeliverer{}
	r, store := newRunner(t, due, del)

	out, err := r.Fire(context.Background(), mondayMark())
	require.NoError(t, err)
	assert.True(t, out.Skipped)
	assert.Equal(t, SkipNothingDue, out.SkipReason)
	assert.Equal(t, week45, out.Week)
	assert.False(t, out.Delivered)
	assert.Empty(t, del.Sends, "an empty week posts nothing")
	assert.Empty(t, del.Alerts, "an empty week is designed silence, not an alert")
	assert.Empty(t, del.Verifies)
	_, ok, err := store.ReadContactWeeklyReceipt()
	require.NoError(t, err)
	assert.False(t, ok, "an empty week writes no receipt")

	// Past the cut-off an empty week is still silent: there was nothing to post.
	out, err = r.Fire(context.Background(), mondayMark().Add(72*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, SkipNothingDue, out.SkipReason)
	assert.Empty(t, del.Alerts)

	var buf bytes.Buffer
	r.log = slog.New(slog.NewJSONHandler(&buf, nil))
	res, err := weeklyWorker{r: r, clock: models.NewFixedClock(mondayMark())}.Work(ctxAt(mondayMark()), nil)
	require.NoError(t, err)
	b, err := json.Marshal(res.Output)
	require.NoError(t, err)
	assert.JSONEq(t, `{"skipped": true, "skip_reason": "nothing-due", "week": "2026-W45"}`, string(b))
	assert.Contains(t, buf.String(), `"level":"INFO"`)
	assert.Contains(t, buf.String(), `"msg":"contact weekly: nothing due — no post"`)
	assert.Contains(t, buf.String(), `"week":"2026-W45"`)

	var jobBuf bytes.Buffer
	job := &flywheel.Job[weeklyArgs]{Logger: slog.New(slog.NewJSONHandler(&jobBuf, nil))}
	_, err = weeklyWorker{r: r, clock: models.NewFixedClock(mondayMark())}.Work(ctxAt(mondayMark()), job)
	require.NoError(t, err)
	assert.Contains(t, jobBuf.String(), "contact weekly: nothing due — no post", "the job's own logger carries the line")
	assert.Empty(t, del.Sends)
}

// TestWork_DeliveredWeekRecordsNoMarker: a delivered post returns the empty
// job result — the nothing-due marker belongs only to an empty week.
func TestWork_DeliveredWeekRecordsNoMarker(t *testing.T) {
	del := &lucidtest.FakeDeliverer{}
	r, _ := newRunner(t, &fakeDue{view: sampleView()}, del)
	res, err := weeklyWorker{r: r, clock: models.NewFixedClock(mondayMark())}.Work(ctxAt(mondayMark()), nil)
	require.NoError(t, err)
	assert.Nil(t, res.Output)
	require.Len(t, del.Sends, 1)
}

// --- Fire: idempotency -----------------------------------------------------------

// TestFire_RetrySameWeek_IsIdempotentSkip: a second fire in the same ISO week,
// whose receipt's message still reads back, is an idempotent skip — no second
// send.
func TestFire_RetrySameWeek_IsIdempotentSkip(t *testing.T) {
	del := &lucidtest.FakeDeliverer{}
	r, _ := newRunner(t, &fakeDue{view: sampleView()}, del)

	first, err := r.Fire(context.Background(), mondayMark())
	require.NoError(t, err)

	again, err := r.Fire(context.Background(), mondayMark().Add(2*time.Hour))
	require.NoError(t, err)
	assert.True(t, again.Skipped)
	assert.Equal(t, flynode.SkipAlreadyDelivered, again.SkipReason)
	assert.Equal(t, first.MessageID, again.MessageID)
	assert.Len(t, del.Sends, 1, "the retry re-uses the receipt; no second post")
	assert.Empty(t, del.Alerts)
}

// TestFire_NextWeekDeliversAgain: the receipt guards only its own ISO week, so
// next Monday's fire posts the new week's list.
func TestFire_NextWeekDeliversAgain(t *testing.T) {
	del := &lucidtest.FakeDeliverer{}
	r, store := newRunner(t, &fakeDue{view: sampleView()}, del)

	_, err := r.Fire(context.Background(), mondayMark())
	require.NoError(t, err)
	next, err := r.Fire(context.Background(), mondayMark().AddDate(0, 0, 7))
	require.NoError(t, err)
	assert.True(t, next.Delivered)
	assert.Equal(t, "2026-W46", next.Week)
	assert.Len(t, del.Sends, 2)

	rec, _, err := store.ReadContactWeeklyReceipt()
	require.NoError(t, err)
	assert.Equal(t, "2026-W46", rec.Week, "the receipt now names the new week")
}

// TestFire_ReceiptMessageGone_ReDelivers: a receipt whose message is provably
// gone (a clean 404) falls through to a fresh post, so the week is never left
// silently empty.
func TestFire_ReceiptMessageGone_ReDelivers(t *testing.T) {
	del := &lucidtest.FakeDeliverer{VerifyErrFor: map[string]error{}}
	r, store := newRunner(t, &fakeDue{view: sampleView()}, del)

	first, err := r.Fire(context.Background(), mondayMark())
	require.NoError(t, err)

	del.VerifyErrFor[first.MessageID] = fmt.Errorf("404 unknown message: %w", flynode.ErrMessageAbsent)
	again, err := r.Fire(context.Background(), mondayMark().Add(3*time.Hour))
	require.NoError(t, err)
	assert.True(t, again.Delivered)
	assert.NotEqual(t, first.MessageID, again.MessageID)
	assert.Len(t, del.Sends, 2, "a receipt whose message is gone re-delivers")

	rec, _, err := store.ReadContactWeeklyReceipt()
	require.NoError(t, err)
	assert.Equal(t, again.MessageID, rec.MessageID, "the receipt names the fresh post")
}

// TestFire_ReceiptReadBackUnknown_AlertsWithoutReposting: a receipt whose
// read-back is indeterminate (a transport blip) is neither honored nor
// re-posted — the node alerts and returns the retryable error.
func TestFire_ReceiptReadBackUnknown_AlertsWithoutReposting(t *testing.T) {
	del := &lucidtest.FakeDeliverer{VerifyErrFor: map[string]error{}}
	r, _ := newRunner(t, &fakeDue{view: sampleView()}, del)

	first, err := r.Fire(context.Background(), mondayMark())
	require.NoError(t, err)

	del.VerifyErrFor[first.MessageID] = errors.New("discord 503")
	_, err = r.Fire(context.Background(), mondayMark().Add(time.Hour))
	require.Error(t, err)
	assert.Len(t, del.Sends, 1, "an unknown read-back never re-posts")
	require.Len(t, del.Alerts, 1)
	assert.Equal(t, alertGuard, del.Alerts[0].Text)
}

// --- Fire: the late attempt and the cut-off ---------------------------------------

// TestFire_WithinCutoff_LateAttemptStillDelivers: the one bounded late attempt
// after the host slept through the Monday mark — Tuesday, still inside the 48h
// window — delivers the week's list as usual, with no alert.
func TestFire_WithinCutoff_LateAttemptStillDelivers(t *testing.T) {
	for _, late := range []time.Duration{26 * time.Hour, 47*time.Hour + 59*time.Minute} {
		t.Run(late.String(), func(t *testing.T) {
			del := &lucidtest.FakeDeliverer{}
			r, store := newRunner(t, &fakeDue{view: sampleView()}, del)

			out, err := r.Fire(context.Background(), mondayMark().Add(late))
			require.NoError(t, err)
			assert.True(t, out.Delivered)
			assert.Equal(t, week45, out.Week)
			require.Len(t, del.Sends, 1)
			assert.Equal(t, samplePost, del.Sends[0].Text, "a late attempt posts the same fixed template")
			assert.Empty(t, del.Alerts)
			_, ok, err := store.ReadContactWeeklyReceipt()
			require.NoError(t, err)
			assert.True(t, ok)
		})
	}
}

// TestFire_PastCutoff_SkipsAndAlerts: past the cut-off (the Monday mark + 48h)
// the node refuses to post a list for a week already days gone — it skips,
// sends one calm alert to the user channel naming the week and pointing at the
// read view, and writes no receipt.
func TestFire_PastCutoff_SkipsAndAlerts(t *testing.T) {
	for _, at := range []time.Duration{48 * time.Hour, 49 * time.Hour} {
		t.Run(at.String(), func(t *testing.T) {
			del := &lucidtest.FakeDeliverer{}
			r, store := newRunner(t, &fakeDue{view: sampleView()}, del)

			out, err := r.Fire(context.Background(), mondayMark().Add(at))
			require.NoError(t, err)
			assert.True(t, out.Skipped)
			assert.Equal(t, flynode.SkipPastCutoff, out.SkipReason)
			assert.Empty(t, del.Sends, "no stale list is posted past the cut-off")
			require.Len(t, del.Alerts, 1, "past the cut-off the user is told instead of left silent")
			assert.Equal(t, engine.ChannelUser, del.Alerts[0].Channel)
			assert.Contains(t, del.Alerts[0].Text, week45, "the alert names the week")
			assert.Contains(t, del.Alerts[0].Text, "`lucid contact due`", "the alert points at the read view")

			_, ok, err := store.ReadContactWeeklyReceipt()
			require.NoError(t, err)
			assert.False(t, ok, "a skipped week writes no receipt")
		})
	}
}

// --- Fire: never silent -------------------------------------------------------------

// TestFire_SendError_AlertsAndErrors: a total send failure alerts and returns a
// loud error, and writes no receipt, so a retry posts again.
func TestFire_SendError_AlertsAndErrors(t *testing.T) {
	del := &lucidtest.FakeDeliverer{SendErr: errors.New("discord 503")}
	r, store := newRunner(t, &fakeDue{view: sampleView()}, del)

	_, err := r.Fire(context.Background(), mondayMark())
	require.Error(t, err)
	require.Len(t, del.Alerts, 1, "a total send failure alerts")
	assert.Equal(t, alertSend, del.Alerts[0].Text)
	_, ok, rerr := store.ReadContactWeeklyReceipt()
	require.NoError(t, rerr)
	assert.False(t, ok, "a failed send writes no receipt")

	// A failed alert is logged, never masking the send error.
	var buf bytes.Buffer
	del.AlertErr = errors.New("alert channel down")
	r.log = slog.New(slog.NewJSONHandler(&buf, nil))
	_, err = r.Fire(context.Background(), mondayMark())
	require.ErrorContains(t, err, "discord 503")
	assert.Contains(t, buf.String(), "alert send failed")
}

// TestFire_VerifyError_AlertsAndErrors: a post that will not read back is a
// loud failure — alert, error, no receipt.
func TestFire_VerifyError_AlertsAndErrors(t *testing.T) {
	del := &lucidtest.FakeDeliverer{VerifyErr: errors.New("404 after send")}
	r, store := newRunner(t, &fakeDue{view: sampleView()}, del)

	_, err := r.Fire(context.Background(), mondayMark())
	require.Error(t, err)
	require.Len(t, del.Alerts, 1)
	assert.Equal(t, alertVerify, del.Alerts[0].Text)
	_, ok, rerr := store.ReadContactWeeklyReceipt()
	require.NoError(t, rerr)
	assert.False(t, ok, "an unverified post writes no receipt")
}

// TestFire_DueReadError_AlertsAndErrors: a due view that cannot be read alerts
// and errors rather than posting nothing in silence.
func TestFire_DueReadError_AlertsAndErrors(t *testing.T) {
	del := &lucidtest.FakeDeliverer{}
	r, _ := newRunner(t, &fakeDue{err: errors.New("people tree unreadable")}, del)

	_, err := r.Fire(context.Background(), mondayMark())
	require.ErrorContains(t, err, "people tree unreadable")
	assert.Empty(t, del.Sends)
	require.Len(t, del.Alerts, 1)
	assert.Equal(t, alertCompose, del.Alerts[0].Text)
}

// TestFire_MalformedMark_Errors: a Runner with a malformed clock mark rejects
// the fire rather than mis-computing the cut-off.
func TestFire_MalformedMark_Errors(t *testing.T) {
	r, _ := newRunner(t, &fakeDue{view: sampleView()}, &lucidtest.FakeDeliverer{})
	r.markHM = "nope"
	_, err := r.Fire(context.Background(), mondayMark())
	require.Error(t, err)
}

// calmDeny is a local copy of the contact ethos deny-list (internal/contact's
// render tests own the full one): no alert may speak of lateness, misses, or
// failure as a judgment.
var calmDeny = regexp.MustCompile(`(?i)\b(overdue|late|behind|missed|failed|neglect|streak|score|forgot)s?\b|%|days ago|should have`)

// TestAlertCopy_IsCalm: every alert the node can send reads calmly.
func TestAlertCopy_IsCalm(t *testing.T) {
	for _, a := range []string{
		fmt.Sprintf(alertCutoff, week45), alertCompose, alertSend, alertVerify, alertGuard, alertReceipt,
	} {
		assert.Empty(t, calmDeny.FindAllString(a, -1), "alert copy: %q", a)
		assert.True(t, strings.HasPrefix(a, "Lucid "), "alerts name who is speaking: %q", a)
	}
}

// --- markFor / weeklyCron --------------------------------------------------------------

// TestMarkFor: the scheduled mark is the configured weekday of now's ISO week
// at the configured time, whichever day the (possibly late) fire runs.
func TestMarkFor(t *testing.T) {
	monday := &Runner{weekday: 1, markHM: "09:30"}
	got, err := monday.markFor(mondayMark().Add(30 * time.Hour))
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC), got)

	sunday := &Runner{weekday: 0, markHM: "18:00"}
	got, err = sunday.markFor(mondayMark())
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 11, 8, 18, 0, 0, 0, time.UTC), got, "weekday 0 is the end of the ISO week")
}

// TestWeeklyCron tables the mark+weekday to cron translation and its rejections.
func TestWeeklyCron(t *testing.T) {
	cases := []struct {
		hm      string
		weekday int
		want    string
		wantErr bool
	}{
		{hm: "09:00", weekday: 1, want: "0 9 * * 1"},
		{hm: "09:30", weekday: 1, want: "30 9 * * 1"},
		{hm: "18:05", weekday: 0, want: "5 18 * * 0"},
		{hm: "", weekday: 1, wantErr: true},
		{hm: "25:00", weekday: 1, wantErr: true},
		{hm: "09:00", weekday: -1, wantErr: true},
		{hm: "09:00", weekday: 7, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s/%d", tc.hm, tc.weekday), func(t *testing.T) {
			got, err := weeklyCron(tc.hm, tc.weekday)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// --- Node wiring: the one weekly periodic -------------------------------------------

// TestUpsertWeeklyPeriodic_RegistersOneMondayCron: reconciling the default
// block registers exactly one periodic — the Monday 09:00 mark, active on the
// node's own queue. There is no Sunday periodic: the wrap is a read view.
func TestUpsertWeeklyPeriodic_RegistersOneMondayCron(t *testing.T) {
	db := newJobDB(t)
	ctx := ctxAt(mondayMark())

	require.NoError(t, upsertWeeklyPeriodic(ctx, db, config.DefaultContactWeekly()))

	views, err := flywheel.ListPeriodics(ctx, db)
	require.NoError(t, err)
	require.Len(t, views, 1, "exactly one periodic — Lucid adds no Sunday post")
	assert.Equal(t, "lucid-contact-weekly", views[0].Slug)
	assert.Equal(t, kindWeekly, views[0].Kind)
	assert.Equal(t, "0 9 * * 1", views[0].Cron)
	assert.Equal(t, queueName, views[0].Queue)
	assert.True(t, views[0].Active)
}

// TestUpsertWeeklyPeriodic_IsIdempotentAcrossRestart: re-reconciling never
// duplicates the definition, and a changed mark moves it in place.
func TestUpsertWeeklyPeriodic_IsIdempotentAcrossRestart(t *testing.T) {
	db := newJobDB(t)
	cfg := config.ContactWeeklyConfig{Enabled: true, Weekday: 1, Time: "09:00"}

	require.NoError(t, upsertWeeklyPeriodic(ctxAt(mondayMark()), db, cfg))
	require.NoError(t, upsertWeeklyPeriodic(ctxAt(mondayMark().AddDate(0, 0, 7)), db, cfg))

	views, err := flywheel.ListPeriodics(ctxAt(mondayMark()), db)
	require.NoError(t, err)
	require.Len(t, views, 1, "no duplicate definition after a restart reconcile")

	cfg.Time = "09:30"
	require.NoError(t, upsertWeeklyPeriodic(ctxAt(mondayMark()), db, cfg))
	views, err = flywheel.ListPeriodics(ctxAt(mondayMark()), db)
	require.NoError(t, err)
	require.Len(t, views, 1)
	assert.Equal(t, "30 9 * * 1", views[0].Cron, "a moved mark updates the one periodic")
}

// TestUpsertWeeklyPeriodic_RejectsMalformedConfig: a bogus time or weekday is
// rejected at reconcile rather than scheduled.
func TestUpsertWeeklyPeriodic_RejectsMalformedConfig(t *testing.T) {
	db := newJobDB(t)
	require.Error(t, upsertWeeklyPeriodic(ctxAt(mondayMark()), db, config.ContactWeeklyConfig{Time: "24:61", Weekday: 1}))
	require.Error(t, upsertWeeklyPeriodic(ctxAt(mondayMark()), db, config.ContactWeeklyConfig{Time: "09:00", Weekday: 9}))
}

// --- Node wiring: end-to-end through the flywheel machinery -----------------------------

// rig is a flywheel runtime over an isolated Ledger and a throwaway job DB:
// the scheduler (BackfillCap 1) and a runner share one registry.
type rig struct {
	db     *gorm.DB
	store  *storage.Adapter
	del    *lucidtest.FakeDeliverer
	sched  *flywheel.Scheduler
	runner *flywheel.Runner
}

func newRig(t *testing.T, clock models.Clock, due DueReader) *rig {
	t.Helper()
	store := runStore(t)
	db := newJobDB(t)
	del := &lucidtest.FakeDeliverer{}
	r := &Runner{due: due, deliver: del, store: store, weekday: 1, markHM: "09:00"}
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
	return &rig{db: db, store: store, del: del, sched: sched, runner: runner}
}

// seedPeriodic declares the Monday periodic as of the Sunday before the mark,
// so the mark is the periodic's next run.
func (g *rig) seedPeriodic(t *testing.T) {
	t.Helper()
	require.NoError(t, upsertWeeklyPeriodic(ctxAt(mondayMark().Add(-time.Hour)), g.db, config.DefaultContactWeekly()))
}

// TestRun_WeeklyPeriodicFiresAndDelivers: the due Monday periodic enqueues
// once and the runner posts the list to the user channel.
func TestRun_WeeklyPeriodicFiresAndDelivers(t *testing.T) {
	now := mondayMark().Add(5 * time.Minute)
	g := newRig(t, models.NewFixedClock(now), &fakeDue{view: sampleView()})
	g.seedPeriodic(t)

	n, err := g.sched.Tick(ctxAt(now))
	require.NoError(t, err)
	require.Equal(t, 1, n, "the due weekly periodic enqueues once")
	require.NoError(t, g.runner.RunUntilIdle(ctxAt(now)))

	require.Len(t, g.del.Sends, 1)
	assert.Equal(t, engine.ChannelUser, g.del.Sends[0].Channel)
	rec, ok, err := g.store.ReadContactWeeklyReceipt()
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, week45, rec.Week)
}

// TestRun_HostAsleepAcrossMark_OneLateAttempt: a host asleep through the
// Monday mark wakes Tuesday to exactly one late attempt (the bounded backfill),
// which delivers inside the cut-off; a further tick that day enqueues nothing.
func TestRun_HostAsleepAcrossMark_OneLateAttempt(t *testing.T) {
	wake := mondayMark().Add(25 * time.Hour) // Tuesday 10:00
	g := newRig(t, models.NewFixedClock(wake), &fakeDue{view: sampleView()})
	g.seedPeriodic(t)

	n, err := g.sched.Tick(ctxAt(wake))
	require.NoError(t, err)
	require.Equal(t, 1, n, "one late attempt, not a replay")
	require.NoError(t, g.runner.RunUntilIdle(ctxAt(wake)))
	require.Len(t, g.del.Sends, 1, "the late attempt delivers inside the cut-off")
	assert.Empty(t, g.del.Alerts)

	n, err = g.sched.Tick(ctxAt(wake.Add(5 * time.Minute)))
	require.NoError(t, err)
	assert.Zero(t, n, "nothing more is enqueued until next Monday")
}

// --- Run: production wiring --------------------------------------------------------------

// TestRun_ReconcilesThenDrainsCleanly exercises the production Run wiring:
// DB open/migrate, engine scaffold, periodic reconcile, node build/run, then a
// cancel drains it to a clean nil.
func TestRun_ReconcilesThenDrainsCleanly(t *testing.T) {
	store := runStore(t)
	dbPath := filepath.Join(t.TempDir(), "contact-weekly.db")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Store:    store,
			Config:   config.ContactWeeklyConfig{Enabled: true, Weekday: 1, Time: "09:00"},
			Due:      &fakeDue{view: emptyView()},
			Notifier: &lucidtest.FakeDeliverer{},
			DBPath:   dbPath,
		})
	}()

	require.Eventually(t, func() bool {
		db, oerr := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: gormlogger.Discard})
		if oerr != nil {
			return false
		}
		defer func() {
			if sqlDB, e := db.DB(); e == nil {
				_ = sqlDB.Close()
			}
		}()
		views, lerr := flywheel.ListPeriodics(context.Background(), db)
		return lerr == nil && len(views) == 1 && views[0].Slug == slugWeekly
	}, 5*time.Second, 25*time.Millisecond, "Run reconciles the weekly periodic")

	cancel()
	select {
	case rerr := <-done:
		require.NoError(t, rerr, "a canceled node drains cleanly")
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}

// TestRun_RejectsMissingCollaborators guards the required-field errors.
func TestRun_RejectsMissingCollaborators(t *testing.T) {
	store := runStore(t)
	due := &fakeDue{view: sampleView()}
	del := &lucidtest.FakeDeliverer{}
	require.ErrorIs(t, Run(context.Background(), Options{Notifier: del, Due: due}), errNoStore)
	require.ErrorIs(t, Run(context.Background(), Options{Store: store, Due: due}), errNoNotifier)
	require.ErrorIs(t, Run(context.Background(), Options{Store: store, Notifier: del}), errNoDue)
}

// TestDefaultDBPath covers the explicit > env > default precedence and that the
// default lands outside the ~/.lucid Ledger under its own file name.
func TestDefaultDBPath(t *testing.T) {
	explicit := filepath.Join(t.TempDir(), "custom.db")
	got, err := DefaultDBPath(explicit)
	require.NoError(t, err)
	assert.Equal(t, explicit, got, "an explicit path wins")

	envPath := filepath.Join(t.TempDir(), "env.db")
	t.Setenv("LUCID_CONTACT_WEEKLY_DB", envPath)
	got, err = DefaultDBPath("")
	require.NoError(t, err)
	assert.Equal(t, envPath, got, "LUCID_CONTACT_WEEKLY_DB is next")

	t.Setenv("LUCID_CONTACT_WEEKLY_DB", "")
	got, err = DefaultDBPath("")
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(got, filepath.Join("lucid", "contact-weekly.db")), "the default lands under the config dir, got %q", got)
	assert.NotContains(t, got, ".lucid", "the job DB is outside the Ledger")
}
