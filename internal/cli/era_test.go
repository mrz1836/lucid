package cli

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEra_Registered confirms the verb is on the spine and self-documents its
// range flags.
func TestEra_Registered(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})
	got := map[string]bool{}
	for _, c := range root.Commands() {
		got[c.Name()] = true
	}
	assert.True(t, got["era"], "era verb not registered")

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "era", "--help")
	require.NoError(t, err)
	assert.Contains(t, out, "era")
	assert.Contains(t, out, "--start")
	assert.Contains(t, out, "--end")
}

// TestEra_CLI_CreateWithRange runs a create with a backdate-aware range through
// the CLI and confirms the --json carries the normalized bounds + precision.
func TestEra_CLI_CreateWithRange(t *testing.T) {
	isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"era", "the college years", "--start", "2009", "--end", "2013-05-31", "--json")
	require.NoError(t, err)

	var view registryWriteView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.Equal(t, "era", view.Kind)
	assert.True(t, view.Created)
	assert.Equal(t, "the college years", view.DisplayName)
	assert.Equal(t, "2009", view.Fields["start"])
	assert.Equal(t, "approximate", view.Fields["start_precision"])
	assert.Equal(t, "2013-05-31", view.Fields["end"])
}

// TestEra_CLI_AckHuman confirms the human ack path for a bare first mention.
func TestEra_CLI_AckHuman(t *testing.T) {
	isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "era", "the road years")
	require.NoError(t, err)
	assert.Contains(t, out, "Recorded")
	assert.Contains(t, out, "era")
}

// TestEra_CLI_RequiresName confirms a bare `era` is a usage error.
func TestEra_CLI_RequiresName(t *testing.T) {
	isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "era")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
}
