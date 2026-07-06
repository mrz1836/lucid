package router

import "fmt"

// The fixed /bootstrap acks. The exit copy is the error-table copy
// (error-states.md §E-6); the enter copy names the paused surface plainly. No
// consolidation pass runs on exit — that is a deferred feature (scope.md §7).
const (
	bootstrapOnAck   = "Bootstrap mode on — historical entries; pattern proposals paused until `/bootstrap done`."
	bootstrapDoneAck = "Done. Pattern proposals will resume on the next `/checkin`."
)

// BootstrapRequest carries the one input for a /bootstrap turn: whether this is
// the `done` exit (Done true) or the enter toggle (Done false).
type BootstrapRequest struct {
	Done bool
}

// BootstrapResult reports the resulting mode and the ack to show the user.
type BootstrapResult struct {
	BootstrapMode bool
	Ack           string
}

// Bootstrap toggles historical-entry mode. `/bootstrap` turns it on
// (bootstrap_mode true): while it is on, captures stamp bootstrap:true and
// Reflection.propose is suppressed (scope.md §4; the Validate path already
// short-circuits on the flag). `/bootstrap done` turns it off and runs no
// consolidation pass (error-states.md §E-6). The persisted lucid.json is
// updated and the router's effective config follows it so the next command
// reads the new mode without a reboot.
func (r *Router) Bootstrap(req BootstrapRequest) (BootstrapResult, error) {
	cfg := r.cfg
	cfg.BootstrapMode = !req.Done
	if err := r.store.SaveConfig(cfg); err != nil {
		return BootstrapResult{}, fmt.Errorf("bootstrap: persist mode: %w", err)
	}
	r.cfg = cfg

	ack := bootstrapOnAck
	if req.Done {
		ack = bootstrapDoneAck
	}
	return BootstrapResult{BootstrapMode: cfg.BootstrapMode, Ack: ack}, nil
}
