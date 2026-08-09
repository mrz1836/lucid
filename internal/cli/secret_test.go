package cli

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/storage"
)

func TestSecretCLI_AddListRemove(t *testing.T) {
	isolatedHome(t)
	now := time.Date(2026, time.August, 9, 15, 30, 0, 0, time.UTC)
	withClock(t, now)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"secret", "add", "FIXTURE_SECRET", "--note", "synthetic reference")
	require.NoError(t, err)
	assert.Equal(t, "Registered reference FIXTURE_SECRET.\n", out)

	out, _, err = runRoot(t, BuildInfo{Version: "dev"}, "secret", "list")
	require.NoError(t, err)
	assert.Contains(t, out, "FIXTURE_SECRET")
	assert.Contains(t, out, "synthetic reference")
	assert.Contains(t, out, now.Format(time.RFC3339))

	out, _, err = runRoot(t, BuildInfo{Version: "dev"}, "secret", "remove", "FIXTURE_SECRET")
	require.NoError(t, err)
	assert.Equal(t, "Removed reference FIXTURE_SECRET.\n", out)

	out, _, err = runRoot(t, BuildInfo{Version: "dev"}, "secret", "list")
	require.NoError(t, err)
	assert.Equal(t, "No secret references registered.\n", out)
}

func TestSecretCLI_ListJSON(t *testing.T) {
	isolatedHome(t)
	now := time.Date(2026, time.August, 9, 15, 31, 0, 0, time.UTC)
	withClock(t, now)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"secret", "add", "FIXTURE_SECRET", "--note", "synthetic reference")
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "secret", "list", "--json")
	require.NoError(t, err)
	var refs []storage.SecretRef
	require.NoError(t, json.Unmarshal([]byte(out), &refs))
	require.Equal(t, []storage.SecretRef{{
		Name:      "FIXTURE_SECRET",
		Note:      "synthetic reference",
		CreatedAt: now.Format(time.RFC3339),
	}}, refs)
	assert.JSONEq(t, `[{"name":"FIXTURE_SECRET","note":"synthetic reference","created_at":"2026-08-09T15:31:00Z"}]`, out)
}

func TestSecretCLI_Note(t *testing.T) {
	isolatedHome(t)
	created := time.Date(2026, time.August, 9, 15, 40, 0, 0, time.UTC)
	withClock(t, created)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"secret", "add", "FIXTURE_SECRET", "--note", "first note")
	require.NoError(t, err)

	// Amend the note an hour later; the creation time must not move.
	withClock(t, created.Add(time.Hour))
	out, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"secret", "note", "FIXTURE_SECRET", "rotated 2026-08")
	require.NoError(t, err)
	assert.Equal(t, "Updated the note on FIXTURE_SECRET.\n", out)

	out, _, err = runRoot(t, BuildInfo{Version: "dev"}, "secret", "list", "--json")
	require.NoError(t, err)
	var refs []storage.SecretRef
	require.NoError(t, json.Unmarshal([]byte(out), &refs))
	require.Equal(t, []storage.SecretRef{{
		Name:      "FIXTURE_SECRET",
		Note:      "rotated 2026-08",
		CreatedAt: created.Format(time.RFC3339),
	}}, refs)

	// --clear removes the note, still keeping the creation time.
	out, _, err = runRoot(t, BuildInfo{Version: "dev"}, "secret", "note", "FIXTURE_SECRET", "--clear")
	require.NoError(t, err)
	assert.Equal(t, "Cleared the note on FIXTURE_SECRET.\n", out)

	out, _, err = runRoot(t, BuildInfo{Version: "dev"}, "secret", "list", "--json")
	require.NoError(t, err)
	refs = nil
	require.NoError(t, json.Unmarshal([]byte(out), &refs))
	require.Len(t, refs, 1)
	assert.Empty(t, refs[0].Note)
	assert.Equal(t, created.Format(time.RFC3339), refs[0].CreatedAt)
}

func TestSecretCLI_NoteRequiresTextOrClear(t *testing.T) {
	isolatedHome(t)
	withClock(t, time.Date(2026, time.August, 9, 15, 40, 0, 0, time.UTC))
	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"secret", "add", "FIXTURE_SECRET", "--note", "keep me")
	require.NoError(t, err)

	// Neither text nor --clear: refused — a forgotten argument must not blank it.
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "secret", "note", "FIXTURE_SECRET")
	require.ErrorContains(t, err, "--clear")

	// Both text and --clear: refused as a contradiction.
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "secret", "note", "FIXTURE_SECRET", "x", "--clear")
	require.ErrorContains(t, err, "cannot combine")

	// The note survived both refusals unchanged.
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "secret", "list", "--json")
	require.NoError(t, err)
	var refs []storage.SecretRef
	require.NoError(t, json.Unmarshal([]byte(out), &refs))
	require.Len(t, refs, 1)
	assert.Equal(t, "keep me", refs[0].Note)
}

func TestSecretCLI_NoteRefusesUnknownName(t *testing.T) {
	isolatedHome(t)
	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "secret", "note", "FIXTURE_SECRET", "x")
	require.Error(t, err)
	assert.Empty(t, out)
	assert.Contains(t, errOut, "not registered")
	assert.Contains(t, errOut, "nothing was changed")
}

func TestSecretCLI_RefusesInvalidAndUnknownInputs(t *testing.T) {
	isolatedHome(t)

	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "secret", "add", "lower")
	require.Error(t, err)
	assert.Empty(t, out)
	assert.Contains(t, errOut, "must match ^[A-Z_][A-Z0-9_]*$")
	assert.Contains(t, errOut, "nothing was saved")

	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "secret", "unknown")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
}

func TestSecretCLI_CommandTreeIsCatalogOnly(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})
	secret, _, err := root.Find([]string{"secret"})
	require.NoError(t, err)
	require.Equal(t, "secret", secret.Name())

	got := make([]string, 0, len(secret.Commands()))
	for _, child := range secret.Commands() {
		got = append(got, child.Name())
	}
	assert.ElementsMatch(t, []string{"add", "list", "note", "remove"}, got)
}
