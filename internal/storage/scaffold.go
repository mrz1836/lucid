package storage

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/mrz1836/lucid/internal/config"
)

// dirPerm and filePerm are the modes for scaffolded Ledger paths. The
// Ledger holds private inner-life content, so it is owner-only
// (local-runtime.md §"Runtime tree").
const (
	dirPerm  fs.FileMode = 0o700
	filePerm fs.FileMode = 0o600
)

// ScaffoldResult reports what a scaffold run changed so callers (the
// `lucid init` command) can print an honest, idempotent summary.
type ScaffoldResult struct {
	// Home is the Ledger root that was scaffolded.
	Home string
	// CreatedDirs lists the Mirror directories created this run (empty
	// on an idempotent re-run).
	CreatedDirs []string
	// WroteConfig is true when this run wrote lucid.json (only when it
	// did not already exist).
	WroteConfig bool
}

// Scaffold creates the six Mirror directories (each with a .keep
// marker) and writes lucid.json if it is missing. It is idempotent:
// existing directories, existing entries under them, and an existing
// lucid.json are all left untouched — a second run makes no changes and
// returns an empty result (acceptance-criteria.md test cases 1.1–1.3).
//
// Scaffold only creates the Mirror tree; the Engine, observations,
// registries, and projections trees are created by their own phases.
func (a *Adapter) Scaffold() (ScaffoldResult, error) {
	cfg := config.Default()
	res := ScaffoldResult{Home: a.home}

	if err := os.MkdirAll(a.home, dirPerm); err != nil {
		return res, fmt.Errorf("storage: create home %q: %w", a.home, err)
	}

	for _, name := range cfg.MirrorDirs() {
		created, err := a.ensureDir(a.dirPath(name))
		if err != nil {
			return res, err
		}
		if created {
			res.CreatedDirs = append(res.CreatedDirs, name)
		}
	}

	wrote, err := a.ensureConfig(cfg)
	if err != nil {
		return res, err
	}
	res.WroteConfig = wrote

	return res, nil
}

// ensureDir creates dir (and a .keep marker inside it) if the directory
// does not already exist. It reports whether it created the directory.
// An existing directory — and anything already inside it — is left
// exactly as found.
func (a *Adapter) ensureDir(dir string) (created bool, err error) {
	info, statErr := os.Stat(dir)
	switch {
	case statErr == nil:
		if !info.IsDir() {
			return false, fmt.Errorf("storage: %q exists but is not a directory", dir)
		}
		// Directory already present: fill only the missing .keep so a
		// pre-existing entry (test case 1.3) is never disturbed.
		if err := a.ensureKeep(dir); err != nil {
			return false, err
		}
		return false, nil
	case errors.Is(statErr, fs.ErrNotExist):
		if err := os.MkdirAll(dir, dirPerm); err != nil {
			return false, fmt.Errorf("storage: create dir %q: %w", dir, err)
		}
		if err := a.ensureKeep(dir); err != nil {
			return false, err
		}
		return true, nil
	default:
		return false, fmt.Errorf("storage: stat dir %q: %w", dir, statErr)
	}
}

// ensureKeep writes an empty .keep marker into dir if one is not
// already present. It never truncates an existing marker.
func (a *Adapter) ensureKeep(dir string) error {
	keep := dir + string(os.PathSeparator) + keepFile
	if _, err := os.Stat(keep); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("storage: stat keep %q: %w", keep, err)
	}
	if err := os.WriteFile(keep, nil, filePerm); err != nil {
		return fmt.Errorf("storage: write keep %q: %w", keep, err)
	}
	return nil
}

// ensureConfig writes lucid.json from cfg only if the file does not yet
// exist. An existing config is never overwritten here — clipping of an
// existing file is a separate, explicit step (see [Adapter.SaveConfig]
// and the router boot). It reports whether it wrote the file.
func (a *Adapter) ensureConfig(cfg config.Config) (wrote bool, err error) {
	path := a.ConfigPath()
	if _, statErr := os.Stat(path); statErr == nil {
		return false, nil
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return false, fmt.Errorf("storage: stat config %q: %w", path, statErr)
	}
	if err := a.SaveConfig(cfg); err != nil {
		return false, err
	}
	return true, nil
}

// LoadConfig reads and parses lucid.json. It does not clip or validate;
// the caller decides when to apply those (the router boot clips).
func (a *Adapter) LoadConfig() (config.Config, error) {
	b, err := os.ReadFile(a.ConfigPath())
	if err != nil {
		return config.Config{}, fmt.Errorf("storage: read config: %w", err)
	}
	cfg, err := config.Unmarshal(b)
	if err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}

// SaveConfig writes cfg to lucid.json, replacing any existing file. It
// is the only path that overwrites the config, so callers must have a
// reason (initial scaffold, or a router-boot clip that changed a
// value).
func (a *Adapter) SaveConfig(cfg config.Config) error {
	b, err := cfg.Marshal()
	if err != nil {
		return err
	}
	if err := os.WriteFile(a.ConfigPath(), b, filePerm); err != nil {
		return fmt.Errorf("storage: write config: %w", err)
	}
	return nil
}
