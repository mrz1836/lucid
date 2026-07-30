//go:build !darwin

package schedinstall

import "context"

// Apply is unsupported off macOS: launchctl has no portable equivalent this
// package drives, so the caller prints [ManualGuidance] instead. This stub keeps
// the package building — and its pure helpers testable — on every platform,
// which is what CI (Linux) exercises.
func Apply(_ context.Context, _ ApplyParams) (ApplyResult, error) {
	return ApplyResult{}, ErrUnsupported
}

// Uninstall is likewise unsupported off macOS.
func Uninstall(_ context.Context, _ UninstallParams) (UninstallResult, error) {
	return UninstallResult{}, ErrUnsupported
}

// WriteArtifacts is unsupported off macOS: the launchd/LaunchAgents layout is
// macOS-specific, so the caller prints [ManualGuidance] instead.
func WriteArtifacts(_ ApplyParams) (ApplyResult, error) {
	return ApplyResult{}, ErrUnsupported
}
