package validate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// realDenylist() mirrors router.SanctuaryDenylist() so the validate package's
// tests stay decoupled from the router package. The CLI test exercises the
// live router denylist end-to-end.
func realDenylist() []string { return []string{"engine/", "observations/", "registries/"} }

// Synthetic forbidden tokens for grep fixtures, assembled from split literals
// so no test source file ever spells a real internal identity or todo id (they
// land only in temp trees). Both match the S-7 rules — the todo-id shape and
// the private-integration identity — without leaking a real one.
const (
	syntheticTodoID   = "T-" + "777"
	syntheticIdentity = "Z" + "ai"
)

// writeFile writes content to dir/rel, creating parent directories. It is the
// shared fixture helper for the grep checks.
func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
}

// repoRoot resolves the checkout root from the package directory (the test's
// working dir is internal/validate, so the root is two levels up) and asserts
// a go.mod is there.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(root, "go.mod"))
	require.NoError(t, err, "expected go.mod at the resolved repo root")
	return root
}

// TestReport_OKErrorsWarnings covers the report tallies and the OK gate:
// warnings never fail the sweep; a single error does.
func TestReport_OKErrorsWarnings(t *testing.T) {
	empty := Report{}
	assert.True(t, empty.OK())
	assert.Equal(t, 0, empty.Errors())
	assert.Equal(t, 0, empty.Warnings())

	warnOnly := Report{Findings: []Finding{
		{Severity: SeverityWarn},
		{Severity: SeverityWarn},
	}}
	assert.True(t, warnOnly.OK(), "warnings alone do not fail the sweep")
	assert.Equal(t, 0, warnOnly.Errors())
	assert.Equal(t, 2, warnOnly.Warnings())

	mixed := Report{Findings: []Finding{
		{Severity: SeverityError},
		{Severity: SeverityWarn},
	}}
	assert.False(t, mixed.OK())
	assert.Equal(t, 1, mixed.Errors())
	assert.Equal(t, 1, mixed.Warnings())
}

// TestRun_Neither: with no repo and no ledger, the sweep runs nothing and is
// trivially clean.
func TestRun_Neither(t *testing.T) {
	rep, err := Run(Options{})
	require.NoError(t, err)
	assert.True(t, rep.OK())
	assert.Empty(t, rep.Ran)
	assert.Empty(t, rep.Findings)
}

// TestRun_RepoOnlyClean: a minimal clean repo runs the four grep checks and
// reports clean; the schema check is absent (no ledger).
func TestRun_RepoOnlyClean(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/x\n")
	writeFile(t, root, "docs/readme.md", "# ok\nno links here\n")

	rep, err := Run(Options{RepoRoot: root, Denylist: realDenylist()})
	require.NoError(t, err)
	assert.True(t, rep.OK())
	assert.ElementsMatch(t,
		[]string{CheckPublicBoundary, CheckDiagnostic, CheckLinks, CheckSanctuary},
		rep.Ran)
	assert.NotContains(t, rep.Ran, CheckSchema)
}

// TestRun_RepoOnlyDirty: a repo with a boundary leak and a missing denylist
// entry fails, and the findings arrive sorted by check.
func TestRun_RepoOnlyDirty(t *testing.T) {
	root := t.TempDir()
	// Build the forbidden token at runtime so this source file never spells a
	// real internal id; the fixture lands only in the temp tree.
	writeFile(t, root, "notes.txt", "internal ref "+syntheticTodoID+" leaked here\n")

	rep, err := Run(Options{RepoRoot: root, Denylist: []string{"engine/"}})
	require.NoError(t, err)
	assert.False(t, rep.OK())
	// A boundary finding (T-###) and two sanctuary denylist-coverage findings
	// (observations/, registries/ missing).
	assert.GreaterOrEqual(t, rep.Errors(), 3)

	// Sorted by check name: diagnostic < public-boundary < sanctuary.
	for i := 1; i < len(rep.Findings); i++ {
		assert.LessOrEqual(t, rep.Findings[i-1].Check, rep.Findings[i].Check)
	}
}

// TestRun_LedgerOnly: with only a ledger source, only the schema check runs.
func TestRun_LedgerOnly(t *testing.T) {
	rep, err := Run(Options{Ledger: &fakeLedger{}})
	require.NoError(t, err)
	assert.True(t, rep.OK())
	assert.Equal(t, []string{CheckSchema}, rep.Ran)
}

// TestRun_Both: repo + ledger runs all five checks.
func TestRun_Both(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/x\n")

	rep, err := Run(Options{RepoRoot: root, Denylist: realDenylist(), Ledger: &fakeLedger{}})
	require.NoError(t, err)
	assert.True(t, rep.OK())
	assert.Len(t, rep.Ran, 5)
}

// TestRun_PropagatesLedgerListError: a listing that cannot be read surfaces as
// an error from Run, not a finding.
func TestRun_PropagatesLedgerListError(t *testing.T) {
	_, err := Run(Options{Ledger: &fakeLedger{listErr: errSentinel}})
	require.Error(t, err)
}

// TestRun_RepoWalkError: an unreadable repo root surfaces as an error from
// Run (a repo-check failure is internal, not a finding).
func TestRun_RepoWalkError(t *testing.T) {
	_, err := Run(Options{RepoRoot: filepath.Join(t.TempDir(), "missing"), Denylist: realDenylist()})
	require.Error(t, err)
}

// TestRun_RealRepoIsClean is the AC-14 gate: the S-7 public-boundary, S-8
// diagnostic-language, and sanctuary greps report clean against the actual
// checkout (the validate package's own tree is skipped by the walker). Link
// warnings, if any, do not fail the sweep.
func TestRun_RealRepoIsClean(t *testing.T) {
	rep, err := Run(Options{RepoRoot: repoRoot(t), Denylist: realDenylist()})
	require.NoError(t, err)
	for _, f := range rep.Findings {
		if f.Severity == SeverityError {
			t.Errorf("unexpected error finding: %s %s:%d — %s", f.Check, f.Path, f.Line, f.Message)
		}
	}
	assert.True(t, rep.OK(), "the real repo must pass the boundary/diagnostic/sanctuary gates")
	assert.Contains(t, rep.Ran, CheckPublicBoundary)
	assert.Contains(t, rep.Ran, CheckDiagnostic)
	assert.Contains(t, rep.Ran, CheckSanctuary)
}

// TestSortFindings_Stable orders by check, path, line, then rule.
func TestSortFindings_Stable(t *testing.T) {
	fs := []Finding{
		{Check: "schema", Path: "b", Line: 2, Rule: "z"},
		{Check: "schema", Path: "b", Line: 2, Rule: "a"},
		{Check: "public-boundary", Path: "a", Line: 9},
		{Check: "schema", Path: "a", Line: 1},
	}
	sortFindings(fs)
	assert.Equal(t, "public-boundary", fs[0].Check)
	assert.Equal(t, "schema", fs[1].Check)
	assert.Equal(t, "a", fs[1].Path)
	// same check+path+line → rule breaks the tie.
	assert.Equal(t, "a", fs[2].Rule)
	assert.Equal(t, "z", fs[3].Rule)
}
