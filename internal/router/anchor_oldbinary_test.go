package router

import (
	"encoding/json"
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

// v3Anchor and v3AnchorLog are anchors.json as an older binary knows it — the
// v3 shape and nothing more. An older binary decodes into this, appends, and
// re-marshals the whole file, so whatever it does not know it drops; it never
// opens anchor_events.json.
type v3Anchor struct {
	ID         string `json:"id,omitempty"`
	Label      string `json:"label"`
	Date       string `json:"date"`
	Note       string `json:"note,omitempty"`
	RecordedAt string `json:"recorded_at"`
	State      string `json:"state,omitempty"`
	Adopts     string `json:"adopts,omitempty"`
	AdoptedAt  string `json:"adopted_at,omitempty"`
}

type v3AnchorLog struct {
	Version int        `json:"version"`
	History []v3Anchor `json:"history"`
}

// olderBinaryAppend is an older binary's anchor write: decode anchors.json into
// the v3 shape, append one record, re-marshal anchors.json alone. The event
// store must come through byte-unchanged.
func olderBinaryAppend(t *testing.T, home string, rec v3Anchor) {
	t.Helper()
	path := filepath.Join(home, "engine", "anchors.json")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var log v3AnchorLog
	require.NoError(t, json.Unmarshal(raw, &log))
	log.History = append(log.History, rec)
	_, eventsBefore := anchorFiles(t, home)
	out, err := json.MarshalIndent(log, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, out, 0o600))
	_, eventsAfter := anchorFiles(t, home)
	require.Equal(t, string(eventsBefore), string(eventsAfter), "an older binary never touches the event store")
}

// latestFor returns the newest anchors.json record a label names, as the older
// binary would copy it into a rename or a correction.
func latestFor(t *testing.T, a *storage.Adapter, label string) engine.Anchor {
	t.Helper()
	log, err := a.ReadAnchors()
	require.NoError(t, err)
	got, ok := engine.FindActiveAnchor(engine.LatestAnchors(log), label)
	require.True(t, ok, "no active anchor %q", label)
	return got
}

// olderBinaryRecord is the record an older binary appends for an anchor: its
// id, a label and a date, stamped later than every write of the test clock.
func olderBinaryRecord(a engine.Anchor, label, date string) v3Anchor {
	return v3Anchor{ID: a.ID, Label: label, Date: date, Note: a.Note, RecordedAt: "2026-04-10T17:00:00Z"}
}

// failAt is a fault seam that stops a two-file write before one boundary, as a
// crash there would.
func failAt(step string) func(string) error {
	return func(at string) error {
		if at == step {
			return errors.New("simulated crash before the " + step + " write")
		}
		return nil
	}
}

// quitReadings is what each binary shows for a quit: the older binary's
// days-since (anchors.json alone) and the current binary's quit run. A missing
// anchor or a current binary that does not read a quit leaves its flag false.
type quitReadings struct {
	old, current     int
	oldOK, currentOK bool
}

// readQuit takes both readings for a label as of kindsToday.
func readQuit(t *testing.T, a *storage.Adapter, label string) quitReadings {
	t.Helper()
	var q quitReadings
	q.old, q.oldOK = oldDaysSince(t, a, label)
	log, err := a.ReadAnchors()
	require.NoError(t, err)
	events, err := a.ReadAnchorEvents()
	require.NoError(t, err)
	for _, f := range engine.FoldAnchorEvents(events, log) {
		if f.Anchor.Label == label && !f.Anchor.IsSunset() && f.Kind == engine.AnchorKindQuit {
			q.current, q.currentOK = engine.ComputeQuitStats(f, kindsTodayDate()).CurrentRun, true
		}
	}
	return q
}

// requireNeverLonger asserts neither binary reads a quit run longer than
// truth, the longest run the accepted history permits.
func requireNeverLonger(t *testing.T, q quitReadings, truth int, where string) {
	t.Helper()
	if q.oldOK {
		assert.LessOrEqual(t, q.old, truth, "%s: an older binary reads a run longer than the truth", where)
	}
	if q.currentOK {
		assert.LessOrEqual(t, q.current, truth, "%s: the current binary reads a run longer than the truth", where)
	}
}

// requireAllPairsComplete asserts no pair is left unfinished.
func requireAllPairsComplete(t *testing.T, a *storage.Adapter) {
	t.Helper()
	for _, st := range pairStates(t, a) {
		assert.Equal(t, engine.AnchorPairComplete, st.Status, "pair %s", st.Intent.Event.ID)
	}
}

// requireUniqueReceipts asserts every stored event id appears once.
func requireUniqueReceipts(t *testing.T, a *storage.Adapter) {
	t.Helper()
	seen := map[string]bool{}
	for _, e := range storedEvents(t, a) {
		assert.False(t, seen[e.ID], "event id %s is recorded twice", e.ID)
		seen[e.ID] = true
	}
}

// crashScenario is one two-file verb, driven to a crash at a boundary and then
// retried. setup builds the prior history; op runs the verb and reports its
// receipt and whether it finished an earlier write; differ is some other
// mutation of the same anchor, which must be refused while the pair is
// unfinished; verify checks both binaries' readings at a stage — the boundary
// the crash hit, or "done" once the retry finished.
type crashScenario struct {
	name   string
	order  string
	setup  func(t *testing.T, r *Router, c *kindsClock)
	op     func(r *Router, c *kindsClock) (receipt string, resumed bool, err error)
	differ func(r *Router, c *kindsClock) error
	verify func(t *testing.T, a *storage.Adapter, stage string)
}

// eventOp adapts an event verb to a crash scenario's op.
func eventOp(res AnchorEventResult, err error) (string, bool, error) {
	return res.Event.ID, res.Resumed, err
}

// crashScenarios are the four two-file verbs this phase writes. Each verify
// holds the readings at a stage to the truth the accepted history permits:
// until a fact-bearing half lands that is the history before the verb, and
// once one has, the history after it.
func crashScenarios(t *testing.T) []crashScenario {
	t.Helper()
	var voidTarget string
	return []crashScenario{
		{
			name:  "add --kind",
			order: engine.AnchorPairEventFirst,
			setup: func(t *testing.T, r *Router, _ *kindsClock) { require.NoError(t, r.prepareEngine()) },
			op: func(r *Router, c *kindsClock) (string, bool, error) {
				res, err := r.AnchorAdd(AnchorAddRequest{
					Label: "sobriety", Date: "2026-01-01", Kind: engine.AnchorKindQuit, Now: c.next(),
				})
				if err != nil {
					return "", false, err
				}
				return res.Event.ID, strings.HasPrefix(res.Ack, "Finished the earlier unfinished write."), nil
			},
			differ: func(r *Router, c *kindsClock) error {
				_, err := r.AnchorAdd(AnchorAddRequest{Label: "sobriety", Date: "2026-01-02", Now: c.next()})
				return err
			},
			verify: func(t *testing.T, a *storage.Adapter, stage string) {
				q := readQuit(t, a, "sobriety")
				if stage != "done" {
					assert.False(t, q.oldOK, "%s: no anchor appears in an older binary", stage)
					assert.False(t, q.currentOK, "%s: no anchor appears in this binary", stage)
					return
				}
				assert.Equal(t, quitReadings{old: 99, current: 99, oldOK: true, currentOK: true}, q)
			},
		},
		{
			name:  "slip",
			order: engine.AnchorPairCompatFirst,
			setup: func(t *testing.T, r *Router, c *kindsClock) {
				addAnchorOfKind(t, r, c, "sobriety", "2026-01-01", engine.AnchorKindQuit, 0, 0)
				_, err := r.AnchorSlip(AnchorSlipRequest{Label: "sobriety", DayArg: "2026-02-01", Now: c.next()})
				require.NoError(t, err)
			},
			op: func(r *Router, c *kindsClock) (string, bool, error) {
				return eventOp(r.AnchorSlip(AnchorSlipRequest{Label: "sobriety", DayArg: "2026-03-01", Now: c.next()}))
			},
			differ: func(r *Router, c *kindsClock) error {
				_, err := r.AnchorAdd(AnchorAddRequest{Label: "sobriety", Date: "2026-01-02", Now: c.next()})
				return err
			},
			verify: func(t *testing.T, a *storage.Adapter, stage string) {
				q := readQuit(t, a, "sobriety")
				switch stage {
				case pairStepIntent, pairStepFirst: // nothing fact-bearing landed: still from 02-01
					assert.Equal(t, quitReadings{old: 68, current: 68, oldOK: true, currentOK: true}, q, stage)
				default: // the re-date landed: both count from the slip, never longer
					assert.Equal(t, quitReadings{old: 40, current: 40, oldOK: true, currentOK: true}, q, stage)
				}
			},
		},
		{
			name:  "void of a slip",
			order: engine.AnchorPairEventFirst,
			setup: func(t *testing.T, r *Router, c *kindsClock) {
				addAnchorOfKind(t, r, c, "sobriety", "2026-01-01", engine.AnchorKindQuit, 0, 0)
				res, err := r.AnchorSlip(AnchorSlipRequest{Label: "sobriety", DayArg: "2026-03-01", Now: c.next()})
				require.NoError(t, err)
				voidTarget = res.Event.ID
			},
			op: func(r *Router, c *kindsClock) (string, bool, error) {
				return eventOp(r.AnchorVoid(AnchorVoidRequest{EventID: voidTarget, Now: c.next()}))
			},
			differ: func(r *Router, c *kindsClock) error {
				_, err := r.AnchorSlip(AnchorSlipRequest{Label: "sobriety", DayArg: "2026-03-05", Now: c.next()})
				return err
			},
			verify: func(t *testing.T, a *storage.Adapter, stage string) {
				q := readQuit(t, a, "sobriety")
				switch stage {
				case pairStepIntent, pairStepFirst: // the void has not landed: from the slip
					assert.Equal(t, quitReadings{old: 40, current: 40, oldOK: true, currentOK: true}, q, stage)
				case pairStepSecond: // the void landed: corrected here, conservatively shorter there
					assert.Equal(t, quitReadings{old: 40, current: 99, oldOK: true, currentOK: true}, q, stage)
				default:
					assert.Equal(t, quitReadings{old: 99, current: 99, oldOK: true, currentOK: true}, q, stage)
				}
			},
		},
		{
			name:  "kind to quit past a later use",
			order: engine.AnchorPairCompatFirst,
			setup: func(t *testing.T, r *Router, c *kindsClock) {
				addAnchorOfKind(t, r, c, "late-night-snacks", "2026-01-01", engine.AnchorKindLimit, 4, 90)
				_, err := r.AnchorUse(AnchorUseRequest{
					Label: "late-night-snacks", Intent: engine.AnchorIntentPlanned, DayArg: "2026-03-14", Now: c.next(),
				})
				require.NoError(t, err)
			},
			op: func(r *Router, c *kindsClock) (string, bool, error) {
				return eventOp(r.AnchorKind(AnchorKindRequest{
					Label: "late-night-snacks", Kind: engine.AnchorKindQuit, Now: c.next(),
				}))
			},
			differ: func(r *Router, c *kindsClock) error {
				_, err := r.AnchorUse(AnchorUseRequest{
					Label: "late-night-snacks", Intent: engine.AnchorIntentSlip, Now: c.next(),
				})
				return err
			},
			verify: func(t *testing.T, a *storage.Adapter, stage string) {
				q := readQuit(t, a, "late-night-snacks")
				f := foldAnchor(t, a, "late-night-snacks")
				switch stage {
				case pairStepIntent, pairStepFirst: // still a limit; an older binary's plain days-since
					assert.Equal(t, engine.AnchorKindLimit, f.Kind, stage)
					assert.Equal(t, 99, q.old, stage)
				case pairStepSecond: // the re-date landed: the older binary counts from the use
					assert.Equal(t, engine.AnchorKindLimit, f.Kind, "%s: the kind event has not landed", stage)
					assert.Equal(t, "2026-03-14", f.Floor, "%s: the pending re-date floors the date", stage)
					assert.Equal(t, 27, q.old, stage)
				default:
					assert.Equal(t, quitReadings{old: 27, current: 27, oldOK: true, currentOK: true}, q, stage)
				}
			},
		},
	}
}

// runCrashScenario drives one two-file verb to a crash before step, checks
// what landed and both binaries' readings, refuses a different change while
// the pair is unfinished, then retries and checks the finished write.
func runCrashScenario(t *testing.T, sc crashScenario, step string) {
	t.Helper()
	r, a, home := newBootedRouter(t)
	c := newKindsClock()
	sc.setup(t, r, c)
	anchorsBefore, eventsBefore := anchorFiles(t, home)
	intentsBefore := len(pairStates(t, a))

	r.pairFault = failAt(step)
	_, _, err := sc.op(r, c)
	r.pairFault = nil
	require.Error(t, err)

	states := pairStates(t, a)
	if step == pairStepIntent {
		assert.Contains(t, err.Error(), "nothing was saved")
		anchorsAfter, eventsAfter := anchorFiles(t, home)
		assert.Equal(t, string(anchorsBefore), string(anchorsAfter))
		assert.Equal(t, string(eventsBefore), string(eventsAfter))
	} else {
		assert.Contains(t, err.Error(), "retry the same command")
		require.Len(t, states, intentsBefore+1)
		assert.Equal(t, sc.order, states[intentsBefore].Intent.Order)
		assert.Equal(t, crashedStatus(sc.order, step), states[intentsBefore].Status)
	}
	sc.verify(t, a, step)

	pending := step != pairStepIntent
	if pending {
		_ = requireNothingWritten(t, home, ErrAnchorUnfinished, func() error { return sc.differ(r, c) })
	}

	receipt, resumed, err := sc.op(r, c)
	require.NoError(t, err, "the explicit retry finishes the write")
	assert.Equal(t, pending, resumed)
	if pending {
		assert.Equal(t, states[intentsBefore].Intent.Event.ID, receipt, "the retry returns the original receipt")
		assert.Len(t, pairStates(t, a), intentsBefore+1, "the retry reuses the intent")
	}
	requireUniqueReceipts(t, a)
	requireAllPairsComplete(t, a)
	sc.verify(t, a, "done")
}

// crashedStatus is the pair state a crash before step leaves: the intent alone
// before the first half, the first half alone before the second.
func crashedStatus(order, step string) string {
	switch {
	case step == pairStepFirst:
		return engine.AnchorPairIntentOnly
	case order == engine.AnchorPairCompatFirst:
		return engine.AnchorPairCompatOnly
	}
	return engine.AnchorPairEventOnly
}

// TestAnchorTwoFileCrashNeverFlatters fault-injects every two-file verb before
// each durable boundary — the pair intent, the first half, the second half —
// and holds both binaries' readings to the accepted history at the crash; a
// different change is refused while the pair is unfinished, and an explicit
// retry finishes it with the original receipt and no duplicate event.
func TestAnchorTwoFileCrashNeverFlatters(t *testing.T) {
	for _, sc := range crashScenarios(t) {
		for _, step := range []string{pairStepIntent, pairStepFirst, pairStepSecond} {
			t.Run(sc.name+"/crash before the "+step+" write", func(t *testing.T) {
				runCrashScenario(t, sc, step)
			})
		}
	}

	t.Run("a slip that moves nothing writes its event alone", func(t *testing.T) {
		r, a, _ := newBootedRouter(t)
		c := newKindsClock()
		addAnchorOfKind(t, r, c, "sobriety", "2026-01-01", engine.AnchorKindQuit, 0, 0)
		_, err := r.AnchorSlip(AnchorSlipRequest{Label: "sobriety", DayArg: "2026-03-20", Now: c.next()})
		require.NoError(t, err)
		intents := len(pairStates(t, a))

		r.pairFault = func(string) error { return errors.New("no boundary should be reached") }
		for _, day := range []string{"2026-03-01", "2026-03-20"} { // backdated, then duplicate-day
			_, err := r.AnchorSlip(AnchorSlipRequest{Label: "sobriety", DayArg: day, Now: c.next()})
			require.NoError(t, err)
		}
		r.pairFault = nil
		assert.Len(t, pairStates(t, a), intents, "no pair intent for a slip that does not re-date")
		q := readQuit(t, a, "sobriety")
		assert.Equal(t, quitReadings{old: 21, current: 21, oldOK: true, currentOK: true}, q)
	})
}

// TestOldBinaryQuitRunNeverLongerThanTruth runs slip, void and reclassify
// sequences and checks, after every step and at every pending boundary, that
// an older binary folding anchors.json alone (today's LatestAnchors →
// partitionAnchors path) and the current binary both read a quit run no longer
// than the truth the accepted history permits — including when an older
// binary writes between an interrupted write and its retry.
func TestOldBinaryQuitRunNeverLongerThanTruth(t *testing.T) {
	steps := []sequenceStep{
		{"a slip", seqSlip("2026-03-01"), 40, true},
		{"a backdated slip", seqSlip("2026-02-01"), 40, true},
		{"a duplicate-day slip", seqSlip("2026-03-01"), 40, true},
		{"void one of the two 03-01 slips", seqVoid("slip 2026-03-01"), 40, true},
		{"reclassify as a limit", seqKind(engine.AnchorKindLimit, 4, 90), 0, false},
		{"a planned use while a limit", seqUse("2026-03-20"), 0, false},
		{"reclassify back to a quit", seqKind(engine.AnchorKindQuit, 0, 0), 21, true},
		{"void the use: the other 03-01 slip still stands", seqVoid("use 2026-03-20"), 40, true},
		{"a slip today", seqSlip(kindsToday), 0, true},
	}
	for _, crashAt := range []string{"", pairStepFirst, pairStepSecond} {
		name := "clean writes"
		if crashAt != "" {
			name = "every two-file write crashes before the " + crashAt + " write, then is retried"
		}
		t.Run(name, func(t *testing.T) {
			crashes := runSequence(t, steps, crashAt)
			if crashAt != "" {
				// The first slip, the reclassify back to a quit and today's slip
				// re-date; nothing else in the sequence writes both files.
				assert.Equal(t, 3, crashes, "every two-file step crashed once")
			}
		})
	}
	t.Run("an older binary writes between an interrupted write and its retry", testOlderBinaryBetweenCrashAndRetry)
}

// sequenceStep is one write in an old-binary honesty sequence. truth is the
// quit run the history permits once it lands, read only when isQuit: while the
// anchor is a limit no binary claims a quit run (an older binary's limit
// reading is the documented plain days-since).
type sequenceStep struct {
	name   string
	run    sequenceWrite
	truth  int
	isQuit bool
}

// sequenceWrite runs one write; ids records receipts by a key later voids name.
type sequenceWrite func(r *Router, c *kindsClock, ids map[string]string) (AnchorEventResult, error)

func seqSlip(day string) sequenceWrite {
	return func(r *Router, c *kindsClock, ids map[string]string) (AnchorEventResult, error) {
		res, err := r.AnchorSlip(AnchorSlipRequest{Label: "sobriety", DayArg: day, Now: c.next()})
		if err == nil && !res.Resumed {
			ids["slip "+day] = res.Event.ID
		}
		return res, err
	}
}

func seqVoid(key string) sequenceWrite {
	return func(r *Router, c *kindsClock, ids map[string]string) (AnchorEventResult, error) {
		return r.AnchorVoid(AnchorVoidRequest{EventID: ids[key], Now: c.next()})
	}
}

func seqKind(kind string, allowance, window int) sequenceWrite {
	return func(r *Router, c *kindsClock, _ map[string]string) (AnchorEventResult, error) {
		return r.AnchorKind(AnchorKindRequest{
			Label: "sobriety", Kind: kind, Allowance: allowance, Window: window, Now: c.next(),
		})
	}
}

func seqUse(day string) sequenceWrite {
	return func(r *Router, c *kindsClock, ids map[string]string) (AnchorEventResult, error) {
		res, err := r.AnchorUse(AnchorUseRequest{
			Label: "sobriety", Intent: engine.AnchorIntentPlanned, DayArg: day, Now: c.next(),
		})
		if err == nil {
			ids["use "+day] = res.Event.ID
		}
		return res, err
	}
}

// runSequence runs the steps on a fresh quit, crashing every two-file write
// before crashAt (none when empty) and retrying it, and returns how many writes
// crashed. After every step and at every crash both binaries are held to the
// truth the accepted history permits.
func runSequence(t *testing.T, steps []sequenceStep, crashAt string) (crashes int) {
	t.Helper()
	r, a, _ := newBootedRouter(t)
	c := newKindsClock()
	addAnchorOfKind(t, r, c, "sobriety", "2026-01-01", engine.AnchorKindQuit, 0, 0)
	truth, wasQuit := 99, true
	requireNeverLonger(t, readQuit(t, a, "sobriety"), truth, "the start")
	ids := map[string]string{}

	for _, s := range steps {
		if crashSequenceStep(t, r, a, c, ids, s, crashAt, truth, wasQuit) {
			crashes++
		}
		truth, wasQuit = s.truth, s.isQuit
		if s.isQuit {
			q := readQuit(t, a, "sobriety")
			requireNeverLonger(t, q, truth, s.name)
			require.True(t, q.currentOK, s.name)
			assert.Equal(t, truth, q.current, "%s: the current binary reads the truth", s.name)
		}
		requireAllPairsComplete(t, a)
	}
	requireUniqueReceipts(t, a)
	return crashes
}

// crashSequenceStep runs one step with the fault seam set to crashAt. Only a
// two-file write can crash; when one does, both binaries are held to the
// accepted history at the crash — the history before the step until a
// fact-bearing half lands, the history after it once one has — and the write
// is retried explicitly. It reports whether the step crashed.
func crashSequenceStep(
	t *testing.T, r *Router, a *storage.Adapter, c *kindsClock, ids map[string]string,
	s sequenceStep, crashAt string, truth int, wasQuit bool,
) bool {
	t.Helper()
	intents := len(pairStates(t, a))
	if crashAt != "" {
		r.pairFault = failAt(crashAt)
	}
	_, err := s.run(r, c, ids)
	r.pairFault = nil
	if err == nil {
		return false
	}
	require.Len(t, pairStates(t, a), intents+1, "%s: %v", s.name, err)
	accepted, claimed := truth, wasQuit
	if crashAt == pairStepSecond {
		accepted, claimed = s.truth, s.isQuit
	}
	if claimed {
		requireNeverLonger(t, readQuit(t, a, "sobriety"), accepted, s.name+" (pending)")
	}
	res, err := s.run(r, c, ids)
	require.NoError(t, err, "%s: the retry", s.name)
	assert.True(t, res.Resumed, s.name)
	return true
}

// testOlderBinaryBetweenCrashAndRetry lands an older binary's write — a
// rename, a correction, a sunset, a mint of the reserved id — between an
// interrupted two-file write and its explicit retry.
func testOlderBinaryBetweenCrashAndRetry(t *testing.T) {
	t.Run("a rename after the re-date landed", func(t *testing.T) {
		r, a, home := newBootedRouter(t)
		c := newKindsClock()
		addAnchorOfKind(t, r, c, "sobriety", "2026-01-01", engine.AnchorKindQuit, 0, 0)
		r.pairFault = failAt(pairStepSecond)
		_, err := r.AnchorSlip(AnchorSlipRequest{Label: "sobriety", DayArg: "2026-03-01", Now: c.next()})
		r.pairFault = nil
		require.Error(t, err)

		olderBinaryAppend(t, home, olderBinaryRecord(latestFor(t, a, "sobriety"), "clear-head", "2026-03-01"))
		requireNeverLonger(t, readQuit(t, a, "clear-head"), 40, "after the older binary's rename")

		res, err := r.AnchorSlip(AnchorSlipRequest{Label: "clear-head", DayArg: "2026-03-01", Now: c.next()})
		require.NoError(t, err)
		assert.True(t, res.Resumed)
		assert.Equal(t, quitReadings{old: 40, current: 40, oldOK: true, currentOK: true}, readQuit(t, a, "clear-head"))
		assert.Equal(t, "clear-head", latestFor(t, a, "clear-head").Label, "the rename stands")
		requireAllPairsComplete(t, a)
	})

	t.Run("a rename before the re-date landed keeps the new name", func(t *testing.T) {
		r, a, home := newBootedRouter(t)
		c := newKindsClock()
		addAnchorOfKind(t, r, c, "sobriety", "2026-01-01", engine.AnchorKindQuit, 0, 0)
		r.pairFault = failAt(pairStepFirst)
		_, err := r.AnchorSlip(AnchorSlipRequest{Label: "sobriety", DayArg: "2026-03-01", Now: c.next()})
		r.pairFault = nil
		require.Error(t, err)

		olderBinaryAppend(t, home, olderBinaryRecord(latestFor(t, a, "sobriety"), "clear-head", "2026-01-01"))
		res, err := r.AnchorSlip(AnchorSlipRequest{Label: "clear-head", DayArg: "2026-03-01", Now: c.next()})
		require.NoError(t, err)
		assert.True(t, res.Resumed)
		assert.Contains(t, res.Ack, "one more record carries clear-head from 2026-03-01")
		assert.Equal(t, "clear-head", latestFor(t, a, "clear-head").Label, "the planned record never undoes the rename")
		assert.Equal(t, quitReadings{old: 40, current: 40, oldOK: true, currentOK: true}, readQuit(t, a, "clear-head"))
		q := engine.ComputeQuitStats(foldAnchor(t, a, "clear-head"), kindsTodayDate())
		assert.Equal(t, 59, q.LongestRun, "the realign moved no start")
		assert.Equal(t, 1, q.Slips)
		requireAllPairsComplete(t, a)
	})

	t.Run("a correction past the planned restore never lets the restore flatter", func(t *testing.T) {
		r, a, home := newBootedRouter(t)
		c := newKindsClock()
		addAnchorOfKind(t, r, c, "sobriety", "2026-01-01", engine.AnchorKindQuit, 0, 0)
		slipRes, err := r.AnchorSlip(AnchorSlipRequest{Label: "sobriety", DayArg: "2026-03-01", Now: c.next()})
		require.NoError(t, err)
		r.pairFault = failAt(pairStepSecond)
		_, err = r.AnchorVoid(AnchorVoidRequest{EventID: slipRes.Event.ID, Now: c.next()})
		r.pairFault = nil
		require.Error(t, err)

		// The start really was 01-10: with the slip voided the truth is 90.
		olderBinaryAppend(t, home, olderBinaryRecord(latestFor(t, a, "sobriety"), "sobriety", "2026-01-10"))
		truth := daysTo(t, "2026-01-10")
		requireNeverLonger(t, readQuit(t, a, "sobriety"), truth, "after the older binary's correction")

		res, err := r.AnchorVoid(AnchorVoidRequest{EventID: slipRes.Event.ID, Now: c.next()})
		require.NoError(t, err)
		assert.True(t, res.Resumed)
		assert.Equal(t, "2026-01-01", res.Compat.Date, "the exact planned half completes the pair")
		q := readQuit(t, a, "sobriety")
		assert.Equal(t, quitReadings{old: truth, current: truth, oldOK: true, currentOK: true}, q,
			"the planned restore would read 99 in an older binary; the realign keeps it at the truth")
		requireAllPairsComplete(t, a)
	})

	t.Run("a correction past the planned re-date never lets the re-date flatter", func(t *testing.T) {
		r, a, home := newBootedRouter(t)
		c := newKindsClock()
		addAnchorOfKind(t, r, c, "sobriety", "2026-01-01", engine.AnchorKindQuit, 0, 0)
		r.pairFault = failAt(pairStepFirst)
		_, err := r.AnchorSlip(AnchorSlipRequest{Label: "sobriety", DayArg: "2026-03-01", Now: c.next()})
		r.pairFault = nil
		require.Error(t, err)

		olderBinaryAppend(t, home, olderBinaryRecord(latestFor(t, a, "sobriety"), "sobriety", "2026-03-15"))
		truth := daysTo(t, "2026-03-15")
		res, err := r.AnchorSlip(AnchorSlipRequest{Label: "sobriety", DayArg: "2026-03-01", Now: c.next()})
		require.NoError(t, err)
		assert.True(t, res.Resumed)
		q := readQuit(t, a, "sobriety")
		assert.Equal(t, quitReadings{old: truth, current: truth, oldOK: true, currentOK: true}, q)
		assert.Equal(t, "2026-03-15", foldAnchor(t, a, "sobriety").Start, "the realign carries the corrected start")
		requireAllPairsComplete(t, a)
	})

	t.Run("a sunset in between refuses a retry that would bring the anchor back", func(t *testing.T) {
		r, a, home := newBootedRouter(t)
		c := newKindsClock()
		addAnchorOfKind(t, r, c, "sobriety", "2026-01-01", engine.AnchorKindQuit, 0, 0)
		slipRes, err := r.AnchorSlip(AnchorSlipRequest{Label: "sobriety", DayArg: "2026-03-01", Now: c.next()})
		require.NoError(t, err)
		r.pairFault = failAt(pairStepSecond)
		_, err = r.AnchorVoid(AnchorVoidRequest{EventID: slipRes.Event.ID, Now: c.next()})
		r.pairFault = nil
		require.Error(t, err)

		sunset := olderBinaryRecord(latestFor(t, a, "sobriety"), "sobriety", "2026-03-01")
		sunset.State = engine.AnchorStateSunset
		olderBinaryAppend(t, home, sunset)
		_ = requireNothingWritten(t, home, ErrAnchorAlreadySunset, func() error {
			_, voidErr := r.AnchorVoid(AnchorVoidRequest{EventID: slipRes.Event.ID, Now: c.next()})
			return voidErr
		})
		_, active := oldDaysSince(t, a, "sobriety")
		assert.False(t, active, "the retired anchor stays retired")
	})

	t.Run("an add --kind whose reserved id an older binary minted blocks nothing", func(t *testing.T) {
		r, a, home := newBootedRouter(t)
		c := newKindsClock()
		r.pairFault = failAt(pairStepSecond)
		_, err := r.AnchorAdd(AnchorAddRequest{
			Label: "sobriety", Date: "2026-01-01", Kind: engine.AnchorKindHabit, Now: c.next(),
		})
		r.pairFault = nil
		require.Error(t, err)
		reserved := pairStates(t, a)[0].Intent.Event.AnchorID

		// An older binary mints from anchors.json alone: the same first slot.
		olderBinaryAppend(t, home, v3Anchor{
			ID: reserved, Label: "first-gate", Date: "2026-05-01", RecordedAt: "2026-04-10T17:00:00Z",
		})
		gate := foldAnchor(t, a, "first-gate")
		assert.Equal(t, engine.AnchorKindMilestone, gate.Kind, "the dangling kind event reclassifies nothing")

		_, err = r.AnchorKind(AnchorKindRequest{Label: "first-gate", Kind: engine.AnchorKindQuit, Now: c.next()})
		require.NoError(t, err, "the other anchor is free to change")

		res, err := r.AnchorAdd(AnchorAddRequest{
			Label: "sobriety", Date: "2026-01-01", Kind: engine.AnchorKindHabit, Now: c.next(),
		})
		require.NoError(t, err, "the label is free to record again")
		assert.NotEqual(t, reserved, res.Anchor.ID, "never the claimed id")
		assert.Equal(t, engine.AnchorKindHabit, foldAnchor(t, a, "sobriety").Kind)
		assert.Equal(t, engine.AnchorKindQuit, foldAnchor(t, a, "first-gate").Kind)
	})
}

// TestOlderBinaryAppendKeepsTheEventStore is the guard on the stand-in itself:
// an older binary's write round-trips every v3 field it knows.
func TestOlderBinaryAppendKeepsTheEventStore(t *testing.T) {
	r, a, home := newBootedRouter(t)
	c := newKindsClock()
	addAnchorOfKind(t, r, c, "sobriety", "2026-01-01", engine.AnchorKindQuit, 0, 0)
	olderBinaryAppend(t, home, v3Anchor{
		ID: "anchor_2026_04_10_b", Label: "first-gate", Date: "2026-05-01",
		RecordedAt: time.Date(2026, time.April, 10, 17, 0, 0, 0, time.UTC).Format(time.RFC3339),
	})
	assert.Equal(t, engine.AnchorKindQuit, foldAnchor(t, a, "sobriety").Kind, "the kind survives an older binary's write")
	assert.Equal(t, engine.AnchorKindMilestone, foldAnchor(t, a, "first-gate").Kind)
}
