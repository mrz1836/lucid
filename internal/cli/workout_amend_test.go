package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/storage"
	"github.com/mrz1836/lucid/internal/workout"
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

// observationsLedgerSnapshot reads every observations day file under home byte
// for byte, keyed by path, so a refusal test proves the Ledger is untouched —
// nothing appended and nothing rewritten — not merely the same event count.
func observationsLedgerSnapshot(t *testing.T, home string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(filepath.Join(home, "observations"), func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() || !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		files[p] = string(b)
		return nil
	})
	require.NoError(t, err)
	return files
}

// setObservationKindEnabled flips one kind on or off in the home's
// observations config, so a test can mint records under an enabled kind and
// then exercise the config gate with it disabled.
func setObservationKindEnabled(t *testing.T, home string, kind observations.Kind, enabled bool) {
	t.Helper()
	a := storage.New(home)
	cfg, err := a.ReadObservationsConfig()
	require.NoError(t, err)
	kinds := make([]observations.Kind, 0, len(cfg.KindsEnabled)+1)
	for _, k := range cfg.KindsEnabled {
		if k != kind {
			kinds = append(kinds, k)
		}
	}
	if enabled {
		kinds = append(kinds, kind)
	}
	cfg.KindsEnabled = kinds
	require.NoError(t, a.SaveObservationsConfig(cfg))
}

// TestWorkoutAmend_Refusals pins every user-facing amend refusal
// (error-states.md W-10..W-18): each exits non-zero, prints nothing to stdout,
// names its reason on stderr prefixed `lucid workout amend:` and ending
// "nothing was saved", and leaves the Ledger byte-identical. The ids refused as
// "not a workout" are real events of other kinds — the body_state reading the
// session's own log wrote, and a memory — so the kind check is what refuses
// them, not a failed lookup.
func TestWorkoutAmend_Refusals(t *testing.T) {
	home := enableWorkoutKinds(t)
	setObservationKindEnabled(t, home, observations.KindMemory, true)

	id := createWorkoutCLI(t, "--type", "climbing", "--duration", "60", "--soreness", "forearms:4")
	states := eventsOfKind(readObsEvents(t, home), observations.KindBodyState)
	require.Len(t, states, 1, "the session's log wrote one body_state reading")
	bodyStateID := states[0].ID
	memoryID := createMemoryCLI(t, "a synthetic memory to aim amend at")

	notesPath := filepath.Join(t.TempDir(), "notes.txt")
	require.NoError(t, os.WriteFile(notesPath, []byte("from the file\n"), 0o600))

	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "unknown id",
			args: []string{"obs_2026_01_15_009", "--rpe", "4"},
			want: `workout "obs_2026_01_15_009" not found`,
		},
		{
			name: "token that isn't an obs id",
			args: []string{"last-climb", "--rpe", "4"},
			want: `workout "last-climb" not found`,
		},
		{
			name: "body_state id",
			args: []string{bodyStateID, "--rpe", "4"},
			want: `"` + bodyStateID + `" is a body_state observation, not a workout session`,
		},
		{
			name: "memory id",
			args: []string{memoryID, "--rpe", "4"},
			want: `"` + memoryID + `" is a memory observation, not a workout session`,
		},
		{
			name: "no content flags",
			args: []string{id},
			want: "no fields to amend",
		},
		{
			name: "no id",
			args: []string{"--rpe", "4"},
			want: "an obs id is required",
		},
		{
			name: "positional free text after the id",
			args: []string{id, "felt", "strong", "--rpe", "4"},
			want: "amend takes a single obs id; free text isn't a field — use flags",
		},
		{
			name: "future day",
			args: []string{id, "--day", "2099-01-01"},
			want: "cannot capture against 2099-01-01 — that day has not happened yet",
		},
		{
			name: "unreadable day",
			args: []string{id, "--day", "@yesterdya"},
			want: `could not read the day "@yesterdya"`,
		},
		{
			name: "soreness passed",
			args: []string{id, "--soreness", "forearms:6"},
			want: "soreness/pain aren't amendable yet; body-state amendment is a planned follow-up",
		},
		{
			name: "pain passed",
			args: []string{id, "--rpe", "4", "--pain", "elbow"},
			want: "soreness/pain aren't amendable yet; body-state amendment is a planned follow-up",
		},
		{
			name: "notes and notes-file together",
			args: []string{id, "--notes", "inline", "--notes-file", notesPath},
			want: "give the notes via --notes or --notes-file, not both",
		},
		{
			name: "empty value",
			args: []string{id, "--type", ""},
			want: "--type needs a value — amend corrects a field, it doesn't clear it",
		},
		{
			name: "negative duration",
			args: []string{id, "--duration=-5"},
			want: "--duration must be zero or more",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := observationsLedgerSnapshot(t, home)

			args := append([]string{"workout", "amend"}, tc.args...)
			stdout, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, args...)
			require.Errorf(t, err, "lucid %s", strings.Join(args, " "))
			assert.Empty(t, stdout, "a refusal prints no receipt")
			assert.Contains(t, stderr, "lucid workout amend: "+tc.want, "the reason reaches stderr, prefixed")
			assert.Contains(t, stderr, "nothing was saved")
			assert.Equal(t, before, observationsLedgerSnapshot(t, home), "a refused amend writes nothing")
		})
	}

	folded := foldedCLIWorkout(t, home, id)
	assert.NotContains(t, folded.Payload, "rpe", "no refused amend reached the session")
	assert.Len(t, eventsOfKind(readObsEvents(t, home), observations.KindWorkout), 1,
		"the session and no correction")
}

// TestWorkoutAmend_CorrectionResolvesToBase proves any id in the chain is a
// valid target: amending a correction's own id resolves to the session it
// corrects, so the second correction also names the base (never the first
// correction), both fold onto the one session in order, and the --json view
// reports the base as the target with the first correction's value as `from`.
func TestWorkoutAmend_CorrectionResolvesToBase(t *testing.T) {
	home := enableWorkoutKinds(t)
	base := createWorkoutCLI(t, "--type", "climbing", "--duration", "60")

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "amend", base, "--rpe", "4", "--json")
	require.NoError(t, err)
	var first workoutAmendView
	require.NoError(t, json.Unmarshal([]byte(out), &first))
	require.Equal(t, base, first.TargetID)

	out, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "amend", first.EventID,
		"--rpe", "5", "--notes", "second pass", "--json")
	require.NoError(t, err)
	assert.Empty(t, stderr, "a correction id is a valid target, not a refusal")
	var second workoutAmendView
	require.NoError(t, json.Unmarshal([]byte(out), &second))
	assert.Equal(t, base, second.TargetID, "the correction's id resolves to its base session")
	assert.NotEqual(t, first.EventID, second.EventID)
	require.Contains(t, second.Changes, "rpe")
	assert.EqualValues(t, 4, second.Changes["rpe"].From, "from is the folded value after the first correction")
	assert.EqualValues(t, 5, second.Changes["rpe"].To)

	workouts := eventsOfKind(readObsEvents(t, home), observations.KindWorkout)
	require.Len(t, workouts, 3, "the session plus both corrections stay in history")
	for _, w := range workouts {
		if w.ID == base {
			assert.NotContains(t, w.Refs, observations.RefCorrects, "the session itself corrects nothing")
			continue
		}
		assert.Equal(t, base, w.Refs[observations.RefCorrects],
			"every correction names the base session, never another correction")
	}

	foldedAll := eventsOfKind(observations.FoldWorkoutAmendments(readObsEvents(t, home)), observations.KindWorkout)
	require.Len(t, foldedAll, 1, "both corrections fold onto one session")
	folded := foldedAll[0]
	assert.Equal(t, base, folded.ID)
	assert.EqualValues(t, 5, folded.Payload["rpe"], "the later correction wins")
	assert.Equal(t, "second pass", folded.Payload["note"])
	assert.Equal(t, "climbing", folded.Payload["type"], "an unpassed field keeps its value")
	assert.EqualValues(t, 60, folded.Payload["duration_min"], "an unpassed field keeps its value")
}

// TestWorkoutAmend_AnchorRefused proves an anchor-only capture (W-16) is not a
// session amend can correct — whether the anchor is a bare marker or carries
// items — and the refusal writes nothing; while a session that also recorded
// the anchor is still a session, and amends normally.
func TestWorkoutAmend_AnchorRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		log  []string
	}{
		{name: "bare anchor", log: []string{"--anchor"}},
		{name: "anchor items", log: []string{"--anchor-item", "squats:55", "--anchor-item", "core:50"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := enableWorkoutKinds(t)
			id := createWorkoutCLI(t, tc.log...)
			before := observationsLedgerSnapshot(t, home)

			stdout, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "amend", id, "--rpe", "4")
			require.Error(t, err)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr,
				"lucid workout amend: amend corrects logged sessions; anchors aren't amendable; nothing was saved")
			assert.Equal(t, before, observationsLedgerSnapshot(t, home), "a refused amend writes nothing")
		})
	}

	t.Run("session that also logged the anchor", func(t *testing.T) {
		home := enableWorkoutKinds(t)
		id := createWorkoutCLI(t, "--type", "push", "--anchor")

		_, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "amend", id, "--rpe", "4")
		require.NoError(t, err)
		assert.Empty(t, stderr)
		folded := foldedCLIWorkout(t, home, id)
		assert.EqualValues(t, 4, folded.Payload["rpe"])
		assert.Equal(t, true, folded.Payload["anchor"], "the anchor marker rides along untouched")
	})
}

// TestWorkoutAmend_ConfigGate proves a disabled workout kind gates amend
// exactly as it gates `workout log`: the enable hint on stdout, nothing on
// stderr, exit 0, and nothing written — even for a session that was logged
// while the kind was on.
func TestWorkoutAmend_ConfigGate(t *testing.T) {
	t.Run("kind disabled after logging", func(t *testing.T) {
		home := enableWorkoutKinds(t)
		id := createWorkoutCLI(t, "--type", "climbing")
		setObservationKindEnabled(t, home, observations.KindWorkout, false)
		before := observationsLedgerSnapshot(t, home)

		stdout, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "amend", id, "--rpe", "4")
		require.NoError(t, err, "a disabled kind is a graceful skip, not a failure")
		assert.Contains(t, stdout, "isn't enabled")
		assert.NotContains(t, stdout, "Amended", "no receipt for an amend that wrote nothing")
		assert.Empty(t, stderr)
		assert.Equal(t, before, observationsLedgerSnapshot(t, home), "a gated amend writes nothing")
	})

	t.Run("fresh ledger matches workout log", func(t *testing.T) {
		home := isolatedHome(t)

		logOut, _, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "log", "--type", "push")
		require.NoError(t, err)
		amendOut, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "amend", "obs_2026_01_15_001", "--rpe", "4")
		require.NoError(t, err)
		assert.Empty(t, stderr)
		assert.Equal(t, logOut, amendOut, "amend prints the same enable hint log does")
		assert.Empty(t, eventsOfKind(readObsEvents(t, home), observations.KindWorkout))
	})
}

// amendMonday is the synthetic Monday noon (UTC) the integration tests below
// pin the CLI clock to — the day the example program schedules its hard legs
// card — so they decide the same pick on every run, whatever the wall clock
// says.
func amendMonday() time.Time { return time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC) }

// workoutSurfaceJSON is the slice of the `lucid workout --json` projection the
// integration tests read back.
type workoutSurfaceJSON struct {
	Recommendation workout.Recommendation `json:"recommendation"`
	Trend          workout.Trend          `json:"trend"`
	Sessions       []workout.SessionView  `json:"sessions"`
}

// readWorkoutJSON runs `lucid workout --json` and decodes the projection.
func readWorkoutJSON(t *testing.T) workoutSurfaceJSON {
	t.Helper()
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "--json")
	require.NoError(t, err)
	var payload workoutSurfaceJSON
	require.NoError(t, json.Unmarshal([]byte(out), &payload))
	return payload
}

// enableAmendSurface sets up a fully configured workout home on the pinned
// Monday with an offline provider that is always down — the deterministic pick
// is identical either way, and --json never needs the model's note. It returns
// the home path.
func enableAmendSurface(t *testing.T) string {
	t.Helper()
	home := enableWorkoutSurface(t)
	withClock(t, amendMonday())
	withScriptedProvider(t).ExhaustErr = provider.ErrUnavailable
	return home
}

// ledgerLine returns the exact Ledger line holding the event with the given id
// from a byte snapshot, failing when no line carries it.
func ledgerLine(t *testing.T, files map[string]string, id string) string {
	t.Helper()
	needle := `"id":"` + id + `"`
	for _, content := range files {
		for _, line := range strings.Split(content, "\n") {
			if strings.Contains(line, needle) {
				return line
			}
		}
	}
	t.Fatalf("no Ledger line carries %s", id)
	return ""
}

// TestWorkoutAmend_EndToEnd is the whole correction cycle on a synthetic
// Ledger, through the CLI only: a session logged without an RPE and then
// amended to RPE 4 reads back through `workout --json` as exactly one session
// carrying RPE 4 — not a second session — while the session's original Ledger
// line stays byte-identical and the correction is appended after it.
func TestWorkoutAmend_EndToEnd(t *testing.T) {
	home := enableAmendSurface(t)
	id := createWorkoutCLI(t, "--type", "climbing", "--duration", "60")

	logged := readWorkoutJSON(t)
	require.Len(t, logged.Sessions, 1)
	assert.Nil(t, logged.Sessions[0].RPE, "the session was logged without an RPE")

	before := observationsLedgerSnapshot(t, home)
	original := ledgerLine(t, before, id)

	out, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "amend", id, "--rpe", "4")
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.Contains(t, out, "Amended workout `"+id+"`")

	after := observationsLedgerSnapshot(t, home)
	for path, content := range before {
		assert.True(t, strings.HasPrefix(after[path], content), "append-only: every existing byte of %s stays put", path)
	}
	assert.Equal(t, original, ledgerLine(t, after, id), "the session's original line is byte-identical")
	assert.Len(t, eventsOfKind(readObsEvents(t, home), observations.KindWorkout), 2,
		"history keeps both the session and its correction")

	amended := readWorkoutJSON(t)
	require.Len(t, amended.Sessions, 1, "one session — the correction folds on, it never double-counts")
	s := amended.Sessions[0]
	assert.Equal(t, id, s.ID)
	require.NotNil(t, s.RPE)
	assert.Equal(t, 4, *s.RPE, "workout --json shows the amended RPE")
	assert.Equal(t, "climbing", s.Type, "an unpassed field keeps its value")
	require.NotNil(t, s.DurationMin)
	assert.Equal(t, 60, *s.DurationMin, "an unpassed field keeps its value")
	assert.Equal(t, 1, amended.Trend.Sessions, "the trend counts the one session day")
	assert.Equal(t, 1, amended.Trend.ThisWeek)
}

// TestWorkoutAmend_Redate proves a `--day` re-date moves the session for every
// reader at once. A hard legs session logged on Monday puts legs inside its
// 48-hour recovery window, so Monday's legs card is vetoed; re-dated ten days
// back, the recovery guardrail (which reads occurred_at) clears legs and the
// trend (which reads logical_date) moves the session from this week to the
// prior one — the two agree because the fold moves the date trio together.
// The session's soreness reading is not relocated: v1 re-dates the workout
// event only (the body-state follow-up closes that gap).
func TestWorkoutAmend_Redate(t *testing.T) {
	home := enableAmendSurface(t)
	id := createWorkoutCLI(t, "--type", "legs", "--soreness", "forearms:3")

	control := readWorkoutJSON(t)
	require.NotEqual(t, "legs", control.Recommendation.Primary.ID,
		"today's hard legs session puts legs inside its recovery window")
	require.NotEmpty(t, control.Recommendation.Vetoes)
	require.Equal(t, 1, control.Trend.ThisWeek)
	require.Equal(t, 0, control.Trend.PriorWeek)

	_, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "amend", id, "--day", "2026-07-10")
	require.NoError(t, err)
	assert.Empty(t, stderr)

	res := readWorkoutJSON(t)
	// Recovery guardrail — reads occurred_at.
	assert.Equal(t, "legs", res.Recommendation.Primary.ID, "re-dated ten days back, legs is clear again")
	assert.Empty(t, res.Recommendation.Vetoes)
	// Progress trend — reads logical_date.
	assert.Equal(t, 1, res.Trend.Sessions, "still one session, not one per correction")
	assert.Equal(t, 0, res.Trend.ThisWeek, "the session left this week")
	assert.Equal(t, 1, res.Trend.PriorWeek, "and landed on its new day in the prior week")
	// The echo shows the moved date trio on the one session.
	require.Len(t, res.Sessions, 1)
	s := res.Sessions[0]
	assert.Equal(t, id, s.ID)
	assert.Equal(t, "2026-07-10", s.LogicalDate)
	assert.True(t, strings.HasPrefix(s.OccurredAt, "2026-07-10T"), "occurred_at moved with the day: %s", s.OccurredAt)
	assert.Equal(t, "legs", s.Type, "a re-date changes no field")

	// v1 limitation: the soreness reading the log wrote stays on the day it
	// was recorded — a re-date moves the workout event only.
	states := eventsOfKind(readObsEvents(t, home), observations.KindBodyState)
	require.Len(t, states, 1)
	assert.Equal(t, "2026-07-20", states[0].LogicalDate, "the body_state reading is not relocated")
	require.Len(t, res.Trend.BodyResponse, 1)
	assert.Equal(t, "forearms", res.Trend.BodyResponse[0].Part)
	assert.Equal(t, "2026-07-20", res.Trend.BodyResponse[0].AsOf, "the trend still reads the reading on its original day")
}
