package frameworks

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// docsFrameworksDir is the shipped definition set, relative to this package.
const docsFrameworksDir = "../../docs/frameworks"

// TestLoadLenses_ShippedDefinitions proves all six framework docs load with
// the correct id/version/name and that attachment-theory carries its two
// licensed blocklist patterns while every other lens licenses nothing.
func TestLoadLenses_ShippedDefinitions(t *testing.T) {
	lenses, err := LoadLenses(docsFrameworksDir)
	if err != nil {
		t.Fatalf("LoadLenses(%q): %v", docsFrameworksDir, err)
	}

	// attachment-theory is the sole licensing exemplar (docs/frameworks.md §6).
	attachmentLicenses := []string{
		`\b(anxious|avoidant|secure|disorganized) (attach\w*|tendenc\w*|style|type|behavior)\b`,
		`\b(attachment style)\b`,
	}
	want := map[string]struct {
		version  int
		name     string
		licenses []string
	}{
		"attachment-theory": {1, "Attachment Theory (as a reflective lens)", attachmentLicenses},
		"eight-dates":       {1, "Eight Dates (Gottman)", nil},
		"four-agreements":   {1, "The Four Agreements (Ruiz)", nil},
		"ifs":               {1, "IFS (Internal Family Systems, as a reflective lens)", nil},
		"nvc":               {1, "NVC (Nonviolent Communication)", nil},
		"stoicism":          {1, "Stoicism", nil},
	}

	if len(lenses) != len(want) {
		t.Fatalf("loaded %d lenses, want %d: %v", len(lenses), len(want), lensIDs(lenses))
	}
	if got := lensIDs(lenses); !sort.StringsAreSorted(got) {
		t.Errorf("LoadLenses returned unsorted ids: %v", got)
	}

	// Look up every expected lens through the id-indexed registry.
	reg, err := NewRegistry(docsFrameworksDir)
	if err != nil {
		t.Fatalf("NewRegistry(%q): %v", docsFrameworksDir, err)
	}
	if reg.Len() != len(want) {
		t.Fatalf("registry holds %d lenses, want %d", reg.Len(), len(want))
	}

	for id, exp := range want {
		lens, ok := reg.Lens(id)
		if !ok {
			t.Errorf("registry missing lens %q", id)
			continue
		}
		if lens.ID != id {
			t.Errorf("lens %q: ID = %q, want %q", id, lens.ID, id)
		}
		if lens.Version != exp.version {
			t.Errorf("lens %q: Version = %d, want %d", id, lens.Version, exp.version)
		}
		if lens.Name != exp.name {
			t.Errorf("lens %q: Name = %q, want %q", id, lens.Name, exp.name)
		}
		if !equalStrings(lens.Licenses, exp.licenses) {
			t.Errorf("lens %q: Licenses = %v, want %v", id, lens.Licenses, exp.licenses)
		}
	}

	// A missing id resolves to not-found rather than a zero-value lens.
	if _, ok := reg.Lens("no-such-lens"); ok {
		t.Errorf("registry reported an unknown lens id as found")
	}
}

// TestLoadLenses_FailsClosed proves malformed, incomplete, and duplicate
// definitions degrade to a clear error rather than a panic or partial load.
func TestLoadLenses_FailsClosed(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
	}{
		{
			name: "no frontmatter fence",
			files: map[string]string{
				"broken.md": "just a body, no frontmatter\n",
			},
		},
		{
			name: "unterminated frontmatter",
			files: map[string]string{
				"broken.md": "---\nid: stoicism\nversion: 1\nname: Stoicism\n",
			},
		},
		{
			name: "unparseable yaml",
			files: map[string]string{
				"broken.md": "---\nid: [this: is, not valid\n---\nbody\n",
			},
		},
		{
			name: "missing id",
			files: map[string]string{
				"noid.md": "---\nversion: 1\nname: No ID\n---\nbody\n",
			},
		},
		{
			name: "missing name",
			files: map[string]string{
				"noname.md": "---\nid: nameless\nversion: 1\n---\nbody\n",
			},
		},
		{
			name: "invalid version",
			files: map[string]string{
				"badver.md": "---\nid: zeroed\nversion: 0\nname: Zeroed\n---\nbody\n",
			},
		},
		{
			name: "duplicate id",
			files: map[string]string{
				"a.md": "---\nid: dupe\nversion: 1\nname: First\n---\nbody\n",
				"b.md": "---\nid: dupe\nversion: 2\nname: Second\n---\nbody\n",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
					t.Fatalf("write fixture %q: %v", name, err)
				}
			}
			if lenses, err := LoadLenses(dir); err == nil {
				t.Fatalf("LoadLenses over %q: want error, got %d lenses", tc.name, len(lenses))
			}
			if _, err := NewRegistry(dir); err == nil {
				t.Fatalf("NewRegistry over %q: want error, got nil", tc.name)
			}
		})
	}
}

// TestLoadLenses_MissingDir returns an error for an absent directory.
func TestLoadLenses_MissingDir(t *testing.T) {
	if _, err := LoadLenses(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Fatal("LoadLenses over a missing dir: want error, got nil")
	}
}

// TestLoadLenses_SkipsNonMarkdown ignores non-.md entries and empty dirs.
func TestLoadLenses_SkipsNonMarkdown(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignore me"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	lenses, err := LoadLenses(dir)
	if err != nil {
		t.Fatalf("LoadLenses: %v", err)
	}
	if len(lenses) != 0 {
		t.Fatalf("loaded %d lenses from a dir with no definitions, want 0", len(lenses))
	}
}

func lensIDs(lenses []Lens) []string {
	ids := make([]string, len(lenses))
	for i, l := range lenses {
		ids[i] = l.ID
	}
	return ids
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
