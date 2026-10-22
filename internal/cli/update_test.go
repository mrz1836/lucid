package cli

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	selfupdate "github.com/mrz1836/go-selfupdate"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeReleaseSource is a ReleaseSource that answers with a fixed release or
// error, so the update command never shells out to gh or calls GitHub. When
// calls is set it counts lookups, so a test can prove the source was never
// consulted.
type fakeReleaseSource struct {
	rel   *selfupdate.Release
	err   error
	calls *int
}

func (f fakeReleaseSource) Latest(context.Context) (*selfupdate.Release, error) {
	if f.calls != nil {
		*f.calls++
	}
	return f.rel, f.err
}

// fakeRelease returns a release whose platform archive and checksums file
// both point at baseURL. The platform asset is present even for an
// up-to-date release because asset selection runs before the version compare.
func fakeRelease(baseURL, tag string) *selfupdate.Release {
	ver := strings.TrimPrefix(tag, "v")
	return &selfupdate.Release{
		TagName: tag,
		Assets: []selfupdate.ReleaseAsset{
			{Name: fmt.Sprintf("lucid_%s_%s_%s.tar.gz", ver, runtime.GOOS, runtime.GOARCH), BrowserDownloadURL: baseURL + "/archive"},
			{Name: fmt.Sprintf("lucid_%s_checksums.txt", ver), BrowserDownloadURL: baseURL + "/checksums"},
		},
	}
}

// newTestUpdateConfig returns an update config for a running "0.36.6" whose
// latest release is tag, served by an httptest server that answers 503 on
// every path. The target is a temp file reading "old" inside dir (a fresh
// temp dir when dir is empty); its path is returned so a test can prove it
// was never replaced, along with the release source's lookup counter.
func newTestUpdateConfig(t *testing.T, tag, dir string) (selfupdate.Config, string, *int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	if dir == "" {
		dir = t.TempDir()
	}
	target := filepath.Join(dir, "lucid")
	require.NoError(t, os.WriteFile(target, []byte("old"), 0o600))

	calls := new(int)
	return selfupdate.Config{
		Owner:          "mrz1836",
		Repo:           "lucid",
		BinaryName:     "lucid",
		CurrentVersion: "0.36.6",
		TargetPath:     target,
		Client:         srv.Client(),
		Source:         fakeReleaseSource{rel: fakeRelease(srv.URL, tag), calls: calls},
	}, target, calls
}

// runUpdateCmd executes args against a minimal root that mirrors production's
// silencing (SilenceUsage and SilenceErrors) and persistent --json flag, with
// the update command attached from cfg, through the same run seam Execute uses
// — which installs the usage-error wiring, renders a usage error, and maps the
// exit code — so "printed once" holds for the whole pipeline.
func runUpdateCmd(t *testing.T, cfg selfupdate.Config, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	root := &cobra.Command{Use: "lucid", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().Bool(jsonFlag, false, "Emit machine-readable JSON output where supported")
	attachUpdateCommand(root, cfg)

	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	code, _ = run(context.Background(), root, args)
	return out.String(), errBuf.String(), code
}

// skipInstallOnWindows skips cases that reach the install path, which the
// library refuses on Windows before any network access.
func skipInstallOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("self-update install is not supported on windows")
	}
}

// TestUpdateCmd_ReportsChecksumFailureOnce drives the real update path to a
// failed checksums download and asserts the library's error reaches stderr
// exactly once, with the exit code and the target binary unchanged.
func TestUpdateCmd_ReportsChecksumFailureOnce(t *testing.T) {
	skipInstallOnWindows(t)

	cfg, target, _ := newTestUpdateConfig(t, "v9.9.9", "")
	stdout, stderr, code := runUpdateCmd(t, cfg, "update")

	assert.Equal(t, ExitErr, code)
	assert.Equal(t, 1, strings.Count(stderr, "lucid: update:"), "stderr = %q", stderr)
	assert.Equal(t, 1, strings.Count(stderr, "failed to fetch checksums file"), "stderr = %q", stderr)
	assert.True(t, strings.HasPrefix(stderr, "lucid: update: go-selfupdate: failed to fetch checksums file"), "stderr = %q", stderr)
	assert.Equal(t, 1, strings.Count(stderr, "\n"), "stderr is not one line: %q", stderr)
	assert.NotContains(t, stderr, "for usage.")
	assert.NotContains(t, stderr, "lucid update: ")
	assert.NotContains(t, stdout, "failed to fetch")

	body, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "old", string(body), "target binary was replaced")
}

// TestUpdateCmd_UpgradeAliasReportsAsUpdate asserts the upgrade alias reports
// the same failure under the canonical update name.
func TestUpdateCmd_UpgradeAliasReportsAsUpdate(t *testing.T) {
	skipInstallOnWindows(t)

	cfg, _, _ := newTestUpdateConfig(t, "v9.9.9", "")
	_, stderr, code := runUpdateCmd(t, cfg, "upgrade")

	assert.Equal(t, ExitErr, code)
	assert.True(t, strings.HasPrefix(stderr, "lucid: update: "), "stderr = %q", stderr)
	assert.Equal(t, 1, strings.Count(stderr, "lucid: update: "), "stderr = %q", stderr)
	assert.Equal(t, 1, strings.Count(stderr, "\n"), "stderr is not one line: %q", stderr)
	assert.NotContains(t, stderr, "for usage.")
	assert.NotContains(t, stderr, "lucid update: ")
}

// TestUpdateCmd_ReportsCheckLookupFailure asserts --check reports a failed
// release lookup instead of exiting silently.
func TestUpdateCmd_ReportsCheckLookupFailure(t *testing.T) {
	cfg, _, _ := newTestUpdateConfig(t, "v9.9.9", "")
	cfg.Source = fakeReleaseSource{err: fmt.Errorf("%w: boom", selfupdate.ErrGitHubAPIFailed)}
	_, stderr, code := runUpdateCmd(t, cfg, "update", "--check")

	assert.Equal(t, ExitErr, code)
	assert.Equal(t, "lucid: update: go-selfupdate: GitHub API request failed: boom\n", stderr)
	assert.NotContains(t, stderr, "for usage.")
	assert.NotContains(t, stderr, "lucid update: ")
}

// TestUpdateCmd_InstallDirNotWritableExitErr asserts the preflight refusal is
// printed and, since lucid has no dedicated permission code, still maps to the
// generic runtime exit code.
func TestUpdateCmd_InstallDirNotWritableExitErr(t *testing.T) {
	skipInstallOnWindows(t)
	if os.Geteuid() == 0 {
		t.Skip("root can write a read-only directory")
	}

	dir := t.TempDir()
	cfg, target, _ := newTestUpdateConfig(t, "v9.9.9", dir)
	require.NoError(t, os.Chmod(dir, 0o555))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	_, stderr, code := runUpdateCmd(t, cfg, "update")

	assert.Equal(t, ExitErr, code)
	assert.Equal(t, 1, strings.Count(stderr, "lucid: update:"), "stderr = %q", stderr)
	assert.Equal(t, 1, strings.Count(stderr, "install dir not writable"), "stderr = %q", stderr)
	assert.True(t, strings.HasPrefix(stderr, "lucid: update: "), "stderr = %q", stderr)
	assert.Equal(t, 1, strings.Count(stderr, "\n"), "stderr is not one line: %q", stderr)

	body, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "old", string(body), "target binary was replaced")
}

// TestUpdateCmd_UpToDateWritesNoStderr asserts a successful no-op update, and
// a --check that finds nothing newer, add nothing to stderr.
func TestUpdateCmd_UpToDateWritesNoStderr(t *testing.T) {
	for _, args := range [][]string{{"update"}, {"update", "--check"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			cfg, _, _ := newTestUpdateConfig(t, "v0.36.6", "")
			stdout, stderr, code := runUpdateCmd(t, cfg, args...)

			assert.Equal(t, ExitOK, code)
			assert.Empty(t, stderr)
			assert.Contains(t, stdout, "up to date")
		})
	}
}

// TestUpdateCmd_UsageErrorAddsNoUpdateLine asserts a usage error on update
// keeps the usage exit code and is reported once by the root, as the standard
// usage line and --help hint under the canonical `lucid update` path, with no
// update report: cobra rejects it before the command runs, so the library's
// error reporting never sees it and the release source is never consulted.
// The production command tree is checked too, so the root's own wiring cannot
// add a line either.
func TestUpdateCmd_UsageErrorAddsNoUpdateLine(t *testing.T) {
	cases := [][]string{{"update", "--bogus"}, {"update", "extra"}, {"upgrade", "--bogus"}}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			cfg, _, calls := newTestUpdateConfig(t, "v9.9.9", "")
			_, stderr, code := runUpdateCmd(t, cfg, args...)

			assert.Equal(t, ExitUsage, code)
			assertUsageErrorOnce(t, stderr, "lucid update")
			assert.Contains(t, stderr, "Run 'lucid update --help' for usage.")
			assert.Equal(t, 0, strings.Count(stderr, "lucid: update:"), "stderr = %q", stderr)
			assert.Equal(t, 0, *calls, "the release source was consulted")
		})
	}

	for _, args := range cases {
		t.Run("production "+strings.Join(args, " "), func(t *testing.T) {
			t.Setenv("LUCID_NO_UPDATE_CHECK", "1")
			_, stderr, err := runRoot(t, BuildInfo{Version: "0.36.6"}, args...)

			assert.Equal(t, ExitUsage, exitCodeForError(err))
			assertUsageErrorOnce(t, stderr, "lucid update")
			assert.Contains(t, stderr, "Run 'lucid update --help' for usage.")
			assert.Equal(t, 0, strings.Count(stderr, "lucid: update:"), "stderr = %q", stderr)
		})
	}
}

// TestUpdateConfig_ProductionIdentity pins the production update identity and
// leaves the release source, transport, output, and target to the library.
func TestUpdateConfig_ProductionIdentity(t *testing.T) {
	cfg := newUpdateConfig("1.2.3")

	assert.Equal(t, "mrz1836", cfg.Owner)
	assert.Equal(t, "lucid", cfg.Repo)
	assert.Equal(t, "lucid", cfg.BinaryName)
	assert.Equal(t, "LUCID_GITHUB_TOKEN", cfg.TokenEnvVar)
	assert.Equal(t, "1.2.3", cfg.CurrentVersion)
	assert.Nil(t, cfg.Source)
	assert.Nil(t, cfg.Client)
	assert.Nil(t, cfg.Stdout)
	assert.Empty(t, cfg.TargetPath)
}

// TestAttachUpdateCommandWiring locks the seam between the root command and the
// go-selfupdate cobracmd package: the self-update command is the library's own,
// registered under the name "update" with the "upgrade" alias (preserving the
// old command name), the check/force/verbose boolean flags, and a hidden, inert
// --use-binary flag, and it is the command attachUpdateCommand returns. The
// command's behavior itself is covered by the library's own suites and the
// TestUpdateCmd tests above, so this asserts only the wiring.
func TestAttachUpdateCommandWiring(t *testing.T) {
	root := &cobra.Command{Use: "lucid"}
	returned := attachUpdateCommand(root, newUpdateConfig("1.2.3"))

	var cmd *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "update" {
			cmd = c
			break
		}
	}
	require.NotNil(t, cmd, "root registers an update command")
	assert.Same(t, cmd, returned, "attachUpdateCommand returns the registered update command")
	assert.NotNil(t, cmd.RunE, "the update command keeps the library's RunE")

	assert.Equal(t, "update", cmd.Name(), "the self-update command is named update")
	assert.Contains(t, cmd.Aliases, "upgrade", "the update command keeps the upgrade alias")
	assert.NotEmpty(t, cmd.Short, "the update command has a Short description")
	assert.NotEmpty(t, cmd.Long, "the update command has a Long description")
	assert.NotEmpty(t, cmd.Example, "the update command has an Example section")

	for _, name := range []string{"check", "force", "verbose"} {
		f := cmd.Flags().Lookup(name)
		require.NotNilf(t, f, "the update command registers --%s", name)
		assert.Equalf(t, "bool", f.Value.Type(), "--%s is a boolean flag", name)
	}

	ub := cmd.Flags().Lookup("use-binary")
	require.NotNil(t, ub, "the deprecated --use-binary flag is registered")
	assert.True(t, ub.Hidden, "--use-binary is hidden")
}
