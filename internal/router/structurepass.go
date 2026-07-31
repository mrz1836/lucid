package router

import (
	"context"
	"fmt"
	"time"

	"github.com/mrz1836/lucid/internal/provider"
)

// structurepass.go is the standalone Structuring seam: it reaches
// [Router.Structure] for an entry this run did not capture, either one id or a
// whole civil-date window. Structuring is a downstream pass over every raw
// entry — /log and /closeout journal lines included (agent-contracts.md §"How
// contracts compose") — so the pass applies no other filter; which captures are
// worth distilling is a reader's judgment, not the router's.
//
// The pass itself is only resolution, policy, and counting. The distilling is
// [Router.Structure], called unmodified, so a standalone run and the run inside
// a check-in session produce the same artifact by the same code.

// structureWindowDateLayout is the civil-date form the window bounds are
// rendered in when handed to the storage enumerator and reported back to the
// caller.
const structureWindowDateLayout = "2006-01-02"

// The per-entry outcomes a pass reports. They are a single label, not a
// counter: an entry that degraded while still writing an artifact reports
// [StructureOutcomeDegraded] — the more informative of the two — while counting
// in both StructurePassResult.Wrote and StructurePassResult.Degraded. The
// precedence is failed, then degraded, then wrote, then skipped.
const (
	StructureOutcomeWrote    = "wrote"
	StructureOutcomeSkipped  = "skipped"
	StructureOutcomeDegraded = "degraded"
	StructureOutcomeFailed   = "failed"
)

// StructurePassRequest carries the inputs for one standalone Structuring pass.
// RawID selects single-entry mode; leaving it empty selects ranged mode over
// the inclusive civil days [Since, Until]. Force re-structures an entry that
// already has a processed artifact, which is otherwise skipped without spending
// a model call.
type StructurePassRequest struct {
	// RawID names the one entry to structure; empty selects ranged mode.
	RawID string
	// Since is the first civil day of the window, inclusive (ranged mode).
	Since time.Time
	// Until is the last civil day of the window, inclusive (ranged mode).
	Until time.Time
	// Force re-structures an entry that already has a processed artifact.
	Force bool
	// Now stamps the artifacts; zero means the wall clock.
	Now time.Time
	// Provider is the model seam Structuring extracts through.
	Provider provider.Provider
	// Progress, when non-nil, is called once per entry — skips included — with
	// the running index, the total, the id, and the entry's outcome, so a long
	// window can show forward motion. It is called on the calling goroutine.
	Progress func(done, total int, rawID, outcome string)
}

// StructurePassEntry is what one raw entry in the pass turned into.
type StructurePassEntry struct {
	RawID   string
	Outcome string
}

// StructurePassResult reports what a pass did. Wrote and Degraded overlap on
// purpose: a degraded extraction still produces an artifact (error-states.md
// §S-5/§St-3 are the two paths that do not), so counting them separately is the
// honest reading of [StructureResult]. Failed counts an entry the pass could
// not read at all — an infrastructure fault, not a degrade.
type StructurePassResult struct {
	// Ranged is true when the pass ran over a window rather than one id.
	Ranged bool
	// Since and Until are the window bounds as civil dates, empty in
	// single-entry mode.
	Since string
	Until string
	// Attempted counts the entries handed to Structuring — skips are not.
	Attempted int
	Wrote     int
	Skipped   int
	Degraded  int
	Failed    int
	// Entries is one record per resolved id, in the order they were passed.
	Entries []StructurePassEntry
}

// StructurePass runs Structuring over one raw entry or a civil-date window of
// them, without capturing anything new. It resolves the id list (a single id,
// or every raw entry in the inclusive window whatever command captured it),
// skips any entry that already has a processed artifact unless req.Force, and
// hands the rest to [Router.Structure] one at a time, accumulating the counts.
//
// A per-entry failure in a window run is recorded and the pass continues: a
// window must be able to finish and be re-run cheaply, which aborting halfway
// would defeat. In single-entry mode there is no remaining window to protect,
// so the read fault surfaces verbatim alongside the counted failure. Because an
// already-structured entry is skipped without a model call, an interrupted run
// resumes for free and a repeat run over a settled window spends nothing.
//
// Cancellation is honored between entries: the pass returns the partial result
// alongside the context error, so a canceled sweep still reports what landed.
func (r *Router) StructurePass(ctx context.Context, req StructurePassRequest) (StructurePassResult, error) {
	res := StructurePassResult{Ranged: req.RawID == ""}
	if res.Ranged {
		res.Since = req.Since.Format(structureWindowDateLayout)
		res.Until = req.Until.Format(structureWindowDateLayout)
	}

	ids, err := r.structurePassIDs(req, res.Since, res.Until)
	if err != nil {
		return res, err
	}
	processed, err := r.processedIDSet()
	if err != nil {
		return res, err
	}

	now := whenOr(req.Now)
	res.Entries = make([]StructurePassEntry, 0, len(ids))
	for i, id := range ids {
		if cerr := ctx.Err(); cerr != nil {
			return res, cerr
		}
		out, perr := r.structurePassEntry(ctx, id, req, processed, now)
		res.count(out)
		res.Entries = append(res.Entries, StructurePassEntry{RawID: id, Outcome: out.label})
		if req.Progress != nil {
			req.Progress(i+1, len(ids), id, out.label)
		}
		if perr != nil && !res.Ranged {
			return res, perr
		}
	}
	return res, nil
}

// structurePassIDs resolves the ids the pass will run over: the one requested
// id, or every raw entry the enumerator finds in the window. It applies no
// other filter (agent-contracts.md §"How contracts compose").
func (r *Router) structurePassIDs(req StructurePassRequest, since, until string) ([]string, error) {
	if req.RawID != "" {
		return []string{req.RawID}, nil
	}
	ids, err := r.store.ListRawIDsInRange(since, until)
	if err != nil {
		return nil, fmt.Errorf("structure pass: list raw entries: %w", err)
	}
	return ids, nil
}

// processedIDSet reads the ids of every processed artifact once, so the
// skip check costs one directory read for the whole pass rather than a stat
// per entry.
func (r *Router) processedIDSet() (map[string]bool, error) {
	ids, err := r.store.ListProcessedIDs()
	if err != nil {
		return nil, fmt.Errorf("structure pass: list processed artifacts: %w", err)
	}
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set, nil
}

// structureOutcome is one entry's contribution to the pass: the label to
// report, and which counters it moves. The label and the counters are separate
// because they answer different questions — an entry that degraded while still
// writing reports one label but moves two counters.
type structureOutcome struct {
	label     string
	attempted bool
	wrote     bool
	degraded  bool
	failed    bool
}

// structurePassEntry runs one entry through the skip check and, when it is not
// skipped, through the unmodified [Router.Structure]. A read failure is an
// infrastructure fault (Structure surfaces it as an error) and counts as
// failed; the two honest degrade paths return no error and are read off the
// result. The error is returned alongside the outcome so single-entry mode can
// surface it verbatim while a window run only counts it.
func (r *Router) structurePassEntry(
	ctx context.Context, id string, req StructurePassRequest, processed map[string]bool, now time.Time,
) (structureOutcome, error) {
	if processed[id] && !req.Force {
		return structureOutcome{label: StructureOutcomeSkipped}, nil
	}

	sres, err := r.Structure(ctx, StructureRequest{RawID: id, Now: now, Provider: req.Provider})
	if err != nil {
		return structureOutcome{label: StructureOutcomeFailed, attempted: true, failed: true}, err
	}

	out := structureOutcome{attempted: true, wrote: sres.Wrote, degraded: sres.Degraded}
	switch {
	case sres.Degraded:
		out.label = StructureOutcomeDegraded
	case sres.Wrote:
		out.label = StructureOutcomeWrote
	default:
		// Structure returns an error, a degrade, or a write; a result that is
		// none of the three would be a silent no-op, so report it as failed
		// rather than let it read as success.
		out.label = StructureOutcomeFailed
		out.failed = true
	}
	return out, nil
}

// count folds one entry's outcome into the pass counters.
func (res *StructurePassResult) count(out structureOutcome) {
	if out.attempted {
		res.Attempted++
	}
	if out.wrote {
		res.Wrote++
	}
	if out.degraded {
		res.Degraded++
	}
	if out.failed {
		res.Failed++
	}
	if out.label == StructureOutcomeSkipped {
		res.Skipped++
	}
}
