package router

import (
	"fmt"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/isoweek"
	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/storage"
)

// weekBundleWindowDays is the accepted-insight recall horizon the week bundle
// carries. It is the same rolling seven-day window `/reflect` surfaces
// (reflect.go recallWindowDays), so the weekly deep-dive reads exactly the
// insight slice the weekly recall does — not the ISO-week bounds, which key the
// bundle's day rows.
const weekBundleWindowDays = 7

// RawEntryDigest is one raw entry in the week bundle: its id, the day it groups
// under in the week, and its verbatim entry text (the fixed "# Entry" heading
// stripped). It carries no model-authored content — it is the user's own words,
// read through the sanctioned `/day` projection, so the deep-dive can cite an
// entry id (a raw-entry-id backreference) against the text the user actually
// wrote. An empty capture yields an empty Text.
type RawEntryDigest struct {
	ID   string
	Date string
	Text string
}

// WeekBundle is the projection-only week context the weekly deep-dive reads.
// Every field is assembled through a sanctuary-safe projection
// — the numbers copied verbatim from the same MetricsResult/StatusResult the CLI
// and companion expose, the per-day volume rows and observation events from the
// sanctioned `/day` join (the same read `lucid stats` uses), the raw digest from
// `/day`'s entry ids resolved to their verbatim text, and the accepted insights
// from the recall window. It never reaches the sanctuary
// engine|observations|registries trees directly, and it excludes companion
// message bodies (they are not persisted and are out of scope). It is a pure
// read: nothing is written beyond the idempotent tree scaffolds the read verbs
// already perform.
type WeekBundle struct {
	ISOWeek          string
	WindowStart      time.Time
	WindowEnd        time.Time
	Metrics          engine.Metrics
	Status           engine.Status
	Stats            []StatsDay
	RawDigest        []RawEntryDigest
	Observations     []observations.Event
	AcceptedInsights []storage.Insight
}

// BuildWeekBundle assembles the projection-only week bundle for the ISO week
// containing `now`. It reads the honest numbers through the Metrics/Status
// projections (never recomputed — identity.md: the Mirror copies projection
// numbers, it does not derive them), walks each logical day of the ISO week
// through the sanctioned `ReadDayView` join to fold the raw digest, the day's
// own observation events, and the per-day volume row (the exact loop shape
// `lucid stats` uses, so a day's counts match `lucid day`), and reads the
// accepted insights validated in the rolling recall window. No sanctuary tree is
// read directly; nothing is written.
func (r *Router) BuildWeekBundle(now time.Time) (WeekBundle, error) {
	now = whenOr(now)
	loc := now.Location()
	if err := r.prepareObservations(); err != nil {
		return WeekBundle{}, err
	}
	if err := r.prepareEngine(); err != nil {
		return WeekBundle{}, err
	}

	// Honest numbers, straight from the same projections `lucid metrics --json`
	// and `lucid status --json` expose. They are copied into the bundle, never
	// recomputed here.
	metricsRes, err := r.Metrics(now)
	if err != nil {
		return WeekBundle{}, fmt.Errorf("weekbundle: read metrics: %w", err)
	}
	statusRes, err := r.Status(now)
	if err != nil {
		return WeekBundle{}, fmt.Errorf("weekbundle: read status: %w", err)
	}

	start, end := isoweek.Bounds(now)
	bundle := WeekBundle{
		ISOWeek:      isoweek.Label(now),
		WindowStart:  start,
		WindowEnd:    end,
		Metrics:      metricsRes.Metrics,
		Status:       statusRes.Status,
		Stats:        make([]StatsDay, 0, 7),
		RawDigest:    make([]RawEntryDigest, 0),
		Observations: make([]observations.Event, 0),
	}

	// Per logical day of the ISO week, read the sanctioned `/day` join and fold
	// it into the digest, the observation slice, and the per-day volume row. This
	// is the same read `lucid stats` performs (stats.go): a user-invoked
	// projection joining across trees, not an agent reading the sanctuary tree.
	for _, d := range logicalDayRange(start, end) {
		dateStr := engine.DateString(d)
		view, verr := r.store.ReadDayView(dateStr, loc)
		if verr != nil {
			return WeekBundle{}, fmt.Errorf("weekbundle: read day view %s: %w", dateStr, verr)
		}

		for _, id := range view.RawEntryIDs {
			doc, rerr := r.store.ReadRaw(id)
			if rerr != nil {
				return WeekBundle{}, fmt.Errorf("weekbundle: read raw %s: %w", id, rerr)
			}
			bundle.RawDigest = append(bundle.RawDigest, RawEntryDigest{
				ID:   id,
				Date: dateStr,
				Text: doc.EntryText(),
			})
		}

		// The day's OWN observation events — exactly what `lucid stats` counts.
		// Obs.Events excludes spanning range events that started on an earlier
		// day, so a multi-day event appears once, on its start day, and the day
		// rows sum to the window without double-counting.
		bundle.Observations = append(bundle.Observations, view.Obs.Events...)

		raw := len(view.RawEntryIDs)
		obs := len(view.Obs.Events)
		bundle.Stats = append(bundle.Stats, StatsDay{
			Date:         dateStr,
			RawEntries:   raw,
			Observations: obs,
			TotalEvents:  raw + obs,
		})
	}

	// Accepted insights validated in the rolling recall window — the same
	// projection `/reflect` surfaces (ReadInsightsWindow reads the insights tree,
	// never the sanctuary engine|observations|registries trees). A nil result is
	// normalized to an empty slice so an empty week is an empty-but-valid bundle.
	insights, err := r.store.ReadInsightsWindow(now.Add(-weekBundleWindowDays*24*time.Hour), 0)
	if err != nil {
		return WeekBundle{}, fmt.Errorf("weekbundle: read accepted insights: %w", err)
	}
	if insights == nil {
		insights = []storage.Insight{}
	}
	bundle.AcceptedInsights = insights

	return bundle, nil
}
