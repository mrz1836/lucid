package schedstatus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	flywheel "github.com/mrz1836/go-flywheel"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/flynode"
	"github.com/mrz1836/lucid/internal/scheduler"
	"github.com/mrz1836/lucid/internal/storage"
)

// Prompt roles and companion windows the gatherer reports, mirrored from the
// compose worker's config keys so the status surface names them the same way.
const (
	roleSystem  = "system"
	roleMorning = "morning"
	roleNight   = "night"

	windowMorning = "morning"
	windowNight   = "night"

	// recentFailuresLimit bounds the recent-run failure summary per job DB — enough
	// to spot a retry storm or a run of timeouts without turning the report into a
	// log dump.
	recentFailuresLimit = 10
)

// The job kinds whose successful runs can carry a compose-fallback marker,
// mirrored from internal/companion, internal/workout and internal/witnessreport.
// Like the Slug* constants they are duplicated rather than imported, so this
// package never depends on a daemon package (and, through one, on a model or a
// provider). A drift test in each daemon package pins its own kind to the
// constant here, so a renamed kind fails a test instead of silently hiding that
// kind's fallbacks from the report.
const (
	KindCompanionMorning         = "lucid_companion_morning"
	KindCompanionNight           = "lucid_companion_night"
	KindCompanionMorningBackstop = "lucid_companion_morning_backstop"
	KindWorkoutDaily             = "lucid_workout_daily"
	KindWitnessReportWeekly      = "lucid_witness_report_weekly"
)

// The daemon names a delivered fallback is logged under, so a status entry greps
// straight to its supervised log line ([FallbackDaemon]).
const (
	daemonCompanionMorning = "companion-morning"
	daemonCompanionNight   = "companion-night"
	daemonWorkout          = "workout"
	daemonWitness          = "witness"
)

// The job stores the compose-fallback scan reads, named as job_stores names
// them so an entry's store matches the path printed for it.
const (
	storeCompanion     = "companion"
	storeWorkout       = "workout"
	storeWitnessReport = "witness-report"
)

// fallbackScanLimit bounds how many succeeded jobs of one kind the scan reads
// from one store. A daily kind finalizes about seven jobs in the window, so this
// is ample headroom while keeping each read a short index scan; the report
// itself lists at most [composeFallbackLimit] across every store.
const fallbackScanLimit = 50

// errDirectoryNotDB is the read failure for a job-store path that names a
// directory: it is never opened as a database.
var errDirectoryNotDB = errors.New("path is a directory, not a database file")

// GatherParams carries everything the impure gather step needs: the booted config
// (the companion/provider block), the Ledger store (chain marks + receipts), the
// two resolved disposable job-DB paths this report fully inspects, the workout
// and witness-report job-store paths read only by the compose-fallback scan, the
// full set of job-store paths to name for legibility, the best-effort host
// probe, and the instant the fallback window ends at. The CLI layer resolves
// every path (flag → env → default, via each daemon's own DefaultDBPath) and
// boots the router before calling Gather, so this package does no path
// resolution or router booting itself.
type GatherParams struct {
	Config      config.Config
	Store       *storage.Adapter
	SchedulerDB string
	CompanionDB string
	// WorkoutDB and WitnessReportDB are opened read-only for the compose-fallback
	// scan only — never for periodics or failures. A blank path is skipped.
	WorkoutDB       string
	WitnessReportDB string
	JobStores       []JobStorePath
	Probe           HostProbe
	// Now anchors the compose-fallback window; the zero value means time.Now().
	Now time.Time
}

// Gather reads all local scheduler state read-only and returns the [Inputs] the
// pure [Assemble] consumes. It performs no writes and never panics on absent
// state: a missing job DB or a never-fired window's missing receipt is captured
// as a not-present input, not an error (their classification is [Assemble]'s
// job), and the compose-fallback scan never fails at all. Only a genuinely
// unreadable chain config — the one piece every scaffolded Ledger has — is
// returned as an error, so the command surfaces it as a runtime failure rather
// than a bogus health verdict.
func Gather(ctx context.Context, p GatherParams) (Inputs, error) {
	chain, err := GatherChain(p.Store)
	if err != nil {
		return Inputs{}, err
	}
	var host []Check
	if p.Probe != nil {
		host = p.Probe.Probe()
	}
	now := p.Now
	if now.IsZero() {
		now = time.Now()
	}
	return Inputs{
		Companion:        GatherCompanion(p.Config),
		Chain:            chain,
		Engine:           GatherDB(ctx, p.SchedulerDB),
		CompanionJobs:    GatherDB(ctx, p.CompanionDB),
		JobStores:        p.JobStores,
		ComposeFallbacks: GatherComposeFallbacks(ctx, now, fallbackStores(p)),
		Receipts:         GatherReceipts(p.Store),
		Host:             host,
	}, nil
}

// GatherCompanion projects the lucid.json companion/provider block into the
// reportable [CompanionInfo]. The effective compose model mirrors the worker's
// resolution: the companion-block override when set, else the provider model. It
// stats each configured prompt path for existence only — the file body is never
// read, so no prompt content can leak (AC-7).
func GatherCompanion(cfg config.Config) CompanionInfo {
	model := cfg.Provider.Model
	if cfg.Companion.Model != "" {
		model = cfg.Companion.Model
	}
	return CompanionInfo{
		Enabled:         cfg.Companion.Enabled,
		ProviderBackend: cfg.Provider.Backend,
		ProviderModel:   model,
		Prompts: []PromptPath{
			promptPath(roleSystem, cfg.Companion.SystemPrompt),
			promptPath(roleMorning, cfg.Companion.MorningTemplate),
			promptPath(roleNight, cfg.Companion.NightTemplate),
		},
	}
}

// promptPath builds one prompt entry, statting the configured path for existence.
func promptPath(role, path string) PromptPath {
	return PromptPath{Role: role, Path: path, Exists: promptExists(path)}
}

// promptExists reports whether a configured prompt file exists, statting the path
// exactly as the compose worker reads it (a direct open, no ~ expansion) so the
// existence marker matches whether compose would actually find the file. It never
// opens the file, so no prompt body is read; an empty configured path is reported
// missing rather than statted.
func promptExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// GatherChain reads the chain bell/tripwire marks the way the daemon resolves
// them: the default profile's bell_time and escalation.tripwire_time. A missing
// or unreadable chain.json is returned as an error (every scaffold writes one).
func GatherChain(store *storage.Adapter) (ChainMarks, error) {
	chain, err := store.ReadChainConfig()
	if err != nil {
		return ChainMarks{}, fmt.Errorf("schedstatus: read chain config: %w", err)
	}
	bell, tripwire, err := scheduler.Marks(chain, engine.DefaultProfile)
	if err != nil {
		return ChainMarks{}, fmt.Errorf("schedstatus: resolve chain marks: %w", err)
	}
	return ChainMarks{BellTime: bell, TripwireTime: tripwire}, nil
}

// GatherReceipts reads the last companion delivery receipt for each window. A
// window that has never fired has no receipt file — reported as Present:false
// ("no receipt yet"), never an error here; the elapsed-window miss classification
// is [Assemble]'s. A receipt whose body cannot be read degrades to not-present so
// the read-only report never crashes on a corrupt receipt.
func GatherReceipts(store *storage.Adapter) []ReceiptStatus {
	return []ReceiptStatus{
		gatherReceipt(store, windowMorning),
		gatherReceipt(store, windowNight),
	}
}

// gatherReceipt reads one window's receipt into a [ReceiptStatus].
func gatherReceipt(store *storage.Adapter, window string) ReceiptStatus {
	rec, ok, err := store.ReadCompanionReceipt(window)
	if err != nil || !ok {
		return ReceiptStatus{Window: window, Present: false}
	}
	return ReceiptStatus{
		Window:      window,
		Present:     true,
		Date:        rec.Date,
		MessageID:   rec.MessageID,
		Verified:    rec.Verified,
		DeliveredAt: rec.DeliveredAt,
	}
}

// GatherDB opens a disposable job DB read-only and reads its periodics and recent
// failures. It never creates a missing file: a path that does not exist yields
// Missing:true (the scheduler has not run), and an unopenable/unreadable file (not
// a database, or a locked/corrupt one) yields Err (malformed). Both degrade
// cleanly — the read-only report never panics on a never-run or corrupt DB (AC-6).
func GatherDB(ctx context.Context, path string) DBInput {
	in := DBInput{Path: path}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			in.Missing = true
			return in
		}
		in.Err = err.Error()
		return in
	}
	if info.IsDir() {
		in.Err = errDirectoryNotDB.Error()
		return in
	}

	db, err := openReadOnly(path)
	if err != nil {
		in.Err = err.Error()
		return in
	}
	defer closeDB(db)

	views, err := flywheel.ListPeriodics(ctx, db)
	if err != nil {
		in.Err = err.Error()
		return in
	}
	in.Periodics = periodicStatuses(views)

	// Periodics read cleanly; a failed failures read still leaves a usable report,
	// so it degrades to "no recent failures" rather than downgrading the whole DB.
	if failures, ferr := flywheel.RecentFailures(ctx, db, flywheel.RecentFailuresParams{Limit: recentFailuresLimit}); ferr == nil {
		in.Failures = runFailures(failures)
	}
	return in
}

// periodicStatuses projects the flywheel read views into the report's periodic
// rows, carrying the optional next-run / last-enqueue timestamps only when set.
func periodicStatuses(views []flywheel.PeriodicView) []PeriodicStatus {
	out := make([]PeriodicStatus, 0, len(views))
	for i := range views {
		v := views[i]
		ps := PeriodicStatus{Slug: v.Slug, Cron: v.Cron, Active: v.Active, Present: true}
		if !v.NextRunAt.IsZero() {
			ps.NextRun = v.NextRunAt
		}
		if v.LastEnqueuedAt != nil && !v.LastEnqueuedAt.IsZero() {
			ps.LastEnqueue = *v.LastEnqueuedAt
		}
		out = append(out, ps)
	}
	return out
}

// runFailures projects the flywheel failure views into the bounded run summary's
// rows. A nil result (no recent failures) is the healthy case.
func runFailures(views []flywheel.FailureView) []RunFailure {
	if len(views) == 0 {
		return nil
	}
	out := make([]RunFailure, 0, len(views))
	for i := range views {
		v := views[i]
		f := RunFailure{Kind: v.Kind, ErrorClass: v.ErrorClass, Message: v.ErrorMessage}
		if !v.FinalizedAt.IsZero() {
			f.FinalizedAt = v.FinalizedAt.Format(time.RFC3339)
		}
		out = append(out, f)
	}
	return out
}

// FallbackStore is one job store the compose-fallback scan reads: its name (as
// job_stores names it), its resolved path, and the fallback-capable job kinds it
// holds.
type FallbackStore struct {
	Name  string
	Path  string
	Kinds []string
}

// fallbackStores lists the job stores the compose-fallback scan reads and the
// kinds each one holds. The companion store is opened a second time for the scan
// — a read-only open is cheap — so [GatherDB] keeps its single purpose, and the
// workout and witness-report stores are read for nothing else.
func fallbackStores(p GatherParams) []FallbackStore {
	return []FallbackStore{
		{
			Name:  storeCompanion,
			Path:  p.CompanionDB,
			Kinds: []string{KindCompanionMorning, KindCompanionNight, KindCompanionMorningBackstop},
		},
		{Name: storeWorkout, Path: p.WorkoutDB, Kinds: []string{KindWorkoutDaily}},
		{Name: storeWitnessReport, Path: p.WitnessReportDB, Kinds: []string{KindWitnessReportWeekly}},
	}
}

// FallbackDaemon maps a fallback-capable job kind to the daemon name its
// fallback is logged under, so a status entry greps straight to its supervised
// log line. The morning backstop reports as companion-morning, exactly as the
// companion daemon logs it. An unknown kind is returned verbatim, so a kind this
// mirror has not learned yet still reads legibly rather than blank.
func FallbackDaemon(jobKind string) string {
	switch jobKind {
	case KindCompanionMorning, KindCompanionMorningBackstop:
		return daemonCompanionMorning
	case KindCompanionNight:
		return daemonCompanionNight
	case KindWorkoutDaily:
		return daemonWorkout
	case KindWitnessReportWeekly:
		return daemonWitness
	default:
		return jobKind
	}
}

// GatherComposeFallbacks scans each store for scheduled sends that were
// delivered as the deterministic fallback within the recent window ending at
// now. It reads only through the public job-store read API: the succeeded jobs of
// each fallback-capable kind, then each job's successful attempt, listing the job
// when that attempt's output carries the compose_fallback marker.
//
// It never fails and never writes. A store whose path is blank or does not exist
// (the feature was never enabled) contributes nothing, silently. A store that
// exists but cannot be read contributes exactly one note and none of its
// entries, so a partial read never passes for a complete one. A job whose runs
// cannot be read, or whose output is empty, truncated, or not a marker, is
// skipped on its own. Bounding and ordering are [Assemble]'s.
func GatherComposeFallbacks(ctx context.Context, now time.Time, stores []FallbackStore) FallbackInput {
	since := now.Add(-composeFallbackWindow).UTC()
	var in FallbackInput
	for _, s := range stores {
		entries, err := scanFallbackStore(ctx, since, s)
		if err != nil {
			in.Notes = append(in.Notes, unreadableStoreNote(s, err))
			continue
		}
		in.Entries = append(in.Entries, entries...)
	}
	return in
}

// scanFallbackStore reads one store's fallback entries finalized at or after
// since. A blank or missing path yields nothing and no error; a directory, an
// unopenable file, or a failed job listing yields the error that becomes the
// store's note.
func scanFallbackStore(ctx context.Context, since time.Time, s FallbackStore) ([]ComposeFallback, error) {
	if strings.TrimSpace(s.Path) == "" {
		return nil, nil
	}
	info, err := os.Stat(s.Path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errDirectoryNotDB
	}

	db, err := openReadOnly(s.Path)
	if err != nil {
		return nil, err
	}
	defer closeDB(db)

	var entries []ComposeFallback
	for _, kind := range s.Kinds {
		found, kerr := scanFallbackKind(ctx, db, s.Name, kind, since)
		if kerr != nil {
			return nil, kerr
		}
		entries = append(entries, found...)
	}
	return entries, nil
}

// scanFallbackKind lists one kind's succeeded jobs finalized at or after since
// and keeps those whose successful attempt carries the fallback marker. Only the
// job listing can fail the store; a per-job read failure skips that job.
func scanFallbackKind(ctx context.Context, db *gorm.DB, store, kind string, since time.Time) ([]ComposeFallback, error) {
	jobs, err := flywheel.ListFinished(ctx, db, flywheel.ListFinishedParams{
		States: []flywheel.JobState{flywheel.StateSucceeded},
		Kind:   kind,
		Since:  since,
		Limit:  fallbackScanLimit,
	})
	if err != nil {
		return nil, err
	}
	var entries []ComposeFallback
	for i := range jobs {
		if e, ok := fallbackEntry(ctx, db, store, jobs[i]); ok {
			entries = append(entries, e)
		}
	}
	return entries, nil
}

// fallbackEntry reads a succeeded job's successful attempt and, when its output
// carries the compose_fallback marker, returns the job as a report entry. The
// finalize time keys the entry's time, window, and order. A failed run read, a
// missing finalize time, or an output that is not a marker skips the job.
func fallbackEntry(ctx context.Context, db *gorm.DB, store string, job flywheel.JobView) (ComposeFallback, bool) {
	if job.FinalizedAt == nil {
		return ComposeFallback{}, false
	}
	runs, err := flywheel.ListRuns(ctx, db, job.ID, flywheel.ListRunsParams{Limit: max(job.Attempt, 1)})
	if err != nil {
		return ComposeFallback{}, false
	}
	run, ok := successfulRun(runs)
	if !ok {
		return ComposeFallback{}, false
	}
	reason, errText, ok := parseFallbackMarker(run)
	if !ok {
		return ComposeFallback{}, false
	}
	finalized := *job.FinalizedAt
	return ComposeFallback{
		Kind:       FallbackDaemon(job.Kind),
		JobKind:    job.Kind,
		Store:      store,
		FinishedAt: finalized.UTC().Format(time.RFC3339),
		Reason:     reason,
		Error:      errText,
		At:         finalized.Local(), //nolint:gosmopolitan // the text report prints run times in the host's local zone
	}, true
}

// successfulRun returns the attempt that finalized a succeeded job: the newest
// run (the runs arrive newest first) whose outcome is success and whose finalize
// was applied rather than superseded. Earlier failed attempts are the failure
// views' business, not this scan's.
func successfulRun(runs []flywheel.JobRunView) (flywheel.JobRunView, bool) {
	for i := range runs {
		if runs[i].Outcome == string(flywheel.OutcomeSuccess) && !runs[i].Superseded {
			return runs[i], true
		}
	}
	return flywheel.JobRunView{}, false
}

// parseFallbackMarker decodes a run's output as the compose-fallback marker and
// returns its reason and error detail. It reports ok=false — the run is not
// listed — for an output the job store truncated, an empty or blank output (a
// model-composed send returns none), anything that does not decode into the
// marker (not JSON, a wrong-typed field, a top-level array), and a marker whose
// compose_fallback is false or absent. An unknown reason is returned verbatim.
func parseFallbackMarker(run flywheel.JobRunView) (reason, errText string, ok bool) {
	if run.OutputTruncated {
		return "", "", false
	}
	raw := bytes.TrimSpace(run.Output)
	if len(raw) == 0 {
		return "", "", false
	}
	var marker flynode.FallbackMarker
	if err := json.Unmarshal(raw, &marker); err != nil || !marker.ComposeFallback {
		return "", "", false
	}
	return marker.Reason, marker.Error, true
}

// unreadableStoreNote is the one-line note an unreadable store contributes to
// the compose-fallback section, worded like the job-store read failure the
// verdict reports for the two inspected stores.
func unreadableStoreNote(s FallbackStore, err error) string {
	return fmt.Sprintf("%s job store at %s is unreadable — compose fallbacks not scanned (%s)", s.Name, s.Path, err)
}

// openReadOnly opens an existing sqlite job DB in read-only mode. It builds a
// file: URI so a path containing a space (macOS "Application Support", the default
// job-DB home) is encoded correctly, requests mode=ro so the inspector can never
// write to the daemon's DB, and sets a short busy timeout so a transient lock from
// the live daemon degrades to a brief retry rather than an instant failure.
func openReadOnly(path string) (*gorm.DB, error) {
	db, err := gorm.Open(sqlite.Open(readonlyDSN(path)), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		return nil, err
	}
	return db, nil
}

// readonlyDSN builds the read-only file: DSN for path, percent-encoding it so a
// space or other reserved character cannot corrupt the query string.
func readonlyDSN(path string) string {
	u := url.URL{Scheme: "file", Path: path}
	q := url.Values{}
	q.Set("mode", "ro")
	q.Set("_pragma", "busy_timeout(2000)")
	u.RawQuery = q.Encode()
	return u.String()
}

// closeDB closes the underlying sql.DB handle behind a gorm connection,
// best-effort — a read-only reporter has nothing to flush.
func closeDB(db *gorm.DB) {
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}
