package router

import (
	"fmt"
	"strings"
	"time"
)

// ExportOutcome is the result of an export command (observations-module.md
// §Commands). Path is the written projection; Message is what a chat surface
// posts — deliberately only the path, never packet body content.
type ExportOutcome struct {
	What        string
	Path        string
	WindowStart string
	WindowEnd   string
	Message     string
}

// ClinicianPacket executes `/packet clinician [@<date>|all]` (observations.md
// §7): it renders the packet under projections/, appends the disclosure-log
// line, and returns only the path as the postable message. The optional
// argument is `@<date>` (override the window start) or `all` (export
// everything); the default is since-the-last-export, first-ever trailing 90
// days. It reads no notes, location, or weather.
func (r *Router) ClinicianPacket(arg string, now time.Time) (ExportOutcome, error) {
	now = whenOr(now)
	loc := now.Location()

	startOverride, all, err := parsePacketArg(arg)
	if err != nil {
		return ExportOutcome{}, err
	}
	res, err := r.store.ExportClinicianPacket(now, loc, startOverride, all)
	if err != nil {
		return ExportOutcome{}, err
	}
	return ExportOutcome{
		What:        res.What,
		Path:        res.Path,
		WindowStart: res.WindowStart,
		WindowEnd:   res.WindowEnd,
		Message:     res.Path, // only the path is posted — never body content
	}, nil
}

// SeriesExport executes the pain/mood/capacity CSV series export
// (observations.md §7). It returns the written path as the postable message.
func (r *Router) SeriesExport(now time.Time) (ExportOutcome, error) {
	now = whenOr(now)
	res, err := r.store.ExportSeriesCSV(now, now.Location())
	if err != nil {
		return ExportOutcome{}, err
	}
	return ExportOutcome{
		What:        res.What,
		Path:        res.Path,
		WindowStart: res.WindowStart,
		WindowEnd:   res.WindowEnd,
		Message:     res.Path,
	}, nil
}

// parsePacketArg maps the optional packet argument to a window: "@<date>"
// overrides the start, "all" exports everything, and anything else is rejected
// (the documented forms are exact). An empty argument is the default window.
func parsePacketArg(arg string) (startOverride string, all bool, err error) {
	switch a := strings.TrimSpace(arg); {
	case a == "":
		return "", false, nil
	case a == "all":
		return "", true, nil
	case strings.HasPrefix(a, "@"):
		date := strings.TrimPrefix(a, "@")
		if _, perr := time.Parse("2006-01-02", date); perr != nil {
			return "", false, fmt.Errorf("packet window override must be @YYYY-MM-DD, got %q", a)
		}
		return date, false, nil
	default:
		return "", false, fmt.Errorf("unknown packet argument %q — use @YYYY-MM-DD or all", arg)
	}
}
