package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// jsonFlag is the persistent flag name that switches supported
// commands (status, day, export, validate, version, upgrade --check)
// into machine-readable output. Human-first prose is the default so
// automation never scrapes formatted text (ADR-0007).
const jsonFlag = "json"

// writeJSON marshals v as indented JSON to w with a trailing newline.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("lucid: encode json: %w", err)
	}
	return nil
}

// containsFold reports whether substr is contained in s,
// case-insensitively.
func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
