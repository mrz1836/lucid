package validate

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCheckPublicBoundary_Clean: a tree with only sanctioned names (the ADR
// dependencies) produces no boundary finding.
func TestCheckPublicBoundary_Clean(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/adr.md", "Depends on OpenClaw, Hermes, hush, go-flywheel, go-foundation.\n")
	writeFile(t, root, "main.go", "package main\n// engineering notes: nothing forbidden here\n")

	found, err := CheckPublicBoundaryTree(root)
	require.NoError(t, err)
	assert.Empty(t, found)
}

// TestCheckPublicBoundary_Identity flags a leaked private-integration identity
// and anchors the finding to the right line and rule.
func TestCheckPublicBoundary_Identity(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "scripts/env.sh", "#!/bin/sh\n# Author: "+syntheticIdentity+"\necho ok\n")

	found, err := CheckPublicBoundaryTree(root)
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, CheckPublicBoundary, found[0].Check)
	assert.Equal(t, SeverityError, found[0].Severity)
	assert.Equal(t, "internal-identity", found[0].Rule)
	assert.Equal(t, "scripts/env.sh", found[0].Path)
	assert.Equal(t, 2, found[0].Line)
}

// TestCheckPublicBoundary_TodoID flags an internal todo id.
func TestCheckPublicBoundary_TodoID(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "CHANGELOG.md", "line one\nfixed in "+syntheticTodoID+"\n")

	found, err := CheckPublicBoundaryTree(root)
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, "internal-todo-id", found[0].Rule)
	assert.Equal(t, 2, found[0].Line)
}

// TestCheckPublicBoundary_SkipsBinaryAndSelfDir: binary files are not scanned,
// and the walker never descends into the validate package's own tree.
func TestCheckPublicBoundary_SkipsBinaryAndSelfDir(t *testing.T) {
	root := t.TempDir()
	// A NUL-bearing "binary" carrying the identity — must be skipped.
	writeFile(t, root, "asset.bin", "prefix\x00"+syntheticIdentity+"\n")
	// A file under internal/validate carrying the identity — must be skipped.
	writeFile(t, root, "internal/validate/fixture.txt", syntheticIdentity+"\n")

	found, err := CheckPublicBoundaryTree(root)
	require.NoError(t, err)
	assert.Empty(t, found)
}

// TestCheckPublicBoundary_SkipsToolingInfra: identity/todo tokens inside the
// tooling and infra trees are not leaks. .github is re-synced weekly from an
// upstream CI framework (which ships such tokens in its own attribution) and
// .claude is local agent config; neither is authored Lucid content, so the
// sweep must not descend into them.
func TestCheckPublicBoundary_SkipsToolingInfra(t *testing.T) {
	root := t.TempDir()
	// Mirrors the real regression: an author attribution in the synced CI
	// loader carrying the identity token.
	writeFile(t, root, ".github/env/load-env.sh", "#!/usr/bin/env bash\n# Author: "+syntheticIdentity+"\n")
	writeFile(t, root, ".claude/settings.json", "{\"note\":\""+syntheticTodoID+"\"}\n")

	found, err := CheckPublicBoundaryTree(root)
	require.NoError(t, err)
	assert.Empty(t, found, ".github and .claude are excluded from the sweep")
}

// TestCheckPublicBoundary_WalkError: a nonexistent root is an error, not a
// silent clean.
func TestCheckPublicBoundary_WalkError(t *testing.T) {
	_, err := CheckPublicBoundaryTree(filepath.Join(t.TempDir(), "does-not-exist"))
	require.Error(t, err)
}
