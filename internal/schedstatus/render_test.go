package schedstatus

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestTextLinesSections asserts the human report carries every required section
// label so a reader (and AC-2) sees companion, provider, chain, both periodic
// groups, receipts, recent runs, and host.
func TestTextLinesSections(t *testing.T) {
	now := fixedNow()
	r := Assemble(healthyEnabled(now), now)
	out := strings.Join(r.TextLines(), "\n")

	for _, label := range []string{
		"Scheduler status: OK",
		"Companion:",
		"Provider:",
		"Chain:",
		"Engine periodics",
		"Companion periodics",
		"Job stores",
		"Receipts:",
		"Recent runs",
		"Recent compose fallbacks",
		"Host:",
	} {
		require.Contains(t, out, label, "missing section label %q", label)
	}

	// A healthy report has no issues block.
	require.NotContains(t, out, "Issues:")
	// The suppressed bell renders as inactive, not as a fault.
	require.Contains(t, out, "inactive")
	// The resolved DB paths are always shown so a path drift is visible.
	require.Contains(t, out, "/var/lucid/flywheel.db")
	require.Contains(t, out, "/var/lucid/companion.db")
}

// TestTextLinesIssuesBlock asserts a broken system renders a worst-first issues
// block naming every warn and error, plus the MISSING / unverified markers.
func TestTextLinesIssuesBlock(t *testing.T) {
	now := fixedNow()
	in := healthyEnabled(now)
	in.Companion.Prompts[1].Exists = false                      // error: missing prompt
	in.CompanionJobs.Periodics = in.CompanionJobs.Periodics[1:] // error: missing morning periodic
	in.Receipts[1].Verified = false                             // warn: unverified night receipt
	in.Engine.Failures = []RunFailure{{Kind: "lucid-tripwire", ErrorClass: "timeout", Message: "discord timeout"}}
	in.Host = []Check{{Name: "host.daemon", State: Unknown, Detail: "launchctl unavailable"}}

	r := Assemble(in, now)
	out := strings.Join(r.TextLines(), "\n")

	require.Equal(t, string(Error), r.Verdict)
	require.Contains(t, out, "Issues:")
	require.Contains(t, out, "[error]")
	require.Contains(t, out, "[warn]")
	require.Contains(t, out, "MISSING")             // the dropped morning periodic
	require.Contains(t, out, "unverified")          // the night receipt
	require.Contains(t, out, "1 recent failure(s)") // the run summary
	require.Contains(t, out, "daemon: unknown")     // the host line
}

// TestTextLinesNeverRun proves the never-initialized scaffold renders clean,
// panic-free "no receipt yet" output and shows the missing-DB detail.
func TestTextLinesNeverRun(t *testing.T) {
	now := fixedNow()
	in := Inputs{
		Companion:     CompanionInfo{Enabled: true, ProviderBackend: "claude_cli", ProviderModel: "opus", Prompts: okPrompts()},
		Chain:         baseChain(),
		Engine:        DBInput{Path: "/var/lucid/flywheel.db", Missing: true},
		CompanionJobs: DBInput{Path: "/var/lucid/companion.db", Missing: true},
		Receipts:      []ReceiptStatus{{Window: "morning", Present: false}, {Window: "night", Present: false}},
	}

	var lines []string
	require.NotPanics(t, func() { lines = Assemble(in, now).TextLines() })
	out := strings.Join(lines, "\n")

	require.Contains(t, out, "no receipt yet")
	require.Contains(t, out, "scheduler not initialized")
}

// TestTextLinesEmptyCollections covers the benign "nothing here" render
// branches: no receipts, a live-but-empty job DB, and a host check with no
// detail — none should panic or read as a fault.
func TestTextLinesEmptyCollections(t *testing.T) {
	now := fixedNow()
	in := Inputs{
		Companion:     CompanionInfo{Enabled: false, ProviderBackend: "claude_cli", ProviderModel: "opus"},
		Chain:         baseChain(),
		Engine:        DBInput{Path: "/var/lucid/flywheel.db", Periodics: []PeriodicStatus{{Slug: SlugBell, Cron: "0 19 * * *", Active: true, Present: true}, {Slug: SlugTripwire, Cron: "0 6 * * *", Active: true, Present: true}}},
		CompanionJobs: DBInput{Path: "/var/lucid/companion.db"}, // present, zero periodics
		Receipts:      nil,
		Host:          []Check{{Name: "host.daemon", State: Ok}}, // no detail
	}

	var lines []string
	require.NotPanics(t, func() { lines = Assemble(in, now).TextLines() })
	out := strings.Join(lines, "\n")

	require.Contains(t, out, "no companion send has been recorded yet")
	require.Contains(t, out, "no periodics registered")
	require.Contains(t, out, "daemon: ok")
}

// TestReportJSONShape confirms the machine contract: a top-level `verdict`
// mirroring the exit code, plus the stable section keys automation reads.
func TestReportJSONShape(t *testing.T) {
	now := fixedNow()
	r := Assemble(healthyEnabled(now), now)

	raw, err := json.Marshal(r)
	require.NoError(t, err)

	var doc map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &doc))

	for _, key := range []string{"verdict", "companion", "chain", "engine", "companion_jobs", "job_stores", "receipts", "runs", "compose_fallbacks", "host", "checks"} {
		require.Contains(t, doc, key, "missing top-level JSON key %q", key)
	}

	var verdict string
	require.NoError(t, json.Unmarshal(doc["verdict"], &verdict))
	require.Equal(t, "ok", verdict)
	require.Equal(t, verdict, r.Verdict)
}

// TestReportJSONVerdictMirrorsExit checks the JSON verdict token and the process
// exit code agree for a warn state (the contract a cron gates on).
func TestReportJSONVerdictMirrorsExit(t *testing.T) {
	now := fixedNow()
	r := Assemble(healthyDisabled(now), now)

	require.Equal(t, "warn", r.Verdict)
	require.Equal(t, 1, r.ExitCode())

	raw, err := json.Marshal(r)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"verdict":"warn"`)
}

// TestPeriodicStatusTimeOmitzero locks the machine contract for the two time
// fields: a set time is emitted, a zero time is omitted — the same JSON the
// prior *time.Time+omitempty shape produced, now without pointer indirection.
func TestPeriodicStatusTimeOmitzero(t *testing.T) {
	set, err := json.Marshal(PeriodicStatus{Slug: "s", Present: true, NextRun: fixedNow()})
	require.NoError(t, err)
	require.Contains(t, string(set), `"next_run"`)
	require.NotContains(t, string(set), `"last_enqueue"`, "a zero time is omitted")

	zero, err := json.Marshal(PeriodicStatus{Slug: "s", Present: true})
	require.NoError(t, err)
	require.NotContains(t, string(zero), `"next_run"`)
	require.NotContains(t, string(zero), `"last_enqueue"`)
}

// TestTextLinesJobStores asserts the human report names every disposable job
// store together with the environment variable that relocates it. Seeing the
// whole set in one place is the point: only the Engine store is pinned by the
// launchd plist, so a reader who knows just that one path has no way to tell
// there are three more, and a repair applied to it alone looks complete.
func TestTextLinesJobStores(t *testing.T) {
	now := fixedNow()
	in := healthyEnabled(now)
	in.JobStores = okJobStores()

	r := Assemble(in, now)
	out := strings.Join(r.TextLines(), "\n")

	require.Contains(t, out, "Job stores (4;")
	require.Contains(t, out, "only the Engine store is pinned by the launchd plist")
	for _, want := range []string{
		"engine", "companion", "witness-report", "workout",
		"LUCID_SCHEDULER_DB", "LUCID_COMPANION_DB", "LUCID_WITNESS_REPORT_DB", "LUCID_WORKOUT_DB",
		"/var/lucid/witness-report.db", "/var/lucid/workout.db",
	} {
		require.Contains(t, out, want, "job-store block omits %q", want)
	}

	// The block is informational: a healthy report stays healthy and issue-free.
	require.Equal(t, string(Ok), r.Verdict)
	require.NotContains(t, out, "Issues:")
}

// TestTextLinesJobStoresDegrade covers the two degenerate renders. A store whose
// path could not be resolved keeps its row and shows a dash — `status` is what
// you run when things are already broken, so it must not drop a row or fail —
// and a report assembled without any gathering says so plainly rather than
// printing a header that claims stores it cannot list.
func TestTextLinesJobStoresDegrade(t *testing.T) {
	now := fixedNow()

	in := healthyEnabled(now)
	in.JobStores = []JobStorePath{{Name: "workout", Env: "LUCID_WORKOUT_DB"}} // unresolved path
	out := strings.Join(Assemble(in, now).TextLines(), "\n")
	require.Contains(t, out, "Job stores (1;")
	require.Contains(t, out, "—  (LUCID_WORKOUT_DB)", "an unresolved path renders as a dash, not a dropped row")

	none := strings.Join(Assemble(healthyEnabled(now), now).TextLines(), "\n")
	require.Contains(t, none, "Job stores (0;")
	require.Contains(t, none, "(none resolved)")
}

// TestReportJSONJobStores locks the machine contract for the job-store block:
// a stable `job_stores` array whose entries each carry a name, a path, and the
// env var that overrides it, so an agent can read the full set without parsing
// the human render.
func TestReportJSONJobStores(t *testing.T) {
	now := fixedNow()
	in := healthyEnabled(now)
	in.JobStores = okJobStores()

	raw, err := json.Marshal(Assemble(in, now))
	require.NoError(t, err)

	var doc struct {
		JobStores []JobStorePath `json:"job_stores"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))
	require.Len(t, doc.JobStores, 4)

	names := make([]string, 0, len(doc.JobStores))
	for _, s := range doc.JobStores {
		require.NotEmpty(t, s.Path, "%s has no resolved path", s.Name)
		require.NotEmpty(t, s.Env, "%s names no override env var", s.Name)
		names = append(names, s.Name)
	}
	require.Equal(t, []string{"engine", "companion", "witness-report", "workout"}, names)
}

// lineIndex returns the index of the first rendered line starting with prefix,
// failing the test when no line does.
func lineIndex(t *testing.T, lines []string, prefix string) int {
	t.Helper()
	for i, l := range lines {
		if strings.HasPrefix(l, prefix) {
			return i
		}
	}
	require.Failf(t, "line not rendered", "no line starts with %q", prefix)
	return -1
}

// TestTextLinesComposeFallbacks asserts the human section: a counted header
// directly after Recent runs and before Host, then one `<time>  <kind>  <reason>`
// line per entry for every reason — an unknown one verbatim. The provider error
// detail never reaches the text, and a fallback never reaches the issues block.
func TestTextLinesComposeFallbacks(t *testing.T) {
	now := fixedNow()
	entries := []ComposeFallback{
		fallbackAt("companion", "lucid_companion_morning", "companion-morning", "provider_outage", now.Add(-1*time.Hour)),
		fallbackAt("companion", "lucid_companion_night", "companion-night", "unusable_reply", now.Add(-17*time.Hour)),
		fallbackAt("witness-report", "lucid_witness_report_weekly", "witness", "safety_tripped", now.Add(-48*time.Hour)),
		fallbackAt("workout", "lucid_workout_daily", "workout", "a_reason_from_the_future", now.Add(-96*time.Hour)),
	}
	entries[0].Error = "synthetic-error-alpha: provider timed out"
	entries[1].Error = "synthetic-error-bravo: reply was empty"
	entries[3].Error = "synthetic-error-delta: something new"

	in := healthyEnabled(now)
	in.ComposeFallbacks.Entries = entries
	r := Assemble(in, now)
	lines := r.TextLines()

	runs := lineIndex(t, lines, "Recent runs:")
	header := lineIndex(t, lines, "Recent compose fallbacks:")
	host := lineIndex(t, lines, "Host:")
	require.Equal(t, "Recent compose fallbacks: 4 in the last 7d", lines[header])
	require.Equal(t, runs+1, header, "the section sits directly after Recent runs")
	require.Less(t, header, host, "and before Host")

	want := []string{
		"  2026-07-16 11:00 UTC  companion-morning  provider_outage",
		"  2026-07-15 19:00 UTC  companion-night  unusable_reply",
		"  2026-07-14 12:00 UTC  witness  safety_tripped",
		"  2026-07-12 12:00 UTC  workout  a_reason_from_the_future",
	}
	require.Equal(t, want, lines[header+1:host], "one line per entry, nothing else in the section")
	for i, e := range r.ComposeFallbacks.Entries {
		require.Equal(t, "  "+fmtTime(e.At)+"  "+e.Kind+"  "+e.Reason, want[i], "the time uses the report's timeLayout")
	}

	out := strings.Join(lines, "\n")
	for _, e := range entries {
		if e.Error != "" {
			require.NotContains(t, out, e.Error, "the provider error detail is JSON-only")
		}
	}
	require.NotContains(t, out, "synthetic-error")
	require.Equal(t, string(Ok), r.Verdict)
	require.NotContains(t, out, "Issues:", "a fallback is never an issue")
}

// TestTextLinesComposeFallbacks_None locks the empty state: one line, worded
// exactly, right after Recent runs — mirroring that section's own "no recent
// failures" line, so a quiet week still reads as checked rather than skipped.
func TestTextLinesComposeFallbacks_None(t *testing.T) {
	now := fixedNow()
	lines := Assemble(healthyEnabled(now), now).TextLines()

	i := lineIndex(t, lines, "Recent compose fallbacks:")
	require.Equal(t, "Recent compose fallbacks: none in the last 7d", lines[i])
	require.Equal(t, "Recent runs: no recent failures", lines[i-1])
	require.True(t, strings.HasPrefix(lines[i+1], "Host:"), "an empty section is a single line")
}

// TestTextLinesComposeFallbacks_Notes: a store that could not be scanned renders
// as a `note:` line under the header — after the empty-state header when nothing
// was found, after the entries when something was — and changes nothing about
// the verdict, the checks, or the exit code.
func TestTextLinesComposeFallbacks_Notes(t *testing.T) {
	now := fixedNow()
	const note = "workout job store at /tmp/synthetic/workout.db is unreadable — " +
		"compose fallbacks not scanned (file is not a database)"
	baseline := Assemble(healthyEnabled(now), now)

	noEntries := healthyEnabled(now)
	noEntries.ComposeFallbacks.Notes = []string{note}
	r := Assemble(noEntries, now)
	lines := r.TextLines()
	i := lineIndex(t, lines, "Recent compose fallbacks:")
	require.Equal(t, []string{
		"Recent compose fallbacks: none in the last 7d",
		"  note: " + note,
	}, lines[i:i+2])
	require.True(t, strings.HasPrefix(lines[i+2], "Host:"))
	require.Equal(t, baseline.Verdict, r.Verdict)
	require.Equal(t, baseline.Checks, r.Checks)
	require.Equal(t, baseline.ExitCode(), r.ExitCode())
	require.NotContains(t, strings.Join(lines, "\n"), "Issues:", "an unreadable store is a note, not an issue")

	withEntries := healthyEnabled(now)
	withEntries.ComposeFallbacks = FallbackInput{
		Entries: []ComposeFallback{
			fallbackAt("companion", "lucid_companion_night", "companion-night", "unusable_reply", now.Add(-2*time.Hour)),
		},
		Notes: []string{note},
	}
	r = Assemble(withEntries, now)
	lines = r.TextLines()
	i = lineIndex(t, lines, "Recent compose fallbacks:")
	require.Equal(t, []string{
		"Recent compose fallbacks: 1 in the last 7d",
		"  2026-07-16 10:00 UTC  companion-night  unusable_reply",
		"  note: " + note,
	}, lines[i:i+3])
	require.True(t, strings.HasPrefix(lines[i+3], "Host:"))
	require.Equal(t, baseline.Verdict, r.Verdict)
	require.Equal(t, baseline.Checks, r.Checks)
	require.Equal(t, baseline.ExitCode(), r.ExitCode())
}

// TestReportJSONComposeFallbacks locks the machine contract for the section: a
// top-level `compose_fallbacks` object with the window, the count, the entries
// (each with kind, job_kind, store, finished_at, reason, and error only when the
// marker carried one — the local instant is never marshaled), and notes only
// when a store could not be scanned. The existing `runs` object keeps exactly its
// two keys.
func TestReportJSONComposeFallbacks(t *testing.T) {
	now := fixedNow()
	const note = "workout job store at /tmp/synthetic/workout.db is unreadable — " +
		"compose fallbacks not scanned (file is not a database)"

	backstop := fallbackAt("companion", "lucid_companion_morning_backstop", "companion-morning", "provider_outage", now.Add(-1*time.Hour))
	backstop.Error = "synthetic provider timeout"
	in := healthyEnabled(now)
	in.ComposeFallbacks = FallbackInput{
		Entries: []ComposeFallback{
			backstop,
			fallbackAt("witness-report", "lucid_witness_report_weekly", "witness", "safety_tripped", now.Add(-72*time.Hour)),
		},
		Notes: []string{note},
	}

	doc := marshalDoc(t, Assemble(in, now))
	require.Contains(t, doc, "compose_fallbacks")

	var cf map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(doc["compose_fallbacks"], &cf))
	require.Equal(t, []string{"count", "entries", "notes", "window"}, slices.Sorted(maps.Keys(cf)))
	require.JSONEq(t, `"168h"`, string(cf["window"]))
	require.JSONEq(t, `2`, string(cf["count"]))

	var notes []string
	require.NoError(t, json.Unmarshal(cf["notes"], &notes))
	require.Equal(t, []string{note}, notes)

	var entries []map[string]any
	require.NoError(t, json.Unmarshal(cf["entries"], &entries))
	require.Equal(t, []map[string]any{
		{
			"kind":        "companion-morning",
			"job_kind":    "lucid_companion_morning_backstop",
			"store":       "companion",
			"finished_at": "2026-07-16T11:00:00Z",
			"reason":      "provider_outage",
			"error":       "synthetic provider timeout",
		},
		{
			"kind":        "witness",
			"job_kind":    "lucid_witness_report_weekly",
			"store":       "witness-report",
			"finished_at": "2026-07-13T12:00:00Z",
			"reason":      "safety_tripped",
		},
	}, entries, "an empty error is omitted, and no other field is marshaled")

	var runs map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(doc["runs"], &runs))
	require.Equal(t, []string{"failure_count", "failures"}, slices.Sorted(maps.Keys(runs)),
		"the runs object is unchanged")

	// Without a note the key is omitted entirely.
	in.ComposeFallbacks.Notes = nil
	doc = marshalDoc(t, Assemble(in, now))
	cf = nil
	require.NoError(t, json.Unmarshal(doc["compose_fallbacks"], &cf))
	require.NotContains(t, cf, "notes")
	require.Contains(t, cf, "entries")
}

// marshalDoc marshals a report and decodes its top level into raw sections, so a
// contract test can inspect one key at a time.
func marshalDoc(t *testing.T, r Report) map[string]json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(r)
	require.NoError(t, err)
	var doc map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &doc))
	return doc
}
