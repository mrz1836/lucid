package contact

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBirthdaysInWeek_Feb29AndYearBoundary pins the two calendar edges
// (ADR-0014 §7): a Feb 29 birthday is marked on Feb 28 in a non-leap year and
// on Feb 29 in a leap year, and a week that spans New Year shows a Dec 31 and a
// Jan 1 birthday both.
func TestBirthdaysInWeek_Feb29AndYearBoundary(t *testing.T) {
	leap := index(info(keySam, "Sam Rivera", "2000-02-29"))

	assert.Equal(t, []BirthdayRow{{PersonKey: keySam, DisplayName: "Sam Rivera", Date: "2027-02-28"}},
		BirthdaysInWeek(leap, "2027-02-24"), "non-leap year: observed Feb 28")
	assert.Empty(t, BirthdaysInWeek(leap, "2027-03-03"), "not the week after")
	assert.Equal(t, []BirthdayRow{{PersonKey: keySam, DisplayName: "Sam Rivera", Date: "2028-02-29"}},
		BirthdaysInWeek(leap, "2028-03-01"), "leap year: Feb 29 itself, once")

	newYear := index(
		info(keyAlex, "Alex Kim", "1991-12-31"),
		info(keyJordan, "Jordan Lee", "1993-01-01"),
	)
	want := []BirthdayRow{
		{PersonKey: keyAlex, DisplayName: "Alex Kim", Date: "2029-12-31"},
		{PersonKey: keyJordan, DisplayName: "Jordan Lee", Date: "2030-01-01"},
	}
	assert.Equal(t, want, BirthdaysInWeek(newYear, "2029-12-31"), "from the week's Monday")
	assert.Equal(t, want, BirthdaysInWeek(newYear, "2030-01-06"), "from the week's Sunday")
	assert.Equal(t, want, Due(nil, newYear, "2030-01-02", false).Birthdays, "the due view carries the same block")
}

// TestBirthdaysInWeek_SundayInNextMondayOut pins the week edges: this Monday
// and this Sunday count, the Sunday before and the Monday after do not. A
// blank or unreadable dob, and a birthday before the person was born, never
// show.
func TestBirthdaysInWeek_SundayInNextMondayOut(t *testing.T) {
	people := index(
		info(keySam, "Sam Rivera", "1990-11-08"),    // this Sunday
		info(keyAlex, "Alex Kim", "1990-11-09"),     // next Monday
		info(keyJordan, "Jordan Lee", "1990-11-02"), // this Monday
		info(keyCasey, "Casey Park", "1990-11-01"),  // the Sunday before
		info(keyRiley, "Riley Moss", "2030-11-05"),  // not born yet in 2026
		info("person_blank", "Riley Moss", ""),
		info("person_partial", "Riley Moss", "11-05"),
	)
	assert.Equal(t, []BirthdayRow{
		{PersonKey: keyJordan, DisplayName: "Jordan Lee", Date: "2026-11-02"},
		{PersonKey: keySam, DisplayName: "Sam Rivera", Date: "2026-11-08"},
	}, BirthdaysInWeek(people, docToday))
	assert.NotNil(t, BirthdaysInWeek(nil, docToday), "never nil")
	assert.Empty(t, BirthdaysInWeek(people, "garbage"))
}

// TestBirthdaysInWeek_OrderDateThenName pins the order within the block: date,
// then case-folded name, then key.
func TestBirthdaysInWeek_OrderDateThenName(t *testing.T) {
	people := index(
		info(keySam, "Sam Rivera", "1980-11-05"),
		info(keyCasey, "casey park", "1981-11-05"),
		info(keyJordan, "Jordan Lee", "1982-11-03"),
	)
	got := BirthdaysInWeek(people, docToday)
	assert.Equal(t, []string{keyJordan, keyCasey, keySam}, []string{got[0].PersonKey, got[1].PersonKey, got[2].PersonKey})
}

// TestBirthdaysInWeek_OffLimitsExcluded pins the off-limits rule: their
// birthday never shows, in the block or in the due view.
func TestBirthdaysInWeek_OffLimitsExcluded(t *testing.T) {
	sam := info(keySam, "Sam Rivera", "1990-11-04")
	sam.OffLimits = true
	people := index(sam, info(keyAlex, "Alex Kim", "1991-11-05"))

	want := []BirthdayRow{{PersonKey: keyAlex, DisplayName: "Alex Kim", Date: "2026-11-05"}}
	assert.Equal(t, want, BirthdaysInWeek(people, docToday))
	assert.Equal(t, want, Due(nil, people, docToday, false).Birthdays)
}
