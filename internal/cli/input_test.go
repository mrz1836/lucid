package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeTemp writes content to a temp file under t.TempDir() and returns its
// path — the on-disk half of the file-input surface under test.
func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "body.txt")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// TestReadBodyFile_Verbatim is the fidelity contract: the reader returns the
// payload byte-for-byte except for stripping exactly one terminal newline.
// Shell metacharacters and leading/trailing spaces survive unchanged — the
// property that stops a journal from being split or truncated by the shell.
func TestReadBodyFile_Verbatim(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "plain text",
			content: "just a simple line\n",
			want:    "just a simple line",
		},
		{
			name:    "metacharacters and surrounding whitespace",
			content: "  & ; | ` $(...) ' \" r&b yoga  \n",
			want:    "  & ; | ` $(...) ' \" r&b yoga  ",
		},
		{
			name:    "no trailing newline is untouched",
			content: "no newline here",
			want:    "no newline here",
		},
		{
			name:    "only one terminal newline is stripped",
			content: "keep this blank line after\n\n",
			want:    "keep this blank line after\n",
		},
		{
			name:    "interior newlines are preserved",
			content: "line one\nline two\n",
			want:    "line one\nline two",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readBodyFile(writeTemp(t, tt.content), nil)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestReadBodyFile_Stdin covers the "-" path: the reader drains cmdIn and
// applies the same one-terminal-newline normalization as a file read.
func TestReadBodyFile_Stdin(t *testing.T) {
	got, err := readBodyFile("-", bytes.NewBufferString("  streamed & piped in  \n"))
	require.NoError(t, err)
	assert.Equal(t, "  streamed & piped in  ", got)

	_, err = readBodyFile("-", nil)
	require.Error(t, err, "a - path with no stdin is a clean error, not a panic")
}

// TestReadBodyFile_MissingFile: an unreadable path is a clean error, never a
// silent empty write.
func TestReadBodyFile_MissingFile(t *testing.T) {
	_, err := readBodyFile(filepath.Join(t.TempDir(), "does-not-exist.txt"), nil)
	require.Error(t, err)
}

// TestReadBodyFile_EmptyFile: a file that is empty (or only the terminal
// newline) supplies nothing, which is a mistake rather than an empty write.
func TestReadBodyFile_EmptyFile(t *testing.T) {
	for _, content := range []string{"", "\n"} {
		_, err := readBodyFile(writeTemp(t, content), nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is empty")
	}
}

// TestReadBodyFile_MultiStdinRejected: stdin is one stream, so at most one
// field may read "-". Two fields both given "-" is rejected before any read.
func TestReadBodyFile_MultiStdinRejected(t *testing.T) {
	err := ensureSingleStdin(map[string]string{
		"--catch-file": "-",
		"--flip-file":  "-",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "one field")

	// One "-" alongside real file paths is fine.
	require.NoError(t, ensureSingleStdin(map[string]string{
		"--catch-file": "-",
		"--flip-file":  "/tmp/flip.txt",
	}))

	// No "-" at all is fine.
	require.NoError(t, ensureSingleStdin(map[string]string{
		"--catch-file": "/tmp/catch.txt",
		"--flip-file":  "/tmp/flip.txt",
	}))
}
