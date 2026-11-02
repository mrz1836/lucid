package witnessreport

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/storage"
)

// journalReaders is the exact deny set: every *storage.Adapter method that
// reads the Ledger's journal-side content — raw entries, processed entries,
// observations, reflections, insights — and the other personal-content
// readers beside them (the day view, gratitude, reframes, self-facts, focus,
// people, media, retros). Read from internal/storage's exported Read*/List*
// methods; receipts, configs, and the engine projections are deliberately not
// listed, because the report reads its own receipt and the engine's day
// records. Contact check-in readers are pinned separately, by
// TestWitnessReport_NeverReadsContactData.
//
//nolint:gochecknoglobals // a fixed test table
var journalReaders = []string{
	// raw and processed entries — the journal itself
	"ReadRaw", "ListRawIDsInRange",
	"ReadProcessed", "ListProcessedIDs",
	// the day view assembled from them
	"ReadDayView",
	// reflection content (not the reflection receipt)
	"ReadReflection", "ReadReflections", "ListReflectionIDs",
	// insights
	"ReadAcceptedInsights", "ListInsightIDs",
	// the other personal-content records
	"ReadGratitude", "ReadGratitudeAll",
	"ReadReframes", "ReadReframesDay", "ReadReframeByID",
	"ReadSelfFacts",
	"ReadFocus", "ReadFocusByID", "ReadFocusDay",
	"ReadPerson", "ListPeopleKeys", "ReadOffLimitsPersonKeys",
	"ListMedia", "ReadMediaByID", "ReadMediaForDay",
	"ReadRetro", "ReadRetroByID",
}

// journalReaderPrefixes deny whole reader families, so a reader added to one
// later (a new observation or insight query) is denied without editing this
// test: ReadObservation* (ReadObservationByID, ReadObservationsConfig, …Day,
// …Kind, …Range) and ReadInsight* (ReadInsight, ReadInsightsWindow).
//
//nolint:gochecknoglobals // a fixed test table
var journalReaderPrefixes = []string{"ReadObservation", "ReadInsight"}

// fileOpeners are the standard-library calls that open a file's content or
// walk a directory, keyed by import path. The package may use none of them:
// its only file reads are the two os.ReadFile references pinned below.
//
//nolint:gochecknoglobals // a fixed test table
var fileOpeners = map[string][]string{
	"os":            {"Open", "OpenFile", "OpenRoot", "OpenInRoot", "ReadDir", "DirFS"},
	"path/filepath": {"Walk", "WalkDir", "Glob"},
	"io/fs":         {"ReadFile", "ReadDir", "WalkDir", "Glob", "Sub"},
}

// allowedReadFiles are the only places the package may reference os.ReadFile:
// the optional curated-asks file and the week-notes read's default seam. Each
// opens exactly one operator-configured path.
//
//nolint:gochecknoglobals // a fixed test table
var allowedReadFiles = []string{"compose.go:readCuratedAsks", "notes.go:osNotesSeam"}

// deniedReader reports whether a selector name is a journal-side reader.
func deniedReader(name string) bool {
	for _, d := range journalReaders {
		if name == d {
			return true
		}
	}
	for _, p := range journalReaderPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// parsedSource is one non-test source file of this package.
type parsedSource struct {
	name string
	file *ast.File
}

// packageSources parses every non-test .go file in this package.
func packageSources(t *testing.T) []parsedSource {
	t.Helper()
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	fset := token.NewFileSet()
	var out []parsedSource
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		require.NoError(t, perr)
		out = append(out, parsedSource{name: name, file: f})
	}
	require.NotEmpty(t, out, "expected to parse the witnessreport source files")
	return out
}

// importNames maps each import's local name in f to its path.
func importNames(t *testing.T, f *ast.File) map[string]string {
	t.Helper()
	names := map[string]string{}
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		require.NoError(t, err)
		local := path.Base(p)
		if imp.Name != nil {
			local = imp.Name.Name
		}
		names[local] = p
	}
	return names
}

// selectorUse is one selector expression (a call or a method/function value)
// and the top-level declaration it sits in.
type selectorUse struct {
	sel   *ast.SelectorExpr
	owner string // the enclosing function, or "package scope"
}

// selectorUses lists every selector expression in f with its owner.
func selectorUses(f *ast.File) []selectorUse {
	var out []selectorUse
	for _, decl := range f.Decls {
		owner := "package scope"
		if fd, ok := decl.(*ast.FuncDecl); ok {
			owner = fd.Name.Name
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				out = append(out, selectorUse{sel: sel, owner: owner})
			}
			return true
		})
	}
	return out
}

// TestWitnessReport_NoJournalReader pins the structural half of the witness
// report's privacy guarantee (ADR-0015 §14): the package has no reader for
// observations, journal entries, or raw log content, and the week notes did
// not add one. It parses every non-test source file and fails on any use —
// call or method value — of a storage journal-side reader, so the only Ledger
// reads stay the engine projection, the day records, and the report's own
// receipt. It proves the deny set is real by finding every name on
// *storage.Adapter, so a rename cannot silently empty it. And it pins the
// package's file reads: no file-opening or directory-walking call at all, and
// os.ReadFile referenced only by the curated-asks read and the week-notes
// seam — each one operator file on one configured path.
func TestWitnessReport_NoJournalReader(t *testing.T) {
	t.Run("every denied reader exists on the storage adapter", assertDenySetReal)

	sources := packageSources(t)
	t.Run("no source file uses a journal-side reader", func(t *testing.T) {
		assertNoJournalReaderUse(t, sources)
	})
	t.Run("no file-opening or directory-walking call", func(t *testing.T) {
		assertNoFileOpeners(t, sources)
	})
	t.Run("os.ReadFile only reads the curated asks and the week notes", func(t *testing.T) {
		assert.Equal(t, allowedReadFiles, readFileOwners(t, sources))
	})
}

// assertDenySetReal finds every denied name, and at least one method per
// denied prefix, on *storage.Adapter.
func assertDenySetReal(t *testing.T) {
	adapter := reflect.TypeOf((*storage.Adapter)(nil))
	for _, name := range journalReaders {
		_, ok := adapter.MethodByName(name)
		assert.Truef(t, ok, "*storage.Adapter has no %s — the deny set is stale; re-read internal/storage", name)
	}
	for _, prefix := range journalReaderPrefixes {
		var found []string
		for i := range adapter.NumMethod() {
			if m := adapter.Method(i).Name; strings.HasPrefix(m, prefix) {
				found = append(found, m)
			}
		}
		assert.NotEmptyf(t, found, "*storage.Adapter has no %s* reader — the deny set is stale", prefix)
	}
}

// assertNoJournalReaderUse fails on any selector naming a denied reader.
func assertNoJournalReaderUse(t *testing.T, sources []parsedSource) {
	t.Helper()
	var scanned int
	for _, src := range sources {
		for _, u := range selectorUses(src.file) {
			assert.Falsef(t, deniedReader(u.sel.Sel.Name),
				"%s:%s uses %s — the witness report never reads journal-side content", src.name, u.owner, u.sel.Sel.Name)
			scanned++
		}
	}
	assert.Positive(t, scanned, "expected selector expressions to scan")
}

// assertNoFileOpeners fails on io/ioutil and on any banned opener or walker.
func assertNoFileOpeners(t *testing.T, sources []parsedSource) {
	t.Helper()
	for _, src := range sources {
		imports := importNames(t, src.file)
		for local, p := range imports {
			assert.NotEqualf(t, "io/ioutil", p, "%s imports io/ioutil as %s", src.name, local)
		}
		for _, u := range selectorUses(src.file) {
			fn, ok := qualifiedSelector(imports, u.sel)
			if !ok {
				continue
			}
			for _, banned := range fileOpeners[fn.pkg] {
				assert.NotEqualf(t, banned, fn.name,
					"%s:%s uses %s.%s — the package opens no file but its configured ones", src.name, u.owner, fn.pkg, banned)
			}
		}
	}
}

// readFileOwners lists, sorted, "file:owner" for every os.ReadFile use.
func readFileOwners(t *testing.T, sources []parsedSource) []string {
	t.Helper()
	var got []string
	for _, src := range sources {
		imports := importNames(t, src.file)
		for _, u := range selectorUses(src.file) {
			if fn, ok := qualifiedSelector(imports, u.sel); ok && fn.pkg == "os" && fn.name == "ReadFile" {
				got = append(got, src.name+":"+u.owner)
			}
		}
	}
	sort.Strings(got)
	return got
}

// qualifiedName is a package-qualified identifier: an import path and a name.
type qualifiedName struct{ pkg, name string }

// qualifiedSelector resolves a selector on an imported package (os.ReadFile) to
// its import path and name; a selector on anything else is not one.
func qualifiedSelector(imports map[string]string, sel *ast.SelectorExpr) (qualifiedName, bool) {
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return qualifiedName{}, false
	}
	p, ok := imports[id.Name]
	if !ok {
		return qualifiedName{}, false
	}
	return qualifiedName{pkg: p, name: sel.Sel.Name}, true
}
