package router

import (
	"fmt"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/storage"
)

// Capture command verbs and the default local source/harness used when a
// caller does not supply one.
const (
	commandLog    = "/log"
	sourceDefault = "cli"
)

// LogRequest carries the inputs for one /log turn. Now, Source, Harness,
// ChannelID, ThreadID, Agent, and Model are supplied by the harness that
// received the command; a zero Now defaults to the wall clock and an empty
// Source/Harness defaults to the local CLI surface, so a bare CLI call
// still produces a well-formed entry. A non-empty Source/Harness is
// normalized through the shared harness-token grammar; Agent and Model are
// optional provenance recorded on the session record when supplied.
type LogRequest struct {
	Text      string
	Now       time.Time
	Source    string
	Harness   string
	ChannelID string
	ThreadID  string
	Agent     string
	Model     string
}

// LogResult reports what a /log turn wrote and the acknowledgement to
// show the user.
type LogResult struct {
	RawID     string
	SessionID string
	EmptyBody bool
	Ack       string
}

// Log executes the /log command: it writes one immutable raw entry
// capturing req.Text plus the session record that frames it, then returns
// the ack. It is capture-only — Safety/Consent is pass-only at this stage
// (the text is stored verbatim), no Structuring runs, and nothing is
// written under processed/ or insights/ (acceptance-criteria.md Phase 2).
// The raw entry is written first, so a write failure (error-states.md
// §St-1) leaves nothing on disk and is safe to retry.
func (r *Router) Log(req LogRequest) (LogResult, error) {
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}

	// Resolve the provenance tokens before writing anything: a malformed
	// source/harness must leave nothing on disk (error-states.md §St-1), so
	// both are validated up front, ahead of the raw write that lands first.
	source, err := resolveSource(req.Source)
	if err != nil {
		return LogResult{}, fmt.Errorf("invalid source; nothing was saved: %w", err)
	}
	harness, err := resolveSource(req.Harness)
	if err != nil {
		return LogResult{}, fmt.Errorf("invalid harness; nothing was saved: %w", err)
	}

	res, err := r.store.WriteRaw(storage.RawEntry{
		RecordedAt:          now,
		OccurredAt:          now,
		OccurredAtPrecision: storage.PrecisionExact,
		Source:              source,
		Command:             commandLog,
		Bootstrap:           false,
		Body:                req.Text,
	})
	if err != nil {
		return LogResult{}, fmt.Errorf(
			"could not write the raw entry (out of disk space or permission denied?); nothing was saved: %w", err,
		)
	}

	if _, err := r.store.WriteSession(storage.Session{
		ID:            res.SessionID,
		StartedAt:     now,
		EndedAt:       now,
		Harness:       harness,
		ChannelID:     req.ChannelID,
		ThreadID:      req.ThreadID,
		Agent:         req.Agent,
		Model:         req.Model,
		Command:       commandLog,
		RawEntryIDs:   []string{res.RawID},
		AgentVersions: r.cfg.AgentVersions,
	}); err != nil {
		return LogResult{}, fmt.Errorf("could not write the session record for %s: %w", res.RawID, err)
	}

	empty := strings.TrimSpace(req.Text) == ""
	return LogResult{
		RawID:     res.RawID,
		SessionID: res.SessionID,
		EmptyBody: empty,
		Ack:       logAck(res.RawID, empty),
	}, nil
}

// logAck builds the user-facing acknowledgement for a saved /log entry.
// An empty body is noted honestly (error-states.md §S-3) without
// promising Structuring, which does not run for /log at this stage.
func logAck(rawID string, emptyBody bool) string {
	if emptyBody {
		return fmt.Sprintf("Saved as `%s` — looks like the body was empty.", rawID)
	}
	return fmt.Sprintf("Saved as `%s`.", rawID)
}

// orDefaultSource returns v, or the local CLI source when v is empty. It
// backs the source/harness defaulting shared by the capture commands so a
// bare CLI call still produces a well-formed entry.
func orDefaultSource(v string) string {
	if v == "" {
		return sourceDefault
	}
	return v
}

// resolveSource resolves a /log source or harness token: an empty value takes
// the local CLI default, and a non-empty value is normalized through the
// shared harness-token grammar (storage.NormalizeSource) so a malformed token
// is rejected honestly rather than silently coerced to cli. It backs both the
// source and harness fields — the two "harness identifier" tokens — and
// returns an error the caller surfaces before anything is written.
func resolveSource(raw string) (string, error) {
	if raw == "" {
		return orDefaultSource(raw), nil
	}
	return storage.NormalizeSource(raw)
}
