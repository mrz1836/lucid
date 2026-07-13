package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/upgrade"
)

// fakeSource returns the same release for every channel; enough to
// drive the cobra upgrade wiring without touching the network.
type fakeSource struct{ rel *upgrade.Release }

func (f fakeSource) Stable(context.Context) (*upgrade.Release, error) { return f.rel, nil }
func (f fakeSource) Beta(context.Context) (*upgrade.Release, error)   { return f.rel, nil }
func (f fakeSource) Edge(context.Context) (*upgrade.Release, error)   { return f.rel, nil }

// useTestSource sets the package test seams and registers cleanup so
// the globals never leak between tests.
func useTestSource(t *testing.T, src upgrade.ReleaseSource, execPath string) {
	t.Helper()
	releaseSourceForTests = src
	execPathForTests = execPath
	t.Cleanup(func() {
		releaseSourceForTests = nil
		execPathForTests = ""
	})
}

// newAssetServer builds a tar.gz containing a single "lucid" file and
// serves it alongside a matching goreleaser checksums.txt. It returns
// a release whose assets point at the server for the host platform.
func newAssetServer(t *testing.T, binBody []byte) *upgrade.Release {
	t.Helper()
	const tag = "9.9.9"

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name: "lucid", Mode: 0o755, Size: int64(len(binBody)), Typeflag: tar.TypeReg,
	}))
	_, err := tw.Write(binBody)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	tarball := buf.Bytes()

	assetName := fmt.Sprintf("lucid_%s_%s_%s.tar.gz", tag, runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256(tarball)
	checksums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), assetName)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/asset":
			_, _ = w.Write(tarball)
		case "/checksums.txt":
			_, _ = io.WriteString(w, checksums)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	return &upgrade.Release{
		TagName: "v" + tag,
		Assets: []upgrade.ReleaseAsset{
			{Name: assetName, BrowserDownloadURL: srv.URL + "/asset"},
			{Name: "lucid_" + tag + "_checksums.txt", BrowserDownloadURL: srv.URL + "/checksums.txt"},
		},
	}
}

func TestRunUpgrade_CheckJSON(t *testing.T) {
	rel := newAssetServer(t, []byte("new binary"))
	useTestSource(t, fakeSource{rel: rel}, filepath.Join(t.TempDir(), "lucid"))

	var stdout, stderr bytes.Buffer
	err := runUpgrade(context.Background(), &stdout, &stderr, upgradeOptions{
		check: true, asJSON: true, currentVersion: "v0.1.0",
	})
	require.NoError(t, err)

	var info upgrade.Info
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &info))
	assert.True(t, info.UpdateAvailable)
	assert.Equal(t, "v9.9.9", info.LatestVersion)
	assert.NotEmpty(t, info.ChecksumSHA256)
}

func TestRunUpgrade_CheckHuman(t *testing.T) {
	rel := newAssetServer(t, []byte("new binary"))
	useTestSource(t, fakeSource{rel: rel}, filepath.Join(t.TempDir(), "lucid"))

	var stdout, stderr bytes.Buffer
	err := runUpgrade(context.Background(), &stdout, &stderr, upgradeOptions{
		check: true, currentVersion: "v0.1.0",
	})
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), "update available:  true")
	assert.Contains(t, stdout.String(), "latest version:    v9.9.9")
}

func TestRunUpgrade_InstallSuccess(t *testing.T) {
	rel := newAssetServer(t, []byte("the new binary bytes"))
	execPath := filepath.Join(t.TempDir(), "lucid")
	require.NoError(t, os.WriteFile(execPath, []byte("old"), 0o755))
	useTestSource(t, fakeSource{rel: rel}, execPath)

	var stdout, stderr bytes.Buffer
	err := runUpgrade(context.Background(), &stdout, &stderr, upgradeOptions{
		currentVersion: "v0.1.0",
	})
	require.NoError(t, err)

	got, rerr := os.ReadFile(execPath)
	require.NoError(t, rerr)
	assert.Equal(t, "the new binary bytes", string(got))
	assert.Contains(t, stdout.String(), upgrade.RestartHint)
}

func TestRunUpgrade_DevBuildWarns(t *testing.T) {
	rel := newAssetServer(t, []byte("bin"))
	useTestSource(t, fakeSource{rel: rel}, filepath.Join(t.TempDir(), "lucid"))

	var stdout, stderr bytes.Buffer
	err := runUpgrade(context.Background(), &stdout, &stderr, upgradeOptions{
		check: true, currentVersion: "dev",
	})
	require.NoError(t, err)
	assert.Contains(t, stderr.String(), "warning: running a dev build")
}

func TestRunUpgrade_CheckError_NoMatchingAsset(t *testing.T) {
	// A release with no asset for the host platform → ErrAssetNotFound.
	rel := &upgrade.Release{TagName: "v9.9.9"}
	useTestSource(t, fakeSource{rel: rel}, filepath.Join(t.TempDir(), "lucid"))

	var stdout, stderr bytes.Buffer
	err := runUpgrade(context.Background(), &stdout, &stderr, upgradeOptions{
		check: true, currentVersion: "v0.1.0",
	})
	require.ErrorIs(t, err, upgrade.ErrAssetNotFound)
	assert.Contains(t, stderr.String(), "lucid: upgrade:")
}

func TestUpgradeCmd_ThroughRoot(t *testing.T) {
	rel := newAssetServer(t, []byte("bin"))
	useTestSource(t, fakeSource{rel: rel}, filepath.Join(t.TempDir(), "lucid"))

	out, _, err := runRoot(t, BuildInfo{Version: "v0.1.0"}, "upgrade", "--check", "--json")
	require.NoError(t, err)
	var info upgrade.Info
	require.NoError(t, json.Unmarshal([]byte(out), &info))
	assert.Equal(t, "v9.9.9", info.LatestVersion)
}

func TestUpgradeCmd_RejectsArgs(t *testing.T) {
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "upgrade", "unexpected")
	require.Error(t, err)
}

func TestResolveChannel(t *testing.T) {
	// Flag wins over env.
	assert.Equal(t, upgrade.Beta, resolveChannel("beta", func(string) string { return "edge" }))
	assert.Equal(t, upgrade.Edge, resolveChannel("edge", func(string) string { return "" }))
	assert.Equal(t, upgrade.Stable, resolveChannel("nonsense", func(string) string { return "" }))
	// No flag → env is consulted.
	assert.Equal(t, upgrade.Edge, resolveChannel("", func(k string) string {
		if k == "UPDATE_CHANNEL" {
			return "edge"
		}
		return ""
	}))
	assert.Equal(t, upgrade.Stable, resolveChannel("", func(string) string { return "" }))
}

func TestIsDevVersion(t *testing.T) {
	assert.True(t, isDevVersion(""))
	assert.True(t, isDevVersion("dev"))
	assert.True(t, isDevVersion("  dev  "))
	assert.False(t, isDevVersion("v1.0.0"))
}

func TestFormatUpgradeErr(t *testing.T) {
	assert.Empty(t, formatUpgradeErr(nil))
	// Package prefix stripped.
	assert.Equal(t, "download failed", formatUpgradeErr(upgrade.ErrDownloadFailed))
	// Install-dir hint appended.
	msg := formatUpgradeErr(fmt.Errorf("%w: /usr/local/bin", upgrade.ErrInstallDirNotWritable))
	assert.Contains(t, msg, "install directory not writable")
	assert.Contains(t, msg, "sudo lucid upgrade")
}

func TestRenderCheckInfo_JSON(t *testing.T) {
	var buf bytes.Buffer
	info := &upgrade.Info{Channel: upgrade.Stable, CurrentVersion: "v1", LatestVersion: "v2", UpdateAvailable: true}
	require.NoError(t, renderCheckInfo(&buf, info, true))
	var got upgrade.Info
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, "v2", got.LatestVersion)
}

func TestLookupEnvString(t *testing.T) {
	t.Setenv("LUCID_TEST_ENV_VAR", "value123")
	assert.Equal(t, "value123", lookupEnvString("LUCID_TEST_ENV_VAR"))
	assert.Empty(t, lookupEnvString("LUCID_DEFINITELY_UNSET_VAR_XYZ"))
}

// TestSelfCheckNotifierSend documents the no-op notifier contract: a self-check
// delivers nothing, so Send never errors and never sends.
func TestSelfCheckNotifierSend(t *testing.T) {
	require.NoError(t, selfCheckNotifier{}.Send("bell", "body"))
}
