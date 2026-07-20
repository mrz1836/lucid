// Package lucid is the module root. It carries only the embedded, versioned
// specification assets the compiled binary needs at runtime regardless of where
// it is installed — today, the interpretation-framework definitions under
// docs/frameworks/ that the lens layer (internal/frameworks) loads.
//
// Keeping the embed at the module root is deliberate: a go:embed directive
// cannot reach a sibling tree from a nested package, and the framework specs
// live at docs/frameworks/, so the root is the only package that can hold them.
// Nothing else lives here — the CLI entry point is cmd/lucid, and all product
// logic is under internal/.
package lucid

import (
	"embed"
	"io/fs"
)

// frameworksFS embeds the shipped interpretation-framework definition docs
// (docs/frameworks/*.md). They are lens-neutral, public specification files —
// no instance state, no personal content — so embedding them into the binary
// is safe and makes the lens registry resolvable without the source tree
// present.
//
//go:embed docs/frameworks/*.md
var frameworksFS embed.FS

// FrameworksFS returns a read-only filesystem rooted at the embedded
// docs/frameworks directory: one *.md definition per interpretation lens. The
// lens registry loads it through frameworks.NewRegistryFS so a run resolves the
// active labeled lens from the binary itself, never from a docs directory an
// installed binary would not carry.
func FrameworksFS() fs.FS {
	sub, err := fs.Sub(frameworksFS, "docs/frameworks")
	if err != nil {
		// The embed path is a compile-time constant, so fs.Sub over it cannot
		// fail in a built binary; fall back to the raw FS rather than panic.
		return frameworksFS
	}
	return sub
}
