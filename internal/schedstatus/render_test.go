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
		"Teeth periodics",
		"Companion periodics",
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
	in.Teeth.Failures = []RunFailure{{Kind: "lucid-tripwire", ErrorClass: "timeout", Message: "discord timeout"}}
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
		Teeth:         DBInput{Path: "/var/lucid/flywheel.db", Missing: true},
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
		Teeth:         DBInput{Path: "/var/lucid/flywheel.db", Periodics: []PeriodicStatus{{Slug: SlugBell, Cron: "0 19 * * *", Active: true, Present: true}, {Slug: SlugTripwire, Cron: "0 6 * * *", Active: true, Present: true}}},
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

	for _, key := range []string{"verdict", "companion", "chain", "teeth", "companion_jobs", "receipts", "runs", "host", "checks"} {
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
