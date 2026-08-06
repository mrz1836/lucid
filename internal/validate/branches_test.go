package validate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// skipIfRoot skips a chmod-based test under a uid that ignores permission bits
// (CI containers sometimes run as root), matching internal/router's guard.
func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
}

// TestRelSlash_ErrorOnMixedAbsRel: relSlash surfaces the filepath.Rel error
// when one path is absolute and the other relative (they cannot be related).
func TestRelSlash_ErrorOnMixedAbsRel(t *testing.T) {
	// Happy path first: a real relative resolution normalizes to slashes.
	got, err := relSlash(filepath.FromSlash("/a/b"), filepath.FromSlash("/a/b/c/d.txt"))
	require.NoError(t, err)
	assert.Equal(t, "c/d.txt", got)

	// An absolute base with a relative target cannot be made relative.
	_, err = relSlash(filepath.FromSlash("/abs/root"), "relative/path")
	require.Error(t, err)
}

// TestWalkTextFiles_ReadErrorPropagates: an unreadable file surfaces the read
// error rather than being silently skipped — the sweep never hides a file it
// could not inspect.
func TestWalkTextFiles_ReadErrorPropagates(t *testing.T) {
	skipIfRoot(t)
	root := t.TempDir()
	secret := filepath.Join(root, "secret.txt")
	require.NoError(t, os.WriteFile(secret, []byte("x\n"), 0o600))
	require.NoError(t, os.Chmod(secret, 0o000))
	t.Cleanup(func() { _ = os.Chmod(secret, 0o600) })

	err := walkTextFiles(root, func(string, []byte) error { return nil })
	require.Error(t, err)
}

// TestCheckDocLinks_WalkErrorPropagates: a repo root that cannot be walked
// surfaces as an error from the link check, not a silent clean.
func TestCheckDocLinks_WalkErrorPropagates(t *testing.T) {
	_, err := CheckDocLinks(filepath.Join(t.TempDir(), "does-not-exist"))
	require.Error(t, err)
}

// TestAgentStringLiterals_StatError: a stat failure that is not "not found"
// (here internal/ is a file, so internal/agents cannot be a directory) is a
// hard error rather than the clean absent-tree degrade.
func TestAgentStringLiterals_StatError(t *testing.T) {
	root := t.TempDir()
	// internal/ is a regular file, so stat of internal/agents fails with a
	// not-a-directory error that is distinct from IsNotExist.
	require.NoError(t, os.WriteFile(filepath.Join(root, "internal"), []byte("x"), 0o600))

	_, err := agentStringLiterals(root)
	require.Error(t, err)
	assert.False(t, os.IsNotExist(err), "the not-found path degrades cleanly; this is a different error")
}

// TestAgentStringLiterals_WalkError: an unreadable directory inside the agent
// tree aborts the walk with its error rather than returning a partial scan.
func TestAgentStringLiterals_WalkError(t *testing.T) {
	skipIfRoot(t)
	root := t.TempDir()
	locked := filepath.Join(root, "internal", "agents", "locked")
	require.NoError(t, os.MkdirAll(locked, 0o755))
	require.NoError(t, os.Chmod(locked, 0o000))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	_, err := agentStringLiterals(root)
	require.Error(t, err)
}

// TestFileStringLiterals_RelError: a successfully parsed file whose path cannot
// be made relative to root surfaces the relSlash error (parse succeeds, the
// path math is what fails).
func TestFileStringLiterals_RelError(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "prompt.go")
	require.NoError(t, os.WriteFile(src, []byte("package p\n\nconst s = \"hi\"\n"), 0o600))

	// An absolute file path cannot be made relative to a relative root.
	_, err := fileStringLiterals("relative-root", src)
	require.Error(t, err)
}
