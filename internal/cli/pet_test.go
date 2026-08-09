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
