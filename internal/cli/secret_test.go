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
	assert.ElementsMatch(t, []string{"add", "list", "remove"}, got)
}
