package router

import (
	"fmt"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
)

// ProfileResult reports a `/profile` switch. Rejected marks an undefined
// profile name (no disk effect); Effective is the logical day the switch
// takes hold — always the next one.
type ProfileResult struct {
	From      string
	To        string
	Effective string
	Rejected  bool
	Ack       string
}

// Profile executes `/profile <name>` (engine-module.md §Commands): switch
// to a named clock profile defined in chain.json. The switch is sticky,
// recorded in profile.json's append-only history, and effective from the
// next logical day — never the current one, so a switch after tonight's
// bell cannot move tonight's clocks. An undefined name is rejected with no
// disk effect (engine-module.md §Error states).
func (r *Router) Profile(name string, now time.Time) (ProfileResult, error) {
	now = whenOr(now)
	if err := r.store.ScaffoldEngine(); err != nil {
		return ProfileResult{}, fmt.Errorf("could not prepare the engine tree: %w", err)
	}
	chain, err := r.store.ReadChainConfig()
	if err != nil {
		return ProfileResult{}, err
	}
	if !chain.HasProfile(name) {
		return ProfileResult{Rejected: true, Ack: profileRejectMsg}, nil
	}
	state, err := r.store.ReadProfileState()
	if err != nil {
		return ProfileResult{}, err
	}
	// HasProfile already gated the undefined-name rejection above, so any
	// error here is a real config fault (e.g. a malformed clock in
	// chain.json) — surface it rather than masking it as a rejection.
	sw, err := engine.ComputeSwitch(chain, state, name, now)
	if err != nil {
		return ProfileResult{}, err
	}
	if err := r.store.AppendProfileEvent(sw); err != nil {
		return ProfileResult{}, err
	}
	return ProfileResult{
		From: sw.From, To: sw.To, Effective: sw.Effective,
		Ack: fmt.Sprintf("Profile switches to %s — effective %s.", name, sw.Effective),
	}, nil
}
