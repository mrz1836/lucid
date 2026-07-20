package storage

import (
	"fmt"
	"os"
)

// ensureDir creates dir (and any missing parents) if absent, wrapping a failure
// with the standard "prepare <label> dir" message the record writers share.
// The Ledger tree is scaffolded at init, so at runtime this is an idempotent
// guard that a record family's directory exists before a write — and the single
// place the write path's MkdirAll wording (and, later, its rooting) lives.
func ensureDir(dir, label string) error {
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("storage: prepare %s dir: %w", label, err)
	}
	return nil
}
