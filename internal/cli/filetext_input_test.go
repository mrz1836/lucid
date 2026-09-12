package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

// metaPayload is a free-text value carrying every shell metacharacter that
// could split or truncate an unquoted command line — the exact class the
// file-input surface exists to keep as data. It has no leading/trailing space
// so it survives writers that trim their surrounding whitespace; the serialized
// raw whitespace guarantee is proven separately against the closeout raw entry.
const metaPayload = "& ; | " + "`" + " $(...) ' \" r&b yoga"

// TestLog_CLI_BodyFileWritesVerbatim: --body-file supplies the entry body off
// the command line, and its metacharacter payload lands in the raw entry as
// data — no split, no truncation.
func TestLog_CLI_BodyFileWritesVerbatim(t *testing.T) {
	home := isolatedHome(t)
	bf := writeTemp(t, metaPayload+"\n")

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "log", "--body-file", bf)
	require.NoError(t, err)
	assert.Contains(t, out, "Saved as `raw_")
	assert.Contains(t, readOnlyRaw(t, home), metaPayload)
}

// TestLog_CLI_BodyFileConflictsWithPositional: positional text alongside
// --body-file is two sources for one field, refused rather than silently picked.
func TestLog_CLI_BodyFileConflictsWithPositional(t *testing.T) {
	isolatedHome(t)
	bf := writeTemp(t, "from the file")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "log", "positional", "words", "--body-file", bf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not both")
}

// TestObs_CLI_BodyFileCaptures: --body-file supplies the whole observation
// expression (kind plus value); it flows through the same kind/value parser as
// positional tokens and lands one event.
func TestObs_CLI_BodyFileCaptures(t *testing.T) {
	home := enableAllObsKinds(t)
	bf := writeTemp(t, "pain 6 knee & thumb")

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "obs", "--body-file", bf)
	require.NoError(t, err)
	assert.Contains(t, out, "Logged pain as `obs_")

	events := readObsEvents(t, home)
	require.Len(t, events, 1)
	assert.Equal(t, observations.KindPain, events[0].Kind)
}

// TestObs_CLI_BodyFileConflictsWithPositional: positional tokens alongside
// --body-file is refused.
func TestObs_CLI_BodyFileConflictsWithPositional(t *testing.T) {
	enableAllObsKinds(t)
	bf := writeTemp(t, "pain 6 knee")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "obs", "pain", "6", "--body-file", bf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not both")
}

// TestAnchorSunset_CLI_ReasonFile: --reason-file supplies the retirement reason
// off the command line and it lands as the record's note.
func TestAnchorSunset_CLI_ReasonFile(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "gate-30", "2026-02-01")
	require.NoError(t, err)

	rf := writeTemp(t, metaPayload)
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "anchor", "sunset", "gate-30", "--reason-file", rf)
	require.NoError(t, err)

	log := readAnchorLog(t, home)
	require.Len(t, log.History, 2)
	assert.Equal(t, metaPayload, log.History[1].Note)
}

// TestAnchorSunset_CLI_ReasonFileConflictsWithPositional: a positional reason
// alongside --reason-file is refused.
func TestAnchorSunset_CLI_ReasonFileConflictsWithPositional(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "gate-30", "2026-02-01")
	require.NoError(t, err)

	rf := writeTemp(t, "from the file")
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "anchor", "sunset", "gate-30", "typed", "reason", "--reason-file", rf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not both")
}

// TestSelfSet_CLI_ValueFileAndNoteFile: --value-file supplies the value and
// --note-file the annotation, both off the command line and both landing on the
// record.
func TestSelfSet_CLI_ValueFileAndNoteFile(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())

	vf := writeTemp(t, metaPayload)
	nf := writeTemp(t, "an annotation & note")
	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"self", "set", "identity.generation", "--value-file", vf, "--note-file", nf)
	require.NoError(t, err)

	log := readSelfLog(t, home)
	require.Len(t, log.History, 1)
	assert.Equal(t, metaPayload, log.History[0].Value)
	assert.Equal(t, "an annotation & note", log.History[0].Note)
}

// TestSelfSet_CLI_ValueFileConflictsWithPositional: a positional value alongside
// --value-file is refused.
func TestSelfSet_CLI_ValueFileConflictsWithPositional(t *testing.T) {
	isolatedHome(t)
	vf := writeTemp(t, "from the file")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"self", "set", "identity.generation", "typed", "value", "--value-file", vf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not both")
}

// TestSelfSet_CLI_NoteFileConflictsWithInline: --note and --note-file for the
// same field is refused.
func TestSelfSet_CLI_NoteFileConflictsWithInline(t *testing.T) {
	isolatedHome(t)
	nf := writeTemp(t, "from the file")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"self", "set", "identity.generation", "millennial", "--note", "inline", "--note-file", nf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not both")
}

// TestThread_CLI_FileInputFields: the name, intent, note, and a repeated domain
// all arrive off the command line, landing verbatim on the --json surface.
func TestThread_CLI_FileInputFields(t *testing.T) {
	isolatedHome(t)

	name := writeTemp(t, "learning to rest & recover")
	intent := writeTemp(t, "recovery as practice")
	d1 := writeTemp(t, "body & mind")
	d2 := writeTemp(t, "spirit")
	out, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"thread", "--body-file", name, "--intent-file", intent,
		"--domain-file", d1, "--domain-file", d2, "--json")
	require.NoError(t, err)

	var view registryWriteView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.Equal(t, "learning to rest & recover", view.DisplayName)
	assert.Equal(t, "recovery as practice", view.Fields["intent"])
	assert.Equal(t, []any{"body & mind", "spirit"}, view.Fields["domains"])
}

// TestThread_CLI_DomainFileConflictsWithInline: mixing --domain and
// --domain-file for the same field is refused.
func TestThread_CLI_DomainFileConflictsWithInline(t *testing.T) {
	isolatedHome(t)
	df := writeTemp(t, "body")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"thread", "resting", "--domain", "mind", "--domain-file", df)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not both")
}

// TestThread_CLI_IntentFileConflictsWithInline: --intent and --intent-file for
// the same field is refused.
func TestThread_CLI_IntentFileConflictsWithInline(t *testing.T) {
	isolatedHome(t)
	inf := writeTemp(t, "recovery")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"thread", "resting", "--intent", "inline", "--intent-file", inf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not both")
}

// TestGratitude_CLI_BodyFile: --body-file supplies the thing off the command
// line and it lands in the tally verbatim.
func TestGratitude_CLI_BodyFile(t *testing.T) {
	isolatedHome(t)
	bf := writeTemp(t, metaPayload)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "add", "--body-file", bf)
	require.NoError(t, err)

	entries := gratitudeListEntries(t)
	require.Len(t, entries, 1)
	assert.Equal(t, metaPayload, entries[0].Thing)
}

// TestGratitude_CLI_BodyFileConflictsWithPositional: positional thing alongside
// --body-file is refused.
func TestGratitude_CLI_BodyFileConflictsWithPositional(t *testing.T) {
	isolatedHome(t)
	bf := writeTemp(t, "from the file")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "add", "my coffee", "--body-file", bf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not both")
}

// TestReframe_CLI_FileInputPair: --catch-file and --flip-file together supply
// both halves off the command line, with trailing positional tags preserved.
func TestReframe_CLI_FileInputPair(t *testing.T) {
	home := isolatedHome(t)
	cf := writeTemp(t, "I can't do this & it's hopeless")
	ff := writeTemp(t, "I can learn this")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"reframe", "add", "--catch-file", cf, "--flip-file", ff, "#growth")
	require.NoError(t, err)

	entries := readReframeEntries(t, home)
	require.Len(t, entries, 1)
	assert.Equal(t, "I can't do this & it's hopeless", entries[0].Catch)
	assert.Equal(t, "I can learn this", entries[0].Flip)
	assert.Equal(t, []string{"growth"}, entries[0].Tags)
}

// TestReframe_CLI_CatchFileRequiresFlipFile: the two required halves move
// together — one file flag without the other is refused and nothing is written.
func TestReframe_CLI_CatchFileRequiresFlipFile(t *testing.T) {
	home := isolatedHome(t)
	cf := writeTemp(t, "only a catch")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "reframe", "add", "--catch-file", cf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "together")
	assert.Empty(t, readReframeEntries(t, home), "an incomplete pair writes nothing")
}

// TestFocus_CLI_FileInputFields: --body-file supplies the text and
// --success-file the criterion off the command line, both landing verbatim.
func TestFocus_CLI_FileInputFields(t *testing.T) {
	home := isolatedHome(t)
	bf := writeTemp(t, "take a walk & breathe")
	sf := writeTemp(t, "I stepped outside")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"focus", "add", "--body-file", bf, "--success-file", sf)
	require.NoError(t, err)

	entries := readFocusEntries(t, home)
	require.Len(t, entries, 1)
	assert.Equal(t, "take a walk & breathe", entries[0].Text)
	assert.Equal(t, "I stepped outside", entries[0].SuccessCriterion)
}

// TestFocus_CLI_SuccessFileConflictsWithInline: --success and --success-file for
// the same field is refused.
func TestFocus_CLI_SuccessFileConflictsWithInline(t *testing.T) {
	isolatedHome(t)
	sf := writeTemp(t, "from the file")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"focus", "add", "a walk", "--success", "inline", "--success-file", sf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not both")
}

// TestMemory_CLI_FileInputFields: the story text and a prose-metadata field
// (tone) both arrive off the command line and land on the event payload.
func TestMemory_CLI_FileInputFields(t *testing.T) {
	home := enableMemoryHome(t)
	bf := writeTemp(t, "the coast at 2am & the tide")
	tf := writeTemp(t, "free & alive")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"memory", "--body-file", bf, "--tone-file", tf, "--certainty", "vivid")
	require.NoError(t, err)

	events := readObsEvents(t, home)
	require.Len(t, events, 1)
	assert.Equal(t, observations.KindMemory, events[0].Kind)
	assert.Equal(t, "the coast at 2am & the tide", events[0].Payload[observations.MemoryFieldText])
	assert.Equal(t, "free & alive", events[0].Payload[observations.MemoryFieldTone])
}

// TestMemory_CLI_ToneFileConflictsWithInline: --tone and --tone-file for the
// same field is refused.
func TestMemory_CLI_ToneFileConflictsWithInline(t *testing.T) {
	enableMemoryHome(t)
	tf := writeTemp(t, "from the file")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"memory", "a plain memory", "--tone", "inline", "--tone-file", tf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not both")
}

// TestMemory_CLI_MultiStdinRejected: stdin is one stream, so routing two prose
// fields to "-" is refused before any read drains it.
func TestMemory_CLI_MultiStdinRejected(t *testing.T) {
	enableMemoryHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"memory", "--body-file", "-", "--tone-file", "-")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "one field")
}

// TestEra_CLI_NoteFile: --note-file supplies the chapter note off the command
// line and it lands on the registry record.
func TestEra_CLI_NoteFile(t *testing.T) {
	isolatedHome(t)
	nf := writeTemp(t, "the wandering & searching years")

	out, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"era", "create", "the road years", "--note-file", nf, "--json")
	require.NoError(t, err)

	var view registryWriteView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.Equal(t, "the wandering & searching years", view.Fields["note"])
}

// TestEra_CLI_NoteFileConflictsWithInline: --note and --note-file for the same
// field is refused.
func TestEra_CLI_NoteFileConflictsWithInline(t *testing.T) {
	isolatedHome(t)
	nf := writeTemp(t, "from the file")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"era", "create", "the road years", "--note", "inline", "--note-file", nf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not both")
}

// runPersonSetStdin executes the cobra tree with the given stdin payload,
// building the root directly so a --*-file "-" flag reads from it. runRoot has
// no stdin parameter, so the file-input stdin path needs this variant — the
// same SetIn pattern reflect week apply uses.
func runPersonSetStdin(t *testing.T, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := newRootCmd(BuildInfo{Version: "dev"})
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	err = root.ExecuteContext(context.Background())
	return out.String(), errBuf.String(), err
}

// TestPersonSet_CLI_FileInputFields: --note-file and --relationship-file supply
// both durable free-text fields off the command line, and the payload — shell
// metacharacters, multiple lines, and UTF-8 — lands on the --json record
// byte-for-byte: no split, no truncation, no escaping.
func TestPersonSet_CLI_FileInputFields(t *testing.T) {
	home := isolatedHome(t)
	writePersonRecord(t, home, "person_a-alex", "Alex", []string{"Alex"}, []string{"raw_1"}, personSeed())

	noteContent := metaPayload + "\nline one\nrésumé & café\nline two"
	relContent := "peer & mentor\nrésumé reviewer — café"
	nf := writeTemp(t, noteContent)
	rf := writeTemp(t, relContent)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"person", "set", "person_a-alex", "--note-file", nf, "--relationship-file", rf, "--json")
	require.NoError(t, err)

	var view personWriteView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	require.NotNil(t, view.Notes)
	assert.Equal(t, noteContent, *view.Notes)
	require.NotNil(t, view.Relationship)
	assert.Equal(t, relContent, *view.Relationship)
}

// TestPersonSet_CLI_NoteFileFromStdin: --note-file - reads the note from stdin,
// landing verbatim on the record without ever touching the command line.
func TestPersonSet_CLI_NoteFileFromStdin(t *testing.T) {
	home := isolatedHome(t)
	writePersonRecord(t, home, "person_a-alex", "Alex", []string{"Alex"}, []string{"raw_1"}, personSeed())

	out, _, err := runPersonSetStdin(t, metaPayload,
		"person", "set", "person_a-alex", "--note-file", "-", "--json")
	require.NoError(t, err)

	var view personWriteView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	require.NotNil(t, view.Notes)
	assert.Equal(t, metaPayload, *view.Notes)
}

// TestPersonSet_CLI_RelationshipFileFromStdin: --relationship-file - reads the
// relationship label from stdin, landing verbatim.
func TestPersonSet_CLI_RelationshipFileFromStdin(t *testing.T) {
	home := isolatedHome(t)
	writePersonRecord(t, home, "person_a-alex", "Alex", []string{"Alex"}, []string{"raw_1"}, personSeed())

	out, _, err := runPersonSetStdin(t, metaPayload,
		"person", "set", "person_a-alex", "--relationship-file", "-", "--json")
	require.NoError(t, err)

	var view personWriteView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	require.NotNil(t, view.Relationship)
	assert.Equal(t, metaPayload, *view.Relationship)
}

// TestPersonSet_CLI_NoteFileConflictsWithInline: --note and --note-file for the
// same field is two sources for one value, refused rather than silently picked.
func TestPersonSet_CLI_NoteFileConflictsWithInline(t *testing.T) {
	isolatedHome(t)
	nf := writeTemp(t, "from the file")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"person", "set", "person_a-alex", "--note", "inline", "--note-file", nf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not both")
}

// TestPersonSet_CLI_RelationshipFileConflictsWithInline: --relationship and
// --relationship-file for the same field is refused.
func TestPersonSet_CLI_RelationshipFileConflictsWithInline(t *testing.T) {
	isolatedHome(t)
	rf := writeTemp(t, "from the file")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"person", "set", "person_a-alex", "--relationship", "inline", "--relationship-file", rf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not both")
}

// TestPersonSet_CLI_NoteFileMissing: an unreadable --note-file path exits
// non-zero and names the real flag and the offending path — not the generic
// --body-file the shared reader used before the label was threaded through.
func TestPersonSet_CLI_NoteFileMissing(t *testing.T) {
	isolatedHome(t)
	missing := filepath.Join(t.TempDir(), "nope.txt")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"person", "set", "person_a-alex", "--note-file", missing)
	require.Error(t, err)
	assert.NotEqual(t, ExitOK, exitCodeForError(err))
	assert.Contains(t, err.Error(), "--note-file")
	assert.Contains(t, err.Error(), missing)
	assert.NotContains(t, err.Error(), "--body-file")
}

// TestPersonSet_CLI_RelationshipFileMissing: an unreadable --relationship-file
// path exits non-zero and names the real flag and path, not --body-file.
func TestPersonSet_CLI_RelationshipFileMissing(t *testing.T) {
	isolatedHome(t)
	missing := filepath.Join(t.TempDir(), "nope.txt")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"person", "set", "person_a-alex", "--relationship-file", missing)
	require.Error(t, err)
	assert.NotEqual(t, ExitOK, exitCodeForError(err))
	assert.Contains(t, err.Error(), "--relationship-file")
	assert.Contains(t, err.Error(), missing)
	assert.NotContains(t, err.Error(), "--body-file")
}

// TestPersonSet_CLI_NoteFileEmpty: an empty --note-file (after the one-newline
// strip) is rejected — a file flag that supplies nothing is a mistake, and
// clearing a field stays the job of the inline --note "".
func TestPersonSet_CLI_NoteFileEmpty(t *testing.T) {
	isolatedHome(t)
	ef := writeTemp(t, "")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"person", "set", "person_a-alex", "--note-file", ef)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is empty")
	assert.Contains(t, err.Error(), "--note-file")
}

// TestPersonSet_CLI_RelationshipFileEmpty: an empty --relationship-file is
// likewise rejected.
func TestPersonSet_CLI_RelationshipFileEmpty(t *testing.T) {
	isolatedHome(t)
	ef := writeTemp(t, "")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"person", "set", "person_a-alex", "--relationship-file", ef)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is empty")
	assert.Contains(t, err.Error(), "--relationship-file")
}
