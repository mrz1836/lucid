package storage

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeAcceptedInsight writes one accepted insight created at `at` with the
// given body and returns its id — the seed the recall/window tests build on.
func writeAcceptedInsight(t *testing.T, a *Adapter, at time.Time, body string) string {
	t.Helper()
	in := validInsight()
	in.CreatedAt = at
	in.Body = body
	res, err := a.WriteInsight(in)
	require.NoError(t, err)
	return res.InsightID
}

// TestUpdateInsightStatus_ConfirmSoftenRetire covers each recall transition:
// the status_history entry appends, the matching timestamp field stamps, and
// only `retired` flips the status field (acceptance-criteria.md 6.1).
func TestUpdateInsightStatus_ConfirmSoftenRetire(t *testing.T) {
	a := New(t.TempDir())
	id := writeAcceptedInsight(t, a, insightNow(), "When M. is in the room, I test an idea once and back off.")

	confirmAt := insightNow().Add(4 * 24 * time.Hour)
	require.NoError(t, a.UpdateInsightStatus(id, RecallConfirmed, confirmAt))
	ins, err := a.ReadInsight(id)
	require.NoError(t, err)
	require.Len(t, ins.StatusHistory, 2)
	assert.Equal(t, RecallConfirmed, ins.StatusHistory[1].Kind)
	require.NotNil(t, ins.LastConfirmedAt)
	assert.True(t, ins.LastConfirmedAt.Equal(confirmAt))
	assert.Equal(t, InsightStatusAccepted, ins.Status, "a confirm never retires")

	softenAt := confirmAt.Add(24 * time.Hour)
	require.NoError(t, a.UpdateInsightStatus(id, RecallSoftened, softenAt))
	ins, err = a.ReadInsight(id)
	require.NoError(t, err)
	require.NotNil(t, ins.LastSoftenedAt)
	assert.Equal(t, InsightStatusAccepted, ins.Status)

	retireAt := softenAt.Add(24 * time.Hour)
	require.NoError(t, a.UpdateInsightStatus(id, RecallRetired, retireAt))
	ins, err = a.ReadInsight(id)
	require.NoError(t, err)
	assert.Equal(t, InsightStatusRetired, ins.Status)
	require.NotNil(t, ins.RetiredAt)
	assert.Equal(t, RecallRetired, ins.StatusHistory[len(ins.StatusHistory)-1].Kind)
}

// TestUpdateInsightStatus_AppendsDuplicateConfirm is the §R-9 idempotency
// behavior: status_history accepts a repeat confirm within a week (append, not
// dedup) rather than erroring.
func TestUpdateInsightStatus_AppendsDuplicateConfirm(t *testing.T) {
	a := New(t.TempDir())
	id := writeAcceptedInsight(t, a, insightNow(), "x pattern")
	require.NoError(t, a.UpdateInsightStatus(id, RecallConfirmed, insightNow().Add(time.Hour)))
	require.NoError(t, a.UpdateInsightStatus(id, RecallConfirmed, insightNow().Add(2*time.Hour)))
	ins, err := a.ReadInsight(id)
	require.NoError(t, err)
	assert.Len(t, ins.StatusHistory, 3, "accepted + two confirms")
}

// TestUpdateInsightStatus_RejectsBadKind rejects a kind that is not a valid
// recall transition.
func TestUpdateInsightStatus_RejectsBadKind(t *testing.T) {
	a := New(t.TempDir())
	id := writeAcceptedInsight(t, a, insightNow(), "x")
	err := a.UpdateInsightStatus(id, "unanswered", insightNow())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "confirmed|softened|retired")
}

// TestUpdateInsightRuleStatus_Transitions covers kept/lapsed/retired appends on
// a ruled insight without changing the insight's own status.
func TestUpdateInsightRuleStatus_Transitions(t *testing.T) {
	a := New(t.TempDir())
	id := writeAcceptedInsight(t, a, insightNow(), "x pattern")
	require.NoError(t, a.SetInsightRule(id, "Finish the sentence when I catch myself folding.", insightNow()))

	require.NoError(t, a.UpdateInsightRuleStatus(id, RuleLapsed, insightNow().Add(24*time.Hour)))
	ins, err := a.ReadInsight(id)
	require.NoError(t, err)
	require.Len(t, ins.RuleHistory, 2, "stated + lapsed")
	assert.Equal(t, RuleLapsed, ins.RuleHistory[1].Kind)
	assert.Equal(t, InsightStatusAccepted, ins.Status, "a lapsed rule is not a retirement")

	require.NoError(t, a.UpdateInsightRuleStatus(id, RuleKept, insightNow().Add(48*time.Hour)))
	ins, err = a.ReadInsight(id)
	require.NoError(t, err)
	assert.Equal(t, RuleKept, ins.RuleHistory[2].Kind)
}

// TestUpdateInsightRuleStatus_Rejects covers the two guards: an unknown kind
// and an insight with no rule.
func TestUpdateInsightRuleStatus_Rejects(t *testing.T) {
	a := New(t.TempDir())
	id := writeAcceptedInsight(t, a, insightNow(), "x")

	err := a.UpdateInsightRuleStatus(id, "maybe", insightNow())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kept|lapsed|retired")

	require.NoError(t, a.SetInsightRule(id, "a rule", insightNow()))
	unruled := writeAcceptedInsight(t, a, insightNow().Add(24*time.Hour), "y")
	err = a.UpdateInsightRuleStatus(unruled, RuleKept, insightNow())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has no rule")
}

// TestReadInsightsWindow_FiltersSortsCaps confirms the recall read primitive
// drops retired insights, honors the age cutoff, sorts most-recent-first, and
// applies the cap.
func TestReadInsightsWindow_FiltersSortsCaps(t *testing.T) {
	a := New(t.TempDir())
	base := time.Date(2026, time.May, 1, 12, 0, 0, 0, time.UTC)
	oldID := writeAcceptedInsight(t, a, base, "old but accepted")
	midID := writeAcceptedInsight(t, a, base.Add(5*24*time.Hour), "mid")
	newID := writeAcceptedInsight(t, a, base.Add(9*24*time.Hour), "new")
	retiredID := writeAcceptedInsight(t, a, base.Add(8*24*time.Hour), "to retire")
	require.NoError(t, a.UpdateInsightStatus(retiredID, RecallRetired, base.Add(8*24*time.Hour)))

	// Age cutoff at base+4d keeps mid + new, drops old and the retired one.
	win, err := a.ReadInsightsWindow(base.Add(4*24*time.Hour), 0)
	require.NoError(t, err)
	require.Len(t, win, 2)
	assert.Equal(t, newID, win[0].ID, "most recent first")
	assert.Equal(t, midID, win[1].ID)

	// Zero time = any age; retired still excluded; cap trims to the newest one.
	capped, err := a.ReadInsightsWindow(time.Time{}, 1)
	require.NoError(t, err)
	require.Len(t, capped, 1)
	assert.Equal(t, newID, capped[0].ID)

	all, err := a.ReadInsightsWindow(time.Time{}, 0)
	require.NoError(t, err)
	assert.Len(t, all, 3, "three accepted, retired excluded")
	assert.NotContains(t, []string{all[0].ID, all[1].ID, all[2].ID}, retiredID)
	_ = oldID
}

// reflectionSeed builds a minimal Reflection for a single pass.
func reflectionSeed(now time.Time, surfaced []ReflectionSurfaced, changeLog []string, summary string) Reflection {
	start := now.AddDate(0, 0, -2)
	end := now.AddDate(0, 0, 4)
	return Reflection{
		ID:           "reflection_2026_w19",
		ISOWeek:      "2026-W19",
		WindowStart:  start,
		WindowEnd:    end,
		CreatedAt:    now,
		AgentVersion: "reflection-2026.05.0",
		Surfaced:     surfaced,
		ChangeLog:    changeLog,
		Summary:      summary,
	}
}

// TestWriteReflection_CreatesThenAppends is the 6.4 core: the first pass creates
// the record; a second pass in the same week appends to insights_surfaced and
// the change log while leaving the body summary untouched.
func TestWriteReflection_CreatesThenAppends(t *testing.T) {
	a := New(t.TempDir())
	now := time.Date(2026, time.May, 9, 20, 10, 14, 0, time.UTC)

	first := reflectionSeed(now,
		[]ReflectionSurfaced{{ID: "i_2026_05_05_a", ResponseKind: "confirmed"}},
		[]string{"2026-05-09: Surfaced i_2026_05_05_a — confirmed."},
		"This week's recall surfaced 1 validated insight(s).")
	res, err := a.WriteReflection(first)
	require.NoError(t, err)
	assert.True(t, res.Created)
	assert.Equal(t, "reflection_2026_w19", res.ID)

	second := reflectionSeed(now.Add(time.Hour),
		[]ReflectionSurfaced{{ID: "i_2026_05_06_b", ResponseKind: "softened"}},
		[]string{"2026-05-09: Surfaced i_2026_05_06_b — softened."},
		"IGNORED second-pass summary")
	res2, err := a.WriteReflection(second)
	require.NoError(t, err)
	assert.False(t, res2.Created, "same-week second pass appends, not creates")

	rec, err := a.ReadReflection("reflection_2026_w19")
	require.NoError(t, err)
	require.Len(t, rec.Surfaced, 2, "both passes' surfaced entries")
	assert.Equal(t, "i_2026_05_05_a", rec.Surfaced[0].ID)
	assert.Equal(t, "i_2026_05_06_b", rec.Surfaced[1].ID)
	require.Len(t, rec.ChangeLog, 2)
	assert.Equal(t, "This week's recall surfaced 1 validated insight(s).", rec.Summary,
		"the body summary is set once on the first pass")
	assert.True(t, rec.CreatedAt.Equal(now), "created_at stays the first pass's instant")

	content, err := os.ReadFile(res.Path)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(content), "# Weekly recall"), "no duplicate body heading")
}

// TestWriteReflection_ByteStableRerun proves the merge is deterministic: writing
// the identical first pass twice produces the identical file bytes.
func TestWriteReflection_ByteStableRerun(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	now := time.Date(2026, time.May, 9, 20, 10, 14, 0, time.UTC)
	seed := reflectionSeed(now,
		[]ReflectionSurfaced{{ID: "i_2026_05_05_a", ResponseKind: "confirmed"}},
		[]string{"2026-05-09: Surfaced i_2026_05_05_a — confirmed."},
		"summary line")

	ra, err := New(dirA).WriteReflection(seed)
	require.NoError(t, err)
	rb, err := New(dirB).WriteReflection(seed)
	require.NoError(t, err)
	ba, err := os.ReadFile(ra.Path)
	require.NoError(t, err)
	bb, err := os.ReadFile(rb.Path)
	require.NoError(t, err)
	assert.Equal(t, ba, bb, "identical input renders byte-identical")
	assert.Contains(t, string(ba), "iso_week: 2026-W19")
	assert.Contains(t, string(ba), "# Weekly recall — week 19, 2026")
	assert.Contains(t, string(ba), "new_insight_ids: []")
}

// TestListAndLatestReflection covers the id listing and the latest-created-at
// helper the pointer-line check depends on.
func TestListAndLatestReflection(t *testing.T) {
	a := New(t.TempDir())

	_, ok, err := a.LatestReflectionCreatedAt()
	require.NoError(t, err)
	assert.False(t, ok, "no records yet")
	ids, err := a.ListReflectionIDs()
	require.NoError(t, err)
	assert.Empty(t, ids)

	earlier := time.Date(2026, time.May, 4, 20, 0, 0, 0, time.UTC)
	later := time.Date(2026, time.May, 11, 20, 0, 0, 0, time.UTC)
	w18 := reflectionSeed(earlier, nil, nil, "w18")
	w18.ID, w18.ISOWeek = "reflection_2026_w18", "2026-W18"
	w19 := reflectionSeed(later, nil, nil, "w19")
	_, err = a.WriteReflection(w18)
	require.NoError(t, err)
	_, err = a.WriteReflection(w19)
	require.NoError(t, err)

	ids, err = a.ListReflectionIDs()
	require.NoError(t, err)
	assert.Equal(t, []string{"reflection_2026_w18", "reflection_2026_w19"}, ids)

	at, ok, err := a.LatestReflectionCreatedAt()
	require.NoError(t, err)
	require.True(t, ok)
	assert.True(t, at.Equal(later), "latest is the most recent week's created_at")
}

// TestWriteReflection_RejectsEmptyID guards the id precondition.
func TestWriteReflection_RejectsEmptyID(t *testing.T) {
	a := New(t.TempDir())
	_, err := a.WriteReflection(Reflection{})
	require.Error(t, err)
}

// TestReadReflection_NotFound surfaces a missing record as an error.
func TestReadReflection_NotFound(t *testing.T) {
	a := New(t.TempDir())
	_, err := a.ReadReflection("reflection_2026_w01")
	require.Error(t, err)
}

// TestSplitReflectionBody covers the body parser directly, including the
// no-change-log and heading-absent branches.
func TestSplitReflectionBody(t *testing.T) {
	body := "\n# Weekly recall — week 19, 2026\n\nA summary.\n\n## Change log\n\n- line one\n- line two\n"
	summary, log := splitReflectionBody(body)
	assert.Equal(t, "A summary.", summary)
	assert.Equal(t, []string{"line one", "line two"}, log)

	summaryOnly, logNone := splitReflectionBody("\n# Weekly recall — week 1\n\nJust a summary.\n")
	assert.Equal(t, "Just a summary.", summaryOnly)
	assert.Empty(t, logNone)
}

// TestReflectionHeading covers the label parser's happy and degrade paths,
// including a `-W`-shaped label whose numbers do not parse.
func TestReflectionHeading(t *testing.T) {
	assert.Equal(t, "# Weekly recall — week 19, 2026", reflectionHeading("2026-W19"))
	assert.Equal(t, "# Weekly recall — not-a-week", reflectionHeading("not-a-week"))
	assert.Equal(t, "# Weekly recall — yyyy-Www", reflectionHeading("yyyy-Www"))
	assert.Equal(t, "# Weekly recall — 2026-Www", reflectionHeading("2026-Www"))
}

// TestSplitReflectionBody_NoHeading covers the branch where the body carries no
// recall heading — the summary is the whole trimmed body.
func TestSplitReflectionBody_NoHeading(t *testing.T) {
	summary, log := splitReflectionBody("just some text, no heading")
	assert.Equal(t, "just some text, no heading", summary)
	assert.Empty(t, log)
}

// TestUpdateInsight_MissingIDErrors confirms the recall update ops surface a
// read failure for an unknown insight rather than silently no-op'ing.
func TestUpdateInsight_MissingIDErrors(t *testing.T) {
	a := New(t.TempDir())
	require.Error(t, a.UpdateInsightStatus("i_2026_01_01_a", RecallConfirmed, insightNow()))
	require.Error(t, a.UpdateInsightRuleStatus("i_2026_01_01_a", RuleKept, insightNow()))
}

// TestReadInsightsWindow_CorruptInsightErrors confirms a corrupt insight file
// fails the window read rather than being silently dropped.
func TestReadInsightsWindow_CorruptInsightErrors(t *testing.T) {
	a := New(t.TempDir())
	id := writeAcceptedInsight(t, a, insightNow(), "x")
	path, err := a.insightPath(id)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("not frontmatter"), 0o600))
	_, err = a.ReadInsightsWindow(time.Time{}, 0)
	require.Error(t, err)
}

// TestReadReflection_CorruptErrors confirms a malformed reflection file surfaces
// a decode error.
func TestReadReflection_CorruptErrors(t *testing.T) {
	a := New(t.TempDir())
	require.NoError(t, os.MkdirAll(a.reflectionsDir(), 0o700))
	require.NoError(t, os.WriteFile(a.reflectionsDir()+"/reflection_2026_w19.md", []byte("no fence here"), 0o600))
	_, err := a.ReadReflection("reflection_2026_w19")
	require.Error(t, err)
}

// TestWriteReflection_AppendReadsCorruptErrors confirms an append onto a corrupt
// existing week file fails rather than clobbering it.
func TestWriteReflection_AppendReadsCorruptErrors(t *testing.T) {
	a := New(t.TempDir())
	require.NoError(t, os.MkdirAll(a.reflectionsDir(), 0o700))
	require.NoError(t, os.WriteFile(a.reflectionsDir()+"/reflection_2026_w19.md", []byte("bad"), 0o600))
	_, err := a.WriteReflection(reflectionSeed(
		time.Date(2026, time.May, 9, 20, 0, 0, 0, time.UTC), nil, nil, "s"))
	require.Error(t, err)
}

// writeRawReflection drops a hand-authored reflection document at the given id.
func writeRawReflection(t *testing.T, a *Adapter, id, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(a.reflectionsDir(), 0o700))
	require.NoError(t, os.WriteFile(a.reflectionsDir()+"/"+id+reflectionExt, []byte(body), 0o600))
}

// TestDecodeReflection_BadTimestamps confirms each timestamp field surfaces a
// parse error rather than a zero time.
func TestDecodeReflection_BadTimestamps(t *testing.T) {
	tmpl := func(created, start, end string) string {
		return "---\nid: reflection_2026_w19\niso_week: 2026-W19\n" +
			"window_start: " + start + "\nwindow_end: " + end + "\ncreated_at: " + created +
			"\nagent_version: reflection-2026.05.0\ninsights_surfaced: []\nnew_insight_ids: []\nnotes: null\n---\n\n" +
			"# Weekly recall — week 19, 2026\n\n## Change log\n"
	}
	good := "2026-05-09T20:00:00Z"
	cases := map[string]string{
		"bad-created": tmpl("NOPE", good, good),
		"bad-start":   tmpl(good, "NOPE", good),
		"bad-end":     tmpl(good, good, "NOPE"),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			a := New(t.TempDir())
			writeRawReflection(t, a, "reflection_2026_w19", body)
			_, err := a.ReadReflection("reflection_2026_w19")
			require.Error(t, err)
		})
	}
}

// TestListReflectionIDs_SkipsNonRecords confirms subdirectories and non-.md
// files in the reflections tree are ignored.
func TestListReflectionIDs_SkipsNonRecords(t *testing.T) {
	a := New(t.TempDir())
	writeRawReflection(t, a, "reflection_2026_w19",
		"---\nid: reflection_2026_w19\niso_week: 2026-W19\nwindow_start: 2026-05-04T00:00:00Z\n"+
			"window_end: 2026-05-10T23:59:59Z\ncreated_at: 2026-05-09T20:00:00Z\n"+
			"agent_version: reflection-2026.05.0\ninsights_surfaced: []\nnew_insight_ids: []\nnotes: null\n---\n\n"+
			"# Weekly recall — week 19, 2026\n\n## Change log\n")
	require.NoError(t, os.WriteFile(a.reflectionsDir()+"/notes.txt", []byte("x"), 0o600))
	require.NoError(t, os.MkdirAll(a.reflectionsDir()+"/archive", 0o700))

	ids, err := a.ListReflectionIDs()
	require.NoError(t, err)
	assert.Equal(t, []string{"reflection_2026_w19"}, ids)
}

// TestLatestReflectionCreatedAt_CorruptErrors confirms a corrupt latest record
// surfaces an error from the helper.
func TestLatestReflectionCreatedAt_CorruptErrors(t *testing.T) {
	a := New(t.TempDir())
	writeRawReflection(t, a, "reflection_2026_w19", "no fence")
	_, _, err := a.LatestReflectionCreatedAt()
	require.Error(t, err)
}
