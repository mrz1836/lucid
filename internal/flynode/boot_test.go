package flynode

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	flywheel "github.com/mrz1836/go-flywheel"
	"github.com/mrz1836/go-foundation/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// fixedNow is an arbitrary anchor for the boot tests' injected clock. Boot never
// consults the clock during the boot spine — it only threads it onto ctx — so
// the exact instant is immaterial. It is a func (not a package var) to stay
// clear of gochecknoglobals.
func fixedNow() time.Time { return time.Date(2026, 7, 29, 21, 0, 0, 0, time.UTC) }

// fakeScaffold is a ScaffoldStore that records the ScaffoldEngine call and can
// be told to fail it, standing in for *storage.Adapter.
type fakeScaffold struct {
	scaffolded bool
	err        error
}

func (f *fakeScaffold) ScaffoldEngine() error {
	f.scaffolded = true
	return f.err
}

// TestBoot_ScaffoldsReconcilesAndDrains proves the full boot spine runs — the
// job DB is opened and migrated, the engine is scaffolded, and Reconcile is
// invoked — and that node.Run returns a clean nil when ctx is already canceled
// (the stop-during/after-boot drain every daemon relies on).
func TestBoot_ScaffoldsReconcilesAndDrains(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "job.db")
	store := &fakeScaffold{}
	var reconciledDB *gorm.DB

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // a stop that lands as the node would start draining

	done := make(chan error, 1)
	go func() {
		done <- Boot(ctx, BootConfig{
			Pkg:      "flynodetest",
			Queue:    "flynode-test",
			DBPath:   dbPath,
			Clock:    models.NewFixedClock(fixedNow()),
			Store:    store,
			Registry: flywheel.NewRegistry(),
			Reconcile: func(_ context.Context, db *gorm.DB) error {
				reconciledDB = db
				return nil
			},
		})
	}()

	select {
	case err := <-done:
		require.NoError(t, err, "a canceled node drains cleanly to nil")
	case <-time.After(5 * time.Second):
		t.Fatal("Boot did not return on a canceled context")
	}

	assert.True(t, store.scaffolded, "Boot scaffolds the engine before reconciling")
	assert.NotNil(t, reconciledDB, "Boot invokes Reconcile with the opened job DB")
	assert.FileExists(t, dbPath, "Boot opens and migrates the disposable job DB")
}

// TestBoot_ReconcileErrorAborts proves a Reconcile failure aborts the boot with
// that exact error, before any node is built or run (a non-canceled ctx would
// otherwise block forever in node.Run, so a prompt return is itself the proof).
func TestBoot_ReconcileErrorAborts(t *testing.T) {
	sentinel := errors.New("reconcile boom")
	err := Boot(context.Background(), BootConfig{
		Pkg:      "flynodetest",
		Queue:    "flynode-test",
		DBPath:   filepath.Join(t.TempDir(), "job.db"),
		Clock:    models.NewFixedClock(fixedNow()),
		Store:    &fakeScaffold{},
		Registry: flywheel.NewRegistry(),
		Reconcile: func(context.Context, *gorm.DB) error {
			return sentinel
		},
	})
	require.ErrorIs(t, err, sentinel, "Reconcile's error propagates verbatim")
}

// TestBoot_ScaffoldErrorAborts proves a scaffold failure aborts the boot with a
// package-tagged error before Reconcile or the node.
func TestBoot_ScaffoldErrorAborts(t *testing.T) {
	var reconciled bool
	err := Boot(context.Background(), BootConfig{
		Pkg:      "flynodetest",
		Queue:    "flynode-test",
		DBPath:   filepath.Join(t.TempDir(), "job.db"),
		Clock:    models.NewFixedClock(fixedNow()),
		Store:    &fakeScaffold{err: errors.New("no disk")},
		Registry: flywheel.NewRegistry(),
		Reconcile: func(context.Context, *gorm.DB) error {
			reconciled = true
			return nil
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "flynodetest: scaffold engine:", "the boot error is tagged with the daemon name")
	assert.False(t, reconciled, "a scaffold failure aborts before Reconcile")
}

// TestResolveDBPath covers the override > env > default-filename precedence.
func TestResolveDBPath(t *testing.T) {
	t.Run("explicit override wins", func(t *testing.T) {
		got, err := ResolveDBPath("/explicit/path.db", "FLYNODE_TEST_DB", "x.db")
		require.NoError(t, err)
		assert.Equal(t, "/explicit/path.db", got)
	})
	t.Run("env override when no explicit path", func(t *testing.T) {
		t.Setenv("FLYNODE_TEST_DB", "/from/env.db")
		got, err := ResolveDBPath("", "FLYNODE_TEST_DB", "x.db")
		require.NoError(t, err)
		assert.Equal(t, "/from/env.db", got)
	})
	t.Run("default filename under the user config dir", func(t *testing.T) {
		got, err := ResolveDBPath("", "FLYNODE_TEST_DB_NEVER_SET", "workout.db")
		require.NoError(t, err)
		assert.True(t, strings.HasSuffix(got, filepath.Join("lucid", "workout.db")),
			"default path ends in lucid/<filename>, got %q", got)
	})
}

// TestResolveClock proves an injected clock wins and, absent one, the clock on
// ctx (else a real-time default) is returned.
func TestResolveClock(t *testing.T) {
	injected := models.NewFixedClock(fixedNow())

	t.Run("injected clock wins", func(t *testing.T) {
		got := ResolveClock(context.Background(), injected)
		assert.Equal(t, fixedNow(), got.Now(context.Background()))
	})
	t.Run("falls back to the ctx clock", func(t *testing.T) {
		ctx := models.WithClock(context.Background(), injected)
		got := ResolveClock(ctx, nil)
		assert.Equal(t, fixedNow(), got.Now(ctx))
	})
	t.Run("real-time default when neither is set", func(t *testing.T) {
		got := ResolveClock(context.Background(), nil)
		require.NotNil(t, got, "a clock-less context still yields a usable clock")
	})
}
