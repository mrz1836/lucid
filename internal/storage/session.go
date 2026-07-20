package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/mrz1836/lucid/internal/config"
)

// sessionExt is the file extension for session records.
const sessionExt = ".json"

// Session is a capture's audit record (data-model.md §"Session record").
// Reading the most recent N sessions gives Reflection its recent window
// without walking the whole processed/ tree.
type Session struct {
	// ID is the session id. When set, WriteSession writes at exactly this
	// id (used by /log, which derives it from the raw id so the two
	// suffixes match). When empty, WriteSession derives and collision-
	// resolves an id from StartedAt.
	ID string
	// StartedAt is when the session/thread opened; it also seeds a derived id.
	StartedAt time.Time
	// EndedAt is when the session closed (zero for an open session).
	EndedAt time.Time
	// Harness is the surface that hosted the session (openclaw, cli, ...).
	Harness string
	// ChannelID and ThreadID locate the conversation.
	ChannelID string
	ThreadID  string
	// Agent and Model name the assistant/agent and model that relayed the
	// capture, when a harness supplies them. Both are optional and additive:
	// they are empty (and omitted on disk) for a plain terminal capture. With
	// Harness/ChannelID/ThreadID they form the record's provenance cluster
	// (data-model.md §"Session record").
	Agent string
	Model string
	// Command is the verb that opened the session.
	Command string
	// RawEntryIDs, ProcessedArtifactIDs, InsightIDs are the audit links.
	RawEntryIDs          []string
	ProcessedArtifactIDs []string
	InsightIDs           []string
	// RejectedProposalCount tracks proposals the user declined this session.
	RejectedProposalCount int
	// AgentVersions stamps which agent versions were current for the session.
	AgentVersions config.AgentVersions
}

// sessionRecord is the on-disk JSON shape; field order matches
// data-model.md §"Session record".
type sessionRecord struct {
	ID                    string               `json:"id"`
	StartedAt             string               `json:"started_at"`
	EndedAt               string               `json:"ended_at"`
	Harness               string               `json:"harness"`
	ChannelID             string               `json:"channel_id"`
	ThreadID              string               `json:"thread_id"`
	Agent                 string               `json:"agent,omitempty"`
	Model                 string               `json:"model,omitempty"`
	Command               string               `json:"command"`
	RawEntryIDs           []string             `json:"raw_entry_ids"`
	ProcessedArtifactIDs  []string             `json:"processed_artifact_ids"`
	InsightIDs            []string             `json:"insight_ids"`
	RejectedProposalCount int                  `json:"rejected_proposal_count"`
	AgentVersions         config.AgentVersions `json:"agent_versions"`
}

// WriteSession writes the session record s under sessions/<id>.json and
// returns the id written. When s.ID is set it writes at exactly that id
// (never overwriting an existing one — data-model.md); when it is empty
// it derives a minute-precision id from s.StartedAt and applies the
// same-minute collision rule. It never overwrites an existing session.
func (a *Adapter) WriteSession(s Session) (string, error) {
	if s.StartedAt.IsZero() {
		return "", errors.New("storage: session started_at is required")
	}
	dir := filepath.Join(a.home, sessionsDirName)
	if err := ensureDir(dir, "sessions"); err != nil {
		return "", err
	}
	if s.ID != "" {
		return a.writeSessionAt(dir, s)
	}
	return a.allocateSession(dir, s)
}

// writeSessionAt writes a session at its explicit id. A pre-existing id
// is an error, not a silent advance: the raw entry that produced this id
// already references it, so writing elsewhere would dangle that link.
func (a *Adapter) writeSessionAt(dir string, s Session) (string, error) {
	content, err := marshalSession(s)
	if err != nil {
		return "", err
	}
	wrote, err := writeExcl(filepath.Join(dir, s.ID+sessionExt), content)
	if err != nil {
		return "", err
	}
	if !wrote {
		return "", fmt.Errorf("storage: session id %q already exists", s.ID)
	}
	return s.ID, nil
}

// allocateSession derives a collision-free id from StartedAt and writes
// the session under it.
func (a *Adapter) allocateSession(dir string, s Session) (string, error) {
	base := sessionBaseID(s.StartedAt)
	for _, id := range collisionCandidates(base, s.StartedAt.Second()) {
		s.ID = id
		content, err := marshalSession(s)
		if err != nil {
			return "", err
		}
		wrote, err := writeExcl(filepath.Join(dir, id+sessionExt), content)
		if err != nil {
			return "", err
		}
		if wrote {
			return id, nil
		}
	}
	return "", fmt.Errorf("storage: could not allocate a unique session id for %s", base)
}

// marshalSession renders a session as the exact indented JSON written to
// disk, with a trailing newline. Nil id-slices are normalized to empty
// arrays so the on-disk shape is stable.
func marshalSession(s Session) ([]byte, error) {
	rec := sessionRecord{
		ID:                    s.ID,
		StartedAt:             s.StartedAt.Format(time.RFC3339),
		EndedAt:               formatOptionalTime(s.EndedAt),
		Harness:               s.Harness,
		ChannelID:             s.ChannelID,
		ThreadID:              s.ThreadID,
		Agent:                 s.Agent,
		Model:                 s.Model,
		Command:               s.Command,
		RawEntryIDs:           orEmpty(s.RawEntryIDs),
		ProcessedArtifactIDs:  orEmpty(s.ProcessedArtifactIDs),
		InsightIDs:            orEmpty(s.InsightIDs),
		RejectedProposalCount: s.RejectedProposalCount,
		AgentVersions:         s.AgentVersions,
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("storage: marshal session: %w", err)
	}
	return append(b, '\n'), nil
}

// sessionBaseID renders the minute-precision id for a timestamp
// (session_YYYY_MM_DD_HH_MM).
func sessionBaseID(t time.Time) string {
	return fmt.Sprintf("%s%04d_%02d_%02d_%02d_%02d",
		sessionIDPrefix, t.Year(), int(t.Month()), t.Day(), t.Hour(), t.Minute())
}

// formatOptionalTime renders t as RFC3339, or "" when t is the zero time
// (an open session with no ended_at).
func formatOptionalTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

// orEmpty returns s, or an empty (non-nil) slice when s is nil, so JSON
// renders `[]` rather than `null`.
func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
