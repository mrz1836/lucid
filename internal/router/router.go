package router

import (
	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/storage"
)

// Router is the command/router spine. It holds the storage adapter (the
// only code that touches ~/.lucid/) and the effective config resolved
// at boot. Command dispatch and agent ordering are added by the phases
// that build each command; this foundation carries construction and the
// boot routine.
type Router struct {
	store *storage.Adapter
	cfg   config.Config
}

// New constructs a router over a storage adapter. Call [Router.Boot]
// before serving commands so the effective config is loaded and clipped.
func New(store *storage.Adapter) *Router {
	return &Router{store: store}
}

// Config returns the effective config resolved by the last [Router.Boot].
// Before Boot it is the zero config.
func (r *Router) Config() config.Config { return r.cfg }

// Store returns the storage adapter the router routes through.
func (r *Router) Store() *storage.Adapter { return r.store }

// Boot loads lucid.json, clips any out-of-range values back into their
// documented bounds, and — if clipping changed anything — rewrites the
// file so the persisted config matches what the router will use. It
// returns the human-readable clip warnings (empty when nothing was out
// of range) so the caller can surface them once at startup
// (acceptance-criteria.md test case 1.4).
func (r *Router) Boot() (warnings []string, err error) {
	loaded, err := r.store.LoadConfig()
	if err != nil {
		return nil, err
	}
	clipped, warnings := loaded.Clip()
	if len(warnings) > 0 {
		if err := r.store.SaveConfig(clipped); err != nil {
			return warnings, err
		}
	}
	r.cfg = clipped
	return warnings, nil
}
