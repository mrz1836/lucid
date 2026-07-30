//go:build darwin

package schedinstall

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// launchctlTimeout bounds each launchctl shell-out so a wedged launchctl never
// hangs the install.
const launchctlTimeout = 10 * time.Second

// File modes for the written artifacts. The launchd plist is world-readable
// because launchd reads it and it carries no secret (the token lives in hush,
// ADR-0005); the supervise config is owner-only — it too names but never carries
// a secret, so there is no reason to widen it.
const (
	launchAgentsDirPerm os.FileMode = 0o755
	launchAgentsPerm    os.FileMode = 0o644
	superviseDirPerm    os.FileMode = 0o700
	superviseFilePerm   os.FileMode = 0o600
)

// runLaunchctl shells out to launchctl with a bounded timeout. It is a package
// var so a test swaps it for a recording fake; ctx is the first argument and is
// never stored in a struct.
//
//nolint:gochecknoglobals // test seam for the launchctl shell-out
var runLaunchctl = func(ctx context.Context, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, launchctlTimeout)
	defer cancel()
	//nolint:gosec // args are fixed literals (bootstrap/bootout/print) plus a resolved uid/label/path, never raw user input
	if out, err := exec.CommandContext(ctx, "launchctl", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("schedinstall: launchctl %s: %w: %s", args[0], err, out)
	}
	return nil
}

// Apply performs the macOS host install: write the plist + supervise config,
// then load the job with launchctl (replacing an already-loaded one only with
// Force). Split into writeArtifacts/loadAgent to fit the funlen budget.
func Apply(ctx context.Context, p ApplyParams) (ApplyResult, error) {
	dir, err := resolveLaunchAgentsDir(p.LaunchAgentsDir)
	if err != nil {
		return ApplyResult{}, err
	}
	plistFile := plistPath(dir, p.Label)
	if werr := writeArtifacts(plistFile, p); werr != nil {
		return ApplyResult{}, werr
	}
	res := ApplyResult{PlistPath: plistFile, SuperviseConfigPath: p.SuperviseConfigPath}
	replaced, err := loadAgent(ctx, p, plistFile)
	if err != nil {
		return res, err
	}
	res.Replaced = replaced
	res.Loaded = true
	return res, nil
}

// WriteArtifacts writes the plist + supervise config without loading the job —
// the "stage it, don't start it" path the CLI uses when hush is absent (a launchd
// job with no hush to exec is not worth loading, and pre-loading it would force a
// later --force to replace). It reuses the same write half Apply performs.
func WriteArtifacts(p ApplyParams) (ApplyResult, error) {
	dir, err := resolveLaunchAgentsDir(p.LaunchAgentsDir)
	if err != nil {
		return ApplyResult{}, err
	}
	plistFile := plistPath(dir, p.Label)
	if werr := writeArtifacts(plistFile, p); werr != nil {
		return ApplyResult{}, werr
	}
	return ApplyResult{PlistPath: plistFile, SuperviseConfigPath: p.SuperviseConfigPath}, nil
}

// writeArtifacts writes the supervise config (owner-only, parents 0o700) and the
// launchd plist (0o644 — launchd reads it; it carries no secret). Split out to
// fit the funlen budget.
func writeArtifacts(plistFile string, p ApplyParams) error {
	if err := os.MkdirAll(filepath.Dir(p.SuperviseConfigPath), superviseDirPerm); err != nil {
		return fmt.Errorf("schedinstall: create supervise dir: %w", err)
	}
	if err := os.WriteFile(p.SuperviseConfigPath, []byte(p.SuperviseBody), superviseFilePerm); err != nil {
		return fmt.Errorf("schedinstall: write supervise config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(plistFile), launchAgentsDirPerm); err != nil {
		return fmt.Errorf("schedinstall: create LaunchAgents dir: %w", err)
	}
	// The launchd plist is world-readable by design (launchd reads it); it names
	// the token but carries no value (ADR-0005).
	if err := os.WriteFile(plistFile, []byte(p.PlistBody), launchAgentsPerm); err != nil {
		return fmt.Errorf("schedinstall: write plist: %w", err)
	}
	return nil
}

// loadAgent loads the job: if it is already loaded, refuse unless Force (in which
// case bootout first), then bootstrap and verify the job actually loaded. Verify
// hinges on `launchctl print` succeeding (the job is loaded), not on a process
// being momentarily up, so the launchd spawn race never reads as a false
// negative. Split out to fit the funlen budget.
func loadAgent(ctx context.Context, p ApplyParams, plistFile string) (replaced bool, err error) {
	uid := os.Getuid()
	if runLaunchctl(ctx, printArgs(uid, p.Label)...) == nil {
		if !p.Force {
			return false, fmt.Errorf("schedinstall: launchd job %s is already loaded; pass --force to replace it", p.Label)
		}
		if berr := runLaunchctl(ctx, bootoutArgs(uid, p.Label)...); berr != nil {
			return false, berr
		}
		replaced = true
	}
	if berr := runLaunchctl(ctx, bootstrapArgs(uid, plistFile)...); berr != nil {
		return replaced, berr
	}
	if verr := runLaunchctl(ctx, printArgs(uid, p.Label)...); verr != nil {
		return replaced, fmt.Errorf("schedinstall: job did not load: %w", verr)
	}
	return replaced, nil
}

// Uninstall performs the macOS host uninstall: bootout the job (tolerating "not
// loaded") and remove the plist (tolerating absent). DryRun reports the plan
// without mutating anything.
func Uninstall(ctx context.Context, p UninstallParams) (UninstallResult, error) {
	dir, err := resolveLaunchAgentsDir(p.LaunchAgentsDir)
	if err != nil {
		return UninstallResult{}, err
	}
	plistFile := plistPath(dir, p.Label)
	if p.DryRun {
		return UninstallResult{PlistPath: plistFile, BootedOut: true, PlistRemoved: true}, nil
	}
	// bootout may report "not loaded" — a clean no-op, not a failure — so its
	// error is deliberately tolerated; the plist removal is the load-bearing step.
	_ = runLaunchctl(ctx, bootoutArgs(os.Getuid(), p.Label)...)
	removed, err := removePlist(plistFile)
	if err != nil {
		return UninstallResult{PlistPath: plistFile, BootedOut: true}, err
	}
	return UninstallResult{PlistPath: plistFile, BootedOut: true, PlistRemoved: removed}, nil
}
