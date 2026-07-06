package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// engineDayCount counts written day records under the isolated home.
func engineDayCount(t *testing.T, home string) int {
	t.Helper()
	var n int
	_ = filepath.WalkDir(filepath.Join(home, "engine", "days"), func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Ext(d.Name()) == ".json" {
			n++
		}
		return nil
	})
	return n
}

func TestCloseoutCLI_CompactWrites(t *testing.T) {
	home := isolatedHome(t)
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "closeout", "dfx", "3/wrist", "Long", "day", "but", "the", "chain", "ran.")
	require.NoError(t, err)
	assert.Contains(t, out, "streak 1.")
	assert.Equal(t, 1, engineDayCount(t, home))

	// A raw journal landed too.
	var raw int
	_ = filepath.WalkDir(filepath.Join(home, "raw"), func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Ext(d.Name()) == ".md" {
			raw++
		}
		return nil
	})
	assert.Equal(t, 1, raw)
}

func TestCloseoutCLI_Skip(t *testing.T) {
	home := isolatedHome(t)
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "closeout", "skip")
	require.NoError(t, err)
	assert.Contains(t, out, "Recorded a miss")
	assert.Equal(t, 1, engineDayCount(t, home))
}

func TestCloseoutCLI_Today(t *testing.T) {
	isolatedHome(t)
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "closeout", "today", "ddd", "4", "solid")
	require.NoError(t, err)
	assert.Contains(t, out, "streak")
}

func TestCloseoutCLI_Backfill(t *testing.T) {
	home := isolatedHome(t)
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "closeout", "backfill", "yesterday", "ddd", "4", "the chain ran")
	require.NoError(t, err)
	assert.Contains(t, out, "Backfilled")
	assert.Equal(t, 1, engineDayCount(t, home))
}

func TestCloseoutCLI_NoArgsIsError(t *testing.T) {
	isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "closeout")
	require.Error(t, err)
	assert.Equal(t, ExitErr, exitCodeForError(err))
}

func TestCloseoutCLI_BadCompactIsError(t *testing.T) {
	isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "closeout", "zz", "3", "bad chars")
	require.Error(t, err)
}

func TestCloseoutCLI_RegisteredInSpine(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})
	var found bool
	for _, c := range root.Commands() {
		if c.Name() == "closeout" {
			found = true
			assert.NotContains(t, c.Short, "not implemented")
		}
	}
	assert.True(t, found, "closeout must be registered")
}
