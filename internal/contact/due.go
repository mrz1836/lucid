package contact

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/isoweek"
)

// PersonInfo is what the due view needs to know about a live person record:
// the canonical key, the name to show, the date of birth (YYYY-MM-DD, "" when
// unset) and whether the person is off-limits. The router builds it from the
// people tree; this package never reads it from disk.
type PersonInfo struct {
	Key         string
	DisplayName string
	Dob         string
	OffLimits   bool
}

// DueRow is one person line in a due group (ADR-0014 §6). Cadence is the window
// as typed (`4w`) and CadenceDays its length; Human is the same window for
// people ("every 4 weeks") and never marshals. LastContact is the logical day of
// the most recent non-voided contact, "" when nothing is logged (null in JSON).
// WindowCloses is a date — the ordering key — and never a count.
type DueRow struct {
	PersonKey    string
	DisplayName  string
	Cadence      string
	CadenceDays  int
	Human        string
	LastContact  string
	WindowCloses string
}

// dueRowWire is the documented `--json` person row.
type dueRowWire struct {
	PersonKey    string  `json:"person_key"`
	DisplayName  string  `json:"display_name"`
	Cadence      string  `json:"cadence"`
	CadenceDays  int     `json:"cadence_days"`
	LastContact  *string `json:"last_contact"`
	WindowCloses string  `json:"window_closes"`
}

// MarshalJSON renders the documented row, with last_contact null when unset.
func (r DueRow) MarshalJSON() ([]byte, error) {
	w := dueRowWire{
		PersonKey:    r.PersonKey,
		DisplayName:  r.DisplayName,
		Cadence:      r.Cadence,
		CadenceDays:  r.CadenceDays,
		WindowCloses: r.WindowCloses,
	}
	if r.LastContact != "" {
		last := r.LastContact
		w.LastContact = &last
	}
	return json.Marshal(w)
}

// UnmarshalJSON reads the documented row back, recomputing Human from the
// cadence so a decoded row renders like the original.
func (r *DueRow) UnmarshalJSON(b []byte) error {
	var w dueRowWire
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	*r = DueRow{
		PersonKey:    w.PersonKey,
		DisplayName:  w.DisplayName,
		Cadence:      w.Cadence,
		CadenceDays:  w.CadenceDays,
		Human:        Window{Text: w.Cadence, Days: w.CadenceDays}.Human(),
		WindowCloses: w.WindowCloses,
	}
	if w.LastContact != nil {
		r.LastContact = *w.LastContact
	}
	return nil
}

// BirthdayRow is one birthday observed in the due week (ADR-0014 §7): Date is
// the day it falls this week, which for a Feb 29 birthday in a non-leap year is
// Feb 28.
type BirthdayRow struct {
	PersonKey   string `json:"person_key"`
	DisplayName string `json:"display_name"`
	Date        string `json:"date"`
}

// DueView is the due view for one logical day (ADR-0014 §6): the ISO
// Monday–Sunday week that contains Day, the three groups, and the birthdays
// that fall in the week. Every slice is non-nil, so `--json` shows `[]` for an
// empty group. A wrap view (Wrap) leaves out ComingUp and Birthdays, and its
// JSON drops both keys. ActiveCadences counts the people with an active
// cadence who may appear in the view (snoozed ones included, off-limits ones
// not); it tells "no cadences yet" apart from "nobody due" and never marshals.
type DueView struct {
	Day            string        `json:"day"`
	Week           string        `json:"week"`
	WeekStart      string        `json:"week_start"`
	WeekEnd        string        `json:"week_end"`
	Wrap           bool          `json:"wrap"`
	DueThisWeek    []DueRow      `json:"due_this_week"`
	StillOpen      []DueRow      `json:"still_open"`
	ComingUp       []DueRow      `json:"coming_up"`
	Birthdays      []BirthdayRow `json:"birthdays"`
	ActiveCadences int           `json:"-"`
}

// dueViewWire is the documented `--json` view. The wrap-only omissions ride on
// nil pointers, so a full view still shows an empty group as `[]`.
type dueViewWire struct {
	Day         string         `json:"day"`
	Week        string         `json:"week"`
	WeekStart   string         `json:"week_start"`
	WeekEnd     string         `json:"week_end"`
	Wrap        bool           `json:"wrap"`
	DueThisWeek []DueRow       `json:"due_this_week"`
	StillOpen   []DueRow       `json:"still_open"`
	ComingUp    *[]DueRow      `json:"coming_up,omitempty"`
	Birthdays   *[]BirthdayRow `json:"birthdays,omitempty"`
}

// MarshalJSON renders the documented, byte-deterministic view: key order as in
// ADR-0014 §6, arrays never null, and no coming_up or birthdays under --wrap.
func (v DueView) MarshalJSON() ([]byte, error) {
	w := dueViewWire{
		Day:         v.Day,
		Week:        v.Week,
		WeekStart:   v.WeekStart,
		WeekEnd:     v.WeekEnd,
		Wrap:        v.Wrap,
		DueThisWeek: nonNil(v.DueThisWeek),
		StillOpen:   nonNil(v.StillOpen),
	}
	if !v.Wrap {
		coming := nonNil(v.ComingUp)
		birthdays := nonNil(v.Birthdays)
		w.ComingUp = &coming
		w.Birthdays = &birthdays
	}
	return json.Marshal(w)
}

// Due groups folded states against the ISO Monday–Sunday week that contains
// today, a YYYY-MM-DD logical day (ADR-0014 §6). Only a person with an active
// cadence, a live record in people, no snooze this week and no off-limits flag
// is grouped:
//   - StillOpen: the window closed before this Monday;
//   - DueThisWeek: it closes this Monday through this Sunday;
//   - ComingUp: it closes next Monday through next Sunday.
//
// A later window is not shown. Each group is ordered by close date, then
// case-folded display name, then person key. Birthdays come from people
// directly — cadence or not, snoozed or paused, never off-limits — and are left
// out of a wrap view along with ComingUp. Week math runs on civil dates, so a
// DST change cannot move a day. An unreadable today yields an empty view.
func Due(states map[string]PersonState, people map[string]PersonInfo, today string, wrap bool) DueView {
	v := DueView{
		Day:         today,
		Wrap:        wrap,
		DueThisWeek: []DueRow{},
		StillOpen:   []DueRow{},
		ComingUp:    []DueRow{},
		Birthdays:   []BirthdayRow{},
	}
	day, ok := civilDate(today)
	if !ok {
		return v
	}
	start, end := weekBounds(day)
	v.Week = isoweek.Label(day)
	v.WeekStart = start.Format(dateLayout)
	v.WeekEnd = end.Format(dateLayout)
	nextStart := start.AddDate(0, 0, 7).Format(dateLayout)
	nextEnd := end.AddDate(0, 0, 7).Format(dateLayout)

	for key, st := range states {
		info, live := people[key]
		if !live || info.OffLimits || st.State != StateActive {
			continue
		}
		v.ActiveCadences++
		if st.SnoozedWeeks[v.Week] || st.Closes == "" {
			continue
		}
		row := dueRow(key, info, st)
		switch {
		case st.Closes < v.WeekStart:
			v.StillOpen = append(v.StillOpen, row)
		case st.Closes <= v.WeekEnd:
			v.DueThisWeek = append(v.DueThisWeek, row)
		case !wrap && st.Closes >= nextStart && st.Closes <= nextEnd:
			v.ComingUp = append(v.ComingUp, row)
		}
	}
	for _, group := range [][]DueRow{v.DueThisWeek, v.StillOpen, v.ComingUp} {
		slices.SortFunc(group, compareDueRows)
	}
	if !wrap {
		v.Birthdays = birthdaysBetween(people, start, end)
	}
	return v
}

// dueRow builds the person line for an active state.
func dueRow(key string, info PersonInfo, st PersonState) DueRow {
	row := DueRow{
		PersonKey:    key,
		DisplayName:  displayName(key, info),
		Cadence:      st.Window.Text,
		CadenceDays:  st.Window.Days,
		Human:        st.Window.Human(),
		WindowCloses: st.Closes,
	}
	if st.LastContact != nil {
		row.LastContact = st.LastContact.Day
	}
	return row
}

// compareDueRows orders by close date, then case-folded name, then key.
func compareDueRows(a, b DueRow) int {
	if c := strings.Compare(a.WindowCloses, b.WindowCloses); c != 0 {
		return c
	}
	if c := strings.Compare(strings.ToLower(a.DisplayName), strings.ToLower(b.DisplayName)); c != 0 {
		return c
	}
	return strings.Compare(a.PersonKey, b.PersonKey)
}

// displayName is the record's display name, or its key when the name is blank.
func displayName(key string, info PersonInfo) string {
	if name := strings.TrimSpace(info.DisplayName); name != "" {
		return name
	}
	return key
}

// weekBounds returns the Monday and Sunday civil dates of the ISO week that
// contains day, at noon UTC so no offset can shift them.
func weekBounds(day time.Time) (monday, sunday time.Time) {
	start, _ := isoweek.Bounds(noonUTC(day))
	monday = noonUTC(start)
	return monday, monday.AddDate(0, 0, 6)
}

// noonUTC is the civil date of t at 12:00 UTC.
func noonUTC(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, time.UTC)
}

// nonNil returns s, or an empty slice for nil, so JSON shows `[]`.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// Snooze refusals (error-states.md Ct-10). A refused snooze is a
// [*SnoozeError] that unwraps to one of these.
var (
	// ErrSnoozeNotActive refuses a snooze for a person with no active cadence.
	ErrSnoozeNotActive = errors.New("contact: no active cadence to snooze")
	// ErrSnoozedThisWeek refuses a second snooze in the same ISO week.
	ErrSnoozedThisWeek = errors.New("contact: already snoozed this week")
)

// SnoozeError is a refused snooze. [SnoozeError.Message] renders the
// user-facing copy for a person, without the trailing "Nothing was saved." the
// caller appends.
type SnoozeError struct {
	// Already is true when the person is already snoozed this week; otherwise
	// the cadence is not active.
	Already bool
}

// Error renders the refusal for an unnamed person.
func (e *SnoozeError) Error() string { return e.Message("this person") }

// Unwrap returns the sentinel for the refusal.
func (e *SnoozeError) Unwrap() error {
	if e.Already {
		return ErrSnoozedThisWeek
	}
	return ErrSnoozeNotActive
}

// Message renders the refusal naming the person.
func (e *SnoozeError) Message(name string) string {
	if e.Already {
		return name + " is already snoozed this week."
	}
	return name + " has no active check-in cadence to snooze."
}

// ValidateSnooze reports whether a person in state may be snoozed on today, a
// YYYY-MM-DD logical day (ADR-0014 §5): the cadence must be active, and the
// ISO week of today must not already carry a snooze. A refusal is a
// [*SnoozeError].
func ValidateSnooze(state PersonState, today string) error {
	if state.State != StateActive {
		return &SnoozeError{}
	}
	if week, ok := weekOf(today); ok && state.SnoozedWeeks[week] {
		return &SnoozeError{Already: true}
	}
	return nil
}
