package cli

import "time"

// clockNow is the wall-clock source for the Stage-2 engine commands
// (`mode`, `status`), whose behavior is time-of-day sensitive — `/mode` is
// fixed at the bell, and the derived status anchors its rolling windows. It
// is a package var so tests can pin a deterministic instant; production always
// uses the real clock.
//
//nolint:gochecknoglobals // a single injected clock seam so the time-of-day-sensitive Stage-2 commands are deterministic under test
var clockNow = time.Now
