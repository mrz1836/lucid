// Package schedstatus is the pure core of `lucid scheduler status` — the
// read-only health surface that answers one plain question: "is the autonomous
// Lucid scheduler healthy, what fires next, and what happened last?"
//
// It owns the report model, the health-verdict thresholds, the verdict rollup,
// and both renderers (human text + JSON). It is deliberately pure: it performs
// no filesystem, database, host, or model access, and imports only the standard
// library. The CLI layer does the impure gathering (open the disposable job DBs
// read-only, read the companion receipts, read lucid.json / chain.json, run the
// best-effort host probe) and hands the already-gathered [Inputs] to [Assemble],
// which returns a fully classified [Report]. Keeping every classification pure
// makes the whole verdict matrix unit-testable with synthetic inputs and keeps
// the package out of the pure Engine scheduler's import graph.
package schedstatus

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CheckState is the health of a single check. The four states mirror the
// documented verdict contract: a positively-good check is [Ok], a benign-but-
// noteworthy condition is [Warn], a real problem is [Error], and a check the
// current platform cannot inspect is [Unknown]. Only [Warn] and [Error] lower
// the overall verdict — [Unknown] never does, so a non-macOS host or an
// unreadable supervisor can never masquerade as healthy nor as broken.
type CheckState string

// The four check states. Their string values are the stable tokens that appear
// in the JSON `state` field and in the top-level `verdict`.
const (
	Ok      CheckState = "ok"
	Warn    CheckState = "warn"
	Error   CheckState = "error"
	Unknown CheckState = "unknown"
)

// The periodic slugs the scheduler registers, mirrored from internal/schedrun
// (teeth) and internal/companion. They are duplicated here as exported
// constants rather than imported so the pure report core stays dependency-free;
// a divergence would surface as a "not registered" check, which is the honest
// signal anyway.
const (
	SlugBell             = "lucid-bell"
	SlugTripwire         = "lucid-tripwire"
	SlugCompanionMorning = "lucid-companion-morning"
	SlugCompanionNight   = "lucid-companion-night"
)

// dateLayout is the logical-day format companion receipts are stamped with
// (YYYY-MM-DD), the same basis the miss check compares an elapsed window's
// expected date against.
const dateLayout = "2006-01-02"

// Check is one classified health signal: a stable machine name, its state, and
// a plain-language detail a human can act on. Every non-[Ok] check contributes
// its detail to the rendered issues block.
type Check struct {
	Name   string     `json:"name"`
	State  CheckState `json:"state"`
	Detail string     `json:"detail,omitempty"`
}

// PromptPath is one companion prompt file the compose worker reads: which role
// it fills, the configured path, and whether that path exists on disk. Only the
// path and existence are ever reported — the file body is never read (a missing
// prompt would fail compose, so its absence is what matters, not its contents).
type PromptPath struct {
	Role   string `json:"role"`
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

// CompanionInfo is the lucid.json companion/provider block as the status surface
// reports it: whether the daily companion is enabled, the provider backend and
// model its compose call uses, and each configured prompt path with an
// existence marker.
type CompanionInfo struct {
	Enabled         bool         `json:"enabled"`
	ProviderBackend string       `json:"provider_backend"`
	ProviderModel   string       `json:"provider_model"`
	Prompts         []PromptPath `json:"prompts"`
}

// ChainMarks are the two deterministic fire times inherited from chain.json:
// the evening bell (which drives the night companion) and the morning tripwire
// (which drives the morning companion). They are reported so the miss check and
// the reader both see when each window is scheduled to fire.
type ChainMarks struct {
	BellTime     string `json:"bell_time"`
	TripwireTime string `json:"tripwire_time"`
}

// PeriodicStatus is one scheduled job definition as read from a disposable job
// DB (or a synthetic placeholder for a required-but-absent slug). Present is
// false when the slug is expected but not registered — the case that flips a
// required periodic to a missing-periodic error.
type PeriodicStatus struct {
	Slug        string     `json:"slug"`
	Cron        string     `json:"cron,omitempty"`
	Active      bool       `json:"active"`
	Present     bool       `json:"present"`
	NextRun     *time.Time `json:"next_run,omitempty"`
	LastEnqueue *time.Time `json:"last_enqueue,omitempty"`
}

// ReceiptStatus is the last companion delivery receipt for one window. Present
// is false when no receipt file exists yet (a window that has never fired), in
// which case the other fields are zero. Date is the logical day the send
// belongs to, which the miss check compares against the most-recent elapsed
// window's expected date.
type ReceiptStatus struct {
	Window      string `json:"window"`
	Present     bool   `json:"present"`
	Date        string `json:"date,omitempty"`
	MessageID   string `json:"message_id,omitempty"`
	Verified    bool   `json:"verified"`
	DeliveredAt string `json:"delivered_at,omitempty"`
}

// RunFailure is one recently-discarded job paired with the error that ended it,
// summarized from a job DB's failure view. A cluster of these is how a retry
// storm or a run of Discord timeouts becomes visible.
type RunFailure struct {
	Kind        string `json:"kind"`
	ErrorClass  string `json:"error_class,omitempty"`
	Message     string `json:"message,omitempty"`
	FinalizedAt string `json:"finalized_at,omitempty"`
}

// RunSummary is the bounded recent-run rollup across both job DBs: the failures
// worth surfacing and their count. An empty summary is the healthy case.
type RunSummary struct {
	Failures     []RunFailure `json:"failures"`
	FailureCount int          `json:"failure_count"`
}

// DBInput is the gathered state of one disposable job DB, handed to [Assemble].
// Exactly one of the failure conditions may hold: Err is non-empty when the DB
// could not be opened or read (malformed/locked), Missing is true when the file
// does not exist, and otherwise the DB was read cleanly and Periodics/Failures
// carry its contents. The gatherer never panics on a missing DB — it sets
// Missing and lets [Assemble] classify it.
type DBInput struct {
	Path      string
	Missing   bool
	Err       string
	Periodics []PeriodicStatus
	Failures  []RunFailure
}

// Inputs is everything the impure CLI layer gathers, ready for pure
// classification. [Assemble] consumes it plus the current time and returns a
// classified [Report]; nothing in this struct requires I/O to interpret.
type Inputs struct {
	Companion     CompanionInfo
	Chain         ChainMarks
	Teeth         DBInput
	CompanionJobs DBInput
	Receipts      []ReceiptStatus
	Host          []Check
}

// DBReport is one job DB as classified for output: its resolved path, whether it
// read cleanly, and the periodics it holds (including synthetic Present:false
// placeholders for required-but-absent slugs).
type DBReport struct {
	Path      string           `json:"path"`
	State     CheckState       `json:"state"`
	Detail    string           `json:"detail,omitempty"`
	Periodics []PeriodicStatus `json:"periodics"`
}

// Report is the fully-classified status document. Verdict is the rolled-up
// health (`ok`/`warn`/`error`) and mirrors [Report.ExitCode]; Checks is the flat
// list of every classification that fed the verdict (host checks live in Host,
// not here); the remaining fields carry the display sections. The JSON tags are
// the stable machine contract.
type Report struct {
	Verdict       string          `json:"verdict"`
	Companion     CompanionInfo   `json:"companion"`
	Chain         ChainMarks      `json:"chain"`
	Teeth         DBReport        `json:"teeth"`
	CompanionJobs DBReport        `json:"companion_jobs"`
	Receipts      []ReceiptStatus `json:"receipts"`
	Runs          RunSummary      `json:"runs"`
	Host          []Check         `json:"host"`
	Checks        []Check         `json:"checks"`
}

// ExitCode maps the verdict to the 3-tier process exit code the command returns,
// identical in text and JSON output: 0 for ok, 1 for warn, 2 for error. A health
// cron or agent can gate on this alone without parsing the JSON.
func (r Report) ExitCode() int {
	switch CheckState(r.Verdict) {
	case Error:
		return 2
	case Warn:
		return 1
	case Ok, Unknown:
		return 0
	default:
		return 0
	}
}

// worst returns the most severe state across the checks: Error dominates, then
// Warn; Unknown and Ok never lower the result. It is the whole verdict-rollup
// rule in one place.
func worst(groups ...[]Check) CheckState {
	result := Ok
	for _, g := range groups {
		for _, c := range g {
			switch c.State {
			case Error:
				return Error
			case Warn:
				result = Warn
			case Ok, Unknown:
				// neither lowers the verdict
			}
		}
	}
	return result
}

// Assemble applies the health-verdict thresholds to the gathered inputs and
// returns a classified report. The rules, in one place:
//
//   - companion disabled                                   -> warn
//   - missing/malformed teeth job DB                       -> error (always)
//   - missing/malformed companion job DB (companion on)    -> error
//   - missing companion job DB (companion off)             -> not a fault (expected)
//   - enabled companion with a missing prompt file         -> error
//   - required periodic inactive/missing (companion on)    -> error
//   - teeth bell inactive while the companion owns the send-> not a fault (suppressed)
//   - latest receipt present but unverified                -> warn
//   - most-recent elapsed window with no/stale receipt     -> error
//   - host: daemon down / not supervised                   -> error (from probe)
//   - host: on-disk build newer than running daemon        -> warn  (from probe)
//   - host: launchd/Hush uninspectable                     -> unknown (from probe)
//
// Host checks arrive already classified from the best-effort probe (so the
// platform-specific logic stays out of this pure core); Assemble folds them into
// the verdict via [worst], where Unknown host checks are inert.
func Assemble(in Inputs, now time.Time) Report {
	r := Report{
		Companion: in.Companion,
		Chain:     in.Chain,
		Receipts:  in.Receipts,
		Host:      in.Host,
		Runs: RunSummary{
			Failures:     appendFailures(in.Teeth.Failures, in.CompanionJobs.Failures),
			FailureCount: len(in.Teeth.Failures) + len(in.CompanionJobs.Failures),
		},
	}

	var checks []Check
	checks = append(checks, classifyCompanion(in.Companion)...)

	// Teeth DB is always expected; the companion DB is only expected when the
	// companion is enabled (a disabled companion never creates its job store).
	var teethChecks, compChecks []Check
	r.Teeth, teethChecks = classifyDB(in.Teeth, "teeth", true)
	r.CompanionJobs, compChecks = classifyDB(in.CompanionJobs, "companion", in.Companion.Enabled)
	checks = append(checks, teethChecks...)
	checks = append(checks, compChecks...)

	// Periodic checks only make sense once the DB actually read.
	if r.Teeth.State == Ok {
		checks = append(checks, classifyTeethPeriodics(&r.Teeth, in.Companion.Enabled)...)
	}
	if in.Companion.Enabled && r.CompanionJobs.State == Ok {
		checks = append(checks, classifyCompanionPeriodics(&r.CompanionJobs)...)
	}

	// Receipts are a companion concept — only checked when it is enabled.
	if in.Companion.Enabled {
		checks = append(checks, classifyReceipts(in.Receipts, in.Chain, now)...)
	}

	r.Checks = checks
	r.Verdict = string(worst(checks, in.Host))
	return r
}

// classifyCompanion reports the enabled/disabled state and, when enabled, an
// error per missing prompt file (a compose call would fail without it).
func classifyCompanion(c CompanionInfo) []Check {
	if !c.Enabled {
		return []Check{{
			Name:   "companion.enabled",
			State:  Warn,
			Detail: "companion is disabled — no morning/night sends will be composed or delivered",
		}}
	}
	checks := []Check{{Name: "companion.enabled", State: Ok, Detail: "companion enabled"}}
	for _, p := range c.Prompts {
		if !p.Exists {
			checks = append(checks, Check{
				Name:   "companion.prompt." + p.Role,
				State:  Error,
				Detail: fmt.Sprintf("%s prompt file is missing: %s", p.Role, p.Path),
			})
		}
	}
	return checks
}

// classifyDB turns a gathered DBInput into its report and any DB-level check. A
// malformed DB is always an error; a missing DB is an error only when required
// (the teeth DB always, the companion DB when the companion is enabled). A
// missing-but-not-required DB is reported as Unknown with an explanatory detail
// and contributes no verdict-lowering check.
func classifyDB(in DBInput, name string, required bool) (DBReport, []Check) {
	rep := DBReport{Path: in.Path, Periodics: in.Periodics, State: Ok}
	switch {
	case in.Err != "":
		rep.State = Error
		rep.Detail = "unreadable: " + in.Err
		return rep, []Check{{
			Name:   name + ".db",
			State:  Error,
			Detail: fmt.Sprintf("%s job store at %s is unreadable: %s", name, in.Path, in.Err),
		}}
	case in.Missing && required:
		rep.State = Error
		rep.Detail = "missing — scheduler not initialized"
		return rep, []Check{{
			Name:   name + ".db",
			State:  Error,
			Detail: fmt.Sprintf("%s job store missing at %s — scheduler not initialized", name, in.Path),
		}}
	case in.Missing:
		rep.State = Unknown
		rep.Detail = "not created (companion disabled)"
		return rep, nil
	default:
		return rep, nil
	}
}

// classifyTeethPeriodics checks the teeth job DB. The morning tripwire (which
// also carries the witness heartbeat and the verdict read) is always required
// active. The evening bell is required active only when the companion is
// disabled; when the companion is enabled it deliberately owns the evening send
// and the bell is suppressed, so an inactive bell is the correct, healthy state
// (never an error). Missing-but-required or inactive-required slugs are errors.
func classifyTeethPeriodics(rep *DBReport, companionEnabled bool) []Check {
	checks := requireActive(rep, SlugTripwire, Error, "morning tripwire")
	if companionEnabled {
		// Bell is expected suppressed; ensure it is shown but never fault it.
		requireActive(rep, SlugBell, "", "evening bell (suppressed — companion owns the evening send)")
		return checks
	}
	return append(checks, requireActive(rep, SlugBell, Error, "evening bell")...)
}

// classifyCompanionPeriodics checks the companion job DB's morning and night
// periodics, both required active whenever the companion is enabled.
func classifyCompanionPeriodics(rep *DBReport) []Check {
	checks := requireActive(rep, SlugCompanionMorning, Error, "morning companion")
	return append(checks, requireActive(rep, SlugCompanionNight, Error, "night companion")...)
}

// requireActive locates slug among the report's periodics. A missing slug is
// recorded as a Present:false placeholder so it renders visibly; a present slug
// that is inactive is flagged. When missState is empty the slug is informational
// (e.g. a deliberately-suppressed bell): it is still shown, but never produces a
// verdict-lowering check.
func requireActive(rep *DBReport, slug string, missState CheckState, label string) []Check {
	p, found := findPeriodic(rep.Periodics, slug)
	switch {
	case !found:
		rep.Periodics = append(rep.Periodics, PeriodicStatus{Slug: slug, Present: false})
		if missState == "" {
			return nil
		}
		return []Check{{
			Name:   "periodic." + slug,
			State:  missState,
			Detail: fmt.Sprintf("%s (%s) is not registered", label, slug),
		}}
	case !p.Active:
		if missState == "" {
			return nil
		}
		return []Check{{
			Name:   "periodic." + slug,
			State:  missState,
			Detail: fmt.Sprintf("%s (%s) is inactive", label, slug),
		}}
	default:
		return nil
	}
}

// classifyReceipts flags every present-but-unverified receipt as a warning and
// the most-recent already-elapsed window as an error when it has no receipt or a
// stale one (a date that does not match that window's expected logical day —
// the same real miss the todo's stale-receipt criterion names).
func classifyReceipts(receipts []ReceiptStatus, chain ChainMarks, now time.Time) []Check {
	var checks []Check
	for _, rec := range receipts {
		if rec.Present && !rec.Verified {
			checks = append(checks, Check{
				Name:   "companion.receipt." + rec.Window + ".verified",
				State:  Warn,
				Detail: fmt.Sprintf("%s receipt (%s) is unverified — a read-back did not confirm delivery", rec.Window, rec.Date),
			})
		}
	}

	w, ok := mostRecentElapsedWindow(now, chain)
	if !ok {
		return checks
	}
	expected := w.fireAt.Format(dateLayout)
	rec, present := receiptFor(receipts, w.window)
	switch {
	case !present:
		checks = append(checks, Check{
			Name:   "companion.receipt." + w.window,
			State:  Error,
			Detail: fmt.Sprintf("most recent %s window (%s) has no delivery receipt — the send appears to have been missed", w.window, expected),
		})
	case rec.Date != expected:
		checks = append(checks, Check{
			Name:   "companion.receipt." + w.window,
			State:  Error,
			Detail: fmt.Sprintf("most recent %s window (%s) has only a stale receipt (last delivered %s) — the current send appears to have been missed", w.window, expected, rec.Date),
		})
	}
	return checks
}

// windowFire pairs a companion window with the most recent time it was scheduled
// to fire at or before now.
type windowFire struct {
	window string
	fireAt time.Time
}

// mostRecentElapsedWindow returns the companion window whose last scheduled fire
// is the most recent one at or before now. The morning window inherits the
// tripwire time and the night window the bell time; a window whose mark cannot
// be parsed is skipped. ok is false when neither mark is parseable.
func mostRecentElapsedWindow(now time.Time, chain ChainMarks) (windowFire, bool) {
	var best windowFire
	var have bool
	for _, cand := range []struct {
		window string
		hm     string
	}{
		{"morning", chain.TripwireTime},
		{"night", chain.BellTime},
	} {
		fireAt, ok := lastFire(now, cand.hm)
		if !ok {
			continue
		}
		if !have || fireAt.After(best.fireAt) {
			best = windowFire{window: cand.window, fireAt: fireAt}
			have = true
		}
	}
	return best, have
}

// lastFire returns the most recent occurrence of the HH:MM mark at or before
// now: today's occurrence if it has already elapsed, otherwise yesterday's. ok
// is false when the mark is not a valid HH:MM.
func lastFire(now time.Time, hm string) (time.Time, bool) {
	h, m, ok := parseHM(hm)
	if !ok {
		return time.Time{}, false
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
	if today.After(now) {
		return today.AddDate(0, 0, -1), true
	}
	return today, true
}

// parseHM parses a strict "HH:MM" 24-hour mark. It rejects anything out of range
// so an unparseable chain mark degrades to "skip the miss check" rather than a
// bogus window time.
func parseHM(hm string) (hour, minute int, ok bool) {
	parts := strings.SplitN(hm, ":", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	h, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || h < 0 || h > 23 {
		return 0, 0, false
	}
	m, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

// findPeriodic returns the periodic with the given slug and whether it was
// found. It only considers entries actually present in the DB.
func findPeriodic(ps []PeriodicStatus, slug string) (PeriodicStatus, bool) {
	for _, p := range ps {
		if p.Slug == slug && p.Present {
			return p, true
		}
	}
	return PeriodicStatus{}, false
}

// receiptFor returns the present receipt for a window, or false when none.
func receiptFor(rs []ReceiptStatus, window string) (ReceiptStatus, bool) {
	for _, r := range rs {
		if r.Window == window && r.Present {
			return r, true
		}
	}
	return ReceiptStatus{}, false
}

// appendFailures concatenates the two per-DB failure slices into one bounded
// list without mutating either input.
func appendFailures(a, b []RunFailure) []RunFailure {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make([]RunFailure, 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	return out
}
