package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	flywheel "github.com/mrz1836/go-flywheel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/schedstatus"
	"github.com/mrz1836/lucid/internal/storage"
)

// schedStatusJSON mirrors the documented `lucid scheduler status --json` shape:
// the top-level verdict a health cron gates on, plus enough of the sections and
// per-check lists to assert the document is structurally complete.
type schedStatusJSON struct {
	Verdict   string `json:"verdict"`
	Companion struct {
		Enabled         bool   `json:"enabled"`
		ProviderBackend string `json:"provider_backend"`
	} `json:"companion"`
	Chain struct {
		BellTime     string `json:"bell_time"`
		TripwireTime string `json:"tripwire_time"`
	} `json:"chain"`
	Teeth struct {
		State string `json:"state"`
	} `json:"teeth"`
	Receipts []struct {
		Window   string `json:"window"`
		Present  bool   `json:"present"`
		Verified bool   `json:"verified"`
	} `json:"receipts"`
	Host []struct {
		Name  string `json:"name"`
		State string `json:"state"`
	} `json:"host"`
	Checks []struct {
		Name  string `json:"name"`
		State string `json:"state"`
	} `json:"checks"`
}

// statusMorning is the pinned instant every health test runs at: 07:00 UTC, when
// the default morning tripwire (06:00) has already elapsed today and the night
// bell (19:00) last fired yesterday. That makes the morning window the most
// recent elapsed one, so a 2026-07-06 morning receipt satisfies the miss check.
func statusMorning() time.Time { return time.Date(2026, 7, 6, 7, 0, 0, 0, time.UTC) }

// fakeHostProbe is the injected [schedstatus.HostProbe] the command tests use in
// place of the real platform probe, so `scheduler status` behaves identically on
// every OS the suite runs on and a host problem can be simulated deterministically.
type fakeHostProbe struct{ checks []schedstatus.Check }

func (f fakeHostProbe) Probe() []schedstatus.Check { return f.checks }

// withHostProbe installs a fake host probe returning checks for the duration of a
// test, restoring the real seam afterward.
func withHostProbe(t *testing.T, checks ...schedstatus.Check) {
	t.Helper()
	prev := newHostProbe
	newHostProbe = func(string) schedstatus.HostProbe { return fakeHostProbe{checks: checks} }
	t.Cleanup(func() { newHostProbe = prev })
}

// unknownHost is the honest "cannot inspect this platform" host result — every
// check Unknown. Assemble must never let it lower an otherwise-ok verdict.
func unknownHost() []schedstatus.Check {
	return []schedstatus.Check{
		{Name: "host.daemon", State: schedstatus.Unknown, Detail: "not inspectable under test"},
		{Name: "host.supervisor", State: schedstatus.Unknown, Detail: "not inspectable under test"},
	}
}

// seedScheduler points LUCID_HOME at a fresh scaffolded Ledger, writes a
// lucid.json whose companion block is enabled/disabled per the flag with three
// real prompt files (bodies set to promptBody so a redaction test can plant a
// sentinel), points the two disposable job-DB env overrides at temp paths so the
// command never resolves the real OS user-config dir, and returns the home plus
// those paths for the caller to seed.
func seedScheduler(t *testing.T, companionEnabled bool, promptBody string) (home, schedulerDB, companionDB string) {
	t.Helper()
	home = isolatedHome(t)
	store := storage.New(home)
	_, err := store.Scaffold()
	require.NoError(t, err)
	require.NoError(t, store.ScaffoldEngine())

	dir := t.TempDir()
	sys := filepath.Join(dir, "system_prompt.md")
	morning := filepath.Join(dir, "morning_template.md")
	night := filepath.Join(dir, "night_template.md")
	for _, f := range []string{sys, morning, night} {
		require.NoError(t, os.WriteFile(f, []byte(promptBody), 0o600))
	}

	cfg := config.Default()
	cfg.Companion = config.CompanionConfig{
		Enabled:         companionEnabled,
		SystemPrompt:    sys,
		MorningTemplate: morning,
		NightTemplate:   night,
	}
	b, err := cfg.Marshal()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(store.ConfigPath(), b, 0o600))

	schedulerDB = filepath.Join(t.TempDir(), "flywheel.db")
	companionDB = filepath.Join(t.TempDir(), "companion.db")
	t.Setenv("LUCID_SCHEDULER_DB", schedulerDB)
	t.Setenv("LUCID_COMPANION_DB", companionDB)
	return home, schedulerDB, companionDB
}

// seedStatusJobDB migrates a flywheel job DB at path and upserts specs, so the
// command's read-only gatherer has real periodics to read back. An intended-
// inactive periodic is deactivated explicitly (a fresh insert defaults active),
// mirroring how the daemon suppresses the bell.
func seedStatusJobDB(t *testing.T, path string, specs ...flywheel.PeriodicSpec) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	require.NoError(t, flywheel.Migrate(db))
	for _, s := range specs {
		require.NoError(t, flywheel.UpsertPeriodic(context.Background(), db, s))
		if !s.Active {
			require.NoError(t, flywheel.SetPeriodicActive(context.Background(), db, s.Slug, false))
		}
	}
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
}

// seedHealthyDBs seeds the teeth and companion job DBs and both delivery receipts
// for a fully-healthy companion at statusMorning: tripwire active + bell
// suppressed (the companion owns the night send), morning + night companion
// periodics active, and verified receipts whose morning date matches the most
// recent elapsed window.
func seedHealthyDBs(t *testing.T, home, schedulerDB, companionDB string) {
	t.Helper()
	seedStatusJobDB(
		t, schedulerDB,
		flywheel.PeriodicSpec{Slug: schedstatus.SlugTripwire, Kind: "lucid_tripwire", Cron: "0 6 * * *", Queue: "lucid", Active: true},
		flywheel.PeriodicSpec{Slug: schedstatus.SlugBell, Kind: "lucid_bell", Cron: "0 19 * * *", Queue: "lucid", Active: false},
	)
	seedStatusJobDB(
		t, companionDB,
		flywheel.PeriodicSpec{Slug: schedstatus.SlugCompanionMorning, Kind: "lucid_companion_morning", Cron: "0 6 * * *", Queue: "lucid-companion", Active: true},
		flywheel.PeriodicSpec{Slug: schedstatus.SlugCompanionNight, Kind: "lucid_companion_night", Cron: "0 19 * * *", Queue: "lucid-companion", Active: true},
	)
	store := storage.New(home)
	require.NoError(t, store.WriteCompanionReceipt(storage.CompanionReceipt{
		Date: "2026-07-06", Window: "morning", MessageID: "m-1", ChannelID: "c", Verified: true, DeliveredAt: "2026-07-06T06:00:04Z",
	}))
	require.NoError(t, store.WriteCompanionReceipt(storage.CompanionReceipt{
		Date: "2026-07-05", Window: "night", MessageID: "n-1", ChannelID: "c", Verified: true, DeliveredAt: "2026-07-05T19:00:04Z",
	}))
}

// TestSchedulerStatus_TreeExposesStatus proves the `status` child is registered
// under the `scheduler` parent and carries the two DB-override flags (AC-1).
func TestSchedulerStatus_TreeExposesStatus(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})

	statusCmd, _, err := root.Find([]string{"scheduler", "status"})
	require.NoError(t, err)
	assert.Equal(t, "status", statusCmd.Name())
	assert.NotNil(t, statusCmd.Flags().Lookup(statusFlagSchedulerDB), "status exposes --scheduler-db")
	assert.NotNil(t, statusCmd.Flags().Lookup(statusFlagCompanionDB), "status exposes --companion-db")
}

// TestSchedulerStatus_RejectsArgs: `scheduler status` is a no-args verb — a stray
// positional is a usage error, matching the rest of the spine.
func TestSchedulerStatus_RejectsArgs(t *testing.T) {
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "status", "extra")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
}

// TestSchedulerStatus_Healthy_ExitOK: a fully-healthy companion exits 0 with a
// nil error, renders every labeled section, and — critically — an all-Unknown
// host probe never lowers the ok verdict (AC-4, AC-5, AC-9).
func TestSchedulerStatus_Healthy_ExitOK(t *testing.T) {
	home, schedulerDB, companionDB := seedScheduler(t, true, "PROMPT BODY")
	seedHealthyDBs(t, home, schedulerDB, companionDB)
	withClock(t, statusMorning())
	withHostProbe(t, unknownHost()...)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "status")
	require.NoError(t, err, "a healthy scheduler returns no error (exit 0)")
	assert.Equal(t, ExitOK, exitCodeForError(err))

	// Verdict + every required section label is present.
	assert.Contains(t, out, "Scheduler status: OK")
	assert.Contains(t, out, "Companion: enabled")
	assert.Contains(t, out, "Provider:")
	assert.Contains(t, out, "Chain: bell 19:00, tripwire 06:00")
	assert.Contains(t, out, "Teeth periodics")
	assert.Contains(t, out, "Companion periodics")
	assert.Contains(t, out, "Receipts:")
	assert.Contains(t, out, "Recent runs:")
	assert.Contains(t, out, "Host:")
	// An Unknown host is shown but does not flip the verdict.
	assert.Contains(t, out, "daemon: unknown")
}

// TestSchedulerStatus_CompanionDisabled_Warn: a disabled companion with healthy
// teeth is a warn (configured but delivery is off) — exit 1, not a hard error
// (AC-4).
func TestSchedulerStatus_CompanionDisabled_Warn(t *testing.T) {
	_, schedulerDB, _ := seedScheduler(t, false, "PROMPT BODY")
	// Companion disabled → the bell is required active; seed both teeth active.
	seedStatusJobDB(
		t, schedulerDB,
		flywheel.PeriodicSpec{Slug: schedstatus.SlugTripwire, Kind: "lucid_tripwire", Cron: "0 6 * * *", Queue: "lucid", Active: true},
		flywheel.PeriodicSpec{Slug: schedstatus.SlugBell, Kind: "lucid_bell", Cron: "0 19 * * *", Queue: "lucid", Active: true},
	)
	withClock(t, statusMorning())
	withHostProbe(t, unknownHost()...)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "status")
	require.Error(t, err, "a warn verdict returns a non-nil ExitCoder error")
	assert.Equal(t, 1, exitCodeForError(err))
	assert.Contains(t, out, "Scheduler status: WARN")
	assert.Contains(t, out, "companion is disabled")
}

// TestSchedulerStatus_NeverRun_Error_NoPanic: a scaffolded-but-never-run
// scheduler (no job DBs, no receipts) does not panic, prints clear "not
// initialized / no receipt yet" output, and classifies the missing teeth DB as a
// hard error (exit 2) (AC-6).
func TestSchedulerStatus_NeverRun_Error_NoPanic(t *testing.T) {
	isolatedHome(t)
	// Point the DB overrides at nonexistent temp paths so the never-run command
	// never resolves the real OS user-config dir.
	t.Setenv("LUCID_SCHEDULER_DB", filepath.Join(t.TempDir(), "never.db"))
	t.Setenv("LUCID_COMPANION_DB", filepath.Join(t.TempDir(), "never.db"))
	withClock(t, statusMorning())
	withHostProbe(t, unknownHost()...)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "status")
	require.Error(t, err)
	assert.Equal(t, 2, exitCodeForError(err))
	assert.Contains(t, out, "not initialized")
	assert.Contains(t, out, "no receipt yet")
}

// TestSchedulerStatus_JSON_HasVerdictAndShape: `--json` decodes to the documented
// shape with a top-level verdict that mirrors the exit code, and the structured
// sections are all present (AC-3, AC-9).
func TestSchedulerStatus_JSON_HasVerdictAndShape(t *testing.T) {
	home, schedulerDB, companionDB := seedScheduler(t, true, "PROMPT BODY")
	seedHealthyDBs(t, home, schedulerDB, companionDB)
	withClock(t, statusMorning())
	withHostProbe(t, unknownHost()...)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "status", "--json")
	require.NoError(t, err)

	var v schedStatusJSON
	require.NoError(t, json.Unmarshal([]byte(out), &v))
	assert.Equal(t, "ok", v.Verdict)
	assert.Equal(t, ExitOK, exitCodeForError(err), "the JSON verdict mirrors the exit code")
	assert.True(t, v.Companion.Enabled)
	assert.NotEmpty(t, v.Companion.ProviderBackend)
	assert.Equal(t, "19:00", v.Chain.BellTime)
	assert.Equal(t, "06:00", v.Chain.TripwireTime)
	assert.Equal(t, "ok", v.Teeth.State)
	require.Len(t, v.Receipts, 2)
	require.NotEmpty(t, v.Host, "host checks are present in the JSON document")
	require.NotEmpty(t, v.Checks, "the flat check list is present in the JSON document")
}

// TestSchedulerStatus_ExitCodeIdenticalTextAndJSON proves the 3-tier exit code is
// identical in text and --json for the same state: a warn verdict exits 1 in both
// modes, and the JSON verdict is "warn" (AC-4).
func TestSchedulerStatus_ExitCodeIdenticalTextAndJSON(t *testing.T) {
	_, schedulerDB, _ := seedScheduler(t, false, "PROMPT BODY")
	seedStatusJobDB(
		t, schedulerDB,
		flywheel.PeriodicSpec{Slug: schedstatus.SlugTripwire, Kind: "lucid_tripwire", Cron: "0 6 * * *", Queue: "lucid", Active: true},
		flywheel.PeriodicSpec{Slug: schedstatus.SlugBell, Kind: "lucid_bell", Cron: "0 19 * * *", Queue: "lucid", Active: true},
	)
	withClock(t, statusMorning())
	withHostProbe(t, unknownHost()...)

	_, _, textErr := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "status")
	require.Error(t, textErr)

	jsonOut, _, jsonErr := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "status", "--json")
	require.Error(t, jsonErr)

	assert.Equal(t, exitCodeForError(textErr), exitCodeForError(jsonErr), "text and --json exit codes match")
	assert.Equal(t, 1, exitCodeForError(jsonErr))

	var v schedStatusJSON
	require.NoError(t, json.Unmarshal([]byte(jsonOut), &v))
	assert.Equal(t, "warn", v.Verdict)
}

// TestSchedulerStatus_HostDown_LowersToError: a positively-detected host problem
// (daemon down) lowers an otherwise-healthy verdict to error — exit 2 — proving
// host checks are folded into the verdict, not just reported (AC-5, AC-9).
func TestSchedulerStatus_HostDown_LowersToError(t *testing.T) {
	home, schedulerDB, companionDB := seedScheduler(t, true, "PROMPT BODY")
	seedHealthyDBs(t, home, schedulerDB, companionDB)
	withClock(t, statusMorning())
	withHostProbe(t, schedstatus.Check{
		Name: "host.daemon", State: schedstatus.Error, Detail: "the scheduler daemon appears to be down",
	})

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "status")
	require.Error(t, err)
	assert.Equal(t, 2, exitCodeForError(err))
	assert.Contains(t, out, "Scheduler status: ERROR")
	assert.Contains(t, out, "daemon appears to be down")
}

// TestSchedulerStatus_NeverLeaksSecrets_Redaction: the command reports prompt
// paths and metadata only — never the injected bot token nor a prompt file body —
// in either the human or --json output (AC-7). A distinctive token is injected in
// the environment and a distinctive body is planted in every prompt file; neither
// may appear.
func TestSchedulerStatus_NeverLeaksSecrets_Redaction(t *testing.T) {
	const tokenSentinel = "SECRET_BOT_TOKEN_REDACTION_SENTINEL"
	const promptSentinel = "PRIVATE_PROMPT_BODY_REDACTION_SENTINEL"

	home, schedulerDB, companionDB := seedScheduler(t, true, promptSentinel)
	seedHealthyDBs(t, home, schedulerDB, companionDB)
	t.Setenv("LUCID_HARNESS_TOKEN", tokenSentinel)
	withClock(t, statusMorning())
	withHostProbe(t, unknownHost()...)

	human, _, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "status")
	require.NoError(t, err)
	assert.NotEmpty(t, human)
	assert.NotContains(t, human, tokenSentinel, "the injected bot token never appears in text output")
	assert.NotContains(t, human, promptSentinel, "a prompt file body is never read or printed")

	jsonOut, _, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "status", "--json")
	require.NoError(t, err)
	assert.NotContains(t, jsonOut, tokenSentinel, "the injected bot token never appears in --json output")
	assert.NotContains(t, jsonOut, promptSentinel, "a prompt file body is never read or printed in --json")
}

// TestSchedulerStatus_RuntimeFailure_ExitErr: a genuine runtime failure (an
// unresolvable/unscaffoldable Ledger) is returned before any verdict is rendered
// and maps to the normal runtime exit (ExitErr, 1) — not swallowed by the
// ExitCoder verdict seam (AC-4).
func TestSchedulerStatus_RuntimeFailure_ExitErr(t *testing.T) {
	unscaffoldableHome(t) // bootedRouter cannot scaffold under a regular file
	withHostProbe(t, unknownHost()...)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "status")
	require.Error(t, err)
	assert.Equal(t, ExitErr, exitCodeForError(err), "a runtime failure maps to ExitErr(1), not a verdict code")
	assert.Empty(t, out, "a runtime failure renders no report")
}

// TestStatusExit_IsExitCoder documents the exit-code seam: a statusExitError carries
// its verdict's code out through the [ExitCoder] interface exitCodeForError
// honors, and its Error() names the verdict for a stray log line.
func TestStatusExit_IsExitCoder(t *testing.T) {
	e := statusExitError{verdict: "warn", code: 1}
	assert.Equal(t, 1, e.ExitCode())
	assert.Contains(t, e.Error(), "warn")

	var coder ExitCoder = e
	assert.Equal(t, 1, coder.ExitCode())
	assert.Equal(t, 1, exitCodeForError(e), "exitCodeForError honors the ExitCoder code")
}

// TestSchedulerStatus_DBFlagsOverrideEnv: the --scheduler-db / --companion-db
// flags win over the env overrides, so an operator can point the inspector at an
// explicit store. Env is aimed at nonexistent paths; the flags at the real seeded
// DBs — a healthy verdict proves the flags were honored.
func TestSchedulerStatus_DBFlagsOverrideEnv(t *testing.T) {
	home, schedulerDB, companionDB := seedScheduler(t, true, "PROMPT BODY")
	seedHealthyDBs(t, home, schedulerDB, companionDB)
	// Redirect the env overrides at nonexistent paths so only the flags can find
	// the real seeded DBs.
	t.Setenv("LUCID_SCHEDULER_DB", filepath.Join(t.TempDir(), "wrong.db"))
	t.Setenv("LUCID_COMPANION_DB", filepath.Join(t.TempDir(), "wrong.db"))
	withClock(t, statusMorning())
	withHostProbe(t, unknownHost()...)

	out, _, err := runRoot(
		t, BuildInfo{Version: "dev"},
		"scheduler", "status",
		"--scheduler-db", schedulerDB,
		"--companion-db", companionDB,
	)
	require.NoError(t, err, "the flags point at healthy DBs, so the verdict is ok")
	assert.Contains(t, out, "Scheduler status: OK")
}
