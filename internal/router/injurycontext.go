package router

import (
	"github.com/mrz1836/lucid/internal/storage"
)

// injurycontext.go is the read-only injury-context projection seam
// (life-archive.md §6, the Q3=A clean seam). A workout / body-guidance consumer
// reads active/managed injuries through this router method instead of touching
// the raw injury registry: registries/ is a sanctuary tree, so consumers read
// projections, not state (agent-contracts.md). The projection shares the exact
// active/managed filter the clinician packet's activeInjuryLines renders from,
// so the packet and this seam can never diverge, and it renders registry facts
// only — never a diagnosis or treatment recommendation (observations.md §9). It
// is a pure read: nothing is written beyond the idempotent tree scaffold the
// read already performs.
//
// The seam takes no time argument. Unlike the cluster-selection contract
// (§6), whose input includes "the current time", the injury-context projection
// is a time-independent status filter — active/managed injuries are current by
// definition — so a "now" parameter would be dead. A consumer that later needs
// an as-of instant can add one additively without breaking this signature.

// InjuryContext returns the structured injury-context projection: one
// [storage.InjuryContext] per active/managed injury (resolved excluded), in
// byte-stable key order, read through the storage adapter (the only code that
// touches ~/.lucid/). It is the stable seam a workout planner consumes.
func (r *Router) InjuryContext() ([]storage.InjuryContext, error) {
	if err := r.prepareObservations(); err != nil {
		return nil, err
	}
	return r.store.InjuryContext()
}
