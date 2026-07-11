package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── launchd render + lint ────────────────────────────────────────────────────

// TestRenderLaunchd_Defaults renders the launchd job with the shipped defaults
// and asserts it lints clean, invokes `hush supervise` (never lucid directly),
// and carries the substituted fields with no template action left behind.
func TestRenderLaunchd_Defaults(t *testing.T) {
	out, err := RenderLaunchd(DefaultLaunchdParams())
	require.NoError(t, err)

	assert.NotContains(t, out, "{{", "no unresolved template action")
	assert.Contains(t, out, "<string>com.lucid.scheduler</string>")
	assert.Contains(t, out, "<string>/usr/local/bin/hush</string>")
	assert.Contains(t, out, "<string>supervise</string>")
	assert.Contains(t, out, "<string>/usr/local/etc/lucid/supervise.toml</string>")
	assert.Contains(t, out, "<true/>", "RunAtLoad/KeepAlive true render as <true/>")
	// A supervised install never names lucid in the launchd job — hush does.
	assert.NotContains(t, out, "<string>lucid</string>")
	require.NoError(t, LintLaunchd(out))
}

// TestRenderLaunchd_PlutilLint runs the rendered plist through Apple's own
// `plutil -lint` when it is available (macOS) — the real-world dry-run gate on
// top of the pure-Go lint. It skips cleanly where plutil is absent (Linux CI).
func TestRenderLaunchd_PlutilLint(t *testing.T) {
	plutil, err := exec.LookPath("plutil")
	if err != nil {
		t.Skip("plutil not available on this platform")
	}
	out, err := RenderLaunchd(DefaultLaunchdParams())
	require.NoError(t, err)

	f := filepath.Join(t.TempDir(), "lucid.plist")
	require.NoError(t, os.WriteFile(f, []byte(out), 0o600))

	cmd := exec.CommandContext(t.Context(), plutil, "-lint", f)
	combined, runErr := cmd.CombinedOutput()
	require.NoErrorf(t, runErr, "plutil -lint failed: %s", combined)
	assert.Contains(t, string(combined), "OK")
}

// TestRenderLaunchd_FlagsFalse renders with lifecycle flags off and confirms
// the boolean template branch produces <false/>.
func TestRenderLaunchd_FlagsFalse(t *testing.T) {
	p := DefaultLaunchdParams()
	p.RunAtLoad = false
	p.KeepAlive = false
	out, err := RenderLaunchd(p)
	require.NoError(t, err)
	assert.Contains(t, out, "<false/>")
	assert.NotContains(t, out, "<true/>")
}

// TestLintLaunchd_Rejects covers each launchd lint failure mode.
func TestLintLaunchd_Rejects(t *testing.T) {
	good, err := RenderLaunchd(DefaultLaunchdParams())
	require.NoError(t, err)

	t.Run("unresolved action", func(t *testing.T) {
		err := LintLaunchd(good + "\n{{ .Leftover }}")
		require.ErrorIs(t, err, ErrLint)
		assert.Contains(t, err.Error(), "unresolved template action")
	})
	t.Run("malformed xml", func(t *testing.T) {
		err := LintLaunchd("<plist><dict><key>Label</key>") // unclosed
		require.ErrorIs(t, err, ErrLint)
		assert.Contains(t, err.Error(), "well-formed")
	})
	t.Run("missing required key", func(t *testing.T) {
		stripped := strings.Replace(good, "<key>KeepAlive</key>", "<key>NotKeepAlive</key>", 1)
		err := LintLaunchd(stripped)
		require.ErrorIs(t, err, ErrLint)
		assert.Contains(t, err.Error(), "KeepAlive")
	})
	t.Run("does not invoke supervise", func(t *testing.T) {
		noSup := strings.Replace(good, "<string>supervise</string>", "<string>serve</string>", 1)
		err := LintLaunchd(noSup)
		require.ErrorIs(t, err, ErrLint)
		assert.Contains(t, err.Error(), "hush supervise")
	})
}

// ── hush supervise render + lint ─────────────────────────────────────────────

// TestRenderSupervise_Defaults renders the hush supervise config and asserts it
// lints clean, is a supervisor session, names the harness token in scope
// (a name, never a value), and invokes lucid with an absolute path.
func TestRenderSupervise_Defaults(t *testing.T) {
	out, err := RenderSupervise(DefaultSuperviseParams())
	require.NoError(t, err)

	assert.NotContains(t, out, "{{", "no unresolved template action")
	assert.Contains(t, out, `session_type              = "supervisor"`)
	assert.Contains(t, out, `"LUCID_HARNESS_TOKEN"`, "the token is named in scope")
	assert.Contains(t, out, `"/usr/local/bin/lucid"`, "child command is an absolute path")
	assert.Contains(t, out, `name                      = "lucid-scheduler"`)
	require.NoError(t, LintSupervise(out))
}

// TestRenderSupervise_PassesChannelEnvThrough asserts the two non-secret
// logical-channel IDs (and the optional job-store override) flow to the child
// via env_passthrough — the notifier reads them from the injected environment —
// while the only secret in scope stays the harness token. Env-var NAMES render;
// no real ID or value appears (ADR-0005, S-7).
func TestRenderSupervise_PassesChannelEnvThrough(t *testing.T) {
	p := DefaultSuperviseParams()
	assert.Contains(t, p.EnvPassthrough, "LUCID_USER_CHANNEL_ID")
	assert.Contains(t, p.EnvPassthrough, "LUCID_WITNESS_CHANNEL_ID")
	assert.Contains(t, p.EnvPassthrough, "LUCID_SCHEDULER_DB")
	assert.Equal(t, []string{"LUCID_HARNESS_TOKEN"}, p.Scope, "the token is still the only secret in scope")

	out, err := RenderSupervise(p)
	require.NoError(t, err)

	// The channel IDs render inside the env_passthrough block, never scope.
	passthrough := out[strings.Index(out, "env_passthrough = ["):]
	assert.Contains(t, passthrough, `"LUCID_USER_CHANNEL_ID"`)
	assert.Contains(t, passthrough, `"LUCID_WITNESS_CHANNEL_ID"`)
	assert.Contains(t, passthrough, `"LUCID_SCHEDULER_DB"`)

	// The channel IDs are not secrets, so they never appear in the scope array.
	scopeBlock := out[strings.Index(out, "scope = ["):strings.Index(out, "[child]")]
	assert.NotContains(t, scopeBlock, "LUCID_USER_CHANNEL_ID")
	assert.NotContains(t, scopeBlock, "LUCID_WITNESS_CHANNEL_ID")

	require.NoError(t, LintSupervise(out))
}

// TestRenderSupervise_CarriesNoSecretValue is the S-7 property at the artifact
// level: the rendered supervise config names a secret but carries no value —
// no token-shaped material appears in the output.
func TestRenderSupervise_CarriesNoSecretValue(t *testing.T) {
	p := DefaultSuperviseParams()
	p.Scope = []string{"LUCID_HARNESS_TOKEN"}
	out, err := RenderSupervise(p)
	require.NoError(t, err)
	// The scope names the env var; the vault holds the value. Nothing here.
	assert.NotContains(t, strings.ToLower(out), "bearer ")
	assert.NotContains(t, out, "token = \"")
}

// TestLintSupervise_Rejects covers each supervise lint failure mode.
func TestLintSupervise_Rejects(t *testing.T) {
	good, err := RenderSupervise(DefaultSuperviseParams())
	require.NoError(t, err)

	t.Run("unresolved action", func(t *testing.T) {
		err := LintSupervise(good + "\nextra = \"{{ .X }}\"")
		require.ErrorIs(t, err, ErrLint)
	})
	t.Run("missing required token", func(t *testing.T) {
		noChild := strings.Replace(good, "[child]", "[not-child]", 1)
		err := LintSupervise(noChild)
		require.ErrorIs(t, err, ErrLint)
		assert.Contains(t, err.Error(), "[child]")
	})
	t.Run("empty scope", func(t *testing.T) {
		p := DefaultSuperviseParams()
		p.Scope = nil
		// Render bypasses the lint (which would reject it) by building the raw
		// output, then lint directly.
		raw, rerr := render("supervise", superviseTemplate, p)
		require.NoError(t, rerr)
		err := LintSupervise(raw)
		require.ErrorIs(t, err, ErrLint)
		assert.Contains(t, err.Error(), "empty secret scope")
	})
}

// TestRenderSupervise_EmptyScopeFails: RenderSupervise itself refuses an empty
// scope (a supervisor that injects no secret is a misconfiguration).
func TestRenderSupervise_EmptyScopeFails(t *testing.T) {
	p := DefaultSuperviseParams()
	p.Scope = nil
	_, err := RenderSupervise(p)
	require.ErrorIs(t, err, ErrLint)
}

// ── render internals ─────────────────────────────────────────────────────────

// TestRender_ParseError surfaces a malformed template as a parse error.
func TestRender_ParseError(t *testing.T) {
	_, err := render("bad", "{{ .Unclosed ", struct{}{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse")
}

// TestRender_ExecuteError surfaces a field the data does not carry.
func TestRender_ExecuteError(t *testing.T) {
	_, err := render("bad", "{{ .Missing.Field }}", struct{}{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "render")
}

// ── backup manifest ──────────────────────────────────────────────────────────

// TestBackupManifest_IsTheADR0002Set pins the canonical backup set and its
// one exclusion (engine/status.json).
func TestBackupManifest_IsTheADR0002Set(t *testing.T) {
	m := BackupManifest()
	require.Len(t, m, 5)
	paths := make([]string, len(m))
	for i, e := range m {
		paths[i] = e.Path
	}
	assert.Equal(t, []string{"raw", "observations", "registries", "engine", "projections/exports.log"}, paths)

	// engine/ is the only entry with an exclusion, and it is status.json.
	for _, e := range m {
		if e.Path == "engine" {
			assert.Equal(t, []string{"engine/status.json"}, e.Exclude)
		} else {
			assert.Empty(t, e.Exclude, "%s carries no exclusion", e.Path)
		}
	}
	assert.Contains(t, RebuildableTrees(), "engine/status.json")
	assert.Contains(t, RebuildableTrees(), "processed")
}

// TestBackupScriptManifestMatchesGo asserts scripts/backup.sh --print-manifest
// is byte-identical to the manifest derived from BackupManifest() — the shell
// and Go encodings of the ADR-0002 set can never silently drift.
func TestBackupScriptManifestMatchesGo(t *testing.T) {
	script := filepath.Join(repoRoot(t), "scripts", "backup.sh")

	out, err := exec.CommandContext(t.Context(), "bash", script, "--print-manifest").Output()
	require.NoError(t, err, "run backup.sh --print-manifest")

	assert.Equal(t, expectedManifest()+"\n", string(out))
}

// expectedManifest renders BackupManifest() into the tab-separated line format
// scripts/backup.sh emits.
func expectedManifest() string {
	manifest := BackupManifest()
	lines := make([]string, 0, len(manifest))
	for _, e := range manifest {
		typ := "file"
		if e.IsDir {
			typ = "dir"
		}
		line := typ + "\t" + e.Path
		if len(e.Exclude) > 0 {
			line += "\texclude=" + strings.Join(e.Exclude, ",")
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// repoRoot resolves the repository root from this test file's location so the
// script path holds regardless of the working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	// this file: <root>/deploy/deploy_test.go
	return filepath.Dir(filepath.Dir(file))
}
