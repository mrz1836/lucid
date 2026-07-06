package cli

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
)

// TestStatusCLI_HumanAfterCloseout: a close-out then `lucid status` renders
// the L0 prose surface with the streak line.
func TestStatusCLI_HumanAfterCloseout(t *testing.T) {
	isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "closeout", "dfx", "3/wrist", "the chain ran")
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "status")
	require.NoError(t, err)
	assert.Contains(t, out, "Streak: 1")
	assert.Contains(t, out, "adherence:")
	assert.Contains(t, out, "Error budget:")
}

// TestStatusCLI_JSON emits the derived projection verbatim for scripts.
func TestStatusCLI_JSON(t *testing.T) {
	isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "closeout", "ddd", "4", "solid")
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "status", "--json")
	require.NoError(t, err)

	var st engine.Status
	require.NoError(t, json.Unmarshal([]byte(out), &st))
	assert.Equal(t, 1, st.CurrentStreak)
	assert.Equal(t, 7, st.Adherence7d.Length)
	assert.Equal(t, 30, st.Adherence30d.Length)
	assert.Equal(t, 4, st.ErrorBudget.Budget)
}

// TestStatusCLI_FreshHomeRebuilds: on a fresh Ledger with no records, status
// regenerates the projection and reports a zero streak.
func TestStatusCLI_FreshHomeRebuilds(t *testing.T) {
	isolatedHome(t)
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "status")
	require.NoError(t, err)
	assert.Contains(t, out, "Streak: 0")
	assert.Contains(t, out, "no decided days yet")
}

// TestStatusCLI_RejectsArgs: status takes no positional args.
func TestStatusCLI_RejectsArgs(t *testing.T) {
	isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "status", "extra")
	require.Error(t, err)
}

// TestStatusCLI_BootError surfaces a Ledger that cannot be scaffolded.
func TestStatusCLI_BootError(t *testing.T) {
	unscaffoldableHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "status")
	require.Error(t, err)
}
