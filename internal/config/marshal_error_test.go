package config

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMarshal_SurfacesEncodingError proves Marshal wraps and returns a JSON
// encoding failure rather than emitting partial bytes: a non-finite float
// (NaN) has no JSON representation, so the render fails cleanly.
func TestMarshal_SurfacesEncodingError(t *testing.T) {
	c := Default()
	c.PersonDominanceThreshold = math.NaN()

	b, err := c.Marshal()
	require.Error(t, err)
	assert.Nil(t, b, "no partial bytes are returned on an encoding failure")
	assert.Contains(t, err.Error(), "config: marshal")
}
