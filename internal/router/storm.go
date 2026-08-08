package router

import (
	"fmt"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
)

// Fixed /storm user copy (engine-module.md §Commands, §Error states), matched
// verbatim by tests.
const (
	stormUnknownLabelMsg = "No clause or window by that name — clauses live in the Charter and are registered in storm.json. `/storm unwritten` if life exceeded the list."
	stormRenewTwiceMsg   = "A storm renews once (engine §4). Past that, this is a season, not a storm — it goes to the quarterly."
	stormNoStandingMsg   = "No standing storm to end — expiry at the duration bound is automatic."
	stormUsageMsg        = "Usage: /storm <clause-label|unwritten> or /storm end."
	stormOutOfOrderMsg   = "That's before the last recorded storm event — storm history is append-only and in order."
)

// StormRequest carries a `/storm` command: the label (or `end`), the instant
// the command ran, and an optional DayArg that dates the event itself
// (engine-module.md §"Backdated storm events"). An empty DayArg records the
// event now, exactly as it always has.
type StormRequest struct {
	Arg    string
	DayArg string
	Now    time.Time
}

// StormResult reports a `/storm` command. Event is the appended history event
// kind on success (declared|renewed|ended); Rejected marks a no-op rejection
// with the fixed copy in Ack.
type StormResult struct {
	Event    string
	Label    string
	Through  string
	Rejected bool
	Ack      string
}

// Storm executes a now-dated `/storm` command — the shape every caller that
// never backdates wants. It is a thin wrapper over [Router.StormCommand].
func (r *Router) Storm(arg string, now time.Time) (StormResult, error) {
	return r.StormCommand(StormRequest{Arg: arg, Now: now})
}

// StormCommand executes `/storm <label|unwritten> [--day]` and `/storm end
// [--day]` (engine-module.md §Commands). A `/storm <label>` re-issued while a
// storm stands is a renewal (allowed once); otherwise it is a fresh declaration
// that stands only on witness confirmation within 72 hours. Clause labels are
// opaque — the words live in the Charter; storm.json holds labels and dates
// only — so an unknown label is rejected. Every accepted command appends to
// storm.json's history and rebuilds status.json.
//
// `--day` dates the event itself, on all three verbs. The case it serves is the
// honest one: the days a storm is worst are precisely the days declaring it at
// the time was not possible. The resolved instant becomes the event's `at`,
// `through` derives from it as usual, and standing is evaluated *at that
// instant* — so a renew or end dated to a moment when no storm stood is
// rejected by the existing checks, unchanged, and renew-once still holds.
//
// One invariant is added for it: history folds in order and the standing
// computation reads the last matching event, so an event dated before the last
// recorded one is rejected as incoherent rather than silently corrupting the
// fold. The guard is unconditional because it is an invariant, not a flag
// behavior — without `--day` the event is dated now and nothing recorded can
// follow it.
func (r *Router) StormCommand(req StormRequest) (StormResult, error) {
	now := whenOr(req.Now)
	loc := now.Location()
	if err := r.prepareEngine(); err != nil {
		return StormResult{}, err
	}
	h, err := r.store.ReadStormState()
	if err != nil {
		return StormResult{}, err
	}

	arg := strings.TrimSpace(req.Arg)
	if arg == "" {
		return StormResult{Rejected: true, Ack: stormUsageMsg}, nil
	}
	at, err := r.stormInstant(req, now)
	if err != nil {
		return StormResult{}, err
	}
	if last, ok := lastStormEventAt(h); ok && at.Before(last) {
		return StormResult{Rejected: true, Ack: stormOutOfOrderMsg}, nil
	}

	if arg == "end" {
		return r.stormEnd(h, at, loc)
	}
	if standing, _ := engine.StormStanding(h, at, loc); standing {
		return r.stormRenew(h, arg, at, loc)
	}
	return r.stormDeclare(h, arg, at, loc)
}

// stormInstant resolves the instant a storm event records: now, or the strict
// resolution of `--day` when one was given.
func (r *Router) stormInstant(req StormRequest, now time.Time) (time.Time, error) {
	if strings.TrimSpace(req.DayArg) == "" {
		return now, nil
	}
	chain, err := r.store.ReadChainConfig()
	if err != nil {
		return time.Time{}, err
	}
	_, clocks, err := r.governingClocks(chain, now)
	if err != nil {
		return time.Time{}, err
	}
	occ, _, err := resolveEngineDay(req.DayArg, now, clocks)
	if err != nil {
		return time.Time{}, err
	}
	return occ, nil
}

// lastStormEventAt returns the instant of the most recent recorded storm event.
// An unparseable timestamp reports no last event: the fold already cannot read
// it either, so refusing every subsequent command over it would strand the
// history rather than protect it.
func lastStormEventAt(h engine.StormHistory) (time.Time, bool) {
	n := len(h.History)
	if n == 0 {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339, h.History[n-1].At)
	if err != nil {
		return time.Time{}, false
	}
	return at, true
}

// stormDeclare records a fresh declaration, pending witness confirmation. An
// unknown label is rejected before any write (the validity check is up front,
// so a DeclareStorm error here is a genuine, unexpected failure).
func (r *Router) stormDeclare(h engine.StormHistory, label string, now time.Time, loc *time.Location) (StormResult, error) {
	if !engine.ValidStormLabel(h, label) {
		return StormResult{Rejected: true, Ack: stormUnknownLabelMsg}, nil
	}
	ev, err := engine.DeclareStorm(h, label, now)
	if err != nil {
		return StormResult{}, err
	}
	if err := r.persistStorm(ev, loc); err != nil {
		return StormResult{}, err
	}
	return StormResult{
		Event: engine.StormDeclared, Label: label,
		Ack: fmt.Sprintf("storm declared (%s) — pending witness confirmation (72h).", label),
	}, nil
}

// stormRenew records a renewal, rejected past the one allowed (the renewal
// count is checked up front).
func (r *Router) stormRenew(h engine.StormHistory, label string, now time.Time, loc *time.Location) (StormResult, error) {
	if engine.RenewalCount(h) >= engine.StormMaxRenewals {
		return StormResult{Rejected: true, Ack: stormRenewTwiceMsg}, nil
	}
	ev, err := engine.RenewStorm(h, now, h.DurationDays)
	if err != nil {
		return StormResult{}, err
	}
	if err := r.persistStorm(ev, loc); err != nil {
		return StormResult{}, err
	}
	return StormResult{
		Event: engine.StormRenewed, Label: label, Through: ev.Through,
		Ack: fmt.Sprintf("storm renewed — standing through %s.", ev.Through),
	}, nil
}

// stormEnd ends a standing storm early, rejected when none stands (the standing
// check is up front).
func (r *Router) stormEnd(h engine.StormHistory, now time.Time, loc *time.Location) (StormResult, error) {
	if standing, _ := engine.StormStanding(h, now, loc); !standing {
		return StormResult{Rejected: true, Ack: stormNoStandingMsg}, nil
	}
	ev, err := engine.EndStorm(h, now)
	if err != nil {
		return StormResult{}, err
	}
	if err := r.persistStorm(ev, loc); err != nil {
		return StormResult{}, err
	}
	return StormResult{Event: engine.StormEnded, Ack: "storm ended — breach math resets from here."}, nil
}

// persistStorm appends a storm event and rebuilds status.json — the shared
// tail of every accepted `/storm` command.
func (r *Router) persistStorm(ev engine.StormEvent, loc *time.Location) error {
	if err := r.store.AppendStormEvent(ev); err != nil {
		return err
	}
	_, err := r.store.RebuildEngineStatus(loc)
	return err
}

// BackfillStormEpisodesRequest is one `lucid storm backfill-episodes` intent:
// give every storm run recorded before the episodes index existed a written id.
// Now fixes the bare-window cutoff and the ultimate mint fallback; DryRun plans
// the records without writing them.
type BackfillStormEpisodesRequest struct {
	Now    time.Time
	DryRun bool
}

// BackfillStormEpisodesResult reports the episode records the backfill planned,
// whether they were written, and the inventory-only ack. Planned is in append
// order and is empty when every run already carries an id; Written is false on
// that no-op and on a dry run.
type BackfillStormEpisodesResult struct {
	Planned []engine.StormEpisode
	Written bool
	Ack     string
}

// BackfillStormEpisodes executes `lucid storm backfill-episodes`
// (engine-module.md §storm.json): give every existing storm run a written
// episode id by *appending* to the sibling episodes[] index, never by touching
// history[]. [engine.PlanStormEpisodes] derives the runs from the folded history
// and mints one id per un-indexed run; the write goes through
// [Adapter.WriteStormEpisodes], which stamps the version and leaves every
// history event exactly where it is — so the derived status.json is unchanged
// (StormStanding never reads episodes[]).
//
// It is idempotent: the planner matches runs already indexed, so a re-run of a
// fully-indexed history plans nothing and writes nothing, and this command is
// safe to run more than once. It is explicit and never automatic — a silent
// write to primary testimony is exactly what this store does not do — though a
// normal storm append also mints forward, so this command's job is only to
// complete the index for runs that predate it.
func (r *Router) BackfillStormEpisodes(req BackfillStormEpisodesRequest) (BackfillStormEpisodesResult, error) {
	now := whenOr(req.Now)

	// Scaffold before reading: a never-scaffolded tree has no storm.json.
	if err := r.prepareEngine(); err != nil {
		return BackfillStormEpisodesResult{}, err
	}
	h, err := r.store.ReadStormState()
	if err != nil {
		return BackfillStormEpisodesResult{}, err
	}

	planned := engine.PlanStormEpisodes(h, now)
	if len(planned) == 0 {
		return BackfillStormEpisodesResult{Ack: stormBackfillNothingAck()}, nil
	}
	if req.DryRun {
		return BackfillStormEpisodesResult{Planned: planned, Ack: stormBackfillDryRunAck(planned)}, nil
	}

	if err := r.store.WriteStormEpisodes(planned...); err != nil {
		return BackfillStormEpisodesResult{}, fmt.Errorf("could not back-fill storm episodes; nothing was saved: %w", err)
	}
	return BackfillStormEpisodesResult{Planned: planned, Written: true, Ack: stormBackfillWrittenAck(planned)}, nil
}

// stormBackfillNothingAck is the no-op ack: every run already carries an id.
func stormBackfillNothingAck() string {
	return "Every storm run already carries an episode id; nothing was appended."
}

// stormBackfillWrittenAck names the runs indexed and reasserts the append-only
// invariant — no history event was touched.
func stormBackfillWrittenAck(planned []engine.StormEpisode) string {
	return fmt.Sprintf(
		"Indexed %s: %s. Episode records were appended; no history event was touched.",
		stormEpisodeCountPhrase(len(planned)), strings.Join(episodeIDs(planned), ", "),
	)
}

// stormBackfillDryRunAck names the runs a real run would index and says plainly
// that nothing was written.
func stormBackfillDryRunAck(planned []engine.StormEpisode) string {
	return fmt.Sprintf(
		"Would index %s: %s. Dry run — nothing was written.",
		stormEpisodeCountPhrase(len(planned)), strings.Join(episodeIDs(planned), ", "),
	)
}

// stormEpisodeCountPhrase renders "1 storm episode" / "N storm episodes".
func stormEpisodeCountPhrase(n int) string {
	if n == 1 {
		return "1 storm episode"
	}
	return fmt.Sprintf("%d storm episodes", n)
}

// episodeIDs collects the minted ids in append order for the ack.
func episodeIDs(planned []engine.StormEpisode) []string {
	ids := make([]string, len(planned))
	for i, ep := range planned {
		ids[i] = ep.ID
	}
	return ids
}
