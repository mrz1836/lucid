package router

import (
	"fmt"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/storage"
)

// Engine command verbs stamped on the journal raw entry (engine-module.md
// §Commands: `command: /closeout`, `command: /closeout backfill`).
const (
	commandCloseout = "/closeout"
	commandBackfill = "/closeout backfill"
)

// User-facing copy that is fixed by the spec (engine-module.md §Error
// states). These are matched verbatim by tests.
const (
	partialAckCopy   = "Saved what we got — floor still counts if the survival link ran."
	profileRejectMsg = "No profile by that name — profiles are defined in chain.json, at a Retro (engine §2)."
)

// CloseoutRequest carries one close-out's gathered inputs (from the guided
// flow or the compact form — they build identical requests). Links maps
// each chain link key to done/floor/skipped; it is empty for Skip.
type CloseoutRequest struct {
	Now            time.Time
	Links          map[string]string
	Capacity       int
	LimiterTag     string
	Journal        string
	Mode           string
	ModeDeclaredAt time.Time
	ForceToday     bool // `/closeout today`
	Skip           bool // `/closeout skip`
	Partial        bool // interrupted flow
	Source         string
	Harness        string
	ChannelID      string
	ThreadID       string
}

// CloseoutResult reports what a close-out wrote and the one-line ack.
type CloseoutResult struct {
	DayID       string
	LogicalDate string
	RawID       string
	Completed   bool
	Missed      bool
	Partial     bool
	Idempotent  bool
	Streak      int
	Ack         string
}

// Closeout executes `/closeout` (engine-module.md §"/closeout sequence").
// It resolves the logical day under the governing clock profile, writes
// the journal line to raw/ via the storage adapter (the Engine module
// never touches raw/ — P3), writes or corrects the engine day record, and
// rebuilds status.json. Idempotent on an already-completed day; an
// interrupted partial is completed by an appended correction. `/closeout
// skip` records an honest miss and writes no journal line.
func (r *Router) Closeout(req CloseoutRequest) (CloseoutResult, error) {
	now := whenOr(req.Now)
	loc := now.Location()
	if err := r.store.ScaffoldEngine(); err != nil {
		return CloseoutResult{}, fmt.Errorf("could not prepare the engine tree: %w", err)
	}
	chain, err := r.store.ReadChainConfig()
	if err != nil {
		return CloseoutResult{}, err
	}

	logicalDay, profileName, err := r.resolveLogicalDay(chain, now, req.ForceToday)
	if err != nil {
		return CloseoutResult{}, err
	}
	dayID := engine.DayID(logicalDay)

	existing, found, err := r.store.ReadEngineDayFolded(dayID)
	if err != nil {
		return CloseoutResult{}, err
	}
	if found && existing.Completed {
		streak, err := r.currentStreak(loc)
		if err != nil {
			return CloseoutResult{}, err
		}
		return CloseoutResult{
			DayID: dayID, LogicalDate: existing.LogicalDate, Completed: true,
			Idempotent: true, Streak: streak,
			Ack: fmt.Sprintf("Already closed out — streak %d.", streak),
		}, nil
	}

	if req.Skip {
		return r.closeoutSkip(chain, req, logicalDay, profileName, found, loc)
	}
	return r.closeoutRecord(chain, req, logicalDay, profileName, found, loc)
}

// resolveLogicalDay picks the logical day a close-out at now answers, under
// the profile governing now's wall date, applying the night-shift back-
// attribution when the previous logical day is unrecorded.
func (r *Router) resolveLogicalDay(chain engine.ChainConfig, now time.Time, forceToday bool) (day time.Time, profile string, err error) {
	state, err := r.store.ReadProfileState()
	if err != nil {
		return time.Time{}, "", err
	}
	profile = engine.GoverningProfile(now, state.History, now.Location())
	clocks, err := chain.ClocksFor(profile)
	if err != nil {
		return time.Time{}, "", err
	}
	base := clocks.BaseLogicalDate(now)
	prevID := engine.DayID(engine.AddDays(base, -1))
	_, prevFound, err := r.store.ReadEngineDay(prevID)
	if err != nil {
		return time.Time{}, "", err
	}
	return clocks.ResolveLogicalDay(now, prevFound, forceToday), profile, nil
}

// closeoutSkip records an explicit miss for the logical day (honest zero).
// It writes no journal line — a miss carries no reflection.
func (r *Router) closeoutSkip(chain engine.ChainConfig, req CloseoutRequest, logicalDay time.Time, profile string, existing bool, loc *time.Location) (CloseoutResult, error) {
	if existing {
		streak, err := r.currentStreak(loc)
		if err != nil {
			return CloseoutResult{}, err
		}
		return CloseoutResult{
			DayID: engine.DayID(logicalDay), LogicalDate: engine.DateString(logicalDay),
			Idempotent: true, Streak: streak,
			Ack: fmt.Sprintf("Already recorded for %s.", engine.DateString(logicalDay)),
		}, nil
	}
	rec := engine.BuildDayRecord(chain, engine.CloseoutInput{
		LogicalDay: logicalDay, RecordedAt: whenOr(req.Now), Profile: profile, Skip: true,
	})
	if err := r.store.WriteEngineDay(rec); err != nil {
		return CloseoutResult{}, err
	}
	status, err := r.store.RebuildEngineStatus(loc)
	if err != nil {
		return CloseoutResult{}, err
	}
	return CloseoutResult{
		DayID: rec.DayID, LogicalDate: rec.LogicalDate, Missed: true, Streak: status.CurrentStreak,
		Ack: fmt.Sprintf("Recorded a miss for %s.", rec.LogicalDate),
	}, nil
}

// closeoutRecord writes (or completes) a full close-out: the journal line
// to raw/, then a fresh day record or a completing correction, then a
// status rebuild.
func (r *Router) closeoutRecord(chain engine.ChainConfig, req CloseoutRequest, logicalDay time.Time, profile string, existing bool, loc *time.Location) (CloseoutResult, error) {
	rawID, err := r.writeJournal(req, commandCloseout, whenOr(req.Now), storage.PrecisionExact, nil)
	if err != nil {
		return CloseoutResult{}, err
	}

	in := engine.CloseoutInput{
		LogicalDay:     logicalDay,
		RecordedAt:     whenOr(req.Now),
		Links:          req.Links,
		Capacity:       req.Capacity,
		LimiterTag:     req.LimiterTag,
		RawEntryID:     rawID,
		Mode:           req.Mode,
		ModeDeclaredAt: formatDeclared(req.ModeDeclaredAt),
		Profile:        profile,
		Partial:        req.Partial,
	}
	rec := engine.BuildDayRecord(chain, in)

	if existing {
		if err = r.store.AppendEngineCorrection(rec.DayID, completingCorrection(rec, whenOr(req.Now))); err != nil {
			return CloseoutResult{}, err
		}
	} else if err = r.store.WriteEngineDay(rec); err != nil {
		return CloseoutResult{}, err
	}

	status, err := r.store.RebuildEngineStatus(loc)
	if err != nil {
		return CloseoutResult{}, err
	}
	return CloseoutResult{
		DayID: rec.DayID, LogicalDate: rec.LogicalDate, RawID: rawID,
		Completed: rec.Completed, Missed: rec.Missed, Partial: rec.Partial,
		Streak: status.CurrentStreak, Ack: closeoutAck(rec, status.CurrentStreak),
	}, nil
}

// completingCorrection folds a prior partial (or otherwise incomplete) day
// record up to the freshly gathered close-out — the "/closeout again
// appends corrections" path (engine-module.md §Error states). Every field
// it names is foldable.
func completingCorrection(rec engine.DayRecord, at time.Time) engine.Correction {
	return engine.Correction{
		At: at.Format(time.RFC3339),
		Fields: map[string]any{
			"links":        rec.Links,
			"floor_day":    rec.FloorDay,
			"completed":    rec.Completed,
			"missed":       rec.Missed,
			"partial":      rec.Partial,
			"capacity":     rec.Capacity,
			"limiter_tag":  rec.LimiterTag,
			"raw_entry_id": rec.RawEntryID,
		},
		Reason: "close-out completed after an interrupted flow",
		Source: "user",
	}
}

// closeoutAck builds the one-line streak ack — no celebration theater
// (engine-module.md step 7). A partial reuses the fixed interrupted-flow
// copy.
func closeoutAck(rec engine.DayRecord, streak int) string {
	if rec.Partial {
		return partialAckCopy
	}
	if rec.Completed {
		return fmt.Sprintf("Closed out %s — streak %d.", rec.LogicalDate, streak)
	}
	return fmt.Sprintf("Recorded %s — the survival link didn't run, so it's a miss.", rec.LogicalDate)
}

// writeJournal writes the close-out journal line as one immutable raw
// entry plus its session record, exactly like /log — the Engine never
// reads it back (P3). occurredEnd is nil except where a range applies.
func (r *Router) writeJournal(req CloseoutRequest, command string, occurredAt time.Time, precision string, occurredEnd *time.Time) (string, error) {
	now := whenOr(req.Now)
	res, err := r.store.WriteRaw(storage.RawEntry{
		RecordedAt:          now,
		OccurredAt:          occurredAt,
		OccurredAtPrecision: precision,
		OccurredAtEnd:       occurredEnd,
		Source:              orDefaultSource(req.Source),
		Command:             command,
		Body:                req.Journal,
	})
	if err != nil {
		return "", fmt.Errorf("could not write the close-out journal line; nothing was saved: %w", err)
	}
	if _, err := r.store.WriteSession(storage.Session{
		ID:            res.SessionID,
		StartedAt:     now,
		EndedAt:       now,
		Harness:       orDefaultSource(req.Harness),
		ChannelID:     req.ChannelID,
		ThreadID:      req.ThreadID,
		Command:       command,
		RawEntryIDs:   []string{res.RawID},
		AgentVersions: r.cfg.AgentVersions,
	}); err != nil {
		return "", fmt.Errorf("could not write the session record for %s: %w", res.RawID, err)
	}
	return res.RawID, nil
}

// currentStreak reads the folded day records and computes the current
// streak without writing — used for the idempotent acks.
func (r *Router) currentStreak(loc *time.Location) (int, error) {
	records, err := r.store.ReadEngineDays()
	if err != nil {
		return 0, err
	}
	return engine.ComputeStreaks(records, loc).Current, nil
}

// Chain returns the chain config, scaffolding the engine tree first so a
// fresh Ledger resolves a default chain rather than erroring. It is the
// read the CLI uses to parse the compact close-out form against the
// chain's link order.
func (r *Router) Chain() (engine.ChainConfig, error) {
	if err := r.store.ScaffoldEngine(); err != nil {
		return engine.ChainConfig{}, err
	}
	return r.store.ReadChainConfig()
}

// whenOr returns t, or the wall clock when t is zero.
func whenOr(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now()
	}
	return t
}

// formatDeclared renders a mode-declaration timestamp, or "" when unset.
func formatDeclared(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}
