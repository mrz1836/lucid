package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/storage"
)

// witnessNotesBody is a synthetic week-notes body: an anonymous user's week, no
// names.
const witnessNotesBody = "A heavy stretch at work; he still closed out every night. " +
	"A friend's call on Thursday helped. Ask him how the new routine is landing."

// witnessNotesFile is a week-notes file dated inside the 2026-07-14 → 2026-07-20
// window the witness CLI tests' clock (Monday 2026-07-20) reports on.
const witnessNotesFile = "---\nwritten: 2026-07-19\n---\n" + witnessNotesBody + "\n"

// seedWitnessNotesHome scaffolds an isolated Ledger (wired through LUCID_HOME)
// with an enabled preview-mode witness report whose notes_file is whatever
// notesPath returns for that home — "" leaves the key unset.
func seedWitnessNotesHome(t *testing.T, notesPath func(home string) string) {
	t.Helper()
	home := isolatedHome(t)
	store := storage.New(home)
	_, err := store.Scaffold()
	require.NoError(t, err)

	dir := t.TempDir()
	sys := filepath.Join(dir, "system_prompt.md")
	tmpl := filepath.Join(dir, "template.md")
	require.NoError(t, os.WriteFile(sys, []byte("WITNESS VOICE"), 0o600))
	require.NoError(t, os.WriteFile(tmpl, []byte("WITNESS TEMPLATE"), 0o600))

	cfg := config.Default()
	cfg.WitnessReport = config.WitnessReportConfig{
		Enabled: true, Mode: config.WitnessReportModePreview, Time: "09:00", Weekday: 1,
		SystemPrompt: sys, Template: tmpl, NotesFile: notesPath(home),
	}
	b, err := cfg.Marshal()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(store.ConfigPath(), b, 0o600))
}

// witnessDryRunJSON runs `lucid witness report --dry-run --json` against the
// seeded home with a scripted fake provider and returns the decoded report and
// the one request the model received.
func witnessDryRunJSON(t *testing.T) (map[string]any, string) {
	t.Helper()
	withClock(t, time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC))
	fake := &provider.Fake{Script: []provider.Exchange{{Content: witnessSlotReply}}}
	withServeProvider(t, fake)

	out, _, err := runWitness(t, "report", "--dry-run", "--json")
	require.NoError(t, err)
	var report map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &report))
	require.Len(t, fake.Requests, 1)
	require.Len(t, fake.Requests[0].Messages, 1)
	return report, fake.Requests[0].Messages[0].Content
}

// TestWitnessReportDryRun_NotesWiring proves the on-demand composition root
// wires the week notes exactly like asks_file: NotesFile from the
// witness_report config block and Home from the booted store. A fresh notes
// file outside the Lucid home is included and reaches the model; one inside
// LUCID_HOME is refused unread as invalid; no key adds no notes key at all.
func TestWitnessReportDryRun_NotesWiring(t *testing.T) {
	t.Run("outside the home is included", func(t *testing.T) {
		seedWitnessNotesHome(t, func(string) string {
			p := filepath.Join(t.TempDir(), "week-notes.md")
			require.NoError(t, os.WriteFile(p, []byte(witnessNotesFile), 0o600))
			return p
		})
		report, request := witnessDryRunJSON(t)

		assert.Equal(t, "included", report["notes_status"])
		assert.Equal(t, witnessNotesBody, report["notes"])
		assert.Equal(t, "2026-07-19", report["notes_written"])
		assert.NotContains(t, report, "notes_problem")
		assert.Contains(t, request, "BEGIN WEEK NOTES\n"+witnessNotesBody+"\nEND WEEK NOTES")
	})

	t.Run("inside LUCID_HOME is invalid", func(t *testing.T) {
		seedWitnessNotesHome(t, func(home string) string {
			p := filepath.Join(home, "week-notes.md")
			require.NoError(t, os.WriteFile(p, []byte(witnessNotesFile), 0o600))
			return p
		})
		report, request := witnessDryRunJSON(t)

		assert.Equal(t, "invalid", report["notes_status"])
		assert.Equal(t, "notes_file is inside the Lucid home", report["notes_problem"])
		assert.NotContains(t, report, "notes")
		assert.NotContains(t, request, "BEGIN WEEK NOTES")
		assert.NotContains(t, request, "A friend's call on Thursday helped.")
	})

	t.Run("no key carries no notes key", func(t *testing.T) {
		seedWitnessNotesHome(t, func(string) string { return "" })
		report, request := witnessDryRunJSON(t)

		for _, k := range []string{"notes", "notes_status", "notes_written", "notes_problem"} {
			assert.NotContains(t, report, k)
		}
		assert.NotContains(t, request, "BEGIN WEEK NOTES")
	})
}

// witnessNotesCase is one week-notes state the CLI reports on, seeded through
// a real notes file against the witness CLI tests' clock (Monday 2026-07-20,
// window 2026-07-14 → 2026-07-20), with the exact status text ADR-0015 §13
// pins for it — "" when nothing is said (off and included).
type witnessNotesCase struct {
	name   string
	file   string // the notes file's bytes; ignored when unset or absent
	unset  bool   // no notes_file key at all
	absent bool   // the configured path does not exist
	status string // the status text after "the notes file is "
}

func witnessNotesCases() []witnessNotesCase {
	return []witnessNotesCase{
		{name: "off", unset: true},
		{name: "included", file: witnessNotesFile},
		{name: "stale", file: "---\nwritten: 2026-07-13\n---\n" + witnessNotesBody + "\n", status: "stale"},
		{name: "empty", file: "---\nwritten: 2026-07-19\n---\n\n", status: "empty"},
		{name: "invalid", file: "---\nwritten: 2026-07-21\n---\n" + witnessNotesBody + "\n", status: "invalid: written is in the future"},
		{name: "unreadable", absent: true, status: "unreadable: the path does not exist"},
	}
}

// seedNotesCase scaffolds the isolated home for one case.
func seedNotesCase(t *testing.T, tc witnessNotesCase) {
	t.Helper()
	seedWitnessNotesHome(t, func(string) string {
		if tc.unset {
			return ""
		}
		p := filepath.Join(t.TempDir(), "week-notes.md")
		if !tc.absent {
			require.NoError(t, os.WriteFile(p, []byte(tc.file), 0o600))
		}
		return p
	})
}

// runWitnessWith runs `lucid witness <args>` at the CLI tests' clock with one
// scripted provider exchange and returns stdout.
func runWitnessWith(t *testing.T, ex provider.Exchange, args ...string) string {
	t.Helper()
	withClock(t, time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC))
	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{ex}})
	out, _, err := runWitness(t, args...)
	require.NoError(t, err, "a notes problem never fails the command")
	return out
}

// TestWitnessDryRun_NotesStatusNote: the markdown dry run names a configured
// week-notes file that is not included — status and fixed reason for invalid
// and unreadable, the bare status for stale and empty — on one line after the
// fallback and trip labels. Off and included print nothing, and --json adds no
// line (the status is in the data model).
func TestWitnessDryRun_NotesStatusNote(t *testing.T) {
	for _, tc := range witnessNotesCases() {
		t.Run(tc.name, func(t *testing.T) {
			seedNotesCase(t, tc)
			out := runWitnessWith(t, provider.Exchange{Content: witnessSlotReply}, "report")
			if tc.status == "" {
				assert.NotContains(t, out, "week notes not included")
			} else {
				assert.Contains(t, out, "\n[week notes not included — the notes file is "+tc.status+"]\n")
				assert.Equal(t, 1, strings.Count(out, "week notes not included"), "one line")
			}
			assert.NotContains(t, out, "A friend's call on Thursday helped.\n[", "the note never quotes the notes")
			if tc.name == "included" {
				assert.Contains(t, out, "**📝 Week notes**", "included notes render in the card instead")
			} else {
				assert.NotContains(t, out, "📝 Week notes")
			}
		})
		t.Run(tc.name+" json", func(t *testing.T) {
			seedNotesCase(t, tc)
			out := runWitnessWith(t, provider.Exchange{Content: witnessSlotReply}, "report", "--dry-run", "--json")
			assert.NotContains(t, out, "week notes not included", "--json adds no line")
			var report map[string]any
			require.NoError(t, json.Unmarshal([]byte(out), &report), "--json is the data model alone")
			if tc.unset {
				assert.NotContains(t, report, "notes_status")
			} else {
				assert.Equal(t, tc.name, report["notes_status"])
			}
		})
	}

	t.Run("after the fallback note", func(t *testing.T) {
		tc := witnessNotesCases()[4] // invalid
		seedNotesCase(t, tc)
		out := runWitnessWith(t, provider.Exchange{Err: provider.ErrUnavailable}, "report")
		fallback := strings.Index(out, "[deterministic fallback")
		notes := strings.Index(out, "[week notes not included — the notes file is invalid: written is in the future]")
		require.GreaterOrEqual(t, fallback, 0)
		require.GreaterOrEqual(t, notes, 0)
		assert.Less(t, fallback, notes, "the notes line follows the fallback label")
		assert.Less(t, notes, strings.Index(out, "Weekly witness report"), "and precedes the card")
	})
}

// alertRecordingDeliverer is the CLI's embed fake plus a plain-text Send, so a
// manual delivery that tried to alert through the transport would be caught.
type alertRecordingDeliverer struct {
	fakeEmbedDeliverer

	alerts []string
}

func (d *alertRecordingDeliverer) Send(_ context.Context, channel, text string) error {
	d.alerts = append(d.alerts, channel+": "+text)
	return nil
}

// TestWitnessDeliver_NotesStatusLineNoAlert: a manual --deliver still posts
// the report when the notes are not included, names the status on its
// confirmation line — " (week notes: <status>[: <reason>])" — and never
// alerts: the private notes alert belongs to the scheduled weekly run.
func TestWitnessDeliver_NotesStatusLineNoAlert(t *testing.T) {
	for _, tc := range witnessNotesCases() {
		t.Run(tc.name, func(t *testing.T) {
			seedNotesCase(t, tc)
			d := &alertRecordingDeliverer{}
			withWitnessDeliverer(t, d)
			out := runWitnessWith(t, provider.Exchange{Content: witnessSlotReply}, "report", "--deliver")

			assert.Equal(t, 1, d.sends, "the report is delivered")
			assert.Empty(t, d.alerts, "a manual delivery never alerts")
			if tc.status == "" {
				assert.Equal(t, "witness report delivered to the user channel — message msg-100000000000000009.\n", out)
			} else {
				assert.Equal(t, "witness report delivered to the user channel (week notes: "+tc.status+") — message msg-100000000000000009.\n", out)
			}
		})
	}

	t.Run("beside the fallback note", func(t *testing.T) {
		seedNotesCase(t, witnessNotesCases()[2]) // stale
		d := &alertRecordingDeliverer{}
		withWitnessDeliverer(t, d)
		out := runWitnessWith(t, provider.Exchange{Err: provider.ErrUnavailable}, "report", "--deliver")
		assert.Equal(t, "witness report delivered to the user channel (deterministic fallback) (week notes: stale) — message msg-100000000000000009.\n", out)
		assert.Empty(t, d.alerts)
	})
}
