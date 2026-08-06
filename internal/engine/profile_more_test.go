package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestComputeSwitch_GoverningProfileWithoutClocksErrors: when the profile that
// currently governs (the `from`) has no resolvable clocks in the chain — a
// history that names a profile the chain never defined — ComputeSwitch surfaces
// the clock-resolution error rather than stamping a switch off a phantom
// profile. The target name is valid, so the rejection is specifically the
// governing profile's missing clocks.
func TestComputeSwitch_GoverningProfileWithoutClocksErrors(t *testing.T) {
	chain := DefaultChain() // defines "default" and "nights"
	state := ProfileState{
		Active:  "ghost",
		History: []ProfileSwitch{{To: "ghost", Effective: "2020-01-01"}},
	}
	_, err := ComputeSwitch(chain, state, "nights", at(2026, 7, 7, 21, 50))
	require.Error(t, err, "the governing 'ghost' profile has no clocks to resolve")
	assert.Contains(t, err.Error(), "ghost")
}
