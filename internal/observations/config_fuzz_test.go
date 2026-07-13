package observations

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// FuzzUnmarshalConfig asserts parsing observations/config.json never panics and
// that a successfully parsed config re-marshals (through normalized()) without
// error — the config file is user-editable, so a mangled file must degrade
// gracefully rather than crash capture (product-principles.md P9).
func FuzzUnmarshalConfig(f *testing.F) {
	seeds := []string{
		`{}`,
		`{"kinds_enabled": ["pain", "sleep"]}`,
		`{"kinds_enabled": null, "enrichers": []}`,
		`{"packet": {"clinical_context": ["note"]}}`,
		`not json`,
		``,
		`{"kinds_enabled": "wrong type"}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, b []byte) {
		c, err := UnmarshalConfig(b)
		if err != nil {
			return
		}
		// KindEnabled is total over any string; and a parsed config must
		// re-marshal without error (normalized fills the collection fields).
		_ = c.KindEnabled("pain")
		_, marshalErr := c.Marshal()
		require.NoError(t, marshalErr, "a parsed config must re-marshal cleanly")
	})
}
