package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestValidMode(t *testing.T) {
	for _, m := range []Mode{ModeGreen, ModeYellow, ModeRed} {
		assert.Truef(t, ValidMode(m), "%s should be valid", m)
	}
	for _, m := range []Mode{"", "purple", "Green", "amber"} {
		assert.Falsef(t, ValidMode(m), "%q should be invalid", m)
	}
}

// TestClocks_ModeRejected covers the three regions around the default clocks
// (rollover 04:00, bell 21:30): accepted only after rollover and before the
// bell; rejected in the small hours and once the bell has rung.
func TestClocks_ModeRejected(t *testing.T) {
	c, err := DefaultChain().ClocksFor(DefaultProfile)
	if err != nil {
		t.Fatalf("clocks: %v", err)
	}
	at := func(hh, mm int) time.Time { return time.Date(2026, 7, 5, hh, mm, 0, 0, time.UTC) }

	assert.True(t, c.ModeRejected(at(3, 0)), "small hours (before rollover) → rejected")
	assert.False(t, c.ModeRejected(at(4, 0)), "at rollover the day opens → accepted")
	assert.False(t, c.ModeRejected(at(14, 2)), "afternoon before the bell → accepted")
	assert.True(t, c.ModeRejected(at(21, 30)), "at the bell → rejected")
	assert.True(t, c.ModeRejected(at(23, 0)), "after the bell → rejected")
}

// TestClocks_ModeDay resolves the current logical day the declaration targets.
func TestClocks_ModeDay(t *testing.T) {
	c, _ := DefaultChain().ClocksFor(DefaultProfile)
	// Afternoon: today.
	got := c.ModeDay(time.Date(2026, 7, 5, 14, 0, 0, 0, time.UTC))
	assert.Equal(t, "2026-07-05", DateString(got))
	// Small hours: yesterday (base rollover day) — declaration is rejected
	// there, but the resolution is still the base logical day.
	got = c.ModeDay(time.Date(2026, 7, 6, 2, 0, 0, 0, time.UTC))
	assert.Equal(t, "2026-07-05", DateString(got))
}

func TestBuildModeRecord(t *testing.T) {
	day := time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC)
	rec := BuildModeRecord(day, ModeYellow, "2026-07-05T14:02:00Z", "nights")
	assert.Equal(t, "day_2026_07_05", rec.DayID)
	assert.Equal(t, "2026-07-05", rec.LogicalDate)
	assert.Equal(t, ModeYellow, rec.Mode)
	assert.Equal(t, "2026-07-05T14:02:00Z", rec.ModeDeclaredAt)
	assert.Equal(t, "nights", rec.Profile)
	assert.False(t, rec.Completed)
	assert.False(t, rec.Missed)
	assert.NotNil(t, rec.Links)
	assert.Empty(t, rec.Links)
	assert.Empty(t, rec.Corrections)

	// Empty profile defaults.
	assert.Equal(t, DefaultProfile, BuildModeRecord(day, ModeGreen, "2026-07-05T14:02:00Z", "").Profile)
}
