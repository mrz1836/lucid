package router

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/observations"
)

func TestMigratePetsFromSelf_MigratesRetiresAndIsIdempotent(t *testing.T) {
	r, store, _ := newBootedRouter(t)
	_, err := r.SelfSet(SelfSetRequest{Key: legacyPetsSelfKey, Value: "Fixture Pet", Now: fixedNow()})
	require.NoError(t, err)

	first, err := r.MigratePetsFromSelf(fixedNow())
	require.NoError(t, err)
	assert.True(t, first.Migrated)
	assert.False(t, first.Skipped)
	assert.Equal(t, legacyPetsSelfKey, first.RetiredKey)
	assert.NotEmpty(t, first.PetKey)

	pets, err := store.ReadRegistryKind(observations.RegistryPet)
	require.NoError(t, err)
	require.Len(t, pets, 1)
	assert.Equal(t, "Fixture Pet", pets[0].DisplayName)
	assert.Equal(t, first.PetKey, pets[0].Key)
	require.Len(t, pets[0].StatusHistory, 1)

	log, err := store.ReadSelfFacts()
	require.NoError(t, err)
	latest := engine.LatestSelfFacts(log)
	_, active := engine.FindActiveSelfFact(latest, legacyPetsSelfKey)
	assert.False(t, active)
	retired, ok := engine.LatestRetiredSelfFactFor(latest, legacyPetsSelfKey)
	require.True(t, ok)
	assert.Equal(t, engine.SelfStateRetired, retired.State)
	assert.Equal(t, "migrated to pet registry ("+first.PetKey+")", retired.Note)

	second, err := r.MigratePetsFromSelf(fixedNow())
	require.NoError(t, err)
	assert.False(t, second.Migrated)
	assert.False(t, second.Skipped)
	assert.Equal(t, "no active misc.pets fact", second.Reason)

	pets, err = store.ReadRegistryKind(observations.RegistryPet)
	require.NoError(t, err)
	require.Len(t, pets, 1, "a re-run must not duplicate the pet")
	assert.Len(t, pets[0].StatusHistory, 1, "a completed re-run must not amend the pet")
}

func TestMigratePetsFromSelf_SkipsMultiValue(t *testing.T) {
	for _, value := range []string{"Fixture Pet, Second Pet", "Fixture Pet; Second Pet", "Fixture Pet\nSecond Pet"} {
		t.Run(value, func(t *testing.T) {
			r, store, _ := newBootedRouter(t)
			_, err := r.SelfSet(SelfSetRequest{Key: legacyPetsSelfKey, Value: value, Now: fixedNow()})
			require.NoError(t, err)

			res, err := r.MigratePetsFromSelf(fixedNow())
			require.NoError(t, err)
			assert.False(t, res.Migrated)
			assert.True(t, res.Skipped)
			assert.Equal(t, "multi-value misc.pets fact; handle manually", res.Reason)

			pets, err := store.ReadRegistryKind(observations.RegistryPet)
			require.NoError(t, err)
			assert.Empty(t, pets)
			log, err := store.ReadSelfFacts()
			require.NoError(t, err)
			_, active := engine.FindActiveSelfFact(engine.LatestSelfFacts(log), legacyPetsSelfKey)
			assert.True(t, active, "a skipped fact must remain active for manual handling")
		})
	}
}

func TestMigratePetsFromSelf_EmptyStoreIsNoOp(t *testing.T) {
	r, store, _ := newBootedRouter(t)

	res, err := r.MigratePetsFromSelf(fixedNow())
	require.NoError(t, err)
	assert.False(t, res.Migrated)
	assert.False(t, res.Skipped)
	assert.Equal(t, "no active misc.pets fact", res.Reason)

	pets, err := store.ReadRegistryKind(observations.RegistryPet)
	require.NoError(t, err)
	assert.Empty(t, pets)
}
