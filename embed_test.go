package lucid_test

import (
	"testing"

	lucid "github.com/mrz1836/lucid"
	"github.com/mrz1836/lucid/internal/frameworks"
)

// TestFrameworksFS_LoadsShippedLenses proves the binary's embedded framework
// set resolves through the same registry loader the CLI uses at runtime: all
// six shipped definitions load, so a consented lens is resolvable without the
// docs directory present on disk.
func TestFrameworksFS_LoadsShippedLenses(t *testing.T) {
	reg, err := frameworks.NewRegistryFS(lucid.FrameworksFS())
	if err != nil {
		t.Fatalf("NewRegistryFS(embedded): %v", err)
	}

	want := []string{"attachment-theory", "eight-dates", "four-agreements", "ifs", "nvc", "stoicism"}
	if reg.Len() != len(want) {
		t.Fatalf("embedded registry holds %d lenses, want %d", reg.Len(), len(want))
	}
	for _, id := range want {
		lens, ok := reg.Lens(id)
		if !ok {
			t.Errorf("embedded registry missing lens %q", id)
			continue
		}
		if lens.Label() == "" {
			t.Errorf("lens %q resolved with an empty provenance label", id)
		}
	}
}
