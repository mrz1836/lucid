package router

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/storage"
)

// personSeedTime is a deterministic instant the /person fixtures anchor on.
func personSeedTime() time.Time {
	return time.Date(2026, time.May, 5, 19, 42, 11, 0, time.UTC)
}

// seedPerson records one mention of display via update_person and returns the
// resolved person_key.
func seedPerson(t *testing.T, a *storage.Adapter, display, rawID string, at time.Time) string {
	t.Helper()
	res, err := a.UpdatePerson(storage.PersonMention{DisplayName: display, RawEntryID: rawID, At: at})
	require.NoError(t, err)
	return res.PersonKey
}

// seedArtifactMentioning writes a processed artifact under id that names the
// given person; peopleKeys empty writes an artifact with a notes value so it
// still validates.
func seedArtifactMentioning(t *testing.T, a *storage.Adapter, id, display, key string, at time.Time) {
	t.Helper()
	art := storage.ProcessedArtifact{
		ID: id, EntryID: id, ProducedAt: at, AgentVersion: "structuring-2026.05.0",
	}
	if key != "" {
		art.People = []storage.ProcessedPerson{{DisplayName: display, PersonKey: key, FirstMention: true}}
	} else {
		notes := "no people in this entry"
		art.Notes = &notes
	}
	require.NoError(t, a.WriteProcessed(art))
}

// TestPerson_P1_NoMatch is §P-1: a name with no record returns the empty state.
func TestPerson_P1_NoMatch(t *testing.T) {
	r, _, _ := newBootedRouter(t)
	res, err := r.Person(PersonRequest{Name: "Nobody"})
	require.NoError(t, err)

	assert.False(t, res.Matched)
	assert.Equal(t, personNoMatch, res.Text)
}

// TestPerson_P2_MultipleMatches is §P-2: a name matching more than one record
// lists the candidates and renders nothing derived.
func TestPerson_P2_MultipleMatches(t *testing.T) {
	r, a, home := newBootedRouter(t)
	// Two records that both carry the aka "A." — constructed directly on disk to
	// simulate a store where a short form maps to two referents.
	writePersonFile(t, home, "person_a-alex", "Alex", []string{"A.", "Alex"}, personSeedTime())
	writePersonFile(t, home, "person_a-andy", "Andy", []string{"A.", "Andy"}, personSeedTime().Add(24*time.Hour))

	res, err := r.Person(PersonRequest{Name: "A."})
	require.NoError(t, err)

	assert.False(t, res.Matched)
	assert.True(t, res.MultipleMatches)
	require.Len(t, res.Candidates, 2)
	assert.Contains(t, res.Text, "more than one person")
	assert.Contains(t, res.Text, "Alex")
	assert.Contains(t, res.Text, "Andy")
	// Nothing derived is rendered for the disambiguation prompt.
	assert.NotContains(t, res.Text, "accepted insights")
	_ = a
}

// TestPerson_FullView_Deterministic renders the full single-match view: the
// record, mention counts, and the accepted insights citing entries mentioning
// them. Below-threshold share means no dominance line, and the output is
// byte-identical across repeated runs (S-22).
func TestPerson_FullView_Deterministic(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	rawID := "raw_2026_05_05_19_42"
	key := seedPerson(t, a, "M.", rawID, personSeedTime())

	// One of four artifacts mentions M. → share 0.25, below the 0.5 threshold.
	seedArtifactMentioning(t, a, rawID, "M.", key, personSeedTime())
	seedArtifactMentioning(t, a, "raw_2026_05_06_08_10", "", "", personSeedTime())
	seedArtifactMentioning(t, a, "raw_2026_05_07_09_00", "", "", personSeedTime())
	seedArtifactMentioning(t, a, "raw_2026_05_08_10_00", "", "", personSeedTime())

	// An accepted insight citing rawID (which mentions M.).
	seedInsight(t, a, personSeedTime(), "I go quiet when M. is in the room.")
	// A retired insight citing the same entry must be excluded from the join.
	retired := seedInsight(t, a, personSeedTime().Add(time.Hour), "An old take on M.")
	require.NoError(t, a.UpdateInsightStatus(retired, storage.RecallRetired, personSeedTime().Add(time.Hour)))

	res, err := r.Person(PersonRequest{Name: "M."})
	require.NoError(t, err)
	require.True(t, res.Matched)
	assert.False(t, res.OffLimits)
	assert.Contains(t, res.Text, "M.")
	assert.Contains(t, res.Text, "Mentioned in 1 entry")
	assert.Contains(t, res.Text, "Referenced by accepted insights: i_2026_05_05_a")
	assert.NotContains(t, res.Text, "worth a look", "share is below threshold — no dominance line")

	again, err := r.Person(PersonRequest{Name: "M."})
	require.NoError(t, err)
	assert.Equal(t, res.Text, again.Text, "/person is byte-identical across repeated runs")
}

// TestPerson_DominanceAboveThreshold_Line shows a hypothesis-framed dominance
// line only when the person's entry share exceeds person_dominance_threshold.
func TestPerson_DominanceAboveThreshold_Line(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	rawID := "raw_2026_05_05_19_42"
	key := seedPerson(t, a, "M.", rawID, personSeedTime())

	// Both artifacts mention M. → share 1.0, above the 0.5 threshold.
	seedArtifactMentioning(t, a, rawID, "M.", key, personSeedTime())
	seedArtifactMentioning(t, a, "raw_2026_05_06_08_10", "M.", key, personSeedTime())

	res, err := r.Person(PersonRequest{Name: "M."})
	require.NoError(t, err)
	assert.Contains(t, res.Text, "appears in 100% of entries")
	assert.Contains(t, res.Text, "worth a look, or expected?")
}

// TestPerson_P3_OffLimits_RawRecordOnly is §P-3: an off-limits person renders the
// raw record only behind the standing header — no insights, no dominance.
func TestPerson_P3_OffLimits_RawRecordOnly(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	rawID := "raw_2026_05_05_19_42"
	key := seedPerson(t, a, "M.", rawID, personSeedTime())
	seedArtifactMentioning(t, a, rawID, "M.", key, personSeedTime())
	seedArtifactMentioning(t, a, "raw_2026_05_06_08_10", "M.", key, personSeedTime())
	seedInsight(t, a, personSeedTime(), "I go quiet when M. is in the room.")

	require.NoError(t, a.WriteOffLimitsPersonKeys([]string{key}))

	res, err := r.Person(PersonRequest{Name: "M."})
	require.NoError(t, err)

	require.True(t, res.Matched)
	assert.True(t, res.OffLimits)
	assert.Contains(t, res.Text, "off-limits to inference")
	assert.Contains(t, res.Text, "raw record only")
	assert.Contains(t, res.Text, rawID, "the raw entry ids are the record")
	// Nothing derived leaks through the redaction.
	assert.NotContains(t, res.Text, "accepted insights")
	assert.NotContains(t, res.Text, "worth a look", "no dominance line for an off-limits person")

	again, err := r.Person(PersonRequest{Name: "M."})
	require.NoError(t, err)
	assert.Equal(t, res.Text, again.Text, "the redacted view is byte-identical too")
}

// TestPerson_MatchByAka confirms a query resolves against an aka variant, not
// only the display name.
func TestPerson_MatchByAka(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	// First mention "M", later "M." — both are aka variants of one record.
	key := seedPerson(t, a, "M", "raw_2026_05_05_19_42", personSeedTime())
	_ = seedPerson(t, a, "M.", "raw_2026_05_06_08_10", personSeedTime().Add(24*time.Hour))
	require.NotEmpty(t, key)

	res, err := r.Person(PersonRequest{Name: "M"})
	require.NoError(t, err)
	assert.True(t, res.Matched)
}

// TestPerson_DominanceTermsAbsentFromStatusAndDay guards S-22: the dominance
// phrasing appears in no /status or daily-surface template.
func TestPerson_DominanceTermsAbsentFromStatusAndDay(t *testing.T) {
	for _, f := range []string{"status.go", "day.go"} {
		b, err := os.ReadFile(f)
		require.NoError(t, err, "reading %s", f)
		body := string(b)
		assert.NotContains(t, body, "worth a look, or expected", "%s must carry no dominance line", f)
		assert.NotContains(t, body, "% of entries", "%s must carry no dominance share", f)
	}
}

// writePersonFile writes a people/<key>.json record directly, for fixtures that
// need a store shape update_person cannot produce (e.g. two records sharing an
// aka for the §P-2 disambiguation path).
func writePersonFile(t *testing.T, home, key, display string, aka []string, seen time.Time) {
	t.Helper()
	dir := filepath.Join(home, "people")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	ts := seen.Format(time.RFC3339)
	var akaJSON strings.Builder
	for i, v := range aka {
		if i > 0 {
			akaJSON.WriteString(", ")
		}
		akaJSON.WriteString(`"` + v + `"`)
	}
	content := `{
  "person_key": "` + key + `",
  "display_name": "` + display + `",
  "aka": [` + akaJSON.String() + `],
  "first_seen_at": "` + ts + `",
  "last_seen_at": "` + ts + `",
  "entry_refs": ["raw_2026_05_05_19_42"],
  "notes": null
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, key+".json"), []byte(content), 0o600))
}
