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
// ChannelID, and ThreadID are supplied by the harness that received the
// command; a zero Now defaults to the wall clock and an empty
// Source/Harness defaults to the local CLI surface, so a bare CLI call
// still produces a well-formed entry.
type LogRequest struct {
	Text      string
	Now       time.Time
	Source    string
	Harness   string
	ChannelID string
	ThreadID  string
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
	source := orDefault(req.Source, sourceDefault)

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
			"could not write the raw entry (out of disk space or permission denied?); nothing was saved: %w", err)
	}

	if _, err := r.store.WriteSession(storage.Session{
		ID:            res.SessionID,
		StartedAt:     now,
		EndedAt:       now,
		Harness:       orDefault(req.Harness, sourceDefault),
		ChannelID:     req.ChannelID,
		ThreadID:      req.ThreadID,
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

// orDefault returns v, or def when v is empty.
func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
