package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExport_Series_CLI(t *testing.T) {
	enableAllObsKinds(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "obs", "pain", "6", "knee")
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "export", "series")
	require.NoError(t, err)
	path := strings.TrimSpace(out)
	assert.True(t, strings.HasSuffix(path, "series.csv"), "the path is posted: %q", path)

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(body), "logical_date,pain,mood,capacity")
}

func TestExport_PacketClinician_CLI(t *testing.T) {
	enableAllObsKinds(t)
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "export", "packet", "clinician")
	require.NoError(t, err)
	path := strings.TrimSpace(out)
	assert.Contains(t, path, "packet_clinician_")

	_, statErr := os.Stat(path)
	require.NoError(t, statErr, "the packet file exists at the posted path")
}

func TestExport_PacketClinician_JSON(t *testing.T) {
	enableAllObsKinds(t)
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "export", "packet", "clinician", "--json")
	require.NoError(t, err)

	var payload map[string]string
	require.NoError(t, json.Unmarshal([]byte(out), &payload))
	assert.Equal(t, "export", payload["command"])
	assert.Equal(t, "clinician", payload["what"])
	assert.Contains(t, payload["path"], "packet_clinician_")
	assert.NotEmpty(t, payload["window_end"])
}

func TestExport_UsageErrors(t *testing.T) {
	enableAllObsKinds(t)
	cases := [][]string{
		{"export"},
		{"export", "bogus"},
		{"export", "packet"},
		{"export", "packet", "bogus"},
		{"export", "packet", "clinician", "@bad"}, // a malformed window override propagates
	}
	for _, args := range cases {
		_, _, err := runRoot(t, BuildInfo{Version: "dev"}, args...)
		assert.Errorf(t, err, "args %v should error", args)
	}
}
