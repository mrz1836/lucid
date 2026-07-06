package storage

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestListPeopleKeys_SortedAndEmpty covers the /person join primitive: an empty
// people tree returns nothing, and populated keys come back sorted.
func TestListPeopleKeys_SortedAndEmpty(t *testing.T) {
	a := New(t.TempDir())

	empty, err := a.ListPeopleKeys()
	require.NoError(t, err)
	assert.Empty(t, empty, "no people yet")

	at := time.Date(2026, time.May, 5, 19, 42, 11, 0, time.UTC)
	k1, err := a.UpdatePerson(PersonMention{DisplayName: "M.", RawEntryID: "raw_2026_05_05_19_42", At: at})
	require.NoError(t, err)
	k2, err := a.UpdatePerson(PersonMention{DisplayName: "J.", RawEntryID: "raw_2026_05_06_08_10", At: at})
	require.NoError(t, err)

	keys, err := a.ListPeopleKeys()
	require.NoError(t, err)
	require.Len(t, keys, 2)
	assert.Contains(t, keys, k1.PersonKey)
	assert.Contains(t, keys, k2.PersonKey)
	assert.Less(t, keys[0], keys[1], "keys are sorted")
}

// TestReadReflections_NewestFirstCapped covers the /ask reflections-slice
// primitive: records come back newest ISO-week first and are capped.
func TestReadReflections_NewestFirstCapped(t *testing.T) {
	a := New(t.TempDir())

	none, err := a.ReadReflections(12)
	require.NoError(t, err)
	assert.Empty(t, none)

	for _, wk := range []struct {
		id, label string
		week      int
	}{{"reflection_2026_w17", "2026-W17", 17}, {"reflection_2026_w18", "2026-W18", 18}, {"reflection_2026_w19", "2026-W19", 19}} {
		now := time.Date(2026, time.May, wk.week, 12, 0, 0, 0, time.UTC)
		_, writeErr := a.WriteReflection(Reflection{
			ID: wk.id, ISOWeek: wk.label, WindowStart: now, WindowEnd: now.Add(6 * 24 * time.Hour),
			CreatedAt: now, AgentVersion: "reflection-2026.05.0", Summary: "week " + wk.label,
		})
		require.NoError(t, writeErr)
	}

	all, err := a.ReadReflections(0)
	require.NoError(t, err)
	require.Len(t, all, 3)
	assert.Equal(t, "reflection_2026_w19", all[0].ID, "newest ISO-week first")
	assert.Equal(t, "reflection_2026_w17", all[2].ID)

	capped, err := a.ReadReflections(2)
	require.NoError(t, err)
	require.Len(t, capped, 2)
	assert.Equal(t, "reflection_2026_w19", capped[0].ID)
	assert.Equal(t, "reflection_2026_w18", capped[1].ID)
}

// TestReadAcceptedInsights_OrdersByLastStatus covers the /ask insights-slice
// primitive: accepted insights order by their most recent status transition
// (not created_at), retired insights are excluded, and the cap trims the tail.
func TestReadAcceptedInsights_OrdersByLastStatus(t *testing.T) {
	a := New(t.TempDir())
	base := time.Date(2026, time.May, 1, 12, 0, 0, 0, time.UTC)

	older := writeAcceptedInsight(t, a, base, "older by creation")
	newer := writeAcceptedInsight(t, a, base.Add(3*24*time.Hour), "newer by creation")
	retired := writeAcceptedInsight(t, a, base.Add(1*24*time.Hour), "will retire")
	require.NoError(t, a.UpdateInsightStatus(retired, RecallRetired, base.Add(2*24*time.Hour)))

	// Confirm the older insight last, so its last status transition is the most
	// recent — it should now sort ahead of the newer-by-creation one.
	require.NoError(t, a.UpdateInsightStatus(older, RecallConfirmed, base.Add(10*24*time.Hour)))

	got, err := a.ReadAcceptedInsights(0)
	require.NoError(t, err)
	require.Len(t, got, 2, "retired excluded")
	assert.Equal(t, older, got[0].ID, "ordered by last status_history transition, not created_at")
	assert.Equal(t, newer, got[1].ID)

	capped, err := a.ReadAcceptedInsights(1)
	require.NoError(t, err)
	require.Len(t, capped, 1)
	assert.Equal(t, older, capped[0].ID)
}

// TestReadAcceptedInsights_CorruptErrors confirms a corrupt insight fails the
// slice read rather than being silently dropped.
func TestReadAcceptedInsights_CorruptErrors(t *testing.T) {
	a := New(t.TempDir())
	id := writeAcceptedInsight(t, a, insightNow(), "x")
	path, err := a.insightPath(id)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("not frontmatter"), 0o600))
	_, err = a.ReadAcceptedInsights(0)
	require.Error(t, err)
}

// TestReadReflections_CorruptErrors confirms a malformed reflection file fails
// the slice read.
func TestReadReflections_CorruptErrors(t *testing.T) {
	a := New(t.TempDir())
	require.NoError(t, os.MkdirAll(a.reflectionsDir(), 0o700))
	require.NoError(t, os.WriteFile(a.reflectionsDir()+"/reflection_2026_w19.md", []byte("no fence here"), 0o600))
	_, err := a.ReadReflections(0)
	require.Error(t, err)
}

// TestReadAcceptedInsights_TieBreakByID confirms equal transition timestamps
// break deterministically by id (descending), so the slice — and any output
// built from it — is byte-stable across runs.
func TestReadAcceptedInsights_TieBreakByID(t *testing.T) {
	a := New(t.TempDir())
	at := time.Date(2026, time.May, 5, 19, 42, 0, 0, time.UTC)
	// Two insights created the same day get slots a, b; both carry only the
	// initial accepted transition at `at`, so lastStatusAt ties.
	idA := writeAcceptedInsight(t, a, at, "first slot")
	idB := writeAcceptedInsight(t, a, at, "second slot")
	require.NotEqual(t, idA, idB)

	got, err := a.ReadAcceptedInsights(0)
	require.NoError(t, err)
	require.Len(t, got, 2)
	// Descending id order: the later slot (b) sorts ahead of a.
	assert.Equal(t, idB, got[0].ID)
	assert.Equal(t, idA, got[1].ID)
}
