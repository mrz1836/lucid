package cli

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInjury_Registered confirms the verb is on the spine and self-documents in
// --help with its convention flags.
func TestInjury_Registered(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})
	got := map[string]bool{}
	for _, c := range root.Commands() {
		got[c.Name()] = true
	}
	assert.True(t, got["injury"], "injury verb not registered")

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "injury", "--help")
	require.NoError(t, err)
	assert.Contains(t, out, "injury")
	assert.Contains(t, out, "--status")
	assert.Contains(t, out, "--onset")
	assert.Contains(t, out, "--body-area")
}

// TestInjury_CLI_CreateThenAmend runs create then amend through the CLI accept
// surface: the first mention acks "Recorded", the amend acks "Updated" and the
// --json shows created=false with the merged, transitioned record.
func TestInjury_CLI_CreateThenAmend(t *testing.T) {
	isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"injury", "left knee", "--body-area", "left knee", "--cause", "landed wrong")
	require.NoError(t, err)
	assert.Contains(t, out, "Recorded")
	assert.Contains(t, out, "injury")

	out, _, err = runRoot(t, BuildInfo{Version: "dev"},
		"injury", "left knee", "--status", "managed", "--severity", "moderate now", "--json")
	require.NoError(t, err)

	var view registryWriteView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.Equal(t, "injury", view.Kind)
	assert.False(t, view.Created, "the second write amends the same record")
	assert.Equal(t, "managed", view.Status)
	assert.Equal(t, "left knee", view.Fields["body_area"], "the create's field survives the amend")
	assert.Equal(t, "moderate now", view.Fields["severity"])
}

// TestInjury_CLI_JSON confirms the --json shape carries the fields a harness
// needs, and that a backdated onset flows through with its precision.
func TestInjury_CLI_JSON(t *testing.T) {
	isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"injury", "old ankle", "--onset", "2014-09", "--uncertainty", "which ankle?", "--json")
	require.NoError(t, err)

	var view registryWriteView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.Equal(t, "injury", view.Kind)
	assert.True(t, view.Created)
	assert.NotEmpty(t, view.Key)
	assert.Equal(t, "2014-09", view.Fields["onset"])
	assert.Equal(t, "approximate", view.Fields["onset_precision"])
	assert.Equal(t, "which ankle?", view.Fields["uncertainty"])
}

// TestInjury_CLI_RejectsBadStatus surfaces an unknown status as a clean runtime
// error (nothing saved), exercised through the CLI.
func TestInjury_CLI_RejectsBadStatus(t *testing.T) {
	isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "injury", "knee", "--status", "flaring")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing was saved")
}

// TestInjury_CLI_RequiresName confirms a bare `injury` is a usage error (exit 2).
func TestInjury_CLI_RequiresName(t *testing.T) {
	isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "injury")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
}
