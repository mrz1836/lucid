package router

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/storage"
)

// writeGalleryMedia writes one synthetic media through storage and returns its
// record — no real content, no real subject. The caption both labels the row
// and seeds the stored id, so distinct captions yield distinct ids.
func writeGalleryMedia(t *testing.T, a *storage.Adapter, caption, logicalDay string, capturedAt time.Time) storage.MediaRecord {
	t.Helper()
	rec, err := a.WriteMedia(storage.MediaAttachment{
		Bytes:            []byte("\xff\xd8\xff synthetic " + caption),
		OriginalFilename: "photo.jpg",
		CapturedAt:       capturedAt,
		LogicalDay:       logicalDay,
		Caption:          caption,
		Source:           "cli",
	})
	require.NoError(t, err)
	return rec
}

// linkGalleryMedia links a stored media to one synthetic subject through the
// real Link verb, so the fixture exercises the same resolve-and-append path the
// CLI does.
func linkGalleryMedia(t *testing.T, r *Router, mediaID, subject string) {
	t.Helper()
	_, err := r.Link(LinkRequest{
		Media: mediaID, Subjects: []string{subject},
		Op: storage.LinkOpLink, Now: fixedNow(), Source: "cli",
	})
	require.NoError(t, err)
}

// logicalDays projects a result's items to their logical days, in order — the
// shape most ordering/window assertions compare against.
func logicalDays(res GalleryResult) []string {
	out := make([]string, 0, len(res.Items))
	for _, it := range res.Items {
		out = append(out, it.Media.LogicalDay)
	}
	return out
}

// captions projects a result's items to their captions, in order.
func captions(res GalleryResult) []string {
	out := make([]string, 0, len(res.Items))
	for _, it := range res.Items {
		out = append(out, it.Media.Caption)
	}
	return out
}

// TestGallery_OrdersByLogicalDay proves the timeline is ascending by logical_day
// with the same-day tiebreak captured_at then id (AC-7). The 2026-05-10 cluster
// is arranged so captured_at (not id) drives the intra-day order, and a
// captured_at tie falls back to id — inserts are out of order so only the sort
// can produce the expected sequence.
func TestGallery_OrdersByLogicalDay(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	day := func(h int) time.Time { return time.Date(2026, 5, 10, h, 0, 0, 0, time.UTC) }
	// Out-of-order inserts across and within days.
	writeGalleryMedia(t, a, "d20", "2026-05-20", fixedNow())
	writeGalleryMedia(t, a, "d10-cap-late", "2026-05-10", day(23)) // latest capture, id sorts mid
	writeGalleryMedia(t, a, "d01", "2026-05-01", fixedNow())
	writeGalleryMedia(t, a, "d10-id-b", "2026-05-10", day(12))     // captured_at tie with id-a → id decides
	writeGalleryMedia(t, a, "d10-cap-early", "2026-05-10", day(1)) // earliest capture, id sorts late
	writeGalleryMedia(t, a, "d10-id-a", "2026-05-10", day(12))     // captured_at tie with id-b → id decides

	res, err := r.Gallery(GalleryRequest{})
	require.NoError(t, err)
	require.True(t, res.Found)

	// Cross-day ascending; within 2026-05-10: captured_at asc (early, then the two
	// noon ties by id a<b, then late) — an order id-only sorting could not produce.
	assert.Equal(t, []string{
		"d01",
		"d10-cap-early",
		"d10-id-a",
		"d10-id-b",
		"d10-cap-late",
		"d20",
	}, captions(res))
}

// TestGallery_DateWindowInclusive proves --since/--until filter inclusively on
// both ends, usable alone or together, and that a day just outside is excluded
// (AC-8).
func TestGallery_DateWindowInclusive(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	writeGalleryMedia(t, a, "may01", "2026-05-01", fixedNow())
	writeGalleryMedia(t, a, "may10", "2026-05-10", fixedNow())
	writeGalleryMedia(t, a, "may20", "2026-05-20", fixedNow())
	writeGalleryMedia(t, a, "may30", "2026-05-30", fixedNow())

	// since-only: the boundary day is included.
	res, err := r.Gallery(GalleryRequest{Since: "2026-05-10"})
	require.NoError(t, err)
	assert.Equal(t, []string{"2026-05-10", "2026-05-20", "2026-05-30"}, logicalDays(res))

	// until-only: the boundary day is included.
	res, err = r.Gallery(GalleryRequest{Until: "2026-05-20"})
	require.NoError(t, err)
	assert.Equal(t, []string{"2026-05-01", "2026-05-10", "2026-05-20"}, logicalDays(res))

	// both bounds: both boundary days are included.
	res, err = r.Gallery(GalleryRequest{Since: "2026-05-10", Until: "2026-05-20"})
	require.NoError(t, err)
	assert.Equal(t, []string{"2026-05-10", "2026-05-20"}, logicalDays(res))

	// a window strictly between two stored days matches nothing.
	res, err = r.Gallery(GalleryRequest{Since: "2026-05-11", Until: "2026-05-19"})
	require.NoError(t, err)
	assert.False(t, res.Found)
	assert.Empty(t, res.Items)
}

// TestGallery_SubjectFilter proves only media linked to the subject appear, that
// the subject filter combines with the date window, and that an unknown subject
// is refused rather than silently empty (AC-9).
func TestGallery_SubjectFilter(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	x := writeGalleryMedia(t, a, "spring baseline", "2026-05-01", fixedNow())
	y := writeGalleryMedia(t, a, "summer progress", "2026-07-20", fixedNow())
	writeGalleryMedia(t, a, "unrelated", "2026-06-01", fixedNow()) // deliberately unlinked

	linkGalleryMedia(t, r, x.ID, "day:2026-07-05")
	linkGalleryMedia(t, r, y.ID, "day:2026-07-05")

	// Subject only: just the two linked media, ordered by logical_day, each
	// carrying its link context; the unlinked media never appears.
	res, err := r.Gallery(GalleryRequest{SubjectKind: "day", SubjectKey: "2026-07-05"})
	require.NoError(t, err)
	assert.Equal(t, []string{"2026-05-01", "2026-07-20"}, logicalDays(res))
	require.Len(t, res.Items, 2)
	require.NotNil(t, res.Items[0].Link, "a subject browse carries the link context")
	assert.Equal(t, "day", res.Items[0].Link.Subject.Kind)
	assert.Equal(t, "2026-07-05", res.Items[0].Link.Subject.Key)

	// Subject + window combine: the window drops the out-of-range linked media.
	res, err = r.Gallery(GalleryRequest{SubjectKind: "day", SubjectKey: "2026-07-05", Until: "2026-06-01"})
	require.NoError(t, err)
	assert.Equal(t, []string{"2026-05-01"}, logicalDays(res))

	// Unknown subject is a refusal, not an honest empty.
	_, err = r.Gallery(GalleryRequest{SubjectKind: "injury", SubjectKey: "injury_absent"})
	require.Error(t, err)
}

// TestGallery_NinetyDayWindowOrdered exercises the motivating before/after case:
// ~90 synthetic days, one media each, inserted in reverse, queried across the
// full window and returned in chronological order (AC-13).
func TestGallery_NinetyDayWindowOrdered(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	const numDays = 90

	// Insert newest-first so only the sort can produce ascending output.
	for i := numDays - 1; i >= 0; i-- {
		d := base.AddDate(0, 0, i)
		day := d.Format(time.DateOnly)
		writeGalleryMedia(t, a, "day-"+day, day, d)
	}

	first := base.Format(time.DateOnly)
	last := base.AddDate(0, 0, numDays-1).Format(time.DateOnly)

	res, err := r.Gallery(GalleryRequest{Since: first, Until: last})
	require.NoError(t, err)
	require.True(t, res.Found)
	require.Len(t, res.Items, numDays)

	assert.Equal(t, first, res.Items[0].Media.LogicalDay)
	assert.Equal(t, last, res.Items[len(res.Items)-1].Media.LogicalDay)
	for i := 1; i < len(res.Items); i++ {
		assert.Less(t, res.Items[i-1].Media.LogicalDay, res.Items[i].Media.LogicalDay,
			"the 90-day timeline is strictly ascending")
	}
}

// TestGallery_EmptyStoreHonest proves an empty store is an honest empty result —
// Found false, no items, no error (AC-12: a missing store never errors).
func TestGallery_EmptyStoreHonest(t *testing.T) {
	r, _, _ := newBootedRouter(t)

	res, err := r.Gallery(GalleryRequest{})
	require.NoError(t, err)
	assert.False(t, res.Found)
	assert.Empty(t, res.Items)
}
