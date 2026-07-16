package schedstatus

import (
	"strconv"
	"strings"
	"time"
)

// Host check names — the stable machine identities of the best-effort
// host/supervisor signals. They keep the "host." prefix the text renderer trims
// for display, and are the JSON `name` values a machine reader keys on.
const (
	checkDaemon     = "host.daemon"
	checkSupervisor = "host.supervisor"
	checkBinary     = "host.binary"
	checkDBPath     = "host.db_path"
)

// launchdLabel is the reverse-DNS launchd job label of the supervised scheduler,
// mirrored from deploy/deploy.go. It names the job in the daemon check's messages
// and is the launchctl/plist target the macOS probe consults; it lives in the
// portable file so the pure classifier can reference it without a build tag.
const launchdLabel = "com.lucid.scheduler"

// HostProbe inspects host/supervisor health for the scheduler daemon. Every
// implementation is best-effort: a signal the current platform cannot inspect is
// reported [Unknown], never [Ok], so a blind probe can neither masquerade as
// healthy nor invent a failure. Probes read process/supervisor metadata only —
// they never open a secret, a token, or a prompt body. Callers inject a probe so
// the command's tests stay deterministic and cross-platform.
type HostProbe interface {
	Probe() []Check
}

// unknownProbe is the portable default: it reports every host check as [Unknown].
// It backs non-macOS builds (host_other.go), where launchd/Hush cannot be
// inspected, so the command is useful everywhere and never hard-requires a
// supervisor. Unknown host checks never lower the verdict.
type unknownProbe struct{}

// Probe reports every host signal as Unknown — the honest answer on a platform
// this build cannot inspect.
func (unknownProbe) Probe() []Check { return unknownHostChecks() }

// unknownHostChecks is the all-Unknown host check set: the honest "cannot inspect
// this platform" answer shared by the portable default and any macOS probe step
// that fails.
func unknownHostChecks() []Check {
	return []Check{
		unknownCheck(checkDaemon, "daemon state not inspectable on this platform"),
		unknownCheck(checkSupervisor, "supervisor state not inspectable on this platform"),
		unknownCheck(checkBinary, "supervised binary not inspectable on this platform"),
		unknownCheck(checkDBPath, "supervised DB path not inspectable on this platform"),
	}
}

// okCheck / warnCheck / errorCheck / unknownCheck are small constructors keeping
// the check-building call sites terse and the states consistent across the
// portable default and the platform probes.
func okCheck(name, detail string) Check    { return Check{Name: name, State: Ok, Detail: detail} }
func warnCheck(name, detail string) Check  { return Check{Name: name, State: Warn, Detail: detail} }
func errorCheck(name, detail string) Check { return Check{Name: name, State: Error, Detail: detail} }

func unknownCheck(name, detail string) Check {
	return Check{Name: name, State: Unknown, Detail: detail}
}

// daemonObservation is the raw, platform-gathered facts about the supervised
// daemon that [classifyDaemon] turns into a Check. Extracting the decision keeps
// the down/up/unknown logic portable and unit-testable; the macOS probe only
// gathers the facts.
type daemonObservation struct {
	pidFileFound bool // the Hush pid file exists and parsed to a pid
	pidFilePID   int
	pidAlive     bool // that pid names a live process
	procFound    bool // a process matching the daemon argv was found (pid-file fallback)
	procPID      int
	launchdKnown bool // launchctl could be consulted at all
	launchdOn    bool // the launchd job is loaded
}

// classifyDaemon turns gathered daemon facts into a Check. A live pid (from the
// pid file or a process match) is Ok; a pid file whose process is dead is a
// positively-detected daemon-down Error; a loaded launchd job with no running
// process is likewise Error; otherwise — nothing to inspect, or the job is not
// even installed — it is Unknown.
func classifyDaemon(o daemonObservation) Check {
	if o.pidFileFound {
		if o.pidAlive {
			return okCheck(checkDaemon, "supervised scheduler process running (pid "+strconv.Itoa(o.pidFilePID)+")")
		}
		return errorCheck(checkDaemon, "Hush pid file present but its process is not running — the scheduler daemon appears to be down")
	}
	if o.procFound {
		return okCheck(checkDaemon, "scheduler process running (pid "+strconv.Itoa(o.procPID)+")")
	}
	if o.launchdKnown && o.launchdOn {
		return errorCheck(checkDaemon, "launchd job "+launchdLabel+" is installed but no scheduler process is running — the daemon appears to be down")
	}
	return unknownCheck(checkDaemon, "no supervised scheduler process found and the launchd job is not installed on this host")
}

// classifyStaleBinary compares the on-disk build's mtime against the running
// daemon's start time: a binary newer than the process is a stale-daemon Warn
// (an upgrade/rebuild that did not restart the supervisor); otherwise Ok. It is
// pure so both outcomes are unit-testable without a live daemon.
func classifyStaleBinary(binMtime, procStart time.Time) Check {
	if binMtime.After(procStart) {
		return warnCheck(checkBinary, "the on-disk lucid build is newer than the running daemon (started "+procStart.Format(time.RFC3339)+") — restart the supervised scheduler to pick it up")
	}
	return okCheck(checkBinary, "the running daemon is at or newer than the on-disk build")
}

// classifyDBPath compares the DB path this command resolved against the daemon's
// launchd-configured path: a non-empty mismatch is a Warn (the inspector is
// reading a different DB than the daemon writes — an env/launchd path drift); a
// match is Ok. The "could not read the plist" case is Unknown and handled by the
// caller.
func classifyDBPath(resolved, daemon string) Check {
	if resolved != "" && daemon != "" && resolved != daemon {
		return warnCheck(checkDBPath, "this command is inspecting "+resolved+" but the daemon is configured for "+daemon+" — they differ, so the status may not reflect the live daemon")
	}
	return okCheck(checkDBPath, "the inspected DB path matches the daemon's launchd-configured path")
}

// parsePID parses a pid file body (a decimal integer, possibly surrounded by
// whitespace or a trailing newline). It is a pure helper so the macOS probe's
// pid-file read is unit-testable without a real process; a blank or non-numeric
// body yields ok=false.
func parsePID(body string) (int, bool) {
	pid, err := strconv.Atoi(strings.TrimSpace(body))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// lstartLayout is the format `ps -o lstart=` prints a process start time in on
// macOS (the C ctime(3) form, e.g. "Wed Jul 16 13:33:46 2026").
const lstartLayout = "Mon Jan _2 15:04:05 2006"

// parseLstart parses the `ps -o lstart=` start-time line into a local time. It is
// a pure helper so the stale-binary comparison is unit-testable without spawning
// a process; a line ps never produced (empty, wrong shape) yields ok=false. The
// time is interpreted in the host's local zone, matching how ps prints it.
func parseLstart(line string) (time.Time, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return time.Time{}, false
	}
	//nolint:gosmopolitan // ps -o lstart= prints the start time in the host's local zone, so it must be parsed in that same local zone
	t, err := time.ParseInLocation(lstartLayout, trimmed, time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// parsePlistEnv extracts one EnvironmentVariables value from a launchd plist's
// XML body by key. launchd plists encode the environment as a <dict> of
// <key>NAME</key><string>VALUE</string> pairs; this scans for the key and returns
// the immediately-following <string>. It is a deliberately small, dependency-free
// scan (not a full plist parse) so the macOS probe's DB-path mismatch check is
// unit-testable with a synthetic plist; a key that is absent, or present without
// a following <string>, yields ok=false.
func parsePlistEnv(plistXML, key string) (string, bool) {
	marker := "<key>" + key + "</key>"
	idx := strings.Index(plistXML, marker)
	if idx < 0 {
		return "", false
	}
	rest := plistXML[idx+len(marker):]
	openIdx := strings.Index(rest, "<string>")
	if openIdx < 0 {
		return "", false
	}
	rest = rest[openIdx+len("<string>"):]
	closeIdx := strings.Index(rest, "</string>")
	if closeIdx < 0 {
		return "", false
	}
	return strings.TrimSpace(rest[:closeIdx]), true
}
