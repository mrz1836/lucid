// Package deploy holds the supervised-ops artifacts for a standalone lucid
// install (build-plan.md Stage 6): the launchd job that runs the scheduler
// under `hush supervise`, the companion hush supervise config that injects
// the harness token at spawn time (ADR-0005), and the ADR-0002 backup set.
//
// The two templates live beside this file (launchd/lucid.plist.tmpl,
// hush/supervise.tmpl) and are embedded so rendering never depends on a
// working directory or an install layout. Render fills a template; Lint
// proves the rendered result is well-formed and carries the keys the init
// system needs — the "dry-run/lint" gate the build-plan's Stage 6 requires,
// without mutating a host. No secret value ever appears here: the token is
// named in the supervise `scope`, never carried (ADR-0005, success-metric
// S-7).
package deploy

import (
	"bytes"
	_ "embed"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/template"
)

//go:embed launchd/lucid.plist.tmpl
var launchdTemplate string

//go:embed hush/supervise.tmpl
var superviseTemplate string

// LaunchdParams fills launchd/lucid.plist.tmpl. The job runs
// `hush supervise --config <SuperviseConfig>`; it never names lucid directly,
// so the scheduler binary and its secrets are hush's concern (ADR-0005).
type LaunchdParams struct {
	// Label is the launchd job label (reverse-DNS, e.g. com.lucid.scheduler).
	Label string
	// HushProgram is the absolute path of the hush binary launchd execs.
	HushProgram string
	// SuperviseConfig is the absolute path of the hush supervise TOML.
	SuperviseConfig string
	// StdoutPath / StderrPath are the launchd log destinations.
	StdoutPath string
	StderrPath string
	// RunAtLoad / KeepAlive are the launchd lifecycle flags. The scheduler is
	// a dead-man backstop, so a supervised install keeps it alive.
	RunAtLoad bool
	KeepAlive bool
}

// SuperviseParams fills hush/supervise.tmpl. Scope names the secret(s) hush
// injects into the child (the harness token); ChildCommand is the lucid
// scheduler invocation, element [0] an absolute path (hush AC-6).
type SuperviseParams struct {
	Name               string
	Reason             string
	ServerURL          string
	ClientMachineIndex int
	RequestedTTL       string
	RefreshWindow      string
	StatusSocket       string
	PidFile            string
	Scope              []string
	ChildCommand       []string
	WorkingDir         string
	EnvPassthrough     []string
	DaemonLabel        string
}

// DefaultLaunchdParams returns a representative, render-ready launchd config
// for the scheduler job. Callers override the paths for their host; the
// defaults exist so a render+lint is exercisable without host specifics.
func DefaultLaunchdParams() LaunchdParams {
	return LaunchdParams{
		Label:           "com.lucid.scheduler",
		HushProgram:     "/usr/local/bin/hush",
		SuperviseConfig: "/usr/local/etc/lucid/supervise.toml",
		StdoutPath:      "/usr/local/var/log/lucid.scheduler.out.log",
		StderrPath:      "/usr/local/var/log/lucid.scheduler.err.log",
		RunAtLoad:       true,
		KeepAlive:       true,
	}
}

// DefaultSuperviseParams returns a representative, render-ready hush supervise
// config for the scheduler. The single scope entry is the harness token's
// env-var name — a name, never a value (ADR-0005). ServerURL and
// ClientMachineIndex are host-provisioning values the operator replaces.
func DefaultSuperviseParams() SuperviseParams {
	return SuperviseParams{
		Name:               "lucid-scheduler",
		Reason:             "Lucid scheduler (bell, tripwire, heartbeat, enrichment)",
		ServerURL:          "http://100.64.0.1:7743/h/example",
		ClientMachineIndex: 1,
		RequestedTTL:       "26h",
		RefreshWindow:      "09:00-10:00",
		StatusSocket:       "/tmp/hush/supervise-lucid-scheduler.sock",
		PidFile:            "/tmp/hush/supervise-lucid-scheduler.pid",
		Scope:              []string{"LUCID_HARNESS_TOKEN"},
		ChildCommand:       []string{"/usr/local/bin/lucid", "scheduler", "run"},
		WorkingDir:         "/usr/local/var/lucid",
		// Non-secret env the child inherits: the base process env plus the two
		// logical-channel IDs the notifier resolves "user"/"witness" against and
		// the optional job-store path override. These are env-var NAMES only —
		// the real channel IDs and the harness token (scope, above) live in the
		// vault, never in this repo (ADR-0005, S-7).
		EnvPassthrough: []string{
			"PATH", "HOME", "SHELL", "LUCID_HOME",
			"LUCID_USER_CHANNEL_ID", "LUCID_WITNESS_CHANNEL_ID", "LUCID_SCHEDULER_DB",
		},
		DaemonLabel: "Lucid Scheduler",
	}
}

// RenderLaunchd renders the launchd plist template with p and lints the
// result. A render or lint failure is returned; the caller never sees
// half-formed output.
func RenderLaunchd(p LaunchdParams) (string, error) {
	out, err := render("launchd", launchdTemplate, p)
	if err != nil {
		return "", err
	}
	if err := LintLaunchd(out); err != nil {
		return "", err
	}
	return out, nil
}

// RenderSupervise renders the hush supervise template with p and lints the
// result.
func RenderSupervise(p SuperviseParams) (string, error) {
	out, err := render("supervise", superviseTemplate, p)
	if err != nil {
		return "", err
	}
	if err := LintSupervise(out); err != nil {
		return "", err
	}
	return out, nil
}

// render executes a text/template against data. Missing map keys are an error
// (missingkey=error) so a typo never renders a silently empty field.
func render(name, tmpl string, data any) (string, error) {
	t, err := template.New(name).Option("missingkey=error").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("deploy: parse %s template: %w", name, err)
	}
	var b bytes.Buffer
	if err := t.Execute(&b, data); err != nil {
		return "", fmt.Errorf("deploy: render %s template: %w", name, err)
	}
	return b.String(), nil
}

// launchdRequiredKeys are the plist dict keys a supervised scheduler job must
// carry for launchd to load and keep it alive.
//
//nolint:gochecknoglobals // fixed, read-only launchd schema key set
var launchdRequiredKeys = []string{
	"Label", "ProgramArguments", "RunAtLoad", "KeepAlive",
	"StandardOutPath", "StandardErrorPath",
}

// ErrLint is the sentinel wrapped by every Lint failure so callers can branch
// on "the artifact did not pass its dry-run" distinctly from an IO error.
var ErrLint = errors.New("deploy: lint failed")

// LintLaunchd verifies rendered launchd plist XML is well-formed, carries no
// unresolved template action, and declares every required key. It is the
// pure-Go dry-run: no `plutil`, no host, so it runs identically in CI.
func LintLaunchd(rendered string) error {
	if err := noUnresolvedActions(rendered); err != nil {
		return err
	}
	if err := wellFormedXML(rendered); err != nil {
		return fmt.Errorf("%w: launchd plist is not well-formed XML: %w", ErrLint, err)
	}
	for _, k := range launchdRequiredKeys {
		if !strings.Contains(rendered, "<key>"+k+"</key>") {
			return fmt.Errorf("%w: launchd plist missing required key %q", ErrLint, k)
		}
	}
	// A supervised scheduler runs hush, not lucid, directly (ADR-0005).
	if !strings.Contains(rendered, "<string>supervise</string>") {
		return fmt.Errorf("%w: launchd plist does not invoke `hush supervise`", ErrLint)
	}
	return nil
}

// superviseRequiredTokens are the fragments a lint of the rendered hush
// supervise config asserts present: the supervisor session type, a non-empty
// secret scope, and a child command block.
//
//nolint:gochecknoglobals // fixed, read-only hush supervise schema token set
var superviseRequiredTokens = []string{
	`session_type              = "supervisor"`,
	"scope = [",
	"[child]",
	"command = [",
}

// LintSupervise verifies a rendered hush supervise config carries no
// unresolved template action and declares the required supervisor keys. It is
// a structural lint (there is no TOML dependency in the tree): it proves the
// substitution completed and the load-bearing sections are present.
func LintSupervise(rendered string) error {
	if err := noUnresolvedActions(rendered); err != nil {
		return err
	}
	for _, tok := range superviseRequiredTokens {
		if !strings.Contains(rendered, tok) {
			return fmt.Errorf("%w: supervise config missing %q", ErrLint, tok)
		}
	}
	// A supervisor config with an empty scope injects no secret — the harness
	// token must be named (ADR-0005), so the scope array is never empty.
	if strings.Contains(rendered, "scope = [\n]") || strings.Contains(rendered, "scope = []") {
		return fmt.Errorf("%w: supervise config has an empty secret scope", ErrLint)
	}
	return nil
}

// noUnresolvedActions rejects a rendered artifact that still carries a Go
// template action ("{{" / "}}") — the tell of a field that never substituted.
func noUnresolvedActions(rendered string) error {
	if strings.Contains(rendered, "{{") || strings.Contains(rendered, "}}") {
		return fmt.Errorf("%w: unresolved template action in rendered output", ErrLint)
	}
	return nil
}

// wellFormedXML streams every token of s through the XML decoder, returning
// the first syntax error. It is the plist well-formedness half of the launchd
// dry-run — it validates structure, not the plist DTD.
func wellFormedXML(s string) error {
	dec := xml.NewDecoder(strings.NewReader(s))
	for {
		_, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// BackupEntry is one path in the ADR-0002 backup set, relative to the Ledger
// home (~/.lucid/). IsDir distinguishes a whole subtree from a single file;
// Exclude names any sub-path pruned from a directory copy — the one such case
// is engine/ minus its derived status.json.
type BackupEntry struct {
	Path    string
	IsDir   bool
	Exclude []string
}

// BackupManifest is the canonical ADR-0002 backup set (also stated in
// local-runtime.md §Rebuildability): the trees that must survive forever
// because they are primary data existing nowhere else — raw entries, the
// observation event log, the registries, the engine tree minus its derived
// status, and the append-only export log. Everything else in ~/.lucid/ is
// rebuildable (see [RebuildableTrees]) and is deliberately omitted.
//
// It is the single source of truth the backup script must match:
// scripts/backup.sh encodes the same set and a test cross-checks the two.
func BackupManifest() []BackupEntry {
	return []BackupEntry{
		{Path: "raw", IsDir: true},
		{Path: "observations", IsDir: true},
		{Path: "registries", IsDir: true},
		{Path: "engine", IsDir: true, Exclude: []string{"engine/status.json"}},
		{Path: "projections/exports.log", IsDir: false},
	}
}

// RebuildableTrees are the home-relative paths a backup omits because they can
// be regenerated from the backup set (local-runtime.md §Rebuildability): the
// derived Mirror artifacts, the derived engine status, and every projection
// except the export log. The people/ and sessions/ indexes and lucid.json are
// likewise outside the backup set — reconstructable, not primary testimony.
func RebuildableTrees() []string {
	return []string{
		"processed", "insights", "reflections", "engine/status.json",
	}
}
