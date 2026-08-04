package router

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/storage"
)

// commandAttach is the verb stamped on the raw entry an attach emits.
const commandAttach = "/attach"

// shaAckPrefixLen bounds the sha256 shown in the human ack. The full hash
// rides the sidecar and the --json result; the ack shows a short prefix so the
// confirmation stays legible (data-model.md renders sha256 the same way).
const shaAckPrefixLen = 12

// AttachRequest carries the inputs for one `lucid attach` turn. Path is the
// file being attached; Caption is the optional verbatim description (stored
// as-is and used to derive the filename slug). DayArg is the optional `--day`
// token (`@yesterday` / `@YYYY-MM-DD`, or bare forms); an empty value attaches
// to the current logical day under the 04:00 rollover. Now, Source, Harness,
// and ChannelID are harness provenance supplied by the surface that received
// the command — a zero Now defaults to the wall clock and an empty
// Source/Harness defaults to the local CLI surface, so a bare CLI call still
// produces a well-formed attachment.
type AttachRequest struct {
	Path      string
	Caption   string
	DayArg    string
	Now       time.Time
	Source    string
	Harness   string
	ChannelID string
}

// AttachResult reports what an attach wrote and the acknowledgement to show
// the user. StoredPath is the absolute path to the stored binary; SHA256 is
// its integrity hash; Day is the logical day it was attributed to; RawID is
// the linked immutable raw entry; Caption echoes what was captured.
type AttachResult struct {
	StoredPath string
	SHA256     string
	Day        string
	RawID      string
	Caption    string
	Ack        string
}

// Attach executes the `lucid attach` command: it copies req.Path into the
// ~/.lucid/media/ store (hashed, sidecar-recorded), then emits one immutable
// raw entry (plus its session record, mirroring [Router.Log]) whose body
// references the stored media so the day view and the Mirror can find it. It
// is deterministic and agent-free — no model runs in the write path
// (architecture P3): the copy, the sha256, and the metadata are mechanical.
//
// The media is written first so the raw entry can reference the real stored
// path; the raw id the sidecar links to is derived from req.Now at minute
// precision (the documented raw-id form), which the single-writer local
// runtime — the same assumption storage's exclusive-write discipline makes —
// resolves to exactly the id [storage.Adapter.WriteRaw] then assigns. A
// malformed source/harness/day, or an unreadable file, is rejected before
// anything is written (error-states.md §St-1), so a bad turn leaves nothing on
// disk.
func (r *Router) Attach(req AttachRequest) (AttachResult, error) {
	now := whenOr(req.Now)

	path := strings.TrimSpace(req.Path)
	if path == "" {
		return AttachResult{}, fmt.Errorf("attach needs a file path; nothing was saved")
	}

	// Resolve the provenance tokens and the target day before writing anything:
	// a malformed value must leave nothing on disk (error-states.md §St-1), so
	// each is validated up front, ahead of the media copy that lands first.
	source, err := resolveSource(req.Source)
	if err != nil {
		return AttachResult{}, fmt.Errorf("invalid source; nothing was saved: %w", err)
	}
	harness, err := resolveSource(req.Harness)
	if err != nil {
		return AttachResult{}, fmt.Errorf("invalid harness; nothing was saved: %w", err)
	}
	when, err := resolveCaptureWhen(req.DayArg, now)
	if err != nil {
		return AttachResult{}, err
	}
	day := when.LogicalDate

	// The sidecar links to the raw entry emitted alongside it; the raw id is
	// derived from now (minute precision), so it is known before the media is
	// stored. WriteMedia reads the source file, so a missing/unreadable path
	// surfaces here with nothing written (error-states.md §St-1).
	rawID := predictRawID(now)
	rec, err := r.store.WriteMedia(storage.MediaAttachment{
		SourcePath:       path,
		OriginalFilename: filepath.Base(path),
		CapturedAt:       now,
		LogicalDay:       day,
		Caption:          req.Caption,
		Source:           source,
		RawEntryID:       rawID,
	})
	if err != nil {
		return AttachResult{}, fmt.Errorf("could not store the media; nothing was saved: %w", err)
	}

	relPath := r.mediaRelPath(rec)
	res, err := r.store.WriteRaw(storage.RawEntry{
		RecordedAt:          now,
		OccurredAt:          when.OccurredAt,
		OccurredAtPrecision: when.Precision,
		OccurredAtEnd:       when.End,
		Source:              source,
		Command:             commandAttach,
		Bootstrap:           r.cfg.BootstrapMode,
		Body:                attachBody(relPath, req.Caption),
	})
	if err != nil {
		return AttachResult{}, fmt.Errorf(
			"could not write the raw entry (out of disk space or permission denied?): %w", err,
		)
	}

	if _, err := r.store.WriteSession(storage.Session{
		ID:            res.SessionID,
		StartedAt:     now,
		EndedAt:       now,
		Harness:       harness,
		ChannelID:     req.ChannelID,
		Command:       commandAttach,
		RawEntryIDs:   []string{res.RawID},
		AgentVersions: r.cfg.AgentVersions,
	}); err != nil {
		return AttachResult{}, fmt.Errorf("could not write the session record for %s: %w", res.RawID, err)
	}

	return AttachResult{
		StoredPath: rec.StoredPath,
		SHA256:     rec.SHA256,
		Day:        day,
		RawID:      res.RawID,
		Caption:    req.Caption,
		Ack:        attachAck(relPath, day, rec.SHA256, res.RawID),
	}, nil
}

// resolveCaptureWhen resolves one capture's occurrence from the optional
// `--day` token: the instant, its precision, the end a clock range yields, and
// the logical day the entry files under. It is the capture verbs' entry into
// the strict tier — `lucid log`, `lucid attach`, `lucid memory`, and
// `lucid memory --attach` all resolve `--day` here, so there is one rule to
// learn and one rollover boundary, never a second clock.
//
// The grammar itself lives in [observations.ResolveDay] (usage/commands.md
// §"Backdating with --day"), which owns every accepted form, the 04:00
// rollover on the relative word, the literal reading of an explicit date, and
// the future ceiling. What this function adds is the capture verbs' own copy:
// an unreadable token or a day that has not happened yet is a clean error
// naming what was wrong, and nothing is written (error-states.md §St-1). The
// realistic typo — a wrong year, transposed digits — is caught here rather
// than filing an entry silently far in the future.
//
// The refusal is a [DayRejectedError], the same type the Engine verbs' side
// returns, so a capture surface can tell a fixed reason the user should read
// from a runtime fault and print it — [cli.Execute] renders no returned error,
// so a refusal nobody prints reaches the user as a bare non-zero exit.
func resolveCaptureWhen(dayArg string, now time.Time) (observations.DayResolution, error) {
	res, err := observations.ResolveDay(dayArg, now, observations.DayOptions{AllowPartial: true})
	switch {
	case errors.Is(err, observations.ErrDayFuture):
		// The ceiling stays in ResolveDay; this second pass only recovers the
		// day the value resolved to, so the refusal can name it.
		future, _ := observations.ResolveDay(dayArg, now, observations.DayOptions{
			AllowFuture: true, AllowPartial: true,
		})
		return observations.DayResolution{}, rejectDay(
			"cannot capture against %s — that day has not happened yet; nothing was saved", future.LogicalDate,
		)
	case err != nil:
		// Self-contained rather than wrapped: the sentinel already carries the
		// value and the accepted forms, and repeating them reads as a stutter.
		return observations.DayResolution{}, rejectDay(
			"could not read the day %q (want %s); nothing was saved", dayArg, observations.AcceptedDayForms,
		)
	}
	return res, nil
}

// resolveCaptureDay is the day-level view of [resolveCaptureWhen] — the
// instant, its precision, and the logical day, without the range end. It
// states the capture verbs' day contract in the three fields every dated
// record needs, and is the shape the backdating branches are covered against.
func resolveCaptureDay(dayArg string, now time.Time) (occ time.Time, precision, day string, err error) {
	res, err := resolveCaptureWhen(dayArg, now)
	if err != nil {
		return time.Time{}, "", "", err
	}
	return res.OccurredAt, res.Precision, res.LogicalDate, nil
}

// predictRawID renders the minute-precision raw id for a capture instant
// (raw_YYYY_MM_DD_HH_MM, data-model.md §"Raw entries"). Attach derives it up
// front so the media sidecar can link to the raw entry it is emitted alongside
// before that entry is written. Under the single-writer local runtime — the
// same assumption the storage adapter's exclusive-write discipline relies on —
// this equals the id WriteRaw then assigns; a separate capture already
// occupying the same minute would shift WriteRaw's id, and the day view still
// joins media by logical day rather than by chasing this link.
func predictRawID(now time.Time) string {
	return fmt.Sprintf("raw_%04d_%02d_%02d_%02d_%02d",
		now.Year(), int(now.Month()), now.Day(), now.Hour(), now.Minute())
}

// mediaRelPath renders the media store-relative path of a stored record
// (media/YYYY/MM/<id>) for the raw body and the ack. It falls back to the
// stored id if the record is somehow outside the Ledger home.
func (r *Router) mediaRelPath(rec storage.MediaRecord) string {
	rel, err := filepath.Rel(r.store.Home(), rec.StoredPath)
	if err != nil {
		return rec.ID
	}
	return rel
}

// attachBody builds the raw entry body: a reference to the stored media path
// plus the verbatim caption, so the entry stands in for the binary in the
// Mirror/Retro. It is inventory, not evaluation — no score, no judgment.
func attachBody(relPath, caption string) string {
	if strings.TrimSpace(caption) == "" {
		return "Media attachment " + relPath
	}
	return "Media attachment " + relPath + " — " + caption
}

// attachAck builds the user-facing acknowledgement, emitted only after the
// writes land (provenance over magic): it names the stored path, a short
// sha256, the logical day, and the linked raw id.
func attachAck(relPath, day, sha, rawID string) string {
	return fmt.Sprintf("Attached %s for %s (sha256 %s) — logged as `%s`.",
		relPath, day, shortSHA(sha), rawID)
}

// shortSHA returns a legible prefix of a sha256 hex digest for the ack.
func shortSHA(sha string) string {
	if len(sha) <= shaAckPrefixLen {
		return sha
	}
	return sha[:shaAckPrefixLen] + "…"
}
