package exports

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/observations"
)

// Episode-detection defaults for the clinician packet (observations.md §7, §10:
// "a flare = consecutive days above a user-set threshold", "episode gap
// tolerance 1 day"). The MVP packet uses a fixed moderate-pain threshold and
// the documented gap tolerance; both are constants here, instance-overridable
// in a later phase.
const (
	EpisodeThreshold = 4
	EpisodeGapDays   = 1
)

// RegimenLine is one current-regimen entry (observations.md §7). Detail is the
// dose for a med most recently taken, or the "(last logged: skipped <date>)"
// marker for a med whose latest event is a deliberate skip — never dropped.
type RegimenLine struct {
	What   string
	Detail string
}

// Episode is one detected pain flare in range: a run of qualifying days,
// possibly bridging a short logging gap.
type Episode struct {
	Start       string
	End         string
	DaysSpanned int
}

// PacketDayRow is one day's clinical timeline row: capacity/mode plus the
// pain readings and the med and intervention markers on the same day. Note
// fields, location, and weather are excluded by construction (never read).
type PacketDayRow struct {
	Date          string
	Capacity      int
	Mode          string
	Pains         []string
	Meds          []string
	Interventions []string
}

// EngineDayFacts is the capacity/mode pair the packet renders per day, keyed
// by logical_date by the caller.
type EngineDayFacts struct {
	Capacity int
	Mode     string
}

// ClinicianInput is everything [RenderClinician] needs, gathered by the storage
// adapter from the Ledger. It deliberately carries no note text, location, or
// weather — the excludes-by-default rule is upheld by never populating them.
type ClinicianInput struct {
	WindowStart     string
	WindowEnd       string
	ClinicalContext []string
	Injuries        []string
	Regimen         []RegimenLine
	EpisodeCount    int
	Episodes        []Episode
	Days            []PacketDayRow
}

// DeriveRegimen computes the current regimen from med events (observations.md
// §7): one line per distinct med, from its most recent event. A med last taken
// renders its dose; a med whose latest event is a logged skip renders
// "(last logged: skipped <date>)" — it is never dropped, so a prescriber sees
// the deliberate gap. Meds are grouped case-insensitively and rendered in
// sorted order for byte-stability.
func DeriveRegimen(medEvents []observations.Event) []RegimenLine {
	type latest struct {
		ev      observations.Event
		display string
	}
	byMed := map[string]latest{}
	for _, e := range medEvents {
		what := payloadStr(e.Payload, "what")
		if what == "" {
			continue
		}
		k := strings.ToLower(what)
		cur, ok := byMed[k]
		if !ok || eventLater(e, cur.ev) {
			byMed[k] = latest{ev: e, display: what}
		}
	}
	keys := make([]string, 0, len(byMed))
	for k := range byMed {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	lines := make([]RegimenLine, 0, len(keys))
	for _, k := range keys {
		l := byMed[k]
		if payloadBool(l.ev.Payload, "taken", true) {
			dose := payloadStr(l.ev.Payload, "dose")
			lines = append(lines, RegimenLine{What: l.display, Detail: dose})
			continue
		}
		lines = append(lines, RegimenLine{
			What:   l.display,
			Detail: fmt.Sprintf("(last logged: skipped %s)", l.ev.LogicalDate),
		})
	}
	return lines
}

// CountEpisodes detects pain flares over the pain events (observations.md §7):
// a qualifying day has a pain reading at or above threshold; consecutive
// qualifying days form an episode, and a gap of up to gapDays unlogged days
// does not break a run. It returns the episode count and their spans, sorted.
func CountEpisodes(painEvents []observations.Event, threshold, gapDays int) (int, []Episode) {
	maxByDate := map[string]int{}
	for _, e := range painEvents {
		if v, ok := payloadInt(e.Payload, "intensity"); ok && v >= threshold {
			if cur, seen := maxByDate[e.LogicalDate]; !seen || v > cur {
				maxByDate[e.LogicalDate] = v
			}
		}
	}
	dates := make([]string, 0, len(maxByDate))
	for d := range maxByDate {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	if len(dates) == 0 {
		return 0, nil
	}

	var episodes []Episode
	start, prev := dates[0], dates[0]
	for _, d := range dates[1:] {
		if daysBetween(prev, d) <= gapDays+1 {
			prev = d
			continue
		}
		episodes = append(episodes, Episode{Start: start, End: prev, DaysSpanned: daysBetween(start, prev) + 1})
		start, prev = d, d
	}
	episodes = append(episodes, Episode{Start: start, End: prev, DaysSpanned: daysBetween(start, prev) + 1})
	return len(episodes), episodes
}

// BuildPacketDayRows joins the per-day clinical timeline: capacity/mode from the
// Engine record plus the day's pain readings and med/intervention markers. It
// reads only clinical fields (intensity/site, med what/dose, intervention
// what/body_site) — never notes, location, or weather. Rows are sorted by date.
func BuildPacketDayRows(painEvents, medEvents, interventionEvents []observations.Event, engineByDate map[string]EngineDayFacts) []PacketDayRow {
	rows := map[string]*PacketDayRow{}
	get := func(date string) *PacketDayRow {
		if r, ok := rows[date]; ok {
			return r
		}
		r := &PacketDayRow{Date: date}
		if f, ok := engineByDate[date]; ok {
			r.Capacity, r.Mode = f.Capacity, f.Mode
		}
		rows[date] = r
		return r
	}
	for date, f := range engineByDate {
		r := get(date)
		r.Capacity, r.Mode = f.Capacity, f.Mode
	}
	addPainMarks(get, painEvents)
	addMedMarks(get, medEvents)
	addInterventionMarks(get, interventionEvents)

	out := make([]PacketDayRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

// rowGetter fetches (creating if needed) the packet row for a logical day.
type rowGetter func(date string) *PacketDayRow

// addPainMarks appends each pain reading (intensity, optional site) to its
// day's row — clinical fields only, never the note.
func addPainMarks(get rowGetter, events []observations.Event) {
	for _, e := range events {
		v, ok := payloadInt(e.Payload, "intensity")
		if !ok {
			continue
		}
		mark := strconv.Itoa(v)
		if site := payloadStr(e.Payload, "site"); site != "" {
			mark += " (" + site + ")"
		}
		r := get(e.LogicalDate)
		r.Pains = append(r.Pains, mark)
	}
}

// addMedMarks appends each med (what, optional dose, skipped flag) to its day's
// row — the adherence record on the clinical timeline.
func addMedMarks(get rowGetter, events []observations.Event) {
	for _, e := range events {
		what := payloadStr(e.Payload, "what")
		if what == "" {
			continue
		}
		mark := what
		if dose := payloadStr(e.Payload, "dose"); dose != "" {
			mark += " " + dose
		}
		if !payloadBool(e.Payload, "taken", true) {
			mark += " (skipped)"
		}
		r := get(e.LogicalDate)
		r.Meds = append(r.Meds, mark)
	}
}

// addInterventionMarks appends each intervention (what, optional body_site) to
// its day's row.
func addInterventionMarks(get rowGetter, events []observations.Event) {
	for _, e := range events {
		what := payloadStr(e.Payload, "what")
		if what == "" {
			continue
		}
		if site := payloadStr(e.Payload, "body_site"); site != "" {
			what += " " + site
		}
		r := get(e.LogicalDate)
		r.Interventions = append(r.Interventions, what)
	}
}

// RenderClinician renders the packet body (observations.md §7). The header is
// deterministic: clinical-context lines verbatim, active injuries, current
// regimen, and the episode count; the body is the per-day timeline. Note
// fields, location, and weather never appear because the input never carries
// them. The output is byte-stable across reruns.
func RenderClinician(in ClinicianInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Clinician packet — %s to %s\n\n", in.WindowStart, in.WindowEnd)
	renderPacketHeader(&b, in)
	b.WriteString("\nDaily record:\n")
	for _, d := range in.Days {
		renderPacketDay(&b, d)
	}
	return b.String()
}

// renderPacketHeader renders the deterministic header: clinical context
// verbatim, active injuries, current regimen, and the episode summary.
func renderPacketHeader(b *strings.Builder, in ClinicianInput) {
	for _, line := range in.ClinicalContext {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if len(in.ClinicalContext) > 0 {
		b.WriteByte('\n')
	}
	if len(in.Injuries) > 0 {
		fmt.Fprintf(b, "Active injuries: %s\n", strings.Join(in.Injuries, ", "))
	}
	if len(in.Regimen) > 0 {
		b.WriteString("Current regimen:\n")
		for _, r := range in.Regimen {
			if r.Detail != "" {
				fmt.Fprintf(b, "  %s %s\n", r.What, r.Detail)
			} else {
				fmt.Fprintf(b, "  %s\n", r.What)
			}
		}
	}
	fmt.Fprintf(b, "Pain episodes in range: %d\n", in.EpisodeCount)
	for _, ep := range in.Episodes {
		fmt.Fprintf(b, "  %s..%s (%d days)\n", ep.Start, ep.End, ep.DaysSpanned)
	}
}

// renderPacketDay renders one clinical-timeline row: capacity/mode, the day's
// pain readings, and med/intervention markers.
func renderPacketDay(b *strings.Builder, d PacketDayRow) {
	b.WriteString("  " + d.Date)
	if d.Mode != "" || d.Capacity > 0 {
		b.WriteString("  ")
		var facts []string
		if d.Capacity > 0 {
			facts = append(facts, fmt.Sprintf("capacity %d", d.Capacity))
		}
		if d.Mode != "" {
			facts = append(facts, "mode "+d.Mode)
		}
		b.WriteString(strings.Join(facts, ", "))
	}
	if len(d.Pains) > 0 {
		b.WriteString("  pain " + strings.Join(d.Pains, ", "))
	}
	for _, m := range d.Meds {
		b.WriteString("  [med " + m + "]")
	}
	for _, iv := range d.Interventions {
		b.WriteString("  [intervention " + iv + "]")
	}
	b.WriteByte('\n')
}

// eventLater reports whether a is more recent than b, by logical_date then id
// (ids sort in time order within a day).
func eventLater(a, b observations.Event) bool {
	if a.LogicalDate != b.LogicalDate {
		return a.LogicalDate > b.LogicalDate
	}
	return a.ID > b.ID
}

// daysBetween returns the count of civil days between two YYYY-MM-DD strings
// (b - a). It assumes well-formed, ordered dates from the sorted set.
func daysBetween(a, b string) int {
	const layout = "2006-01-02"
	ta, err1 := time.ParseInLocation(layout, a, time.UTC)
	tb, err2 := time.ParseInLocation(layout, b, time.UTC)
	if err1 != nil || err2 != nil {
		return 0
	}
	return int(tb.Sub(ta).Hours() / 24)
}
