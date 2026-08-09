package router

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/storage"
)

func TestSecretAddListRemoveAndReregister(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	now := fixedNow()

	added, err := r.SecretAdd(SecretAddRequest{
		Name:   "FIXTURE_SECRET",
		Note:   "synthetic reference",
		Now:    now,
		Source: "cli",
	})
	require.NoError(t, err)
	assert.Equal(t, storage.SecretRefOpRegister, added.Event.Op)
	assert.Equal(t, "Registered reference FIXTURE_SECRET.", added.Ack)

	refs, err := r.SecretList()
	require.NoError(t, err)
	require.Equal(t, []storage.SecretRef{{
		Name:      "FIXTURE_SECRET",
		Note:      "synthetic reference",
		CreatedAt: now.Format(time.RFC3339),
	}}, refs)

	removed, err := r.SecretRemove("FIXTURE_SECRET", now.Add(time.Minute), "cli")
	require.NoError(t, err)
	assert.Equal(t, storage.SecretRefOpRemove, removed.Event.Op)
	assert.Equal(t, "Removed reference FIXTURE_SECRET.", removed.Ack)
	refs, err = r.SecretList()
	require.NoError(t, err)
	assert.Empty(t, refs)

	readded, err := r.SecretAdd(SecretAddRequest{
		Name: "FIXTURE_SECRET",
		Now:  now.Add(2 * time.Minute),
	})
	require.NoError(t, err)
	assert.Equal(t, "secretref_3", readded.Event.ID)
	refs, err = r.SecretList()
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, now.Add(2*time.Minute).Format(time.RFC3339), refs[0].CreatedAt)

	events, skipped, err := a.ReadSecretRefEvents()
	require.NoError(t, err)
	assert.Zero(t, skipped)
	assert.Len(t, events, 3)
}

func TestSecretAddRefusesDuplicateWithoutAppending(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	req := SecretAddRequest{Name: "FIXTURE_SECRET", Now: fixedNow(), Source: "cli"}
	_, err := r.SecretAdd(req)
	require.NoError(t, err)

	_, err = r.SecretAdd(req)
	require.EqualError(t, err, `reference "FIXTURE_SECRET" is already registered; nothing was saved`)

	events, skipped, err := a.ReadSecretRefEvents()
	require.NoError(t, err)
	assert.Zero(t, skipped)
	assert.Len(t, events, 1)
}

func TestSecretNoteUpdatesNoteKeepingCreatedAt(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	created := fixedNow()

	_, err := r.SecretAdd(SecretAddRequest{Name: "FIXTURE_SECRET", Note: "first", Now: created, Source: "cli"})
	require.NoError(t, err)

	res, err := r.SecretNote("FIXTURE_SECRET", "second", created.Add(time.Hour), "cli")
	require.NoError(t, err)
	assert.Equal(t, storage.SecretRefOpNote, res.Event.Op)
	assert.Equal(t, "Updated the note on FIXTURE_SECRET.", res.Ack)

	refs, err := r.SecretList()
	require.NoError(t, err)
	require.Equal(t, []storage.SecretRef{{
		Name:      "FIXTURE_SECRET",
		Note:      "second",
		CreatedAt: created.Format(time.RFC3339),
	}}, refs)

	// The amend is a new append; nothing was rewritten or removed.
	events, skipped, err := a.ReadSecretRefEvents()
	require.NoError(t, err)
	assert.Zero(t, skipped)
	assert.Len(t, events, 2)
}

func TestSecretNoteClearsNote(t *testing.T) {
	r, _, _ := newBootedRouter(t)
	_, err := r.SecretAdd(SecretAddRequest{Name: "FIXTURE_SECRET", Note: "first", Now: fixedNow(), Source: "cli"})
	require.NoError(t, err)

	res, err := r.SecretNote("FIXTURE_SECRET", "", fixedNow().Add(time.Minute), "cli")
	require.NoError(t, err)
	assert.Equal(t, "Cleared the note on FIXTURE_SECRET.", res.Ack)

	refs, err := r.SecretList()
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Empty(t, refs[0].Note)
}

func TestSecretNoteRefusesMissingWithoutAppending(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	_, err := r.SecretNote("FIXTURE_SECRET", "x", fixedNow(), "cli")
	require.EqualError(t, err, `reference "FIXTURE_SECRET" is not registered; nothing was changed`)

	events, skipped, err := a.ReadSecretRefEvents()
	require.NoError(t, err)
	assert.Zero(t, skipped)
	assert.Empty(t, events)
}

func TestSecretNoteRefusesInvalidInput(t *testing.T) {
	r, _, _ := newBootedRouter(t)
	_, err := r.SecretAdd(SecretAddRequest{Name: "FIXTURE_SECRET", Now: fixedNow(), Source: "cli"})
	require.NoError(t, err)

	_, err = r.SecretNote("lower", "x", fixedNow(), "cli")
	require.EqualError(t, err, "could not update the note; nothing was saved: storage: secret reference name must match ^[A-Z_][A-Z0-9_]*$ (1-64 characters)")

	_, err = r.SecretNote("FIXTURE_SECRET", strings.Repeat("x", 257), fixedNow(), "cli")
	require.EqualError(t, err, "could not update the note; nothing was saved: storage: secret reference note must be at most 256 characters")

	_, err = r.SecretNote("FIXTURE_SECRET", "x", fixedNow(), "bad token!")
	require.ErrorContains(t, err, "invalid source; nothing was saved")
}

func TestSecretRemoveRefusesMissingWithoutAppending(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	_, err := r.SecretRemove("FIXTURE_SECRET", fixedNow(), "cli")
	require.EqualError(t, err, `reference "FIXTURE_SECRET" is not registered; nothing was removed`)

	events, skipped, err := a.ReadSecretRefEvents()
	require.NoError(t, err)
	assert.Zero(t, skipped)
	assert.Empty(t, events)
}

func TestSecretIntentsRefuseInvalidInputWithFixedReasons(t *testing.T) {
	r, a, home := newBootedRouter(t)

	_, err := r.SecretAdd(SecretAddRequest{Name: "lower", Now: fixedNow()})
	require.EqualError(t, err, "could not register the reference; nothing was saved: storage: secret reference name must match ^[A-Z_][A-Z0-9_]*$ (1-64 characters)")

	_, err = r.SecretAdd(SecretAddRequest{
		Name: "FIXTURE_SECRET",
		Note: strings.Repeat("x", 257),
		Now:  fixedNow(),
	})
	require.EqualError(t, err, "could not register the reference; nothing was saved: storage: secret reference note must be at most 256 characters")

	_, err = r.SecretRemove("lower", fixedNow(), "cli")
	require.EqualError(t, err, "could not remove the reference; nothing was saved: storage: secret reference name must match ^[A-Z_][A-Z0-9_]*$ (1-64 characters)")

	_, err = r.SecretAdd(SecretAddRequest{Name: "FIXTURE_SECRET", Now: fixedNow(), Source: "bad token!"})
	require.ErrorContains(t, err, "invalid source; nothing was saved")

	events, skipped, err := a.ReadSecretRefEvents()
	require.NoError(t, err)
	assert.Zero(t, skipped)
	assert.Empty(t, events)
	assert.NoDirExists(t, home+"/secrets", "validation failures must not scaffold the catalog")
}
