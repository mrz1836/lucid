package workout

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestCardLookupMisses proves the library lookup reports a miss for an unknown
// id rather than returning a hollow card — the not-found branch the pick and the
// forward scan both lean on.
func TestCardLookupMisses(t *testing.T) {
	t.Parallel()

	_, ok := ExampleProgram().card("ghost")
	assert.False(t, ok, "an unknown card id resolves to no card")
}
