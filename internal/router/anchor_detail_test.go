package router

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
)

// TestAnchorDetailReaderWeekEvents: the opt-in anchor-detail read returns the
// applied anchor events dated in the seven logical days ending today, each
// joined with its anchor's label and current kind and carrying its note,
// ordered by label, day, then receipt. A voided event and its void, an event
// before the week, and an unfinished graduation are left out; a graduation
// whose sunset landed is in, and the retired anchor still appears.
func TestAnchorDetailReaderWeekEvents(t *testing.T) {
	r, _, _ := newBootedRouter(t)
	c := newKindsClock()

	c.noon(t, "2026-03-01")
	addAnchorOfKind(t, r, c, "sobriety", "2026-03-01", engine.AnchorKindQuit, 0, 0)
	addAnchorOfKind(t, r, c, "morning-walk", "2026-03-01", engine.AnchorKindHabit, 0, 0)
	addAnchorOfKind(t, r, c, "evening-stretch", "2026-03-01", engine.AnchorKindHabit, 0, 0)

	c.noon(t, "2026-04-02") // before the 04-04..04-10 week
	_, err := r.AnchorMiss(AnchorMissRequest{Label: "morning-walk", Note: "too early", Now: c.next()})
	require.NoError(t, err)

	c.noon(t, "2026-04-04")
	addAnchorOfKind(t, r, c, "late-night-snacks", "2026-04-04", engine.AnchorKindLimit, 4, 90)
	c.noon(t, "2026-04-06")
	slip, err := r.AnchorSlip(AnchorSlipRequest{Label: "sobriety", Note: "rough night", Now: c.next()})
	require.NoError(t, err)
	use, err := r.AnchorUse(AnchorUseRequest{Label: "late-night-snacks", Intent: engine.AnchorIntentPlanned, Note: "birthday", Now: c.next()})
	require.NoError(t, err)
	wrong, err := r.AnchorUse(AnchorUseRequest{Label: "late-night-snacks", Intent: engine.AnchorIntentSlip, Now: c.next()})
	require.NoError(t, err)
	_, err = r.AnchorVoid(AnchorVoidRequest{EventID: wrong.Event.ID, Reason: "misheard", Now: c.next()})
	require.NoError(t, err)
	c.noon(t, "2026-04-08")
	miss, err := r.AnchorMiss(AnchorMissRequest{Label: "morning-walk", Note: "line one\nline two", Now: c.next()})
	require.NoError(t, err)
	_, err = r.AnchorSunset(AnchorSunsetRequest{Label: "evening-stretch", Graduated: true, Now: c.next()})
	require.NoError(t, err)

	got, err := r.AnchorDetail(time.Date(2026, time.April, 10, 18, 0, 0, 0, time.UTC))
	require.NoError(t, err)

	type line struct{ label, kind, typ, day, intent, note string }
	lines := make([]line, 0, len(got))
	for _, e := range got {
		lines = append(lines, line{e.Label, e.AnchorKind, e.Type, e.Day, e.Intent, e.Note})
	}
	assert.Equal(t, []line{
		{"evening-stretch", engine.AnchorKindHabit, engine.AnchorEventGraduate, "2026-04-08", "", ""},
		{"late-night-snacks", engine.AnchorKindLimit, engine.AnchorEventKind, "2026-04-04", "", ""},
		{"late-night-snacks", engine.AnchorKindLimit, engine.AnchorEventUse, "2026-04-06", engine.AnchorIntentPlanned, "birthday"},
		{"morning-walk", engine.AnchorKindHabit, engine.AnchorEventMiss, "2026-04-08", "", "line one\nline two"},
		{"sobriety", engine.AnchorKindQuit, engine.AnchorEventSlip, "2026-04-06", "", "rough night"},
	}, lines)

	byID := map[string]AnchorDetailEvent{}
	for _, e := range got {
		byID[e.ID] = e
	}
	assert.Contains(t, byID, slip.Event.ID, "the receipt travels with the line")
	assert.Contains(t, byID, use.Event.ID)
	assert.Contains(t, byID, miss.Event.ID)
	assert.NotContains(t, byID, wrong.Event.ID, "a voided event is left out")
	assert.Equal(t, 4, byID[got[1].ID].Allowance)
	assert.Equal(t, 90, byID[got[1].ID].WindowDays)

	t.Run("the week ends at the current logical day", func(t *testing.T) {
		// 02:00 on 04-11 is still 04-10's logical day (rollover 04:00).
		early, err := r.AnchorDetail(time.Date(2026, time.April, 11, 2, 0, 0, 0, time.UTC))
		require.NoError(t, err)
		assert.Equal(t, got, early)

		// A week later every event has aged out.
		later, err := r.AnchorDetail(time.Date(2026, time.April, 17, 18, 0, 0, 0, time.UTC))
		require.NoError(t, err)
		assert.Empty(t, later)
	})
}
