package cli

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestThread_Registered confirms the verb is on the spine and self-documents
// its intent/domain flags — and deliberately exposes no progress/percent/streak
// flag (the obliquity guard).
func TestThread_Registered(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})
	got := map[string]bool{}
	for _, c := range root.Commands() {
		got[c.Name()] = true
	}
	assert.True(t, got["thread"], "thread verb not registered")

	threadCmd := newThreadCmd()
	assert.NotNil(t, threadCmd.Flags().Lookup("intent"))
	assert.NotNil(t, threadCmd.Flags().Lookup("domain"))
	assert.Nil(t, threadCmd.Flags().Lookup("progress"), "a thread has no progress flag")
	assert.Nil(t, threadCmd.Flags().Lookup("percent"), "a thread has no percent flag")
	assert.Nil(t, threadCmd.Flags().Lookup("streak"), "a thread has no streak flag")
}

// TestThread_CLI_IntentAndDomains runs a create with an intent + repeated
// --domain flags through the CLI and confirms the --json shape.
func TestThread_CLI_IntentAndDomains(t *testing.T) {
	isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"thread", "learning to rest", "--intent", "recovery as practice",
		"--domain", "body", "--domain", "mind", "--json")
	require.NoError(t, err)

	var view registryWriteView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.Equal(t, "thread", view.Kind)
	assert.True(t, view.Created)
	assert.Equal(t, "recovery as practice", view.Fields["intent"])
	assert.Equal(t, []any{"body", "mind"}, view.Fields["domains"])
	// No progress-shaped key on the record (the obliquity guard).
	for key := range view.Fields {
		assert.NotContains(t, key, "progress")
		assert.NotContains(t, key, "percent")
		assert.NotContains(t, key, "streak")
	}
}

// TestThread_CLI_RequiresName confirms a bare `thread` is a usage error.
func TestThread_CLI_RequiresName(t *testing.T) {
	isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "thread")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
}
