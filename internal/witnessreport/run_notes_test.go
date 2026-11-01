package witnessreport

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/lucidtest"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/storage"
)

// notesRunnerOptions is a scheduler-root Options over a synthetic fixture week,
// real temp prompt files, a fresh scripted fake per compose, and the given
// store and notes_file.
func notesRunnerOptions(t *testing.T, store *storage.Adapter, notesFile string) Options {
	t.Helper()
	dir := t.TempDir()
	numbers, records := fixtureReaders(t, "miss-heavy-week.json")
	return Options{
		Store: store,
		Config: config.WitnessReportConfig{
			Enabled:      true,
			Mode:         config.WitnessReportModePreview,
			Time:         "09:00",
			Weekday:      1,
			SystemPrompt: writeFile(t, dir, "system.md", "You are the witness-report voice."),
			Template:     writeFile(t, dir, "template.md", composeGoldenTemplate),
			NotesFile:    notesFile,
		},
		Provider: config.ProviderConfig{Backend: "fake"},
		Numbers:  numbers,
		Records:  records,
		Build: func(config.ProviderConfig) (provider.Provider, error) {
			return &provider.Fake{Script: []provider.Exchange{{Content: wellFormedReply}}}, nil
		},
	}
}

// runnerComposer is the Composer a NewRunner wired.
func runnerComposer(t *testing.T, r *Runner) *Composer {
	t.Helper()
	c, ok := r.compose.(*Composer)
	require.True(t, ok, "NewRunner wires the real Composer")
	return c
}

// TestNewRunner_WiresNotesFileAndHome proves the scheduler composition root
// wires the week notes exactly like asks_file: NotesFile from the
// witness_report config block and Home from the store, so the scheduled report
// reads the same one file the dry run does and refuses a path inside the Lucid
// home. Without a store the home is empty and a configured notes file fails
// closed instead of being read.
func TestNewRunner_WiresNotesFileAndHome(t *testing.T) {
	outside := writeNotes(t, notesWrittenFresh, composeNotesBody)

	t.Run("wires the config path and the store home", func(t *testing.T) {
		store := runStore(t)
		c := runnerComposer(t, NewRunner(notesRunnerOptions(t, store, outside)))
		assert.Equal(t, outside, c.notesFile)
		assert.Equal(t, store.Home(), c.home)
		require.NotEmpty(t, c.home)
	})

	t.Run("outside the home is included", func(t *testing.T) {
		c := runnerComposer(t, NewRunner(notesRunnerOptions(t, runStore(t), outside)))
		r, err := c.Compose(context.Background(), reportNow())
		require.NoError(t, err)
		assert.Equal(t, NotesIncluded, r.NotesStatus)
		assert.Equal(t, composeNotesBody, r.Notes)
	})

	t.Run("inside the store home is invalid", func(t *testing.T) {
		store := runStore(t)
		inside := writeFile(t, store.Home(), "week-notes.md", notesFile(notesWrittenFresh, composeNotesBody))
		c := runnerComposer(t, NewRunner(notesRunnerOptions(t, store, inside)))
		r, err := c.Compose(context.Background(), reportNow())
		require.NoError(t, err)
		assert.Equal(t, NotesInvalid, r.NotesStatus)
		assert.Equal(t, notesProblemInsideHome, r.NotesProblem)
		assert.Empty(t, r.Notes)
	})

	t.Run("no store fails closed", func(t *testing.T) {
		c := runnerComposer(t, NewRunner(notesRunnerOptions(t, nil, outside)))
		assert.Empty(t, c.home)
		r, err := c.Compose(context.Background(), reportNow())
		require.NoError(t, err)
		assert.Equal(t, NotesInvalid, r.NotesStatus)
		assert.Equal(t, notesProblemHomeUnset, r.NotesProblem)
	})

	t.Run("no key is off", func(t *testing.T) {
		c := runnerComposer(t, NewRunner(notesRunnerOptions(t, runStore(t), "")))
		assert.Empty(t, c.notesFile)
		r, err := c.Compose(context.Background(), reportNow())
		require.NoError(t, err)
		assert.Empty(t, r.NotesStatus)
	})

	t.Run("a missing file is unreadable", func(t *testing.T) {
		c := runnerComposer(t, NewRunner(notesRunnerOptions(t, runStore(t), filepath.Join(t.TempDir(), "missing.md"))))
		r, err := c.Compose(context.Background(), reportNow())
		require.NoError(t, err)
		assert.Equal(t, NotesUnreadable, r.NotesStatus)
	})
}

// --- the week-notes alert (ADR-0015 §5) ---------------------------------------

// notesAlertSentinel is a synthetic notes body that no alert may ever carry: a
// distinctive marker inside an anonymous user's week.
const notesAlertSentinel = "NOTES-BODY-SENTINEL — a heavy stretch at work; he still closed out every night."

// notesAlertText is the alert ADR-0015 §5 pins, spelled out as a literal rather
// than built from the package's own helper, so the test independently proves
// the string the operator reads.
const notesAlertText = "Lucid weekly witness report for %s went out without its week notes — the notes file is %s: %s. Check witness_report.notes_file."

// notesStatusReport is a delivered report whose compose pass left the given
// week-notes status and reason — exactly the fields the reader sets.
func notesStatusReport(status, problem string) Report {
	r := sampleReport()
	r.NotesStatus, r.NotesProblem = status, problem
	if status == NotesIncluded {
		r.Notes, r.NotesWritten = notesAlertSentinel, "2026-07-12"
	}
	return r
}

// notesAlertsOf returns the alerts that name the week notes.
func notesAlertsOf(alerts []lucidtest.SendRecord) []lucidtest.SendRecord {
	var out []lucidtest.SendRecord
	for _, a := range alerts {
		if strings.Contains(a.Text, "week notes") {
			out = append(out, a)
		}
	}
	return out
}

// receiptAtAlertDeliverer is the shared fake plus one probe: at each alert it
// reads the week's receipt, so a test proves the notes alert fires only after
// the delivered week was recorded.
type receiptAtAlertDeliverer struct {
	lucidtest.FakeDeliverer

	store              *storage.Adapter
	deliveredAtAlert   []bool
	receiptReadFailure error
}

func (d *receiptAtAlertDeliverer) Send(ctx context.Context, channel, text string) error {
	rec, ok, err := d.store.ReadWitnessReportReceipt()
	if err != nil {
		d.receiptReadFailure = err
	}
	d.deliveredAtAlert = append(d.deliveredAtAlert, ok && rec.Verified && rec.MessageID != "")
	return d.FakeDeliverer.Send(ctx, channel, text)
}

// TestWitnessRunner_NotesAlertInvalidUnreadable: a week whose configured notes
// file was invalid or unreadable still delivers its report — to the witness
// channel in auto mode — and then raises exactly one private alert, to the
// operator's user channel only, after the receipt is persisted. It names the
// status and the fixed reason.
func TestWitnessRunner_NotesAlertInvalidUnreadable(t *testing.T) {
	cases := []struct {
		name, status, problem string
	}{
		{"invalid future date", NotesInvalid, notesProblemFuture},
		{"invalid inside the home", NotesInvalid, notesProblemInsideHome},
		{"invalid unpackable body", NotesInvalid, notesProblemUnpackable},
		{"unreadable missing path", NotesUnreadable, notesProblemMissing},
		{"unreadable directory", NotesUnreadable, notesProblemDirectory},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			comp := &runFakeComposer{report: notesStatusReport(tc.status, tc.problem)}
			r, store := newRunner(t, comp, nil)
			del := &receiptAtAlertDeliverer{store: store}
			r.deliver = del
			r.mode = config.WitnessReportModeAuto

			out, err := r.Fire(context.Background(), reportNow())
			require.NoError(t, err, "a notes problem never fails the fire")
			assert.True(t, out.Delivered, "the report goes out without its notes")
			require.Len(t, del.Embeds, 1)
			assert.Equal(t, engine.ChannelWitness, del.Embeds[0].Channel, "the report reaches its audience")

			require.Len(t, del.Alerts, 1, "exactly one alert")
			assert.Equal(t, engine.ChannelUser, del.Alerts[0].Channel, "the notes alert is private: the user channel only")
			assert.Equal(t, fmt.Sprintf(notesAlertText, out.Week, tc.status, tc.problem), del.Alerts[0].Text)
			require.NoError(t, del.receiptReadFailure)
			assert.Equal(t, []bool{true}, del.deliveredAtAlert, "the alert fires after the delivered week is recorded")

			rec, ok, rerr := store.ReadWitnessReportReceipt()
			require.NoError(t, rerr)
			require.True(t, ok)
			assert.True(t, rec.Verified)
		})
	}
}

// TestWitnessRunner_NoNotesAlertStaleEmptyOff: off, stale, empty, and
// included are not problems — stale and empty are the normal states of a week
// without notes — so the report goes out with no alert at all.
func TestWitnessRunner_NoNotesAlertStaleEmptyOff(t *testing.T) {
	for _, status := range []string{"", NotesStale, NotesEmpty, NotesIncluded} {
		name := status
		if name == "" {
			name = "off"
		}
		t.Run(name, func(t *testing.T) {
			del := &lucidtest.FakeDeliverer{}
			r, _ := newRunner(t, &runFakeComposer{report: notesStatusReport(status, "")}, del)

			out, err := r.Fire(context.Background(), reportNow())
			require.NoError(t, err)
			assert.True(t, out.Delivered)
			assert.Empty(t, del.Alerts, "no alert for %q notes", name)
		})
	}
}

// TestWitnessRunner_NotesAlertNeverQuotesBody: the alert names only the status
// and the fixed reason. Even with a notes body on the report, no alert — notes
// or otherwise — carries any of its text.
func TestWitnessRunner_NotesAlertNeverQuotesBody(t *testing.T) {
	for _, status := range []string{NotesInvalid, NotesUnreadable} {
		t.Run(status, func(t *testing.T) {
			rep := notesStatusReport(status, notesProblemCannotRead)
			// The reader never returns a body for a problem status; set one anyway
			// so the test proves the alert cannot quote it even if one rode along.
			rep.Notes = notesAlertSentinel
			rep.SafetyTripped, rep.Fallback = true, true
			del := &lucidtest.FakeDeliverer{}
			r, _ := newRunner(t, &runFakeComposer{report: rep}, del)

			_, err := r.Fire(context.Background(), reportNow())
			require.NoError(t, err)
			require.Len(t, notesAlertsOf(del.Alerts), 1)
			for _, a := range del.Alerts {
				assert.NotContains(t, a.Text, "NOTES-BODY-SENTINEL")
				assert.NotContains(t, a.Text, "closed out every night")
			}
		})
	}
}

// TestWitnessRunner_NotesAndSafetyTripBothAlert: the two post-delivery alerts
// are independent — a week whose model prose tripped the scan and whose notes
// file was invalid raises both, the safety-trip alert first, each once.
func TestWitnessRunner_NotesAndSafetyTripBothAlert(t *testing.T) {
	rep := notesStatusReport(NotesInvalid, notesProblemOverCap)
	rep.SafetyTripped, rep.Fallback = true, true
	del := &lucidtest.FakeDeliverer{}
	r, _ := newRunner(t, &runFakeComposer{report: rep}, del)

	out, err := r.Fire(context.Background(), reportNow())
	require.NoError(t, err)
	assert.True(t, out.Delivered)
	assert.True(t, out.SafetyTripped)
	require.Len(t, del.Alerts, 2, "both alerts fire")
	assert.Contains(t, del.Alerts[0].Text, "tripped the witness-safe scan")
	assert.Equal(t, fmt.Sprintf(notesAlertText, out.Week, NotesInvalid, notesProblemOverCap), del.Alerts[1].Text)
	for _, a := range del.Alerts {
		assert.Equal(t, engine.ChannelUser, a.Channel)
	}
}

// TestWitnessRunner_ResumedBatchReplaysNotes: a multi-message report is a
// frozen batch, and its week-notes fields travel inside the frozen parts. A
// batch stopped partway resumes from the receipt without re-composing, and
// every part — the notes fields included — goes out byte-identical to what the
// first attempt rendered. A resumed attempt carries no composed report, so it
// raises no notes alert (ADR-0015 §5, the resume gap): only the loud
// partial-delivery alert from the first attempt fires.
func TestWitnessRunner_ResumedBatchReplaysNotes(t *testing.T) {
	notesBody := shortWeekNotes + "\n\n" + weekDayNotes() + "\n\n" + weekSummaryNotes
	stopAtPart2 := func() *lucidtest.FakeDeliverer {
		return &lucidtest.FakeDeliverer{VerifyErrFor: map[string]error{"msg-2": errors.New("discord 503")}}
	}

	t.Run("included notes replay byte-identical from the receipt parts", func(t *testing.T) {
		rep := withNotes(oversizedDetailReport(), notesBody)
		parts := RenderEmbeds(rep)
		require.Greater(t, len(parts), 2, "the fixture spans several messages")
		require.Equal(t, notesBody, reassembleSection(parts, fieldWeekNotes), "the rendered notes are the body, losslessly")

		comp := &runFakeComposer{report: rep}
		del := stopAtPart2()
		r, store := newRunner(t, comp, del)

		_, err := r.Fire(context.Background(), reportNow())
		require.Error(t, err, "the first attempt stops partway")
		frozen, ok, rerr := store.ReadWitnessReportReceipt()
		require.NoError(t, rerr)
		require.True(t, ok)
		require.True(t, frozen.Pending())
		require.Equal(t, parts, thawParts(frozen.Parts), "the whole batch, notes included, was frozen before the first send")
		require.NotEmpty(t, sectionFieldsOf(thawParts(frozen.Parts), fieldWeekNotes), "the notes travel in the frozen parts")

		delete(del.VerifyErrFor, "msg-2")
		out, err := r.Fire(context.Background(), reportNow().Add(time.Hour))
		require.NoError(t, err)
		assert.True(t, out.Delivered)
		assert.Equal(t, 1, comp.calls, "the retry resumes the frozen batch, never re-composes")
		assert.Equal(t, parts, sentEmbeds(del), "every part, the notes included, landed once, byte-identical")
		assert.Equal(t, notesBody, reassembleSection(sentEmbeds(del), fieldWeekNotes), "friends read the approved body exactly")
		require.Len(t, del.Alerts, 1, "only the partial-delivery alert")
		assert.Contains(t, del.Alerts[0].Text, "is incomplete")
		assert.Empty(t, notesAlertsOf(del.Alerts))
	})

	t.Run("a resumed batch raises no notes alert", func(t *testing.T) {
		rep := notesStatusReport(NotesInvalid, notesProblemFuture)
		rep.AnchorDetail = oversizedDetailReport().AnchorDetail
		rep.Narrative = longWarmRead()
		require.Greater(t, len(RenderEmbeds(rep)), 1, "the fixture spans several messages")
		comp := &runFakeComposer{report: rep}
		del := stopAtPart2()
		r, _ := newRunner(t, comp, del)

		_, err := r.Fire(context.Background(), reportNow())
		require.Error(t, err)
		require.Len(t, del.Alerts, 1)
		assert.Empty(t, notesAlertsOf(del.Alerts), "a partial delivery never reached postSend")

		delete(del.VerifyErrFor, "msg-2")
		out, err := r.Fire(context.Background(), reportNow().Add(time.Hour))
		require.NoError(t, err)
		assert.True(t, out.Delivered)
		assert.Equal(t, 1, comp.calls)
		require.Len(t, del.Alerts, 1, "the resumed attempt adds no alert")
		assert.Empty(t, notesAlertsOf(del.Alerts), "a resumed batch carries no composed report, so it never re-alerts")
	})
}
