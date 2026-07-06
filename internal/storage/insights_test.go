package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// insightNow is a deterministic creation instant for the insight tests.
func insightNow() time.Time {
	return time.Date(2026, time.May, 5, 19, 43, 50, 0, time.UTC)
}

// validInsight builds an accepted insight with complete provenance — the
// shape WriteInsight and ValidateInsight require.
func validInsight() Insight {
	return Insight{
		CreatedAt: insightNow(),
		Status:    InsightStatusAccepted,
		Provenance: InsightProvenance{
			RawEntryIDs:             []string{"raw_2026_05_05_19_42", "raw_2026_05_03_21_10"},
			ProcessedArtifactID:     "raw_2026_05_05_19_42",
			ReflectionPromptVersion: "reflection-2026.05.0",
			UserResponseKind:        ResponseAccepted,
			UserResponseText:        "Yes, that fits.",
		},
		Body: "When M. is in the room, I tend to test an idea once and back off.",
	}
}

// TestWriteInsight_AllocatesSlotAndValidates writes an insight, confirms the
// id slot, that the file validates, and that it round-trips through ReadInsight.
func TestWriteInsight_AllocatesSlotAndValidates(t *testing.T) {
	a := New(t.TempDir())
	res, err := a.WriteInsight(validInsight())
	require.NoError(t, err)
	assert.Equal(t, "i_2026_05_05_a", res.InsightID)

	content, err := os.ReadFile(res.Path)
	require.NoError(t, err)
	require.NoError(t, ValidateInsight(content))

	ins, err := a.ReadInsight(res.InsightID)
	require.NoError(t, err)
	assert.Equal(t, InsightStatusAccepted, ins.Status)
	assert.Equal(t, "raw_2026_05_05_19_42", ins.Provenance.ProcessedArtifactID)
	assert.Equal(t, ResponseAccepted, ins.Provenance.UserResponseKind)
	assert.Len(t, ins.StatusHistory, 1)
	assert.Equal(t, "accepted", ins.StatusHistory[0].Kind)
	assert.Nil(t, ins.Rule)
	assert.Contains(t, ins.Body, "test an idea once")
}

// TestWriteInsight_SlotsAdvancePerDay confirms three insights on the same day
// get a, b, c and the next day resets to a.
func TestWriteInsight_SlotsAdvancePerDay(t *testing.T) {
	a := New(t.TempDir())
	for _, want := range []string{"i_2026_05_05_a", "i_2026_05_05_b", "i_2026_05_05_c"} {
		res, err := a.WriteInsight(validInsight())
		require.NoError(t, err)
		assert.Equal(t, want, res.InsightID)
	}
	next := validInsight()
	next.CreatedAt = insightNow().Add(24 * time.Hour)
	res, err := a.WriteInsight(next)
	require.NoError(t, err)
	assert.Equal(t, "i_2026_05_06_a", res.InsightID)
}

// TestSlotLabel covers the bijective base-26 slot labeler across the first-,
// second-, and third-digit boundaries.
func TestSlotLabel(t *testing.T) {
	assert.Equal(t, "a", slotLabel(0))
	assert.Equal(t, "z", slotLabel(25))
	assert.Equal(t, "aa", slotLabel(26))
	assert.Equal(t, "ab", slotLabel(27))
	assert.Equal(t, "az", slotLabel(51))
	assert.Equal(t, "ba", slotLabel(52))
}

// TestWriteInsight_NuancedRoundTrips confirms the nuanced flag and a nuanced
// user_response_kind persist and decode.
func TestWriteInsight_NuancedRoundTrips(t *testing.T) {
	a := New(t.TempDir())
	in := validInsight()
	in.NuancedFromProposal = true
	in.Provenance.UserResponseKind = ResponseNuanced
	in.Provenance.UserResponseText = "Mostly yes — it's more when M. is in the room."
	in.Body = in.Provenance.UserResponseText

	res, err := a.WriteInsight(in)
	require.NoError(t, err)
	ins, err := a.ReadInsight(res.InsightID)
	require.NoError(t, err)
	assert.True(t, ins.NuancedFromProposal)
	assert.Equal(t, ResponseNuanced, ins.Provenance.UserResponseKind)
}

// TestWriteInsight_ProvenanceGapsRejected is error-states.md §St-5: every
// provenance gap raises the validator before any file is written.
func TestWriteInsight_ProvenanceGapsRejected(t *testing.T) {
	mutations := map[string]func(*Insight){
		"no raw entry ids":    func(in *Insight) { in.Provenance.RawEntryIDs = nil },
		"no processed id":     func(in *Insight) { in.Provenance.ProcessedArtifactID = "" },
		"bad version prefix":  func(in *Insight) { in.Provenance.ReflectionPromptVersion = "v1" },
		"bad response kind":   func(in *Insight) { in.Provenance.UserResponseKind = "maybe" },
		"empty response text": func(in *Insight) { in.Provenance.UserResponseText = "  " },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			a := New(t.TempDir())
			in := validInsight()
			mutate(&in)
			_, err := a.WriteInsight(in)
			require.Error(t, err)
			assert.Equal(t, 0, countInsightFiles(t, a), "no insight is written when provenance is incomplete")
		})
	}
}

// TestWriteInsight_MissingCreatedAt rejects a zero created_at.
func TestWriteInsight_MissingCreatedAt(t *testing.T) {
	a := New(t.TempDir())
	in := validInsight()
	in.CreatedAt = time.Time{}
	_, err := a.WriteInsight(in)
	require.Error(t, err)
}

// TestValidateInsight_StructuralGaps covers the validator's non-provenance
// branches on crafted documents: a bad created_at, an unknown status, and an
// empty status_history.
func TestValidateInsight_StructuralGaps(t *testing.T) {
	base := "---\nid: i_2026_05_05_a\ncreated_at: %s\nstatus: %s\nnuanced_from_proposal: false\n" +
		"provenance:\n  raw_entry_ids: [raw_2026_05_05_19_42]\n  processed_artifact_id: raw_2026_05_05_19_42\n" +
		"  reflection_prompt_version: reflection-2026.05.0\n  framework: null\n  user_response_kind: accepted\n" +
		"  user_response_text: yes\nstatus_history:%s\nrule: null\n---\n\n# Insight\n\nbody\n"
	oneEvent := "\n  - at: 2026-05-05T19:43:50-04:00\n    kind: accepted"

	badDate := []byte(fmt.Sprintf(base, "not-a-date", "accepted", oneEvent))
	require.Error(t, ValidateInsight(badDate))

	badStatus := []byte(fmt.Sprintf(base, "2026-05-05T19:43:50-04:00", "pending", oneEvent))
	require.Error(t, ValidateInsight(badStatus))

	emptyHistory := []byte(fmt.Sprintf(base, "2026-05-05T19:43:50-04:00", "accepted", " []"))
	require.Error(t, ValidateInsight(emptyHistory))

	good := []byte(fmt.Sprintf(base, "2026-05-05T19:43:50-04:00", "accepted", oneEvent))
	require.NoError(t, ValidateInsight(good))
}

// TestSetInsightRule sets a rule once, confirms it is recorded verbatim with a
// stated history entry, and that an empty rule is refused.
func TestSetInsightRule(t *testing.T) {
	a := New(t.TempDir())
	res, err := a.WriteInsight(validInsight())
	require.NoError(t, err)

	rule := "When I catch myself folding mid-sentence, finish the sentence."
	require.NoError(t, a.SetInsightRule(res.InsightID, rule, insightNow()))

	ins, err := a.ReadInsight(res.InsightID)
	require.NoError(t, err)
	require.NotNil(t, ins.Rule)
	assert.Equal(t, rule, *ins.Rule)
	require.Len(t, ins.RuleHistory, 1)
	assert.Equal(t, RuleStated, ins.RuleHistory[0].Kind)

	// The insight still validates after the rule set.
	content, err := os.ReadFile(res.Path)
	require.NoError(t, err)
	assert.NoError(t, ValidateInsight(content))

	assert.Error(t, a.SetInsightRule(res.InsightID, "   ", insightNow()))
}

// TestReadInsight_Missing surfaces a read error for an absent id.
func TestReadInsight_Missing(t *testing.T) {
	a := New(t.TempDir())
	_, err := a.ReadInsight("i_2026_05_05_a")
	require.Error(t, err)
}

// TestListInsightIDs returns ids sorted, and nil for an absent dir.
func TestListInsightIDs(t *testing.T) {
	a := New(t.TempDir())
	ids, err := a.ListInsightIDs()
	require.NoError(t, err)
	assert.Empty(t, ids)

	first, err := a.WriteInsight(validInsight())
	require.NoError(t, err)
	second, err := a.WriteInsight(validInsight())
	require.NoError(t, err)

	ids, err = a.ListInsightIDs()
	require.NoError(t, err)
	assert.Equal(t, []string{first.InsightID, second.InsightID}, ids)
}

// TestInsightPath_RejectsSeparator guards the id-escaping check.
func TestInsightPath_RejectsSeparator(t *testing.T) {
	a := New(t.TempDir())
	_, err := a.insightPath("../escape")
	require.Error(t, err)
	_, err = a.ReadInsight("bad/id")
	require.Error(t, err)
}

// TestWriteInsight_FullOptionalRoundTrip sets every nullable field — the *At
// timestamps, a rule, and a multi-entry rule_history — and confirms they
// decode back, exercising the optional-time render/parse and history paths.
func TestWriteInsight_FullOptionalRoundTrip(t *testing.T) {
	a := New(t.TempDir())
	confirmed := insightNow().Add(96 * time.Hour)
	softened := insightNow().Add(120 * time.Hour)
	retired := insightNow().Add(200 * time.Hour)
	rule := "Finish the sentence."

	in := validInsight()
	in.StatusHistory = []TimedEvent{
		{At: insightNow(), Kind: "accepted"},
		{At: confirmed, Kind: "confirmed"},
	}
	in.LastConfirmedAt = &confirmed
	in.LastSoftenedAt = &softened
	in.RetiredAt = &retired
	in.Rule = &rule
	in.RuleHistory = []TimedEvent{{At: insightNow(), Kind: RuleStated}, {At: confirmed, Kind: RuleKept}}

	res, err := a.WriteInsight(in)
	require.NoError(t, err)
	ins, err := a.ReadInsight(res.InsightID)
	require.NoError(t, err)

	require.NotNil(t, ins.LastConfirmedAt)
	assert.True(t, confirmed.Equal(*ins.LastConfirmedAt))
	require.NotNil(t, ins.LastSoftenedAt)
	require.NotNil(t, ins.RetiredAt)
	require.NotNil(t, ins.Rule)
	assert.Equal(t, rule, *ins.Rule)
	assert.Len(t, ins.StatusHistory, 2)
	assert.Len(t, ins.RuleHistory, 2)
}

// TestReadInsight_MalformedTimestamps surfaces a decode error for a corrupt
// created_at and for a corrupt optional timestamp.
func TestReadInsight_MalformedTimestamps(t *testing.T) {
	a := New(t.TempDir())
	require.NoError(t, os.MkdirAll(a.insightsDir(), dirPerm))

	badOptional := "---\nid: i_2026_05_05_a\ncreated_at: 2026-05-05T19:43:50-04:00\nstatus: accepted\n" +
		"nuanced_from_proposal: false\nprovenance:\n  raw_entry_ids: [raw_x]\n  processed_artifact_id: raw_x\n" +
		"  reflection_prompt_version: reflection-2026.05.0\n  framework: null\n  user_response_kind: accepted\n" +
		"  user_response_text: yes\nstatus_history:\n  - at: 2026-05-05T19:43:50-04:00\n    kind: accepted\n" +
		"last_confirmed_at: not-a-time\nrule: null\n---\n\n# Insight\n\nbody\n"
	require.NoError(t, os.WriteFile(filepath.Join(a.insightsDir(), "i_2026_05_05_a"+insightExt), []byte(badOptional), filePerm))
	_, err := a.ReadInsight("i_2026_05_05_a")
	require.Error(t, err)
}

// TestReadInsight_MalformedFrontmatter surfaces an error for a document with
// no frontmatter fence and for unparseable YAML.
func TestReadInsight_MalformedFrontmatter(t *testing.T) {
	a := New(t.TempDir())
	require.NoError(t, os.MkdirAll(a.insightsDir(), dirPerm))
	require.NoError(t, os.WriteFile(filepath.Join(a.insightsDir(), "i_bad"+insightExt), []byte("no fence here"), filePerm))
	_, err := a.ReadInsight("i_bad")
	require.Error(t, err)

	assert.Error(t, ValidateInsight([]byte("no fence")))
}

// TestReadInsight_MalformedHistoryAndYAML surfaces errors for a corrupt
// status_history timestamp and for a fenced-but-unparseable frontmatter block.
func TestReadInsight_MalformedHistoryAndYAML(t *testing.T) {
	a := New(t.TempDir())
	require.NoError(t, os.MkdirAll(a.insightsDir(), dirPerm))

	badHistory := "---\nid: i_2026_05_05_a\ncreated_at: 2026-05-05T19:43:50-04:00\nstatus: accepted\n" +
		"nuanced_from_proposal: false\nprovenance:\n  raw_entry_ids: [raw_x]\n  processed_artifact_id: raw_x\n" +
		"  reflection_prompt_version: reflection-2026.05.0\n  framework: null\n  user_response_kind: accepted\n" +
		"  user_response_text: yes\nstatus_history:\n  - at: not-a-time\n    kind: accepted\nrule: null\n---\n\n# Insight\n\nbody\n"
	require.NoError(t, os.WriteFile(filepath.Join(a.insightsDir(), "i_2026_05_05_a"+insightExt), []byte(badHistory), filePerm))
	_, err := a.ReadInsight("i_2026_05_05_a")
	require.Error(t, err)

	badYAML := "---\n\tid: [unbalanced\n---\n\n# Insight\n\nbody\n"
	require.NoError(t, os.WriteFile(filepath.Join(a.insightsDir(), "i_bad_yaml"+insightExt), []byte(badYAML), filePerm))
	_, err = a.ReadInsight("i_bad_yaml")
	require.Error(t, err)
	assert.Error(t, ValidateInsight([]byte(badYAML)))
}

// TestWriteInsight_UnwritableDirErrors covers the write-failure branch: an
// unwritable insights dir surfaces an error, nothing is written.
func TestWriteInsight_UnwritableDirErrors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
	a := New(t.TempDir())
	require.NoError(t, os.MkdirAll(a.insightsDir(), 0o500))
	t.Cleanup(func() { _ = os.Chmod(a.insightsDir(), 0o700) })
	_, err := a.WriteInsight(validInsight())
	require.Error(t, err)
}

// TestSetInsightRule_ErrorPaths covers the two failure branches: a missing
// insight (read error) and an unwritable insight file (write error).
func TestSetInsightRule_ErrorPaths(t *testing.T) {
	a := New(t.TempDir())
	require.Error(t, a.SetInsightRule("i_2026_05_05_a", "a rule", insightNow()))

	res, err := a.WriteInsight(validInsight())
	require.NoError(t, err)
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
	require.NoError(t, os.Chmod(res.Path, 0o400))
	t.Cleanup(func() { _ = os.Chmod(res.Path, 0o600) })
	require.Error(t, a.SetInsightRule(res.InsightID, "a rule", insightNow()))
}

// TestWriteInsight_MkdirFails covers the prepare-dir branch: an unwritable
// Ledger home with no insights dir yet fails at MkdirAll.
func TestWriteInsight_MkdirFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
	home := filepath.Join(t.TempDir(), "ledger")
	require.NoError(t, os.MkdirAll(home, 0o500))
	t.Cleanup(func() { _ = os.Chmod(home, 0o700) })
	_, err := New(home).WriteInsight(validInsight())
	require.Error(t, err)
}

// TestReadInsight_MalformedCreatedAt covers decode's created_at parse branch
// (ReadInsight does not pre-validate, so a bad created_at surfaces here).
func TestReadInsight_MalformedCreatedAt(t *testing.T) {
	a := New(t.TempDir())
	require.NoError(t, os.MkdirAll(a.insightsDir(), dirPerm))
	doc := "---\nid: i_2026_05_05_a\ncreated_at: nope\nstatus: accepted\nnuanced_from_proposal: false\n" +
		"provenance:\n  raw_entry_ids: [raw_x]\n  processed_artifact_id: raw_x\n" +
		"  reflection_prompt_version: reflection-2026.05.0\n  framework: null\n  user_response_kind: accepted\n" +
		"  user_response_text: yes\nstatus_history:\n  - at: 2026-05-05T19:43:50-04:00\n    kind: accepted\nrule: null\n---\n\n# Insight\n\nbody\n"
	require.NoError(t, os.WriteFile(filepath.Join(a.insightsDir(), "i_2026_05_05_a"+insightExt), []byte(doc), filePerm))
	_, err := a.ReadInsight("i_2026_05_05_a")
	require.Error(t, err)
}

// TestListInsightIDs_ReadDirError covers the non-notexist ReadDir branch: a
// file where the insights dir should be makes ReadDir fail.
func TestListInsightIDs_ReadDirError(t *testing.T) {
	a := New(t.TempDir())
	require.NoError(t, os.MkdirAll(a.home, dirPerm))
	require.NoError(t, os.WriteFile(a.insightsDir(), []byte("not a dir"), filePerm))
	_, err := a.ListInsightIDs()
	require.Error(t, err)
}

// countInsightFiles counts the .md files under the insights dir.
func countInsightFiles(t *testing.T, a *Adapter) int {
	t.Helper()
	entries, err := os.ReadDir(a.insightsDir())
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(t, err)
	var n int
	for _, e := range entries {
		if filepath.Ext(e.Name()) == insightExt {
			n++
		}
	}
	return n
}
