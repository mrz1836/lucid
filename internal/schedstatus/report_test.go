package schedstatus

import (
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

// okTeeth builds a healthy teeth job DB. The bell is active only when the
// companion is disabled; when the companion is enabled the bell is deliberately
// suppressed (inactive), which is the correct state and must not fault.
func okTeeth(companionEnabled bool, now time.Time) DBInput {
	return DBInput{
		Path: "/var/lucid/flywheel.db",
		Periodics: []PeriodicStatus{
			{Slug: SlugBell, Cron: "0 19 * * *", Active: !companionEnabled, Present: true, NextRun: now.Add(6 * time.Hour)},
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
		Teeth:         okTeeth(true, now),
		CompanionJobs: okCompanionDB(),
		Receipts:      okReceipts(now),
		Host:          []Check{{Name: "host.daemon", State: Ok, Detail: "running"}},
	}
}

// healthyDisabled is a healthy teeth-only system: the companion is off (a warn,
// not an error), the bell and tripwire both deliver to the user and are active,
// and the companion job DB legitimately does not exist.
func healthyDisabled(now time.Time) Inputs {
	return Inputs{
		Companion:     CompanionInfo{Enabled: false, ProviderBackend: "claude_cli", ProviderModel: "opus"},
		Chain:         baseChain(),
		Teeth:         okTeeth(false, now),
		CompanionJobs: DBInput{Path: "/var/lucid/companion.db", Missing: true},
		Host:          []Check{{Name: "host.daemon", State: Ok, Detail: "running"}},
	}
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
			name: "missing teeth DB is an error",
			build: func() Inputs {
				in := healthyEnabled(now)
				in.Teeth = DBInput{Path: "/var/lucid/flywheel.db", Missing: true}
				return in
			},
			verdict: Error,
		},
		{
			name: "malformed teeth DB is an error",
			build: func() Inputs {
				in := healthyEnabled(now)
				in.Teeth = DBInput{Path: "/var/lucid/flywheel.db", Err: "file is not a database"}
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
				in.Teeth.Periodics[1].Active = false // tripwire off
				return in
			},
			verdict: Error,
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
				in.Teeth.Periodics = in.Teeth.Periodics[1:] // drop the suppressed bell entirely
				return in
			},
			verdict: Ok,
		},
		{
			name: "inactive bell while companion disabled is an error",
			build: func() Inputs {
				in := healthyDisabled(now)
				in.Teeth.Periodics[0].Active = false // bell off, nothing delivers the evening
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
	in.Teeth.Failures = []RunFailure{{Kind: "lucid-tripwire", ErrorClass: "timeout", Message: "discord timeout"}}
	in.CompanionJobs.Failures = []RunFailure{{Kind: "lucid-companion-morning", ErrorClass: "timeout", Message: "discord timeout"}}

	r := Assemble(in, now)
	require.Equal(t, 2, r.Runs.FailureCount)
	require.Len(t, r.Runs.Failures, 2)
	require.Equal(t, string(Ok), r.Verdict, "recent failures alone must not lower the verdict")
}

// TestAssembleNeverRun is the never-initialized scaffold: no job DBs, no
// receipts. It must not panic and must classify the missing teeth DB as an error.
func TestAssembleNeverRun(t *testing.T) {
	now := fixedNow()
	in := Inputs{
		Companion: CompanionInfo{Enabled: false},
		Chain:     baseChain(),
		Teeth:     DBInput{Path: "/var/lucid/flywheel.db", Missing: true},
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
