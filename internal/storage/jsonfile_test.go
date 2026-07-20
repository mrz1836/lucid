package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jfRec is a small record type for exercising the generic JSON skeleton.
type jfRec struct {
	A string `json:"a"`
	N int    `json:"n"`
}

func seedFile(t *testing.T, path, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(body), filePerm))
}

// TestReadJSON covers the required-file reader: a missing file is an error and
// a corrupt body is a parse error, each carrying the caller's label.
func TestReadJSON(t *testing.T) {
	dir := t.TempDir()
	valid := filepath.Join(dir, "valid.json")
	seedFile(t, valid, `{"a":"x","n":7}`)
	corrupt := filepath.Join(dir, "corrupt.json")
	seedFile(t, corrupt, `{not json`)

	t.Run("valid", func(t *testing.T) {
		got, err := readJSON[jfRec](valid, "valid.json")
		require.NoError(t, err)
		assert.Equal(t, jfRec{A: "x", N: 7}, got)
	})
	t.Run("missing is read error", func(t *testing.T) {
		_, err := readJSON[jfRec](filepath.Join(dir, "nope.json"), "nope.json")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "read nope.json")
	})
	t.Run("corrupt is parse error", func(t *testing.T) {
		_, err := readJSON[jfRec](corrupt, "corrupt.json")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "parse corrupt.json")
	})
}

// TestWriteJSON covers the marshal-then-write half, including both failure
// wraps. writeJSON deliberately does not create the parent directory, so a
// missing parent surfaces as a write error.
func TestWriteJSON(t *testing.T) {
	dir := t.TempDir()

	t.Run("roundtrip", func(t *testing.T) {
		path := filepath.Join(dir, "out.json")
		require.NoError(t, writeJSON(path, "out.json", jfRec{A: "y", N: 3}))
		got, err := readJSON[jfRec](path, "out.json")
		require.NoError(t, err)
		assert.Equal(t, jfRec{A: "y", N: 3}, got)
	})
	t.Run("marshal error", func(t *testing.T) {
		err := writeJSON(filepath.Join(dir, "bad.json"), "bad.json", make(chan int))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "marshal bad.json")
	})
	t.Run("write error on missing parent", func(t *testing.T) {
		err := writeJSON(filepath.Join(dir, "missing", "x.json"), "x.json", jfRec{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "write x.json")
	})
}

// TestReadJSONResilient covers the ephemeral reader: both a missing file and a
// corrupt body reset to the zero value (never blocking capture), while an
// unexpected read error still surfaces.
func TestReadJSONResilient(t *testing.T) {
	dir := t.TempDir()
	valid := filepath.Join(dir, "valid.json")
	seedFile(t, valid, `{"a":"z","n":9}`)
	corrupt := filepath.Join(dir, "corrupt.json")
	seedFile(t, corrupt, `{oops`)

	t.Run("valid", func(t *testing.T) {
		got, err := readJSONResilient[jfRec](valid, "valid.json")
		require.NoError(t, err)
		assert.Equal(t, jfRec{A: "z", N: 9}, got)
	})
	t.Run("missing resets to zero", func(t *testing.T) {
		got, err := readJSONResilient[jfRec](filepath.Join(dir, "nope.json"), "nope.json")
		require.NoError(t, err)
		assert.Equal(t, jfRec{}, got)
	})
	t.Run("corrupt resets to zero", func(t *testing.T) {
		got, err := readJSONResilient[jfRec](corrupt, "corrupt.json")
		require.NoError(t, err)
		assert.Equal(t, jfRec{}, got)
	})
	t.Run("unexpected read error surfaces", func(t *testing.T) {
		// A directory at the path is neither missing nor a decode failure.
		subdir := filepath.Join(dir, "adir")
		require.NoError(t, os.Mkdir(subdir, dirPerm))
		_, err := readJSONResilient[jfRec](subdir, "adir")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "read adir")
	})
}

// TestReadJSONOptional covers the may-not-exist reader: a missing file is
// (zero, false, nil), a corrupt body is still a parse error, and a present
// file is (value, true, nil).
func TestReadJSONOptional(t *testing.T) {
	dir := t.TempDir()
	valid := filepath.Join(dir, "valid.json")
	seedFile(t, valid, `{"a":"q","n":1}`)
	corrupt := filepath.Join(dir, "corrupt.json")
	seedFile(t, corrupt, `nope`)

	t.Run("valid found", func(t *testing.T) {
		got, found, err := readJSONOptional[jfRec](valid, "valid.json")
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, jfRec{A: "q", N: 1}, got)
	})
	t.Run("missing not found", func(t *testing.T) {
		got, found, err := readJSONOptional[jfRec](filepath.Join(dir, "nope.json"), "nope.json")
		require.NoError(t, err)
		assert.False(t, found)
		assert.Equal(t, jfRec{}, got)
	})
	t.Run("corrupt is parse error", func(t *testing.T) {
		_, found, err := readJSONOptional[jfRec](corrupt, "corrupt.json")
		require.Error(t, err)
		assert.False(t, found)
		assert.Contains(t, err.Error(), "parse corrupt.json")
	})
}
