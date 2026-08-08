package cli

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/storage"
)

// seedCLIMedia writes one synthetic media into the isolated home and returns its
// stored id — the media the link verbs point a subject at. It writes directly
// through storage so the id is deterministic (2026-07-05-photo.jpg).
func seedCLIMedia(t *testing.T, home string) string {
	t.Helper()
	rec, err := storage.New(home).WriteMedia(storage.MediaAttachment{
		Bytes:            []byte("\xff\xd8\xff synthetic jpeg bytes"),
		OriginalFilename: "photo.jpg",
		CapturedAt:       time.Date(2026, time.July, 5, 18, 41, 39, 0, time.UTC),
		LogicalDay:       "2026-07-05",
		Source:           "cli",
		RawEntryID:       "raw_2026_07_05_18_41",
	})
	require.NoError(t, err)
	return rec.ID
}

// TestLinkVerbs_Registered confirms link, unlink, and annotate are on the spine
// and self-document their --to grammar (AC-9).
func TestLinkVerbs_Registered(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})
	got := map[string]bool{}
	for _, c := range root.Commands() {
		got[c.Name()] = true
	}
	for _, v := range []string{"link", "unlink", "annotate"} {
		assert.Truef(t, got[v], "%s verb not registered", v)
	}

	for _, v := range []string{"link", "unlink", "annotate"} {
		out, _, err := runRoot(t, BuildInfo{Version: "dev"}, v, "--help")
		require.NoErrorf(t, err, "%s --help", v)
		assert.Containsf(t, out, "--to", "%s must document --to", v)
	}
}

// TestLink_CLI_ProseAndJSON links a media to a subject and renders both the
// prose ack and the --json applied shape (AC-9).
func TestLink_CLI_ProseAndJSON(t *testing.T) {
	home := isolatedHome(t)
	mediaID := seedCLIMedia(t, home)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "link", mediaID, "--to", "day:2020-01-01")
	require.NoError(t, err)
	assert.Contains(t, out, "Linked")
	assert.Contains(t, out, "day:2020-01-01")

	// --json on a fresh subject: a real append, no_op false, with an event id.
	out, _, err = runRoot(t, BuildInfo{Version: "dev"}, "link", mediaID, "--to", "day:2020-02-02", "--json")
	require.NoError(t, err)
	var view linkResultView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.Equal(t, mediaID, view.MediaID)
	require.Len(t, view.Applied, 1)
	assert.Equal(t, "day", view.Applied[0].Kind)
	assert.Equal(t, "2020-02-02", view.Applied[0].Key)
	assert.False(t, view.Applied[0].NoOp)
	assert.NotEmpty(t, view.Applied[0].EventID)
}

// TestLink_CLI_RepeatableTo links one media to several subjects in one turn via
// repeated --to flags.
func TestLink_CLI_RepeatableTo(t *testing.T) {
	home := isolatedHome(t)
	mediaID := seedCLIMedia(t, home)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"link", mediaID, "--to", "day:2020-01-01", "--to", "day:2020-02-02", "--json")
	require.NoError(t, err)
	var view linkResultView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.Len(t, view.Applied, 2)
}

// TestLink_CLI_NoOpExitsZero: re-linking a live pair is a no-op ack, exit 0
// (AC-7 growth-friendly semantics).
func TestLink_CLI_NoOpExitsZero(t *testing.T) {
	home := isolatedHome(t)
	mediaID := seedCLIMedia(t, home)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "link", mediaID, "--to", "day:2020-01-01")
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "link", mediaID, "--to", "day:2020-01-01")
	require.NoError(t, err, "a re-link is a no-op ack, not an error")
	assert.Contains(t, out, "already linked")
}

// TestUnlink_CLI retires a live link and no-ops an unlink of a pair that is not
// live, both exit 0.
func TestUnlink_CLI(t *testing.T) {
	home := isolatedHome(t)
	mediaID := seedCLIMedia(t, home)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "link", mediaID, "--to", "day:2020-01-01")
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "unlink", mediaID, "--to", "day:2020-01-01")
	require.NoError(t, err)
	assert.Contains(t, out, "Unlinked")

	// Unlinking a pair that is not live is a no-op, exit 0.
	out, _, err = runRoot(t, BuildInfo{Version: "dev"}, "unlink", mediaID, "--to", "day:2020-01-01")
	require.NoError(t, err)
	assert.Contains(t, out, "was not linked")
}

// TestAnnotate_CLI attaches a note and requires --note.
func TestAnnotate_CLI(t *testing.T) {
	home := isolatedHome(t)
	mediaID := seedCLIMedia(t, home)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"annotate", mediaID, "--to", "day:2020-01-01", "--note", "a synthetic note")
	require.NoError(t, err)
	assert.Contains(t, out, "Annotated")

	// --note is required for annotate.
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "annotate", mediaID, "--to", "day:2020-01-01")
	require.Error(t, err, "annotate without --note is a usage error")
}

// TestLink_RejectsNoteFlag: --note is annotate's own op, so it is not a flag on
// link (D1) — passing it is an unknown-flag usage error.
func TestLink_RejectsNoteFlag(t *testing.T) {
	home := isolatedHome(t)
	mediaID := seedCLIMedia(t, home)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"link", mediaID, "--to", "day:2020-01-01", "--note", "nope")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err), "--note on link is an unknown flag")
}

// TestLink_UnknownKindExitsNonZero: an unknown subject kind exits non-zero and
// names the accepted kinds on stderr (AC-13 surfaced through the verb).
func TestLink_UnknownKindExitsNonZero(t *testing.T) {
	home := isolatedHome(t)
	mediaID := seedCLIMedia(t, home)

	_, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "link", mediaID, "--to", "bogus:x")
	require.Error(t, err)
	assert.Equal(t, ExitErr, exitCodeForError(err))
	assert.Contains(t, errOut, "bogus")
	assert.Contains(t, errOut, "accepted kinds", "the refusal lists the accepted kinds")
}

// TestLink_MissingTo: --to is required.
func TestLink_MissingTo(t *testing.T) {
	home := isolatedHome(t)
	mediaID := seedCLIMedia(t, home)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "link", mediaID)
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
}
