package schedstatus

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fixedNow is the deterministic clock every classification test runs against.
// At 12:00 UTC the morning window (tripwire 06:00) has already elapsed today
// while the night window (bell 19:00) has not, so the most-recent elapsed
// window is this morning — the receipt-miss cases pivot on that.
func fixedNow() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) }

func baseChain() ChainMarks { return ChainMarks{BellTime: "19:00", TripwireTime: "06:00"} }

func okPrompts() []PromptPath {
	return []PromptPath{
		{Role: "morning", Path: "/prompts/morning.md", Exists: true},
		{Role: "night", Path: "/prompts/night.md", Exists: true},
		{Role: "system", Path: "/prompts/system.md", Exists: true},
	}
}

// okEngine builds a healthy engine job DB. The bell and its evening backstop take
// turns: with the companion enabled the bell is deliberately suppressed and the
// backstop is armed to catch a companion that never delivers; with the companion
// disabled the bell fires itself and the backstop stands down. Either way the
// inactive one is the correct state and must not fault.
func okEngine(companionEnabled bool, now time.Time) DBInput {
	return DBInput{
		Path: "/var/lucid/flywheel.db",
		Periodics: []PeriodicStatus{
			{Slug: SlugBell, Cron: "0 19 * * *", Active: !companionEnabled, Present: true, NextRun: now.Add(6 * time.Hour)},
			{Slug: SlugBellFallback, Cron: "0 23 * * *", Active: companionEnabled, Present: true, NextRun: now.Add(10 * time.Hour)},
			{Slug: SlugTripwire, Cron: "0 6 * * *", Active: true, Present: true, NextRun: now.Add(18 * time.Hour)},
		},
	}
}

// okCompanionDB builds a healthy companion job DB: all three periodics — the
// morning and night sends and the morning backstop that catches a morning send
// that never went out — registered and active. None carries a recorded cursor,
// which is deliberately not evidence of a frozen one.
func okCompanionDB() DBInput {
	return DBInput{
		Path: "/var/lucid/companion.db",
		Periodics: []PeriodicStatus{
			{Slug: SlugCompanionMorning, Cron: "0 6 * * *", Active: true, Present: true},
			{Slug: SlugCompanionNight, Cron: "0 19 * * *", Active: true, Present: true},
			{Slug: SlugCompanionMorningBackstop, Cron: "0 7 * * *", Active: true, Present: true},
		},
	}
}

// okJobStores builds the full set of resolved job-store paths the CLI hands the
// report — the five independent stores the supervised scheduler writes to, each
// with the environment variable that relocates it. It is informational input
// only: no case here should ever see it change a verdict.
func okJobStores() []JobStorePath {
	return []JobStorePath{
		{Name: "engine", Path: "/var/lucid/flywheel.db", Env: "LUCID_SCHEDULER_DB"},
		{Name: "companion", Path: "/var/lucid/companion.db", Env: "LUCID_COMPANION_DB"},
		{Name: "witness-report", Path: "/var/lucid/witness-report.db", Env: "LUCID_WITNESS_REPORT_DB"},
		{Name: "workout", Path: "/var/lucid/workout.db", Env: "LUCID_WORKOUT_DB"},
		{Name: "contact-weekly", Path: "/var/lucid/contact-weekly.db", Env: "LUCID_CONTACT_WEEKLY_DB"},
	}
}

// okReceipts builds current, verified receipts for both windows. Morning is the
// most-recent elapsed window at 12:00, so it carries today's date; night last
// fired yesterday evening.
func okReceipts(now time.Time) []ReceiptStatus {
	return []ReceiptStatus{
		{Window: "morning", Present: true, Date: now.Format(dateLayout), MessageID: "m1", Verified: true, DeliveredAt: "2026-07-16T06:00:05Z"},
		{Window: "night", Present: true, Date: now.AddDate(0, 0, -1).Format(dateLayout), MessageID: "n1", Verified: true, DeliveredAt: "2026-07-15T19:00:05Z"},
	}
}

// healthyEnabled is a fully-healthy companion-enabled system: bell suppressed,
// tripwire + both companion periodics active, current verified receipts, daemon
// up. Every case that wants to prove a single failure mutates a copy of this.
func healthyEnabled(now time.Time) Inputs {
	return Inputs{
		Companion:     CompanionInfo{Enabled: true, ProviderBackend: "claude_cli", ProviderModel: "opus", Prompts: okPrompts()},
		Chain:         baseChain(),
		Engine:        okEngine(true, now),
		CompanionJobs: okCompanionDB(),
		Receipts:      okReceipts(now),
		Host:          []Check{{Name: "host.daemon", State: Ok, Detail: "running"}},
	}
}

// healthyDisabled is a healthy engine-only system: the companion is off (a warn,
// not an error), the bell and tripwire both deliver to the user and are active,
// and the companion job DB legitimately does not exist.
func healthyDisabled(now time.Time) Inputs {
	return Inputs{
		Companion:     CompanionInfo{Enabled: false, ProviderBackend: "claude_cli", ProviderModel: "opus"},
		Chain:         baseChain(),
		Engine:        okEngine(false, now),
		CompanionJobs: DBInput{Path: "/var/lucid/companion.db", Missing: true},
		Host:          []Check{{Name: "host.daemon", State: Ok, Detail: "running"}},
	}
}

// parkEngine switches one engine periodic off by slug — the exact state a parked
// send is in — so a case reads as what it is testing and never depends on
// fixture ordering.
func parkEngine(in *Inputs, slug string) {
	for i := range in.Engine.Periodics {
		if in.Engine.Periodics[i].Slug == slug {
			in.Engine.Periodics[i].Active = false
			return
		}
	}
}

// setTripwireCursor moves the tripwire's next run — the knob that separates a
// merely-due periodic from one whose cursor is frozen.
func setTripwireCursor(in *Inputs, next time.Time) {
	for i := range in.Engine.Periodics {
		if in.Engine.Periodics[i].Slug == SlugTripwire {
			in.Engine.Periodics[i].NextRun = next
			return
		}
	}
}

// checkFor returns the check with the given name, or false when the report has
// none — how the legibility tests assert on one classification without depending
// on check ordering.
func checkFor(r Report, name string) (Check, bool) {
	for _, c := range r.Checks {
		if c.Name == name {
			return c, true
		}
	}
	return Check{}, false
}

// periodicFor returns the reported engine periodic row for a slug.
func periodicFor(r Report, slug string) (PeriodicStatus, bool) {
	for _, p := range r.Engine.Periodics {
		if p.Slug == slug {
			return p, true
		}
	}
	return PeriodicStatus{}, false
}

// verdictCase is one scenario of the verdict matrix: a name, a builder for its
// inputs, and the verdict those inputs must roll up to.
type verdictCase struct {
	name    string
	build   func() Inputs
	verdict CheckState
}

// verdictMatrixCases is the verdict matrix — every threshold Assemble applies,
// as synthetic inputs at now. It is shared so a property that must hold for
// every verdict (compose fallbacks never move it) runs over the same scenarios
// the matrix itself proves.
func verdictMatrixCases(now time.Time) []verdictCase {
	return []verdictCase{
		{
			name:    "healthy enabled",
			build:   func() Inputs { return healthyEnabled(now) },
			verdict: Ok,
		},
		{
			name:    "healthy disabled is a warn",
			build:   func() Inputs { return healthyDisabled(now) },
			verdict: Warn,
		},
		{
			name: "missing prompt path is an error",
			build: func() Inputs {
				in := healthyEnabled(now)
				in.Companion.Prompts[1].Exists = false // night prompt gone
				return in
			},
			verdict: Error,
		},
		{
			name: "missing engine DB is an error",
			build: func() Inputs {
				in := healthyEnabled(now)
				in.Engine = DBInput{Path: "/var/lucid/flywheel.db", Missing: true}
				return in
			},
			verdict: Error,
		},
		{
			name: "malformed engine DB is an error",
			build: func() Inputs {
				in := healthyEnabled(now)
				in.Engine = DBInput{Path: "/var/lucid/flywheel.db", Err: "file is not a database"}
				return in
			},
			verdict: Error,
		},
		{
			name: "missing companion DB while enabled is an error",
			build: func() Inputs {
				in := healthyEnabled(now)
				in.CompanionJobs = DBInput{Path: "/var/lucid/companion.db", Missing: true}
				return in
			},
			verdict: Error,
		},
		{
			name: "missing required companion periodic is an error",
			build: func() Inputs {
				in := healthyEnabled(now)
				in.CompanionJobs.Periodics = in.CompanionJobs.Periodics[1:] // drop morning
				return in
			},
			verdict: Error,
		},
		{
			name: "inactive tripwire is an error",
			build: func() Inputs {
				in := healthyEnabled(now)
				parkEngine(&in, SlugTripwire)
				return in
			},
			verdict: Error,
		},
		{
			name: "inactive evening backstop while the companion owns the send is an error",
			build: func() Inputs {
				in := healthyEnabled(now)
				parkEngine(&in, SlugBellFallback)
				return in
			},
			verdict: Error,
		},
		{
			name: "inactive evening backstop while the companion is disabled is not an error",
			build: func() Inputs {
				// healthyDisabled already stands the backstop down; prove ok
				// (the warn is the disabled companion, not the backstop).
				return healthyDisabled(now)
			},
			verdict: Warn,
		},
		{
			name: "an engine cursor stuck a full day behind is an error",
			build: func() Inputs {
				in := healthyEnabled(now)
				setTripwireCursor(&in, now.Add(-36*time.Hour))
				return in
			},
			verdict: Error,
		},
		{
			name: "a merely-due engine cursor is not an error",
			build: func() Inputs {
				in := healthyEnabled(now)
				setTripwireCursor(&in, now.Add(-30*time.Minute))
				return in
			},
			verdict: Ok,
		},
		{
			name: "inactive bell while companion owns the evening send is not an error",
			build: func() Inputs {
				// healthyEnabled already suppresses the bell (inactive); prove ok.
				return healthyEnabled(now)
			},
			verdict: Ok,
		},
		{
			name: "missing bell while companion owns the evening send is not an error",
			build: func() Inputs {
				in := healthyEnabled(now)
				in.Engine.Periodics = in.Engine.Periodics[1:] // drop the suppressed bell entirely
				return in
			},
			verdict: Ok,
		},
		{
			name: "inactive bell while companion disabled is an error",
			build: func() Inputs {
				in := healthyDisabled(now)
				in.Engine.Periodics[0].Active = false // bell off, nothing delivers the evening
				return in
			},
			verdict: Error,
		},
		{
			name: "verified receipts are ok",
			build: func() Inputs {
				return healthyEnabled(now)
			},
			verdict: Ok,
		},
		{
			name: "unverified latest receipt is a warn",
			build: func() Inputs {
				in := healthyEnabled(now)
				in.Receipts[0].Verified = false // this-morning receipt unverified
				return in
			},
			verdict: Warn,
		},
		{
			name: "most-recent elapsed window with no receipt is an error",
			build: func() Inputs {
				in := healthyEnabled(now)
				in.Receipts[0] = ReceiptStatus{Window: "morning", Present: false} // this morning never delivered
				return in
			},
			verdict: Error,
		},
		{
			name: "most-recent elapsed window with a stale receipt is an error",
			build: func() Inputs {
				in := healthyEnabled(now)
				in.Receipts[0].Date = now.AddDate(0, 0, -1).Format(dateLayout) // yesterday's date, stale
				return in
			},
			verdict: Error,
		},
		{
			name: "unknown host checks do not lower an otherwise-ok verdict",
			build: func() Inputs {
				in := healthyEnabled(now)
				in.Host = []Check{
					{Name: "host.daemon", State: Unknown, Detail: "launchctl unavailable"},
					{Name: "host.hush", State: Unknown, Detail: "socket not found"},
				}
				return in
			},
			verdict: Ok,
		},
		{
			name: "daemon-down host check is an error",
			build: func() Inputs {
				in := healthyEnabled(now)
				in.Host = []Check{{Name: "host.daemon", State: Error, Detail: "daemon not running"}}
				return in
			},
			verdict: Error,
		},
		{
			name: "stale supervised binary host check is a warn",
			build: func() Inputs {
				in := healthyEnabled(now)
				in.Host = []Check{{Name: "host.binary", State: Warn, Detail: "on-disk build is newer than the running daemon"}}
				return in
			},
			verdict: Warn,
		},
		{
			name: "unparseable chain marks skip the receipt-miss check",
			build: func() Inputs {
				in := healthyEnabled(now)
				in.Chain = ChainMarks{BellTime: "nope", TripwireTime: "bad"}
				in.Receipts[0] = ReceiptStatus{Window: "morning", Present: false} // would miss, but no window is derivable
				return in
			},
			verdict: Ok,
		},
	}
}

func TestAssembleVerdictMatrix(t *testing.T) {
	now := fixedNow()

	for _, tc := range verdictMatrixCases(now) {
		t.Run(tc.name, func(t *testing.T) {
			r := Assemble(tc.build(), now)
			require.Equal(t, string(tc.verdict), r.Verdict, "verdict mismatch")
			require.Equal(t, wantExit(tc.verdict), r.ExitCode(), "exit code must mirror verdict")
		})
	}
}

// wantExit is the exit code each verdict must map to (0/1/2).
func wantExit(v CheckState) int {
	switch v {
	case Error:
		return 2
	case Warn:
		return 1
	case Ok, Unknown:
		return 0
	default:
		return 0
	}
}

// TestExitCodeMirrorsVerdict checks the mapping directly for every verdict token,
// including the defensive default.
func TestExitCodeMirrorsVerdict(t *testing.T) {
	require.Equal(t, 0, Report{Verdict: string(Ok)}.ExitCode())
	require.Equal(t, 1, Report{Verdict: string(Warn)}.ExitCode())
	require.Equal(t, 2, Report{Verdict: string(Error)}.ExitCode())
	require.Equal(t, 0, Report{Verdict: string(Unknown)}.ExitCode())
	require.Equal(t, 0, Report{Verdict: "garbage"}.ExitCode())
}

// TestAssembleAggregatesRunFailures confirms the bounded recent-run summary is
// concatenated across both job DBs and counted, without changing the verdict
// (failures are surfaced, not classified).
func TestAssembleAggregatesRunFailures(t *testing.T) {
	now := fixedNow()
	in := healthyEnabled(now)
	in.Engine.Failures = []RunFailure{{Kind: "lucid-tripwire", ErrorClass: "timeout", Message: "discord timeout"}}
	in.CompanionJobs.Failures = []RunFailure{{Kind: "lucid-companion-morning", ErrorClass: "timeout", Message: "discord timeout"}}

	r := Assemble(in, now)
	require.Equal(t, 2, r.Runs.FailureCount)
	require.Len(t, r.Runs.Failures, 2)
	require.Equal(t, string(Ok), r.Verdict, "recent failures alone must not lower the verdict")
}

// TestParkedEnginePeriodicNamesRemedy proves the whole point of the parked-send
// report: a periodic that is intended active but switched off is an error whose
// detail names the slug *and* the exact command that repairs it, so the report is
// actionable without a second lookup.
func TestParkedEnginePeriodicNamesRemedy(t *testing.T) {
	now := fixedNow()
	in := healthyEnabled(now)
	parkEngine(&in, SlugTripwire)

	r := Assemble(in, now)
	require.Equal(t, string(Error), r.Verdict)

	c, ok := checkFor(r, "periodic."+SlugTripwire)
	require.True(t, ok, "the parked periodic produces its own named check")
	require.Equal(t, Error, c.State)
	require.Contains(t, c.Detail, SlugTripwire, "the line names the parked periodic")
	require.Contains(t, c.Detail, "run: lucid scheduler reconcile --slug "+SlugTripwire,
		"the line names the exact repair command")

	// And it reaches the reader through the closing issues block.
	out := strings.Join(r.TextLines(), "\n")
	require.Contains(t, out, "[error] ")
	require.Contains(t, out, "lucid scheduler reconcile --slug "+SlugTripwire)
}

// TestStuckCursorNamesRemedy covers the second half of parked: an *active*
// periodic whose cursor froze a full day behind is reported with the same remedy,
// while one that has merely just come due is left alone — a running daemon
// between its mark and its next poll is never called broken.
func TestStuckCursorNamesRemedy(t *testing.T) {
	now := fixedNow()

	stuck := healthyEnabled(now)
	setTripwireCursor(&stuck, now.Add(-36*time.Hour))
	r := Assemble(stuck, now)
	require.Equal(t, string(Error), r.Verdict)
	c, ok := checkFor(r, "periodic."+SlugTripwire)
	require.True(t, ok)
	require.Equal(t, Error, c.State)
	require.Contains(t, c.Detail, "stuck")
	require.Contains(t, c.Detail, "run: lucid scheduler reconcile --slug "+SlugTripwire)

	due := healthyEnabled(now)
	setTripwireCursor(&due, now.Add(-30*time.Minute))
	r = Assemble(due, now)
	require.Equal(t, string(Ok), r.Verdict, "a merely-due cursor is not parked")
	_, ok = checkFor(r, "periodic."+SlugTripwire)
	require.False(t, ok, "a healthy periodic produces no check at all")

	// A periodic with no recorded cursor is not evidence of a frozen one.
	unknown := healthyEnabled(now)
	setTripwireCursor(&unknown, time.Time{})
	require.Equal(t, string(Ok), Assemble(unknown, now).Verdict)
}

// TestSuppressedBellIsIntendedOK is the inverse reading: a bell that is inactive
// because the companion owns the evening send is a healthy, intended state. It is
// reported Ok with the reason — never an error, and never silently — both in the
// check list and on the periodic's own row.
func TestSuppressedBellIsIntendedOK(t *testing.T) {
	now := fixedNow()
	r := Assemble(healthyEnabled(now), now)
	require.Equal(t, string(Ok), r.Verdict)
	require.Equal(t, 0, r.ExitCode(), "the healthy exit code is unchanged")

	c, ok := checkFor(r, "periodic."+SlugBell)
	require.True(t, ok, "the suppressed bell is reported, not omitted")
	require.Equal(t, Ok, c.State, "an intended-inactive periodic is never a fault")
	require.Contains(t, c.Detail, "suppressed by companion (intended)")

	p, ok := periodicFor(r, SlugBell)
	require.True(t, ok)
	require.Equal(t, "suppressed by companion (intended)", p.Note)

	out := strings.Join(r.TextLines(), "\n")
	require.Contains(t, out, "suppressed by companion (intended)", "the reason renders beside the row")
	require.NotContains(t, out, "Issues:", "an intended state is not an issue")
}

// TestParkedBackstopNamesRemedy: while the companion owns the evening window its
// backstop is what guarantees the window is never silently missed, so a parked
// backstop is an error naming its own repair command.
func TestParkedBackstopNamesRemedy(t *testing.T) {
	now := fixedNow()
	in := healthyEnabled(now)
	parkEngine(&in, SlugBellFallback)

	r := Assemble(in, now)
	require.Equal(t, string(Error), r.Verdict)
	c, ok := checkFor(r, "periodic."+SlugBellFallback)
	require.True(t, ok)
	require.Equal(t, Error, c.State)
	require.Contains(t, c.Detail, SlugBellFallback)
	require.Contains(t, c.Detail, "run: lucid scheduler reconcile --slug "+SlugBellFallback)
}

// TestBackstopStoodDownIsIntendedOK: with the companion disabled the real bell
// fires and the backstop is deliberately inactive — reported with its reason, and
// the only fault is the disabled companion itself.
func TestBackstopStoodDownIsIntendedOK(t *testing.T) {
	now := fixedNow()
	r := Assemble(healthyDisabled(now), now)
	require.Equal(t, string(Warn), r.Verdict, "the disabled companion is the warn, not the backstop")

	c, ok := checkFor(r, "periodic."+SlugBellFallback)
	require.True(t, ok)
	require.Equal(t, Ok, c.State)
	require.Contains(t, c.Detail, "stood down")

	p, ok := periodicFor(r, SlugBellFallback)
	require.True(t, ok)
	require.Contains(t, p.Note, "stood down")
}

// ── companion periodics ──────────────────────────────────────────────────────

// companionReport builds the classifier's input the way Assemble does: a DBReport
// over the healthy companion store, ready for a case to park one row.
func companionReport() DBReport {
	in := okCompanionDB()
	return DBReport{Path: in.Path, State: Ok, Periodics: in.Periodics}
}

// parkCompanion switches one companion periodic off in a DBReport, the state a
// transient outage leaves behind.
func parkCompanion(rep *DBReport, slug string) {
	for i := range rep.Periodics {
		if rep.Periodics[i].Slug == slug {
			rep.Periodics[i].Active = false
			return
		}
	}
}

// TestClassifyCompanionPeriodics_HealthyStoreIsSilent: three registered, active
// periodics with no frozen cursor produce no checks at all. A classifier that
// spoke on a healthy store would make every real fault harder to see.
func TestClassifyCompanionPeriodics_HealthyStoreIsSilent(t *testing.T) {
	rep := companionReport()
	require.Empty(t, classifyCompanionPeriodics(&rep, fixedNow()))
	require.Len(t, rep.Periodics, 3, "and no placeholder row is invented")
}

// TestClassifyCompanionPeriodics_InactiveNamesReconcile: a companion send
// switched off in its own store is the silent failure repair exists for, so it is
// an error that names the exact command that fixes it — the half the report used
// to report while deliberately naming no remedy.
func TestClassifyCompanionPeriodics_InactiveNamesReconcile(t *testing.T) {
	rep := companionReport()
	parkCompanion(&rep, SlugCompanionNight)

	checks := classifyCompanionPeriodics(&rep, fixedNow())
	require.Len(t, checks, 1)
	require.Equal(t, "periodic."+SlugCompanionNight, checks[0].Name)
	require.Equal(t, Error, checks[0].State)
	require.Contains(t, checks[0].Detail, "is inactive")
	require.Contains(t, checks[0].Detail, "run: lucid scheduler reconcile --slug "+SlugCompanionNight)
}

// TestClassifyCompanionPeriodics_StuckCursorNamesReconcile covers the other
// parked shape — active, but with a cursor a full day behind and nothing
// advancing it. This is precisely the half a daily upsert never repaired, so
// without a fault naming `reconcile` the repair lever would have nothing pointing
// at it.
func TestClassifyCompanionPeriodics_StuckCursorNamesReconcile(t *testing.T) {
	now := fixedNow()
	rep := companionReport()
	rep.Periodics[0].NextRun = now.Add(-48 * time.Hour)

	checks := classifyCompanionPeriodics(&rep, now)
	require.Len(t, checks, 1)
	require.Equal(t, "periodic."+SlugCompanionMorning, checks[0].Name)
	require.Equal(t, Error, checks[0].State)
	require.Contains(t, checks[0].Detail, "stuck at")
	require.Contains(t, checks[0].Detail, "run: lucid scheduler reconcile --slug "+SlugCompanionMorning)

	// A cursor merely due — the normal state between a mark and the next tick —
	// is not stuck, so the pass stays silent on a running daemon.
	due := companionReport()
	due.Periodics[0].NextRun = now.Add(-1 * time.Hour)
	require.Empty(t, classifyCompanionPeriodics(&due, now))
}

// TestClassifyCompanionPeriodics_AbsentBackstopIsReported: the morning backstop is
// classified like its two siblings, and a slug the store has no row for cannot be
// re-armed — there is nothing to re-arm — so the fault points at the one thing
// that seeds it, a daemon start. The absent slug still renders as a row.
func TestClassifyCompanionPeriodics_AbsentBackstopIsReported(t *testing.T) {
	rep := companionReport()
	rep.Periodics = rep.Periodics[:2] // drop the backstop row entirely

	checks := classifyCompanionPeriodics(&rep, fixedNow())
	require.Len(t, checks, 1)
	require.Equal(t, "periodic."+SlugCompanionMorningBackstop, checks[0].Name)
	require.Equal(t, Error, checks[0].State)
	require.Contains(t, checks[0].Detail, "not registered")
	require.Contains(t, checks[0].Detail, "run: lucid scheduler run")

	require.Len(t, rep.Periodics, 3, "the absent slug is appended as a visible placeholder")
	require.Equal(t, SlugCompanionMorningBackstop, rep.Periodics[2].Slug)
	require.False(t, rep.Periodics[2].Present)
}

// TestAssemble_CompanionStuckCursorLowersTheVerdict is the same fault end to end:
// a frozen companion cursor reaches the rolled-up verdict and the exit code a
// health cron gates on, not just the classifier that found it.
func TestAssemble_CompanionStuckCursorLowersTheVerdict(t *testing.T) {
	now := fixedNow()
	in := healthyEnabled(now)
	in.CompanionJobs.Periodics[2].NextRun = now.Add(-36 * time.Hour) // the backstop

	r := Assemble(in, now)
	require.Equal(t, string(Error), r.Verdict)
	require.Equal(t, 2, r.ExitCode())

	c, ok := checkFor(r, "periodic."+SlugCompanionMorningBackstop)
	require.True(t, ok)
	require.Equal(t, Error, c.State)
	require.Contains(t, c.Detail, "run: lucid scheduler reconcile --slug "+SlugCompanionMorningBackstop)

	require.Contains(t, strings.Join(r.TextLines(), "\n"), "lucid scheduler reconcile",
		"the remedy reaches the rendered report, not only the JSON check list")
}

// TestAbsentEnginePeriodicPointsAtTheDaemon: a slug the store has no row for
// cannot be re-armed — there is nothing to re-arm — so the fault points at the
// one thing that seeds it, a daemon start. An absent *intended-inactive* periodic
// stays a non-fault with its reason.
func TestAbsentEnginePeriodicPointsAtTheDaemon(t *testing.T) {
	now := fixedNow()
	in := healthyEnabled(now)
	in.Engine.Periodics = in.Engine.Periodics[:2] // drop the tripwire row entirely

	r := Assemble(in, now)
	require.Equal(t, string(Error), r.Verdict)
	c, ok := checkFor(r, "periodic."+SlugTripwire)
	require.True(t, ok)
	require.Equal(t, Error, c.State)
	require.Contains(t, c.Detail, "not registered")
	require.Contains(t, c.Detail, "run: lucid scheduler run")

	// The absent slug still renders, so it is visible rather than simply gone.
	p, ok := periodicFor(r, SlugTripwire)
	require.True(t, ok)
	require.False(t, p.Present)
	require.Contains(t, strings.Join(r.TextLines(), "\n"), "MISSING")

	// An absent bell while the companion owns the evening is not a fault.
	noBell := healthyEnabled(now)
	noBell.Engine.Periodics = noBell.Engine.Periodics[1:]
	r = Assemble(noBell, now)
	require.Equal(t, string(Ok), r.Verdict)
	c, ok = checkFor(r, "periodic."+SlugBell)
	require.True(t, ok)
	require.Equal(t, Ok, c.State)
	require.Contains(t, c.Detail, "(intended)")
}

// TestMissedEveningWindowStaysLegible: the evening window's own miss check is
// untouched by the backstop work. At 20:00 the night window has elapsed, so a
// night receipt still stamped with yesterday is a real miss — reported as an
// error naming the window and the date it last delivered.
func TestMissedEveningWindowStaysLegible(t *testing.T) {
	evening := time.Date(2026, 7, 16, 20, 0, 0, 0, time.UTC)
	in := healthyEnabled(evening)

	r := Assemble(in, evening)
	require.Equal(t, string(Error), r.Verdict)
	c, ok := checkFor(r, "companion.receipt.night")
	require.True(t, ok, "the elapsed evening window is classified")
	require.Equal(t, Error, c.State)
	require.Contains(t, c.Detail, "night")
	require.Contains(t, c.Detail, "stale receipt")

	// A delivered evening clears it, and the suppressed bell still reads as intended.
	delivered := healthyEnabled(evening)
	delivered.Receipts[1].Date = evening.Format(dateLayout)
	r = Assemble(delivered, evening)
	require.Equal(t, string(Ok), r.Verdict)
	c, ok = checkFor(r, "periodic."+SlugBell)
	require.True(t, ok)
	require.Equal(t, Ok, c.State)
}

// TestAssembleNeverRun is the never-initialized scaffold: no job DBs, no
// receipts. It must not panic and must classify the missing engine DB as an error.
func TestAssembleNeverRun(t *testing.T) {
	now := fixedNow()
	in := Inputs{
		Companion: CompanionInfo{Enabled: false},
		Chain:     baseChain(),
		Engine:    DBInput{Path: "/var/lucid/flywheel.db", Missing: true},
		CompanionJobs: DBInput{
			Path:    "/var/lucid/companion.db",
			Missing: true,
		},
		Receipts: []ReceiptStatus{{Window: "morning", Present: false}, {Window: "night", Present: false}},
	}
	require.NotPanics(t, func() {
		r := Assemble(in, now)
		require.Equal(t, string(Error), r.Verdict)
		require.Equal(t, 2, r.ExitCode())
	})
}

// TestAssembleJobStoresAreInformational locks the one property that keeps the
// job-store block safe to add: it is copied through untouched and classified
// nowhere. Adding, blanking, or dropping the paths must move neither the verdict
// nor the check list — the block exists to be read, not to be judged.
func TestAssembleJobStoresAreInformational(t *testing.T) {
	now := fixedNow()

	bare := Assemble(healthyEnabled(now), now)
	require.Empty(t, bare.JobStores, "an ungathered report simply has no job stores")

	with := healthyEnabled(now)
	with.JobStores = okJobStores()
	full := Assemble(with, now)

	require.Equal(t, okJobStores(), full.JobStores, "copied through verbatim")
	require.Equal(t, bare.Verdict, full.Verdict)
	require.Equal(t, bare.Checks, full.Checks, "no check is derived from a job-store path")

	// Even a wholly unresolvable set is inert: a blank path is a rendering
	// concern, never a fault.
	blank := healthyEnabled(now)
	blank.JobStores = []JobStorePath{{Name: "workout", Env: "LUCID_WORKOUT_DB"}}
	require.Equal(t, bare.Verdict, Assemble(blank, now).Verdict)
	require.Equal(t, bare.Checks, Assemble(blank, now).Checks)
}

// ── compose fallbacks ────────────────────────────────────────────────────────

// fallbackAt builds one synthetic compose-fallback entry finalized at the given
// instant, in the shape the gatherer hands Assemble: the instant itself for
// ordering and the text render, and the same instant as an RFC3339 UTC string
// for the machine contract.
func fallbackAt(store, jobKind, kind, reason string, at time.Time) ComposeFallback {
	return ComposeFallback{
		Kind:       kind,
		JobKind:    jobKind,
		Store:      store,
		FinishedAt: at.UTC().Format(time.RFC3339),
		Reason:     reason,
		At:         at,
	}
}

// sampleFallbacks is a small in-window scan result — three fallbacks, one of them
// carrying a provider error — plus one unreadable-store note: enough for a case
// to prove the section is carried while nothing else moves.
func sampleFallbacks(now time.Time) FallbackInput {
	morning := fallbackAt("companion", "lucid_companion_morning", "companion-morning", "provider_outage", now.Add(-1*time.Hour))
	morning.Error = "synthetic provider timeout"
	return FallbackInput{
		Entries: []ComposeFallback{
			morning,
			fallbackAt("companion", "lucid_companion_night", "companion-night", "unusable_reply", now.Add(-17*time.Hour)),
			fallbackAt("workout", "lucid_workout_daily", "workout", "safety_tripped", now.Add(-50*time.Hour)),
		},
		Notes: []string{
			"witness-report job store at /tmp/synthetic/witness-report.db is unreadable — compose fallbacks not scanned (file is not a database)",
		},
	}
}

// fallbackKey renders an entry as "<finished> <store> <job kind>", so an ordering
// assertion reads as the sequence it expects.
func fallbackKey(e ComposeFallback) string {
	return e.FinishedAt + " " + e.Store + " " + e.JobKind
}

// TestAssembleComposeFallbacks_WindowCapNewestFirst locks the section's bounds:
// only runs finalized in the last seven days, at most ten across every store,
// newest first — and a tie on the finalize time broken by store, then job kind,
// so two runs of the same report never trade places.
func TestAssembleComposeFallbacks_WindowCapNewestFirst(t *testing.T) {
	now := fixedNow()
	day := 24 * time.Hour
	tie := now.Add(-3 * time.Hour)

	// Twelve in-window entries across the three stores, interleaved so nothing
	// leans on input order, plus one a minute outside the window.
	in := healthyEnabled(now)
	in.ComposeFallbacks.Entries = []ComposeFallback{
		fallbackAt("companion", "lucid_companion_night", "companion-night", "unusable_reply", now.Add(-6*day)),
		fallbackAt("workout", "lucid_workout_daily", "workout", "provider_outage", tie),
		fallbackAt("witness-report", "lucid_witness_report_weekly", "witness", "safety_tripped", now.Add(-4*day)),
		fallbackAt("workout", "lucid_workout_daily", "workout", "unusable_reply", now.Add(-7*day-time.Minute)),
		fallbackAt("companion", "lucid_companion_morning", "companion-morning", "provider_outage", now.Add(-1*time.Hour)),
		fallbackAt("companion", "lucid_companion_night", "companion-night", "unusable_reply", tie),
		fallbackAt("workout", "lucid_workout_daily", "workout", "unusable_reply", now.Add(-5*day)),
		fallbackAt("companion", "lucid_companion_morning", "companion-morning", "provider_outage", now.Add(-7*day+time.Minute)),
		fallbackAt("companion", "lucid_companion_morning_backstop", "companion-morning", "provider_outage", tie),
		fallbackAt("companion", "lucid_companion_night", "companion-night", "unusable_reply", now.Add(-2*day)),
		fallbackAt("witness-report", "lucid_witness_report_weekly", "witness", "safety_tripped", now.Add(-2*time.Hour)),
		fallbackAt("companion", "lucid_companion_morning", "companion-morning", "provider_outage", now.Add(-3*day)),
		fallbackAt("workout", "lucid_workout_daily", "workout", "provider_outage", now.Add(-1*day)),
	}
	before := slices.Clone(in.ComposeFallbacks.Entries)

	cf := Assemble(in, now).ComposeFallbacks
	require.Equal(t, "168h", cf.Window)
	require.Equal(t, 10, cf.Count)
	require.Len(t, cf.Entries, 10)

	keys := make([]string, 0, len(cf.Entries))
	for _, e := range cf.Entries {
		keys = append(keys, fallbackKey(e))
	}
	require.Equal(t, []string{
		"2026-07-16T11:00:00Z companion lucid_companion_morning",
		"2026-07-16T10:00:00Z witness-report lucid_witness_report_weekly",
		"2026-07-16T09:00:00Z companion lucid_companion_morning_backstop",
		"2026-07-16T09:00:00Z companion lucid_companion_night",
		"2026-07-16T09:00:00Z workout lucid_workout_daily",
		"2026-07-15T12:00:00Z workout lucid_workout_daily",
		"2026-07-14T12:00:00Z companion lucid_companion_night",
		"2026-07-13T12:00:00Z companion lucid_companion_morning",
		"2026-07-12T12:00:00Z witness-report lucid_witness_report_weekly",
		"2026-07-11T12:00:00Z workout lucid_workout_daily",
	}, keys, "newest first, ties by store then job kind, capped at ten")
	require.Equal(t, before, in.ComposeFallbacks.Entries, "the gathered input is never reordered in place")

	// The window holds on its own, independent of the cap: a run a minute
	// outside it is dropped, while one exactly at the edge and one a minute
	// inside it are kept.
	edge := healthyEnabled(now)
	edge.ComposeFallbacks.Entries = []ComposeFallback{
		fallbackAt("workout", "lucid_workout_daily", "workout", "provider_outage", now.Add(-7*day-time.Minute)),
		fallbackAt("companion", "lucid_companion_night", "companion-night", "unusable_reply", now.Add(-7*day)),
		fallbackAt("witness-report", "lucid_witness_report_weekly", "witness", "safety_tripped", now.Add(-7*day+time.Minute)),
	}
	cf = Assemble(edge, now).ComposeFallbacks
	require.Equal(t, 2, cf.Count)
	require.Equal(t, "2026-07-09T12:01:00Z witness-report lucid_witness_report_weekly", fallbackKey(cf.Entries[0]))
	require.Equal(t, "2026-07-09T12:00:00Z companion lucid_companion_night", fallbackKey(cf.Entries[1]))
}

// TestAssembleComposeFallbacks_NeverChangesVerdict is the property that makes the
// section safe to add: fallbacks are reported, never classified. For every
// scenario the verdict matrix proves — ok, warn, and error alike — the same
// inputs with three fallbacks and an unreadable-store note roll up to exactly the
// same verdict, checks, exit code, and run summary. A health cron or a
// post-deploy check may gate on that exit code alone.
func TestAssembleComposeFallbacks_NeverChangesVerdict(t *testing.T) {
	now := fixedNow()
	seen := make(map[CheckState]bool)

	for _, tc := range verdictMatrixCases(now) {
		seen[tc.verdict] = true
		t.Run(tc.name, func(t *testing.T) {
			without := Assemble(tc.build(), now)

			in := tc.build()
			in.ComposeFallbacks = sampleFallbacks(now)
			with := Assemble(in, now)

			require.Equal(t, 3, with.ComposeFallbacks.Count, "the fallbacks are carried, so the comparison is not vacuous")
			require.Len(t, with.ComposeFallbacks.Notes, 1)

			require.Equal(t, string(tc.verdict), with.Verdict)
			require.Equal(t, without.Verdict, with.Verdict)
			require.Equal(t, without.Checks, with.Checks, "no check is derived from a fallback or a note")
			require.Equal(t, without.ExitCode(), with.ExitCode())
			require.Equal(t, without.Runs, with.Runs, "a fallback run is a success, never a failure")
			require.Equal(t, issueLines(without), issueLines(with))
		})
	}

	require.True(t, seen[Ok], "the matrix covers an ok verdict")
	require.True(t, seen[Warn], "the matrix covers a warn verdict")
	require.True(t, seen[Error], "the matrix covers an error verdict")
}

// TestAssembleComposeFallbacks_EmptyShape locks the empty section's contract: the
// window is still named, the count is zero, and entries is an empty array rather
// than null — so a consumer never has to special-case a missing list — while
// notes is omitted entirely.
func TestAssembleComposeFallbacks_EmptyShape(t *testing.T) {
	now := fixedNow()
	r := Assemble(healthyEnabled(now), now) // a zero FallbackInput

	cf := r.ComposeFallbacks
	require.Equal(t, "168h", cf.Window)
	require.Zero(t, cf.Count)
	require.NotNil(t, cf.Entries, "entries is never nil")
	require.Empty(t, cf.Entries)
	require.Nil(t, cf.Notes)

	raw, err := json.Marshal(r)
	require.NoError(t, err)
	var doc map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &doc))
	require.JSONEq(t, `{"window":"168h","count":0,"entries":[]}`, string(doc["compose_fallbacks"]))
	require.Contains(t, string(doc["compose_fallbacks"]), `"entries":[]`)
	require.NotContains(t, string(doc["compose_fallbacks"]), `"notes"`)

	// An empty-but-non-nil notes list is omitted just the same.
	in := healthyEnabled(now)
	in.ComposeFallbacks.Notes = []string{}
	raw, err = json.Marshal(Assemble(in, now))
	require.NoError(t, err)
	require.NotContains(t, string(raw), `"notes"`)
}
