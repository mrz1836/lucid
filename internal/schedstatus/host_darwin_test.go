//go:build darwin

package schedstatus

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunCmd covers the read-only shell-out helper: a real command returns its
// trimmed stdout with ok=true; a missing binary returns ok=false.
func TestRunCmd(t *testing.T) {
	out, ok := runCmd("echo", "  hello  ")
	require.True(t, ok)
	assert.Equal(t, "hello", out)

	_, ok = runCmd("lucid-no-such-binary-xyz")
	assert.False(t, ok, "a missing binary is a clean not-ok, not a panic")
}

// TestPidFromFile covers reading and parsing a pid file, and the missing-file
// degradation.
func TestPidFromFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "pid")
	require.NoError(t, os.WriteFile(p, []byte("321\n"), 0o600))
	pid, ok := pidFromFile(p)
	require.True(t, ok)
	assert.Equal(t, 321, pid)

	_, ok = pidFromFile(filepath.Join(t.TempDir(), "absent"))
	assert.False(t, ok)
}

// TestFileExists covers presence detection for the Hush-socket check.
func TestFileExists(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	require.NoError(t, os.WriteFile(p, []byte("x"), 0o600))
	assert.True(t, fileExists(p))
	assert.False(t, fileExists(filepath.Join(t.TempDir(), "nope")))
}

// TestProcessAliveAndStart: this test process is alive with a readable start
// time, and an implausible pid is not alive.
func TestProcessAliveAndStart(t *testing.T) {
	pid := os.Getpid()
	assert.True(t, processAlive(pid), "the test process is alive")
	assert.False(t, processAlive(999999), "an implausible pid is not alive")

	if start, ok := processStart(pid); ok {
		assert.False(t, start.IsZero(), "a resolved start time is non-zero")
	}
}

// TestBestEffortProbesDoNotPanic exercises the remaining host shell-outs for
// coverage. Their results are host-dependent (the daemon may or may not be
// running here), so it asserts only the invariants that always hold and that the
// calls return without panicking — the whole point of a best-effort probe.
func TestBestEffortProbesDoNotPanic(t *testing.T) {
	_, _ = pgrepScheduler()
	_, _ = launchdInstalled()
	_, _ = supervisedPID()
	_, _ = launchdEnv(envSchedulerDBKey)

	bin, ok := onDiskBinary()
	assert.True(t, ok, "the running executable path always resolves")
	assert.NotEmpty(t, bin)
}
