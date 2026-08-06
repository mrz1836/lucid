package claudecli

import (
	"context"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDefaultRunner_FeedsStdinAndReturnsStdout exercises the real process seam
// over a portable stdin-echo utility (`cat`): the runner feeds stdin and
// returns the child's stdout. It skips cleanly where the tool is absent so no
// build environment is required to carry it.
func TestDefaultRunner_FeedsStdinAndReturnsStdout(t *testing.T) {
	if _, err := exec.LookPath("cat"); err != nil {
		t.Skip("cat not available on this platform")
	}
	out, err := defaultRunner(context.Background(), "cat", nil, []byte("hello from stdin"))
	require.NoError(t, err)
	assert.Equal(t, "hello from stdin", string(out))
}

// TestDefaultRunner_NoStdin covers the empty-stdin branch: with no stdin bytes
// the runner still spawns the process and returns its stdout.
func TestDefaultRunner_NoStdin(t *testing.T) {
	echo, err := exec.LookPath("echo")
	if err != nil {
		t.Skip("echo not available on this platform")
	}
	out, rerr := defaultRunner(context.Background(), echo, []string{"ok"}, nil)
	require.NoError(t, rerr)
	assert.Equal(t, "ok\n", string(out))
}
