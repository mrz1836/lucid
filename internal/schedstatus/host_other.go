//go:build !darwin

package schedstatus

// NewHostProbe returns the best-effort host probe for the current platform. On
// every non-macOS platform the scheduler is not launchd/Hush-supervised, so there
// is nothing this build can positively inspect: the probe reports all host checks
// [Unknown] — never [Ok] — which keeps the command useful off macOS and never
// hard-requires a supervisor. The resolved scheduler DB path is accepted for a
// uniform signature with the macOS probe but is unused here.
func NewHostProbe(_ string) HostProbe { return unknownProbe{} }
