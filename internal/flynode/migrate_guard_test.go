package flynode

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	// flywheelImportPath is the module whose Migrate this guard polices.
	flywheelImportPath = `"github.com/mrz1836/go-flywheel"`

	// migrateHelperFile is the one production file allowed to call the
	// flywheel Migrate family directly: the shared helper itself.
	migrateHelperFile = "internal/flynode/migrate.go"
)

// TestNoDirectFlywheelMigrate is the structural half of the job-store migrate
// fix. MigrateJobStore centralizes the decision to migrate with index
// reconciliation on; this guard is what stops a daemon added later from quietly
// routing around it.
//
// It matters because the failure is invisible until the next schema bump: a job
// store migrated without reconciliation refuses to start on any index drift, and
// a scheduler node that refuses to start crash-loops on boot. On 2026-07-30 that
// took every node down at once, and fixing the first store found left three more
// looping — the cost of a missed call site is an outage, not a lint nit.
//
// It matches on the AST rather than by grepping text: this package's own doc
// comments and the regression test's seeded DDL both mention flywheel.Migrate by
// name, and a text scan would false-fire on the very documentation explaining
// the guard.
//
// Scope is non-test files only. Test files legitimately call flywheel.Migrate to
// stand up a fresh store — and TestPlainMigrateStillFailsOnDrift must call it,
// since the refusal is what it asserts. A "scheduler node" is production code; a
// store created inside a test has no drift to reconcile.
func TestNoDirectFlywheelMigrate(t *testing.T) {
	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err, "resolve the module root")

	paths, err := productionGoFiles(moduleRoot)
	require.NoError(t, err, "walk the module for production Go files")
	require.NotEmpty(t, paths,
		"expected to scan at least one non-test Go file — a walk that finds nothing must fail, not pass")

	fset := token.NewFileSet()
	for _, rel := range paths {
		if rel == migrateHelperFile {
			continue
		}
		file, parseErr := parser.ParseFile(fset, filepath.Join(moduleRoot, rel), nil, 0)
		require.NoError(t, parseErr, "parse %s", rel)
		reportDirectMigrateCalls(t, fset, rel, file)
	}
}

// productionGoFiles returns every non-test .go file under root, as slash-separated
// paths relative to it, skipping directories that hold no first-party source.
func productionGoFiles(root string) ([]string, error) {
	skipDirs := map[string]bool{".git": true, ".github": true, "vendor": true, "node_modules": true}

	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out, err
}

// reportDirectMigrateCalls fails t for every call to a flywheel Migrate-family
// function in file, naming the offending line and why it matters. It matches the
// whole family — MigrateWithOptions included — because calling that one directly
// with a zero MigrateOpts is the same opt-out as calling Migrate.
func reportDirectMigrateCalls(t *testing.T, fset *token.FileSet, rel string, file *ast.File) {
	t.Helper()

	locals := flywheelIdents(file)
	if len(locals) == 0 {
		return
	}

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !strings.HasPrefix(sel.Sel.Name, "Migrate") {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || !slices.Contains(locals, pkg.Name) {
			return true
		}
		t.Errorf(
			"%s:%d calls %s.%s directly — route it through flynode.MigrateJobStore.\n"+
				"A job store migrated without index reconciliation refuses to start on any index "+
				"drift, and a scheduler node that refuses to start crash-loops on boot. That is the "+
				"2026-07-30 outage this guard exists to prevent; only %s may call it directly.",
			rel, fset.Position(call.Pos()).Line, pkg.Name, sel.Sel.Name, migrateHelperFile,
		)
		return true
	})
}

// flywheelIdents returns the local identifiers file uses for the flywheel
// package: its explicit import alias if it has one, else the package's own name.
// Returning a slice (rather than a single name) keeps the guard honest if a file
// ever imports it twice under different names.
func flywheelIdents(file *ast.File) []string {
	var idents []string
	for _, imp := range file.Imports {
		if imp.Path.Value != flywheelImportPath {
			continue
		}
		if imp.Name == nil {
			idents = append(idents, "flywheel")
			continue
		}
		if imp.Name.Name == "_" || imp.Name.Name == "." {
			continue
		}
		idents = append(idents, imp.Name.Name)
	}
	return idents
}
