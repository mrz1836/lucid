package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// person_identity_e2e_test.go drives the person identity verbs end to end
// through the cobra root: the motivating case (an old "Sam" from a single
// imported mention, then a new "Sam Rivera" who matters now), and the S-22
// byte-stability of every new and changed people surface. Every person here is
// synthetic: two people named "Sam" ("Sam" and "Sam Rivera"), plus "Alex",
// "Alex Kim", "Alex Stone", "Jordan Lee", and "Riley Moss".

// e2eRun runs one lucid command and requires it to succeed.
func e2eRun(t *testing.T, args ...string) string {
	t.Helper()
	out, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, args...)
	require.NoErrorf(t, err, "lucid %s: %s", strings.Join(args, " "), stderr)
	return out
}

// e2eAka reads a person's aka[] straight from their record file.
func e2eAka(t *testing.T, home, key string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, "people", key+".json"))
	require.NoError(t, err)
	var rec struct {
		Aka []string `json:"aka"`
	}
	require.NoError(t, json.Unmarshal(b, &rec))
	return rec.Aka
}

// e2eProcessedPeople reads the person keys and held forms a structured raw
// entry recorded.
func e2eProcessedPeople(t *testing.T, home, raw string) (keys, held []string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, "processed", raw+".json"))
	require.NoErrorf(t, err, "processed artifact for %s", raw)
	var art struct {
		People []struct {
			PersonKey string `json:"person_key"`
		} `json:"people"`
		HeldPeople []struct {
			DisplayName string `json:"display_name"`
		} `json:"held_people"`
	}
	require.NoError(t, json.Unmarshal(b, &art))
	for _, p := range art.People {
		keys = append(keys, p.PersonKey)
	}
	for _, h := range art.HeldPeople {
		held = append(held, h.DisplayName)
	}
	return keys, held
}

// e2eLines splits command output into its lines, dropping the final newline.
func e2eLines(out string) []string { return strings.Split(strings.TrimSuffix(out, "\n"), "\n") }

// TestPersonVerbsCLI_MotivatingCase runs the case behind the identity verbs
// through the CLI, with a scripted model: an old "Sam" known from one imported
// mention and a new "Sam Rivera" created on purpose. Reconcile surfaces the
// overlap with its fixes; making the old Sam dormant sends the next bare "Sam"
// to Sam Rivera (a first-name pin, Sam Rivera's aka[] untouched); `person Sam`
// shows Sam Rivera and names the dormant Sam; a claim keeps "Sam" on Sam
// Rivera even after the old Sam is restored; a held mention from an aka
// collision is pinned by a claim; a move-ref repairs a misfiled mention; and
// `lucid validate` stays clean.
func TestPersonVerbsCLI_MotivatingCase(t *testing.T) {
	home := isolatedHome(t)
	t.Setenv("LUCID_NO_UPDATE_CHECK", "1")
	t.Setenv("LUCID_REFUSE_REAL_HOME", "1")
	withClock(t, time.Date(2023, time.March, 4, 20, 0, 0, 0, time.UTC))
	e2eRun(t, "init")
	base := identityCLIAt()

	// 1. The old Sam comes from one mention years ago; Sam Rivera is created
	// deliberately today.
	oldRaw := identityCLIStructure(t, time.Date(2023, time.March, 4, 20, 5, 0, 0, time.UTC), identityMention{DisplayName: "Sam"})
	withClock(t, base)
	e2eRun(t, "person", "create", "Sam Rivera")
	sam := identityCLIKey(t, "Sam")
	river := identityCLIKey(t, "Sam Rivera")
	oldKeys, _ := e2eProcessedPeople(t, home, oldRaw)
	require.Equal(t, []string{sam}, oldKeys)

	// 2. Reconcile offers merge (same person) or claim/dormant (different
	// people) on the one pair.
	rec := e2eRun(t, "person", "reconcile")
	assert.Contains(t, rec, fmt.Sprintf(`- "Sam" (%s) and "Sam Rivera" (%s) — similar names`, sam, river))
	assert.Contains(t, rec, fmt.Sprintf(`  Or keep them apart: a bare "Sam" goes to "Sam" (%s, first seen 2023-03-04), and "Sam Rivera" (%s) also has the first name "Sam".`, sam, river))
	assert.Contains(t, rec, fmt.Sprintf("    → lucid person claim %s \"Sam\"\n    → lucid person dormant %s", river, sam))

	// 3. "I don't care about the old Sam anymore": the next bare "Sam" goes to
	// Sam Rivera by first name, pinned, and never joins their aka[].
	akaBefore := e2eAka(t, home, river)
	withClock(t, base.Add(time.Minute))
	out := e2eRun(t, "person", "dormant", "Sam", "--reason-file", writeReasonFile(t, "an old contact from an imported note\n"))
	assert.Equal(t, `"Sam" is now dormant — the record and its history are kept (identity_2026_11_17_a).`+"\n", out)
	firstNameRaw := identityCLIStructure(t, base.Add(2*time.Minute), identityMention{DisplayName: "Sam"})
	keys, held := e2eProcessedPeople(t, home, firstNameRaw)
	assert.Equal(t, []string{river}, keys)
	assert.Empty(t, held)
	assert.Equal(t, akaBefore, e2eAka(t, home, river), "a first-name route never adds the form to aka[]")
	assert.Contains(t, identityLog(t, home), `"kind":"pin","at":`)
	assert.Contains(t, identityLog(t, home), `"raw_id":"`+firstNameRaw+`","form":"Sam","person_key":"`+river+`"`)
	assert.Contains(t, identityLog(t, home), `"cause":"first_name"`)

	// 4. `person Sam` is Sam Rivera now, naming the dormant Sam; the old Sam's
	// own view says since when, and why.
	view := e2eLines(e2eRun(t, "person", "Sam"))
	require.GreaterOrEqual(t, len(view), 2)
	assert.Equal(t, "Sam Rivera", view[0])
	assert.Equal(t, fmt.Sprintf(`"Sam" also names a dormant person: "Sam" (%s).`, sam), view[1])
	own := e2eLines(e2eRun(t, "person", sam))
	assert.Equal(t, []string{"Sam", "Dormant since 2026-11-17 — an old contact from an imported note"}, own[:2])

	// 5. "When I say Sam I mean Sam Rivera": the claim succeeds, since the only
	// other holder is dormant.
	withClock(t, base.Add(3*time.Minute))
	claim, _ := identityCLIWrite(t, "person", "claim", "Sam Rivera", "Sam")
	assert.Equal(t, "Sam", claim.Form)
	assert.Equal(t, river, claim.PersonKey)
	assert.Empty(t, claim.Displaced)

	// 6. The old Sam is back, but the claim keeps a bare "Sam" on Sam Rivera.
	withClock(t, base.Add(4*time.Minute))
	restore, _ := identityCLIWrite(t, "person", "dormant", "Sam", "--restore")
	assert.Equal(t, sam, restore.PersonKey, "--restore reaches the dormant Sam though a bare Sam now means someone else")
	assert.Equal(t, "active", restore.Status)
	claimedRaw := identityCLIStructure(t, base.Add(5*time.Minute), identityMention{DisplayName: "Sam"})
	keys, _ = e2eProcessedPeople(t, home, claimedRaw)
	assert.Equal(t, []string{river}, keys, "the claim wins over the restored exact holder")
	assert.Equal(t, akaBefore, e2eAka(t, home, river))
	assert.Equal(t, "Sam Rivera", e2eLines(e2eRun(t, "person", "Sam"))[0])

	// 7. An aka collision holds a bare "Alex" (§P-11); a claim pins it.
	identityCLIStructure(t, base.Add(6*time.Minute), identityMention{DisplayName: "Alex"})
	identityCLIStructure(t, base.Add(7*time.Minute), identityMention{DisplayName: "Jordan Lee", Aka: []string{"Alex"}})
	heldRaw := identityCLIStructure(t, base.Add(8*time.Minute), identityMention{DisplayName: "Alex"})
	keys, held = e2eProcessedPeople(t, home, heldRaw)
	assert.Empty(t, keys)
	assert.Equal(t, []string{"Alex"}, held)
	assert.Contains(t, e2eRun(t, "person", "reconcile"), "- "+heldRaw+`: "Alex" could mean `)
	jordan := identityCLIKey(t, "Jordan Lee")
	withClock(t, base.Add(9*time.Minute))
	pinned, _ := identityCLIWrite(t, "person", "claim", jordan, "Alex", "--force")
	require.Len(t, pinned.Pinned, 1)
	assert.Equal(t, personIdentityPinView{RawID: heldRaw, Form: "Alex", PersonKey: jordan}, pinned.Pinned[0])
	assert.Zero(t, pinned.StillHeld)
	assert.NotContains(t, e2eRun(t, "person", "reconcile"), heldRaw, "a pinned mention is no longer held")

	// 8. The claimed mention was really the old Sam: a move-ref repairs it on
	// read, without touching people/ or processed/.
	samMentions := e2eLines(e2eRun(t, "person", sam))[1]
	riverMentions := e2eLines(e2eRun(t, "person", river))[1]
	assert.True(t, strings.HasPrefix(samMentions, "Mentioned in 1 entry "), samMentions)
	assert.True(t, strings.HasPrefix(riverMentions, "Mentioned in 2 entries "), riverMentions)
	before := identityTreeHash(t, home)
	withClock(t, base.Add(10*time.Minute))
	moved, _ := identityCLIWrite(t, "person", "move-ref", claimedRaw, "--from", "Sam Rivera", "--to", sam)
	assert.Equal(t, sam, moved.To)
	assert.Equal(t, river, moved.From)
	after := identityTreeHash(t, home)
	for path, sum := range before {
		if !strings.HasPrefix(path, "identity/") {
			assert.Equalf(t, sum, after[path], "move-ref leaves %s byte-identical", path)
		}
	}
	assert.True(t, strings.HasPrefix(e2eLines(e2eRun(t, "person", sam))[1], "Mentioned in 2 entries "))
	assert.True(t, strings.HasPrefix(e2eLines(e2eRun(t, "person", river))[1], "Mentioned in 1 entry "))

	// 9. The Ledger validates clean, the identity log included.
	t.Chdir(t.TempDir())
	var report struct {
		OK       bool              `json:"ok"`
		Ran      []string          `json:"ran"`
		Errors   int               `json:"errors"`
		Warnings int               `json:"warnings"`
		Findings []json.RawMessage `json:"findings"`
	}
	require.NoError(t, json.Unmarshal([]byte(e2eRun(t, "validate", "--json")), &report))
	assert.True(t, report.OK)
	assert.Contains(t, report.Ran, "people-identity")
	assert.Zero(t, report.Errors)
	assert.Zero(t, report.Warnings)
	assert.Empty(t, report.Findings)
}

// byteStableTranscript builds one synthetic Ledger through the CLI on a
// pinned clock — every write's output kept in order — then renders every new
// and changed people surface twice, human and --json, requiring each pair to
// match. It returns the transcript (labeled outputs, the identity log last)
// for a second build to be compared against.
func byteStableTranscript(t *testing.T) []string {
	t.Helper()
	home := isolatedHome(t)
	t.Setenv("LUCID_NO_UPDATE_CHECK", "1")
	t.Setenv("LUCID_REFUSE_REAL_HOME", "1")
	base := identityCLIAt()
	withClock(t, base)
	e2eRun(t, "init")
	var transcript []string
	write := func(label string, args ...string) {
		t.Helper()
		transcript = append(transcript, label+"\n"+e2eRun(t, args...))
	}

	at := func(n int) time.Time { return base.Add(time.Duration(n) * time.Minute) }
	for i, name := range []string{"Sam", "Sam Rivera", "Alex", "Alex Kim", "Alex Stone", "Riley Moss"} {
		identityCLIStructure(t, at(i+1), identityMention{DisplayName: name})
	}
	alex := identityCLIKey(t, "Alex")
	riley := identityCLIKey(t, "Riley Moss")
	river := identityCLIKey(t, "Sam Rivera")
	sam := identityCLIKey(t, "Sam")

	withClock(t, at(10))
	write("contact every riley", "contact", "every", riley, "1w", "--json")
	write("contact every river", "contact", "every", river, "1w")
	write("dormant alex", "person", "dormant", alex, "--reason-file", writeReasonFile(t, "an old contact\n"), "--json")
	heldRaw := identityCLIStructure(t, at(11), identityMention{DisplayName: "Alex"})
	withClock(t, at(12))
	write("dormant riley", "person", "dormant", riley)
	identityCLIStructure(t, at(13), identityMention{DisplayName: "Riley Moss"})
	movedRaw := identityCLIStructure(t, at(14), identityMention{DisplayName: "Sam Rivera"})
	withClock(t, at(15))
	write("claim", "person", "claim", river, "Rivera", "--json")
	write("release", "person", "release", river, "Rivera")
	write("move-ref", "person", "move-ref", movedRaw, "--from", river, "--to", sam, "--json")
	write("move-ref back", "person", "move-ref", movedRaw, "--from", sam, "--to", river)
	write("dormant no-op", "person", "dormant", riley, "--json")

	reads := [][]string{
		{"person", alex},
		{"person", alex, "--json"},
		{"person", "Alex"},
		{"person", "Alex", "--json"},
		{"person", "Sam"},
		{"person", "Sam", "--json"},
		{"person", "reconcile"},
		{"person", "reconcile", "--json"},
		{"person", "reconcile", "--all"},
		{"person", "reconcile", "--all", "--json"},
		{"contact", "due", "--all"},
		{"contact", "due", "--all", "--json"},
	}
	for _, args := range reads {
		first, again := e2eRun(t, args...), e2eRun(t, args...)
		require.Equalf(t, first, again, "lucid %s renders byte-identically on a second run", strings.Join(args, " "))
		transcript = append(transcript, strings.Join(args, " ")+"\n"+first)
	}

	// The transcript covers what it claims to: the dormant view, §P-2 with a
	// dormant candidate, every reconcile addition, and a dormant contact row.
	all := strings.Join(transcript, "\n")
	assert.Contains(t, all, "Dormant since 2026-11-17 — an old contact")
	assert.Contains(t, all, "Alex (first seen 2026-11-17, dormant)")
	assert.Contains(t, all, "- "+heldRaw+`: "Alex" could mean `)
	assert.Contains(t, all, `also has the first name "Sam"`)
	assert.Contains(t, all, "lucid person dormant "+riley+" --restore")
	assert.Contains(t, all, " (dormant)\n")
	assert.Contains(t, all, `"dormant": true`)

	return append(transcript, "identity.jsonl\n"+identityLog(t, home))
}

// TestPeopleOutput_ByteStable (S-22): every new and changed people surface —
// the dormant person view, §P-2 with a dormant candidate, reconcile with its
// held, overlap, and dormant look-alike lines (with and without --all),
// `contact due --all`, each identity-verb receipt and ack, and the identity
// log's lines and their order — renders byte-identically, human and --json:
// across two runs on the same store, and across two stores built by the same
// steps.
func TestPeopleOutput_ByteStable(t *testing.T) {
	first := byteStableTranscript(t)
	second := byteStableTranscript(t)
	require.Len(t, second, len(first))
	for i := range first {
		assert.Equal(t, first[i], second[i])
	}
	logLines := e2eLines(strings.SplitN(first[len(first)-1], "\n", 2)[1])
	require.NotEmpty(t, logLines)
	ids := make([]string, 0, len(logLines))
	for _, line := range logLines {
		var ev struct {
			ID string `json:"id"`
		}
		require.NoError(t, json.Unmarshal([]byte(line), &ev))
		ids = append(ids, ev.ID)
	}
	assert.IsIncreasing(t, ids, "the identity log is written in receipt order")
}
