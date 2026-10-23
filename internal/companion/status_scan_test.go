package companion

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mrz1836/lucid/internal/schedstatus"
)

// TestCompanionKindsMatchStatusScan pins the companion's job kinds and the daemon
// names its fallbacks are logged under to the mirror `scheduler status` scans
// for, so renaming a kind fails here instead of silently hiding its compose
// fallbacks from the status report.
func TestCompanionKindsMatchStatusScan(t *testing.T) {
	assert.Equal(t, schedstatus.KindCompanionMorning, kindMorning)
	assert.Equal(t, schedstatus.KindCompanionNight, kindNight)
	assert.Equal(t, schedstatus.KindCompanionMorningBackstop, kindMorningBackstop)

	assert.Equal(t, "companion-"+string(ModeMorning), schedstatus.FallbackDaemon(kindMorning))
	assert.Equal(t, "companion-"+string(ModeMorning), schedstatus.FallbackDaemon(kindMorningBackstop),
		"the backstop reports its fallback as the morning send")
	assert.Equal(t, "companion-"+string(ModeNight), schedstatus.FallbackDaemon(kindNight))
}
