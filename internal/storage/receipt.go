package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// companionDirName is the engine-tree subdir holding the companion's delivery
// receipts — the small durable memory that makes a companion send idempotent
// across a supervisor retry. One receipt per window, overwritten each fire, so
// the tree stays tiny (it is not a history).
const companionDirName = "companion"

// CompanionReceipt is the record of the last companion delivery for one window
// (engine/companion/receipt_<window>.json). It is written ONLY through the
// binary — never hand-edited — and read on the next fire so a retry within the
// same day/window is an idempotent skip rather than a double-post. Date is the
// logical YYYY-MM-DD the send belongs to, Window is the fire window
// ("morning"/"night"), MessageID is the Discord snowflake the notifier
// returned, ChannelID names where it landed, Verified records that a read-back
// confirmed the id, and DeliveredAt is the send timestamp.
type CompanionReceipt struct {
	Date        string `json:"date"`
	Window      string `json:"window"`
	MessageID   string `json:"message_id"`
	ChannelID   string `json:"channel_id"`
	Verified    bool   `json:"verified"`
	DeliveredAt string `json:"delivered_at"`
}

// companionDir returns the ~/.lucid/engine/companion/ root.
func (a *Adapter) companionDir() string { return filepath.Join(a.engineDir(), companionDirName) }

// companionReceiptPath resolves the receipt file for a window, guarding the
// window token so a receipt path can never escape the companion dir. The window
// is an internal constant ("morning"/"night"), but the path is derived
// defensively — an empty token or one carrying a separator is rejected rather
// than written somewhere unexpected.
func (a *Adapter) companionReceiptPath(window string) (string, error) {
	if window == "" || window != filepath.Base(window) || strings.ContainsAny(window, `/\`) {
		return "", fmt.Errorf("storage: invalid companion window %q", window)
	}
	return filepath.Join(a.companionDir(), "receipt_"+window+".json"), nil
}

// ReadCompanionReceipt reads the last delivery receipt for a window. A missing
// file is not an error — a window that has never fired simply has no receipt —
// so it returns (zero, false, nil), letting the caller fire fresh. A corrupt
// body still surfaces a parse error.
func (a *Adapter) ReadCompanionReceipt(window string) (CompanionReceipt, bool, error) {
	path, err := a.companionReceiptPath(window)
	if err != nil {
		return CompanionReceipt{}, false, err
	}
	return readJSONOptional[CompanionReceipt](path, fmt.Sprintf("companion receipt %q", window))
}

// WriteCompanionReceipt writes (overwrites) the delivery receipt for a window,
// creating engine/companion/ if a bare run reaches it before a scaffold. The
// receipt is the idempotency guard the companion node reads on its next fire;
// it is written only here, through the binary, never by hand.
func (a *Adapter) WriteCompanionReceipt(r CompanionReceipt) error {
	path, err := a.companionReceiptPath(r.Window)
	if err != nil {
		return err
	}
	content, err := marshalJSON(r)
	if err != nil {
		return err
	}
	if err := ensureDir(a.companionDir(), "companion"); err != nil {
		return err
	}
	if err := os.WriteFile(path, content, filePerm); err != nil {
		return fmt.Errorf("storage: write companion receipt %q: %w", r.Window, err)
	}
	return nil
}
