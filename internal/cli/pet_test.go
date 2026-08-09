package cli

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/storage"
)

func runPet(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return runRoot(t, BuildInfo{Version: "dev"}, append([]string{"pet"}, args...)...)
}

func TestPet_CLICreateThenAmend(t *testing.T) {
	isolatedHome(t)

	out, _, err := runPet(t, "Fixture Pet", "--species", "dog", "--note", "synthetic companion")
	require.NoError(t, err)
	assert.Contains(t, out, "Recorded")
	assert.Contains(t, out, "pet")

	out, _, err = runPet(t, "Fixture Pet", "--status", "rehomed", "--json")
	require.NoError(t, err)
	var view registryWriteView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.Equal(t, "pet", view.Kind)
	assert.False(t, view.Created, "the second write amends the same record")
	assert.Equal(t, "rehomed", view.Status)
	assert.Equal(t, "dog", view.Fields["species"], "the create's field survives the amend")
	assert.Equal(t, "synthetic companion", view.Fields["note"])
}

func TestPet_CLIRejectsNonPetStatus(t *testing.T) {
	home := isolatedHome(t)

	_, stderr, err := runPet(t, "Fixture Pet", "--status", "managed")
	require.Error(t, err)
	// The reason must reach the user, not just the exit code: the root sets
	// SilenceErrors, so the verb has to print it (emitErr). Assert the printed
	// stream, not only err.Error(), so this guards the surfacing regression.
	assert.Contains(t, stderr, "want active, rehomed, or passed")
	assert.Contains(t, stderr, "nothing was saved")

	pets, readErr := storage.New(home).ReadRegistryKind(observations.RegistryPet)
	require.NoError(t, readErr)
	assert.Empty(t, pets)
}

// TestPet_RegisteredWithDateFlags confirms the verb self-documents its
// backdate-aware life-span flags in --help.
func TestPet_RegisteredWithDateFlags(t *testing.T) {
	out, _, err := runPet(t, "--help")
	require.NoError(t, err)
	assert.Contains(t, out, "--start")
	assert.Contains(t, out, "--end")
	assert.Contains(t, out, "--species")
}

// TestPet_CLIBackdatedLifeSpan is the user's headline case: a companion from
// years ago, recorded with whatever precision is remembered. A year-only start
// and a YYYY-MM end are stored **as typed** (never snapped), each with an
// approximate precision, alongside the passed status.
func TestPet_CLIBackdatedLifeSpan(t *testing.T) {
	isolatedHome(t)

	out, _, err := runPet(t, "Old Sable", "--species", "dog",
		"--start", "2005", "--end", "2012-06", "--status", "passed", "--json")
	require.NoError(t, err)

	var view registryWriteView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.Equal(t, "pet", view.Kind)
	assert.True(t, view.Created)
	assert.Equal(t, "passed", view.Status)
	assert.Equal(t, "2005", view.Fields["start"], "a year-only start is stored as typed, never snapped to 2005-01-01")
	assert.Equal(t, "approximate", view.Fields["start_precision"])
	assert.Equal(t, "2012-06", view.Fields["end"], "a YYYY-MM end is stored as typed")
	assert.Equal(t, "approximate", view.Fields["end_precision"])
	assert.Equal(t, "dog", view.Fields["species"])
}

// TestPet_CLIAmendPreservesLifeSpan proves a later amend (adding a note) leaves
// the recorded life-span intact — the append-only merge never drops a prior
// field — and reports the amend, not a create.
func TestPet_CLIAmendPreservesLifeSpan(t *testing.T) {
	isolatedHome(t)

	_, _, err := runPet(t, "Old Sable", "--start", "2005", "--end", "2012-06", "--status", "passed")
	require.NoError(t, err)

	out, _, err := runPet(t, "Old Sable", "--note", "best good boy", "--json")
	require.NoError(t, err)

	var view registryWriteView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.False(t, view.Created, "the second write amends the same record")
	assert.Equal(t, "passed", view.Status, "an amend with no --status keeps the status in force")
	assert.Equal(t, "2005", view.Fields["start"], "the prior life-span survives the amend")
	assert.Equal(t, "2012-06", view.Fields["end"])
	assert.Equal(t, "best good boy", view.Fields["note"])
}

// TestPet_CLIRejectsBadDatesSurfaced proves the strict date tier reaches the
// pet verb AND that every refusal is printed — a future date names the day, an
// unreadable phrase points at --note — with nothing saved either way. This is
// the emitErr guarantee: a rejection must never be a bare, silent exit code.
func TestPet_CLIRejectsBadDatesSurfaced(t *testing.T) {
	home := isolatedHome(t)

	_, stderr, err := runPet(t, "Ghost Pup", "--start", "2030")
	require.Error(t, err)
	assert.Contains(t, stderr, "has not happened yet")
	assert.Contains(t, stderr, "nothing was saved")

	_, stderr, err = runPet(t, "Ghost Pup", "--start", "last summer")
	require.Error(t, err)
	assert.Contains(t, stderr, "--note", "an unreadable date points prose at --note")
	assert.Contains(t, stderr, "nothing was saved")

	// The future ceiling applies to --end too, on an otherwise-valid create.
	_, stderr, err = runPet(t, "Ghost Pup", "--start", "2005", "--end", "2999-01-01")
	require.Error(t, err)
	assert.Contains(t, stderr, "has not happened yet")

	pets, readErr := storage.New(home).ReadRegistryKind(observations.RegistryPet)
	require.NoError(t, readErr)
	assert.Empty(t, pets, "a rejected date leaves no pet behind")
}

// TestPet_CLILinksMedia is the end-to-end proof that a pet is a linkable media
// subject (life-archive.md §8: "linkable media"): a photo attaches to a pet by
// key and folds into the association ledger, exactly like any other subject.
func TestPet_CLILinksMedia(t *testing.T) {
	home := isolatedHome(t)

	out, _, err := runPet(t, "Old Sable", "--species", "dog", "--json")
	require.NoError(t, err)
	var pet registryWriteView
	require.NoError(t, json.Unmarshal([]byte(out), &pet))

	photo := writeTempFile(t, "at-the-lake.jpg", []byte("\xff\xd8\xff synthetic image bytes"))
	_, _, err = runRoot(t, BuildInfo{Version: "dev"},
		"attach", photo, "--caption", "at the lake", "--to", "pet:"+pet.Key)
	require.NoError(t, err, "attaching media to a pet must resolve, not refuse an unknown kind")

	links, err := storage.New(home).LinksForSubject(observations.RegistryPet, pet.Key)
	require.NoError(t, err)
	require.Len(t, links, 1, "the photo folds into the pet's association ledger")
}

func TestPetMigrate_CLIHidden(t *testing.T) {
	cmd := newPetCmd()
	child, _, err := cmd.Find([]string{"migrate-self"})
	require.NoError(t, err)
	require.NotNil(t, child)
	assert.True(t, child.Hidden)

	out, _, err := runPet(t, "--help")
	require.NoError(t, err)
	assert.NotContains(t, out, "migrate-self")
}

func TestPetMigrate_CLIEndToEndAndRerun(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "init")
	require.NoError(t, err)
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "self", "set", "misc.pets", "Fixture Pet")
	require.NoError(t, err)

	out, _, err := runPet(t, "migrate-self")
	require.NoError(t, err)
	assert.Contains(t, out, "Migrated misc.pets to pet registry")

	store := storage.New(home)
	pets, err := store.ReadRegistryKind(observations.RegistryPet)
	require.NoError(t, err)
	require.Len(t, pets, 1)
	assert.Equal(t, "Fixture Pet", pets[0].DisplayName)
	log, err := store.ReadSelfFacts()
	require.NoError(t, err)
	_, active := engine.FindActiveSelfFact(engine.LatestSelfFacts(log), "misc.pets")
	assert.False(t, active)

	out, _, err = runPet(t, "migrate-self")
	require.NoError(t, err)
	assert.Contains(t, out, "No active misc.pets fact to migrate.")
	pets, err = store.ReadRegistryKind(observations.RegistryPet)
	require.NoError(t, err)
	assert.Len(t, pets, 1)
}

func TestPetMigrate_CLIJSON(t *testing.T) {
	isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "init")
	require.NoError(t, err)

	out, _, err := runPet(t, "migrate-self", "--json")
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, []string{"migrated", "pet_key", "reason", "retired_key", "skipped"}, sortedKeys(got))
	assert.Equal(t, false, got["migrated"])
	assert.Equal(t, false, got["skipped"])
	assert.Equal(t, "no active misc.pets fact", got["reason"])
	assert.Empty(t, got["pet_key"])
	assert.Empty(t, got["retired_key"])
}
