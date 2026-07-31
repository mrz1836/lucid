package schedstatus

import (
	"encoding/json"
	"strings"
	"testing"

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

	for _, key := range []string{"verdict", "companion", "chain", "engine", "companion_jobs", "job_stores", "receipts", "runs", "host", "checks"} {
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
