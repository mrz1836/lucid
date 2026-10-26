package storage

import (
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
//
// Parts is present only for a multi-message report — full anchor detail that
// outgrew one embed. The scheduled run freezes the whole rendered batch here
// before its first send and records each part's message id and read-back as it
// goes, so a retry resumes the frozen batch without re-sending a verified part.
// Until every part has verified, MessageID stays empty and Verified false: the
// week is not delivered, which is also how an older binary (which ignores
// Parts) reads it. Once every part verified, MessageID is part 1's id — the
// report card — and Verified is true, exactly the single-message shape. A
// legacy receipt carries no parts and reads unchanged.
type WitnessReportReceipt struct {
	Week        string              `json:"week"`
	MessageID   string              `json:"message_id"`
	ChannelID   string              `json:"channel_id"`
	Verified    bool                `json:"verified"`
	DeliveredAt string              `json:"delivered_at"`
	Parts       []WitnessReportPart `json:"parts,omitempty"`
}

// WitnessReportPart is one frozen message of a multi-message weekly report:
// the exact embed to send, the message id the send returned (empty until it is
// sent), and whether a read-back confirmed that id.
type WitnessReportPart struct {
	Embed     WitnessReportEmbed `json:"embed"`
	MessageID string             `json:"message_id,omitempty"`
	Verified  bool               `json:"verified"`
}

// WitnessReportEmbed is a frozen rich embed: the payload of one part, stored
// verbatim so a resumed delivery sends exactly what the first attempt rendered.
type WitnessReportEmbed struct {
	Title       string               `json:"title,omitempty"`
	Description string               `json:"description,omitempty"`
	Color       int                  `json:"color,omitempty"`
	Fields      []WitnessReportField `json:"fields,omitempty"`
	Footer      string               `json:"footer,omitempty"`
}

// WitnessReportField is one named field of a frozen embed.
type WitnessReportField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

// Pending reports whether the receipt holds a frozen multi-message batch that
// has not finished delivering — the state a retry resumes rather than
// re-composing.
func (r WitnessReportReceipt) Pending() bool {
	return len(r.Parts) > 0 && (!r.Verified || r.MessageID == "")
}

// PartsVerified counts the parts a read-back has confirmed.
func (r WitnessReportReceipt) PartsVerified() int {
	n := 0
	for _, p := range r.Parts {
		if p.Verified {
			n++
		}
	}
	return n
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
// it is written only here, through the binary, never by hand. The write is
// atomic, so a crash mid-write leaves the previous receipt — and with it the
// recorded progress of a multi-message batch — intact.
func (a *Adapter) WriteWitnessReportReceipt(r WitnessReportReceipt) error {
	content, err := marshalJSON(r)
	if err != nil {
		return err
	}
	if err := ensureDir(a.witnessReportDir(), "witness"); err != nil {
		return err
	}
	return writeFileAtomic(a.witnessReportReceiptPath(), content, "witness report receipt")
}
