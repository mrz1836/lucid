package workout

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mrz1836/lucid/internal/schedstatus"
)

// TestWorkoutKindMatchesStatusScan pins the workout job kind and the daemon name
// its fallback is logged under to the mirror `scheduler status` scans for, so
// renaming either fails here instead of silently hiding workout compose
// fallbacks from the status report.
func TestWorkoutKindMatchesStatusScan(t *testing.T) {
	assert.Equal(t, schedstatus.KindWorkoutDaily, kindDaily)
	assert.Equal(t, fallbackDaemon, schedstatus.FallbackDaemon(kindDaily))
}
