package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/upgrade"
)

// releaseSourceForTests, when non-nil, overrides the production release
// source (gh CLI with REST API fallback) inside newUpgradeCmd. Set
// only by upgrade_test.go so the cobra layer can exercise the full
// pipeline without touching the network.
//
//nolint:gochecknoglobals // test-only seam; never set outside _test.go
var releaseSourceForTests upgrade.ReleaseSource

// execPathForTests, when non-empty, overrides Config.ExecPath inside
// newUpgradeCmd so tests can target a writable temp directory instead
// of the real binary.
//
//nolint:gochecknoglobals // test-only seam; never set outside _test.go
var execPathForTests string

// Flag names for `lucid upgrade`. Centralized for the tests that read
// flag values back via cmd.Flags().GetX.
const (
	upgradeFlagCheck   = "check"
	upgradeFlagForce   = "force"
	upgradeFlagChannel = "channel"
)

// newUpgradeCmd wires `lucid upgrade` — the house self-upgrade cloned
// from `hush`/`atlas` (ADR-0007): resolve the latest GitHub release,
// verify its SHA-256, and swap the running binary into place
// atomically so a running scheduler is never corrupted mid-run.
func newUpgradeCmd(bi BuildInfo) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade lucid in place from a GitHub release",
		Long: `Upgrade replaces the running lucid binary with the latest release
published on GitHub. The matching platform tarball is downloaded,
verified against the published SHA-256 checksums file, extracted, and
swapped into place atomically (copy → <dst>.new → rename) so a running
lucid scheduler is not corrupted mid-execution.

Channel selection follows the UPDATE_CHANNEL environment variable
(stable | beta | edge, case-insensitive; default stable). The
--channel flag overrides UPDATE_CHANNEL when both are set.

The install target is the resolved path of the currently running lucid
binary (os.Executable with symlinks evaluated). If that directory is
not writable the command exits with a clear error naming the directory
— it never silently installs elsewhere.

On a supervised host, upgrade is invoked through the managed-upgrade
flow and honors the drain window — never between the evening bell and
the morning close-out (ADR-0007, P10).`,
		Args: cobra.NoArgs,
		Example: `  # Check whether a newer release is available (no install).
  lucid upgrade --check

  # Upgrade to the latest stable release.
  lucid upgrade

  # Force a reinstall of the current version.
  lucid upgrade --force

  # Pick a non-default channel.
  lucid upgrade --channel beta
  UPDATE_CHANNEL=edge lucid upgrade`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			check, _ := cmd.Flags().GetBool(upgradeFlagCheck)
			force, _ := cmd.Flags().GetBool(upgradeFlagForce)
			channelFlag, _ := cmd.Flags().GetString(upgradeFlagChannel)
			asJSON, _ := cmd.Flags().GetBool(jsonFlag)
			return runUpgrade(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), upgradeOptions{
				check:          check,
				force:          force,
				channelFlag:    channelFlag,
				asJSON:         asJSON,
				currentVersion: bi.Version,
			})
		},
	}
	cmd.Flags().Bool(upgradeFlagCheck, false, "Check for an available upgrade without downloading or installing")
	cmd.Flags().Bool(upgradeFlagForce, false, "Reinstall the latest release even when already current")
	cmd.Flags().String(upgradeFlagChannel, "", "Release channel: stable | beta | edge (overrides UPDATE_CHANNEL)")
	return cmd
}

// upgradeOptions bundles the parsed cobra flags so runUpgrade has a
// single argument that's easy to construct from tests.
type upgradeOptions struct {
	check          bool
	force          bool
	channelFlag    string
	asJSON         bool
	currentVersion string
}

// runUpgrade drives the upgrade.Check or upgrade.Install pipeline,
// renders output to stdout, and funnels every error through a locked
// "lucid: upgrade: <message>" line on stderr. The underlying error is
// returned verbatim so exitCodeForError can classify it.
func runUpgrade(ctx context.Context, stdout, stderr io.Writer, opts upgradeOptions) error {
	cfg := upgrade.Config{
		ReleaseSource:  releaseSourceForTests,
		Channel:        resolveChannel(opts.channelFlag, lookupEnvString),
		CurrentVersion: opts.currentVersion,
		Force:          opts.force,
		Stdout:         stdout,
	}
	if execPathForTests != "" {
		cfg.ExecPath = execPathForTests
	}

	if isDevVersion(opts.currentVersion) {
		_, _ = fmt.Fprintf(stderr, "lucid: upgrade: warning: running a dev build (%s); proceeding\n", opts.currentVersion)
	}

	if opts.check {
		info, err := upgrade.Check(ctx, cfg)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "lucid: upgrade: %s\n", formatUpgradeErr(err))
			return err
		}
		return renderCheckInfo(stdout, info, opts.asJSON)
	}

	if err := upgrade.Install(ctx, cfg); err != nil {
		_, _ = fmt.Fprintf(stderr, "lucid: upgrade: %s\n", formatUpgradeErr(err))
		return err
	}
	return nil
}

// lookupEnvString returns the value of the named env var (empty when
// unset). Wrapping os.LookupEnv lets resolveChannel stay a pure
// function of a getenv lambda.
func lookupEnvString(key string) string {
	v, _ := os.LookupEnv(key)
	return v
}

// resolveChannel turns the --channel flag (if any) plus the
// UPDATE_CHANNEL env into the upgrade.Channel the driver consumes.
// The flag wins when both are set.
func resolveChannel(flagVal string, getenv func(string) string) upgrade.Channel {
	if flagVal != "" {
		switch strings.ToLower(strings.TrimSpace(flagVal)) {
		case "beta":
			return upgrade.Beta
		case "edge":
			return upgrade.Edge
		default:
			return upgrade.Stable
		}
	}
	return upgrade.GetChannel(getenv)
}

// isDevVersion reports whether v is a placeholder (empty or "dev") so
// the cobra layer can warn before invoking the driver. The driver
// itself treats both cases as older-than-any-real-semver.
func isDevVersion(v string) bool {
	trimmed := strings.TrimSpace(v)
	return trimmed == "" || trimmed == "dev"
}

// renderCheckInfo prints a summary of upgrade.Check output as prose,
// or the same Info as a JSON document when asJSON is set.
func renderCheckInfo(stdout io.Writer, info *upgrade.Info, asJSON bool) error {
	if asJSON {
		return writeJSON(stdout, info)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "channel:           %s\n", info.Channel)
	fmt.Fprintf(&b, "current version:   %s\n", info.CurrentVersion)
	fmt.Fprintf(&b, "latest version:    %s\n", info.LatestVersion)
	fmt.Fprintf(&b, "update available:  %t\n", info.UpdateAvailable)
	if info.AssetName != "" {
		fmt.Fprintf(&b, "asset:             %s\n", info.AssetName)
	}
	if info.ChecksumSHA256 != "" {
		fmt.Fprintf(&b, "checksum sha256:   %s\n", info.ChecksumSHA256)
	}
	_, err := io.WriteString(stdout, b.String())
	if err != nil {
		return fmt.Errorf("lucid: write output: %w", err)
	}
	return nil
}

// formatUpgradeErr collapses the wrapped sentinel into a single
// human-readable line. The "lucid/upgrade: …" package prefix is
// stripped because the caller already prints "lucid: upgrade:".
func formatUpgradeErr(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	msg = strings.TrimPrefix(msg, "lucid/upgrade: ")
	for strings.Contains(msg, "lucid/upgrade: ") {
		msg = strings.ReplaceAll(msg, "lucid/upgrade: ", "")
	}
	if errors.Is(err, upgrade.ErrInstallDirNotWritable) {
		msg += " (try `sudo lucid upgrade` or copy the new binary into place manually)"
	}
	return msg
}
