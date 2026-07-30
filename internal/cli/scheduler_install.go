package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/deploy"
	"github.com/mrz1836/lucid/internal/schedinstall"
	"github.com/mrz1836/lucid/internal/schedrun"
	"github.com/mrz1836/lucid/internal/storage"
)

// Flag names for `lucid scheduler install` / `uninstall`, shared with the tests.
const (
	installFlagOut             = "out"
	installFlagApply           = "apply"
	installFlagForce           = "force"
	installFlagLucid           = "lucid"
	installFlagHush            = "hush"
	installFlagSchedulerDB     = "scheduler-db"
	installFlagSuperviseConfig = "supervise-config"
	installFlagHushServer      = "hush-server"
	installFlagMachineIndex    = "machine-index"

	uninstallFlagDryRun = "dry-run"
	uninstallFlagLabel  = "label"
)

// Channel-ID env var names — env-only per ADR-0005, never lucid.json fields.
const (
	envUserChannelID    = "LUCID_USER_CHANNEL_ID"
	envWitnessChannelID = "LUCID_WITNESS_CHANNEL_ID"
)

// renderLaunchd / renderSupervise are the deploy render+lint seams, package vars
// so a test can inject a lint failure. Production always renders through deploy,
// so the lint always runs (ADR-0005): an artifact that skipped its dry-run is
// never emitted.
//
//nolint:gochecknoglobals // test seams for the deploy render+lint step
var (
	renderLaunchd   = deploy.RenderLaunchd
	renderSupervise = deploy.RenderSupervise
)

// installOptions carries the resolved `scheduler install` flag values.
type installOptions struct {
	out             string
	apply           bool
	force           bool
	lucidPath       string
	hushPath        string
	schedulerDB     string
	superviseConfig string
	hushServer      string
	machineIndex    int
}

// installInputs is the fully-resolved set of paths and values the two render
// param-builders draw from.
type installInputs struct {
	userHome        string
	ledgerHome      string
	lucidBin        string
	hushBin         string
	hushPresent     bool
	schedulerDB     string
	superviseConfig string
	logOut          string
	logErr          string
	userChannel     string
	witnessChannel  string
	hushServer      string
	machineIndex    int
	force           bool
	notes           []string
}

// installPlan is the rendered, linted artifact pair plus the params a host
// install needs.
type installPlan struct {
	label       string
	plist       string
	supervise   string
	schedulerDB string
	hushPresent bool
	notes       []string
	applyParams schedinstall.ApplyParams
}

// installJSON is the default (print-mode) --json payload.
type installJSON struct {
	Plist       string   `json:"plist"`
	Supervise   string   `json:"supervise"`
	HushPresent bool     `json:"hush_present"`
	Notes       []string `json:"notes,omitempty"`
}

// installWriteJSON is the --json payload for the --out and --apply modes.
type installWriteJSON struct {
	PlistPath     string   `json:"plist_path"`
	SupervisePath string   `json:"supervise_path"`
	HushPresent   bool     `json:"hush_present"`
	Loaded        bool     `json:"loaded"`
	Notes         []string `json:"notes,omitempty"`
}

// uninstallJSON is the `scheduler uninstall` --json payload.
type uninstallJSON struct {
	Label        string `json:"label"`
	PlistPath    string `json:"plist_path"`
	BootedOut    bool   `json:"booted_out"`
	PlistRemoved bool   `json:"plist_removed"`
	DryRun       bool   `json:"dry_run"`
}

// newSchedulerInstallCmd wires `lucid scheduler install` — render + lint the
// launchd plist and hush supervise config (default: print them, zero mutation),
// write them with --out, or perform the macOS host install with --apply. The
// binary stays credential-dumb: the artifacts name the harness token in the
// supervise scope and carry no value (ADR-0005).
func newSchedulerInstallCmd() *cobra.Command {
	opts := &installOptions{}
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Render (and, with --apply, load) the supervised launchd + hush artifacts",
		Long: `install lays down the supervised-daemon artifacts that keep the
scheduler alive across reboots: a launchd job that runs ` + "`hush supervise`" + `,
and the supervise config it reads. The launchd job never names lucid — hush
injects the harness token at spawn and execs the scheduler (ADR-0005). Every
render is linted before it is shown or written, so an artifact that failed its
dry-run is never emitted. Default prints both artifacts (no mutation); --out
writes them to a directory; --apply performs the host install on macOS.`,
		Args: cobra.NoArgs,
		Example: `  # Inspect the artifacts (no mutation).
  lucid scheduler install

  # Write them to a directory for review.
  lucid scheduler install --out ./deploy-out

  # Install and load the launchd job (macOS).
  lucid scheduler install --apply`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return emitErr(cmd, runSchedulerInstall(cmd, opts))
		},
	}
	bindInstallFlags(cmd, opts)
	return cmd
}

// bindInstallFlags registers the install flags. Split out to keep the command
// constructor within the funlen budget.
func bindInstallFlags(cmd *cobra.Command, opts *installOptions) {
	f := cmd.Flags()
	f.StringVar(&opts.out, installFlagOut, "", "Write the two artifacts to this directory instead of printing them")
	f.BoolVar(&opts.apply, installFlagApply, false, "Perform the host install (macOS): write to ~/Library/LaunchAgents + the supervise path and launchctl bootstrap")
	f.BoolVar(&opts.force, installFlagForce, false, "With --apply, replace an already-loaded job instead of refusing")
	f.StringVar(&opts.lucidPath, installFlagLucid, "", "Absolute path of the lucid binary the daemon runs (default: this executable)")
	f.StringVar(&opts.hushPath, installFlagHush, "", "Path of the hush binary launchd execs (default: hush on PATH)")
	f.StringVar(&opts.schedulerDB, installFlagSchedulerDB, "", "Job store the plist pins as LUCID_SCHEDULER_DB (default: the daemon's resolved path)")
	f.StringVar(&opts.superviseConfig, installFlagSuperviseConfig, "", "Where the supervise config is written/referenced (default: ~/.hush/supervisors/lucid-scheduler.toml)")
	f.StringVar(&opts.hushServer, installFlagHushServer, "", "hush server_url for the supervise config (default: the example placeholder)")
	f.IntVar(&opts.machineIndex, installFlagMachineIndex, 0, "hush client_machine_index for the supervise config (default: the example placeholder)")
	cmd.MarkFlagsMutuallyExclusive(installFlagOut, installFlagApply)
}

// runSchedulerInstall builds the linted plan, then dispatches to the print,
// write, or apply mode.
func runSchedulerInstall(cmd *cobra.Command, opts *installOptions) error {
	plan, err := buildInstallPlan(opts)
	if err != nil {
		return err
	}
	switch {
	case opts.apply:
		return applyInstall(cmd, plan)
	case opts.out != "":
		return writeInstall(cmd, plan, opts.out)
	default:
		return printInstall(cmd, plan)
	}
}

// buildInstallPlan resolves every input and renders + lints both artifacts. A
// lint failure surfaces via [deploy.ErrLint] and mutates nothing.
func buildInstallPlan(opts *installOptions) (installPlan, error) {
	in, err := resolveInstallInputs(opts)
	if err != nil {
		return installPlan{}, err
	}
	lp := launchdParamsFor(in)
	plist, err := renderLaunchd(lp)
	if err != nil {
		return installPlan{}, installLintError(err)
	}
	supervise, err := renderSupervise(superviseParamsFor(in))
	if err != nil {
		return installPlan{}, installLintError(err)
	}
	return installPlan{
		label:       lp.Label,
		plist:       plist,
		supervise:   supervise,
		schedulerDB: in.schedulerDB,
		hushPresent: in.hushPresent,
		notes:       in.notes,
		applyParams: schedinstall.ApplyParams{
			Label:               lp.Label,
			PlistBody:           plist,
			SuperviseBody:       supervise,
			SuperviseConfigPath: in.superviseConfig,
			Force:               in.force,
		},
	}, nil
}

// installLintError names a render failure as a failed dry-run when it is a lint
// failure, so the operator knows nothing was written.
func installLintError(err error) error {
	if errors.Is(err, deploy.ErrLint) {
		return fmt.Errorf("lucid scheduler install: %w (the rendered artifact failed its dry-run; nothing was written)", err)
	}
	return fmt.Errorf("lucid scheduler install: render: %w", err)
}

// resolveInstallInputs resolves the home, ledger, job store, binaries, supervise
// path, log paths, channel IDs, and any operator notes.
func resolveInstallInputs(opts *installOptions) (installInputs, error) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return installInputs{}, fmt.Errorf("lucid scheduler install: resolve home: %w", err)
	}
	ledgerHome, err := storage.DefaultHome()
	if err != nil {
		return installInputs{}, fmt.Errorf("lucid scheduler install: resolve ledger home: %w", err)
	}
	schedulerDB, err := schedrun.DefaultDBPath(opts.schedulerDB)
	if err != nil {
		return installInputs{}, fmt.Errorf("lucid scheduler install: resolve scheduler db: %w", err)
	}
	lucidBin, err := resolveLucidBinary(opts.lucidPath)
	if err != nil {
		return installInputs{}, err
	}
	hushBin, hushPresent := resolveHushBinary(opts.hushPath)
	userCh, witnessCh, notes := resolveChannelsAndNotes(hushPresent)
	return installInputs{
		userHome: userHome, ledgerHome: ledgerHome, lucidBin: lucidBin,
		hushBin: hushBin, hushPresent: hushPresent, schedulerDB: schedulerDB,
		superviseConfig: resolveSuperviseConfigPath(opts.superviseConfig, userHome),
		logOut:          filepath.Join(userHome, "Library", "Logs", "lucid.scheduler.out.log"),
		logErr:          filepath.Join(userHome, "Library", "Logs", "lucid.scheduler.err.log"),
		userChannel:     userCh, witnessChannel: witnessCh,
		hushServer: opts.hushServer, machineIndex: opts.machineIndex,
		force: opts.force, notes: notes,
	}, nil
}

// resolveLucidBinary returns the absolute lucid path the child command runs: an
// explicit --lucid (must be absolute), else this executable with symlinks
// resolved.
func resolveLucidBinary(override string) (string, error) {
	if override != "" {
		if !filepath.IsAbs(override) {
			return "", fmt.Errorf("lucid scheduler install: --lucid must be an absolute path, got %q", override)
		}
		return override, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("lucid scheduler install: resolve this binary: %w", err)
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		return resolved, nil
	}
	return exe, nil
}

// resolveHushBinary returns the hush path launchd execs and whether hush was
// actually found: an explicit --hush, else hush on PATH, else the deploy
// placeholder (with present=false so the caller does not claim success).
func resolveHushBinary(override string) (bin string, present bool) {
	if override != "" {
		return override, true
	}
	if p, err := exec.LookPath("hush"); err == nil {
		return p, true
	}
	return deploy.DefaultLaunchdParams().HushProgram, false
}

// resolveSuperviseConfigPath returns the supervise-config path: an explicit
// override, else the template's prescribed ~/.hush/supervisors/... location.
func resolveSuperviseConfigPath(override, userHome string) string {
	if override != "" {
		return override
	}
	return filepath.Join(userHome, ".hush", "supervisors", "lucid-scheduler.toml")
}

// resolveChannelsAndNotes reads the two logical channel IDs from the environment
// (env-only, ADR-0005), falling back to the deploy placeholder names, and builds
// the provisioning notes for any unset channel or absent hush.
func resolveChannelsAndNotes(hushPresent bool) (userCh, witnessCh string, notes []string) {
	def := deploy.DefaultLaunchdParams().EnvironmentVariables
	userCh = os.Getenv(envUserChannelID)
	witnessCh = os.Getenv(envWitnessChannelID)
	if userCh == "" || witnessCh == "" {
		notes = append(notes, "channel IDs are env-only (ADR-0005): set "+envUserChannelID+" / "+envWitnessChannelID+" in the environment; the artifacts carry placeholder names until then.")
	}
	if userCh == "" {
		userCh = def[envUserChannelID]
	}
	if witnessCh == "" {
		witnessCh = def[envWitnessChannelID]
	}
	if !hushPresent {
		notes = append(notes, "hush not found on PATH: provision hush (or pass --hush) before the supervised send works — it injects the harness token.")
	}
	return userCh, witnessCh, notes
}

// launchdParamsFor layers the resolved inputs onto the deploy launchd defaults.
// Split from superviseParamsFor to fit the funlen budget.
func launchdParamsFor(in installInputs) deploy.LaunchdParams {
	p := deploy.DefaultLaunchdParams()
	p.HushProgram = in.hushBin
	p.SuperviseConfig = in.superviseConfig
	p.StdoutPath = in.logOut
	p.StderrPath = in.logErr
	p.EnvironmentVariables["HOME"] = in.userHome
	p.EnvironmentVariables["LUCID_HOME"] = in.ledgerHome
	p.EnvironmentVariables["LUCID_SCHEDULER_DB"] = in.schedulerDB
	p.EnvironmentVariables[envUserChannelID] = in.userChannel
	p.EnvironmentVariables[envWitnessChannelID] = in.witnessChannel
	return p
}

// superviseParamsFor layers the resolved inputs onto the deploy supervise
// defaults (the child command's absolute lucid path, the working dir, and any
// hush provisioning values).
func superviseParamsFor(in installInputs) deploy.SuperviseParams {
	p := deploy.DefaultSuperviseParams()
	p.ChildCommand = []string{in.lucidBin, "scheduler", "run"}
	p.WorkingDir = in.userHome
	if in.hushServer != "" {
		p.ServerURL = in.hushServer
	}
	if in.machineIndex > 0 {
		p.ClientMachineIndex = in.machineIndex
	}
	return p
}

// printInstall prints both artifacts (or the --json payload). Zero mutation.
func printInstall(cmd *cobra.Command, plan installPlan) error {
	if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
		return writeJSON(cmd.OutOrStdout(), installJSON{
			Plist: plan.plist, Supervise: plan.supervise,
			HushPresent: plan.hushPresent, Notes: plan.notes,
		})
	}
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintln(out, "# launchd plist — write to ~/Library/LaunchAgents/"+plan.label+".plist")
	_, _ = fmt.Fprintln(out, plan.plist)
	_, _ = fmt.Fprintln(out, "# hush supervise config — write to "+plan.applyParams.SuperviseConfigPath)
	_, _ = fmt.Fprintln(out, plan.supervise)
	printNotes(out, plan.notes)
	return nil
}

// writeInstall writes both artifacts to a directory (portable; no host load).
func writeInstall(cmd *cobra.Command, plan installPlan, outDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil { //nolint:gosec // an operator-named output dir for review, not under ~/.lucid
		return fmt.Errorf("lucid scheduler install: create --out dir: %w", err)
	}
	plistFile := filepath.Join(outDir, plan.label+".plist")
	if err := os.WriteFile(plistFile, []byte(plan.plist), 0o644); err != nil { //nolint:gosec // the plist is world-readable by design (launchd reads it); it carries no secret
		return fmt.Errorf("lucid scheduler install: write plist: %w", err)
	}
	supFile := filepath.Join(outDir, "lucid-scheduler.toml")
	if err := os.WriteFile(supFile, []byte(plan.supervise), 0o600); err != nil {
		return fmt.Errorf("lucid scheduler install: write supervise config: %w", err)
	}
	lines := append([]string{
		"Wrote launchd plist:      " + plistFile,
		"Wrote hush supervise cfg: " + supFile,
	}, notesLines(plan.notes)...)
	return emit(cmd, installWriteJSON{
		PlistPath: plistFile, SupervisePath: supFile,
		HushPresent: plan.hushPresent, Loaded: false, Notes: plan.notes,
	}, lines)
}

// applyInstall performs the host install. When hush is absent it stages the
// artifacts without loading (so a re-run needs no --force); on a non-macOS host
// it prints the manual guidance.
func applyInstall(cmd *cobra.Command, plan installPlan) error {
	if !plan.hushPresent {
		return stageInstall(cmd, plan)
	}
	res, err := schedinstall.Apply(cmd.Context(), plan.applyParams)
	if errors.Is(err, schedinstall.ErrUnsupported) {
		return unsupportedInstall(cmd, plan)
	}
	if err != nil {
		return fmt.Errorf("lucid scheduler install: %w", err)
	}
	return reportApply(cmd, plan, res)
}

// stageInstall writes the artifacts without loading a job (hush absent) and
// returns a non-success error so the command never claims a working install.
func stageInstall(cmd *cobra.Command, plan installPlan) error {
	res, err := schedinstall.WriteArtifacts(plan.applyParams)
	if errors.Is(err, schedinstall.ErrUnsupported) {
		return unsupportedInstall(cmd, plan)
	}
	if err != nil {
		return fmt.Errorf("lucid scheduler install: %w", err)
	}
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintln(out, "Staged the supervised-scheduler artifacts (not loaded — hush is absent):")
	_, _ = fmt.Fprintln(out, "  launchd plist:  "+res.PlistPath)
	_, _ = fmt.Fprintln(out, "  supervise cfg:  "+res.SuperviseConfigPath)
	printNotes(out, plan.notes)
	_, _ = fmt.Fprintln(out, "Provision hush, then re-run `lucid scheduler install --apply` to load the job.")
	return fmt.Errorf("lucid scheduler install: hush not found on PATH — artifacts staged but not loaded")
}

// reportApply reports a successful host install and reuses the scheduler-status
// host probe to confirm the job is loaded.
func reportApply(cmd *cobra.Command, plan installPlan, res schedinstall.ApplyResult) error {
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintln(out, "Installed the supervised scheduler:")
	_, _ = fmt.Fprintln(out, "  launchd plist:  "+res.PlistPath)
	_, _ = fmt.Fprintln(out, "  supervise cfg:  "+res.SuperviseConfigPath)
	if res.Replaced {
		_, _ = fmt.Fprintln(out, "  (replaced an already-loaded job)")
	}
	_, _ = fmt.Fprintln(out, "  launchd job:    loaded")
	printProbe(out, plan.schedulerDB)
	printNotes(out, plan.notes)
	_, _ = fmt.Fprintln(out, "Confirm with: lucid scheduler status")
	return nil
}

// unsupportedInstall prints the non-macOS manual guidance and returns
// ErrUnsupported (non-zero exit — the command did not install to this host).
func unsupportedInstall(cmd *cobra.Command, plan installPlan) error {
	out := cmd.ErrOrStderr()
	for _, line := range schedinstall.ManualGuidance(plan.applyParams) {
		_, _ = fmt.Fprintln(out, line)
	}
	printNotes(out, plan.notes)
	return fmt.Errorf("lucid scheduler install: %w", schedinstall.ErrUnsupported)
}

// printProbe renders the scheduler-status host checks after an install, so the
// operator sees the same signals `lucid scheduler status` reports.
func printProbe(out io.Writer, schedulerDB string) {
	_, _ = fmt.Fprintln(out, "Host checks (also via `lucid scheduler status`):")
	for _, c := range newHostProbe(schedulerDB).Probe() {
		_, _ = fmt.Fprintf(out, "  [%s] %s\n", c.State, c.Detail)
	}
}

// notesLines renders operator notes as a titled block, or nothing when empty.
func notesLines(notes []string) []string {
	if len(notes) == 0 {
		return nil
	}
	lines := []string{"Notes:"}
	for _, n := range notes {
		lines = append(lines, "  - "+n)
	}
	return lines
}

// printNotes writes the notes block to out.
func printNotes(out io.Writer, notes []string) {
	for _, line := range notesLines(notes) {
		_, _ = fmt.Fprintln(out, line)
	}
}

// newSchedulerUninstallCmd wires `lucid scheduler uninstall` — bootout the
// launchd job and remove its plist (idempotent; --dry-run previews).
func newSchedulerUninstallCmd() *cobra.Command {
	var dryRun bool
	var label string
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Bootout and remove the supervised launchd job (idempotent)",
		Long: `uninstall tears the supervised-scheduler launchd job down: launchctl
bootout, then remove the plist. It is idempotent — an already-absent job or plist
is a clean no-op — and never touches ~/.lucid/ or the disposable job store. On a
non-macOS host it prints manual guidance.`,
		Args: cobra.NoArgs,
		Example: `  # Preview what would be removed.
  lucid scheduler uninstall --dry-run

  # Bootout + remove the plist (macOS).
  lucid scheduler uninstall`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return emitErr(cmd, runSchedulerUninstall(cmd, label, dryRun))
		},
	}
	cmd.Flags().BoolVar(&dryRun, uninstallFlagDryRun, false, "Report what would be removed; touch nothing")
	cmd.Flags().StringVar(&label, uninstallFlagLabel, "", "launchd job label to remove (default: com.lucid.scheduler)")
	return cmd
}

// runSchedulerUninstall delegates to the schedinstall host seam and reports what
// was removed.
func runSchedulerUninstall(cmd *cobra.Command, label string, dryRun bool) error {
	if label == "" {
		label = deploy.DefaultLaunchdParams().Label
	}
	res, err := schedinstall.Uninstall(cmd.Context(), schedinstall.UninstallParams{Label: label, DryRun: dryRun})
	if errors.Is(err, schedinstall.ErrUnsupported) {
		return unsupportedUninstall(cmd, label)
	}
	if err != nil {
		return fmt.Errorf("lucid scheduler uninstall: %w", err)
	}
	return emit(cmd, uninstallJSON{
		Label: label, PlistPath: res.PlistPath, BootedOut: res.BootedOut,
		PlistRemoved: res.PlistRemoved, DryRun: dryRun,
	}, uninstallLines(res, dryRun))
}

// unsupportedUninstall prints the non-macOS manual guidance and returns
// ErrUnsupported.
func unsupportedUninstall(cmd *cobra.Command, label string) error {
	out := cmd.ErrOrStderr()
	_, _ = fmt.Fprintln(out, "Automated uninstall is macOS-only (launchctl). To remove the supervised scheduler elsewhere:")
	_, _ = fmt.Fprintln(out, "  1. Stop supervision (however you started `hush supervise`).")
	_, _ = fmt.Fprintln(out, "  2. Remove the launchd/init unit for "+label+".")
	return fmt.Errorf("lucid scheduler uninstall: %w", schedinstall.ErrUnsupported)
}

// uninstallLines renders the human summary for an uninstall (or its dry-run plan).
func uninstallLines(res schedinstall.UninstallResult, dryRun bool) []string {
	if dryRun {
		return []string{"Dry run — would bootout the job and remove:", "  " + res.PlistPath}
	}
	if !res.PlistRemoved {
		return []string{"Nothing to remove — the launchd job and plist were already absent (" + res.PlistPath + ")."}
	}
	return []string{"Removed the supervised scheduler launchd job (" + res.PlistPath + ")."}
}
