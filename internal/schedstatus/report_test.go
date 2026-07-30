package schedstatus

import (
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

func okCompanionDB() DBInput {
	return DBInput{
		Path: "/var/lucid/companion.db",
		Periodics: []PeriodicStatus{
			{Slug: SlugCompanionMorning, Cron: "0 6 * * *", Active: true, Present: true},
			{Slug: SlugCompanionNight, Cron: "0 19 * * *", Active: true, Present: true},
		},
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

func TestAssembleVerdictMatrix(t *testing.T) {
	now := fixedNow()

	cases := []struct {
		name    string
		build   func() Inputs
		verdict CheckState
	}{
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

	for _, tc := range cases {
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
