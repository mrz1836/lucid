package router

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

// TestRecallHelpers_PureBranches locks the render edges of the recall projection
// helpers that a normal browse never exercises: the two-sided and open-start era
// range, the untitled-memory fallback, the nil-map guards, the list-valued field
// renders, and the empty-label humanizer.
func TestRecallHelpers_PureBranches(t *testing.T) {
	// eraRange: both bounds, then an open start (end only).
	assert.Equal(t, "2010-06-01 – 2012-01-01",
		eraRange(map[string]any{"start": "2010-06-01", "end": "2012-01-01"}))
	assert.Equal(t, "– 2012-01-01", eraRange(map[string]any{"end": "2012-01-01"}))

	// storyTitle falls back for a text-less (partial) memory payload.
	assert.Equal(t, "(untitled memory)", storyTitle(nil))
	assert.Equal(t, "(untitled memory)", storyTitle(map[string]any{observations.MemoryFieldText: "   "}))

	// refString / payloadStr guard a nil map and a non-string value.
	assert.Empty(t, refString(nil, "era"))
	assert.Empty(t, refString(map[string]any{"era": 7}, "era"))
	assert.Empty(t, payloadStr(nil, observations.MemoryFieldText))
	assert.Empty(t, payloadStr(map[string]any{observations.MemoryFieldText: 7}, observations.MemoryFieldText))

	// fieldValue: a JSON-read []any, an in-memory []string (blanks dropped), and
	// an unrenderable type.
	assert.Equal(t, "a, b", fieldValue([]any{"a", " b ", "", 3}))
	assert.Equal(t, "x, y", fieldValue([]string{"x", "", " y "}))
	assert.Empty(t, fieldValue(42))

	// humanizeLabel guards the empty key.
	assert.Empty(t, humanizeLabel(""))
	assert.Equal(t, "Body area", humanizeLabel("body_area"))
}

// TestRecall_ReadRegistryFails surfaces a corrupt registry record on a keyed
// browse rather than presenting a fabricated referent.
func TestRecall_ReadRegistryFails(t *testing.T) {
	r, _, home := bootedMemoryRouter(t)
	inj, err := r.WriteInjury(InjuryWriteRequest{Name: "left knee", Now: fixedNow()})
	require.NoError(t, err)

	path := filepath.Join(home, "registries", "injuries", inj.Key+".json")
	require.NoError(t, os.WriteFile(path, []byte("{bad"), 0o600))

	_, err = r.Recall(RecallRequest{Dimension: RecallInjury, Key: inj.Key})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "recall: read injury")
}

// TestRecall_IndexReadFails surfaces a registry-kind read failure while building
// the bare index rather than degrading to a silent partial index.
func TestRecall_IndexReadFails(t *testing.T) {
	skipIfRoot(t)
	r, _, home := bootedMemoryRouter(t)

	erasDir := filepath.Join(home, "registries", "eras")
	require.NoError(t, os.Chmod(erasDir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(erasDir, 0o700) })

	_, err := r.Recall(RecallRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "recall: read era index")
}

// TestRecall_StoriesReadFails surfaces an unreadable memory day file behind a
// resolved referent rather than dropping the stories silently.
func TestRecall_StoriesReadFails(t *testing.T) {
	skipIfRoot(t)
	r, _, home := bootedMemoryRouter(t)
	era, err := r.WriteEra(EraWriteRequest{Name: "wild summer", Now: fixedNow()})
	require.NoError(t, err)
	mem, err := r.WriteMemory(MemoryWriteRequest{Text: "a night we drove", Era: era.Key, Now: fixedNow()})
	require.NoError(t, err)

	// Make the logical day file the memory landed in unreadable so
	// ReadObservationsKind fails while the era referent still resolves cleanly.
	date := mem.LogicalDate
	year, month := date[0:4], date[5:7]
	name := "obs_" + date[0:4] + "_" + date[5:7] + "_" + date[8:10] + ".jsonl"
	path := filepath.Join(home, "observations", year, month, name)
	require.NoError(t, os.Chmod(path, 0o000))
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	_, err = r.Recall(RecallRequest{Dimension: RecallEra, Key: era.Key})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "recall: read memories")
}
