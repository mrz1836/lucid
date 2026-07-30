// Package schedinstall is the macOS host-install seam for the supervised
// scheduler daemon. It writes the launchd plist and the hush supervise config
// (both outside ~/.lucid, so the Sanctuary boundary is untouched) and drives
// launchctl bootstrap/bootout. It renders nothing itself — the top-level deploy
// package renders and lints the artifacts; this package only lays the linted
// bodies down and loads them.
//
// Every decision that can be made without a host lives in this untagged file, so
// the Linux build (and CI) covers it: the argv builders, the service-target and
// path construction, the LaunchAgents resolution, and the non-macOS manual
// guidance. apply_darwin.go is a thin imperative shell (the launchctl shell-out
// plus the write-then-load sequence); apply_other.go returns [ErrUnsupported].
// The split uses only GOOS build tags — no custom tag — so the Linux build never
// needs a `-tags` flag.
package schedinstall

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
)

// ErrUnsupported is returned by [Apply] and [Uninstall] on a non-macOS host:
// launchctl has no portable equivalent this package drives, so off macOS the CLI
// prints [ManualGuidance] instead of attempting a half-install.
var ErrUnsupported = errors.New("schedinstall: host install is supported only on macOS")

// ApplyParams is the fully-resolved input to a host install. PlistBody and
// SuperviseBody are already rendered and linted by the caller (deploy); this
// package writes them verbatim and never inspects a secret.
type ApplyParams struct {
	// Label is the launchd job label (reverse-DNS, e.g. com.lucid.scheduler).
	Label string
	// PlistBody is the rendered, linted launchd plist XML.
	PlistBody string
	// SuperviseBody is the rendered, linted hush supervise TOML.
	SuperviseBody string
	// SuperviseConfigPath is where the supervise config is written — outside
	// ~/.lucid, user-writable (default ~/.hush/supervisors/lucid-scheduler.toml).
	SuperviseConfigPath string
	// LaunchAgentsDir is the directory the plist is written into. Empty means the
	// user's ~/Library/LaunchAgents; a test points it at a temp dir.
	LaunchAgentsDir string
	// Force replaces an already-loaded job (bootout then bootstrap) instead of
	// refusing.
	Force bool
}

// ApplyResult reports what a host install did.
type ApplyResult struct {
	// PlistPath is the absolute path the launchd plist was written to.
	PlistPath string
	// SuperviseConfigPath echoes where the supervise config was written.
	SuperviseConfigPath string
	// Loaded is true when launchctl reported the job loaded after bootstrap.
	Loaded bool
	// Replaced is true when an already-loaded job was booted out first (--force).
	Replaced bool
}

// UninstallParams is the input to a host uninstall.
type UninstallParams struct {
	// Label is the launchd job label to bootout and whose plist to remove.
	Label string
	// LaunchAgentsDir is the directory holding the plist (empty ⇒ default).
	LaunchAgentsDir string
	// DryRun reports the plan without mutating anything.
	DryRun bool
}

// UninstallResult reports what a host uninstall did (or would do, under DryRun).
type UninstallResult struct {
	// PlistPath is the plist path that was (or would be) removed.
	PlistPath string
	// BootedOut is true when launchctl bootout was run (or would be).
	BootedOut bool
	// PlistRemoved is true when the plist was removed (or would be).
	PlistRemoved bool
}

// bootstrapArgs builds the launchctl argv that loads a plist into the per-user
// GUI domain: `launchctl bootstrap gui/<uid> <plistPath>`.
func bootstrapArgs(uid int, plistPath string) []string {
	return []string{"bootstrap", "gui/" + strconv.Itoa(uid), plistPath}
}

// bootoutArgs builds the launchctl argv that unloads a job by label from the
// per-user GUI domain: `launchctl bootout gui/<uid>/<label>`.
func bootoutArgs(uid int, label string) []string {
	return []string{"bootout", guiTarget(uid, label)}
}

// printArgs builds the launchctl argv that queries a job by label:
// `launchctl print gui/<uid>/<label>`. A zero exit means the job is loaded.
func printArgs(uid int, label string) []string {
	return []string{"print", guiTarget(uid, label)}
}

// guiTarget is the `gui/<uid>/<label>` service target launchctl addresses a
// per-user job by.
func guiTarget(uid int, label string) string {
	return "gui/" + strconv.Itoa(uid) + "/" + label
}

// plistPath resolves the launchd plist path for a label under a LaunchAgents dir.
func plistPath(dir, label string) string {
	return filepath.Join(dir, label+".plist")
}

// resolveLaunchAgentsDir returns the override when set, else the user's
// ~/Library/LaunchAgents.
func resolveLaunchAgentsDir(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("schedinstall: resolve home: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents"), nil
}

// removePlist removes the plist, tolerating an already-absent file so an
// uninstall is idempotent. It reports whether a file was actually removed.
func removePlist(path string) (bool, error) {
	err := os.Remove(path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, fmt.Errorf("schedinstall: remove plist: %w", err)
	}
}

// ManualGuidance returns the non-macOS "place these files, then supervise them"
// steps, so the render/lint path stays useful on every platform. It resolves the
// LaunchAgents dir for a concrete path, falling back to a readable placeholder.
func ManualGuidance(p ApplyParams) []string {
	dir := p.LaunchAgentsDir
	if dir == "" {
		if resolved, err := resolveLaunchAgentsDir(""); err == nil {
			dir = resolved
		} else {
			dir = filepath.Join("~", "Library", "LaunchAgents")
		}
	}
	return []string{
		"Automated host install is macOS-only (launchctl). To run the supervised scheduler elsewhere:",
		"  1. Write the launchd plist to " + plistPath(dir, p.Label) + " (or your init system's unit dir).",
		"  2. Write the hush supervise config to " + p.SuperviseConfigPath + ".",
		"  3. Start supervision:  hush supervise " + p.SuperviseConfigPath,
		"  4. Confirm it is live:  lucid scheduler status",
	}
}
