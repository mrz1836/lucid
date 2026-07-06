package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateCLI_CleanRepo runs `lucid validate` from the checkout with no
// Ledger: the grep checks (against the real repo and the live router denylist)
// pass and the schema check is skipped. This is the AC-14 end-to-end gate.
func TestValidateCLI_CleanRepo(t *testing.T) {
	isolatedHome(t) // LUCID_HOME points at a fresh, uninitialized tree
	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "validate")
	require.NoError(t, err)
	assert.Contains(t, out, "validate: clean")
	assert.Contains(t, out, "public-boundary")
	assert.Contains(t, out, "sanctuary")
	assert.Contains(t, errOut, "skipped: schema check")
}

// TestValidateCLI_JSON emits the machine-readable report; ok is true and the
// four grep checks ran.
func TestValidateCLI_JSON(t *testing.T) {
	isolatedHome(t)
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "validate", "--json")
	require.NoError(t, err)

	var payload validateJSON
	require.NoError(t, json.Unmarshal([]byte(out), &payload))
	assert.True(t, payload.OK)
	assert.Equal(t, 0, payload.Errors)
	assert.Contains(t, payload.Ran, "public-boundary")
	assert.Contains(t, payload.Ran, "diagnostic-language")
	assert.Contains(t, payload.Ran, "sanctuary")
	assert.NotEmpty(t, payload.Repo)
}

// TestValidateCLI_SchemaError: an initialized Ledger with a corrupt processed
// record fails validate with a non-zero exit and a schema finding.
func TestValidateCLI_SchemaError(t *testing.T) {
	home := isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "init")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "processed", "bad.json"), []byte("not json"), 0o600))

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "validate")
	require.ErrorIs(t, err, errValidationFailed)
	assert.Contains(t, out, "error schema")
	assert.Contains(t, out, "processed/bad")
}

// TestValidateCLI_JSONSchemaError: the JSON path also returns the failure
// sentinel and reports the Ledger home.
func TestValidateCLI_JSONSchemaError(t *testing.T) {
	home := isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "init")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "processed", "bad.json"), []byte("nope"), 0o600))

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "validate", "--json")
	require.ErrorIs(t, err, errValidationFailed)

	var payload validateJSON
	require.NoError(t, json.Unmarshal([]byte(out), &payload))
	assert.False(t, payload.OK)
	assert.GreaterOrEqual(t, payload.Errors, 1)
	assert.Equal(t, home, payload.Ledger)
}

// TestValidateCLI_RejectsArgs: validate takes no positional args.
func TestValidateCLI_RejectsArgs(t *testing.T) {
	isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "validate", "extra")
	require.Error(t, err)
}

// TestFindRepoRoot covers both branches: from the package dir it finds the
// checkout; from an unrelated dir it reports none.
func TestFindRepoRoot(t *testing.T) {
	root, ok := findRepoRoot()
	require.True(t, ok)
	_, statErr := os.Stat(filepath.Join(root, "go.mod"))
	require.NoError(t, statErr)

	t.Chdir(t.TempDir()) // a dir outside any Go module
	_, ok = findRepoRoot()
	assert.False(t, ok)
}

// TestValidateCLI_CloseoutNeedsNoModel evidences the AC-14 sanctuary bullet at
// the skill boundary: an Engine verb (`closeout`) completes with every model
// down. The binary configures no provider for closeout — the Engine is
// agent-free — so a compact close-out records the day and acks with no LLM
// anywhere in the path.
func TestValidateCLI_CloseoutNeedsNoModel(t *testing.T) {
	isolatedHome(t)
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "closeout", "dfx", "3/wrist", "the chain ran")
	require.NoError(t, err)
	assert.Contains(t, out, "streak 1")
}

// TestValidateCLI_NoRepoNoLedger: outside a checkout with no Ledger, validate
// runs nothing and is trivially clean, noting both skips.
func TestValidateCLI_NoRepoNoLedger(t *testing.T) {
	isolatedHome(t)
	t.Chdir(t.TempDir())
	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "validate")
	require.NoError(t, err)
	assert.Contains(t, out, "validate: clean")
	assert.Contains(t, errOut, "repo checks")
	assert.Contains(t, errOut, "schema check")
}
