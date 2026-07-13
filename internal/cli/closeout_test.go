package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseBackfillDate covers the explicit YYYY-MM-DD backfill target parse:
// a well-formed date resolves in the host zone, anything else is the start of
// the compact form (ok=false) and the caller falls through to the router.
func TestParseBackfillDate(t *testing.T) {
	tests := []struct {
		name   string
		tok    string
		wantOK bool
	}{
		{"iso date", "2026-07-05", true},
		{"yesterday keyword", "yesterday", false},
		{"compact form head", "dfx", false},
		{"empty", "", false},
		{"malformed date", "2026-13-40", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseBackfillDate(tt.tok)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				require.NotNil(t, got)
				want, perr := time.ParseInLocation("2006-01-02", tt.tok, time.Now().Location())
				require.NoError(t, perr)
				assert.True(t, got.Equal(want))
			} else {
				assert.Nil(t, got)
			}
		})
	}
}

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
