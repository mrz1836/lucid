package router

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/storage"
)

// seedLinkMedia writes one synthetic media through storage and returns its
// stored id — the media a link test points a subject at. No real content, no
// real subject.
func seedLinkMedia(t *testing.T, a *storage.Adapter) string {
	t.Helper()
	rec, err := a.WriteMedia(storage.MediaAttachment{
		Bytes:            []byte("\xff\xd8\xff synthetic jpeg bytes"),
		OriginalFilename: "photo.jpg",
		CapturedAt:       fixedNow(),
		LogicalDay:       "2026-07-05",
		Source:           "cli",
		RawEntryID:       "raw_2026_07_05_18_41",
	})
	require.NoError(t, err)
	return rec.ID
}

// linkEventCount returns how many link events the log holds — the cheapest
// "did anything get appended?" check.
func linkEventCount(t *testing.T, a *storage.Adapter) int {
	t.Helper()
	events, skipped, err := a.ReadLinkEvents()
	require.NoError(t, err)
	require.Zero(t, skipped, "no link line should be malformed in a test")
	return len(events)
}

// linksFileBytes reads the raw links.jsonl bytes (or nil when the log does not
// exist yet) so a test can assert a refused turn left the ledger untouched.
func linksFileBytes(t *testing.T, home string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, "links", "links.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	return b
}

// TestParseSubjectRef covers the --to grammar: a first-colon-only split (a key
// may itself contain colons), and every malformed shape refused by name.
func TestParseSubjectRef(t *testing.T) {
	t.Run("splits on the first colon only", func(t *testing.T) {
		kind, key, err := ParseSubjectRef("observation:obs_2026_08_07_001")
		require.NoError(t, err)
		assert.Equal(t, "observation", kind)
		assert.Equal(t, "obs_2026_08_07_001", key)

		// A key that itself contains colons keeps every colon after the first.
		kind, key, err = ParseSubjectRef("memory:obs:2026:001")
		require.NoError(t, err)
		assert.Equal(t, "memory", kind)
		assert.Equal(t, "obs:2026:001", key)
	})

	t.Run("trims surrounding whitespace", func(t *testing.T) {
		kind, key, err := ParseSubjectRef("  person:person_abc  ")
		require.NoError(t, err)
		assert.Equal(t, "person", kind)
		assert.Equal(t, "person_abc", key)
	})

	t.Run("refuses malformed tokens", func(t *testing.T) {
		for _, tok := range []string{
			"no-colon",  // no separator
			":key",      // empty kind
			"person:",   // empty key
			"Person:x",  // uppercase kind
			"1bad:x",    // kind starts with a digit
			"pe-rson:x", // hyphen is not a legal kind character
			"_kind:x",   // kind starts with an underscore
		} {
			_, _, err := ParseSubjectRef(tok)
			require.Errorf(t, err, "token %q must be refused", tok)
		}
	})
}

// TestLink_NoOpPathsAppendNothing: re-linking a live pair and unlinking a pair
// that is not live both succeed with a no-op ack and append nothing (D4) — the
// growth-friendly semantics SC-4 requires.
func TestLink_NoOpPathsAppendNothing(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	mediaID := seedLinkMedia(t, a)

	// First link is real.
	first, err := r.Link(LinkRequest{Media: mediaID, Subjects: []string{"day:2020-01-01"}, Op: storage.LinkOpLink, Now: fixedNow()})
	require.NoError(t, err)
	require.Len(t, first.Applied, 1)
	assert.False(t, first.Applied[0].NoOp)
	assert.Equal(t, "link_1", first.Applied[0].EventID)
	assert.Equal(t, 1, linkEventCount(t, a))

	// Re-linking the live pair is a no-op ack, exit 0, nothing appended.
	again, err := r.Link(LinkRequest{Media: mediaID, Subjects: []string{"day:2020-01-01"}, Op: storage.LinkOpLink, Now: fixedNow()})
	require.NoError(t, err)
	require.Len(t, again.Applied, 1)
	assert.True(t, again.Applied[0].NoOp)
	assert.Empty(t, again.Applied[0].EventID)
	assert.Contains(t, again.Ack, "already linked")
	assert.Equal(t, 1, linkEventCount(t, a), "a re-link of a live pair appends nothing")

	// Unlinking a pair that is not live is likewise a no-op ack.
	noop, err := r.Link(LinkRequest{Media: mediaID, Subjects: []string{"day:2019-01-01"}, Op: storage.LinkOpUnlink, Now: fixedNow()})
	require.NoError(t, err)
	require.Len(t, noop.Applied, 1)
	assert.True(t, noop.Applied[0].NoOp)
	assert.Contains(t, noop.Ack, "was not linked")
	assert.Equal(t, 1, linkEventCount(t, a), "an unlink of a pair that is not live appends nothing")

	// A real unlink of the live pair does append, and a re-unlink is then a no-op.
	unl, err := r.Link(LinkRequest{Media: mediaID, Subjects: []string{"day:2020-01-01"}, Op: storage.LinkOpUnlink, Now: fixedNow()})
	require.NoError(t, err)
	assert.False(t, unl.Applied[0].NoOp)
	assert.Equal(t, 2, linkEventCount(t, a))
}

// TestLink_ManyToManyAndReverse: one media links to several subjects, and both
// fold directions agree — the association layer SC-4/SC-8 promise.
func TestLink_ManyToManyAndReverse(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	mediaID := seedLinkMedia(t, a)

	res, err := r.Link(LinkRequest{
		Media:    mediaID,
		Subjects: []string{"day:2020-01-01", "day:2020-02-02", "day:2020-03-03"},
		Op:       storage.LinkOpLink,
		Now:      fixedNow(),
	})
	require.NoError(t, err)
	require.Len(t, res.Applied, 3)
	for _, ap := range res.Applied {
		assert.False(t, ap.NoOp)
		assert.NotEmpty(t, ap.EventID)
	}

	subs, err := a.LinksForMedia(mediaID)
	require.NoError(t, err)
	assert.Len(t, subs, 3, "the media lists all three linked subjects")
}

// TestLink_AnnotateAlwaysAppends: an annotate appends even against a pair that
// is not currently linked, and does not make it live (D1).
func TestLink_AnnotateAlwaysAppends(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	mediaID := seedLinkMedia(t, a)

	res, err := r.Link(LinkRequest{
		Media:    mediaID,
		Subjects: []string{"day:2020-01-01"},
		Op:       storage.LinkOpAnnotate,
		Note:     "a synthetic note",
		Now:      fixedNow(),
	})
	require.NoError(t, err)
	require.Len(t, res.Applied, 1)
	assert.False(t, res.Applied[0].NoOp, "an annotate always appends")
	assert.Equal(t, 1, linkEventCount(t, a))

	// The pair was never linked, so no live subject surfaces.
	subs, err := a.LinksForMedia(mediaID)
	require.NoError(t, err)
	assert.Empty(t, subs, "an annotate does not make a pair live")
}

// TestLink_MultiSubjectOneBadWritesNothing: a turn where one subject is refused
// refuses the whole turn and appends nothing — no silent half-write (SC-4).
func TestLink_MultiSubjectOneBadWritesNothing(t *testing.T) {
	r, a, home := newBootedRouter(t)
	mediaID := seedLinkMedia(t, a)

	before := linksFileBytes(t, home)

	_, err := r.Link(LinkRequest{
		Media:    mediaID,
		Subjects: []string{"day:2020-01-01", "day:2999-01-01"}, // second is in the future → refused
		Op:       storage.LinkOpLink,
		Now:      fixedNow(),
	})
	require.Error(t, err)
	assert.Equal(t, before, linksFileBytes(t, home), "a refused multi-subject turn leaves the ledger byte-identical")
	assert.Zero(t, linkEventCount(t, a))

	// An unknown kind is refused the same way.
	_, err = r.Link(LinkRequest{Media: mediaID, Subjects: []string{"bogus:x"}, Op: storage.LinkOpLink, Now: fixedNow()})
	require.Error(t, err)
	assert.Zero(t, linkEventCount(t, a))
}

// TestLink_UnknownMediaRefused: an id that names no stored media is refused
// before any subject is touched, and nothing is appended.
func TestLink_UnknownMediaRefused(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	_, err := r.Link(LinkRequest{Media: "2020-01-01-nope.jpg", Subjects: []string{"day:2020-01-01"}, Op: storage.LinkOpLink, Now: fixedNow()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no media is stored under")
	assert.Zero(t, linkEventCount(t, a))
}

// TestLink_AcceptsSidecarPath: a media reference given as the sidecar path
// (<id>.json) resolves to the stored id, so a stored path pasted from a listing
// works (D3).
func TestLink_AcceptsSidecarPath(t *testing.T) {
	r, a, home := newBootedRouter(t)
	mediaID := seedLinkMedia(t, a)
	sidecar := filepath.Join(home, "media", "2026", "07", mediaID+".json")

	res, err := r.Link(LinkRequest{Media: sidecar, Subjects: []string{"day:2020-01-01"}, Op: storage.LinkOpLink, Now: fixedNow()})
	require.NoError(t, err)
	assert.Equal(t, mediaID, res.MediaID, "a sidecar path reduces to the stored id")
	assert.False(t, res.Applied[0].NoOp)
}

// TestLink_NoSubjectsRefused: a turn with no --to subject is a clean refusal,
// nothing written.
func TestLink_NoSubjectsRefused(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	mediaID := seedLinkMedia(t, a)

	_, err := r.Link(LinkRequest{Media: mediaID, Op: storage.LinkOpLink, Now: fixedNow()})
	require.Error(t, err)
	assert.Zero(t, linkEventCount(t, a))
}
