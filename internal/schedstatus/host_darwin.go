//go:build darwin

package schedstatus

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Host-probe targets, mirrored from deploy/deploy.go's DefaultLaunchdParams /
// DefaultSupervisorParams. They are duplicated as local constants so this
// read-only probe stays decoupled from the deploy package's write-path machinery;
// a divergence surfaces as an [Unknown] check (the honest "can't find it" signal)
// rather than a false [Ok].
const (
	hushSocket  = "/tmp/hush/supervise-lucid-scheduler.sock"
	hushPidFile = "/tmp/hush/supervise-lucid-scheduler.pid"

	// supervisedProcMatch matches the daemon's argv (`lucid scheduler run`) for a
	// best-effort process lookup when the pid file is absent.
	supervisedProcMatch = "scheduler run"

	// envSchedulerDBKey is the launchd EnvironmentVariables key the daemon reads
	// its disposable job-DB path from; the mismatch check compares its value with
	// the path this command resolved.
	envSchedulerDBKey = "LUCID_SCHEDULER_DB"

	// probeTimeout bounds every read-only shell-out so a wedged launchctl/ps never
	// hangs the status command.
	probeTimeout = 2 * time.Second
)

// darwinProbe is the macOS best-effort host/supervisor probe. It inspects the
// launchd job, the Hush supervision socket/pid, the running daemon process, and
// the on-disk build — all read-only, parsing metadata only. Every step that
// cannot positively determine a state degrades to [Unknown]; only a positively
// detected problem (a dead supervised process, a stale supervised binary, a
// DB-path drift) lowers the verdict.
type darwinProbe struct {
	// resolvedSchedulerDB is the scheduler DB path the command resolved, compared
	// against the daemon's launchd-configured path for the drift check.
	resolvedSchedulerDB string
}

// NewHostProbe returns the macOS host probe, carrying the resolved scheduler DB
// path so the probe can flag a drift between what this command inspects and what
// the supervised daemon is configured to write.
func NewHostProbe(resolvedSchedulerDB string) HostProbe {
	return darwinProbe{resolvedSchedulerDB: resolvedSchedulerDB}
}

// Probe runs every host check and returns them in a stable order.
func (p darwinProbe) Probe() []Check {
	return []Check{
		p.daemonCheck(),
		p.supervisorCheck(),
		p.binaryCheck(),
		p.dbPathCheck(),
	}
}

// daemonCheck gathers the daemon facts (pid file, process match, launchd job
// state) and hands them to the pure [classifyDaemon] decision. A live pid is Ok;
// a pid file whose process is dead, or a loaded launchd job with no process, is a
// positively-detected daemon-down Error; otherwise Unknown.
func (p darwinProbe) daemonCheck() Check {
	var o daemonObservation
	if pid, ok := pidFromFile(hushPidFile); ok {
		o.pidFileFound = true
		o.pidFilePID = pid
		o.pidAlive = processAlive(pid)
	} else if pid, ok := pgrepScheduler(); ok {
		o.procFound = true
		o.procPID = pid
	} else if installed, known := launchdInstalled(); known {
		o.launchdKnown = true
		o.launchdOn = installed
	}
	return classifyDaemon(o)
}

// supervisorCheck reports whether Hush is supervising the daemon, detected by the
// presence of its status socket. A present socket is [Ok]; its absence is
// [Unknown] (this host may not use Hush supervision) rather than an assumed
// failure — the daemon-down signal is the daemon check's job, not this one's.
func (p darwinProbe) supervisorCheck() Check {
	if fileExists(hushSocket) {
		return okCheck(checkSupervisor, "Hush supervision socket present at "+hushSocket)
	}
	return unknownCheck(checkSupervisor, "Hush supervision socket not found at "+hushSocket+" — supervision could not be confirmed")
}

// binaryCheck reports whether the running supervised daemon predates the on-disk
// lucid build — a stale daemon a rebuild/upgrade did not restart. It gathers the
// supervised process's start time and the on-disk binary's mtime, then hands them
// to the pure [classifyStaleBinary] decision. When the process, its start time,
// or the binary cannot be resolved, it degrades to [Unknown] — never a false [Ok].
func (p darwinProbe) binaryCheck() Check {
	pid, ok := supervisedPID()
	if !ok {
		return unknownCheck(checkBinary, "no running supervised process to compare the on-disk build against")
	}
	started, ok := processStart(pid)
	if !ok {
		return unknownCheck(checkBinary, "could not read the supervised process start time")
	}
	binPath, ok := onDiskBinary()
	if !ok {
		return unknownCheck(checkBinary, "could not resolve the on-disk lucid binary")
	}
	info, err := os.Stat(binPath)
	if err != nil {
		return unknownCheck(checkBinary, "could not stat the on-disk lucid binary")
	}
	return classifyStaleBinary(info.ModTime(), started)
}

// dbPathCheck reads the LUCID_SCHEDULER_DB the launchd job injects into the daemon
// and hands it, with the path this command resolved, to the pure [classifyDBPath]
// decision — a mismatch is a path-drift [Warn]. When the plist cannot be read or
// does not pin the path, it degrades to [Unknown].
func (p darwinProbe) dbPathCheck() Check {
	daemonDB, ok := launchdEnv(envSchedulerDBKey)
	if !ok {
		return unknownCheck(checkDBPath, "could not read the launchd job's configured scheduler DB path")
	}
	return classifyDBPath(p.resolvedSchedulerDB, daemonDB)
}

// ── best-effort shell-outs and filesystem reads ──────────────────────────────

// pidFromFile reads and parses a pid file, returning ok=false when the file is
// absent, unreadable, or not a positive integer.
func pidFromFile(path string) (int, bool) {
	body, err := os.ReadFile(path) //nolint:gosec // a fixed supervisor pid-file path, read read-only for a pid integer
	if err != nil {
		return 0, false
	}
	return parsePID(string(body))
}

// processAlive reports whether pid names a live process, tolerating the
// permission-denied case (a process owned by another user is still alive).
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// pgrepScheduler looks up the supervised daemon by its argv when no pid file is
// present, returning the first matching pid. A missing pgrep or no match yields
// ok=false.
func pgrepScheduler() (int, bool) {
	out, ok := runCmd("pgrep", "-f", supervisedProcMatch)
	if !ok {
		return 0, false
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return 0, false
	}
	return parsePID(fields[0])
}

// supervisedPID resolves the supervised daemon's pid, preferring the Hush pid
// file and falling back to a process match.
func supervisedPID() (int, bool) {
	if pid, ok := pidFromFile(hushPidFile); ok && processAlive(pid) {
		return pid, true
	}
	return pgrepScheduler()
}

// launchdInstalled reports whether the launchd job is loaded, and whether
// launchctl could be consulted at all (ok=false when launchctl is absent or
// errors in a way that is not a clean "not loaded").
func launchdInstalled() (installed, ok bool) {
	target := "gui/" + strconv.Itoa(os.Getuid()) + "/" + launchdLabel
	if _, run := runCmd("launchctl", "print", target); run {
		return true, true
	}
	// launchctl ran but the target was not found — a clean "not installed".
	if _, run := runCmd("launchctl", "list"); run {
		return false, true
	}
	return false, false
}

// processStart reads a process's start time via `ps -o lstart=` and parses it.
func processStart(pid int) (time.Time, bool) {
	out, ok := runCmd("ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	if !ok {
		return time.Time{}, false
	}
	return parseLstart(out)
}

// onDiskBinary resolves the on-disk lucid binary to stat: the running executable
// (the freshly-installed build the operator is invoking), with symlinks resolved.
func onDiskBinary() (string, bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		return resolved, true
	}
	return exe, true
}

// launchdEnv reads one EnvironmentVariables value from the installed launchd
// plist. It reads the user LaunchAgents plist read-only and scans it for the key;
// an absent/unreadable plist or a key it does not pin yields ok=false.
func launchdEnv(key string) (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	plistPath := filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist")
	body, err := os.ReadFile(plistPath) //nolint:gosec // a fixed launchd plist path, read read-only for a non-secret env value
	if err != nil {
		return "", false
	}
	return parsePlistEnv(string(body), key)
}

// fileExists reports whether path exists (of any type). Used for the Hush socket
// presence check.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// runCmd runs a read-only command with a bounded timeout and returns its trimmed
// stdout and whether it exited zero. Any failure (missing binary, non-zero exit,
// timeout) yields ok=false — the caller then degrades the check to Unknown.
func runCmd(name string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	//nolint:gosec // read-only host inspection; every call site passes a fixed literal command (launchctl/pgrep/ps), never user input
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}
