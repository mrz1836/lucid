package router

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/isoweek"
	"github.com/mrz1836/lucid/internal/storage"
)

// seedReflectionCursor stamps the reflected-through cursor at `through`, the
// state an explicit close leaves behind. It is the only input a default window
// resolves its start from.
func seedReflectionCursor(t *testing.T, a *storage.Adapter, through time.Time) {
	t.Helper()
	require.NoError(t, a.WriteReflectionReceipt(storage.ReflectionReceipt{
		ReflectedThrough: through.Format(time.RFC3339),
		Source:           "close",
		WrittenAt:        through.Format(time.RFC3339),
	}))
}

// TestResolveReflectWindow_DefaultFromCursor is the catch-up guarantee: the
// default window runs from the day of the last close through the end of today,
// however long ago that close was. A skipped week widens the window instead of
// falling outside it.
func TestResolveReflectWindow_DefaultFromCursor(t *testing.T) {
	for _, tc := range []struct {
		name     string
		daysBack int
		wantDays int
	}{
		{"one week", 6, 7},
		{"two weeks — a skipped reflection", 13, 14},
		{"three weeks — two skipped", 20, 21},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, a := statsRouter(t)
			seedReflectionCursor(t, a, weekOf().AddDate(0, 0, -tc.daysBack))

			win, err := r.resolveReflectWindow(weekOf(), ReflectWindowOptions{})
			require.NoError(t, err)

			assert.Equal(t, tc.wantDays, win.Days)
			assert.Equal(t, reflectSourceCursor, win.Source)
			assert.Equal(t, weekOf().AddDate(0, 0, -tc.daysBack).Format("2006-01-02"),
				win.Start.Format("2006-01-02"), "the window opens on the close day")
			assert.Equal(t, endOfCivilDay(weekOf()), win.End, "and closes at the end of today")
			assert.False(t, win.Capped)
			assert.Zero(t, win.UncoveredDays)
		})
	}
}

// TestResolveReflectWindow_CursorDayIsReRead is the never-drop boundary. Closing
// at 20:00 and logging at 22:00 the same evening would lose that entry if the
// next window started at the close INSTANT, so it starts at the beginning of the
// close day: the day is re-read rather than half-dropped.
func TestResolveReflectWindow_CursorDayIsReRead(t *testing.T) {
	r, a := statsRouter(t)
	closedAt := time.Date(2026, 6, 28, 20, 0, 0, 0, edt)
	seedReflectionCursor(t, a, closedAt)

	win, err := r.resolveReflectWindow(weekOf(), ReflectWindowOptions{})
	require.NoError(t, err)

	assert.Equal(t, time.Date(2026, 6, 28, 0, 0, 0, 0, edt), win.Start,
		"the whole close day is in the window, not the remainder of it")
	assert.True(t, win.Start.Before(closedAt), "an entry logged after the close is still read")
	assert.Equal(t, 5, win.Days, "06-28 through 07-02 inclusive")
}

// TestResolveReflectWindow_FirstRunFromEarliestEntry: with no cursor, a
// first-ever reflection reads from the earliest logged entry, so nothing in the
// record is skipped on the very first sit-down.
func TestResolveReflectWindow_FirstRunFromEarliestEntry(t *testing.T) {
	r, a := statsRouter(t)
	seedRaws(t, a, time.Date(2026, 6, 20, 9, 0, 0, 0, edt), 1)
	seedRaws(t, a, time.Date(2026, 7, 1, 9, 0, 0, 0, edt), 1)

	win, err := r.resolveReflectWindow(weekOf(), ReflectWindowOptions{})
	require.NoError(t, err)

	assert.Equal(t, reflectSourceFirstRun, win.Source)
	assert.Equal(t, time.Date(2026, 6, 20, 0, 0, 0, 0, edt), win.Start, "the earliest entry anchors the window")
	assert.Equal(t, 13, win.Days)
}

// TestResolveReflectWindow_EmptyLedgerFallsBackToISOWeek: with neither a cursor
// nor a logged entry there is no honest "since", so the window is today's ISO
// week — which routes straight into the existing empty-week short-circuit.
func TestResolveReflectWindow_EmptyLedgerFallsBackToISOWeek(t *testing.T) {
	r, _ := statsRouter(t)

	win, err := r.resolveReflectWindow(weekOf(), ReflectWindowOptions{})
	require.NoError(t, err)

	wantStart, wantEnd := isoweek.Bounds(weekOf())
	assert.Equal(t, reflectSourceEmpty, win.Source)
	assert.Equal(t, wantStart, win.Start)
	assert.Equal(t, wantEnd, win.End)
	assert.Equal(t, 7, win.Days)
}

// TestResolveReflectWindow_WeekMatchesISOBounds: the --week override reproduces
// the pre-catch-up behavior byte for byte. Asserted by calling isoweek.Bounds
// rather than re-deriving Monday and Sunday, so the two can never drift apart.
func TestResolveReflectWindow_WeekMatchesISOBounds(t *testing.T) {
	r, a := statsRouter(t)
	// A cursor and a raw entry are both present: --week must ignore them.
	seedReflectionCursor(t, a, weekOf().AddDate(0, 0, -40))
	seedRaws(t, a, time.Date(2026, 5, 1, 9, 0, 0, 0, edt), 1)

	win, err := r.resolveReflectWindow(weekOf(), ReflectWindowOptions{Mode: ReflectWindowWeek})
	require.NoError(t, err)

	wantStart, wantEnd := isoweek.Bounds(weekOf())
	assert.Equal(t, wantStart, win.Start)
	assert.Equal(t, wantEnd, win.End)
	assert.Equal(t, 7, win.Days)
	assert.Equal(t, reflectSourceWeek, win.Source)
	assert.False(t, win.Capped, "an explicit override is never capped")
}

// TestResolveReflectWindow_SinceBypassesTheCap: --since is a deliberate request
// to read the full span, so a window far beyond the ceiling is honored intact.
func TestResolveReflectWindow_SinceBypassesTheCap(t *testing.T) {
	r, _ := statsRouter(t)
	from := weekOf().AddDate(0, 0, -95)

	win, err := r.resolveReflectWindow(weekOf(), ReflectWindowOptions{Mode: ReflectWindowSince, Since: from})
	require.NoError(t, err)

	assert.Equal(t, 96, win.Days, "the full span, uncapped")
	assert.False(t, win.Capped)
	assert.Zero(t, win.UncoveredDays)
	assert.Equal(t, reflectSourceSince, win.Source)
	assert.Equal(t, from.Format("2006-01-02"), win.Start.Format("2006-01-02"))
}

// TestResolveReflectWindow_Days: --days N reads the trailing N logical days,
// today inclusive, and rejects a count below one rather than resolving an empty
// or inverted window.
func TestResolveReflectWindow_Days(t *testing.T) {
	r, _ := statsRouter(t)

	win, err := r.resolveReflectWindow(weekOf(), ReflectWindowOptions{Mode: ReflectWindowDays, Days: 3})
	require.NoError(t, err)
	assert.Equal(t, 3, win.Days)
	assert.Equal(t, reflectSourceDays, win.Source)
	assert.Equal(t, time.Date(2026, 6, 30, 0, 0, 0, 0, edt), win.Start, "today inclusive")
	assert.Equal(t, endOfCivilDay(weekOf()), win.End)

	for _, bad := range []int{0, -1} {
		_, err = r.resolveReflectWindow(weekOf(), ReflectWindowOptions{Mode: ReflectWindowDays, Days: bad})
		require.Errorf(t, err, "days=%d is rejected", bad)
	}
}

// TestResolveReflectWindow_CapDefersTheShortfall: past the ceiling the read
// covers the most recent allowed span and records exactly what it did not reach.
// The deferral is a stated number, not a silent truncation — that number is what
// the surface layer prints and what --since overrides.
func TestResolveReflectWindow_CapDefersTheShortfall(t *testing.T) {
	r, a := statsRouter(t)
	require.Equal(t, 35, r.Config().ReflectWeekMaxDays, "the default ceiling is five weeks")
	seedReflectionCursor(t, a, weekOf().AddDate(0, 0, -95))

	win, err := r.resolveReflectWindow(weekOf(), ReflectWindowOptions{})
	require.NoError(t, err)

	assert.True(t, win.Capped)
	assert.Equal(t, 35, win.Days)
	assert.Equal(t, 61, win.UncoveredDays, "96 un-reflected days less the 35 covered")
	assert.Equal(t, endOfCivilDay(weekOf()), win.End, "the cap moves the start, never the end")
	assert.Equal(t, time.Date(2026, 5, 29, 0, 0, 0, 0, edt), win.Start)
	assert.Equal(t, reflectSourceCursor, win.Source, "a capped window is still a cursor window")
}

// TestResolveReflectWindow_CapIsConfigurable proves the ceiling is read from
// config rather than hardcoded, so a Ledger can widen or tighten it.
func TestResolveReflectWindow_CapIsConfigurable(t *testing.T) {
	r, a := statsRouter(t)
	seedReflectionCursor(t, a, weekOf().AddDate(0, 0, -95))
	r.cfg.ReflectWeekMaxDays = 14

	win, err := r.resolveReflectWindow(weekOf(), ReflectWindowOptions{})
	require.NoError(t, err)

	assert.True(t, win.Capped)
	assert.Equal(t, 14, win.Days)
	assert.Equal(t, 82, win.UncoveredDays)
}

// TestResolveReflectWindow_DSTSpanCountsWholeDays: a window spanning the
// spring-forward transition still counts whole calendar days. Elapsed-hour
// arithmetic would report one day short, because one of those days is only 23
// hours long — and a window that quietly loses a day is the failure this whole
// surface exists to prevent.
func TestResolveReflectWindow_DSTSpanCountsWholeDays(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("tzdata unavailable; the DST span needs a real transition")
	}
	r, _ := statsRouter(t)

	// 2026-03-08 is the spring-forward Sunday in America/New_York.
	now := time.Date(2026, 3, 15, 12, 0, 0, 0, loc)
	from := time.Date(2026, 3, 1, 0, 0, 0, 0, loc)

	win, err := r.resolveReflectWindow(now, ReflectWindowOptions{Mode: ReflectWindowSince, Since: from})
	require.NoError(t, err)

	assert.Equal(t, 15, win.Days, "03-01 through 03-15 inclusive, transition and all")
	assert.Less(t, win.End.Sub(win.Start).Hours(), float64(15*24),
		"the span is genuinely 23 hours short of 15×24 — the day count must not be derived from it")
}

// TestResolveReflectWindow_FutureCursorClamps: a cursor ahead of today (clock
// skew, a hand-edited receipt) yields a single-day window rather than an
// inverted one with a negative day count.
func TestResolveReflectWindow_FutureCursorClamps(t *testing.T) {
	r, a := statsRouter(t)
	seedReflectionCursor(t, a, weekOf().AddDate(0, 0, 5))

	win, err := r.resolveReflectWindow(weekOf(), ReflectWindowOptions{})
	require.NoError(t, err)

	assert.Equal(t, startOfCivilDay(weekOf()), win.Start)
	assert.Equal(t, 1, win.Days)
}

// TestResolveReflectWindow_CorruptCursorSurfaces: the cursor is written only
// through the binary, so an unparseable value is a hand-edit or a bad write.
// Guessing a start date from it could silently drop days, so it is surfaced.
func TestResolveReflectWindow_CorruptCursorSurfaces(t *testing.T) {
	r, a := statsRouter(t)
	require.NoError(t, a.WriteReflectionReceipt(storage.ReflectionReceipt{
		ReflectedThrough: "last Tuesday",
		Source:           "close",
	}))

	_, err := r.resolveReflectWindow(weekOf(), ReflectWindowOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reflected_through")
}

// TestResolveReflectWindow_RejectsIncompleteOptions: a since-mode request with
// no date, and an unrecognized mode, are both errors rather than a silent
// fallback to some other window.
func TestResolveReflectWindow_RejectsIncompleteOptions(t *testing.T) {
	r, _ := statsRouter(t)

	_, err := r.resolveReflectWindow(weekOf(), ReflectWindowOptions{Mode: ReflectWindowSince})
	require.Error(t, err, "since without a date is rejected")

	_, err = r.resolveReflectWindow(weekOf(), ReflectWindowOptions{Mode: "fortnight"})
	require.Error(t, err, "an unknown mode is rejected")
}

// TestResolveReflectWindow_ReadsNothingIntoTheTree is the read-only floor: the
// resolver reads the cursor and must never create it. The weekly deep-dive calls
// this on every invocation, and a resolver that scaffolded its own receipt would
// mark days reflected that nobody sat with.
func TestResolveReflectWindow_ReadsNothingIntoTheTree(t *testing.T) {
	a := storage.New(filepath.Join(t.TempDir(), ".lucid"))
	_, err := a.Scaffold()
	require.NoError(t, err)
	require.NoError(t, a.ScaffoldObservations())
	require.NoError(t, a.ScaffoldEngine())
	r := New(a)
	_, err = r.Boot()
	require.NoError(t, err)

	win, err := r.resolveReflectWindow(weekOf(), ReflectWindowOptions{})
	require.NoError(t, err)
	assert.Equal(t, reflectSourceEmpty, win.Source)

	_, err = os.Stat(filepath.Join(a.Home(), "engine", "reflection", "receipt.json"))
	assert.True(t, os.IsNotExist(err), "resolving a window creates no cursor")
}
