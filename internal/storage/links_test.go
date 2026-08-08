package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// syntheticLinkEvent builds a well-formed link event with synthetic ids
// (no real media, subject, or content) stamped at [fixedTime]. It mirrors
// syntheticMedia / syntheticRaw: a deterministic fixture the link tests vary
// from. Tests that need a different op set it on the returned value.
func syntheticLinkEvent(mediaID, kind, key string) LinkEvent {
	return LinkEvent{
		MediaID: mediaID,
		Subject: LinkSubject{Kind: kind, Key: key},
		Op:      LinkOpLink,
		At:      fixedTime().Format(time.RFC3339),
		Source:  "cli",
	}
}

// linksLogPath returns the on-disk log path a test seeds or inspects directly.
func linksLogPath(home string) string {
	return filepath.Join(home, "links", "links.jsonl")
}

// containsPair reports whether views carries the (media, subject) pair.
func containsPair(views []LinkView, mediaID string, subj LinkSubject) bool {
	for _, v := range views {
		if v.MediaID == mediaID && v.Subject == subj {
			return true
		}
	}
	return false
}

// --- Task 15: types, scaffold, append, read, id discipline ---

func TestScaffoldLinks_LazyAndIdempotent(t *testing.T) {
	home := t.TempDir()
	a := New(home)
	require.NoError(t, a.ScaffoldLinks())
	require.NoError(t, a.ScaffoldLinks()) // idempotent
	info, err := os.Stat(filepath.Join(home, "links"))
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestAppendLinkEvent_RoundTrip(t *testing.T) {
	a := New(t.TempDir())
	ev, err := a.AppendLinkEvent(syntheticLinkEvent("2026-07-05-sunset.jpg", "person", "person_abc"))
	require.NoError(t, err)
	assert.Equal(t, "link_1", ev.ID)
	assert.Equal(t, LinkSchema, ev.Schema)

	events, skipped, err := a.ReadLinkEvents()
	require.NoError(t, err)
	assert.Equal(t, 0, skipped)
	require.Len(t, events, 1)
	assert.Equal(t, ev, events[0])
}

// TestAppendLinkEvent_IDsAreMaxSeqPlusOne: the next id is parsed max+1, never a
// line count — a gap and a malformed line neither reuse an id nor shift one.
func TestAppendLinkEvent_IDsAreMaxSeqPlusOne(t *testing.T) {
	home := t.TempDir()
	a := New(home)
	path := linksLogPath(home)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	seed := `{"schema":"link.v1","id":"link_1","media_id":"m","subject":{"kind":"person","key":"k"},"op":"link","at":"2026-07-05T18:41:39Z","source":"cli"}
{ this line is not valid json
{"schema":"link.v1","id":"link_5","media_id":"m","subject":{"kind":"person","key":"k"},"op":"unlink","at":"2026-07-05T18:41:39Z","source":"cli"}
`
	require.NoError(t, os.WriteFile(path, []byte(seed), 0o600))

	ev, err := a.AppendLinkEvent(syntheticLinkEvent("m2", "injury", "inj_1"))
	require.NoError(t, err)
	assert.Equal(t, "link_6", ev.ID, "next id is parsed max (5) + 1, skipping the gap and the garbage line")
}

// TestReadLinkEvents_MissingFileIsEmptyAndWritesNothing: a read never creates
// the tree.
func TestReadLinkEvents_MissingFileIsEmptyAndWritesNothing(t *testing.T) {
	home := t.TempDir()
	a := New(home)
	events, skipped, err := a.ReadLinkEvents()
	require.NoError(t, err)
	assert.Empty(t, events)
	assert.Equal(t, 0, skipped)
	_, statErr := os.Stat(filepath.Join(home, "links"))
	assert.True(t, os.IsNotExist(statErr), "a read must not create links/")
}

// TestReadLinkEvents_SkipsMalformedAndCounts mirrors the observations bad-line
// discipline: a malformed line is skipped and counted, the good ones survive.
func TestReadLinkEvents_SkipsMalformedAndCounts(t *testing.T) {
	home := t.TempDir()
	a := New(home)
	path := linksLogPath(home)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	seed := `{"schema":"link.v1","id":"link_1","media_id":"m","subject":{"kind":"person","key":"k"},"op":"link","at":"2026-07-05T18:41:39Z","source":"cli"}
{ broken
{"schema":"link.v1","id":"link_2","media_id":"m2","subject":{"kind":"era","key":"e"},"op":"link","at":"2026-07-05T18:41:39Z","source":"cli"}
`
	require.NoError(t, os.WriteFile(path, []byte(seed), 0o600))
	events, skipped, err := a.ReadLinkEvents()
	require.NoError(t, err)
	assert.Equal(t, 1, skipped)
	assert.Len(t, events, 2)
}

// TestAppendLinkEvent_AcceptsMediaOfAnyAge (AC-6): a years-old media may be
// linked today; the event records its own at with no recency constraint, and
// the media's age never touches it.
func TestAppendLinkEvent_AcceptsMediaOfAnyAge(t *testing.T) {
	a := New(t.TempDir())
	linkedAt := "2026-08-08T09:00:00-04:00"
	ev := LinkEvent{
		MediaID: "2019-03-02-old-scan.jpg", // captured years ago
		Subject: LinkSubject{Kind: "person", Key: "person_abc"},
		Op:      LinkOpLink,
		At:      linkedAt,
		Source:  "cli",
	}
	got, err := a.AppendLinkEvent(ev)
	require.NoError(t, err)
	assert.Equal(t, linkedAt, got.At, "the link records its own at, not the media's age")

	views, err := a.LinksForMedia("2019-03-02-old-scan.jpg")
	require.NoError(t, err)
	require.Len(t, views, 1)
	assert.Equal(t, linkedAt, views[0].LinkedAt)
}

// TestAppendLinkEvent_Refusals: every structural refusal fires, and a refused
// append leaves the ledger empty (validation runs before the write).
func TestAppendLinkEvent_Refusals(t *testing.T) {
	a := New(t.TempDir())
	base := syntheticLinkEvent("m", "person", "k")

	cases := map[string]func(LinkEvent) LinkEvent{
		"missing media_id": func(e LinkEvent) LinkEvent { e.MediaID = ""; return e },
		"missing kind":     func(e LinkEvent) LinkEvent { e.Subject.Kind = ""; return e },
		"missing key":      func(e LinkEvent) LinkEvent { e.Subject.Key = ""; return e },
		"unknown op":       func(e LinkEvent) LinkEvent { e.Op = "frobnicate"; return e },
		"unparseable at":   func(e LinkEvent) LinkEvent { e.At = "not-a-time"; return e },
		"missing source":   func(e LinkEvent) LinkEvent { e.Source = ""; return e },
		"note on link":     func(e LinkEvent) LinkEvent { e.Note = "hi"; return e },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := a.AppendLinkEvent(mut(base))
			require.Error(t, err)
		})
	}

	events, _, err := a.ReadLinkEvents()
	require.NoError(t, err)
	assert.Empty(t, events, "a refused append writes nothing")
}

// TestLinkEventLocators_ListAndReadErr (AC-19 storage half): the listing and the
// per-record read agree on locators; a malformed line is nameable and errors;
// the read never rewrites the file; an absent tree is clean.
func TestLinkEventLocators_ListAndReadErr(t *testing.T) {
	home := t.TempDir()
	a := New(home)
	_, err := a.AppendLinkEvent(syntheticLinkEvent("m1", "person", "k1"))
	require.NoError(t, err)
	_, err = a.AppendLinkEvent(syntheticLinkEvent("m2", "injury", "k2"))
	require.NoError(t, err)

	ids, err := a.ListLinkEventIDs()
	require.NoError(t, err)
	assert.Equal(t, []string{"link_1", "link_2"}, ids)
	for _, id := range ids {
		require.NoError(t, a.ReadLinkEventErr(id))
	}

	// Append a malformed line by hand.
	path := linksLogPath(home)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, werr := f.WriteString("{ not valid json\n")
	require.NoError(t, f.Close())
	require.NoError(t, werr)

	ids, err = a.ListLinkEventIDs()
	require.NoError(t, err)
	require.Len(t, ids, 3)
	assert.Equal(t, "line-3", ids[2], "a malformed line is nameable by position")
	require.Error(t, a.ReadLinkEventErr("line-3"))

	before, err := os.ReadFile(path)
	require.NoError(t, err)
	_ = a.ReadLinkEventErr("line-3")
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after, "the schema read never rewrites the log")

	empty := New(t.TempDir())
	ids, err = empty.ListLinkEventIDs()
	require.NoError(t, err)
	assert.Empty(t, ids, "an absent links/ tree lists nothing and is clean")
}

// --- Task 16: fold both directions, ReadMediaByID ---

// TestLinks_ManyToMany (AC-7): one media links many subjects, one subject
// carries many media, and both folds agree on a shared pair.
func TestLinks_ManyToMany(t *testing.T) {
	a := New(t.TempDir())
	at := fixedTime().Format(time.RFC3339)
	link := func(mediaID string, subj LinkSubject) {
		_, err := a.AppendLinkEvent(LinkEvent{MediaID: mediaID, Subject: subj, Op: LinkOpLink, At: at, Source: "cli"})
		require.NoError(t, err)
	}

	// One media → three subjects.
	group := "2026-07-05-group.jpg"
	for _, subj := range []LinkSubject{
		{Kind: "person", Key: "person_a"},
		{Kind: "injury", Key: "inj_1"},
		{Kind: "anchor", Key: "anchor_2026_07_05_a"},
	} {
		link(group, subj)
	}
	subjects, err := a.LinksForMedia(group)
	require.NoError(t, err)
	require.Len(t, subjects, 3)

	// One subject → three media.
	shared := LinkSubject{Kind: "person", Key: "person_z"}
	for _, mid := range []string{"2026-07-01-a.jpg", "2026-07-02-b.jpg", "2026-07-03-c.jpg"} {
		link(mid, shared)
	}
	media, err := a.LinksForSubject("person", "person_z")
	require.NoError(t, err)
	require.Len(t, media, 3)

	// A shared pair appears identically from both directions.
	link(group, shared)
	fromMedia, err := a.LinksForMedia(group)
	require.NoError(t, err)
	fromSubject, err := a.LinksForSubject("person", "person_z")
	require.NoError(t, err)
	assert.True(t, containsPair(fromMedia, group, shared))
	assert.True(t, containsPair(fromSubject, group, shared))
}

// TestLinksForSubjectAndMedia_FoldBothDirectionsAgree exercises the two reads
// under the LinksFor* checkpoint filter: an unlinked subject is an empty (not
// nil-crashing) slice, and output is stable.
func TestLinksForSubjectAndMedia_FoldBothDirectionsAgree(t *testing.T) {
	a := New(t.TempDir())
	at := fixedTime().Format(time.RFC3339)
	_, err := a.AppendLinkEvent(LinkEvent{MediaID: "2026-07-05-x.jpg", Subject: LinkSubject{Kind: "place", Key: "place_1"}, Op: LinkOpLink, At: at, Source: "cli"})
	require.NoError(t, err)

	fromSubject, err := a.LinksForSubject("place", "place_1")
	require.NoError(t, err)
	require.Len(t, fromSubject, 1)
	assert.Equal(t, "2026-07-05-x.jpg", fromSubject[0].MediaID)

	fromMedia, err := a.LinksForMedia("2026-07-05-x.jpg")
	require.NoError(t, err)
	require.Len(t, fromMedia, 1)
	assert.Equal(t, LinkSubject{Kind: "place", Key: "place_1"}, fromMedia[0].Subject)

	empty, err := a.LinksForSubject("place", "place_none")
	require.NoError(t, err)
	assert.Empty(t, empty)
}

// TestLinksFor_UnlinkRemovesBothDirectionsAndRelinkRestores: unlink hides a pair
// from both folds; a later link brings it back.
func TestLinksFor_UnlinkRemovesBothDirectionsAndRelinkRestores(t *testing.T) {
	a := New(t.TempDir())
	m := "2026-07-05-x.jpg"
	subj := LinkSubject{Kind: "place", Key: "place_1"}
	at := fixedTime().Format(time.RFC3339)
	mk := func(op LinkOp) LinkEvent { return LinkEvent{MediaID: m, Subject: subj, Op: op, At: at, Source: "cli"} }

	_, err := a.AppendLinkEvent(mk(LinkOpLink))
	require.NoError(t, err)
	fm, _ := a.LinksForMedia(m)
	fs, _ := a.LinksForSubject("place", "place_1")
	require.Len(t, fm, 1)
	require.Len(t, fs, 1)

	_, err = a.AppendLinkEvent(mk(LinkOpUnlink))
	require.NoError(t, err)
	fm, _ = a.LinksForMedia(m)
	fs, _ = a.LinksForSubject("place", "place_1")
	assert.Empty(t, fm)
	assert.Empty(t, fs)

	_, err = a.AppendLinkEvent(mk(LinkOpLink))
	require.NoError(t, err)
	fm, _ = a.LinksForMedia(m)
	fs, _ = a.LinksForSubject("place", "place_1")
	assert.Len(t, fm, 1)
	assert.Len(t, fs, 1)
}

// TestLinksFor_AnnotateOnNonLivePairIsRecordedWithoutMakingItLive: an
// annotate never resurrects an unlinked pair, and an annotate-only pair is never
// live.
func TestLinksFor_AnnotateOnNonLivePairIsRecordedWithoutMakingItLive(t *testing.T) {
	a := New(t.TempDir())
	m := "2026-07-05-y.jpg"
	subj := LinkSubject{Kind: "era", Key: "era_1"}
	at := fixedTime().Format(time.RFC3339)
	mk := func(op LinkOp, note string) LinkEvent {
		return LinkEvent{MediaID: m, Subject: subj, Op: op, At: at, Source: "cli", Note: note}
	}
	for _, ev := range []LinkEvent{mk(LinkOpLink, ""), mk(LinkOpUnlink, ""), mk(LinkOpAnnotate, "seen later")} {
		_, err := a.AppendLinkEvent(ev)
		require.NoError(t, err)
	}
	fm, err := a.LinksForMedia(m)
	require.NoError(t, err)
	assert.Empty(t, fm, "annotate on an unlinked pair must not make it live")

	// Annotate-only (never linked) is likewise not live.
	a2 := New(t.TempDir())
	_, err = a2.AppendLinkEvent(LinkEvent{MediaID: "2026-01-01-z.jpg", Subject: LinkSubject{Kind: "thread", Key: "th_1"}, Op: LinkOpAnnotate, At: at, Source: "cli", Note: "note"})
	require.NoError(t, err)
	fm2, err := a2.LinksForMedia("2026-01-01-z.jpg")
	require.NoError(t, err)
	assert.Empty(t, fm2)
}

// TestLinksFor_AnnotateOnLivePairSurfacesNote: a live pair carries its folded
// annotations.
func TestLinksFor_AnnotateOnLivePairSurfacesNote(t *testing.T) {
	a := New(t.TempDir())
	m := "2026-07-05-w.jpg"
	subj := LinkSubject{Kind: "self", Key: "self_2026_07_05_a"}
	at := fixedTime().Format(time.RFC3339)
	_, err := a.AppendLinkEvent(LinkEvent{MediaID: m, Subject: subj, Op: LinkOpLink, At: at, Source: "cli"})
	require.NoError(t, err)
	_, err = a.AppendLinkEvent(LinkEvent{MediaID: m, Subject: subj, Op: LinkOpAnnotate, At: at, Source: "harness", Note: "context"})
	require.NoError(t, err)

	fm, err := a.LinksForMedia(m)
	require.NoError(t, err)
	require.Len(t, fm, 1)
	require.Len(t, fm[0].Notes, 1)
	assert.Equal(t, "context", fm[0].Notes[0].Text)
	assert.Equal(t, "harness", fm[0].Notes[0].Source)
}

// TestLink_LeavesMediaBytesAndSidecarUnchanged (AC-8): no link operation ever
// touches the immutable media binary or its sidecar.
func TestLink_LeavesMediaBytesAndSidecarUnchanged(t *testing.T) {
	a := New(t.TempDir())
	content := []byte("\xff\xd8\xff\xe0opaque bytes") // not real image data
	rec, err := a.WriteMedia(syntheticMedia("a caption", "photo.jpg", content))
	require.NoError(t, err)

	binBefore, err := os.ReadFile(rec.StoredPath)
	require.NoError(t, err)
	sidecarBefore, err := os.ReadFile(rec.StoredPath + ".json")
	require.NoError(t, err)

	at := fixedTime().Format(time.RFC3339)
	subj := LinkSubject{Kind: "person", Key: "person_a"}
	steps := []struct {
		op   LinkOp
		note string
	}{
		{LinkOpLink, ""},
		{LinkOpAnnotate, "note"},
		{LinkOpUnlink, ""},
	}
	for _, s := range steps {
		_, appendErr := a.AppendLinkEvent(LinkEvent{MediaID: rec.ID, Subject: subj, Op: s.op, At: at, Source: "cli", Note: s.note})
		require.NoError(t, appendErr)
	}

	binAfter, err := os.ReadFile(rec.StoredPath)
	require.NoError(t, err)
	sidecarAfter, err := os.ReadFile(rec.StoredPath + ".json")
	require.NoError(t, err)
	assert.Equal(t, hashOf(binBefore), hashOf(binAfter), "the media binary must be unchanged by linking")
	assert.Equal(t, sidecarBefore, sidecarAfter, "the media sidecar bytes must be unchanged by linking")
}

func TestReadMediaByID_FoundMissingAndMalformed(t *testing.T) {
	a := New(t.TempDir())
	rec, err := a.WriteMedia(syntheticMedia("hello world", "img.png", []byte("bytes")))
	require.NoError(t, err)

	got, found, err := a.ReadMediaByID(rec.ID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, rec.ID, got.ID)
	assert.Equal(t, rec.SHA256, got.SHA256)
	assert.Equal(t, rec.StoredPath, got.StoredPath)

	// A missing sidecar is not-found, not an error.
	_, found, err = a.ReadMediaByID("2026-07-05-nope.jpg")
	require.NoError(t, err)
	assert.False(t, found)

	// An id too short to carry a day prefix is not-found, not an error.
	_, found, err = a.ReadMediaByID("garbage")
	require.NoError(t, err)
	assert.False(t, found)

	// An id whose prefix is not a valid day is not-found, not an error.
	_, found, err = a.ReadMediaByID("2026-13-40-x.jpg")
	require.NoError(t, err)
	assert.False(t, found)
}
