package router

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/storage"
)

// TestLinkContext_BothDirections threads the seam end to end: a media linked to
// a subject through the real Link verb surfaces from the subject (SubjectMedia,
// paired with its record) and from the media (MediaSubjects) alike, so the two
// folds agree.
func TestLinkContext_BothDirections(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	mediaID := seedLinkMedia(t, a)

	_, err := r.Link(LinkRequest{
		Media: mediaID, Subjects: []string{"day:2026-07-05"},
		Op: storage.LinkOpLink, Now: fixedNow(), Source: "cli",
	})
	require.NoError(t, err)

	// Subject → media: the paired record carries the id and the stored path.
	media, err := r.SubjectMedia("day", "2026-07-05")
	require.NoError(t, err)
	require.Len(t, media, 1)
	assert.Equal(t, mediaID, media[0].Media.ID)
	assert.NotEmpty(t, media[0].Media.StoredPath, "the paired record carries the stored path")
	assert.Equal(t, "day", media[0].Link.Subject.Kind)
	assert.Equal(t, "2026-07-05", media[0].Link.Subject.Key)

	// Media → subject: the reverse read names the same pair.
	subjects, err := r.MediaSubjects(mediaID)
	require.NoError(t, err)
	require.Len(t, subjects, 1)
	assert.Equal(t, "day", subjects[0].Subject.Kind)
	assert.Equal(t, "2026-07-05", subjects[0].Subject.Key)
}

// TestLinkContext_UnlinkedIsEmptyNonNil proves an unlinked subject and an
// unlinked media each degrade to an empty, non-nil slice — so a caller can range
// over the result without a nil special case.
func TestLinkContext_UnlinkedIsEmptyNonNil(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	mediaID := seedLinkMedia(t, a)

	media, err := r.SubjectMedia("day", "2026-07-05")
	require.NoError(t, err)
	require.NotNil(t, media, "an unlinked subject returns a non-nil slice")
	assert.Empty(t, media)

	subjects, err := r.MediaSubjects(mediaID)
	require.NoError(t, err)
	require.NotNil(t, subjects, "an unlinked media returns a non-nil slice")
	assert.Empty(t, subjects)
}

// TestLinkContext_DanglingLinkSkippedAndCounted proves a link whose media binary
// has since disappeared is skipped from the surface and counted by the storage
// read that backs the seam — the same tolerance the observation reader applies
// to a malformed line, so a hand-deleted binary degrades one row rather than
// breaking the surface.
func TestLinkContext_DanglingLinkSkippedAndCounted(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	mediaID := seedLinkMedia(t, a)

	// A real, resolvable link.
	_, err := r.Link(LinkRequest{
		Media: mediaID, Subjects: []string{"day:2026-07-05"},
		Op: storage.LinkOpLink, Now: fixedNow(), Source: "cli",
	})
	require.NoError(t, err)

	// A live link to a media id with no sidecar — a dangling pointer. It is
	// appended directly, because the Link verb refuses an unknown media up front.
	_, err = a.AppendLinkEvent(storage.LinkEvent{
		MediaID: "2026-07-05-vanished.jpg",
		Subject: storage.LinkSubject{Kind: "day", Key: "2026-07-05"},
		Op:      storage.LinkOpLink,
		At:      fixedNow().Format(time.RFC3339),
		Source:  "cli",
	})
	require.NoError(t, err)

	// The seam returns only the real link, no error.
	media, err := r.SubjectMedia("day", "2026-07-05")
	require.NoError(t, err)
	require.Len(t, media, 1)
	assert.Equal(t, mediaID, media[0].Media.ID)

	// The storage read that backs the seam counts the skip.
	paired, skipped, err := a.LinkedMediaForSubject("day", "2026-07-05")
	require.NoError(t, err)
	assert.Len(t, paired, 1)
	assert.Equal(t, 1, skipped, "the dangling link is counted, not surfaced")
}
