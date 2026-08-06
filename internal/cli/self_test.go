package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/router"
	"github.com/mrz1836/lucid/internal/storage"
)

// readSelfLog reads the append-only self store from an isolated home.
func readSelfLog(t *testing.T, home string) engine.SelfLog {
	t.Helper()
	log, err := storage.New(home).ReadSelfFacts()
	require.NoError(t, err)
	return log
}

// jsonArrayField unmarshals one JSON object and returns the named field as a
// slice of untyped objects, asserting it is an array rather than null — a
// harness must be able to index the surface unconditionally.
func jsonArrayField(t *testing.T, out, field string) []map[string]any {
	t.Helper()
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(out), &raw))
	require.Contains(t, raw, field)
	var rows []map[string]any
	require.NoError(t, json.Unmarshal(raw[field], &rows))
	require.NotNil(t, rows, "%s must be an array, never null", field)
	return rows
}

// runSelf runs a `lucid self …` invocation against the isolated home.
func runSelf(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return runRoot(t, BuildInfo{Version: "dev"}, append([]string{"self"}, args...)...)
}

// seedSelfProfile records one fact in each namespace, deliberately in reverse
// priority order, so a grouped render that leaned on write order would fail.
func seedSelfProfile(t *testing.T) {
	t.Helper()
	for _, kv := range [][2]string{
		{"misc.tattoo", "left forearm"},
		{"pref.editor", "vim"},
		{"body.handedness", "left"},
		{"constraint.diet.dairy", "avoid"},
		{"identity.generation", "elder millennial"},
	} {
		_, _, err := runSelf(t, "set", kv[0], kv[1])
		require.NoError(t, err)
	}
}

// TestSelfSet_CLI_RecordsAndAcks records a multi-word value and asserts the
// inventory ack plus one persisted record. The trailing args join, so the two
// words are one value rather than a value and a stray argument.
func TestSelfSet_CLI_RecordsAndAcks(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())

	out, _, err := runSelf(t, "set", "identity.generation", "elder", "millennial")
	require.NoError(t, err)
	assert.Contains(t, out, "Fact recorded: identity.generation — elder millennial.")

	log := readSelfLog(t, home)
	require.Len(t, log.History, 1)
	assert.Equal(t, "identity.generation", log.History[0].Key)
	assert.Equal(t, "elder millennial", log.History[0].Value)
	assert.Equal(t, "self_2026_07_05_a", log.History[0].ID)
}

// TestSelfSet_CLI_JSON is the shape guard on `self set --json`: it asserts the
// EXACT key set, so a renamed, removed, or unexpected key fails here. The
// surface is projected rather than marshaled from the stored record, and `id`
// is unconditional — a harness must be able to address what it just wrote.
// `since` and `note` are omitempty, so both the bare and the annotated shapes
// are pinned.
func TestSelfSet_CLI_JSON(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())

	out, _, err := runSelf(t, "set", "body.handedness", "left", "--json")
	require.NoError(t, err)
	got := jsonObject(t, out)
	assert.Equal(t, []string{"id", "key", "recorded_at", "value"}, sortedKeys(got))
	assert.Equal(t, "body.handedness", got["key"])
	assert.Equal(t, "left", got["value"])

	out, _, err = runSelf(t, "set", "pref.editor", "vim", "--since", "2014-09", "--note", "a note", "--json")
	require.NoError(t, err)
	got = jsonObject(t, out)
	assert.Equal(t, []string{"id", "key", "note", "recorded_at", "since", "value"}, sortedKeys(got))
	assert.Equal(t, "2014-09", got["since"])
	assert.Equal(t, "a note", got["note"])
}

// TestSelfSet_CLI_KeepsSinceAndNote asserts the two optional fields reach the
// store rather than being dropped between the flag and the record.
func TestSelfSet_CLI_KeepsSinceAndNote(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())

	_, _, err := runSelf(t, "set", "identity.birthplace", "Portland", "--since", "1985", "--note", "moved at nine")
	require.NoError(t, err)

	log := readSelfLog(t, home)
	require.Len(t, log.History, 1)
	assert.Equal(t, "1985", log.History[0].Since)
	assert.Equal(t, "moved at nine", log.History[0].Note)
}

// TestSelfSet_CLI_SincePartialStaysAsTyped pins the flag's one deliberate
// divergence from `anchor add`: a partial origin is kept exactly as the user
// typed it and is never snapped to the period's first day, while a full day is
// normalized to the civil date. A store read back in a decade must not carry a
// January 1st nobody claimed.
func TestSelfSet_CLI_SincePartialStaysAsTyped(t *testing.T) {
	for _, tc := range []struct{ name, arg, want string }{
		{"year", "1985", "1985"},
		{"month", "2014-09", "2014-09"},
		{"day", "2026-01-15", "2026-01-15"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := isolatedHome(t)
			withClock(t, afternoon())

			_, _, err := runSelf(t, "set", "identity.birthplace", "Portland", "--since", tc.arg)
			require.NoError(t, err)

			log := readSelfLog(t, home)
			require.Len(t, log.History, 1)
			assert.Equal(t, tc.want, log.History[0].Since)
		})
	}
}

// TestSelfSet_CLI_RejectsUnreadableSince covers the two ways an origin can be
// refused: a day that has not happened yet, and a value no branch of the shared
// grammar reads. Both print the fixed reason on stderr, exit non-zero, and
// write nothing — the file is not even created.
func TestSelfSet_CLI_RejectsUnreadableSince(t *testing.T) {
	for _, tc := range []struct{ name, arg, want string }{
		{"future", "2027-01-01", "has not happened yet"},
		{"unreadable", "sometime-ish", "could not read the origin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := isolatedHome(t)
			withClock(t, afternoon())

			out, errOut, err := runSelf(t, "set", "identity.birthplace", "Portland", "--since", tc.arg)
			require.Error(t, err)
			assert.Empty(t, out)
			assert.Contains(t, errOut, tc.want)
			assert.Contains(t, errOut, "nothing was saved")
			assert.Empty(t, readSelfLog(t, home).History)
		})
	}
}

// TestSelfSet_CLI_RejectsUnknownNamespace asserts the taxonomy rejection lands
// on stderr with a non-zero exit — never on stdout, where a harness reading the
// happy-path stream would mistake it for an ack — and that it names both the
// accepted set and the misc. landing spot, so no fact is ever turned away for
// want of a category.
func TestSelfSet_CLI_RejectsUnknownNamespace(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())

	out, errOut, err := runSelf(t, "set", "health.blood_type", "O+")
	require.ErrorIs(t, err, errSelfNotRecorded)
	assert.Empty(t, out)
	assert.Contains(t, errOut, "identity., constraint., body., pref., misc.")
	assert.Contains(t, errOut, "misc.")
	assert.Contains(t, errOut, "nothing was saved")
	assert.Empty(t, readSelfLog(t, home).History)
}

// TestSelfRetire_CLI_AcksAndKeepsRecord retires a fact and asserts the ack says
// both of the things a user needs next: it left the profile, and the record was
// kept.
func TestSelfRetire_CLI_AcksAndKeepsRecord(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())

	_, _, err := runSelf(t, "set", "pref.editor", "vim")
	require.NoError(t, err)

	out, _, err := runSelf(t, "retire", "pref.editor", "switched years ago")
	require.NoError(t, err)
	assert.Contains(t, out, "Fact retired: pref.editor — vim.")
	assert.Contains(t, out, "the record is kept")

	log := readSelfLog(t, home)
	require.Len(t, log.History, 2)
	assert.Equal(t, engine.SelfStateRetired, log.History[1].State)
	assert.Equal(t, "switched years ago", log.History[1].Note)
}

// TestSelfRetire_CLI_UnknownKeyRejected asserts the state rejection reaches
// stderr with a non-zero exit and names the recorded keys, since there is no
// list verb for a user to reach for instead.
func TestSelfRetire_CLI_UnknownKeyRejected(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())

	_, _, err := runSelf(t, "set", "pref.editor", "vim")
	require.NoError(t, err)

	out, errOut, err := runSelf(t, "retire", "pref.shell")
	require.ErrorIs(t, err, errSelfNotRecorded)
	assert.Empty(t, out)
	assert.Contains(t, errOut, `no fact recorded under "pref.shell"`)
	assert.Contains(t, errOut, "pref.editor")
	assert.Len(t, readSelfLog(t, home).History, 1)
}

// TestSelfMove_CLI_NamesBothKeys asserts the move ack names the key it had and
// the key it has, and that the store grew by exactly one record — the CLI's
// half of the one-append guarantee.
func TestSelfMove_CLI_NamesBothKeys(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())

	_, _, err := runSelf(t, "set", "misc.handedness", "left")
	require.NoError(t, err)

	out, _, err := runSelf(t, "move", "misc.handedness", "body.handedness")
	require.NoError(t, err)
	assert.Contains(t, out, "misc.handedness")
	assert.Contains(t, out, "body.handedness")

	log := readSelfLog(t, home)
	require.Len(t, log.History, 2)
	assert.Equal(t, log.History[0].ID, log.History[1].ID)
	assert.Equal(t, "body.handedness", log.History[1].Key)
}

// TestSelfMove_CLI_JSON is the shape guard on `self move --json`: the moved
// record, not the one it replaced.
func TestSelfMove_CLI_JSON(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())

	_, _, err := runSelf(t, "set", "misc.handedness", "left")
	require.NoError(t, err)

	out, _, err := runSelf(t, "move", "misc.handedness", "body.handedness", "--json")
	require.NoError(t, err)
	got := jsonObject(t, out)
	assert.Equal(t, []string{"id", "key", "recorded_at", "value"}, sortedKeys(got))
	assert.Equal(t, "body.handedness", got["key"])
	assert.Equal(t, "left", got["value"])
}

// TestSelfRetire_CLI_JSON is the shape guard on `self retire --json`: the
// appended retirement carries the state, so a harness can tell it from an
// ordinary record.
func TestSelfRetire_CLI_JSON(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())

	_, _, err := runSelf(t, "set", "pref.editor", "vim")
	require.NoError(t, err)

	out, _, err := runSelf(t, "retire", "pref.editor", "--json")
	require.NoError(t, err)
	got := jsonObject(t, out)
	assert.Equal(t, []string{"id", "key", "recorded_at", "state", "value"}, sortedKeys(got))
	assert.Equal(t, engine.SelfStateRetired, got["state"])
}

// TestSelfRead_CLI_GroupsByNamespacePriority is the render guard: the groups
// appear in the engine's priority order — identity, constraint, body, pref,
// misc — regardless of the order the facts were written in, which the seed
// deliberately reverses. Priority, not alphabetical and not recency.
func TestSelfRead_CLI_GroupsByNamespacePriority(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())
	seedSelfProfile(t)

	out, _, err := runSelf(t)
	require.NoError(t, err)

	last := -1
	for _, header := range []string{"identity.", "constraint.", "body.", "pref.", "misc."} {
		at := strings.Index(out, "\n"+header+"\n")
		if at < 0 && strings.HasPrefix(out, header+"\n") {
			at = 0
		}
		require.GreaterOrEqualf(t, at, 0, "missing group header %q in:\n%s", header, out)
		assert.Greaterf(t, at, last, "group %q is out of priority order in:\n%s", header, out)
		last = at
	}
	// The leaf is rendered under its header; the header carries the dot so the
	// full key a write verb takes is still readable off the page.
	assert.Contains(t, out, "generation")
	assert.Contains(t, out, "elder millennial")
}

// TestSelfRead_CLI_PrefixAndExactKey covers the two argument readings: an
// exact active key reads that one fact, and anything else filters by prefix on
// dot-delimited segments.
func TestSelfRead_CLI_PrefixAndExactKey(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())
	seedSelfProfile(t)

	out, _, err := runSelf(t, "body")
	require.NoError(t, err)
	assert.Contains(t, out, "body.")
	assert.Contains(t, out, "left")
	assert.NotContains(t, out, "identity.")

	out, _, err = runSelf(t, "identity.generation")
	require.NoError(t, err)
	assert.Contains(t, out, "elder millennial")
	assert.NotContains(t, out, "vim")
}

// TestSelfRead_CLI_JSON is the shape guard on the read surface: facts is always
// an array, and each row is the projected record.
func TestSelfRead_CLI_JSON(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())
	seedSelfProfile(t)

	out, _, err := runSelf(t, "--json")
	require.NoError(t, err)
	rows := jsonArrayField(t, out, "facts")
	require.Len(t, rows, 5)
	assert.Equal(t, "identity.generation", rows[0]["key"])
	assert.Equal(t, "misc.tattoo", rows[4]["key"])
}

// TestSelfRead_CLI_EmptyStoreNamesTheRemedy asserts an empty profile prints one
// honest line naming the way in rather than an empty list — there is no list
// verb, so the remedy has to travel with the message. The read must also not
// have scaffolded the store just to answer.
func TestSelfRead_CLI_EmptyStoreNamesTheRemedy(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())

	out, _, err := runSelf(t)
	require.NoError(t, err)
	assert.Contains(t, out, "No facts recorded yet")
	assert.Contains(t, out, "lucid self set")
	assert.Empty(t, readSelfLog(t, home).History)

	// A query that matched nothing says so instead, because the store is not
	// necessarily empty.
	out, _, err = runSelf(t, "body")
	require.NoError(t, err)
	assert.Contains(t, out, `No facts recorded under "body"`)
}

// TestSelfRead_CLI_RetiredFactLeavesTheProfile pins the retirement contract on
// the read surface: gone from the render and the JSON profile, still present in
// the history.
func TestSelfRead_CLI_RetiredFactLeavesTheProfile(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())

	_, _, err := runSelf(t, "set", "pref.editor", "vim")
	require.NoError(t, err)
	_, _, err = runSelf(t, "retire", "pref.editor")
	require.NoError(t, err)

	out, _, err := runSelf(t)
	require.NoError(t, err)
	assert.NotContains(t, out, "vim")

	out, _, err = runSelf(t, "--json")
	require.NoError(t, err)
	assert.Empty(t, jsonArrayField(t, out, "facts"))

	out, _, err = runSelf(t, "pref.editor", "--history")
	require.NoError(t, err)
	assert.Contains(t, out, "vim")
	assert.Contains(t, out, "[retired]")
}

// TestSelfRead_CLI_HistorySpansAMove is the surface's half of the identity
// model: a moved fact's history includes the records written under its previous
// key, in append order, because history resolves by id and never by key.
func TestSelfRead_CLI_HistorySpansAMove(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())

	_, _, err := runSelf(t, "set", "misc.handedness", "left")
	require.NoError(t, err)
	_, _, err = runSelf(t, "move", "misc.handedness", "body.handedness")
	require.NoError(t, err)

	out, _, err := runSelf(t, "body.handedness", "--history")
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, lines, 2)
	assert.Contains(t, lines[0], "misc.handedness")
	assert.Contains(t, lines[1], "body.handedness")

	out, _, err = runSelf(t, "body.handedness", "--history", "--json")
	require.NoError(t, err)
	rows := jsonArrayField(t, out, "history")
	require.Len(t, rows, 2)
	assert.Equal(t, "misc.handedness", rows[0]["key"])
	assert.Equal(t, "body.handedness", rows[1]["key"])
	assert.Equal(t, rows[0]["id"], rows[1]["id"])
}

// TestSelfHelp_CLI_NamesTheObsBoundary asserts the one thing that keeps this
// store from becoming a time series is in the binary's help and not only in the
// docs: a value that changes on a schedule is an observation.
func TestSelfHelp_CLI_NamesTheObsBoundary(t *testing.T) {
	out, _, err := runSelf(t, "--help")
	require.NoError(t, err)
	assert.Contains(t, out, "lucid obs")

	out, _, err = runSelf(t, "set", "--help")
	require.NoError(t, err)
	assert.Contains(t, out, "lucid obs")
}

// TestSelf_CLI_RegisteredOnRoot asserts the verb reaches the command spine, so
// the read surface is discoverable from `lucid --help` beside `person` — the
// asymmetry this store exists to close.
func TestSelf_CLI_RegisteredOnRoot(t *testing.T) {
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "--help")
	require.NoError(t, err)
	assert.Contains(t, out, "Read and record durable facts about yourself")
	assert.Regexp(t, `(?m)^\s+self\s`, out)
}

// TestSelfFactRow_RendersSinceAndNote covers the row formatter directly: the
// leaf is padded to the column width and the optional origin and annotation are
// appended only when present, so a bare fact renders without dangling markers.
func TestSelfFactRow_RendersSinceAndNote(t *testing.T) {
	cases := []struct {
		name string
		fact engine.SelfFact
		want string
	}{
		{"bare", engine.SelfFact{Key: "body.handedness", Value: "left"}, "handedness  left"},
		{"since", engine.SelfFact{Key: "body.handedness", Value: "left", Since: "1985"}, "handedness  left (since 1985)"},
		{"note", engine.SelfFact{Key: "body.handedness", Value: "left", Note: "writes right"}, "handedness  left — writes right"},
		{
			"since and note",
			engine.SelfFact{Key: "body.handedness", Value: "left", Since: "1985", Note: "writes right"},
			"handedness  left (since 1985) — writes right",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, selfFactRow(tc.fact, len("handedness")))
		})
	}
}

// TestSelfKeySplit_DegradesOnMissingDot proves the namespace/leaf split returns
// the whole key rather than failing when a hand-edited record carries a key with
// no dot — a read path never panics on malformed state.
func TestSelfKeySplit_DegradesOnMissingDot(t *testing.T) {
	assert.Equal(t, "identity", selfKeyNamespace("identity.generation"))
	assert.Equal(t, "generation", selfKeyLeaf("identity.generation"))
	assert.Equal(t, "nodot", selfKeyNamespace("nodot"))
	assert.Equal(t, "nodot", selfKeyLeaf("nodot"))
}

// TestSelfHistoryLines covers the append-log renderer: an empty selection names
// the miss, a retirement carries the [retired] marker, and an active record does
// not.
func TestSelfHistoryLines(t *testing.T) {
	assert.Equal(t, []string{"No records in the history for that selection."}, selfHistoryLines(nil))

	lines := selfHistoryLines([]engine.SelfFact{
		{RecordedAt: "2026-07-05T10:00:00Z", Key: "pref.editor", Value: "vim"},
		{RecordedAt: "2026-07-06T10:00:00Z", Key: "pref.editor", Value: "vim", State: engine.SelfStateRetired},
	})
	require.Len(t, lines, 2)
	assert.Equal(t, "2026-07-05T10:00:00Z  pref.editor  vim", lines[0])
	assert.Contains(t, lines[1], "[retired]")
}

// TestSelfRejection_PassesThroughRealFailure proves a non-rejection error travels
// unchanged (it is a real failure, not a deterministic refusal) while a
// SelfRejectedError maps to the shared not-recorded sentinel with the reason on
// stderr.
func TestSelfRejection_PassesThroughRealFailure(t *testing.T) {
	cmd, errBuf := selfRejectionCmd()
	boom := errors.New("disk on fire")
	assert.Equal(t, boom, selfRejection(cmd, boom), "a real failure is returned verbatim")
	assert.Empty(t, errBuf.String())

	cmd, errBuf = selfRejectionCmd()
	rejected := &router.SelfRejectedError{Reason: "no active fact holds pref.editor"}
	require.ErrorIs(t, selfRejection(cmd, rejected), errSelfNotRecorded)
	assert.Contains(t, errBuf.String(), "no active fact holds pref.editor")

	cmd, errBuf = selfRejectionCmd()
	wrapped := fmt.Errorf("gate: %w", router.ErrSelfRejected)
	require.ErrorIs(t, selfRejection(cmd, wrapped), errSelfNotRecorded)
	assert.Contains(t, errBuf.String(), "self fact rejected")
}

// selfRejectionCmd returns a bare command whose stderr is captured, for the
// deterministic-rejection routing tests.
func selfRejectionCmd() (*cobra.Command, *bytes.Buffer) {
	cmd := &cobra.Command{}
	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	cmd.SetOut(&bytes.Buffer{})
	return cmd, &errBuf
}

// TestSelfWriteVerbs_BootErrorSurfaces proves each write verb surfaces a boot
// failure (an unscaffoldable home) rather than appending — the router never
// comes up, so nothing is recorded. Mirrors the other *_BootError guards on the
// spine.
func TestSelfWriteVerbs_BootErrorSurfaces(t *testing.T) {
	for _, args := range [][]string{
		{"self", "set", "body.handedness", "left"},
		{"self", "retire", "pref.editor"},
		{"self", "move", "misc.handedness", "body.handedness"},
	} {
		t.Run(args[1], func(t *testing.T) {
			unscaffoldableHome(t)
			_, _, err := runRoot(t, BuildInfo{Version: "dev"}, args...)
			require.Error(t, err)
		})
	}
}
