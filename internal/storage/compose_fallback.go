package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// composeFallbackDirName is the engine-tree subdir holding the compose-fallback
// ping receipt — the small durable memory that bounds the scheduled-fallback
// user-channel ping to once per local day across every message daemon. A single
// receipt file, overwritten on each claim (it is not a history), so the tree
// stays tiny.
const composeFallbackDirName = "compose_fallback"

// ComposeFallbackPing is the record of the last scheduled-fallback ping
// (engine/compose_fallback/ping.json). It is written ONLY through the binary —
// never hand-edited — and read on the next fallback so a later one the same
// local day is logged and marked but not pinged again, even across a scheduler
// restart. Date is the local logical day the ping belongs to ("YYYY-MM-DD"),
// Daemon names the daemon whose fallback pinged (companion-morning, workout, …),
// Reason is its fallback reason, and PingedAt is the claim timestamp.
type ComposeFallbackPing struct {
	Date     string `json:"date"`
	Daemon   string `json:"daemon"`
	Reason   string `json:"reason"`
	PingedAt string `json:"pinged_at"`
}

// composeFallbackDir returns the ~/.lucid/engine/compose_fallback/ root.
func (a *Adapter) composeFallbackDir() string {
	return filepath.Join(a.engineDir(), composeFallbackDirName)
}

// composeFallbackPingPath resolves the single ping receipt file.
func (a *Adapter) composeFallbackPingPath() string {
	return filepath.Join(a.composeFallbackDir(), "ping.json")
}

// ReadComposeFallbackPing reads the last scheduled-fallback ping receipt. A
// missing file is not an error — no fallback has pinged yet — so it returns
// (zero, false, nil). A corrupt body still surfaces a parse error.
func (a *Adapter) ReadComposeFallbackPing() (ComposeFallbackPing, bool, error) {
	return readJSONOptional[ComposeFallbackPing](a.composeFallbackPingPath(), "compose fallback ping")
}

// WriteComposeFallbackPing writes (overwrites) the scheduled-fallback ping
// receipt, creating engine/compose_fallback/ if a bare run reaches it before a
// scaffold. It is written only here, through the binary, never by hand.
func (a *Adapter) WriteComposeFallbackPing(p ComposeFallbackPing) error {
	content, err := marshalJSON(p)
	if err != nil {
		return err
	}
	if err := ensureDir(a.composeFallbackDir(), "compose fallback"); err != nil {
		return err
	}
	if err := os.WriteFile(a.composeFallbackPingPath(), content, filePerm); err != nil {
		return fmt.Errorf("storage: write compose fallback ping: %w", err)
	}
	return nil
}

// ClaimComposeFallbackPing claims the once-per-local-day scheduled-fallback ping
// for p.Date. When the receipt already records a ping for that same Date it
// returns (false, nil): another fallback — from any daemon — already pinged
// today, so this one is logged and marked but not pinged. Otherwise it records p
// and returns (true, nil).
//
// It fails OPEN: an unreadable or corrupt receipt still attempts the write and
// returns (true, readErr), and a failed write returns (true, writeErr), so the
// caller pings and logs the error rather than silencing the notice. The
// read-compare-write runs under the adapter's mutex, so daemons sharing this
// adapter can never both claim the same day. The receipt is written only through
// the binary.
func (a *Adapter) ClaimComposeFallbackPing(p ComposeFallbackPing) (bool, error) {
	a.composeFallbackMu.Lock()
	defer a.composeFallbackMu.Unlock()

	prev, found, readErr := a.ReadComposeFallbackPing()
	if readErr == nil && found && prev.Date == p.Date {
		return false, nil
	}
	if err := a.WriteComposeFallbackPing(p); err != nil {
		return true, errors.Join(readErr, err)
	}
	return true, readErr
}
