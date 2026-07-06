package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unscaffoldableHome points LUCID_HOME under a regular file so the Ledger
// scaffold (and thus bootedRouter) fails deterministically without chmod.
func unscaffoldableHome(t *testing.T) {
	t.Helper()
	f := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(f, []byte("x"), 0o600))
	t.Setenv("LUCID_HOME", filepath.Join(f, "sub"))
}

// withClock pins clockNow to a fixed instant for the duration of a test so
// the time-of-day-sensitive engine commands behave deterministically.
func withClock(t *testing.T, at time.Time) {
	t.Helper()
	prev := clockNow
	clockNow = func() time.Time { return at }
	t.Cleanup(func() { clockNow = prev })
}

func afternoon() time.Time { return time.Date(2026, 7, 5, 14, 0, 0, 0, time.UTC) }
func afterBell() time.Time { return time.Date(2026, 7, 5, 22, 0, 0, 0, time.UTC) }

// TestModeCLI_Accepted declares a mode before the bell and writes the record.
func TestModeCLI_Accepted(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "mode", "green")
	require.NoError(t, err)
	assert.Contains(t, out, "Mode set to green for 2026-07-05.")
	assert.Equal(t, 1, engineDayCount(t, home))
}

// TestModeCLI_RejectedAfterBell prints the fixed copy to stderr and exits
// non-zero without writing a record.
func TestModeCLI_RejectedAfterBell(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afterBell())

	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "mode", "red")
	require.ErrorIs(t, err, errModeNotAccepted)
	assert.Equal(t, ExitErr, exitCodeForError(err))
	assert.Empty(t, out)
	assert.Contains(t, errOut, "Mode is fixed at the bell (21:30).")
	assert.Equal(t, 0, engineDayCount(t, home))
}

// TestModeCLI_Invalid rejects an unknown mode name (time-independent).
func TestModeCLI_Invalid(t *testing.T) {
	isolatedHome(t)
	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "mode", "purple")
	require.ErrorIs(t, err, errModeNotAccepted)
	assert.Empty(t, out)
	assert.Contains(t, errOut, "Mode must be one of green, yellow, or red.")
}

// TestModeCLI_RequiresExactlyOneArg guards the arg count.
func TestModeCLI_RequiresExactlyOneArg(t *testing.T) {
	isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "mode")
	require.Error(t, err)
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "mode", "green", "yellow")
	require.Error(t, err)
}

// TestModeCLI_BootError: a Ledger that cannot be scaffolded surfaces the
// boot error rather than a false success.
func TestModeCLI_BootError(t *testing.T) {
	unscaffoldableHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "mode", "green")
	require.Error(t, err)
}

// TestModeCLI_IgnoresJSON: mode is not a script-facing surface, so --json is
// ignored — the human prose is still what prints.
func TestModeCLI_IgnoresJSON(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "mode", "yellow", "--json")
	require.NoError(t, err)
	assert.Contains(t, out, "Mode set to yellow")
	assert.NotContains(t, out, "{")
}
