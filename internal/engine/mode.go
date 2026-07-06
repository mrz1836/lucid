package engine

import "time"

// ValidMode reports whether m is a declarable Engine mode (engine-module.md
// §2). Anything else is rejected before it can reach a day record.
func ValidMode(m string) bool {
	switch m {
	case ModeGreen, ModeYellow, ModeRed:
		return true
	default:
		return false
	}
}

// ModeRejected reports whether a `/mode` declaration at now must be rejected
// because the governing day's bell has already rung (engine-module.md §2:
// "the mode is fixed at the bell and there is no retroactive amendment").
//
//   - now < rollover ⇒ the logical day is yesterday, whose bell rang last
//     evening ⇒ rejected (a small-hours declaration is retroactive).
//   - now ≥ rollover ⇒ the logical day is today; rejected once now has
//     reached today's bell time (first declaration before the bell wins).
func (c Clocks) ModeRejected(now time.Time) bool {
	if minutesOfDay(now) < c.RolloverMin {
		return true
	}
	return minutesOfDay(now) >= c.BellMin
}

// ModeDay resolves the logical day a `/mode` declaration at now targets — the
// current logical day under the governing clocks, i.e. the base rollover day.
// It is only meaningful when the declaration is accepted (see [Clocks.ModeRejected]).
func (c Clocks) ModeDay(now time.Time) time.Time { return c.baseLogicalDate(now) }

// BuildModeRecord builds the mode-only base day record `/mode` writes before
// the bell: it fixes the declared mode and its declaration time, leaving the
// day undecided (neither completed nor missed) for the evening close-out to
// fold in via corrections. mode must already be validated by [ValidMode].
func BuildModeRecord(logicalDay time.Time, mode, declaredAt, profile string) DayRecord {
	return DayRecord{
		DayID:          DayID(logicalDay),
		LogicalDate:    DateString(logicalDay),
		RecordedAt:     declaredAt,
		Mode:           mode,
		ModeDeclaredAt: declaredAt,
		Links:          map[string]string{},
		Profile:        orDefaultProfile(profile),
		Corrections:    []Correction{},
	}
}
