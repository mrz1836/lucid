package router

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/storage"
)

// gallery.go is the read-only media-timeline seam (mvp/life-archive.md §7) — the
// media sibling of recall. It projects the immutable media store and the
// append-only link ledger into a single date-ordered timeline for before/after
// progress-photo comparison. Like recall it is a pure read: it reads only
// through the storage adapter's projection seams (ListMedia for the whole store,
// LinkedMediaForSubject for one subject) and writes nothing — no scaffold, no
// sidecar, no ledger line — so the media tree and the link ledger are
// byte-identical before and after a run (the sanctuary boundary, data-model.md
// §"Immutability"). No model runs in any path; a thin or missing store degrades
// to an honest empty result rather than an error.

// GalleryRequest is the read-only timeline selector (mvp/life-archive.md §7).
// Since/Until bound the window inclusively against each record's logical_day
// (empty on a side ⇒ unbounded there); SubjectKind/SubjectKey, when both set,
// narrow the timeline to the media currently linked to that one subject. Now is
// carried for signature stability and a possible future as-of filter; the
// timeline is otherwise time-independent (mirroring RecallRequest.Now), so it is
// currently unread.
type GalleryRequest struct {
	Since       string
	Until       string
	SubjectKind string
	SubjectKey  string
	Now         time.Time
}

// GalleryItem is one media on the timeline: its sidecar record (with StoredPath
// set) and, on the subject-filtered path, the live link that names why it is
// here (nil on the whole-store path). Pairing the link keeps a subject browse
// from rendering a caption without the association behind it.
type GalleryItem struct {
	Media storage.MediaRecord
	Link  *storage.LinkView
}

// GalleryResult is the read-only timeline: the items in ascending logical_day
// order and Found (false for an empty result — an honest empty, never a
// fabricated one, no model spent over it).
type GalleryResult struct {
	Found bool
	Items []GalleryItem
}

// Gallery projects the media store into a date-ordered timeline
// (mvp/life-archive.md §7). With no subject it reads the whole store
// (ListMedia); with a subject it resolves that subject up front — refusing an
// unknown or absent one by its native error, mirroring the link verbs'
// resolve-before-read order — and reads only the media linked to it
// (LinkedMediaForSubject, which skips a dangling link rather than failing). The
// selected set is filtered to the inclusive [Since, Until] window on logical_day
// and sorted ascending by (logical_day, captured_at, id). It reads only through
// the storage projection seams and writes nothing.
func (r *Router) Gallery(req GalleryRequest) (GalleryResult, error) {
	items, err := r.galleryItems(req.SubjectKind, req.SubjectKey)
	if err != nil {
		return GalleryResult{}, err
	}

	since := strings.TrimSpace(req.Since)
	until := strings.TrimSpace(req.Until)
	kept := make([]GalleryItem, 0, len(items))
	for _, it := range items {
		if withinWindow(it.Media.LogicalDay, since, until) {
			kept = append(kept, it)
		}
	}

	slices.SortFunc(kept, func(a, b GalleryItem) int {
		return cmp.Or(
			cmp.Compare(a.Media.LogicalDay, b.Media.LogicalDay),
			cmp.Compare(a.Media.CapturedAt, b.Media.CapturedAt),
			cmp.Compare(a.Media.ID, b.Media.ID),
		)
	})

	return GalleryResult{Found: len(kept) > 0, Items: kept}, nil
}

// galleryItems gathers the unfiltered media set the request selects: the whole
// store when no subject is given, or one subject's linked media otherwise. Both
// paths are pure reads through the storage adapter, and both return an honest
// empty set when their store is absent.
func (r *Router) galleryItems(kind, key string) ([]GalleryItem, error) {
	kind = strings.TrimSpace(kind)
	key = strings.TrimSpace(key)
	if kind == "" && key == "" {
		return r.galleryWholeStore()
	}
	return r.gallerySubject(kind, key)
}

// galleryWholeStore reads every stored media record (ListMedia) as an unlinked
// timeline item — the no-subject path. A missing store yields no items and no
// error.
func (r *Router) galleryWholeStore() ([]GalleryItem, error) {
	recs, err := r.store.ListMedia()
	if err != nil {
		return nil, err
	}
	items := make([]GalleryItem, 0, len(recs))
	for _, rec := range recs {
		items = append(items, GalleryItem{Media: rec})
	}
	return items, nil
}

// gallerySubject resolves the subject (an unknown kind or absent key is refused
// by its native error, before any read) and returns the media currently linked
// to it, each carrying its live link context. A link whose media binary has
// since disappeared is skipped by LinkedMediaForSubject, so a dangling link
// degrades one row rather than breaking the timeline.
func (r *Router) gallerySubject(kind, key string) ([]GalleryItem, error) {
	if err := r.store.ResolveSubject(kind, key); err != nil {
		return nil, err
	}
	linked, _, err := r.store.LinkedMediaForSubject(kind, key)
	if err != nil {
		return nil, err
	}
	items := make([]GalleryItem, 0, len(linked))
	for _, lm := range linked {
		link := lm.Link
		items = append(items, GalleryItem{Media: lm.Media, Link: &link})
	}
	return items, nil
}

// withinWindow reports whether a logical day falls inside the inclusive
// [since, until] window; an empty bound is unbounded on that side. The day and
// both bounds are zero-padded YYYY-MM-DD, so a lexical compare is chronological.
func withinWindow(day, since, until string) bool {
	if since != "" && day < since {
		return false
	}
	if until != "" && day > until {
		return false
	}
	return true
}
