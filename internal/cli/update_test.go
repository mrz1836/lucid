package cli

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAttachUpdateCommandWiring locks the seam between the root command and the
// go-selfupdate cobracmd package: the self-update command is registered under
// the name "update" with the "upgrade" alias (preserving the old command name),
// the check/force/verbose boolean flags, and a hidden, inert --use-binary flag.
// The command's behavior itself is covered by the library's own suites, so this
// asserts only the wiring.
func TestAttachUpdateCommandWiring(t *testing.T) {
	root := &cobra.Command{Use: "lucid"}
	attachUpdateCommand(root, "1.2.3")

	var cmd *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "update" {
			cmd = c
			break
		}
	}
	require.NotNil(t, cmd, "root registers an update command")

	assert.Equal(t, "update", cmd.Name(), "the self-update command is named update")
	assert.Contains(t, cmd.Aliases, "upgrade", "the update command keeps the upgrade alias")
	assert.NotEmpty(t, cmd.Short, "the update command has a Short description")
	assert.NotEmpty(t, cmd.Long, "the update command has a Long description")
	assert.NotEmpty(t, cmd.Example, "the update command has an Example section")

	for _, name := range []string{"check", "force", "verbose"} {
		f := cmd.Flags().Lookup(name)
		require.NotNilf(t, f, "the update command registers --%s", name)
		assert.Equalf(t, "bool", f.Value.Type(), "--%s is a boolean flag", name)
	}

	ub := cmd.Flags().Lookup("use-binary")
	require.NotNil(t, ub, "the deprecated --use-binary flag is registered")
	assert.True(t, ub.Hidden, "--use-binary is hidden")
}
