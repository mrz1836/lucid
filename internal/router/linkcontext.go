package router

import (
	"github.com/mrz1836/lucid/internal/storage"
)

// linkcontext.go is the read-only link projection seam: one generic pair of
// reads every subject surface consumes instead of touching the link ledger
// directly (data-model.md §"Link ledger", the fold). Because the fold is keyed
// on an opaque (kind, key), the seam is generic — no kind is structurally
// excluded, and a surface for a new kind adopts it without new plumbing. Both
// reads go through the storage adapter (the only code that touches ~/.lucid/),
// and both are pure reads: nothing is written, and there is no time argument,
// because a current link is current by definition (mirroring
// [Router.InjuryContext]).

// SubjectMedia returns the media currently linked to one subject, folded from
// the append-only link ledger and paired with each media record — the caption,
// the stored path, and when/why it was linked, in one value. A link whose media
// sidecar has since disappeared is skipped (see
// [storage.Adapter.LinkedMediaForSubject]), so a hand-deleted binary degrades
// one row rather than breaking the surface. An unlinked subject yields an empty,
// non-nil slice, so a caller can range over the result without a nil special
// case. It is the stable seam every subject surface consumes instead of reading
// ledger state.
func (r *Router) SubjectMedia(kind, key string) ([]storage.LinkedMedia, error) {
	media, _, err := r.store.LinkedMediaForSubject(kind, key)
	if err != nil {
		return nil, err
	}
	if media == nil {
		media = []storage.LinkedMedia{}
	}
	return media, nil
}

// MediaSubjects returns the subjects one media is currently linked to, folded
// the same way and in the same byte-stable order — the reverse read, so a media
// surface can name everything an attachment is about. An unlinked media yields
// an empty, non-nil slice.
func (r *Router) MediaSubjects(mediaID string) ([]storage.LinkView, error) {
	subjects, err := r.store.LinksForMedia(mediaID)
	if err != nil {
		return nil, err
	}
	if subjects == nil {
		subjects = []storage.LinkView{}
	}
	return subjects, nil
}
