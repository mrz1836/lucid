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
