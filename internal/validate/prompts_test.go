package validate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bt is a single backtick, used to embed a raw-string regex literal inside a
// fixture that is itself built from interpreted strings.
const bt = "`"

// writeAgentFile writes a fixture Go file under <root>/internal/agents/<pkg>/.
func writeAgentFile(t *testing.T, root, rel, content string) {
	t.Helper()
	writeFile(t, root, "internal/agents/"+rel, content)
}

// cleanAgentSource is a prompt file whose prose is hypothesis-framed (clean)
// and whose only diagnostic vocabulary lives in a regex literal (skipped as a
// pattern definition, exactly like the real Structuring/Safety blocklists).
func cleanAgentSource() string {
	return "package mirror\n\n" +
		`const cleanPrompt = "I noticed a possible pattern. Does this resonate, or is it off?"` + "\n" +
		"var overclaimPat = regexp.MustCompile(" + bt + `(?i)\b(clearly|obviously)\b` + bt + ")\n" +
		"var labelPat = regexp.MustCompile(" + bt + `(?i)\b(attachment style|narcissist)\b` + bt + ")\n"
}

// TestCheckDiagnostic_Clean: hypothesis prose plus regex-literal patterns
// produces no diagnostic finding.
func TestCheckDiagnostic_Clean(t *testing.T) {
	root := t.TempDir()
	writeAgentFile(t, root, "mirror/mirror.go", cleanAgentSource())

	found, err := CheckDiagnosticLanguage(root)
	require.NoError(t, err)
	assert.Empty(t, found)
}

// TestCheckDiagnostic_Flags: a diagnostic phrase in a prose prompt literal is
// flagged, while the regex-literal "clearly|obviously" beside it is skipped —
// so exactly one finding, anchored to the prose line.
func TestCheckDiagnostic_Flags(t *testing.T) {
	root := t.TempDir()
	src := cleanAgentSource() +
		`const dirtyPrompt = "You always fold under pressure."` + "\n"
	writeAgentFile(t, root, "mirror/mirror.go", src)

	found, err := CheckDiagnosticLanguage(root)
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, CheckDiagnostic, found[0].Check)
	assert.Equal(t, SeverityError, found[0].Severity)
	assert.Equal(t, "internal/agents/mirror/mirror.go", found[0].Path)
	assert.Positive(t, found[0].Line)
}

// TestCheckDiagnostic_SkipsTestFiles: a _test.go file carrying a diagnostic
// phrase is not scanned (tests legitimately quote the forbidden phrases).
func TestCheckDiagnostic_SkipsTestFiles(t *testing.T) {
	root := t.TempDir()
	writeAgentFile(t, root, "mirror/mirror_test.go",
		"package mirror\n\n"+`const s = "You always fold."`+"\n")

	found, err := CheckDiagnosticLanguage(root)
	require.NoError(t, err)
	assert.Empty(t, found)
}

// TestCheckDiagnostic_AbsentTree: no agent tree (validate run outside a
// checkout) yields no findings and no error.
func TestCheckDiagnostic_AbsentTree(t *testing.T) {
	found, err := CheckDiagnosticLanguage(t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, found)
}

// TestCheckDiagnostic_ParseError: an unparseable agent file is a hard error,
// not a silent clean.
func TestCheckDiagnostic_ParseError(t *testing.T) {
	root := t.TempDir()
	writeAgentFile(t, root, "bad/bad.go", "package bad\nthis is not go\n")

	_, err := CheckDiagnosticLanguage(root)
	require.Error(t, err)
}

// TestCheckSanctuary_DenylistCoverage: a denylist missing two subtrees yields
// exactly those two coverage findings.
func TestCheckSanctuary_DenylistCoverage(t *testing.T) {
	root := t.TempDir()
	writeAgentFile(t, root, "mirror/mirror.go", cleanAgentSource())

	found, err := CheckSanctuaryTree(root, []string{"engine/"})
	require.NoError(t, err)
	require.Len(t, found, 2)
	rules := map[string]bool{}
	for _, f := range found {
		assert.Equal(t, "denylist-coverage", f.Rule)
		assert.Equal(t, "router.SanctuaryDenylist", f.Path)
		rules[f.Message] = true
	}
	assert.True(t, rules["sanctuary denylist does not cover observations/"])
	assert.True(t, rules["sanctuary denylist does not cover registries/"])
}

// TestCheckSanctuary_PathLeak: an agent prompt literal that names a sanctuary
// subtree path is flagged; the denylist itself is complete.
func TestCheckSanctuary_PathLeak(t *testing.T) {
	root := t.TempDir()
	src := "package mirror\n\n" +
		`const leak = "then read engine/days/day_1.json for context"` + "\n"
	writeAgentFile(t, root, "mirror/mirror.go", src)

	found, err := CheckSanctuaryTree(root, realDenylist())
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, "sanctuary-path", found[0].Rule)
	assert.Contains(t, found[0].Message, "engine/")
}

// TestCheckSanctuary_Clean: a complete denylist and a clean agent tree pass.
func TestCheckSanctuary_Clean(t *testing.T) {
	root := t.TempDir()
	writeAgentFile(t, root, "mirror/mirror.go", cleanAgentSource())

	found, err := CheckSanctuaryTree(root, realDenylist())
	require.NoError(t, err)
	assert.Empty(t, found)
}

// TestCheckSanctuary_ParseError propagates an unparseable agent file.
func TestCheckSanctuary_ParseError(t *testing.T) {
	root := t.TempDir()
	writeAgentFile(t, root, "bad/bad.go", "package bad\n@@@\n")

	_, err := CheckSanctuaryTree(root, realDenylist())
	require.Error(t, err)
}

// TestLooksLikeRegex covers each metacharacter marker and a plain prose
// negative.
func TestLooksLikeRegex(t *testing.T) {
	for _, s := range []string{
		`(?i)hello`, `word\bboundary`, `(?:group)`, `[a-z]+`, `[0-9]`,
		`\wclass`, `\sspace`, `\ddigit`, `^[start`,
	} {
		assert.Truef(t, looksLikeRegex(s), "expected regex-shaped: %q", s)
	}
	assert.False(t, looksLikeRegex("You always fold under pressure."))
	assert.False(t, looksLikeRegex("I noticed a possible pattern."))
}

// TestContainsString covers the small set helper.
func TestContainsString(t *testing.T) {
	assert.True(t, containsString([]string{"a", "b"}, "b"))
	assert.False(t, containsString([]string{"a", "b"}, "c"))
	assert.False(t, containsString(nil, "a"))
}
