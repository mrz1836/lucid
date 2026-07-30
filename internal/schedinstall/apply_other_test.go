//go:build !darwin

package schedinstall

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// On a non-macOS host, Apply and Uninstall refuse with ErrUnsupported so the CLI
// can fall back to ManualGuidance. This is the CI (Linux) path.

func TestApply_UnsupportedOffDarwin(t *testing.T) {
	_, err := Apply(t.Context(), ApplyParams{Label: "com.lucid.scheduler"})
	require.ErrorIs(t, err, ErrUnsupported)
}

func TestUninstall_UnsupportedOffDarwin(t *testing.T) {
	_, err := Uninstall(t.Context(), UninstallParams{Label: "com.lucid.scheduler"})
	require.ErrorIs(t, err, ErrUnsupported)
}
