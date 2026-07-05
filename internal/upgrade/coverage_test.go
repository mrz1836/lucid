package upgrade

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildTarGzOneFile returns a tar.gz containing a single regular file
// with the given name and body.
func buildTarGzOneFile(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg,
	}))
	_, err := tw.Write(body)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

// TestDefaultReleaseSource_Resolves exercises the composed source's
// hasGH lambda and both the gh-success and gh-error → api-fallback
// legs. An API test server backs the fallback so the test is hermetic
// whether or not `gh` is on PATH.
func TestDefaultReleaseSource_Resolves(t *testing.T) {
	apiSrv := newReleasesLatestServer(t, Release{TagName: "v2.0.0"})

	rel := ghRelease{TagName: "v1.0.0", PublishedAt: time.Now().UTC().Format(time.RFC3339)}
	okRunner := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		b, merr := json.Marshal(rel)
		require.NoError(t, merr)
		return b, nil
	}
	got, err := DefaultReleaseSource(okRunner, apiSrv.Client(), apiSrv.URL).Stable(t.Context())
	require.NoError(t, err)
	assert.Contains(t, []string{"v1.0.0", "v2.0.0"}, got.TagName)

	errRunner := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return nil, errors.New("gh boom")
	}
	got2, err := DefaultReleaseSource(errRunner, apiSrv.Client(), apiSrv.URL).Stable(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "v2.0.0", got2.TagName)
}

func TestGHPath_ListErrorPropagates(t *testing.T) {
	runner := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return nil, errors.New("boom")
	}
	src := NewGHSource(runner)
	_, err := src.Beta(t.Context())
	require.ErrorIs(t, err, ErrGHCLIFailed)
	_, err = src.Edge(t.Context())
	require.ErrorIs(t, err, ErrGHCLIFailed)
}

func TestGHPath_ListMalformedJSON(t *testing.T) {
	runner := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte("not json"), nil
	}
	src := NewGHSource(runner)
	_, err := src.Beta(t.Context())
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrGHCLIFailed)
}

func TestGHPath_StableMalformedPublishedAt(t *testing.T) {
	// A valid JSON envelope but an unparseable publishedAt exercises
	// convertGHReleaseToRelease's error branch.
	runner := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte(`{"tagName":"v1.0.0","publishedAt":"not-a-time"}`), nil
	}
	src := NewGHSource(runner)
	_, err := src.Stable(t.Context())
	require.Error(t, err)
}

func TestAPIPath_BetaEdgeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	src := NewAPISource(srv.Client(), srv.URL)
	_, err := src.Beta(t.Context())
	require.ErrorIs(t, err, ErrGitHubAPIFailed)
	_, err = src.Edge(t.Context())
	require.ErrorIs(t, err, ErrGitHubAPIFailed)
}

func TestAPIPath_DecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "not json")
	}))
	t.Cleanup(srv.Close)
	src := NewAPISource(srv.Client(), srv.URL)
	_, err := src.Stable(t.Context())
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrGitHubAPIFailed)
}

// TestCheck_ChecksumFetchFailsIsNonFatal covers the warn branch in
// Check where an asset resolves but its checksum lookup fails: Check
// returns successfully with an empty ChecksumSHA256 (Install is the
// gate that refuses to proceed without one).
func TestCheck_ChecksumFetchFailsIsNonFatal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/checksums.txt" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte("asset"))
	}))
	t.Cleanup(srv.Close)

	assetName := hostAssetName("0.2.0")
	rel := &Release{
		TagName: "v0.2.0",
		Assets: []ReleaseAsset{
			{Name: assetName, BrowserDownloadURL: srv.URL + "/asset"},
			{Name: "checksums.txt", BrowserDownloadURL: srv.URL + "/checksums.txt"},
		},
	}
	info, err := Check(t.Context(), Config{
		ReleaseSource:  &stubSource{stable: rel},
		HTTPClient:     srv.Client(),
		ExecPath:       filepath.Join(t.TempDir(), "lucid"),
		CurrentVersion: "dev",
	})
	require.NoError(t, err)
	assert.Empty(t, info.ChecksumSHA256)
}

// TestInstall_ExtractOKButNoBinary covers Install's locateBinary error
// leg: the tarball verifies and extracts but contains no `lucid`
// executable.
func TestInstall_ExtractOKButNoBinary(t *testing.T) {
	tarball := buildTarGzOneFile(t, "README.md", []byte("docs only"))
	srv, _ := newReleaseAndChecksumServer(t, tarball)

	execPath := filepath.Join(t.TempDir(), "lucid")
	require.NoError(t, os.WriteFile(execPath, []byte("old"), 0o755))

	err := Install(t.Context(), Config{
		ReleaseSource:  &stubSource{stable: srv.release},
		HTTPClient:     srv.server.Client(),
		ExecPath:       execPath,
		CurrentVersion: "dev",
		Stdout:         io.Discard,
	})
	require.ErrorIs(t, err, ErrBinaryNotFound)
	// Original binary is untouched.
	got, _ := os.ReadFile(execPath)
	assert.Equal(t, "old", string(got))
}

// TestExtractTarGz_MkdirConflict covers extractOneFile's MkdirAll
// error branch: a parent path component already exists as a file.
func TestExtractTarGz_MkdirConflict(t *testing.T) {
	dest := t.TempDir()
	extractDir := filepath.Join(dest, "out")
	require.NoError(t, os.MkdirAll(extractDir, 0o750))
	// Pre-create "a" as a FILE so MkdirAll(out/a) for entry "a/b" fails.
	require.NoError(t, os.WriteFile(filepath.Join(extractDir, "a"), []byte("x"), 0o600))

	tarPath := filepath.Join(dest, "t.tar.gz")
	writeTarGz(t, tarPath, map[string]tarEntry{"a/b": {mode: 0o644, body: []byte("data")}})

	err := extractTarGz(tarPath, extractDir)
	require.Error(t, err)
}

// TestInstall_ExtractError covers Install's extractTarGz error leg:
// the download verifies against its checksum but the payload is not a
// valid gzip archive.
func TestInstall_ExtractError(t *testing.T) {
	srv, _ := newReleaseAndChecksumServer(t, []byte("this is not a gzip stream"))

	execPath := filepath.Join(t.TempDir(), "lucid")
	require.NoError(t, os.WriteFile(execPath, []byte("old"), 0o755))

	err := Install(t.Context(), Config{
		ReleaseSource:  &stubSource{stable: srv.release},
		HTTPClient:     srv.server.Client(),
		ExecPath:       execPath,
		CurrentVersion: "dev",
		Stdout:         io.Discard,
	})
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrBinaryNotFound)
	got, _ := os.ReadFile(execPath)
	assert.Equal(t, "old", string(got))
}

// TestExtractTarGz_CreateFileConflict covers extractOneFile's OpenFile
// create-error branch: a regular-file entry whose target path already
// exists as a directory.
func TestExtractTarGz_CreateFileConflict(t *testing.T) {
	dest := t.TempDir()
	extractDir := filepath.Join(dest, "out")
	require.NoError(t, os.MkdirAll(filepath.Join(extractDir, "x"), 0o750)) // "x" is a dir

	tarPath := filepath.Join(dest, "t.tar.gz")
	writeTarGz(t, tarPath, map[string]tarEntry{"x": {mode: 0o644, body: []byte("data")}})

	err := extractTarGz(tarPath, extractDir)
	require.Error(t, err)
}

// TestFallbackSource_BetaEdgeGHSuccess covers the gh-success return in
// the composed source's Beta and Edge legs (the api source must not be
// consulted).
func TestFallbackSource_BetaEdgeGHSuccess(t *testing.T) {
	rel := &Release{TagName: "v1.2.3"}
	apiCalls := 0
	fb := &fallbackSource{
		gh: stubFn{
			onBeta: func() (*Release, error) { return rel, nil },
			onEdge: func() (*Release, error) { return rel, nil },
		},
		api: stubFn{
			onBeta: func() (*Release, error) { apiCalls++; return nil, errors.New("nope") },
			onEdge: func() (*Release, error) { apiCalls++; return nil, errors.New("nope") },
		},
		hasGH: func() bool { return true },
	}
	b, err := fb.Beta(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "v1.2.3", b.TagName)
	e, err := fb.Edge(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "v1.2.3", e.TagName)
	assert.Equal(t, 0, apiCalls)
}

// TestCopyFile_SrcIsDirectory covers copyFile's io.Copy error branch:
// os.Open succeeds on a directory but reading from it fails.
func TestCopyFile_SrcIsDirectory(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "srcd")
	require.NoError(t, os.MkdirAll(srcDir, 0o750))
	err := copyFile(srcDir, filepath.Join(dir, "out"))
	require.Error(t, err)
}

// TestDownloadAndVerify_DstCreateError covers the "create download
// file" branch: the destination sits inside a directory that does not
// exist.
func TestDownloadAndVerify_DstCreateError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("payload"))
	}))
	t.Cleanup(srv.Close)

	dst := filepath.Join(t.TempDir(), "missing-dir", "out.bin")
	info := &Info{DownloadURL: srv.URL, ChecksumSHA256: "deadbeef"}
	err := downloadAndVerify(t.Context(), srv.Client(), info, dst)
	require.Error(t, err)
}

// hostAssetName returns the goreleaser asset name for the running
// platform at the given version (no leading v).
func hostAssetName(version string) string {
	return fmt.Sprintf("lucid_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
}

// newReleasesLatestServer serves a single Release at the goreleaser
// /releases/latest path for the API source.
func newReleasesLatestServer(t *testing.T, rel Release) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "/releases/latest")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rel)
	}))
	t.Cleanup(srv.Close)
	return srv
}
