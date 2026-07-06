package storage

import (
	"bytes"
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// fence is the YAML frontmatter delimiter used by raw entries,
// insights, reflections, and channel-memory files (data-model.md).
const fence = "---"

// RawRequiredKeys returns the frontmatter keys every raw entry must
// carry (data-model.md §"Raw entries" → "Field semantics"). Presence is
// required; a value may legitimately be null (e.g. agent_versions.intake
// on a /log entry). Structuring, /log, and /checkin writers validate
// against this set before a raw entry is considered well-formed.
func RawRequiredKeys() []string {
	return []string{
		"id",
		"recorded_at",
		"occurred_at",
		"occurred_at_precision",
		"source",
		"session_id",
		"command",
		"agent_versions",
		"bootstrap",
	}
}

// SplitFrontmatter separates a leading YAML frontmatter block from the
// document body. Content must begin with a "---" fence line; the block
// runs to the next line that is exactly "---". It returns the raw YAML
// bytes (without the fences) and the remaining body. An absent or
// unterminated block is an error — this validator does not guess.
func SplitFrontmatter(content []byte) (front, body []byte, err error) {
	lines := bytes.Split(content, []byte("\n"))
	if len(lines) == 0 || string(bytes.TrimRight(lines[0], "\r")) != fence {
		return nil, nil, fmt.Errorf("storage: content does not start with a --- frontmatter fence")
	}
	for i := 1; i < len(lines); i++ {
		if string(bytes.TrimRight(lines[i], "\r")) == fence {
			front = bytes.Join(lines[1:i], []byte("\n"))
			if i+1 < len(lines) {
				body = bytes.Join(lines[i+1:], []byte("\n"))
			}
			return front, body, nil
		}
	}
	return nil, nil, fmt.Errorf("storage: unterminated frontmatter block (no closing --- fence)")
}

// ParseFrontmatter splits and YAML-decodes the frontmatter into a
// generic map, returning the map and the document body.
func ParseFrontmatter(content []byte) (fields map[string]any, body []byte, err error) {
	front, body, err := SplitFrontmatter(content)
	if err != nil {
		return nil, nil, err
	}
	fields = map[string]any{}
	if err := yaml.Unmarshal(front, &fields); err != nil {
		return nil, nil, fmt.Errorf("storage: parse frontmatter yaml: %w", err)
	}
	return fields, body, nil
}

// ValidateRequiredKeys reports the first required key missing from
// fields, or nil when all are present. A key that is present with a
// null value satisfies the check (presence, not non-nullness).
func ValidateRequiredKeys(fields map[string]any, required []string) error {
	for _, k := range required {
		if _, ok := fields[k]; !ok {
			return fmt.Errorf("storage: missing required key %q", k)
		}
	}
	return nil
}

// ValidateRawFrontmatter parses a raw-entry document and confirms every
// key in [RawRequiredKeys] is present. It is the deterministic gate the
// /log and /checkin writers run before treating an entry as valid.
func ValidateRawFrontmatter(content []byte) error {
	fields, _, err := ParseFrontmatter(content)
	if err != nil {
		return err
	}
	return ValidateRequiredKeys(fields, RawRequiredKeys())
}

// ValidateJSONRequiredKeys reports the first required top-level key
// missing from a JSON object, or nil when all are present. It is the
// deterministic schema gate for the JSON records under ~/.lucid/
// (processed artifacts, people, sessions) that later phases produce.
func ValidateJSONRequiredKeys(b []byte, required []string) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(b, &obj); err != nil {
		return fmt.Errorf("storage: parse json object: %w", err)
	}
	for _, k := range required {
		if _, ok := obj[k]; !ok {
			return fmt.Errorf("storage: missing required json key %q", k)
		}
	}
	return nil
}
