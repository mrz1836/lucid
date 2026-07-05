package storage

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// rawExt is the file extension for raw entry documents.
const rawExt = ".md"

// Occurred-at precision values for a raw entry (data-model.md §"Field
// semantics"): when it happened is known exactly, only approximately, or
// spans a range (with occurred_at_end set).
const (
	PrecisionExact       = "exact"
	PrecisionApproximate = "approximate"
	PrecisionRange       = "range"
)

// maxSameSecondIDs bounds the monotonic counter appended when both the
// minute-precision id and its seconds-suffixed form already exist
// (data-model.md §"Same-minute id collision rule"). It is far above any
// plausible same-second capture rate and exists only so a pathological
// caller cannot spin forever.
const maxSameSecondIDs = 1000

// RawEntry is the caller's intent for one immutable capture (data-model.md
// §"Raw entries"). The storage adapter derives the id and, when
// SessionID is empty, the paired session id; the caller never sets those.
type RawEntry struct {
	// RecordedAt is when the user wrote this (always "now" at write time);
	// it also seeds the minute-precision id.
	RecordedAt time.Time
	// OccurredAt is when it happened (equal to RecordedAt for "now" entries).
	OccurredAt time.Time
	// OccurredAtPrecision is one of [PrecisionExact], [PrecisionApproximate],
	// or [PrecisionRange].
	OccurredAtPrecision string
	// OccurredAtEnd is set only when OccurredAtPrecision is [PrecisionRange].
	OccurredAtEnd *time.Time
	// Source is the harness identifier (discord, cli, ...).
	Source string
	// SessionID names the session this entry belongs to. Leave empty for a
	// one-shot capture (e.g. /log): WriteRaw derives a session id that
	// shares the raw id's suffix and returns it for the caller to write the
	// session record under.
	SessionID string
	// Command is the verb that produced the entry (/log, /checkin, ...).
	Command string
	// IntakeQuestions is present only for /checkin entries.
	IntakeQuestions []string
	// IntakeVersion stamps the intake agent version; nil renders as
	// `intake: null` (the /log case, where no agent touched the capture).
	IntakeVersion *string
	// Bootstrap is true for entries written during a /bootstrap session.
	Bootstrap bool
	// Body is the verbatim entry text (may be empty).
	Body string
}

// RawResult reports the ids WriteRaw assigned to a capture.
type RawResult struct {
	// RawID is the id of the raw entry that was written.
	RawID string
	// SessionID is the session id the entry references — either the one the
	// caller supplied or, for a one-shot capture, the derived id the caller
	// must now write the session record under.
	SessionID string
}

// RawDocument is a parsed raw entry as returned by ReadRaw.
type RawDocument struct {
	// ID is the raw entry id.
	ID string
	// Fields is the decoded YAML frontmatter.
	Fields map[string]any
	// Body is the entry body with surrounding whitespace trimmed.
	Body string
}

// rawFrontmatter is the on-disk YAML frontmatter shape. Field order
// matches data-model.md §"Raw entries" so a written file reads like the
// documented schema.
type rawFrontmatter struct {
	ID                  string           `yaml:"id"`
	RecordedAt          string           `yaml:"recorded_at"`
	OccurredAt          string           `yaml:"occurred_at"`
	OccurredAtPrecision string           `yaml:"occurred_at_precision"`
	OccurredAtEnd       *string          `yaml:"occurred_at_end,omitempty"`
	Source              string           `yaml:"source"`
	SessionID           string           `yaml:"session_id"`
	Command             string           `yaml:"command"`
	IntakeQuestions     []string         `yaml:"intake_questions,omitempty"`
	AgentVersions       rawAgentVersions `yaml:"agent_versions"`
	Bootstrap           bool             `yaml:"bootstrap"`
}

// rawAgentVersions is the raw entry's agent_versions block. Only intake
// can touch a capture; a nil pointer renders as `intake: null`.
type rawAgentVersions struct {
	Intake *string `yaml:"intake"`
}

// WriteRaw appends a new immutable raw entry and returns the ids it
// assigned. The raw id is derived from e.RecordedAt at minute precision;
// a same-minute collision appends `_SS` (seconds) and, if that also
// exists, `_SS_N` (a small counter), per data-model.md §"Same-minute id
// collision rule". There is no update_raw — an existing id is never
// overwritten. When e.SessionID is empty the entry is paired with a
// fresh session whose id shares the raw id's suffix; the returned
// SessionID is what the caller writes the session record under. On any
// write failure (error-states.md §St-1: disk full or permission denied)
// nothing is left on disk and the error is returned for the caller to
// surface — the writer never retries silently.
func (a *Adapter) WriteRaw(e RawEntry) (RawResult, error) {
	if err := e.validate(); err != nil {
		return RawResult{}, err
	}
	dir := a.rawShardDir(e.RecordedAt)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return RawResult{}, fmt.Errorf("storage: prepare raw dir %q: %w", dir, err)
	}
	base := rawBaseID(e.RecordedAt)
	for _, id := range collisionCandidates(base, e.RecordedAt.Second()) {
		sid := e.SessionID
		if sid == "" {
			sid = sessionIDPrefix + strings.TrimPrefix(id, rawIDPrefix)
		}
		content, err := renderRawDoc(id, sid, e)
		if err != nil {
			return RawResult{}, err
		}
		wrote, err := writeExcl(filepath.Join(dir, id+rawExt), content)
		if err != nil {
			return RawResult{}, err
		}
		if wrote {
			return RawResult{RawID: id, SessionID: sid}, nil
		}
	}
	return RawResult{}, fmt.Errorf("storage: could not allocate a unique raw id for %s", base)
}

// ReadRaw loads the raw entry with the given id, returning its decoded
// frontmatter and body. The shard directory is derived from the id.
func (a *Adapter) ReadRaw(id string) (RawDocument, error) {
	path, err := a.rawPath(id)
	if err != nil {
		return RawDocument{}, err
	}
	b, err := os.ReadFile(path) //nolint:gosec // path is adapter-internal, derived from the resolved Ledger home and a validated id
	if err != nil {
		return RawDocument{}, fmt.Errorf("storage: read raw %q: %w", id, err)
	}
	fields, body, err := ParseFrontmatter(b)
	if err != nil {
		return RawDocument{}, fmt.Errorf("storage: parse raw %q: %w", id, err)
	}
	return RawDocument{ID: id, Fields: fields, Body: strings.TrimSpace(string(body))}, nil
}

// validate reports the first structural problem with a raw entry before
// it is written. It guards the required frontmatter fields the writer
// cannot invent.
func (e RawEntry) validate() error {
	if e.RecordedAt.IsZero() {
		return errors.New("storage: raw entry recorded_at is required")
	}
	if e.OccurredAt.IsZero() {
		return errors.New("storage: raw entry occurred_at is required")
	}
	switch e.OccurredAtPrecision {
	case PrecisionExact, PrecisionApproximate, PrecisionRange:
	default:
		return fmt.Errorf("storage: invalid occurred_at_precision %q", e.OccurredAtPrecision)
	}
	if e.Source == "" {
		return errors.New("storage: raw entry source is required")
	}
	if e.Command == "" {
		return errors.New("storage: raw entry command is required")
	}
	return nil
}

// renderRawDoc builds the full Markdown-with-frontmatter document for a
// raw entry and self-validates it against [RawRequiredKeys] before
// returning, so a malformed entry can never reach disk.
func renderRawDoc(id, sessionID string, e RawEntry) ([]byte, error) {
	fm := rawFrontmatter{
		ID:                  id,
		RecordedAt:          e.RecordedAt.Format(time.RFC3339),
		OccurredAt:          e.OccurredAt.Format(time.RFC3339),
		OccurredAtPrecision: e.OccurredAtPrecision,
		Source:              e.Source,
		SessionID:           sessionID,
		Command:             e.Command,
		IntakeQuestions:     e.IntakeQuestions,
		AgentVersions:       rawAgentVersions{Intake: e.IntakeVersion},
		Bootstrap:           e.Bootstrap,
	}
	if e.OccurredAtPrecision == PrecisionRange && e.OccurredAtEnd != nil {
		end := e.OccurredAtEnd.Format(time.RFC3339)
		fm.OccurredAtEnd = &end
	}

	var buf bytes.Buffer
	buf.WriteString(fence + "\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(fm); err != nil {
		return nil, fmt.Errorf("storage: encode raw frontmatter: %w", err)
	}
	_ = enc.Close()
	buf.WriteString(fence + "\n\n# Entry\n")
	if body := strings.TrimRight(e.Body, "\n"); body != "" {
		buf.WriteString("\n" + body + "\n")
	}

	content := buf.Bytes()
	if err := ValidateRawFrontmatter(content); err != nil {
		return nil, fmt.Errorf("storage: rendered raw entry failed validation: %w", err)
	}
	return content, nil
}

// rawBaseID renders the minute-precision id for a timestamp
// (raw_YYYY_MM_DD_HH_MM), the starting point before collision suffixes.
func rawBaseID(t time.Time) string {
	return fmt.Sprintf("%s%04d_%02d_%02d_%02d_%02d",
		rawIDPrefix, t.Year(), int(t.Month()), t.Day(), t.Hour(), t.Minute())
}

// rawShardDir returns the raw/YYYY/MM shard directory for a timestamp.
func (a *Adapter) rawShardDir(t time.Time) string {
	return filepath.Join(a.home, rawDirName,
		fmt.Sprintf("%04d", t.Year()), fmt.Sprintf("%02d", int(t.Month())))
}

// rawPath resolves the on-disk path for a raw id (raw/YYYY/MM/<id>.md),
// deriving the shard from the id itself.
func (a *Adapter) rawPath(id string) (string, error) {
	year, month, err := rawShardFromID(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(a.home, rawDirName, year, month, id+rawExt), nil
}

// rawShardFromID extracts the YYYY and MM shard components from a raw id
// of the form raw_YYYY_MM_DD_HH_MM[...].
func rawShardFromID(id string) (year, month string, err error) {
	parts := strings.Split(id, "_")
	if len(parts) < 6 || parts[0] != "raw" {
		return "", "", fmt.Errorf("storage: malformed raw id %q", id)
	}
	return parts[1], parts[2], nil
}

// collisionCandidates yields the ordered id candidates for a base id per
// the same-minute collision rule: the base first, then the
// seconds-suffixed form, then that form with a small monotonic counter
// appended.
func collisionCandidates(base string, sec int) []string {
	out := make([]string, 0, maxSameSecondIDs+2)
	out = append(out, base)
	secID := fmt.Sprintf("%s_%02d", base, sec)
	out = append(out, secID)
	for n := 1; n <= maxSameSecondIDs; n++ {
		out = append(out, fmt.Sprintf("%s_%d", secID, n))
	}
	return out
}

// writeExcl writes content to path only when nothing exists there,
// mirroring the Stat-then-write discipline the scaffold uses. It reports
// (false, nil) when the path is already taken so the caller advances to
// the next id candidate — an existing id is never overwritten
// (data-model.md). This assumes the single-writer local runtime; per-
// session write serialization (error-states.md §St-6) is a later concern.
func writeExcl(path string, content []byte) (wrote bool, err error) {
	if _, statErr := os.Stat(path); statErr == nil {
		return false, nil
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return false, fmt.Errorf("storage: stat %q: %w", path, statErr)
	}
	if err := os.WriteFile(path, content, filePerm); err != nil {
		return false, fmt.Errorf("storage: write %q: %w", path, err)
	}
	return true, nil
}
