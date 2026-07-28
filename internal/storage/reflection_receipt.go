package storage

import (
	"fmt"
	"os"
	"path/filepath"
)

// reflectionDirName is the engine-tree subdir holding the weekly deep-dive's
// reflected-through cursor — the small durable memory that lets a reflection
// cover every day since the last one instead of a fixed calendar week. A single
// receipt file, overwritten each close (it is not a history), so the tree stays
// tiny.
const reflectionDirName = "reflection"

// ReflectionReceipt is the reflected-through cursor
// (engine/reflection/receipt.json): the watermark the weekly deep-dive resolves
// its window from. It is written ONLY through the binary — never hand-edited —
// and only by an explicit close or a completed apply, never by the read itself,
// which is read-only by contract. ReflectedThrough is the instant the user
// finished reflecting (RFC3339, local TZ); the next window starts at the
// beginning of that logical day, so the close day is re-read rather than
// half-dropped. Source records which act stamped it ("close" or "apply") and
// WrittenAt is when.
type ReflectionReceipt struct {
	ReflectedThrough string `json:"reflected_through"`
	Source           string `json:"source"`
	WrittenAt        string `json:"written_at"`
}

// reflectionDir returns the ~/.lucid/engine/reflection/ root.
func (a *Adapter) reflectionDir() string {
	return filepath.Join(a.engineDir(), reflectionDirName)
}

// reflectionReceiptPath resolves the single reflected-through cursor file.
func (a *Adapter) reflectionReceiptPath() string {
	return filepath.Join(a.reflectionDir(), "receipt.json")
}

// ReadReflectionReceipt reads the reflected-through cursor. A missing file is
// not an error — a Ledger whose owner has never closed a reflection simply has
// no cursor — so it returns (zero, false, nil), letting the caller fall back to
// a first-run window. It never creates anything: the weekly read calls this on
// every invocation and must leave the tree byte-identical. A corrupt body still
// surfaces a parse error.
func (a *Adapter) ReadReflectionReceipt() (ReflectionReceipt, bool, error) {
	return readJSONOptional[ReflectionReceipt](a.reflectionReceiptPath(), "reflection receipt")
}

// WriteReflectionReceipt writes (overwrites) the reflected-through cursor,
// creating engine/reflection/ if a bare run reaches it before a scaffold. The
// cursor is the sole input to the next window's start; it is written only here,
// through the binary, and only by an explicit close or a completed apply.
func (a *Adapter) WriteReflectionReceipt(r ReflectionReceipt) error {
	content, err := marshalJSON(r)
	if err != nil {
		return err
	}
	if err := ensureDir(a.reflectionDir(), "reflection"); err != nil {
		return err
	}
	if err := os.WriteFile(a.reflectionReceiptPath(), content, filePerm); err != nil {
		return fmt.Errorf("storage: write reflection receipt: %w", err)
	}
	return nil
}
