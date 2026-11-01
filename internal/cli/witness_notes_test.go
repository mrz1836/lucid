package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
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
