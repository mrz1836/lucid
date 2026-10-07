package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

// workoutLogAckID pulls the session id out of a `workout log` acknowledgement
// ("Logged workout as `obs_…`.").
var workoutLogAckID = regexp.MustCompile("Logged workout as `(obs_[^`]+)`")

// createWorkoutCLI logs one session through the CLI and returns its obs id, so
// the amend tests have a real base session to correct.
func createWorkoutCLI(t *testing.T, args ...string) string {
	t.Helper()
	full := append([]string{"workout", "log"}, args...)
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, full...)
	require.NoError(t, err)
	m := workoutLogAckID.FindStringSubmatch(out)
	require.Len(t, m, 2, "the log ack names the session id: %q", out)
	return m[1]
}

// foldedCLIWorkout reads the Ledger, folds the workout corrections, and returns
// the current (folded) state of one session — what every read surface shows
// after an amend.
func foldedCLIWorkout(t *testing.T, home, id string) observations.Event {
	t.Helper()
	for _, e := range observations.FoldWorkoutAmendments(readObsEvents(t, home)) {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("workout %s not found after fold", id)
	return observations.Event{}
}

// metacharNote is a synthetic note carrying every shell metacharacter the
// file-input surface exists to protect, plus an embedded newline.
const metacharNote = "pulled hard; felt $HOME `whoami` & \"quoted\" 'single' | piped\nsecond line > not a redirect"

// TestWorkoutAmend_Registered confirms `workout amend` is a child of `workout`,
// self-documents every v1 flag, and keeps the deferred --soreness/--pain off the
// help surface (they are parsed only to be refused).
func TestWorkoutAmend_Registered(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})
	cmd, _, err := root.Find([]string{"workout", "amend"})
	require.NoError(t, err)
	assert.Equal(t, "amend", cmd.Name())

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "amend", "--help")
	require.NoError(t, err)
	for _, flag := range []string{
		"--rpe", "--duration", "--type", "--movements", "--parts",
		"--notes", "--notes-file", "--day",
	} {
		assert.Contains(t, out, flag, "help lists %s", flag)
	}
	assert.NotContains(t, out, "--soreness", "soreness is hidden — not amendable in this version")
	assert.NotContains(t, out, "--pain", "pain is hidden — not amendable in this version")

	logHelp, _, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "log", "--help")
	require.NoError(t, err)
	assert.Contains(t, logHelp, "--notes-file", "workout log takes --notes-file too")
}

// TestWorkoutAmend_NotesFile proves the replacement note arrives off the
// command line through the shared reader — from a path and from stdin — with
// shell metacharacters and newlines stored verbatim, and that two note sources
// or an empty file are refused with nothing written.
func TestWorkoutAmend_NotesFile(t *testing.T) {
	t.Run("path", func(t *testing.T) {
		home := enableWorkoutKinds(t)
		id := createWorkoutCLI(t, "--type", "climbing", "--duration", "60")

		path := filepath.Join(t.TempDir(), "notes.txt")
		require.NoError(t, os.WriteFile(path, []byte(metacharNote+"\n"), 0o600))

		_, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "amend", id, "--notes-file", path)
		require.NoError(t, err)
		assert.Empty(t, stderr)
		assert.Equal(t, metacharNote, foldedCLIWorkout(t, home, id).Payload["note"],
			"metacharacters and the embedded newline survive; only the editor newline is framing")
	})

	t.Run("stdin", func(t *testing.T) {
		home := enableWorkoutKinds(t)
		id := createWorkoutCLI(t, "--type", "climbing")

		_, _, err := runRootWithStdin(t, metacharNote+"\n", "workout", "amend", id, "--notes-file", "-")
		require.NoError(t, err)
		assert.Equal(t, metacharNote, foldedCLIWorkout(t, home, id).Payload["note"])
	})

	t.Run("notes and notes-file together are refused", func(t *testing.T) {
		home := enableWorkoutKinds(t)
		id := createWorkoutCLI(t, "--type", "climbing")
		before := readObsEvents(t, home)

		path := filepath.Join(t.TempDir(), "notes.txt")
		require.NoError(t, os.WriteFile(path, []byte("from the file\n"), 0o600))

		_, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "amend", id,
			"--notes", "inline", "--notes-file", path)
		require.Error(t, err)
		assert.Contains(t, stderr, "lucid workout amend: give the notes via --notes or --notes-file, not both")
		assert.Contains(t, stderr, "nothing was saved")
		assert.Len(t, readObsEvents(t, home), len(before), "a refused amend writes nothing")
	})

	t.Run("empty file is refused", func(t *testing.T) {
		home := enableWorkoutKinds(t)
		id := createWorkoutCLI(t, "--type", "climbing")
		before := readObsEvents(t, home)

		path := filepath.Join(t.TempDir(), "empty.txt")
		require.NoError(t, os.WriteFile(path, []byte("\n"), 0o600))

		_, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "amend", id, "--notes-file", path)
		require.Error(t, err)
		assert.Contains(t, stderr, "lucid workout amend: --notes-file: file is empty")
		assert.Len(t, readObsEvents(t, home), len(before), "a refused amend writes nothing")
	})
}

// TestWorkoutAmend_JSONView proves --json emits the amend view — the new
// correction id (not the session's), the session it corrects, the logical day,
// and a {from, to} entry for exactly the changed fields — and that the
// correction appended carries refs.corrects with only the changed payload.
func TestWorkoutAmend_JSONView(t *testing.T) {
	home := enableWorkoutKinds(t)
	id := createWorkoutCLI(t, "--type", "climbing", "--duration", "60")

	out, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "amend", id, "--rpe", "4", "--json")
	require.NoError(t, err)
	assert.Empty(t, stderr)

	var view struct {
		EventID     string                     `json:"event_id"`
		TargetID    string                     `json:"target_id"`
		LogicalDate string                     `json:"logical_date"`
		Changes     map[string]json.RawMessage `json:"changes"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.NotEmpty(t, view.EventID)
	assert.NotEqual(t, id, view.EventID, "the view names the new correction, not the session")
	assert.Equal(t, id, view.TargetID)
	assert.NotEmpty(t, view.LogicalDate)
	require.Len(t, view.Changes, 1, "only the changed field is reported")
	assert.JSONEq(t, `{"from": null, "to": 4}`, string(view.Changes["rpe"]))

	workouts := eventsOfKind(readObsEvents(t, home), observations.KindWorkout)
	require.Len(t, workouts, 2, "the session plus exactly one correction")
	var correction observations.Event
	for _, w := range workouts {
		if w.ID == view.EventID {
			correction = w
		}
	}
	assert.Equal(t, id, correction.Refs[observations.RefCorrects])
	assert.Equal(t, map[string]any{"rpe": float64(4)}, correction.Payload, "only the changed field is written")

	folded := foldedCLIWorkout(t, home, id)
	assert.EqualValues(t, 4, folded.Payload["rpe"])
	assert.Equal(t, "climbing", folded.Payload["type"], "an unpassed field keeps its value")
	assert.EqualValues(t, 60, folded.Payload["duration_min"], "an unpassed field keeps its value")
}

// TestWorkoutAmend_JSONViewReportsPriorValue proves a changed field that was
// already set reports its folded prior value as `from`, and a list flag carries
// the full replacement list, comma-split and accumulated like `workout log`.
func TestWorkoutAmend_JSONViewReportsPriorValue(t *testing.T) {
	enableWorkoutKinds(t)
	id := createWorkoutCLI(t, "--type", "climbing", "--rpe", "6", "--parts", "fingers")

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "amend", id,
		"--rpe", "4", "--parts", "fingers,forearms", "--parts", "back", "--json")
	require.NoError(t, err)

	var view workoutAmendView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	require.Contains(t, view.Changes, "rpe")
	assert.EqualValues(t, 6, view.Changes["rpe"].From)
	assert.EqualValues(t, 4, view.Changes["rpe"].To)
	require.Contains(t, view.Changes, "body_parts")
	assert.Equal(t, []any{"fingers", "forearms", "back"}, view.Changes["body_parts"].To)
}

// TestWorkoutAmend_Receipt proves the human path prints the inventory ack —
// the session and the new correction id, no score — through the receipt tail.
func TestWorkoutAmend_Receipt(t *testing.T) {
	home := enableWorkoutKinds(t)
	id := createWorkoutCLI(t, "--type", "climbing")

	out, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "amend", id, "--rpe", "4")
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.Contains(t, out, "Amended workout `"+id+"`")
	assert.Contains(t, out, "recorded as `obs_")
	assert.EqualValues(t, 4, foldedCLIWorkout(t, home, id).Payload["rpe"])
}

// TestWorkoutAmend_RouterRefusalIsPrefixed proves a reason the router decides
// reaches stderr prefixed with the verb, with nothing written.
func TestWorkoutAmend_RouterRefusalIsPrefixed(t *testing.T) {
	home := enableWorkoutKinds(t)
	id := createWorkoutCLI(t, "--type", "climbing")
	before := readObsEvents(t, home)

	_, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "amend", id, "--rpe", "11")
	require.Error(t, err)
	assert.Contains(t, stderr, "lucid workout amend: --rpe must be 0-10; nothing was saved")
	assert.Len(t, readObsEvents(t, home), len(before))
}

// TestWorkout_Log_NotesFile proves `workout log` takes --notes-file through the
// same reader (verbatim metacharacters), refuses it alongside --notes, and
// treats it as a structured flag that can't ride a spoken drop.
func TestWorkout_Log_NotesFile(t *testing.T) {
	t.Run("stdin stored verbatim", func(t *testing.T) {
		home := enableWorkoutKinds(t)
		_, _, err := runRootWithStdin(t, metacharNote+"\n", "workout", "log", "--type", "push", "--notes-file", "-")
		require.NoError(t, err)
		assert.Equal(t, metacharNote, singleWorkout(t, home).Payload["note"])
	})

	t.Run("notes and notes-file together are refused", func(t *testing.T) {
		home := enableWorkoutKinds(t)
		path := filepath.Join(t.TempDir(), "notes.txt")
		require.NoError(t, os.WriteFile(path, []byte("from the file\n"), 0o600))

		_, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "log",
			"--type", "push", "--notes", "inline", "--notes-file", path)
		require.Error(t, err)
		assert.Contains(t, stderr, "lucid workout log: give the notes via --notes or --notes-file, not both")
		assert.Empty(t, readObsEvents(t, home))
	})

	t.Run("spoken drop with notes-file is a mixed form", func(t *testing.T) {
		home := enableWorkoutKinds(t)
		path := filepath.Join(t.TempDir(), "notes.txt")
		require.NoError(t, os.WriteFile(path, []byte("from the file\n"), 0o600))

		_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "log", "did pull today", "--notes-file", path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not both")
		assert.Empty(t, readObsEvents(t, home))
	})
}
