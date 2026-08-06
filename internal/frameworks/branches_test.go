package frameworks

import (
	"os"
	"sort"
	"testing"
)

// TestLoadLensEntry_ReadError proves a definition file that cannot be read
// surfaces a labeled read error rather than a partial or silent load.
func TestLoadLensEntry_ReadError(t *testing.T) {
	// The FS has no such entry, so fs.ReadFile fails before any parse.
	if _, err := loadLensEntry(os.DirFS(t.TempDir()), "ghost.md"); err == nil {
		t.Fatal("loadLensEntry over a missing file: want error, got nil")
	}
}

// TestRegistry_Lenses proves the accessor returns every loaded lens sorted by
// id, as a copy the caller cannot use to mutate the registry's own slice.
func TestRegistry_Lenses(t *testing.T) {
	reg, err := NewRegistry(docsFrameworksDir)
	if err != nil {
		t.Fatalf("NewRegistry(%q): %v", docsFrameworksDir, err)
	}

	got := reg.Lenses()
	if len(got) != reg.Len() {
		t.Fatalf("Lenses returned %d lenses, registry holds %d", len(got), reg.Len())
	}
	if ids := lensIDs(got); !sort.StringsAreSorted(ids) {
		t.Errorf("Lenses returned unsorted ids: %v", ids)
	}

	// The returned slice is a copy: mutating it does not disturb the registry.
	if len(got) > 0 {
		got[0] = Lens{ID: "mutated"}
		if again := reg.Lenses(); again[0].ID == "mutated" {
			t.Error("Lenses returned the backing slice; a caller mutation leaked into the registry")
		}
	}
}
