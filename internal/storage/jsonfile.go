package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// readJSON reads and decodes the JSON record at path into a T. A missing file
// is an error — the policy for records the caller treats as required (the
// engine state files: chain.json, profile.json, storm.json, …). label names the
// record for the error message ("chain.json", or a formatted "engine day
// \"…\""), preserving the per-call wording. It is the read half of the JSON
// record skeleton every Adapter method used to hand-copy.
func readJSON[T any](path, label string) (T, error) {
	var v T
	b, err := os.ReadFile(path) //nolint:gosec // adapter-internal path derived from the resolved Ledger home
	if err != nil {
		return v, fmt.Errorf("storage: read %s: %w", label, err)
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return v, fmt.Errorf("storage: parse %s: %w", label, err)
	}
	return v, nil
}

// writeJSON marshals v and writes it to path, wrapping either failure with
// label. It is the write half of the JSON record skeleton — the counterpart to
// [readJSON] — so whole-file JSON writers stop hand-copying the marshal-then-
// write dance and its two error wraps.
func writeJSON(path, label string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("storage: marshal %s: %w", label, err)
	}
	if err := os.WriteFile(path, b, filePerm); err != nil {
		return fmt.Errorf("storage: write %s: %w", label, err)
	}
	return nil
}

// readJSONResilient decodes the ephemeral JSON state at path into a T, treating
// BOTH a missing file and a corrupt body as the fresh zero value rather than an
// error: ephemeral ask-state (discovery, curiosity) resets instead of blocking
// capture. Only an unexpected read error surfaces. label names the record for
// that error. It is the read skeleton the two ephemeral state files shared.
func readJSONResilient[T any](path, label string) (T, error) {
	var v T
	b, err := os.ReadFile(path) //nolint:gosec // adapter-internal path derived from the resolved Ledger home
	if errors.Is(err, fs.ErrNotExist) {
		return v, nil
	}
	if err != nil {
		return v, fmt.Errorf("storage: read %s: %w", label, err)
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return v, nil //nolint:nilerr // a corrupt ephemeral file resets, never blocks capture
	}
	return v, nil
}

// readJSONOptional decodes the JSON record at path into a T, treating a missing
// file as "not found" rather than an error: it returns the zero value, found ==
// false, and a nil error (the policy for records that legitimately may not
// exist yet — an engine day, tripwire.json, a person, a registry). A malformed
// body is still a parse error. Callers whose contract is (T, error) discard the
// bool; those that surface found (like ReadEngineDay) return it directly.
func readJSONOptional[T any](path, label string) (T, bool, error) {
	var v T
	b, err := os.ReadFile(path) //nolint:gosec // adapter-internal path derived from the resolved Ledger home
	if errors.Is(err, fs.ErrNotExist) {
		return v, false, nil
	}
	if err != nil {
		return v, false, fmt.Errorf("storage: read %s: %w", label, err)
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return v, false, fmt.Errorf("storage: parse %s: %w", label, err)
	}
	return v, true, nil
}
