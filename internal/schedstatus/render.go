package schedstatus

import (
	"fmt"
	"strings"
	"time"
)

// timeLayout formats a periodic's next-run / last-enqueue timestamps in the
// human report — local, minute-resolution, calm to scan.
const timeLayout = "2006-01-02 15:04 MST"

// TextLines renders the report as the calm, human-first output the command
// prints by default. It leads with the one-word verdict, then one short section
// per group (companion, provider, chain, teeth periodics, companion periodics,
// receipts, recent runs, host). A healthy report stays terse; every warning or
// error is repeated in a closing issues block so a reader sees exactly what to
// act on without re-scanning. The JSON form (the marshaled [Report]) carries the
// same data for automation.
func (r Report) TextLines() []string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }

	add("Scheduler status: %s", strings.ToUpper(r.Verdict))
	add("")

	// Companion + provider.
	if r.Companion.Enabled {
		add("Companion: enabled")
	} else {
		add("Companion: disabled")
	}
	add("Provider: %s / %s", orDash(r.Companion.ProviderBackend), orDash(r.Companion.ProviderModel))
	for _, p := range r.Companion.Prompts {
		add("  %s prompt: %s (%s)", p.Role, orDash(p.Path), existsMark(p.Exists))
	}

	// Chain marks.
	add("Chain: bell %s, tripwire %s", orDash(r.Chain.BellTime), orDash(r.Chain.TripwireTime))

	// Teeth + companion periodics.
	out = append(out, dbLines("Teeth periodics", r.Teeth)...)
	out = append(out, dbLines("Companion periodics", r.CompanionJobs)...)

	// Receipts.
	add("Receipts:")
	if len(r.Receipts) == 0 {
		add("  (none — no companion send has been recorded yet)")
	}
	for _, rec := range r.Receipts {
		out = append(out, receiptLine(rec))
	}

	// Recent runs.
	if r.Runs.FailureCount == 0 {
		add("Recent runs: no recent failures")
	} else {
		add("Recent runs: %d recent failure(s)", r.Runs.FailureCount)
		for _, f := range r.Runs.Failures {
			add("  %s: %s %s", orDash(f.Kind), orDash(f.ErrorClass), f.Message)
		}
	}

	// Host / supervisor.
	add("Host:")
	if len(r.Host) == 0 {
		add("  (no host checks)")
	}
	for _, c := range r.Host {
		add("  %s: %s%s", hostLabel(c.Name), c.State, detailSuffix(c.Detail))
	}

	// Closing issues block: every non-ok check, most severe first.
	issues := issueLines(r)
	if len(issues) > 0 {
		add("")
		add("Issues:")
		out = append(out, issues...)
	}

	return out
}

// dbLines renders a job DB's header (with its resolved path and state detail) and
// one line per periodic. The resolved path is always shown so an env/launchd path
// drift is visible rather than silently green.
func dbLines(label string, db DBReport) []string {
	var out []string
	header := fmt.Sprintf("%s (%s):", label, orDash(db.Path))
	if db.State != Ok && db.Detail != "" {
		header += " " + db.Detail
	}
	out = append(out, header)
	if db.State != Ok {
		// A missing/unreadable DB has no trustworthy periodics to list.
		if len(db.Periodics) == 0 {
			return out
		}
	}
	if len(db.Periodics) == 0 {
		out = append(out, "  (no periodics registered)")
	}
	for _, p := range db.Periodics {
		out = append(out, periodicLine(p))
	}
	return out
}

// periodicLine renders one periodic: its slug, active/inactive/missing marker,
// cron, and next-run / last-enqueue timestamps.
func periodicLine(p PeriodicStatus) string {
	state := "active"
	switch {
	case !p.Present:
		state = "MISSING"
	case !p.Active:
		state = "inactive"
	}
	return fmt.Sprintf("  %-24s %-8s cron=%s next=%s last=%s",
		p.Slug, state, orDash(p.Cron), fmtTime(p.NextRun), fmtTime(p.LastEnqueue))
}

// receiptLine renders one window's last delivery receipt, or a "no receipt yet"
// placeholder when the window has never fired.
func receiptLine(rec ReceiptStatus) string {
	if !rec.Present {
		return fmt.Sprintf("  %-8s (no receipt yet)", rec.Window)
	}
	verified := "unverified"
	if rec.Verified {
		verified = "verified"
	}
	return fmt.Sprintf("  %-8s %s  msg=%s  %s  delivered=%s",
		rec.Window, orDash(rec.Date), orDash(rec.MessageID), verified, orDash(rec.DeliveredAt))
}

// issueLines returns one line per non-ok check (checks then host), errors before
// warnings, so the closing block reads worst-first.
func issueLines(r Report) []string {
	all := make([]Check, 0, len(r.Checks)+len(r.Host))
	all = append(all, r.Checks...)
	all = append(all, r.Host...)

	var errs, warns []string
	for _, c := range all {
		switch c.State {
		case Error:
			errs = append(errs, "  [error] "+c.Detail)
		case Warn:
			warns = append(warns, "  [warn]  "+c.Detail)
		case Ok, Unknown:
			// not an issue
		}
	}
	return append(errs, warns...)
}

// hostLabel trims the machine "host." prefix from a host check name for display.
func hostLabel(name string) string {
	return strings.TrimPrefix(name, "host.")
}

// detailSuffix renders " — <detail>" when a detail is present, else "".
func detailSuffix(detail string) string {
	if detail == "" {
		return ""
	}
	return " — " + detail
}

// existsMark renders a present/missing marker for a prompt path.
func existsMark(exists bool) string {
	if exists {
		return "ok"
	}
	return "MISSING"
}

// fmtTime renders an optional timestamp, or an em dash when unset.
func fmtTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "—"
	}
	return t.Format(timeLayout)
}

// orDash returns s, or an em dash when s is empty, so an unset field reads as a
// deliberate blank rather than a formatting gap.
func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
