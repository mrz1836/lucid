package scheduler

import (
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/engine/templates"
	"github.com/mrz1836/lucid/internal/lucidtest"
	"github.com/mrz1836/lucid/internal/storage"
)

// capture is one delivered send recorded by the fake notifier.
type capture struct{ channel, text string }

// fakeNotifier captures every delivered send and can be told to fail a given
// logical channel — the seam for the "witness unreachable" fallback.
type fakeNotifier struct {
	sent   []capture
	failOn map[string]bool
}

func (f *fakeNotifier) Send(channel, text string) error {
	if f.failOn[channel] {
		return fmt.Errorf("unreachable channel %q", channel)
	}
	f.sent = append(f.sent, capture{channel, text})
	return nil
}

func (f *fakeNotifier) count(channel string) int {
	n := 0
	for _, c := range f.sent {
		if c.channel == channel {
			n++
		}
	}
	return n
}

func (f *fakeNotifier) first(channel string) (capture, bool) {
	for _, c := range f.sent {
		if c.channel == channel {
			return c, true
		}
	}
	return capture{}, false
}

// newSched returns a scheduler over a fresh scaffolded Ledger plus its adapter
// and fake notifier.
func newSched(t *testing.T) (*Scheduler, *storage.Adapter, *fakeNotifier) {
	t.Helper()
	_, a := lucidtest.Ledger(t, lucidtest.NestedHome(), lucidtest.WithEngine())
	n := &fakeNotifier{failOn: map[string]bool{}}
	return New(a, n), a, n
}

//nolint:unparam // y is kept explicit for fixture readability even though the cases share 2026
func at(y int, mo time.Month, d, hh, mm int) time.Time {
	return time.Date(y, mo, d, hh, mm, 0, 0, time.UTC)
}

func completedRec(date string) engine.DayRecord {
	d, _ := time.Parse("2006-01-02", date)
	return engine.DayRecord{
		DayID: engine.DayID(d), LogicalDate: date, RecordedAt: date + "T22:00:00Z",
		Mode: engine.ModeGreen, Links: map[string]string{"journal": engine.StatusDone},
		Completed: true, Profile: engine.DefaultProfile, Corrections: []engine.Correction{},
	}
}

// missedRec carries a limiter tag and capacity — Engine telemetry the witness
// L2 must never leak (engine-module.md §Day record).
//
//nolint:unparam // date is kept explicit for fixture readability even though the cases share a date
func missedRec(date string, storm bool) engine.DayRecord {
	d, _ := time.Parse("2006-01-02", date)
	return engine.DayRecord{
		DayID: engine.DayID(d), LogicalDate: date, RecordedAt: date + "T09:00:00Z",
		Mode: engine.ModeGreen, Links: map[string]string{}, Missed: true, Storm: storm,
		LimiterTag: "secrettag", Capacity: 4, Profile: engine.DefaultProfile, Corrections: []engine.Correction{},
	}
}

// seed writes day records and stamps chain_start (via a rebuild) so the
// escalation ladder is active, as it is after any real first close-out.
func seed(t *testing.T, a *storage.Adapter, recs ...engine.DayRecord) {
	t.Helper()
	for _, r := range recs {
		require.NoError(t, a.WriteEngineDay(r))
	}
	_, err := a.RebuildEngineStatus(time.UTC)
	require.NoError(t, err)
}

func armWitness(t *testing.T, a *storage.Adapter) {
	t.Helper()
	confirmedAt := "2026-07-01T18:20:00Z"
	require.NoError(t, a.WriteWitnessContract(engine.WitnessContract{
		WitnessName: "J.", ConfirmedAt: &confirmedAt, L2Enabled: true,
		StatusHistory: []engine.WitnessTransition{{Status: engine.WitnessConfirmed}},
	}))
}

func seedStandingStorm(t *testing.T, a *storage.Adapter) {
	t.Helper()
	require.NoError(t, a.AppendStormEvents(
		engine.StormEvent{At: "2026-07-01T08:00:00Z", Event: engine.StormDeclared, Label: "clause-1"},
		engine.StormEvent{At: "2026-07-01T09:00:00Z", Event: engine.StormConfirmed, Through: "2026-07-28"},
	))
}

// ── Bell ──────────────────────────────────────────────────────────────────

// TestRunBell posts the chain label to the user channel with no sign-off.
func TestRunBell(t *testing.T) {
	sc, _, n := newSched(t)
	msg, err := sc.RunBell()
	require.NoError(t, err)
	assert.Equal(t, engine.ChannelUser, msg.Channel)
	assert.Contains(t, msg.Text, "Journal. Dock. Read.")
	assert.False(t, strings.HasSuffix(msg.Text, templates.SignOff), "the bell does not sign off")
	assert.Equal(t, 1, n.count(engine.ChannelUser))
}

// TestRunBell_DisabledSendsNothing: a bell disabled in chain.json is silent.
func TestRunBell_DisabledSendsNothing(t *testing.T) {
	sc, a, n := newSched(t)
	chain, err := a.ReadChainConfig()
	require.NoError(t, err)
	chain.Bell.Enabled = false
	require.NoError(t, a.WriteChainConfig(chain))

	msg, err := sc.RunBell()
	require.NoError(t, err)
	assert.Empty(t, msg.Text)
	assert.Zero(t, n.count(engine.ChannelUser))
}

// ── Tripwire escalation ladder ──────────────────────────────────────────────

// TestTripwire_CompletedNoSend: a completed yesterday sends nothing.
func TestTripwire_CompletedNoSend(t *testing.T) {
	sc, a, n := newSched(t)
	seed(t, a, completedRec("2026-07-05"))
	rep, err := sc.RunTripwire(at(2026, 7, 6, 9, 0))
	require.NoError(t, err)
	assert.Empty(t, rep.Sends)
	assert.Empty(t, n.sent)
	assert.Equal(t, engine.EscalationNone, rep.Escalation)
}

// TestTripwire_OneMissFiresOneL1: one miss ⇒ exactly one L1 to the user with
// the survival floor named, and escalation reaches l1_fired.
func TestTripwire_OneMissFiresOneL1(t *testing.T) {
	sc, a, n := newSched(t)
	seed(t, a, completedRec("2026-07-04")) // 07-05 absent
	rep, err := sc.RunTripwire(at(2026, 7, 6, 9, 0))
	require.NoError(t, err)

	require.Len(t, rep.Sends, 1)
	assert.Equal(t, engine.ChannelUser, rep.Sends[0].Channel)
	assert.Equal(t, engine.SendL1, rep.Sends[0].Kind)
	assert.Contains(t, rep.Sends[0].Text, "one line, spoken or typed", "the floor is named")
	assert.True(t, strings.HasSuffix(rep.Sends[0].Text, templates.SignOff))
	assert.Equal(t, 1, n.count(engine.ChannelUser))
	assert.Equal(t, engine.EscalationL1, rep.Escalation)

	st, err := a.ReadEngineStatus()
	require.NoError(t, err)
	assert.Equal(t, engine.EscalationL1, st.EscalationState, "escalation persists to status.json")
}

// TestTripwire_TwoConsecutiveFiresOneL2WitnessNoLeak: two consecutive misses ⇒
// exactly one L2 to the witness carrying zero journal/capacity bytes.
func TestTripwire_TwoConsecutiveFiresOneL2WitnessNoLeak(t *testing.T) {
	sc, a, n := newSched(t)
	armWitness(t, a)
	seed(t, a, completedRec("2026-07-03"), missedRec("2026-07-04", false)) // 07-05 absent

	rep, err := sc.RunTripwire(at(2026, 7, 6, 9, 0))
	require.NoError(t, err)

	require.Len(t, rep.Sends, 1, "exactly one L2, heartbeat suppressed")
	assert.Equal(t, engine.ChannelWitness, rep.Sends[0].Channel)
	assert.Equal(t, engine.SendL2, rep.Sends[0].Kind)
	assert.Equal(t, 1, n.count(engine.ChannelWitness))
	assert.Zero(t, n.count(engine.ChannelUser), "nothing to the user on an L2")

	// The witness payload carries streak/mode only — grep it for Mirror data.
	payload, _ := n.first(engine.ChannelWitness)
	assert.NotContains(t, payload.text, "secrettag", "the limiter tag never reaches the witness")
	assert.NotContains(t, strings.ToLower(payload.text), "capacity")
	assert.NotContains(t, strings.ToLower(payload.text), "journal")
	assert.Equal(t, engine.EscalationL2, rep.Escalation)
}

// TestTripwire_L2BlockedWhenWitnessUnconfirmed: the L2 stage with an
// unconfirmed witness is blocked and the user is notified; escalation still
// reaches l2_fired.
func TestTripwire_L2BlockedWhenWitnessUnconfirmed(t *testing.T) {
	sc, a, n := newSched(t)
	seed(t, a, completedRec("2026-07-03"), missedRec("2026-07-04", false))

	rep, err := sc.RunTripwire(at(2026, 7, 6, 9, 0))
	require.NoError(t, err)

	require.Len(t, rep.Sends, 1)
	assert.Equal(t, engine.ChannelUser, rep.Sends[0].Channel)
	assert.Equal(t, engine.SendL2Blocked, rep.Sends[0].Kind)
	assert.Contains(t, rep.Sends[0].Text, "disarmed")
	assert.Zero(t, n.count(engine.ChannelWitness), "nothing reaches an unconfirmed witness")
	assert.Equal(t, engine.EscalationL2, rep.Escalation)
}

// TestTripwire_WitnessUnreachableFallsBack: an L2 whose witness channel is
// unreachable falls back to a user-channel "you owe the message" note, and
// escalation is still l2_fired.
func TestTripwire_WitnessUnreachableFallsBack(t *testing.T) {
	sc, a, n := newSched(t)
	armWitness(t, a)
	n.failOn[engine.ChannelWitness] = true
	seed(t, a, completedRec("2026-07-03"), missedRec("2026-07-04", false))

	rep, err := sc.RunTripwire(at(2026, 7, 6, 9, 0))
	require.NoError(t, err)

	require.Len(t, rep.Sends, 1)
	assert.Equal(t, engine.ChannelUser, rep.Sends[0].Channel)
	assert.Contains(t, rep.Sends[0].Text, "you owe the message")
	assert.Equal(t, engine.EscalationL2, rep.Escalation, "the escalation fired even though delivery failed")
}

// TestTripwire_RecoveryResetsAndDoesNotRefire: after a completed recovery day,
// the next run resets the ladder and does not re-fire the prior L1.
func TestTripwire_RecoveryResetsAndDoesNotRefire(t *testing.T) {
	sc, a, n := newSched(t)
	seed(t, a, completedRec("2026-07-03"), missedRec("2026-07-04", false), completedRec("2026-07-05"))

	// Run on 07-06 evaluates 07-05 (completed) → nothing.
	rep, err := sc.RunTripwire(at(2026, 7, 6, 9, 0))
	require.NoError(t, err)
	assert.Empty(t, rep.Sends)
	assert.Empty(t, n.sent)
	assert.Equal(t, engine.EscalationNone, rep.Escalation)
}

// TestTripwire_BackfillBeforeRunSuppresses: a completed record for yesterday
// (a backfill that landed before the run) suppresses any L1/L2.
func TestTripwire_BackfillBeforeRunSuppresses(t *testing.T) {
	sc, a, n := newSched(t)
	// 07-04 completed (chain start) and a backfilled completed 07-05.
	seed(t, a, completedRec("2026-07-04"), completedRec("2026-07-05"))
	rep, err := sc.RunTripwire(at(2026, 7, 6, 9, 0))
	require.NoError(t, err)
	assert.Empty(t, rep.Sends)
	assert.Empty(t, n.sent)
}

// ── Storm variants ──────────────────────────────────────────────────────────

// TestTripwire_StormL1Variant: a standing storm + one miss ⇒ one L1 storm
// variant, and a storm miss spends zero budget.
func TestTripwire_StormL1Variant(t *testing.T) {
	sc, a, n := newSched(t)
	seedStandingStorm(t, a)
	seed(t, a, completedRec("2026-07-04")) // 07-05 absent, under the standing storm

	rep, err := sc.RunTripwire(at(2026, 7, 6, 9, 0))
	require.NoError(t, err)
	require.Len(t, rep.Sends, 1)
	assert.Equal(t, engine.SendL1, rep.Sends[0].Kind)
	assert.Contains(t, rep.Sends[0].Text, "storm standing, nothing is owed")
	assert.True(t, strings.HasSuffix(rep.Sends[0].Text, templates.SignOff))
	_ = n

	st, err := a.ReadEngineStatus()
	require.NoError(t, err)
	assert.Zero(t, st.ErrorBudget.Burn, "a storm miss spends no budget")
	assert.False(t, st.StakeOwed)
}

// TestTripwire_StormL2VariantNoStake: a standing storm + two consecutive misses
// ⇒ one L2 storm variant, zero budget spend, and no stake ever owed.
func TestTripwire_StormL2VariantNoStake(t *testing.T) {
	sc, a, n := newSched(t)
	armWitness(t, a)
	seedStandingStorm(t, a)
	seed(t, a, completedRec("2026-07-03"), missedRec("2026-07-04", true)) // 07-05 absent

	rep, err := sc.RunTripwire(at(2026, 7, 6, 9, 0))
	require.NoError(t, err)
	require.Len(t, rep.Sends, 1)
	assert.Equal(t, engine.SendL2, rep.Sends[0].Kind)
	assert.Contains(t, rep.Sends[0].Text, "storm standing (confirmed 2026-07-01)")
	assert.Equal(t, 1, n.count(engine.ChannelWitness))

	st, err := a.ReadEngineStatus()
	require.NoError(t, err)
	assert.Zero(t, st.ErrorBudget.Burn)
	assert.False(t, st.StakeOwed, "a storm miss is never a stake event")
	assert.Equal(t, engine.EscalationL2, st.EscalationState)
}

// ── Storm bookkeeping (silent) ──────────────────────────────────────────────

// TestTripwire_StormLapseAppendsAndNotifies: a pending declaration past 72h
// lapses with a user-channel note.
func TestTripwire_StormLapseAppendsAndNotifies(t *testing.T) {
	sc, a, n := newSched(t)
	require.NoError(t, a.AppendStormEvent(engine.StormEvent{At: "2026-07-01T07:00:00Z", Event: engine.StormDeclared, Label: "clause-1"}))
	seed(t, a, completedRec("2026-07-04")) // completed yesterday → no escalation, isolate the lapse

	rep, err := sc.RunTripwire(at(2026, 7, 5, 9, 0))
	require.NoError(t, err)

	msg, ok := n.first(engine.ChannelUser)
	require.True(t, ok)
	assert.Contains(t, msg.text, "storm declaration lapsed")
	require.Len(t, rep.StormEvents, 1)
	assert.Equal(t, engine.StormLapsed, rep.StormEvents[0].Event)

	h, err := a.ReadStormState()
	require.NoError(t, err)
	assert.Equal(t, engine.StormLapsed, h.History[len(h.History)-1].Event, "the lapse is appended to history")
}

// TestTripwire_AmbushEnterIsSilent: an ambush window whose start has arrived is
// entered with no send.
func TestTripwire_AmbushEnterIsSilent(t *testing.T) {
	sc, a, n := newSched(t)
	// Register a bare ambush window whose start is the run day.
	h, err := a.ReadStormState()
	require.NoError(t, err)
	h.Windows = []engine.StormWindow{{Label: "w1", Start: "2026-11-02", End: "2026-11-09"}}
	writeStorm(t, a, h)
	seed(t, a, completedRec("2026-11-01")) // completed yesterday → no escalation

	rep, err := sc.RunTripwire(at(2026, 11, 2, 9, 0))
	require.NoError(t, err)
	assert.Empty(t, n.sent, "entry is silent")
	require.Len(t, rep.StormEvents, 1)
	assert.Equal(t, engine.StormEntered, rep.StormEvents[0].Event)
}

// TestTripwire_ExpiryAppendsExpired: a standing storm past its through date is
// expired (silent bookkeeping).
func TestTripwire_ExpiryAppendsExpired(t *testing.T) {
	sc, a, n := newSched(t)
	seedStandingStorm(t, a) // through 2026-07-28
	seed(t, a, completedRec("2026-07-28"))

	rep, err := sc.RunTripwire(at(2026, 7, 29, 9, 0))
	require.NoError(t, err)
	assert.Empty(t, n.sent)
	require.NotEmpty(t, rep.StormEvents)
	assert.Equal(t, engine.StormExpired, rep.StormEvents[0].Event)
}

// ── Heartbeat ──────────────────────────────────────────────────────────────

// TestTripwire_HeartbeatFirstRunOfMonthOnce: the heartbeat fires on the first
// run of a calendar month and not again that month.
func TestTripwire_HeartbeatFirstRunOfMonthOnce(t *testing.T) {
	sc, a, n := newSched(t)
	armWitness(t, a)
	seed(t, a, completedRec("2026-07-05"), completedRec("2026-07-06"))

	rep, err := sc.RunTripwire(at(2026, 7, 6, 9, 0))
	require.NoError(t, err)
	require.Len(t, rep.Sends, 1)
	assert.Equal(t, engine.SendHeartbeat, rep.Sends[0].Kind)
	assert.Contains(t, rep.Sends[0].Text, "all clear")
	assert.False(t, strings.HasSuffix(rep.Sends[0].Text, templates.SignOff), "the heartbeat does not sign off")

	// A later run the same month posts no second heartbeat.
	rep, err = sc.RunTripwire(at(2026, 7, 7, 9, 0))
	require.NoError(t, err)
	assert.Empty(t, rep.Sends, "the heartbeat fires once per month")
	assert.Equal(t, 1, n.count(engine.ChannelWitness))
}

// TestTripwire_HeartbeatSuppressedByL2: an L2 to the witness on the first run
// of the month suppresses the heartbeat — the L2 is the month's proof of life.
func TestTripwire_HeartbeatSuppressedByL2(t *testing.T) {
	sc, a, n := newSched(t)
	armWitness(t, a)
	seed(t, a, completedRec("2026-07-03"), missedRec("2026-07-04", false))

	rep, err := sc.RunTripwire(at(2026, 7, 6, 9, 0))
	require.NoError(t, err)
	require.Len(t, rep.Sends, 1)
	assert.Equal(t, engine.SendL2, rep.Sends[0].Kind)
	assert.Equal(t, 1, n.count(engine.ChannelWitness), "only the L2, no heartbeat")
}

// ── Send-free verdict + companion presentation ───────────────────────────────

// TestTripwireUserVerdict_OneMissReturnsExactL1: the send-free verdict on a
// one-miss day returns exactly the L1 string the live run would deliver — the
// byte-for-byte line the companion appends — and writes/sends nothing.
func TestTripwireUserVerdict_OneMissReturnsExactL1(t *testing.T) {
	sc, a, n := newSched(t)
	seed(t, a, completedRec("2026-07-04")) // 07-05 absent -> one miss

	got, err := sc.TripwireUserVerdict(at(2026, 7, 6, 9, 0))
	require.NoError(t, err)

	// Byte-for-byte parity with the live run's L1 over an identical Ledger.
	scLive, aLive, _ := newSched(t)
	seed(t, aLive, completedRec("2026-07-04"))
	rep, err := scLive.RunTripwire(at(2026, 7, 6, 9, 0))
	require.NoError(t, err)
	require.Len(t, rep.Sends, 1)
	assert.Equal(t, rep.Sends[0].Text, got, "the verdict is the exact L1 the Engine would send")
	assert.True(t, strings.HasSuffix(got, templates.SignOff), "the pinned sign-off is present")

	// Nothing delivered and nothing persisted by the verdict read.
	assert.Empty(t, n.sent, "the verdict read delivers nothing")
	tw, err := a.ReadTripwireState()
	require.NoError(t, err)
	assert.Empty(t, tw.LastRunDate, "the verdict read persists no tripwire state")
	st, err := a.ReadEngineStatus()
	require.NoError(t, err)
	assert.Equal(t, engine.EscalationNone, st.EscalationState, "the verdict read persists no escalation")
}

// TestTripwireUserVerdict_CompletedReturnsEmpty: a completed reference day posts
// nothing to the user, so the verdict is the empty string.
func TestTripwireUserVerdict_CompletedReturnsEmpty(t *testing.T) {
	sc, a, n := newSched(t)
	seed(t, a, completedRec("2026-07-05"))

	got, err := sc.TripwireUserVerdict(at(2026, 7, 6, 9, 0))
	require.NoError(t, err)
	assert.Empty(t, got, "a completed day has no user verdict")
	assert.Empty(t, n.sent)
}

// TestTripwireUserVerdict_PureL2ReturnsEmpty: a two-consecutive-miss day with an
// armed witness escalates to the witness channel — no user send — so the
// user-channel verdict is empty even though the Engine will fire an L2.
func TestTripwireUserVerdict_PureL2ReturnsEmpty(t *testing.T) {
	sc, a, n := newSched(t)
	armWitness(t, a)
	seed(t, a, completedRec("2026-07-03"), missedRec("2026-07-04", false)) // 07-05 absent

	got, err := sc.TripwireUserVerdict(at(2026, 7, 6, 9, 0))
	require.NoError(t, err)
	assert.Empty(t, got, "an L2 is a witness send; there is no user verdict")
	assert.Empty(t, n.sent)
}

// TestTripwireUserVerdict_L2BlockedReturnsUserNote: two consecutive misses with
// an unarmed witness degrade to the user-channel L2-blocked note, so the verdict
// carries that exact text.
func TestTripwireUserVerdict_L2BlockedReturnsUserNote(t *testing.T) {
	sc, a, _ := newSched(t)
	seed(t, a, completedRec("2026-07-03"), missedRec("2026-07-04", false))

	got, err := sc.TripwireUserVerdict(at(2026, 7, 6, 9, 0))
	require.NoError(t, err)
	assert.Equal(t, templates.L2Blocked(false), got, "the L2-blocked note is a user-channel verdict")
}

// TestRunTripwirePresented_SuppressesUserButPersists: on a one-miss day the
// presented run delivers no user send (the companion presents it) yet still
// persists escalation_state to l1_fired, exactly as the live run would.
func TestRunTripwirePresented_SuppressesUserButPersists(t *testing.T) {
	sc, a, n := newSched(t)
	seed(t, a, completedRec("2026-07-04")) // 07-05 absent -> one miss

	rep, err := sc.RunTripwirePresented(at(2026, 7, 6, 9, 0))
	require.NoError(t, err)
	assert.Empty(t, rep.Sends, "no user send in presented mode")
	assert.Zero(t, n.count(engine.ChannelUser), "the Engine stays silent on the user channel")
	assert.Equal(t, engine.EscalationL1, rep.Escalation, "the decision still reaches l1_fired")

	st, err := a.ReadEngineStatus()
	require.NoError(t, err)
	assert.Equal(t, engine.EscalationL1, st.EscalationState, "escalation_state persists unchanged in presented mode")

	tw, err := a.ReadTripwireState()
	require.NoError(t, err)
	assert.Equal(t, "2026-07-06", tw.LastRunDate, "tripwire state still records the run")
}

// TestRunTripwirePresented_StillFiresWitnessL2: presentation withholds only the
// user channel — the witness L2 tooth still fires on a two-consecutive-miss day
// (carrying no Mirror bytes), and escalation still reaches l2_fired.
func TestRunTripwirePresented_StillFiresWitnessL2(t *testing.T) {
	sc, a, n := newSched(t)
	armWitness(t, a)
	seed(t, a, completedRec("2026-07-03"), missedRec("2026-07-04", false)) // 07-05 absent

	rep, err := sc.RunTripwirePresented(at(2026, 7, 6, 9, 0))
	require.NoError(t, err)
	require.Len(t, rep.Sends, 1)
	assert.Equal(t, engine.SendL2, rep.Sends[0].Kind)
	assert.Equal(t, 1, n.count(engine.ChannelWitness), "the witness L2 still fires in presented mode")
	assert.Zero(t, n.count(engine.ChannelUser))
	assert.Equal(t, engine.EscalationL2, rep.Escalation)
}

// TestRunTripwirePresented_HeartbeatStillFires: the monthly heartbeat is a
// witness send, so presentation does not suppress it — the first run of the
// month still posts the heartbeat to the witness.
func TestRunTripwirePresented_HeartbeatStillFires(t *testing.T) {
	sc, a, n := newSched(t)
	armWitness(t, a)
	seed(t, a, completedRec("2026-07-05"), completedRec("2026-07-06"))

	rep, err := sc.RunTripwirePresented(at(2026, 7, 6, 9, 0))
	require.NoError(t, err)
	require.Len(t, rep.Sends, 1)
	assert.Equal(t, engine.SendHeartbeat, rep.Sends[0].Kind)
	assert.Equal(t, 1, n.count(engine.ChannelWitness), "the heartbeat still fires in presented mode")
	assert.Zero(t, n.count(engine.ChannelUser))
}

// TestRunTripwirePresented_StormLapseSuppressedFromUser: a storm lapse is a
// user-channel note, so presentation withholds the Engine's own send while the
// lapse is still appended to storm history (and the companion presents the note
// via TripwireUserVerdict).
func TestRunTripwirePresented_StormLapseSuppressedFromUser(t *testing.T) {
	sc, a, n := newSched(t)
	require.NoError(t, a.AppendStormEvent(engine.StormEvent{At: "2026-07-01T07:00:00Z", Event: engine.StormDeclared, Label: "clause-1"}))
	seed(t, a, completedRec("2026-07-04")) // completed yesterday -> isolate the lapse

	rep, err := sc.RunTripwirePresented(at(2026, 7, 5, 9, 0))
	require.NoError(t, err)
	assert.Zero(t, n.count(engine.ChannelUser), "the lapse note is withheld from the user in presented mode")
	require.Len(t, rep.StormEvents, 1)
	assert.Equal(t, engine.StormLapsed, rep.StormEvents[0].Event, "the lapse is still appended to storm history")

	h, err := a.ReadStormState()
	require.NoError(t, err)
	assert.Equal(t, engine.StormLapsed, h.History[len(h.History)-1].Event, "storm bookkeeping persists in presented mode")

	// On an identical pre-run Ledger the send-free read surfaces the withheld
	// lapse line — the companion presents it before the tripwire consumes it.
	scV, aV, _ := newSched(t)
	require.NoError(t, aV.AppendStormEvent(engine.StormEvent{At: "2026-07-01T07:00:00Z", Event: engine.StormDeclared, Label: "clause-1"}))
	seed(t, aV, completedRec("2026-07-04"))
	verdict, err := scV.TripwireUserVerdict(at(2026, 7, 5, 9, 0))
	require.NoError(t, err)
	assert.Contains(t, verdict, "storm declaration lapsed", "the withheld note is available to the companion")
}

// ── Schedule metadata + purity ──────────────────────────────────────────────

func TestMarks(t *testing.T) {
	chain := engine.DefaultChain()
	bell, tripwire, err := Marks(chain, engine.DefaultProfile)
	require.NoError(t, err)
	assert.Equal(t, "19:00", bell)
	assert.Equal(t, "06:00", tripwire)

	bell, tripwire, err = Marks(chain, "nights")
	require.NoError(t, err)
	assert.Equal(t, "08:30", bell)
	assert.Equal(t, "17:00", tripwire)

	_, _, err = Marks(chain, "no-such-profile")
	assert.Error(t, err)
}

// TestSchedulerImportsNoModel is the "no LLM in the tripwire path" guard for
// the scheduler: it may reach storage, the pure engine, and the static
// templates — never a provider/agent/model package.
func TestSchedulerImportsNoModel(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	forbidden := []string{"provider", "agent", "openai", "anthropic", "llm", "model"}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.ImportsOnly)
		require.NoError(t, perr)
		for _, imp := range f.Imports {
			for _, bad := range forbidden {
				require.NotContainsf(t, strings.ToLower(imp.Path.Value), bad,
					"scheduler file %s imports %s — no model may sit in the tripwire path", name, imp.Path.Value)
			}
		}
	}
}

// writeStorm rewrites storm.json wholesale — a test helper for seeding windows
// the append-only API does not expose.
func writeStorm(t *testing.T, a *storage.Adapter, h engine.StormHistory) {
	t.Helper()
	b, err := json.Marshal(h)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(a.Home(), "engine", "storm.json"), b, 0o600))
}
