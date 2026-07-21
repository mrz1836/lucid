package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lifeArchiveNow pins a fixed instant for the life-archive integration flow so
// the backdating, logical-day filing, and attach raw ids are deterministic.
func lifeArchiveNow() time.Time { return time.Date(2026, 7, 5, 14, 0, 0, 0, time.UTC) }

// TestLifeArchive_Integration threads the whole excavation loop through the real
// CLI surfaces — `lucid injury` (create, then amend) → the read-only
// `lucid excavate` selection that moves on as gaps close → `lucid era` +
// `lucid memory` (with an attached photo) → the read-only `lucid recall` browse
// that surfaces the story cited under its era and the injury by its key. Unlike
// the per-verb unit tests, the excavate leg consumes the exact records the write
// legs produced, and the recall leg surfaces the exact story the memory leg
// wrote, so this proves the capture → select → amend → recall contract
// end-to-end over synthetic fixtures. The read-only surfaces are proven to write
// nothing (file-count identical), and the empty/missing inputs degrade honestly.
func TestLifeArchive_Integration(t *testing.T) {
	t.Run("capture_select_amend_recall_end_to_end", func(t *testing.T) {
		withClock(t, lifeArchiveNow())
		home := enableMemoryHome(t)
		bi := BuildInfo{Version: "dev"}

		// ── Stage 1: a thin injury is the first thing to excavate. A bare
		// `lucid injury` create mints the record with no convention Fields, so
		// `lucid excavate` selects it on the injury track and emits a generic
		// prompt for every gap. ──
		injKey := seedRegistry(t, "injury", "left knee")

		var ex excavateView
		require.NoError(t, json.Unmarshal([]byte(runJSON(t, bi, "excavate", "--json")), &ex))
		assert.True(t, ex.Found, "a thin injury is a cluster to excavate")
		assert.Equal(t, "injury", ex.Track)
		assert.Equal(t, injKey, ex.Key, "excavate selects the injury the create minted")
		assert.Equal(t, "left knee", ex.DisplayName)
		assert.Len(t, ex.Gaps, 9, "a bare injury is missing every convention field")
		require.NotEmpty(t, ex.Prompts, "the selection emits generic gap prompts")

		// ── Stage 2: fill the injury's Fields. With every convention field
		// present the injury has no gaps, and no era exists yet, so
		// `lucid excavate` moves on to an honest empty result — it does not keep
		// asking about a record that is now complete. ──
		_, _, err := runRoot(t, bi, "injury", "left knee",
			"--onset", "2014-09",
			"--timeline", "flared for a year, then settled",
			"--body-area", "left knee, medial",
			"--cause", "landed wrong off a ledge",
			"--severity", "moderate then, mild now",
			"--lasting-effects", "stiff in the cold",
			"--current-limitations", "no deep squats",
			"--treatments", "PT and rest",
			"--uncertainty", "which ligament it was")
		require.NoError(t, err)

		var exFilled excavateView
		require.NoError(t, json.Unmarshal([]byte(runJSON(t, bi, "excavate", "--json")), &exFilled))
		assert.False(t, exFilled.Found, "a fully-recorded injury is no longer selected, and no era to open yet")

		// ── Stage 3: open an era and capture a story inside it, with a photo
		// attached. The story reuses `lucid attach` — the returned raw id is
		// linked from refs.entry — and is filed under the era via refs.era. ──
		eraKey := seedRegistry(t, "era", "wild summer", "--start", "2010-06-01")

		photo := filepath.Join(t.TempDir(), "coast.jpg")
		require.NoError(t, os.WriteFile(photo, []byte("synthetic-bytes"), 0o600))

		var mem memoryWriteView
		require.NoError(t, json.Unmarshal([]byte(runJSON(t, bi,
			"memory", "the night we drove to the coast",
			"--era", eraKey, "--certainty", "vivid", "--why", "felt free",
			"--attach", photo, "--caption", "the pier at 2am", "--json")), &mem))
		require.NotEmpty(t, mem.EventID)
		assert.False(t, mem.Rejected, "the memory kind is enabled, so the story lands")
		assert.Equal(t, eraKey, mem.Refs["era"], "the story is filed under its era")
		entry, ok := mem.Refs["entry"].(string)
		require.True(t, ok, "the attached photo is linked via refs.entry")
		assert.NotEmpty(t, entry)

		// The injury is complete, so excavate now moves to the story track and
		// offers the era's chapter — the two tracks stay separate.
		var exStory excavateView
		require.NoError(t, json.Unmarshal([]byte(runJSON(t, bi, "excavate", "--json")), &exStory))
		assert.True(t, exStory.Found)
		assert.Equal(t, "story", exStory.Track, "with the injury filled, excavate moves to the story track")
		assert.Equal(t, eraKey, exStory.Key)

		// ── Stage 4: recall surfaces what was archived. `--era` returns the era
		// referent and the story filed under it, cited by the story's own
		// observation id; `--injury` returns the injury's record, cited by its
		// registry provenance. ──
		var reEra recallView
		require.NoError(t, json.Unmarshal([]byte(runJSON(t, bi, "recall", "--era", eraKey, "--json")), &reEra))
		assert.True(t, reEra.Found)
		assert.Equal(t, "era", reEra.Dimension)
		require.NotNil(t, reEra.Referent)
		assert.Equal(t, "wild summer", reEra.Referent.DisplayName)
		require.Len(t, reEra.Items, 1, "the one story in the chapter is surfaced")
		story := reEra.Items[0]
		assert.Equal(t, "story", story.Kind)
		assert.Equal(t, "the night we drove to the coast", story.Title)
		assert.Equal(t, "excavation", story.Source)
		assert.Contains(t, story.SupportingEntryIDs, mem.EventID, "the surfaced story is cited by its observation id")

		var reInj recallView
		require.NoError(t, json.Unmarshal([]byte(runJSON(t, bi, "recall", "--injury", injKey, "--json")), &reInj))
		assert.True(t, reInj.Found)
		assert.Equal(t, "injury", reInj.Dimension)
		require.NotNil(t, reInj.Referent)
		assert.Equal(t, "left knee", reInj.Referent.DisplayName)
		assert.Equal(t, "registry", reInj.Referent.Source, "an injury referent is cited by its registry provenance")
		assert.NotEmpty(t, reInj.Referent.Fields, "the amended convention Fields are surfaced")

		// ── Read-only guarantee: the selection and browse surfaces write nothing.
		// The whole-tree file count is identical before and after a full sweep of
		// the read-only verbs over the now-seeded store. ──
		before := countHomeFiles(t, home, "")
		_, _, err = runRoot(t, bi, "excavate")
		require.NoError(t, err)
		_, _, err = runRoot(t, bi, "recall", "--era", eraKey)
		require.NoError(t, err)
		_, _, err = runRoot(t, bi, "recall", "--injury", injKey)
		require.NoError(t, err)
		_, _, err = runRoot(t, bi, "recall")
		require.NoError(t, err)
		assert.Equal(t, before, countHomeFiles(t, home, ""), "excavate and recall write nothing")
	})

	t.Run("empty_and_missing_inputs_degrade_honestly", func(t *testing.T) {
		isolatedHome(t)
		withClock(t, lifeArchiveNow())
		bi := BuildInfo{Version: "dev"}

		// An empty store: both read-only surfaces print their calm fallback and
		// never error — an honest empty result, with no model to spend over it.
		out, _, err := runRoot(t, bi, "excavate")
		require.NoError(t, err)
		assert.Contains(t, out, "Nothing to excavate")

		out, _, err = runRoot(t, bi, "recall")
		require.NoError(t, err)
		assert.Contains(t, out, "Nothing archived")

		// A browse for a key that does not resolve is an honest not-found, not an
		// error — the archive says what it does not hold.
		out, _, err = runRoot(t, bi, "recall", "--era", "era_nope")
		require.NoError(t, err)
		assert.Contains(t, out, "No era found")
	})
}

// runJSON runs a command that must succeed and returns its stdout, so the
// integration flow reads as a sequence of writes and reads without a NoError
// check at every step.
func runJSON(t *testing.T, bi BuildInfo, args ...string) string {
	t.Helper()
	out, _, err := runRoot(t, bi, args...)
	require.NoError(t, err)
	return out
}
