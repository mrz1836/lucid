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
	"io/fs"
	"os"
	"path"
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

// Label is the provenance string form of the lens, "<id> v<version>" (e.g.
// "stoicism v1"), stamped on a lens-framed insight's provenance.framework so a
// later definition-version bump never retro-colors an already-framed insight
// (docs/frameworks.md §2; docs/mvp/data-model.md §"Insight provenance").
func (l Lens) Label() string {
	return fmt.Sprintf("%s v%d", l.ID, l.Version)
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
// sorted by id. Every Markdown file in dir is treated as a definition. It is a
// thin adapter over [LoadLensesFS] backed by the local directory, so a docs
// checkout and the binary's embedded set travel the identical validation path.
//
// It fails closed: a file whose frontmatter is missing, unterminated, or
// unparseable; a definition missing a required key (id, version, name); or a
// duplicate id across two files all return an error rather than a partial or
// silently-degraded set. A consent layer must never load a broken lens.
func LoadLenses(dir string) ([]Lens, error) {
	return LoadLensesFS(os.DirFS(dir))
}

// LoadLensesFS is the filesystem-agnostic loader [LoadLenses] and the embedded
// registry both build on: a local docs directory arrives as os.DirFS(dir), the
// shipped set as the binary's embedded FS (github.com/mrz1836/lucid.FrameworksFS).
// Every Markdown file at the root of fsys is treated as a definition, and it
// fails closed for the same reasons as [LoadLenses].
func LoadLensesFS(fsys fs.FS) ([]Lens, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("frameworks: read dir: %w", err)
	}
	lenses := make([]Lens, 0, len(entries))
	seen := make(map[string]string) // id -> file it first appeared in
	for _, e := range entries {
		if e.IsDir() || path.Ext(e.Name()) != ".md" {
			continue
		}
		lens, err := loadLensEntry(fsys, e.Name())
		if err != nil {
			return nil, err
		}
		if first, dup := seen[lens.ID]; dup {
			return nil, fmt.Errorf("frameworks: duplicate lens id %q in %s and %s", lens.ID, first, e.Name())
		}
		seen[lens.ID] = e.Name()
		lenses = append(lenses, lens)
	}
	sort.Slice(lenses, func(i, j int) bool { return lenses[i].ID < lenses[j].ID })
	return lenses, nil
}

// loadLensEntry reads one definition file from fsys and validates it. name is
// the root-relative file name (also the error label).
func loadLensEntry(fsys fs.FS, name string) (Lens, error) {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return Lens{}, fmt.Errorf("frameworks: read %q: %w", name, err)
	}
	return parseLens(name, b)
}

// parseLens validates and decodes one definition file's bytes into a Lens,
// reusing the storage frontmatter fence-split idiom. name is used only for
// error messages, so the os- and embed-backed loaders report identically.
func parseLens(name string, b []byte) (Lens, error) {
	front, _, err := storage.SplitFrontmatter(b)
	if err != nil {
		return Lens{}, fmt.Errorf("frameworks: %s: %w", name, err)
	}
	var fm lensFrontmatter
	if err := yaml.Unmarshal(front, &fm); err != nil {
		return Lens{}, fmt.Errorf("frameworks: decode %s frontmatter: %w", name, err)
	}
	if fm.ID == "" {
		return Lens{}, fmt.Errorf("frameworks: %s: missing required frontmatter key %q", name, "id")
	}
	if fm.Version < 1 {
		return Lens{}, fmt.Errorf("frameworks: %s: lens %q has invalid version %d (want >= 1)", name, fm.ID, fm.Version)
	}
	if fm.Name == "" {
		return Lens{}, fmt.Errorf("frameworks: %s: lens %q missing required frontmatter key %q", name, fm.ID, "name")
	}
	// The frontmatter and domain types carry identical fields, so a direct
	// conversion yields the tag-free [Lens] the consent layer works with.
	return Lens(fm), nil
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
	return NewRegistryFS(os.DirFS(dir))
}

// NewRegistryFS loads the definitions from fsys and indexes them by id — the
// constructor the CLI uses over the binary's embedded framework set so the
// active labeled lens resolves without a docs directory on disk. It fails
// closed for the same reasons as [LoadLensesFS].
func NewRegistryFS(fsys fs.FS) (*Registry, error) {
	lenses, err := LoadLensesFS(fsys)
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

// ActiveSelection is the minimal consent view [Registry.ActiveLens] needs: the
// id of the deterministically-selected active framework, or ("", false) for
// the baseline voice. config.Config satisfies it via ActiveFramework, so this
// pure loader binds a selected id to its definition without depending on the
// instance-config package — which lenses a user runs stays instance data
// decided elsewhere (lucid.json).
type ActiveSelection interface {
	ActiveFramework() (string, bool)
}

// ActiveLens resolves the labeled lens that frames a run's proposals. It reads
// the active id from sel (the consent/stack decision made in lucid.json) and
// looks it up in the loaded registry. It returns (Lens, true) only when a lens
// is both selected AND defined; a selected id with no shipped definition fails
// closed to the baseline voice, never a partial or zero-value lens. No rotation
// lives here — sel decides which id is active (deterministic, stack-ordered),
// and this method only binds that id to its definition and version for the
// "<id> v<version>" provenance label.
func (r *Registry) ActiveLens(sel ActiveSelection) (Lens, bool) {
	id, ok := sel.ActiveFramework()
	if !ok {
		return Lens{}, false
	}
	return r.Lens(id)
}
