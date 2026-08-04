package observations

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Date granularity of a resolved day (usage/commands.md §"Backdating with
// --day"). A deliberate date flag may name a whole year or a whole month; the
// resolver snaps either to the period's first instant at approximate precision
// so logical_date still names one real day and the frozen envelope is
// untouched. Granularity reports which form the user actually typed, so a
// caller that stores testimony rather than a capture (the era/injury registry)
// can write the string back exactly as typed. It is never persisted on an
// observation — the envelope has no field for it and never will.
const (
	GranularityDay   = ""      // a full day or an exact instant
	GranularityYear  = "year"  // YYYY
	GranularityMonth = "month" // YYYY-MM
)

// AcceptedDayForms names every form the shared grammar reads, for the error a
// strict-tier flag returns when it cannot read one. Kept beside the parser so
// the message can never drift from the branches below.
const AcceptedDayForms = "@yesterday, YYYY-MM-DD, YYYY-MM-DDTHH:MM, HH:MM, HH:MM-HH:MM, " +
	"a partial YYYY or YYYY-MM, or a date followed by a time"

// Strict-tier failures (usage/commands.md §"Backdating with --day"). A
// deliberate date flag returns one of these and its caller writes nothing;
// callers match with errors.Is, so the user-facing wording can change without
// breaking the tier contract. The permissive inline grammar in prose never
// surfaces them — [ResolveBackdate] swallows every one of them and falls back
// to now, because capture never blocks (product-principles.md P10).
var (
	// ErrDayUnparseable is returned for a value no branch of the grammar reads.
	ErrDayUnparseable = errors.New("observations: unreadable day")

	// ErrDayFuture is returned for a day that has not happened yet, unless the
	// caller opted out of the ceiling with [DayOptions].AllowFuture.
	ErrDayFuture = errors.New("observations: day has not happened yet")

	// ErrDayPartial is returned for a well-formed partial date (YYYY / YYYY-MM)
	// on a command that needs a specific day.
	ErrDayPartial = errors.New("observations: partial date needs a specific day")
)

// DayOptions carries the two per-command carve-outs the strict tier allows.
// The zero value is the ordinary strict flag: a specific, non-future day.
type DayOptions struct {
	// AllowFuture keeps a forward-dated value instead of rejecting it. Only
	// `anchor add` sets it — an anchor may be a date you are counting toward.
	AllowFuture bool

	// AllowPartial accepts a YYYY / YYYY-MM value, snapped to the period's
	// first instant at approximate precision. Every date flag sets it except
	// `anchor add`, whose output is a precise day count.
	AllowPartial bool
}

// DayResolution is one resolved date: the instant and precision an event
// records, the placement key derived from them, and the two provenance fields
// a caller may need to honor its own contract.
type DayResolution struct {
	// OccurredAt is the resolved instant, in the location of the passed now.
	OccurredAt time.Time

	// Precision is exact, approximate, or range — the three frozen values.
	Precision string

	// End is the range end, set only for a clock range.
	End *time.Time

	// LogicalDate is the placement key derived from OccurredAt and Precision,
	// so no caller recomputes it.
	LogicalDate string

	// Granularity reports whether the user named a year, a month, or a day.
	Granularity string

	// Typed is the date token as typed, trimmed and with a leading @ stripped.
	// The registry writes this back verbatim for a partial date.
	Typed string

	// Relative reports that the value was the relative word `yesterday`, so an
	// Engine-side caller can re-resolve it against its governing profile's
	// rollover rather than the observations default.
	Relative bool
}

// ResolveDay resolves a deliberate date flag (`--day`, `--start`, `--end`,
// `--onset`, `anchor add`'s positional date) through the shared grammar
// (usage/commands.md §"Backdating with --day"). It is the strict tier: an
// unreadable value, a future day, or a partial date the command cannot use is
// a clean error, and the caller writes nothing. The permissive counterpart is
// [ResolveBackdate], which never returns an error.
//
// An empty arg is now at exact precision. A leading @ is optional. The value
// may carry a trailing time (`@yesterday 19:30`), which promotes the record to
// exact precision on that day; a trailing token the grammar does not consume
// is an error rather than a silent drop.
//
// AllowPartial governs acceptance, not reading: a bare four-digit token is
// always read as a year here, so a command that needs a specific day rejects
// `2014` with [ErrDayPartial] instead of silently recording today at 20:14.
// The inline prose grammar keeps the opposite order — see [parseDayToken].
func ResolveDay(dayArg string, now time.Time, opts DayOptions) (DayResolution, error) {
	if now.IsZero() {
		now = time.Now()
	}
	arg := strings.TrimSpace(dayArg)
	if arg == "" {
		return DayResolution{
			OccurredAt:  now,
			Precision:   PrecisionExact,
			LogicalDate: DeriveLogicalDate(now, PrecisionExact, DefaultRolloverMin),
			Granularity: GranularityDay,
		}, nil
	}

	body, following, err := splitDayArg(arg)
	if err != nil {
		return DayResolution{}, err
	}

	occ, prec, end, gran, relative, consumed, ok := parseDayToken(body, following, now, true)
	switch {
	case !ok:
		return DayResolution{}, unreadableDay(arg)
	case following != "" && !consumed:
		// The grammar read the date but not the rest — never drop it silently.
		return DayResolution{}, unreadableDay(arg)
	case gran != GranularityDay && !opts.AllowPartial:
		return DayResolution{}, fmt.Errorf("%w: %q names a period, not a day", ErrDayPartial, body)
	case !opts.AllowFuture && DateOf(occ).After(DateOf(now)):
		return DayResolution{}, fmt.Errorf("%w: %q", ErrDayFuture, DateString(DateOf(occ)))
	}

	return DayResolution{
		OccurredAt:  occ,
		Precision:   prec,
		End:         end,
		LogicalDate: DeriveLogicalDate(occ, prec, DefaultRolloverMin),
		Granularity: gran,
		Typed:       body,
		Relative:    relative,
	}, nil
}

// unreadableDay builds the ErrDayUnparseable error, naming the value and every
// accepted form so a rejection teaches the grammar rather than just refusing.
func unreadableDay(arg string) error {
	return fmt.Errorf("%w: %q (want %s)", ErrDayUnparseable, arg, AcceptedDayForms)
}

// splitDayArg strips an optional leading @ and splits the remainder into the
// date body and an optional following time — the split [ResolveBackdate] used
// to skip, which is why `--day "@yesterday 19:30"` silently resolved to now.
// More than two whitespace-separated tokens is not a date, so it is refused
// rather than partly read.
func splitDayArg(arg string) (body, following string, err error) {
	fields := strings.Fields(strings.TrimPrefix(arg, "@"))
	switch len(fields) {
	case 1:
		return fields[0], "", nil
	case 2:
		return fields[0], fields[1], nil
	default:
		return "", "", unreadableDay(arg)
	}
}

// parseDayToken is the one implementation of the @-grammar, shared by the
// strict flag path ([ResolveDay]) and the permissive inline path
// ([parseAtToken] over prose). The two tiers differ only in failure handling
// and in one branch order — never in what a form means.
//
// allowPartial selects the branch order, and the order is the contract:
//
//   - true (a deliberate date flag): range → yesterday → timestamp → YYYY-MM-DD
//     → YYYY-MM → YYYY → bare clock time.
//   - false (inline prose): range → bare clock time → yesterday → timestamp →
//     YYYY-MM-DD, exactly as it has always read.
//
// The reason the orders differ is a genuine collision: [parseClockHM] accepts a
// colon-less HHMM, so a bare four-digit token is ambiguous. Every year from
// 1900-1959 and 2000-2059 also reads as a valid wall-clock time, so `2014` is
// both the year 2014 and 20:14. On a flag the year wins (you typed a date on
// purpose); in dictated prose the clock time wins (`ate eggs @2014` is a
// timestamp, not a childhood memory). Collapsing these into one order silently
// breaks whichever side loses.
func parseDayToken(body, following string, now time.Time, allowPartial bool) (
	occ time.Time, prec string, end *time.Time, gran string, relative, consumedFollowing, ok bool,
) {
	loc := now.Location()

	// Clock range within today: HH:MM-HH:MM.
	if start, fin, isRange := parseClockRange(body, now); isRange {
		return start, PrecisionRange, &fin, GranularityDay, false, false, true
	}

	// Prose only: a bare clock time outranks the date forms (dictation).
	if !allowPartial {
		if hm, isTime := parseClockHM(body); isTime {
			return atTime(DateOf(now), hm), PrecisionExact, nil, GranularityDay, false, false, true
		}
	}

	// `yesterday` — the one relative word, rollover-aware on both paths: at
	// 02:00 it names the day before the one a bare capture files under.
	if strings.EqualFold(body, "yesterday") {
		day := LogicalBaseDate(now, DefaultRolloverMin).AddDate(0, 0, -1)
		if hm, isTime := parseClockHM(following); isTime {
			return atTime(day, hm), PrecisionExact, nil, GranularityDay, true, true, true
		}
		return day, PrecisionApproximate, nil, GranularityDay, true, false, true
	}

	// Full timestamp inside the token: YYYY-MM-DDTHH:MM[:SS].
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04"} {
		if t, parseErr := time.ParseInLocation(layout, body, loc); parseErr == nil {
			return t, PrecisionExact, nil, GranularityDay, false, false, true
		}
	}

	// Bare date (approximate midnight), promoted to exact by a following time.
	if d, parseErr := time.ParseInLocation(dateLayout, body, loc); parseErr == nil {
		if hm, isTime := parseClockHM(following); isTime {
			return atTime(d, hm), PrecisionExact, nil, GranularityDay, false, true, true
		}
		return d, PrecisionApproximate, nil, GranularityDay, false, false, true
	}

	// Flags only: partial dates, ahead of the bare-clock branch (see above).
	if allowPartial {
		if d, g, isPartial := parsePartialDate(body, loc); isPartial {
			// A period has no time of day, so a partial never absorbs one.
			return d, PrecisionApproximate, nil, g, false, false, true
		}
		if hm, isTime := parseClockHM(body); isTime {
			return atTime(DateOf(now), hm), PrecisionExact, nil, GranularityDay, false, false, true
		}
	}

	return time.Time{}, "", nil, GranularityDay, false, false, false
}

// parseClockRange resolves an HH:MM-HH:MM body to a start and end within
// today. It reports isRange=false when the body is not a range shape, or when
// it looks like one but either half is not a clock time — in which case the
// caller falls through to the remaining branches, as it always has.
func parseClockRange(body string, now time.Time) (start, end time.Time, isRange bool) {
	a, b, looksRange := splitTimeRange(body)
	if !looksRange {
		return time.Time{}, time.Time{}, false
	}
	from, fromOK := parseClockHM(a)
	to, toOK := parseClockHM(b)
	if !fromOK || !toOK {
		return time.Time{}, time.Time{}, false
	}
	day := DateOf(now)
	return atTime(day, from), atTime(day, to), true
}

// parsePartialDate reads a YYYY or YYYY-MM value and snaps it to the period's
// first instant (mvp/observations-module.md: "occurred_at 2014-09-01T00:00
// approximate → the file for 2014-09-01"). The caller marks it approximate, so
// the frozen envelope needs no new precision value and no new field — the cost
// is that after the write `2014` and `2014-01-01` are indistinguishable on an
// observation. An out-of-range month is not a partial date; time.Date would
// silently normalize it, so it is rejected here.
func parsePartialDate(body string, loc *time.Location) (day time.Time, granularity string, ok bool) {
	if y, isYear := parseYear(body); isYear {
		return time.Date(y, time.January, 1, 0, 0, 0, 0, loc), GranularityYear, true
	}
	if len(body) != 7 || body[4] != '-' {
		return time.Time{}, GranularityDay, false
	}
	y, isYear := parseYear(body[:4])
	m, isMonth := parseMonth(body[5:])
	if !isYear || !isMonth {
		return time.Time{}, GranularityDay, false
	}
	return time.Date(y, time.Month(m), 1, 0, 0, 0, 0, loc), GranularityMonth, true
}

// parseYear reads exactly four ASCII digits as a civil year. The fixed width
// and the 1000 floor keep a stray token from becoming the year 2 ("0002",
// which stays the dictated clock time 00:02) and keep the form unambiguous
// against every other branch of the grammar.
func parseYear(tok string) (year int, ok bool) {
	y, isNumeric := parseDigits(tok, 4)
	if !isNumeric || y < 1000 {
		return 0, false
	}
	return y, true
}

// parseMonth reads exactly two ASCII digits as a calendar month (01-12).
func parseMonth(tok string) (month int, ok bool) {
	m, isNumeric := parseDigits(tok, 2)
	if !isNumeric || m < 1 || m > 12 {
		return 0, false
	}
	return m, true
}

// parseDigits reads exactly width ASCII digits as a non-negative integer. It
// is deliberately not strconv.Atoi: a signed or spaced value ("+9", " 9") must
// not pass, and at these widths an overflow error is unreachable — so walking
// the bytes keeps every line here one a test can actually exercise.
func parseDigits(tok string, width int) (val int, ok bool) {
	if len(tok) != width {
		return 0, false
	}
	for i := 0; i < len(tok); i++ {
		c := tok[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		val = val*10 + int(c-'0')
	}
	return val, true
}
