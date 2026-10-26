package engine

import (
	"fmt"
	"slices"
	"time"
)

// The two anchor thresholds' defaults (engine-module.md §"Anchor event store" →
// "Thresholds"). They come from the lucid.json anchors block and are passed in
// as parameters; the Engine never reads config.
const (
	// DefaultMissAlarm is the miss-run length that alarms. It is also the
	// floor: a 1 would let a single isolated miss alarm.
	DefaultMissAlarm = 2
	// DefaultGraduationDays is the accounted-day bar a habit clears to be
	// surfaced as eligible to graduate.
	DefaultGraduationDays = 90
)

// anchorWeekDays is "this week" for the note-free week counts (slips_7d,
// miss_run_7d): the last seven logical days, today included — the witness
// report's week.
const anchorWeekDays = 7

// AnchorThresholds carries the two thresholds the habit math reads. Its zero
// value is safe: an unset field takes its default, and a miss alarm below 2 is
// raised to 2, so a single isolated miss can never alarm whatever is passed.
type AnchorThresholds struct {
	MissAlarm      int
	GraduationDays int
}

// withDefaults fills unset or out-of-range thresholds with their defaults.
func (t AnchorThresholds) withDefaults() AnchorThresholds {
	if t.MissAlarm < DefaultMissAlarm {
		t.MissAlarm = DefaultMissAlarm
	}
	if t.GraduationDays < 1 {
		t.GraduationDays = DefaultGraduationDays
	}
	return t
}

// QuitStats is a quit anchor's honest number: days since the last slip, with
// the longest clean run and the slip count kept beside it. JSON names are the
// `lucid metrics --json` contract (commands.md §metrics).
type QuitStats struct {
	CurrentRun int    `json:"current_run"`
	LongestRun int    `json:"longest_run"`
	Slips      int    `json:"slips"`
	Slips7d    int    `json:"slips_7d"`
	LastSlip   string `json:"last_slip,omitempty"`
}

// LimitStats is a limit anchor's honest number: use-days spent against an
// allowance over a rolling window. A planned use is spend, never failure.
// DaysSinceLastUse is a pointer because 0 (a use today) is a real value and
// the field is omitted only when nothing was ever used.
type LimitStats struct {
	Allowance        int    `json:"allowance"`
	WindowDays       int    `json:"window_days"`
	Used             int    `json:"used"`
	Remaining        int    `json:"remaining"`
	Planned          int    `json:"planned"`
	Slips            int    `json:"slips"`
	Slips7d          int    `json:"slips_7d"`
	LastUse          string `json:"last_use,omitempty"`
	DaysSinceLastUse *int   `json:"days_since_last_use,omitempty"`
	OverAllowance    bool   `json:"over_allowance"`
}

// HabitStats is a habit anchor's honest number: kept days out of accounted
// days, where a day is accounted only once it is confirmed or carries a logged
// miss. An unconfirmed day is unaccounted, never kept.
type HabitStats struct {
	WindowDays         int    `json:"window_days"`
	HabitSince         string `json:"habit_since"`
	Accounted          int    `json:"accounted"`
	Kept               int    `json:"kept"`
	Misses             int    `json:"misses"`
	CurrentMissRun     int    `json:"current_miss_run"`
	Alarm              bool   `json:"alarm"`
	MissRun7d          int    `json:"miss_run_7d"`
	LastConfirmed      string `json:"last_confirmed,omitempty"`
	NextConfirmFrom    string `json:"next_confirm_from"`
	GraduationEligible bool   `json:"graduation_eligible"`
}

// civilDay is a civil date as a whole-day number (days since 1970-01-01), so
// the kind math counts and steps days with integer arithmetic. It is the
// UTC-civil discipline DaysSince uses: each date is re-anchored to UTC midnight
// from its own Y/M/D, so a DST transition can never shift a count.
type civilDay int

// secondsPerDay is the length of a UTC day; Unix time has no leap seconds, so
// every UTC midnight is an exact multiple of it.
const secondsPerDay = 24 * 60 * 60

// civilDayOf returns t's civil date, read from t's own location.
func civilDayOf(t time.Time) civilDay {
	y, m, d := t.Date()
	return civilDay(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / secondsPerDay)
}

// parseCivilDay parses a YYYY-MM-DD string; ok is false for anything else.
func parseCivilDay(s string) (civilDay, bool) {
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return 0, false
	}
	return civilDayOf(t), true
}

// Time returns the civil date as UTC midnight.
func (d civilDay) Time() time.Time { return time.Unix(int64(d)*secondsPerDay, 0).UTC() }

// String renders the civil date as YYYY-MM-DD.
func (d civilDay) String() string { return DateString(d.Time()) }

// anchorBase is the day an anchor's events begin: the fold's Start — its
// definition date, which a completed compat re-date never moves. The limit and
// habit windows clip here rather than at the plain folded date, because after a
// quit's slips that date is a re-date, and clipping there would drop the
// slips before it from a limit's window. ok is false only for a store with no
// parseable date, which no projected anchor has.
func anchorBase(f AnchorFacts) (civilDay, bool) {
	if d, ok := parseCivilDay(f.Start); ok {
		return d, true
	}
	return parseCivilDay(f.Anchor.Date)
}

// anchorUseDays maps each distinct use-day to whether it carries a slip. Quit
// slips and limit uses are both use-days — the facts that outlive a kind
// change. A day is a slip day when it holds a quit slip or a use logged --slip;
// a day holding only planned uses is not. Several events on one day are one
// use-day, and a day with both intents is a slip day, the honest direction.
func anchorUseDays(f AnchorFacts) map[civilDay]bool {
	days := map[civilDay]bool{}
	for _, e := range f.Slips {
		if d, ok := parseCivilDay(e.Day); ok {
			days[d] = true
		}
	}
	for _, e := range f.Uses {
		if d, ok := parseCivilDay(e.Day); ok {
			days[d] = days[d] || e.Intent == AnchorIntentSlip
		}
	}
	return days
}

// inWeek reports whether d falls in the last seven logical days ending today.
func inWeek(d, today civilDay) bool { return d > today-anchorWeekDays && d <= today }

// ComputeQuitStats folds a quit's facts into its numbers as of the logical day
// today (PD-5). Use-days on or after the start are its slips: quit slips, and
// any uses logged while it was a limit. The current run is the days since the
// later of the start, the pending floor and the latest use-day — with no slips,
// exactly a milestone's count. The longest run is the longest gap between
// successive use-days, counted from the start, the current run included.
//
// A pending floor (a compat re-date whose pair is incomplete) ends a run like a
// use-day but is not counted as a slip: the slip count catches up when the
// write is retried, and treating the floor as a boundary keeps every run, the
// longest included, from reading longer than the truth meanwhile. slips_7d
// counts only slip-intent days — a planned use from a former limit never
// counts toward the witness week.
func ComputeQuitStats(f AnchorFacts, today time.Time) QuitStats {
	start, ok := anchorBase(f)
	if !ok {
		return QuitStats{}
	}
	now := civilDayOf(today)
	var s QuitStats
	boundaries := make([]civilDay, 0, len(f.Slips)+len(f.Uses)+1)
	for d, slip := range anchorUseDays(f) {
		if d < start {
			continue // before a correction moved the start: not this quit's history
		}
		boundaries = append(boundaries, d)
		if slip && inWeek(d, now) {
			s.Slips7d++
		}
	}
	slices.Sort(boundaries)
	s.Slips = len(boundaries)
	if s.Slips > 0 {
		s.LastSlip = boundaries[s.Slips-1].String()
	}
	if floor, ok := parseCivilDay(f.Floor); ok && floor > start {
		boundaries = append(boundaries, floor)
		slices.Sort(boundaries)
	}

	prev := start
	longest, seen := 0, false
	for _, b := range boundaries {
		run := int(b - prev)
		if !seen || run > longest {
			longest, seen = run, true
		}
		prev = b
	}
	s.CurrentRun = int(now - prev)
	if !seen || s.CurrentRun > longest {
		longest = s.CurrentRun
	}
	s.LongestRun = longest
	return s
}

// ComputeLimitStats folds a limit's facts into its numbers as of the logical
// day today (PD-6). The window is the window_days logical days ending today,
// inclusive, clipped at the anchor's start. Quit slips logged before a
// conversion count as slip-intent uses. A planned use spends allowance and
// nothing else: it never resets or zeroes a number.
func ComputeLimitStats(f AnchorFacts, today time.Time) LimitStats {
	s := LimitStats{Allowance: f.Allowance, WindowDays: f.WindowDays}
	base, ok := anchorBase(f)
	if !ok {
		s.Remaining = max(0, s.Allowance)
		return s
	}
	now := civilDayOf(today)
	lo := max(now-civilDay(f.WindowDays)+1, base)

	var last civilDay
	hasLast := false
	for d, slip := range anchorUseDays(f) {
		if !hasLast || d > last {
			last, hasLast = d, true
		}
		if d < base || d > now {
			continue
		}
		if slip && inWeek(d, now) {
			s.Slips7d++
		}
		if d < lo {
			continue
		}
		s.Used++
		if slip {
			s.Slips++
		} else {
			s.Planned++
		}
	}
	s.Remaining = max(0, s.Allowance-s.Used)
	s.OverAllowance = s.Used > s.Allowance
	if hasLast {
		s.LastUse = last.String()
		days := int(now - last)
		s.DaysSinceLastUse = &days
	}
	return s
}

// missRun is one maximal run of consecutive calendar miss days, first..last.
type missRun struct{ first, last civilDay }

func (r missRun) length() int { return int(r.last-r.first) + 1 }

// habitLedger is a habit's day sets over [base, today]: the logged miss days
// and the accounted days (confirmed or missed). A miss always wins over a
// confirm, so a missed day is accounted but never kept.
type habitLedger struct {
	misses    map[civilDay]bool
	accounted map[civilDay]bool
}

// newHabitLedger builds the day sets from a habit's non-voided misses and
// confirm spans, clipped to [base, today]: nothing before the anchor's start
// is ever accounted, and nothing after today has happened yet.
func newHabitLedger(f AnchorFacts, base, today civilDay) habitLedger {
	h := habitLedger{misses: map[civilDay]bool{}, accounted: map[civilDay]bool{}}
	for _, e := range f.Misses {
		if d, ok := parseCivilDay(e.Day); ok && d >= base && d <= today {
			h.misses[d] = true
			h.accounted[d] = true
		}
	}
	for _, e := range f.Confirms {
		from, okFrom := parseCivilDay(e.From)
		through, okThrough := parseCivilDay(e.Day)
		if !okFrom || !okThrough {
			continue
		}
		for d := max(from, base); d <= min(through, today); d++ {
			h.accounted[d] = true
		}
	}
	return h
}

// runs returns the maximal miss runs in day order.
func (h habitLedger) runs() []missRun {
	days := make([]civilDay, 0, len(h.misses))
	for d := range h.misses {
		days = append(days, d)
	}
	slices.Sort(days)
	var out []missRun
	for _, d := range days {
		if n := len(out); n > 0 && out[n-1].last == d-1 {
			out[n-1].last = d
			continue
		}
		out = append(out, missRun{first: d, last: d})
	}
	return out
}

// latestAccounted returns the latest accounted day.
func (h habitLedger) latestAccounted() (civilDay, bool) {
	var latest civilDay
	found := false
	for d := range h.accounted {
		if !found || d > latest {
			latest, found = d, true
		}
	}
	return latest, found
}

// accountedFrom counts the accounted days on or after from.
func (h habitLedger) accountedFrom(from civilDay) int {
	n := 0
	for d := range h.accounted {
		if d >= from {
			n++
		}
	}
	return n
}

// habitSince is the day the anchor became a habit: the day of its latest
// habit kind event, which for a habit is its latest kind event (KindSince).
// Re-recording habit on a habit moves it, by design (commands.md §"anchor
// kind"). ok is false for an anchor that is not a habit.
func habitSince(f AnchorFacts) (civilDay, bool) {
	if f.Kind != AnchorKindHabit {
		return 0, false
	}
	return parseCivilDay(f.KindSince)
}

// lastConfirmed returns the latest through-day of a non-voided confirm.
func lastConfirmed(f AnchorFacts) (civilDay, bool) {
	var latest civilDay
	found := false
	for _, e := range f.Confirms {
		if d, ok := parseCivilDay(e.Day); ok && (!found || d > latest) {
			latest, found = d, true
		}
	}
	return latest, found
}

// defaultConfirmStart is the first day the next confirm claims by default
// (PD-8): the later of the day after the last confirm and the day the anchor
// became a habit — never before the anchor's start. So by default nothing
// before the cutover is claimed, and a skipped week heals itself.
func defaultConfirmStart(f AnchorFacts, since, base civilDay) civilDay {
	start := max(since, base)
	if last, ok := lastConfirmed(f); ok {
		start = max(start, last+1)
	}
	return start
}

// ComputeHabitStats folds a habit's facts into its numbers as of the logical
// day today (PD-7). The window is window_days logical days ending today,
// clipped at the anchor's start. Accounted days are confirmed or missed;
// kept days are accounted days without a miss. The current miss run is the
// consecutive calendar miss days ending at the latest accounted day, and it
// alarms at th.MissAlarm. Graduation reads the whole history: it is eligible
// once the accounted days since the last alarm run ended — or since the anchor
// became a habit, whichever is later — reach th.GraduationDays.
func ComputeHabitStats(f AnchorFacts, today time.Time, th AnchorThresholds) HabitStats {
	th = th.withDefaults()
	window := f.WindowDays
	if window <= 0 {
		window = DefaultHabitWindowDays
	}
	s := HabitStats{WindowDays: window}
	base, okBase := anchorBase(f)
	since, okSince := habitSince(f)
	if !okBase || !okSince {
		return s
	}
	now := civilDayOf(today)
	s.HabitSince = since.String()
	s.NextConfirmFrom = defaultConfirmStart(f, since, base).String()
	if last, ok := lastConfirmed(f); ok {
		s.LastConfirmed = last.String()
	}

	h := newHabitLedger(f, base, now)
	lo := max(now-civilDay(window)+1, base)
	for d := range h.accounted {
		if d < lo {
			continue
		}
		s.Accounted++
		if h.misses[d] {
			s.Misses++
		}
	}
	s.Kept = s.Accounted - s.Misses

	runs := h.runs()
	if latest, ok := h.latestAccounted(); ok && h.misses[latest] {
		s.CurrentMissRun = runs[len(runs)-1].length() // the latest miss run ends at the latest accounted day
	}
	s.Alarm = s.CurrentMissRun >= th.MissAlarm

	gradFrom := since
	for _, r := range runs {
		if r.first <= now && r.last > now-anchorWeekDays { // touches the week, counted at full length
			s.MissRun7d = max(s.MissRun7d, r.length())
		}
		if r.length() >= th.MissAlarm {
			gradFrom = max(gradFrom, r.last+1)
		}
	}
	s.GraduationEligible = h.accountedFrom(gradFrom) >= th.GraduationDays
	return s
}

// MissRunThrough returns the length of the run of consecutive calendar miss
// days that contains day — 0 when day carries no non-voided miss on or after
// the anchor's start — and whether that run reaches th.MissAlarm. It is the
// run `anchor miss` calls out for the day it logged, wherever that day falls,
// so a backdated miss beside an earlier one is never quietly isolated. One
// isolated miss never alarms: the floor of 2 holds whatever is passed.
func MissRunThrough(f AnchorFacts, day string, th AnchorThresholds) (run int, alarm bool) {
	th = th.withDefaults()
	d, okDay := parseCivilDay(day)
	base, okBase := anchorBase(f)
	if !okDay || !okBase || d < base {
		return 0, false
	}
	misses := map[civilDay]bool{}
	for _, e := range f.Misses {
		if m, ok := parseCivilDay(e.Day); ok && m >= base {
			misses[m] = true
		}
	}
	if !misses[d] {
		return 0, false
	}
	first, last := d, d
	for misses[first-1] {
		first--
	}
	for misses[last+1] {
		last++
	}
	run = missRun{first: first, last: last}.length()
	return run, run >= th.MissAlarm
}

// ConfirmSpan resolves the span one anchor confirm attests for a habit
// (PD-8), ending at the through-day. By default it starts at the later of the
// day after the habit's last non-voided confirm and the day it became a habit,
// never before the anchor's start. from, when given, sets the start to the
// stretch the person vouches for: it may reach back before the cutover, but
// never before the anchor's start, and never past the through-day. A from later
// than the default start narrows the span, so it never claims a day the person
// did not vouch for. empty is true when the span holds no day — the habit is
// already confirmed through the through-day, or the through-day falls before it
// became a habit — and then nothing should be written. The text after the
// "engine: " prefix of an error is the user-facing rejection copy
// (error-states.md An-4, An-11). The returned days are civil dates at UTC
// midnight.
func ConfirmSpan(f AnchorFacts, through time.Time, from *time.Time) (start, last time.Time, empty bool, err error) {
	label := f.Anchor.Label
	if f.Kind != AnchorKindHabit {
		return time.Time{}, time.Time{}, false, fmt.Errorf(
			"engine: %q is a %s, and a confirm belongs to a habit — reclassify it as a habit first: lucid anchor kind %s habit",
			label, f.Kind, label,
		)
	}
	base, okBase := anchorBase(f)
	since, okSince := habitSince(f)
	if !okBase || !okSince {
		return time.Time{}, time.Time{}, false, fmt.Errorf("engine: %q has no readable start date or habit day", label)
	}
	end := civilDayOf(through)
	begin := defaultConfirmStart(f, since, base)
	if from != nil {
		vouched := civilDayOf(*from)
		if vouched < base {
			return time.Time{}, time.Time{}, false, fmt.Errorf("engine: --from can't be before %q started (%s)", label, base)
		}
		if vouched > end {
			return time.Time{}, time.Time{}, false, fmt.Errorf("engine: --from %s is after the through-day %s", vouched, end)
		}
		begin = vouched
	}
	return begin.Time(), end.Time(), begin > end, nil
}
