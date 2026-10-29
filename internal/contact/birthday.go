package contact

import (
	"slices"
	"strings"
	"time"
)

// BirthdaysInWeek returns the birthdays that fall in the ISO Monday–Sunday
// week containing day, a YYYY-MM-DD logical day (ADR-0014 §7). Everyone with a
// readable dob who is not off-limits counts, cadence or not. A Feb 29 birthday
// is observed on Feb 28 in a non-leap year, and a week that spans New Year
// checks both years. Rows are ordered by date, then case-folded name, then key;
// the result is never nil. An unreadable day yields no rows.
func BirthdaysInWeek(people map[string]PersonInfo, day string) []BirthdayRow {
	d, ok := civilDate(day)
	if !ok {
		return []BirthdayRow{}
	}
	start, end := weekBounds(d)
	return birthdaysBetween(people, start, end)
}

// birthdaysBetween collects the birthdays observed from monday through sunday
// (civil dates at noon UTC, at most one year boundary apart).
func birthdaysBetween(people map[string]PersonInfo, monday, sunday time.Time) []BirthdayRow {
	from, to := monday.Format(dateLayout), sunday.Format(dateLayout)
	years := []int{monday.Year()}
	if sunday.Year() != monday.Year() {
		years = append(years, sunday.Year())
	}
	rows := []BirthdayRow{}
	for key, info := range people {
		if info.OffLimits {
			continue
		}
		dob, ok := civilDate(strings.TrimSpace(info.Dob))
		if !ok {
			continue
		}
		for _, year := range years {
			observed := observedBirthday(dob, year)
			date := observed.Format(dateLayout)
			if date < from || date > to || observed.Before(dob) {
				continue
			}
			rows = append(rows, BirthdayRow{PersonKey: key, DisplayName: displayName(key, info), Date: date})
			break
		}
	}
	slices.SortFunc(rows, func(a, b BirthdayRow) int {
		if c := strings.Compare(a.Date, b.Date); c != 0 {
			return c
		}
		if c := strings.Compare(strings.ToLower(a.DisplayName), strings.ToLower(b.DisplayName)); c != 0 {
			return c
		}
		return strings.Compare(a.PersonKey, b.PersonKey)
	})
	return rows
}

// observedBirthday is the day a dob's birthday is marked in year: the same
// month and day, except Feb 29, which falls on Feb 28 when year is not a leap
// year.
func observedBirthday(dob time.Time, year int) time.Time {
	month, day := dob.Month(), dob.Day()
	if month == time.February && day == 29 && !isLeap(year) {
		day = 28
	}
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// isLeap reports whether year is a Gregorian leap year.
func isLeap(year int) bool {
	return year%4 == 0 && (year%100 != 0 || year%400 == 0)
}
