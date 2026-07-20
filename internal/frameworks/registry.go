// Package frameworks loads the interpretation-lens definitions that live as
// versioned Markdown specs under docs/frameworks/. Each definition file is a
// framework the user may consent to (docs/frameworks.md) — this package is the
// code layer those specs plug into, and the prerequisite the lens-rotation
// protocol (P-2) is blocked on.
//
// The loader is pure: paths in, structs out. It reads only the shared,
// lens-neutral definition files, never any ~/.lucid instance state, and it
// holds no consent or stack data — which lenses a user runs is instance data
// that lives elsewhere (lucid.json).
package frameworks

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/mrz1836/lucid/internal/storage"
	"gopkg.in/yaml.v3"
)

// Lens is one interpretation framework defined by docs/frameworks/<id>.md
// (binding schema: docs/frameworks.md §2). It carries only the shared,
// lens-neutral metadata; the vocabulary, question templates, and reframe
// shapes stay in the Markdown body, and a user's stack/consent are instance
// data elsewhere. The zero value is not a valid lens.
type Lens struct {
	// ID is the stable identifier, e.g. "stoicism". It is also the file stem.
	ID string
	// Version is the definition version; provenance stamps "<id> v<version>"
	// so a definition change never retro-colors an already-framed insight.
	Version int
	// Name is the human-readable display name.
	Name string
	// Lineage is the tradition/source line the definition answers to.
	Lineage string
	// Licenses are the blocklist patterns this lens unlocks
	// (docs/frameworks.md §6). Almost always empty; attachment-theory is the
	// worked exception. Licenses never inherit through composition (§4).
	Licenses []string
	// Composes names the parent lens ids of a composite lens (§4). Empty for
	// a primitive lens.
	Composes []string
}

// lensFrontmatter mirrors the YAML frontmatter block of a definition file. It
// is decoded then copied into the exported [Lens], keeping the domain type
// free of on-disk tags (the internal/storage insight pattern).
type lensFrontmatter struct {
	ID       string   `yaml:"id"`
	Version  int      `yaml:"version"`
	Name     string   `yaml:"name"`
	Lineage  string   `yaml:"lineage"`
	Licenses []string `yaml:"licenses"`
	Composes []string `yaml:"composes"`
}

// LoadLenses parses every *.md definition in dir into a slice of lenses,
// sorted by id. Every Markdown file in dir is treated as a definition.
//
// It fails closed: a file whose frontmatter is missing, unterminated, or
// unparseable; a definition missing a required key (id, version, name); or a
// duplicate id across two files all return an error rather than a partial or
// silently-degraded set. A consent layer must never load a broken lens.
func LoadLenses(dir string) ([]Lens, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("frameworks: read dir %q: %w", dir, err)
	}
	lenses := make([]Lens, 0, len(entries))
	seen := make(map[string]string) // id -> file it first appeared in
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
			continue
		}
		lens, err := loadLensFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		if first, dup := seen[lens.ID]; dup {
			return nil, fmt.Errorf("frameworks: duplicate lens id %q in %s and %s",
				lens.ID, filepath.Base(first), e.Name())
		}
		seen[lens.ID] = filepath.Join(dir, e.Name())
		lenses = append(lenses, lens)
	}
	sort.Slice(lenses, func(i, j int) bool { return lenses[i].ID < lenses[j].ID })
	return lenses, nil
}

// loadLensFile reads and validates a single definition file, reusing the
// storage frontmatter fence-split idiom.
func loadLensFile(path string) (Lens, error) {
	b, err := os.ReadFile(path) //nolint:gosec // caller-supplied docs dir; pure read of a versioned spec
	if err != nil {
		return Lens{}, fmt.Errorf("frameworks: read %q: %w", path, err)
	}
	front, _, err := storage.SplitFrontmatter(b)
	if err != nil {
		return Lens{}, fmt.Errorf("frameworks: %s: %w", filepath.Base(path), err)
	}
	var fm lensFrontmatter
	if err := yaml.Unmarshal(front, &fm); err != nil {
		return Lens{}, fmt.Errorf("frameworks: decode %s frontmatter: %w", filepath.Base(path), err)
	}
	if fm.ID == "" {
		return Lens{}, fmt.Errorf("frameworks: %s: missing required frontmatter key %q", filepath.Base(path), "id")
	}
	if fm.Version < 1 {
		return Lens{}, fmt.Errorf("frameworks: %s: lens %q has invalid version %d (want >= 1)",
			filepath.Base(path), fm.ID, fm.Version)
	}
	if fm.Name == "" {
		return Lens{}, fmt.Errorf("frameworks: %s: lens %q missing required frontmatter key %q",
			filepath.Base(path), fm.ID, "name")
	}
	return Lens{
		ID:       fm.ID,
		Version:  fm.Version,
		Name:     fm.Name,
		Lineage:  fm.Lineage,
		Licenses: fm.Licenses,
		Composes: fm.Composes,
	}, nil
}

// Registry is an id-indexed, immutable view over a loaded set of lenses. It is
// the lookup surface later phases resolve the active weekly lens through.
type Registry struct {
	byID   map[string]Lens
	sorted []Lens
}

// NewRegistry loads the definitions in dir and indexes them by id. It fails
// closed for the same reasons as [LoadLenses].
func NewRegistry(dir string) (*Registry, error) {
	lenses, err := LoadLenses(dir)
	if err != nil {
		return nil, err
	}
	reg := &Registry{
		byID:   make(map[string]Lens, len(lenses)),
		sorted: lenses,
	}
	for _, l := range lenses {
		reg.byID[l.ID] = l
	}
	return reg, nil
}

// Lens returns the lens with the given id and whether it was found.
func (r *Registry) Lens(id string) (Lens, bool) {
	l, ok := r.byID[id]
	return l, ok
}

// Lenses returns a copy of all loaded lenses, sorted by id.
func (r *Registry) Lenses() []Lens {
	out := make([]Lens, len(r.sorted))
	copy(out, r.sorted)
	return out
}

// Len reports how many lenses the registry holds.
func (r *Registry) Len() int { return len(r.sorted) }
