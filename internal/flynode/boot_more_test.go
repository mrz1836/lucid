package flynode

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	flywheel "github.com/mrz1836/go-flywheel"
	"github.com/mrz1836/go-foundation/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// skipIfRoot guards the chmod-based failure injection: root ignores the
// permission bits, so the write these tests force to fail would succeed.
func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
}

// TestResolveDBPath_UserConfigDirError: with no explicit path, no env override,
// and no discoverable user-config dir, the resolver surfaces a tagged error
// rather than returning a bogus path the daemon would then try to open.
func TestResolveDBPath_UserConfigDirError(t *testing.T) {
	// os.UserConfigDir derives from these; emptying both makes it fail on every
	// supported platform (XDG then HOME on Linux, HOME on darwin).
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")

	_, err := ResolveDBPath("", "FLYNODE_TEST_DB_NEVER_SET", "x.db")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "flynode: resolve user config dir")
}

// TestBoot_JobDBDirError: when the job DB's parent directory cannot be created,
// Boot fails with the package-tagged "create job db dir" error before it opens
// any store — the first thing the spine does, and the first thing that can fail.
func TestBoot_JobDBDirError(t *testing.T) {
	skipIfRoot(t)
	parent := filepath.Join(t.TempDir(), "readonly")
	require.NoError(t, os.MkdirAll(parent, 0o500)) // read+exec, no write
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	// The DB sits under a new subdir of the unwritable parent, so MkdirAll fails.
	dbPath := filepath.Join(parent, "sub", "job.db")
	var reconciled bool
	err := Boot(context.Background(), BootConfig{
		Pkg:      "flynodetest",
		Queue:    "flynode-test",
		DBPath:   dbPath,
		Clock:    models.NewFixedClock(fixedNow()),
		Store:    &fakeScaffold{},
		Registry: flywheel.NewRegistry(),
		Reconcile: func(context.Context, *gorm.DB) error {
			reconciled = true
			return nil
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "flynodetest: create job db dir")
	assert.False(t, reconciled, "a dir-creation failure aborts before any store work")
}
