package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/provider"
)

// The people characterization goldens freeze what every people surface prints
// today, byte for byte, so a later change to how a bare name resolves can prove
// that a Ledger it never touched still reads exactly the same. They are written
// against the code as it stands and are meant never to be edited: a later change
// that moves any of these bytes is a behavior change, not a stale golden.
//
// Two Ledgers are built entirely through the CLI, with a pinned clock, synthetic
// names only, and a scripted provider.Fake standing in for the structuring model:
//
//   - Ledger A has no first-name overlap: no single-word form held by exactly one
//     person that is also another person's first name. It does carry one form two
//     records share through aka[] ("A."), built by capturing an explicit
//     "X, aka Y" phrasing, but no later capture ever mentions that form bare.
//   - Ledger B holds "Sam" (seen first, mentioned more) and "Sam Rivera", both
//     current. Its reconcile output is pinned by goldenOverlapReconcileText /
//     goldenOverlapReconcileJSON, asserted by people_golden_overlap_pre_test.go.
//
// Every captured output is a transcript (exit code, stdout, stderr) with the
// Ledger's temp path replaced by $LUCID_HOME. Ledger bytes are pinned as a
// relative-path → sha256 map. Every read surface is followed by a check that the
// Ledger bytes did not move and that no identity/ tree appeared.
//
// The file is self-contained: it relies only on the cobra root (runRoot), the
// clock seam (withClock), the buildProvider seam with provider.Fake, an isolated
// LUCID_HOME under t.TempDir, and LUCID_REFUSE_REAL_HOME=1.

// goldenHomePlaceholder replaces the Ledger's temp path in every transcript.
const goldenHomePlaceholder = "$LUCID_HOME"

// goldenWallClock masks a value that `person create` stamps from the wall clock
// (it takes no clock seam), so the golden does not depend on the day it runs.
const goldenWallClock = "<wall-clock>"

// goldenMention is one people[] entry of a scripted structuring reply.
type goldenMention struct {
	DisplayName string   `json:"display_name"`
	Aka         []string `json:"aka,omitempty"`
}

// goldenExtractionReply is the scripted structuring reply shape.
type goldenExtractionReply struct {
	Emotions []goldenItem    `json:"emotions"`
	Themes   []goldenItem    `json:"themes"`
	People   []goldenMention `json:"people"`
	Notes    *string         `json:"notes"`
}

// goldenItem is one emotion of a scripted structuring reply.
type goldenItem struct {
	Name      string `json:"name"`
	Rationale string `json:"rationale"`
}

// goldenAt is an instant in the fixed golden month (UTC).
func goldenAt(month time.Month, day, hour, minute int) time.Time {
	return time.Date(2026, month, day, hour, minute, 0, 0, time.UTC)
}

// goldenEnv isolates the run: a fresh Ledger under t.TempDir, the real-home
// refusal, no update check, and no provenance inherited from the caller's env.
func goldenEnv(t *testing.T) string {
	t.Helper()
	t.Setenv("LUCID_REFUSE_REAL_HOME", "1")
	t.Setenv("LUCID_NO_UPDATE_CHECK", "1")
	for _, k := range []string{"LUCID_SOURCE", "LUCID_HARNESS", "LUCID_AGENT", "LUCID_MODEL", "LUCID_CHANNEL", "LUCID_THREAD"} {
		t.Setenv(k, "")
	}
	home := filepath.Join(t.TempDir(), ".lucid")
	t.Setenv("LUCID_HOME", home)
	// The day view scaffolds the whole Ledger, observations/ included, and writes
	// nothing else.
	withClock(t, goldenAt(time.June, 1, 8, 0))
	goldenRunOK(t, home, "day")
	goldenSeedObservations(t, home)
	return home
}

// goldenKeySalt replaces the random per-Ledger key_salt the scaffold generates,
// so the salted registry keys (a gratitude id) are the same on every run.
const goldenKeySalt = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

// goldenSeedObservations pins observations/config.json: the fixed key_salt, and
// the enable-gated memory kind switched on (there is no verb that enables a
// kind; the file is the documented switch).
func goldenSeedObservations(t *testing.T, home string) {
	t.Helper()
	path := filepath.Join(home, "observations", "config.json")
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(b, &cfg))
	cfg["key_salt"] = goldenKeySalt
	kinds, ok := cfg["kinds_enabled"].([]any)
	require.True(t, ok, "kinds_enabled")
	cfg["kinds_enabled"] = append(kinds, "memory")
	out, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, append(out, '\n'), 0o600))
}

// goldenRun executes one lucid command and returns its normalized transcript.
func goldenRun(t *testing.T, home string, args ...string) string {
	t.Helper()
	stdout, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, args...)
	return goldenTranscript(home, stdout, stderr, exitCodeForError(err))
}

// goldenRunOK executes one lucid command that must succeed and returns its
// normalized transcript.
func goldenRunOK(t *testing.T, home string, args ...string) string {
	t.Helper()
	stdout, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, args...)
	require.NoErrorf(t, err, "lucid %s: %s", strings.Join(args, " "), stderr)
	return goldenTranscript(home, stdout, stderr, ExitOK)
}

// goldenTranscript renders one command's exit code and streams, with the
// Ledger's temp path replaced so the transcript is machine-independent.
func goldenTranscript(home, stdout, stderr string, code int) string {
	s := fmt.Sprintf("exit %d\n--- stdout\n%s--- stderr\n%s", code, stdout, stderr)
	return strings.ReplaceAll(s, home, goldenHomePlaceholder)
}

// goldenLog captures one raw entry at the given instant and returns its id.
func goldenLog(t *testing.T, at time.Time, body string) string {
	t.Helper()
	withClock(t, at)
	stdout, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "log", body, "--json")
	require.NoErrorf(t, err, "lucid log: %s", stderr)
	var receipt map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &receipt))
	id, ok := receipt["receipt_id"].(string)
	require.Truef(t, ok, "log receipt: %s", stdout)
	return id
}

// goldenExtraction renders a well-formed structuring reply naming people.
func goldenExtraction(t *testing.T, people ...goldenMention) string {
	t.Helper()
	b, err := json.Marshal(goldenExtractionReply{
		Emotions: []goldenItem{{Name: "calm", Rationale: "'easy'"}},
		Themes:   []goldenItem{},
		People:   people,
	})
	require.NoError(t, err)
	return string(b)
}

// goldenStructure structures one raw entry at the given instant, with a
// provider.Fake scripted to name people, and returns the transcript. The
// package's buildProvider seam is restored before it returns, so no other verb
// in the run ever reaches the fake.
func goldenStructure(t *testing.T, home string, at time.Time, args []string, people ...goldenMention) string {
	t.Helper()
	withClock(t, at)
	fake := &provider.Fake{Script: []provider.Exchange{{Content: goldenExtraction(t, people...)}}}
	prev := buildProvider
	buildProvider = func(config.ProviderConfig) (provider.Provider, error) { return fake, nil }
	defer func() { buildProvider = prev }()
	out := goldenRunOK(t, home, append([]string{"structure"}, args...)...)
	require.Equal(t, 1, fake.Calls(), "one extraction call per structured entry")
	return out
}

// goldenPerson builds a scripted people[] entry.
func goldenPerson(name string, aka ...string) goldenMention {
	return goldenMention{DisplayName: name, Aka: aka}
}

// goldenTree hashes every file under home, keyed by slash-separated relative
// path.
func goldenTree(t *testing.T, home string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(home, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil || d.IsDir() {
			return werr
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		rel, rerr := filepath.Rel(home, path)
		if rerr != nil {
			return rerr
		}
		sum := sha256.Sum256(b)
		files[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	require.NoError(t, err)
	return files
}

// goldenTreeDiff reports the files whose bytes differ between two hash maps:
// the new hash for an added or changed file, "removed" for a deleted one.
func goldenTreeDiff(before, after map[string]string) map[string]string {
	diff := map[string]string{}
	for path, sum := range after {
		if before[path] != sum {
			diff[path] = sum
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			diff[path] = "removed"
		}
	}
	return diff
}

// goldenAssertReadOnly checks that a read left the Ledger byte-identical and
// created no identity/ tree.
func goldenAssertReadOnly(t *testing.T, home string, want map[string]string, step string) {
	t.Helper()
	assert.Equalf(t, want, goldenTree(t, home), "%s changed the Ledger", step)
	assert.NoDirExistsf(t, filepath.Join(home, "identity"), "%s created identity/", step)
}

// goldenCopyLedger copies a Ledger into a fresh temp home, points LUCID_HOME at
// it, and returns the copy's path, so a write can run on its own copy.
func goldenCopyLedger(t *testing.T, src string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), ".lucid")
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		return os.WriteFile(target, b, 0o600)
	})
	require.NoError(t, err)
	t.Setenv("LUCID_HOME", dst)
	return dst
}

// goldenWriteMedia writes one synthetic attachment and its sidecar straight onto
// disk, so a link can point a person at it without the wall-clock `attach` path.
func goldenWriteMedia(t *testing.T, home string) string {
	t.Helper()
	const id = "2026-07-05-photo.jpg"
	bin := []byte("\xff\xd8\xff synthetic jpeg bytes")
	sum := sha256.Sum256(bin)
	sidecar := fmt.Sprintf(`{
  "id": %q,
  "sha256": %q,
  "original_filename": "photo.jpg",
  "captured_at": "2026-07-05T18:41:39Z",
  "logical_day": "2026-07-05",
  "alt": null,
  "raw_entry_id": "raw_2026_07_05_18_41",
  "source": "cli"
}
`, id, hex.EncodeToString(sum[:]))
	dir := filepath.Join(home, "media", "2026", "07")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, id), bin, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, id+".json"), []byte(sidecar), 0o600))
	return id
}

// goldenMaskWallClock masks the seen-window dates `person create` stamps from
// the wall clock.
func goldenMaskWallClock(s string) string {
	re := regexp.MustCompile(`("(?:first|last)_seen_at": )"[0-9]{4}-[0-9]{2}-[0-9]{2}"`)
	return re.ReplaceAllString(s, `$1"`+goldenWallClock+`"`)
}

// goldenCheck compares one captured transcript with its golden.
func goldenCheck(t *testing.T, want map[string]string, name, got string) {
	t.Helper()
	exp, ok := want[name]
	if !assert.Truef(t, ok, "no golden named %q", name) {
		return
	}
	assert.Equalf(t, exp, got, "golden %q", name)
}

// TestPeopleGolden_UntouchedLedger freezes every people surface over Ledger A.
func TestPeopleGolden_UntouchedLedger(t *testing.T) {
	home := goldenEnv(t)
	want := goldenUntouchedOutputs()

	goldenBuildLedgerA(t, home, want)
	files := goldenTree(t, home)
	assert.Equal(t, goldenLedgerAFiles(), files, "Ledger A bytes after capture")

	goldenReadLedgerA(t, home, want, files)
	goldenCurateLedgerA(t, home, want, files)
}

// goldenBuildLedgerA captures Ledger A through the CLI: six structured entries
// (a mint, a fold, a nickname route, two explicit "X, aka Y" phrasings, and a
// --force re-run), then a dob, an off-limits mark, a cadence and a contact, a
// gratitude link and expression, a memory with people refs, and a person link.
func goldenBuildLedgerA(t *testing.T, home string, want map[string]string) {
	t.Helper()
	r1 := goldenLog(t, goldenAt(time.July, 1, 9, 0), "Coffee with Jordan Lee and Riley Moss before work.")
	goldenCheck(t, want, "structure/mint", goldenStructure(t, home, goldenAt(time.July, 1, 9, 5),
		[]string{r1}, goldenPerson("Jordan Lee"), goldenPerson("Riley Moss")))

	r2 := goldenLog(t, goldenAt(time.July, 2, 9, 0), "Riley Moss and Jordan Lee came by again.")
	goldenCheck(t, want, "structure/fold", goldenStructure(t, home, goldenAt(time.July, 2, 9, 5),
		[]string{r2, "--json"}, goldenPerson("Riley Moss"), goldenPerson("Jordan Lee")))

	r3 := goldenLog(t, goldenAt(time.July, 2, 18, 0), "Long walk with Michael Torres and Jordan Lee.")
	goldenCheck(t, want, "structure/mint-second", goldenStructure(t, home, goldenAt(time.July, 2, 18, 5),
		[]string{r3}, goldenPerson("Michael Torres"), goldenPerson("Jordan Lee")))

	r4 := goldenLog(t, goldenAt(time.July, 3, 9, 0), "Mike called about the trip.")
	goldenCheck(t, want, "structure/nickname-route", goldenStructure(t, home, goldenAt(time.July, 3, 9, 5),
		[]string{r4}, goldenPerson("Mike")))

	r5 := goldenLog(t, goldenAt(time.July, 3, 18, 0), "Dinner with Alex, aka A., and Jordan Lee.")
	goldenCheck(t, want, "structure/explicit-aka", goldenStructure(t, home, goldenAt(time.July, 3, 18, 5),
		[]string{r5}, goldenPerson("Alex", "A."), goldenPerson("Jordan Lee")))

	r6 := goldenLog(t, goldenAt(time.July, 4, 9, 0), "Andy, aka A., stopped by.")
	goldenCheck(t, want, "structure/explicit-aka-shared", goldenStructure(t, home, goldenAt(time.July, 4, 9, 5),
		[]string{r6}, goldenPerson("Andy", "A.")))

	goldenCheck(t, want, "structure/force-rerun", goldenStructure(t, home, goldenAt(time.July, 4, 9, 10),
		[]string{r2, "--force"}, goldenPerson("Riley Moss"), goldenPerson("Jordan Lee")))

	withClock(t, goldenAt(time.July, 4, 10, 0))
	goldenCheck(t, want, "setup/set-dob", goldenRunOK(t, home, "person", "set", "Jordan Lee", "--dob", "1990-07-08"))
	goldenCheck(t, want, "setup/off-limits", goldenRunOK(t, home, "person", "off-limits", "Riley Moss"))
	goldenCheck(t, want, "contact/every", goldenRunOK(t, home, "contact", "every", "Jordan Lee", "1w"))

	withClock(t, goldenAt(time.July, 4, 12, 0))
	goldenCheck(t, want, "contact/log", goldenRunOK(t, home, "contact", "Jordan Lee", "--via", "call"))

	withClock(t, goldenAt(time.July, 5, 20, 0))
	add := goldenRunOK(t, home, "gratitude", "add", "coffee with Jordan on the porch", "--person", "Jordan Lee", "--json")
	goldenCheck(t, want, "gratitude/add-person", add)
	grat := goldenJSONField(t, add, "id")
	goldenCheck(t, want, "gratitude/thank", goldenRunOK(t, home, "gratitude", "thank", grat, "--person", "Mike"))
	goldenCheck(t, want, "gratitude/add-person-human", goldenRunOK(t, home,
		"gratitude", "add", "a long walk by the river", "--person", "person_b-briar"))

	withClock(t, goldenAt(time.July, 5, 21, 0))
	goldenCheck(t, want, "memory/people", goldenRunOK(t, home,
		"memory", "The summer we fixed the porch.", "--people", "Jordan Lee,A.,Mike"))

	media := goldenWriteMedia(t, home)
	withClock(t, goldenAt(time.July, 5, 22, 0))
	goldenCheck(t, want, "link/person", goldenRunOK(t, home, "link", media, "--to", "person:person_s-gravel"))
}

// goldenReadFile returns one Ledger file's bytes as a string, so a golden can
// show which record a capture landed on.
func goldenReadFile(t *testing.T, home string, elem ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{home}, elem...)...))
	require.NoError(t, err)
	return string(b)
}

// goldenJSONField reads one string field from a transcript's JSON stdout.
func goldenJSONField(t *testing.T, transcript, field string) string {
	t.Helper()
	_, rest, ok := strings.Cut(transcript, "--- stdout\n")
	require.True(t, ok)
	body, _, ok := strings.Cut(rest, "--- stderr\n")
	require.True(t, ok)
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &m))
	v, ok := m[field].(string)
	require.Truef(t, ok, "field %q in %s", field, body)
	return v
}

// goldenReadLedgerA runs every read surface over Ledger A, each followed by the
// read-only check.
func goldenReadLedgerA(t *testing.T, home string, want, files map[string]string) {
	t.Helper()
	withClock(t, goldenAt(time.July, 8, 10, 0))
	reads := []struct {
		name string
		args []string
	}{
		{"person/single", []string{"person", "Jordan Lee"}},
		{"person/single-json", []string{"person", "Jordan Lee", "--json"}},
		{"person/aka-form", []string{"person", "Mike"}},
		{"person/no-match", []string{"person", "Nobody"}},
		{"person/no-match-json", []string{"person", "Nobody", "--json"}},
		{"person/shared-aka", []string{"person", "A."}},
		{"person/shared-aka-json", []string{"person", "A.", "--json"}},
		{"person/off-limits", []string{"person", "Riley Moss"}},
		{"person/off-limits-json", []string{"person", "Riley Moss", "--json"}},
		{"person/exact-key", []string{"person", "person_s-gravel"}},
		{"person/exact-key-json", []string{"person", "person_s-gravel", "--json"}},
		{"person/tombstone-key", []string{"person", "person_l-boar"}},
		{"person/tombstone-key-json", []string{"person", "person_l-boar", "--json"}},
		{"reconcile/text", []string{"person", "reconcile"}},
		{"reconcile/json", []string{"person", "reconcile", "--json"}},
		{"contact/due", []string{"contact", "due"}},
		{"contact/due-json", []string{"contact", "due", "--json"}},
		// Refused writes on the shared form: a subject that means two people is
		// never guessed, and the refusal writes nothing.
		{"contact/log-shared-aka", []string{"contact", "A.", "--via", "call"}},
		{"gratitude/add-shared-aka", []string{"gratitude", "add", "a quiet morning", "--person", "A."}},
	}
	for _, rd := range reads {
		goldenCheck(t, want, rd.name, goldenRun(t, home, rd.args...))
		goldenAssertReadOnly(t, home, files, rd.name)
	}

	t.Chdir(t.TempDir())
	goldenCheck(t, want, "validate/json", goldenRun(t, home, "validate", "--json"))
	goldenAssertReadOnly(t, home, files, "validate/json")
}

// goldenCurateLedgerA runs each curation verb on its own fresh copy of Ledger A,
// human and --json, pinning the ack and the bytes it changed.
func goldenCurateLedgerA(t *testing.T, home string, want, files map[string]string) {
	t.Helper()
	withClock(t, goldenAt(time.July, 8, 11, 0))
	verbs := []struct {
		name string
		args []string
	}{
		{"create-new", []string{"person", "create", "Casey"}},
		{"create-recorded", []string{"person", "create", "Jordan Lee"}},
		{"merge", []string{"person", "merge", "Andy", "Alex"}},
		{"alias", []string{"person", "alias", "Jordan Lee", "Jordan"}},
		{"rename", []string{"person", "rename", "Andy", "Dana"}},
		{"set", []string{"person", "set", "Michael Torres", "--relationship", "colleague", "--note", "met at the co-op"}},
		{"off-limits", []string{"person", "off-limits", "Michael Torres"}},
		{"off-limits-restore", []string{"person", "off-limits", "Riley Moss", "--restore"}},
		{"reject-p5", []string{"person", "set", "A.", "--note", "x"}},
		{"reject-p7", []string{"person", "alias", "Jordan Lee", "Alex"}},
	}
	for _, v := range verbs {
		for _, asJSON := range []bool{false, true} {
			name, args := "curate/"+v.name, v.args
			if asJSON {
				name, args = name+"-json", append(append([]string{}, v.args...), "--json")
			}
			copyHome := goldenCopyLedger(t, home)
			got := goldenRun(t, copyHome, args...)
			diff := goldenTreeDiff(files, goldenTree(t, copyHome))
			if v.name == "create-new" {
				got = goldenMaskWallClock(got)
				for path := range diff {
					diff[path] = goldenWallClock
				}
			}
			goldenCheck(t, want, name, got+goldenRenderDiff(diff))
			assert.NoDirExistsf(t, filepath.Join(copyHome, "identity"), "%s created identity/", name)
		}
	}
	t.Setenv("LUCID_HOME", home)
}

// goldenRenderDiff renders a tree diff as sorted "path sha" lines under a
// header, appended to a write's transcript.
func goldenRenderDiff(diff map[string]string) string {
	keys := make([]string, 0, len(diff))
	for k := range diff {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("--- changed\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "%s %s\n", k, diff[k])
	}
	return b.String()
}

// goldenBuildOverlapLedger builds Ledger B through the CLI: "Sam" captured
// twice, first, and "Sam Rivera" captured once, later. Both stay current.
func goldenBuildOverlapLedger(t *testing.T) string {
	t.Helper()
	home := goldenEnv(t)
	r1 := goldenLog(t, goldenAt(time.June, 1, 9, 0), "Lunch with Sam.")
	goldenStructure(t, home, goldenAt(time.June, 1, 9, 5), []string{r1}, goldenPerson("Sam"))
	r2 := goldenLog(t, goldenAt(time.June, 2, 9, 0), "Sam called after dinner.")
	goldenStructure(t, home, goldenAt(time.June, 2, 9, 5), []string{r2}, goldenPerson("Sam"))
	r3 := goldenLog(t, goldenAt(time.July, 1, 9, 0), "Met Sam Rivera at the co-op.")
	goldenStructure(t, home, goldenAt(time.July, 1, 9, 5), []string{r3}, goldenPerson("Sam Rivera"))
	return home
}

// TestPeopleGolden_OverlapLedgerOtherSurfaces freezes every people surface over
// Ledger B except person reconcile (pinned separately by the two
// goldenOverlapReconcile constants).
func TestPeopleGolden_OverlapLedgerOtherSurfaces(t *testing.T) {
	home := goldenBuildOverlapLedger(t)
	want := goldenOverlapOutputs()

	r4 := goldenLog(t, goldenAt(time.July, 2, 9, 0), "Sam stopped by with the ladder.")
	goldenCheck(t, want, "structure/bare-first-name", goldenStructure(t, home, goldenAt(time.July, 2, 9, 5),
		[]string{r4, "--json"}, goldenPerson("Sam")))
	goldenCheck(t, want, "processed/bare-first-name", goldenReadFile(t, home, "processed", r4+".json"))

	withClock(t, goldenAt(time.July, 2, 10, 0))
	goldenCheck(t, want, "contact/every-sam", goldenRunOK(t, home, "contact", "every", "Sam", "2w"))
	goldenCheck(t, want, "contact/every-sam-rivera", goldenRunOK(t, home, "contact", "every", "Sam Rivera", "1w"))
	withClock(t, goldenAt(time.July, 3, 12, 0))
	goldenCheck(t, want, "contact/log-sam", goldenRunOK(t, home, "contact", "Sam", "--via", "text"))
	withClock(t, goldenAt(time.July, 3, 20, 0))
	goldenCheck(t, want, "gratitude/add-sam", goldenRunOK(t, home,
		"gratitude", "add", "a ride to the market", "--person", "Sam", "--json"))
	goldenCheck(t, want, "memory/people-sam", goldenRunOK(t, home,
		"memory", "The day we fixed the fence.", "--people", "Sam"))

	files := goldenTree(t, home)
	assert.Equal(t, goldenLedgerBFiles(), files, "Ledger B bytes after capture")

	withClock(t, goldenAt(time.July, 8, 10, 0))
	reads := []struct {
		name string
		args []string
	}{
		{"person/sam", []string{"person", "Sam"}},
		{"person/sam-json", []string{"person", "Sam", "--json"}},
		{"person/sam-rivera", []string{"person", "Sam Rivera"}},
		{"person/sam-rivera-json", []string{"person", "Sam Rivera", "--json"}},
		{"contact/due", []string{"contact", "due"}},
		{"contact/due-json", []string{"contact", "due", "--json"}},
	}
	for _, rd := range reads {
		goldenCheck(t, want, rd.name, goldenRun(t, home, rd.args...))
		goldenAssertReadOnly(t, home, files, rd.name)
	}
}

// goldenOverlapReconcileText is `lucid person reconcile` over Ledger B today.
const goldenOverlapReconcileText = `Found 1 likely-duplicate pair — review each; nothing has changed:
- "Sam" (person_g-rye) and "Sam Rivera" (person_c-carp) — similar names ("samrivera" ~ "sam").
    → lucid person merge person_c-carp person_g-rye
`

// goldenOverlapReconcileJSON is `lucid person reconcile --json` over Ledger B
// today.
const goldenOverlapReconcileJSON = `{
  "candidates": [
    {
      "source_key": "person_c-carp",
      "source_display_name": "Sam Rivera",
      "target_key": "person_g-rye",
      "target_display_name": "Sam",
      "reason": "similar names (\"samrivera\" ~ \"sam\")",
      "suggested_merge": "lucid person merge person_c-carp person_g-rye"
    }
  ]
}
`

// goldenUntouchedOutputs holds Ledger A's transcripts.
func goldenUntouchedOutputs() map[string]string {
	return map[string]string{
		"structure/mint": `exit 0
--- stdout
raw_2026_07_01_09_00 → written
Structured 1 of 1 entry — 1 written, 0 skipped, 0 degraded, 0 failed.
--- stderr
`,
		"structure/fold": `exit 0
--- stdout
{
  "mode": "entry",
  "since": "",
  "until": "",
  "force": false,
  "attempted": 1,
  "written": 1,
  "skipped": 0,
  "degraded": 0,
  "failed": 0,
  "entries": [
    {
      "raw_id": "raw_2026_07_02_09_00",
      "outcome": "wrote"
    }
  ]
}
--- stderr
`,
		"structure/mint-second": `exit 0
--- stdout
raw_2026_07_02_18_00 → written
Structured 1 of 1 entry — 1 written, 0 skipped, 0 degraded, 0 failed.
--- stderr
`,
		"structure/nickname-route": `exit 0
--- stdout
raw_2026_07_03_09_00 → written
Structured 1 of 1 entry — 1 written, 0 skipped, 0 degraded, 0 failed.
--- stderr
`,
		"structure/explicit-aka": `exit 0
--- stdout
raw_2026_07_03_18_00 → written
Structured 1 of 1 entry — 1 written, 0 skipped, 0 degraded, 0 failed.
--- stderr
`,
		"structure/explicit-aka-shared": `exit 0
--- stdout
raw_2026_07_04_09_00 → written
Structured 1 of 1 entry — 1 written, 0 skipped, 0 degraded, 0 failed.
--- stderr
`,
		"structure/force-rerun": `exit 0
--- stdout
raw_2026_07_02_09_00 → written
Structured 1 of 1 entry — 1 written, 0 skipped, 0 degraded, 0 failed.
--- stderr
`,
		"setup/set-dob": `exit 0
--- stdout
Recorded on "Jordan Lee": dob 1990-07-08.
--- stderr
`,
		"setup/off-limits": `exit 0
--- stdout
"Riley Moss" is now off-limits to inference — the raw record is kept.
--- stderr
`,
		"contact/every": `exit 0
--- stdout
Jordan Lee: every week — next due by Jul 11 (contact_2026_07_04_a).
--- stderr
`,
		"contact/log": `exit 0
--- stdout
Logged contact with Jordan Lee on 2026-07-04 (contact_2026_07_04_b).
--- stderr
`,
		"gratitude/add-person": `exit 0
--- stdout
{
  "receipt": "grat_2026_07_05_001",
  "id": "gratitude_r-cave",
  "thing": "coffee with Jordan on the porch",
  "count": 1,
  "first": "2026-07-05",
  "last": "2026-07-05",
  "created": true,
  "person_key": "person_s-gravel"
}
--- stderr
`,
		"gratitude/thank":            "exit 0\n--- stdout\nNoted that you told Michael Torres about \"coffee with Jordan on the porch\" (2026-07-05) as `grat_2026_07_05_002` — the tally stays ×1.\n--- stderr\n",
		"gratitude/add-person-human": "exit 0\n--- stdout\nStarted tally for \"a long walk by the river\" (×1) as `grat_2026_07_05_003`. Linked to Michael Torres.\n--- stderr\n",
		"memory/people":              "exit 0\n--- stdout\nLogged memory as `obs_2026_07_05_001`.\n--- stderr\n",
		"link/person": `exit 0
--- stdout
2026-07-05-photo.jpg: Linked to person:person_s-gravel.
--- stderr
`,
		"person/single": `exit 0
--- stdout
Jordan Lee
Mentioned in 4 entries (first seen 2026-07-01, last seen 2026-07-03).
Check-ins: every week — last caught up 2026-07-04
No accepted insights reference them yet.
Jordan Lee appears in 67% of entries — worth a look, or expected?
Media linked to them: 2026-07-05-photo.jpg
--- stderr
`,
		"person/single-json": `exit 0
--- stdout
{
  "query": "Jordan Lee",
  "matched": true,
  "multiple_matches": false,
  "candidates": [],
  "off_limits": false,
  "person_key": "person_s-gravel",
  "contact": {
    "state": "active",
    "cadence": "1w",
    "cadence_days": 7,
    "last_contact": "2026-07-04"
  },
  "text": "Jordan Lee\nMentioned in 4 entries (first seen 2026-07-01, last seen 2026-07-03).\nCheck-ins: every week — last caught up 2026-07-04\nNo accepted insights reference them yet.\nJordan Lee appears in 67% of entries — worth a look, or expected?\nMedia linked to them: 2026-07-05-photo.jpg"
}
--- stderr
`,
		"person/aka-form": `exit 0
--- stdout
Michael Torres
Mentioned in 2 entries (first seen 2026-07-02, last seen 2026-07-03).
No accepted insights reference them yet.
--- stderr
`,
		"person/no-match": `exit 0
--- stdout
No one by that name yet — people appear here as you mention them.
--- stderr
`,
		"person/no-match-json": `exit 0
--- stdout
{
  "query": "Nobody",
  "matched": false,
  "multiple_matches": false,
  "candidates": [],
  "off_limits": false,
  "person_key": "",
  "text": "No one by that name yet — people appear here as you mention them."
}
--- stderr
`,
		"person/shared-aka": `exit 0
--- stdout
That matches more than one person — which did you mean: Alex (first seen 2026-07-03); Andy (first seen 2026-07-04)?
--- stderr
`,
		"person/shared-aka-json": `exit 0
--- stdout
{
  "query": "A.",
  "matched": false,
  "multiple_matches": true,
  "candidates": [
    {
      "person_key": "person_c-marsh",
      "display_name": "Alex",
      "first_seen_at": "2026-07-03"
    },
    {
      "person_key": "person_h-brine",
      "display_name": "Andy",
      "first_seen_at": "2026-07-04"
    }
  ],
  "off_limits": false,
  "person_key": "",
  "text": "That matches more than one person — which did you mean: Alex (first seen 2026-07-03); Andy (first seen 2026-07-04)?"
}
--- stderr
`,
		"person/off-limits": `exit 0
--- stdout
Riley Moss is off-limits to inference — what follows is your raw record only: mentions and dates, nothing derived.
Mentioned in 2 entries (first seen 2026-07-01, last seen 2026-07-02).
Entries: raw_2026_07_01_09_00, raw_2026_07_02_09_00
--- stderr
`,
		"person/off-limits-json": `exit 0
--- stdout
{
  "query": "Riley Moss",
  "matched": true,
  "multiple_matches": false,
  "candidates": [],
  "off_limits": true,
  "person_key": "person_s-alder",
  "text": "Riley Moss is off-limits to inference — what follows is your raw record only: mentions and dates, nothing derived.\nMentioned in 2 entries (first seen 2026-07-01, last seen 2026-07-02).\nEntries: raw_2026_07_01_09_00, raw_2026_07_02_09_00"
}
--- stderr
`,
		"person/exact-key": `exit 0
--- stdout
Jordan Lee
Mentioned in 4 entries (first seen 2026-07-01, last seen 2026-07-03).
Check-ins: every week — last caught up 2026-07-04
No accepted insights reference them yet.
Jordan Lee appears in 67% of entries — worth a look, or expected?
Media linked to them: 2026-07-05-photo.jpg
--- stderr
`,
		"person/exact-key-json": `exit 0
--- stdout
{
  "query": "person_s-gravel",
  "matched": true,
  "multiple_matches": false,
  "candidates": [],
  "off_limits": false,
  "person_key": "person_s-gravel",
  "contact": {
    "state": "active",
    "cadence": "1w",
    "cadence_days": 7,
    "last_contact": "2026-07-04"
  },
  "text": "Jordan Lee\nMentioned in 4 entries (first seen 2026-07-01, last seen 2026-07-03).\nCheck-ins: every week — last caught up 2026-07-04\nNo accepted insights reference them yet.\nJordan Lee appears in 67% of entries — worth a look, or expected?\nMedia linked to them: 2026-07-05-photo.jpg"
}
--- stderr
`,
		"person/tombstone-key": `exit 0
--- stdout
Alex
Mentioned in 1 entry (first seen 2026-07-03, last seen 2026-07-03).
No accepted insights reference them yet.
--- stderr
`,
		"person/tombstone-key-json": `exit 0
--- stdout
{
  "query": "person_l-boar",
  "matched": true,
  "multiple_matches": false,
  "candidates": [],
  "off_limits": false,
  "person_key": "person_c-marsh",
  "text": "Alex\nMentioned in 1 entry (first seen 2026-07-03, last seen 2026-07-03).\nNo accepted insights reference them yet."
}
--- stderr
`,
		"reconcile/text": `exit 0
--- stdout
Found 1 likely-duplicate pair — review each; nothing has changed:
- "Alex" (person_c-marsh) and "Andy" (person_h-brine) — share the written form "a".
    → lucid person merge person_h-brine person_c-marsh
--- stderr
`,
		"reconcile/json": `exit 0
--- stdout
{
  "candidates": [
    {
      "source_key": "person_h-brine",
      "source_display_name": "Andy",
      "target_key": "person_c-marsh",
      "target_display_name": "Alex",
      "reason": "share the written form \"a\"",
      "suggested_merge": "lucid person merge person_h-brine person_c-marsh"
    }
  ]
}
--- stderr
`,
		"contact/due": `exit 0
--- stdout
Week of Jul 6 – Jul 12

Due this week
- Jordan Lee (every week), last caught up Jul 4

Birthdays this week
- Jordan Lee — Wed Jul 8
--- stderr
`,
		"contact/due-json": `exit 0
--- stdout
{
  "day": "2026-07-08",
  "week": "2026-W28",
  "week_start": "2026-07-06",
  "week_end": "2026-07-12",
  "wrap": false,
  "due_this_week": [
    {
      "person_key": "person_s-gravel",
      "display_name": "Jordan Lee",
      "cadence": "1w",
      "cadence_days": 7,
      "last_contact": "2026-07-04",
      "window_closes": "2026-07-11"
    }
  ],
  "still_open": [],
  "coming_up": [],
  "birthdays": [
    {
      "person_key": "person_s-gravel",
      "display_name": "Jordan Lee",
      "date": "2026-07-08"
    }
  ]
}
--- stderr
`,
		"contact/log-shared-aka": `exit 1
--- stdout
--- stderr
"A." matches more than one person — disambiguate with a person_key (person_c-marsh / person_h-brine); nothing was saved.
`,
		"gratitude/add-shared-aka": `exit 1
--- stdout
--- stderr
"A." matches more than one person — disambiguate with a person_key (person_c-marsh / person_h-brine); nothing was saved.
`,
		"validate/json": `exit 0
--- stdout
{
  "ok": true,
  "ran": [
    "schema",
    "people-redirects",
    "anchors"
  ],
  "skipped": [
    "repo checks (no checkout found)"
  ],
  "errors": 0,
  "warnings": 0,
  "findings": null,
  "ledger": "$LUCID_HOME"
}
--- stderr
`,
		"curate/create-new": `exit 0
--- stdout
Recorded "Casey".
--- stderr
--- changed
people/person_c-duck.json <wall-clock>
`,
		"curate/create-new-json": `exit 0
--- stdout
{
  "person_key": "person_c-duck",
  "display_name": "Casey",
  "aka": [
    "Casey"
  ],
  "first_seen_at": "<wall-clock>",
  "last_seen_at": "<wall-clock>",
  "entry_refs": [],
  "notes": null,
  "ack": "Recorded \"Casey\"."
}
--- stderr
--- changed
people/person_c-duck.json <wall-clock>
`,
		"curate/create-recorded": `exit 0
--- stdout
"Jordan Lee" is already recorded — nothing to create.
--- stderr
--- changed
`,
		"curate/create-recorded-json": `exit 0
--- stdout
{
  "person_key": "person_s-gravel",
  "display_name": "Jordan Lee",
  "aka": [
    "Jordan Lee"
  ],
  "first_seen_at": "2026-07-01",
  "last_seen_at": "2026-07-03",
  "entry_refs": [
    "raw_2026_07_01_09_00",
    "raw_2026_07_02_09_00",
    "raw_2026_07_02_18_00",
    "raw_2026_07_03_18_00"
  ],
  "dob": "1990-07-08",
  "notes": null,
  "ack": "\"Jordan Lee\" is already recorded — nothing to create."
}
--- stderr
--- changed
`,
		"curate/merge": `exit 0
--- stdout
Merged "Andy" into "Alex" — 2 entries now resolve to one record.
--- stderr
--- changed
people/person_c-marsh.json f6ae49577b7e35e72d9f830221734f6731ffa6e2b310d34cced134cdf1412b58
people/person_h-brine.json ed732dc4b599aff11dc86a0e56cf97ffde69df74c23eb762aac9d35cb1446654
`,
		"curate/merge-json": `exit 0
--- stdout
{
  "person_key": "person_c-marsh",
  "display_name": "Alex",
  "aka": [
    "A.",
    "Alex",
    "Andy"
  ],
  "first_seen_at": "2026-07-03",
  "last_seen_at": "2026-07-04",
  "entry_refs": [
    "raw_2026_07_03_18_00",
    "raw_2026_07_04_09_00"
  ],
  "notes": null,
  "ack": "Merged \"Andy\" into \"Alex\" — 2 entries now resolve to one record."
}
--- stderr
--- changed
people/person_c-marsh.json f6ae49577b7e35e72d9f830221734f6731ffa6e2b310d34cced134cdf1412b58
people/person_h-brine.json ed732dc4b599aff11dc86a0e56cf97ffde69df74c23eb762aac9d35cb1446654
`,
		"curate/alias": `exit 0
--- stdout
Recorded "Jordan" as another form of "Jordan Lee".
--- stderr
--- changed
people/person_g-gate.json f8f285104d4ffdad26c8c716074a556976699df79a7d216e4df783c109bac188
people/person_s-gravel.json bf06d4d2c7cdf334f48eec52973dd9e7b89d3761273d927efdfdfbaabdacc671
`,
		"curate/alias-json": `exit 0
--- stdout
{
  "person_key": "person_s-gravel",
  "display_name": "Jordan Lee",
  "aka": [
    "Jordan",
    "Jordan Lee"
  ],
  "first_seen_at": "2026-07-01",
  "last_seen_at": "2026-07-03",
  "entry_refs": [
    "raw_2026_07_01_09_00",
    "raw_2026_07_02_09_00",
    "raw_2026_07_02_18_00",
    "raw_2026_07_03_18_00"
  ],
  "dob": "1990-07-08",
  "notes": null,
  "ack": "Recorded \"Jordan\" as another form of \"Jordan Lee\"."
}
--- stderr
--- changed
people/person_g-gate.json f8f285104d4ffdad26c8c716074a556976699df79a7d216e4df783c109bac188
people/person_s-gravel.json bf06d4d2c7cdf334f48eec52973dd9e7b89d3761273d927efdfdfbaabdacc671
`,
		"curate/rename": `exit 0
--- stdout
Renamed "Andy" to "Dana" — the person_key is unchanged, and the old form is kept.
--- stderr
--- changed
people/person_h-brine.json a463113392cd82defa8c786c1b060807c7332e120cc9e85dc58b6f5d02716413
people/person_m-butte.json 2f6fb6d55fb12a74d05aa7989f432700c822d3994b2b0dc647468ace0db88f98
`,
		"curate/rename-json": `exit 0
--- stdout
{
  "person_key": "person_h-brine",
  "display_name": "Dana",
  "aka": [
    "A.",
    "Andy",
    "Dana"
  ],
  "first_seen_at": "2026-07-04",
  "last_seen_at": "2026-07-04",
  "entry_refs": [
    "raw_2026_07_04_09_00"
  ],
  "notes": null,
  "ack": "Renamed \"Andy\" to \"Dana\" — the person_key is unchanged, and the old form is kept."
}
--- stderr
--- changed
people/person_h-brine.json a463113392cd82defa8c786c1b060807c7332e120cc9e85dc58b6f5d02716413
people/person_m-butte.json 2f6fb6d55fb12a74d05aa7989f432700c822d3994b2b0dc647468ace0db88f98
`,
		"curate/set": `exit 0
--- stdout
Recorded on "Michael Torres": relationship colleague, note met at the co-op.
--- stderr
--- changed
people/person_b-briar.json d0906ebaa0bad64836f9ee8ff4c9cfc9e326c375c92f8d4c2d3a6dbccbb7c1c9
`,
		"curate/set-json": `exit 0
--- stdout
{
  "person_key": "person_b-briar",
  "display_name": "Michael Torres",
  "aka": [
    "Michael Torres",
    "Mike"
  ],
  "first_seen_at": "2026-07-02",
  "last_seen_at": "2026-07-03",
  "entry_refs": [
    "raw_2026_07_02_18_00",
    "raw_2026_07_03_09_00"
  ],
  "relationship": "colleague",
  "notes": "met at the co-op",
  "ack": "Recorded on \"Michael Torres\": relationship colleague, note met at the co-op."
}
--- stderr
--- changed
people/person_b-briar.json d0906ebaa0bad64836f9ee8ff4c9cfc9e326c375c92f8d4c2d3a6dbccbb7c1c9
`,
		"curate/off-limits": `exit 0
--- stdout
"Michael Torres" is now off-limits to inference — the raw record is kept.
--- stderr
--- changed
off_limits.json 0095fa765e7ed5370febd322ed5800f6405a5eb1927ce29434c835cb5aec1e61
`,
		"curate/off-limits-json": `exit 0
--- stdout
{
  "person_key": "person_b-briar",
  "display_name": "Michael Torres",
  "aka": [
    "Michael Torres",
    "Mike"
  ],
  "first_seen_at": "2026-07-02",
  "last_seen_at": "2026-07-03",
  "entry_refs": [
    "raw_2026_07_02_18_00",
    "raw_2026_07_03_09_00"
  ],
  "notes": null,
  "off_limits": true,
  "ack": "\"Michael Torres\" is now off-limits to inference — the raw record is kept."
}
--- stderr
--- changed
off_limits.json 0095fa765e7ed5370febd322ed5800f6405a5eb1927ce29434c835cb5aec1e61
`,
		"curate/off-limits-restore": `exit 0
--- stdout
"Riley Moss" is no longer off-limits to inference.
--- stderr
--- changed
off_limits.json baacd432238417c49b1a0bdec2f995de22ec594cb490b7888341f4c32f5be0d3
`,
		"curate/off-limits-restore-json": `exit 0
--- stdout
{
  "person_key": "person_s-alder",
  "display_name": "Riley Moss",
  "aka": [
    "Riley Moss"
  ],
  "first_seen_at": "2026-07-01",
  "last_seen_at": "2026-07-02",
  "entry_refs": [
    "raw_2026_07_01_09_00",
    "raw_2026_07_02_09_00"
  ],
  "notes": null,
  "off_limits": false,
  "ack": "\"Riley Moss\" is no longer off-limits to inference."
}
--- stderr
--- changed
off_limits.json baacd432238417c49b1a0bdec2f995de22ec594cb490b7888341f4c32f5be0d3
`,
		"curate/reject-p5": `exit 1
--- stdout
--- stderr
"A." matches more than one person — disambiguate with a person_key (person_c-marsh / person_h-brine); nothing was saved.
--- changed
`,
		"curate/reject-p5-json": `exit 1
--- stdout
--- stderr
"A." matches more than one person — disambiguate with a person_key (person_c-marsh / person_h-brine); nothing was saved.
--- changed
`,
		"curate/reject-p7":      "exit 1\n--- stdout\n--- stderr\nthe form \"Alex\" already belongs to another person — use `lucid person merge` if they are the same; nothing was saved.\n--- changed\n",
		"curate/reject-p7-json": "exit 1\n--- stdout\n--- stderr\nthe form \"Alex\" already belongs to another person — use `lucid person merge` if they are the same; nothing was saved.\n--- changed\n",
	}
}

// goldenLedgerAFiles holds Ledger A's bytes after capture.
func goldenLedgerAFiles() map[string]string {
	return map[string]string{
		"contacts/2026/07/contact_2026_07_04.jsonl":  "e7be083b5d0bd61020e6dfe3b5562324a4cc1b2ac3e30292602d60227ab064f8",
		"engine/anchors.json":                        "c5b85b9fe18c71597fcc084fe9a80fc1eaa0f8766efd8ae1d8c6b0c4dd60179c",
		"engine/chain.json":                          "5ca68d28225ac3f690aafa1236fb36e1a6e161f9248377bf7f6049c7bd9754df",
		"engine/profile.json":                        "bb6568fd1f4c08bdd740b9bc7dccb5f29bc4117eaae97458970916b77310e350",
		"engine/self.json":                           "0e2909b920b951ed1927f30740ca7389b7f97c0a67ae551b8c94f436708d09e9",
		"engine/storm.json":                          "989830f3a36667a054009e7788ab90c9517b068369403419c51b6fb8c19473fd",
		"engine/witness.json":                        "fa84d726be6cd120ed94f7ce2ac869542240f5246743c1637783ea5a41bd84f2",
		"insights/.keep":                             "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"links/links.jsonl":                          "bea53299cbc50312720a23246fd3161ff58ebb7066384e00896dee6c6c5eaad5",
		"lucid.json":                                 "ff4009f14724698a5669e3f1a498619340f44f3ac677cdc46a1d7def1bf3e00a",
		"media/2026/07/2026-07-05-photo.jpg":         "1ad209fc1b78306bcb3cd55714fdb7f7a834cffa014fbe3c0c7eb911b5933752",
		"media/2026/07/2026-07-05-photo.jpg.json":    "af21ea706c8fa9d54b8cdeddd3f608879649400683acee6b03e36c805c35d63d",
		"observations/2026/07/obs_2026_07_05.jsonl":  "afc6dd95a7d228e99490cd117071112faf49a1cd67046844c7384744fa617fde",
		"observations/config.json":                   "314fc9176f49c52bcb03b80a5f2d1fd145be67ec3133631f4ac773c7de6c9700",
		"off_limits.json":                            "f223fc6ca95a80a462f9f9b0d67761444057e0139bd631c77850686889727857",
		"people/.keep":                               "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"people/person_b-briar.json":                 "49675b54534dcfae0285ebdf17a46399762b64a8e0c3e911270bd4363770fb75",
		"people/person_c-marsh.json":                 "ff3a22822e70c064c6fd413b26ed96672aa23d93af0f4ed6ecb04685dcd2d276",
		"people/person_h-brine.json":                 "d93dadfb576f0e1c4e664048ab2beda871c456b1366737f3da9f7107190b6d8c",
		"people/person_l-boar.json":                  "3a581e3c9155ace5761b947ecf6006c8e84bee7d6d9eff9e4b2c9e9ccc4cabd7",
		"people/person_o-steel.json":                 "8fe54a48b5460534e30655ee642e895dbc1fad832fc5de6d4e727e5fcf2b9f60",
		"people/person_s-alder.json":                 "9c41d54f6a53f15bdea931cff365358ca6626e5a5a38bba8ec8c3d4d10de9922",
		"people/person_s-gravel.json":                "da762f1560690c5b3479c3b33d57f23dd192240b7f6fb2d927fce5b4f358253d",
		"processed/.keep":                            "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"processed/raw_2026_07_01_09_00.json":        "a68be69e6efc62aaf16b758b213eda6c7e9d2b6cd69b64045ad1e5dbc7b4b8fc",
		"processed/raw_2026_07_02_09_00.json":        "e9b48d78751e3d5eb0fd46500319590dc1059b013139bbc0cc81e24e11bc3c2a",
		"processed/raw_2026_07_02_18_00.json":        "2e99b5610cceda1de8934aa3025d4581e44a92e75bd912b98f025f5410cca2ce",
		"processed/raw_2026_07_03_09_00.json":        "bc918b8a14fee45bc53c98697f28b937341593a1cb0ce82a4a85c7b7f716ce11",
		"processed/raw_2026_07_03_18_00.json":        "63ec92894c7fc4d9070d224935f6fcc70a4f8d70eb2c888575abe5ba708bc020",
		"processed/raw_2026_07_04_09_00.json":        "526f0843f17042119bdbecc704b578359e9e4452f256e0ab4ec42f3082884274",
		"raw/.keep":                                  "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"raw/2026/07/raw_2026_07_01_09_00.md":        "72301696ba4be33b8dc44f5b7c700156e6dbb0dcc6fb14913a127d347e936199",
		"raw/2026/07/raw_2026_07_02_09_00.md":        "972baefa0fea5b6cef4f01d6e0890a34db6cab82df21e7ebc78147dab5c35c66",
		"raw/2026/07/raw_2026_07_02_18_00.md":        "3f8772e85705f58325d9fb4775f5a43efa8983af03b90fb52a1a48e258ad15c0",
		"raw/2026/07/raw_2026_07_03_09_00.md":        "0c80323692a2bd42b8acea02c755c7575cdb5f85094cbf179199a7a0b71c721e",
		"raw/2026/07/raw_2026_07_03_18_00.md":        "568c780288010ecd045cdb84855fbb4602e7710fc324d07d6393054eff13d519",
		"raw/2026/07/raw_2026_07_04_09_00.md":        "6e2281ef6f02acbabde211f76bc4bfb6d4459e1dbe56914f1a44221fd7b04db0",
		"reflections/.keep":                          "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"registries/gratitude/gratitude_m-eel.json":  "0e39f1720b22da47191e7def4bf482238c6aec1c14df7b52e7b60bceda0f8743",
		"registries/gratitude/gratitude_r-cave.json": "e84160eaf8d79df6875931d626c063c4c63cfc05605c350a006e75e881b7d1b8",
		"sessions/.keep":                             "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"sessions/session_2026_07_01_09_00.json":     "a700d2cf89600d78aba0d24a52fadc75f00bf30dfacdfc341ddaeca94d2f80cc",
		"sessions/session_2026_07_02_09_00.json":     "b3d7999643a3d21c3c8d9dddf1f492e7b252305beee012af37a9c8241da4c910",
		"sessions/session_2026_07_02_18_00.json":     "57ff083b8305b63c6b045d4ecafa01a0c5b2968b1defa139d5b77d898ea536f5",
		"sessions/session_2026_07_03_09_00.json":     "4ff1c6ee4ae2592a601a3ecb722cda9592c3b7f86fcbd855cb24f77b41f4be35",
		"sessions/session_2026_07_03_18_00.json":     "03e7f808c3c456d3f54395872f96cf98cbff65e92a081945db46ea92fb1ab6f4",
		"sessions/session_2026_07_04_09_00.json":     "d1d4192bfc42dce37bc322c98da7cb2dcb4e111c941f1e16cd6f463485dfba11",
	}
}

// goldenOverlapOutputs holds Ledger B's transcripts.
func goldenOverlapOutputs() map[string]string {
	return map[string]string{
		"structure/bare-first-name": `exit 0
--- stdout
{
  "mode": "entry",
  "since": "",
  "until": "",
  "force": false,
  "attempted": 1,
  "written": 1,
  "skipped": 0,
  "degraded": 0,
  "failed": 0,
  "entries": [
    {
      "raw_id": "raw_2026_07_02_09_00",
      "outcome": "wrote"
    }
  ]
}
--- stderr
`,
		"processed/bare-first-name": `{
  "id": "raw_2026_07_02_09_00",
  "entry_id": "raw_2026_07_02_09_00",
  "produced_at": "2026-07-02T09:05:00Z",
  "agent_version": "structuring-2026.05.0",
  "emotions": [
    {
      "name": "calm",
      "rationale": "'easy'"
    }
  ],
  "themes": [],
  "people": [
    {
      "display_name": "Sam",
      "person_key": "person_g-rye",
      "first_mention": false
    }
  ],
  "notes": null,
  "rejected_proposals": [],
  "unanswered_proposals": []
}
`,
		"contact/every-sam": `exit 0
--- stdout
Sam: every 2 weeks — next due by Jul 16 (contact_2026_07_02_a).
--- stderr
`,
		"contact/every-sam-rivera": `exit 0
--- stdout
Sam Rivera: every week — next due by Jul 9 (contact_2026_07_02_b).
--- stderr
`,
		"contact/log-sam": `exit 0
--- stdout
Logged contact with Sam on 2026-07-03 (contact_2026_07_03_a).
--- stderr
`,
		"gratitude/add-sam": `exit 0
--- stdout
{
  "receipt": "grat_2026_07_03_001",
  "id": "gratitude_o-tan",
  "thing": "a ride to the market",
  "count": 1,
  "first": "2026-07-03",
  "last": "2026-07-03",
  "created": true,
  "person_key": "person_g-rye"
}
--- stderr
`,
		"memory/people-sam": "exit 0\n--- stdout\nLogged memory as `obs_2026_07_03_001`.\n--- stderr\n",
		"person/sam": `exit 0
--- stdout
Sam
Mentioned in 3 entries (first seen 2026-06-01, last seen 2026-07-02).
Check-ins: every 2 weeks — last caught up 2026-07-03
No accepted insights reference them yet.
Sam appears in 75% of entries — worth a look, or expected?
--- stderr
`,
		"person/sam-json": `exit 0
--- stdout
{
  "query": "Sam",
  "matched": true,
  "multiple_matches": false,
  "candidates": [],
  "off_limits": false,
  "person_key": "person_g-rye",
  "contact": {
    "state": "active",
    "cadence": "2w",
    "cadence_days": 14,
    "last_contact": "2026-07-03"
  },
  "text": "Sam\nMentioned in 3 entries (first seen 2026-06-01, last seen 2026-07-02).\nCheck-ins: every 2 weeks — last caught up 2026-07-03\nNo accepted insights reference them yet.\nSam appears in 75% of entries — worth a look, or expected?"
}
--- stderr
`,
		"person/sam-rivera": `exit 0
--- stdout
Sam Rivera
Mentioned in 1 entry (first seen 2026-07-01, last seen 2026-07-01).
Check-ins: every week — not logged yet
No accepted insights reference them yet.
--- stderr
`,
		"person/sam-rivera-json": `exit 0
--- stdout
{
  "query": "Sam Rivera",
  "matched": true,
  "multiple_matches": false,
  "candidates": [],
  "off_limits": false,
  "person_key": "person_c-carp",
  "contact": {
    "state": "active",
    "cadence": "1w",
    "cadence_days": 7,
    "last_contact": null
  },
  "text": "Sam Rivera\nMentioned in 1 entry (first seen 2026-07-01, last seen 2026-07-01).\nCheck-ins: every week — not logged yet\nNo accepted insights reference them yet."
}
--- stderr
`,
		"contact/due": `exit 0
--- stdout
Week of Jul 6 – Jul 12

Due this week
- Sam Rivera (every week), not logged yet

Coming up next week
- Sam (every 2 weeks), last caught up Jul 3
--- stderr
`,
		"contact/due-json": `exit 0
--- stdout
{
  "day": "2026-07-08",
  "week": "2026-W28",
  "week_start": "2026-07-06",
  "week_end": "2026-07-12",
  "wrap": false,
  "due_this_week": [
    {
      "person_key": "person_c-carp",
      "display_name": "Sam Rivera",
      "cadence": "1w",
      "cadence_days": 7,
      "last_contact": null,
      "window_closes": "2026-07-09"
    }
  ],
  "still_open": [],
  "coming_up": [
    {
      "person_key": "person_g-rye",
      "display_name": "Sam",
      "cadence": "2w",
      "cadence_days": 14,
      "last_contact": "2026-07-03",
      "window_closes": "2026-07-17"
    }
  ],
  "birthdays": []
}
--- stderr
`,
	}
}

// goldenLedgerBFiles holds Ledger B's bytes after capture.
func goldenLedgerBFiles() map[string]string {
	return map[string]string{
		"contacts/2026/07/contact_2026_07_02.jsonl": "edcad2918fba68ecfb159a6a59bee301840aa4a57a62d52bdf008e0953b26191",
		"contacts/2026/07/contact_2026_07_03.jsonl": "7f26bb3a49ef5b1e94f2b6454e793d86e02aaf64e93dc089a59077aeef8a624d",
		"engine/anchors.json":                       "c5b85b9fe18c71597fcc084fe9a80fc1eaa0f8766efd8ae1d8c6b0c4dd60179c",
		"engine/chain.json":                         "5ca68d28225ac3f690aafa1236fb36e1a6e161f9248377bf7f6049c7bd9754df",
		"engine/profile.json":                       "bb6568fd1f4c08bdd740b9bc7dccb5f29bc4117eaae97458970916b77310e350",
		"engine/self.json":                          "0e2909b920b951ed1927f30740ca7389b7f97c0a67ae551b8c94f436708d09e9",
		"engine/storm.json":                         "989830f3a36667a054009e7788ab90c9517b068369403419c51b6fb8c19473fd",
		"engine/witness.json":                       "fa84d726be6cd120ed94f7ce2ac869542240f5246743c1637783ea5a41bd84f2",
		"insights/.keep":                            "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"lucid.json":                                "ff4009f14724698a5669e3f1a498619340f44f3ac677cdc46a1d7def1bf3e00a",
		"observations/2026/07/obs_2026_07_03.jsonl": "51124ebb39a5f937d77b43574411ad90a8a1d31726f8e147ebbeb90d8bffda3d",
		"observations/config.json":                  "314fc9176f49c52bcb03b80a5f2d1fd145be67ec3133631f4ac773c7de6c9700",
		"people/.keep":                              "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"people/person_c-carp.json":                 "08beff7b47b2cad3c214716bda8aff4909bd8180d833042bd476b268e46263ca",
		"people/person_g-rye.json":                  "75231f29c1a721f14e44940dd9b2b4ac0dce41dbb1c302374585000dba33c114",
		"processed/.keep":                           "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"processed/raw_2026_06_01_09_00.json":       "61e00274a651a4ff1fff48541fa94275216f8bb4b0dab5af9605ab1cdd7adffd",
		"processed/raw_2026_06_02_09_00.json":       "2aaf446d37994d3077d02631a1983f5692755ce9302d5ce4b1cad4fcf855797a",
		"processed/raw_2026_07_01_09_00.json":       "a628e1cafd511e50edb6d8bea7d94e2f984b1a181367ad2e9323b1827acfb687",
		"processed/raw_2026_07_02_09_00.json":       "1d243023e1410c9c41b5341e59cc8e7f36f8c0edc8a56499d6f82b7d00cf343b",
		"raw/.keep":                                 "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"raw/2026/06/raw_2026_06_01_09_00.md":       "f2179bf49b8fdbc4f5ec4447035da900dd7c6e74d3e4951d2a0fe356ed6cafd2",
		"raw/2026/06/raw_2026_06_02_09_00.md":       "207b3165c1a6bcbc6d358efba4f6693e81155be5427538724a468a20831eee63",
		"raw/2026/07/raw_2026_07_01_09_00.md":       "65b75aed78925edca55a3b5ded448ba2e28d6daeca5b3cbb0001bbb91a0105dd",
		"raw/2026/07/raw_2026_07_02_09_00.md":       "f40e543996c4836eeabf9cd8bc2c885147ac422db930de9ad46a5a2ad87a3393",
		"reflections/.keep":                         "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"registries/gratitude/gratitude_o-tan.json": "cfc9618ceb016b114ab46be93046a6a4dee9f09c431868d5eed31c92bd91eb0e",
		"sessions/.keep":                            "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"sessions/session_2026_06_01_09_00.json":    "97a437b2160f9d288c7fa43c2a973ae1b9fabebe327fbdd8813ffa4306dea94d",
		"sessions/session_2026_06_02_09_00.json":    "fb378def95941ff11526081f55eedc10bbcb86703ef11a879a9aef047c16b0c9",
		"sessions/session_2026_07_01_09_00.json":    "a700d2cf89600d78aba0d24a52fadc75f00bf30dfacdfc341ddaeca94d2f80cc",
		"sessions/session_2026_07_02_09_00.json":    "b3d7999643a3d21c3c8d9dddf1f492e7b252305beee012af37a9c8241da4c910",
	}
}
