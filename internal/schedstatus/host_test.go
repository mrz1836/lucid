package schedstatus

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeProbe is a deterministic HostProbe for tests: it returns exactly the checks
// it is constructed with, so a test can drive Assemble with a down / stale /
// unknown host without touching the real launchd/Hush/process state.
type fakeProbe struct{ checks []Check }

func (p fakeProbe) Probe() []Check { return p.checks }

// validState reports whether s is one of the four defined check states — the
// invariant every probe (real or portable-default) must honor.
func validState(s CheckState) bool {
	switch s {
	case Ok, Warn, Error, Unknown:
		return true
	default:
		return false
	}
}

// TestUnknownProbe: the portable default reports every host signal Unknown, never
// Ok, with the stable host.* names.
func TestUnknownProbe(t *testing.T) {
	checks := unknownProbe{}.Probe()
	require.Len(t, checks, 4)
	names := map[string]bool{}
	for _, c := range checks {
		assert.Equal(t, Unknown, c.State, "%s must be Unknown on the portable default", c.Name)
		assert.NotEmpty(t, c.Detail)
		names[c.Name] = true
	}
	for _, want := range []string{checkDaemon, checkSupervisor, checkBinary, checkDBPath} {
		assert.True(t, names[want], "missing host check %q", want)
	}
}

// TestNewHostProbe: the platform probe (macOS best-effort, or the portable
// Unknown default elsewhere) always returns the four host.* checks with valid
// states and never panics — the cross-platform contract. It does not assert
// specific states, since a host where the daemon is actually running may
// legitimately report Ok.
func TestNewHostProbe(t *testing.T) {
	checks := NewHostProbe("/some/resolved/flywheel.db").Probe()
	require.Len(t, checks, 4)
	for _, c := range checks {
		assert.True(t, validState(c.State), "%s has an invalid state %q", c.Name, c.State)
		assert.Contains(t, c.Name, "host.", "host check names carry the host. prefix")
	}
}

// TestParsePID covers the pid-file body parse.
func TestParsePID(t *testing.T) {
	cases := []struct {
		body string
		want int
		ok   bool
	}{
		{"1234\n", 1234, true},
		{"  42  ", 42, true},
		{"", 0, false},
		{"abc", 0, false},
		{"0", 0, false},
		{"-5", 0, false},
	}
	for _, tc := range cases {
		got, ok := parsePID(tc.body)
		assert.Equal(t, tc.ok, ok, "parsePID(%q) ok", tc.body)
		assert.Equal(t, tc.want, got, "parsePID(%q) value", tc.body)
	}
}

// TestParseLstart parses the ctime start-time form ps prints, and rejects junk.
func TestParseLstart(t *testing.T) {
	got, ok := parseLstart("Wed Jul 16 13:33:46 2026")
	require.True(t, ok)
	assert.Equal(t, 2026, got.Year())
	assert.Equal(t, time.July, got.Month())
	assert.Equal(t, 16, got.Day())
	assert.Equal(t, 13, got.Hour())

	_, ok = parseLstart("")
	assert.False(t, ok)
	_, ok = parseLstart("not a date")
	assert.False(t, ok)
}

// TestParsePlistEnv extracts an env value from a synthetic launchd plist, and
// reports absence/malformed entries as not-found.
func TestParsePlistEnv(t *testing.T) {
	plist := `<dict>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key><string>/usr/bin</string>
    <key>LUCID_SCHEDULER_DB</key><string>/var/lucid/flywheel.db</string>
  </dict>
</dict>`
	got, ok := parsePlistEnv(plist, "LUCID_SCHEDULER_DB")
	require.True(t, ok)
	assert.Equal(t, "/var/lucid/flywheel.db", got)

	got, ok = parsePlistEnv(plist, "PATH")
	require.True(t, ok)
	assert.Equal(t, "/usr/bin", got)

	_, ok = parsePlistEnv(plist, "NOT_PRESENT")
	assert.False(t, ok)

	_, ok = parsePlistEnv("<key>DANGLING</key>", "DANGLING")
	assert.False(t, ok, "a key with no following <string> is not found")
}

// TestClassifyDaemon covers every branch of the pure daemon decision.
func TestClassifyDaemon(t *testing.T) {
	alive := classifyDaemon(daemonObservation{pidFileFound: true, pidFilePID: 10, pidAlive: true})
	assert.Equal(t, Ok, alive.State)

	dead := classifyDaemon(daemonObservation{pidFileFound: true, pidFilePID: 10, pidAlive: false})
	assert.Equal(t, Error, dead.State, "a pid file with a dead process is a positively-detected down")

	proc := classifyDaemon(daemonObservation{procFound: true, procPID: 99})
	assert.Equal(t, Ok, proc.State)

	loadedNoProc := classifyDaemon(daemonObservation{launchdKnown: true, launchdOn: true})
	assert.Equal(t, Error, loadedNoProc.State, "an installed launchd job with no process is down")

	nothing := classifyDaemon(daemonObservation{launchdKnown: true, launchdOn: false})
	assert.Equal(t, Unknown, nothing.State, "not installed / nothing to see is Unknown, never a false problem")

	blind := classifyDaemon(daemonObservation{})
	assert.Equal(t, Unknown, blind.State)
}

// TestClassifyStaleBinary: a build newer than the running daemon warns; an equal
// or older build is ok.
func TestClassifyStaleBinary(t *testing.T) {
	start := time.Date(2026, 7, 16, 13, 0, 0, 0, time.UTC)
	newer := classifyStaleBinary(start.Add(time.Hour), start)
	assert.Equal(t, Warn, newer.State, "an on-disk build newer than the daemon is stale")

	older := classifyStaleBinary(start.Add(-time.Hour), start)
	assert.Equal(t, Ok, older.State)

	equal := classifyStaleBinary(start, start)
	assert.Equal(t, Ok, equal.State)
}

// TestClassifyDBPath: a real mismatch warns; a match or an empty side is ok.
func TestClassifyDBPath(t *testing.T) {
	assert.Equal(t, Warn, classifyDBPath("/a/flywheel.db", "/b/flywheel.db").State)
	assert.Equal(t, Ok, classifyDBPath("/a/flywheel.db", "/a/flywheel.db").State)
	assert.Equal(t, Ok, classifyDBPath("", "/b/flywheel.db").State, "an unknown resolved path does not warn")
	assert.Equal(t, Ok, classifyDBPath("/a/flywheel.db", "").State)
}

// TestHostChecksDriveVerdict: a fake probe returning a down (Error) or stale
// (Warn) host check flows through Assemble into the verdict, while an Unknown-only
// host leaves an otherwise-healthy verdict Ok.
func TestHostChecksDriveVerdict(t *testing.T) {
	now := time.Date(2026, 7, 16, 20, 0, 0, 0, time.UTC)

	// A down host (Error) dominates an otherwise-healthy verdict.
	down := healthyInputs()
	down.Host = []Check{errorCheck(checkDaemon, "daemon down")}
	assert.Equal(t, string(Error), Assemble(down, now).Verdict, "a down host drives error")

	// A stale-binary host (Warn) lowers an otherwise-ok verdict to warn.
	stale := healthyInputs()
	stale.Host = []Check{warnCheck(checkBinary, "stale build")}
	assert.Equal(t, string(Warn), Assemble(stale, now).Verdict, "a stale-binary host drives warn")

	// An Unknown-only host must not lower an otherwise-ok verdict.
	blind := healthyInputs()
	blind.Host = []Check{unknownCheck(checkDaemon, "not inspectable")}
	assert.Equal(t, string(Ok), Assemble(blind, now).Verdict, "unknown host never lowers the verdict")
}

// healthyInputs builds a fully-healthy enabled-companion Inputs (all periodics
// active, both elapsed windows verified as of 2026-07-16 20:00 UTC) so a test can
// isolate the host contribution to the verdict.
func healthyInputs() Inputs {
	today := "2026-07-16"
	return Inputs{
		Companion: CompanionInfo{Enabled: true, Prompts: nil},
		Chain:     ChainMarks{BellTime: "19:00", TripwireTime: "06:00"},
		Teeth: DBInput{Path: "/t.db", Periodics: []PeriodicStatus{
			{Slug: SlugTripwire, Present: true, Active: true},
			{Slug: SlugBell, Present: true, Active: false},
		}},
		CompanionJobs: DBInput{Path: "/c.db", Periodics: []PeriodicStatus{
			{Slug: SlugCompanionMorning, Present: true, Active: true},
			{Slug: SlugCompanionNight, Present: true, Active: true},
		}},
		Receipts: []ReceiptStatus{
			{Window: windowMorning, Present: true, Verified: true, Date: today},
			{Window: windowNight, Present: true, Verified: true, Date: today},
		},
	}
}
