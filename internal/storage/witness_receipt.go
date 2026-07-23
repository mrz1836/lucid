package storage

import (
	"fmt"
	"os"
	"path/filepath"
)

// witnessReportDirName is the engine-tree subdir holding the weekly witness
// report's delivery receipt — the small durable memory that makes a weekly send
// idempotent across a supervisor retry. A single receipt file, overwritten each
// week, keyed on the ISO week it delivered (it is not a history), so the tree
// stays tiny.
const witnessReportDirName = "witness"

// WitnessReportReceipt is the record of the last weekly witness report delivery
// (engine/witness/receipt.json). It is written ONLY through the binary — never
// hand-edited — and read on the next fire so a retry within the same ISO week is
// an idempotent skip rather than a double-post. Week is the ISO-8601 week label
// the report belongs to ("YYYY-Www"), MessageID is the Discord snowflake the
// notifier returned, ChannelID names where it landed (preview → the user
// channel, auto → the witness channel), Verified records that a read-back
// confirmed the id, and DeliveredAt is the send timestamp.
type WitnessReportReceipt struct {
	Week        string `json:"week"`
	MessageID   string `json:"message_id"`
	ChannelID   string `json:"channel_id"`
	Verified    bool   `json:"verified"`
	DeliveredAt string `json:"delivered_at"`
}

// witnessReportDir returns the ~/.lucid/engine/witness/ root.
func (a *Adapter) witnessReportDir() string {
	return filepath.Join(a.engineDir(), witnessReportDirName)
}

// witnessReportReceiptPath resolves the single weekly-report receipt file.
func (a *Adapter) witnessReportReceiptPath() string {
	return filepath.Join(a.witnessReportDir(), "receipt.json")
}

// ReadWitnessReportReceipt reads the last weekly-report delivery receipt. A
// missing file is not an error — a report that has never fired simply has no
// receipt — so it returns (zero, false, nil), letting the caller fire fresh. A
// corrupt body still surfaces a parse error.
func (a *Adapter) ReadWitnessReportReceipt() (WitnessReportReceipt, bool, error) {
	return readJSONOptional[WitnessReportReceipt](a.witnessReportReceiptPath(), "witness report receipt")
}

// WriteWitnessReportReceipt writes (overwrites) the weekly-report delivery
// receipt, creating engine/witness/ if a bare run reaches it before a scaffold.
// The receipt is the idempotency guard the weekly node reads on its next fire;
// it is written only here, through the binary, never by hand.
func (a *Adapter) WriteWitnessReportReceipt(r WitnessReportReceipt) error {
	content, err := marshalJSON(r)
	if err != nil {
		return err
	}
	if err := ensureDir(a.witnessReportDir(), "witness"); err != nil {
		return err
	}
	if err := os.WriteFile(a.witnessReportReceiptPath(), content, filePerm); err != nil {
		return fmt.Errorf("storage: write witness report receipt: %w", err)
	}
	return nil
}
