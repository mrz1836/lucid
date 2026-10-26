package router

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/storage"
)

// kindsToday is the logical day every kind test writes on: the test clock sits
// mid-afternoon, well past the default 04:00 rollover. All labels and dates in
// this file are synthetic.
const kindsToday = "2026-04-10"

// kindsClock hands out strictly increasing record times on one logical day, so
// every write carries its own recorded_at, as writes on a real Ledger do.
type kindsClock struct{ at time.Time }

func newKindsClock() *kindsClock {
	return &kindsClock{at: time.Date(2026, time.April, 10, 12, 0, 0, 0, time.UTC)}
}

// next advances the clock one minute and returns it.
func (c *kindsClock) next() time.Time {
	c.at = c.at.Add(time.Minute)
	return c.at
}

// kindsTodayDate is kindsToday as a civil date.
func kindsTodayDate() time.Time { return time.Date(2026, time.April, 10, 0, 0, 0, 0, time.UTC) }

// civil parses a YYYY-MM-DD test date.
func civil(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	require.NoError(t, err)
	return d
}

// daysTo is the day count from a civil date to kindsToday.
func daysTo(t *testing.T, from string) int {
	t.Helper()
	return engine.DaysSince(civil(t, from), kindsTodayDate())
}

// addAnchorOfKind records a new anchor of a kind through the router.
func addAnchorOfKind(
	t *testing.T, r *Router, c *kindsClock, label, date, kind string, allowance, window int,
) AnchorAddResult {
	t.Helper()
	res, err := r.AnchorAdd(AnchorAddRequest{
		Label: label, Date: date, Kind: kind, Allowance: allowance, Window: window, Now: c.next(),
	})
	require.NoError(t, err)
	return res
}

// foldAnchor reads both files and returns the facts of the anchor a label
// names (active or retired).
func foldAnchor(t *testing.T, a *storage.Adapter, label string) engine.AnchorFacts {
	t.Helper()
	log, err := a.ReadAnchors()
	require.NoError(t, err)
	events, err := a.ReadAnchorEvents()
	require.NoError(t, err)
	for _, f := range engine.FoldAnchorEvents(events, log) {
		if f.Anchor.Label == label {
			return f
		}
	}
	require.Failf(t, "no anchor", "no anchor labeled %q", label)
	return engine.AnchorFacts{}
}

// newQuitRun is the current binary's quit run for a label as of kindsToday.
func newQuitRun(t *testing.T, a *storage.Adapter, label string) int {
	t.Helper()
	f := foldAnchor(t, a, label)
	require.Equal(t, engine.AnchorKindQuit, f.Kind, "%s should read as a quit", label)
	return engine.ComputeQuitStats(f, kindsTodayDate()).CurrentRun
}

// oldDaysSince is what an older binary shows for a label: anchors.json alone,
// folded through today's LatestAnchors → BuildMetrics → partitionAnchors path,
// with no event store. ok is false when that binary shows no active anchor.
func oldDaysSince(t *testing.T, a *storage.Adapter, label string) (days int, ok bool) {
	t.Helper()
	log, err := a.ReadAnchors()
	require.NoError(t, err)
	clocks, err := engine.DefaultChain().ClocksFor(engine.DefaultProfile)
	require.NoError(t, err)
	m := engine.BuildMetrics(engine.MetricsInput{
		Anchors: engine.LatestAnchors(log),
		Now:     time.Date(2026, time.April, 10, 18, 0, 0, 0, time.UTC),
		Clocks:  clocks,
		Loc:     time.UTC,
	})
	for _, row := range m.Anchors {
		if row.Label == label {
			return row.DaysSince, true
		}
	}
	return 0, false
}

// anchorFiles snapshots both anchor files' bytes (nil for a missing file), so a
// test can assert a rejected verb wrote nothing to either.
func anchorFiles(t *testing.T, home string) (anchors, events []byte) {
	t.Helper()
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join(home, "engine", name))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		require.NoError(t, err)
		return b
	}
	return read("anchors.json"), read("anchor_events.json")
}

// requireNothingWritten runs a rejected verb and asserts it left both anchor
// files byte-unchanged and returned an error of the given class.
func requireNothingWritten(t *testing.T, home string, class error, verb func() error) error {
	t.Helper()
	beforeAnchors, beforeEvents := anchorFiles(t, home)
	err := verb()
	require.Error(t, err)
	require.ErrorIs(t, err, class)
	afterAnchors, afterEvents := anchorFiles(t, home)
	assert.Equal(t, string(beforeAnchors), string(afterAnchors), "a rejection must not touch anchors.json")
	assert.Equal(t, string(beforeEvents), string(afterEvents), "a rejection must not touch anchor_events.json")
	return err
}

// storedEvents reads the event store's events.
func storedEvents(t *testing.T, a *storage.Adapter) []engine.AnchorEvent {
	t.Helper()
	log, err := a.ReadAnchorEvents()
	require.NoError(t, err)
	return log.Events
}

// pairStates reads every pair intent's state.
func pairStates(t *testing.T, a *storage.Adapter) []engine.AnchorPairState {
	t.Helper()
	log, err := a.ReadAnchors()
	require.NoError(t, err)
	events, err := a.ReadAnchorEvents()
	require.NoError(t, err)
	return engine.AnchorPairStates(events, log)
}

// TestAnchorAddKindWritesDefinitionAndKindEvent: `add --kind` writes the v3
// definition plus the anchor's first kind event as one event-first pair, for
// every kind; the kind event carries today's logical day and the parameters,
// and its id is the receipt.
func TestAnchorAddKindWritesDefinitionAndKindEvent(t *testing.T) {
	cases := []struct {
		name, label, kind string
		allowance, window int
		ack               string
	}{
		{
			"quit", "sobriety", engine.AnchorKindQuit, 0, 0,
			"Anchor recorded: sobriety — 2026-01-05, a quit. Saved as `anchorev_2026_04_10_a`.",
		},
		{
			"limit", "late-night-snacks", engine.AnchorKindLimit, 4, 90,
			"Anchor recorded: late-night-snacks — 2026-01-05, a limit of 4 days in 90. Saved as `anchorev_2026_04_10_a`.",
		},
		{
			"habit with the default window", "morning-walk", engine.AnchorKindHabit, 0, 0,
			"Anchor recorded: morning-walk — 2026-01-05, a habit (30-day window); confirmations start 2026-04-10. " +
				"Saved as `anchorev_2026_04_10_a`.",
		},
		{
			"habit with a window", "evening-stretch", engine.AnchorKindHabit, 0, 14,
			"Anchor recorded: evening-stretch — 2026-01-05, a habit (14-day window); confirmations start 2026-04-10. " +
				"Saved as `anchorev_2026_04_10_a`.",
		},
		{
			"milestone", "first-gate", engine.AnchorKindMilestone, 0, 0,
			"Anchor recorded: first-gate — 2026-01-05, a milestone. Saved as `anchorev_2026_04_10_a`.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, a, _ := newBootedRouter(t)
			c := newKindsClock()

			res := addAnchorOfKind(t, r, c, tc.label, "2026-01-05", tc.kind, tc.allowance, tc.window)
			assert.Equal(t, tc.ack, res.Ack)
			require.NotNil(t, res.Event)

			log, err := a.ReadAnchors()
			require.NoError(t, err)
			require.Len(t, log.History, 1, "one definition record")
			def := log.History[0]
			assert.Equal(t, res.Anchor, def)
			assert.Equal(t, "anchor_2026_04_10_a", def.ID)
			assert.Equal(t, engine.AnchorVersion, log.Version, "anchors.json stays v3")

			events := storedEvents(t, a)
			require.Len(t, events, 1, "one kind event")
			ev := events[0]
			assert.Equal(t, *res.Event, ev)
			assert.Equal(t, "anchorev_2026_04_10_a", ev.ID, "the receipt is the event id")
			assert.Equal(t, def.ID, ev.AnchorID)
			assert.Equal(t, engine.AnchorEventKind, ev.Type)
			assert.Equal(t, kindsToday, ev.Day, "a kind event carries the logical day it was written")
			assert.Equal(t, def.RecordedAt, ev.RecordedAt, "the two halves share recorded_at")
			assert.Equal(t, tc.kind, ev.Kind)
			assert.Equal(t, tc.allowance, ev.Allowance)
			assert.Equal(t, tc.window, ev.WindowDays)

			states := pairStates(t, a)
			require.Len(t, states, 1)
			assert.Equal(t, engine.AnchorPairEventFirst, states[0].Intent.Order, "kind event first, then the definition")
			assert.Equal(t, engine.AnchorPairComplete, states[0].Status)

			assert.Equal(t, tc.kind, foldAnchor(t, a, tc.label).Kind)
		})
	}

	t.Run("a label an active anchor holds is rejected with the reclassify remedy", func(t *testing.T) {
		r, a, home := newBootedRouter(t)
		c := newKindsClock()
		_, err := r.AnchorAdd(AnchorAddRequest{Label: "sobriety", Date: "2026-01-01", Now: c.next()})
		require.NoError(t, err)

		err = requireNothingWritten(t, home, ErrAnchorLabelTaken, func() error {
			_, addErr := r.AnchorAdd(AnchorAddRequest{
				Label: "sobriety", Date: "2026-01-01", Kind: engine.AnchorKindQuit, Now: c.next(),
			})
			return addErr
		})
		assert.Equal(t,
			`"sobriety" is already recorded — reclassify it in place with lucid anchor kind sobriety quit. Nothing was saved.`,
			err.Error())
		assert.Empty(t, storedEvents(t, a))

		err = requireNothingWritten(t, home, ErrAnchorLabelTaken, func() error {
			_, addErr := r.AnchorAdd(AnchorAddRequest{
				Label: "sobriety", Date: "2026-01-01", Kind: engine.AnchorKindLimit, Allowance: 4, Window: 90, Now: c.next(),
			})
			return addErr
		})
		assert.Contains(t, err.Error(), "lucid anchor kind sobriety limit --allowance 4 --window 90.")
	})

	t.Run("invalid kind parameters are rejected before any write", func(t *testing.T) {
		r, _, home := newBootedRouter(t)
		bad := []AnchorAddRequest{
			{Label: "x", Date: "2026-01-01", Kind: "streak"},
			{Label: "x", Date: "2026-01-01", Kind: engine.AnchorKindLimit, Allowance: 4},
			{Label: "x", Date: "2026-01-01", Kind: engine.AnchorKindQuit, Window: 30},
			{Label: "x", Date: "2026-01-01", Kind: engine.AnchorKindHabit, Allowance: 2},
			{Label: "x", Date: "2026-01-01", Allowance: 4, Window: 90},
		}
		for _, req := range bad {
			err := requireNothingWritten(t, home, ErrAnchorRejected, func() error {
				_, addErr := r.AnchorAdd(req)
				return addErr
			})
			assert.Contains(t, err.Error(), "Nothing was saved.")
		}
	})

	t.Run("a label freed by a sunset starts a new anchor of the kind", func(t *testing.T) {
		r, _, _ := newBootedRouter(t)
		c := newKindsClock()
		_, err := r.AnchorAdd(AnchorAddRequest{Label: "sobriety", Date: "2026-01-01", Now: c.next()})
		require.NoError(t, err)
		_, err = r.AnchorSunset(AnchorSunsetRequest{Label: "sobriety", Now: c.next()})
		require.NoError(t, err)

		res := addAnchorOfKind(t, r, c, "sobriety", "2026-03-01", engine.AnchorKindQuit, 0, 0)
		assert.Equal(t,
			"Anchor added: sobriety — 2026-03-01 (a previous sobriety was sunset 2026-04-10), a quit. "+
				"Saved as `anchorev_2026_04_10_a`.",
			res.Ack)
		assert.Equal(t, "anchor_2026_04_10_b", res.Anchor.ID, "a new id, never the retired one")
	})
}

// TestAnchorAddWithoutKindWritesNoEvent: a plain `add` is exactly what it has
// always been — a milestone, the same ack, and no event store at all.
func TestAnchorAddWithoutKindWritesNoEvent(t *testing.T) {
	r, a, home := newBootedRouter(t)
	c := newKindsClock()

	res, err := r.AnchorAdd(AnchorAddRequest{Label: "first-gate", Date: "2026-03-01", Note: "a gate", Now: c.next()})
	require.NoError(t, err)
	assert.Equal(t, "Anchor recorded: first-gate — 2026-03-01.", res.Ack)
	assert.Nil(t, res.Event)

	// A correction is still the same append under the same id.
	again, err := r.AnchorAdd(AnchorAddRequest{Label: "first-gate", Date: "2026-03-02", Now: c.next()})
	require.NoError(t, err)
	assert.Equal(t, res.Anchor.ID, again.Anchor.ID)

	_, statErr := os.Stat(filepath.Join(home, "engine", "anchor_events.json"))
	require.ErrorIs(t, statErr, os.ErrNotExist, "a plain add never creates the event store")
	assert.Equal(t, engine.AnchorKindMilestone, foldAnchor(t, a, "first-gate").Kind)
}

// TestAnchorKindReclassifyKeepsIDAndDate: `anchor kind` appends one kind event
// and leaves anchors.json untouched; the id and the date never move, and a
// parameter change is the same verb.
func TestAnchorKindReclassifyKeepsIDAndDate(t *testing.T) {
	r, a, home := newBootedRouter(t)
	c := newKindsClock()
	added, err := r.AnchorAdd(AnchorAddRequest{Label: "late-night-snacks", Date: "2026-01-05", Now: c.next()})
	require.NoError(t, err)
	anchorsBefore, _ := anchorFiles(t, home)

	steps := []struct {
		req  AnchorKindRequest
		ack  string
		kind string
	}{
		{
			AnchorKindRequest{Kind: engine.AnchorKindLimit, Allowance: 4, Window: 90},
			"late-night-snacks is now a limit of 4 days in 90. Saved as `anchorev_2026_04_10_a`.", engine.AnchorKindLimit,
		},
		{
			AnchorKindRequest{Kind: engine.AnchorKindLimit, Allowance: 3, Window: 90, Note: "a tighter allowance"},
			"late-night-snacks is now a limit of 3 days in 90. Saved as `anchorev_2026_04_10_b`.", engine.AnchorKindLimit,
		},
		{
			AnchorKindRequest{Kind: engine.AnchorKindHabit},
			"late-night-snacks is now a habit (30-day window); confirmations start 2026-04-10. " +
				"Saved as `anchorev_2026_04_10_c`.", engine.AnchorKindHabit,
		},
		{
			AnchorKindRequest{Kind: engine.AnchorKindQuit},
			"late-night-snacks is now a quit — counting from 2026-01-05. Saved as `anchorev_2026_04_10_d`.",
			engine.AnchorKindQuit,
		},
		{
			AnchorKindRequest{Kind: engine.AnchorKindMilestone},
			"late-night-snacks is now a milestone — counting from 2026-01-05. Saved as `anchorev_2026_04_10_e`.",
			engine.AnchorKindMilestone,
		},
	}
	for i, step := range steps {
		step.req.Label, step.req.Now = "late-night-snacks", c.next()
		res, kindErr := r.AnchorKind(step.req)
		require.NoError(t, kindErr)
		assert.Equal(t, step.ack, res.Ack)
		assert.Nil(t, res.Compat, "no use-day, so nothing to re-date")
		assert.Equal(t, "late-night-snacks", res.Label)

		f := foldAnchor(t, a, "late-night-snacks")
		assert.Equal(t, step.kind, f.Kind)
		assert.Equal(t, added.Anchor.ID, f.AnchorID, "the id never moves")
		assert.Len(t, f.KindHistory, i+1, "every reclassify is one more dated kind event")
		assert.Equal(t, step.req.Note, res.Event.Note)
	}
	anchorsAfter, _ := anchorFiles(t, home)
	assert.Equal(t, string(anchorsBefore), string(anchorsAfter), "a reclassify leaves anchors.json untouched")
	assert.Empty(t, pairStates(t, a), "single-file writes write no pair intent")
	assert.Equal(t, 3, foldAnchor(t, a, "late-night-snacks").KindHistory[1].Allowance,
		"the parameter history is kept")

	t.Run("an unknown or sunset label is rejected", func(t *testing.T) {
		_ = requireNothingWritten(t, home, ErrAnchorUnknownLabel, func() error {
			_, kindErr := r.AnchorKind(AnchorKindRequest{Label: "nope", Kind: engine.AnchorKindQuit, Now: c.next()})
			return kindErr
		})
		_, err := r.AnchorSunset(AnchorSunsetRequest{Label: "late-night-snacks", Now: c.next()})
		require.NoError(t, err)
		err = requireNothingWritten(t, home, ErrAnchorAlreadySunset, func() error {
			_, kindErr := r.AnchorKind(AnchorKindRequest{
				Label: "late-night-snacks", Kind: engine.AnchorKindQuit, Now: c.next(),
			})
			return kindErr
		})
		assert.Contains(t, err.Error(), "takes no new events")
	})

	t.Run("invalid parameters are rejected", func(t *testing.T) {
		_ = requireNothingWritten(t, home, ErrAnchorRejected, func() error {
			_, kindErr := r.AnchorKind(AnchorKindRequest{Label: "x", Kind: engine.AnchorKindLimit, Now: c.next()})
			return kindErr
		})
	})
}

// TestAnchorKindToQuitRedatesForLaterUse: reclassifying to quit counts from the
// latest use-day when it is later than the anchor's date, and appends a compat
// re-date before the kind event so an older binary agrees. A recent planned use
// can never hide inside a long quit run.
func TestAnchorKindToQuitRedatesForLaterUse(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	c := newKindsClock()
	addAnchorOfKind(t, r, c, "late-night-snacks", "2026-01-01", engine.AnchorKindLimit, 4, 90)
	_, err := r.AnchorUse(AnchorUseRequest{
		Label: "late-night-snacks", Intent: engine.AnchorIntentPlanned, DayArg: "2026-03-14", Now: c.next(),
	})
	require.NoError(t, err)

	res, err := r.AnchorKind(AnchorKindRequest{Label: "late-night-snacks", Kind: engine.AnchorKindQuit, Now: c.next()})
	require.NoError(t, err)
	assert.Equal(t,
		"late-night-snacks is now a quit — counting from 2026-03-14, its latest use-day. Saved as `anchorev_2026_04_10_c`.",
		res.Ack)
	require.NotNil(t, res.Compat)
	assert.Equal(t, "2026-03-14", res.Compat.Date)
	assert.Equal(t, res.Event.RecordedAt, res.Compat.RecordedAt)

	log, err := a.ReadAnchors()
	require.NoError(t, err)
	require.Len(t, log.History, 2)
	assert.Equal(t, *res.Compat, log.History[1], "the compat re-date is a plain v3 record")
	assert.Equal(t, "2026-01-01", log.History[0].Date, "the definition is never rewritten")

	states := pairStates(t, a)
	require.Len(t, states, 2)
	assert.Equal(t, engine.AnchorPairCompatFirst, states[1].Intent.Order, "the re-date lands before the kind event")
	assert.Equal(t, engine.AnchorPairComplete, states[1].Status)

	want := daysTo(t, "2026-03-14")
	assert.Equal(t, want, newQuitRun(t, a, "late-night-snacks"), "the quit counts from the planned use")
	old, ok := oldDaysSince(t, a, "late-night-snacks")
	require.True(t, ok)
	assert.Equal(t, want, old, "an older binary counts from the use too")
	f := foldAnchor(t, a, "late-night-snacks")
	assert.Equal(t, "2026-01-01", f.Start, "the completed compat re-date never moves the start")
	assert.Equal(t, 1, engine.ComputeQuitStats(f, kindsTodayDate()).Slips, "the use is the quit's one slip")
}

// TestAnchorSlipRestartsCountKeepsLongest: a slip restarts the count from its
// day while the longest run and the slip count are kept; a backdated slip
// behind a later one writes its event alone; `add` stays the correction path.
func TestAnchorSlipRestartsCountKeepsLongest(t *testing.T) {
	r, a, home := newBootedRouter(t)
	c := newKindsClock()
	addAnchorOfKind(t, r, c, "sobriety", "2026-01-01", engine.AnchorKindQuit, 0, 0)

	res, err := r.AnchorSlip(AnchorSlipRequest{Label: "sobriety", DayArg: "2026-03-01", Note: "a party", Now: c.next()})
	require.NoError(t, err)
	assert.Equal(t,
		"Logged a slip on sobriety for 2026-03-01 as `anchorev_2026_04_10_b` — counting again from 2026-03-01 "+
			"(longest 59 days, 1 slip).",
		res.Ack)
	require.NotNil(t, res.Compat, "the slip moved the effective date")
	assert.Equal(t, "2026-03-01", res.Compat.Date)
	assert.Equal(t, "a party", res.Event.Note)
	q := engine.ComputeQuitStats(foldAnchor(t, a, "sobriety"), kindsTodayDate())
	assert.Equal(t, engine.QuitStats{CurrentRun: 40, LongestRun: 59, Slips: 1, LastSlip: "2026-03-01"}, q)

	anchorsBefore, _ := anchorFiles(t, home)
	back, err := r.AnchorSlip(AnchorSlipRequest{Label: "sobriety", DayArg: "2026-02-01", Now: c.next()})
	require.NoError(t, err)
	assert.Nil(t, back.Compat, "a slip behind a later one does not move the date")
	assert.Equal(t,
		"Logged a slip on sobriety for 2026-02-01 as `anchorev_2026_04_10_c` — still counting from 2026-03-01 "+
			"(longest 40 days, 2 slips).",
		back.Ack)
	anchorsAfter, _ := anchorFiles(t, home)
	assert.Equal(t, string(anchorsBefore), string(anchorsAfter), "an event-alone slip leaves anchors.json untouched")

	// A duplicate-day slip is one more record of the same use-day.
	_, err = r.AnchorSlip(AnchorSlipRequest{Label: "sobriety", DayArg: "2026-03-01", Now: c.next()})
	require.NoError(t, err)
	assert.Equal(t, 2, engine.ComputeQuitStats(foldAnchor(t, a, "sobriety"), kindsTodayDate()).Slips)

	// A slip on the start day counts and bounds a run of zero.
	r2, a2, _ := newBootedRouter(t)
	addAnchorOfKind(t, r2, c, "first-gate", "2026-04-01", engine.AnchorKindQuit, 0, 0)
	_, err = r2.AnchorSlip(AnchorSlipRequest{Label: "first-gate", DayArg: "2026-04-01", Now: c.next()})
	require.NoError(t, err)
	assert.Equal(t, 1, engine.ComputeQuitStats(foldAnchor(t, a2, "first-gate"), kindsTodayDate()).Slips)

	t.Run("a day before the start or in the future is rejected", func(t *testing.T) {
		err := requireNothingWritten(t, home, ErrAnchorBeforeStart, func() error {
			_, slipErr := r.AnchorSlip(AnchorSlipRequest{Label: "sobriety", DayArg: "2025-12-31", Now: c.next()})
			return slipErr
		})
		assert.Equal(t,
			`that's before "sobriety" started (2026-01-01) — an anchor's events begin on its start. Nothing was saved.`,
			err.Error())

		beforeA, beforeE := anchorFiles(t, home)
		for _, day := range []string{"2026-04-11", "2026", "2026-04", "not-a-day"} {
			_, slipErr := r.AnchorSlip(AnchorSlipRequest{Label: "sobriety", DayArg: day, Now: c.next()})
			var refused *DayRejectedError
			require.ErrorAs(t, slipErr, &refused, "day %q", day)
			assert.Contains(t, refused.Reason, "nothing was saved")
		}
		afterA, afterE := anchorFiles(t, home)
		assert.Equal(t, string(beforeA), string(afterA))
		assert.Equal(t, string(beforeE), string(afterE))
	})

	t.Run("a correction with add still moves the start", func(t *testing.T) {
		_, err := r.AnchorAdd(AnchorAddRequest{Label: "sobriety", Date: "2026-03-15", Now: c.next()})
		require.NoError(t, err)
		f := foldAnchor(t, a, "sobriety")
		assert.Equal(t, "2026-03-15", f.Start)
		assert.Equal(t, daysTo(t, "2026-03-15"), engine.ComputeQuitStats(f, kindsTodayDate()).CurrentRun)
	})
}

// TestAnchorUseRequiresIntent: a use needs exactly one intent; the binary never
// guesses. A planned use spends allowance and the ack says where it stands.
func TestAnchorUseRequiresIntent(t *testing.T) {
	r, a, home := newBootedRouter(t)
	c := newKindsClock()
	addAnchorOfKind(t, r, c, "late-night-snacks", "2026-01-05", engine.AnchorKindLimit, 2, 90)

	for _, intent := range []string{"", "maybe", "Planned"} {
		err := requireNothingWritten(t, home, ErrAnchorRejected, func() error {
			_, useErr := r.AnchorUse(AnchorUseRequest{Label: "late-night-snacks", Intent: intent, Now: c.next()})
			return useErr
		})
		assert.Equal(t, "say whether this use was planned or a slip: --planned or --slip. Nothing was saved.", err.Error())
	}

	res, err := r.AnchorUse(AnchorUseRequest{
		Label: "late-night-snacks", Intent: engine.AnchorIntentPlanned, DayArg: "2026-04-01", Now: c.next(),
	})
	require.NoError(t, err)
	assert.Equal(t,
		"Logged a planned use of late-night-snacks for 2026-04-01 as `anchorev_2026_04_10_b` — 1 of 2 in the last 90 days.",
		res.Ack)
	assert.Equal(t, engine.AnchorIntentPlanned, res.Event.Intent)
	assert.Nil(t, res.Compat, "a use touches the event store only")

	// Several uses on one day are one use-day.
	_, err = r.AnchorUse(AnchorUseRequest{
		Label: "late-night-snacks", Intent: engine.AnchorIntentPlanned, DayArg: "2026-04-01", Now: c.next(),
	})
	require.NoError(t, err)
	_, err = r.AnchorUse(AnchorUseRequest{
		Label: "late-night-snacks", Intent: engine.AnchorIntentSlip, DayArg: "2026-04-05", Now: c.next(),
	})
	require.NoError(t, err)
	over, err := r.AnchorUse(AnchorUseRequest{Label: "late-night-snacks", Intent: engine.AnchorIntentSlip, Now: c.next()})
	require.NoError(t, err)
	assert.Equal(t,
		"Logged a slip use of late-night-snacks for 2026-04-10 as `anchorev_2026_04_10_e` — 3 of 2 in the last 90 days, "+
			"over allowance.",
		over.Ack)
	l := engine.ComputeLimitStats(foldAnchor(t, a, "late-night-snacks"), kindsTodayDate())
	assert.Equal(t, 3, l.Used)
	assert.Equal(t, 1, l.Planned)
	assert.True(t, l.OverAllowance)
}

// TestAnchorVoidDropsEventFromMath: a void appends, the voided event drops out
// of every number, and the store keeps both records.
func TestAnchorVoidDropsEventFromMath(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	c := newKindsClock()
	addAnchorOfKind(t, r, c, "late-night-snacks", "2026-01-05", engine.AnchorKindLimit, 4, 90)
	keep, err := r.AnchorUse(AnchorUseRequest{
		Label: "late-night-snacks", Intent: engine.AnchorIntentPlanned, DayArg: "2026-04-01", Now: c.next(),
	})
	require.NoError(t, err)
	misheard, err := r.AnchorUse(AnchorUseRequest{
		Label: "late-night-snacks", Intent: engine.AnchorIntentSlip, DayArg: "2026-04-08", Now: c.next(),
	})
	require.NoError(t, err)
	require.Equal(t, 2, engine.ComputeLimitStats(foldAnchor(t, a, "late-night-snacks"), kindsTodayDate()).Used)

	res, err := r.AnchorVoid(AnchorVoidRequest{EventID: misheard.Event.ID, Reason: "misheard", Now: c.next()})
	require.NoError(t, err)
	assert.Equal(t,
		"Voided `anchorev_2026_04_10_c` (a slip use on late-night-snacks, 2026-04-08) as `anchorev_2026_04_10_d` — "+
			"it no longer counts; both records are kept.",
		res.Ack)
	assert.Equal(t, misheard.Event.ID, res.Event.Target)
	assert.Equal(t, "misheard", res.Event.Note)
	assert.Equal(t, kindsToday, res.Event.Day)
	assert.Nil(t, res.Compat, "voiding a use writes no definition record")

	l := engine.ComputeLimitStats(foldAnchor(t, a, "late-night-snacks"), kindsTodayDate())
	assert.Equal(t, 1, l.Used, "the voided use no longer counts")
	assert.Equal(t, 0, l.Slips)
	assert.Equal(t, "2026-04-01", l.LastUse)

	ids := map[string]bool{}
	for _, e := range storedEvents(t, a) {
		ids[e.ID] = true
	}
	assert.True(t, ids[keep.Event.ID])
	assert.True(t, ids[misheard.Event.ID], "the voided event stays in the store")
	assert.True(t, ids[res.Event.ID])

	// A miss and a confirm are voidable facts too.
	r2, a2, _ := newBootedRouter(t)
	habit := addAnchorOfKind(t, r2, c, "morning-walk", "2026-01-05", engine.AnchorKindHabit, 0, 0)
	miss := engine.AnchorEvent{
		ID: "anchorev_2026_04_10_m", AnchorID: habit.Anchor.ID, Type: engine.AnchorEventMiss,
		Day: "2026-04-10", RecordedAt: c.next().Format(time.RFC3339),
	}
	require.NoError(t, a2.AppendAnchorEvent(miss))
	_, err = r2.AnchorVoid(AnchorVoidRequest{EventID: miss.ID, Now: c.next()})
	require.NoError(t, err)
	assert.Empty(t, foldAnchor(t, a2, "morning-walk").Misses)
}

// TestAnchorVoidRejectsKindEvent: a void never targets a kind event, a void,
// an unknown id or an event already voided, and nothing is written.
func TestAnchorVoidRejectsKindEvent(t *testing.T) {
	r, a, home := newBootedRouter(t)
	c := newKindsClock()
	added := addAnchorOfKind(t, r, c, "sobriety", "2026-01-01", engine.AnchorKindQuit, 0, 0)
	slip, err := r.AnchorSlip(AnchorSlipRequest{Label: "sobriety", DayArg: "2026-03-01", Now: c.next()})
	require.NoError(t, err)
	void, err := r.AnchorVoid(AnchorVoidRequest{EventID: slip.Event.ID, Now: c.next()})
	require.NoError(t, err)

	err = requireNothingWritten(t, home, ErrAnchorEventNotVoidable, func() error {
		_, voidErr := r.AnchorVoid(AnchorVoidRequest{EventID: added.Event.ID, Now: c.next()})
		return voidErr
	})
	assert.Equal(t,
		"a kind event is never voided — reclassify with lucid anchor kind sobriety <kind>. Nothing was saved.", err.Error())

	err = requireNothingWritten(t, home, ErrAnchorEventNotVoidable, func() error {
		_, voidErr := r.AnchorVoid(AnchorVoidRequest{EventID: void.Event.ID, Now: c.next()})
		return voidErr
	})
	assert.Equal(t, "a void can't itself be voided. Nothing was saved.", err.Error())

	err = requireNothingWritten(t, home, ErrAnchorAlreadyVoided, func() error {
		_, voidErr := r.AnchorVoid(AnchorVoidRequest{EventID: slip.Event.ID, Now: c.next()})
		return voidErr
	})
	assert.Equal(t,
		`"anchorev_2026_04_10_b" is already voided (by "anchorev_2026_04_10_c"). Nothing was saved.`, err.Error())

	err = requireNothingWritten(t, home, ErrAnchorEventUnknown, func() error {
		_, voidErr := r.AnchorVoid(AnchorVoidRequest{EventID: "anchorev_2026_03_14_b", Now: c.next()})
		return voidErr
	})
	assert.Equal(t, `no anchor event "anchorev_2026_03_14_b". Nothing was saved.`, err.Error())

	t.Run("an id only an unfinished write holds is that write, not an unknown id", func(t *testing.T) {
		r.pairFault = failAt(pairStepFirst)
		_, err := r.AnchorSlip(AnchorSlipRequest{Label: "sobriety", DayArg: "2026-03-10", Now: c.next()})
		r.pairFault = nil
		require.Error(t, err)
		states := pairStates(t, a)
		reserved := states[len(states)-1].Intent.Event.ID

		err = requireNothingWritten(t, home, ErrAnchorUnfinished, func() error {
			_, voidErr := r.AnchorVoid(AnchorVoidRequest{EventID: reserved, Now: c.next()})
			return voidErr
		})
		assert.Contains(t, err.Error(), "the re-dated record and the slip event are missing")

		_, err = r.AnchorSlip(AnchorSlipRequest{Label: "sobriety", DayArg: "2026-03-10", Now: c.next()})
		require.NoError(t, err, "the retry finishes it")
	})

	t.Run("a sunset anchor takes no void except of its own graduation", func(t *testing.T) {
		use := engine.AnchorEvent{
			ID: "anchorev_2026_04_10_x", AnchorID: added.Anchor.ID, Type: engine.AnchorEventSlip,
			Day: "2026-03-20", RecordedAt: c.next().Format(time.RFC3339),
		}
		require.NoError(t, a.AppendAnchorEvent(use))
		grad := engine.AnchorEvent{
			ID: "anchorev_2026_04_10_y", AnchorID: added.Anchor.ID, Type: engine.AnchorEventGraduate,
			Day: kindsToday, RecordedAt: c.next().Format(time.RFC3339),
		}
		require.NoError(t, a.AppendAnchorEvent(grad))
		_, err := r.AnchorSunset(AnchorSunsetRequest{Label: "sobriety", Now: c.next()})
		require.NoError(t, err)
		require.True(t, foldAnchor(t, a, "sobriety").Graduated)

		_ = requireNothingWritten(t, home, ErrAnchorAlreadySunset, func() error {
			_, voidErr := r.AnchorVoid(AnchorVoidRequest{EventID: use.ID, Now: c.next()})
			return voidErr
		})
		res, err := r.AnchorVoid(AnchorVoidRequest{EventID: grad.ID, Now: c.next()})
		require.NoError(t, err)
		assert.Nil(t, res.Compat)
		f := foldAnchor(t, a, "sobriety")
		assert.False(t, f.Graduated, "voiding the graduation leaves an ordinary retirement")
		assert.True(t, f.Anchor.IsSunset())
	})
}

// TestAnchorVoidOfSlipRestoresDefinition: voiding a slip that had moved a
// quit's effective date appends the restoring record after the void, so both
// binaries read the corrected date; voiding one that had not writes the void
// alone.
func TestAnchorVoidOfSlipRestoresDefinition(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	c := newKindsClock()
	addAnchorOfKind(t, r, c, "sobriety", "2026-01-01", engine.AnchorKindQuit, 0, 0)
	early, err := r.AnchorSlip(AnchorSlipRequest{Label: "sobriety", DayArg: "2026-03-01", Now: c.next()})
	require.NoError(t, err)
	late, err := r.AnchorSlip(AnchorSlipRequest{Label: "sobriety", DayArg: "2026-03-20", Now: c.next()})
	require.NoError(t, err)

	res, err := r.AnchorVoid(AnchorVoidRequest{EventID: late.Event.ID, Now: c.next()})
	require.NoError(t, err)
	require.NotNil(t, res.Compat, "the voided slip had moved the date")
	assert.Equal(t, "2026-03-01", res.Compat.Date, "restored to the latest remaining use-day")
	assert.Equal(t,
		"Voided `anchorev_2026_04_10_c` (a slip on sobriety, 2026-03-20) as `anchorev_2026_04_10_d` — it no longer counts; "+
			"both records are kept. sobriety counts from 2026-03-01 again.",
		res.Ack)
	states := pairStates(t, a)
	last := states[len(states)-1]
	assert.Equal(t, engine.AnchorPairEventFirst, last.Intent.Order, "the void lands before the restoring record")
	assert.Equal(t, engine.AnchorPairComplete, last.Status)
	old, ok := oldDaysSince(t, a, "sobriety")
	require.True(t, ok)
	assert.Equal(t, daysTo(t, "2026-03-01"), old)
	assert.Equal(t, daysTo(t, "2026-03-01"), newQuitRun(t, a, "sobriety"))

	res, err = r.AnchorVoid(AnchorVoidRequest{EventID: early.Event.ID, Now: c.next()})
	require.NoError(t, err)
	require.NotNil(t, res.Compat)
	assert.Equal(t, "2026-01-01", res.Compat.Date, "with no slip left the quit counts from its start")
	assert.Contains(t, res.Ack, "sobriety counts from 2026-01-01 again.")
	old, _ = oldDaysSince(t, a, "sobriety")
	assert.Equal(t, daysTo(t, "2026-01-01"), old)
	q := engine.ComputeQuitStats(foldAnchor(t, a, "sobriety"), kindsTodayDate())
	assert.Equal(t, daysTo(t, "2026-01-01"), q.CurrentRun)
	assert.Equal(t, 0, q.Slips)

	t.Run("voiding a slip behind a later one writes the void alone", func(t *testing.T) {
		r2, a2, _ := newBootedRouter(t)
		addAnchorOfKind(t, r2, c, "sobriety", "2026-01-01", engine.AnchorKindQuit, 0, 0)
		first, err := r2.AnchorSlip(AnchorSlipRequest{Label: "sobriety", DayArg: "2026-03-01", Now: c.next()})
		require.NoError(t, err)
		_, err = r2.AnchorSlip(AnchorSlipRequest{Label: "sobriety", DayArg: "2026-03-20", Now: c.next()})
		require.NoError(t, err)
		intents := len(pairStates(t, a2))

		res, err := r2.AnchorVoid(AnchorVoidRequest{EventID: first.Event.ID, Now: c.next()})
		require.NoError(t, err)
		assert.Nil(t, res.Compat)
		assert.Len(t, pairStates(t, a2), intents, "an event-alone void writes no pair intent")
		assert.Equal(t, daysTo(t, "2026-03-20"), newQuitRun(t, a2, "sobriety"))
	})
}

// TestAnchorWrongKindRejectedWithRemedy: an event the anchor's kind does not
// take is rejected with the reclassify command named, and nothing is written.
func TestAnchorWrongKindRejectedWithRemedy(t *testing.T) {
	r, _, home := newBootedRouter(t)
	c := newKindsClock()
	_, err := r.AnchorAdd(AnchorAddRequest{Label: "sobriety", Date: "2026-01-01", Now: c.next()})
	require.NoError(t, err)
	addAnchorOfKind(t, r, c, "morning-walk", "2026-01-01", engine.AnchorKindHabit, 0, 0)
	addAnchorOfKind(t, r, c, "late-night-snacks", "2026-01-01", engine.AnchorKindLimit, 4, 90)

	cases := []struct {
		name string
		verb func() error
		want string
	}{
		{"slip on a milestone", func() error {
			_, e := r.AnchorSlip(AnchorSlipRequest{Label: "sobriety", Now: c.next()})
			return e
		}, `"sobriety" is a milestone, and a slip belongs to a quit — reclassify it as a quit first: ` +
			`lucid anchor kind sobriety quit. Nothing was saved.`},
		{"use on a habit", func() error {
			_, e := r.AnchorUse(AnchorUseRequest{Label: "morning-walk", Intent: engine.AnchorIntentPlanned, Now: c.next()})
			return e
		}, `"morning-walk" is a habit, and a use belongs to a limit — reclassify it as a limit first: ` +
			`lucid anchor kind morning-walk limit --allowance N --window D. Nothing was saved.`},
		{"slip on a limit", func() error {
			_, e := r.AnchorSlip(AnchorSlipRequest{Label: "late-night-snacks", Now: c.next()})
			return e
		}, `"late-night-snacks" is a limit, and a slip belongs to a quit — reclassify it as a quit first: ` +
			`lucid anchor kind late-night-snacks quit. Nothing was saved.`},
		{"miss on a milestone", func() error {
			_, e := r.AnchorMiss(AnchorMissRequest{Label: "sobriety", Now: c.next()})
			return e
		}, `"sobriety" is a milestone, and a miss belongs to a habit — reclassify it as a habit first: ` +
			`lucid anchor kind sobriety habit. Nothing was saved.`},
		{"miss on a limit", func() error {
			_, e := r.AnchorMiss(AnchorMissRequest{Label: "late-night-snacks", Now: c.next()})
			return e
		}, `"late-night-snacks" is a limit, and a miss belongs to a habit — reclassify it as a habit first: ` +
			`lucid anchor kind late-night-snacks habit. Nothing was saved.`},
		{"a named confirm on a limit", func() error {
			_, e := r.AnchorConfirm(AnchorConfirmRequest{Labels: []string{"late-night-snacks"}, Now: c.next()})
			return e
		}, `"late-night-snacks" is a limit, and a confirm belongs to a habit — reclassify it as a habit first: ` +
			`lucid anchor kind late-night-snacks habit. Nothing was saved.`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := requireNothingWritten(t, home, ErrAnchorWrongKind, tc.verb)
			assert.Equal(t, tc.want, err.Error())
		})
	}
}

// TestAnchorEventRejectsLegacyAnchor: an anchor recorded before ids existed
// takes no kind and no event until the explicit backfill gives it a durable
// id; there is no implicit adoption write.
func TestAnchorEventRejectsLegacyAnchor(t *testing.T) {
	r, a, home := newBootedRouter(t)
	c := newKindsClock()
	require.NoError(t, a.ScaffoldEngine())
	require.NoError(t, a.AppendAnchor(engine.Anchor{
		Label: "sobriety", Date: "2026-01-01", RecordedAt: c.next().Format(time.RFC3339),
	}))

	want := `"sobriety" was recorded before anchors had ids — run lucid anchor backfill-ids first, then try again. ` +
		`Nothing was saved.`
	verbs := map[string]func() error{
		"kind": func() error {
			_, e := r.AnchorKind(AnchorKindRequest{Label: "sobriety", Kind: engine.AnchorKindQuit, Now: c.next()})
			return e
		},
		"slip": func() error {
			_, e := r.AnchorSlip(AnchorSlipRequest{Label: "sobriety", Now: c.next()})
			return e
		},
		"use": func() error {
			_, e := r.AnchorUse(AnchorUseRequest{Label: "sobriety", Intent: engine.AnchorIntentSlip, Now: c.next()})
			return e
		},
		"miss": func() error {
			_, e := r.AnchorMiss(AnchorMissRequest{Label: "sobriety", Now: c.next()})
			return e
		},
		"confirm <label>": func() error {
			_, e := r.AnchorConfirm(AnchorConfirmRequest{Labels: []string{"sobriety"}, Now: c.next()})
			return e
		},
		"sunset --graduated": func() error {
			_, e := r.AnchorSunset(AnchorSunsetRequest{Label: "sobriety", Graduated: true, Now: c.next()})
			return e
		},
	}
	for name, verb := range verbs {
		t.Run(name, func(t *testing.T) {
			err := requireNothingWritten(t, home, ErrAnchorNeedsID, verb)
			assert.Equal(t, want, err.Error())
		})
	}

	_, err := r.BackfillAnchorIDs(BackfillAnchorIDsRequest{Now: c.next()})
	require.NoError(t, err)
	res, err := r.AnchorKind(AnchorKindRequest{Label: "sobriety", Kind: engine.AnchorKindQuit, Now: c.next()})
	require.NoError(t, err, "after the backfill the anchor takes a kind")
	assert.Equal(t, engine.AnchorKindQuit, foldAnchor(t, a, "sobriety").Kind)
	assert.Equal(t, "2026-01-01", foldAnchor(t, a, "sobriety").Anchor.Date)
	assert.NotEmpty(t, res.Event.AnchorID)
}

// TestAnchorEventReceiptIsEventID: every verb returns the stored event's id as
// its receipt, in the result and in the ack; ids follow the per-day slot
// grammar from the write's logical day, and an id a pair intent reserved is
// never handed to another event.
func TestAnchorEventReceiptIsEventID(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	c := newKindsClock()
	quit := addAnchorOfKind(t, r, c, "sobriety", "2026-01-01", engine.AnchorKindQuit, 0, 0)
	limit := addAnchorOfKind(t, r, c, "late-night-snacks", "2026-01-01", engine.AnchorKindLimit, 4, 90)

	slip, err := r.AnchorSlip(AnchorSlipRequest{Label: "sobriety", DayArg: "@yesterday", Now: c.next()})
	require.NoError(t, err)
	assert.Equal(t, "2026-04-09", slip.Event.Day, "@yesterday is the day before the logical day")
	use, err := r.AnchorUse(AnchorUseRequest{Label: "late-night-snacks", Intent: engine.AnchorIntentPlanned, Now: c.next()})
	require.NoError(t, err)
	kind, err := r.AnchorKind(AnchorKindRequest{
		Label: "late-night-snacks", Kind: engine.AnchorKindLimit,
		Allowance: 3, Window: 90, Now: c.next(),
	})
	require.NoError(t, err)
	void, err := r.AnchorVoid(AnchorVoidRequest{EventID: use.Event.ID, Now: c.next()})
	require.NoError(t, err)

	receipts := []string{quit.Event.ID, limit.Event.ID, slip.Event.ID, use.Event.ID, kind.Event.ID, void.Event.ID}
	acks := []string{quit.Ack, limit.Ack, slip.Ack, use.Ack, kind.Ack, void.Ack}
	stored := storedEvents(t, a)
	require.Len(t, stored, len(receipts))
	for i, id := range receipts {
		assert.Equal(t, stored[i].ID, id, "receipt %d is the stored event's id", i)
		assert.Equal(t, "anchorev_2026_04_10_"+engine.SlotLabel(i), id, "the per-day slot grammar")
		assert.Contains(t, acks[i], "`"+id+"`", "the ack names the receipt")
		require.NoError(t, engine.ValidateAnchorEvent(stored[i]))
	}

	t.Run("an id a pair intent reserved is never reissued", func(t *testing.T) {
		r.pairFault = func(step string) error {
			if step == pairStepFirst {
				return errors.New("crash")
			}
			return nil
		}
		_, err := r.AnchorSlip(AnchorSlipRequest{Label: "sobriety", Now: c.next()})
		require.Error(t, err)
		r.pairFault = nil
		reserved := pairStates(t, a)
		require.Equal(t, engine.AnchorPairIntentOnly, reserved[len(reserved)-1].Status)

		other, err := r.AnchorUse(AnchorUseRequest{
			Label: "late-night-snacks", Intent: engine.AnchorIntentSlip, Now: c.next(),
		})
		require.NoError(t, err)
		assert.NotEqual(t, reserved[len(reserved)-1].Intent.Event.ID, other.Event.ID)
		assert.Equal(t, "anchorev_2026_04_10_h", other.Event.ID, "the reserved slot g is skipped")
	})

	t.Run("a write after midnight mints from the evening's logical day", func(t *testing.T) {
		r2, _, _ := newBootedRouter(t)
		res, err := r2.AnchorAdd(AnchorAddRequest{
			Label: "sobriety", Date: "2026-01-01", Kind: engine.AnchorKindQuit,
			Now: time.Date(2026, time.April, 11, 0, 30, 0, 0, time.UTC),
		})
		require.NoError(t, err)
		assert.Equal(t, "anchorev_2026_04_10_a", res.Event.ID)
		assert.Equal(t, kindsToday, res.Event.Day)
	})
}

// noon moves the clock to noon UTC on a civil day — past the 04:00 rollover,
// so that day is the logical day. Later writes tick a minute at a time from
// there.
func (c *kindsClock) noon(t *testing.T, day string) {
	t.Helper()
	c.at = civil(t, day).Add(12 * time.Hour)
}

// habitStats is the current binary's habit numbers for a label as of a
// logical day.
func habitStats(t *testing.T, a *storage.Adapter, label, today string) engine.HabitStats {
	t.Helper()
	f := foldAnchor(t, a, label)
	require.Equal(t, engine.AnchorKindHabit, f.Kind, "%s should read as a habit", label)
	return engine.ComputeHabitStats(f, civil(t, today), engine.AnchorThresholds{})
}

// kindsMetrics projects both anchor files through BuildMetrics as of
// kindsToday, as `lucid metrics` reads them.
func kindsMetrics(t *testing.T, a *storage.Adapter) engine.Metrics {
	t.Helper()
	log, err := a.ReadAnchors()
	require.NoError(t, err)
	events, err := a.ReadAnchorEvents()
	require.NoError(t, err)
	clocks, err := engine.DefaultChain().ClocksFor(engine.DefaultProfile)
	require.NoError(t, err)
	return engine.BuildMetrics(engine.MetricsInput{
		Anchors:   engine.LatestAnchors(log),
		AnchorLog: log,
		Events:    events,
		Now:       time.Date(2026, time.April, 10, 18, 0, 0, 0, time.UTC),
		Clocks:    clocks,
		Loc:       time.UTC,
	})
}

// confirm runs one confirm and requires it to succeed.
func confirm(t *testing.T, r *Router, req AnchorConfirmRequest) AnchorConfirmResult {
	t.Helper()
	res, err := r.AnchorConfirm(req)
	require.NoError(t, err)
	return res
}

// TestAnchorMissRecordsHabitMiss: a miss is one event in the store and nothing
// in anchors.json; it defaults to the current logical day, backdates with
// --day, and always wins over a confirm on the same day. One isolated miss is
// never called out; a run reaching anchors.miss_alarm is, wherever the logged
// day falls in it, and the alarm reads the config.
func TestAnchorMissRecordsHabitMiss(t *testing.T) {
	r, a, home := newBootedRouter(t)
	c := newKindsClock()
	habit := addAnchorOfKind(t, r, c, "morning-walk", "2026-01-05", engine.AnchorKindHabit, 0, 0)
	anchorsBefore, _ := anchorFiles(t, home)

	res, err := r.AnchorMiss(AnchorMissRequest{
		Label: "morning-walk", DayArg: "2026-04-07", Note: "at a friend's place", Now: c.next(),
	})
	require.NoError(t, err)
	assert.Equal(t, "Logged a miss on morning-walk for 2026-04-07 as `anchorev_2026_04_10_b`.", res.Ack)
	assert.Equal(t, "morning-walk", res.Label)
	assert.Nil(t, res.Compat, "a miss touches the event store only")
	assert.False(t, res.Resumed)
	assert.Equal(t, engine.AnchorEvent{
		ID: "anchorev_2026_04_10_b", AnchorID: habit.Anchor.ID, Type: engine.AnchorEventMiss, Day: "2026-04-07",
		RecordedAt: res.Event.RecordedAt, Note: "at a friend's place",
	}, res.Event)
	stored := storedEvents(t, a)
	assert.Equal(t, res.Event, stored[len(stored)-1])

	res, err = r.AnchorMiss(AnchorMissRequest{Label: "morning-walk", DayArg: "@yesterday", Now: c.next()})
	require.NoError(t, err)
	assert.Equal(t, "2026-04-09", res.Event.Day, "@yesterday is the day before the logical day")
	assert.Equal(t, "Logged a miss on morning-walk for 2026-04-09 as `anchorev_2026_04_10_c`.", res.Ack,
		"04-07 and 04-09 are not consecutive")

	res, err = r.AnchorMiss(AnchorMissRequest{Label: "morning-walk", DayArg: "2026-04-08", Now: c.next()})
	require.NoError(t, err)
	assert.Equal(t,
		"Logged a miss on morning-walk for 2026-04-08 as `anchorev_2026_04_10_d` — missed 3 days in a row.", res.Ack,
		"a backdated miss joins the run around it")

	res, err = r.AnchorMiss(AnchorMissRequest{Label: "morning-walk", Now: c.next()})
	require.NoError(t, err)
	assert.Equal(t, kindsToday, res.Event.Day, "the default is the current logical day")
	assert.Contains(t, res.Ack, "missed 4 days in a row.")

	anchorsAfter, _ := anchorFiles(t, home)
	assert.Equal(t, string(anchorsBefore), string(anchorsAfter), "a miss never touches anchors.json")
	assert.Len(t, foldAnchor(t, a, "morning-walk").Misses, 4)

	t.Run("a miss wins over a confirm on the same day", func(t *testing.T) {
		r2, a2, _ := newBootedRouter(t)
		c2 := newKindsClock()
		addAnchorOfKind(t, r2, c2, "morning-walk", "2026-01-05", engine.AnchorKindHabit, 0, 0)
		confirm(t, r2, AnchorConfirmRequest{FromArg: "2026-03-30", ThroughArg: "2026-04-05", Now: c2.next()})
		before := habitStats(t, a2, "morning-walk", kindsToday)
		require.Equal(t, 7, before.Kept)

		res, err := r2.AnchorMiss(AnchorMissRequest{Label: "morning-walk", DayArg: "2026-04-02", Now: c2.next()})
		require.NoError(t, err, "a miss on a confirmed day is still recorded")
		assert.Equal(t, "Logged a miss on morning-walk for 2026-04-02 as `anchorev_2026_04_10_c`.", res.Ack)
		after := habitStats(t, a2, "morning-walk", kindsToday)
		assert.Equal(t, 7, after.Accounted)
		assert.Equal(t, 6, after.Kept, "the confirmed day now counts as a miss")
		assert.Equal(t, 1, after.Misses)
	})

	t.Run("the alarm reads anchors.miss_alarm", func(t *testing.T) {
		r2, a2, _ := newBootedRouter(t)
		cfg := r2.Config()
		cfg.Anchors.MissAlarm = 3
		require.NoError(t, a2.SaveConfig(cfg))
		_, err := r2.Boot()
		require.NoError(t, err)
		c2 := newKindsClock()
		addAnchorOfKind(t, r2, c2, "morning-walk", "2026-01-05", engine.AnchorKindHabit, 0, 0)

		_, err = r2.AnchorMiss(AnchorMissRequest{Label: "morning-walk", DayArg: "2026-04-08", Now: c2.next()})
		require.NoError(t, err)
		res, err := r2.AnchorMiss(AnchorMissRequest{Label: "morning-walk", DayArg: "2026-04-09", Now: c2.next()})
		require.NoError(t, err)
		assert.NotContains(t, res.Ack, "in a row", "a run of 2 is below a miss alarm of 3")
		res, err = r2.AnchorMiss(AnchorMissRequest{Label: "morning-walk", Now: c2.next()})
		require.NoError(t, err)
		assert.Contains(t, res.Ack, "— missed 3 days in a row.")
	})

	t.Run("a day before the start or not yet lived is rejected", func(t *testing.T) {
		err := requireNothingWritten(t, home, ErrAnchorBeforeStart, func() error {
			_, missErr := r.AnchorMiss(AnchorMissRequest{Label: "morning-walk", DayArg: "2026-01-04", Now: c.next()})
			return missErr
		})
		assert.Equal(t,
			`that's before "morning-walk" started (2026-01-05) — an anchor's events begin on its start. Nothing was saved.`,
			err.Error())

		beforeA, beforeE := anchorFiles(t, home)
		for _, day := range []string{"2026-04-11", "2026-04", "not-a-day"} {
			_, missErr := r.AnchorMiss(AnchorMissRequest{Label: "morning-walk", DayArg: day, Now: c.next()})
			var refused *DayRejectedError
			require.ErrorAs(t, missErr, &refused, "day %q", day)
		}
		afterA, afterE := anchorFiles(t, home)
		assert.Equal(t, string(beforeA), string(afterA))
		assert.Equal(t, string(beforeE), string(afterE))
	})

	t.Run("an unknown or sunset label is rejected", func(t *testing.T) {
		_ = requireNothingWritten(t, home, ErrAnchorUnknownLabel, func() error {
			_, missErr := r.AnchorMiss(AnchorMissRequest{Label: "evening-stretch", Now: c.next()})
			return missErr
		})
		_, err := r.AnchorSunset(AnchorSunsetRequest{Label: "morning-walk", Now: c.next()})
		require.NoError(t, err)
		err = requireNothingWritten(t, home, ErrAnchorAlreadySunset, func() error {
			_, missErr := r.AnchorMiss(AnchorMissRequest{Label: "morning-walk", Now: c.next()})
			return missErr
		})
		assert.Contains(t, err.Error(), "takes no new events")
	})
}

// TestAnchorConfirmSpanRules: a confirm attests a span per habit, ending at the
// through-day (default: yesterday's logical day) and starting at the later of
// the day after the last confirm and the day the anchor became a habit — never
// the anchor date — so a first confirm claims nothing before the cutover, a
// skipped week heals itself, and a kind change away and back starts again at
// the later day. --from widens the start, never before the anchor date nor past
// the through-day. An empty span writes nothing; no labels covers every active
// habit; every habit is checked before anything is written.
func TestAnchorConfirmSpanRules(t *testing.T) {
	r, a, home := newBootedRouter(t)
	c := newKindsClock()
	c.noon(t, "2026-03-01")
	addAnchorOfKind(t, r, c, "morning-walk", "2026-01-05", engine.AnchorKindHabit, 0, 0)

	t.Run("the first confirm starts on the day it became a habit", func(t *testing.T) {
		c.noon(t, "2026-03-08")
		res := confirm(t, r, AnchorConfirmRequest{Labels: []string{"morning-walk"}, Now: c.next()})
		assert.Equal(t, "2026-03-07", res.Through, "the through-day defaults to yesterday's logical day")
		require.Len(t, res.Confirmed, 1)
		ev := res.Confirmed[0].Event
		assert.Equal(t, "2026-03-01", ev.From, "never the anchor date (2026-01-05)")
		assert.Equal(t, "2026-03-07", ev.Day)
		assert.Equal(t, engine.AnchorEventConfirm, ev.Type)
		assert.Equal(t, "morning-walk", res.Confirmed[0].Label)
		assert.Empty(t, res.AlreadyConfirmed)
		h := habitStats(t, a, "morning-walk", "2026-03-08")
		assert.Equal(t, 7, h.Accounted)
		assert.Equal(t, 7, h.Kept)
		assert.Equal(t, "2026-03-07", h.LastConfirmed)
	})

	t.Run("a skipped week heals itself and a miss inside the span stays a miss", func(t *testing.T) {
		c.noon(t, "2026-03-20")
		_, err := r.AnchorMiss(AnchorMissRequest{Label: "morning-walk", DayArg: "2026-03-10", Now: c.next()})
		require.NoError(t, err)
		c.noon(t, "2026-03-22")
		res := confirm(t, r, AnchorConfirmRequest{Now: c.next()})
		require.Len(t, res.Confirmed, 1)
		ev := res.Confirmed[0].Event
		assert.Equal(t, "2026-03-08", ev.From, "the next confirm covers the gap")
		assert.Equal(t, "2026-03-21", ev.Day)
		h := habitStats(t, a, "morning-walk", "2026-03-22")
		assert.Equal(t, 21, h.Accounted)
		assert.Equal(t, 20, h.Kept)
		assert.Equal(t, 1, h.Misses)
	})

	t.Run("an empty span writes nothing", func(t *testing.T) {
		beforeA, beforeE := anchorFiles(t, home)
		res := confirm(t, r, AnchorConfirmRequest{Now: c.next()})
		assert.Empty(t, res.Confirmed)
		require.Len(t, res.AlreadyConfirmed, 1)
		assert.Equal(t, AnchorConfirmSkipped{
			Label: "morning-walk", AnchorID: foldAnchor(t, a, "morning-walk").AnchorID,
			LastConfirmed: "2026-03-21", NextFrom: "2026-03-22",
		}, res.AlreadyConfirmed[0])
		assert.Equal(t, "morning-walk: already confirmed through 2026-03-21; nothing written.", res.Ack)
		afterA, afterE := anchorFiles(t, home)
		assert.Equal(t, string(beforeA), string(afterA))
		assert.Equal(t, string(beforeE), string(afterE), "an empty span writes no event")
	})

	t.Run("--from is rejected before the anchor date or past the through-day", func(t *testing.T) {
		err := requireNothingWritten(t, home, ErrAnchorConfirmSpan, func() error {
			_, confirmErr := r.AnchorConfirm(AnchorConfirmRequest{FromArg: "2026-01-04", Now: c.next()})
			return confirmErr
		})
		assert.Equal(t, `--from can't be before "morning-walk" started (2026-01-05). Nothing was saved.`, err.Error())

		err = requireNothingWritten(t, home, ErrAnchorConfirmSpan, func() error {
			_, confirmErr := r.AnchorConfirm(AnchorConfirmRequest{
				FromArg: "2026-03-20", ThroughArg: "2026-03-15", Now: c.next(),
			})
			return confirmErr
		})
		assert.Equal(t, "--from 2026-03-20 is after the through-day 2026-03-15. Nothing was saved.", err.Error())
	})

	t.Run("a through-day not yet lived or partial is rejected", func(t *testing.T) {
		beforeA, beforeE := anchorFiles(t, home)
		for _, day := range []string{"2026-03-23", "2026-03", "not-a-day"} {
			_, err := r.AnchorConfirm(AnchorConfirmRequest{ThroughArg: day, Now: c.next()})
			var refused *DayRejectedError
			require.ErrorAs(t, err, &refused, "through %q", day)
			_, err = r.AnchorConfirm(AnchorConfirmRequest{FromArg: day, Now: c.next()})
			require.ErrorAs(t, err, &refused, "from %q", day)
		}
		afterA, afterE := anchorFiles(t, home)
		assert.Equal(t, string(beforeA), string(afterA))
		assert.Equal(t, string(beforeE), string(afterE))
	})

	t.Run("a kind change away and back starts at the later day", func(t *testing.T) {
		c.noon(t, "2026-03-25")
		_, err := r.AnchorKind(AnchorKindRequest{Label: "morning-walk", Kind: engine.AnchorKindMilestone, Now: c.next()})
		require.NoError(t, err)
		_ = requireNothingWritten(t, home, ErrAnchorWrongKind, func() error {
			_, confirmErr := r.AnchorConfirm(AnchorConfirmRequest{Labels: []string{"morning-walk"}, Now: c.next()})
			return confirmErr
		})
		noHabits := confirm(t, r, AnchorConfirmRequest{Now: c.next()})
		assert.Equal(t, "No active habits to confirm; nothing written.", noHabits.Ack, "a milestone is not covered")

		c.noon(t, "2026-04-01")
		_, err = r.AnchorKind(AnchorKindRequest{Label: "morning-walk", Kind: engine.AnchorKindHabit, Now: c.next()})
		require.NoError(t, err)
		c.noon(t, "2026-04-05")
		res := confirm(t, r, AnchorConfirmRequest{Now: c.next()})
		require.Len(t, res.Confirmed, 1)
		assert.Equal(t, "2026-04-01", res.Confirmed[0].Event.From,
			"the later of the day after the last confirm (03-22) and the day it became a habit again")
		assert.Equal(t, "2026-04-04", res.Confirmed[0].Event.Day)
	})

	t.Run("--from vouches for an earlier stretch", func(t *testing.T) {
		r, a, _ := newBootedRouter(t)
		c := newKindsClock()
		addAnchorOfKind(t, r, c, "morning-walk", "2026-03-01", engine.AnchorKindHabit, 0, 0)
		res := confirm(t, r, AnchorConfirmRequest{FromArg: "2026-03-01", Now: c.next()})
		require.Len(t, res.Confirmed, 1)
		assert.Equal(t, "2026-03-01", res.Confirmed[0].Event.From, "back to the anchor date, which the person vouched for")
		assert.Equal(t, "2026-04-09", res.Confirmed[0].Event.Day)
		h := habitStats(t, a, "morning-walk", kindsToday)
		assert.Equal(t, 29, h.Kept, "every vouched day in the window is kept")
		assert.Equal(t, 29, h.Accounted, "today, inside the window but not over, stays unaccounted")
	})

	t.Run("a through-day before it became a habit is an empty span", func(t *testing.T) {
		r, _, home := newBootedRouter(t)
		c := newKindsClock()
		addAnchorOfKind(t, r, c, "morning-walk", "2026-01-05", engine.AnchorKindHabit, 0, 0)
		beforeA, beforeE := anchorFiles(t, home)
		res := confirm(t, r, AnchorConfirmRequest{Now: c.next()})
		assert.Equal(t, "2026-04-09", res.Through)
		require.Len(t, res.AlreadyConfirmed, 1)
		assert.Empty(t, res.AlreadyConfirmed[0].LastConfirmed, "never confirmed")
		assert.Equal(t,
			"morning-walk: nothing to confirm through 2026-04-09 — its confirmations start 2026-04-10; nothing written.",
			res.Ack)
		afterA, afterE := anchorFiles(t, home)
		assert.Equal(t, string(beforeA), string(afterA))
		assert.Equal(t, string(beforeE), string(afterE))
	})

	t.Run("no labels covers every active habit, and only those", func(t *testing.T) {
		r, a, home := newBootedRouter(t)
		c := newKindsClock()
		c.noon(t, "2026-04-01")
		addAnchorOfKind(t, r, c, "morning-walk", "2026-01-05", engine.AnchorKindHabit, 0, 0)
		addAnchorOfKind(t, r, c, "evening-stretch", "2026-01-05", engine.AnchorKindHabit, 0, 14)
		addAnchorOfKind(t, r, c, "retired-habit", "2026-01-05", engine.AnchorKindHabit, 0, 0)
		addAnchorOfKind(t, r, c, "sobriety", "2026-01-01", engine.AnchorKindQuit, 0, 0)
		_, err := r.AnchorAdd(AnchorAddRequest{Label: "first-gate", Date: "2026-05-01", Now: c.next()})
		require.NoError(t, err)
		_, err = r.AnchorSunset(AnchorSunsetRequest{Label: "retired-habit", Now: c.next()})
		require.NoError(t, err)
		eventsBefore := len(storedEvents(t, a))

		c.noon(t, "2026-04-05")
		res := confirm(t, r, AnchorConfirmRequest{Now: c.next()})
		require.Len(t, res.Confirmed, 2)
		assert.Equal(t, "evening-stretch", res.Confirmed[0].Label, "label order")
		assert.Equal(t, "morning-walk", res.Confirmed[1].Label)
		assert.Equal(t, "anchorev_2026_04_05_a", res.Confirmed[0].Event.ID)
		assert.Equal(t, "anchorev_2026_04_05_b", res.Confirmed[1].Event.ID, "one write, consecutive receipts")
		assert.Len(t, storedEvents(t, a), eventsBefore+2)
		assert.Equal(t, 4, habitStats(t, a, "evening-stretch", "2026-04-05").Kept, "04-01 through 04-04, all kept")

		t.Run("a rejection for one named habit saves nothing for any", func(t *testing.T) {
			c.noon(t, "2026-04-09")
			err := requireNothingWritten(t, home, ErrAnchorWrongKind, func() error {
				_, confirmErr := r.AnchorConfirm(AnchorConfirmRequest{
					Labels: []string{"morning-walk", "sobriety"}, Now: c.next(),
				})
				return confirmErr
			})
			assert.Equal(t,
				`"sobriety" is a quit, and a confirm belongs to a habit — reclassify it as a habit first: `+
					`lucid anchor kind sobriety habit. Nothing was saved.`,
				err.Error())
			_ = requireNothingWritten(t, home, ErrAnchorUnknownLabel, func() error {
				_, confirmErr := r.AnchorConfirm(AnchorConfirmRequest{
					Labels: []string{"morning-walk", "nope"}, Now: c.next(),
				})
				return confirmErr
			})
			_ = requireNothingWritten(t, home, ErrAnchorAlreadySunset, func() error {
				_, confirmErr := r.AnchorConfirm(AnchorConfirmRequest{
					Labels: []string{"morning-walk", "retired-habit"}, Now: c.next(),
				})
				return confirmErr
			})
		})

		t.Run("a label named twice is confirmed once", func(t *testing.T) {
			res := confirm(t, r, AnchorConfirmRequest{
				Labels: []string{"morning-walk", "morning-walk"}, Now: c.next(),
			})
			require.Len(t, res.Confirmed, 1)
			assert.Equal(t, "2026-04-05", res.Confirmed[0].Event.From)
			assert.Equal(t, "2026-04-08", res.Confirmed[0].Event.Day)
		})
	})

	t.Run("no active habits is an honest no-op", func(t *testing.T) {
		r, _, home := newBootedRouter(t)
		c := newKindsClock()
		_, err := r.AnchorAdd(AnchorAddRequest{Label: "first-gate", Date: "2026-03-01", Now: c.next()})
		require.NoError(t, err)
		res := confirm(t, r, AnchorConfirmRequest{Now: c.next()})
		assert.Equal(t, "No active habits to confirm; nothing written.", res.Ack)
		assert.Equal(t, "2026-04-09", res.Through)
		assert.NotNil(t, res.Confirmed)
		assert.NotNil(t, res.AlreadyConfirmed)
		_, statErr := os.Stat(filepath.Join(home, "engine", "anchor_events.json"))
		require.ErrorIs(t, statErr, os.ErrNotExist, "a no-op confirm never creates the event store")
	})
}

// TestAnchorConfirmAckNamesSpan: the ack is one line per habit in label order,
// naming the exact span confirmed and its receipt — so whoever asked can say
// it back — or why nothing was written for it.
func TestAnchorConfirmAckNamesSpan(t *testing.T) {
	r, _, _ := newBootedRouter(t)
	c := newKindsClock()
	c.noon(t, "2026-01-18")
	addAnchorOfKind(t, r, c, "morning-walk", "2026-01-05", engine.AnchorKindHabit, 0, 0)
	addAnchorOfKind(t, r, c, "evening-stretch", "2026-01-05", engine.AnchorKindHabit, 0, 0)
	c.noon(t, "2026-01-25")
	first := confirm(t, r, AnchorConfirmRequest{Labels: []string{"evening-stretch"}, Now: c.next()})
	assert.Equal(t, "evening-stretch: confirmed 2026-01-18–2026-01-24 as `anchorev_2026_01_25_a`.", first.Ack)

	res := confirm(t, r, AnchorConfirmRequest{Now: c.next()})
	assert.Equal(t,
		"evening-stretch: already confirmed through 2026-01-24; nothing written.\n"+
			"morning-walk: confirmed 2026-01-18–2026-01-24 as `anchorev_2026_01_25_b`.",
		res.Ack)

	c.noon(t, "2026-01-26")
	one := confirm(t, r, AnchorConfirmRequest{Labels: []string{"morning-walk"}, Now: c.next()})
	assert.Equal(t, "morning-walk: confirmed 2026-01-25–2026-01-25 as `anchorev_2026_01_26_a`.", one.Ack,
		"a one-day span still names both ends")
}

// TestAnchorSunsetGraduatedWritesPlainSunsetAndEvent: --graduated writes a
// graduate event, then the very same plain sunset record — its state exactly
// "sunset", never a new value — as one event-first pair. The new binary reads
// it as graduated and projects it in anchors_graduated; an older binary reads
// an ordinary sunset. Any kind can graduate, and a plain sunset writes no event.
func TestAnchorSunsetGraduatedWritesPlainSunsetAndEvent(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	c := newKindsClock()
	habit := addAnchorOfKind(t, r, c, "morning-walk", "2026-01-05", engine.AnchorKindHabit, 0, 0)

	grad, err := r.AnchorSunset(AnchorSunsetRequest{
		Label: "morning-walk", Reason: "part of every morning now", Graduated: true, Now: c.next(),
	})
	require.NoError(t, err)
	assert.Equal(t,
		"Anchor graduated: morning-walk — 2026-01-05. It no longer counts on the metrics surface; the record is kept. "+
			"Saved as `anchorev_2026_04_10_b`.",
		grad.Ack)
	require.NotNil(t, grad.Event)
	assert.Equal(t, engine.AnchorEvent{
		ID: "anchorev_2026_04_10_b", AnchorID: habit.Anchor.ID, Type: engine.AnchorEventGraduate, Day: kindsToday,
		RecordedAt: grad.Anchor.RecordedAt, Note: "part of every morning now",
	}, *grad.Event)

	log, err := a.ReadAnchors()
	require.NoError(t, err)
	assert.Equal(t, engine.AnchorVersion, log.Version, "anchors.json stays v3")
	require.Len(t, log.History, 2)
	rec := log.History[1]
	assert.Equal(t, grad.Anchor, rec)
	assert.Equal(t, engine.Anchor{
		ID: habit.Anchor.ID, Label: "morning-walk", Date: "2026-01-05", Note: "part of every morning now",
		RecordedAt: grad.Event.RecordedAt, State: engine.AnchorStateSunset,
	}, rec, "a plain v3 sunset: the state is exactly sunset")

	states := pairStates(t, a)
	require.Len(t, states, 2)
	assert.Equal(t, engine.AnchorPairEventFirst, states[1].Intent.Order, "the graduate event, then the sunset")
	assert.Equal(t, engine.AnchorPairComplete, states[1].Status)

	f := foldAnchor(t, a, "morning-walk")
	assert.True(t, f.Graduated)
	assert.Equal(t, engine.AnchorKindHabit, f.Kind, "the kind survives the retirement")
	_, active := oldDaysSince(t, a, "morning-walk")
	assert.False(t, active, "an older binary reads an ordinary sunset")

	m := kindsMetrics(t, a)
	require.Len(t, m.AnchorsGraduated, 1)
	assert.Equal(t, "morning-walk", m.AnchorsGraduated[0].Label)
	assert.Empty(t, m.AnchorsSunset, "graduated, not abandoned")
	assert.Empty(t, m.Anchors)

	t.Run("any kind can graduate", func(t *testing.T) {
		r2, a2, _ := newBootedRouter(t)
		c2 := newKindsClock()
		addAnchorOfKind(t, r2, c2, "sobriety", "2026-01-01", engine.AnchorKindQuit, 0, 0)
		addAnchorOfKind(t, r2, c2, "late-night-snacks", "2026-01-01", engine.AnchorKindLimit, 4, 90)
		_, err := r2.AnchorAdd(AnchorAddRequest{Label: "first-gate", Date: "2026-03-01", Now: c2.next()})
		require.NoError(t, err)
		for _, label := range []string{"sobriety", "late-night-snacks", "first-gate"} {
			res, err := r2.AnchorSunset(AnchorSunsetRequest{Label: label, Graduated: true, Now: c2.next()})
			require.NoError(t, err, label)
			assert.Equal(t, engine.AnchorStateSunset, res.Anchor.State, label)
			assert.True(t, foldAnchor(t, a2, label).Graduated, label)
		}
		assert.Len(t, kindsMetrics(t, a2).AnchorsGraduated, 3)
	})

	t.Run("a plain sunset writes no event and stays a plain sunset", func(t *testing.T) {
		r2, a2, home2 := newBootedRouter(t)
		c2 := newKindsClock()
		_, err := r2.AnchorAdd(AnchorAddRequest{Label: "gate-30", Date: "2026-02-01", Now: c2.next()})
		require.NoError(t, err)
		res, err := r2.AnchorSunset(AnchorSunsetRequest{Label: "gate-30", Reason: "mistyped label", Now: c2.next()})
		require.NoError(t, err)
		assert.Nil(t, res.Event)
		assert.Equal(t,
			"Anchor sunset: gate-30 — 2026-02-01. It no longer counts on the metrics surface; the record is kept.", res.Ack)
		_, statErr := os.Stat(filepath.Join(home2, "engine", "anchor_events.json"))
		require.ErrorIs(t, statErr, os.ErrNotExist, "a plain sunset never creates the event store")
		assert.False(t, foldAnchor(t, a2, "gate-30").Graduated)
		m := kindsMetrics(t, a2)
		assert.Len(t, m.AnchorsSunset, 1)
		assert.Empty(t, m.AnchorsGraduated)
	})

	t.Run("rejections write nothing", func(t *testing.T) {
		r2, a2, home2 := newBootedRouter(t)
		c2 := newKindsClock()
		require.NoError(t, a2.ScaffoldEngine())
		require.NoError(t, a2.AppendAnchor(engine.Anchor{
			Label: "old-gate", Date: "2026-01-01", RecordedAt: c2.next().Format(time.RFC3339),
		}))
		err := requireNothingWritten(t, home2, ErrAnchorNeedsID, func() error {
			_, sunsetErr := r2.AnchorSunset(AnchorSunsetRequest{Label: "old-gate", Graduated: true, Now: c2.next()})
			return sunsetErr
		})
		assert.Equal(t,
			`"old-gate" was recorded before anchors had ids — run lucid anchor backfill-ids first, then try again. `+
				`Nothing was saved.`, err.Error())
		_ = requireNothingWritten(t, home2, ErrAnchorUnknownLabel, func() error {
			_, sunsetErr := r2.AnchorSunset(AnchorSunsetRequest{Label: "nope", Graduated: true, Now: c2.next()})
			return sunsetErr
		})
		_, err = r2.AnchorSunset(AnchorSunsetRequest{Label: "old-gate", Now: c2.next()})
		require.NoError(t, err, "a plain sunset of a pre-id anchor is unaffected")
		err = requireNothingWritten(t, home2, ErrAnchorAlreadySunset, func() error {
			_, sunsetErr := r2.AnchorSunset(AnchorSunsetRequest{Label: "old-gate", Graduated: true, Now: c2.next()})
			return sunsetErr
		})
		assert.Contains(t, err.Error(), "was already sunset on")
	})

	t.Run("an interrupted graduation refuses a plain sunset and finishes on retry", func(t *testing.T) {
		r2, a2, home2 := newBootedRouter(t)
		c2 := newKindsClock()
		addAnchorOfKind(t, r2, c2, "morning-walk", "2026-01-05", engine.AnchorKindHabit, 0, 0)
		r2.pairFault = failAt(pairStepSecond)
		_, err := r2.AnchorSunset(AnchorSunsetRequest{Label: "morning-walk", Graduated: true, Now: c2.next()})
		r2.pairFault = nil
		require.Error(t, err)
		assert.Contains(t, err.Error(), "retry the same command")
		assert.False(t, foldAnchor(t, a2, "morning-walk").Graduated, "no graduation before its sunset half lands")
		_, active := oldDaysSince(t, a2, "morning-walk")
		assert.True(t, active, "still an active anchor in an older binary")

		err = requireNothingWritten(t, home2, ErrAnchorUnfinished, func() error {
			_, sunsetErr := r2.AnchorSunset(AnchorSunsetRequest{Label: "morning-walk", Now: c2.next()})
			return sunsetErr
		})
		assert.Contains(t, err.Error(), "the sunset record is missing")
		_ = requireNothingWritten(t, home2, ErrAnchorUnfinished, func() error {
			_, renameErr := r2.AnchorRename(AnchorRenameRequest{Label: "morning-walk", NewLabel: "walks", Now: c2.next()})
			return renameErr
		})

		res, err := r2.AnchorSunset(AnchorSunsetRequest{Label: "morning-walk", Graduated: true, Now: c2.next()})
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(res.Ack, "Finished the earlier unfinished write. Anchor graduated: morning-walk"))
		assert.Equal(t, pairStates(t, a2)[1].Intent.Event.ID, res.Event.ID, "the original receipt")
		assert.True(t, foldAnchor(t, a2, "morning-walk").Graduated)
		requireAllPairsComplete(t, a2)
	})

	t.Run("voiding the graduation leaves an ordinary retirement", func(t *testing.T) {
		voided, err := r.AnchorVoid(AnchorVoidRequest{EventID: grad.Event.ID, Now: c.next()})
		require.NoError(t, err)
		assert.Contains(t, voided.Ack, "(a graduation on morning-walk, 2026-04-10)")
		f := foldAnchor(t, a, "morning-walk")
		assert.False(t, f.Graduated)
		assert.True(t, f.Anchor.IsSunset())
		m := kindsMetrics(t, a)
		assert.Len(t, m.AnchorsSunset, 1)
		assert.Empty(t, m.AnchorsGraduated)
	})
}

// TestAnchorIdentityVerbsUnchangedForEveryKind: an anchor's kind and events key
// on its id, so rename, sunset, re-adding a freed label, backfill-ids and the
// adoption a pre-id rename writes all work exactly as before for every kind —
// ids and kinds survive, and every event stays attached.
func TestAnchorIdentityVerbsUnchangedForEveryKind(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	c := newKindsClock()
	require.NoError(t, a.ScaffoldEngine())
	for _, label := range []string{"old-walk", "old-gate"} { // recorded before ids existed
		require.NoError(t, a.AppendAnchor(engine.Anchor{
			Label: label, Date: "2026-01-02", RecordedAt: c.next().Format(time.RFC3339),
		}))
	}
	_, err := r.AnchorAdd(AnchorAddRequest{Label: "first-gate", Date: "2026-05-01", Now: c.next()})
	require.NoError(t, err)
	addAnchorOfKind(t, r, c, "sobriety", "2026-01-01", engine.AnchorKindQuit, 0, 0)
	addAnchorOfKind(t, r, c, "late-night-snacks", "2026-01-01", engine.AnchorKindLimit, 4, 90)
	addAnchorOfKind(t, r, c, "morning-walk", "2026-01-05", engine.AnchorKindHabit, 0, 0)
	_, err = r.AnchorSlip(AnchorSlipRequest{Label: "sobriety", DayArg: "2026-03-01", Now: c.next()})
	require.NoError(t, err)
	_, err = r.AnchorUse(AnchorUseRequest{
		Label: "late-night-snacks", Intent: engine.AnchorIntentPlanned, DayArg: "2026-04-01", Now: c.next(),
	})
	require.NoError(t, err)
	_, err = r.AnchorMiss(AnchorMissRequest{Label: "morning-walk", DayArg: "2026-04-10", Now: c.next()})
	require.NoError(t, err)

	// snapshot is everything about an anchor that must survive an identity verb.
	type snapshot struct {
		id, kind                      string
		slips, uses, misses, confirms int
		quit                          engine.QuitStats
		used                          int
	}
	snap := func(label string) snapshot {
		f := foldAnchor(t, a, label)
		s := snapshot{
			id: f.AnchorID, kind: f.Kind,
			slips: len(f.Slips), uses: len(f.Uses), misses: len(f.Misses), confirms: len(f.Confirms),
		}
		switch f.Kind {
		case engine.AnchorKindQuit:
			s.quit = engine.ComputeQuitStats(f, kindsTodayDate())
		case engine.AnchorKindLimit:
			s.used = engine.ComputeLimitStats(f, kindsTodayDate()).Used
		}
		return s
	}
	kinded := map[string]string{
		"first-gate": engine.AnchorKindMilestone, "sobriety": engine.AnchorKindQuit,
		"late-night-snacks": engine.AnchorKindLimit, "morning-walk": engine.AnchorKindHabit,
	}
	before := map[string]snapshot{}
	for label, kind := range kinded {
		before[label] = snap(label)
		require.Equal(t, kind, before[label].kind)
	}
	assert.Equal(t, 1, before["sobriety"].quit.Slips)

	t.Run("backfill-ids adopts only the pre-id anchors", func(t *testing.T) {
		res, err := r.BackfillAnchorIDs(BackfillAnchorIDsRequest{Now: c.next()})
		require.NoError(t, err)
		assert.Equal(t, []string{"old-gate", "old-walk"}, adoptedLabels(res.Planned))
		for label := range kinded {
			assert.Equal(t, before[label], snap(label), "%s is untouched by the backfill", label)
		}
		_, err = r.AnchorKind(AnchorKindRequest{Label: "old-walk", Kind: engine.AnchorKindHabit, Now: c.next()})
		require.NoError(t, err, "a backfilled anchor takes a kind")
		_, err = r.AnchorMiss(AnchorMissRequest{Label: "old-walk", Now: c.next()})
		require.NoError(t, err)
		kinded["old-walk"] = engine.AnchorKindHabit
		before["old-walk"] = snap("old-walk")
	})

	t.Run("rename keeps the id, the kind and every event", func(t *testing.T) {
		for label := range kinded {
			renamed := label + "-renamed"
			_, err := r.AnchorRename(AnchorRenameRequest{Label: label, NewLabel: renamed, Now: c.next()})
			require.NoError(t, err, label)
			assert.Equal(t, before[label], snap(renamed), "%s keeps everything through a rename", label)
		}
	})

	t.Run("adopting a pre-id anchor by rename gives it a durable id that takes a kind", func(t *testing.T) {
		// old-gate was backfilled above; a fresh pre-id record exercises the
		// rename adoption itself.
		require.NoError(t, a.AppendAnchor(engine.Anchor{
			Label: "legacy-gate", Date: "2026-01-03", RecordedAt: c.next().Format(time.RFC3339),
		}))
		res, err := r.AnchorRename(AnchorRenameRequest{Label: "legacy-gate", NewLabel: "gate-two", Now: c.next()})
		require.NoError(t, err)
		require.NotNil(t, res.Retired, "the adoption writes the retired stub")
		_, err = r.AnchorKind(AnchorKindRequest{Label: "gate-two", Kind: engine.AnchorKindQuit, Now: c.next()})
		require.NoError(t, err)
		_, err = r.AnchorRename(AnchorRenameRequest{Label: "gate-two", NewLabel: "gate-2", Now: c.next()})
		require.NoError(t, err)
		f := foldAnchor(t, a, "gate-2")
		assert.Equal(t, res.Anchor.ID, f.AnchorID)
		assert.Equal(t, engine.AnchorKindQuit, f.Kind, "the kind follows the adopted id through a rename")
	})

	t.Run("sunset keeps the kind and events; re-adding the label starts a new anchor", func(t *testing.T) {
		for label := range kinded {
			name := label + "-renamed"
			_, err := r.AnchorSunset(AnchorSunsetRequest{Label: name, Now: c.next()})
			require.NoError(t, err, label)
			retired := foldAnchor(t, a, name)
			assert.True(t, retired.Anchor.IsSunset())
			assert.False(t, retired.Graduated)
			assert.Equal(t, before[label].kind, retired.Kind, "%s: a retired anchor keeps its kind", label)
			assert.Len(t, retired.Slips, before[label].slips)
			assert.Len(t, retired.Uses, before[label].uses)
			assert.Len(t, retired.Misses, before[label].misses)

			again, err := r.AnchorAdd(AnchorAddRequest{Label: name, Date: "2026-04-01", Now: c.next()})
			require.NoError(t, err, label)
			assert.NotEqual(t, before[label].id, again.Anchor.ID, "%s: a freed label mints a new id", label)
			f, ok := engine.FoldAnchorEvents(mustEvents(t, a), mustAnchors(t, a))[again.Anchor.ID]
			require.True(t, ok)
			assert.Equal(t, engine.AnchorKindMilestone, f.Kind, "%s: the new anchor starts with no kind", label)
			assert.Empty(t, f.Slips)
			assert.Empty(t, f.Misses)
		}
		facts := engine.FoldAnchorEvents(mustEvents(t, a), mustAnchors(t, a))
		for label, s := range before {
			f, ok := facts[s.id]
			require.True(t, ok, label)
			assert.Equal(t, s.kind, f.Kind, "%s: the retired run keeps its kind under its own id", label)
			assert.True(t, f.Anchor.IsSunset(), label)
		}
	})
}

// mustEvents reads the event store.
func mustEvents(t *testing.T, a *storage.Adapter) engine.AnchorEventLog {
	t.Helper()
	log, err := a.ReadAnchorEvents()
	require.NoError(t, err)
	return log
}

// mustAnchors reads anchors.json.
func mustAnchors(t *testing.T, a *storage.Adapter) engine.AnchorLog {
	t.Helper()
	log, err := a.ReadAnchors()
	require.NoError(t, err)
	return log
}
