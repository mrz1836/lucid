package witnessreport

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mrz1836/lucid/internal/schedstatus"
)

// TestWitnessReportKindMatchesStatusScan pins the witness-report job kind and the
// daemon name its fallback is logged under to the mirror `scheduler status`
// scans for, so renaming either fails here instead of silently hiding
// witness-report compose fallbacks from the status report.
func TestWitnessReportKindMatchesStatusScan(t *testing.T) {
	assert.Equal(t, schedstatus.KindWitnessReportWeekly, kindWeekly)
	assert.Equal(t, fallbackDaemon, schedstatus.FallbackDaemon(kindWeekly))
}
