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
// optional provenance recorded on the session record when supplied. DayArg
// is the optional `--day` token (`@yesterday` / `@YYYY-MM-DD`, or the bare
// forms) resolved through the backdating grammar the capture verbs share; an
// empty value records the entry at now, at exact precision, under the 04:00
// rollover.
type LogRequest struct {
	Text      string
	Now       time.Time
	DayArg    string
	Source    string
	Harness   string
	ChannelID string
	ThreadID  string
	Agent     string
	Model     string
}

// LogResult reports what a /log turn wrote and the acknowledgement to
// show the user. Day is the logical day the entry was attributed to, always
// populated — including the rollover-aware value on the no-flag path. It is
// an in-process result field, not a record field: nothing new is persisted
// for it.
type LogResult struct {
	RawID     string
	SessionID string
	Day       string
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
	// The optional --day rides the same resolver attach uses, so there is one
	// backdating grammar across the capture verbs. Its error already reads
	// honestly ("nothing was saved"), so it is surfaced verbatim rather than
	// re-wrapped.
	occ, precision, day, err := resolveCaptureDay(req.DayArg, now)
	if err != nil {
		return LogResult{}, err
	}

	res, err := r.store.WriteRaw(storage.RawEntry{
		RecordedAt:          now,
		OccurredAt:          occ,
		OccurredAtPrecision: precision,
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
	ack := logAck(res.RawID, empty)
	if strings.TrimSpace(req.DayArg) != "" {
		ack = logAckForDay(res.RawID, day, empty)
	}
	return LogResult{
		RawID:     res.RawID,
		SessionID: res.SessionID,
		Day:       day,
		EmptyBody: empty,
		Ack:       ack,
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

// logAckForDay is [logAck] for a backdated capture: it names the day the
// entry was attributed to, so a --day that resolved to something other than
// today confirms itself rather than succeeding silently (provenance over
// magic, the same way the attach ack names its day). It is a sibling rather
// than an extra parameter on logAck so the no-flag ack cannot change by
// construction: the caller reaches this function only when --day was
// supplied.
func logAckForDay(rawID, day string, emptyBody bool) string {
	if emptyBody {
		return fmt.Sprintf("Saved as `%s` for %s — looks like the body was empty.", rawID, day)
	}
	return fmt.Sprintf("Saved as `%s` for %s.", rawID, day)
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
