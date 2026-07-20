package schedstatus

import (
	"context"
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

// GatherParams carries everything the impure gather step needs: the booted config
// (the companion/provider block), the Ledger store (chain marks + receipts), the
// two resolved disposable job-DB paths, and the best-effort host probe. The CLI
// layer resolves the DB paths (flag → env → default, via schedrun/companion
// DefaultDBPath) and boots the router before calling Gather, so this package does
// no path resolution or router booting itself.
type GatherParams struct {
	Config      config.Config
	Store       *storage.Adapter
	SchedulerDB string
	CompanionDB string
	Probe       HostProbe
}

// Gather reads all local scheduler state read-only and returns the [Inputs] the
// pure [Assemble] consumes. It performs no writes and never panics on absent
// state: a missing job DB or a never-fired window's missing receipt is captured
// as a not-present input, not an error (their classification is [Assemble]'s
// job). Only a genuinely unreadable chain config — the one piece every scaffolded
// Ledger has — is returned as an error, so the command surfaces it as a runtime
// failure rather than a bogus health verdict.
func Gather(p GatherParams) (Inputs, error) {
	chain, err := GatherChain(p.Store)
	if err != nil {
		return Inputs{}, err
	}
	var host []Check
	if p.Probe != nil {
		host = p.Probe.Probe()
	}
	return Inputs{
		Companion:     GatherCompanion(p.Config),
		Chain:         chain,
		Teeth:         GatherDB(p.SchedulerDB),
		CompanionJobs: GatherDB(p.CompanionDB),
		Receipts:      GatherReceipts(p.Store),
		Host:          host,
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
func GatherDB(path string) DBInput {
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
		in.Err = "path is a directory, not a database file"
		return in
	}

	db, err := openReadOnly(path)
	if err != nil {
		in.Err = err.Error()
		return in
	}
	defer closeDB(db)

	ctx := context.Background()
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
