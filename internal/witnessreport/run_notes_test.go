package witnessreport

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/config"
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
