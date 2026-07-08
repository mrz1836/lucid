package validate

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWalkTextFiles_VisitsAndSkips: the walker visits text files, skips binary
// ones and the skip-dirs, and passes repo-relative slash paths.
func TestWalkTextFiles_VisitsAndSkips(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.txt", "hello\n")
	writeFile(t, root, "sub/b.md", "world\n")
	writeFile(t, root, "bin.dat", "x\x00y\n")
	writeFile(t, root, ".git/config", "should be skipped\n")
	writeFile(t, root, ".github/env/load-env.sh", "should be skipped\n")
	writeFile(t, root, ".claude/settings.json", "should be skipped\n")
	writeFile(t, root, "internal/validate/self.txt", "should be skipped\n")

	seen := map[string]bool{}
	err := walkTextFiles(root, func(rel string, _ []byte) error {
		seen[rel] = true
		return nil
	})
	require.NoError(t, err)
	assert.True(t, seen["a.txt"])
	assert.True(t, seen["sub/b.md"])
	assert.False(t, seen["bin.dat"], "binary files are skipped")
	assert.False(t, seen[".git/config"], ".git is skipped")
	assert.False(t, seen[".github/env/load-env.sh"], ".github (upstream CI, re-synced) is skipped")
	assert.False(t, seen[".claude/settings.json"], ".claude (local agent config) is skipped")
	assert.False(t, seen["internal/validate/self.txt"], "the validate tree is skipped")
}

// TestWalkTextFiles_FnErrorAborts: an error from the callback aborts the walk.
func TestWalkTextFiles_FnErrorAborts(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.txt", "x\n")
	sentinel := errors.New("stop")
	err := walkTextFiles(root, func(_ string, _ []byte) error { return sentinel })
	require.ErrorIs(t, err, sentinel)
}

// TestSplitLines covers CRLF trimming and the trailing-newline case.
func TestSplitLines(t *testing.T) {
	assert.Nil(t, splitLines(nil))
	assert.Equal(t, []string{"a", "b"}, splitLines([]byte("a\nb\n")))
	assert.Equal(t, []string{"a", "b"}, splitLines([]byte("a\r\nb")))
	assert.Equal(t, []string{"only"}, splitLines([]byte("only")))
}

// TestLooksBinary flags a NUL byte and clears plain text (including large
// text past the sniff window).
func TestLooksBinary(t *testing.T) {
	assert.True(t, looksBinary([]byte("abc\x00def")))
	assert.False(t, looksBinary([]byte("plain text")))
	big := make([]byte, 9000)
	for i := range big {
		big[i] = 'a'
	}
	assert.False(t, looksBinary(big))
}

// TestIsSkippedDir covers both the base-name and repo-relative skip rules.
func TestIsSkippedDir(t *testing.T) {
	assert.True(t, isSkippedDir(".git", ".git"))
	assert.True(t, isSkippedDir(".github", ".github"))
	assert.True(t, isSkippedDir(".claude", ".claude"))
	assert.True(t, isSkippedDir("internal/validate", "validate"))
	assert.False(t, isSkippedDir("internal/agents", "agents"))
}
