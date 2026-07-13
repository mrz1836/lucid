package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// FuzzUnmarshal asserts parsing lucid.json bytes never panics and that a
// successful parse round-trips through Clip without panicking — lucid.json is
// user-editable, so a hand-mangled file must fail gracefully, never crash the
// daily surface (product-principles.md P9).
func FuzzUnmarshal(f *testing.F) {
	seeds := []string{
		`{}`,
		`{"ask_insights_cap": 3, "ask_reflections_cap": 2}`,
		`{"provider": {"backend": "claude-cli", "timeout_seconds": 30}}`,
		`{"ask_insights_cap": -5}`,
		`not json`,
		``,
		`{"ask_insights_cap": "wrong type"}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, b []byte) {
		c, err := Unmarshal(b)
		if err != nil {
			return
		}
		// A parsed config must survive clipping and validation checks without
		// panicking; the returned warnings/error are the graceful outcome. Clip
		// is idempotent — a clipped config never reports further clip warnings.
		clipped, _ := c.Clip()
		reclipped, warnings := clipped.Clip()
		require.Empty(t, warnings, "clipping an already-clipped config yields no warnings")
		_ = reclipped.Validate()
	})
}
