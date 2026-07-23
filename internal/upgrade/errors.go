// Package upgrade implements the lucid self-upgrade pattern: resolve a
// GitHub release for the requested channel, download the platform-
// specific tarball, verify its SHA256 against the published checksums
// file, extract it with Zip-Slip / zip-bomb defenses, and atomically
// replace the running binary via a copy-and-rename dance that is safe
// against the SIGBUS class of in-place binary mutation bugs.
//
// The pattern is the house self-upgrade (ADR-0007), cloned from `hush`
// and `atlas`. On a supervised host the scheduler drives the bell and
// tripwire jobs; the atomic swap guarantees an upgrade never corrupts a
// running scheduler mid-execution.
//
// The package is intentionally self-contained: every external seam
// (release-source lookup, HTTP client, exec path, current version) is
// passed in through [Config] so unit tests can drive every code path
// without touching the network or the real filesystem layout.
//
// Sentinel errors are collected here for easy errors.Is matching by
// [internal/cli] and the exit-code mapper.
package upgrade

import "errors"

// Sentinel error catalog. Every documented failure mode maps to
// exactly one sentinel; errors.Is is the matching primitive used by
// the cobra layer. Sentinel messages are static category strings —
// they never echo user input.
var (
	// Release-lookup errors.
	ErrNoReleasesFound     = errors.New("lucid/upgrade: no releases found")
	ErrNoBetaReleasesFound = errors.New("lucid/upgrade: no beta releases found")
	ErrGHCLIFailed         = errors.New("lucid/upgrade: gh CLI command failed")
	ErrGitHubAPIFailed     = errors.New("lucid/upgrade: GitHub API request failed")

	// Asset / platform errors.
	ErrAssetNotFound       = errors.New("lucid/upgrade: no matching release asset for platform")
	ErrUnsupportedPlatform = errors.New("lucid/upgrade: unsupported platform (require linux/darwin × amd64/arm64)")
	ErrBinaryNotFound      = errors.New("lucid/upgrade: lucid binary not found in extracted files")

	// Download / network errors.
	ErrDownloadFailed = errors.New("lucid/upgrade: download failed")

	// Checksum errors.
	ErrChecksumFetchFailed = errors.New("lucid/upgrade: failed to fetch checksums file")
	ErrChecksumNotFound    = errors.New("lucid/upgrade: checksum not found in checksums file")
	ErrChecksumMismatch    = errors.New("lucid/upgrade: checksum verification failed")
	ErrChecksumMissing     = errors.New("lucid/upgrade: release has no checksums file; refusing to install unverified binary")

	// Extract errors.
	ErrPathTraversal = errors.New("lucid/upgrade: path traversal attempt detected")
	ErrFileTooLarge  = errors.New("lucid/upgrade: extracted file exceeds maximum allowed size")
	ErrNoTarGzFound  = errors.New("lucid/upgrade: no tar.gz file found in update directory")

	// Install errors.
	ErrInstallDirNotWritable = errors.New("lucid/upgrade: install directory not writable")

	// errInvalidSemverTuple is returned by parseVersionTuple when the
	// input is not parseable as major.minor.patch. Kept package-private
	// because callers don't need to errors.Is against it; isNewer
	// already collapses both this and parse-int failures to a single
	// "not newer" outcome.
	errInvalidSemverTuple = errors.New("lucid/upgrade: not a semver tuple")
)
