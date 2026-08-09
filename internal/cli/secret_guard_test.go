package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/router"
	"github.com/mrz1836/lucid/internal/storage"
)

func TestSecretGuard_SourceHasNoExternalLifecyclePath(t *testing.T) {
	_, here, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repo := filepath.Clean(filepath.Join(filepath.Dir(here), "..", ".."))
	paths := []string{
		filepath.Join(repo, "internal", "cli", "secret.go"),
		filepath.Join(repo, "internal", "router", "secret.go"),
		filepath.Join(repo, "internal", "storage", "secretref.go"),
	}
	forbidden := []string{
		"hush request",
		"hush secret",
		"hush vault",
		"reveal",
		"--exec",
		`"os/exec"`,
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		text := strings.ToLower(string(raw))
		for _, term := range forbidden {
			assert.NotContainsf(t, text, term, "%s must not contain %q", path, term)
		}
	}
}

func TestSecretGuard_CatalogSurfaceIsValueFree(t *testing.T) {
	routerType := reflect.TypeOf((*router.Router)(nil))
	methods := make([]string, 0, 4)
	for i := range routerType.NumMethod() {
		method := routerType.Method(i)
		if strings.HasPrefix(method.Name, "Secret") {
			methods = append(methods, method.Name)
			assertValueFreeType(t, method.Type, map[reflect.Type]bool{})
		}
	}
	assert.ElementsMatch(t, []string{"SecretAdd", "SecretList", "SecretNote", "SecretRemove"}, methods)

	assertValueFreeType(t, reflect.TypeOf(storage.SecretRefEvent{}), map[reflect.Type]bool{})
	assertValueFreeType(t, reflect.TypeOf(storage.SecretRef{}), map[reflect.Type]bool{})
}

func TestSecretGuard_ExportedCatalogParametersAreValueFree(t *testing.T) {
	_, here, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repo := filepath.Clean(filepath.Join(filepath.Dir(here), "..", ".."))
	paths := []string{
		filepath.Join(repo, "internal", "router", "secret.go"),
		filepath.Join(repo, "internal", "storage", "secretref.go"),
	}
	for _, path := range paths {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		require.NoError(t, err)
		ast.Inspect(file, func(node ast.Node) bool {
			field, fieldOK := node.(*ast.Field)
			if !fieldOK {
				return true
			}
			for _, name := range field.Names {
				assertValueFreeIdentifier(t, path, name.Name)
			}
			return true
		})
	}
}

func assertValueFreeType(t *testing.T, typ reflect.Type, seen map[reflect.Type]bool) {
	t.Helper()
	if typ == nil || seen[typ] {
		return
	}
	seen[typ] = true
	switch typ.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Chan:
		assertValueFreeType(t, typ.Elem(), seen)
	case reflect.Map:
		assertValueFreeType(t, typ.Key(), seen)
		assertValueFreeType(t, typ.Elem(), seen)
	case reflect.Func:
		// Method values include their receiver as input zero. The receiver is
		// infrastructure, not catalog data, so inspect only call arguments.
		for i := 1; i < typ.NumIn(); i++ {
			assertValueFreeType(t, typ.In(i), seen)
		}
		for i := range typ.NumOut() {
			assertValueFreeType(t, typ.Out(i), seen)
		}
	case reflect.Struct:
		for i := range typ.NumField() {
			field := typ.Field(i)
			assertValueFreeIdentifier(t, typ.String(), field.Name)
			assertValueFreeType(t, field.Type, seen)
		}
	case reflect.Invalid,
		reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.Complex64, reflect.Complex128,
		reflect.Interface, reflect.String, reflect.UnsafePointer:
		return
	}
}

func assertValueFreeIdentifier(t *testing.T, source, identifier string) {
	t.Helper()
	lower := strings.ToLower(identifier)
	for _, fragment := range []string{"value", "cipher", "token", "jwt", "payload", "plaintext"} {
		assert.NotContainsf(t, lower, fragment, "%s contains value-bearing identifier %q", source, identifier)
	}
}
