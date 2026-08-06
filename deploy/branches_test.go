package deploy

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRenderLaunchd_LintFailurePropagates proves RenderLaunchd never returns
// half-formed output: a params set that renders valid XML but omits a required
// environment variable (LUCID_HOME) fails the lint, and that failure is
// returned rather than the rendered string.
func TestRenderLaunchd_LintFailurePropagates(t *testing.T) {
	p := DefaultLaunchdParams()
	// A syntactically fine render whose env map lacks the required LUCID_HOME
	// key: render succeeds, lint rejects it.
	p.EnvironmentVariables = map[string]string{"PATH": "/usr/bin"}

	out, err := RenderLaunchd(p)
	require.ErrorIs(t, err, ErrLint)
	assert.Empty(t, out, "a lint failure returns no rendered output")
	assert.Contains(t, err.Error(), "LUCID_HOME")
}

// TestLintLaunchd_RejectsConfigFlag covers the `--config` guard: `hush
// supervise` takes its config path positionally, so a plist that passes it with
// a --config flag is a misconfiguration the lint refuses.
func TestLintLaunchd_RejectsConfigFlag(t *testing.T) {
	good, err := RenderLaunchd(DefaultLaunchdParams())
	require.NoError(t, err)

	// Inject a --config flag into the otherwise-clean program arguments.
	withFlag := strings.Replace(good,
		"<string>supervise</string>",
		"<string>supervise</string>\n    <string>--config</string>", 1)

	lintErr := LintLaunchd(withFlag)
	require.ErrorIs(t, lintErr, ErrLint)
	assert.Contains(t, lintErr.Error(), "positionally")
}
