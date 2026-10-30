package storage

import "path/filepath"

// contactWeeklyReceiptFile is the Monday contact post's delivery receipt at the
// root of the contacts/ tree (ADR-0014 §2, §8). It is operational state, not
// Ledger truth: a single file overwritten each week, keyed on the ISO week it
// delivered, so the tree never grows a history of sends. The day-shard readers
// skip it — it is not a contact_*.jsonl file.
const contactWeeklyReceiptFile = "weekly_receipt.json"

// ContactWeeklyReceipt is the record of the last Monday contact post delivery
// (contacts/weekly_receipt.json). It is written ONLY through the binary — never
// hand-edited — and read on the next fire so a retry within the same ISO week is
// an idempotent skip rather than a double-post. Week is the ISO-8601 week label
// the post belongs to ("YYYY-Www"), MessageID is the message id the notifier
// returned, ChannelID names where it landed (always the user channel),
// Verified records that a read-back confirmed the id, and DeliveredAt is the
// send timestamp. It mirrors the single-message [WitnessReportReceipt].
type ContactWeeklyReceipt struct {
	Week        string `json:"week"`
	MessageID   string `json:"message_id"`
	ChannelID   string `json:"channel_id"`
	Verified    bool   `json:"verified"`
	DeliveredAt string `json:"delivered_at"`
}

// contactWeeklyReceiptPath resolves the single Monday-post receipt file.
func (a *Adapter) contactWeeklyReceiptPath() string {
	return filepath.Join(a.contactDir(), contactWeeklyReceiptFile)
}

// ReadContactWeeklyReceipt reads the last Monday-post delivery receipt. A
// missing file is not an error — a post that has never fired simply has no
// receipt — so it returns (zero, false, nil), letting the caller fire fresh. A
// corrupt body still surfaces a parse error. It never creates anything.
func (a *Adapter) ReadContactWeeklyReceipt() (ContactWeeklyReceipt, bool, error) {
	return readJSONOptional[ContactWeeklyReceipt](a.contactWeeklyReceiptPath(), "contact weekly receipt")
}

// WriteContactWeeklyReceipt writes (overwrites) the Monday-post delivery
// receipt, creating contacts/ if a bare run reaches it before a scaffold. The
// receipt is the idempotency guard the weekly node reads on its next fire; it
// is written only here, through the binary, never by hand. The write is
// atomic, so a crash mid-write leaves the previous receipt intact.
func (a *Adapter) WriteContactWeeklyReceipt(r ContactWeeklyReceipt) error {
	content, err := marshalJSON(r)
	if err != nil {
		return err
	}
	if err := ensureDir(a.contactDir(), "contacts"); err != nil {
		return err
	}
	return writeFileAtomic(a.contactWeeklyReceiptPath(), content, "contact weekly receipt")
}
