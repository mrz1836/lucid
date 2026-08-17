package companion

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	flywheel "github.com/mrz1836/go-flywheel"
	"github.com/mrz1836/go-foundation/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/lucidtest"
)

// skipIfRoot guards the chmod-based write-failure injection: root ignores the
// permission bits, so the write these tests force to fail would succeed.
func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
}

// corruptChain overwrites the scaffolded chain.json with unparseable bytes, so
// the next ReadChainConfig surfaces a loud parse error.
func corruptChain(t *testing.T, home string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(home, "engine", "chain.json"), []byte("{bad"), 0o600))
}

// TestFire_ChainReadError_IsLoud: a fire whose chain.json cannot be read fails
// loudly (the mark and cut-off are derived from it) rather than guessing a
// schedule — no send, and no receipt.
func TestFire_ChainReadError_IsLoud(t *testing.T) {
	comp := &fakeComposer{res: Result{Text: "GM"}}
	del := &lucidtest.FakeDeliverer{}
	r, store := newRunner(t, comp, del)
	corruptChain(t, store.Home())

	_, err := r.Fire(context.Background(), ModeMorning, at(2026, 7, 6, 6, 0))
	require.Error(t, err)
	assert.Empty(t, del.Sends, "no send when the chain cannot be read")
	assert.Equal(t, 0, comp.calls, "no compose when the chain cannot be read")
}

// TestFire_ReceiptReadError_IsLoud: the idempotency guard's receipt read is
// life-critical — a corrupt receipt file fails the fire loudly rather than
// risking a double-post by treating it as "never delivered".
func TestFire_ReceiptReadError_IsLoud(t *testing.T) {
	comp := &fakeComposer{res: Result{Text: "GM"}}
	del := &lucidtest.FakeDeliverer{}
	r, store := newRunner(t, comp, del)

	companionDir := filepath.Join(store.Home(), "engine", "companion")
	require.NoError(t, os.MkdirAll(companionDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(companionDir, "receipt_morning.json"), []byte("{bad"), 0o600))

	_, err := r.Fire(context.Background(), ModeMorning, at(2026, 7, 6, 6, 0))
	require.Error(t, err)
	assert.Empty(t, del.Sends, "a corrupt receipt is not treated as a fresh window")
}

// TestFire_ReceiptWriteError_IsLoud: a fire that delivers and verifies but then
// cannot persist its receipt fails loudly — the receipt is the exactly-once
// guard, and a delivery it cannot record must surface rather than pass silently.
func TestFire_ReceiptWriteError_IsLoud(t *testing.T) {
	skipIfRoot(t)
	comp := &fakeComposer{res: Result{Text: "GM"}}
	del := &lucidtest.FakeDeliverer{}
	r, store := newRunner(t, comp, del)

	companionDir := filepath.Join(store.Home(), "engine", "companion")
	require.NoError(t, os.MkdirAll(companionDir, 0o755))
	require.NoError(t, os.Chmod(companionDir, 0o500)) // read+exec, no write
	t.Cleanup(func() { _ = os.Chmod(companionDir, 0o700) })

	_, err := r.Fire(context.Background(), ModeMorning, at(2026, 7, 6, 6, 0))
	require.Error(t, err)
	require.Len(t, del.Sends, 1, "the message did go out; it is the receipt that failed")
	assert.Empty(t, del.Alerts, "a receipt-write failure surfaces through the returned error, not a duplicate alert")
}

// TestMorningWorker_FireError_Propagates: a delivery failure inside the morning
// worker becomes a job failure (a non-nil Work error), which is what puts it on
// the supervisor's retry ladder rather than silently dropping the send.
func TestMorningWorker_FireError_Propagates(t *testing.T) {
	comp := &fakeComposer{res: Result{Text: "GM"}}
	del := &lucidtest.FakeDeliverer{SendErr: errors.New("discord 503")}
	r, _ := newRunner(t, comp, del)
	now := at(2026, 7, 6, 6, 0)
	w := morningWorker{r: r, clock: models.NewFixedClock(now)}

	_, err := w.Work(ctxAt(now), &flywheel.Job[companionArgs]{})
	require.Error(t, err)
}

// TestNightWorker_FireError_Propagates is the night counterpart: a night
// delivery failure is a job failure too.
func TestNightWorker_FireError_Propagates(t *testing.T) {
	comp := &fakeComposer{res: Result{Text: "GN"}}
	del := &lucidtest.FakeDeliverer{SendErr: errors.New("discord 503")}
	r, _ := newRunner(t, comp, del)
	now := at(2026, 7, 6, 19, 0)
	w := nightWorker{r: r, clock: models.NewFixedClock(now)}

	_, err := w.Work(ctxAt(now), &flywheel.Job[companionArgs]{})
	require.Error(t, err)
}

// TestBackstopWorker_FireError_Propagates: inside the window, a backstop whose
// re-fire fails to deliver returns a job error — the backstop is the morning's
// safety net, so its own failure must be loud rather than swallowed.
func TestBackstopWorker_FireError_Propagates(t *testing.T) {
	comp := &fakeComposer{res: Result{Text: "GM"}}
	del := &lucidtest.FakeDeliverer{SendErr: errors.New("discord 503")}
	r, _ := newRunner(t, comp, del)
	now := at(2026, 7, 6, 7, 0) // inside the 10:00 cut-off
	w := morningBackstopWorker{r: r, clock: models.NewFixedClock(now)}

	_, err := w.Work(ctxAt(now), &flywheel.Job[companionArgs]{})
	require.Error(t, err)
}

// TestUpsertPeriodics_ChainReadError_IsLoud: seeding the periodics needs the
// chain marks, so an unreadable chain.json fails the reconcile loudly rather
// than arming the sends on a guessed schedule.
func TestUpsertPeriodics_ChainReadError_IsLoud(t *testing.T) {
	store := newStore(t)
	db := newJobDB(t)
	corruptChain(t, store.Home())

	require.Error(t, upsertPeriodics(ctxAt(at(2026, 7, 5, 12, 0)), db, store))
}

// TestRun_DBPathResolutionError_IsReturned: with the required collaborators in
// place but no resolvable job-DB path (no override, no env, no user-config
// dir), Run returns the resolution error rather than booting against a bogus
// path.
func TestRun_DBPathResolutionError_IsReturned(t *testing.T) {
	store := newStore(t)
	// Force flynode.ResolveDBPath down to os.UserConfigDir and make that fail on
	// every supported platform (XDG then HOME on Linux, HOME on darwin).
	t.Setenv(envCompanionDB, "")
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")

	err := Run(context.Background(), Options{
		Store:    store,
		Notifier: &lucidtest.FakeDeliverer{},
		Numbers:  fakeNumbers{},
		Verdict:  fakeVerdict{},
		// DBPath left empty on purpose.
	})
	require.Error(t, err)
}
