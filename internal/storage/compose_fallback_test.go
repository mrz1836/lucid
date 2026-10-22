package storage

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fallbackPing builds a synthetic ping claim for one local day.
func fallbackPing(date, daemon string) ComposeFallbackPing {
	return ComposeFallbackPing{Date: date, Daemon: daemon, Reason: "provider_outage", PingedAt: date + "T07:00:00Z"}
}

// TestComposeFallbackPing_MissingIsZero: a Ledger with no fallback ping yet reads
// (zero, false, nil), and a written receipt round-trips whole.
func TestComposeFallbackPing_MissingIsZero(t *testing.T) {
	a := newEngineAdapter(t)

	_, ok, err := a.ReadComposeFallbackPing()
	require.NoError(t, err)
	assert.False(t, ok, "no fallback has pinged yet")

	want := fallbackPing("2026-01-05", "workout")
	require.NoError(t, a.WriteComposeFallbackPing(want))

	got, ok, err := a.ReadComposeFallbackPing()
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, want, got)
}

// TestClaimComposeFallbackPing_OncePerLocalDay: the first claim of a local day
// wins, a second claim that same day — even from a different daemon — is
// refused, and the next day is claimable again.
func TestClaimComposeFallbackPing_OncePerLocalDay(t *testing.T) {
	a := newEngineAdapter(t)

	claimed, err := a.ClaimComposeFallbackPing(fallbackPing("2026-01-05", "companion-morning"))
	require.NoError(t, err)
	assert.True(t, claimed, "the first fallback of the day pings")

	claimed, err = a.ClaimComposeFallbackPing(fallbackPing("2026-01-05", "workout"))
	require.NoError(t, err)
	assert.False(t, claimed, "a later fallback the same day, from another daemon, is not pinged")

	got, ok, err := a.ReadComposeFallbackPing()
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "companion-morning", got.Daemon, "a refused claim leaves the day's receipt untouched")

	claimed, err = a.ClaimComposeFallbackPing(fallbackPing("2026-01-06", "workout"))
	require.NoError(t, err)
	assert.True(t, claimed, "the next local day pings again")
}

// TestClaimComposeFallbackPing_SurvivesFreshAdapter: the claim is durable — a
// fresh adapter over the same home (a scheduler restart) still sees it.
func TestClaimComposeFallbackPing_SurvivesFreshAdapter(t *testing.T) {
	a := newEngineAdapter(t)
	claimed, err := a.ClaimComposeFallbackPing(fallbackPing("2026-01-05", "companion-night"))
	require.NoError(t, err)
	require.True(t, claimed)

	fresh := New(a.home)
	claimed, err = fresh.ClaimComposeFallbackPing(fallbackPing("2026-01-05", "witness"))
	require.NoError(t, err)
	assert.False(t, claimed, "a restart does not re-ping the same day")
}

// TestClaimComposeFallbackPing_CorruptStateFailsOpen: an unparseable receipt
// fails open — the claim succeeds (so the ping still goes out), the parse error
// is returned for the caller to log, and the receipt is rewritten whole.
func TestClaimComposeFallbackPing_CorruptStateFailsOpen(t *testing.T) {
	a := newEngineAdapter(t)
	require.NoError(t, os.MkdirAll(a.composeFallbackDir(), 0o700))
	require.NoError(t, os.WriteFile(a.composeFallbackPingPath(), []byte("{not json"), 0o600))

	claimed, err := a.ClaimComposeFallbackPing(fallbackPing("2026-01-05", "workout"))
	require.Error(t, err, "the corrupt state is surfaced")
	assert.True(t, claimed, "a corrupt receipt never silences the ping")

	got, ok, rerr := a.ReadComposeFallbackPing()
	require.NoError(t, rerr, "the claim rewrote a valid receipt")
	require.True(t, ok)
	assert.Equal(t, "2026-01-05", got.Date)
}

// TestClaimComposeFallbackPing_WriteFailureFailsOpen: an unwritable receipt
// directory fails open — the claim succeeds and the write error is returned.
func TestClaimComposeFallbackPing_WriteFailureFailsOpen(t *testing.T) {
	skipIfRoot(t)
	a := newEngineAdapter(t)
	dir := a.composeFallbackDir()
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	claimed, err := a.ClaimComposeFallbackPing(fallbackPing("2026-01-05", "workout"))
	require.Error(t, err, "the failed write is surfaced")
	assert.True(t, claimed, "an unwritable receipt never silences the ping")
	_, statErr := os.Stat(filepath.Join(dir, "ping.json"))
	assert.True(t, os.IsNotExist(statErr), "nothing was written")
}

// TestComposeFallbackPing_WriteDirError: a receipt whose engine root
// cannot be created surfaces the prepare-dir error rather than writing nothing
// silently.
func TestComposeFallbackPing_WriteDirError(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".lucid")
	require.NoError(t, os.MkdirAll(home, 0o700))
	// A regular file where the engine dir should be makes MkdirAll fail.
	require.NoError(t, os.WriteFile(filepath.Join(home, engineDirName), []byte("x"), 0o600))

	err := New(home).WriteComposeFallbackPing(fallbackPing("2026-01-05", "workout"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "compose fallback")
}

// TestClaimComposeFallbackPing_ConcurrentClaimsOneWinner: daemons sharing one
// adapter race to claim the same day; the mutex lets exactly one win.
func TestClaimComposeFallbackPing_ConcurrentClaimsOneWinner(t *testing.T) {
	a := newEngineAdapter(t)
	daemons := []string{"companion-morning", "companion-night", "workout", "witness"}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		wins int
	)
	for _, d := range daemons {
		wg.Add(1)
		go func(daemon string) {
			defer wg.Done()
			claimed, err := a.ClaimComposeFallbackPing(fallbackPing("2026-01-05", daemon))
			assert.NoError(t, err)
			if claimed {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}(d)
	}
	wg.Wait()
	assert.Equal(t, 1, wins, "exactly one daemon pings per local day")
}
