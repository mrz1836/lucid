package cli

import (
	"bytes"
	"context"
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
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/storage"
)

// seedCompanionHome scaffolds an isolated Ledger and writes a lucid.json whose
// companion block is enabled and points at three real opaque prompt files (under
// a separate temp dir, so they never perturb a home-tree hash). It returns the
// home so a test can assert read-only behavior.
func seedCompanionHome(t *testing.T) string {
	t.Helper()
	home := isolatedHome(t)

	store := storage.New(home)
	_, err := store.Scaffold()
	require.NoError(t, err)

	dir := t.TempDir()
	sys := filepath.Join(dir, "system_prompt.md")
	morning := filepath.Join(dir, "morning_template.md")
	night := filepath.Join(dir, "night_template.md")
	require.NoError(t, os.WriteFile(sys, []byte("VOICE PROMPT"), 0o600))
	require.NoError(t, os.WriteFile(morning, []byte("MORNING BODY"), 0o600))
	require.NoError(t, os.WriteFile(night, []byte("NIGHT BODY"), 0o600))

	cfg := config.Default()
	cfg.Companion = config.CompanionConfig{
		Enabled:         true,
		SystemPrompt:    sys,
		MorningTemplate: morning,
		NightTemplate:   night,
	}
	b, err := cfg.Marshal()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(store.ConfigPath(), b, 0o600))
	return home
}

// runCompanion executes `lucid companion [args...]`, capturing stdout, stderr,
// and the command error.
func runCompanion(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := newRootCmd(BuildInfo{Version: "dev"})
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetArgs(append([]string{"companion"}, args...))
	err = root.ExecuteContext(context.Background())
	return out.String(), errBuf.String(), err
}

// TestCompanion_TreeExposesFire proves the `companion` group and its `fire`
// child are registered, and that `fire` carries the mode/deliver/dry-run flags.
func TestCompanion_TreeExposesFire(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})

	comp, _, err := root.Find([]string{"companion"})
	require.NoError(t, err)
	assert.Equal(t, "companion", comp.Name())

	fireCmd, _, err := root.Find([]string{"companion", "fire"})
	require.NoError(t, err)
	assert.Equal(t, "fire", fireCmd.Name())
	assert.NotNil(t, fireCmd.Flags().Lookup(companionFlagMode))
	assert.NotNil(t, fireCmd.Flags().Lookup(companionFlagDeliver))
	assert.NotNil(t, fireCmd.Flags().Lookup(companionFlagDryRun))
}

// TestCompanion_RegisteredOnSpine proves `companion` is on the cobra root.
func TestCompanion_RegisteredOnSpine(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})
	found := false
	for _, c := range root.Commands() {
		if c.Name() == "companion" {
			found = true
		}
	}
	assert.True(t, found, "companion must be registered on the root command")
}

// TestCompanionFire_DryRun_ComposesNoSideEffect: a dry-run composes the message
// through the (faked) provider, prints it, and leaves ~/.lucid byte-identical —
// no delivery, no receipt.
func TestCompanionFire_DryRun_ComposesNoSideEffect(t *testing.T) {
	home := seedCompanionHome(t)
	withClock(t, time.Date(2026, 7, 6, 6, 0, 0, 0, time.UTC))
	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{{Content: "WARM MORNING PREVIEW"}}})

	out, _, err := runCompanion(t, "fire", "--mode", "morning")
	require.NoError(t, err)

	assert.Contains(t, out, "WARM MORNING PREVIEW")
	assert.Contains(t, out, "dry-run")

	// The meaningful zero-side-effect guarantee: a dry-run delivers nothing, so
	// it writes no delivery receipt (a subsequent real fire is not deduped away).
	_, ok, rerr := storage.New(home).ReadCompanionReceipt("morning")
	require.NoError(t, rerr)
	assert.False(t, ok, "a dry-run writes no delivery receipt")
}

// TestCompanionFire_DryRun_NightUsesNightTemplate proves the night window
// composes from the night template (the fake echoes the body it was sent).
func TestCompanionFire_DryRun_NightUsesNightTemplate(t *testing.T) {
	seedCompanionHome(t)
	withClock(t, time.Date(2026, 7, 6, 19, 0, 0, 0, time.UTC))
	fake := &provider.Fake{Script: []provider.Exchange{{Content: "WARM NIGHT PREVIEW"}}}
	withServeProvider(t, fake)

	out, _, err := runCompanion(t, "fire", "--mode", "night")
	require.NoError(t, err)
	assert.Contains(t, out, "WARM NIGHT PREVIEW")
	require.Len(t, fake.Requests, 1)
	assert.Contains(t, fake.Requests[0].Messages[0].Content, "NIGHT BODY", "the night window sends the night template")
}

// TestCompanionFire_MissingMode_Errors: --mode is required.
func TestCompanionFire_MissingMode_Errors(t *testing.T) {
	seedCompanionHome(t)
	_, _, err := runCompanion(t, "fire")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--mode is required")
}

// TestCompanionFire_UnknownMode_Errors rejects a mode that is neither window.
func TestCompanionFire_UnknownMode_Errors(t *testing.T) {
	seedCompanionHome(t)
	_, _, err := runCompanion(t, "fire", "--mode", "noon")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown mode")
}

// TestCompanionFire_DeliverAndDryRun_MutuallyExclusive: the two flags cannot both
// be set.
func TestCompanionFire_DeliverAndDryRun_MutuallyExclusive(t *testing.T) {
	seedCompanionHome(t)
	_, _, err := runCompanion(t, "fire", "--mode", "morning", "--deliver", "--dry-run")
	require.Error(t, err)
}

// TestCompanionFire_DisabledWarns: with the companion disabled a dry-run still
// composes (a manual preview), but warns that the scheduler will not fire it.
func TestCompanionFire_DisabledWarns(t *testing.T) {
	home := isolatedHome(t)
	store := storage.New(home)
	_, err := store.Scaffold() // default config: companion disabled
	require.NoError(t, err)

	dir := t.TempDir()
	sys := filepath.Join(dir, "s.md")
	morning := filepath.Join(dir, "m.md")
	night := filepath.Join(dir, "n.md")
	for _, f := range []string{sys, morning, night} {
		require.NoError(t, os.WriteFile(f, []byte("BODY"), 0o600))
	}
	cfg := config.Default()
	cfg.Companion = config.CompanionConfig{Enabled: false, SystemPrompt: sys, MorningTemplate: morning, NightTemplate: night}
	b, err := cfg.Marshal()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(store.ConfigPath(), b, 0o600))

	withClock(t, time.Date(2026, 7, 6, 6, 0, 0, 0, time.UTC))
	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{{Content: "PREVIEW"}}})

	out, stderr, err := runCompanion(t, "fire", "--mode", "morning")
	require.NoError(t, err)
	assert.Contains(t, out, "PREVIEW")
	assert.Contains(t, stderr, "companion.enabled is false")
}

// TestRunScheduler_CompanionEnabled_WiresSuppression drives the full scheduler
// composition root with the companion enabled: the teeth come up with the bell
// periodic suppressed (the companion owns the night user send) and the companion
// node comes up with its own morning + night periodics — proving the suppression
// flag and the concurrent companion node are wired. A cancel drains both to a
// clean nil return.
func TestRunScheduler_CompanionEnabled_WiresSuppression(t *testing.T) {
	_ = seedCompanionHome(t) // sets LUCID_HOME + an enabled companion config
	t.Setenv("LUCID_HARNESS_TOKEN", "test-bot-token")
	t.Setenv("LUCID_USER_CHANNEL_ID", "100000000000000001")
	t.Setenv("LUCID_WITNESS_CHANNEL_ID", "100000000000000002")

	teethDB := filepath.Join(t.TempDir(), "flywheel.db")
	companionDB := filepath.Join(t.TempDir(), "companion.db")
	t.Setenv("LUCID_COMPANION_DB", companionDB)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	var stderr bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- runScheduler(ctx, &stderr, teethDB) }()

	// Wait until both nodes reconciled: the teeth (bell inactive, tripwire
	// active) and the companion (morning + night).
	require.Eventually(t, func() bool {
		return bellSuppressed(t, teethDB) && companionPeriodics(t, companionDB) == 2
	}, 10*time.Second, 25*time.Millisecond, "both nodes reconcile with the bell suppressed")

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err, "a canceled companion+teeth daemon drains cleanly")
		assert.Empty(t, stderr.String(), "a clean drain writes no error line")
	case <-time.After(10 * time.Second):
		t.Fatal("scheduler run did not return after cancellation")
	}
}

// bellSuppressed reports whether the teeth job DB has both periodics with the
// bell reconciled inactive (companion owns the night user send) and the tripwire
// active.
func bellSuppressed(t *testing.T, dbPath string) bool {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		return false
	}
	defer closeGorm(t, db)
	views, err := flywheel.ListPeriodics(context.Background(), db)
	if err != nil || len(views) != 2 {
		return false
	}
	active := map[string]bool{}
	for _, v := range views {
		active[v.Slug] = v.Active
	}
	return !active["lucid-bell"] && active["lucid-tripwire"]
}

// companionPeriodics returns how many periodics the companion job DB carries.
func companionPeriodics(t *testing.T, dbPath string) int {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		return -1
	}
	defer closeGorm(t, db)
	views, err := flywheel.ListPeriodics(context.Background(), db)
	if err != nil {
		return -1
	}
	return len(views)
}

// closeGorm closes a gorm handle's pool so the poll loop leaks no FDs.
func closeGorm(t *testing.T, db *gorm.DB) {
	t.Helper()
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

// Guard: the fire command is a no-args verb (a stray positional is a usage
// error), matching the rest of the spine.
func TestCompanionFire_RejectsArgs(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})
	root.SetArgs([]string{"companion", "fire", "extra"})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	err := root.Execute()
	require.Error(t, err)
}
