package router

import (
	"fmt"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
)

// BackfillRequest carries a `/closeout backfill` — the chain ran but went
// unrecorded (engine-module.md §"/closeout backfill sequence"). Target is
// nil for the default resolution (the most recent logical day without a
// completed record); otherwise it names the day explicitly. Yesterday asks
// the router to resolve the target as the logical day before today (the
// `yesterday` keyword) — computed here against the rollover boundary rather
// than as a naive calendar date, which before the rollover would collide with
// the in-progress day and read as out-of-window. The remaining fields are the
// same compact-form inputs as a close-out.
type BackfillRequest struct {
	Now        time.Time
	Target     *time.Time
	Yesterday  bool
	Links      map[string]string
	Capacity   int
	LimiterTag string
	Journal    string
	Mode       string
	Source     string
	Harness    string
	ChannelID  string
	ThreadID   string
}

// BackfillResult reports what a backfill wrote and its ack. Rejected marks
// an out-of-window target (no disk effect).
type BackfillResult struct {
	DayID       string
	LogicalDate string
	RawID       string
	Created     bool
	Corrected   bool
	Idempotent  bool
	Rejected    bool
	Streak      int
	Ack         string
}

// Backfill executes `/closeout backfill`. It resolves the target day,
// rejects a target beyond backfill_window_days, and then creates a
// backfilled record (or appends a correction to an existing one), writing
// the journal line to raw/ with `command: /closeout backfill`. Retraction
// is arithmetic: a valid backfill folds state and the next status rebuild
// restores the streak; no message is ever unsent (engine-module.md).
func (r *Router) Backfill(req BackfillRequest) (BackfillResult, error) {
	now := whenOr(req.Now)
	loc := now.Location()
	if err := r.prepareEngine(); err != nil {
		return BackfillResult{}, err
	}
	chain, err := r.store.ReadChainConfig()
	if err != nil {
		return BackfillResult{}, err
	}
	state, err := r.store.ReadProfileState()
	if err != nil {
		return BackfillResult{}, err
	}
	clocks, err := chain.ClocksFor(engine.GoverningProfile(now, state.History, loc))
	if err != nil {
		return BackfillResult{}, err
	}
	today := clocks.BaseLogicalDate(now)

	records, err := r.store.ReadEngineDays()
	if err != nil {
		return BackfillResult{}, err
	}

	target := r.backfillTarget(req, records, today, chain.BackfillWindowDays)
	if !engine.BackfillInWindow(target, today, chain.BackfillWindowDays) {
		return BackfillResult{
			Rejected: true,
			Ack: fmt.Sprintf("That's outside the backfill window (%d days). "+
				"The record stands — the budget absorbs it; the Retro can annotate context.",
				chain.BackfillWindowDays),
		}, nil
	}
	return r.applyBackfill(chain, req, target, state, loc)
}

// backfillTarget resolves the target day: the explicit target when given,
// the logical day before today when the `yesterday` keyword was used, and
// otherwise the default (most recent logical day without a completed record
// within the window).
func (r *Router) backfillTarget(req BackfillRequest, records []engine.DayRecord, today time.Time, window int) time.Time {
	if req.Target != nil {
		return engine.DateOf(*req.Target)
	}
	if req.Yesterday {
		return engine.AddDays(today, -1)
	}
	return engine.ResolveBackfillTarget(records, today, window)
}

// applyBackfill creates or corrects the target day's record and rebuilds
// status. A completed target is an idempotent no-op.
func (r *Router) applyBackfill(chain engine.ChainConfig, req BackfillRequest, target time.Time, state engine.ProfileState, loc *time.Location) (BackfillResult, error) {
	dayID := engine.DayID(target)
	existing, found, err := r.store.ReadEngineDayFolded(dayID)
	if err != nil {
		return BackfillResult{}, err
	}
	if found && existing.Completed {
		streak, sErr := r.currentStreak(loc)
		if sErr != nil {
			return BackfillResult{}, sErr
		}
		return BackfillResult{
			DayID: dayID, LogicalDate: existing.LogicalDate, Idempotent: true, Streak: streak,
			Ack: fmt.Sprintf("Already closed out — streak %d.", streak),
		}, nil
	}

	// The chain ran on the target day; occurred_at is that day (approximate),
	// while the entry is recorded now.
	occurred := target.Add(12 * time.Hour)
	rawID, err := r.writeJournal(closeoutReqFrom(req), commandBackfill, occurred, "approximate", nil)
	if err != nil {
		return BackfillResult{}, err
	}

	rec := engine.BuildDayRecord(chain, engine.CloseoutInput{
		LogicalDay: target,
		RecordedAt: whenOr(req.Now),
		Links:      req.Links,
		Capacity:   req.Capacity,
		LimiterTag: req.LimiterTag,
		RawEntryID: rawID,
		Mode:       req.Mode,
		Profile:    engine.GoverningProfile(target, state.History, loc),
		Backfilled: true,
	})

	created := !found
	if found {
		if err = r.store.AppendEngineCorrection(dayID, completingCorrection(rec, whenOr(req.Now))); err != nil {
			return BackfillResult{}, err
		}
	} else if err = r.store.WriteEngineDay(rec); err != nil {
		return BackfillResult{}, err
	}

	status, err := r.store.RebuildEngineStatus(loc)
	if err != nil {
		return BackfillResult{}, err
	}
	return BackfillResult{
		DayID: rec.DayID, LogicalDate: rec.LogicalDate, RawID: rawID,
		Created: created, Corrected: found, Streak: status.CurrentStreak,
		Ack: fmt.Sprintf("Backfilled %s — streak %d.", rec.LogicalDate, status.CurrentStreak),
	}, nil
}

// closeoutReqFrom adapts a backfill request to the journal-writer's
// CloseoutRequest shape (only the journal/routing fields are read).
func closeoutReqFrom(req BackfillRequest) CloseoutRequest {
	return CloseoutRequest{
		Now:       req.Now,
		Journal:   req.Journal,
		Source:    req.Source,
		Harness:   req.Harness,
		ChannelID: req.ChannelID,
		ThreadID:  req.ThreadID,
	}
}
