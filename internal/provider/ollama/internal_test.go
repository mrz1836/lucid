package ollama

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// fakeNetError is a net.Error whose Timeout reading is configurable, so isTimeout
// can be exercised on both a timed-out and a non-timeout net error.
type fakeNetError struct{ timeout bool }

func (fakeNetError) Error() string   { return "fake net error" }
func (e fakeNetError) Timeout() bool { return e.timeout }
func (fakeNetError) Temporary() bool { return false }

// TestIsTimeout covers all three readings: a net.Error that timed out is a
// timeout, one that did not is not, and a plain non-net error is never a
// timeout (the fallthrough return).
func TestIsTimeout(t *testing.T) {
	assert.True(t, isTimeout(fakeNetError{timeout: true}), "a timed-out net error is a timeout")
	assert.False(t, isTimeout(fakeNetError{timeout: false}), "a net error that did not time out is not a timeout")
	assert.False(t, isTimeout(errors.New("connection refused")), "a plain error is not a net timeout")
	assert.False(t, isTimeout(nil), "no error is not a timeout")
}
