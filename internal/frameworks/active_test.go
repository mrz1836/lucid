package frameworks

import (
	"testing"

	"github.com/mrz1836/lucid/internal/config"
)

// fakeSelection is a minimal [ActiveSelection] for exercising resolution edge
// cases (a selected-but-undefined id) without standing up a full config.
type fakeSelection struct {
	id string
	ok bool
}

func (f fakeSelection) ActiveFramework() (string, bool) { return f.id, f.ok }

// TestActiveLens_ResolvesConsentedStackLens proves a real consented stack
// resolves to the shipped lens definition and its "<id> v<version>" label,
// picking the deterministically-first consented lens (the stack head).
func TestActiveLens_ResolvesConsentedStackLens(t *testing.T) {
	reg, err := NewRegistry(docsFrameworksDir)
	if err != nil {
		t.Fatalf("NewRegistry(%q): %v", docsFrameworksDir, err)
	}

	cfg := config.Default()
	cfg.FrameworkStack = []string{"stoicism", "nvc"}
	cfg.FrameworkConsents = map[string]string{
		"stoicism": "2026-07-05T18:00:00-04:00",
		"nvc":      "2026-07-05T18:02:00-04:00",
	}

	lens, ok := reg.ActiveLens(cfg)
	if !ok {
		t.Fatal("ActiveLens: want a resolved lens for a consented stack, got none")
	}
	if lens.ID != "stoicism" {
		t.Errorf("ActiveLens picked %q, want the stack head %q", lens.ID, "stoicism")
	}
	if got, want := lens.Label(), "stoicism v1"; got != want {
		t.Errorf("Label() = %q, want %q", got, want)
	}
}

// TestActiveLens_UnconsentedResolvesNone proves an empty or unconsented stack
// yields the baseline voice (no lens), never a zero-value lens.
func TestActiveLens_UnconsentedResolvesNone(t *testing.T) {
	reg, err := NewRegistry(docsFrameworksDir)
	if err != nil {
		t.Fatalf("NewRegistry(%q): %v", docsFrameworksDir, err)
	}

	// Default config: empty stack, nothing consented.
	if lens, ok := reg.ActiveLens(config.Default()); ok {
		t.Errorf("ActiveLens over an empty stack: want none, got %q", lens.ID)
	}

	// A stack entry with no recorded consent still fails closed.
	cfg := config.Default()
	cfg.FrameworkStack = []string{"ifs"} // stacked but no matching consent timestamp
	if lens, ok := reg.ActiveLens(cfg); ok {
		t.Errorf("ActiveLens over an unconsented stack entry: want none, got %q", lens.ID)
	}
}

// TestActiveLens_SelectedButUndefinedFailsClosed proves a selected id with no
// shipped definition degrades to the baseline voice rather than a partial lens.
func TestActiveLens_SelectedButUndefinedFailsClosed(t *testing.T) {
	reg, err := NewRegistry(docsFrameworksDir)
	if err != nil {
		t.Fatalf("NewRegistry(%q): %v", docsFrameworksDir, err)
	}
	if lens, ok := reg.ActiveLens(fakeSelection{id: "no-such-lens", ok: true}); ok {
		t.Errorf("ActiveLens over an undefined id: want none, got %q", lens.ID)
	}
	if lens, ok := reg.ActiveLens(fakeSelection{ok: false}); ok {
		t.Errorf("ActiveLens over an unselected config: want none, got %q", lens.ID)
	}
}

// TestLens_Label pins the "<id> v<version>" provenance string form for a couple
// of shipped lenses.
func TestLens_Label(t *testing.T) {
	reg, err := NewRegistry(docsFrameworksDir)
	if err != nil {
		t.Fatalf("NewRegistry(%q): %v", docsFrameworksDir, err)
	}
	for id, want := range map[string]string{
		"stoicism": "stoicism v1",
		"nvc":      "nvc v1",
	} {
		lens, ok := reg.Lens(id)
		if !ok {
			t.Fatalf("registry missing %q", id)
		}
		if got := lens.Label(); got != want {
			t.Errorf("Label() for %q = %q, want %q", id, got, want)
		}
	}
}
