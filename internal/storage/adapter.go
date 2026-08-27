// Package storage is the storage adapter: the only code in lucid that
// reads or writes the ~/.lucid/ Ledger tree (architecture.md §4). It
// hides the on-disk layout (data-model.md) behind named ops so agents
// and the router never touch the filesystem directly. This Phase-1
// foundation carries the home resolution, the scaffold routine, config
// read/write, the deterministic person_key derivation, and the
// frontmatter/JSON validators later phases build on.
package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrz1836/lucid/internal/config"
)

// EnvHome is the environment variable that overrides the Ledger home.
// It exists so tests (and a second instance) can point lucid at an
// isolated tree and never touch the real ~/.lucid/ (plan.md Approach
// §"Isolated test home").
const EnvHome = "LUCID_HOME"

// EnvRefuseRealHome is the environment variable that, when set to a truthy
// value (1/true), makes DefaultHome refuse to resolve the real ~/.lucid Ledger
// and return an error instead of a path. The verification harness exports it so
// any CLI-invoking verification step — not just Go tests — is structurally
// prevented from writing the real Ledger while a suite runs.
const EnvRefuseRealHome = "LUCID_REFUSE_REAL_HOME"

// keepFile is the marker written into each scaffolded directory to
// prove the path is writable and keep the (otherwise empty) directory
// present in a fresh Ledger (acceptance-criteria.md test case 1.1).
const keepFile = ".keep"

// configFile is the lucid.json basename at the Ledger root.
const configFile = "lucid.json"

// Fixed Ledger subtree names and id prefixes. The layout is stable
// (data-model.md §"Top-level layout"); these match the documented
// defaults the scaffold writes, so capture and read resolve the same
// paths without threading the config through every op.
const (
	rawDirName      = "raw"
	sessionsDirName = "sessions"
	rawIDPrefix     = "raw_"
	sessionIDPrefix = "session_"
)

// Adapter owns all access to a single ~/.lucid/ home. Construct it with
// [New] (an explicit home, used by tests) or [Open] (the resolved
// default). Nothing outside this package should hold the home path.
type Adapter struct {
	home string
	// enrichFetch is the outbound transport for the enrichment job's single
	// audited network op. It is nil in production (a default https getter is
	// used) and injected by tests so no real socket ever opens.
	enrichFetch EnrichmentFetcher
	// subjects is the per-adapter link-subject resolver table, lazily seeded
	// from defaultSubjectResolvers on first use (see linksubject.go). It lives on
	// the adapter rather than a package global so a registered kind never leaks
	// between instances or tests, and so the open taxonomy costs one resolver
	// per kind — not a mutable global the linter (gochecknoglobals) would reject.
	subjects map[string]SubjectResolver
}

// New returns an adapter rooted at an explicit home directory. Tests
// pass a t.TempDir() so they never touch the real Ledger.
func New(home string) *Adapter {
	return &Adapter{home: home}
}

// Open resolves the Ledger home from the LUCID_HOME override or the
// user's home directory and returns an adapter for it. It does not
// create anything on disk — call [Adapter.Scaffold] for that.
func Open() (*Adapter, error) {
	home, err := DefaultHome()
	if err != nil {
		return nil, err
	}
	return New(home), nil
}

// DefaultHome returns the Ledger home path: the LUCID_HOME override when
// set, otherwise ~/.lucid/. It resolves the path only; it creates nothing.
//
// Two guards protect the real Ledger from synthetic or fat-fingered writes. If
// the resolved home is the real ~/.lucid — whether reached via the default
// fallback or an explicit LUCID_HOME that names that path — DefaultHome refuses
// (returns an error) when LUCID_REFUSE_REAL_HOME is set, and panics under
// `go test` so an unisolated test is caught the instant it would touch the real
// Ledger. See [guardRealHome].
func DefaultHome() (string, error) {
	override := os.Getenv(EnvHome)

	uh, err := os.UserHomeDir()
	if err != nil {
		// Without a resolvable user home there is no real ~/.lucid to guard
		// against: honor an explicit override, otherwise surface the
		// resolution error (the deliberate TestCovOpen_DefaultHomeError case).
		if override != "" {
			return override, nil
		}
		return "", err
	}

	realHome := filepath.Join(uh, ".lucid")
	resolved := realHome
	if override != "" {
		resolved = override
	}

	if guardErr := guardRealHome(resolved, realHome); guardErr != nil {
		return "", guardErr
	}
	return resolved, nil
}

// guardRealHome protects the real ~/.lucid Ledger. When the selected home
// resolves to the real Ledger it (a) refuses with a clean error if the
// LUCID_REFUSE_REAL_HOME verification signal is set — checked first so the
// refusal stays unit-testable — and (b) panics under `go test` so an unisolated
// test is caught the instant it would touch the real Ledger. A home that is not
// the real Ledger passes through untouched.
func guardRealHome(resolved, realHome string) error {
	if filepath.Clean(resolved) != filepath.Clean(realHome) {
		return nil
	}
	if refuseRealHome() {
		return fmt.Errorf(
			"refusing to use the real Ledger at %s: %s is set; point %s at an isolated tree",
			realHome, EnvRefuseRealHome, EnvHome,
		)
	}
	if testing.Testing() {
		panic("lucid: a test resolved the Ledger to the real ~/.lucid; set LUCID_HOME to an isolated t.TempDir()")
	}
	return nil
}

// refuseRealHome reports whether the LUCID_REFUSE_REAL_HOME verification signal
// is set to a truthy value (1/true, case-insensitive, surrounding space
// ignored).
func refuseRealHome() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvRefuseRealHome))) {
	case "1", "true":
		return true
	default:
		return false
	}
}

// Home returns the absolute Ledger root this adapter manages.
func (a *Adapter) Home() string { return a.home }

// ConfigPath returns the path to lucid.json at the Ledger root.
func (a *Adapter) ConfigPath() string { return filepath.Join(a.home, configFile) }

// MirrorDirPaths returns the absolute paths of the six Mirror
// directories for the given config, in scaffold order.
func (a *Adapter) MirrorDirPaths(cfg config.Config) []string {
	names := cfg.MirrorDirs()
	paths := make([]string, len(names))
	for i, n := range names {
		paths[i] = a.dirPath(n)
	}
	return paths
}

// dirPath joins a directory name (from the config) onto the Ledger
// root. It is the single place a home-relative path is built.
func (a *Adapter) dirPath(name string) string { return filepath.Join(a.home, name) }
