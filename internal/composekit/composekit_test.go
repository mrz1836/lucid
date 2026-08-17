package composekit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/config"
)

func TestReadPromptFile(t *testing.T) {
	t.Run("reads an explicit file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "prompt.md")
		require.NoError(t, os.WriteFile(path, []byte("system prompt body"), 0o600))
		got, err := ReadPromptFile(path)
		require.NoError(t, err)
		assert.Equal(t, "system prompt body", got)
	})
	t.Run("rejects an empty path", func(t *testing.T) {
		_, err := ReadPromptFile("   ")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty prompt path")
	})
	t.Run("propagates a read error", func(t *testing.T) {
		_, err := ReadPromptFile(filepath.Join(t.TempDir(), "does-not-exist.md"))
		require.Error(t, err)
	})
}

func TestWithModelOverride(t *testing.T) {
	base := config.ProviderConfig{Backend: "claude-cli", Model: "base-model", TimeoutSeconds: 30}

	t.Run("override replaces the model", func(t *testing.T) {
		got := WithModelOverride(base, "feature-model")
		assert.Equal(t, "feature-model", got.Model)
		assert.Equal(t, base.Backend, got.Backend, "only the model is overridden")
		assert.Equal(t, base.TimeoutSeconds, got.TimeoutSeconds)
	})
	t.Run("empty override keeps the base model", func(t *testing.T) {
		got := WithModelOverride(base, "")
		assert.Equal(t, "base-model", got.Model)
	})
	t.Run("base is not mutated", func(t *testing.T) {
		_ = WithModelOverride(base, "feature-model")
		assert.Equal(t, "base-model", base.Model, "the caller's provider config is taken by value")
	})
}

func TestStripBullet(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"- do the thing", "do the thing"},
		{"* do the thing", "do the thing"},
		{"• do the thing", "do the thing"},
		{"  -   spaced  ", "spaced"},
		{"-*• over-marked", "over-marked"},
		{"no marker", "no marker"},
		{"", ""},
		{"   ", ""},
		{"- ", ""},
		{"mid - dash stays", "mid - dash stays"},
	}
	for _, tc := range cases {
		assert.Equalf(t, tc.want, StripBullet(tc.in), "StripBullet(%q)", tc.in)
	}
}
