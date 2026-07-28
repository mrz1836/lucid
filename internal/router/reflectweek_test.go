package router

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/frameworks"
	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/storage"
)

// reflectWeekRouter builds a booted router over a fresh temp Ledger (base +
// observations + engine trees scaffolded) and returns it with the adapter and
// the home path, so the read-only assertion can walk the whole tree.
func reflectWeekRouter(t *testing.T) (*Router, *storage.Adapter, string) {
	t.Helper()
	home := filepath.Join(t.TempDir(), ".lucid")
	a := storage.New(home)
	_, err := a.Scaffold()
	require.NoError(t, err)
	require.NoError(t, a.ScaffoldObservations())
	require.NoError(t, a.ScaffoldEngine())
	r := New(a)
	_, err = r.Boot()
	require.NoError(t, err)
	return r, a, home
}

// countTreeFiles counts every non-directory file under root — the read-only
// probe: the count must not change across a ReflectWeek run.
func countTreeFiles(t *testing.T, root string) int {
	t.Helper()
	var n int
	require.NoError(t, filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			n++
		}
		return nil
	}))
	return n
}

// deepWeekReply builds a full, valid deep-dive completion. A non-empty
// candidateText adds a candidate block citing citeID with shape_tag; the text is
// caller-chosen so a test can drive the clean / diagnostic / overclaim gates.
func deepWeekReply(candidateText, shapeTag, citeID string) provider.Exchange {
	body := `{"summary":"A steadier week overall.",` +
		`"wins":["logged entries"],"misses":["one skipped closeout"],` +
		`"body_pain":["a pain note mid-week"],"habit_change":["earlier evenings"],` +
		`"next_week":["one small experiment"]`
	if candidateText != "" {
		body += `,"candidate":{"proposal_text":"` + candidateText + `",` +
			`"shape_tag":"` + shapeTag + `","supporting_entry_ids":["` + citeID + `"]}`
	}
	body += "}"
	return provider.Exchange{Content: body}
}

// seedWeek lays down a realistic week: raws (giving citable ids), a body signal,
// and an in-window accepted insight. It returns the first raw id for citations.
func seedWeek(t *testing.T, a *storage.Adapter) string {
	t.Helper()
	seedRaws(t, a, time.Date(2026, 7, 1, 9, 0, 0, 0, edt), 2)
	seedObs(t, a, observations.KindPain, "2026-07-02", "2026-07-02T09:00:00-04:00")
	seedAcceptedInsight(t, a, time.Date(2026, 7, 1, 12, 0, 0, 0, edt), "I go quiet in groups.")
	return "raw_2026_07_01_09_00"
}

// TestReflectWeek_RichSurfacesSectionsAndPatternReadOnly proves a full week
// yields the ISO-week label, every narrative section, and a Safety-cleared
// candidate — and writes nothing (AC-2, AC-3, AC-7).
func TestReflectWeek_RichSurfacesSectionsAndPatternReadOnly(t *testing.T) {
	r, a, home := reflectWeekRouter(t)
	citeID := seedWeek(t, a)

	// Warm the sanctioned projections so the engine-status cache the read path
	// materializes on first call is already present; the file count then isolates
	// exactly what ReflectWeek writes (nothing).
	_, err := r.BuildWeekBundle(weekOf(), weekWindow(t, r, weekOf()))
	require.NoError(t, err)

	before := countTreeFiles(t, home)
	fake := &provider.Fake{Script: []provider.Exchange{
		deepWeekReply("One possible pattern: preparation as a way to feel safe.", "prep-as-safety", citeID),
	}}

	res, err := r.ReflectWeek(context.Background(), ReflectWeekRequest{Now: weekOf(), Provider: fake})
	require.NoError(t, err)

	assert.Equal(t, "2026-W27", res.ISOWeek)
	assert.NotEmpty(t, res.Summary)
	assert.NotEmpty(t, res.Wins)
	assert.NotEmpty(t, res.Misses)
	assert.NotEmpty(t, res.BodyPain)
	assert.NotEmpty(t, res.HabitChange)
	assert.NotEmpty(t, res.NextWeek)
	require.NotNil(t, res.Pattern, "a Safety-cleared candidate is surfaced")
	assert.Equal(t, "prep-as-safety", res.Pattern.ShapeTag)
	assert.Equal(t, []string{citeID}, res.Pattern.SupportingEntryIDs)

	assert.Equal(t, before, countTreeFiles(t, home), "reflect week writes no new files (read-only)")
}

// TestReflectWeek_EmptyStoreNoModelCall proves an empty store yields an
// empty-but-valid result with no model call and no pattern (AC-11).
func TestReflectWeek_EmptyStoreNoModelCall(t *testing.T) {
	r, _, home := reflectWeekRouter(t)

	// Warm the projection cache first (see the rich case), then isolate ReflectWeek.
	_, err := r.BuildWeekBundle(weekOf(), weekWindow(t, r, weekOf()))
	require.NoError(t, err)

	before := countTreeFiles(t, home)
	fake := &provider.Fake{}

	res, err := r.ReflectWeek(context.Background(), ReflectWeekRequest{Now: weekOf(), Provider: fake})
	require.NoError(t, err)

	assert.Equal(t, 0, fake.Calls(), "no model call over an empty store")
	assert.Equal(t, "2026-W27", res.ISOWeek)
	assert.Empty(t, res.Summary)
	assert.Nil(t, res.Pattern)
	assert.Equal(t, before, countTreeFiles(t, home), "empty reflect week writes nothing")
}

// TestReflectWeek_SafetyBlocksDiagnosticCandidate proves a diagnostic candidate
// is blocked by Safety and never surfaced, while the narrative survives (AC-7).
func TestReflectWeek_SafetyBlocksDiagnosticCandidate(t *testing.T) {
	r, a, _ := reflectWeekRouter(t)
	citeID := seedWeek(t, a)

	fake := &provider.Fake{Script: []provider.Exchange{
		deepWeekReply("You have an avoidant attachment style.", "avoidant-label", citeID),
	}}

	res, err := r.ReflectWeek(context.Background(), ReflectWeekRequest{Now: weekOf(), Provider: fake})
	require.NoError(t, err)

	assert.NotEmpty(t, res.Summary, "the narrative survives a blocked candidate")
	assert.Nil(t, res.Pattern, "a diagnostic candidate is blocked, never surfaced")
}

// TestReflectWeek_PauseSuppressesCandidate proves an active proposal pause
// suppresses the candidate while the narrative still surfaces, and the pause
// state is read without being mutated (still read-only).
func TestReflectWeek_PauseSuppressesCandidate(t *testing.T) {
	r, a, _ := reflectWeekRouter(t)
	citeID := seedWeek(t, a)

	until := weekOf().Add(48 * time.Hour)
	require.NoError(t, a.WriteProposalPauseState(storage.ProposalPauseState{ConsecutiveUnanswered: 3, PausedUntil: &until}))

	fake := &provider.Fake{Script: []provider.Exchange{
		deepWeekReply("One possible pattern: preparation as a way to feel safe.", "prep-as-safety", citeID),
	}}

	res, err := r.ReflectWeek(context.Background(), ReflectWeekRequest{Now: weekOf(), Provider: fake})
	require.NoError(t, err)

	assert.NotEmpty(t, res.Summary, "the narrative surfaces even while paused")
	assert.Nil(t, res.Pattern, "an active pause suppresses the candidate")

	// The pause was read, not cleared: it still stands unchanged.
	st, err := a.ReadProposalPauseState()
	require.NoError(t, err)
	require.NotNil(t, st.PausedUntil)
	assert.True(t, st.PausedUntil.Equal(until), "the pause is read-only, never rewritten")
}

// reflectionReceiptPath is the reflected-through cursor's on-disk location under
// a test Ledger — the file whose ABSENCE proves the read stayed read-only.
func reflectionReceiptPath(home string) string {
	return filepath.Join(home, "engine", "reflection", "receipt.json")
}

// TestReflectWeek_ReadLeavesNoReflectionCursor is the read-only guarantee at its
// sharpest point. Resolving the window now READS the reflected-through cursor,
// so the one way this change could break the contract is by creating that file
// on a Ledger that has none. It must not: only an explicit close or a completed
// apply advances the cursor, and a read that stamped it would silently mark the
// days done — exactly the drop the catch-up window exists to prevent.
func TestReflectWeek_ReadLeavesNoReflectionCursor(t *testing.T) {
	r, a, home := reflectWeekRouter(t)
	citeID := seedWeek(t, a)

	// Warm the projections so the file count isolates ReflectWeek alone.
	_, err := r.BuildWeekBundle(weekOf(), weekWindow(t, r, weekOf()))
	require.NoError(t, err)
	before := countTreeFiles(t, home)

	fake := &provider.Fake{Script: []provider.Exchange{
		deepWeekReply("One possible pattern: preparation as a way to feel safe.", "prep-as-safety", citeID),
	}}
	_, err = r.ReflectWeek(context.Background(), ReflectWeekRequest{Now: weekOf(), Provider: fake})
	require.NoError(t, err)

	assert.NoFileExists(t, reflectionReceiptPath(home), "the read never stamps the reflected-through cursor")
	assert.NoDirExists(t, filepath.Dir(reflectionReceiptPath(home)), "nor creates its directory")
	assert.Equal(t, before, countTreeFiles(t, home), "the read writes no file at all")

	// And it is repeatable: a second read resolves the same window, because the
	// first one did not move the cursor out from under it.
	_, _, err = a.ReadReflectionReceipt()
	require.NoError(t, err)
}

// TestReflectWeek_CarriesResolvedWindowFacts proves the window the router
// resolved reaches the result verbatim — the surface layer reports what was
// actually read rather than re-deriving it and drifting.
func TestReflectWeek_CarriesResolvedWindowFacts(t *testing.T) {
	r, a, _ := reflectWeekRouter(t)
	citeID := seedWeek(t, a)

	fake := &provider.Fake{Script: []provider.Exchange{
		deepWeekReply("One possible pattern: preparation as a way to feel safe.", "prep-as-safety", citeID),
	}}
	res, err := r.ReflectWeek(context.Background(), ReflectWeekRequest{
		Now:      weekOf(),
		Provider: fake,
		Window:   ReflectWindowOptions{Mode: ReflectWindowDays, Days: 14},
	})
	require.NoError(t, err)

	assert.Equal(t, 14, res.DaysCovered)
	assert.Equal(t, "2026-06-19", res.WindowStart.Format("2006-01-02"))
	assert.Equal(t, "2026-07-02", res.WindowEnd.Format("2006-01-02"))
	assert.Equal(t, "2026-W27", res.ISOWeek, "the label names the window's END week")
	assert.False(t, res.Capped, "an explicit range override is never capped")
	assert.Zero(t, res.UncoveredDays)
}

// TestReflectWeek_DefaultWindowIsTheCatchUpRead proves the request's zero value
// resolves to the since-last-reflection window rather than a fixed calendar
// week: with no cursor the read reaches back to the earliest logged entry.
func TestReflectWeek_DefaultWindowIsTheCatchUpRead(t *testing.T) {
	r, a, _ := reflectWeekRouter(t)
	citeID := seedWeek(t, a)

	fake := &provider.Fake{Script: []provider.Exchange{
		deepWeekReply("One possible pattern: preparation as a way to feel safe.", "prep-as-safety", citeID),
	}}
	res, err := r.ReflectWeek(context.Background(), ReflectWeekRequest{Now: weekOf(), Provider: fake})
	require.NoError(t, err)

	assert.Equal(t, "2026-07-01", res.WindowStart.Format("2006-01-02"), "the first-run window starts at the earliest entry")
	assert.Equal(t, "2026-07-02", res.WindowEnd.Format("2006-01-02"))
	assert.Equal(t, 2, res.DaysCovered)
}

// TestReflectWeekClose_StampsAndRereadsCursor proves a close writes the cursor
// and it round-trips: the instant is preserved and the act is stamped as its
// provenance.
func TestReflectWeekClose_StampsAndRereadsCursor(t *testing.T) {
	r, a, home := reflectWeekRouter(t)

	res, err := r.CloseReflectWeek(weekOf())
	require.NoError(t, err)
	assert.True(t, res.ReflectedThrough.Equal(weekOf()))
	assert.Equal(t, "close", res.Source)
	assert.True(t, res.WrittenAt.Equal(weekOf()))
	assert.FileExists(t, reflectionReceiptPath(home))

	receipt, ok, err := a.ReadReflectionReceipt()
	require.NoError(t, err)
	require.True(t, ok, "the cursor is readable after a close")
	assert.Equal(t, "close", receipt.Source)

	through, err := time.Parse(time.RFC3339, receipt.ReflectedThrough)
	require.NoError(t, err)
	assert.True(t, through.Equal(weekOf()), "the close instant is stamped, not a remembered window")
}

// TestReflectWeekClose_OverwritesRatherThanAppends proves the receipt is a
// cursor, not a history: a second close replaces the first, leaving one file.
func TestReflectWeekClose_OverwritesRatherThanAppends(t *testing.T) {
	r, a, home := reflectWeekRouter(t)

	_, err := r.CloseReflectWeek(weekOf())
	require.NoError(t, err)
	after := weekOf().Add(72 * time.Hour)
	_, err = r.CloseReflectWeek(after)
	require.NoError(t, err)

	assert.Equal(t, 1, countTreeFiles(t, filepath.Dir(reflectionReceiptPath(home))), "one cursor file, overwritten")

	receipt, ok, err := a.ReadReflectionReceipt()
	require.NoError(t, err)
	require.True(t, ok)
	through, err := time.Parse(time.RFC3339, receipt.ReflectedThrough)
	require.NoError(t, err)
	assert.True(t, through.Equal(after), "the later close wins")
}

// TestReflectWeek_ClosedCursorBoundsTheNextRead is the whole point of the
// cursor, proven end to end at the router: after a close, the next default read
// starts at the close DAY — re-reading it rather than half-dropping an entry
// logged later that same evening.
func TestReflectWeek_ClosedCursorBoundsTheNextRead(t *testing.T) {
	r, a, _ := reflectWeekRouter(t)
	citeID := seedWeek(t, a)

	closedAt := time.Date(2026, 7, 2, 20, 0, 0, 0, edt)
	_, err := r.CloseReflectWeek(closedAt)
	require.NoError(t, err)

	fake := &provider.Fake{Script: []provider.Exchange{
		deepWeekReply("One possible pattern: preparation as a way to feel safe.", "prep-as-safety", citeID),
	}}
	res, err := r.ReflectWeek(context.Background(), ReflectWeekRequest{
		Now:      time.Date(2026, 7, 5, 9, 0, 0, 0, edt),
		Provider: fake,
	})
	require.NoError(t, err)

	assert.Equal(t, "2026-07-02", res.WindowStart.Format("2006-01-02"), "the close day is re-read, never half-dropped")
	assert.Equal(t, "2026-07-05", res.WindowEnd.Format("2006-01-02"))
	assert.Equal(t, 4, res.DaysCovered)
}

// TestReflectWeek_AppliedLensLabel proves a consented lens frames the run and
// its "<id> v<version>" label reaches the result (AC-6 label path).
func TestReflectWeek_AppliedLensLabel(t *testing.T) {
	r, a, _ := reflectWeekRouter(t)
	citeID := seedWeek(t, a)

	lens := frameworks.Lens{ID: "stoicism", Version: 1, Name: "Stoicism"}
	fake := &provider.Fake{Script: []provider.Exchange{
		deepWeekReply("One possible pattern: preparation as a way to feel safe.", "prep-as-safety", citeID),
	}}

	res, err := r.ReflectWeek(context.Background(), ReflectWeekRequest{Now: weekOf(), Provider: fake, ActiveLens: &lens})
	require.NoError(t, err)

	assert.Equal(t, "stoicism v1", res.AppliedLens)
}

// TestWeekWindowLabel_NamesTheSpanTruthfully proves the label the deep-dive is
// handed states the real span, and pluralizes its day count. A single-day window
// is the realistic case — close today, read again today — and telling the model
// "(1 days)" in the one line that frames its entire slice is exactly the kind of
// sloppiness that reads as an untrustworthy Mirror.
func TestWeekWindowLabel_NamesTheSpanTruthfully(t *testing.T) {
	cases := []struct {
		name       string
		start, end string
		days       int
		want       string
	}{
		{"a single day is singular", "2026-07-05", "2026-07-05", 1, "2026-07-05 to 2026-07-05 (1 day)"},
		{"a catch-up span is plural", "2026-07-13", "2026-07-26", 14, "2026-07-13 to 2026-07-26 (14 days)"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, err := time.ParseInLocation("2006-01-02", tc.start, edt)
			require.NoError(t, err)
			end, err := time.ParseInLocation("2006-01-02", tc.end, edt)
			require.NoError(t, err)

			label := weekWindowLabel(WeekBundle{WindowStart: start, WindowEnd: end, DaysCovered: tc.days})
			assert.Equal(t, tc.want, label)
			// The slice the model sees names dates only — never a Ledger path.
			assert.NotContains(t, label, "engine/")
		})
	}
}
