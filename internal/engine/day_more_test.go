package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestToInt_Coercions covers every arm of the correction-fold int coercion,
// including the fallback for a value that is neither a Go int nor a JSON number
// — a hand-edited field is ignored rather than panicking.
func TestToInt_Coercions(t *testing.T) {
	cases := []struct {
		name   string
		in     any
		want   int
		wantOK bool
	}{
		{"go int", 7, 7, true},
		{"json int64", int64(9), 9, true},
		{"json float64", float64(4), 4, true},
		{"string is ignored", "5", 0, false},
		{"bool is ignored", true, 0, false},
		{"nil is ignored", nil, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := toInt(tc.in)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestToStringMap_Coercions covers the native map[string]string branch (a
// correction built in-process), the JSON-decoded map[string]any of strings, the
// rejection of a decoded map carrying a non-string value, and the fallback for
// an entirely wrong type.
func TestToStringMap_Coercions(t *testing.T) {
	t.Run("native map[string]string clones through", func(t *testing.T) {
		src := map[string]string{"journal": StatusDone}
		got, ok := toStringMap(src)
		assert.True(t, ok)
		assert.Equal(t, map[string]string{"journal": StatusDone}, got)
		got["journal"] = StatusFloor
		assert.Equal(t, StatusDone, src["journal"], "the source map is cloned, not aliased")
	})

	t.Run("json map[string]any of strings", func(t *testing.T) {
		got, ok := toStringMap(map[string]any{"journal": StatusDone, "dock": StatusFloor})
		assert.True(t, ok)
		assert.Equal(t, map[string]string{"journal": StatusDone, "dock": StatusFloor}, got)
	})

	t.Run("json map with a non-string value is rejected", func(t *testing.T) {
		got, ok := toStringMap(map[string]any{"journal": StatusDone, "count": 3})
		assert.False(t, ok)
		assert.Nil(t, got)
	})

	t.Run("a wholly wrong type is rejected", func(t *testing.T) {
		got, ok := toStringMap(7)
		assert.False(t, ok)
		assert.Nil(t, got)
	})
}
