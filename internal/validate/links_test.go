package validate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCheckDocLinks_ResolvesAndBreaks: a resolvable relative link is clean; a
// missing one is a warning anchored to its line.
func TestCheckDocLinks_ResolvesAndBreaks(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/other.md", "# other\n")
	writeFile(t, root, "docs/index.md",
		"See [other](other.md) and [gone](missing.md).\n"+
			"External [site](https://example.com) and [anchor](#top) are skipped.\n")

	found, err := CheckDocLinks(root)
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, CheckLinks, found[0].Check)
	assert.Equal(t, SeverityWarn, found[0].Severity)
	assert.Equal(t, "broken-link", found[0].Rule)
	assert.Equal(t, "docs/index.md", found[0].Path)
	assert.Equal(t, 1, found[0].Line)
	assert.Contains(t, found[0].Message, "missing.md")
}

// TestCheckDocLinks_SkipsVendoredAndNonDoc: a broken link under .github/ (the
// vendored template) is out of scope, and non-.md files are ignored.
func TestCheckDocLinks_SkipsVendoredAndNonDoc(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".github/CONTRIBUTING.md", "[missing](./AGENTS.md)\n")
	writeFile(t, root, "docs/note.txt", "[missing](nope.md)\n")

	found, err := CheckDocLinks(root)
	require.NoError(t, err)
	assert.Empty(t, found)
}

// TestCheckDocLinks_RootReadmeInScope: a root-level README is checked.
func TestCheckDocLinks_RootReadmeInScope(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "[dead](nowhere.md)\n")

	found, err := CheckDocLinks(root)
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, "README.md", found[0].Path)
}

// TestCheckDocLinks_AnchorAndQueryStripped: a link with a trailing anchor
// resolves against the file path with the anchor removed.
func TestCheckDocLinks_AnchorAndQueryStripped(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/a.md", "# a\n")
	writeFile(t, root, "docs/b.md", "[a](a.md#section) and [self](#top)\n")

	found, err := CheckDocLinks(root)
	require.NoError(t, err)
	assert.Empty(t, found)
}

// TestIsDocFile covers the doc-set membership rule.
func TestIsDocFile(t *testing.T) {
	assert.True(t, isDocFile("docs/mvp/scope.md"))
	assert.True(t, isDocFile("README.md"))
	assert.True(t, isDocFile("CLAUDE.md"))
	assert.False(t, isDocFile(".github/CONTRIBUTING.md"))
	assert.False(t, isDocFile("internal/x/doc.md"))
}

// TestIsCheckableLink covers which targets are worth resolving.
func TestIsCheckableLink(t *testing.T) {
	assert.True(t, isCheckableLink("other.md"))
	assert.True(t, isCheckableLink("../adr/0001.md"))
	assert.False(t, isCheckableLink(""))
	assert.False(t, isCheckableLink("#anchor"))
	assert.False(t, isCheckableLink("//cdn.example.com/x"))
	assert.False(t, isCheckableLink("https://example.com"))
	assert.False(t, isCheckableLink("mailto:a@b.com"))
}

// TestLinkResolves covers relative resolution, anchor stripping, and the
// empty-after-strip (same-file) case.
func TestLinkResolves(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/adr/0001.md", "x\n")
	writeFile(t, root, "docs/index.md", "x\n")

	assert.True(t, linkResolves(root, "docs/index.md", "adr/0001.md"))
	assert.True(t, linkResolves(root, "docs/adr/0001.md", "../index.md"))
	assert.False(t, linkResolves(root, "docs/index.md", "adr/9999.md"))
	// Anchor-only after strip resolves to the same file (true).
	assert.True(t, linkResolves(root, "docs/index.md", "#top"))
}
