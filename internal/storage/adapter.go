// Package storage is the storage adapter: the only code in lucid that
// reads or writes the ~/.lucid/ Ledger tree (architecture.md §4). It
// hides the on-disk layout (data-model.md) behind named ops so agents
// and the router never touch the filesystem directly. This Phase-1
// foundation carries the home resolution, the scaffold routine, config
// read/write, the deterministic person_key derivation, and the
// frontmatter/JSON validators later phases build on.
package storage

import (
	"os"
	"path/filepath"

	"github.com/mrz1836/lucid/internal/config"
)

// EnvHome is the environment variable that overrides the Ledger home.
// It exists so tests (and a second instance) can point lucid at an
// isolated tree and never touch the real ~/.lucid/ (plan.md Approach
// §"Isolated test home").
const EnvHome = "LUCID_HOME"

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
// set, otherwise ~/.lucid/. It resolves the path only; it creates
// nothing.
func DefaultHome() (string, error) {
	if h := os.Getenv(EnvHome); h != "" {
		return h, nil
	}
	uh, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(uh, ".lucid"), nil
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
