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

// writeTombstone writes a redirect tombstone at key forwarding to target — the
// on-disk shape a merge leaves behind, hand-authored so the read-path tests do
// not depend on the Phase 2 curation verbs.
func writeTombstone(t *testing.T, home, key, display, target string, seen time.Time) {
	t.Helper()
	dir := filepath.Join(home, "people")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	ts := seen.Format(time.RFC3339)
	content := `{
  "person_key": "` + key + `",
  "display_name": "` + display + `",
  "aka": ["` + display + `"],
  "first_seen_at": "` + ts + `",
  "last_seen_at": "` + ts + `",
  "entry_refs": ["raw_2026_05_05_19_42"],
  "redirect_to": "` + target + `",
  "notes": null
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, key+".json"), []byte(content), 0o600))
}

// TestPerson_MergedFormResolvesToOneCanonical proves the tombstone-skip in
// matchPeople: after a merge, the merged-away form lives on the canonical's
// aka[] and its old slug is a tombstone — so querying that form yields exactly
// one match (the canonical), never a spurious §P-2. Byte-stable across runs.
func TestPerson_MergedFormResolvesToOneCanonical(t *testing.T) {
	r, a, home := newBootedRouter(t)
	keyAlex := seedPerson(t, a, "Alex", "raw_2026_05_05_19_42", personSeedTime())
	keyAndy := seedPerson(t, a, "Andy", "raw_2026_05_06_08_10", personSeedTime().Add(24*time.Hour))
	require.NotEqual(t, keyAlex, keyAndy)

	// Simulate a merge of Andy → Alex: the canonical absorbs the form, and Andy's
	// slug becomes a redirect tombstone.
	writePersonFile(t, home, keyAlex, "Alex", []string{"Alex", "Andy"}, personSeedTime())
	writeTombstone(t, home, keyAndy, "Andy", keyAlex, personSeedTime().Add(24*time.Hour))

	res, err := r.Person(PersonRequest{Name: "Andy"})
	require.NoError(t, err)
	assert.True(t, res.Matched, "the merged-away form resolves to the canonical")
	assert.False(t, res.MultipleMatches, "the tombstone is skipped — no spurious P-2")
	assert.Equal(t, keyAlex, res.PersonKey)

	// Querying the canonical's own name also yields the one record.
	byName, err := r.Person(PersonRequest{Name: "Alex"})
	require.NoError(t, err)
	assert.True(t, byName.Matched)
	assert.Equal(t, keyAlex, byName.PersonKey)

	// Byte-stable across repeated runs on the merged store (S-22).
	again, err := r.Person(PersonRequest{Name: "Andy"})
	require.NoError(t, err)
	assert.Equal(t, res.Text, again.Text)
}

// TestPerson_DominanceCountsPreMergeKeys proves the dominance join canonicalizes
// the person_key: historical artifacts keep the pre-merge key, and only folding
// them onto the canonical pushes the share over threshold. Without
// canonicalization the canonical would count 1/2 (below 0.5) — with it, 2/2.
func TestPerson_DominanceCountsPreMergeKeys(t *testing.T) {
	r, a, home := newBootedRouter(t)
	keyAlex := seedPerson(t, a, "Alex", "raw_2026_05_05_19_42", personSeedTime())
	keyAndy := seedPerson(t, a, "Andy", "raw_2026_05_06_08_10", personSeedTime().Add(24*time.Hour))

	// Two artifacts, each keyed to a different pre-merge slug.
	seedArtifactMentioning(t, a, "raw_2026_05_05_19_42", "Alex", keyAlex, personSeedTime())
	seedArtifactMentioning(t, a, "raw_2026_05_06_08_10", "Andy", keyAndy, personSeedTime())

	// Merge Andy → Alex.
	writePersonFile(t, home, keyAlex, "Alexandra", []string{"Alex", "Alexandra", "Andy"}, personSeedTime())
	writeTombstone(t, home, keyAndy, "Andy", keyAlex, personSeedTime().Add(24*time.Hour))

	res, err := r.Person(PersonRequest{Name: "Alexandra"})
	require.NoError(t, err)
	require.True(t, res.Matched)
	assert.Contains(t, res.Text, "appears in 100% of entries", "both pre-merge artifacts fold onto the canonical")
	assert.Contains(t, res.Text, "worth a look, or expected?")
}

// linkMediaToPerson seeds one synthetic media and links it to the person key
// through the real Link verb, returning the stored media id.
func linkMediaToPerson(t *testing.T, r *Router, a *storage.Adapter, personKey string) string {
	t.Helper()
	mediaID := seedLinkMedia(t, a)
	_, err := r.Link(LinkRequest{
		Media: mediaID, Subjects: []string{"person:" + personKey},
		Op: storage.LinkOpLink, Now: fixedNow(), Source: "cli",
	})
	require.NoError(t, err)
	return mediaID
}

// TestPerson_LinkedMedia_Surfaced is SC-9 for /person: media associated by an
// explicit link surface in the single-match view, labeled "linked" so they read
// distinctly from a same-day coincidence join.
func TestPerson_LinkedMedia_Surfaced(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	key := seedPerson(t, a, "M.", "raw_2026_05_05_19_42", personSeedTime())
	mediaID := linkMediaToPerson(t, r, a, key)

	res, err := r.Person(PersonRequest{Name: "M."})
	require.NoError(t, err)
	require.True(t, res.Matched)
	assert.False(t, res.OffLimits)
	assert.Contains(t, res.Text, "Media linked to them: "+mediaID)

	again, err := r.Person(PersonRequest{Name: "M."})
	require.NoError(t, err)
	assert.Equal(t, res.Text, again.Text, "the linked-media line is byte-stable across runs")
}

// TestPerson_NoLinks_NoMediaLine proves a person with no linked media renders no
// media line at all — so every existing person fixture is unchanged (A4/S-22).
func TestPerson_NoLinks_NoMediaLine(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	seedPerson(t, a, "M.", "raw_2026_05_05_19_42", personSeedTime())

	res, err := r.Person(PersonRequest{Name: "M."})
	require.NoError(t, err)
	require.True(t, res.Matched)
	assert.NotContains(t, res.Text, "Media linked to them", "no links → no media line")
}

// TestPerson_OffLimits_NoLinkedMediaLine guards the sanctuary boundary: an
// off-limits person with linked media still renders the raw record only, with no
// derived media line — the redaction returns before the link seam is reached.
func TestPerson_OffLimits_NoLinkedMediaLine(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	key := seedPerson(t, a, "M.", "raw_2026_05_05_19_42", personSeedTime())
	linkMediaToPerson(t, r, a, key)
	require.NoError(t, a.WriteOffLimitsPersonKeys([]string{key}))

	res, err := r.Person(PersonRequest{Name: "M."})
	require.NoError(t, err)
	require.True(t, res.Matched)
	assert.True(t, res.OffLimits)
	assert.NotContains(t, res.Text, "Media linked to them", "an off-limits person surfaces nothing derived")
}
